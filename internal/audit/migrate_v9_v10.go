package audit

import (
	"context"
	"database/sql"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/provenance"
)

// migrateV9toV10Step raises the audit schema ceiling to version 10.
//
// WHY THE FLOOR, IN PLAIN WORDS. Version 10 is the ceiling at which a store's
// identity representation is the slash form only. It is a floor that only moves
// up: once a file is at version 10, an older pasture binary refuses the whole
// file for EVERY command, exactly as it already does for version 9. The way
// past this file is an upgraded binary, never a hand-edited key.
//
// WHAT GATES IT. The step refuses a populated store that does not carry the
// dependency's slash identity-format marker, so no path can raise the floor
// without the marker. The offline cutover applies this step AFTER it publishes
// the marker, inside one transaction, so the marker and the ceiling commit
// together; plain `pasture migrate` never raises the floor on a store that is
// not marker-bearing, because its precheck refuses every non-NORMAL store
// before any step writes and this gate is the backstop.
//
// ATOMICITY. runStep holds one BEGIN IMMEDIATE transaction and commits only on
// a nil return; the version stamp is the LAST statement, so a crash rolls the
// step back.
func migrateV9toV10Step(tx *sql.Tx, now int64) error {
	return raiseIdentityCeiling(tx, now, "the database being migrated")
}

// RaiseIDFormatCeilingInTx applies the remaining identity-format ceiling step(s)
// inside the CALLER's conversion transaction, after the dependency has published
// the slash marker and this transaction has written the cutover receipt. It is
// the same floor the normal open path enforces, applied at the moment of
// conversion, so the marker, the receipt and the ceiling can never disagree:
// one without the others is exactly the half-converted state the ceiling exists
// to prevent. The caller owns Commit/Rollback.
func RaiseIDFormatCeilingInTx(tx *sql.Tx, storePath string, nowUnixNano int64) error {
	ctx := context.Background()
	inspection, err := provenance.InspectIDFormat(ctx, tx)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Pasture couldn't confirm the converted store's identity format before raising its schema ceiling.",
			Why:      "Reading the identity-format marker inside the conversion transaction failed.",
			Where:    "Raising the database schema ceiling (internal/audit/migrate_v9_v10.go in audit.RaiseIDFormatCeilingInTx).",
			Impact:   "The schema version was not changed. The conversion transaction rolls back, leaving the store as it was.",
			Fix: "1. Confirm the conversion transaction is still open and the store is writable.\n" +
				"2. Retry the conversion; if it keeps failing, restore the archive the conversion made.",
			Cause: err,
		}
	}
	if inspection.Populated && inspection.Format != provenance.IDFormatSlash {
		return identityFormatCeilingRefusal(storePath)
	}
	current, err := readVersionInTx(ctx, tx)
	if err != nil {
		return err
	}
	return ApplyCeilingInTx(ctx, tx, current, nowUnixNano)
}

// raiseIdentityCeiling is the shared body of the v9→v10 step: it refuses a
// populated store without the slash marker, then stamps the ceiling version.
func raiseIdentityCeiling(tx *sql.Tx, now int64, location string) error {
	inspection, err := provenance.InspectIDFormat(context.Background(), tx)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't read the identity format of %s before raising its schema ceiling.", location),
			Why:      "The dependency's read-only format inspection failed inside the migration step.",
			Where:    "Raising the database schema ceiling (internal/audit/migrate_v9_v10.go in audit.raiseIdentityCeiling).",
			Impact:   "The schema version was not changed; the step rolls back.",
			Fix:      "1. Confirm the database is a readable SQLite file and retry the migration.",
			Cause:    err,
		}
	}
	if inspection.Populated && inspection.Format != provenance.IDFormatSlash {
		return identityFormatCeilingRefusal(location)
	}
	return writeVersion(tx, MaxKnownSchemaVersion, now)
}

// identityFormatCeilingRefusal is returned when a caller asks to raise the
// audit schema ceiling without the dependency's slash marker present on a
// populated store. The offline conversion publishes the marker before it calls
// this, so the ceiling and the marker can never disagree.
func identityFormatCeilingRefusal(storePath string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("The pasture database at %q cannot be raised to the identity-format schema ceiling.", storePath),
		Why: "The populated store does not carry the slash identity-format marker, so the conversion " +
			"and the schema ceiling would disagree about how identities are stored.",
		Where: "Raising the database schema ceiling (internal/audit/migrate_v9_v10.go in audit.identityFormatCeilingRefusal).",
		Impact: "The schema version was not changed and the store still carries its retired identities. " +
			"The whole conversion transaction rolls back.",
		Fix: "1. Re-run the offline conversion, which publishes the marker before it raises the ceiling:\n" +
			"     pasture migrate ids\n" +
			"2. If it keeps failing, restore the consistent archive the conversion made and retry from that copy.",
	}
}
