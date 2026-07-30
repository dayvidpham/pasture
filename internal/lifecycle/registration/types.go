package registration

import (
	"slices"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

type BlockingMode uint8
type MutationMode uint8
type FailureMode uint8
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
	FailureReportAndContinue FailureMode = iota + 1
	FailureExitTwoBlocks
)
const (
	StopLoopNotApplicable StopLoopPolicy = iota + 1
	StopLoopConsultWhenInactive
)

type Identity struct {
	Field    model.NativeFieldID
	Binding  model.NativeBindingKind
	Required bool
}

// Event is generated target-neutral registration data. Host-specific source
// code is inaccessible to this package by Go's internal-package rule.
type Event struct {
	Kind          model.ContractEventKind
	NativeName    string
	AllowedFields []model.NativeFieldID
	Identities    []Identity
	Blocking      BlockingMode
	Mutation      MutationMode
	Failure       FailureMode
	StopLoop      StopLoopPolicy
}

func (e Event) Fields() []model.NativeFieldID { return slices.Clone(e.AllowedFields) }
func (e Event) IdentityFields() []Identity    { return slices.Clone(e.Identities) }

type Manifest struct {
	Harness  ir.HarnessID
	Version  string
	Contract ir.RuntimeContractID
	Events   []Event
}

func (m Manifest) Entries() []Event {
	out := slices.Clone(m.Events)
	for i := range out {
		out[i].AllowedFields = slices.Clone(out[i].AllowedFields)
		out[i].Identities = slices.Clone(out[i].Identities)
	}
	return out
}
