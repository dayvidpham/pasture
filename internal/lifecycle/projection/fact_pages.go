package projection

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/provenance"
)

// OccurrenceEvidenceKinds is the one closed inventory shared by occurrence
// scans and session publication fences. Callers receive a copy; adding a new
// codec must update this inventory rather than a second watermark query.
func OccurrenceEvidenceKinds() []provenance.EvidenceKind {
	return slices.Clone(occurrenceEvidenceKinds)
}

// OccurrenceFactPage retains at most one bounded page, with the global journal
// snapshot that certifies its scan. No payload body or pooled lease escapes it.
type OccurrenceFactPage struct {
	Facts                []OccurrenceFact
	SnapshotMaxJournalID provenance.JournalID
	Next                 *provenance.FactCursor
}

// ReadOccurrenceFactPage reads journal truth without rebuilding another read
// model or resolving semantic envelopes. A zero snapshot requests CURRENT
// evidence: when AfterJournalID is nonzero, first capture the current watermark
// and then query after the cursor AT THAT WATERMARK. Using the cursor itself as
// the snapshot would hide every newly committed occurrence.
//
// The reader MUST be bound to tx: the kind guard and evidence query must see
// the same store and transaction snapshot. Production constructs it with
// provenance.NewTransactionEvidenceReader(ctx, tx).
func ReadOccurrenceFactPage(ctx context.Context, reader EvidencePageReader, tx *sql.Tx, request provenance.FactPageRequest) (OccurrenceFactPage, error) {
	if ctx == nil {
		return OccurrenceFactPage{}, fmt.Errorf("read lifecycle occurrence page: context is absent; supply the caller's live replay context before reading evidence")
	}
	if err := ctx.Err(); err != nil {
		return OccurrenceFactPage{}, err
	}
	if reader == nil || tx == nil || request.Limit < 1 || request.Limit > provenance.MaxFactPageSize {
		return OccurrenceFactPage{}, fmt.Errorf("read lifecycle occurrence page: transaction, bound reader or page limit is invalid; supply a live transaction, its evidence reader and a limit between 1 and %d", provenance.MaxFactPageSize)
	}
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}}, Kinds: OccurrenceEvidenceKinds(), Page: request}
	if request.SnapshotMaxJournalID == 0 && request.AfterJournalID != 0 {
		current := query
		current.Page = provenance.FactPageRequest{Limit: 1}
		page, err := reader.QueryEvidence(current)
		if err != nil {
			return OccurrenceFactPage{}, err
		}
		if request.AfterJournalID > page.SnapshotMaxJournalID {
			return OccurrenceFactPage{}, fmt.Errorf("read lifecycle occurrence page: certified cursor %d exceeds current journal %d; restore the journal or rebuild the session cache from a verified store", request.AfterJournalID, page.SnapshotMaxJournalID)
		}
		query.Page.SnapshotMaxJournalID = page.SnapshotMaxJournalID
	}
	page, err := reader.QueryEvidence(query)
	if err != nil {
		return OccurrenceFactPage{}, err
	}
	if err := RefuseUnknownOccurrenceKinds(ctx, tx, request.AfterJournalID, page.SnapshotMaxJournalID); err != nil {
		return OccurrenceFactPage{}, err
	}
	result := OccurrenceFactPage{SnapshotMaxJournalID: page.SnapshotMaxJournalID, Next: page.Next, Facts: make([]OccurrenceFact, 0, len(page.Rows))}
	validated := newOccurrenceValidationSet()
	for _, evidence := range page.Rows {
		if err := ctx.Err(); err != nil {
			return OccurrenceFactPage{}, err
		}
		var payload occurrencePayload
		if err := decodeOccurrence(evidence.EvidenceKind, evidence.Payload, evidence.ContentDigest, &payload); err != nil {
			return OccurrenceFactPage{}, fmt.Errorf("decode lifecycle occurrence %d during bounded session replay: %w; restore the damaged journal row before retrying", evidence.JournalID, err)
		}
		contract, err := parseOccurrenceContract(payload.Contract)
		if err != nil {
			return OccurrenceFactPage{}, err
		}
		if evidence.EvidenceKind == occurrenceEvidenceKindV3 {
			owner, ok := reader.(receipt.DefinitionReader)
			if !ok {
				return OccurrenceFactPage{}, fmt.Errorf("read lifecycle occurrence %d: definition owner resolution is unavailable; use a journal or transaction reader with committed-result lookup", evidence.JournalID)
			}
			if err := validated.admitOnce(occurrenceEnvelopeKeyOf(evidence.EvidenceKind, contract, payload.Envelope), func() error {
				return receipt.ValidateStoredOccurrenceEnvelope(owner, evidence.EvidenceKind, contract, payload.Envelope, page.SnapshotMaxJournalID)
			}); err != nil {
				return OccurrenceFactPage{}, fmt.Errorf("read lifecycle occurrence %d: owner envelope validation failed: %w; restore verified journal evidence", evidence.JournalID, err)
			}
		}
		result.Facts = append(result.Facts, OccurrenceFact{JournalID: evidence.JournalID, Contract: contract, Event: payload.Event, ReceivedAt: evidence.RecordedAt.UnixNano(), Bindings: append([]model.NativeBinding(nil), payload.Bindings...), Capture: payload.Capture, PayloadDigest: payload.Body, Execution: payload.Execution, Version: evidence.EvidenceKind, SessionTransition: storedTransition(payload.SessionTransition)})
	}
	return result, nil
}
