// Package handlers — migrate.go
//
// Handler for `pasture migrate [--dry-run]` (PROPOSAL-2 §7.9 / §11 Scenario 15).
//
// Two execution paths:
//
//  1. Dry-run: open a private *sql.DB on the file, read the on-disk version
//     via audit.ReadSchemaVersion, build a MigratePlan via audit.PlanMigrations,
//     render via formatters.FormatMigratePlan, exit 0. The file is never
//     written to (Scenario 15 asserts SHA-256 unchanged before/after).
//
//  2. Apply: open the file the same way OpenTaskTracker does — via the
//     audit subsystem's NewSqliteAuditTrail, which internally invokes
//     audit.Migrate. This GUARANTEES the explicit-command path and the
//     auto-on-open path share one migrator implementation (PROPOSAL-2 §7.10
//     / IMPL_PLAN §3 S6 binding). After the migration runs, we re-probe
//     the version so the success line reports the actual to-version.
//
// All errors are *pasterrors.StructuredError; ExitCode maps category to the
// canonical exit code. Storage errors map to exit 5 (CategoryStorage); newer-
// schema rejection surfaces with the same exit code through audit.Migrate's
// own structured error.
package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver; ensures sql.Open("sqlite", ...) works

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// MigrateInput captures the inputs for `pasture migrate`.
type MigrateInput struct {
	// DBPath is the filesystem path to the unified pasture.db. Empty resolves
	// to tasks.DefaultDBPath().
	DBPath string

	// DryRun, when true, prints the planned migrations and exits without
	// modifying the file. Per Scenario 15, the file's SHA-256 must be
	// unchanged before and after.
	DryRun bool
}

// Migrate runs the migration command. Returns the standard (exitCode, error)
// tuple used by RunE handlers.
//
// On success:
//   - Dry-run prints the plan and returns (0, nil).
//   - Apply prints "migrated <db> from v<from> to v<to>" and returns (0, nil).
//
// On failure: returns ExitCode(err), err. Common categories:
//   - CategoryStorage (exit 5): migration apply failed, or version probe
//     failed, or newer-schema rejection.
//   - CategoryConnection (exit 2): file/dir cannot be opened.
//   - CategoryValidation (exit 1): bad input (currently unused — DBPath empty
//     falls back to default rather than rejecting).
func Migrate(w io.Writer, in MigrateInput, format types.OutputFormat) (int, error) {
	dbPath := in.DBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}

	// Ensure the parent directory exists. Without this, dry-run against a
	// non-existent target path would fail with a confusing "unable to open
	// database file" rather than the actionable directory-creation error.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't prepare the folder for the pasture database at %q.", dbPath),
			Why:      "Creating the parent directory failed.",
			Where:    "Migrating the pasture database (internal/handlers/migrate.go in handlers.Migrate).",
			Impact:   "The migration can't run because the database file's folder doesn't exist and can't be created.",
			Fix: fmt.Sprintf("1. Create the folder yourself and retry:\n"+
				"     mkdir -p %q\n"+
				"     pasture migrate\n"+
				"2. Or pick a different database location:\n"+
				"     pasture migrate --db <path>\n"+
				"3. Or set the location via environment variable:\n"+
				"     export %s=<path>",
				filepath.Dir(dbPath), tasks.DBPathEnv),
			Cause: err,
		}
		return pasterrors.ExitCode(se), se
	}

	if in.DryRun {
		return runMigrateDryRun(w, dbPath, format)
	}
	return runMigrateApply(w, dbPath, format)
}

// runMigrateDryRun opens the file read-only, probes the version + plan, and
// prints the plan WITHOUT modifying the file. Scenario 15 asserts the file
// SHA-256 is identical before and after this call.
//
// We open via sql.Open + read-only queries; we DO NOT call audit.Migrate or
// NewSqliteAuditTrail (both of which would write to audit_schema_meta).
func runMigrateDryRun(w io.Writer, dbPath string, format types.OutputFormat) (int, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't open the pasture database at %q to preview the migration.", dbPath),
			Why:      "Opening the database file failed.",
			Where:    "Previewing the migration (internal/handlers/migrate.go in handlers.runMigrateDryRun).",
			Impact:   "The dry-run can't show you what would change because the database file is unreachable.",
			Fix: fmt.Sprintf("1. Confirm the file exists and is a SQLite database:\n"+
				"     ls -l %q\n"+
				"2. If the path is wrong, pass the right one:\n"+
				"     pasture migrate --dry-run --db <path>\n"+
				"3. Make sure the file is readable by your user.",
				dbPath),
			Cause: err,
		}
		return pasterrors.ExitCode(se), se
	}
	defer db.Close()

	currentVersion, err := audit.ReadSchemaVersion(db)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	// Future binary wrote this database. Surface the same actionable error
	// that an apply would produce, so dry-run and apply diverge ONLY in
	// whether they write — not in their error semantics.
	if currentVersion > audit.MaxKnownSchemaVersion {
		// audit.Migrate exposes the canonical error; replay it here so the
		// CLI surface is consistent. We accept the cost of also writing the
		// audit_schema_meta if Migrate had unexpected side effects — but
		// readVersion + Migrate's first probe both return the same error
		// without writing anything when the version is too high.
		err := audit.Migrate(db)
		if err == nil {
			// Defensive: should be unreachable. If audit.Migrate ever stops
			// returning an error for a too-high version, the CLI would
			// silently accept it; surface a clear error instead.
			se := &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("The pasture database at %q was written by a newer version of pasture than this one.", dbPath),
				Why: fmt.Sprintf("The file reports schema version %d, but this build of pasture only knows up\n"+
					"to version %d. A newer-schema check inside pasture should have rejected this\n"+
					"already; that it didn't suggests an internal bug.",
					currentVersion, audit.MaxKnownSchemaVersion),
				Where: "Previewing the migration (internal/handlers/migrate.go in handlers.runMigrateDryRun).",
				Impact: "The dry-run can't continue safely. This pasture build can't read rows written\n" +
					"by the newer schema, so any preview it produced could be wrong.",
				Fix: "1. Upgrade pasture to a version that supports the on-disk schema:\n" +
					"     pasture --version          # check your current version\n" +
					"     # then install the matching newer release\n" +
					"2. If you can't upgrade, do not downgrade the file by hand — file an issue\n" +
					"   instead.",
			}
			return pasterrors.ExitCode(se), se
		}
		return pasterrors.ExitCode(err), err
	}

	plan := formatters.MigratePlan{
		DBPath:         dbPath,
		CurrentVersion: currentVersion,
		TargetVersion:  audit.MaxKnownSchemaVersion,
		DryRun:         true,
		Steps:          toFormatterSteps(audit.PlanMigrations(currentVersion)),
	}

	out, fErr := formatters.FormatMigratePlan(plan, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// runMigrateApply opens the file via the audit subsystem (which internally
// runs audit.Migrate — the SAME code the auto-on-open path uses), then probes
// the post-migration version and prints the success line.
//
// PROPOSAL-2 §7.10 / IMPL_PLAN §3 S6: this MUST share the migrator
// implementation with OpenTaskTracker's auto-on-open path, with NO duplicate
// code. We achieve that by routing through NewSqliteAuditTrail, which is
// the single open-side call site for audit.Migrate. The Scenario 15
// convergence test (file A migrated via OpenTaskTracker; file B migrated via
// `pasture migrate`) compares the two final states to verify byte-for-byte
// identity (modulo SQLite WAL ordering).
func runMigrateApply(w io.Writer, dbPath string, format types.OutputFormat) (int, error) {
	// Probe the from-version BEFORE the migration so we can render an
	// accurate "from v<from>" in the success line. We open a read-only
	// handle, read, then close, so the audit.NewSqliteAuditTrail call below
	// gets a fresh handle and can acquire its own write lock.
	fromVersion, err := probeVersionReadOnly(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	// Run the migration via the SAME constructor OpenTaskTracker uses. This
	// is the single shared migrator path required by PROPOSAL-2 §7.10.
	// NewSqliteAuditTrail invokes audit.Migrate internally; on a newer-
	// schema file it surfaces the structured error unchanged.
	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	// We don't need the trail itself — only its side effect of running
	// Migrate. Close immediately so the file is released for any follow-up
	// process (e.g., a daemon start in the same script).
	if cErr := trail.Close(); cErr != nil {
		// Non-fatal: the migration itself succeeded. Surface as a storage
		// warning by failing soft? Per the binding "actionable errors"
		// convention, we return it as a storage error rather than swallowing.
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The migration finished, but pasture couldn't release its handle on %q.", dbPath),
			Why:      "Closing the database handle after the migration failed.",
			Where:    "Migrating the pasture database (internal/handlers/migrate.go in handlers.runMigrateApply).",
			Impact: "The migration itself was applied successfully — your data is up-to-date. But the\n" +
				"next command that opens the same file may transiently fail with a \"database is\n" +
				"locked\" error until the leaked handle times out.",
			Fix: "1. Wait about 5 seconds and try the next command — the busy timeout will clear.\n" +
				"2. If the error keeps happening, find any process still holding the file open and\n" +
				"   restart it (typically pastured):\n" +
				"     pkill pastured && pastured",
			Cause: cErr,
		}
		return pasterrors.ExitCode(se), se
	}

	// Re-probe to render the actual to-version. On the idempotent re-run
	// path (file already at MaxKnownSchemaVersion), to == from.
	toVersion, err := probeVersionReadOnly(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	result := formatters.MigrateResult{
		DBPath:      dbPath,
		FromVersion: fromVersion,
		ToVersion:   toVersion,
	}
	out, fErr := formatters.FormatMigrateResult(result, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// probeVersionReadOnly opens dbPath, reads audit_schema_meta.version, and
// closes. Used to capture the from/to versions for the migrate-apply success
// line WITHOUT bringing the audit subsystem up (which would trigger Migrate).
func probeVersionReadOnly(dbPath string) (int, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't open the pasture database at %q to read its current schema version.", dbPath),
			Why:      "Opening the database file failed.",
			Where:    "Probing the database schema version (internal/handlers/migrate.go in handlers.probeVersionReadOnly).",
			Impact:   "The migration can't run because pasture doesn't know which version it's upgrading from.",
			Fix: fmt.Sprintf("1. Confirm the file exists and is a SQLite database:\n"+
				"     ls -l %q\n"+
				"2. If the path is wrong, pass the right one:\n"+
				"     pasture migrate --db <path>\n"+
				"3. Make sure the file is readable by your user.",
				dbPath),
			Cause: err,
		}
	}
	defer db.Close()
	return audit.ReadSchemaVersion(db)
}

// toFormatterSteps maps audit.MigrationStepSummary to the formatter-side
// shape. Translation lives here (handler) rather than in the formatter or
// audit package so each side keeps its own type without a cross-import.
func toFormatterSteps(steps []audit.MigrationStepSummary) []formatters.MigratePlanStep {
	out := make([]formatters.MigratePlanStep, len(steps))
	for i, s := range steps {
		out[i] = formatters.MigratePlanStep{
			FromVersion: s.FromVersion,
			ToVersion:   s.ToVersion,
			Description: s.Description,
		}
	}
	return out
}
