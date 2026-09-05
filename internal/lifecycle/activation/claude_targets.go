package activation

import (
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// This file is the Claude Code activation target declaration. It is the one
// file a Claude Code coverage change edits: the proof arms below are generated
// into proofs_claude.gen.go by `make generate`, and the target table binds
// each generated event to its proofs or to its withholding reason.
//
// Ordinals 1-99 belong to Claude Code. The generator refuses an ordinal
// outside that range and an ordinal another harness file already uses.

// claudeCaptureProofs declares every Claude Code capture proof: the committed
// fixture that proves a reviewed native capture exists for the pinned
// contract. The arm becomes the constant CaptureProof<arm>.
var claudeCaptureProofs = [...]captureProofDeclaration{
	{ordinal: 1, arm: "SessionStart", event: registration.EventSessionStart, fixture: "internal/lifecycle/ingress/claude/testdata/fixtures/session_start_2_1_222.json (Claude Code 2.1.222 authentic capture)"},
	{ordinal: 2, arm: "SessionEnd", event: registration.EventSessionEnd, fixture: "internal/lifecycle/ingress/claude/testdata/fixtures/session_end_2_1_222.json (Claude Code 2.1.222 authentic capture)"},
	{ordinal: 3, arm: "PreToolUse", event: registration.EventPreToolUse, fixture: "internal/lifecycle/ingress/claude/testdata/fixtures/pre_tool_use_2_1_222.json (Claude Code 2.1.222 authentic capture)"},
	{ordinal: 4, arm: "PostToolUse", event: registration.EventPostToolUse, fixture: "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_use_2_1_222.json (Claude Code 2.1.222 authentic capture)"},
	{ordinal: 5, arm: "PostToolUseFailure", event: registration.EventPostToolUseFailure, fixture: "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_use_failure_2_1_222.json (Claude Code 2.1.222 authentic capture)"},
	{ordinal: 6, arm: "PostToolBatch", event: registration.EventPostToolBatch, fixture: "internal/lifecycle/ingress/claude/testdata/fixtures/post_tool_batch_2_1_222.json (Claude Code 2.1.222 authentic capture)"},
	{ordinal: 7, arm: "PreCompact", event: registration.EventPreCompact, fixture: "internal/lifecycle/ingress/claude/testdata/fixtures/pre_compact_2_1_222.json (Claude Code 2.1.222 authentic capture)"},
	{ordinal: 8, arm: "PostCompact", event: registration.EventPostCompact, fixture: "internal/lifecycle/ingress/claude/testdata/fixtures/post_compact_2_1_222.json (Claude Code 2.1.222 authentic capture)"},
}

// claudeProductionProofs declares every Claude Code production proof: the
// production-path test that admitted the event through the built binary. The
// arm becomes the constant ProductionProof<arm>.
var claudeProductionProofs = [...]productionProofDeclaration{
	{ordinal: 1, arm: "SessionStart", event: registration.EventSessionStart, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/SessionStart"},
	{ordinal: 2, arm: "SessionEnd", event: registration.EventSessionEnd, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/SessionEnd"},
	{ordinal: 3, arm: "PreToolUse", event: registration.EventPreToolUse, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PreToolUse"},
	{ordinal: 4, arm: "PostToolUse", event: registration.EventPostToolUse, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolUse"},
	{ordinal: 5, arm: "PostToolUseFailure", event: registration.EventPostToolUseFailure, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolUseFailure"},
	{ordinal: 6, arm: "PostToolBatch", event: registration.EventPostToolBatch, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostToolBatch"},
	{ordinal: 7, arm: "PreCompact", event: registration.EventPreCompact, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PreCompact"},
	{ordinal: 8, arm: "PostCompact", event: registration.EventPostCompact, test: "cmd/pasture/hook_lifecycle_production_test.go:TestEnabledClaudeAuthenticFixturesToDurableEvidence/PostCompact"},
}

// claudeTargetEventDeclarations is the static Claude Code target table.
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
	return targetEvents(claudeTargetEventDeclarations[:])
}

// ClaudeCode2_1_210 returns a fresh exhaustive activation manifest. The
// manifest is derived only from the generated registration manifest and the
// static typed target declaration table; it performs no filesystem access.
func ClaudeCode2_1_210() ([]Entry, error) {
	return deriveManifest("activation.ClaudeCode2_1_210", registration.ClaudeCode2_1_210().Entries(), claudeTargetEventDeclarations[:])
}
