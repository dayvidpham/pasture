package projection

import (
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
	query := provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{occurrenceEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	type row struct {
		journalID  provenance.JournalID
		recordedAt int64
		actor      string
		payload    occurrencePayload
	}
	var rows []row
	for {
		page, err := journal.Facts().QueryEvidence(query)
		if err != nil {
			return ReclaimOutcome{}, projectionError("Lifecycle occurrence evidence could not be read for projection rebuild.", "The bounded journal query failed before the disposable projection was touched.", "No projection rows were changed.", "Repair the journal query failure and retry the rebuild.", err)
		}
		for _, evidence := range page.Rows {
			var payload occurrencePayload
			if err := json.Unmarshal(evidence.Payload, &payload); err != nil {
				return ReclaimOutcome{}, projectionError("A lifecycle occurrence journal row could not be decoded.", fmt.Sprintf("Evidence row %d does not contain the canonical occurrence envelope.", evidence.JournalID), "No projection rows were changed.", "Repair or restore the malformed journal row before rebuilding.", err)
			}
			rows = append(rows, row{journalID: evidence.JournalID, recordedAt: evidence.RecordedAt.UnixNano(), actor: evidence.EffectiveActorID.String(), payload: payload})
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM lifecycle_occurrences`); err != nil {
		return ReclaimOutcome{}, projectionError("The old lifecycle occurrence projection could not be cleared.", "SQLite rejected the first statement of the atomic rebuild.", "The rebuild transaction will roll back, preserving the old projection.", "Confirm the database is writable and retry.", err)
	}
	snapshot := provenance.JournalID(0)
	if len(rows) > 0 {
		snapshot = rows[len(rows)-1].journalID
	}
	for _, item := range rows {
		envelope, err := json.Marshal(item.payload.Envelope)
		if err != nil {
			return ReclaimOutcome{}, projectionError("A lifecycle envelope could not be encoded for projection.", "The journal payload decoded but its typed envelope could not be serialized.", "The rebuild transaction will roll back.", "Report the incompatible envelope type.", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO lifecycle_occurrences(journal_id, contract, event_kind, received_at, actor_id, capture_disposition, payload_digest, envelope_json, snapshot_journal_id) VALUES(?,?,?,?,?,?,?,?,?)`, item.journalID, item.payload.Contract, item.payload.Event, item.recordedAt, item.actor, item.payload.Capture, item.payload.Body, envelope, snapshot); err != nil {
			return ReclaimOutcome{}, projectionError("A lifecycle occurrence could not be projected.", fmt.Sprintf("SQLite rejected replay of journal row %d, commonly because its content-addressed payload blob is absent.", item.journalID), "The rebuild transaction will roll back without exposing a partial projection.", "Restore the referenced blob or repair the journal before retrying.", err)
		}
		for index, binding := range item.payload.Bindings {
			if _, err := tx.ExecContext(ctx, `INSERT INTO lifecycle_occurrence_bindings(journal_id,binding_index,binding_kind,native_name,binding_value) VALUES(?,?,?,?,?)`, item.journalID, index, binding.Kind, []byte(binding.NativeName), []byte(binding.Value)); err != nil {
				return ReclaimOutcome{}, projectionError("A lifecycle occurrence binding could not be projected.", fmt.Sprintf("SQLite rejected binding %d for journal row %d.", index, item.journalID), "The rebuild transaction will roll back without exposing a partial projection.", "Repair malformed binding evidence and retry.", err)
			}
		}
	}
	// THE RECLAIM RUNS HERE AND NOWHERE ELSE: after the projection rows are
	// re-inserted and before the commit, in this same transaction, so the
	// projection it reads is exactly the journal as of the snapshot.
	outcome := ReclaimOutcome{}
	deleted, reclaimErr, fatal := reclaimOrphanPayloads(ctx, tx, snapshotInstant, reclaim.Window)
	if fatal {
		return ReclaimOutcome{}, projectionError("The orphan payload reclaim left the rebuild transaction in an unknown state.", reclaimErr.Error(), "The rebuild transaction will roll back; the prior projection remains authoritative and nothing was deleted.", "Confirm storage health and run a read command again.", reclaimErr)
	}
	if reclaimErr != nil {
		outcome.Failure = reclaimErr
	}
	outcome.Reclaimed = deleted
	if err := tx.Commit(); err != nil {
		return ReclaimOutcome{}, projectionError("The lifecycle occurrence projection could not be committed.", "SQLite rejected the atomic replacement after replay completed.", "The prior projection remains authoritative.", "Confirm storage health and retry.", err)
	}
	committed = true
	return outcome, nil
}

func projectionError(what, why, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryStorage, What: what, Why: why, Where: "Rebuilding lifecycle occurrence projections (internal/lifecycle/projection/rebuild.go in projection.RebuildOccurrences).", Impact: impact, Fix: fix, Cause: cause}
}
