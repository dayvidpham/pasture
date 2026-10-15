package registration

import (
	"slices"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

type BlockingMode uint8
type MutationMode uint8
type StopLoopPolicy uint8

const (
	NonBlocking BlockingMode = iota + 1
	Blocking
	ConditionallyBlocking
)
const (
	MutationNone MutationMode = iota + 1
	MutationInput
)
const (
	StopLoopNotApplicable StopLoopPolicy = iota + 1
	StopLoopConsultWhenInactive
)

type Identity struct {
	Field    model.NativeFieldID     `json:"Field"`
	Binding  model.NativeBindingKind `json:"Binding"`
	Required bool                    `json:"Required"`
}

// Event is generated target-neutral registration data. Host-specific source
// code is inaccessible to this package by Go's internal-package rule.
type Event struct {
	Kind          model.ContractEventKind    `json:"Kind"`
	NativeName    string                     `json:"NativeName"`
	AllowedFields []model.NativeFieldID      `json:"AllowedFields"`
	Identities    []Identity                 `json:"Identities"`
	Blocking      BlockingMode               `json:"Blocking"`
	Mutation      MutationMode               `json:"Mutation"`
	Failure       pastureruntime.FailureMode `json:"Failure"`
	StopLoop      StopLoopPolicy             `json:"StopLoop"`
}

func (e Event) Fields() []model.NativeFieldID { return slices.Clone(e.AllowedFields) }
func (e Event) IdentityFields() []Identity    { return slices.Clone(e.Identities) }

type Manifest struct {
	Harness  ir.HarnessID         `json:"Harness"`
	Version  string               `json:"Version"`
	Contract ir.RuntimeContractID `json:"Contract"`
	Events   []Event              `json:"Events"`
}

func (m Manifest) Entries() []Event {
	out := slices.Clone(m.Events)
	for i := range out {
		out[i].AllowedFields = slices.Clone(out[i].AllowedFields)
		out[i].Identities = slices.Clone(out[i].Identities)
	}
	return out
}
