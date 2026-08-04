package activation

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// openCodeTargetEventDeclarations is the static typed target declaration for
// OpenCode 1.18.10. Generated catalog membership alone carries no proof.
var openCodeTargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventOpenCodeSessionCreated, captureProof: CaptureProofOpenCodeSessionCreated, productionProof: ProductionProofOpenCodeSessionCreated},
	{event: registration.EventOpenCodeToolExecuteBefore, captureProof: CaptureProofOpenCodeToolExecuteBefore, productionProof: ProductionProofOpenCodeToolExecuteBefore},
}

// OpenCode1_18_10TargetEvents returns a defensive copy of the proved target set.
func OpenCode1_18_10TargetEvents() []model.ContractEventKind {
	out := make([]model.ContractEventKind, len(openCodeTargetEventDeclarations))
	for index, declaration := range openCodeTargetEventDeclarations {
		out[index] = declaration.event
	}
	return out
}

func openCodeTargetDeclaration(event model.ContractEventKind) (targetEventDeclaration, bool) {
	for _, declaration := range openCodeTargetEventDeclarations {
		if declaration.event == event {
			return declaration, true
		}
	}
	return targetEventDeclaration{}, false
}

// OpenCode1_18_10 derives a fresh exhaustive activation manifest from the
// generated host manifest and the proved static target declaration.
func OpenCode1_18_10() ([]Entry, error) {
	events := registration.OpenCode1_18_10().Entries()
	out := make([]Entry, 0, len(events))
	for _, event := range events {
		declaration, target := openCodeTargetDeclaration(event.Kind)
		if !target {
			entry, err := NewWithheld(event.Kind, WithheldOutsideTargetSet)
			if err != nil {
				return nil, fmt.Errorf("activation.OpenCode1_18_10: withhold generated event %q: %w", event.NativeName, err)
			}
			out = append(out, entry)
			continue
		}
		entry, err := NewEnabled(event.Kind, declaration.captureProof, declaration.productionProof)
		if err != nil {
			return nil, fmt.Errorf("activation.OpenCode1_18_10: enable proved event %q: %w", event.NativeName, err)
		}
		out = append(out, entry)
	}
	return out, nil
}
