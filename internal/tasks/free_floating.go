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
//  1. Calls tracker.RecordEvent (writes the audit_events row).
//  2. Looks up the just-inserted row's id via the auxiliary *sql.DB.
//  3. Calls tracker.AttachContext (writes the context_edges row).
//  4. Returns the event id so the caller can attach further contexts if needed
//     (Scenario 7 multi-context attachment: e.g., a post-epoch git commit
//     citing epoch X gets BOTH a ContextGit edge and a ContextEpoch edge).
//
// ─── Why we take an explicit *sql.DB handle ──────────────────────────────────
//
// PROPOSAL-2 §7.11 specifies "protocol.TaskTracker.RecordEvent(...) → returns
// event_id", but the S5 interface signature is `RecordEvent(ctx, event) error`
// with no event_id return — the audit-side enhancement to surface
// LastInsertRowID is an explicit "out of scope for S5" item documented in
// internal/tasks/tracker_test.go's recordEventForTest helper (S5 worker note).
//
// Until that enhancement lands, the helpers in this file accept the auxiliary
// *sql.DB handle (the same handle openTaskTrackerImpl opens via
// openAuditHandle) so they can run a SELECT MAX(id) lookup against the SAME
// connection that just wrote — which is race-safe under D11 (low write
// contention; modernc/sqlite WAL + busy_timeout=5000) and matches the
// recordEventForTest pattern already in use by Scenarios 1 and 7.
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
	"errors"
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
//   - ctx: context for cancellation; propagates to RecordEvent and AttachContext.
//   - tracker: the unified TaskTracker (typically obtained from
//     OpenTaskTracker). MUST be non-nil.
//   - auditDB: the auxiliary *sql.DB handle that backs the audit subsystem (the
//     same one openTaskTrackerImpl opens via openAuditHandle). Used only for
//     the post-write SELECT MAX(id) lookup that recovers the event id; will
//     become unnecessary once the audit-side RecordEventReturningID
//     enhancement lands (PROPOSAL-2 §7.11). MUST be non-nil.
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
//   - CategoryValidation: nil tracker, nil auditDB, empty sha, or an empty
//     eventType (the underlying audit store would silently accept these but
//     they're programming errors here).
//   - CategoryStorage: RecordEvent / SELECT MAX(id) / AttachContext write or
//     read failure (file unwritable, schema drift, etc.). Underlying error
//     wrapped via pasterrors.StructuredError.Why.
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
//  3. Call tracker.RecordEvent — surfaces audit-store write failures as %w.
//  4. SELECT MAX(id) via auditDB — recovers the just-inserted row id (D11 low
//     write contention makes this race-safe in practice; recordEventForTest
//     uses the same pattern in tests).
//  5. Call tracker.AttachContext — writes the context_edges row.
//
// Returns the recovered event id on success so the caller can issue follow-up
// AttachContext calls for multi-context attachment (Scenario 7).
func recordFreeFloating(
	ctx context.Context,
	tracker protocol.TaskTracker,
	auditDB *sql.DB,
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
	if auditDB == nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.%s: auditDB is nil", fnName),
			Why:      "the helper was invoked without the auxiliary *sql.DB handle needed to recover the event id after RecordEvent",
			Impact:   "the free-floating event cannot be recorded; without the event id the AttachContext call cannot reference the event row",
			Fix:      "open the auxiliary handle via tasks.OpenAuditDBForFreeFloating(dbPath) (or expose your tracker's auditDB), then pass it to this helper alongside the tracker",
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

	if err := tracker.RecordEvent(ctx, event); err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.%s: tracker.RecordEvent failed for kind=%s contextID=%q eventType=%q", fnName, kind, contextID, eventType),
			Why:      err.Error(),
			Impact:   "the free-floating event was not persisted; no context_edges row will be created either",
			Fix:      "verify the SQLite file at the configured pasture.db path is writable and the schema is at v3 or higher (run 'pasture migrate' if you suspect schema drift)",
		}
	}

	// Recover the just-inserted row id. SELECT MAX(id) is race-safe under D11
	// (low write contention) and matches the pattern used by
	// recordEventForTest. When the audit-side RecordEventReturningID
	// enhancement lands (PROPOSAL-2 §7.11 future work), this lookup goes away.
	eventID, err := lookupLastEventID(ctx, auditDB)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.%s: failed to recover last event id after RecordEvent (kind=%s contextID=%q)", fnName, kind, contextID),
			Why:      err.Error(),
			Impact:   "the audit_events row was written but the context_edges attachment was skipped; the event will not appear in Timeline lookups for this (kind, contextID)",
			Fix:      "verify the auxiliary *sql.DB handle is open and connected to the same pasture.db file as the tracker; if the audit_events table is empty the underlying RecordEvent silently no-op'd",
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
// If the audit_events table is empty, returns (0, sql.ErrNoRows wrapped) so
// callers can distinguish "the prior RecordEvent silently no-op'd" from "the
// SELECT itself failed". Either case is fatal for the helper because the
// follow-on AttachContext needs a real id.
func lookupLastEventID(ctx context.Context, auditDB *sql.DB) (int64, error) {
	var id sql.NullInt64
	err := auditDB.QueryRowContext(ctx, `SELECT MAX(id) FROM audit_events`).Scan(&id)
	if err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, errors.New("audit_events table is empty after RecordEvent — the write was not persisted")
	}
	if id.Int64 <= 0 {
		return 0, fmt.Errorf("audit_events MAX(id)=%d is not positive; the table may be corrupted", id.Int64)
	}
	return id.Int64, nil
}

// ─── Auxiliary handle for free-floating writes (public) ──────────────────────

// OpenAuditDBForFreeFloating opens an auxiliary *sql.DB handle on the same
// pasture.db file that protocol.TaskTracker writes to, with the same WAL +
// busy_timeout=5000 pragmas openTaskTrackerImpl applies. It exists so callers
// outside internal/tasks (e.g. cmd/pastured wiring code, test code) can pair
// a TaskTracker with the SELECT MAX(id) lookup the free-floating helpers need
// without depending on internal trackerImpl details.
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
