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
		if hasEpochID, err := auditEventsHasColumn(ctx, auditDB, "epoch_id"); err == nil {
			t.hasEpochIDColumn = hasEpochID
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

// Task CRUD.

func (t *trackerImpl) Create(namespace, title, description string, taskType provenance.TaskType, priority provenance.Priority, phase provenance.Phase) (provenance.Task, error) {
	return t.prov.Create(namespace, title, description, taskType, priority, phase)
}
func (t *trackerImpl) Show(id provenance.TaskID) (provenance.Task, error) {
	return t.prov.Show(id)
}
func (t *trackerImpl) Update(id provenance.TaskID, fields provenance.UpdateFields) (provenance.Task, error) {
	return t.prov.Update(id, fields)
}
func (t *trackerImpl) CloseTask(id provenance.TaskID, reason string) (provenance.Task, error) {
	return t.prov.CloseTask(id, reason)
}
func (t *trackerImpl) List(filter provenance.ListFilter) ([]provenance.Task, error) {
	return t.prov.List(filter)
}

// Edges.

func (t *trackerImpl) AddEdge(sourceID provenance.TaskID, targetID string, kind provenance.EdgeKind) error {
	return t.prov.AddEdge(sourceID, targetID, kind)
}
func (t *trackerImpl) RemoveEdge(sourceID provenance.TaskID, targetID string, kind provenance.EdgeKind) error {
	return t.prov.RemoveEdge(sourceID, targetID, kind)
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
	return t.prov.AddLabel(id, label)
}
func (t *trackerImpl) RemoveLabel(id provenance.TaskID, label string) error {
	return t.prov.RemoveLabel(id, label)
}
func (t *trackerImpl) Labels(id provenance.TaskID) ([]string, error) { return t.prov.Labels(id) }

// Comments.

func (t *trackerImpl) AddComment(id provenance.TaskID, authorID provenance.AgentID, body string) (provenance.Comment, error) {
	return t.prov.AddComment(id, authorID, body)
}
func (t *trackerImpl) Comments(id provenance.TaskID) ([]provenance.Comment, error) {
	return t.prov.Comments(id)
}

// Agents.

func (t *trackerImpl) RegisterHumanAgent(namespace, name, contact string) (provenance.HumanAgent, error) {
	return t.prov.RegisterHumanAgent(namespace, name, contact)
}
func (t *trackerImpl) RegisterMLAgent(namespace string, role provenance.Role, provider provenance.Provider, modelName provenance.ModelID) (provenance.MLAgent, error) {
	return t.prov.RegisterMLAgent(namespace, role, provider, modelName)
}
func (t *trackerImpl) RegisterSoftwareAgent(namespace, name, version, source string) (provenance.SoftwareAgent, error) {
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

func (t *trackerImpl) StartActivity(agentID provenance.AgentID, phase provenance.Phase, stage provenance.Stage, notes string) (provenance.Activity, error) {
	return t.prov.StartActivity(agentID, phase, stage, notes)
}
func (t *trackerImpl) EndActivity(id provenance.ActivityID) (provenance.Activity, error) {
	return t.prov.EndActivity(id)
}
func (t *trackerImpl) Activities(agentID *provenance.AgentID) ([]provenance.Activity, error) {
	return t.prov.Activities(agentID)
}

// ─── Audit Trail surface (4 methods, signature-identical to audit.Trail) ─────

func (t *trackerImpl) RecordEvent(ctx context.Context, event protocol.AuditEvent) error {
	return t.trail.RecordEvent(ctx, event)
}

// RecordEventReturningID forwards to the wrapped audit.Trail's
// RecordEventReturningID and returns the just-inserted audit_events.id.
//
// Race safety: the underlying audit.Trail recovers the id from
// sql.Result.LastInsertId on the SAME INSERT statement that wrote the row
// (per-statement, not per-connection — see audit/sqlite.go's
// SqliteAuditTrail.RecordEventReturningID). This is race-free under any level
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
func (t *trackerImpl) RecordEventReturningID(ctx context.Context, event protocol.AuditEvent) (int64, error) {
	return t.trail.RecordEventReturningID(ctx, event)
}
func (t *trackerImpl) QueryEvents(ctx context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	return t.trail.QueryEvents(ctx, epochID, phase, role)
}
func (t *trackerImpl) RecordSessionEntries(ctx context.Context, entries []protocol.SessionEntry) error {
	return t.trail.RecordSessionEntries(ctx, entries)
}
func (t *trackerImpl) QuerySessionEntries(ctx context.Context, sessionID string) ([]protocol.SessionEntry, error) {
	return t.trail.QuerySessionEntries(ctx, sessionID)
}

// ─── Pasture-side category decoration (R8) ───────────────────────────────────

// SetAgentCategories upserts the (automaton, pasture) pair for id into
// pasture_agent_categories. Uses INSERT OR REPLACE; idempotent.
//
// Validation: both enums must be valid IsValid() members. Empty / unknown
// values produce CategoryValidation. Storage failures produce CategoryStorage.
func (t *trackerImpl) SetAgentCategories(id provenance.AgentID, automaton protocol.AutomatonRole, pastureRole protocol.PastureRole) error {
	if !automaton.IsValid() {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.SetAgentCategories: invalid AutomatonRole %q", automaton),
			Why:      "the AutomatonRole value is not a member of protocol.AllAutomatonRoles",
			Impact:   "the agent category cannot be stored; downstream JOINs against pasture_agent_categories would resolve to an unknown role",
			Fix:      "pass one of protocol.AllAutomatonRoles (e.g. AutomatonRoleNone, AutomatonRoleConstraintChecker, AutomatonRoleHookHandler)",
		}
	}
	if !pastureRole.IsValid() {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.SetAgentCategories: invalid PastureRole %q", pastureRole),
			Why:      "the PastureRole value is not a member of protocol.AllPastureRoles",
			Impact:   "the agent category cannot be stored; downstream JOINs against pasture_agent_categories would resolve to an unknown role",
			Fix:      "pass one of protocol.AllPastureRoles (e.g. PastureRoleNone, PastureRoleArchitect, PastureRoleWorker)",
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
			What:     fmt.Sprintf("tasks.SetAgentCategories: write to pasture_agent_categories failed for agent %q", id.String()),
			Why:      err.Error(),
			Impact:   "the agent's pasture-side category is not persisted; subsequent JOINs will return the default ('None','None')",
			Fix:      "verify the SQLite file is writable and the schema is at v3 or higher (run 'pasture migrate' if you suspect schema drift)",
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
			What:     fmt.Sprintf("tasks.AgentCategories: read from pasture_agent_categories failed for agent %q", id.String()),
			Why:      err.Error(),
			Impact:   "the agent's pasture-side category cannot be looked up; downstream attribution checks will fall back to the default",
			Fix:      "verify the SQLite file is readable and the schema is at v3 or higher (run 'pasture migrate' if you suspect schema drift)",
		}
	}
	return protocol.AutomatonRole(automatonStr), protocol.PastureRole(pastureRoleStr), nil
}

// ─── Context attachment (R9) ─────────────────────────────────────────────────

// AttachContext writes (eventID, kind, contextID) into context_edges.
//
// The (event_id, context_kind, context_id) triple is the BCNF composite
// primary key — duplicate inserts are converted to no-ops via INSERT OR
// IGNORE so the call is idempotent (repeated calls return nil and leave the
// existing row untouched).
//
// kind MUST be valid; contextID MUST be non-empty (a zero-length context_id
// is meaningless and would silently break Timeline lookups).
func (t *trackerImpl) AttachContext(ctx context.Context, eventID int64, kind protocol.ContextKind, contextID string) error {
	if !kind.IsValid() {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.AttachContext: invalid ContextKind %q", kind),
			Why:      "the ContextKind value is not a member of protocol.AllContextKinds",
			Impact:   "the event-context edge cannot be stored; the event would be invisible to Timeline lookups for this kind",
			Fix:      "pass one of protocol.AllContextKinds (e.g. ContextEpoch, ContextSlice, ContextGit)",
		}
	}
	if contextID == "" {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "tasks.AttachContext: contextID is empty",
			Why:      "AttachContext was called with an empty context_id, which would create a row that no Timeline lookup can match",
			Impact:   "the event-context edge cannot be stored; the event would be unreachable via Timeline",
			Fix:      "pass the canonical id for the kind (e.g. for ContextEpoch: the originating REQUEST TaskID's String(); for ContextGit: the git commit SHA)",
		}
	}
	if eventID <= 0 {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.AttachContext: eventID %d is not positive", eventID),
			Why:      "audit_events.id is AUTOINCREMENT and starts at 1; a zero or negative eventID indicates a programming error",
			Impact:   "the event-context edge cannot be stored",
			Fix:      "pass the int64 returned by the audit store after RecordEvent (this is currently surfaced only via lastInsertRowID — see the audit-side enhancement note in PROPOSAL-2 §7.11)",
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
		eventID, string(kind), contextID,
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.AttachContext: write to context_edges failed for event %d kind=%s context=%q", eventID, kind, contextID),
			Why:      err.Error(),
			Impact:   "the event-context edge is not persisted; the event will be invisible to Timeline lookups for this (kind, context_id)",
			Fix:      "verify the SQLite file is writable and the schema is at v3 or higher (run 'pasture migrate' if you suspect schema drift)",
		}
	}
	return nil
}

// EventContexts returns all (Kind, ContextID) edges attached to eventID, in
// insertion order (rowid ASC). Returns an empty (non-nil) slice when no
// edges exist for eventID.
func (t *trackerImpl) EventContexts(ctx context.Context, eventID int64) ([]protocol.Context, error) {
	if err := t.ensurePastureTablesOnce(); err != nil {
		return nil, err
	}

	rows, err := t.auditDB.QueryContext(ctx,
		`SELECT context_kind, context_id
		 FROM context_edges
		 WHERE event_id = ?
		 ORDER BY rowid ASC`,
		eventID,
	)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.EventContexts: query failed for event %d", eventID),
			Why:      err.Error(),
			Impact:   "the contexts attached to this event cannot be enumerated; downstream attribution displays will be incomplete",
			Fix:      "verify the SQLite file is readable and the schema is at v3 or higher (run 'pasture migrate' if you suspect schema drift)",
		}
	}
	defer rows.Close()

	contexts := make([]protocol.Context, 0)
	for rows.Next() {
		var kind, contextID string
		if err := rows.Scan(&kind, &contextID); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("tasks.EventContexts: row scan failed for event %d", eventID),
				Why:      err.Error(),
				Impact:   "partial result; the context list cannot be returned reliably",
				Fix:      "re-run the query; if the error persists, inspect the context_edges row layout via 'sqlite3 <db> .schema context_edges'",
			}
		}
		contexts = append(contexts, protocol.Context{
			Kind:      protocol.ContextKind(kind),
			ContextID: contextID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.EventContexts: row iteration failed for event %d", eventID),
			Why:      err.Error(),
			Impact:   "partial result; the context list cannot be returned reliably",
			Fix:      "re-run the query; if the error persists, the SQLite file may be corrupt — check 'PRAGMA integrity_check'",
		}
	}
	return contexts, nil
}

// Timeline returns all audit events whose context_edges row matches the
// (kind, contextID) pair, in chronological order (timestamp ASC).
//
// kind MUST be valid; contextID MUST be non-empty. An empty contextID returns
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
//     AgentID field will land alongside the audit_events.agent_id surface
//     work). epoch_id is still present until v4 lands.
//
// We detect the post-v3 shape via PRAGMA table_info probed once in
// newTrackerImpl and cached as hasRoleColumn / hasEpochIDColumn on the
// receiver. Timeline reads those cached flags with no per-call PRAGMA overhead.
//
// kind MUST be valid; contextID MUST be non-empty. An empty contextID returns
// an empty slice (no error) since the lookup is well-defined but vacuous.
func (t *trackerImpl) Timeline(ctx context.Context, kind protocol.ContextKind, contextID string) ([]protocol.AuditEvent, error) {
	if !kind.IsValid() {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("tasks.Timeline: invalid ContextKind %q", kind),
			Why:      "the ContextKind value is not a member of protocol.AllContextKinds",
			Impact:   "the timeline query cannot be executed",
			Fix:      "pass one of protocol.AllContextKinds (e.g. ContextEpoch, ContextSlice, ContextGit)",
		}
	}
	if contextID == "" {
		return []protocol.AuditEvent{}, nil
	}

	if err := t.ensurePastureTablesOnce(); err != nil {
		return nil, err
	}

	// Use the column-presence flags cached at construction time (probed once
	// via PRAGMA table_info in newTrackerImpl). No per-call PRAGMA round-trips.
	hasRole := t.hasRoleColumn
	hasEpochID := t.hasEpochIDColumn

	// SELECT projection for epoch_id varies across schema versions:
	//
	//   - v1/v2:  audit_events.epoch_id is NOT NULL (legacy column).
	//   - v3:     audit_events.epoch_id is still present but role is gone.
	//   - v4:     audit_events.epoch_id is dropped; the canonical source is
	//             context_edges.context_id where context_kind='EpochContext'.
	//
	// For the EpochContext lookup specifically, the epoch_id IS the
	// `ce.context_id` value supplied as the WHERE arg — so when the column
	// is gone we substitute `ce.context_id` (still the same epochID for
	// every row matching the filter). For non-Epoch context kinds (Git,
	// Skill, Session, etc.) the epoch_id is naturally empty since those
	// events were never anchored to an epoch in the first place.
	epochProj := "COALESCE(ae.epoch_id, '')"
	if !hasEpochID {
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

	rows, err := t.auditDB.QueryContext(ctx, query, string(kind), contextID)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.Timeline: query failed for kind=%s context=%q", kind, contextID),
			Why:      err.Error(),
			Impact:   "the timeline cannot be returned; this context appears empty even if events exist",
			Fix:      "verify the SQLite file is readable and the schema is at v3 or higher (run 'pasture migrate' if you suspect schema drift)",
		}
	}
	defer rows.Close()

	events := make([]protocol.AuditEvent, 0)
	for rows.Next() {
		var (
			epochID, phaseStr, roleOrAgent, eventTypeStr, payloadJSON string
			tsNano                                                    int64
		)
		if err := rows.Scan(&epochID, &phaseStr, &roleOrAgent, &eventTypeStr, &payloadJSON, &tsNano); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("tasks.Timeline: row scan failed for kind=%s context=%q", kind, contextID),
				Why:      err.Error(),
				Impact:   "partial result; the timeline cannot be returned reliably",
				Fix:      "re-run the query; if the error persists, inspect the audit_events row layout via 'sqlite3 <db> .schema audit_events'",
			}
		}
		ev, perr := decodeAuditEvent(epochID, phaseStr, roleOrAgent, eventTypeStr, payloadJSON, tsNano)
		if perr != nil {
			return nil, perr
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("tasks.Timeline: row iteration failed for kind=%s context=%q", kind, contextID),
			Why:      err.Error(),
			Impact:   "partial result; the timeline cannot be returned reliably",
			Fix:      "re-run the query; if the error persists, the SQLite file may be corrupt — check 'PRAGMA integrity_check'",
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
			What:     "tasks.auditEventsHasColumn: PRAGMA table_info(audit_events) failed",
			Why:      err.Error(),
			Impact:   "the schema-aware query path cannot decide whether the legacy `role` column is present; downstream Timeline / events queries cannot proceed safely",
			Fix:      "verify the SQLite file is readable; if the file is intact, this is unexpected — file an issue against pasture/internal/tasks",
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
				What:     "tasks.auditEventsHasColumn: row scan failed for PRAGMA table_info(audit_events)",
				Why:      err.Error(),
				Impact:   "the schema-aware query path cannot proceed",
				Fix:      "verify the SQLite file is not corrupt; run 'sqlite3 <db> \"PRAGMA table_info(audit_events)\"' to inspect manually",
			}
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "tasks.auditEventsHasColumn: row iteration failed for PRAGMA table_info(audit_events)",
			Why:      err.Error(),
			Impact:   "the schema-aware query path cannot proceed",
			Fix:      "verify the SQLite file is readable and not concurrently being rewritten",
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
				What:     "tasks.trackerImpl.Close: both subsystems failed to close cleanly",
				Why:      fmt.Sprintf("provenance.Close: %v; audit.Close: %v", provErr, trailErr),
				Impact:   "the database file may be left with stale locks; further opens may transiently fail with SQLITE_BUSY",
				Fix:      "wait for the busy timeout (5s) and retry; if the error persists, restart the process holding the file",
			}
		case provErr != nil:
			t.closeErr = &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "tasks.trackerImpl.Close: provenance subsystem failed to close",
				Why:      provErr.Error(),
				Impact:   "Provenance's connection to the database is not released cleanly",
				Fix:      "wait for the busy timeout (5s) and retry; if the error persists, restart the process holding the file",
			}
		case trailErr != nil:
			t.closeErr = &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "tasks.trackerImpl.Close: audit subsystem failed to close",
				Why:      trailErr.Error(),
				Impact:   "the audit *sql.DB connection is not released cleanly",
				Fix:      "wait for the busy timeout (5s) and retry; if the error persists, restart the process holding the file",
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
