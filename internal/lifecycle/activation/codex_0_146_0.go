package activation

import (
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// codexTargetEventDeclarations is the static typed target declaration for Codex
// 0.146.0. Exactly the two authentically-proven events — SessionStart and
// PreToolUse — bind their event-bound capture and production proofs; generated
// catalog membership alone carries no proof, so every other Codex event derives
// to Withheld (outside-target-set).
//
// This catalog is the committed Codex dispatch activation adopted at M3
// Implementation UAT (ratified proposal step 6, "activation last"). It is now
// the wired production default for the Codex harness, mirroring the Claude and
// OpenCode catalog docs; the pre-activation production proof exercised the same
// durable path by injecting it through handlers.HookLifecycleInput.Activations.
var codexTargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventCodexSessionStart, captureProof: CaptureProofCodexSessionStart, productionProof: ProductionProofCodexSessionStart},
	{event: registration.EventCodexPreToolUse, captureProof: CaptureProofCodexPreToolUse, productionProof: ProductionProofCodexPreToolUse},
}

// Codex0_146_0TargetEvents returns a defensive copy of the proved target set.
func Codex0_146_0TargetEvents() []model.ContractEventKind {
	out := make([]model.ContractEventKind, len(codexTargetEventDeclarations))
	for index, declaration := range codexTargetEventDeclarations {
		out[index] = declaration.event
	}
	return out
}

func codexTargetDeclaration(event model.ContractEventKind) (targetEventDeclaration, bool) {
	for _, declaration := range codexTargetEventDeclarations {
		if declaration.event == event {
			return declaration, true
		}
	}
	return targetEventDeclaration{}, false
}

// Codex0_146_0 derives a fresh exhaustive activation manifest from the generated
// Codex host manifest and the proved static target declaration. Exactly the two
// proven events are Enabled; every other generated Codex event is Withheld with
// the outside-target-set reason. The manifest performs no filesystem access and
// derives only from the generated registration manifest, so Codex evidence can
// never enable an OpenCode or Claude entry.
func Codex0_146_0() ([]Entry, error) {
	events := registration.Codex0_146_0().Entries()
	out := make([]Entry, 0, len(events))
	for _, event := range events {
		declaration, target := codexTargetDeclaration(event.Kind)
		if !target {
			entry, err := NewWithheld(event.Kind, WithheldOutsideTargetSet)
			if err != nil {
				return nil, fmt.Errorf("activation.Codex0_146_0: withhold generated event %q: %w", event.NativeName, err)
			}
			out = append(out, entry)
			continue
		}
		entry, err := NewEnabled(event.Kind, declaration.captureProof, declaration.productionProof)
		if err != nil {
			return nil, fmt.Errorf("activation.Codex0_146_0: enable proved event %q: %w", event.NativeName, err)
		}
		out = append(out, entry)
	}
	return out, nil
}
