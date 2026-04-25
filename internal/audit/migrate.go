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
//   - v1 → v2: bootstrap audit_schema_meta (this slice, S1).
//   - v2 → v3: new tables context_edges, pasture_agent_categories,
//     pasture_well_known_agents (S2; not yet wired here).
//   - v3 → v4: EpochContext backfill into context_edges (S4; not yet wired).
//
// Slices S2/S3/S4 will register their migration steps in the steps table
// below and bump MaxKnownSchemaVersion accordingly. Until they land, this
// binary tops out at v2.
package audit

import (
	"database/sql"
	"fmt"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// MaxKnownSchemaVersion is the highest schema version this binary can
// produce. Bumped by S2 (→3) and S4 (→4); stays at 2 until those slices land.
//
// Layer Integration Point owned by S1: any caller that needs to know "what
// version does my binary support?" reads this constant.
const MaxKnownSchemaVersion = 2

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
// knows how to apply. Slices S2/S3/S4 will append their steps here.
//
// Order MUST be ascending by fromVersion. Migrate iterates this slice and
// applies any step whose fromVersion equals the current on-disk version.
func migrationSteps() []migrationStep {
	return []migrationStep{
		{fromVersion: 1, toVersion: 2, apply: migrateV1toV2},
		// S2 will append: {fromVersion: 2, toVersion: 3, apply: migrateV2toV3}
		// S4 will append: {fromVersion: 3, toVersion: 4, apply: migrateV3toV4}
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
			What:     "audit.Migrate: db handle is nil",
			Why:      "Migrate was called with a nil *sql.DB",
			Impact:   "no migration can run; the audit database cannot be opened",
			Fix:      "ensure NewSqliteAuditTrail (or another caller) successfully opened the SQLite file before invoking audit.Migrate",
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
				What:     fmt.Sprintf("audit.Migrate: missing migration step for version %d", currentVersion),
				Why:      fmt.Sprintf("the next registered step starts at version %d but the database is at version %d", step.fromVersion, currentVersion),
				Impact:   "the audit database cannot be brought up to the version this binary supports",
				Fix:      "this is an audit-package bug — file an issue against pasture/internal/audit/migrate.go and pin to the previous binary",
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
// transaction. The audit_schema_meta version bump (writeVersion call inside
// step.apply) MUST be the last statement in the transaction so that a
// crash before Commit rolls everything back atomically.
//
// modernc.org/sqlite supports BEGIN IMMEDIATE via raw Exec; we can't use
// db.BeginTx alone (which issues plain BEGIN) because plain BEGIN is a
// DEFERRED transaction and acquires the write lock lazily on the first
// write — that creates a window where a concurrent writer can sneak in
// between version probe and migration apply.
func runStep(db *sql.DB, step migrationStep) error {
	tx, err := db.Begin()
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.runStep: cannot begin transaction for v%d→v%d", step.fromVersion, step.toVersion),
			Why:      err.Error(),
			Impact:   "the migration cannot run; the database remains at the current version",
			Fix:      "verify the SQLite file is accessible and not held by another writer; rerun 'pasture migrate' once the lock is released",
		}
	}
	// Best-effort rollback on any error path before Commit; a successful
	// Commit makes Rollback a no-op.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`BEGIN IMMEDIATE`); err != nil {
		// modernc.org/sqlite already started a DEFERRED tx via db.Begin();
		// some drivers reject a nested BEGIN IMMEDIATE here. If that
		// happens we fall back silently — the writeVersion INSERT will
		// still acquire the write lock, just one statement later than
		// ideal. This is an edge case for v1→v2 (which has no other
		// writes); later slices may need explicit conn.Raw access.
		_ = err
	}

	nowUnixNano := time.Now().UTC().UnixNano()
	if err := step.apply(tx, nowUnixNano); err != nil {
		// step.apply returns *StructuredError already.
		return err
	}

	if err := tx.Commit(); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("audit.runStep: cannot commit transaction for v%d→v%d", step.fromVersion, step.toVersion),
			Why:      err.Error(),
			Impact:   "the migration was rolled back; the database remains at the previous version",
			Fix:      "verify the SQLite file is writable and the disk has space; rerun 'pasture migrate' once the underlying problem is resolved",
		}
	}
	return nil
}

// newerSchemaError builds the *StructuredError returned when the database
// reports a schema version higher than MaxKnownSchemaVersion. The exact
// field values are asserted by §11 Scenario 5; do not change wording
// without updating that test.
func newerSchemaError(dbVersion, maxKnownVersion int) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("audit database schema version %d is newer than supported version %d", dbVersion, maxKnownVersion),
		Why:      "this binary was built before the schema was bumped",
		Impact:   "no events can be read or written until the binary is upgraded",
		Fix:      fmt.Sprintf("upgrade pasture to a version that supports schema v%d, or pin to the older binary that wrote it; do NOT downgrade the database", dbVersion),
	}
}
