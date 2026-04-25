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
}

// newTrackerImpl wires up a trackerImpl. The caller (OpenTaskTracker) is
// responsible for opening prov, trail, and auditDB against the same dbPath
// and for running audit.Migrate before this constructor is invoked. This
// helper is package-private so it can also be used by tests with mocked
// dependencies.
//
// auditDB MUST be the same *sql.DB handle used by trail; the race test relies
// on single-writer serialisation through this one handle.
func newTrackerImpl(prov provenance.Tracker, trail audit.Trail, auditDB *sql.DB) *trackerImpl {
	return &trackerImpl{
		prov:    prov,
		trail:   trail,
		auditDB: auditDB,
	}
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

	if err := ensurePastureTables(t.auditDB); err != nil {
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
	if err := ensurePastureTables(t.auditDB); err != nil {
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

	if err := ensurePastureTables(t.auditDB); err != nil {
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
	if err := ensurePastureTables(t.auditDB); err != nil {
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
// non-epoch contexts. The columns selected match protocol.AuditEvent;
// audit_events.role and audit_events.epoch_id are NOT projected here because
// (a) v3+ drops audit_events.role in favor of agent_id, and (b) v4+ drops
// audit_events.epoch_id in favor of context_edges (PROPOSAL-2 §7.2).
//
// Until S3 (v3 backfill) lands the audit_events table still has role and
// epoch_id columns; this Timeline query reads only the columns that survive
// the v3+ migrations so it works against legacy v1/v2 databases too.
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

	if err := ensurePastureTables(t.auditDB); err != nil {
		return nil, err
	}

	// JOIN context_edges → audit_events. We project the v1-stable columns
	// (epoch_id, phase, role, event_type, payload, timestamp) so this query
	// works on legacy databases that have not yet been migrated past v2.
	// Once S3 + S4 land the SELECT clause will widen to include agent_id
	// and drop epoch_id / role; that change is owned by S6 (CLI consumer),
	// not S5 (interface + impl).
	rows, err := t.auditDB.QueryContext(ctx,
		`SELECT ae.epoch_id, ae.phase, ae.role, ae.event_type, ae.payload, ae.timestamp
		 FROM context_edges ce
		 JOIN audit_events ae ON ae.id = ce.event_id
		 WHERE ce.context_kind = ? AND ce.context_id = ?
		 ORDER BY ae.timestamp ASC, ae.id ASC`,
		string(kind), contextID,
	)
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
			epochID, phaseStr, role, eventTypeStr, payloadJSON string
			tsNano                                             int64
		)
		if err := rows.Scan(&epochID, &phaseStr, &role, &eventTypeStr, &payloadJSON, &tsNano); err != nil {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("tasks.Timeline: row scan failed for kind=%s context=%q", kind, contextID),
				Why:      err.Error(),
				Impact:   "partial result; the timeline cannot be returned reliably",
				Fix:      "re-run the query; if the error persists, inspect the audit_events row layout via 'sqlite3 <db> .schema audit_events'",
			}
		}
		ev, perr := decodeAuditEvent(epochID, phaseStr, role, eventTypeStr, payloadJSON, tsNano)
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
