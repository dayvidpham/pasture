// Package audit_test — migrate_v2_v3_test.go
//
// File-backed tests for the v2→v3 migration (S2). Coverage:
//
//   - Table-existence assertions for all three new tables (context_edges,
//     pasture_agent_categories, pasture_well_known_agents).
//   - Index-existence assertions for both context_edges indexes.
//   - BCNF inspection: context_edges has exactly 3 columns, all part of the
//     composite primary key, no non-key columns.
//   - UAT-1 schema invariant: pasture_well_known_agents columns are
//     (agent_id PK, name UNIQUE) — NOT inverted.
//   - Old audit_events rows survive the migration verbatim (no destructive
//     changes in this slice; the audit_events column changes are in S3).
//   - New tables are insertable via raw SQL; FK + UNIQUE constraints are
//     enforced.
//   - Idempotent re-run: a second Migrate on a v3 DB is a no-op.
//   - Version bookkeeping: post-Migrate, MAX(version) = 3.
//
// All tests are file-backed via t.TempDir() per pasture/CLAUDE.md and
// IMPL_PLAN §1.2 (in-memory SQLite would bypass WAL/busy_timeout/fsync).
package audit_test

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"

	_ "modernc.org/sqlite"
)

// ---- Test helpers ----------------------------------------------------------

// seedLegacyV1DB hand-builds a pre-PROPOSAL-2 audit database (audit_events
// table only, no audit_schema_meta) and inserts one row so we can verify the
// row survives the v1→v2→v3 migration unchanged. Returns the row's auto-
// generated id so downstream tests can reference it from context_edges.
func seedLegacyV1DB(t *testing.T, dbPath string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open seed: %v", err)
	}
	defer db.Close()

	mustExec(t, db, `
		CREATE TABLE audit_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			epoch_id   TEXT    NOT NULL,
			phase      TEXT    NOT NULL,
			role       TEXT    NOT NULL,
			event_type TEXT    NOT NULL,
			payload    TEXT    NOT NULL,
			timestamp  INTEGER NOT NULL
		)`)
	res, err := db.Exec(
		`INSERT INTO audit_events (epoch_id, phase, role, event_type, payload, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"epoch-pre-s2", "p1-request", "supervisor",
		"PhaseTransition", `{"note":"survives v2→v3"}`, time.Now().UnixNano(),
	)
	if err != nil {
		t.Fatalf("seed audit_events insert: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed LastInsertId: %v", err)
	}
	return id
}

// openDB opens the file-backed SQLite DB and returns it; closes on cleanup.
func openDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// tableExists reports whether the named table is present in sqlite_master.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&got)
	switch {
	case err == sql.ErrNoRows:
		return false
	case err != nil:
		t.Fatalf("tableExists(%q): %v", name, err)
	}
	return got == name
}

// indexExists reports whether the named index is present in sqlite_master.
func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, name,
	).Scan(&got)
	switch {
	case err == sql.ErrNoRows:
		return false
	case err != nil:
		t.Fatalf("indexExists(%q): %v", name, err)
	}
	return got == name
}

// columnInfo describes one row of PRAGMA table_info output.
type columnInfo struct {
	cid     int
	name    string
	colType string
	notnull int
	pk      int // PK column rank, 0 if not part of PK
}

// tableInfo returns PRAGMA table_info for the named table, sorted by cid.
func tableInfo(t *testing.T, db *sql.DB, table string) []columnInfo {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()

	var out []columnInfo
	for rows.Next() {
		var ci columnInfo
		var dflt sql.NullString
		if err := rows.Scan(&ci.cid, &ci.name, &ci.colType, &ci.notnull, &dflt, &ci.pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		out = append(out, ci)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].cid < out[j].cid })
	return out
}

// ---- Table-existence assertions --------------------------------------------

// TestMigrateV2toV3_TablesExist verifies that running Migrate against a
// freshly-seeded legacy v1 database creates all three new tables required by
// PROPOSAL-2 §7.2.
func TestMigrateV2toV3_TablesExist(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_tables.db")
	_ = seedLegacyV1DB(t, dbPath)

	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	for _, name := range []string{
		"context_edges",
		"pasture_agent_categories",
		"pasture_well_known_agents",
	} {
		if !tableExists(t, db, name) {
			t.Errorf("table %q absent after Migrate; want present", name)
		}
	}
}

// TestMigrateV2toV3_IndexesExist verifies the two context_edges helper
// indexes are present per §7.2.
func TestMigrateV2toV3_IndexesExist(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_indexes.db")
	_ = seedLegacyV1DB(t, dbPath)

	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	for _, idx := range []string{
		"idx_context_edges_lookup",
		"idx_context_edges_event",
	} {
		if !indexExists(t, db, idx) {
			t.Errorf("index %q absent after Migrate; want present", idx)
		}
	}

	// Cross-check via sqlite_master.tbl_name to make sure the indexes are
	// attached to context_edges (not, e.g., audit_events by mistake).
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='context_edges'
		 ORDER BY name`,
	)
	if err != nil {
		t.Fatalf("sqlite_master probe: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	// Both expected indexes must appear; SQLite may also create implicit
	// indexes for the composite PK (sqlite_autoindex_*), so assert presence
	// rather than exact equality.
	for _, want := range []string{"idx_context_edges_event", "idx_context_edges_lookup"} {
		found := false
		for _, name := range got {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("index %q not attached to context_edges; got indexes=%v", want, got)
		}
	}
}

// ---- BCNF inspection -------------------------------------------------------

// TestMigrateV2toV3_ContextEdges_BCNF asserts the §7.8 BCNF invariant:
// context_edges has exactly 3 columns, all part of the composite primary
// key, no non-key columns. This test is the regression guard against
// downstream slices accidentally adding a payload column or similar.
func TestMigrateV2toV3_ContextEdges_BCNF(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_bcnf.db")
	_ = seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	cols := tableInfo(t, db, "context_edges")
	if len(cols) != 3 {
		t.Fatalf("context_edges has %d columns, want exactly 3 (BCNF: all-key, no non-key columns); got=%v",
			len(cols), cols)
	}

	// Every column must be marked NOT NULL and part of the PK (pk > 0).
	for _, c := range cols {
		if c.notnull != 1 {
			t.Errorf("context_edges column %q: notnull=%d, want 1", c.name, c.notnull)
		}
		if c.pk == 0 {
			t.Errorf("context_edges column %q: pk=%d, want > 0 (BCNF: every column part of PK)",
				c.name, c.pk)
		}
	}

	// Column names + order: (event_id, context_kind, context_id).
	wantNames := []string{"event_id", "context_kind", "context_id"}
	gotNames := make([]string, len(cols))
	for i, c := range cols {
		gotNames[i] = c.name
	}
	if !equalStrings(gotNames, wantNames) {
		t.Errorf("context_edges column order = %v, want %v", gotNames, wantNames)
	}

	// Column types: INTEGER, TEXT, TEXT.
	wantTypes := []string{"INTEGER", "TEXT", "TEXT"}
	for i, c := range cols {
		if !strings.EqualFold(c.colType, wantTypes[i]) {
			t.Errorf("context_edges column %q type = %q, want %q", c.name, c.colType, wantTypes[i])
		}
	}
}

// TestMigrateV2toV3_WellKnownAgents_UAT1Layout asserts the UAT-1 schema
// invariant for pasture_well_known_agents: agent_id is PK, name is UNIQUE
// (NOT inverted). This is a binding contract from PROPOSAL-2 §7.2 + §7.7.1.
func TestMigrateV2toV3_WellKnownAgents_UAT1Layout(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_uat1.db")
	_ = seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	cols := tableInfo(t, db, "pasture_well_known_agents")
	if len(cols) != 2 {
		t.Fatalf("pasture_well_known_agents has %d columns, want 2; got=%v", len(cols), cols)
	}

	// Column 0 must be agent_id PK; column 1 must be name (NOT NULL, UNIQUE).
	if cols[0].name != "agent_id" {
		t.Errorf("pasture_well_known_agents column 0 = %q, want %q (UAT-1: agent_id is PK)",
			cols[0].name, "agent_id")
	}
	if cols[0].pk != 1 {
		t.Errorf("pasture_well_known_agents.agent_id pk = %d, want 1 (UAT-1: agent_id is PK)", cols[0].pk)
	}
	if cols[1].name != "name" {
		t.Errorf("pasture_well_known_agents column 1 = %q, want %q (UAT-1: name is UNIQUE)",
			cols[1].name, "name")
	}
	if cols[1].notnull != 1 {
		t.Errorf("pasture_well_known_agents.name notnull = %d, want 1", cols[1].notnull)
	}
	// `name` must NOT be a PK column (UAT-1 explicitly forbids the inversion).
	if cols[1].pk != 0 {
		t.Errorf("pasture_well_known_agents.name pk = %d, want 0 (UAT-1: name is UNIQUE, not PK)",
			cols[1].pk)
	}

	// Verify the UNIQUE index on `name` exists in sqlite_master. SQLite emits
	// a sqlite_autoindex_<table>_<n> for each UNIQUE constraint.
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='pasture_well_known_agents'`,
	)
	if err != nil {
		t.Fatalf("UNIQUE index probe: %v", err)
	}
	defer rows.Close()
	var indexes []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		indexes = append(indexes, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	hasAutoIndex := false
	for _, idx := range indexes {
		if strings.HasPrefix(idx, "sqlite_autoindex_pasture_well_known_agents_") {
			hasAutoIndex = true
			break
		}
	}
	if !hasAutoIndex {
		t.Errorf("expected SQLite auto-index for UNIQUE constraint on pasture_well_known_agents; indexes=%v", indexes)
	}
}

// TestMigrateV2toV3_AgentCategories_Layout asserts the column layout for
// pasture_agent_categories per §7.2.
func TestMigrateV2toV3_AgentCategories_Layout(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_categories.db")
	_ = seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	cols := tableInfo(t, db, "pasture_agent_categories")
	if len(cols) != 3 {
		t.Fatalf("pasture_agent_categories has %d columns, want 3; got=%v", len(cols), cols)
	}
	wantNames := []string{"agent_id", "automaton_role", "pasture_role"}
	for i, c := range cols {
		if c.name != wantNames[i] {
			t.Errorf("pasture_agent_categories column %d name = %q, want %q",
				i, c.name, wantNames[i])
		}
	}
	// agent_id is PK.
	if cols[0].pk != 1 {
		t.Errorf("pasture_agent_categories.agent_id pk = %d, want 1", cols[0].pk)
	}
	// Both role columns are NOT NULL.
	for _, c := range cols[1:] {
		if c.notnull != 1 {
			t.Errorf("pasture_agent_categories.%s notnull = %d, want 1", c.name, c.notnull)
		}
	}
}

// ---- Old-row preservation --------------------------------------------------

// TestMigrateV2toV3_LegacyAuditEventsBackfilled proves S3+S4's scope:
// old audit_events rows survive the v3+v4 migration with their non-role,
// non-epoch_id data preserved AND get an agent_id populated by S3's
// find-or-create flow AND get a context_edges row with kind='EpochContext'
// from S4's backfill.
//
// History:
//   - Originally TestMigrateV2toV3_LegacyAuditEventsUntouched (S2 scope):
//     asserted role + epoch_id columns still existed.
//   - Updated for S3: role column dropped via table-rebuild; agent_id
//     populated; epoch_id still present.
//   - Updated for S4: epoch_id column dropped via table-rebuild;
//     epoch correlation now in context_edges with kind='EpochContext'.
func TestMigrateV2toV3_LegacyAuditEventsBackfilled(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_legacy.db")
	originalId := seedLegacyV1DB(t, dbPath)

	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	// Row count unchanged — every legacy row survives both table rebuilds.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatalf("audit_events count: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_events count after Migrate = %d, want 1 (no data loss)", count)
	}

	// Non-{role, epoch_id} columns preserved verbatim. Phase, event_type,
	// payload, agent_id all read from the post-v4 audit_events shape.
	var (
		gotPhase, gotType, gotPayload string
		gotAgentId                    sql.NullString
	)
	err := db.QueryRow(
		`SELECT phase, event_type, payload, agent_id FROM audit_events WHERE id=?`,
		originalId,
	).Scan(&gotPhase, &gotType, &gotPayload, &gotAgentId)
	if err != nil {
		t.Fatalf("legacy row probe: %v", err)
	}
	if gotPayload != `{"note":"survives v2→v3"}` {
		t.Errorf("payload mutated; got=%q", gotPayload)
	}

	// epoch_id is recoverable via context_edges (S4 backfill).
	var gotEpoch string
	err = db.QueryRow(
		`SELECT context_id FROM context_edges WHERE event_id=? AND context_kind='EpochContext'`,
		originalId,
	).Scan(&gotEpoch)
	if err != nil {
		t.Fatalf("epoch_id probe via context_edges: %v", err)
	}
	if gotEpoch != "epoch-pre-s2" {
		t.Errorf("epoch_id (via context_edges) = %q, want %q (S4 must migrate epoch_id as-is)", gotEpoch, "epoch-pre-s2")
	}

	// agent_id MUST be populated by the v3 backfill — every legacy row
	// gets attributed to a synthetic SoftwareAgent for its legacy role.
	if !gotAgentId.Valid {
		t.Fatal("audit_events.agent_id is NULL post-v3 Migrate; backfill failed")
	}

	// The agent_id MUST resolve to "pasture/legacy-role/supervisor" via
	// agents_software (the role of the seeded row was "supervisor").
	var agentName string
	err = db.QueryRow(
		`SELECT name FROM agents_software WHERE agent_id=?`,
		gotAgentId.String,
	).Scan(&agentName)
	if err != nil {
		t.Fatalf("resolve agent_id %q via agents_software: %v", gotAgentId.String, err)
	}
	if agentName != "pasture/legacy-role/supervisor" {
		t.Errorf("agents_software.name = %q, want %q",
			agentName, "pasture/legacy-role/supervisor")
	}

	// audit_events.role MUST be gone post-v3 (S3 drops it).
	// audit_events.epoch_id MUST be gone post-v4 (S4 drops it).
	cols := tableInfo(t, db, "audit_events")
	hasRole, hasEpochId, hasAgentId := false, false, false
	for _, c := range cols {
		switch c.name {
		case "role":
			hasRole = true
		case "epoch_id":
			hasEpochId = true
		case "agent_id":
			hasAgentId = true
		}
	}
	if hasRole {
		t.Error("audit_events.role column still present post-Migrate; S3 must drop it via table-rebuild")
	}
	if hasEpochId {
		t.Error("audit_events.epoch_id column still present post-Migrate; S4 must drop it via table-rebuild")
	}
	if !hasAgentId {
		t.Error("audit_events.agent_id column missing post-Migrate; S3 must add it")
	}
}

// ---- Insertability via raw SQL --------------------------------------------

// TestMigrateV2toV3_NewTablesInsertable verifies each new table accepts
// INSERT statements via raw SQL post-migration. This exercises the schema
// path the consuming slices will invoke through their own helpers (S5
// AttachContext writes context_edges; S5 SetAgentCategories writes
// pasture_agent_categories; S7 ensureWellKnownAgent writes
// pasture_well_known_agents).
//
// Post-S4 note: S4's v3→v4 backfill auto-populates context_edges with one
// row per legacy audit_events row (kind='EpochContext', context_id from
// the legacy epoch_id). The seeded legacy row contributes one such
// context_edges entry, so the user-inserted EpochContext row uses a
// different context_id to avoid colliding with the auto-backfill.
func TestMigrateV2toV3_NewTablesInsertable(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_insert.db")
	eventId := seedLegacyV1DB(t, dbPath)

	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	// Snapshot context_edges row count after migration (S4 backfilled one
	// row for the seeded legacy event); subsequent insertions are tested
	// relative to this baseline.
	var contextEdgesBaseline int
	if err := db.QueryRow(`SELECT COUNT(*) FROM context_edges`).Scan(&contextEdgesBaseline); err != nil {
		t.Fatalf("count context_edges (post-Migrate baseline): %v", err)
	}
	if contextEdgesBaseline != 1 {
		t.Fatalf("context_edges baseline post-Migrate = %d, want 1 (S4 backfills one row per legacy event with non-NULL epoch_id)", contextEdgesBaseline)
	}

	// Insert one new row in each of the three new tables. For
	// context_edges, use a distinct context_id from the auto-backfilled
	// row (whose context_id is "epoch-pre-s2", from seedLegacyV1DB).
	const userContextId = "aura-plugins--01968a3c-1234-7000-8000-000000000001"
	mustExec(t, db,
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventId, "SliceContext", userContextId,
	)
	mustExec(t, db,
		`INSERT INTO pasture_agent_categories (agent_id, automaton_role, pasture_role) VALUES (?, ?, ?)`,
		"01968a3c-1234-7000-8000-000000000002", "ConstraintChecker", "None",
	)
	mustExec(t, db,
		`INSERT INTO pasture_well_known_agents (agent_id, name) VALUES (?, ?)`,
		"01968a3c-1234-7000-8000-000000000003", "pasture/automaton/check-constraints",
	)

	// pasture_agent_categories and pasture_well_known_agents start empty
	// (S4 doesn't touch them); each now has exactly the one user row.
	for _, table := range []string{
		"pasture_agent_categories", "pasture_well_known_agents",
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("count(%s) = %d, want 1", table, n)
		}
	}

	// context_edges has the S4-backfilled row + the user row.
	var contextEdgesNow int
	if err := db.QueryRow(`SELECT COUNT(*) FROM context_edges`).Scan(&contextEdgesNow); err != nil {
		t.Fatalf("count context_edges (post-insert): %v", err)
	}
	if contextEdgesNow != contextEdgesBaseline+1 {
		t.Errorf("count(context_edges) = %d, want %d (baseline %d + 1 user-inserted row)",
			contextEdgesNow, contextEdgesBaseline+1, contextEdgesBaseline)
	}

	// Read-back the user-inserted context_edges row to verify the
	// composite PK round-trips.
	var (
		gotEventId        int64
		gotKind, gotCtxId string
	)
	err := db.QueryRow(
		`SELECT event_id, context_kind, context_id FROM context_edges WHERE context_id = ?`,
		userContextId,
	).Scan(&gotEventId, &gotKind, &gotCtxId)
	if err != nil {
		t.Fatalf("context_edges read-back: %v", err)
	}
	if gotEventId != eventId || gotKind != "SliceContext" {
		t.Errorf("context_edges row mismatch: event_id=%d kind=%q, want event_id=%d kind=%q",
			gotEventId, gotKind, eventId, "SliceContext")
	}
}

// TestMigrateV2toV3_WellKnownAgents_NameUniqueEnforced verifies the UNIQUE
// constraint on pasture_well_known_agents.name (the idempotency anchor used
// by S7's ensureWellKnownAgent flow).
func TestMigrateV2toV3_WellKnownAgents_NameUniqueEnforced(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_unique.db")
	_ = seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	const name = "pasture/automaton/check-constraints"
	mustExec(t, db,
		`INSERT INTO pasture_well_known_agents (agent_id, name) VALUES (?, ?)`,
		"id-1", name,
	)

	// Inserting a different agent_id with the SAME name MUST fail (UNIQUE).
	_, err := db.Exec(
		`INSERT INTO pasture_well_known_agents (agent_id, name) VALUES (?, ?)`,
		"id-2", name,
	)
	if err == nil {
		t.Fatalf("expected UNIQUE constraint violation on duplicate name=%q, got nil", name)
	}
	// Inserting the SAME agent_id with a different name MUST also fail (PK).
	_, err = db.Exec(
		`INSERT INTO pasture_well_known_agents (agent_id, name) VALUES (?, ?)`,
		"id-1", "pasture/automaton/something-else",
	)
	if err == nil {
		t.Fatalf("expected PK constraint violation on duplicate agent_id=%q, got nil", "id-1")
	}
}

// TestMigrateV2toV3_ContextEdges_PKEnforced verifies the composite PK on
// context_edges rejects duplicate (event_id, context_kind, context_id).
//
// Post-S4 note: S4's v3→v4 backfill auto-populates context_edges with one
// row per legacy audit_events row (kind='EpochContext', context_id from
// the legacy epoch_id). The user-inserted rows in this test use
// SliceContext (not EpochContext) on a distinct context_id from the
// auto-backfill so the assertions measure only user-insert behaviour.
func TestMigrateV2toV3_ContextEdges_PKEnforced(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_pk.db")
	eventId := seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	// Snapshot the post-S4 baseline: one row per legacy event with
	// non-NULL epoch_id. The seedLegacyV1DB helper writes one row.
	var baseline int
	if err := db.QueryRow(`SELECT COUNT(*) FROM context_edges`).Scan(&baseline); err != nil {
		t.Fatalf("baseline COUNT(*): %v", err)
	}

	mustExec(t, db,
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventId, "SliceContext", "ctx-1",
	)
	// Duplicate triple must fail.
	_, err := db.Exec(
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventId, "SliceContext", "ctx-1",
	)
	if err == nil {
		t.Fatalf("expected PK violation on duplicate (event_id,kind,context_id), got nil")
	}
	// Different context_kind on the same (event_id, context_id) must succeed.
	mustExec(t, db,
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventId, "GitContext", "ctx-1",
	)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM context_edges`).Scan(&n); err != nil {
		t.Fatalf("COUNT(*): %v", err)
	}
	if n != baseline+2 {
		t.Errorf("context_edges count = %d, want %d (baseline %d + 2 user-inserted rows on different kinds)",
			n, baseline+2, baseline)
	}
}

// ---- Idempotency -----------------------------------------------------------

// TestMigrateV2toV3_Idempotent_ReRunNoop verifies a second Migrate call on
// an already-current database leaves the schema and audit_schema_meta
// unchanged. (Asserts the post-MaxKnownSchemaVersion idempotency property
// — read from the constant so the assertion follows the binary's
// supported version, currently v4 after S4.)
func TestMigrateV2toV3_Idempotent_ReRunNoop(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "v3_idemp.db")
	_ = seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)

	if err := audit.Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	// Snapshot row count of audit_schema_meta after first migrate.
	var firstCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_schema_meta`).Scan(&firstCount); err != nil {
		t.Fatalf("first count: %v", err)
	}

	if err := audit.Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var secondCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_schema_meta`).Scan(&secondCount); err != nil {
		t.Fatalf("second count: %v", err)
	}
	if firstCount != secondCount {
		t.Errorf("audit_schema_meta row count changed across re-Migrate: %d → %d (want stable)",
			firstCount, secondCount)
	}

	// Version still at MaxKnownSchemaVersion.
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("version probe: %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("MAX(version) after re-Migrate = %d, want %d (MaxKnownSchemaVersion)",
			version, audit.MaxKnownSchemaVersion)
	}

	// Tables still present, indexes still present.
	for _, table := range []string{
		"context_edges", "pasture_agent_categories", "pasture_well_known_agents",
	} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q missing after re-Migrate", table)
		}
	}
	for _, idx := range []string{"idx_context_edges_lookup", "idx_context_edges_event"} {
		if !indexExists(t, db, idx) {
			t.Errorf("index %q missing after re-Migrate", idx)
		}
	}
}

// ---- Version bookkeeping ---------------------------------------------------

// TestMigrate_VersionBumpedToMaxKnown verifies that after Migrate against
// a legacy v1 database, audit_schema_meta records the binary's
// MaxKnownSchemaVersion (currently 4 after S4). Catches accidental
// regressions to the constant or the migration step registry.
//
// History: this test was originally TestMigrateV2toV3_VersionBumpedTo3
// and hard-coded "3". Updated to read from the constant so the assertion
// follows the binary; explicitly asserts MaxKnownSchemaVersion >= 4 to
// catch a regression below S4's published guarantee.
func TestMigrate_VersionBumpedToMaxKnown(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "max_known_version.db")
	_ = seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	if audit.MaxKnownSchemaVersion < 4 {
		t.Errorf("audit.MaxKnownSchemaVersion = %d, want >= 4 (S4 must bump to 4 or higher)",
			audit.MaxKnownSchemaVersion)
	}

	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("version probe: %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("post-Migrate MAX(version) = %d, want %d (MaxKnownSchemaVersion)",
			version, audit.MaxKnownSchemaVersion)
	}
}

// TestMigrate_FreshV2DbAdvancesToMaxKnown verifies the explicit v2→...→
// MaxKnownSchemaVersion forward chain on a synthetic v2 fixture. Each
// step in migrationSteps() is exercised in order from v2 onward.
//
// History: originally TestMigrateV2toV3_FreshV2DbAdvancesToV3 (hard-coded
// "3"). Updated to read MaxKnownSchemaVersion so the assertion follows
// the binary as new v* steps are added.
func TestMigrate_FreshV2DbAdvancesToMaxKnown(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "fresh_v2_advances.db")

	// Hand-build a v2-shaped database: audit_events + audit_schema_meta with
	// version=2 row but none of the v3+ tables.
	{
		db := openDB(t, dbPath)
		mustExec(t, db, `
			CREATE TABLE audit_events (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				epoch_id   TEXT    NOT NULL,
				phase      TEXT    NOT NULL,
				role       TEXT    NOT NULL,
				event_type TEXT    NOT NULL,
				payload    TEXT    NOT NULL,
				timestamp  INTEGER NOT NULL
			)`)
		mustExec(t, db, `
			CREATE TABLE audit_schema_meta (
				version    INTEGER PRIMARY KEY,
				applied_at INTEGER NOT NULL
			)`)
		mustExec(t, db,
			`INSERT INTO audit_schema_meta (version, applied_at) VALUES (?, ?)`,
			2, time.Now().UnixNano(),
		)
	}

	// Reopen and run Migrate.
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("Migrate from v2: %v", err)
	}

	// All v3+-introduced tables must exist.
	for _, table := range []string{
		"context_edges", "pasture_agent_categories", "pasture_well_known_agents",
	} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q absent after Migrate from v2", table)
		}
	}

	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("version probe: %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("post-Migrate MAX(version) = %d, want %d (MaxKnownSchemaVersion)",
			version, audit.MaxKnownSchemaVersion)
	}
}

// ---- Misc helpers ----------------------------------------------------------

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
