// tasktracker.go — Public TaskTracker façade.
//
// PROPOSAL-2 §7.4 (R2; UAT-1 placement direction): the unified façade composes
// the upstream Provenance task tracker (28 methods) with pasture's audit Trail
// (4 methods) and adds 6 pasture-only methods. The interface lives in
// pkg/protocol so external dayvidpham-org modules can program against the
// façade; the implementation lives in internal/tasks (private to pasture).
//
// Implementation detail: PROPOSAL-2's pseudocode embeds `audit.Trail` directly,
// but the audit package already imports pkg/protocol (for AuditEvent /
// SessionEntry / PhaseId), so a literal embedding would create an import
// cycle. We resolve by re-declaring the 4 audit method signatures inline here;
// any audit.Trail implementation satisfies them automatically because the
// signatures match exactly. The net public surface is identical (28 + 4 + 6 +
// Close + OpenTaskTracker) — only the Go-level composition differs.
//
// See bd comment on aura-plugins-mbkfi for the full design rationale.

package protocol

import (
	"context"

	"github.com/dayvidpham/provenance"
)

// TaskTracker is the unified Pasture workflow-record façade. Implementations
// wrap a provenance.Tracker (task CRUD, edges, labels, comments, agents,
// activities) and an audit.Trail (event recording, query, session entries),
// both opened against the same SQLite file at ~/.local/share/pasture/pasture.db.
//
// The interface adds 6 pasture-only methods on top of the 28 + 4 inherited:
//
//   - Agent categorisation (R8): SetAgentCategories, AgentCategories
//   - Context attachment (R9): AttachContext, EventContexts, Timeline
//   - Lifecycle: Close (closes both wrapped subsystems exactly once)
//
// The constructor OpenTaskTracker is the supported way to obtain an instance;
// see its doc comment for error semantics. Callers MUST call Close on the
// returned tracker.
//
// All methods are safe for concurrent use; the SQLite file is opened in WAL
// mode with busy_timeout=5000 (PROPOSAL-2 §10.3 / D11 binding). The
// cross-subsystem race test (BLOCKER B3) in internal/tasks proves this.
type TaskTracker interface {
	// ─── Embedded: Provenance task CRUD, edges, labels, comments,
	// agents (Human/ML/Software), activities. See provenance.Tracker. ───
	provenance.Tracker

	// ─── Audit Trail surface (signatures match audit.Trail exactly) ─────

	// RecordEvent persists a single audit event. Returns an error if the
	// underlying store is unavailable or the write fails. The caller
	// (typically a Temporal activity) is responsible for retry policy.
	//
	// PROPOSAL-2 §7.11: workflows call this then immediately call
	// AttachContext with ContextEpoch. Free-floating events use other
	// ContextKind values (Git/Skill/Session).
	//
	// Note: callers that need the inserted event_id (so they can attach
	// context_edges rows in the same logical step) should prefer
	// RecordEventReturningID — it bundles the write + id-recovery in a
	// single call, removing the post-write SELECT MAX(id) round-trip the
	// S9 free-floating helpers had to do as a workaround.
	RecordEvent(ctx context.Context, event AuditEvent) error

	// RecordEventReturningID persists a single audit event and returns the
	// audit_events.id of the just-inserted row. The implementation reads the
	// id from sql.Result.LastInsertId on the SAME INSERT statement that wrote
	// the row, so the returned id is race-safe under any level of write
	// contention — independent of the D11 "low write contention" deployment
	// binding. Returns the new id and a nil error on success; on failure
	// returns 0 and an actionable *pasterrors.StructuredError.
	//
	// This is the canonical RecordEvent entry point for workflow activities
	// (PROPOSAL-2 §7.11): RecordTransition and RecordAuditEvent call this
	// then immediately call AttachContext(eventID, ContextEpoch, epochID)
	// to record the event-to-epoch correlation. Free-floating helpers
	// (RecordGitEvent / RecordSkillEvent / RecordSessionEvent) also use it
	// in place of the older SELECT MAX(id) workaround that this method
	// supersedes (Phase 11 R1-B per finding aura-plugins-d1h6y).
	//
	// Behaviour for non-SQLite trail backends (e.g. *audit.InMemoryAuditTrail
	// used in tests): the returned id is a synthetic per-trail monotonic
	// counter — it is NOT a real audit_events row id and MUST NOT be
	// persisted across processes. The counter is incremented atomically per
	// call so concurrent test goroutines always observe distinct ids,
	// matching the SQLite trail's per-statement-LastInsertId guarantee.
	// AttachContext on an in-memory trail is a no-op anyway (no
	// context_edges table backing it), so the synthetic id is only
	// meaningful for AttachContext-relative assertions in unit tests that
	// exercise the workflow integration path without paying for a real
	// SQLite file.
	RecordEventReturningID(ctx context.Context, event AuditEvent) (int64, error)

	// QueryEvents returns audit events filtered by epoch and (optionally)
	// phase / role. Results are returned in chronological order. epochID
	// is required and is always part of the WHERE clause.
	//
	// Note: this is the legacy v1 query path; new callers should prefer
	// Timeline(ctx, ContextEpoch, epochID) which uses the context_edges
	// JOIN and works for all ContextKind values, not just epoch.
	QueryEvents(ctx context.Context, epochID string, phase *PhaseId, role *string) ([]AuditEvent, error)

	// RecordSessionEntries persists a batch of SessionEntry records
	// atomically (single transaction). Nil or empty slices are no-ops.
	RecordSessionEntries(ctx context.Context, entries []SessionEntry) error

	// QuerySessionEntries returns all session entries for sessionID in
	// insertion order. Returns an empty (non-nil) slice when no entries
	// exist for sessionID.
	QuerySessionEntries(ctx context.Context, sessionID string) ([]SessionEntry, error)

	// ─── Pasture-side category decoration (R8) ──────────────────────────

	// SetAgentCategories upserts the (automaton, pasture-role) pair for
	// the given agent into pasture_agent_categories. Idempotent: a second
	// call with the same id replaces the row. Both AutomatonRole and
	// PastureRole MUST be valid enum values (see IsValid); a nil/zero
	// value is permitted and stored as the literal "None".
	//
	// Returns *pasterrors.StructuredError{Category: CategoryStorage} on
	// write failure, or {Category: CategoryValidation} if either enum
	// value is unknown.
	SetAgentCategories(id provenance.AgentID, automaton AutomatonRole, pastureRole PastureRole) error

	// AgentCategories returns the (automaton, pasture-role) pair stored
	// for id. Returns ("None", "None", nil) if no row exists for id.
	AgentCategories(id provenance.AgentID) (AutomatonRole, PastureRole, error)

	// ─── Context attachment (R9) ────────────────────────────────────────

	// AttachContext adds a row to context_edges binding eventID to the
	// (kind, contextID) pair. The (event_id, context_kind, context_id)
	// triple is the BCNF composite primary key — duplicate inserts are
	// idempotent (returns nil; the existing row is preserved).
	//
	// kind MUST be a valid ContextKind (kind.IsValid()); contextID MUST
	// be non-empty. Validation failures return CategoryValidation.
	AttachContext(ctx context.Context, eventID int64, kind ContextKind, contextID string) error

	// EventContexts returns the typed contexts attached to eventID, in
	// insertion order. Returns an empty (non-nil) slice when no edges
	// exist for eventID.
	EventContexts(ctx context.Context, eventID int64) ([]Context, error)

	// Timeline returns all events whose context_edges row matches the
	// (kind, contextID) pair, in chronological order. The intended usage:
	//
	//   events := tracker.Timeline(ctx, ContextEpoch, epochID)
	//   events := tracker.Timeline(ctx, ContextGit, "<sha>")
	//
	// A nil/empty contextID returns an empty slice (no error).
	Timeline(ctx context.Context, kind ContextKind, contextID string) ([]AuditEvent, error)

	// ─── Lifecycle ──────────────────────────────────────────────────────

	// Close releases all resources held by the tracker. It is safe to call
	// Close multiple times; the second and subsequent calls return nil.
	//
	// Note: provenance.Tracker also declares Close(), and the embedded
	// method satisfies this interface requirement; implementations MUST
	// however ensure both subsystems (the provenance.Tracker AND the
	// underlying audit.Trail's *sql.DB) are closed exactly once.
	Close() error
}

// OpenTaskTracker opens the unified SQLite database at dbPath, runs the audit
// migrator (PROPOSAL-2 §7.10), and returns a TaskTracker that wraps the
// resulting provenance.Tracker and audit.Trail on the same file.
//
// dbPath: filesystem path to the unified pasture.db. Empty string resolves to
// the conventional location (see internal/tasks.DefaultDBPath, which honours
// $PASTURE_DB_PATH and $XDG_DATA_HOME). Parent directories are created if
// missing.
//
// Errors are *pasterrors.StructuredError with one of three categories:
//
//   - CategoryConnection (exit code 2): file open failure (parent dir not
//     writable, file locked, etc.).
//   - CategoryStorage (exit code 5): migration failure or corrupt schema.
//   - CategoryValidation (exit code 1): newer-schema rejection (the database
//     file's audit_schema_meta.version is greater than this binary supports;
//     PROPOSAL-2 §7.10.4 / Scenario 5).
//
// Callers map the returned error to a process exit code via
// pasterrors.ExitCode and MUST call Close on the returned tracker.
//
// # Wiring requirement
//
// This function delegates to an implementation registered by
// internal/tasks.init(). The implementation is wired automatically when
// internal/tasks is imported — all in-tree binaries (cmd/pasture,
// cmd/pastured) and handler packages (internal/handlers) already import
// internal/tasks directly and therefore satisfy this requirement without any
// extra step.
//
// In-tree callers that do NOT go through internal/handlers should import
// internal/tasks directly and call tasks.OpenTaskTracker — the direct call
// is idiomatic Go and avoids the indirection entirely:
//
//	import "github.com/dayvidpham/pasture/internal/tasks"
//	tracker, err := tasks.OpenTaskTracker("") // preferred in-tree pattern
//
// If you intend to call protocol.OpenTaskTracker directly (e.g. from a new
// main package or an integration test), call MustHaveImpl() at program
// startup to panic with a clear error rather than discovering the missing
// wiring at the first open call:
//
//	import (
//	    "github.com/dayvidpham/pasture/pkg/protocol"
//	    _ "github.com/dayvidpham/pasture/internal/tasks" // wires the impl
//	)
//	func init() { protocol.MustHaveImpl() } // fail fast if wiring is absent
//
// Implementation note: the body lives in internal/tasks (UAT-1 placement
// binding per PROPOSAL-2 §7.4). pkg/protocol cannot import internal/tasks
// directly (import cycle: internal/tasks imports pkg/protocol for the
// TaskTracker type). The registered-var indirection is the minimal fix that
// keeps (TaskTracker, OpenTaskTracker) co-located in this file.
var openTaskTrackerImpl func(dbPath string) (TaskTracker, error)

// RegisterOpenTaskTracker is called by internal/tasks's init() to wire the
// constructor implementation. It is exported only so the internal package can
// assign through it; external packages MUST NOT call it directly (doing so is
// a programming error and will overwrite the implementation).
//
// This indirection keeps (TaskTracker, OpenTaskTracker) co-located in
// pkg/protocol (PROPOSAL-2 §7.4) while the body lives in internal/tasks
// (UAT-1 placement binding). Without it, OpenTaskTracker's body would import
// internal/tasks — forbidden because internal/tasks already imports
// pkg/protocol for the TaskTracker type, which would create an import cycle.
//
// See MustHaveImpl for a startup-time guard that panics if this function has
// not been called.
func RegisterOpenTaskTracker(impl func(dbPath string) (TaskTracker, error)) {
	openTaskTrackerImpl = impl
}

// OpenTaskTracker opens the unified SQLite database at dbPath and returns a
// wrapped TaskTracker. See the OpenTaskTracker var doc comment above for the
// full contract (errors, side effects, lifecycle, wiring requirement).
//
// If the implementation has not been registered (internal/tasks not imported),
// this function returns a descriptive error. Call MustHaveImpl() at startup to
// catch this condition at init time rather than at the first call site.
func OpenTaskTracker(dbPath string) (TaskTracker, error) {
	if openTaskTrackerImpl == nil {
		return nil, &openTaskTrackerNotWiredError{dbPath: dbPath}
	}
	return openTaskTrackerImpl(dbPath)
}

// MustHaveImpl panics if RegisterOpenTaskTracker has not yet been called.
// Call this at program startup (e.g. in a TestMain or an init() of a top-level
// package) to fail fast with a clear error rather than discovering the missing
// wiring the first time OpenTaskTracker is called.
//
// Example — add this to your main package or TestMain:
//
//	func init() { protocol.MustHaveImpl() }
//
// In-tree callers (cmd/pasture, cmd/pastured, internal/handlers) already import
// internal/tasks directly and therefore never need this guard. The guard exists
// for completeness and for any future in-module integration tests that construct
// main-like binaries without going through the handler layer.
func MustHaveImpl() {
	if openTaskTrackerImpl == nil {
		panic("pasture/protocol: OpenTaskTracker implementation not registered — " +
			"import github.com/dayvidpham/pasture/internal/tasks in your main package " +
			"(its init() wires the constructor), or call tasks.OpenTaskTracker directly " +
			"instead of protocol.OpenTaskTracker")
	}
}

// openTaskTrackerNotWiredError is the structured error returned when
// OpenTaskTracker is called before internal/tasks has been imported. It is
// emitted as a *pasterrors.StructuredError shape by Error(), but defined here
// to avoid a pkg/protocol → internal/errors dependency cycle.
type openTaskTrackerNotWiredError struct {
	dbPath string
}

func (e *openTaskTrackerNotWiredError) Error() string {
	return "pasture/protocol: OpenTaskTracker called before the constructor was registered — " +
		"import github.com/dayvidpham/pasture/internal/tasks in your main package " +
		"(its init() calls RegisterOpenTaskTracker), or call tasks.OpenTaskTracker directly; " +
		"call protocol.MustHaveImpl() at startup to catch this at init time rather than at open time"
}
