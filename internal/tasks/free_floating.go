// Package tasks — Free-floating event recording helpers (PROPOSAL-2 §7.5,
// §11 Scenario 6, §8 S9).
//
// "Free-floating" events are audit_events that are NOT anchored to an active
// EpochWorkflow — git commits, /pasture:* skill invocations outside an epoch, and
// Claude Code session boundaries. They live in the same audit_events table as
// epoch-anchored events; what differentiates them is the context_edges row(s)
// recorded alongside via protocol.TaskTracker.AttachContext: free-floating
// events get ContextGit / ContextSkill / ContextSession edges instead of (or
// in addition to) ContextEpoch.
//
// PROPOSAL-2 §7.3 free-floating example (verbatim):
//
//	A git commit hook fires → records AuditEvent{event_type: "GitCommit", ...}
//	with context_edges(event_id, GitContext, "<sha>").
//
// This file provides the three Record* helpers that hook handlers and skill
// entry points call to land that pair of writes atomically (from the caller's
// perspective). Each helper:
//
//  1. Calls tracker.RecordEventReturningId (writes the audit_events row and
//     returns its id atomically — no separate SELECT MAX round-trip needed).
//  2. Calls tracker.AttachContext (writes the context_edges row).
//  3. Returns the event id so the caller can attach further contexts if needed
//     (Scenario 7 multi-context attachment: e.g., a post-epoch git commit
//     citing epoch X gets BOTH a ContextGit edge and a ContextEpoch edge).
//
// ─── Why we still accept an explicit *sql.DB handle ─────────────────────────
//
// The three public helpers (RecordGitEvent / RecordSkillEvent /
// RecordSessionEvent) still carry an auditDB *sql.DB parameter for API
// compatibility with existing callers (hooks/git_recorder.go,
// cmd/pastured/main.go, and tests). The parameter is no longer used by the
// implementation — RecordEventReturningId (added in S8) bundles the write +
// id-recovery in a single call. A future clean-up pass may drop the parameter
// once all call sites are updated.
//
// The hook handler in internal/hooks/git_recorder.go caches the (*sql.DB,
// TaskTracker) pair at construction time so callers downstream see only a
// trivial "fire one hook event" surface (per §8 S9 "stub hook handler that
// demonstrates the wiring").
//
// ─── EventType conventions for free-floating events ──────────────────────────
//
// PROPOSAL-2 §7.3 uses bare strings like "GitCommit" for the event_type. The
// existing protocol.EventType enum (PhaseTransition, SliceStarted, ...) does
// NOT include git/skill/session event types — those are intentionally
// open-string per the proposal so plugins can emit their own event_type values
// without enum churn. The audit storage layer (SqliteAuditTrail.RecordEvent)
// stores event_type as TEXT without IsValid() enforcement, so unknown
// EventType strings persist correctly and round-trip through QueryEvents /
// Timeline.
//
// We expose well-known constants here so callers don't need to spell the
// strings themselves; this is purely ergonomic — the storage path doesn't care.

package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Well-known free-floating event type strings ─────────────────────────────

// Free-floating event types are open-string by design (§7.3 / §7.5). These
// constants are the canonical spellings used by the in-tree hook handlers and
// skill entry points; external plugins MAY define their own event_type values.
const (
	// EventGitCommit fires after a git commit completes (e.g., from a
	// Claude Code Stop hook firing after `git agent-commit`). Payload should
	// contain at least {"sha": "<commit-sha>"}.
	EventGitCommit protocol.EventType = "GitCommit"

	// EventGitPush fires after a git push completes. Payload should contain
	// at least {"refs": ["<branch>"], "remote": "<remote>"}.
	EventGitPush protocol.EventType = "GitPush"

	// EventGitRebase fires after a git rebase completes. Payload should
	// contain at least {"onto": "<base-ref>", "head": "<head-ref>"}.
	EventGitRebase protocol.EventType = "GitRebase"

	// EventSkillInvoked fires when a /pasture:* skill is invoked (per Pasture
	// URD R9). Payload should contain at least {"skill": "pasture:<name>"}.
	EventSkillInvoked protocol.EventType = "SkillInvoked"

	// EventSessionRecorded fires when a Claude Code session is recorded.
	// Payload should contain at least {"sessionId": "<id>"}.
	EventSessionRecorded protocol.EventType = "SessionRecorded"
)

// ─── Free-floating event recording helpers ───────────────────────────────────

// ─── Default role attribution for free-floating events ──────────────────────
//
// PROPOSAL-2 §7.7.2 enumerates well-known automaton names. The in-tree
// recorders in this file use the corresponding hook-handler names so that
// once S3's audit-side legacy-role compatibility shim resolves Role to an
// agent_id (and post-S8 when the workflow boundary supplies agent_id
// directly), free-floating events flow into the right pasture_agent_categories
// row. The role is overridable per call so plugins can attribute their own
// hook handlers without re-entering this file.
const (
	// DefaultGitRole is the canonical Role string written into
	// audit_events.role for git events recorded via RecordGitEvent. Matches
	// PROPOSAL-2 §7.7.2's pattern "pasture/automaton/hook/<hook-name>".
	DefaultGitRole = "pasture/automaton/hook/git-recorder"

	// DefaultSkillRole is the canonical Role string written into
	// audit_events.role for skill-invocation events recorded via
	// RecordSkillEvent.
	DefaultSkillRole = "pasture/automaton/hook/skill-recorder"

	// DefaultSessionRole is the canonical Role string written into
	// audit_events.role for Claude Code session events recorded via
	// RecordSessionEvent.
	DefaultSessionRole = "pasture/automaton/hook/session-recorder"
)

// RecordGitEvent records a free-floating git event (commit, push, rebase) to
// audit_events and attaches a ContextGit edge keyed by the supplied SHA / ref.
//
// Parameters:
//   - ctx: context for cancellation; propagates to RecordEventReturningId and
//     AttachContext.
//   - tracker: the unified TaskTracker (typically obtained from
//     OpenTaskTracker). MUST be non-nil.
//   - auditDB: retained for API compatibility with existing callers; no longer
//     used by the implementation (RecordEventReturningId bundles write + id
//     recovery atomically, removing the need for a separate SELECT MAX round-trip).
//     MAY be nil without causing a validation error.
//   - sha: the git commit SHA (or remote ref for pushes / base-ref for
//     rebases). Used as the context_id of the ContextGit edge. MUST be
//     non-empty; an empty sha would create a row no Timeline lookup can match.
//   - eventType: usually one of EventGitCommit / EventGitPush / EventGitRebase
//     above; callers MAY pass any non-empty string per §7.3 / §7.5
//     open-string convention.
//   - payload: free-form key/value pairs. Should include {"sha": sha} for
//     symmetry with the context edge; not enforced.
//
// The recorded AuditEvent's Role field is set to DefaultGitRole — the canonical
// hook-handler name from PROPOSAL-2 §7.7.2's "pasture/automaton/hook/<name>"
// pattern. S3's audit-side legacy-role shim resolves this to an agent_id;
// post-S8 the workflow boundary will supply agent_id directly.
//
// On success returns the int64 audit_events.id of the newly-inserted row so
// callers can attach further contexts (e.g., a post-epoch commit citing epoch
// X also gets ContextEpoch — see §11 Scenario 7 multi-context attachment).
//
// Errors are *pasterrors.StructuredError with one of:
//   - CategoryValidation: nil tracker, empty sha, or an empty eventType (the
//     underlying audit store would silently accept these but they're programming
//     errors here).
//   - CategoryStorage: RecordEventReturningId / AttachContext write failure
//     (file unwritable, schema drift, etc.). Underlying error wrapped via
//     pasterrors.StructuredError.Why.
func RecordGitEvent(
	ctx context.Context,
	tracker protocol.TaskTracker,
	auditDB *sql.DB,
	sha string,
	eventType protocol.EventType,
	payload map[string]any,
) (int64, error) {
	return recordFreeFloating(ctx, tracker, auditDB, protocol.ContextGit, sha, eventType, payload, DefaultGitRole, "RecordGitEvent")
}

// RecordSkillEvent records a free-floating skill-invocation event to
// audit_events and attaches a ContextSkill edge keyed by the supplied skill
// run id (e.g., "pasture:user-elicit-<run-id>").
//
// See RecordGitEvent for the parameter / error contract; only the context kind
// (ContextSkill) and the suggested EventType (EventSkillInvoked) differ.
//
// `skillRunId` is the canonical id of the skill invocation. Per PROPOSAL-2
// §7.3 example: "pasture:user-elicit-<run-id>" — the wire string is whatever the
// caller wants to use for Timeline lookups via
// `pasture task events --context-kind=SkillContext --context-id=<run-id>`.
func RecordSkillEvent(
	ctx context.Context,
	tracker protocol.TaskTracker,
	auditDB *sql.DB,
	skillRunId string,
	eventType protocol.EventType,
	payload map[string]any,
) (int64, error) {
	return recordFreeFloating(ctx, tracker, auditDB, protocol.ContextSkill, skillRunId, eventType, payload, DefaultSkillRole, "RecordSkillEvent")
}

// RecordSessionEvent records a free-floating Claude Code session event to
// audit_events and attaches a ContextSession edge keyed by the supplied
// session id.
//
// See RecordGitEvent for the parameter / error contract; only the context kind
// (ContextSession) and the suggested EventType (EventSessionRecorded) differ.
//
// Note: pasture also has a separate session_entries table (R6 — sub-PROV-O
// granularity for ACP SessionUpdate streams) reached via
// tracker.RecordSessionEntries; that path is for storing the streamed session
// content. RecordSessionEvent here records the higher-level "a session
// happened" event so it shows up in the audit timeline alongside epoch
// transitions and git events.
func RecordSessionEvent(
	ctx context.Context,
	tracker protocol.TaskTracker,
	auditDB *sql.DB,
	sessionId string,
	eventType protocol.EventType,
	payload map[string]any,
) (int64, error) {
	return recordFreeFloating(ctx, tracker, auditDB, protocol.ContextSession, sessionId, eventType, payload, DefaultSessionRole, "RecordSessionEvent")
}

// ─── Internal: shared recording flow ─────────────────────────────────────────

// recordFreeFloating is the shared implementation of RecordGitEvent /
// RecordSkillEvent / RecordSessionEvent. It is package-private because:
//   - Each public helper documents a specific (kind, ContextId-shape) contract
//     that callers depend on; collapsing them to a single Record(kind, id)
//     surface would lose that documentation.
//   - The "fnName" parameter is purely cosmetic (it appears in error.What for
//     attribution); we do not want callers passing arbitrary names.
//
// The flow:
//
//  1. Validate inputs (early-return *StructuredError on any invalid input).
//  2. Build the AuditEvent with EpochId="" (no epoch anchor — Scenario 6
//     "no `epoch_id` column or fail because no epoch is active"). The
//     audit_events.epoch_id NOT NULL constraint accepts empty string at the
//     SQL level; v4 migration (S4) drops the column entirely so this becomes
//     moot post-S4. The Phase field is also left empty for the same reason —
//     PROPOSAL-2 §7.2 documents the column as "nullable for free-floating
//     events" in the v3+ schema.
//  3. Call tracker.RecordEventReturningId — persists the event and returns
//     its audit_events.id atomically (no separate SELECT MAX round-trip).
//  4. Call tracker.AttachContext — writes the context_edges row.
//
// The auditDB parameter is retained for API compatibility with existing callers
// (hooks, daemon wiring) but is not used by this implementation now that
// RecordEventReturningId bundles the write + id-recovery in a single call.
//
// Returns the event id on success so the caller can issue follow-up
// AttachContext calls for multi-context attachment (Scenario 7).
func recordFreeFloating(
	ctx context.Context,
	tracker protocol.TaskTracker,
	_ *sql.DB, // auditDB: retained for call-site compatibility; no longer used (RecordEventReturningId bundles write + id)
	kind protocol.ContextKind,
	contextId string,
	eventType protocol.EventType,
	payload map[string]any,
	defaultRole string,
	fnName string,
) (int64, error) {
	if tracker == nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture tried to record a %s event without an open task store.", contextKindLabel(kind)),
			Why: "The recorder was called without a task store handle. This is a bug in\n" +
				"the code that called it — a working task store is required.",
			Where: fmt.Sprintf("Recording a %s event (internal/tasks/free_floating.go in tasks.%s).", contextKindLabel(kind), fnName),
			Impact: fmt.Sprintf(
				"The %s event isn't recorded, and nothing was written to the database.",
				contextKindLabel(kind),
			),
			Fix: "1. Open a task store first, then pass it to the recorder.\n" +
				"2. If you hit this from the CLI rather than from your own code, please\n" +
				"   file a bug — it shouldn't be reachable in normal use.",
		}
	}
	if contextId == "" {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture tried to record a %s event with no identifier to attach it to.", contextKindLabel(kind)),
			Why: fmt.Sprintf(
				"The recorder was called with an empty %s. We need a real identifier so\n"+
					"the event can be looked up later.",
				contextIDLabel(kind),
			),
			Where: fmt.Sprintf("Recording a %s event (internal/tasks/free_floating.go in tasks.%s).", contextKindLabel(kind), fnName),
			Impact: fmt.Sprintf(
				"The %s event isn't recorded — without an identifier, nothing could find it again.",
				contextKindLabel(kind),
			),
			Fix: fmt.Sprintf("1. Pass a real identifier when recording the event:\n"+
				"     %s\n"+
				"   For example, for a git commit use the full commit SHA; for a skill\n"+
				"   invocation use the skill run id; for a Claude Code session use the\n"+
				"   session id.",
				contextIDExample(kind)),
		}
	}
	if eventType == "" {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture tried to record a %s event with no event type.", contextKindLabel(kind)),
			Why: "The recorder was called with an empty event-type string. Event types are\n" +
				"how we filter and look up events later, so they can't be blank.",
			Where: fmt.Sprintf("Recording a %s event (internal/tasks/free_floating.go in tasks.%s).", contextKindLabel(kind), fnName),
			Impact: "The event isn't recorded — there'd be no way to ask for events of this\n" +
				"kind in queries or timelines.",
			Fix: "1. Pass one of the built-in event type constants for the event you're\n" +
				"   recording (for git: GitCommit, GitPush, GitRebase; for skills:\n" +
				"   SkillInvoked; for sessions: SessionRecorded).\n" +
				"2. Or pass your own short string (for example \"MyPluginAction\") if you\n" +
				"   are recording a custom event type.",
		}
	}

	// Build the AuditEvent. EpochId and Phase are intentionally left empty —
	// see Scenario 6 "no epoch_id column or fail because no epoch is active"
	// and §7.2 "phase TEXT, -- nullable for free-floating events". The
	// underlying SQLite NOT NULL on epoch_id is satisfied by the empty string
	// at the SQL layer (NULL != ""); S4 will drop the column entirely.
	//
	// Role is set to the helper-specific defaultRole (e.g. DefaultGitRole =
	// "pasture/automaton/hook/git-recorder"). S3's audit-side legacy-role
	// shim resolves Role → agent_id via find-or-create on agents_software,
	// minting a SoftwareAgent named after the role. Post-S8 the workflow
	// boundary will carry agent_id directly; for free-floating events the
	// shim's role-based attribution is the right semantics.
	event := protocol.AuditEvent{
		EpochId:   "",
		Phase:     "",
		Role:      defaultRole,
		EventType: eventType,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}

	// RecordEventReturningId atomically persists the event and returns its
	// audit_events.id. The trail-side implementation recovers the id from
	// sql.Result.LastInsertId on the SAME INSERT statement, so the returned
	// id is race-safe under any concurrency level (Phase 11 R1-B replaced the
	// previous SELECT MAX(id) workaround that could return another goroutine's
	// row id under concurrent writes — see audit/sqlite.go's
	// RecordEventReturningId for the full guarantee).
	eventId, err := tracker.RecordEventReturningId(ctx, event)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't save the %s event to the database.", contextKindLabel(kind)),
			Why: fmt.Sprintf(
				"Tried to write a %q event for %s %q to the audit log, but the write failed.",
				eventType, contextIDLabel(kind), contextId,
			),
			Where: fmt.Sprintf("Recording a %s event (internal/tasks/free_floating.go in tasks.%s).", contextKindLabel(kind), fnName),
			Impact: fmt.Sprintf(
				"The %s event isn't recorded, and the link between this event and its\n"+
					"%s won't be created either.",
				contextKindLabel(kind), contextIDLabel(kind),
			),
			Fix: "1. Make sure the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the operation that produced this event.",
			Cause: err,
		}
	}

	if err := tracker.AttachContext(ctx, eventId, kind, contextId); err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture saved the %s event but couldn't link it to its %s.", contextKindLabel(kind), contextIDLabel(kind)),
			Why: fmt.Sprintf(
				"The event (id %d, type %q) was written to the audit log, but linking it\n"+
					"to %s %q failed.",
				eventId, eventType, contextIDLabel(kind), contextId,
			),
			Where: fmt.Sprintf("Recording a %s event (internal/tasks/free_floating.go in tasks.%s).", contextKindLabel(kind), fnName),
			Impact: fmt.Sprintf(
				"The event is in the database but won't show up when you ask for events\n"+
					"by %s, which leaves a gap in the recorded history.",
				contextIDLabel(kind),
			),
			Fix: fmt.Sprintf("1. Repair the link by re-attaching the event to its %s:\n"+
				"     pasture task contexts attach %d %s %q\n"+
				"2. If the repair keeps failing, run a migration to confirm the schema is\n"+
				"   up to date:\n"+
				"     pasture migrate",
				contextIDLabel(kind), eventId, kind, contextId),
			Cause: err,
		}
	}

	return eventId, nil
}

// ─── Plain-language labels for context kinds ─────────────────────────────────
//
// These helpers translate the protocol-internal ContextKind constants into
// short ordinary-English phrases that read well inside an error message. They
// are intentionally trivial (a switch + default) because the goal is to keep
// user-facing strings out of the constants themselves — these labels are only
// used in plain-language StructuredError construction.

// contextKindLabel returns a short English label naming the kind of event,
// e.g. "git" for ContextGit. Used at the start of error sentences.
func contextKindLabel(kind protocol.ContextKind) string {
	switch kind {
	case protocol.ContextGit:
		return "git"
	case protocol.ContextSkill:
		return "skill"
	case protocol.ContextSession:
		return "session"
	case protocol.ContextEpoch:
		return "epoch"
	case protocol.ContextSlice:
		return "slice"
	default:
		return string(kind)
	}
}

// contextIDLabel returns a short English noun naming what the context-id
// stands for, e.g. "commit SHA" for ContextGit. Used in "linked to its X"
// phrases.
func contextIDLabel(kind protocol.ContextKind) string {
	switch kind {
	case protocol.ContextGit:
		return "commit SHA"
	case protocol.ContextSkill:
		return "skill run id"
	case protocol.ContextSession:
		return "session id"
	case protocol.ContextEpoch:
		return "epoch id"
	case protocol.ContextSlice:
		return "slice id"
	default:
		return "context id"
	}
}

// contextIDExample returns a short, concrete example of how a caller would
// pass the context-id for the given kind. Embedded in Fix bodies.
func contextIDExample(kind protocol.ContextKind) string {
	switch kind {
	case protocol.ContextGit:
		return "tasks.RecordGitEvent(ctx, tracker, db, \"<commit-sha>\", ...)"
	case protocol.ContextSkill:
		return "tasks.RecordSkillEvent(ctx, tracker, db, \"<skill-run-id>\", ...)"
	case protocol.ContextSession:
		return "tasks.RecordSessionEvent(ctx, tracker, db, \"<session-id>\", ...)"
	default:
		return "<helper>(ctx, tracker, db, \"<id>\", ...)"
	}
}

// ─── Auxiliary handle for free-floating writes (public) ──────────────────────

// OpenAuditDBForFreeFloating opens an auxiliary *sql.DB handle on the same
// pasture.db file that protocol.TaskTracker writes to, with the same WAL +
// busy_timeout=5000 pragmas openTaskTrackerImpl applies. It exists so callers
// outside internal/tasks (e.g. cmd/pastured wiring code, test code) can obtain
// a direct SQL handle for ad-hoc queries or pasture-specific table writes
// without depending on internal trackerImpl details.
//
// Note: the free-floating recording helpers (RecordGitEvent / RecordSkillEvent /
// RecordSessionEvent) no longer require this handle internally — they now use
// tracker.RecordEventReturningId which bundles the write + id recovery. The
// handle is still accepted as a parameter by those helpers for API compatibility.
//
// The returned handle MUST be closed by the caller; typically callers cache
// it for the lifetime of the daemon and Close it at shutdown.
//
// dbPath: the SAME path used to open the TaskTracker. Empty string resolves
// to DefaultDBPath() (matching openTaskTrackerImpl's behaviour).
//
// Errors are *pasterrors.StructuredError with CategoryConnection (file open
// failure) or CategoryStorage (PRAGMA failure).
//
// This helper is a thin re-export of openAuditHandle so the rest of the tree
// (cmd/pastured, hooks/git_recorder.go) doesn't need access to package-private
// names.
func OpenAuditDBForFreeFloating(dbPath string) (*sql.DB, error) {
	if dbPath == "" {
		dbPath = DefaultDBPath()
	}
	return openAuditHandle(dbPath)
}
