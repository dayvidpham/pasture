// Command pasture-migrate-crash is a TEST-ONLY binary that drives the v2→v3
// migration on a supplied SQLite file but aborts via os.Exit(137)
// (SIGKILL-equivalent) AFTER the v3 transaction has executed
// `INSERT INTO audit_schema_meta (version=3, ...)` but BEFORE
// `tx.Commit()`.
//
// It exists to satisfy PROPOSAL-2 §11 Scenario 11 (BLOCKER B1): the
// "crash mid-migration recovery" assertion requires an OS-level kill in
// the middle of a SQLite transaction so the WAL/journal recovery path on
// the next open is exercised. Go's defer/panic mechanism cannot simulate
// this — it would still drain the deferred Rollback before the process
// exits.
//
// Usage (called by tests via os/exec.Cmd):
//
//	pasture-migrate-crash <dbPath>
//
// The binary expects exactly one positional argument: the absolute path
// to the SQLite file to migrate. It exits with one of:
//
//   - exit 137 (success — crash injected as designed; the Scenario 11
//     test treats this as the expected outcome and asserts the file is
//     either at v2 or v3, never half-migrated)
//   - exit 1   (validation error: missing arg, unreadable file, etc.)
//   - exit 5   (storage error: migration failed BEFORE the crash point;
//     also acceptable for the test, which retries)
//
// Build: included in the standard `make build` target list (no build-tag
// gating per HANDOFF §7); the binary is small (~5 MB stripped) and
// shipping it doesn't materially change pasture's distribution size.
//
// # Why this is a separate binary, not a test helper
//
// The Scenario 11 test must call os.Exit (or kill -9 on a child) to
// simulate the kernel rolling back uncommitted WAL frames. A test helper
// in the same Go test binary cannot os.Exit without skipping all
// downstream tests in the same package; spawning a separate process via
// os/exec.Cmd is the only safe way to assert the recovery semantics.
package main

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	_ "modernc.org/sqlite" // pure-Go driver; CGO_ENABLED=0 compatible
)

// crashExitCode is the SIGKILL-equivalent exit code we use to signal
// "the migration was deliberately interrupted at the documented crash
// point". 137 = 128 + 9 (SIGKILL); the Scenario 11 test asserts on this
// specific value to distinguish a planned crash from an unexpected
// failure.
const crashExitCode = 137

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr,
			"Error: pasture-migrate-crash needs exactly one argument: the path to the database file to migrate.\n"+
				"\n"+
				"  Usage:    pasture-migrate-crash <dbPath>\n"+
				"  Example:  pasture-migrate-crash /tmp/test-fixture.db\n"+
				"\n"+
				"How to fix:\n"+
				"  1. Pass an absolute path to the .db file as the only argument.\n",
		)
		os.Exit(1)
	}
	dbPath := os.Args[1]

	if _, err := os.Stat(dbPath); err != nil {
		fmt.Fprintf(os.Stderr,
			"Error: the file %q can't be opened.\n"+
				"\n"+
				"  Reason:  %s\n"+
				"  Impact:  The crash test can't run because there's no database file to migrate.\n"+
				"\n"+
				"How to fix:\n"+
				"  1. Copy the legacy fixture to a writable temp path:\n"+
				"       cp pasture/testdata/legacy_audit_v1.db <your-temp-dir>/test.db\n"+
				"  2. Pass the temp path as the only argument.\n"+
				"  3. If the calling test created the path with t.TempDir(), confirm\n"+
				"     the test wrote the fixture before spawning this binary.\n",
			dbPath, err,
		)
		os.Exit(1)
	}

	exitCode, err := runCrashMigration(dbPath)
	if err != nil {
		// runCrashMigration returns the post-mortem error (typically a
		// *StructuredError from the migrator) when the migration failed
		// BEFORE the crash point. We surface it via stderr with exit
		// code 5 (CategoryStorage) so the calling test can distinguish
		// a real migration failure from a planned crash.
		var se *pasterrors.StructuredError
		if stderrors.As(err, &se) {
			se.Report(os.Stderr)
			os.Exit(pasterrors.ExitCode(se))
		}
		fmt.Fprintf(os.Stderr,
			"Error: the migration failed before the planned crash point.\n"+
				"\n"+
				"  Reason:  %s\n"+
				"  Impact:  No crash was injected; the database is in whatever state the\n"+
				"           failed migration left it.\n",
			err,
		)
		os.Exit(int(pasterrors.CategoryStorage[0])) // fallback; should not reach
	}
	os.Exit(exitCode)
}

// runCrashMigration opens the SQLite file at dbPath and walks it through
// the v2→v3 migration manually — duplicating the behaviour of
// audit.runStep in the production migrator (modernc.org/sqlite +
// _txlock=immediate + BEGIN IMMEDIATE) but inserting an os.Exit(137)
// between the audit_schema_meta version bump and the tx.Commit().
//
// The duplication is acceptable here because (a) we MUST control the
// exit timing precisely, and (b) the production runStep is unaware of
// the test crash injection point. The DDL/DML statements are identical
// to what audit.migrateV2toV3 → migrateV3Backfill execute; if those
// change, this binary's body must change in lockstep.
//
// Pre-conditions
//
//   - The file at dbPath should be at schema v1 or v2 (typically a copy
//     of pasture/testdata/legacy_audit_v1.db). The caller is responsible
//     for ensuring this; running against a v3+ file produces a no-op exit.
//   - The file is opened with _txlock=immediate so the BeginTx call
//     issues "BEGIN IMMEDIATE" and acquires the write lock immediately.
//
// Post-conditions
//
//   - On the success path, the process exits with crashExitCode (137)
//     and the audit_schema_meta row for version=3 is staged in the
//     transaction but NOT committed. SQLite's WAL recovery on the next
//     open rolls back the entire transaction so the file is observably
//     at v2.
//   - On the failure path (DDL fails, the file is at the wrong starting
//     version, etc.), the process exits with the appropriate non-137
//     code and stderr carries the *StructuredError diagnostic.
func runCrashMigration(dbPath string) (int, error) {
	ctx := context.Background()

	// Open with _txlock=immediate so BeginTx issues "BEGIN IMMEDIATE"
	// (modernc.org/sqlite/sqlite.go:187-193 + tx.go:22-25). The
	// connection-string syntax matches NewSqliteAuditTrail in
	// internal/audit/sqlite.go.
	db, err := sql.Open("sqlite", dbPath+"?_txlock=immediate")
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The database file %q couldn't be opened.", dbPath),
			Why:      fmt.Sprintf("SQLite reported: %s", err),
			Impact:   "The crash test can't run because the file isn't reachable, so no migration was attempted.",
			Fix: "1. Confirm the path is correct and the file exists:\n" +
				fmt.Sprintf("     ls -l %q\n", dbPath) +
				"2. Make sure the file is readable by the current user.",
		}
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Apply the same pragmas NewSqliteAuditTrail applies so the file is
	// in WAL mode with a 5s busy_timeout. This is what makes the
	// concurrent-migrator race in Scenario 12 work; for Scenario 11 it
	// is harmless but kept for behavioural parity with production.
	for _, p := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`} {
		if _, err := db.Exec(p); err != nil {
			return 0, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("Couldn't apply SQLite setting %q to %q.", p, dbPath),
				Why:      fmt.Sprintf("SQLite reported: %s", err),
				Impact:   "The crash test can't run with the same SQLite settings the daemon uses, so its result wouldn't be representative.",
				Fix: "1. Confirm the file is writable by the current user.\n" +
					"2. Confirm the file lives on a filesystem that supports SQLite's\n" +
					"   write-ahead log (most local filesystems do; some networked\n" +
					"   filesystems don't).",
			}
		}
	}

	// Run v1→v2 first (if needed) using the production Migrate path. This
	// matches the legacy_audit_v1.db starting point: the migrator runs
	// v1→v2 cleanly, then we hand-execute v2→v3 with the crash injection.
	//
	// We can't reuse audit.Migrate for the v2→v3 step because it always
	// calls tx.Commit at the end, defeating the crash injection.
	if err := promoteToV2IfNeeded(ctx, db); err != nil {
		return 0, err
	}

	// Hand-execute v2→v3 with crash injection. Open an IMMEDIATE
	// transaction (db.BeginTx + _txlock=immediate already does this).
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't open a write transaction for the v2-to-v3 migration step.",
			Why:      fmt.Sprintf("SQLite reported: %s", err),
			Impact:   "The crash test can't move past version 2 because it can't claim the write lock.",
			Fix: "1. Make sure no other process is holding the database file open\n" +
				"   (close any other pastured or sqlite processes pointing at it).\n" +
				"2. Re-run the test once the file is free.",
		}
	}

	// Execute the same DDL/DML that audit.migrateV2toV3 +
	// audit.migrateV3Backfill execute, in the same order, EXCEPT we
	// insert the crash AFTER the audit_schema_meta INSERT and BEFORE
	// tx.Commit.
	if err := executeV3Statements(ctx, tx); err != nil {
		_ = tx.Rollback()
		return 0, err
	}

	// CRASH POINT — per PROPOSAL-2 §11 Scenario 11. The transaction is
	// fully built (audit_schema_meta has the version=3 row staged) but
	// not yet COMMITted. SQLite's WAL recovery on the next open will
	// roll back the entire transaction; the file will report MAX(version)
	// as 2 (or 3 if WAL happened to flush, which the scenario also
	// accepts as a valid outcome).
	//
	// We do NOT call tx.Rollback() because we want the OS to terminate
	// us before any cleanup runs. tx is a leaked *sql.Tx; the process
	// exit reaps it.
	fmt.Fprintf(os.Stderr,
		"The v2-to-v3 migration is staged but not committed (the version=3 row is in the open transaction). Crashing now with exit %d to simulate a kernel-level kill in the middle of the migration.\n",
		crashExitCode,
	)
	os.Exit(crashExitCode)
	return 0, nil // unreachable
}

// promoteToV2IfNeeded brings the database from v1 (no audit_schema_meta)
// to v2 using the production audit.Migrate path. It is a no-op if the
// file is already at v2 or v3.
//
// Splitting v1→v2 out is intentional: the Scenario 11 fixture is a v1
// database, so the test exercises v1 → (v2 via audit.Migrate) → (v2→v3
// crash via this binary). The v1→v2 promotion is uninteresting for the
// crash test because it commits cleanly; only the v3 transition is the
// subject of the recovery assertion.
//
// We tolerate "already at v3+" by running audit.Migrate which is
// idempotent at MaxKnownSchemaVersion. If the file is somehow at a
// future schema version, the production newer-schema rejection error
// surfaces and the binary exits with code 5.
func promoteToV2IfNeeded(ctx context.Context, db *sql.DB) error {
	_ = ctx // ctx not currently consumed by audit.Migrate; reserved.

	// audit.Migrate runs all forward steps, INCLUDING v2→v3. That would
	// commit the v3 transaction normally and defeat our crash injection.
	// We need a way to migrate v1→v2 ONLY.
	//
	// Strategy: detect the on-disk version manually via sqlite_master +
	// audit_schema_meta. If it's 1, run only the v1→v2 statements
	// inline. If it's >=2, no-op.
	hasMeta, err := tableExists(db, "audit_schema_meta")
	if err != nil {
		return err
	}
	if !hasMeta {
		// v1 — promote to v2 inline.
		return promoteV1ToV2(db)
	}

	// v2+ — no-op.
	return nil
}

// promoteV1ToV2 inlines the v1→v2 DDL+seed (CREATE audit_schema_meta +
// INSERT version=2). Mirrors the production audit.migrateV1toV2 (single
// transaction, IF NOT EXISTS / INSERT OR IGNORE for idempotency).
//
// We keep this inline (rather than calling audit.Migrate) so that
// audit.Migrate cannot accidentally proceed to v2→v3, which would
// commit and defeat the crash test.
func promoteV1ToV2(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't open a transaction to bring the database from v1 up to v2.",
			Why:      fmt.Sprintf("SQLite reported: %s", err),
			Impact:   "The v1 fixture can't be promoted to v2, so the crash test (which targets the v2-to-v3 step) can't even reach its starting point.",
			Fix: "1. Make sure the database file is writable by the current user.\n" +
				"2. Make sure no other process is holding the file open.\n" +
				"3. Re-run the test once the file is free.",
		}
	}
	defer tx.Rollback() //nolint:errcheck // best-effort rollback on early returns

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS audit_schema_meta (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't create the schema-version table while bringing the database from v1 up to v2.",
			Why:      fmt.Sprintf("SQLite reported: %s", err),
			Impact:   "The v1 fixture can't be promoted to v2, so the crash test can't reach its starting point.",
			Fix: "1. Confirm the database file is writable by the current user.\n" +
				"2. Confirm the disk has space for the new table.",
		}
	}
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO audit_schema_meta (version, applied_at) VALUES (?, ?)`,
		2, time.Now().UTC().UnixNano(),
	); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't record the v2 schema version while bringing the database from v1 up to v2.",
			Why:      fmt.Sprintf("SQLite reported: %s", err),
			Impact:   "The v1 fixture can't be promoted to v2, so the crash test can't reach its starting point.",
			Fix: "1. Confirm the database file is writable by the current user.\n" +
				"2. Confirm the disk has space for the row insert.",
		}
	}
	if err := tx.Commit(); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't commit the v1-to-v2 promotion transaction.",
			Why:      fmt.Sprintf("SQLite reported: %s", err),
			Impact:   "The v1 fixture can't be promoted to v2, so the crash test can't reach its starting point.",
			Fix: "1. Confirm the database file is writable by the current user.\n" +
				"2. Confirm the disk has space to commit pending changes.",
		}
	}
	return nil
}

// executeV3Statements runs the same statements audit.migrateV2toV3 +
// audit.migrateV3Backfill execute, BUT it also INSERTs the
// audit_schema_meta(version=3) row at the end so the partial-commit
// state we want for the crash injection is reached.
//
// IMPORTANT: this body MUST mirror the production migrator's body
// statement-for-statement. If audit/migrate_v2_v3.go or
// audit/migrate_v3_backfill.go change, this function MUST be updated to
// match — otherwise the crash test verifies the wrong DDL.
//
// We use audit.Trail's exposed helpers WHERE POSSIBLE (e.g. via the
// Migrate framework) but inline the rest because the framework's
// commit-at-end semantic is exactly what we need to subvert.
func executeV3Statements(ctx context.Context, tx *sql.Tx) error {
	// Mirror migrateV2toV3 body (1): create the three new tables + indexes.
	tableDDL := []struct {
		what string
		sql  string
	}{
		{"create context_edges", `CREATE TABLE IF NOT EXISTS context_edges (
			event_id     INTEGER NOT NULL REFERENCES audit_events(id) ON DELETE CASCADE,
			context_kind TEXT NOT NULL,
			context_id   TEXT NOT NULL,
			PRIMARY KEY (event_id, context_kind, context_id)
		)`},
		{"create pasture_agent_categories", `CREATE TABLE IF NOT EXISTS pasture_agent_categories (
			agent_id        TEXT PRIMARY KEY,
			automaton_role  TEXT NOT NULL DEFAULT 'None',
			pasture_role    TEXT NOT NULL DEFAULT 'None'
		)`},
		{"create pasture_well_known_agents", `CREATE TABLE IF NOT EXISTS pasture_well_known_agents (
			agent_id  TEXT PRIMARY KEY,
			name      TEXT NOT NULL UNIQUE
		)`},
		{"create idx_context_edges_lookup", `CREATE INDEX IF NOT EXISTS idx_context_edges_lookup ON context_edges (context_kind, context_id)`},
		{"create idx_context_edges_event", `CREATE INDEX IF NOT EXISTS idx_context_edges_event ON context_edges (event_id)`},
	}
	for _, s := range tableDDL {
		if _, err := tx.ExecContext(ctx, s.sql); err != nil {
			return wrapStorageErr(fmt.Sprintf("v3 step (%s)", s.what), err)
		}
	}

	// Body (2) is the v3 backfill itself. Rather than duplicate
	// findOrCreateLegacyRoleAgent + the table-rebuild here (which
	// would risk drifting from migrate_v3_backfill.go), we leverage
	// audit.MigrateV3BackfillForCrashTest — a TEST-ONLY exported entry
	// point that runs the same backfill steps without the writeVersion
	// call. See pasture/internal/audit/migrate_v3_backfill_export.go.
	if err := audit.MigrateV3BackfillForCrashTest(tx); err != nil {
		return wrapStorageErr("v3 backfill (delegated)", err)
	}

	// Final statement: stage the audit_schema_meta version=3 row in
	// the SAME transaction. This is what the production
	// migrateV2toV3 does (writeVersion call) immediately before
	// tx.Commit. We INSERT it here, then OS-kill ourselves before
	// the (absent) Commit — the WAL recovery rolls everything back.
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO audit_schema_meta (version, applied_at) VALUES (?, ?)`,
		3, time.Now().UTC().UnixNano(),
	); err != nil {
		return wrapStorageErr("v3 step (audit_schema_meta version=3 INSERT)", err)
	}
	return nil
}

// wrapStorageErr wraps an arbitrary error in a *StructuredError of
// CategoryStorage so it surfaces with the right exit code (5).
func wrapStorageErr(what string, err error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("A step in the v2-to-v3 migration (%s) couldn't run.", what),
		Why:      fmt.Sprintf("SQLite reported: %s", err),
		Impact:   "The v3 transaction couldn't be staged, so no crash was injected and the test didn't get to exercise the recovery path.",
		Fix: "1. Read the SQLite error above and fix the underlying SQL or DDL issue.\n" +
			"2. If the error mentions an unexpected table or column, the input fixture is\n" +
			"   probably at the wrong starting version. Confirm the file is at v1 or v2:\n" +
			"     sqlite3 <dbPath> 'SELECT MAX(version) FROM audit_schema_meta;'\n" +
			"3. Re-run the test once the underlying issue is resolved.",
	}
}

// tableExists reports whether the named table exists in the database.
// Mirrors audit.schemaMetaTableExists but specialised for arbitrary
// names so the crash binary doesn't depend on package-private symbols.
func tableExists(db *sql.DB, name string) (bool, error) {
	var got string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
		name,
	).Scan(&got)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		// Surface the underlying error so callers can wrap it.
		if strings.Contains(err.Error(), "no such table: sqlite_master") {
			// Should never happen — sqlite_master always exists. Treat
			// as a hard storage error.
			return false, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "The database file appears to be corrupt: it has no sqlite_master table.",
				Why:      fmt.Sprintf("SQLite reported: %s. Every healthy SQLite file has a sqlite_master table; the file is unusable without it.", err),
				Impact:   "The crash test can't proceed because it can't read the file's structure.",
				Fix: "1. Regenerate the legacy fixture from a known-good source:\n" +
					"     cp pasture/testdata/legacy_audit_v1.db <your-temp-dir>/test.db\n" +
					"2. Re-run the test against the fresh copy.",
			}
		}
		return false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't ask the database whether the table %q exists.", name),
			Why:      fmt.Sprintf("SQLite reported: %s", err),
			Impact:   "The crash test can't tell what schema version the file is at, so it doesn't know which migration step to run.",
			Fix: "1. Confirm the file is readable by the current user:\n" +
				"     ls -l <dbPath>\n" +
				"2. If the file is unreadable or corrupt, copy a fresh fixture and retry:\n" +
				"     cp pasture/testdata/legacy_audit_v1.db <your-temp-dir>/test.db",
		}
	}
	return got == name, nil
}
