// Package tasks — id_format_guard.go
//
// The normal-open identity-format guard. It runs BEFORE the audit schema
// migrator and BEFORE any Provenance activation, fixed-actor registration,
// genesis reconciliation or journal decode: a populated store that still uses
// the retired two-dash identity format is refused here, with a storage-category
// (exit 5) error that names the path, the converting command and the unchanged
// state.
//
// PLACEMENT IS THE WHOLE POINT. The guard reads the store as the PREVIOUS
// build left it. It cannot run inside a migration step, because a legacy
// store's first open legitimately creates populated Provenance tables through
// the v2→v3 backfill; the identity-format marker is published later, by the
// dependency's activation transaction. A guard that ran after the migrator
// would refuse the store the migrator had just populated. Running it first also
// means a genuinely old-format store never reaches schema work, actor
// registration or a single journal decode.
//
// It is read-only and opens its own short-lived handle (mode=ro) so it never
// writes: the guard must be free of side effects on a store it may refuse.
package tasks

import (
	"context"
	"fmt"
	"os"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// refuseRetiredIdentityFormat inspects dbPath and returns the storage refusal
// when the store is populated and unmarked. A store that does not exist yet, an
// empty store, or a store already carrying the slash marker is allowed through;
// a read-only open failure is left for the normal open path to report, so the
// guard never turns a readable-store problem into a misleading format error.
func refuseRetiredIdentityFormat(ctx context.Context, dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil {
		// A missing file (or one the process cannot stat) is the normal open
		// path's concern, not this guard's.
		return nil
	}
	db, err := dbconn.OpenReadOnlyDB(dbPath)
	if err != nil {
		return nil
	}
	defer db.Close()
	return refuseRetiredIdentityFormatOn(ctx, db, dbPath)
}

// refuseRetiredIdentityFormatOn is the testable core: it inspects an open
// reader and returns the refusal when the store is populated and unmarked.
func refuseRetiredIdentityFormatOn(ctx context.Context, reader provenance.IDFormatReader, dbPath string) error {
	inspection, err := provenance.InspectIDFormat(ctx, reader)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't read the identity format of the database at %q.", dbPath),
			Why: "The dependency's read-only format inspection failed, or the store's identity-format marker " +
				"is malformed or unknown to this build.",
			Where: "Checking the store identity format before open (internal/tasks/id_format_guard.go in tasks.refuseRetiredIdentityFormatOn).",
			Impact: "No store data was read or written; this open stopped before schema work, actor registration, " +
				"genesis reconciliation or journal decode.",
			Fix: "1. Confirm the file is a readable SQLite database:\n" +
				"     sqlite3 <path> 'PRAGMA integrity_check'\n" +
				"2. If the marker looks wrong, restore the complete byte-exact archive of the store and retry.",
			Cause: err,
		}
	}
	if inspection.Format == provenance.IDFormatRetired {
		return retiredIdentityFormatRefusal(dbPath)
	}
	return nil
}

// retiredIdentityFormatRefusal is the storage-format refusal a populated
// two-dash store earns on a normal open. It names the path, the converting
// command, the operation that stopped, the unchanged-state impact, the backup
// guidance, and the irreversibility of the cutover.
func retiredIdentityFormatRefusal(dbPath string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("The pasture database at %q still uses the retired two-dash task identity format.", dbPath),
		Why: "Every namespaced identity in this store joins the namespace and UUID with \"--\", but this build " +
			"reads only the slash form \"<namespace>/<uuid>\". The store has not published the slash identity " +
			"marker, and pasture will not guess at a half-converted store by sampling its keys.",
		Where: "Opening the pasture database before converting it (internal/tasks/id_format_guard.go in tasks.refuseRetiredIdentityFormat).",
		Impact: "Nothing was changed by this attempt. No task, edge, comment, agent or journal row was read or " +
			"written. This open stopped before schema work, actor registration, genesis reconciliation and " +
			"journal decode. The store stays exactly as it is until it is converted.",
		Fix: fmt.Sprintf("1. Stop every writer — the daemon and any CLI or hook process:\n"+
			"     pgrep -af 'pasture|pastured'\n"+
			"2. See what the conversion would change, without writing:\n"+
			"     pasture migrate ids --dry-run --db %q\n"+
			"3. Keep the consistent backup the dry run tells you it will make, then convert once:\n"+
			"     pasture migrate ids --db %q\n"+
			"4. Restart only upgraded binaries. After conversion the old two-dash input never resolves again, "+
			"and an older pasture binary refuses this file for every command until it is upgraded.",
			dbPath, dbPath),
	}
}
