package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
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
// EvidencePageReader may be pooled or bound to the caller's SQL transaction.
// The latter makes this same function the current post-scan publication fence.
// Use a context-bound read-only transaction reader when pool acquisition and
// each underlying query must obey ctx; the compatibility pooled API has no
// context parameter of its own.
func ReadOccurrenceFactPage(ctx context.Context, reader EvidencePageReader, request provenance.FactPageRequest) (OccurrenceFactPage, error) {
	if ctx == nil {
		return OccurrenceFactPage{}, fmt.Errorf("read lifecycle occurrence page: context is absent; supply the caller's live replay context before reading evidence")
	}
	if err := ctx.Err(); err != nil {
		return OccurrenceFactPage{}, err
	}
	if reader == nil || request.Limit < 1 || request.Limit > provenance.MaxFactPageSize {
		return OccurrenceFactPage{}, fmt.Errorf("read lifecycle occurrence page: reader or page limit is invalid; supply a live reader and a limit between 1 and %d", provenance.MaxFactPageSize)
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
	result := OccurrenceFactPage{SnapshotMaxJournalID: page.SnapshotMaxJournalID, Next: page.Next, Facts: make([]OccurrenceFact, 0, len(page.Rows))}
	for _, evidence := range page.Rows {
		if err := ctx.Err(); err != nil {
			return OccurrenceFactPage{}, err
		}
		var payload occurrencePayload
		if err := json.Unmarshal(evidence.Payload, &payload); err != nil {
			return OccurrenceFactPage{}, fmt.Errorf("decode lifecycle occurrence %d during bounded session replay: %w; restore the damaged journal row before retrying", evidence.JournalID, err)
		}
		contract, err := parseOccurrenceContract(payload.Contract)
		if err != nil {
			return OccurrenceFactPage{}, err
		}
		result.Facts = append(result.Facts, OccurrenceFact{JournalID: evidence.JournalID, Contract: contract, Event: payload.Event, ReceivedAt: evidence.RecordedAt.UnixNano(), Bindings: append([]model.NativeBinding(nil), payload.Bindings...), Capture: payload.Capture, PayloadDigest: payload.Body})
	}
	return result, nil
}
