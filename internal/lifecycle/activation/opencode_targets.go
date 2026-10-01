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
}

// openCodeProductionProofs declares every OpenCode production proof. The arm
// becomes the constant ProductionProof<arm>.
var openCodeProductionProofs = [...]productionProofDeclaration{
	{ordinal: 200, arm: "OpenCodeSessionCreated", event: registration.EventOpenCodeSessionCreated, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCodeHandlersToDurableReadBack/session.created"},
	{ordinal: 201, arm: "OpenCodeToolExecuteBefore", event: registration.EventOpenCodeToolExecuteBefore, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledOpenCodeHandlersToDurableReadBack/tool.execute.before"},
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
// OpenCode at the 2.0.20 contract. Every row is a bare target with no proofs,
// so each derives withheld for missing-fixture: the 2.0.20 capture sitting
// (with inventory, substitution, secret scan and clearance) and the
// production-path proofs have not landed yet. Rows gain proofs, never lose
// their target place, as that evidence arrives.
//
// Each row names the fixture it expects, one per coordinate, under the
// capture naming rule <harness>_<snake_event>_<host_version>.<n>.json in
// internal/lifecycle/ingress/opencode/testdata/fixtures/: the snake event
// spells the dotted native coordinate with underscores, the host version
// spells 2.0.20 with underscores, and <n> is the capture sequence starting
// at 1. The permission evaluate row expects
// opencode_permission_evaluate_2_0_20.1.json. The trailing comment on each
// row records that expected basename; no fixture is committed yet, so every
// row stays withheld and every response capability stays none until the
// capture and its clearance land, at which point enabling a row is a data
// change (declaring its proofs and binding them here) with no derivation
// change.
var openCode2TargetEventDeclarations = [...]targetEventDeclaration{
	{event: registration.EventOpenCode2SessionCreated},                 // expects opencode_session_created_2_0_20.1.json
	{event: registration.EventOpenCode2SessionPrompt},                  // expects opencode_session_prompt_2_0_20.1.json
	{event: registration.EventOpenCode2SessionContext},                 // expects opencode_session_context_2_0_20.1.json
	{event: registration.EventOpenCode2SessionCompaction},              // expects opencode_session_compaction_2_0_20.1.json
	{event: registration.EventOpenCode2SessionGenerate},                // expects opencode_session_generate_2_0_20.1.json
	{event: registration.EventOpenCode2SessionTitle},                   // expects opencode_session_title_2_0_20.1.json
	{event: registration.EventOpenCode2SessionModelRequest},            // expects opencode_session_model_request_2_0_20.1.json
	{event: registration.EventOpenCode2SessionHttpRequest},             // expects opencode_session_http_request_2_0_20.1.json
	{event: registration.EventOpenCode2SessionHttpResponse},            // expects opencode_session_http_response_2_0_20.1.json
	{event: registration.EventOpenCode2SessionExperimentalWsHandshake}, // expects opencode_session_experimental_ws_handshake_2_0_20.1.json
	{event: registration.EventOpenCode2SessionExperimentalWsSend},      // expects opencode_session_experimental_ws_send_2_0_20.1.json
	{event: registration.EventOpenCode2SessionExperimentalWsReceive},   // expects opencode_session_experimental_ws_receive_2_0_20.1.json
	{event: registration.EventOpenCode2SessionRetry},                   // expects opencode_session_retry_2_0_20.1.json
	{event: registration.EventOpenCode2ToolExecuteBefore},              // expects opencode_tool_execute_before_2_0_20.1.json
	{event: registration.EventOpenCode2ToolExecuteAfter},               // expects opencode_tool_execute_after_2_0_20.1.json
	{event: registration.EventOpenCode2PermissionEvaluate},             // expects opencode_permission_evaluate_2_0_20.1.json
	{event: registration.EventOpenCode2ShellCreateBefore},              // expects opencode_shell_create_before_2_0_20.1.json
}

// OpenCode2_0_20TargetEvents returns a defensive copy of the 2.0.20 target set.
func OpenCode2_0_20TargetEvents() []model.ContractEventKind {
	return targetEvents(openCode2TargetEventDeclarations[:])
}

// OpenCode2_0_20 derives a fresh exhaustive activation manifest from the
// generated 2.0.20 host manifest and the static target declaration. Until rows
// carry capture and production proofs every entry is withheld.
func OpenCode2_0_20() ([]Entry, error) {
	return deriveManifest("activation.OpenCode2_0_20", registration.OpenCode2_0_20().Entries(), openCode2TargetEventDeclarations[:])
}
