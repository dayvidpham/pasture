package audit

import (
	"database/sql"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// retiredAssignmentIndexTriggers are the two triggers the retired
// assignment-index machinery installed on pasture_actor_assignment. Both
// write into pasture_actor_assignment_state, so they are removed before any
// table they depend on is dropped.
var retiredAssignmentIndexTriggers = []string{
	"pasture_assignment_changed",
	"pasture_assignment_deleted",
}

// retiredAssignmentIndexTables are the six tables that held the retired
// assignment index: the three per-generation recovery tables, the coverage
// state, the build watermark, and the index itself. The index's
// idx_pasture_actor_assignment_actor index goes with its table and needs no
// statement of its own.
//
// pasture_session_claim is NOT in this list. It records which actor a
// harness session is bound to, it is read on the live gate path, and it
// survives the upgrade with its rows.
var retiredAssignmentIndexTables = []string{
	"pasture_assignment_recovery_member",
	"pasture_assignment_recovery_cache",
	"pasture_assignment_recovery_scan",
	"pasture_actor_assignment_state",
	"pasture_actor_assignment_watermark",
	"pasture_actor_assignment",
}

// migrateV8toV9Step removes the retired assignment index and records version
// 9.
//
// WHAT THE OPERATOR GETS, IN PLAIN WORDS: the tables and triggers listed in
// retiredAssignmentIndexTriggers and retiredAssignmentIndexTables are gone
// after this step, and pasture_session_claim keeps every row it had. Nothing
// else in the file is read, copied, or rewritten.
//
// THE COST, IN PLAIN WORDS: version 9 is a floor that only moves up. Once a
// file has been upgraded to version 9, an OLDER pasture binary that opens it
// refuses the whole audit database for EVERY command — tasks, epochs, hooks,
// the daemon, the migrate command itself — and says the file was written by a
// newer pasture than the build supports. It stays refused until that older
// binary is upgraded. A lifecycle hook on an older build turns that refusal
// into an open failure on every event, which is the existing fail-open
// storage-fault path. There is no shim and no compatibility mode: the way back
// is a newer binary, never a hand-downgraded file.
//
// ATOMICITY: runStep holds one BEGIN IMMEDIATE transaction and commits only on
// a nil return, so a failure part-way through the drops leaves the file whole
// at version 8 — no relation is left half removed.
func migrateV8toV9Step(tx *sql.Tx, now int64) error {
	if err := dropRetiredAssignmentIndex(tx); err != nil {
		return err
	}
	// The version stamp is the LAST statement, per the rule every step obeys:
	// a crash before it rolls the drops back with it.
	return writeVersion(tx, 9, now)
}

// dropRetiredAssignmentIndex drops the two triggers and then the six tables,
// in that order, each behind IF EXISTS. IF EXISTS is what makes the step a
// no-op on a file that never had a relation and on a fresh file that never
// gets one, so the same statement list serves every version-8 file this build
// can meet.
func dropRetiredAssignmentIndex(tx *sql.Tx) error {
	for _, trigger := range retiredAssignmentIndexTriggers {
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + trigger); err != nil {
			return dropFailure(trigger, "trigger", err)
		}
	}
	for _, table := range retiredAssignmentIndexTables {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			return dropFailure(table, "table", err)
		}
	}
	return nil
}

// dropFailure wraps a failed drop in the actionable error shape. The dropped
// relation's own name is in What, the file position in Where, and the fact
// that nothing was removed in Impact, so an operator reading the message does
// not have to guess which of the eight relations failed or whether the file is
// damaged.
func dropFailure(name, kind string, err error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What: "The retired assignment-index " + kind + " " + name +
			" could not be removed while upgrading the database from version 8 to 9.",
		Why:    err.Error(),
		Where:  "Migrating the audit schema (internal/audit/migrate_v8_v9.go in dropRetiredAssignmentIndex).",
		Impact: "The migration transaction is rolled back; the database stays at version 8 with every relation it had. Nothing was removed.",
		Fix:    "Confirm the database file is writable and the disk has free space, then run `pasture migrate` again.",
		Cause:  err,
	}
}
