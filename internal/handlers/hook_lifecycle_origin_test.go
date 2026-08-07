package handlers

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// TestNativeParseClosuresStampAuthenticCaptureOrigin pins the SLICE-1 L3 wiring
// of every production ingress parse site: the three frontendRegistry rows
// (Claude/OpenCode/Codex) must stamp the native sentinel (authentic-capture) on
// BOTH the delivery origin and the envelope origin they pass into the per-harness
// ingress parser, so native commits disclose their origin while remaining
// behaviorally unchanged.
//
// This test exercises the exact closure the production handler dispatches
// (frontendRegistry -> dispatchLifecycle -> parse), not a test-only export.
// EXPECted-FAIL at L2: the closures do not stamp origin until L3 wires them,
// so the delivery arrives with the empty origin and the assertion fails.
func TestNativeParseClosuresStampAuthenticCaptureOrigin(t *testing.T) {
	t.Parallel()
	for _, harness := range []ir.HarnessID{ir.HarnessClaudeCode, ir.HarnessOpenCode, ir.HarnessCodex} {
		harness := harness
		t.Run(string(harness), func(t *testing.T) {
			t.Parallel()
			dispatch, err := dispatchLifecycle(harness)
			require.NoError(t, err, "registry row must resolve for a supported harness")
			event := dispatch.manifest.Events[0]
			capture := dispatch.parse([]byte(`{}`), event, "2.1.210")
			require.Equal(t, acceptance.OriginAuthenticCapture, capture.delivery.Origin,
				"%s parse closure must stamp the native sentinel on the delivery origin", dispatch.name)
			require.Equal(t, acceptance.OriginAuthenticCapture, capture.delivery.Envelope.Origin,
				"%s parse closure must stamp the native sentinel on the envelope origin", dispatch.name)
		})
	}
}
