// Package audit — schema_meta.go
//
// The audit_schema_meta table records the on-disk schema version of the
// pasture audit database. It is the single source of truth read by the
// migrator on every open to decide which forward migrations (if any) need
// to run, and to detect a database that is newer than the running binary
// supports.
//
// Schema (per PROPOSAL-2 §7.10.1):
//
//	version    INTEGER PRIMARY KEY  — monotonically increasing schema version
//	applied_at INTEGER NOT NULL     — Unix nanoseconds UTC when the row landed
//
// The table is created lazily by audit.Migrate (NOT by ensureSchema) so that
// pre-existing v1 databases — which predate this table — are still openable
// and detectable as "no audit_schema_meta row yet ⇒ version 1".
package audit

import (
	"database/sql"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// schemaMetaDDL is the CREATE TABLE statement for audit_schema_meta. Wrapped
// in CREATE TABLE IF NOT EXISTS so it is idempotent — the v1→v2 migration
// is the only path that creates this table, but re-creating it on subsequent
// opens of an already-migrated database is a no-op.
const schemaMetaDDL = `
CREATE TABLE IF NOT EXISTS audit_schema_meta (
	version    INTEGER PRIMARY KEY,
	applied_at INTEGER NOT NULL
)
`

// schemaMetaTableExists reports whether the audit_schema_meta table is
// present in the database. A database that pre-dates PROPOSAL-2 (legacy v1)
// will return false here; the migrator treats that as version=1.
//
// The error return covers infrastructure failures only (db handle closed,
// file deleted mid-query); a missing table is not an error.
func schemaMetaTableExists(db *sql.DB) (bool, error) {
	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='audit_schema_meta'`,
	).Scan(&name)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't check whether the audit-database version-tracking table exists.",
			Why:      "The database refused the query against its internal table catalog.",
			Where:    "Reading the audit-database schema version (internal/audit/schema_meta.go in audit.schemaMetaTableExists).",
			Impact: "Without knowing whether the version-tracking table is present, the migrator can't tell\n" +
				"what version the audit database is at. The database can't be opened until this resolves.",
			Fix: "1. Confirm the audit database file exists, is readable by this process, and isn't corrupted:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. Re-check the version once the file is healthy:\n" +
				"     pasture migrate --dry-run",
			Cause: err,
		}
	}
	return true, nil
}

// readVersion returns the highest schema version recorded in
// audit_schema_meta. Returns 1 if the table does not exist (legacy database
// that pre-dates PROPOSAL-2's migration framework).
//
// Returns 0 only on infrastructure failure (with a *StructuredError); a
// successfully-detected legacy database always returns 1.
func readVersion(db *sql.DB) (int, error) {
	exists, err := schemaMetaTableExists(db)
	if err != nil {
		return 0, err
	}
	if !exists {
		// Legacy v1 database: pre-PROPOSAL-2 schema, no meta table yet.
		return 1, nil
	}

	var version sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		return 0, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't read the audit database's current version.",
			Why:      "The database refused the read of the schema-version table.",
			Where:    "Reading the audit-database schema version (internal/audit/schema_meta.go in audit.readVersion).",
			Impact: "Without knowing the current version, the migrator can't decide what (if anything)\n" +
				"needs to be upgraded. The audit database can't be opened until this resolves.",
			Fix: "1. Confirm the audit database file is accessible and not corrupted:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     sqlite3 <path-to-audit.db> 'PRAGMA integrity_check'\n" +
				"2. If the file looks intact, take a backup and re-check the version:\n" +
				"     cp <path-to-audit.db> <path-to-audit.db>.backup\n" +
				"     pasture migrate --dry-run",
			Cause: err,
		}
	}
	if !version.Valid {
		// Table exists but is empty — treat as v1 (rare; can happen if v1→v2
		// was interrupted between CREATE TABLE and INSERT, a state the
		// transactional v1→v2 migration prevents on success but accommodates
		// here defensively).
		return 1, nil
	}
	return int(version.Int64), nil
}

// writeVersion records (version, applied_at=nowUnixNano) into
// audit_schema_meta inside the supplied transaction. Uses INSERT OR IGNORE
// so re-running an already-applied migration is a no-op rather than a PK
// conflict.
//
// Caller is responsible for committing the transaction.
func writeVersion(tx *sql.Tx, version int, nowUnixNano int64) error {
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO audit_schema_meta (version, applied_at) VALUES (?, ?)`,
		version, nowUnixNano,
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't record the new version after the audit-database upgrade ran.",
			Why:      "The database refused the insert into the schema-version table.",
			Where:    "Stamping the new audit-database version (internal/audit/schema_meta.go in audit.writeVersion).",
			Impact: "The upgrade itself is rolled back because the version stamp couldn't be saved. The\n" +
				"next time you open the audit database, the same upgrade will be attempted again.",
			Fix: "1. Confirm the audit database file is writable and the disk has free space:\n" +
				"     ls -l <path-to-audit.db>\n" +
				"     df -h <path-to-audit.db>\n" +
				"2. Re-run the migration once the underlying problem is resolved:\n" +
				"     pasture migrate",
			Cause: err,
		}
	}
	return nil
}
