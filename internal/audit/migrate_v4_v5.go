// Package audit — migrate_v4_v5.go
//
// The v4→v5 step adds a deterministic deduplication key to audit_events so
// the durable engine can record a forensic row exactly once even when a
// crashed step replays.
//
// Two additive statements run in the SAME BEGIN IMMEDIATE transaction owned
// by runStep, then the version bump:
//
//  1. ALTER TABLE audit_events ADD COLUMN dedup_key TEXT
//  2. CREATE UNIQUE INDEX idx_audit_events_dedup
//     ON audit_events(dedup_key) WHERE dedup_key IS NOT NULL
//
// The uniqueness is a PARTIAL unique index, not a column UNIQUE constraint:
// SQLite's ALTER TABLE ADD COLUMN forbids adding a UNIQUE/PK constraint, and a
// column constraint would force a full table rebuild. A partial index on the
// non-NULL keys lets legacy/non-engine rows (which leave dedup_key NULL)
// coexist freely — multiple NULLs are allowed — while still rejecting a
// duplicate engine emission. The v4 CREATE TABLE shape is left untouched.
//
// dedup_key is NULL for every existing row and for any writer that does not
// supply one, so the column is additive and back-compatible: existing
// databases migrate without data loss.
package audit

import (
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// migrateV4toV5 advances the audit database from schema version 4 to version 5
// in the supplied transaction. The transaction MUST already hold the SQLite
// write lock (BEGIN IMMEDIATE from runStep). The caller (migrateV4toV5Step)
// bumps audit_schema_meta to 5 as the final statement before tx.Commit.
//
// Bail-out: if audit_events does not exist (a fresh file opened for
// schema-meta-only work), there is no table to extend. Returning nil lets the
// framework still advance audit_schema_meta to 5; the next open creates
// audit_events at the current shape.
//
// Idempotency: the ADD COLUMN + CREATE INDEX pair is not individually
// re-runnable (a second ADD COLUMN would fail because the column already
// exists), but the whole step lives in one transaction. A partial run is
// rolled back atomically and the retry starts from the v4 state.
func migrateV4toV5(tx *sql.Tx, _ int64) error {
	exists, err := auditEventsTableExists(tx)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	statements := []struct {
		what string
		sql  string
	}{
		{
			what: "add the deduplication-key column to the audit-events table",
			sql:  `ALTER TABLE audit_events ADD COLUMN dedup_key TEXT`,
		},
		{
			what: "create the partial unique index on the deduplication-key column",
			sql: `CREATE UNIQUE INDEX idx_audit_events_dedup
			      ON audit_events(dedup_key) WHERE dedup_key IS NOT NULL`,
		},
	}

	for _, s := range statements {
		if _, err := tx.Exec(s.sql); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"Couldn't %s while upgrading the audit database from version 4 to 5.",
					s.what,
				),
				Why: "The database refused one of the two additive statements (add the dedup_key column,\n" +
					"then create the partial unique index over its non-NULL values).",
				Where: "Upgrading the audit database from version 4 to 5 (internal/audit/migrate_v4_v5.go in audit.migrateV4toV5).",
				Impact: "The version 4 → 5 upgrade was rolled back atomically, so the audit database stays at\n" +
					"version 4 with no partial change. Existing rows are untouched.",
				Fix: "1. Confirm the audit database file is writable and the disk has free space:\n" +
					"     ls -l <path-to-pasture.db>\n" +
					"     df -h <path-to-pasture.db>\n" +
					"2. Inspect the current audit-events shape; the dedup_key column must not already exist\n" +
					"   at version 4:\n" +
					"     sqlite3 <path-to-pasture.db> '.schema audit_events'\n" +
					"3. Re-run the migration once the underlying problem is resolved:\n" +
					"     pasture migrate",
				Cause: err,
			}
		}
	}
	return nil
}

// migrateV4toV5Step is the migration framework's entry point for the v4→v5
// hop. It runs the migrateV4toV5 body, then bumps audit_schema_meta from 4 to
// 5 as the LAST statement before the caller's tx.Commit.
//
// A crash anywhere inside this function — including between the body and the
// writeVersion call — rolls back the entire transaction atomically because the
// caller (runStep) holds the BEGIN IMMEDIATE lock and only commits on a nil
// return. The file remains observably at v4; the next open re-runs the v5 step
// from scratch.
func migrateV4toV5Step(tx *sql.Tx, nowUnixNano int64) error {
	if err := migrateV4toV5(tx, nowUnixNano); err != nil {
		return err
	}
	return writeVersion(tx, 5, nowUnixNano)
}
