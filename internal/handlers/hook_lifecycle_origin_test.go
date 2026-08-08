package handlers

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// TestNativeParseClosuresPreservePreOriginPayloadShape pins the SLICE-1 native
// ZERO-diff invariant: the three frontendRegistry rows (Claude/OpenCode/Codex)
// must leave BOTH the delivery origin and the envelope origin at the zero value
// (the native sentinel default for pre-origin callers), so the committed native
// payload stays byte-identical and every frozen golden native payload pin in
// cmd/pasture keeps passing. The raw path (SLICE-2) is the only producer that
// populates the origin carriers.
//
// This test exercises the exact closure the production handler dispatches
// (frontendRegistry -> dispatchLifecycle -> parse), not a test-only export, and
// pins the serialized shape: no "origin" member may appear on the delivery raw
// JSON, matching what the committed occurrence payload service emits for the
// unset carrier (omitempty omits it).
func TestNativeParseClosuresPreservePreOriginPayloadShape(t *testing.T) {
	t.Parallel()
	for _, harness := range []ir.HarnessID{ir.HarnessClaudeCode, ir.HarnessOpenCode, ir.HarnessCodex} {
		harness := harness
		t.Run(string(harness), func(t *testing.T) {
			t.Parallel()
			dispatch, err := dispatchLifecycle(harness)
			require.NoError(t, err, "registry row must resolve for a supported harness")
			event := dispatch.manifest.Events[0]
			capture := dispatch.parse([]byte(`{}`), event, "2.1.210")
			require.Zero(t, capture.delivery.Origin,
				"%s parse closure must leave the delivery origin at the zero value (native sentinel default)", dispatch.name)
			require.Zero(t, capture.delivery.Envelope.Origin,
				"%s parse closure must leave the envelope origin at the zero value (native sentinel default)", dispatch.name)

			raw, err := json.Marshal(capture.delivery)
			require.NoError(t, err)
			decoder := json.NewDecoder(bytes.NewReader(raw))
			var members map[string]json.RawMessage
			require.NoError(t, decoder.Decode(&members))
			require.NotContains(t, members, "origin",
				"%s delivery raw JSON must stay byte-identical to the pre-origin path (no origin member)", dispatch.name)
		})
	}
}
