package audit

import (
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// lifecycleV8Statements give every payload blob a written-at stamp, in unix
// nanoseconds. The column is what an age bound on orphan reclaim reads; without
// it a blob whose journal append is still in flight cannot be told from one
// abandoned an hour ago.
//
// The column default is a constant 0 because SQLite accepts only a constant
// default on ALTER TABLE. That default is NOT the stamp a pre-existing row
// keeps: the step then sets written_at to the migration instant on every row
// the table already holds (lifecycleV8StampStatement), so a blob written before
// the column existed reads as exactly as old as the upgrade, and a writer that
// was between its blob write and its journal append when the upgrade ran gets
// the same grace as any other writer. "Written before the upgrade" and "written
// long ago" are two different facts; a store that is upgraded while an older
// build still writes to it is the one case where they diverge, and a default of
// 0 read as "older than any bound" would let the first read command after the
// upgrade delete a moments-old blob under that writer's append.
//
// After this step a row can read 0 only if a build that predates the column
// inserts it AFTER the upgrade; such a stamp is UNKNOWN, and the reclaim never
// treats an unknown age as satisfying an age bound (see
// receipt.PayloadReclaimer.ReclaimOrphansWrittenBefore).
var lifecycleV8Statements = []string{
	`ALTER TABLE lifecycle_payload_blobs ADD COLUMN written_at INTEGER NOT NULL DEFAULT 0`,
}

// lifecycleV8StampStatement sets the migration instant on every row that
// existed before the column did. It runs in the same transaction as the ALTER,
// so no committed state ever holds a pre-existing row at 0.
const lifecycleV8StampStatement = `UPDATE lifecycle_payload_blobs SET written_at = ? WHERE written_at = 0`

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
	if _, err := tx.Exec(lifecycleV8StampStatement, now); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "The payload blobs that existed before the upgrade could not be stamped with the upgrade instant while upgrading the database from version 7 to 8.",
			Why:      err.Error(),
			Where:    "Migrating the audit schema (internal/audit/migrate_v7_v8.go in migrateV7toV8Step).",
			Impact:   "The migration transaction is rolled back; the database stays at version 7. Nothing was deleted and no blob was left at the unstamped value.",
			Fix:      "Confirm the database is writable, then run `pasture migrate` again.",
			Cause:    err,
		}
	}
	if err := requireWrittenAtColumn(tx); err != nil {
		return err
	}
	if err := requireNoUnstampedBlob(tx); err != nil {
		return err
	}
	return writeVersion(tx, 8, now)
}

// requireNoUnstampedBlob reads the table back and refuses to record version 8
// while any row still carries the column default, so a migration that added
// the column but did not stamp the pre-existing rows cannot claim the version.
func requireNoUnstampedBlob(tx *sql.Tx) error {
	var unstamped int
	if err := tx.QueryRow(`SELECT count(*) FROM lifecycle_payload_blobs WHERE written_at = 0`).Scan(&unstamped); err != nil {
		return fmt.Errorf("verify lifecycle v8 payload blob stamps: %w", err)
	}
	if unstamped != 0 {
		return fmt.Errorf("verify lifecycle v8 payload blob stamps: %d pre-existing payload blobs still carry written_at 0 after the upgrade statements ran; every pre-existing row must carry the migration instant", unstamped)
	}
	return nil
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
