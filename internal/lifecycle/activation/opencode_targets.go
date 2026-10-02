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
	{ordinal: 200, arm: "OpenCodeSessionCreated", event: registration.EventOpenCodeSessionCreated, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/session_created_1_18_29.json (OpenCode 1.18.29 authentic callback-object capture)"},
	{ordinal: 201, arm: "OpenCodeToolExecuteBefore", event: registration.EventOpenCodeToolExecuteBefore, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/tool_execute_before_1_18_29.json (OpenCode 1.18.29 authentic callback-object capture)"},
	{ordinal: 202, arm: "OpenCode2SessionPrompt", event: registration.EventOpenCode2SessionPrompt, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_prompt_2_0_20.1.json (OpenCode 2.0.20 authentic hook capture)"},
	{ordinal: 203, arm: "OpenCode2SessionContext", event: registration.EventOpenCode2SessionContext, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_context_2_0_20.1.json (OpenCode 2.0.20 authentic hook capture)"},
	{ordinal: 204, arm: "OpenCode2SessionTitle", event: registration.EventOpenCode2SessionTitle, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_title_2_0_20.1.json (OpenCode 2.0.20 authentic hook capture)"},
	{ordinal: 205, arm: "OpenCode2SessionModelRequest", event: registration.EventOpenCode2SessionModelRequest, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_model_request_2_0_20.2.json (OpenCode 2.0.20 authentic hook capture)"},
	{ordinal: 206, arm: "OpenCode2SessionHttpRequest", event: registration.EventOpenCode2SessionHttpRequest, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_http_request_2_0_20.2.json (OpenCode 2.0.20 authentic hook capture)"},
	{ordinal: 207, arm: "OpenCode2SessionHttpResponse", event: registration.EventOpenCode2SessionHttpResponse, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_http_response_2_0_20.1.json (OpenCode 2.0.20 authentic hook capture)"},
	{ordinal: 208, arm: "OpenCode2ToolExecuteBefore", event: registration.EventOpenCode2ToolExecuteBefore, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_tool_execute_before_2_0_20.1.json (OpenCode 2.0.20 authentic hook capture)"},
	{ordinal: 209, arm: "OpenCode2ToolExecuteAfter", event: registration.EventOpenCode2ToolExecuteAfter, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_tool_execute_after_2_0_20.1.json (OpenCode 2.0.20 authentic hook capture)"},
	{ordinal: 210, arm: "OpenCode2PermissionEvaluate", event: registration.EventOpenCode2PermissionEvaluate, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_permission_evaluate_2_0_20.2.json (OpenCode 2.0.20 authentic hook capture)"},
}

// openCodeProductionProofs declares every OpenCode production proof. The arm
// becomes the constant ProductionProof<arm>.
var openCodeProductionProofs = [...]productionProofDeclaration{
	{ordinal: 200, arm: "OpenCodeSessionCreated", event: registration.EventOpenCodeSessionCreated, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCodeHandlersToDurableReadBack/session.created"},
	{ordinal: 201, arm: "OpenCodeToolExecuteBefore", event: registration.EventOpenCodeToolExecuteBefore, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCodeHandlersToDurableReadBack/tool.execute.before"},
	{ordinal: 202, arm: "OpenCode2SessionPrompt", event: registration.EventOpenCode2SessionPrompt, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.prompt"},
	{ordinal: 203, arm: "OpenCode2SessionContext", event: registration.EventOpenCode2SessionContext, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.context"},
	{ordinal: 204, arm: "OpenCode2SessionTitle", event: registration.EventOpenCode2SessionTitle, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.title"},
	{ordinal: 205, arm: "OpenCode2SessionModelRequest", event: registration.EventOpenCode2SessionModelRequest, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.model.request"},
	{ordinal: 206, arm: "OpenCode2SessionHttpRequest", event: registration.EventOpenCode2SessionHttpRequest, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.http.request"},
	{ordinal: 207, arm: "OpenCode2SessionHttpResponse", event: registration.EventOpenCode2SessionHttpResponse, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.http.response"},
	{ordinal: 208, arm: "OpenCode2ToolExecuteBefore", event: registration.EventOpenCode2ToolExecuteBefore, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/tool.execute.before"},
	{ordinal: 209, arm: "OpenCode2ToolExecuteAfter", event: registration.EventOpenCode2ToolExecuteAfter, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/tool.execute.after"},
	{ordinal: 210, arm: "OpenCode2PermissionEvaluate", event: registration.EventOpenCode2PermissionEvaluate, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/permission.evaluate"},
}

// openCodeTargetEventDeclarations is the static typed target declaration for
// OpenCode at the recorded version. Generated catalog membership alone carries no proof.
var openCodeTargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventOpenCodeSessionCreated, captureProof: CaptureProofOpenCodeSessionCreated, productionProof: ProductionProofOpenCodeSessionCreated},
	{event: registration.EventOpenCodeToolExecuteBefore, captureProof: CaptureProofOpenCodeToolExecuteBefore, productionProof: ProductionProofOpenCodeToolExecuteBefore},
}

// OpenCode1_18_29TargetEvents returns a defensive copy of the proved target set.
func OpenCode1_18_29TargetEvents() []model.ContractEventKind {
	return targetEvents(openCodeTargetEventDeclarations[:])
}

// OpenCode1_18_29 derives a fresh exhaustive activation manifest from the
// generated host manifest and the proved static target declaration.
func OpenCode1_18_29() ([]Entry, error) {
	return deriveManifest("activation.OpenCode1_18_29", registration.OpenCode1_18_29().Entries(), openCodeTargetEventDeclarations[:])
}

// openCode2TargetEventDeclarations is the static typed target declaration for
// OpenCode at the 2.0.20 contract, recorded by the capture sitting in
// internal/lifecycle/ingress/opencode/testdata/fixtures/CLEARANCE.md.
//
// Nine coordinates are enabled: each binds the capture proof naming its
// committed fixture (the smallest authentic capture, so the sequence number is
// not always 1) and the production proof that drives those bytes through the
// built binary. The shell create row fired and was captured, but its payload
// is an environment dump the substitution rules cannot make safe; it stays
// withheld by the user decision recorded in that CLEARANCE.md. The seven
// remaining coordinates did not fire in the sitting and stay bare targets,
// withheld for missing-fixture until a capture lands. Enabling a row is a data
// change only: declare its proofs above and bind them here; the response
// capability derivation is untouched.
var openCode2TargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventOpenCode2SessionCreated}, // did not fire in the 2.0.20 capture sitting; withheld missing-fixture
	{event: registration.EventOpenCode2SessionPrompt, captureProof: CaptureProofOpenCode2SessionPrompt, productionProof: ProductionProofOpenCode2SessionPrompt},
	{event: registration.EventOpenCode2SessionContext, captureProof: CaptureProofOpenCode2SessionContext, productionProof: ProductionProofOpenCode2SessionContext},
	{event: registration.EventOpenCode2SessionCompaction}, // did not fire in the 2.0.20 capture sitting; withheld missing-fixture
	{event: registration.EventOpenCode2SessionGenerate},   // did not fire in the 2.0.20 capture sitting; withheld missing-fixture
	{event: registration.EventOpenCode2SessionTitle, captureProof: CaptureProofOpenCode2SessionTitle, productionProof: ProductionProofOpenCode2SessionTitle},
	{event: registration.EventOpenCode2SessionModelRequest, captureProof: CaptureProofOpenCode2SessionModelRequest, productionProof: ProductionProofOpenCode2SessionModelRequest},
	{event: registration.EventOpenCode2SessionHttpRequest, captureProof: CaptureProofOpenCode2SessionHttpRequest, productionProof: ProductionProofOpenCode2SessionHttpRequest},
	{event: registration.EventOpenCode2SessionHttpResponse, captureProof: CaptureProofOpenCode2SessionHttpResponse, productionProof: ProductionProofOpenCode2SessionHttpResponse},
	{event: registration.EventOpenCode2SessionExperimentalWsHandshake}, // did not fire in the 2.0.20 capture sitting; withheld missing-fixture
	{event: registration.EventOpenCode2SessionExperimentalWsSend},      // did not fire in the 2.0.20 capture sitting; withheld missing-fixture
	{event: registration.EventOpenCode2SessionExperimentalWsReceive},   // did not fire in the 2.0.20 capture sitting; withheld missing-fixture
	{event: registration.EventOpenCode2SessionRetry},                   // did not fire in the 2.0.20 capture sitting; withheld missing-fixture
	{event: registration.EventOpenCode2ToolExecuteBefore, captureProof: CaptureProofOpenCode2ToolExecuteBefore, productionProof: ProductionProofOpenCode2ToolExecuteBefore},
	{event: registration.EventOpenCode2ToolExecuteAfter, captureProof: CaptureProofOpenCode2ToolExecuteAfter, productionProof: ProductionProofOpenCode2ToolExecuteAfter},
	{event: registration.EventOpenCode2PermissionEvaluate, captureProof: CaptureProofOpenCode2PermissionEvaluate, productionProof: ProductionProofOpenCode2PermissionEvaluate},
	{event: registration.EventOpenCode2ShellCreateBefore, withheldReason: WithheldUnclearablePayload, clearance: "internal/lifecycle/ingress/opencode/testdata/fixtures/CLEARANCE.md"},
}

// OpenCode2_0_20TargetEvents returns a defensive copy of the 2.0.20 target set.
func OpenCode2_0_20TargetEvents() []model.ContractEventKind {
	return targetEvents(openCode2TargetEventDeclarations[:])
}

// OpenCode2_0_20 derives a fresh exhaustive activation manifest from the
// generated 2.0.20 host manifest and the static target declaration.
func OpenCode2_0_20() ([]Entry, error) {
	return deriveManifest("activation.OpenCode2_0_20", registration.OpenCode2_0_20().Entries(), openCode2TargetEventDeclarations[:])
}
