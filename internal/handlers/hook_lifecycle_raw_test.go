package handlers

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// TestRawSchemaVersionConstantsPinToGeneratedRegistrations pins the
// RawSchemaVersion constants to the generated per-harness registrations via
// the derived rawSchemaVersionFor, so the doc claim "the closed set is pinned
// by tests against the generated registrations" is true (review MINOR-2): a
// build cannot advertise a wire identity its own registrations do not decode.
func TestRawSchemaVersionConstantsPinToGeneratedRegistrations(t *testing.T) {
	t.Parallel()

	require.Equal(t, string(RawSchemaClaudeCode2_1_261), string(rawSchemaVersionFor(ir.HarnessClaudeCode)),
		"the Claude wire identity must equal the contract derived from the generated 2.1.261 registration")
	require.Equal(t, string(RawSchemaOpenCode1_18_29), string(rawSchemaVersionFor(ir.HarnessOpenCode)),
		"the OpenCode wire identity must equal the contract derived from the generated 1.18.29 registration")
	require.Equal(t, string(RawSchemaCodex0_153_0), string(rawSchemaVersionFor(ir.HarnessCodex)),
		"the Codex wire identity must equal the contract derived from the generated 0.153.0 registration")
}
