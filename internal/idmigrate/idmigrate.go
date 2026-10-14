// Package idmigrate owns pasture's one-time OFFLINE identity-format cutover:
// the `pasture migrate ids [--dry-run]` command's production path and the
// cutover write the first open of a legacy store performs. It is not a runtime
// reader and adds no compatibility mode. After it runs, the retired two-dash
// representation never resolves anywhere in pasture.
//
// The dependency (github.com/dayvidpham/provenance) owns the format, the parser
// and every Provenance-owned table, canonical mutation and allocation receipt.
// This package owns only what pasture owns: the audit trail, the pasture-side
// registry and projection tables, the shared lifecycle replay that must run
// inside the same transaction, and the cutover receipt. It calls the
// dependency's published conversion APIs; it never patches or duplicates them.
//
// The cutover is one fenced pipeline with named phases:
//
//	P0 read-only state check (EvaluateCutoverOnHandle); the dry run stops here.
//	P1 archive (ArchiveStore): a consistent logical copy through a read-only
//	   source, verified, with the archive's own format marker recorded.
//	P2 audit catch-up with the DEFERRED ceiling (audit.WithDeferredCeiling).
//	P3 conversion transaction (Convert): ONE BEGIN IMMEDIATE on a pinned
//	   connection, bracketed by the dependency's verified FK-off/restore.
//	P4 reopen proof (the command frontend only).
//
// Inside P3 the phases are: (a) the under-lock EVIDENCE verdict; (b) the
// runtime-state refusal; (c) the raw blob inventory; (d) schema completion;
// (e) the conversions; (f) owner verification; (g) the transaction-bound
// non-reclaiming lifecycle replay and comparison; (h) inventory equality;
// (i) the marker; (j) the receipt; (k) the deferred ceiling — then COMMIT.
//
// Any failure in (b)-(k) rolls the entire transaction back: no key, canonical
// byte, digest, projection row, blob row, marker, receipt or ceiling survives.
// No normal open or pooled read runs inside the lock. Reclaim is structurally
// absent from this path, so the conversion reports zero reclaimed bodies.
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
		return Inspection{StorePath: storePath}, formatInspectionError(storePath, err)
	}
	return Inspection{StorePath: storePath, Format: inspection.Format, Populated: inspection.Populated}, nil
}

// ConvertOptions carries the inputs to the P3 conversion transaction.
type ConvertOptions struct {
	// StorePath is the resolved database path. It labels errors and names the
	// store in refusals.
	StorePath string
	// Archive is the P1 result this run recorded. P3's scoped refusals name it.
	Archive Archive
	// AppliedCatchUpSteps is the number of audit steps this run's P2 catch-up
	// applied (zero when the store was already current). P3's scoped refusals
	// name it.
	AppliedCatchUpSteps int
	// Now is the timestamp source for the receipt and the ceiling. Nil means
	// the wall clock.
	Now func() time.Time
	// beforeBegin is a TEST-ONLY seam invoked after the foreign-key bracket is
	// established and immediately before BEGIN IMMEDIATE, so a test can capture
	// the store's final on-disk digest and prove the conversion transaction
	// wrote nothing. Production never sets it (every production construction
	// site leaves the zero value).
	beforeBegin func()
}

// Report is what one conversion did. It is returned even for the no-op path
// (already current).
type Report struct {
	StorePath          string
	ArchivePath        string
	ArchiveSHA256      string
	AlreadyCurrent     bool
	ReceiptWritten     bool
	FloorVersion       int
	ChangedFields      int
	ChangedMutations   int
	ChangedAllocations int
	PastureRows        int
	Reclaimed          int
}

// Convert runs the P3 conversion transaction against an already-open shared
// handle. The handle MUST be the only writer: the caller (the CLI handler or
// the open path) is responsible for the stopped-writer requirement and for
// closing the handle before any reopen.
//
// It never opens a second connection and never runs a normal open inside the
// conversion transaction.
func Convert(ctx context.Context, db *sql.DB, opts ConvertOptions) (Report, error) {
	if db == nil {
		return Report{}, conversionError("The identity conversion has no database handle.", "An open, exclusive handle is required; the conversion never opens its own.", "Nothing was converted.", "Open the store and retry.", nil)
	}
	report := Report{StorePath: opts.StorePath, ArchivePath: opts.Archive.Path, ArchiveSHA256: opts.Archive.SHA256}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return report, conversionError("The identity conversion couldn't pin a database connection.", err.Error(), "Nothing was converted.", "Stop other writers and retry.", err)
	}
	defer conn.Close()

	restoreFK, err := provenance.PauseForeignKeys(ctx, conn)
	if err != nil {
		return report, conversionError("The identity conversion couldn't pause foreign-key enforcement.", err.Error(), "Nothing was converted; the store is unchanged.", "Confirm no transaction is open on this connection and retry.", err)
	}

	// TEST-ONLY seam: the store is now in its final journal mode and the FK
	// bracket is established, immediately before BEGIN IMMEDIATE. A digest
	// captured here isolates the conversion transaction.
	if opts.beforeBegin != nil {
		opts.beforeBegin()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		restoreErr := restoreFK(err)
		return report, conversionError("The identity conversion transaction could not start.", "SQLite did not grant the exclusive write transaction.", "No store row was changed; the archive remains available.", "Stop other writers, confirm the store is healthy, and retry.", restoreErr)
	}

	opErr := runConversion(ctx, tx, opts, now, &report)
	if opErr != nil {
		_ = tx.Rollback()
		return report, restoreFK(opErr)
	}
	if err := tx.Commit(); err != nil {
		return report, restoreFK(conversionError("The identity conversion could not be committed.", "SQLite rejected the commit after every conversion and verification step succeeded.", "The store keeps its retired identities and the archive remains available.", "Confirm storage health and free space, then retry.", err))
	}
	return report, restoreFK(nil)
}

// runConversion is the single P3 body. It returns nil only when every step
// succeeded and the transaction is ready to commit; the caller rolls back on
// any error.
func runConversion(ctx context.Context, tx *sql.Tx, opts ConvertOptions, now func() time.Time, report *Report) error {
	// P3a: the under-lock EVIDENCE verdict. This is the evidence subset only
	// (marker, receipt, marker_format match, recorded version vs floor, version
	// vs MaxKnown). The populated column is P0 routing and is NOT consulted.
	state, err := EvaluateEvidenceInTx(ctx, tx, opts.StorePath)
	if err != nil {
		return err
	}
	switch state.EvidenceVerdict() {
	case EvidenceNormal:
		report.AlreadyCurrent = true
		return nil
	case EvidenceRefuseUnbackedMark:
		return ScopedP3aRefusal(opts.StorePath, !opts.Archive.Skipped, opts.Archive.Marker, opts.Archive.Path, opts.AppliedCatchUpSteps)
	case EvidenceNewerSchema:
		return audit.NewerSchemaError(state.RecordedVersion, audit.MaxKnownSchemaVersion)
	}
	// EvidenceMarkerless: proceed on both frontends.

	// P3b: refuse non-empty runtime state, never guess vendor bytes.
	if err := refuseRuntimeState(ctx, tx, opts.StorePath, opts.Archive, opts.AppliedCatchUpSteps); err != nil {
		return err
	}

	// P3c: raw evidence inventory, before any write.
	before, err := captureBlobInventory(ctx, tx)
	if err != nil {
		return err
	}

	// P3d: complete the Provenance schema inside this transaction, so a legacy
	// store has the governed_allocation_operations and journal_operations
	// relations conversion and verification read.
	if err := provenance.EnsureSchemaInTx(ctx, tx, provenance.DefaultModelRegistry().Models()); err != nil {
		return conversionError("Pasture couldn't complete the store's Provenance schema inside the conversion transaction.", err.Error(), "The whole conversion transaction rolls back, including the schema additions; the store keeps its retired identities.", "Confirm the store is writable and retry.", err)
	}

	// P3e: dependency-owned columns, canonical mutations, allocation receipts,
	// their owner-derived digests, and Provenance-owned extension contexts.
	dependencyReport, err := provenance.ConvertIDsInTx(ctx, tx, provenance.IDConversionOptions{
		RewriteEffect:  rewritePastureEffect,
		RewriteContext: rewritePastureContext,
	})
	if err != nil {
		return conversionError("Converting the dependency-owned identities failed.", err.Error(), "The whole conversion transaction rolls back; the store keeps its retired identities and the archive remains available.", "Repair the reported column/key from the archive and retry.", err)
	}
	report.ChangedFields = dependencyReport.ChangedFields
	report.ChangedMutations = dependencyReport.ChangedMutations
	report.ChangedAllocations = dependencyReport.ChangedAllocations

	// P3e (pasture): pasture-owned typed identity columns and declared
	// structured fields, in the SAME transaction.
	pastureRows, err := convertPastureIdentities(ctx, tx)
	if err != nil {
		return err
	}
	report.PastureRows = pastureRows

	// P3f: owner integrity on this transaction.
	if err := provenance.VerifyIDConversionInTx(ctx, tx); err != nil {
		return conversionError("The converted store failed owner integrity verification.", err.Error(), "The whole conversion transaction rolls back; the store keeps its retired identities and the archive remains available.", "Restore the matching archived bytes/digest/version and retry.", err)
	}

	// P3g: same-transaction, non-reclaiming lifecycle replay, then comparison
	// against the transaction-bound evidence.
	reader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	if err != nil {
		return conversionError("The transaction-bound lifecycle evidence reader could not be constructed.", err.Error(), "The whole conversion transaction rolls back.", "Confirm the transaction is still open and retry.", err)
	}
	if err := projection.RebuildOccurrencesInTx(ctx, tx, reader); err != nil {
		return conversionError("Replaying the converted lifecycle journal failed.", err.Error(), "The whole conversion transaction rolls back, including every converted key and digest, and no marker is published.", "Repair the reported evidence row from the archive and retry.", err)
	}
	if err := projection.CompareOccurrencesToEvidence(ctx, tx, reader); err != nil {
		return err
	}

	// P3h: raw inventory equality. Conversion must not delete, rewrite or
	// reclaim any raw payload body.
	after, err := captureBlobInventory(ctx, tx)
	if err != nil {
		return err
	}
	if err := compareBlobInventories(before, after); err != nil {
		return err
	}
	report.Reclaimed = 0

	// P3i: the marker, LAST-but-two. PublishIDFormatInTx repeats dependency
	// integrity and SQLite constraint checks before writing.
	if err := provenance.PublishIDFormatInTx(ctx, tx); err != nil {
		return conversionError("Publishing the identity-format marker failed.", err.Error(), "The whole conversion transaction rolls back; no partial marker is published and the store keeps its retired identities.", "Restore the archive and retry.", err)
	}

	// P3j: the cutover receipt, in the SAME transaction as the marker and the
	// floor, so the fence evidence commits together. Written once.
	if err := writeReceipt(ctx, tx, Receipt{
		MarkerFormat:  string(provenance.IDFormatSlash),
		FloorVersion:  audit.MaxKnownSchemaVersion,
		ArchivePath:   opts.Archive.Path,
		ArchiveSHA256: opts.Archive.SHA256,
		CutoverAt:     now().UTC().UnixNano(),
	}); err != nil {
		return err
	}
	report.ReceiptWritten = true
	report.FloorVersion = audit.MaxKnownSchemaVersion

	// P3k: the deferred ceiling step(s), applied by name, LAST.
	if err := audit.RaiseIDFormatCeilingInTx(tx, opts.StorePath, now().UTC().UnixNano()); err != nil {
		return err
	}
	return nil
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
