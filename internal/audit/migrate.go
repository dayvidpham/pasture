// Package audit — migrate.go
//
// Migrate is the public entry point for the audit-database schema
// migration framework introduced by PROPOSAL-2 (§7.10). It is invoked by
// NewSqliteAuditTrail on every open and (in later slices) by the
// `pasture migrate` CLI command — both paths share this single
// implementation, so behaviour is identical.
//
// Versioning model
//
//   - Version is stored in the audit_schema_meta table (see schema_meta.go).
//   - A database with no audit_schema_meta table is treated as version 1
//     (legacy, pre-PROPOSAL-2).
//   - MaxKnownSchemaVersion is the highest version this binary knows how to
//     produce. A database whose recorded version exceeds MaxKnownSchemaVersion
//     is rejected with an actionable *StructuredError (Scenario 5).
//
// Transactional guarantees
//
//   - Each forward step runs inside a single transaction acquired via
//     BEGIN IMMEDIATE so that a concurrent writer cannot interleave with
//     the migration.
//   - The audit_schema_meta version bump is the LAST statement in the
//     transaction, so a crash mid-migration leaves the database
//     observably at the previous version (rollback).
//
// Migration table
//
//   - v1 → v2: bootstrap audit_schema_meta (S1, landed).
//   - v2 → v3: new tables context_edges, pasture_agent_categories,
//     pasture_well_known_agents (S2, landed); audit_events.agent_id add +
//     role-backfill + role-drop triple (S3, landed).
//   - v3 → v4: EpochContext backfill from audit_events.epoch_id into
//     context_edges; audit_events.epoch_id column dropped (S4, landed —
//     migrate_v3_v4.go).
//   - v4 → v5: additive dedup_key TEXT column on audit_events + a partial
//     unique index over its non-NULL values, for engine exactly-once
//     (migrate_v4_v5.go).
//
// This binary tops out at v5. Future migrations extend the dispatch table in
// migrationSteps() below by appending a new step and bumping
// MaxKnownSchemaVersion.
package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// busyRetryCeiling caps the total wall-clock time the migrator will spend
// retrying BEGIN IMMEDIATE against a database held by a concurrent writer.
//
// Per PROPOSAL-2 §7.10.3, when a second pasture process opens the same db
// while the first is mid-migration, the second's BEGIN IMMEDIATE may queue
// behind the first within the SQLite-level busy_timeout=5000 ms (set in
// sqlite.go). If the first migration takes longer than that — possible for
// the v3 backfill against the 1024-row fixture under load — the retry loop
// here keeps trying with exponential backoff until either (a) the lock is
// released and we succeed, or (b) we hit this ceiling and surface the
// actionable Scenario 12 error.
const busyRetryCeiling = 30 * time.Second

// busyRetryInitialDelay is the first delay between BUSY retries; subsequent
// delays double up to busyRetryMaxDelay. Kept small so the common case
// (concurrent migrator finishes in <1s) doesn't introduce visible latency.
const busyRetryInitialDelay = 50 * time.Millisecond

// busyRetryMaxDelay caps the per-attempt sleep so we don't go silent for
// many seconds at the tail of the backoff curve.
const busyRetryMaxDelay = 2 * time.Second

// MaxKnownSchemaVersion is the highest schema version this binary can
// produce. Bumped by S2 (→3, landed) and S4 (→4, landed).
//
// Layer Integration Point owned by S1: any caller that needs to know "what
// version does my binary support?" reads this constant. The §11 Scenario 5
// newer-schema rejection error reports this value as the "supported
// version" — bumping it here automatically updates the assertion.
const MaxKnownSchemaVersion = 5

// migrationStep applies a single forward migration. Each step receives an
// open transaction (already holding the write lock via BEGIN IMMEDIATE)
// and a timestamp to use for the audit_schema_meta.applied_at column.
//
// Steps are responsible for:
//  1. Performing their schema/data changes inside the transaction.
//  2. Calling writeVersion(tx, <toVersion>, nowUnixNano) as the LAST
//     statement in the step so a mid-step crash rolls back atomically.
//
// Steps MUST NOT commit the transaction; the caller (Migrate) does that.
type migrationStep struct {
	fromVersion int
	toVersion   int
	apply       func(tx *sql.Tx, nowUnixNano int64) error
}

// migrationSteps is the ordered registry of forward migrations this binary
// knows how to apply. Future slices append additional steps here.
//
// Order MUST be ascending by fromVersion. Migrate iterates this slice and
// applies any step whose fromVersion equals the current on-disk version.
//
// Each step's apply function is responsible for performing its
// schema/data work AND calling writeVersion(toVersion, ...) as the LAST
// statement before returning nil. The migrate.runStep wrapper holds the
// BEGIN IMMEDIATE transaction and commits only on a nil return.
func migrationSteps() []migrationStep {
	return []migrationStep{
		{fromVersion: 1, toVersion: 2, apply: migrateV1toV2},
		{fromVersion: 2, toVersion: 3, apply: migrateV2toV3},
		{fromVersion: 3, toVersion: 4, apply: migrateV3toV4Step},
		{fromVersion: 4, toVersion: 5, apply: migrateV4toV5Step},
	}
}

// Migrate brings the audit database at db up to MaxKnownSchemaVersion. It
// is safe to call repeatedly: an already-current database is a no-op.
//
// Behaviour summary:
//
//   - If the database has no audit_schema_meta row yet (legacy v1), Migrate
//     runs forward steps starting from v1.
//   - If the database is already at MaxKnownSchemaVersion, Migrate returns
//     nil without opening a transaction.
//   - If the database is at a version higher than MaxKnownSchemaVersion
//     (a future binary wrote it), Migrate returns a *StructuredError with
//     Category=CategoryStorage. This is the "newer-schema rejection" path
//     asserted by §11 Scenario 5.
//
// Each step runs in its own BEGIN IMMEDIATE transaction so a crash between
// steps leaves the database at a consistent intermediate version.
//
// Layer Integration Point: this signature is owned by S1 and consumed by
// S5 (OpenTaskTracker calls it) and S6 (`pasture migrate` calls it).
// Callers should treat the returned error as a *pasterrors.StructuredError
// (use errors.As).
func Migrate(db *sql.DB) error {
	if db == nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "The audit database can't be migrated because no database connection was provided.",
			Why:      "The migrator was called without an open database — this is a wiring bug in the caller.",
			Where:    "Upgrading the audit database (internal/audit/migrate.go in audit.Migrate).",
			Impact:   "No migration can run, and the audit database can't be opened until the caller provides a valid handle.",
			Fix: "1. Open the audit database first, then pass the handle to the migrator.\n" +
				"2. If you're calling the migrator directly from a test or tool, make sure the database\n" +
				"   was opened successfully before invoking it.",
		}
	}

	currentVersion, err := readVersion(db)
	if err != nil {
		// readVersion already returns a *StructuredError.
		return err
	}

	// Newer-schema rejection (§7.10.4 / §11 Scenario 5).
	if currentVersion > MaxKnownSchemaVersion {
		return newerSchemaError(currentVersion, MaxKnownSchemaVersion)
	}

	// Already current — no work to do.
	if currentVersion == MaxKnownSchemaVersion {
		return nil
	}

	// Apply each forward step in order. Each runs in its own transaction so
	// a crash between steps leaves the file at the most recent fully-
	// committed version.
	for _, step := range migrationSteps() {
		if step.fromVersion < currentVersion {
			continue
		}
		if step.fromVersion != currentVersion {
			// Gap in the migration table: this means the registry skipped a
			// version. That's a programming error (someone added a v3→v4
			// step without a v2→v3 step), and is not recoverable at runtime.
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     fmt.Sprintf("The audit database is at version %d but no upgrade step starts there.", currentVersion),
				Why: fmt.Sprintf(
					"The next migration step expects to start at version %d, but the database is currently\n"+
						"at version %d. There's a gap in the upgrade sequence — the migration table\n"+
						"is missing the step needed to move forward from here.",
					step.fromVersion, currentVersion,
				),
				Where: "Upgrading the audit database (internal/audit/migrate.go in audit.Migrate).",
				Impact: "The audit database can't be upgraded to the version this build of pasture supports,\n" +
					"so it can't be opened until the gap is fixed.",
				Fix: "1. This is a pasture bug — the migration table is missing a step.\n" +
					"2. Pin to the previous version of pasture (the one that wrote this database) so you\n" +
					"   can keep working while the bug is fixed.\n" +
					"3. File an issue with this database's current version (the number above) and the\n" +
					"   version this binary expected.",
			}
		}

		if err := runStep(db, step); err != nil {
			// runStep returns *StructuredError already.
			return err
		}
		currentVersion = step.toVersion
		if currentVersion >= MaxKnownSchemaVersion {
			break
		}
	}

	return nil
}

// runStep executes a single migration step inside its own BEGIN IMMEDIATE
// transaction with busy-retry. The audit_schema_meta version bump
// (writeVersion call inside step.apply) MUST be the last statement in the
// transaction so that a crash before Commit rolls everything back
// atomically.
//
// # BEGIN IMMEDIATE acquisition
//
// modernc.org/sqlite supports `_txlock=immediate` as a connection-string
// parameter (see modernc.org/sqlite/sqlite.go:187-193 + tx.go:22-25); when
// set, the driver issues "BEGIN IMMEDIATE" instead of plain "BEGIN" inside
// db.Begin / db.BeginTx. NewSqliteAuditTrail opens its db with this
// parameter so the BeginTx call below yields a transaction that holds the
// write lock from the first statement onward.
//
// Without IMMEDIATE locking, a deferred BEGIN would let a concurrent writer
// sneak in between our version probe and our first write — the race
// PROPOSAL-2 §7.10.3 calls out and §11 Scenario 12 asserts against.
//
// Busy retry up to busyRetryCeiling (30s)
//
// Per PROPOSAL-2 §7.10.3, when two pastured processes start against the
// same v1 db simultaneously, the loser's BEGIN IMMEDIATE may exceed the
// SQLite-level busy_timeout=5000ms (set in sqlite.go) if the winner's
// migration is slow. Rather than fail-fast at 5s, we retry the BeginTx
// call with exponential backoff up to 30s total, then return the
// actionable Scenario 12 error.
//
// Concurrent-migrator no-op (Scenario 12 outcome 1)
//
// After we acquire the write lock, we re-read MAX(version) from
// audit_schema_meta. If a concurrent migrator advanced the file past our
// step's fromVersion while we were spinning, we roll back without
// changes; the outer Migrate loop will see the new version and exit
// cleanly (or pick up a later step).
func runStep(db *sql.DB, step migrationStep) error {
	ctx := context.Background()

	tx, err := beginImmediateWithRetry(ctx, db, step.fromVersion, step.toVersion)
	if err != nil {
		return err
	}
	// Best-effort rollback on any error path before Commit; a successful
	// Commit makes Rollback a no-op.
	defer func() { _ = tx.Rollback() }()

	// Re-check the on-disk version under the write lock. A concurrent
	// migrator that finished while we were spinning in busy-retry may have
	// already advanced the file past our fromVersion; in that case we
	// release the lock and let the outer Migrate loop pick up the new
	// version on its next iteration. Without this re-check, two racing
	// migrators would both run the v3 backfill, doubling agents_software
	// rows (PROPOSAL-2 §7.10.3 + §11 Scenario 12).
	currentVersion, err := readVersionInTx(ctx, tx)
	if err != nil {
		return err
	}
	if currentVersion >= step.toVersion {
		// Another process already completed this step. Release the lock
		// cleanly; the outer loop will observe the new version.
		return nil
	}
	if currentVersion != step.fromVersion {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"The audit database changed underneath us while we were waiting for write access (now at version %d).",
				currentVersion,
			),
			Why: fmt.Sprintf(
				"While this process was waiting for the database write lock to upgrade from version %d to %d,\n"+
					"another pasture process upgraded the database to version %d. That's not the version we\n"+
					"expected to find when the lock finally became available.",
				step.fromVersion, step.toVersion, currentVersion,
			),
			Where: "Upgrading the audit database (internal/audit/migrate.go in audit.runStep).",
			Impact: "This upgrade step can't continue because the database is no longer at the version it\n" +
				"was planning to upgrade from. The database is intact — only this attempt was abandoned.",
			Fix: "1. Re-run the migration so the upgrade path is recomputed from the current version:\n" +
				"     pasture migrate\n" +
				"2. If the database is already up to date, the rerun will be a no-op.",
		}
	}

	nowUnixNano := time.Now().UTC().UnixNano()
	if err := step.apply(tx, nowUnixNano); err != nil {
		// step.apply returns *StructuredError already.
		return err
	}

	if err := tx.Commit(); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What: fmt.Sprintf(
				"The upgrade from audit-database version %d to %d couldn't be saved.",
				step.fromVersion, step.toVersion,
			),
			Why:   "The database refused to commit the upgrade transaction.",
			Where: "Upgrading the audit database (internal/audit/migrate.go in audit.runStep).",
			Impact: "The upgrade was rolled back — your audit database is unchanged and stays at the\n" +
				"previous version. No data was lost; the upgrade simply didn't happen.",
			Fix: "1. Confirm the audit database file is writable and the disk has free space:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     df -h <path-to-audit.db>\n" +
				"2. Once the underlying problem is fixed, re-run the migration:\n" +
				"     pasture migrate",
			Cause: err,
		}
	}
	return nil
}

// beginImmediateWithRetry calls db.BeginTx with retry-on-busy semantics. It
// relies on the underlying *sql.DB having been opened with the
// modernc.org/sqlite `_txlock=immediate` parameter so the issued statement
// is "BEGIN IMMEDIATE" (NewSqliteAuditTrail enforces this; ad-hoc callers
// that open the db without that parameter will get plain BEGIN and lose
// the §7.10.3 race-safety guarantee).
//
// Returns the active *sql.Tx on success; the caller must Commit or
// Rollback. On total-timeout or permanent error, returns the
// PROPOSAL-2 §7.10.3 Scenario 12 *StructuredError.
func beginImmediateWithRetry(ctx context.Context, db *sql.DB, fromVersion, toVersion int) (*sql.Tx, error) {
	deadline := time.Now().Add(busyRetryCeiling)
	delay := busyRetryInitialDelay
	for {
		tx, err := db.BeginTx(ctx, nil)
		if err == nil {
			return tx, nil
		}
		if !isBusyError(err) {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't start the database write for the audit-database upgrade from version %d to %d.",
					fromVersion, toVersion,
				),
				Why:   "The database refused to start the upgrade transaction.",
				Where: "Upgrading the audit database (internal/audit/migrate.go in audit.beginImmediateWithRetry).",
				Impact: "The migration can't get exclusive write access to the database, so the upgrade\n" +
					"can't run. The database remains at its current version.",
				Fix: "1. Confirm the audit database file is readable and writable by this process:\n" +
					"     ls -l <path-to-audit.db>\n" +
					"2. If the file or its directory has restrictive permissions, fix them:\n" +
					"     chmod u+rw <path-to-audit.db>\n" +
					"3. Re-run the migration once the underlying problem is resolved:\n" +
					"     pasture migrate",
				Cause: err,
			}
		}
		if time.Now().After(deadline) {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "Another pasture process is already upgrading the audit database.",
				Why: fmt.Sprintf(
					"This process waited more than %s for write access to the audit database, but another\n"+
						"pasture or pastured process held it the whole time. That other process is upgrading\n"+
						"the database from version %d to %d, so we can't safely start the same upgrade in parallel.",
					busyRetryCeiling, fromVersion, toVersion,
				),
				Where: "Upgrading the audit database (internal/audit/migrate.go in audit.beginImmediateWithRetry).",
				Impact: "This process can't open the audit database until the other migration finishes.\n" +
					"No data was changed by this attempt — the wait simply timed out.",
				Fix: "1. Wait for the other pasture or pastured process to finish, then re-run:\n" +
					"     pasture migrate\n" +
					"2. If the other process is stuck, find and stop it:\n" +
					"     pgrep -fa 'pasture|pastured'\n" +
					"     kill <pid-of-stuck-process>\n" +
					"3. Once the lock is free, you can confirm the upgrade by listing agents:\n" +
					"     pasture task agents list",
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"The audit-database upgrade from version %d to %d was cancelled before it could start.",
					fromVersion, toVersion,
				),
				Why:   "The migration was cancelled by its caller before it could acquire the write lock.",
				Where: "Upgrading the audit database (internal/audit/migrate.go in audit.beginImmediateWithRetry).",
				Impact: "The upgrade was abandoned while waiting for write access. Your audit database is\n" +
					"unchanged — nothing was committed.",
				Fix: "1. Identify what cancelled the call (an interrupted CLI, a parent process timeout,\n" +
					"   a pastured shutdown signal) and let it clear.\n" +
					"2. Re-run the migration:\n" +
					"     pasture migrate",
				Cause: ctx.Err(),
			}
		case <-timer.C:
		}
		delay *= 2
		if delay > busyRetryMaxDelay {
			delay = busyRetryMaxDelay
		}
	}
}

// isBusyError reports whether err corresponds to a SQLITE_BUSY or
// SQLITE_LOCKED return code from modernc.org/sqlite. The driver does not
// expose typed sentinels for either, so we substring-match on the canonical
// message — the same approach used by tracker_race_test.go (PROPOSAL-2
// §10.3 race test) for symmetry.
func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "SQLITE_LOCKED") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

// readVersionInTx reads MAX(version) from audit_schema_meta inside the
// active transaction. Mirrors readVersion (which operates on a *sql.DB)
// but uses the supplied *sql.Tx so the read participates in the
// migrator's IMMEDIATE transaction without taking a second connection.
//
// Returns 1 (legacy v1) when the table is missing or empty — same
// semantics as readVersion — so callers don't need to special-case the
// bootstrap path.
func readVersionInTx(ctx context.Context, tx *sql.Tx) (int, error) {
	// Probe table existence first.
	var tableName string
	row := tx.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='audit_schema_meta'`)
	switch err := row.Scan(&tableName); {
	case err == sql.ErrNoRows:
		return 1, nil
	case err != nil:
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't re-check the audit database's version after taking the write lock.",
			Why:      "The database refused the query against its internal table catalog.",
			Where:    "Upgrading the audit database (internal/audit/migrate.go in audit.readVersionInTx).",
			Impact: "We can't safely confirm what version the database is at right now, so the upgrade\n" +
				"was abandoned to avoid corrupting it. The database itself is unchanged.",
			Fix: "1. Check the audit database file isn't corrupted:\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. If the file is healthy, re-run the migration:\n" +
				"     pasture migrate\n" +
				"3. If integrity_check reports problems, restore from a backup before retrying.",
			Cause: err,
		}
	}

	var version sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't read the audit database's current version after taking the write lock.",
			Why:      "The database refused the read of the schema-version table.",
			Where:    "Upgrading the audit database (internal/audit/migrate.go in audit.readVersionInTx).",
			Impact: "We can't tell whether another process already finished the upgrade we were about to\n" +
				"run, so the migration was abandoned to avoid duplicate work. The database is unchanged.",
			Fix: "1. Check the audit database file is readable:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. Re-run the migration:\n" +
				"     pasture migrate",
			Cause: err,
		}
	}
	if !version.Valid {
		return 1, nil
	}
	return int(version.Int64), nil
}

// newerSchemaError builds the *StructuredError returned when the database
// reports a schema version higher than MaxKnownSchemaVersion. The exact
// field values are asserted by §11 Scenario 5; do not change wording
// without updating that test.
func newerSchemaError(dbVersion, maxKnownVersion int) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What: fmt.Sprintf(
			"This audit database was written by a newer pasture (version %d) than this build supports (version %d).",
			dbVersion, maxKnownVersion,
		),
		Why: fmt.Sprintf(
			"The database file says it's at audit-database version %d, but this build of pasture only\n"+
				"knows how to read up to version %d. A newer pasture upgraded the file at some point.",
			dbVersion, maxKnownVersion,
		),
		Where: "Opening the audit database (internal/audit/migrate.go in audit.newerSchemaError).",
		Impact: "No audit events can be read or written through this build of pasture until you upgrade.\n" +
			"The database itself is fine — it's just newer than this binary understands.",
		Fix: fmt.Sprintf(
			"1. Upgrade pasture to a version that supports audit-database version %d:\n"+
				"     # install or switch to a newer pasture release\n"+
				"2. Or, if you need to keep using this build for now, pin back to the older pasture\n"+
				"   that wrote this database originally.\n"+
				"3. Do NOT downgrade the database file itself — there's no safe way to undo an upgrade.",
			dbVersion,
		),
	}
}
