package projection

import (
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// occurrenceEnvelopeKey identifies one admission decision for stored occurrence
// evidence: the four definition references the envelope names plus the delivery
// contract.
//
// THE KEY IS COMPLETE, and that is why one admission per key is exact rather
// than an approximation. ValidateOccurrenceEnvelope tests exactly these four
// references (complete, correctly typed, nonzero content identity), the
// contract equality is part of the key, and every other envelope member
// (HostVersion, HostVersionSource, Origin) is documented provenance that
// admission never reads. The page also pins one watermark, so the definition
// resolution behind the key cannot change within the set's lifetime. A new
// definition version is a different reference and therefore a different key.
type occurrenceEnvelopeKey struct {
	runtime        model.DefinitionRef
	schema         model.DefinitionRef
	implementation model.DefinitionRef
	retention      model.DefinitionRef
	contract       ir.RuntimeContractID
}

// occurrenceEnvelopeKeyOf derives the key an envelope and its delivery contract
// stand for.
func occurrenceEnvelopeKeyOf(contract ir.RuntimeContractID, envelope model.OccurrenceEnvelopeRef) occurrenceEnvelopeKey {
	return occurrenceEnvelopeKey{
		runtime:        envelope.Runtime.Definition,
		schema:         envelope.Schema.Definition,
		implementation: envelope.Implementation.Definition,
		retention:      envelope.Retention.Definition,
		contract:       contract,
	}
}

// occurrenceValidationSet admits each distinct envelope decision once.
//
// ITS LIFETIME IS ONE PAGE. A page carries thousands of rows that name the same
// four definitions, and resolving them per row re-reads the same evidence rows,
// committed results and state records again and again: measured at 39.7ms per
// row on a 1.9M-row store, against 1.0ms when each distinct key is resolved
// once. The set is not a cache in the eviction sense: it is created by
// ReadOccurrenceFactPage, dies with the page, and cannot serve a decision across
// pages, scans, or watermarks.
type occurrenceValidationSet struct {
	seen map[occurrenceEnvelopeKey]struct{}
}

func newOccurrenceValidationSet() *occurrenceValidationSet {
	return &occurrenceValidationSet{seen: make(map[occurrenceEnvelopeKey]struct{})}
}

// first reports whether this is the first time key has been offered to the set.
func (s *occurrenceValidationSet) first(key occurrenceEnvelopeKey) bool {
	if _, seen := s.seen[key]; seen {
		return false
	}
	s.seen[key] = struct{}{}
	return true
}
