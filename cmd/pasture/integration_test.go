// Package main_test — Slice S10 integration assertions (PROPOSAL-2 §7.4 +
// §7.10 + §11 Scenario 9).
//
// These tests exercise the binary built by main_test.go's TestMain. They
// verify the unification claim that PROPOSAL-2 §7.1 makes:
//
//  1. The `pasture` CLI now writes to the unified pasture.db file (via the
//     OpenTaskTracker re-route landed in S10 L2a). Pre-PROPOSAL-2 the CLI
//     used OpenTracker which only opened the Provenance subsystem; the audit
//     tables (audit_schema_meta, context_edges, pasture_agent_categories,
//     pasture_well_known_agents) were absent. After S10 L2a, even a single
//     `pasture task create` triggers OpenTaskTracker → audit.NewSqliteAuditTrail
//     → audit.Migrate, leaving all four audit tables on the same file.
//
//  2. Auto-on-open migration applied transparently to legacy provenance-only
//     databases. The user runs an existing CLI command against an old file;
//     the audit migrator catches up to MaxKnownSchemaVersion silently. No
//     user-visible change in the command's stdout/stderr (Scenario 9
//     byte-identical-output requirement).
//
//  3. The default DB path resolves to the unified pasture.db filename when
//     the user does not pass --db (covered indirectly here; the unit test
//     for the helper lives in internal/tasks/paths_test.go).
//
// Subprocess execution (rather than in-process import) is the correct model
// for the CLI assertions because exitWithCode(int) calls os.Exit and would
// terminate the test runner if executed in-process. The compiled binary at
// binaryPath is shared with the existing main_test.go assertions.
package main_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/audit"
)

// TestCLI_S10_TaskCreate_LeavesUnifiedAuditTables verifies that a single
// `pasture task create` against a fresh database file produces all four
// pasture-side tables on disk (the audit-side ones), proving the
// OpenTaskTracker re-route is wired through the CLI and the audit migrator
// runs as a side effect.
//
// Pre-S10 L2a the same invocation would have produced ONLY the Provenance
// tables (tasks, agents, edges, etc.) and no audit_schema_meta, no
// context_edges, no pasture_well_known_agents. Asserting on the audit
// tables' presence is therefore the cleanest way to prove the route flipped
// without requiring deeper introspection (e.g., audit_schema_meta version).
func TestCLI_S10_TaskCreate_LeavesUnifiedAuditTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	out := runCLI(t,
		"--db", dbPath,
		"--namespace", "demo",
		"--format", "json",
		"task", "create", "S10 unified-tables probe",
	)
	if out.exitCode != 0 {
		t.Fatalf("create exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}

	// Audit-side tables that PROPOSAL-2 §7.4 + §7.10 require to exist on
	// the unified file after any OpenTaskTracker call. Pre-S10 the
	// `pasture` CLI used OpenTracker (Provenance-only) and these tables
	// were absent until the user ran the daemon or `pasture migrate`.
	expectedAuditTables := []string{
		"audit_schema_meta",
		"audit_events",
		"context_edges",
		"pasture_agent_categories",
		"pasture_well_known_agents",
	}

	tables := mustListSQLiteTables(t, dbPath)
	tableSet := make(map[string]bool, len(tables))
	for _, name := range tables {
		tableSet[name] = true
	}

	for _, want := range expectedAuditTables {
		if !tableSet[want] {
			t.Errorf("expected audit-side table %q on unified pasture.db; got tables=%v",
				want, tables)
		}
	}

	// Belt-and-braces: the Provenance tables must ALSO be present (the CLI
	// wouldn't have produced a task ID in the first place if they weren't,
	// but asserting it explicitly proves we haven't accidentally bypassed
	// the Provenance subsystem). We only check a representative subset.
	expectedProvenanceTables := []string{
		"tasks",
		"agents",
		"edges",
	}
	for _, want := range expectedProvenanceTables {
		if !tableSet[want] {
			t.Errorf("expected Provenance table %q on unified pasture.db; got tables=%v",
				want, tables)
		}
	}
}

// TestCLI_S10_AutoMigratesLegacyV1OnFirstUse verifies that running a
// pre-PROPOSAL-2 hjsdt command against a legacy v1 database file silently
// brings the audit_schema_meta version to MaxKnownSchemaVersion. The
// assertion proves the CLI now triggers the same auto-migration path the
// daemon and `pasture migrate` use (PROPOSAL-2 §7.10, three-paths-one-
// migrator binding).
//
// The legacy fixture is constructed in-place via Provenance bootstrap +
// raw v1 audit_events DDL (mirroring cmd/pasture/task_events_test.go's
// newLegacyV1DB helper, but inlined here so this test stands alone if
// task_events_test.go is reorganised). On first `pasture task list` the
// audit migrator should detect version<MaxKnownSchemaVersion and run the
// forward chain transparently.
func TestCLI_S10_AutoMigratesLegacyV1OnFirstUse(t *testing.T) {
	dbPath := makeLegacyV1ForS10(t)

	// Pre-condition: the file is a Provenance database with a v1-shaped
	// audit_events table and NO audit_schema_meta yet. mustReadVersionS10
	// returns 0 in that case (no audit_schema_meta row exists).
	if v := mustReadVersionS10(t, dbPath); v != 0 {
		t.Fatalf("pre-condition: expected v=0 (no audit_schema_meta) on fresh legacy fixture, got v=%d", v)
	}

	// Run a no-op CLI command (`task list` against an empty namespace). The
	// success or failure of the listing is irrelevant; what we care about is
	// the side effect: OpenTaskTracker → audit.NewSqliteAuditTrail →
	// audit.Migrate runs to completion, leaving audit_schema_meta at
	// MaxKnownSchemaVersion.
	out := runCLI(t,
		"--db", dbPath,
		"--namespace", "demo",
		"--format", "json",
		"task", "list",
	)
	if out.exitCode != 0 {
		t.Fatalf("task list exit %d; stdout=%q stderr=%q", out.exitCode, out.stdout, out.stderr)
	}

	// Post-condition: the migrator ran; on-disk version equals
	// MaxKnownSchemaVersion. This is the byte-identical-output Scenario 9
	// invariant: the CLI command produced its expected output AND the file
	// is now upgraded — no separate migration step needed.
	post := mustReadVersionS10(t, dbPath)
	if post != audit.MaxKnownSchemaVersion {
		t.Errorf("post auto-migration version = %d, want %d", post, audit.MaxKnownSchemaVersion)
	}
}

// TestCLI_S10_HelpMentionsUnifiedDefault verifies that `pasture --help` now
// mentions the unified pasture.db default path. This is a guard against the
// help text drifting back to "provenance.db" or "audit.db" (which would
// confuse users about where their data lives) and pairs with the binding in
// internal/tasks.DefaultDBFilename.
func TestCLI_S10_HelpMentionsUnifiedDefault(t *testing.T) {
	out := runCLI(t, "--help")
	if out.exitCode != 0 {
		t.Fatalf("--help exit %d; stderr=%q", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	if !strings.Contains(combined, "pasture.db") {
		t.Errorf("expected --help output to mention the unified pasture.db filename;\n%s", combined)
	}
	// The legacy filename must NOT appear in the help text — that would
	// imply the documentation is stale and the binding from L1 / paths.go
	// has regressed silently.
	if strings.Contains(combined, "provenance.db") || strings.Contains(combined, "audit.db") {
		t.Errorf("help text should not mention legacy provenance.db or audit.db; got:\n%s", combined)
	}
}

// ─── helpers (S10-scoped, named distinct from main_test.go to avoid clashes) ─

// makeLegacyV1ForS10 constructs a Provenance + v1-audit-events database in
// t.TempDir(). Returns the path. Mirrors the helper in task_events_test.go
// but lives in this file so the S10 integration tests stand alone if
// task_events_test.go is reorganised.
func makeLegacyV1ForS10(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	tr, err := provenanceOpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("provenance bootstrap: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("provenance close: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE audit_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			epoch_id   TEXT    NOT NULL,
			phase      TEXT    NOT NULL,
			role       TEXT    NOT NULL,
			event_type TEXT    NOT NULL,
			payload    TEXT    NOT NULL,
			timestamp  INTEGER NOT NULL
		)`); err != nil {
		t.Fatalf("create v1 audit_events: %v", err)
	}
	return dbPath
}

// mustListSQLiteTables returns all user-defined table names on dbPath.
// Internal SQLite tables (sqlite_*) are filtered out so the assertion
// targets pasture's own schema.
func mustListSQLiteTables(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", dbPath, err)
	}
	defer db.Close()

	rows, err := db.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iter: %v", err)
	}
	return names
}

// mustReadVersionS10 returns MAX(version) from audit_schema_meta. Returns 0
// when the table does not exist OR contains no rows — which is the legacy
// pre-PROPOSAL-2 state (no audit_schema_meta table at all).
func mustReadVersionS10(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", dbPath, err)
	}
	defer db.Close()

	// Probe table existence first so we can return 0 cleanly for legacy
	// files (avoids a "no such table" error from the SELECT MAX path).
	var name sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='audit_schema_meta'`,
	).Scan(&name); err != nil {
		if err == sql.ErrNoRows {
			return 0
		}
		t.Fatalf("probe audit_schema_meta: %v", err)
	}
	if !name.Valid {
		return 0
	}

	var v sql.NullInt64
	if err := db.QueryRowContext(context.Background(),
		`SELECT MAX(version) FROM audit_schema_meta`,
	).Scan(&v); err != nil {
		t.Fatalf("read MAX(version): %v", err)
	}
	if !v.Valid {
		return 0
	}
	return int(v.Int64)
}
