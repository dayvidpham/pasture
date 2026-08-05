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
	withheldReason  WithheldReason
}

var claudeTargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventSessionStart, captureProof: CaptureProofSessionStart, productionProof: ProductionProofSessionStart},
	{event: registration.EventSessionEnd, captureProof: CaptureProofSessionEnd, productionProof: ProductionProofSessionEnd},
	{event: registration.EventPreToolUse, captureProof: CaptureProofPreToolUse, productionProof: ProductionProofPreToolUse},
	{event: registration.EventPostToolUse, captureProof: CaptureProofPostToolUse, productionProof: ProductionProofPostToolUse},
	{event: registration.EventPostToolUseFailure, captureProof: CaptureProofPostToolUseFailure, productionProof: ProductionProofPostToolUseFailure},
	{event: registration.EventPostToolBatch, captureProof: CaptureProofPostToolBatch, productionProof: ProductionProofPostToolBatch},
	{event: registration.EventPreCompact, captureProof: CaptureProofPreCompact, productionProof: ProductionProofPreCompact},
	{event: registration.EventPostCompact, captureProof: CaptureProofPostCompact, productionProof: ProductionProofPostCompact},
	{event: registration.EventElicitation, withheldReason: WithheldMissingRequestCorrelation},
	{event: registration.EventElicitationResult, withheldReason: WithheldMissingRequestCorrelation},
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
			if declaration.withheldReason != 0 {
				return nil, fmt.Errorf("activation.ClaudeCode2_1_210: target event %q has both proofs and withholding reason %q; choose one static activation state", event.NativeName, declaration.withheldReason.String())
			}
			entry, err := NewEnabled(event.Kind, declaration.captureProof, declaration.productionProof)
			if err != nil {
				return nil, fmt.Errorf("activation.ClaudeCode2_1_210: enable generated event %q: %w", event.NativeName, err)
			}
			out = append(out, entry)
			continue
		}
		reason := declaration.withheldReason
		if reason == 0 {
			reason = WithheldMissingFixture
		}
		entry, err := NewWithheld(event.Kind, reason)
		if err != nil {
			return nil, fmt.Errorf("activation.ClaudeCode2_1_210: withhold target event %q: %w", event.NativeName, err)
		}
		out = append(out, entry)
	}
	return out, nil
}
