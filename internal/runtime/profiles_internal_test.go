package runtime

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessToolLoweringIsValidRejectsMalformedShapes proves the two documented
// lowering shapes are the only ones accepted. HarnessToolLowering's fields are
// unexported, so the malformed shapes are constructible only from this
// in-package test.
func TestHarnessToolLoweringIsValidRejectsMalformedShapes(t *testing.T) {
	t.Parallel()

	valid := []HarnessToolLowering{
		nativeHarnessTool("Read"),
		unavailableHarnessTool("the host declares no per-agent tool list"),
	}
	for _, lowering := range valid {
		assert.True(t, lowering.IsValid(), "documented lowering shape must validate")
	}

	malformed := map[string]HarnessToolLowering{
		"zero value":                    {},
		"native without availability":   {native: "Read"},
		"availability without a name":   {available: true},
		"native and degrade together":   {native: "Read", available: true, degrade: "why"},
		"native name and degrade only":  {native: "Read", degrade: "why"},
		"blank degradation instruction": {degrade: "   "},
		"blank native name":             {native: "  ", available: true},
	}
	for name, lowering := range malformed {
		assert.False(t, lowering.IsValid(), "malformed lowering %q must not validate", name)
	}
}

// TestLookupHarnessToolRejectsUnregisteredOperands proves the lookup fails
// closed on an unregistered harness and an unregistered tool rather than
// returning an empty lowering.
func TestLookupHarnessToolRejectsUnregisteredOperands(t *testing.T) {
	t.Parallel()

	_, err := LookupHarnessTool(ir.HarnessID("no-such-harness"), ir.HarnessToolRead)
	require.Error(t, err, "an unregistered harness must not resolve a tool")
	assert.Contains(t, err.Error(), "no harness-tool lowering table")

	_, err = LookupHarnessTool(ir.HarnessClaudeCode, ir.HarnessTool("shell"))
	require.Error(t, err, "an unregistered tool must not resolve a lowering")
	assert.Contains(t, err.Error(), "not registered")
}
