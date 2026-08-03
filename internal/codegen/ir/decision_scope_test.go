package ir_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequestUserDecisionScopeAndResultsAreCanonical proves the fix for the
// prior revision's bug: RequestUserDecision.canonicalOperation() always used
// a zero ScopeID and no declared results, so a decision never actually
// participated in the lexical binding system every other SemanticOperation
// uses. It must now carry its constructor-owned scope and declared result(s)
// through to CanonicalSemanticOperation, and reject an out-of-scope result
// exactly like every other operation.
func TestRequestUserDecisionScopeAndResultsAreCanonical(t *testing.T) {
	t.Parallel()

	scope, err := ir.NewRootScope("assignment")
	require.NoError(t, err)
	codec, err := ir.NewJSONCodec[string]("pasture.test.decision-answer/v1", nil)
	require.NoError(t, err)
	resultKey, err := ir.NewBindingKey[string]("pasture.binding.decision-answer")
	require.NoError(t, err)
	slot, err := ir.NewResultSlot(resultKey, scope, codec)
	require.NoError(t, err)
	declaration, err := ir.DeclareResultSlot(slot)
	require.NoError(t, err)

	prompt := ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "What changed?"}}
	request, err := ir.NewRequestUserDecision(
		"request-scope-1", ir.HarnessClaudeCode, mustContract(t, ir.HarnessClaudeCode, "2.1.210"),
		mustTaskRef(t, "epoch-1"), mustTaskRef(t, "gate-1"), "test-purpose", prompt,
		scope, declaration,
	)
	require.NoError(t, err)
	assert.Equal(t, scope, request.Scope())
	require.Len(t, request.Results(), 1)
	assert.Equal(t, declaration.Key(), request.Results()[0].Key())

	// CanonicalSemanticOperation is the input to target lowering (native
	// rendering) as well as the parent-mediated protocol; both must see the
	// real scope and declared results, not the zero ScopeID a prior revision
	// silently substituted.
	canonical, err := ir.CanonicalSemanticOperation(request)
	require.NoError(t, err)
	var envelope struct {
		Kind    string `json:"kind"`
		Scope   string `json:"scope"`
		Results []struct {
			Key    string `json:"key"`
			Scope  string `json:"scope"`
			Schema string `json:"schema"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(canonical, &envelope))
	assert.Equal(t, string(ir.OperationRequestUserDecision), envelope.Kind)
	assert.Equal(t, scope.String(), envelope.Scope)
	require.Len(t, envelope.Results, 1)
	assert.Equal(t, "pasture.binding.decision-answer", envelope.Results[0].Key)
	assert.Equal(t, scope.String(), envelope.Results[0].Scope)
	assert.Equal(t, "pasture.test.decision-answer/v1", envelope.Results[0].Schema)
}

// TestRequestUserDecisionRejectsOutOfScopeResult mirrors newOperationBase's
// existing out-of-scope rejection (already exercised for InvokeSkill etc.)
// specifically for RequestUserDecision, since it now shares the same
// operationBase wiring.
func TestRequestUserDecisionRejectsOutOfScopeResult(t *testing.T) {
	t.Parallel()

	requestScope, err := ir.NewRootScope("request-scope")
	require.NoError(t, err)
	otherScope, err := ir.NewRootScope("other-scope")
	require.NoError(t, err)
	codec, err := ir.NewJSONCodec[string]("pasture.test.decision-answer/v1", nil)
	require.NoError(t, err)
	resultKey, err := ir.NewBindingKey[string]("pasture.binding.decision-answer")
	require.NoError(t, err)
	// Declare the result slot in otherScope, then try to attach it to a
	// request constructed with requestScope — must fail, matching every
	// other operation's "result belongs to scope %q, not operation scope %q"
	// rejection.
	slot, err := ir.NewResultSlot(resultKey, otherScope, codec)
	require.NoError(t, err)
	declaration, err := ir.DeclareResultSlot(slot)
	require.NoError(t, err)

	prompt := ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "What changed?"}}
	_, err = ir.NewRequestUserDecision(
		"request-scope-2", ir.HarnessClaudeCode, mustContract(t, ir.HarnessClaudeCode, "2.1.210"),
		mustTaskRef(t, "epoch-1"), mustTaskRef(t, "gate-1"), "test-purpose", prompt,
		requestScope, declaration,
	)
	require.Error(t, err)
	for _, field := range []string{"what:", "why:", "where:", "phase:", "impact:", "fix:"} {
		assert.Contains(t, err.Error(), field)
	}
}

// TestReportedUserDecisionCopiesScopeAndResultsFromRequest proves the report
// side of the fix: base (scope/results) is never part of the wire form —
// DecodeReportedResult always copies it from the already-validated
// originating request, so a caller decoding an untrusted report still learns
// exactly where the captured answer belongs in the parent's bindings.
func TestReportedUserDecisionCopiesScopeAndResultsFromRequest(t *testing.T) {
	t.Parallel()

	scope, err := ir.NewRootScope("assignment")
	require.NoError(t, err)
	codec, err := ir.NewJSONCodec[string]("pasture.test.decision-answer/v1", nil)
	require.NoError(t, err)
	resultKey, err := ir.NewBindingKey[string]("pasture.binding.decision-answer")
	require.NoError(t, err)
	slot, err := ir.NewResultSlot(resultKey, scope, codec)
	require.NoError(t, err)
	declaration, err := ir.DeclareResultSlot(slot)
	require.NoError(t, err)

	prompt := ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "What changed?"}}
	request, err := ir.NewRequestUserDecision(
		"request-scope-3", ir.HarnessClaudeCode, mustContract(t, ir.HarnessClaudeCode, "2.1.210"),
		mustTaskRef(t, "epoch-1"), mustTaskRef(t, "gate-1"), "test-purpose", prompt,
		scope, declaration,
	)
	require.NoError(t, err)

	report := reportFor(request, ir.FreeTextResult{Text: "an answer"})
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	decoded, _, err := request.DecodeReportedResult(bytes.NewReader(encoded), 64<<10)
	require.NoError(t, err)
	assert.Equal(t, scope, decoded.Scope())
	require.Len(t, decoded.Results(), 1)
	assert.Equal(t, declaration.Key(), decoded.Results()[0].Key())

	// A raw struct literal (as any caller outside this package must use,
	// since base is unexported) never has scope/results — proving they are
	// constructor/decode-owned, not a public field a caller can forge.
	var literal ir.ReportedUserDecision
	assert.False(t, literal.Scope().IsValid())
	assert.Empty(t, literal.Results())
}

// TestRequestUserDecisionRejectsCrossHarnessRuntimeContract is the mismatch-rejection guard:
// a RuntimeContractID constructed for one
// harness must not be accepted as the contract for a request declaring a
// different harness, even though both harness and contract are individually
// valid.
func TestRequestUserDecisionRejectsCrossHarnessRuntimeContract(t *testing.T) {
	t.Parallel()

	scope, err := ir.NewRootScope("mismatch")
	require.NoError(t, err)
	prompt := ir.FreeTextPrompt{Stimulus: ir.PromptStimulus{Question: "Explain"}}

	openCodeContract := mustContract(t, ir.HarnessOpenCode, "1.17.18")
	_, err = ir.NewRequestUserDecision(
		"request-cross-harness", ir.HarnessClaudeCode, openCodeContract,
		mustTaskRef(t, "epoch-1"), mustTaskRef(t, "gate-1"), "test-purpose", prompt,
		scope,
	)
	require.Error(t, err, "a claude-code harness declaration with an opencode-bound contract must be rejected")
	assert.Contains(t, err.Error(), "opencode")
	assert.Contains(t, err.Error(), "claude-code")

	// A forged report claiming a different (but individually valid) contract
	// than the one the request was constructed with must also be rejected —
	// compareReportIdentity is exact-equality, so this also demonstrates the
	// forged-value path end to end through the decode boundary, not just at
	// construction time.
	claudeContract := mustContract(t, ir.HarnessClaudeCode, "2.1.210")
	request, err := ir.NewRequestUserDecision(
		"request-forged", ir.HarnessClaudeCode, claudeContract,
		mustTaskRef(t, "epoch-1"), mustTaskRef(t, "gate-1"), "test-purpose", prompt,
		scope,
	)
	require.NoError(t, err)
	forgedReport := reportFor(request, ir.FreeTextResult{Text: "answer"})
	forgedReport.RuntimeContract = mustContract(t, ir.HarnessClaudeCode, "9.9.9")
	encoded, err := json.Marshal(forgedReport)
	require.NoError(t, err)
	_, canonical, err := request.DecodeReportedResult(bytes.NewReader(encoded), 64<<10)
	require.Error(t, err, "a report claiming a different runtime_contract than the originating request must be rejected")
	assert.False(t, canonical.IsValid())
}
