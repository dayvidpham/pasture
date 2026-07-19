// Package tasks — TaskTracker implementation (PROPOSAL-2 §7.4 + UAT-1).
//
// trackerImpl is the unified TaskTracker façade. It wraps a provenance.Tracker
// (28 task/agent/activity methods) and an audit.Trail (4 audit/session methods)
// opened against the same SQLite file at ~/.local/share/pasture/pasture.db,
// and adds the 6 pasture-only methods declared on protocol.TaskTracker:
//
//   - SetAgentCategories / AgentCategories  → pasture_agent_categories
//   - AttachContext / EventContexts / Timeline → context_edges
//   - Close → closes both wrapped subsystems exactly once
//
// The constructor lives in open_unified.go; this file contains only the
// wrapper type and its methods so the file boundary mirrors the conceptual
// split between "what the type IS" (here) and "how it's wired up" (open_unified).
//
// The wrapper uses a pasture-side *sql.DB (the same handle backing the audit
// subsystem) for the 6 new methods. Single-writer serialisation (sqlite.go's
// SetMaxOpenConns(1) + busy_timeout=5000 + WAL mode) gives us cross-subsystem
// safety on one file. The race test in tracker_race_test.go proves D11/C5.
//
// Concurrency note: the io.Closer-style Close() is idempotent (sync.Once); a
// double-close returns nil rather than a use-after-free. The 6 new methods are
// safe for concurrent use because *sql.DB is itself goroutine-safe.

package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// trackerImpl is the concrete TaskTracker. It wraps a provenance.Tracker (for
// task/agent/activity ops) and an audit.Trail (for event/session ops), both
// backed by the same SQLite file, and exposes a *sql.DB handle for the 6
// pasture-only methods that operate on context_edges and pasture_agent_categories.
type trackerImpl struct {
	prov      provenance.Tracker
	trail     audit.Trail
	auditDB   *sql.DB // shared with trail; used for pasture-only table writes
	closeOnce sync.Once
	closeErr  error

	// sysOnce/sysSession/sysErr memoize the journaled task-backend system identity
	// (committing actor + genesis authority). The mutation verbs commit through
	// this Session (Tracker.As), so the identity is resolved and persisted at most
	// once per tracker lifetime. See system_identity.go.
	sysOnce    sync.Once
	sysSession *provenance.Session
	sysErr     error

	// writeMu serializes every write across BOTH backing connection families —
	// the provenance journal connection (task/edge/label/comment/agent/activity
	// writes) and the pasture audit connection (event/context/category writes).
	// The journaled backend commits each mutation as a multi-statement Apply
	// transaction that holds the WAL write lock longer than the pre-journal
	// direct write did; two separate connections racing that longer lock produced
	// SQLITE_BUSY_SNAPSHOT that busy_timeout cannot absorb (it is returned
	// immediately, not retried). Interposing this single writer serializes the two
	// families so contention never escapes as a busy error — the interposition the
	// unified-DB race test (tracker_race_test.go) anticipates. Reads stay
	// unsynchronized (WAL concurrent readers). The bootstrap path (system_identity.go)
	// writes through t.prov directly, so a guarded verb that triggers bootstrap does
	// not re-enter this mutex.
	writeMu sync.Mutex

	// pastureTablesOnce ensures ensurePastureTables runs at most once for the
	// lifetime of this trackerImpl, even when test helpers construct the struct
	// without going through openTaskTrackerImpl (which calls ensurePastureTables
	// upfront). Methods that need the pasture tables call
	// t.ensurePastureTablesOnce() instead of ensurePastureTables directly.
	pastureTablesOnce sync.Once
	pastureTablesErr  error

	// Schema-awareness flags for the audit_events table. Probed once in
	// newTrackerImpl via PRAGMA table_info and cached here so Timeline never
	// issues PRAGMA table_info more than once per tracker lifetime. The flags
	// are stable for the lifetime of the connection: pasture schema migrations
	// are transactional and no DDL is applied while a tracker is open.
	hasRoleColumn    bool // true when audit_events still has the legacy `role` column (pre-v3 schema)
	hasEpochIDColumn bool // true when audit_events still has `epoch_id` (pre-v4 schema)
}

// newTrackerImpl wires up a trackerImpl. The caller (OpenTaskTracker) is
// responsible for opening prov, trail, and auditDB against the same dbPath
// and for running audit.Migrate before this constructor is invoked. This
// helper is package-private so it can also be used by tests with mocked
// dependencies.
//
// auditDB MUST be the same *sql.DB handle used by trail; the race test relies
// on single-writer serialisation through this one handle.
//
// newTrackerImpl calls ensurePastureTables once and caches the
// audit_events column-presence flags (hasRoleColumn, hasEpochIDColumn) so
// that Timeline never issues PRAGMA table_info more than once per tracker
// lifetime.
func newTrackerImpl(prov provenance.Tracker, trail audit.Trail, auditDB *sql.DB) *trackerImpl {
	t := &trackerImpl{
		prov:    prov,
		trail:   trail,
		auditDB: auditDB,
	}

	// Prime the pasture tables and column-cache eagerly. openTaskTrackerImpl
	// already called ensurePastureTables before reaching here, so this is
	// effectively a no-op (the Once body runs but the DDL is idempotent and
	// fast). For test helpers that bypass openTaskTrackerImpl the Once ensures
	// the tables exist before any method body touches them.
	t.pastureTablesOnce.Do(func() {
		t.pastureTablesErr = ensurePastureTables(auditDB)
	})

	// Probe the audit_events schema once. If auditDB is nil or
	// ensurePastureTables failed we leave both flags false (safe default:
	// callers will receive a storage error when they actually try to query).
	if auditDB != nil && t.pastureTablesErr == nil {
		// Use a background context for the one-time probe; it is not
		// cancellable by the caller because it happens at construction time.
		ctx := context.Background()
		if hasRole, err := auditEventsHasColumn(ctx, auditDB, "role"); err == nil {
			t.hasRoleColumn = hasRole
		}
		if hasEpochId, err := auditEventsHasColumn(ctx, auditDB, "epoch_id"); err == nil {
			t.hasEpochIDColumn = hasEpochId
		}
	}

	return t
}

// ensurePastureTablesOnce is the in-method guard that replaces per-call
// ensurePastureTables(t.auditDB) invocations. It delegates to the sync.Once
// that was already fired in newTrackerImpl (so this is a no-op on the hot
// path) and returns the cached error if the initial DDL call failed.
func (t *trackerImpl) ensurePastureTablesOnce() error {
	t.pastureTablesOnce.Do(func() {
		t.pastureTablesErr = ensurePastureTables(t.auditDB)
	})
	return t.pastureTablesErr
}

// ─── Embedded forwarding: provenance.Tracker (28 methods) ────────────────────
//
// Promoted via field embedding would work, but Go's embedding rules pull in
// the field name in test/debug output. Explicit forwarding gives us
//   (a) a single grep target for "all the things I forward to provenance",
//   (b) clearer test failures (the method receiver is *trackerImpl, not the
//       embedded interface), and
//   (c) zero confusion when the audit-side methods (RecordEvent etc.) are
//       declared inline below — readers see the full surface in one file.
//
// The 28 methods below are signature-identical to provenance.Tracker; updates
// to the upstream interface will be caught at compile time by the
// `var _ protocol.TaskTracker = (*trackerImpl)(nil)` check at the bottom of
// this file.

// ─── Journaled mutation verbs (Tracker.As → Session) ─────────────────────────
//
// Task/edge/label/comment MUTATIONS moved off the direct-write Tracker onto the
// journaled Session SDK (provenance.Tracker.As): each verb commits one logical
// operation through the ordered journal under the pasture-system committing actor
// and genesis authority (system_identity.go). These façade methods keep the exact
// pre-journal signatures so every caller is unchanged, and route the write through
// the memoized system Session, so the observable behaviour (returned task/comment,
// typed errors) is preserved while the change is now journaled and reproducible.
//
// As exposes the underlying binding directly for callers that need to commit under
// a specific (actor, authority) pair rather than the system identity.

// As returns a Session bound to the given committing actor and governing authority.
func (t *trackerImpl) As(actor provenance.ActorID, authority provenance.JournalID) *provenance.Session {
	return t.prov.As(actor, authority)
}

// Journal exposes the ordered global-journal surface.
func (t *trackerImpl) Journal() provenance.JournalAPI {
	return t.prov.Journal()
}

// lockWrite acquires the single cross-connection write mutex and returns the
// paired unlock, for `defer t.lockWrite()()` at the top of every write method.
func (t *trackerImpl) lockWrite() func() {
	t.writeMu.Lock()
	return t.writeMu.Unlock
}

// Task CRUD.

func (t *trackerImpl) Create(namespace, title, description string, taskType provenance.TaskType, priority provenance.Priority, phase provenance.Phase) (provenance.Task, error) {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return provenance.Task{}, err
	}
	return s.Create(namespace, title, description, taskType, priority, phase)
}
func (t *trackerImpl) Show(id provenance.TaskID) (provenance.Task, error) {
	return t.prov.Show(id)
}
func (t *trackerImpl) Update(id provenance.TaskID, fields provenance.UpdateFields) (provenance.Task, error) {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return provenance.Task{}, err
	}
	return s.Update(id, fields)
}
func (t *trackerImpl) CloseTask(id provenance.TaskID, reason string) (provenance.Task, error) {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return provenance.Task{}, err
	}
	return s.CloseTask(id, reason)
}

// Start transitions a task open → in_progress through the journaled lifecycle FSM.
func (t *trackerImpl) Start(id provenance.TaskID) (provenance.Task, error) {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return provenance.Task{}, err
	}
	return s.Start(id)
}

// Stop transitions a task in_progress → open through the journaled lifecycle FSM.
func (t *trackerImpl) Stop(id provenance.TaskID) (provenance.Task, error) {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return provenance.Task{}, err
	}
	return s.Stop(id)
}

// Reopen transitions a task closed → open through the journaled lifecycle FSM.
func (t *trackerImpl) Reopen(id provenance.TaskID) (provenance.Task, error) {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return provenance.Task{}, err
	}
	return s.Reopen(id)
}

func (t *trackerImpl) List(filter provenance.ListFilter) ([]provenance.Task, error) {
	return t.prov.List(filter)
}

// Edges.

func (t *trackerImpl) AddEdge(sourceId provenance.TaskID, targetId string, kind provenance.EdgeKind) error {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return err
	}
	return s.AddEdge(sourceId, targetId, kind)
}
func (t *trackerImpl) RemoveEdge(sourceId provenance.TaskID, targetId string, kind provenance.EdgeKind) error {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return err
	}
	return s.RemoveEdge(sourceId, targetId, kind)
}
func (t *trackerImpl) Edges(id provenance.TaskID, kind *provenance.EdgeKind) ([]provenance.Edge, error) {
	return t.prov.Edges(id, kind)
}

// Readiness.

func (t *trackerImpl) Blocked() ([]provenance.Task, error) { return t.prov.Blocked() }
func (t *trackerImpl) Ready() ([]provenance.Task, error)   { return t.prov.Ready() }
func (t *trackerImpl) DepTree(id provenance.TaskID) ([]provenance.Edge, error) {
	return t.prov.DepTree(id)
}
func (t *trackerImpl) Ancestors(id provenance.TaskID) ([]provenance.Task, error) {
	return t.prov.Ancestors(id)
}
func (t *trackerImpl) Descendants(id provenance.TaskID) ([]provenance.Task, error) {
	return t.prov.Descendants(id)
}

// Labels.

func (t *trackerImpl) AddLabel(id provenance.TaskID, label string) error {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return err
	}
	return s.AddLabel(id, label)
}
func (t *trackerImpl) RemoveLabel(id provenance.TaskID, label string) error {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return err
	}
	return s.RemoveLabel(id, label)
}
func (t *trackerImpl) Labels(id provenance.TaskID) ([]string, error) { return t.prov.Labels(id) }

// Comments.

func (t *trackerImpl) AddComment(id provenance.TaskID, authorId provenance.AgentID, body string) (provenance.Comment, error) {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return provenance.Comment{}, err
	}
	return s.AddComment(id, authorId, body)
}
func (t *trackerImpl) Comments(id provenance.TaskID) ([]provenance.Comment, error) {
	return t.prov.Comments(id)
}

// Agents.

func (t *trackerImpl) RegisterHumanAgent(namespace, name, contact string) (provenance.HumanAgent, error) {
	defer t.lockWrite()()
	return t.prov.RegisterHumanAgent(namespace, name, contact)
}
func (t *trackerImpl) RegisterMLAgent(namespace string, role provenance.Role, provider provenance.Provider, modelName provenance.ModelID) (provenance.MLAgent, error) {
	defer t.lockWrite()()
	return t.prov.RegisterMLAgent(namespace, role, provider, modelName)
}
func (t *trackerImpl) RegisterSoftwareAgent(namespace, name, version, source string) (provenance.SoftwareAgent, error) {
	defer t.lockWrite()()
	return t.prov.RegisterSoftwareAgent(namespace, name, version, source)
}
func (t *trackerImpl) Agent(id provenance.AgentID) (provenance.Agent, error) {
	return t.prov.Agent(id)
}
func (t *trackerImpl) HumanAgent(id provenance.AgentID) (provenance.HumanAgent, error) {
	return t.prov.HumanAgent(id)
}
func (t *trackerImpl) MLAgent(id provenance.AgentID) (provenance.MLAgent, error) {
	return t.prov.MLAgent(id)
}
func (t *trackerImpl) SoftwareAgent(id provenance.AgentID) (provenance.SoftwareAgent, error) {
	return t.prov.SoftwareAgent(id)
}

// Activities.

func (t *trackerImpl) StartActivity(agentId provenance.AgentID, phase provenance.Phase, stage provenance.Stage, notes string) (provenance.Activity, error) {
	defer t.lockWrite()()
	return t.prov.StartActivity(agentId, phase, stage, notes)
}
func (t *trackerImpl) StartActivityWithID(id provenance.ActivityID, agentId provenance.AgentID, phase provenance.Phase, stage provenance.Stage, notes string) (provenance.Activity, error) {
	defer t.lockWrite()()
	return t.prov.StartActivityWithID(id, agentId, phase, stage, notes)
}
func (t *trackerImpl) EndActivity(id provenance.ActivityID) (provenance.Activity, error) {
	defer t.lockWrite()()
	return t.prov.EndActivity(id)
}
func (t *trackerImpl) Activities(agentId *provenance.AgentID) ([]provenance.Activity, error) {
	return t.prov.Activities(agentId)
}

// ─── Audit Trail surface (4 methods, signature-identical to audit.Trail) ─────

func (t *trackerImpl) RecordEvent(ctx context.Context, event protocol.AuditEvent) error {
	defer t.lockWrite()()
	return t.trail.RecordEvent(ctx, event)
}

// RecordEventReturningId forwards to the wrapped audit.Trail's
// RecordEventReturningId and returns the just-inserted audit_events.id.
//
// Race safety: the underlying audit.Trail recovers the id from
// sql.Result.LastInsertId on the SAME INSERT statement that wrote the row
// (per-statement, not per-connection — see audit/sqlite.go's
// SqliteAuditTrail.RecordEventReturningId). This is race-free under any level
// of write contention and replaces the older "trail.RecordEvent + SELECT
// MAX(id) on auditDB" workaround that could return a row id belonging to a
// concurrent writer (PROPOSAL-2 §7.11 future-work, realised in Phase 11 R1-B
// per finding aura-plugins-d1h6y).
//
// Errors are propagated unchanged from the trail (already shaped as
// *pasterrors.StructuredError on the SQLite backend). Callers that need to
// attribute the failure to the trackerImpl façade may inspect the error's
// What field — the audit-side messages name the SqliteAuditTrail receiver
// directly so the origin is clear without re-wrapping.
func (t *trackerImpl) RecordEventReturningId(ctx context.Context, event protocol.AuditEvent) (int64, error) {
	defer t.lockWrite()()
	return t.trail.RecordEventReturningId(ctx, event)
}
func (t *trackerImpl) QueryEvents(ctx context.Context, epochId string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	return t.trail.QueryEvents(ctx, epochId, phase, role)
}
func (t *trackerImpl) RecordSessionEntries(ctx context.Context, entries []protocol.SessionEntry) error {
	defer t.lockWrite()()
	return t.trail.RecordSessionEntries(ctx, entries)
}
func (t *trackerImpl) QuerySessionEntries(ctx context.Context, sessionId string) ([]protocol.SessionEntry, error) {
	return t.trail.QuerySessionEntries(ctx, sessionId)
}

// ─── Pasture-side category decoration (R8) ───────────────────────────────────

// SetAgentCategories upserts the (automaton, pasture) pair for id into
// pasture_agent_categories. Uses INSERT OR REPLACE; idempotent.
//
// Validation: both enums must be valid IsValid() members. Empty / unknown
// values produce CategoryValidation. Storage failures produce CategoryStorage.
func (t *trackerImpl) SetAgentCategories(id provenance.AgentID, automaton protocol.AutomatonRole, pastureRole protocol.PastureRole) error {
	defer t.lockWrite()()
	if !automaton.IsValid() {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture got an unknown automation role %q when setting an agent's category.", automaton),
			Why: "Automation roles must be one of the names pasture knows about; this one\n" +
				"isn't on the list. Either it's misspelled or it was made up.",
			Where: "Setting an agent's category (internal/tasks/tracker.go in trackerImpl.SetAgentCategories).",
			Impact: "The agent's category isn't saved. Anything that filters by automation\n" +
				"role won't see this agent.",
			Fix: "1. Pass one of the named automation roles, for example:\n" +
				"     none, constraint-checker, hook-handler\n" +
				"2. List the full set of valid roles to find the one you meant:\n" +
				"     pasture task agents list --roles",
		}
	}
	if !pastureRole.IsValid() {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture got an unknown pasture role %q when setting an agent's category.", pastureRole),
			Why: "Pasture roles must be one of the names pasture knows about; this one\n" +
				"isn't on the list. Either it's misspelled or it was made up.",
			Where: "Setting an agent's category (internal/tasks/tracker.go in trackerImpl.SetAgentCategories).",
			Impact: "The agent's category isn't saved. Anything that filters by pasture\n" +
				"role won't see this agent.",
			Fix: "1. Pass one of the named pasture roles, for example:\n" +
				"     none, architect, worker\n" +
				"2. List the full set of valid roles to find the one you meant:\n" +
				"     pasture task agents list --roles",
		}
	}

	if err := t.ensurePastureTablesOnce(); err != nil {
		return err
	}

	_, err := t.auditDB.Exec(
		`INSERT OR REPLACE INTO pasture_agent_categories (agent_id, automaton_role, pasture_role)
		 VALUES (?, ?, ?)`,
		id.String(), string(automaton), string(pastureRole),
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't save the category for agent %q.", id.String()),
			Why:      "Tried to write the category to the database but the write failed.",
			Where:    "Setting an agent's category (internal/tasks/tracker.go in trackerImpl.SetAgentCategories).",
			Impact: "The agent's category isn't saved. Lookups will return the default\n" +
				"\"None\" category until the row is written.",
			Fix: "1. Confirm the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the operation once the database is healthy.",
			Cause: err,
		}
	}
	return nil
}

// AgentCategories returns the (automaton, pasture) pair for id. Returns
// ("None","None", nil) when no row exists for id (this models "no category
// has been set" rather than an error condition).
func (t *trackerImpl) AgentCategories(id provenance.AgentID) (protocol.AutomatonRole, protocol.PastureRole, error) {
	if err := t.ensurePastureTablesOnce(); err != nil {
		return "", "", err
	}
	var automatonStr, pastureRoleStr string
	err := t.auditDB.QueryRow(
		`SELECT automaton_role, pasture_role
		 FROM pasture_agent_categories
		 WHERE agent_id = ?`,
		id.String(),
	).Scan(&automatonStr, &pastureRoleStr)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.AutomatonRoleNone, protocol.PastureRoleNone, nil
	}
	if err != nil {
		return "", "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't read the category for agent %q.", id.String()),
			Why:      "Tried to look up the category in the database but the read failed.",
			Where:    "Reading an agent's category (internal/tasks/tracker.go in trackerImpl.AgentCategories).",
			Impact: "The agent's category can't be returned. Anything that needs to filter\n" +
				"by category will fall back to the default \"None\".",
			Fix: "1. Confirm the database is readable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the operation once the database is healthy.",
			Cause: err,
		}
	}
	return protocol.AutomatonRole(automatonStr), protocol.PastureRole(pastureRoleStr), nil
}

// ─── Context attachment (R9) ─────────────────────────────────────────────────

// AttachContext writes (eventId, kind, contextId) into context_edges.
//
// The (event_id, context_kind, context_id) triple is the BCNF composite
// primary key — duplicate inserts are converted to no-ops via INSERT OR
// IGNORE so the call is idempotent (repeated calls return nil and leave the
// existing row untouched).
//
// kind MUST be valid; contextId MUST be non-empty (a zero-length context_id
// is meaningless and would silently break Timeline lookups).
func (t *trackerImpl) AttachContext(ctx context.Context, eventId int64, kind protocol.ContextKind, contextId string) error {
	defer t.lockWrite()()
	if !kind.IsValid() {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture got an unknown context kind %q when linking an event.", kind),
			Why: "Context kinds must be one of the names pasture knows about (such as\n" +
				"epoch, slice, or git); this one isn't on the list.",
			Where: "Linking an event to a context (internal/tasks/tracker.go in trackerImpl.AttachContext).",
			Impact: "The event isn't linked to anything, so it won't show up when you ask\n" +
				"for events by this kind of context.",
			Fix: "1. Pass one of the named context kinds, for example:\n" +
				"     epoch, slice, git, skill, session\n" +
				"2. List the full set of valid kinds to find the one you meant:\n" +
				"     pasture task contexts list --kinds",
		}
	}
	if contextId == "" {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Pasture tried to link an event to a context with no identifier.",
			Why: "The context identifier was an empty string. Without an identifier,\n" +
				"no future lookup could find the event again.",
			Where:  "Linking an event to a context (internal/tasks/tracker.go in trackerImpl.AttachContext).",
			Impact: "The event isn't linked, so it won't show up in any context-based query.",
			Fix: "1. Pass a real identifier for the kind of context you're linking:\n" +
				"     - for an epoch: the originating REQUEST task's id\n" +
				"     - for a git commit: the commit SHA\n" +
				"     - for a skill invocation: the skill run id\n" +
				"     - for a session: the session id\n" +
				"2. If you don't have an id yet, create the parent task first to get one:\n" +
				"     pasture task create REQUEST --type=feature \"<title>\"",
		}
	}
	if eventId <= 0 {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture got a non-positive event id (%d) when linking an event.", eventId),
			Why: "Event ids are always positive numbers handed back when an event is\n" +
				"saved. A zero or negative value means the caller never recorded the\n" +
				"event before trying to link it. This is a bug in the calling code.",
			Where:  "Linking an event to a context (internal/tasks/tracker.go in trackerImpl.AttachContext).",
			Impact: "The event isn't linked to anything.",
			Fix: "1. Save the event first and use the id that's returned, then link it.\n" +
				"2. If you hit this from the CLI rather than from your own code, please\n" +
				"   file a bug — it shouldn't be reachable in normal use.",
		}
	}

	if err := t.ensurePastureTablesOnce(); err != nil {
		return err
	}

	// INSERT OR IGNORE preserves the BCNF idempotency guarantee: re-issuing
	// the same (event_id, context_kind, context_id) triple is a no-op.
	_, err := t.auditDB.ExecContext(ctx,
		`INSERT OR IGNORE INTO context_edges (event_id, context_kind, context_id)
		 VALUES (?, ?, ?)`,
		eventId, string(kind), contextId,
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't link event %d to its %s.", eventId, contextKindLabel(kind)),
			Why: fmt.Sprintf(
				"Tried to write the link to %s %q in the database but the write failed.",
				contextIDLabel(kind), contextId,
			),
			Where: "Linking an event to a context (internal/tasks/tracker.go in trackerImpl.AttachContext).",
			Impact: fmt.Sprintf(
				"The event is in the database but won't show up when you ask for events\n"+
					"by %s, which leaves a gap in the recorded history.",
				contextIDLabel(kind),
			),
			Fix: "1. Confirm the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the link once the database is healthy.",
			Cause: err,
		}
	}
	return nil
}

// EventContexts returns all (Kind, ContextId) edges attached to eventId, in
// insertion order (rowid ASC). Returns an empty (non-nil) slice when no
// edges exist for eventId.
func (t *trackerImpl) EventContexts(ctx context.Context, eventId int64) ([]protocol.Context, error) {
	if err := t.ensurePastureTablesOnce(); err != nil {
		return nil, err
	}

	rows, err := t.auditDB.QueryContext(ctx,
		`SELECT context_kind, context_id
		 FROM context_edges
		 WHERE event_id = ?
		 ORDER BY rowid ASC`,
		eventId,
	)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't read the contexts linked to event %d.", eventId),
			Why: "The database query asking which contexts (epoch, slice, git, ...) this\n" +
				"event is linked to failed.",
			Where: "Reading the contexts for an event (internal/tasks/tracker.go in trackerImpl.EventContexts).",
			Impact: "The list of contexts for this event can't be returned, so anything that\n" +
				"shows attribution for the event will be incomplete.",
			Fix: "1. Confirm the database is readable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the query once the database is healthy.",
			Cause: err,
		}
	}
	defer rows.Close()

	contexts := make([]protocol.Context, 0)
	for rows.Next() {
		var kind, contextId string
		if err := rows.Scan(&kind, &contextId); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("Pasture couldn't read one of the context rows for event %d.", eventId),
				Why:      "Reading the row's columns out of the result set failed.",
				Where:    "Reading the contexts for an event (internal/tasks/tracker.go in trackerImpl.EventContexts).",
				Impact: "Only some of the contexts for this event are readable; the result\n" +
					"can't be returned reliably.",
				Fix: "1. Retry the query — transient read errors usually resolve on their own.\n" +
					"2. If the error keeps happening, check the database file for damage:\n" +
					"     sqlite3 <db-path> \"PRAGMA integrity_check\"\n" +
					"3. Run a migration to bring the schema up to date if needed:\n" +
					"     pasture migrate",
				Cause: err,
			}
		}
		contexts = append(contexts, protocol.Context{
			Kind:      protocol.ContextKind(kind),
			ContextId: contextId,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture stopped partway through reading the contexts for event %d.", eventId),
			Why:      "The database stream ended with an error before all rows were read.",
			Where:    "Reading the contexts for an event (internal/tasks/tracker.go in trackerImpl.EventContexts).",
			Impact: "Only some of the contexts for this event are readable; the result\n" +
				"can't be returned reliably.",
			Fix: "1. Retry the query — transient read errors usually resolve on their own.\n" +
				"2. If the error keeps happening, the database file may be damaged. Check\n" +
				"   it for corruption (this can take a while on large files):\n" +
				"     sqlite3 <db-path> \"PRAGMA integrity_check\"",
			Cause: err,
		}
	}
	return contexts, nil
}

// Timeline returns all audit events whose context_edges row matches the
// (kind, contextId) pair, in chronological order (timestamp ASC).
//
// kind MUST be valid; contextId MUST be non-empty. An empty contextId returns
// an empty slice (no error) since the lookup is well-defined but vacuous.
//
// Timeline is the new query path that supersedes audit.Trail.QueryEvents for
// non-epoch contexts. It JOINs context_edges against audit_events on event_id.
//
// Schema-version awareness (S6 widening — owns this from S5's TODO):
//   - v1/v2 schema: audit_events still has the `role` column; agent_id is
//     absent. Project (epoch_id, phase, role, event_type, payload, timestamp).
//   - v3+ schema:   audit_events.role is dropped; agent_id is NOT NULL. Read
//     agent_id and surface it in protocol.AuditEvent.Role for one-line
//     compatibility with the existing AuditEvent shape (the dedicated
//     AgentId field will land alongside the audit_events.agent_id surface
//     work). epoch_id is still present until v4 lands.
//
// We detect the post-v3 shape via PRAGMA table_info probed once in
// newTrackerImpl and cached as hasRoleColumn / hasEpochIDColumn on the
// receiver. Timeline reads those cached flags with no per-call PRAGMA overhead.
//
// kind MUST be valid; contextId MUST be non-empty. An empty contextId returns
// an empty slice (no error) since the lookup is well-defined but vacuous.
func (t *trackerImpl) Timeline(ctx context.Context, kind protocol.ContextKind, contextId string) ([]protocol.AuditEvent, error) {
	if !kind.IsValid() {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Pasture got an unknown context kind %q when reading a timeline.", kind),
			Why: "Context kinds must be one of the names pasture knows about (such as\n" +
				"epoch, slice, or git); this one isn't on the list.",
			Where: "Reading a timeline (internal/tasks/tracker.go in trackerImpl.Timeline).",
			Impact: "The timeline can't be returned because pasture doesn't know what kind\n" +
				"of events to look for.",
			Fix: "1. Pass one of the named context kinds, for example:\n" +
				"     epoch, slice, git, skill, session\n" +
				"2. List the full set of valid kinds to find the one you meant:\n" +
				"     pasture task contexts list --kinds",
		}
	}
	if contextId == "" {
		return []protocol.AuditEvent{}, nil
	}

	if err := t.ensurePastureTablesOnce(); err != nil {
		return nil, err
	}

	// Use the column-presence flags cached at construction time (probed once
	// via PRAGMA table_info in newTrackerImpl). No per-call PRAGMA round-trips.
	hasRole := t.hasRoleColumn
	hasEpochId := t.hasEpochIDColumn

	// SELECT projection for epoch_id varies across schema versions:
	//
	//   - v1/v2:  audit_events.epoch_id is NOT NULL (legacy column).
	//   - v3:     audit_events.epoch_id is still present but role is gone.
	//   - v4:     audit_events.epoch_id is dropped; the canonical source is
	//             context_edges.context_id where context_kind='EpochContext'.
	//
	// For the EpochContext lookup specifically, the epoch_id IS the
	// `ce.context_id` value supplied as the WHERE arg — so when the column
	// is gone we substitute `ce.context_id` (still the same epochId for
	// every row matching the filter). For non-Epoch context kinds (Git,
	// Skill, Session, etc.) the epoch_id is naturally empty since those
	// events were never anchored to an epoch in the first place.
	epochProj := "COALESCE(ae.epoch_id, '')"
	if !hasEpochId {
		// Use ce.context_id when the kind is EpochContext (it IS the
		// epoch_id); for other context kinds, return empty string. SQLite
		// CASE on the bound context_kind parameter is awkward so we use a
		// CASE on the literal column compared to the canonical string.
		epochProj = "CASE WHEN ce.context_kind = 'EpochContext' THEN ce.context_id ELSE '' END"
	}

	var query string
	if hasRole {
		// Pre-v3 shape (legacy db that has not yet been migrated past v2).
		query = `SELECT ae.epoch_id, ae.phase, ae.role, ae.event_type, ae.payload, ae.timestamp
		         FROM context_edges ce
		         JOIN audit_events ae ON ae.id = ce.event_id
		         WHERE ce.context_kind = ? AND ce.context_id = ?
		         ORDER BY ae.timestamp ASC, ae.id ASC`
	} else {
		// Post-v3 shape: agent_id is the attribution column. LEFT JOIN
		// agents_software so we can repopulate event.Role from the agent
		// name (legacy-role agents carry the canonical
		// "pasture/legacy-role/<role>" prefix; live well-known agents from
		// S7 carry their own pasture/automaton/... names). decodeAuditEvent
		// strips the legacy prefix to recover the original role string,
		// preserving the existing API contract for callers.
		query = `SELECT ` + epochProj + ` AS epoch_id, COALESCE(ae.phase, '') AS phase,
		                COALESCE(asw.name, ''), ae.event_type, ae.payload, ae.timestamp
		         FROM context_edges ce
		         JOIN audit_events ae ON ae.id = ce.event_id
		         LEFT JOIN agents_software asw ON asw.agent_id = ae.agent_id
		         WHERE ce.context_kind = ? AND ce.context_id = ?
		         ORDER BY ae.timestamp ASC, ae.id ASC`
	}

	rows, err := t.auditDB.QueryContext(ctx, query, string(kind), contextId)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't read the timeline for %s %q.", contextIDLabel(kind), contextId),
			Why: fmt.Sprintf(
				"The database query asking for events linked to this %s failed.",
				contextIDLabel(kind),
			),
			Where: "Reading a timeline (internal/tasks/tracker.go in trackerImpl.Timeline).",
			Impact: fmt.Sprintf(
				"The timeline can't be returned. This %s will look empty even if there\n"+
					"really are events recorded for it.",
				contextIDLabel(kind),
			),
			Fix: "1. Confirm the database is readable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the query once the database is healthy.",
			Cause: err,
		}
	}
	defer rows.Close()

	events := make([]protocol.AuditEvent, 0)
	for rows.Next() {
		var (
			epochId, phaseStr, roleOrAgent, eventTypeStr, payloadJSON string
			tsNano                                                    int64
		)
		if err := rows.Scan(&epochId, &phaseStr, &roleOrAgent, &eventTypeStr, &payloadJSON, &tsNano); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("Pasture couldn't read one of the timeline rows for %s %q.", contextIDLabel(kind), contextId),
				Why:      "Reading the row's columns out of the result set failed.",
				Where:    "Reading a timeline (internal/tasks/tracker.go in trackerImpl.Timeline).",
				Impact: "Only some of the timeline is readable; the result can't be returned\n" +
					"reliably.",
				Fix: "1. Retry the query — transient read errors usually resolve on their own.\n" +
					"2. If the error keeps happening, check the database for damage:\n" +
					"     sqlite3 <db-path> \"PRAGMA integrity_check\"\n" +
					"3. Run a migration to bring the schema up to date if needed:\n" +
					"     pasture migrate",
				Cause: err,
			}
		}
		ev, perr := decodeAuditEvent(epochId, phaseStr, roleOrAgent, eventTypeStr, payloadJSON, tsNano)
		if perr != nil {
			return nil, perr
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture stopped partway through reading the timeline for %s %q.", contextIDLabel(kind), contextId),
			Why:      "The database stream ended with an error before all rows were read.",
			Where:    "Reading a timeline (internal/tasks/tracker.go in trackerImpl.Timeline).",
			Impact: "Only some of the timeline is readable; the result can't be returned\n" +
				"reliably.",
			Fix: "1. Retry the query — transient read errors usually resolve on their own.\n" +
				"2. If the error keeps happening, the database file may be damaged. Check\n" +
				"   it for corruption (this can take a while on large files):\n" +
				"     sqlite3 <db-path> \"PRAGMA integrity_check\"",
			Cause: err,
		}
	}
	return events, nil
}

// auditDBHandle returns the audit *sql.DB handle used by this tracker for
// pasture-only table writes (context_edges, pasture_agent_categories,
// pasture_well_known_agents). It is the unexported accessor that satisfies
// the auditDBHolder interface declared in well_known.go, allowing
// RegisterWellKnownAgents to be called by any value that exposes its audit
// DB handle — including test fakes — without a concrete *trackerImpl assertion.
func (t *trackerImpl) auditDBHandle() *sql.DB { return t.auditDB }

// auditEventsHasColumn returns true when audit_events has a column with the
// given name. Used by Timeline (and any future column-aware query) to pick
// the right SELECT projection without parsing audit_schema_meta.
//
// We probe via PRAGMA table_info instead of reading audit_schema_meta because
// the migrations that change audit_events shape may run partially across
// concurrent processes (CLI dry-run vs daemon apply); the column-presence
// check is the ground truth.
func auditEventsHasColumn(ctx context.Context, db *sql.DB, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(audit_events)`)
	if err != nil {
		return false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't inspect the layout of the audit-events table.",
			Why:      "Asking the database for the audit-events table layout failed.",
			Where:    "Probing the database schema (internal/tasks/tracker.go in tasks.auditEventsHasColumn).",
			Impact: "Pasture can't tell which schema version this database is on, so timeline\n" +
				"and event queries can't safely run — they might read the wrong columns.",
			Fix: "1. Confirm the database is readable:\n" +
				"     sqlite3 <db-path> \".schema audit_events\"\n" +
				"2. If the file is intact and you still see this error, please file a bug.",
			Cause: err,
		}
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return false, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Pasture couldn't read one of the column descriptions for the audit-events table.",
				Why:      "Reading a row out of the table-layout result set failed.",
				Where:    "Probing the database schema (internal/tasks/tracker.go in tasks.auditEventsHasColumn).",
				Impact: "Pasture can't tell which schema version this database is on, so timeline\n" +
					"and event queries can't safely run.",
				Fix: "1. Inspect the table layout by hand to see what's wrong:\n" +
					"     sqlite3 <db-path> \"PRAGMA table_info(audit_events)\"\n" +
					"2. If the file is intact and you still see this error, please file a bug.",
				Cause: err,
			}
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture stopped partway through reading the audit-events table layout.",
			Why: "The database stream ended with an error before all column descriptions\n" +
				"were read.",
			Where: "Probing the database schema (internal/tasks/tracker.go in tasks.auditEventsHasColumn).",
			Impact: "Pasture can't tell which schema version this database is on, so timeline\n" +
				"and event queries can't safely run.",
			Fix: "1. Confirm nothing else is rewriting the database while you read it:\n" +
				"     pgrep -af pastured\n" +
				"     pgrep -af 'pasture migrate'\n" +
				"2. Retry once any other writer has finished.",
			Cause: err,
		}
	}
	return false, nil
}

// ─── Lifecycle ───────────────────────────────────────────────────────────────

// Close closes both wrapped subsystems exactly once. Safe for concurrent
// callers and idempotent (a second call returns the cached result of the
// first). The audit *sql.DB is the same handle held by trail (when trail is a
// *audit.SqliteAuditTrail), so closing trail releases auditDB; we do not call
// auditDB.Close() separately to avoid a double-close panic in modernc/sqlite.
func (t *trackerImpl) Close() error {
	t.closeOnce.Do(func() {
		// Close the Provenance tracker first; it owns its own *sql.DB
		// (separate from auditDB even though both point at the same file).
		var provErr error
		if t.prov != nil {
			provErr = t.prov.Close()
		}

		// Close the audit subsystem next. SqliteAuditTrail.Close() releases
		// auditDB; for non-SQLite trails (e.g. InMemoryAuditTrail) Close is
		// a no-op or method-missing and we skip it.
		var trailErr error
		if closer, ok := t.trail.(interface{ Close() error }); ok {
			trailErr = closer.Close()
		}

		switch {
		case provErr != nil && trailErr != nil:
			t.closeErr = &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Pasture couldn't close the database cleanly.",
				Why: "Both halves of the database (the task store and the audit log) failed\n" +
					"to close.",
				Where: "Closing the database (internal/tasks/tracker.go in trackerImpl.Close).",
				Impact: "The database file may be left locked. The next process that tries to\n" +
					"open it may have to wait or see a \"database is locked\" error briefly.",
				Fix: "1. Wait about 5 seconds for the lock to clear, then retry.\n" +
					"2. If the error keeps happening, restart any process still holding the\n" +
					"   file open:\n" +
					"     pgrep -af pastured\n" +
					"     pkill -f pastured",
				Cause: errors.Join(provErr, trailErr),
			}
		case provErr != nil:
			t.closeErr = &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Pasture couldn't close the task store cleanly.",
				Why:      "Closing the task-store half of the database failed.",
				Where:    "Closing the database (internal/tasks/tracker.go in trackerImpl.Close).",
				Impact: "The task-store connection may be left open. The next process that tries\n" +
					"to open the database may have to wait or see a \"database is locked\"\n" +
					"error briefly.",
				Fix: "1. Wait about 5 seconds for the lock to clear, then retry.\n" +
					"2. If the error keeps happening, restart any process still holding the\n" +
					"   file open:\n" +
					"     pgrep -af pastured\n" +
					"     pkill -f pastured",
				Cause: provErr,
			}
		case trailErr != nil:
			t.closeErr = &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Pasture couldn't close the audit log cleanly.",
				Why:      "Closing the audit-log half of the database failed.",
				Where:    "Closing the database (internal/tasks/tracker.go in trackerImpl.Close).",
				Impact: "The audit-log connection may be left open. The next process that tries\n" +
					"to open the database may have to wait or see a \"database is locked\"\n" +
					"error briefly.",
				Fix: "1. Wait about 5 seconds for the lock to clear, then retry.\n" +
					"2. If the error keeps happening, restart any process still holding the\n" +
					"   file open:\n" +
					"     pgrep -af pastured\n" +
					"     pkill -f pastured",
				Cause: trailErr,
			}
		}
	})
	return t.closeErr
}

// Compile-time check that *trackerImpl satisfies protocol.TaskTracker. If the
// upstream provenance.Tracker grows a new method, this check fails until we
// add the corresponding forwarder above (or the codebase intentionally drops
// the upstream API).
var _ protocol.TaskTracker = (*trackerImpl)(nil)
