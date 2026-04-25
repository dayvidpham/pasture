// Package audit — migrate_v1_v2.go
//
// The v1→v2 migration is the bootstrap step that introduces the
// audit_schema_meta table. It is intentionally a near-no-op: it creates
// the meta table (if not already created by an earlier partial run) and
// seeds (version=2, applied_at=<now>) so that subsequent migrations can
// branch on the recorded version.
//
// All schema work is wrapped in the migrator's enclosing transaction
// (see migrate.go). v1→v2 itself does NOT touch audit_events or any other
// existing table — pre-PROPOSAL-2 data is preserved verbatim.
package audit

import (
	"database/sql"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// migrateV1toV2 promotes a legacy v1 database (audit_events present, no
// audit_schema_meta) to v2 by creating the audit_schema_meta table and
// seeding (version=2, applied_at=nowUnixNano). Idempotent: re-running on
// an already-v2 database is a no-op courtesy of CREATE TABLE IF NOT EXISTS
// and INSERT OR IGNORE.
//
// The transaction (tx) must already hold the SQLite write lock (BEGIN
// IMMEDIATE in production code paths). Caller commits.
func migrateV1toV2(tx *sql.Tx, nowUnixNano int64) error {
	if _, err := tx.Exec(schemaMetaDDL); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "audit.migrateV1toV2: cannot create audit_schema_meta table",
			Why:      err.Error(),
			Impact:   "the v1→v2 schema migration cannot complete; the database remains at v1",
			Fix:      "verify the SQLite file is writable and the disk has space; rerun 'pasture migrate' once the underlying problem is resolved",
		}
	}
	if err := writeVersion(tx, 2, nowUnixNano); err != nil {
		// writeVersion already returns a *StructuredError with full context.
		return err
	}
	return nil
}
