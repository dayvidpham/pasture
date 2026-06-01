// Package audit_test — schema_meta_test.go
//
// Tests for the audit_schema_meta read/write helpers. All tests are
// file-backed via t.TempDir() per pasture/CLAUDE.md and IMPL_PLAN §1.2:
// in-memory SQLite bypasses WAL/busy_timeout/fsync, the exact mechanisms
// the migration design (D11, BLOCKERs A1–B2) relies on.
package audit_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	stderrors "errors"
	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"

	_ "modernc.org/sqlite"
)

// openTempDB opens a fresh empty SQLite file under t.TempDir() and returns
// the *sql.DB (registered for cleanup) plus its filesystem path.
func openTempDB(t *testing.T, name string) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, name)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(sqlite, %q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dbPath
}

// ---- audit.Migrate behaviour on a fresh DB ---------------------------------

// TestMigrate_FreshDB_LandsAtMaxKnownVersion verifies that calling
// audit.Migrate on a brand-new SQLite file lands at MaxKnownSchemaVersion
// (i.e. the highest version this binary supports).
func TestMigrate_FreshDB_LandsAtMaxKnownVersion(t *testing.T) {
	db, _ := openTempDB(t, "fresh.db")

	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate on fresh DB: %v", err)
	}

	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		t.Fatalf("SELECT MAX(version) FROM audit_schema_meta: %v", err)
	}
	if version != audit.MaxKnownSchemaVersion {
		t.Errorf("schema version after Migrate = %d, want %d", version, audit.MaxKnownSchemaVersion)
	}
}

// TestMigrate_AppliedAtIsRecent verifies the seeded applied_at column is
// a sane Unix nanosecond timestamp close to wall-clock now.
func TestMigrate_AppliedAtIsRecent(t *testing.T) {
	db, _ := openTempDB(t, "applied_at.db")

	before := time.Now().UTC().UnixNano()
	if err := audit.Migrate(db); err != nil {
		t.Fatalf("audit.Migrate: %v", err)
	}
	after := time.Now().UTC().UnixNano()

	var appliedAt int64
	if err := db.QueryRow(`SELECT applied_at FROM audit_schema_meta WHERE version=2`).Scan(&appliedAt); err != nil {
		t.Fatalf("SELECT applied_at: %v", err)
	}

	if appliedAt < before || appliedAt > after {
		t.Errorf("applied_at = %d, want in [%d, %d]", appliedAt, before, after)
	}
}

// ---- Idempotency -----------------------------------------------------------

// TestMigrate_Idempotent_SecondCallNoOp verifies that calling Migrate
// twice in a row does NOT add duplicate audit_schema_meta rows: the row
// count after the second call equals the count after the first call.
// Idempotency is required for the auto-on-open path: every
// NewSqliteAuditTrail open re-runs Migrate.
//
// (One row per applied step from v1 to MaxKnownSchemaVersion, so the first
// Migrate seeds MaxKnownSchemaVersion-1 rows; the second is a no-op.)
func TestMigrate_Idempotent_SecondCallNoOp(t *testing.T) {
	db, _ := openTempDB(t, "idempotent.db")

	if err := audit.Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	var firstRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_schema_meta`).Scan(&firstRows); err != nil {
		t.Fatalf("first COUNT(*): %v", err)
	}

	if err := audit.Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var secondRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_schema_meta`).Scan(&secondRows); err != nil {
		t.Fatalf("second COUNT(*): %v", err)
	}
	if firstRows != secondRows {
		t.Errorf("audit_schema_meta row count changed across re-Migrate: first=%d, second=%d (want stable)",
			firstRows, secondRows)
	}
	wantRows := audit.MaxKnownSchemaVersion - 1
	if firstRows != wantRows {
		t.Errorf("audit_schema_meta row count after first Migrate = %d, want %d (one row per step from v1 to v%d)",
			firstRows, wantRows, audit.MaxKnownSchemaVersion)
	}
}

// TestMigrate_Idempotent_AppliedAtUnchanged verifies that the second
// Migrate call (which is a no-op) does not overwrite the applied_at
// timestamp set by the first call. This proves the v1→v2 step uses
// INSERT OR IGNORE (not REPLACE) and respects existing data.
func TestMigrate_Idempotent_AppliedAtUnchanged(t *testing.T) {
	db, _ := openTempDB(t, "applied_at_stable.db")

	if err := audit.Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	var firstApplied int64
	if err := db.QueryRow(`SELECT applied_at FROM audit_schema_meta WHERE version=2`).Scan(&firstApplied); err != nil {
		t.Fatalf("first applied_at: %v", err)
	}

	// Sleep so a re-INSERT (if it incorrectly fired) would have a
	// distinguishable timestamp.
	time.Sleep(2 * time.Millisecond)

	if err := audit.Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var secondApplied int64
	if err := db.QueryRow(`SELECT applied_at FROM audit_schema_meta WHERE version=2`).Scan(&secondApplied); err != nil {
		t.Fatalf("second applied_at: %v", err)
	}

	if secondApplied != firstApplied {
		t.Errorf("applied_at changed after no-op Migrate: first=%d, second=%d", firstApplied, secondApplied)
	}
}

// ---- Nil DB rejection ------------------------------------------------------

// TestMigrate_NilDB_StructuredError verifies the actionable error returned
// when a caller passes a nil *sql.DB. CategoryStorage per PROPOSAL-2 §7.10.5.
func TestMigrate_NilDB_StructuredError(t *testing.T) {
	err := audit.Migrate(nil)
	if err == nil {
		t.Fatal("Migrate(nil) returned nil, want *StructuredError")
	}
	var se *pasterrors.StructuredError
	if !stderrors.As(err, &se) {
		t.Fatalf("Migrate(nil) returned %T, want *pasterrors.StructuredError", err)
	}
	if se.Category != pasterrors.CategoryStorage {
		t.Errorf("Category = %q, want %q", se.Category, pasterrors.CategoryStorage)
	}
}
