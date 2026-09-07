package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
)

// These identities predate the FileChanged member. Keep this compatibility
// baseline independent of the allocation source so a reordered source is RED.
func TestFieldAllocationPreservesExistingIDs(t *testing.T) {
	t.Parallel()
	contracts := []hostcontract.Contract{hostcontract.ClaudeCode2_1_261(), hostcontract.OpenCode1_18_29(), hostcontract.Codex0_153_0()}
	ids := renderedFieldIDs(t, renderKinds(contracts...))
	baseline := []string{
		"FieldSessionID", "FieldScratchpadDir", "FieldTranscriptPath", "FieldCWD", "FieldPermissionMode",
		"FieldHookEventName", "FieldEffort", "FieldAgentID", "FieldAgentType", "FieldSource",
		"FieldModel", "FieldSessionTitle", "FieldTrigger", "FieldReason", "FieldPrompt",
		"FieldCommandName", "FieldStopHookActive", "FieldError", "FieldErrorType", "FieldToolName",
		"FieldToolInput", "FieldToolUseID", "FieldRequestID", "FieldToolOutput", "FieldBatchResults",
		"FieldFilePath", "FieldConfigSource", "FieldMemoryType", "FieldLoadReason", "FieldGlobs",
		"FieldTriggerFilePath", "FieldParentFilePath", "FieldAgentTranscriptPath", "FieldTeammateName", "FieldTaskID",
		"FieldMessage", "FieldNotificationType", "FieldTitle", "FieldContent", "FieldFields",
		"FieldMCPServerName", "FieldResponse", "FieldPromptID", "FieldToolResponse", "FieldDurationMS",
		"FieldIsInterrupt", "FieldToolCalls", "FieldCustomInstructions", "FieldCompactSummary", "FieldMode",
		"FieldRequestedSchema", "FieldAction", "FieldOpenCodeSessionID", "FieldOpenCodeCallID", "FieldCodexSessionID",
		"FieldCodexTurnID", "FieldCodexToolUseID",
	}
	for i, symbol := range baseline {
		require.Equal(t, i+1, ids[symbol], "existing numeric identity for %s", symbol)
	}
	require.Equal(t, len(baseline)+1, ids["FieldFileEvent"])
	// FileChanged belongs to the first contract, not the final contract. Its
	// addition must nevertheless allocate after every pre-existing field.
	require.Equal(t, "FieldFileEvent", contracts[0].Fields[len(contracts[0].Fields)-1].Symbol)
	require.Equal(t, len(baseline)+2, ids["FieldCodexAgentID"])
	require.Len(t, ids, len(baseline)+2)
}

func renderedFieldIDs(t *testing.T, source []byte) map[string]int {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, 0)
	require.NoError(t, err)
	ids := map[string]int{}
	for _, decl := range f.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		first := group.Specs[0].(*ast.ValueSpec)
		if first.Names[0].Name != "FieldSessionID" {
			continue
		}
		// Assert the actual iota expression, not just the declaration positions.
		require.Len(t, first.Values, 1)
		expr := first.Values[0].(*ast.BinaryExpr)
		require.Equal(t, token.ADD, expr.Op)
		require.Equal(t, "iota", expr.X.(*ast.Ident).Name)
		require.Equal(t, "1", expr.Y.(*ast.BasicLit).Value)
		for i, spec := range group.Specs {
			value := spec.(*ast.ValueSpec)
			require.Len(t, value.Names, 1)
			if i > 0 {
				require.Empty(t, value.Values)
			}
			ids[value.Names[0].Name] = i + 1
		}
	}
	require.NotEmpty(t, ids)
	return ids
}

func TestFieldAllocationChecksContractPopulation(t *testing.T) {
	t.Parallel()
	contracts := []hostcontract.Contract{hostcontract.ClaudeCode2_1_261(), hostcontract.OpenCode1_18_29(), hostcontract.Codex0_153_0()}
	order := hostcontract.FieldAllocationOrder()
	allocated, err := allocatedFieldSymbols(contracts, order)
	require.NoError(t, err)
	require.Equal(t, order, allocated)
	// Add to the earliest contract but allocate at the end. All prior IDs stay
	// at the same position; no test list enumerates this new field in advance.
	contracts[0].Fields = append(contracts[0].Fields, hostcontract.Field{Symbol: "FieldAdditional"})
	_, err = allocatedFieldSymbols(contracts, order)
	require.ErrorContains(t, err, "missing allocation for field symbol \"FieldAdditional\"")
	extended := append(append([]string(nil), order...), "FieldAdditional")
	allocated, err = allocatedFieldSymbols(contracts, extended)
	require.NoError(t, err)
	require.Equal(t, order, allocated[:len(order)])
	require.Equal(t, "FieldAdditional", allocated[len(order)])
	_, err = allocatedFieldSymbols(contracts, append(extended, "FieldAdditional"))
	require.ErrorContains(t, err, "duplicate allocation")
	_, err = allocatedFieldSymbols(contracts, append(extended, "FieldNotDeclared"))
	require.ErrorContains(t, err, "unknown")
	contracts[1].Fields = append(contracts[1].Fields, hostcontract.Field{Symbol: "FieldAdditional"})
	_, err = allocatedFieldSymbols(contracts, extended)
	require.ErrorContains(t, err, "duplicate contract")
}
