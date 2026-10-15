package definitions

import (
	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// ActiveDocuments is the complete immutable owner document set one runtime
// contract activates against: the contract's own runtime document plus the
// shared schema, epoch implementation, retention, and interpreter documents.
// Every document is owner-built from its generated authority; none is an
// unavailable-body placeholder.
type ActiveDocuments struct {
	Runtime             Document
	Schema              Document
	EpochImplementation Document
	Retention           Document
	Interpreter         Document
}

// ActiveForContract returns the complete owner document set for one runtime
// contract. The runtime document is selected by contract; the remaining four
// are the shared compiled documents. All five are independently activatable
// through the same gated writer the runtime document uses.
func ActiveForContract(contract ir.RuntimeContractID) (ActiveDocuments, error) {
	runtime, err := NewRuntimeContract(contract)
	if err != nil {
		return ActiveDocuments{}, err
	}
	schema, err := NewLifecycleSchema()
	if err != nil {
		return ActiveDocuments{}, err
	}
	implementation, err := NewEpochImplementation()
	if err != nil {
		return ActiveDocuments{}, err
	}
	retention, err := NewRetentionPolicy()
	if err != nil {
		return ActiveDocuments{}, err
	}
	interpreter, err := NewInterpreter()
	if err != nil {
		return ActiveDocuments{}, err
	}
	documents := ActiveDocuments{Runtime: runtime, Schema: schema, EpochImplementation: implementation, Retention: retention, Interpreter: interpreter}
	if err := documents.Validate(); err != nil {
		return ActiveDocuments{}, err
	}
	return documents, nil
}

// Validate proves every document in the set is owner-built and distinct. It
// reports which slot fails so a partial set can never be mistaken for the
// complete contract documents.
func (d ActiveDocuments) Validate() error {
	slots := []struct {
		name     string
		document Document
	}{
		{"runtime", d.Runtime}, {"schema", d.Schema}, {"epoch-implementation", d.EpochImplementation},
		{"retention", d.Retention}, {"interpreter", d.Interpreter},
	}
	seen := make(map[string]bool, len(slots))
	for _, slot := range slots {
		if !slot.document.IsValid() {
			return failure("ActiveDocuments.Validate", "The "+slot.name+" document is not owner-built.", nil)
		}
		key := string(slot.document.Kind()) + "/" + string(slot.document.ID())
		if seen[key] {
			return failure("ActiveDocuments.Validate", "The contract document set names a duplicated owner.", nil)
		}
		seen[key] = true
	}
	return nil
}
