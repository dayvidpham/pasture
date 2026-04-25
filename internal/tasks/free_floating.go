// Package tasks — Free-floating event recording helpers (PROPOSAL-2 §7.5,
// §11 Scenario 6, §8 S9).
//
// "Free-floating" events are audit_events that are NOT anchored to an active
// EpochWorkflow — git commits, /aura:* skill invocations outside an epoch, and
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
//  1. Calls tracker.RecordEventReturningID (writes the audit_events row and
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
// implementation — RecordEventReturningID (added in S8) bundles the write +
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

	// EventSkillInvoked fires when a /aura:* skill is invoked (per Pasture
	// URD R9). Payload should contain at least {"skill": "aura:<name>"}.
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
//   - ctx: context for cancellation; propagates to RecordEventReturningID and
//     AttachContext.
//   - tracker: the unified TaskTracker (typically obtained from
//     OpenTaskTracker). MUST be non-nil.
//   - auditDB: retained for API compatibility with existing callers; no longer
//     used by the implementation (RecordEventReturningID bundles write + id
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
//   - CategoryStorage: RecordEventReturningID / AttachContext write failure
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
// run id (e.g., "aura:user-elicit-<run-id>").
//
// See RecordGitEvent for the parameter / error contract; only the context kind
// (ContextSkill) and the suggested EventType (EventSkillInvoked) differ.
//
// `skillRunID` is the canonical id of the skill invocation. Per PROPOSAL-2
// §7.3 example: "aura:user-elicit-<run-id>" — the wire string is whatever the
// caller wants to use for Timeline lookups via
// `pasture task events --context-kind=SkillContext --context-id=<run-id>`.
func RecordSkillEvent(
	ctx context.Context,
	tracker protocol.TaskTracker,
	auditDB *sql.DB,
	skillRunID string,
	eventType protocol.EventType,
	payload map[string]any,
) (int64, error) {
	return recordFreeFloating(ctx, tracker, auditDB, protocol.ContextSkill, skillRunID, eventType, payload, DefaultSkillRole, "RecordSkillEvent")
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
	sessionID string,
	eventType protocol.EventType,
	payload map[string]any,
) (int64, error) {
	return recordFreeFloating(ctx, tracker, auditDB, protocol.ContextSession, sessionID, eventType, payload, DefaultSessionRole, "RecordSessionEvent")
}

// ─── Internal: shared recording flow ─────────────────────────────────────────

// recordFreeFloating is the shared implementation of RecordGitEvent /
// RecordSkillEvent / RecordSessionEvent. It is package-private because:
//   - Each public helper documents a specific (kind, ContextID-shape) contract
//     that callers depend on; collapsing them to a single Record(kind, id)
//     surface would lose that documentation.
//   - The "fnName" parameter is purely cosmetic (it appears in error.What for
//     attribution); we do not want callers passing arbitrary names.
//
// The flow:
//
//  1. Validate inputs (early-return *StructuredError on any invalid input).
//  2. Build the AuditEvent with EpochID="" (no epoch anchor — Scenario 6
//     "no `epoch_id` column or fail because no epoch is active"). The
//     audit_events.epoch_id NOT NULL constraint accepts empty string at the
//     SQL level; v4 migration (S4) drops the column entirely so this becomes
//     moot post-S4. The Phase field is also left empty for the same reason —
//     PROPOSAL-2 §7.2 documents the column as "nullable for free-floating
//     events" in the v3+ schema.
//  3. Call tracker.RecordEventReturningID — persists the event and returns
//     its audit_events.id atomically (no separate SELECT MAX round-trip).
//  4. Call tracker.AttachContext — writes the context_edges row.
//
// The auditDB parameter is retained for API compatibility with existing callers
// (hooks, daemon wiring) but is not used by this implementation now that
// RecordEventReturningID bundles the write + id-recovery in a single call.
//
// Returns the event id on success so the caller can issue follow-up
// AttachContext calls for multi-context attachment (Scenario 7).
func recordFreeFloating(
	ctx context.Context,
	tracker protocol.TaskTracker,
	_ *sql.DB, // auditDB: retained for call-site compatibility; no longer used (RecordEventReturningID bundles write + id)
	kind protocol.ContextKind,
	contextID string,
	eventType protocol.EventType,
	payload map[string]any,
	defaultRole string,
	fnName string,
) (int64, error) {
	if tracker == nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.%s: tracker is nil", fnName),
			Why:      "the helper was invoked without a TaskTracker — this is a programming error",
			Impact:   "the free-floating event cannot be recorded; the call is a no-op from the caller's perspective but no row was written",
			Fix:      "obtain a TaskTracker via tasks.OpenTaskTracker(dbPath) or protocol.OpenTaskTracker(dbPath) and pass it to this helper",
		}
	}
	if contextID == "" {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.%s: contextID is empty", fnName),
			Why:      "an empty context_id would be persisted as a row that no Timeline lookup can find (kind=" + string(kind) + ", context_id=\"\")",
			Impact:   "the free-floating event cannot be recorded with a useful context attachment",
			Fix:      "pass the canonical id for the kind (for ContextGit: a commit SHA; for ContextSkill: the skill run id; for ContextSession: the session id)",
		}
	}
	if eventType == "" {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.%s: eventType is empty", fnName),
			Why:      "an empty event_type would be persisted but cannot be filtered against in QueryEvents / Timeline",
			Impact:   "the free-floating event cannot be classified at recall time",
			Fix:      "pass one of the well-known constants in this package (EventGitCommit / EventGitPush / EventGitRebase / EventSkillInvoked / EventSessionRecorded) or a project-specific event type string",
		}
	}

	// Build the AuditEvent. EpochID and Phase are intentionally left empty —
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
		EpochID:   "",
		Phase:     "",
		Role:      defaultRole,
		EventType: eventType,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}

	// RecordEventReturningID atomically persists the event and returns its
	// audit_events.id. This replaces the previous two-step pattern of
	// RecordEvent + SELECT MAX(id) (lookupLastEventID) that free_floating.go
	// used before RecordEventReturningID was added to the interface (S8).
	eventID, err := tracker.RecordEventReturningID(ctx, event)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.%s: tracker.RecordEventReturningID failed for kind=%s contextID=%q eventType=%q", fnName, kind, contextID, eventType),
			Why:      err.Error(),
			Impact:   "the free-floating event was not persisted; no context_edges row will be created either",
			Fix:      "verify the SQLite file at the configured pasture.db path is writable and the schema is at v3 or higher (run 'pasture migrate' if you suspect schema drift)",
		}
	}

	if err := tracker.AttachContext(ctx, eventID, kind, contextID); err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.%s: tracker.AttachContext failed for event %d kind=%s contextID=%q", fnName, eventID, kind, contextID),
			Why:      err.Error(),
			Impact:   "the audit_events row was written but no context_edges row exists; the event is reachable via QueryEvents but invisible to Timeline / 'pasture task events --context-kind=...' lookups",
			Fix:      "the audit_events row already exists; re-call tracker.AttachContext(ctx, " + fmt.Sprintf("%d", eventID) + ", kind, contextID) directly to attach the context, or treat the event as orphaned and re-record it",
		}
	}

	return eventID, nil
}

// lookupLastEventID returns the highest audit_events.id value visible to the
// supplied auditDB connection. It is intentionally simple (a single SELECT
// MAX(id)) — the race-window between RecordEvent and this read is guarded by
// D11's "low write contention" binding and the modernc/sqlite WAL mode (which
// gives readers immediate visibility of committed writes).
//
// This helper is used by trackerImpl.RecordEventReturningID (tracker.go) to
// recover the just-inserted row id after trail.RecordEvent commits. It is
// retained here (rather than moved to tracker.go) because it captures the
// well-known error messages for the two failure modes callers must handle:
// empty table and non-positive id.
//
// If the audit_events table is empty, returns (0, *StructuredError{CategoryStorage})
// so callers can distinguish "the prior RecordEvent silently no-op'd" from
// "the SELECT itself failed". Either case is fatal because the follow-on
// AttachContext needs a real id.
func lookupLastEventID(ctx context.Context, auditDB *sql.DB) (int64, error) {
	var id sql.NullInt64
	err := auditDB.QueryRowContext(ctx, `SELECT MAX(id) FROM audit_events`).Scan(&id)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "tasks.lookupLastEventID: SELECT MAX(id) FROM audit_events failed",
			Why:      err.Error(),
			Impact:   "the just-inserted audit_events row id cannot be recovered; any dependent AttachContext call will be skipped",
			Fix:      "verify the auxiliary *sql.DB handle is open and the schema contains the audit_events table (run 'pasture migrate' if you suspect schema drift)",
		}
	}
	if !id.Valid {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "tasks.lookupLastEventID: audit_events table is empty after RecordEvent",
			Why:      "SELECT MAX(id) returned NULL, meaning the prior RecordEvent write was not persisted to the audit_events table",
			Impact:   "the event id cannot be recovered; the follow-on AttachContext call will be skipped and the event will not appear in Timeline lookups",
			Fix:      "verify the SQLite file is writable and the schema is at v3 or higher; inspect via 'sqlite3 <db> \"SELECT COUNT(*) FROM audit_events\"'",
		}
	}
	if id.Int64 <= 0 {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.lookupLastEventID: audit_events MAX(id)=%d is not positive", id.Int64),
			Why:      "the highest audit_events.id is zero or negative, which indicates table corruption or an id sequence reset",
			Impact:   "a non-positive event id cannot be used for AttachContext; the context_edges row will not be created",
			Fix:      "inspect the audit_events table directly via 'sqlite3 <db> \"SELECT id FROM audit_events ORDER BY id DESC LIMIT 5\"' and file a bug if ids are unexpectedly non-positive",
		}
	}
	return id.Int64, nil
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
// tracker.RecordEventReturningID which bundles the write + id recovery. The
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
