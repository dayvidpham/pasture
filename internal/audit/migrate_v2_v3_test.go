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

// TestMigrateV2toV3_LegacyAuditEventsUntouched proves S2's scope boundary:
// old audit_events rows survive verbatim. The audit_events column changes
// (agent_id add + role drop) live in S3.
func TestMigrateV2toV3_LegacyAuditEventsUntouched(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v3_legacy.db")
	originalID := seedLegacyV1DB(t, dbPath)

	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	// Row count unchanged.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatalf("audit_events count: %v", err)
	}
	if count != 1 {
		t.Errorf("audit_events count after Migrate = %d, want 1 (no data loss)", count)
	}

	// Row content preserved verbatim.
	var (
		gotEpoch, gotPhase, gotRole, gotType, gotPayload string
	)
	err := db.QueryRow(
		`SELECT epoch_id, phase, role, event_type, payload FROM audit_events WHERE id=?`,
		originalID,
	).Scan(&gotEpoch, &gotPhase, &gotRole, &gotType, &gotPayload)
	if err != nil {
		t.Fatalf("legacy row probe: %v", err)
	}
	if gotEpoch != "epoch-pre-s2" {
		t.Errorf("epoch_id = %q, want %q", gotEpoch, "epoch-pre-s2")
	}
	if gotRole != "supervisor" {
		t.Errorf("role = %q, want %q (S2 scope: role column untouched)", gotRole, "supervisor")
	}
	if gotPayload != `{"note":"survives v2→v3"}` {
		t.Errorf("payload mutated; got=%q", gotPayload)
	}

	// audit_events.role and audit_events.epoch_id columns must STILL exist
	// in v3 (S3 drops role; S4 drops epoch_id). This test is the canary that
	// fires if S3/S4 work accidentally lands in S2.
	cols := tableInfo(t, db, "audit_events")
	hasRole, hasEpochID := false, false
	for _, c := range cols {
		switch c.name {
		case "role":
			hasRole = true
		case "epoch_id":
			hasEpochID = true
		}
	}
	if !hasRole {
		t.Error("audit_events.role column missing post-Migrate; S2 scope says it must remain (S3 drops it)")
	}
	if !hasEpochID {
		t.Error("audit_events.epoch_id column missing post-Migrate; S2 scope says it must remain (S4 drops it)")
	}
}

// ---- Insertability via raw SQL --------------------------------------------

// TestMigrateV2toV3_NewTablesInsertable verifies each new table accepts
// INSERT statements via raw SQL post-migration. This exercises the schema
// path the consuming slices will invoke through their own helpers (S5
// AttachContext writes context_edges; S5 SetAgentCategories writes
// pasture_agent_categories; S7 ensureWellKnownAgent writes
// pasture_well_known_agents).
func TestMigrateV2toV3_NewTablesInsertable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v3_insert.db")
	eventID := seedLegacyV1DB(t, dbPath)

	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	// Insert one row in each of the three new tables.
	mustExec(t, db,
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventID, "EpochContext", "aura-plugins--01968a3c-1234-7000-8000-000000000001",
	)
	mustExec(t, db,
		`INSERT INTO pasture_agent_categories (agent_id, automaton_role, pasture_role) VALUES (?, ?, ?)`,
		"01968a3c-1234-7000-8000-000000000002", "ConstraintChecker", "None",
	)
	mustExec(t, db,
		`INSERT INTO pasture_well_known_agents (agent_id, name) VALUES (?, ?)`,
		"01968a3c-1234-7000-8000-000000000003", "pasture/automaton/check-constraints",
	)

	// Each table now has exactly one row.
	for _, table := range []string{
		"context_edges", "pasture_agent_categories", "pasture_well_known_agents",
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("count(%s) = %d, want 1", table, n)
		}
	}

	// Read-back a context_edges row to verify the composite PK round-trips.
	var (
		gotEventID        int64
		gotKind, gotCtxID string
	)
	err := db.QueryRow(
		`SELECT event_id, context_kind, context_id FROM context_edges`,
	).Scan(&gotEventID, &gotKind, &gotCtxID)
	if err != nil {
		t.Fatalf("context_edges read-back: %v", err)
	}
	if gotEventID != eventID || gotKind != "EpochContext" {
		t.Errorf("context_edges row mismatch: event_id=%d kind=%q, want event_id=%d kind=%q",
			gotEventID, gotKind, eventID, "EpochContext")
	}
}

// TestMigrateV2toV3_WellKnownAgents_NameUniqueEnforced verifies the UNIQUE
// constraint on pasture_well_known_agents.name (the idempotency anchor used
// by S7's ensureWellKnownAgent flow).
func TestMigrateV2toV3_WellKnownAgents_NameUniqueEnforced(t *testing.T) {
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
func TestMigrateV2toV3_ContextEdges_PKEnforced(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v3_pk.db")
	eventID := seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	mustExec(t, db,
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventID, "EpochContext", "ctx-1",
	)
	// Duplicate triple must fail.
	_, err := db.Exec(
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventID, "EpochContext", "ctx-1",
	)
	if err == nil {
		t.Fatalf("expected PK violation on duplicate (event_id,kind,context_id), got nil")
	}
	// Different context_kind on the same (event_id, context_id) must succeed.
	mustExec(t, db,
		`INSERT INTO context_edges (event_id, context_kind, context_id) VALUES (?, ?, ?)`,
		eventID, "GitContext", "ctx-1",
	)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM context_edges`).Scan(&n); err != nil {
		t.Fatalf("COUNT(*): %v", err)
	}
	if n != 2 {
		t.Errorf("context_edges count = %d, want 2 (different kinds on same event+context_id)", n)
	}
}

// ---- Idempotency -----------------------------------------------------------

// TestMigrateV2toV3_Idempotent_ReRunNoop verifies a second Migrate call on
// an already-v3 database leaves the schema and audit_schema_meta unchanged.
func TestMigrateV2toV3_Idempotent_ReRunNoop(t *testing.T) {
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

	// Version still 3.
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("version probe: %v", err)
	}
	if version != 3 {
		t.Errorf("MAX(version) after re-Migrate = %d, want 3", version)
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

// TestMigrateV2toV3_VersionBumpedTo3 verifies that after Migrate against a
// legacy v1 database, audit_schema_meta records version=3 (the new
// MaxKnownSchemaVersion after S2). Catches accidental regressions to the
// constant or the migration step registry.
func TestMigrateV2toV3_VersionBumpedTo3(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v3_version.db")
	_ = seedLegacyV1DB(t, dbPath)
	db := openDB(t, dbPath)
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	if audit.MaxKnownSchemaVersion != 3 {
		t.Errorf("audit.MaxKnownSchemaVersion = %d, want 3 (S2 must bump)",
			audit.MaxKnownSchemaVersion)
	}

	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("version probe: %v", err)
	}
	if version != 3 {
		t.Errorf("post-Migrate MAX(version) = %d, want 3", version)
	}
}

// TestMigrateV2toV3_FreshV2DbAdvancesToV3 verifies the explicit v2→v3 step
// alone (legacy v1 DB has been promoted to v2; we then exercise just the
// v2→v3 hop on a synthetic v2 fixture).
func TestMigrateV2toV3_FreshV2DbAdvancesToV3(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v3_from_v2.db")

	// Hand-build a v2-shaped database: audit_events + audit_schema_meta with
	// version=2 row but none of the v3 tables.
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
		t.Fatalf("Migrate v2→v3: %v", err)
	}

	for _, table := range []string{
		"context_edges", "pasture_agent_categories", "pasture_well_known_agents",
	} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q absent after v2→v3 Migrate", table)
		}
	}

	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("version probe: %v", err)
	}
	if version != 3 {
		t.Errorf("post-Migrate MAX(version) = %d, want 3", version)
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
