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
	constructed      bool
}

func NewInterpretedRecord(id InterpretationID, occurrence OccurrenceID, semantic runtime.EventSemantic, identities []waist.SemanticIdentity, unresolved []waist.UnresolvedFact, contract ir.RuntimeContractID) (InterpretedRecord, error) {
	if id.JournalID() == 0 || occurrence.JournalID() == 0 || !semantic.IsValid() || !contract.IsValid() {
		return InterpretedRecord{}, fmt.Errorf("construct lifecycle interpreted record: invalid journal identity, semantic, or runtime contract")
	}
	return InterpretedRecord{InterpretationID: id, OccurrenceID: occurrence, semantic: semantic, identities: append([]waist.SemanticIdentity(nil), identities...), unresolved: append([]waist.UnresolvedFact(nil), unresolved...), contract: contract, constructed: true}, nil
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
