package runtime_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classify returns the runtime class every pinned contract assigns to a core
// operation, exercising the real lookup path (unsupported yields an error and
// therefore no binding).
func classify(t *testing.T, contract runtime.RuntimeContract, kind ir.OperationKind) (effects.RuntimeClass, string) {
	t.Helper()
	descriptor, ok := runtime.CoreOperationDescriptorFor(kind)
	require.True(t, ok)
	binding, err := runtime.LookupOperationBinding(contract, descriptor)
	if err != nil {
		return effects.RuntimeClassUnsupported, nativeCallName(binding)
	}
	return binding.Class(), nativeCallName(binding)
}

func nativeCallName(binding runtime.RuntimeBinding[runtime.OrchestrationRequest, runtime.OrchestrationResult]) string {
	if binding == nil {
		return ""
	}
	if call, ok := binding.Native(); ok {
		return call.CallName()
	}
	return ""
}

func TestPinnedContractsClassifyEveryCoreOperation(t *testing.T) {
	t.Parallel()
	for _, contract := range runtime.PinnedContracts() {
		contract := contract
		t.Run(contract.ID().String(), func(t *testing.T) {
			t.Parallel()
			for _, kind := range ir.AllOperationKinds() {
				class, _ := classify(t, contract, kind)
				assert.True(t, class.IsValid(), "operation %q has a valid classification", kind)
			}
		})
	}
}

func TestClaudeContractNamesNoRemovedTeamLifecycleCalls(t *testing.T) {
	t.Parallel()
	claude := runtime.ClaudeCode2_1_210()
	for _, kind := range ir.AllOperationKinds() {
		_, callName := classify(t, claude, kind)
		lowered := strings.ToLower(callName)
		assert.NotContains(t, lowered, "teamcreate", "operation %q must not name a removed team-lifecycle call", kind)
		assert.NotContains(t, lowered, "teamdelete", "operation %q must not name a removed team-lifecycle call", kind)
	}
}

func TestOpenCodeContractInventsNoTools(t *testing.T) {
	t.Parallel()
	opencode := runtime.OpenCode1_17_18()

	// No invented persistent-message / follow-up / wait native tools.
	forbidden := []string{"task_agent_message", "follow_up", "followup", "wait", "task_close"}
	for _, kind := range ir.AllOperationKinds() {
		_, callName := classify(t, opencode, kind)
		lowered := strings.ToLower(callName)
		for _, name := range forbidden {
			assert.NotContains(t, lowered, name, "operation %q must not invent OpenCode tool %q", kind, name)
		}
	}

	// Stopping an assignment is explicitly unsupported, not a fabricated close.
	class, _ := classify(t, opencode, ir.OperationStopAssignment)
	assert.Equal(t, effects.RuntimeClassUnsupported, class)

	// Only documented surfaces appear as native calls.
	skillClass, skillCall := classify(t, opencode, ir.OperationInvokeSkill)
	assert.Equal(t, effects.RuntimeClassNative, skillClass)
	assert.Equal(t, "skill", skillCall)
	taskClass, taskCall := classify(t, opencode, ir.OperationDelegateAssignment)
	assert.Equal(t, effects.RuntimeClassNative, taskClass)
	assert.Equal(t, "task", taskCall)
	questionClass, questionCall := classify(t, opencode, ir.OperationRequestUserDecision)
	assert.Equal(t, effects.RuntimeClassNative, questionClass)
	assert.Equal(t, "question", questionCall)
}

func TestPinnedContractVersionBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		contract runtime.RuntimeContract
		exact    string
		lower    string
		higher   string
	}{
		{contract: runtime.ClaudeCode2_1_210(), exact: "2.1.210", lower: "2.1.209", higher: "2.1.211"},
		{contract: runtime.OpenCode1_17_18(), exact: "1.17.18", lower: "1.17.17", higher: "1.17.19"},
		{contract: runtime.Codex0_144_1(), exact: "0.144.1", lower: "0.144.0", higher: "0.144.2"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.contract.ID().String(), func(t *testing.T) {
			t.Parallel()
			assert.True(t, tc.contract.Supports(mustParse(t, tc.exact)), "exact accepted boundary")
			assert.True(t, tc.contract.Supports(mustParse(t, tc.exact+"+build.5")), "build metadata does not change precedence")
			assert.False(t, tc.contract.Supports(mustParse(t, tc.lower)), "immediately lower rejected")
			assert.False(t, tc.contract.Supports(mustParse(t, tc.higher)), "immediately higher rejected")
			assert.False(t, tc.contract.Supports(mustParse(t, tc.exact+"-rc.1")), "prerelease requires explicit inclusion")
			assert.False(t, tc.contract.Supports(runtime.HostVersion{}), "unparsed host rejected")
		})
	}
}

func TestPinnedContractHarnessBinding(t *testing.T) {
	t.Parallel()
	assert.Equal(t, ir.HarnessClaudeCode, runtime.ClaudeCode2_1_210().Harness())
	assert.Equal(t, ir.HarnessOpenCode, runtime.OpenCode1_17_18().Harness())
	assert.Equal(t, ir.HarnessCodex, runtime.Codex0_144_1().Harness())
}
