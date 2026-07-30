package hostcontract

import "github.com/dayvidpham/pasture/internal/lifecycle/model"

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

type Field struct {
	ID     model.NativeFieldID
	Symbol string
	Name   string
}

type Identity struct {
	Field    model.NativeFieldID
	Binding  model.NativeBindingKind
	Required bool
}

type Event struct {
	Kind       model.ContractEventKind
	Symbol     string
	Name       string
	Fields     []model.NativeFieldID
	Identities []Identity
	Blocking   BlockingMode
	Mutation   MutationMode
	Failure    FailureMode
	StopLoop   StopLoopPolicy
}

type Contract struct {
	Version string
	Fields  []Field
	Events  []Event
}
