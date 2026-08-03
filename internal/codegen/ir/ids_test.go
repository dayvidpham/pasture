package ir_test

import (
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnabledHarnessIDsIsDefensiveAndExact(t *testing.T) {
	t.Parallel()

	first := ir.EnabledHarnessIDs()
	require.NotEmpty(t, first)
	first[0] = ir.HarnessID("forged")

	second := ir.EnabledHarnessIDs()
	assert.NotEqual(t, ir.HarnessID("forged"), second[0], "EnabledHarnessIDs must return a fresh defensive copy on every call")
	assert.ElementsMatch(t, []ir.HarnessID{ir.HarnessClaudeCode, ir.HarnessOpenCode, ir.HarnessCodex}, second)
	for _, harness := range second {
		assert.True(t, harness.IsValid())
	}
}

func TestRuntimeContractIDIsOpaqueAndHarnessBound(t *testing.T) {
	t.Parallel()

	contract, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "2.1.210")
	require.NoError(t, err)
	assert.Equal(t, ir.HarnessClaudeCode, contract.Harness())
	assert.Equal(t, "claude-code/2.1.210", contract.String())
	assert.True(t, contract.IsValid())
	assert.False(t, (ir.RuntimeContractID{}).IsValid(), "the zero value must not be a valid contract")

	// Re-supplying the already-prefixed string round-trips to an equal value
	// bound to the same harness, not a doubled prefix.
	roundTripped, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, contract.String())
	require.NoError(t, err)
	assert.Equal(t, contract, roundTripped)
}

func TestRuntimeContractIDConstructorRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	t.Run("unknown harness", func(t *testing.T) {
		t.Parallel()
		_, err := ir.NewRuntimeContractID(ir.HarnessID("unknown-harness"), "1.0.0")
		require.Error(t, err)
	})

	t.Run("invalid UTF-8 name", func(t *testing.T) {
		t.Parallel()
		_, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, string([]byte{0xff, 0xfe}))
		require.Error(t, err)
	})

	t.Run("empty name", func(t *testing.T) {
		t.Parallel()
		_, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "")
		require.Error(t, err)
	})

	t.Run("padded name", func(t *testing.T) {
		t.Parallel()
		_, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, " 2.1.210 ")
		require.Error(t, err)
	})

	t.Run("control character in name", func(t *testing.T) {
		t.Parallel()
		_, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "2.1.210\n")
		require.Error(t, err)
	})

	t.Run("empty suffix after harness prefix", func(t *testing.T) {
		t.Parallel()
		// The prefix alone, with nothing naming a specific version-bound
		// profile, must be rejected — a prior revision's own parser
		// (runtimeContractHarness) rejected exactly this value even though
		// NewRuntimeContractID had already constructed it.
		_, err := ir.NewRuntimeContractID(ir.HarnessClaudeCode, "claude-code/")
		require.Error(t, err)
	})

	for _, name := range []string{"", " ", "\t", "\n"} {
		name := name
		t.Run("whitespace-only name "+name, func(t *testing.T) {
			t.Parallel()
			_, err := ir.NewRuntimeContractID(ir.HarnessOpenCode, name)
			require.Error(t, err)
		})
	}
}

func TestRuntimeContractIDJSONRoundTripAndForgedValues(t *testing.T) {
	t.Parallel()

	contract, err := ir.NewRuntimeContractID(ir.HarnessCodex, "0.144.1")
	require.NoError(t, err)
	encoded, err := json.Marshal(contract)
	require.NoError(t, err)
	assert.JSONEq(t, `"codex/0.144.1"`, string(encoded))

	var decoded ir.RuntimeContractID
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, contract, decoded)
	assert.Equal(t, ir.HarnessCodex, decoded.Harness())

	for name, forged := range map[string]string{
		"unknown harness prefix": `"unknown-harness/1.0.0"`,
		"empty suffix":           `"codex/"`,
		"no prefix at all":       `"1.0.0"`,
		"not a string":           `17`,
		"null":                   `null`,
		"internal whitespace":    `"codex/ 1.0.0"`,
		"leading whitespace":     `" codex/1.0.0"`,
		"trailing whitespace":    `"codex/1.0.0 "`,
		"wrong harness spelling": `"Codex/1.0.0"`,
	} {
		name, forged := name, forged
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var target ir.RuntimeContractID
			assert.Error(t, json.Unmarshal([]byte(forged), &target), "forged runtime_contract %q must not decode", forged)
		})
	}
}

func TestRuntimeContractIDZeroValueIsRejectedByMarshal(t *testing.T) {
	t.Parallel()
	_, err := json.Marshal(ir.RuntimeContractID{})
	require.Error(t, err)
}
