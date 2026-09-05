package activation

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// This file is the Codex activation target declaration. It is the one file a
// Codex coverage change edits: the proof arms below are generated into
// proofs_codex.gen.go by `make generate`, and the target table binds each
// generated event to its proofs or to its withholding reason.
//
// Ordinals 100-199 belong to Codex. The generator refuses an ordinal outside
// that range and an ordinal another harness file already uses.

// codexCaptureProofs declares every Codex capture proof. The arm becomes the
// constant CaptureProof<arm>.
var codexCaptureProofs = [...]captureProofDeclaration{
	{ordinal: 100, arm: "CodexSessionStart", event: registration.EventCodexSessionStart, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/session_start_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 101, arm: "CodexPreToolUse", event: registration.EventCodexPreToolUse, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/pre_tool_use_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
}

// codexProductionProofs declares every Codex production proof. The arm becomes
// the constant ProductionProof<arm>.
var codexProductionProofs = [...]productionProofDeclaration{
	{ordinal: 100, arm: "CodexSessionStart", event: registration.EventCodexSessionStart, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledCodexHandlersToDurableReadBack/SessionStart"},
	{ordinal: 101, arm: "CodexPreToolUse", event: registration.EventCodexPreToolUse, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledCodexHandlersToDurableReadBack/PreToolUse"},
}

// codexTargetEventDeclarations is the static typed target declaration for
// Codex at the recorded version. Exactly the two authentically-proven events, SessionStart and
// PreToolUse, bind their event-bound capture and production proofs; generated
// catalog membership alone carries no proof, so every other Codex event
// derives to Withheld (outside-target-set).
var codexTargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventCodexSessionStart, captureProof: CaptureProofCodexSessionStart, productionProof: ProductionProofCodexSessionStart},
	{event: registration.EventCodexPreToolUse, captureProof: CaptureProofCodexPreToolUse, productionProof: ProductionProofCodexPreToolUse},
}

// Codex0_153_0TargetEvents returns a defensive copy of the proved target set.
func Codex0_153_0TargetEvents() []model.ContractEventKind {
	return targetEvents(codexTargetEventDeclarations[:])
}

// Codex0_153_0 derives a fresh exhaustive activation manifest from the generated
// Codex host manifest and the proved static target declaration. Exactly the two
// proven events are Enabled; every other generated Codex event is Withheld with
// the outside-target-set reason. The manifest performs no filesystem access and
// derives only from the generated registration manifest, so Codex evidence can
// never enable an OpenCode or Claude entry.
func Codex0_153_0() ([]Entry, error) {
	return deriveManifest("activation.Codex0_153_0", registration.Codex0_153_0().Entries(), codexTargetEventDeclarations[:])
}
