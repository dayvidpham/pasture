// Package audit — migrate_v2_v3.go
//
// The v2→v3 migration adds three new tables that underpin the unified
// pasture workflow record (PROPOSAL-2 §7.2):
//
//   - context_edges            — many-to-many event⇄context attachment, BCNF
//     (composite-key, no non-key columns; §7.8).
//   - pasture_agent_categories — typed categorisation rows for Provenance
//     agents (R8); written by SetAgentCategories.
//   - pasture_well_known_agents — stable logical-name → AgentId mapping for
//     idempotent automaton registration at daemon
//     startup (BLOCKER A2). UAT-1 schema invariant:
//     (agent_id PK, name UNIQUE).
//
// IMPORTANT scope boundary: this slice (S2) does NOT touch existing
// audit_events rows. The audit_events.agent_id column add + role-backfill +
// role-drop triple lives in S3 (migrate_v3 backfill) per BLOCKER A1, which
// requires the entire (create-column → backfill → drop-role) sequence to run
// in one BEGIN IMMEDIATE transaction. Workers integrating with this file
// downstream must not insert any audit_events DDL into migrateV2toV3.
//
// Layer Integration Points exposed by this slice:
//
//   - pasture_well_known_agents — DDL here; rows written by S7 (startup
//     registration); cached AgentIDs read by S8 (activity attribution).
//   - context_edges — DDL here; consumed by S5 (AttachContext writes), S6
//     (`pasture task contexts` reads), S8 (epoch attachment), S9
//     (free-floating contexts).
//   - pasture_agent_categories — DDL here; written by S5 SetAgentCategories;
//     read by S6 (`pasture task agents` listing).
package audit

import (
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// contextEdgesDDL creates the context_edges table.
//
// BCNF rationale (PROPOSAL-2 §7.8): the only non-trivial functional dependency
// is (event_id, context_kind, context_id) → ∅. There are no partial or
// transitive dependencies because there are no non-key columns. The table is
// in 6NF, which implies BCNF. Workers extending this file MUST NOT add
// non-key columns to context_edges — doing so breaks the invariant asserted
// by §11 Scenarios 6 and 7 and the BCNF inspection test.
//
// ON DELETE CASCADE on the event_id FK ensures that deleting an audit_events
// row also removes its context attachments, so context_edges never refers to
// a non-existent event.
const contextEdgesDDL = `
CREATE TABLE IF NOT EXISTS context_edges (
	event_id     INTEGER NOT NULL REFERENCES audit_events(id) ON DELETE CASCADE,
	context_kind TEXT NOT NULL,
	context_id   TEXT NOT NULL,
	PRIMARY KEY (event_id, context_kind, context_id)
)
`

// pastureAgentCategoriesDDL creates the typed categorisation table for
// Provenance agents (R8). One row per registered SoftwareAgent that needs
// pasture-side typed categorisation; inserted by
// protocol.TaskTracker.SetAgentCategories immediately after
// RegisterSoftwareAgent.
//
// agent_id is a soft reference to provenance.agents.id (no FK constraint —
// Provenance owns its tables and is unmodified per C4). Application code is
// the integrity layer.
const pastureAgentCategoriesDDL = `
CREATE TABLE IF NOT EXISTS pasture_agent_categories (
	agent_id        TEXT PRIMARY KEY,
	automaton_role  TEXT NOT NULL DEFAULT 'None',
	pasture_role    TEXT NOT NULL DEFAULT 'None'
)
`

// pastureWellKnownAgentsDDL creates the stable logical-name → AgentId
// mapping consulted by ensureWellKnownAgent at daemon startup (BLOCKER A2).
//
// UAT-1 schema invariant (PROPOSAL-2 §7.2 + §7.7.1): agent_id is PK; name is
// UNIQUE. This keeps the canonical-identity column (agent_id) consistent
// across pasture-side tables (pasture_agent_categories, agents_software).
// The UNIQUE constraint on `name` is the idempotency anchor — lookup-by-name
// is O(1) via the unique index. Workers MUST NOT invert these.
const pastureWellKnownAgentsDDL = `
CREATE TABLE IF NOT EXISTS pasture_well_known_agents (
	agent_id  TEXT PRIMARY KEY,
	name      TEXT NOT NULL UNIQUE
)
`

// contextEdgesLookupIndexDDL accelerates the most common query shape:
// "show me all events tied to <context_kind, context_id>" (e.g. an epoch).
//
// Per PROPOSAL-2 §7.2.
const contextEdgesLookupIndexDDL = `
CREATE INDEX IF NOT EXISTS idx_context_edges_lookup
ON context_edges (context_kind, context_id)
`

// contextEdgesEventIndexDDL accelerates the inverse query: "show me all
// contexts attached to event Y". Per PROPOSAL-2 §7.2.
//
// Note: SQLite already creates a per-PK btree, but the leading column of the
// composite PK is event_id, so this index is technically redundant for
// equality lookups by event_id alone. It is included verbatim per the
// proposal spec to make intent explicit and to insulate against future
// reorderings of the composite PK.
const contextEdgesEventIndexDDL = `
CREATE INDEX IF NOT EXISTS idx_context_edges_event
ON context_edges (event_id)
`

// migrateV2toV3 advances the audit database from schema version 2 to
// version 3 in a single BEGIN IMMEDIATE transaction (held by the caller,
// see migrate.runStep). The full v2→v3 transition does TWO bodies of work:
//
//  1. (S2 — landed at 7bee59e) Create context_edges,
//     pasture_agent_categories, pasture_well_known_agents (plus the two
//     context_edges indexes).
//  2. (S3 — this slice) Add audit_events.agent_id, backfill from the
//     legacy role column, then table-rebuild to drop role. See
//     migrate_v3_backfill.go for the per-step implementations.
//
// Both bodies share the same transaction. The audit_schema_meta version
// bump from 2 to 3 is the LAST statement before the caller's commit, per
// PROPOSAL-2 §7.10.2 BLOCKER A1: a crash mid-way leaves the file at v2 and
// a retry runs the full v2→v3 fresh (the find branch in
// findOrCreateLegacyRoleAgent reuses any orphan agents from a prior
// rolled-back run).
//
// Each statement in body (1) is wrapped with IF NOT EXISTS so a partial
// run that crashed between body (1) and body (2) re-creates the tables as
// no-ops on retry. Body (2)'s addAgentIDColumn is also tolerant of an
// already-added column (idempotent). The table-rebuild in body (2) is NOT
// individually idempotent but is safe under the whole-step rollback
// guarantee.
//
// The transaction (tx) MUST already hold the SQLite write lock (BEGIN
// IMMEDIATE in production paths). Caller commits.
func migrateV2toV3(tx *sql.Tx, nowUnixNano int64) error {
	// Body (1): new tables + indexes (S2 scope).
	steps := []struct {
		what string
		ddl  string
	}{
		{"create table context_edges", contextEdgesDDL},
		{"create table pasture_agent_categories", pastureAgentCategoriesDDL},
		{"create table pasture_well_known_agents", pastureWellKnownAgentsDDL},
		{"create index idx_context_edges_lookup", contextEdgesLookupIndexDDL},
		{"create index idx_context_edges_event", contextEdgesEventIndexDDL},
	}

	for _, step := range steps {
		if _, err := tx.Exec(step.ddl); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't %s during the audit-database upgrade from version 2 to 3.",
					step.what,
				),
				Why:   "The database refused the CREATE statement.",
				Where: "Upgrading the audit database from version 2 to 3 (internal/audit/migrate_v2_v3.go in audit.migrateV2toV3).",
				Impact: "The version 2 → 3 upgrade can't complete, so the audit database stays at version 2.\n" +
					"The new tables that link audit events to tasks, agents, and contexts can't be created\n" +
					"until this is fixed. No data was changed; the entire upgrade was rolled back.",
				Fix: "1. Confirm the audit database file is writable and the disk has free space:\n" +
					"     ls -l <path-to-audit.db>\n" +
					"     df -h <path-to-audit.db>\n" +
					"2. Re-run the migration once the underlying problem is resolved:\n" +
					"     pasture migrate",
				Cause: err,
			}
		}
	}

	// Body (2): audit_events.agent_id add + role backfill + role drop
	// (S3 scope; impl in migrate_v3_backfill.go).
	if err := migrateV3Backfill(tx, nowUnixNano); err != nil {
		// migrateV3Backfill already returns a *StructuredError.
		return err
	}

	// Final statement before commit: bump audit_schema_meta to 3. A crash
	// here, or any earlier point in this function, rolls back the entire
	// transaction so the file remains observably at v2 (PROPOSAL-2
	// §7.10.2 paragraph 4 + §11 Scenario 11).
	if err := writeVersion(tx, 3, nowUnixNano); err != nil {
		// writeVersion already returns a *StructuredError with full context.
		return err
	}
	return nil
}
