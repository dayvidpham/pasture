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
// declares for one kind, its availability class, and the exact native call name
// for a native row. Non-native rows assert structure and distinctness rather
// than pinned wording, so a behaviour-neutral rewording does not fail the test.
type loweringRow struct {
	harness ir.HarnessID
	kind    ir.OperationKind
	class   effects.RuntimeClass
	native  string
}

// loweringRows has exactly one row per kind per target. The coverage loop in
// TestLoweringTable refuses a missing row and the uniqueness check refuses a
// duplicate, so this table cannot silently stop covering a contract row.
var loweringRows = []loweringRow{
	// Claude Code
	{ir.HarnessClaudeCode, ir.OperationInvokeSkill, effects.RuntimeClassNative, "Skill"},
	{ir.HarnessClaudeCode, ir.OperationDelegateAssignment, effects.RuntimeClassNative, "Agent"},
	{ir.HarnessClaudeCode, ir.OperationContinueAssignment, effects.RuntimeClassNative, "SendMessage"},
	{ir.HarnessClaudeCode, ir.OperationSendAssignmentMessage, effects.RuntimeClassNative, "SendMessage"},
	{ir.HarnessClaudeCode, ir.OperationCollectAssignmentResults, effects.RuntimeClassParentMediated, ""},
	{ir.HarnessClaudeCode, ir.OperationStopAssignment, effects.RuntimeClassNative, "TaskStop"},
	{ir.HarnessClaudeCode, ir.OperationRequestUserDecision, effects.RuntimeClassNative, "AskUserQuestion"},

	// OpenCode
	{ir.HarnessOpenCode, ir.OperationInvokeSkill, effects.RuntimeClassNative, "skill"},
	{ir.HarnessOpenCode, ir.OperationDelegateAssignment, effects.RuntimeClassNative, "task"},
	{ir.HarnessOpenCode, ir.OperationContinueAssignment, effects.RuntimeClassSemanticInstruction, ""},
	{ir.HarnessOpenCode, ir.OperationSendAssignmentMessage, effects.RuntimeClassSemanticInstruction, ""},
	{ir.HarnessOpenCode, ir.OperationCollectAssignmentResults, effects.RuntimeClassSemanticInstruction, ""},
	{ir.HarnessOpenCode, ir.OperationStopAssignment, effects.RuntimeClassUnsupported, ""},
	{ir.HarnessOpenCode, ir.OperationRequestUserDecision, effects.RuntimeClassNative, "question"},

	// Codex
	{ir.HarnessCodex, ir.OperationInvokeSkill, effects.RuntimeClassSemanticInstruction, ""},
	{ir.HarnessCodex, ir.OperationDelegateAssignment, effects.RuntimeClassParentMediated, ""},
	{ir.HarnessCodex, ir.OperationContinueAssignment, effects.RuntimeClassParentMediated, ""},
	{ir.HarnessCodex, ir.OperationSendAssignmentMessage, effects.RuntimeClassParentMediated, ""},
	{ir.HarnessCodex, ir.OperationCollectAssignmentResults, effects.RuntimeClassParentMediated, ""},
	{ir.HarnessCodex, ir.OperationStopAssignment, effects.RuntimeClassParentMediated, ""},
	{ir.HarnessCodex, ir.OperationRequestUserDecision, effects.RuntimeClassNative, "request-input"},
}

type loweringKey struct {
	harness ir.HarnessID
	kind    ir.OperationKind
}

type loweringClass struct {
	harness ir.HarnessID
	class   effects.RuntimeClass
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

	// Two kinds in the same class on the same harness must carry different
	// instructions; identical text means one of them describes the other.
	seenInstruction := make(map[loweringClass]map[string]ir.OperationKind)
	recordInstruction := func(harness ir.HarnessID, class effects.RuntimeClass, text string, kind ir.OperationKind) {
		key := loweringClass{harness, class}
		if seenInstruction[key] == nil {
			seenInstruction[key] = make(map[string]ir.OperationKind)
		}
		if previous, duplicate := seenInstruction[key][text]; duplicate {
			assert.Failf(t, "duplicate lowering instruction",
				"%s/%s reuses the %s instruction of %s", harness, kind, class, previous)
		}
		seenInstruction[key][text] = kind
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
				assert.NotEmpty(t, err.Error(), "%s/%s unsupported must explain why", contract.Harness(), kind)
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
			case effects.RuntimeClassParentMediated:
				lowering, mediated := binding.Mediated()
				require.True(t, mediated, "%s/%s is mediated but exposes no mediation", contract.Harness(), kind)
				assert.NotEmpty(t, lowering.Instruction(), "%s/%s mediation must instruct the caller", contract.Harness(), kind)
				recordInstruction(contract.Harness(), row.class, lowering.Instruction(), kind)
				assert.Empty(t, row.native, "%s/%s is mediated and must not name a native call", contract.Harness(), kind)
			case effects.RuntimeClassSemanticInstruction:
				lowering, semantic := binding.Semantic()
				require.True(t, semantic, "%s/%s is a semantic instruction but exposes none", contract.Harness(), kind)
				assert.NotEmpty(t, lowering.InstructionTemplate(), "%s/%s semantic instruction must instruct the caller", contract.Harness(), kind)
				recordInstruction(contract.Harness(), row.class, lowering.InstructionTemplate(), kind)
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

	type toolCell struct {
		harness ir.HarnessID
		tool    ir.HarnessTool
	}
	rows := make(map[toolCell]string, len(harnessToolRows))
	for _, row := range harnessToolRows {
		key := toolCell{row.harness, row.tool}
		_, duplicate := rows[key]
		require.False(t, duplicate, "harness-tool lowering table has two rows for %s/%s", row.harness, row.tool)
		rows[key] = row.native
	}

	// Every registered tool must be classified on every pinned harness: a tool
	// added without a lowering leaves that cell unproven and is refused here.
	for _, contract := range runtime.PinnedContracts() {
		for _, tool := range ir.AllHarnessTools() {
			key := toolCell{contract.Harness(), tool}
			_, ok := rows[key]
			require.True(t, ok, "harness-tool lowering table has no row for %s/%s", contract.Harness(), tool)
		}
	}

	for _, contract := range runtime.PinnedContracts() {
		for _, tool := range ir.AllHarnessTools() {
			native := rows[toolCell{contract.Harness(), tool}]
			harness := contract.Harness()
			t.Run(string(harness)+"/"+string(tool), func(t *testing.T) {
				t.Parallel()
				lowering, err := runtime.LookupHarnessTool(harness, tool)
				require.NoError(t, err, "%s/%s must resolve a lowering", harness, tool)
				require.True(t, lowering.IsValid(), "%s/%s lowering must be either native or a degradation instruction", harness, tool)
				if native == "" {
					assert.False(t, lowering.Available(), "%s/%s must be unavailable", harness, tool)
					assert.Empty(t, lowering.NativeName(), "%s/%s must name no native tool", harness, tool)
					assert.NotEmpty(t, lowering.DegradeInstruction(), "%s/%s must explain the degradation", harness, tool)
					return
				}
				assert.True(t, lowering.Available(), "%s/%s must be available", harness, tool)
				assert.Equal(t, native, lowering.NativeName(), "%s/%s native name", harness, tool)
				assert.Empty(t, lowering.DegradeInstruction(), "%s/%s is native and carries no degradation", harness, tool)
			})
		}
	}
}
