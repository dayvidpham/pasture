// Package audit_test — migrate_test.go
//
// End-to-end migration tests for audit.Migrate, including:
//   - Round-trip across a Close/reopen cycle (durability).
//   - Legacy-v1 detection (no audit_schema_meta table ⇒ version 1).
//   - NewSqliteAuditTrail wiring (Migrate is invoked automatically on open).
//   - Newer-schema rejection — PROPOSAL-2 §11 Scenario 5.
//
// All tests are file-backed via t.TempDir() per pasture/CLAUDE.md and
// IMPL_PLAN §1.2.
package audit_test

import (
	"database/sql"
	stderrors "errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"

	_ "modernc.org/sqlite"
)

// ---- Round-trip durability across reopen -----------------------------------

// TestMigrate_RoundTrip_OpenMigrateReopen verifies the audit_schema_meta
// row survives a Close/reopen cycle and the second open is a no-op.
// This is the property NewSqliteAuditTrail relies on for the
// "auto-migrate-on-open" semantics (§7.10).
func TestMigrate_RoundTrip_OpenMigrateReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "roundtrip.db")

	// Phase 1: open, migrate to v2, close.
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("sql.Open phase 1: %v", err)
		}
		if err := audit.Migrate(db); err != nil {
			db.Close()
			t.Fatalf("Migrate phase 1: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close phase 1: %v", err)
		}
	}

	// Phase 2: reopen, run Migrate again, assert version still 2 and only
	// one row in audit_schema_meta.
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("sql.Open phase 2: %v", err)
		}
		defer db.Close()

		if err := audit.Migrate(db); err != nil {
			t.Fatalf("Migrate phase 2: %v", err)
		}

		var version int
		if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
			t.Fatalf("SELECT MAX(version): %v", err)
		}
		if version != audit.MaxKnownSchemaVersion {
			t.Errorf("schema version after reopen = %d, want %d", version, audit.MaxKnownSchemaVersion)
		}

		// One row per applied migration step. From a fresh DB the migrator
		// applies steps v1→v2, v2→v3 (and v3→v4 once S4 lands), so the count
		// equals MaxKnownSchemaVersion - 1. The reopen path must not add
		// duplicate rows, so the count after reopen equals the count after
		// the first migrate.
		var rows int
		if err := db.QueryRow(`SELECT COUNT(*) FROM audit_schema_meta`).Scan(&rows); err != nil {
			t.Fatalf("COUNT(*): %v", err)
		}
		wantRows := audit.MaxKnownSchemaVersion - 1
		if rows != wantRows {
			t.Errorf("audit_schema_meta row count after reopen = %d, want %d (one row per step from v1 to v%d)",
				rows, wantRows, audit.MaxKnownSchemaVersion)
		}
	}
}

// ---- Legacy-v1 detection ---------------------------------------------------

// TestMigrate_LegacyV1Database_PromotedToV2 simulates a pre-PROPOSAL-2
// audit database (audit_events present, no audit_schema_meta table). The
// migrator should detect this as version 1, run v1→v2, and leave the file
// at version 2 — without touching the existing audit_events rows.
func TestMigrate_LegacyV1Database_PromotedToV2(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy_v1.db")

	// Hand-build a legacy-shaped DB: audit_events table with one row, no
	// audit_schema_meta. This matches the on-disk shape of every existing
	// pasture audit database before PROPOSAL-2 lands.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
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
	mustExec(t, db,
		`INSERT INTO audit_events (epoch_id, phase, role, event_type, payload, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"epoch-legacy-1", "p1-request", "supervisor", "PhaseTransition", `{"note":"legacy"}`, time.Now().UnixNano(),
	)

	// Sanity-check: no audit_schema_meta yet.
	var preCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='audit_schema_meta'`,
	).Scan(&preCount)
	if err != nil {
		t.Fatalf("pre-Migrate sqlite_master probe: %v", err)
	}
	if preCount != 0 {
		t.Fatalf("expected no audit_schema_meta table pre-Migrate, found %d", preCount)
	}

	// Run the migrator.
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}

	// Post-conditions:
	//   1. audit_schema_meta exists, has version=2.
	//   2. audit_events row count is unchanged (1).
	//   3. The legacy row's data survived verbatim.
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("post-Migrate SELECT MAX(version): %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("post-Migrate version = %d, want %d", version, audit.MaxKnownSchemaVersion)
	}

	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&eventCount); err != nil {
		t.Fatalf("post-Migrate audit_events count: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("audit_events row count after Migrate = %d, want 1 (no data loss)", eventCount)
	}

	// Post-S3, audit_events.role is dropped via table-rebuild and replaced
	// with agent_id (NOT NULL). The role string is recoverable by joining
	// agents_software via agent_id; the v3 backfill mints one
	// pasture/legacy-role/<role> agent per distinct legacy role.
	var (
		epoch     string
		agentName string
	)
	err = db.QueryRow(
		`SELECT ae.epoch_id, asw.name
		 FROM audit_events ae
		 JOIN agents_software asw ON asw.agent_id = ae.agent_id`,
	).Scan(&epoch, &agentName)
	if err != nil {
		t.Fatalf("legacy-row probe (epoch_id + agents_software join): %v", err)
	}
	if epoch != "epoch-legacy-1" {
		t.Errorf("legacy row epoch_id = %q, want %q (S3 must preserve epoch_id; S4 drops it)",
			epoch, "epoch-legacy-1")
	}
	if agentName != "pasture/legacy-role/supervisor" {
		t.Errorf("legacy row agent name = %q, want %q (S3 backfill maps role 'supervisor' to this synthetic agent)",
			agentName, "pasture/legacy-role/supervisor")
	}
}

// ---- NewSqliteAuditTrail wiring --------------------------------------------

// TestNewSqliteAuditTrail_RunsMigrate verifies that NewSqliteAuditTrail
// invokes audit.Migrate after ensureSchema, so any caller that opens an
// audit database via the public constructor automatically lands at the
// current schema version. This is the contract S5 (OpenTaskTracker)
// relies on.
func TestNewSqliteAuditTrail_RunsMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wired.db")

	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail: %v", err)
	}
	t.Cleanup(func() { _ = trail.Close() })

	// Open a second handle to inspect the on-disk state. (The trail's
	// internal *sql.DB is unexported; we re-open the same file.)
	probe, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("probe sql.Open: %v", err)
	}
	defer probe.Close()

	var version int
	err = probe.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version)
	if err != nil {
		t.Fatalf("probe SELECT MAX(version): %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("post-NewSqliteAuditTrail version = %d, want %d (Migrate must run on open)",
			version, audit.MaxKnownSchemaVersion)
	}
}

// ---- §11 Scenario 5: Newer-schema rejection (BLOCKER A3) -------------------

// TestMigrate_NewerSchema_RejectedWithStructuredError implements PROPOSAL-2
// §11 Scenario 5 (newer-schema rejection — error shape verified). A
// database whose audit_schema_meta says version=99 is rejected by an older
// binary that knows up to MaxKnownSchemaVersion (2 at S1; will be 4 after
// S4). Field-by-field assertions match the spec's text exactly.
//
// Note on MaxKnownSchemaVersion: at S1 time this is 2, so the asserted
// "supported version" string reads "version 99 is newer than supported
// version 2". After S2/S4 land this becomes 4; the test reads the
// constant rather than hard-coding a literal so the assertion follows
// the binary.
func TestMigrate_NewerSchema_RejectedWithStructuredError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "future.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	// Manually create the meta table and seed a future version (99). This
	// simulates a database written by a newer binary than the one running
	// the test.
	mustExec(t, db, `
		CREATE TABLE audit_schema_meta (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`)
	mustExec(t, db,
		`INSERT INTO audit_schema_meta (version, applied_at) VALUES (?, ?)`,
		99, time.Now().UnixNano(),
	)

	// Migrate must reject and return an actionable *StructuredError.
	err = audit.Migrate(db)
	if err == nil {
		t.Fatal("Migrate against v99 DB returned nil, want *StructuredError")
	}

	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("Migrate returned %T, want *pasterrors.StructuredError", err)
	}

	// Field-by-field assertions per Scenario 5.
	if se.Category != pasterrors.CategoryStorage {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryStorage)
	}

	wantWhat := fmt.Sprintf("audit database schema version 99 is newer than supported version %d", audit.MaxKnownSchemaVersion)
	if se.What != wantWhat {
		t.Errorf("What = %q, want %q", se.What, wantWhat)
	}

	wantWhy := "this binary was built before the schema was bumped"
	if se.Why != wantWhy {
		t.Errorf("Why = %q, want %q", se.Why, wantWhy)
	}

	wantImpact := "no events can be read or written until the binary is upgraded"
	if se.Impact != wantImpact {
		t.Errorf("Impact = %q, want %q", se.Impact, wantImpact)
	}

	wantFixSubstring := "upgrade pasture to a version that supports schema v99"
	if !strings.Contains(se.Fix, wantFixSubstring) {
		t.Errorf("Fix = %q, want substring %q", se.Fix, wantFixSubstring)
	}

	// Exit code mapping (Scenario 5 explicitly asserts ExitCode(err) == 5).
	if got := pasterrors.ExitCode(err); got != 5 {
		t.Errorf("ExitCode(err) = %d, want 5", got)
	}
}

// TestNewSqliteAuditTrail_NewerSchema_Rejected verifies the rejection
// also happens through the public constructor path (NewSqliteAuditTrail),
// not just direct audit.Migrate calls. This is the path S5 will exercise
// via OpenTaskTracker.
func TestNewSqliteAuditTrail_NewerSchema_Rejected(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "future_via_constructor.db")

	// Pre-seed the file with a future-schema marker before the constructor
	// sees it.
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("sql.Open seed: %v", err)
		}
		mustExec(t, db, `
			CREATE TABLE audit_schema_meta (
				version    INTEGER PRIMARY KEY,
				applied_at INTEGER NOT NULL
			)`)
		mustExec(t, db,
			`INSERT INTO audit_schema_meta (version, applied_at) VALUES (?, ?)`,
			99, time.Now().UnixNano(),
		)
		_ = db.Close()
	}

	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err == nil {
		_ = trail.Close()
		t.Fatal("NewSqliteAuditTrail against v99 DB returned nil error, want *StructuredError")
	}

	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("NewSqliteAuditTrail returned %T, want *pasterrors.StructuredError", err)
	}
	if se.Category != pasterrors.CategoryStorage {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryStorage)
	}
}

// ---- helpers ---------------------------------------------------------------

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
