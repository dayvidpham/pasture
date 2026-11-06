package projection

import (
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// occurrenceEnvelopeKey identifies one admission decision for stored occurrence
// evidence: the evidence kind, the four typed definition references the
// envelope names, and the delivery contract.
//
// THE KEY IS COMPLETE BY CONSTRUCTION. ValidateStoredOccurrenceEnvelope
// dispatches on the kind and, for v2/v3, reads exactly the four typed
// references plus Runtime.Contract, comparing that contract to the delivery
// contract. Keying the typed references keeps Runtime.Contract in the key
// without a separate field. The remaining envelope members (HostVersion,
// HostVersionSource, Origin) are documented provenance that admission never
// reads, so two envelopes differing only there are the same decision.
//
// The page pins one watermark, so the definition resolution behind a key cannot
// change within the set's lifetime, and a new definition version is a different
// reference and therefore a different key.
type occurrenceEnvelopeKey struct {
	kind           provenance.EvidenceKind
	runtime        model.RuntimeContractDefinitionRef
	schema         model.LifecycleSchemaDefinitionRef
	implementation model.EpochImplementationRef
	retention      model.RetentionPolicyDefinitionRef
	contract       ir.RuntimeContractID
}

// occurrenceEnvelopeKeyOf derives the key an envelope and its delivery contract
// stand for.
func occurrenceEnvelopeKeyOf(kind provenance.EvidenceKind, contract ir.RuntimeContractID, envelope model.OccurrenceEnvelopeRef) occurrenceEnvelopeKey {
	return occurrenceEnvelopeKey{
		kind:           kind,
		runtime:        envelope.Runtime,
		schema:         envelope.Schema,
		implementation: envelope.Implementation,
		retention:      envelope.Retention,
		contract:       contract,
	}
}

// occurrenceValidationSet admits each distinct envelope decision once.
//
// ITS LIFETIME IS ONE PAGE, which is at most provenance.MaxFactPageSize rows. A
// page repeats the same definitions on every row, and resolving them per row
// re-reads the same evidence rows, committed results and state records again
// and again. The set is not a cache in the eviction sense: ReadOccurrenceFactPage
// creates it, it dies with the page, and it cannot serve a decision across
// pages, scans, or watermarks.
type occurrenceValidationSet struct {
	seen map[occurrenceEnvelopeKey]struct{}
}

func newOccurrenceValidationSet() *occurrenceValidationSet {
	return &occurrenceValidationSet{}
}

// admitOnce runs validate the first time key is offered and skips it afterwards.
// The key is recorded before validate runs, which is safe only because the caller
// aborts on error: a failed decision must never be reused by a later row.
func (s *occurrenceValidationSet) admitOnce(key occurrenceEnvelopeKey, validate func() error) error {
	if _, seen := s.seen[key]; seen {
		return nil
	}
	if s.seen == nil {
		s.seen = make(map[occurrenceEnvelopeKey]struct{})
	}
	s.seen[key] = struct{}{}
	return validate()
}
