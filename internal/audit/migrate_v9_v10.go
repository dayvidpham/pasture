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
// WHERE THE FORMAT GATE LIVES, AND WHY NOT HERE. The gate that refuses a
// populated two-dash store runs BEFORE this migrator, on the normal open path
// (internal/tasks/id_format_guard.go), because a legacy store's first open
// legitimately CREATES populated provenance tables through the v2→v3 backfill:
// the identity-format marker is published later, by the dependency's activation
// transaction. A gate inside a migration step would therefore refuse the very
// store it had just populated. The open-path guard inspects the format before
// any schema work, so it sees the store as the previous build left it. The
// conversion publishes the marker and raises THIS ceiling in one transaction
// (audit.RaiseIDFormatCeilingInTx), so a converted file can never carry one
// without the other.
//
// ATOMICITY. runStep holds one BEGIN IMMEDIATE transaction and commits only on
// a nil return; the version stamp is the LAST statement, so a crash rolls the
// step back.
func migrateV9toV10Step(tx *sql.Tx, now int64) error {
	return writeVersion(tx, 10, now)
}

// RaiseIDFormatCeilingInTx stamps the identity-format schema ceiling (version
// 10) inside the CALLER's conversion transaction, after the dependency has
// published the slash marker. It is the same floor the normal open path
// enforces, applied at the moment of conversion, so the marker and the ceiling
// can never disagree on a populated store: one without the other is exactly the
// half-converted state the ceiling exists to prevent. The caller owns
// Commit/Rollback.
func RaiseIDFormatCeilingInTx(tx *sql.Tx, storePath string, nowUnixNano int64) error {
	inspection, err := provenance.InspectIDFormat(context.Background(), tx)
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
	return writeVersion(tx, MaxKnownSchemaVersion, nowUnixNano)
}

// identityFormatCeilingRefusal is returned when a caller asks to raise the
// audit schema ceiling without the dependency's slash marker present on a
// populated store. The offline conversion calls this before stamping version
// 10 so the ceiling and the marker can never disagree.
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
			"2. If it keeps failing, restore the complete archive the conversion made and retry from that copy.",
	}
}
