package activation

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// targetEventDeclaration is the one static target declaration table. It uses
// generated event ordinals rather than native-name strings or a second map.
type targetEventDeclaration struct {
	event           model.ContractEventKind
	captureProof    CaptureProof
	productionProof ProductionProof
}

var claudeTargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventSessionStart, captureProof: CaptureProofSessionStart, productionProof: ProductionProofSessionStart},
	{event: registration.EventSessionEnd},
	{event: registration.EventPreToolUse},
	{event: registration.EventPostToolUse},
	{event: registration.EventPostToolUseFailure},
	{event: registration.EventPostToolBatch},
	{event: registration.EventPreCompact},
	{event: registration.EventPostCompact},
	{event: registration.EventElicitation},
	{event: registration.EventElicitationResult},
}

// ClaudeCode2_1_210TargetEvents returns the typed target subset in declaration
// order. The returned slice is independent of the static declaration table.
func ClaudeCode2_1_210TargetEvents() []model.ContractEventKind {
	out := make([]model.ContractEventKind, len(claudeTargetEventDeclarations))
	for index, declaration := range claudeTargetEventDeclarations {
		out[index] = declaration.event
	}
	return out
}

func claudeTargetDeclaration(event model.ContractEventKind) (targetEventDeclaration, bool) {
	for _, declaration := range claudeTargetEventDeclarations {
		if declaration.event == event {
			return declaration, true
		}
	}
	return targetEventDeclaration{}, false
}

// ClaudeCode2_1_210 returns a fresh exhaustive activation manifest. The
// manifest is derived only from the generated registration manifest and the
// static typed target declaration table; it performs no filesystem access.
func ClaudeCode2_1_210() ([]Entry, error) {
	events := registration.ClaudeCode2_1_210().Entries()
	out := make([]Entry, 0, len(events))
	for _, event := range events {
		declaration, target := claudeTargetDeclaration(event.Kind)
		if !target {
			entry, err := NewWithheld(event.Kind, WithheldOutsideTargetSet)
			if err != nil {
				return nil, fmt.Errorf("activation.ClaudeCode2_1_210: withhold generated event %q: %w", event.NativeName, err)
			}
			out = append(out, entry)
			continue
		}
		if declaration.captureProof != 0 || declaration.productionProof != 0 {
			entry, err := NewEnabled(event.Kind, declaration.captureProof, declaration.productionProof)
			if err != nil {
				return nil, fmt.Errorf("activation.ClaudeCode2_1_210: enable generated event %q: %w", event.NativeName, err)
			}
			out = append(out, entry)
			continue
		}
		entry, err := NewWithheld(event.Kind, WithheldMissingFixture)
		if err != nil {
			return nil, fmt.Errorf("activation.ClaudeCode2_1_210: withhold target event %q: %w", event.NativeName, err)
		}
		out = append(out, entry)
	}
	return out, nil
}
