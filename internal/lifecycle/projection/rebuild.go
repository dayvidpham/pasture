package projection

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

const occurrenceEvidenceKind = provenance.EvidenceKind("pasture.lifecycle.occurrence.v1")

type occurrencePayload struct {
	Contract string                      `json:"contract"`
	Event    model.ContractEventKind     `json:"event"`
	Envelope model.OccurrenceEnvelopeRef `json:"envelope"`
	Bindings []model.NativeBinding       `json:"bindings"`
	Capture  model.CaptureDisposition    `json:"capture"`
	Body     string                      `json:"body_digest"`
}

// EvidencePageReader is the narrow evidence surface the non-reclaiming replay
// needs. It is satisfied BOTH by the ordinary pooled journal
// (provenance.FactQueryAPI) and by provenance.TransactionEvidenceReader, which
// reads only the caller's active SQL transaction. The migration binds the
// transaction reader so every converted evidence row, actor join and digest it
// replays comes from the SAME uncommitted view, never a second connection.
type EvidencePageReader interface {
	QueryEvidence(query provenance.EvidenceQuery) (provenance.EvidencePage, error)
}

// occurrenceRow is one decoded occurrence envelope in JournalID order.
type occurrenceRow struct {
	journalID  provenance.JournalID
	recordedAt int64
	actor      string
	payload    occurrencePayload
}

// readOccurrenceRows pages the occurrence evidence through reader and decodes
// every envelope. The first page establishes the snapshot watermark when the
// caller did not pin one; later pages carry it, so the read is one bounded
// snapshot. It is shared by the ordinary rebuild (pooled journal) and the
// transaction-bound migration replay (transaction reader).
func readOccurrenceRows(reader EvidencePageReader) ([]occurrenceRow, error) {
	query := provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{occurrenceEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var rows []occurrenceRow
	for {
		page, err := reader.QueryEvidence(query)
		if err != nil {
			return nil, projectionError("Lifecycle occurrence evidence could not be read for projection rebuild.", "The bounded journal query failed before the disposable projection was touched.", "No projection rows were changed.", "Repair the journal query failure and retry the rebuild.", err)
		}
		for _, evidence := range page.Rows {
			var payload occurrencePayload
			if err := json.Unmarshal(evidence.Payload, &payload); err != nil {
				return nil, projectionError("A lifecycle occurrence journal row could not be decoded.", fmt.Sprintf("Evidence row %d does not contain the canonical occurrence envelope.", evidence.JournalID), "No projection rows were changed.", "Repair or restore the malformed journal row before rebuilding.", err)
			}
			rows = append(rows, occurrenceRow{journalID: evidence.JournalID, recordedAt: evidence.RecordedAt.UnixNano(), actor: evidence.EffectiveActorID.String(), payload: payload})
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	return rows, nil
}

// writeOccurrenceRows replaces every disposable occurrence/binding row with the
// supplied journal-truth rows inside tx. It never begins, commits or rolls
// back, and it never reclaims: transaction ownership belongs to the caller.
func writeOccurrenceRows(ctx context.Context, tx *sql.Tx, rows []occurrenceRow) error {
	// DELETE THE CHILD ROWS FIRST, EXPLICITLY. The bindings reference the
	// occurrence with ON DELETE CASCADE, but the conversion runs inside
	// provenance.PauseForeignKeys: with enforcement paused the cascade does NOT
	// fire, so deleting only the parent leaves the old binding rows behind and
	// re-inserting them collides on (journal_id, binding_index). Deleting the
	// child first is correct with enforcement both paused and on.
	if _, err := tx.ExecContext(ctx, `DELETE FROM lifecycle_occurrence_bindings`); err != nil {
		return projectionError("The old lifecycle occurrence bindings could not be cleared.", "SQLite rejected the binding delete that precedes the atomic rebuild.", "The rebuild transaction will roll back, preserving the old projection.", "Confirm the database is writable and retry.", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM lifecycle_occurrences`); err != nil {
		return projectionError("The old lifecycle occurrence projection could not be cleared.", "SQLite rejected an early statement of the atomic rebuild.", "The rebuild transaction will roll back, preserving the old projection.", "Confirm the database is writable and retry.", err)
	}
	snapshot := provenance.JournalID(0)
	if len(rows) > 0 {
		snapshot = rows[len(rows)-1].journalID
	}
	for _, item := range rows {
		envelope, err := json.Marshal(item.payload.Envelope)
		if err != nil {
			return projectionError("A lifecycle envelope could not be encoded for projection.", "The journal payload decoded but its typed envelope could not be serialized.", "The rebuild transaction will roll back.", "Report the incompatible envelope type.", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO lifecycle_occurrences(journal_id, contract, event_kind, received_at, actor_id, capture_disposition, payload_digest, envelope_json, snapshot_journal_id) VALUES(?,?,?,?,?,?,?,?,?)`, item.journalID, item.payload.Contract, item.payload.Event, item.recordedAt, item.actor, item.payload.Capture, item.payload.Body, envelope, snapshot); err != nil {
			return projectionError("A lifecycle occurrence could not be projected.", fmt.Sprintf("SQLite rejected replay of journal row %d, commonly because its content-addressed payload blob is absent.", item.journalID), "The rebuild transaction will roll back without exposing a partial projection.", "Restore the referenced blob or repair the journal before retrying.", err)
		}
		for index, binding := range item.payload.Bindings {
			if _, err := tx.ExecContext(ctx, `INSERT INTO lifecycle_occurrence_bindings(journal_id,binding_index,binding_kind,native_name,binding_value) VALUES(?,?,?,?,?)`, item.journalID, index, binding.Kind, []byte(binding.NativeName), []byte(binding.Value)); err != nil {
				return projectionError("A lifecycle occurrence binding could not be projected.", fmt.Sprintf("SQLite rejected binding %d for journal row %d.", index, item.journalID), "The rebuild transaction will roll back without exposing a partial projection.", "Repair malformed binding evidence and retry.", err)
			}
		}
	}
	return nil
}

// RebuildOccurrences derives the complete occurrence projection from journal
// evidence in JournalID order, and reclaims orphan payload blobs inside the
// same write transaction. The projection is disposable: journal truth is read
// first, then one transaction replaces all projection rows, and after the rows
// are re-inserted, where the projection is authoritative, the bounded reclaim
// runs under a savepoint (see reclaimOrphanPayloads for the safety argument).
// A reclaim failure never fails the rebuild: it is returned in the outcome,
// and the caller that owns a diagnostic stream prints it. Every read command
// reaches this through the tasks wrapper; there is no rebuild without the
// reclaim.
func RebuildOccurrences(ctx context.Context, journal provenance.Journal, db *sql.DB, options RebuildOptions) (ReclaimOutcome, error) {
	if err := options.validate(); err != nil {
		return ReclaimOutcome{}, err
	}
	return rebuildOccurrences(ctx, journal, db, options)
}

func rebuildOccurrences(ctx context.Context, journal provenance.Journal, db *sql.DB, reclaim RebuildOptions) (ReclaimOutcome, error) {
	if journal == nil || db == nil {
		return ReclaimOutcome{}, projectionError("The lifecycle occurrence projection cannot be rebuilt.", "Both the Provenance journal and projection database handle are required.", "No projection rows were changed.", "Open the unified store and retry the rebuild.", nil)
	}
	// THE SNAPSHOT INSTANT IS TAKEN BEFORE THE JOURNAL IS READ. The reclaim
	// ages orphan blobs against this instant and never against the instant
	// it deletes at: a row committed after this instant is absent from the
	// projection, and its blob is younger than the writer window relative to
	// it, so it cannot be selected however slow the rebuild is.
	snapshotInstant := reclaim.Clock.Now()
	rows, err := readOccurrenceRows(journal.Facts())
	if err != nil {
		return ReclaimOutcome{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ReclaimOutcome{}, projectionError("The lifecycle occurrence projection transaction could not start.", "SQLite did not grant the bounded rebuild transaction.", "Existing projection rows remain unchanged.", "Confirm database health and retry.", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := writeOccurrenceRows(ctx, tx, rows); err != nil {
		return ReclaimOutcome{}, err
	}
	// THE RECLAIM RUNS HERE AND NOWHERE ELSE: after the projection rows are
	// re-inserted and before the commit, in this same transaction, so the
	// projection it reads is exactly the journal as of the snapshot.
	outcome := ReclaimOutcome{}
	result, reclaimErr, fatal := reclaimOrphanPayloads(ctx, tx, snapshotInstant, reclaim.Window, reclaim.Reclaim)
	if fatal {
		return ReclaimOutcome{}, projectionError("The orphan payload reclaim left the rebuild transaction in an unknown state.", reclaimErr.Error(), "The rebuild transaction will roll back; the prior projection remains authoritative and nothing was deleted.", "Confirm storage health and run a read command again.", reclaimErr)
	}
	if reclaimErr != nil {
		outcome.Failure = reclaimErr
	}
	outcome.Reclaimed = result.deleted
	outcome.ReclaimedBytes = result.bytes
	if err := tx.Commit(); err != nil {
		return ReclaimOutcome{}, projectionError("The lifecycle occurrence projection could not be committed.", "SQLite rejected the atomic replacement after replay completed.", "The prior projection remains authoritative.", "Confirm storage health and retry.", err)
	}
	committed = true
	return outcome, nil
}

// RebuildOccurrencesInTx recreates the disposable occurrence projection from
// converted journal truth inside the CALLER's transaction, reading that truth
// through reader. It is the offline identity-format conversion's same-transaction
// replay: the reader is bound to the caller's SQL transaction, so every evidence
// row, effective actor and digest it reads reflects the converted rows that have
// not been committed yet.
//
// RECLAIM IS STRUCTURALLY ABSENT, not disabled. This entry has no clock, no
// window and no savepoint, so no caller can reclaim by passing a zero window or
// a default: there is no reclaim call to reach. The migration must preserve the
// complete raw blob inventory, so reclaim does not belong in this path at all.
// The ordinary RebuildOccurrences wrapper keeps its reclaim, its snapshot
// timing and its outcome unchanged, and both entries share the decode/write
// functions above.
//
// It never calls Begin, Commit or Rollback: transaction ownership stays with
// the migration, which rolls the whole thing back on any error.
func RebuildOccurrencesInTx(ctx context.Context, tx *sql.Tx, reader EvidencePageReader) error {
	if ctx == nil || tx == nil || reader == nil {
		return projectionError("The transaction-bound lifecycle replay is incompletely wired.", "The caller's SQL transaction and its transaction-bound evidence reader are both required; a pooled reader would read past the conversion.", "No projection rows were changed.", "Pass the active migration transaction and the reader bound to it.", nil)
	}
	rows, err := readOccurrenceRows(reader)
	if err != nil {
		return err
	}
	return writeOccurrenceRows(ctx, tx, rows)
}

func projectionError(what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryStorage, What: what, Why: why, Where: "Rebuilding lifecycle occurrence projections (internal/lifecycle/projection/rebuild.go in projection.RebuildOccurrences).", Impact: impact, Fix: fix, Cause: cause}
}

// expectedOccurrence is one occurrence as the converted journal truth declares
// it. It is built only from the transaction-bound evidence read, so a comparison
// against it never trusts the projection rows it checks.
type expectedOccurrence struct {
	contract      string
	event         uint16
	capture       uint8
	receivedAt    int64
	actor         string
	payloadDigest string
	envelope      []byte
	bindings      []expectedBinding
}

type expectedBinding struct {
	index int
	kind  uint8
	name  []byte
	value []byte
}

// CompareOccurrencesToEvidence verifies that the disposable occurrence and
// binding rows committed in tx equal the journal truth read through reader — the
// caller's transaction view. It is the conversion's semantic comparison: it
// catches a replay that inserted rows the evidence does not contain, dropped a
// row the evidence does contain, or wrote a different actor, envelope, digest or
// binding bytes. It never writes, never reclaims and never owns the transaction.
func CompareOccurrencesToEvidence(ctx context.Context, tx *sql.Tx, reader EvidencePageReader) error {
	if ctx == nil || tx == nil || reader == nil {
		return projectionError("The lifecycle projection comparison is incompletely wired.", "The caller's transaction and its evidence reader are required.", "No comparison was performed.", "Pass the active transaction and its bound reader.", nil)
	}
	evidenceRows, err := readOccurrenceRows(reader)
	if err != nil {
		return err
	}
	expected := make(map[provenance.JournalID]expectedOccurrence, len(evidenceRows))
	var snapshot provenance.JournalID
	for _, row := range evidenceRows {
		envelope, err := json.Marshal(row.payload.Envelope)
		if err != nil {
			return projectionError("A lifecycle envelope could not be encoded for comparison.", "The decoded envelope could not be serialized.", "No comparison was performed.", "Report the incompatible envelope type.", err)
		}
		bindings := make([]expectedBinding, 0, len(row.payload.Bindings))
		for index, binding := range row.payload.Bindings {
			bindings = append(bindings, expectedBinding{index: index, kind: uint8(binding.Kind), name: []byte(binding.NativeName), value: []byte(binding.Value)})
		}
		expected[row.journalID] = expectedOccurrence{contract: row.payload.Contract, event: uint16(row.payload.Event), capture: uint8(row.payload.Capture), receivedAt: row.recordedAt, actor: row.actor, payloadDigest: row.payload.Body, envelope: envelope, bindings: bindings}
		if row.journalID > snapshot {
			snapshot = row.journalID
		}
	}

	rows, err := tx.QueryContext(ctx, `SELECT journal_id, contract, event_kind, received_at, actor_id, capture_disposition, payload_digest, envelope_json, snapshot_journal_id FROM lifecycle_occurrences ORDER BY journal_id`)
	if err != nil {
		return projectionError("The committed lifecycle projection could not be read for comparison.", "SQLite rejected the projection read inside the conversion transaction.", "The conversion aborts.", "Repair storage and retry.", err)
	}
	type projectedOccurrence struct {
		contract      string
		event         uint16
		capture       uint8
		receivedAt    int64
		actor         string
		payloadDigest string
		envelope      []byte
		snapshot      int64
	}
	actual := make(map[provenance.JournalID]projectedOccurrence, len(expected))
	for rows.Next() {
		var journalID int64
		var item projectedOccurrence
		var envelope []byte
		if err := rows.Scan(&journalID, &item.contract, &item.event, &item.receivedAt, &item.actor, &item.capture, &item.payloadDigest, &envelope, &item.snapshot); err != nil {
			_ = rows.Close()
			return projectionError("A committed lifecycle projection row could not be decoded for comparison.", err.Error(), "The conversion aborts.", "Repair the projection and retry.", err)
		}
		item.envelope = envelope
		actual[provenance.JournalID(journalID)] = item
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(expected) != len(actual) {
		return projectionError("The replayed lifecycle projection does not match the converted journal.", fmt.Sprintf("The journal truth has %d occurrence(s) but the projection has %d.", len(expected), len(actual)), "The conversion aborts and the whole transaction rolls back.", "Repair the journal evidence or the projection and retry.", nil)
	}
	for journalID, want := range expected {
		got, ok := actual[journalID]
		if !ok {
			return projectionError("A converted journal occurrence is missing from the replayed projection.", fmt.Sprintf("Journal row %d has evidence but no projection row.", journalID), "The conversion aborts and the whole transaction rolls back.", "Repair the replay path and retry.", nil)
		}
		if got.contract != want.contract || got.event != want.event || got.capture != want.capture || got.receivedAt != want.receivedAt || got.actor != want.actor || got.payloadDigest != want.payloadDigest || !bytes.Equal(got.envelope, want.envelope) {
			return projectionError("A replayed lifecycle projection row disagrees with the converted journal.", fmt.Sprintf("Journal row %d has a different contract, event, capture, actor, digest, timestamp or envelope in the projection.", journalID), "The conversion aborts and the whole transaction rolls back.", "Repair the replay path and retry.", nil)
		}
		if got.snapshot != int64(snapshot) {
			return projectionError("A replayed lifecycle projection snapshot watermark is wrong.", fmt.Sprintf("Journal row %d carries snapshot %d, expected %d.", journalID, got.snapshot, snapshot), "The conversion aborts and the whole transaction rolls back.", "Repair the replay path and retry.", nil)
		}
	}
	return compareBindingRows(ctx, tx, expected)
}

func compareBindingRows(ctx context.Context, tx *sql.Tx, expected map[provenance.JournalID]expectedOccurrence) error {
	rows, err := tx.QueryContext(ctx, `SELECT journal_id, binding_index, binding_kind, native_name, binding_value FROM lifecycle_occurrence_bindings ORDER BY journal_id, binding_index`)
	if err != nil {
		return projectionError("The committed lifecycle bindings could not be read for comparison.", "SQLite rejected the binding read inside the conversion transaction.", "The conversion aborts.", "Repair storage and retry.", err)
	}
	defer rows.Close()
	position := map[provenance.JournalID]int{}
	for rows.Next() {
		var journalID int64
		var bindingIndex int
		var kind uint8
		var name, value []byte
		if err := rows.Scan(&journalID, &bindingIndex, &kind, &name, &value); err != nil {
			return projectionError("A committed lifecycle binding row could not be decoded for comparison.", err.Error(), "The conversion aborts.", "Repair the projection and retry.", err)
		}
		want, ok := expected[provenance.JournalID(journalID)]
		if !ok {
			return projectionError("The replayed projection has a binding for a journal row that has no evidence.", fmt.Sprintf("Journal row %d.", journalID), "The conversion aborts and the whole transaction rolls back.", "Repair the replay path and retry.", nil)
		}
		at := position[provenance.JournalID(journalID)]
		if at >= len(want.bindings) {
			return projectionError("The replayed projection has more bindings than the journal evidence.", fmt.Sprintf("Journal row %d.", journalID), "The conversion aborts and the whole transaction rolls back.", "Repair the replay path and retry.", nil)
		}
		expectedBinding := want.bindings[at]
		if expectedBinding.index != bindingIndex || expectedBinding.kind != kind || !bytes.Equal(expectedBinding.name, name) || !bytes.Equal(expectedBinding.value, value) {
			return projectionError("A replayed lifecycle binding disagrees with the converted journal.", fmt.Sprintf("Journal row %d binding %d.", journalID, bindingIndex), "The conversion aborts and the whole transaction rolls back.", "Repair the replay path and retry.", nil)
		}
		position[provenance.JournalID(journalID)] = at + 1
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for journalID, want := range expected {
		if position[journalID] != len(want.bindings) {
			return projectionError("A replayed lifecycle row is missing bindings the journal evidence declares.", fmt.Sprintf("Journal row %d.", journalID), "The conversion aborts and the whole transaction rolls back.", "Repair the replay path and retry.", nil)
		}
	}
	return nil
}
