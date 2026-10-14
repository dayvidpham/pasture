// Package idmigrate owns pasture's one-time OFFLINE identity-format conversion:
// the `pasture migrate ids [--dry-run]` command's production path. It is not a
// runtime reader and adds no compatibility mode. After it runs, the retired
// two-dash representation never resolves anywhere in pasture.
//
// The dependency (github.com/dayvidpham/provenance) owns the format, the parser
// and every Provenance-owned table, canonical mutation and allocation receipt.
// This package owns only what pasture owns: the audit trail, the pasture-side
// registry and projection tables, and the shared lifecycle replay that must run
// inside the same transaction. It calls the dependency's published conversion
// APIs; it never patches or duplicates them.
//
// One transaction, marker last. The whole conversion runs inside ONE
// BEGIN IMMEDIATE transaction:
//
//  1. preflight is repeated under the lock;
//  2. Provenance-owned columns, canonical mutations and allocation receipts are
//     converted by provenance.ConvertIDsInTx;
//  3. pasture-owned typed columns and declared structured fields are converted
//     by this package's codecs;
//  4. provenance.VerifyIDConversionInTx checks canonical and allocation
//     integrity through their real owners, on this transaction;
//  5. the disposable lifecycle projection is replayed from the CONVERTED journal
//     truth through a transaction-bound evidence reader (no pooled connection),
//     then compared against that truth;
//  6. the complete raw payload-blob inventory is compared with its pre-conversion
//     inventory, so nothing is silently deleted or rewritten;
//  7. provenance.PublishIDFormatInTx publishes the format marker, and
//     audit.RaiseIDFormatCeilingInTx raises the schema ceiling, BOTH last.
//
// Any failure in steps 2-6 rolls the entire transaction back: no key, canonical
// byte, digest, projection row, blob row or marker survives a failure. No normal
// open or pooled read runs inside the lock. Reclaim is structurally absent from
// this path, so the conversion reports zero reclaimed bodies.
package idmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
)

// Inspection is the read-only identity-format inspection of one store. It never
// writes and never activates schema.
type Inspection struct {
	StorePath string
	Format    provenance.IDFormat
	Populated bool
}

// Retired reports whether the store must be converted before it can be opened.
func (i Inspection) Retired() bool { return i.Format == provenance.IDFormatRetired }

// Current reports whether the store already carries the slash marker.
func (i Inspection) Current() bool { return i.Format == provenance.IDFormatSlash }

// InspectionReader is the read surface Inspect needs: an open database or the
// caller's transaction both satisfy it, so the same inspection runs before and
// under the conversion lock.
type InspectionReader = provenance.IDFormatReader

// Inspect reads the store's identity format without writing anything. storePath
// is used only to label errors; it is never opened here.
func Inspect(ctx context.Context, reader InspectionReader, storePath string) (Inspection, error) {
	inspection, err := provenance.InspectIDFormat(ctx, reader)
	if err != nil {
		return Inspection{StorePath: storePath}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't read the identity format of the database at %q.", storePath),
			Why:      "The dependency's read-only format inspection failed, or the store's marker is malformed or unknown to this build.",
			Where:    "Inspecting the store identity format (internal/idmigrate/idmigrate.go in idmigrate.Inspect).",
			Impact:   "No store data was read or written. The format cannot be confirmed, so no open or conversion proceeds.",
			Fix: "1. Confirm the file is a readable SQLite database:\n" +
				"     sqlite3 <path> 'PRAGMA integrity_check'\n" +
				"2. If the marker looks wrong, restore the complete byte-exact archive of the store and retry.",
			Cause: err,
		}
	}
	return Inspection{StorePath: storePath, Format: inspection.Format, Populated: inspection.Populated}, nil
}

// ConvertOptions carries the inputs to a single offline conversion.
type ConvertOptions struct {
	// StorePath is the resolved database path. It labels errors and names the
	// backup, and it is the path the operator is told to convert.
	StorePath string
	// BackupDir is where the consistent backup is written. Empty means the
	// store's own directory.
	BackupDir string
	// Now is the transaction timestamp source. Nil means the wall clock.
	Now func() time.Time
}

// Report is what one conversion did. It is returned even for the no-op paths
// (already current, or an empty store with nothing to convert).
type Report struct {
	StorePath          string
	BackupPath         string
	BackupSHA256       string
	AlreadyCurrent     bool
	NothingToConvert   bool
	ChangedFields      int
	ChangedMutations   int
	ChangedAllocations int
	PastureRows        int
	Reclaimed          int
}

// Convert runs the one-time offline conversion against an already-open
// database handle. The handle MUST be the only writer: the caller (the CLI
// handler) is responsible for the stopped-writer requirement and for closing
// the handle before any reopen.
//
// It never opens a second connection and never runs a normal open inside the
// conversion transaction.
func Convert(ctx context.Context, db *sql.DB, opts ConvertOptions) (Report, error) {
	if db == nil {
		return Report{}, conversionError("The identity conversion has no database handle.", "An open, exclusive handle is required; the conversion never opens its own.", "Nothing was converted.", "Open the store and retry.", nil)
	}
	report := Report{StorePath: opts.StorePath}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	inspection, err := Inspect(ctx, db, opts.StorePath)
	if err != nil {
		return report, err
	}
	if inspection.Current() {
		report.AlreadyCurrent = true
		return report, nil
	}
	if !inspection.Populated {
		// An empty store gets the slash marker from the dependency's own
		// activation transaction the first time it is opened; there is nothing
		// to convert and nothing to back up.
		report.NothingToConvert = true
		return report, nil
	}

	backup, err := makeConsistentBackup(ctx, db, opts.StorePath, opts.BackupDir, now())
	if err != nil {
		return report, err
	}
	report.BackupPath = backup.Path
	report.BackupSHA256 = backup.SHA256

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return report, conversionError("The identity conversion transaction could not start.", "SQLite did not grant the exclusive write transaction.", "No store row was changed; the backup remains available.", "Stop other writers, confirm the store is healthy, and retry.", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// RE-PREFLIGHT UNDER THE LOCK. A writer could have changed the format
	// between the unlocked inspection and the lock; repeat it inside the
	// transaction so the conversion never runs against a store it did not
	// inspect.
	locked, err := Inspect(ctx, tx, opts.StorePath)
	if err != nil {
		return report, err
	}
	if locked.Current() {
		report.AlreadyCurrent = true
		return report, nil
	}
	if !locked.Populated {
		report.NothingToConvert = true
		return report, nil
	}

	// RUNTIME STATE IS REFUSED, NEVER GUESSED. A non-empty serialized DBOS
	// execution/history table can carry identities in opaque vendor bytes that
	// this conversion has no owner recipe for. Fail closed with drain/export
	// guidance instead of rewriting vendor history.
	if err := refuseRuntimeState(ctx, tx); err != nil {
		return report, err
	}

	// RAW EVIDENCE INVENTORY, BEFORE ANY WRITE. Every payload blob row is
	// captured as (digest, body, byte_count, written_at) so the exact inventory
	// can be compared after the conversion. Row counts would miss a swapped body.
	before, err := captureBlobInventory(ctx, tx)
	if err != nil {
		return report, err
	}

	// 1. Dependency-owned columns, canonical mutations, allocation receipts,
	//    their owner-derived digests, and Provenance-owned extension contexts.
	dependencyReport, err := provenance.ConvertIDsInTx(ctx, tx, provenance.IDConversionOptions{
		RewriteEffect:  rewritePastureEffect,
		RewriteContext: rewritePastureContext,
	})
	if err != nil {
		return report, conversionError("Converting the dependency-owned identities failed.", err.Error(), "The whole conversion transaction rolls back; the store keeps its retired identities and the backup is available.", "Repair the reported column/key from the archive and retry.", err)
	}
	report.ChangedFields = dependencyReport.ChangedFields
	report.ChangedMutations = dependencyReport.ChangedMutations
	report.ChangedAllocations = dependencyReport.ChangedAllocations

	// 2. Pasture-owned typed identity columns and declared structured fields, in
	//    the SAME transaction.
	pastureRows, err := convertPastureIdentities(ctx, tx)
	if err != nil {
		return report, err
	}
	report.PastureRows = pastureRows

	// 3. Owner integrity on this transaction: canonical mutations, allocation
	//    receipts, evidence and projections through their production owners.
	if err := provenance.VerifyIDConversionInTx(ctx, tx); err != nil {
		return report, conversionError("The converted store failed owner integrity verification.", err.Error(), "The whole conversion transaction rolls back; the store keeps its retired identities and the backup is available.", "Restore the matching archived bytes/digest/version and retry.", err)
	}

	// 4. SAME-TRANSACTION, NON-RECLAIMING LIFECYCLE REPLAY. The reader is bound
	//    to this transaction, so it observes the converted rows that are not yet
	//    committed; there is no pooled reader and no second connection.
	reader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	if err != nil {
		return report, conversionError("The transaction-bound lifecycle evidence reader could not be constructed.", err.Error(), "The whole conversion transaction rolls back.", "Confirm the transaction is still open and retry.", err)
	}
	if err := projection.RebuildOccurrencesInTx(ctx, tx, reader); err != nil {
		return report, conversionError("Replaying the converted lifecycle journal failed.", err.Error(), "The whole conversion transaction rolls back, including every converted key and digest, and no marker is published.", "Repair the reported evidence row from the archive and retry.", err)
	}
	if err := projection.CompareOccurrencesToEvidence(ctx, tx, reader); err != nil {
		return report, err
	}

	// 5. RAW INVENTORY EQUALITY. Conversion must not delete, rewrite or reclaim
	//    any raw payload body. The eligible aged orphan is retained too.
	after, err := captureBlobInventory(ctx, tx)
	if err != nil {
		return report, err
	}
	if err := compareBlobInventories(before, after); err != nil {
		return report, err
	}
	report.Reclaimed = 0

	// 6. MARKER AND CEILING LAST. PublishIDFormatInTx repeats dependency
	//    integrity and SQLite constraint checks, then writes the marker; the
	//    audit ceiling goes in after it, in the same transaction, so a store can
	//    never carry the ceiling without the marker.
	if err := provenance.PublishIDFormatInTx(ctx, tx); err != nil {
		return report, conversionError("Publishing the identity-format marker failed.", err.Error(), "The whole conversion transaction rolls back; no partial marker is published and the store keeps its retired identities.", "Restore the complete archive and retry.", err)
	}
	if err := audit.RaiseIDFormatCeilingInTx(tx, opts.StorePath, now().UTC().UnixNano()); err != nil {
		return report, err
	}

	if err := tx.Commit(); err != nil {
		return report, conversionError("The identity conversion could not be committed.", "SQLite rejected the commit after every conversion and verification step succeeded.", "The store keeps its retired identities and the backup remains available.", "Confirm storage health and free space, then retry.", err)
	}
	committed = true
	return report, nil
}

// rewritePastureEffect and rewritePastureContext translate the declared
// structured fields of an operation's canonical effect/context that pasture
// itself owns. Pasture records no extension identity context and declares no
// canonical-backed extension effect, so both are identity-preserving: they
// return the value unchanged and let the dependency's complete field/order
// validation reject anything unsupported rather than guessing.
func rewritePastureEffect(effect provenance.Effect) (provenance.Effect, error) {
	return effect, nil
}

func rewritePastureContext(kind, value string) (string, error) {
	return value, nil
}

// conversionError builds the uniform CategoryStorage failure for the conversion
// steps. Every message names what failed, why, the rollback, and the fix.
func conversionError(what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     what,
		Why:      why,
		Where:    "Converting the store identity format (internal/idmigrate/idmigrate.go in idmigrate.Convert).",
		Impact:   impact,
		Fix:      fix,
		Cause:    cause,
	}
}

// convertRetiredIdentity translates one retired "<namespace>--<uuid>" value to
// the slash form and validates the result with the dependency's own parser, so
// the dependency stays the authority on the new shape. Pasture uses it only in
// the offline migration: no runtime lookup or parse reaches it. It mirrors the
// dependency's separation rule exactly — split on the LAST two-dash separator,
// refuse a value whose namespace already contains a slash after that separator.
func convertRetiredIdentity(raw string) (string, error) {
	i := strings.LastIndex(raw, "--")
	if i <= 0 {
		return "", fmt.Errorf("retired identity %q has no non-empty namespace and final two-dash separator", raw)
	}
	if strings.LastIndexByte(raw, '/') > i {
		return "", fmt.Errorf("retired identity %q has a slash after its final two-dash separator", raw)
	}
	candidate := raw[:i] + "/" + raw[i+2:]
	if _, err := provenance.ParseActorID(candidate); err != nil {
		return "", fmt.Errorf("retired identity %q cannot be converted without changing its namespace or UUID: %w", raw, err)
	}
	return candidate, nil
}

// looksRetired reports whether raw is a recognizable retired namespaced-UUID
// identity: a final two-dash separator with a non-empty namespace before it and
// a canonical 36-character hyphenated UUID after it. Arbitrary text, Git SHAs
// and session names are not retired identities.
func looksRetired(raw string) bool {
	i := strings.LastIndex(raw, "--")
	if i <= 0 || strings.LastIndexByte(raw, '/') > i {
		return false
	}
	_, err := provenance.ParseActorID(raw[:i] + "/" + raw[i+2:])
	return err == nil
}
