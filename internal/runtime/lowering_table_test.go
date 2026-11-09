package runtime_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loweringRow pins one cell of the operation lowering table: what a target
// declares for one kind, its availability class, and the text an agent reads
// when the kind is not native. native is set only for a native row; degrade is
// a required substring for mediated, semantic and unsupported rows.
type loweringRow struct {
	harness ir.HarnessID
	kind    ir.OperationKind
	class   effects.RuntimeClass
	native  string
	degrade string
}

// loweringRows has exactly one row per kind per target. The coverage loop in
// TestLoweringTable refuses a missing row and the uniqueness check refuses a
// duplicate, so this table cannot silently stop covering a contract row.
var loweringRows = []loweringRow{
	// Claude Code
	{ir.HarnessClaudeCode, ir.OperationInvokeSkill, effects.RuntimeClassNative, "Skill", ""},
	{ir.HarnessClaudeCode, ir.OperationDelegateAssignment, effects.RuntimeClassNative, "Agent", ""},
	{ir.HarnessClaudeCode, ir.OperationContinueAssignment, effects.RuntimeClassNative, "SendMessage", ""},
	{ir.HarnessClaudeCode, ir.OperationSendAssignmentMessage, effects.RuntimeClassNative, "SendMessage", ""},
	{ir.HarnessClaudeCode, ir.OperationCollectAssignmentResults, effects.RuntimeClassParentMediated, "", "the parent gathers each delegated Agent result"},
	{ir.HarnessClaudeCode, ir.OperationStopAssignment, effects.RuntimeClassNative, "TaskStop", ""},
	{ir.HarnessClaudeCode, ir.OperationRequestUserDecision, effects.RuntimeClassNative, "AskUserQuestion", ""},

	// OpenCode
	{ir.HarnessOpenCode, ir.OperationInvokeSkill, effects.RuntimeClassNative, "skill", ""},
	{ir.HarnessOpenCode, ir.OperationDelegateAssignment, effects.RuntimeClassNative, "task", ""},
	{ir.HarnessOpenCode, ir.OperationContinueAssignment, effects.RuntimeClassSemanticInstruction, "", "this contract binds no native follow-up call"},
	{ir.HarnessOpenCode, ir.OperationSendAssignmentMessage, effects.RuntimeClassSemanticInstruction, "", "this contract binds no native persistent-message call"},
	{ir.HarnessOpenCode, ir.OperationCollectAssignmentResults, effects.RuntimeClassSemanticInstruction, "", "this contract binds no native wait call"},
	{ir.HarnessOpenCode, ir.OperationStopAssignment, effects.RuntimeClassUnsupported, "", "this contract binds no native close or stop call"},
	{ir.HarnessOpenCode, ir.OperationRequestUserDecision, effects.RuntimeClassNative, "question", ""},

	// Codex
	{ir.HarnessCodex, ir.OperationInvokeSkill, effects.RuntimeClassSemanticInstruction, "", "this contract binds no native skill call"},
	{ir.HarnessCodex, ir.OperationDelegateAssignment, effects.RuntimeClassParentMediated, "", "the parent drives Codex delegation over the collaboration surface"},
	{ir.HarnessCodex, ir.OperationContinueAssignment, effects.RuntimeClassParentMediated, "", "the parent resumes the Codex assignment"},
	{ir.HarnessCodex, ir.OperationSendAssignmentMessage, effects.RuntimeClassParentMediated, "", "the parent relays the message over the Codex collaboration surface"},
	{ir.HarnessCodex, ir.OperationCollectAssignmentResults, effects.RuntimeClassParentMediated, "", "the parent collects Codex assignment results"},
	{ir.HarnessCodex, ir.OperationStopAssignment, effects.RuntimeClassParentMediated, "", "the parent stops the Codex assignment"},
	{ir.HarnessCodex, ir.OperationRequestUserDecision, effects.RuntimeClassNative, "request-input", ""},
}

type loweringKey struct {
	harness ir.HarnessID
	kind    ir.OperationKind
}

func TestLoweringTable(t *testing.T) {
	t.Parallel()

	rows := make(map[loweringKey]loweringRow, len(loweringRows))
	for _, row := range loweringRows {
		key := loweringKey{row.harness, row.kind}
		_, duplicate := rows[key]
		require.False(t, duplicate, "lowering table has two rows for %s/%s", row.harness, row.kind)
		rows[key] = row
	}

	for _, contract := range runtime.PinnedContracts() {
		for _, kind := range ir.AllOperationKinds() {
			key := loweringKey{contract.Harness(), kind}
			row, ok := rows[key]
			require.True(t, ok, "lowering table has no row for %s/%s", contract.Harness(), kind)

			descriptor, ok := runtime.CoreOperationDescriptorFor(kind)
			require.True(t, ok, "kind %q has no core descriptor", kind)
			binding, err := runtime.LookupOperationBinding(contract, descriptor)

			if row.class == effects.RuntimeClassUnsupported {
				require.Error(t, err, "%s/%s is unsupported but LookupOperationBinding returned a binding", contract.Harness(), kind)
				assert.Contains(t, err.Error(), row.degrade,
					"%s/%s unsupported reason does not carry its degrade text", contract.Harness(), kind)
				assert.Empty(t, row.native, "%s/%s is unsupported and must not name a native call", contract.Harness(), kind)
				continue
			}

			require.NoError(t, err, "%s/%s must resolve a binding", contract.Harness(), kind)
			assert.Equal(t, row.class, binding.Class(), "%s/%s availability class", contract.Harness(), kind)

			switch row.class {
			case effects.RuntimeClassNative:
				call, native := binding.Native()
				require.True(t, native, "%s/%s is native but exposes no native call", contract.Harness(), kind)
				assert.Equal(t, row.native, call.CallName(), "%s/%s native name", contract.Harness(), kind)
				assert.Empty(t, row.degrade, "%s/%s is native and must carry no degrade text", contract.Harness(), kind)
			case effects.RuntimeClassParentMediated:
				lowering, mediated := binding.Mediated()
				require.True(t, mediated, "%s/%s is mediated but exposes no mediation", contract.Harness(), kind)
				assert.Contains(t, lowering.Instruction(), row.degrade,
					"%s/%s mediation does not carry its degrade text", contract.Harness(), kind)
				assert.Empty(t, row.native, "%s/%s is mediated and must not name a native call", contract.Harness(), kind)
			case effects.RuntimeClassSemanticInstruction:
				lowering, semantic := binding.Semantic()
				require.True(t, semantic, "%s/%s is a semantic instruction but exposes none", contract.Harness(), kind)
				assert.Contains(t, lowering.InstructionTemplate(), row.degrade,
					"%s/%s semantic instruction does not carry its degrade text", contract.Harness(), kind)
				assert.Empty(t, row.native, "%s/%s is semantic and must not name a native call", contract.Harness(), kind)
			default:
				t.Fatalf("%s/%s has unknown class %q", contract.Harness(), kind, row.class)
			}
		}
	}
}

// harnessToolRows pins the one home for native file and shell tool names.
var harnessToolRows = []struct {
	harness ir.HarnessID
	tool    ir.HarnessTool
	native  string // empty when the harness declares no per-agent tool list
}{
	{ir.HarnessClaudeCode, ir.HarnessToolRead, "Read"},
	{ir.HarnessClaudeCode, ir.HarnessToolGlob, "Glob"},
	{ir.HarnessClaudeCode, ir.HarnessToolGrep, "Grep"},
	{ir.HarnessClaudeCode, ir.HarnessToolBash, "Bash"},
	{ir.HarnessClaudeCode, ir.HarnessToolEdit, "Edit"},
	{ir.HarnessClaudeCode, ir.HarnessToolWrite, "Write"},
	{ir.HarnessClaudeCode, ir.HarnessToolTask, "Task"},
	{ir.HarnessClaudeCode, ir.HarnessToolWebFetch, "WebFetch"},

	{ir.HarnessOpenCode, ir.HarnessToolRead, "read"},
	{ir.HarnessOpenCode, ir.HarnessToolGlob, "glob"},
	{ir.HarnessOpenCode, ir.HarnessToolGrep, "grep"},
	{ir.HarnessOpenCode, ir.HarnessToolBash, "bash"},
	{ir.HarnessOpenCode, ir.HarnessToolEdit, "edit"},
	{ir.HarnessOpenCode, ir.HarnessToolWrite, "write"},
	{ir.HarnessOpenCode, ir.HarnessToolTask, "task"},
	{ir.HarnessOpenCode, ir.HarnessToolWebFetch, "webfetch"},

	{ir.HarnessCodex, ir.HarnessToolRead, ""},
	{ir.HarnessCodex, ir.HarnessToolGlob, ""},
	{ir.HarnessCodex, ir.HarnessToolGrep, ""},
	{ir.HarnessCodex, ir.HarnessToolBash, ""},
	{ir.HarnessCodex, ir.HarnessToolEdit, ""},
	{ir.HarnessCodex, ir.HarnessToolWrite, ""},
	{ir.HarnessCodex, ir.HarnessToolTask, ""},
	{ir.HarnessCodex, ir.HarnessToolWebFetch, ""},
}

func TestHarnessToolLoweringTable(t *testing.T) {
	t.Parallel()

	for _, row := range harnessToolRows {
		t.Run(string(row.harness)+"/"+string(row.tool), func(t *testing.T) {
			t.Parallel()
			lowering, err := runtime.LookupHarnessTool(row.harness, row.tool)
			require.NoError(t, err)
			require.True(t, lowering.IsValid(), "lowering must be either native or a degradation instruction")
			if row.native == "" {
				assert.False(t, lowering.Available(), "%s/%s must be unavailable", row.harness, row.tool)
				assert.Empty(t, lowering.NativeName())
				assert.Contains(t, lowering.DegradeInstruction(), "no per-agent tool list")
				return
			}
			assert.True(t, lowering.Available(), "%s/%s must be available", row.harness, row.tool)
			assert.Equal(t, row.native, lowering.NativeName())
			assert.Empty(t, lowering.DegradeInstruction())
		})
	}
}
