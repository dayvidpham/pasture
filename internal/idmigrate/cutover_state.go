package idmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// Outcome is the read-only verdict of the cutover state machine. It is the ONE
// classification the guard, both pipeline frontends, the plain-migrate precheck
// and the guidance texts consume, so they can never disagree about whether a
// store is cut over.
type Outcome int

const (
	// OutcomeNormal: marker + matching receipt + recorded version >= floor.
	OutcomeNormal Outcome = iota + 1
	// OutcomeCutover: no marker and no receipt, and the store has no Provenance
	// identity rows (or the file does not exist). The first open is a cutover.
	OutcomeCutover
	// OutcomeRefusePopulated: no marker and no receipt, but Provenance identity
	// rows exist. Only the operator command converts these.
	OutcomeRefusePopulated
	// OutcomeRefuseUnbackedMark: a marker outside a fenced, archived pasture
	// cutover (missing/mismatched receipt, receipt without marker, or a recorded
	// version below the receipt's floor).
	OutcomeRefuseUnbackedMark
	// OutcomeNewerSchema: the recorded audit version is above this build's
	// maximum; a read-only preflight refusal, evaluated before the rest.
	OutcomeNewerSchema
)

func (o Outcome) String() string {
	switch o {
	case OutcomeNormal:
		return "normal"
	case OutcomeCutover:
		return "cutover"
	case OutcomeRefusePopulated:
		return "refuse-populated"
	case OutcomeRefuseUnbackedMark:
		return "refuse-unbacked-mark"
	case OutcomeNewerSchema:
		return "newer-schema"
	default:
		return "unknown"
	}
}

// Receipt is the pasture-owned singleton cutover record. It is evidence, not a
// lookup key: nothing resolves identities through it.
type Receipt struct {
	MarkerFormat  string
	FloorVersion  int
	ArchivePath   string
	ArchiveSHA256 string
	CutoverAt     int64
}

// ReceiptReader is the read surface the state machine needs: an open database
// or the caller's transaction both satisfy it.
type ReceiptReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// receiptTableDDL creates the singleton cutover record. It is created inside the
// conversion transaction (P3j) together with the marker and the floor, so a
// store can never carry one without the others.
const receiptTableDDL = `CREATE TABLE IF NOT EXISTS pasture_id_cutover (
	singleton_id    INTEGER PRIMARY KEY CHECK (singleton_id = 0),
	marker_format   TEXT    NOT NULL,
	floor_version   INTEGER NOT NULL,
	archive_path    TEXT    NOT NULL,
	archive_sha256  TEXT    NOT NULL,
	cutover_at      INTEGER NOT NULL
)`

// ensureReceiptTable creates the receipt table inside the caller's transaction.
func ensureReceiptTable(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, receiptTableDDL); err != nil {
		return conversionError("Pasture couldn't create the cutover receipt record.", err.Error(), "The whole conversion transaction rolls back; the store keeps its retired identities.", "Confirm the store is writable and retry.", err)
	}
	return nil
}

// readReceipt reads the singleton cutover record. It reports (Receipt, false,
// nil) when the table is absent or empty: a store with no receipt is a store
// the fenced cutover has not produced.
func readReceipt(ctx context.Context, q ReceiptReader) (Receipt, bool, error) {
	exists, err := tableExistsReader(ctx, q, "pasture_id_cutover")
	if err != nil {
		return Receipt{}, false, err
	}
	if !exists {
		return Receipt{}, false, nil
	}
	var r Receipt
	err = q.QueryRowContext(ctx,
		`SELECT marker_format, floor_version, archive_path, archive_sha256, cutover_at FROM pasture_id_cutover WHERE singleton_id = 0`,
	).Scan(&r.MarkerFormat, &r.FloorVersion, &r.ArchivePath, &r.ArchiveSHA256, &r.CutoverAt)
	switch err {
	case sql.ErrNoRows:
		return Receipt{}, false, nil
	case nil:
		return r, true, nil
	default:
		return Receipt{}, false, conversionError("Pasture couldn't read the cutover receipt.", err.Error(), "No store data was changed.", "Confirm the store is readable and retry.", err)
	}
}

// writeReceipt writes the singleton cutover record inside the caller's
// transaction. It is written once, never overwritten.
func writeReceipt(ctx context.Context, tx *sql.Tx, r Receipt) error {
	if err := ensureReceiptTable(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pasture_id_cutover (singleton_id, marker_format, floor_version, archive_path, archive_sha256, cutover_at)
		 VALUES (0, ?, ?, ?, ?, ?)`,
		r.MarkerFormat, r.FloorVersion, r.ArchivePath, r.ArchiveSHA256, r.CutoverAt,
	); err != nil {
		return conversionError("Pasture couldn't write the cutover receipt.", err.Error(), "The whole conversion transaction rolls back; no marker is published.", "Confirm the store is writable and retry.", err)
	}
	return nil
}

// CutoverState is the full read-only evidence the state machine disposes over.
type CutoverState struct {
	StorePath string
	// FileExists is the observation that gates P1's archive skip.
	FileExists bool
	// Format is the dependency's inspected identity format.
	Format provenance.IDFormat
	// MarkerPresent is true when the store carries any identity-format marker.
	MarkerPresent bool
	// ReceiptPresent and ReceiptMatches describe the pasture receipt.
	ReceiptPresent bool
	ReceiptMatches bool
	Receipt        Receipt
	// RecordedVersion is the audit schema version (1 when no meta table exists).
	RecordedVersion int
	// Populated is the dependency's EXISTS probe over Provenance identity tables.
	// It is a P0 ROUTING input only; P3a must not re-dispose on it.
	Populated bool
	Outcome   Outcome
}

// EvaluateCutover runs the read-only state machine against an open reader. The
// caller is responsible for the single-snapshot read transaction (see
// EvaluateCutoverOnHandle); the guard and both pipeline frontends use this one
// function so they can never disagree.
func evaluateCutover(ctx context.Context, q ReceiptReader, storePath string, fileExists bool) (CutoverState, error) {
	state := CutoverState{StorePath: storePath, FileExists: fileExists}
	if !fileExists {
		state.Outcome = OutcomeCutover
		return state, nil
	}

	version, err := readRecordedVersion(ctx, q)
	if err != nil {
		return CutoverState{}, err
	}
	state.RecordedVersion = version

	if version > audit.MaxKnownSchemaVersion {
		state.Outcome = OutcomeNewerSchema
		return state, nil
	}

	inspection, err := provenance.InspectIDFormat(ctx, q)
	if err != nil {
		return CutoverState{}, formatInspectionError(storePath, err)
	}
	state.Format = inspection.Format
	state.MarkerPresent = inspection.Format == provenance.IDFormatSlash
	state.Populated = inspection.Populated

	receipt, hasReceipt, err := readReceipt(ctx, q)
	if err != nil {
		return CutoverState{}, err
	}
	state.Receipt = receipt
	state.ReceiptPresent = hasReceipt
	if hasReceipt {
		state.ReceiptMatches = state.MarkerPresent && receipt.MarkerFormat == string(inspection.Format)
	}

	switch {
	case state.MarkerPresent && state.ReceiptMatches && version >= receipt.FloorVersion:
		state.Outcome = OutcomeNormal
	case state.MarkerPresent:
		// Marker present with a missing/mismatched receipt, or a recorded version
		// below the receipt's floor: an out-of-band or inconsistent mark.
		state.Outcome = OutcomeRefuseUnbackedMark
	case state.ReceiptPresent:
		// A receipt without a marker is an inconsistent record.
		state.Outcome = OutcomeRefuseUnbackedMark
	case state.Populated:
		state.Outcome = OutcomeRefusePopulated
	default:
		state.Outcome = OutcomeCutover
	}
	return state, nil
}

// EvaluateCutoverOnHandle evaluates the state machine in ONE deferred read
// transaction, so the marker, the receipt and the recorded version come from a
// single snapshot and cannot be torn by a concurrent commit.
func EvaluateCutoverOnHandle(ctx context.Context, db *sql.DB, storePath string, fileExists bool) (CutoverState, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return CutoverState{}, conversionError("Pasture couldn't start the read-only state check.", err.Error(), "No store data was changed.", "Confirm the store is readable and retry.", err)
	}
	defer func() { _ = tx.Rollback() }()
	return evaluateCutover(ctx, tx, storePath, fileExists)
}

// InspectOpenState evaluates the state machine against a store path, opening
// its own short-lived READ-ONLY handle. An absent store is CUTOVER (the first
// open creates and cuts it over). A read-only open failure that is NOT a
// missing file fails closed as a storage error rather than being deferred.
func InspectOpenState(ctx context.Context, storePath string) (CutoverState, error) {
	if !StatStore(storePath) {
		return CutoverState{StorePath: storePath, FileExists: false, Outcome: OutcomeCutover}, nil
	}
	db, err := dbconn.OpenReadOnlyDB(storePath)
	if err != nil {
		return CutoverState{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Pasture couldn't inspect the identity format of the database at %q.", storePath),
			Why: "The read-only inspection handle could not be opened even though the file exists, so the store's " +
				"identity format cannot be confirmed.",
			Where:  "Checking the store identity format before open (internal/idmigrate/cutover_state.go in idmigrate.InspectOpenState).",
			Impact: "This open stopped before schema work, actor registration, genesis reconciliation or journal decode.",
			Fix: "1. Confirm the file is a readable SQLite database:\n     sqlite3 <path> 'PRAGMA integrity_check'\n" +
				"2. If it is held by another process, stop that writer and retry.",
			Cause: err,
		}
	}
	defer db.Close()
	return EvaluateCutoverOnHandle(ctx, db, storePath, true)
}

// EvidenceVerdict is the P3a under-lock verdict. It consumes the EVIDENCE rows
// only (marker, receipt, marker_format match, recorded version vs floor, version
// vs MaxKnown). The populated column is deliberately NOT consulted: it is a P0
// routing input, and P2's catch-up populates the Provenance tables by design.
type EvidenceVerdict int

const (
	EvidenceNormal EvidenceVerdict = iota + 1
	EvidenceRefuseUnbackedMark
	EvidenceNewerSchema
	EvidenceMarkerless
)

// EvidenceVerdict classifies the state for the under-lock P3a decision.
func (s CutoverState) EvidenceVerdict() EvidenceVerdict {
	switch s.Outcome {
	case OutcomeNormal:
		return EvidenceNormal
	case OutcomeRefuseUnbackedMark:
		return EvidenceRefuseUnbackedMark
	case OutcomeNewerSchema:
		return EvidenceNewerSchema
	default:
		return EvidenceMarkerless
	}
}

// evaluateEvidenceInTx evaluates the P3a verdict inside the caller's conversion
// transaction. It is the same evidence read as the P0 state machine.
func evaluateEvidenceInTx(ctx context.Context, tx *sql.Tx, storePath string) (CutoverState, error) {
	return evaluateCutover(ctx, tx, storePath, true)
}

// StatStore reports whether the store file exists, without creating it.
func StatStore(storePath string) bool {
	_, err := os.Stat(storePath)
	return err == nil
}

// RecordedSchemaVersionPath reads the audit schema version through a
// short-lived read-only handle, reporting 1 (legacy pre-v2) when the file or the
// meta table is absent. It is used to count how many catch-up steps a run
// applied.
func RecordedSchemaVersionPath(storePath string) int {
	db, err := dbconn.OpenReadOnlyDB(storePath)
	if err != nil {
		return 1
	}
	defer db.Close()
	version, err := readRecordedVersion(context.Background(), db)
	if err != nil {
		return 1
	}
	return version
}

// readRecordedVersion reads MAX(version) from audit_schema_meta. An absent table
// or an empty/NULL row is version 1 (legacy pre-v2), matching the migrator.
func readRecordedVersion(ctx context.Context, q ReceiptReader) (int, error) {
	exists, err := tableExistsReader(ctx, q, "audit_schema_meta")
	if err != nil {
		return 0, err
	}
	if !exists {
		return 1, nil
	}
	var version sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MAX(version) FROM audit_schema_meta`).Scan(&version); err != nil {
		return 0, conversionError("Pasture couldn't read the recorded audit schema version.", err.Error(), "No store data was changed.", "Confirm the store is readable and retry.", err)
	}
	if !version.Valid {
		return 1, nil
	}
	return int(version.Int64), nil
}

// ArchiveMarker is the marker evidence P1 records by reading the staged
// archive. It is the observed fact the scoped P3a refusal states.
type ArchiveMarker struct {
	Present bool
}

// readArchiveMarker reads the provenance_id_format singleton from an already
// open reader on the staged archive. It is the same fact the guard's inspection
// reads; it is present/absent, and it is never inferred from timing.
func readArchiveMarker(ctx context.Context, q ReceiptReader) (ArchiveMarker, error) {
	inspection, err := provenance.InspectIDFormat(ctx, q)
	if err != nil {
		return ArchiveMarker{}, formatInspectionError("the staged archive", err)
	}
	return ArchiveMarker{Present: inspection.Format == provenance.IDFormatSlash}, nil
}

func formatInspectionError(storePath string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("Pasture couldn't read the identity format of the database at %q.", storePath),
		Why: "The dependency's read-only format inspection failed, or the store's identity-format marker " +
			"is malformed or unknown to this build.",
		Where:  "Reading the store identity format (internal/idmigrate/cutover_state.go).",
		Impact: "No store data was read or written. The format cannot be confirmed, so no open or conversion proceeds.",
		Fix: "1. Confirm the file is a readable SQLite database:\n" +
			"     sqlite3 <path> 'PRAGMA integrity_check'\n" +
			"2. If the marker looks wrong, restore the consistent archive of the store and retry.",
		Cause: cause,
	}
}
