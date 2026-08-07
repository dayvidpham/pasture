package projection

import (
	"bytes"
	"crypto/sha256"
	"sort"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/provenance"
)

const lineageLinkKind = provenance.EvidenceKind("pasture.lifecycle.link.v1")

// LinkReader reads committed occurrence lineage links from journal truth. Like
// the occurrence Reader it is a disposable projection over the journal: it holds
// no state, reads under one pinned snapshot, and validates every row's digest
// before decoding (the F18 reader discipline). It is strictly read-side and
// never writes — materialization is the lineage command's gated write.
type LinkReader struct {
	Facts provenance.FactQueryAPI
}

// Links returns every committed link record under a single consistent snapshot,
// sorted by committed journal identity. Each row is digest-validated against its
// canonical payload before decoding, so a tampered or noncanonical link row is
// refused rather than returned. The read is bounded page-by-page
// (provenance.MaxFactPageSize) and the pinned snapshot is re-asserted on every
// page so a concurrent write cannot yield a torn view.
func (r LinkReader) Links() ([]model.LinkRecord, error) {
	if r.Facts == nil {
		return nil, readerError("The lifecycle link reader is incompletely wired.",
			"A provenance fact query dependency is required to read committed links.",
			"No links were read.",
			"Construct the reader with the unified store's provenance fact query API (tracker.Journal().Facts()).", nil)
	}

	snapshot, err := r.snapshot()
	if err != nil {
		return nil, err
	}
	if snapshot == 0 {
		return nil, nil
	}

	var records []model.LinkRecord
	query := provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{lineageLinkKind},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize, SnapshotMaxJournalID: snapshot},
	}
	for {
		page, err := r.Facts.QueryEvidence(query)
		if err != nil {
			return nil, readerError("Committed lifecycle links could not be read.",
				"The pinned provenance link query failed.",
				"No partial page is returned.",
				"Repair journal reads and retry.", err)
		}
		if page.SnapshotMaxJournalID != snapshot {
			return nil, snapshotDriftError()
		}
		for _, row := range page.Rows {
			sum := sha256.Sum256(row.Payload)
			if !bytes.Equal(sum[:], row.ContentDigest) {
				return nil, readerError("A committed lifecycle link failed digest validation.",
					"The stored digest differs from the exact link payload.",
					"No partial page is returned.",
					"Restore canonical link evidence.", nil)
			}
			record, err := receipt.DecodeLink(model.LifecycleLinkID(row.JournalID), row.Payload)
			if err != nil {
				return nil, readerError("A committed lifecycle link is malformed or noncanonical.",
					err.Error(),
					"No partial page is returned.",
					"Repair the link evidence producer and row.", err)
			}
			records = append(records, record)
		}
		if page.Next == nil {
			break
		}
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}

	sort.SliceStable(records, func(i, j int) bool {
		return records[i].LinkID.JournalID() < records[j].LinkID.JournalID()
	})
	return records, nil
}

// snapshot establishes the global link-read watermark: a single bounded query
// whose returned SnapshotMaxJournalID pins the consistent view every subsequent
// page is asserted against.
func (r LinkReader) snapshot() (provenance.JournalID, error) {
	watermark, err := r.Facts.QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}},
		Kinds:  []provenance.EvidenceKind{lineageLinkKind},
		Page:   provenance.FactPageRequest{Limit: 1},
	})
	if err != nil {
		return 0, readerError("The lifecycle link reader could not establish a global snapshot.",
			"Provenance rejected the link watermark query.",
			"No links were read.",
			"Repair journal reads.", err)
	}
	return watermark.SnapshotMaxJournalID, nil
}
