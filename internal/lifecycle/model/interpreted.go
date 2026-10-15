package model

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

type InterpretedRecord struct {
	InterpretationID InterpretationID
	OccurrenceID     OccurrenceID
	semantic         runtime.EventSemantic
	identities       []waist.SemanticIdentity
	unresolved       []waist.UnresolvedFact
	contract         ir.RuntimeContractID
	metamodel        LifecycleMetamodelManifest
	hasMetamodel     bool
	envelope         SemanticEnvelopeRef
	hasEnvelope      bool
	constructed      bool
}

func NewInterpretedRecord(id InterpretationID, occurrence OccurrenceID, semantic runtime.EventSemantic, identities []waist.SemanticIdentity, unresolved []waist.UnresolvedFact, contract ir.RuntimeContractID) (InterpretedRecord, error) {
	if id.JournalID() == 0 || occurrence.JournalID() == 0 || !semantic.IsValid() || !contract.IsValid() {
		return InterpretedRecord{}, fmt.Errorf("construct lifecycle interpreted record: invalid journal identity, semantic, or runtime contract")
	}
	return InterpretedRecord{InterpretationID: id, OccurrenceID: occurrence, semantic: semantic, identities: append([]waist.SemanticIdentity(nil), identities...), unresolved: append([]waist.UnresolvedFact(nil), unresolved...), contract: contract, constructed: true}, nil
}

// NewInterpretedRecordWithMetamodel constructs an interpreted.v2 record that
// carries the metamodel coordinate it was interpreted against (D2). It is the
// decode counterpart for interpreted.v2 evidence; the coordinate must be valid.
// Committed interpreted.v1 records decode through NewInterpretedRecord and have
// no coordinate — Metamodel() reports false for them so the read surface can
// disclose "metamodel unresolved (pre-M5)" rather than inventing one.
func NewInterpretedRecordWithMetamodel(id InterpretationID, occurrence OccurrenceID, semantic runtime.EventSemantic, identities []waist.SemanticIdentity, unresolved []waist.UnresolvedFact, contract ir.RuntimeContractID, manifest LifecycleMetamodelManifest) (InterpretedRecord, error) {
	if !manifest.IsValid() {
		return InterpretedRecord{}, fmt.Errorf("construct lifecycle interpreted.v2 record: invalid metamodel coordinate")
	}
	record, err := NewInterpretedRecord(id, occurrence, semantic, identities, unresolved, contract)
	if err != nil {
		return InterpretedRecord{}, err
	}
	record.metamodel = manifest
	record.hasMetamodel = true
	return record, nil
}

// NewInterpretedRecordWithEnvelope constructs an interpreted.v3 record that
// carries the complete five-reference semantic envelope it was finalized
// against. The envelope must be structurally complete: all five refs carry a
// positive journal coordinate, their owner kind, and a nonzero content
// identity. Owner resolution (body, digest, slot, state) and cross-envelope
// equality with the associated occurrence are enforced at the receipt and
// read boundaries, not here, so this constructor stays pure.
func NewInterpretedRecordWithEnvelope(id InterpretationID, occurrence OccurrenceID, semantic runtime.EventSemantic, identities []waist.SemanticIdentity, unresolved []waist.UnresolvedFact, contract ir.RuntimeContractID, manifest LifecycleMetamodelManifest, envelope SemanticEnvelopeRef) (InterpretedRecord, error) {
	if !manifest.IsValid() {
		return InterpretedRecord{}, fmt.Errorf("construct lifecycle interpreted.v3 record: invalid metamodel coordinate")
	}
	if err := ValidateSemanticEnvelope(envelope); err != nil {
		return InterpretedRecord{}, err
	}
	record, err := NewInterpretedRecord(id, occurrence, semantic, identities, unresolved, contract)
	if err != nil {
		return InterpretedRecord{}, err
	}
	record.metamodel = manifest
	record.hasMetamodel = true
	record.envelope = envelope
	record.hasEnvelope = true
	return record, nil
}

// ValidateSemanticEnvelope proves an envelope is structurally complete: five
// nonzero refs with the exact owner kind per slot. A resolving boundary must
// additionally prove every ref owner-resolvable and the three shared refs
// exactly equal to the associated occurrence envelope.
func ValidateSemanticEnvelope(envelope SemanticEnvelopeRef) error {
	slots := []struct {
		name string
		ref  DefinitionRef
		kind DefinitionKind
	}{
		{"runtime", envelope.Runtime.Definition, DefinitionRuntimeContract},
		{"schema", envelope.Schema.Definition, DefinitionLifecycleSchema},
		{"metamodel", envelope.Metamodel.Definition, DefinitionMetamodel},
		{"interpreter", envelope.Interpreter.Definition, DefinitionInterpreter},
		{"implementation", envelope.Implementation.Definition, DefinitionEpochImplementation},
	}
	for _, slot := range slots {
		if slot.ref.Definition <= 0 || slot.ref.Kind != slot.kind || slot.ref.Content == (ContentIdentity{}) {
			return fmt.Errorf("construct lifecycle interpreted.v3 record: semantic envelope slot %q is not a complete nonzero owner reference", slot.name)
		}
	}
	return nil
}

// SemanticEnvelope returns the complete five-reference envelope this
// interpretation was finalized against and whether one is present. It is false
// for decoded legacy interpreted.v1/v2 records, which predate the semantic
// envelope: no envelope is inferred for historical reads.
func (r InterpretedRecord) SemanticEnvelope() (SemanticEnvelopeRef, bool) {
	return r.envelope, r.hasEnvelope
}

// Metamodel returns the metamodel coordinate this interpretation was produced
// against and whether one is present. It is false for decoded interpreted.v1
// records, which predate the metamodel producer (M5).
func (r InterpretedRecord) Metamodel() (LifecycleMetamodelManifest, bool) {
	return r.metamodel, r.hasMetamodel
}
func (r InterpretedRecord) JournalID() provenance.JournalID { return r.InterpretationID.JournalID() }
func (r InterpretedRecord) Semantic() runtime.EventSemantic { return r.semantic }
func (r InterpretedRecord) Identities() []waist.SemanticIdentity {
	return append([]waist.SemanticIdentity(nil), r.identities...)
}
func (r InterpretedRecord) UnresolvedFacts() []waist.UnresolvedFact {
	return append([]waist.UnresolvedFact(nil), r.unresolved...)
}
func (r InterpretedRecord) Contract() ir.RuntimeContractID { return r.contract }
func (r InterpretedRecord) IsValid() bool                  { return r.constructed }

type LifecycleRecord struct {
	nonJournalValue
	Occurrence  OccurrenceRecord
	interpreted []InterpretedRecord
}

func NewLifecycleRecord(occurrence OccurrenceRecord, interpreted []InterpretedRecord) (LifecycleRecord, error) {
	if occurrence.JournalID() == 0 || len(interpreted) > 1 {
		return LifecycleRecord{}, fmt.Errorf("construct lifecycle record: occurrence must be valid and have at most one interpretation")
	}
	return LifecycleRecord{Occurrence: occurrence, interpreted: append([]InterpretedRecord(nil), interpreted...)}, nil
}
func (r LifecycleRecord) Interpreted() []InterpretedRecord {
	return append([]InterpretedRecord(nil), r.interpreted...)
}
