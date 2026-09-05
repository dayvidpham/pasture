package audit

import (
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// lifecycleV8Statements give every payload blob a written-at stamp, in unix
// nanoseconds. The column is what an age bound on orphan reclaim reads; without
// it a blob whose journal append is still in flight cannot be told from one
// abandoned an hour ago. Legacy rows keep 0, which is OLDER THAN ANY BOUND:
// a blob written before the stamp existed is eligible for reclaim as soon as
// nothing names it, and a blob an occurrence names is never eligible at any
// age.
var lifecycleV8Statements = []string{
	`ALTER TABLE lifecycle_payload_blobs ADD COLUMN written_at INTEGER NOT NULL DEFAULT 0`,
}

func migrateV7toV8Step(tx *sql.Tx, now int64) error {
	for _, statement := range lifecycleV8Statements {
		if _, err := tx.Exec(statement); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "The payload blob written-at stamp could not be added while upgrading the database from version 7 to 8.",
				Why:      err.Error(),
				Where:    "Migrating the audit schema (internal/audit/migrate_v7_v8.go in migrateV7toV8Step).",
				Impact:   "The migration transaction is rolled back; the database stays at version 7 and orphan payload blobs cannot be reclaimed by age.",
				Fix:      "Confirm the database is writable and that lifecycle_payload_blobs still has its version 6 shape, then run `pasture migrate` again.",
				Cause:    err,
			}
		}
	}
	if err := requireWrittenAtColumn(tx); err != nil {
		return err
	}
	return writeVersion(tx, 8, now)
}

// requireWrittenAtColumn reads the table back and refuses to record version 8
// unless the column exists, is NOT NULL and defaults to 0, so a migration that
// ran and changed nothing cannot claim the version.
func requireWrittenAtColumn(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(lifecycle_payload_blobs)`)
	if err != nil {
		return fmt.Errorf("verify lifecycle v8 payload blob columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			kind    string
			notNull int
			dflt    sql.NullString
			primary int
		)
		if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &primary); err != nil {
			return fmt.Errorf("verify lifecycle v8 payload blob columns: %w", err)
		}
		if name != "written_at" {
			continue
		}
		if notNull != 1 || !dflt.Valid || dflt.String != "0" {
			return fmt.Errorf("verify lifecycle v8 payload blob columns: written_at exists but is not NOT NULL DEFAULT 0 (notnull=%d default=%q)", notNull, dflt.String)
		}
		return rows.Err()
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return fmt.Errorf("verify lifecycle v8 payload blob columns: the written_at column is missing after the upgrade statements ran")
}
