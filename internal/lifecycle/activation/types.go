package activation

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// State is the closed progressive-activation state of one native event.
type State uint8

const (
	Enabled State = iota + 1
	Withheld
)

// WithheldReason explains why an event is not registered with its host.
type WithheldReason uint8

const (
	WithheldMissingFixture WithheldReason = iota + 1
	WithheldUnverifiedBuild
	WithheldProductionProofMissing
)

// Entry is one complete activation decision. Withheld entries always carry a
// reason; enabled entries never do.
type Entry struct {
	Event  model.ContractEventKind
	State  State
	Reason WithheldReason
}

func NewEnabled(event model.ContractEventKind, authenticFixture, productionProof bool) (Entry, error) {
	if event == 0 {
		return Entry{}, fmt.Errorf("activation.NewEnabled: event kind is zero; a generated event ordinal is required before registration; select an event from the generated manifest")
	}
	if !authenticFixture {
		return Entry{}, fmt.Errorf("activation.NewEnabled: event %d has no authentic exact-version capture; registering it would claim support from descriptor-derived evidence; capture a raw payload from the pinned host and record its verified digest", event)
	}
	if !productionProof {
		return Entry{}, fmt.Errorf("activation.NewEnabled: event %d has no passing production-path proof; registration would expose an unverified event; run the generated-hook to public-reader proof first", event)
	}
	return Entry{Event: event, State: Enabled}, nil
}

func NewWithheld(event model.ContractEventKind, reason WithheldReason) (Entry, error) {
	if event == 0 || reason < WithheldMissingFixture || reason > WithheldProductionProofMissing {
		return Entry{}, fmt.Errorf("activation.NewWithheld: event %d or reason %d is invalid; support reporting requires a generated event ordinal and a typed withholding reason; use the declared activation constants", event, reason)
	}
	return Entry{Event: event, State: Withheld, Reason: reason}, nil
}
