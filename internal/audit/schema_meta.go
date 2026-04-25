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
			What:     "audit.schemaMetaTableExists: cannot query sqlite_master for audit_schema_meta presence",
			Why:      err.Error(),
			Impact:   "the migrator cannot determine the on-disk schema version; the audit database cannot be opened",
			Fix:      "verify the SQLite file is accessible, not corrupted, and that the process has read permission; run 'pasture migrate --dry-run' to retry the version probe",
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
			What:     "audit.readVersion: cannot read MAX(version) from audit_schema_meta",
			Why:      err.Error(),
			Impact:   "the migrator cannot determine the on-disk schema version; the audit database cannot be opened",
			Fix:      "verify the SQLite file is accessible and not corrupted; if the file is intact, this may indicate concurrent corruption — back up the file and run 'pasture migrate --dry-run' to retry",
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
			What:     "audit.writeVersion: cannot insert (version, applied_at) into audit_schema_meta",
			Why:      err.Error(),
			Impact:   "the migrator cannot record that schema migration completed; subsequent opens will retry the migration",
			Fix:      "verify the SQLite file is writable and the transaction is still active; if the database is full or the disk is out of space, free space and retry 'pasture migrate'",
		}
	}
	return nil
}
