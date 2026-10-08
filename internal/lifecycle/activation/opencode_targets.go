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
	{ordinal: 211, arm: "OpenCode2SessionCreated", event: registration.EventOpenCode2SessionCreated, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_created_2_0_21.1.json (OpenCode 2.0.21 authentic bus capture)"},
	{ordinal: 212, arm: "OpenCode2SessionCompaction", event: registration.EventOpenCode2SessionCompaction, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_compaction_2_0_21.1.json (OpenCode 2.0.21 authentic hook capture)"},
	{ordinal: 213, arm: "OpenCode2SessionGenerate", event: registration.EventOpenCode2SessionGenerate, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_generate_2_0_21.1.json (OpenCode 2.0.21 authentic hook capture)"},
	{ordinal: 214, arm: "OpenCode2SessionExperimentalWsHandshake", event: registration.EventOpenCode2SessionExperimentalWsHandshake, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_experimental_ws_handshake_2_0_21.1.json (OpenCode 2.0.21 authentic hook capture)"},
	{ordinal: 215, arm: "OpenCode2SessionExperimentalWsSend", event: registration.EventOpenCode2SessionExperimentalWsSend, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_experimental_ws_send_2_0_21.1.json (OpenCode 2.0.21 authentic hook capture)"},
	{ordinal: 216, arm: "OpenCode2SessionExperimentalWsReceive", event: registration.EventOpenCode2SessionExperimentalWsReceive, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_experimental_ws_receive_2_0_21.1.json (OpenCode 2.0.21 authentic hook capture)"},
	{ordinal: 217, arm: "OpenCode2SessionRetry", event: registration.EventOpenCode2SessionRetry, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_session_retry_2_0_21.1.json (OpenCode 2.0.21 authentic hook capture)"},
	{ordinal: 218, arm: "OpenCode2ShellCreateBefore", event: registration.EventOpenCode2ShellCreateBefore, fixture: "internal/lifecycle/ingress/opencode/testdata/fixtures/opencode_shell_create_before_2_0_20.1.json (OpenCode 2.0.20 authentic hook capture, environment values cleared by env-dump-v1)"},
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
	{ordinal: 211, arm: "OpenCode2SessionCreated", event: registration.EventOpenCode2SessionCreated, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.created"},
	{ordinal: 212, arm: "OpenCode2SessionCompaction", event: registration.EventOpenCode2SessionCompaction, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.compaction"},
	{ordinal: 213, arm: "OpenCode2SessionGenerate", event: registration.EventOpenCode2SessionGenerate, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.generate"},
	{ordinal: 214, arm: "OpenCode2SessionExperimentalWsHandshake", event: registration.EventOpenCode2SessionExperimentalWsHandshake, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.experimental.ws.handshake"},
	{ordinal: 215, arm: "OpenCode2SessionExperimentalWsSend", event: registration.EventOpenCode2SessionExperimentalWsSend, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.experimental.ws.send"},
	{ordinal: 216, arm: "OpenCode2SessionExperimentalWsReceive", event: registration.EventOpenCode2SessionExperimentalWsReceive, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.experimental.ws.receive"},
	{ordinal: 217, arm: "OpenCode2SessionRetry", event: registration.EventOpenCode2SessionRetry, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/session.retry"},
	{ordinal: 218, arm: "OpenCode2ShellCreateBefore", event: registration.EventOpenCode2ShellCreateBefore, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCode2HandlersToDurableReadBack/shell.create.before"},
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
// OpenCode at the 2.0.20 contract, recorded by the capture sittings in
// internal/lifecycle/ingress/opencode/testdata/fixtures/CLEARANCE.md.
//
// Seventeen coordinates are enabled. Ten were captured at 2.0.20 (nine
// coordinates plus shell.create.before); session.created did not fire in that
// sitting and was captured later on an OpenCode 2.0.21 host, a later release
// the 2.0.20 contract admits, through the plugin's bus subscription; the six
// remaining coordinates did not fire in that sitting either and were captured
// later on the same 2.0.21 host, through their registered hooks. Each enabled
// row binds the capture proof naming its committed fixture (the smallest
// authentic capture, so the sequence number is not always 1) and the
// production proof that drives those bytes through the built binary. The shell
// create payload is an environment dump; its values are redacted to a fixed
// placeholder by env-dump-v1 and the name-shape refusal is admitted only
// because the provenance records that rule and every value under the dump is
// neutralized. Enabling a row is a data change only: declare its proofs above
// and bind them here; the response capability derivation is untouched.
var openCode2TargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventOpenCode2SessionCreated, captureProof: CaptureProofOpenCode2SessionCreated, productionProof: ProductionProofOpenCode2SessionCreated},
	{event: registration.EventOpenCode2SessionPrompt, captureProof: CaptureProofOpenCode2SessionPrompt, productionProof: ProductionProofOpenCode2SessionPrompt},
	{event: registration.EventOpenCode2SessionContext, captureProof: CaptureProofOpenCode2SessionContext, productionProof: ProductionProofOpenCode2SessionContext},
	{event: registration.EventOpenCode2SessionCompaction, captureProof: CaptureProofOpenCode2SessionCompaction, productionProof: ProductionProofOpenCode2SessionCompaction},
	{event: registration.EventOpenCode2SessionGenerate, captureProof: CaptureProofOpenCode2SessionGenerate, productionProof: ProductionProofOpenCode2SessionGenerate},
	{event: registration.EventOpenCode2SessionTitle, captureProof: CaptureProofOpenCode2SessionTitle, productionProof: ProductionProofOpenCode2SessionTitle},
	{event: registration.EventOpenCode2SessionModelRequest, captureProof: CaptureProofOpenCode2SessionModelRequest, productionProof: ProductionProofOpenCode2SessionModelRequest},
	{event: registration.EventOpenCode2SessionHttpRequest, captureProof: CaptureProofOpenCode2SessionHttpRequest, productionProof: ProductionProofOpenCode2SessionHttpRequest},
	{event: registration.EventOpenCode2SessionHttpResponse, captureProof: CaptureProofOpenCode2SessionHttpResponse, productionProof: ProductionProofOpenCode2SessionHttpResponse},
	{event: registration.EventOpenCode2SessionExperimentalWsHandshake, captureProof: CaptureProofOpenCode2SessionExperimentalWsHandshake, productionProof: ProductionProofOpenCode2SessionExperimentalWsHandshake},
	{event: registration.EventOpenCode2SessionExperimentalWsSend, captureProof: CaptureProofOpenCode2SessionExperimentalWsSend, productionProof: ProductionProofOpenCode2SessionExperimentalWsSend},
	{event: registration.EventOpenCode2SessionExperimentalWsReceive, captureProof: CaptureProofOpenCode2SessionExperimentalWsReceive, productionProof: ProductionProofOpenCode2SessionExperimentalWsReceive},
	{event: registration.EventOpenCode2SessionRetry, captureProof: CaptureProofOpenCode2SessionRetry, productionProof: ProductionProofOpenCode2SessionRetry},
	{event: registration.EventOpenCode2ToolExecuteBefore, captureProof: CaptureProofOpenCode2ToolExecuteBefore, productionProof: ProductionProofOpenCode2ToolExecuteBefore},
	{event: registration.EventOpenCode2ToolExecuteAfter, captureProof: CaptureProofOpenCode2ToolExecuteAfter, productionProof: ProductionProofOpenCode2ToolExecuteAfter},
	{event: registration.EventOpenCode2PermissionEvaluate, captureProof: CaptureProofOpenCode2PermissionEvaluate, productionProof: ProductionProofOpenCode2PermissionEvaluate},
	{event: registration.EventOpenCode2ShellCreateBefore, captureProof: CaptureProofOpenCode2ShellCreateBefore, productionProof: ProductionProofOpenCode2ShellCreateBefore},
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
