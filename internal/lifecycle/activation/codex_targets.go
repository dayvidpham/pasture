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
	{ordinal: 102, arm: "CodexUserPromptSubmit", event: registration.EventCodexUserPromptSubmit, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/user_prompt_submit_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 103, arm: "CodexPermissionRequest", event: registration.EventCodexPermissionRequest, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/permission_request_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 104, arm: "CodexPostToolUse", event: registration.EventCodexPostToolUse, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/post_tool_use_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 105, arm: "CodexPreCompact", event: registration.EventCodexPreCompact, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/pre_compact_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 106, arm: "CodexPostCompact", event: registration.EventCodexPostCompact, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/post_compact_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 107, arm: "CodexSubagentStart", event: registration.EventCodexSubagentStart, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/subagent_start_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 108, arm: "CodexSubagentStop", event: registration.EventCodexSubagentStop, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/subagent_stop_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 109, arm: "CodexStop", event: registration.EventCodexStop, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/stop_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 110, arm: "CodexSessionEnd", event: registration.EventCodexSessionEnd, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/session_end_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
	{ordinal: 111, arm: "CodexInterrupt", event: registration.EventCodexInterrupt, fixture: "internal/lifecycle/ingress/codex/testdata/fixtures/interrupt_0_153_0.json (Codex 0.153.0 authentic command-hook capture)"},
}

// codexProductionProofs declares every Codex production proof. The arm becomes
// the constant ProductionProof<arm>.
var codexProductionProofs = [...]productionProofDeclaration{
	{ordinal: 100, arm: "CodexSessionStart", event: registration.EventCodexSessionStart, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/SessionStart"},
	{ordinal: 101, arm: "CodexPreToolUse", event: registration.EventCodexPreToolUse, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/PreToolUse"},
	{ordinal: 102, arm: "CodexUserPromptSubmit", event: registration.EventCodexUserPromptSubmit, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/UserPromptSubmit"},
	{ordinal: 103, arm: "CodexPermissionRequest", event: registration.EventCodexPermissionRequest, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/PermissionRequest"},
	{ordinal: 104, arm: "CodexPostToolUse", event: registration.EventCodexPostToolUse, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/PostToolUse"},
	{ordinal: 105, arm: "CodexPreCompact", event: registration.EventCodexPreCompact, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/PreCompact"},
	{ordinal: 106, arm: "CodexPostCompact", event: registration.EventCodexPostCompact, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/PostCompact"},
	{ordinal: 107, arm: "CodexSubagentStart", event: registration.EventCodexSubagentStart, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/SubagentStart"},
	{ordinal: 108, arm: "CodexSubagentStop", event: registration.EventCodexSubagentStop, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/SubagentStop"},
	{ordinal: 109, arm: "CodexStop", event: registration.EventCodexStop, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/Stop"},
	{ordinal: 110, arm: "CodexSessionEnd", event: registration.EventCodexSessionEnd, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/SessionEnd"},
	{ordinal: 111, arm: "CodexInterrupt", event: registration.EventCodexInterrupt, test: "internal/codegen/codex_transport_e2e_test.go:TestCodexGeneratedRunnerDrivesBuiltCLI/Interrupt"},
}

// codexTargetEventDeclarations is the static typed target declaration for
// Codex at the recorded version. Each selected event binds its own cleared
// capture and generated-runner proof. Catalog membership alone is not proof:
// a future event remains withheld until it receives both bindings here.
var codexTargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventCodexSessionStart, captureProof: CaptureProofCodexSessionStart, productionProof: ProductionProofCodexSessionStart},
	{event: registration.EventCodexPreToolUse, captureProof: CaptureProofCodexPreToolUse, productionProof: ProductionProofCodexPreToolUse},
	{event: registration.EventCodexUserPromptSubmit, captureProof: CaptureProofCodexUserPromptSubmit, productionProof: ProductionProofCodexUserPromptSubmit},
	{event: registration.EventCodexPermissionRequest, captureProof: CaptureProofCodexPermissionRequest, productionProof: ProductionProofCodexPermissionRequest},
	{event: registration.EventCodexPostToolUse, captureProof: CaptureProofCodexPostToolUse, productionProof: ProductionProofCodexPostToolUse},
	{event: registration.EventCodexPreCompact, captureProof: CaptureProofCodexPreCompact, productionProof: ProductionProofCodexPreCompact},
	{event: registration.EventCodexPostCompact, captureProof: CaptureProofCodexPostCompact, productionProof: ProductionProofCodexPostCompact},
	{event: registration.EventCodexSubagentStart, captureProof: CaptureProofCodexSubagentStart, productionProof: ProductionProofCodexSubagentStart},
	{event: registration.EventCodexSubagentStop, captureProof: CaptureProofCodexSubagentStop, productionProof: ProductionProofCodexSubagentStop},
	{event: registration.EventCodexStop, captureProof: CaptureProofCodexStop, productionProof: ProductionProofCodexStop},
	{event: registration.EventCodexSessionEnd, captureProof: CaptureProofCodexSessionEnd, productionProof: ProductionProofCodexSessionEnd},
	{event: registration.EventCodexInterrupt, captureProof: CaptureProofCodexInterrupt, productionProof: ProductionProofCodexInterrupt},
}

// Codex0_153_0TargetEvents returns a defensive copy of the proved target set.
func Codex0_153_0TargetEvents() []model.ContractEventKind {
	return targetEvents(codexTargetEventDeclarations[:])
}

// Codex0_153_0 derives a fresh exhaustive activation manifest from the generated
// Codex host manifest and the proved static target declaration. An event without
// a target declaration is withheld outside-target-set. The manifest performs no filesystem access and
// derives only from the generated registration manifest, so Codex evidence can
// never enable an OpenCode or Claude entry.
func Codex0_153_0() ([]Entry, error) {
	return deriveManifest("activation.Codex0_153_0", registration.Codex0_153_0().Entries(), codexTargetEventDeclarations[:])
}
