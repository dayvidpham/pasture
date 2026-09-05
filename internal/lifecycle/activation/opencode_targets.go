package activation

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// This file is the OpenCode activation target declaration. It is the one file
// an OpenCode coverage change edits: the proof arms below are generated into
// proofs_opencode.gen.go by `make generate`, and the target table binds each
// generated event to its proofs or to its withholding reason.
//
// Ordinals 200-299 belong to OpenCode. The generator refuses an ordinal
// outside that range and an ordinal another harness file already uses.

// openCodeCaptureProofs declares every OpenCode capture proof. The arm becomes
// the constant CaptureProof<arm>.
var openCodeCaptureProofs = [...]captureProofDeclaration{
	{ordinal: 200, arm: "OpenCodeSessionCreated", event: registration.EventOpenCodeSessionCreated, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/session_created_1_18_10.capture.json (OpenCode 1.18.10 authentic callback-object capture)"},
	{ordinal: 201, arm: "OpenCodeToolExecuteBefore", event: registration.EventOpenCodeToolExecuteBefore, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/tool_execute_before_1_18_10.capture.json (OpenCode 1.18.10 authentic callback-object capture)"},
}

// openCodeProductionProofs declares every OpenCode production proof. The arm
// becomes the constant ProductionProof<arm>.
var openCodeProductionProofs = [...]productionProofDeclaration{
	{ordinal: 200, arm: "OpenCodeSessionCreated", event: registration.EventOpenCodeSessionCreated, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCodeHandlersToDurableReadBack/session.created"},
	{ordinal: 201, arm: "OpenCodeToolExecuteBefore", event: registration.EventOpenCodeToolExecuteBefore, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCodeHandlersToDurableReadBack/tool.execute.before"},
}

// openCodeTargetEventDeclarations is the static typed target declaration for
// OpenCode 1.18.10. Generated catalog membership alone carries no proof.
var openCodeTargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventOpenCodeSessionCreated, captureProof: CaptureProofOpenCodeSessionCreated, productionProof: ProductionProofOpenCodeSessionCreated},
	{event: registration.EventOpenCodeToolExecuteBefore, captureProof: CaptureProofOpenCodeToolExecuteBefore, productionProof: ProductionProofOpenCodeToolExecuteBefore},
}

// OpenCode1_18_10TargetEvents returns a defensive copy of the proved target set.
func OpenCode1_18_10TargetEvents() []model.ContractEventKind {
	return targetEvents(openCodeTargetEventDeclarations[:])
}

// OpenCode1_18_10 derives a fresh exhaustive activation manifest from the
// generated host manifest and the proved static target declaration.
func OpenCode1_18_10() ([]Entry, error) {
	return deriveManifest("activation.OpenCode1_18_10", registration.OpenCode1_18_10().Entries(), openCodeTargetEventDeclarations[:])
}
