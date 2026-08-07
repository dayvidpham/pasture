package nativeresponse_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	codexfrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/codex"
	codexingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// TestEncodeGoldenNativeContinuationBytes pins the exact native bytes the
// per-host encoders (CodexContinuation, CanonicalProceed) emit for every
// (harness, response-validity) pair — the encoders the frontendRegistry encode
// members reference. The Codex shapes are
// derived from the pinned Codex 0.146.0 command-hook output contract:
//
//   - {"continue":true} — hooks/src/schema.rs HookUniversalOutputWire.continue
//     is the sole universal directive a blocking PreToolUse gate needs to
//     proceed; output_parser.rs rejects continue==false, and a bare
//     {"continue":true} carries no block decision and no updatedInput, so the
//     host proceeds with the tool call unchanged.
//   - {} — the native default continuation object for a non-blocking
//     observation (SessionStart): every universal field defaults
//     (default_continue=true) and an observation adds no hookSpecificOutput.
//
// The OpenCode and Claude shapes are the canonical Pasture host response and
// MUST remain byte-identical to M2 (regression guard).
//
// FAILS until the L3 encoder bodies land.
func TestEncodeGoldenNativeContinuationBytes(t *testing.T) {
	t.Parallel()
	proceed := proceedResponse(t)
	observation := backend.HostResponse{} // invalid zero response: no decision produced

	cases := []struct {
		name     string
		harness  ir.HarnessID
		response backend.HostResponse
		want     []byte
	}{
		{name: "codex gate proceed", harness: ir.HarnessCodex, response: proceed, want: []byte(`{"continue":true}`)},
		{name: "codex observation default", harness: ir.HarnessCodex, response: observation, want: []byte(`{}`)},
		{name: "opencode gate proceed", harness: ir.HarnessOpenCode, response: proceed, want: []byte(`{"decision":"proceed"}`)},
		{name: "opencode observation no stdout", harness: ir.HarnessOpenCode, response: observation, want: nil},
		{name: "claude gate proceed", harness: ir.HarnessClaudeCode, response: proceed, want: []byte(`{"decision":"proceed"}`)},
		{name: "claude observation no stdout", harness: ir.HarnessClaudeCode, response: observation, want: nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Exercise the exact per-host encoder the registry row references:
			// Codex routes through CodexContinuation, and the canonical-object
			// harnesses (Claude, OpenCode) through CanonicalProceed. The former
			// nativeresponse.Encode(harness, response) switch is deleted; the
			// unknown-harness negative it hosted is relocated to the registry
			// lookup (handlers.dispatchLifecycle + HookLifecycleNative).
			var (
				got []byte
				err error
			)
			switch tc.harness {
			case ir.HarnessCodex:
				got, err = nativeresponse.CodexContinuation(tc.response)
			default:
				got, err = nativeresponse.CanonicalProceed(tc.response)
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got, "native continuation bytes must equal the pinned golden shape")
		})
	}
}

// proceedResponse derives a genuine constructor-built Proceed HostResponse by
// running the authentic Codex PreToolUse fixture through the production ingress,
// frontend, and middle-end. This keeps the golden encoder input on the real
// production value rather than a hand-built response.
func proceedResponse(t *testing.T) backend.HostResponse {
	t.Helper()
	raw, err := os.ReadFile("../ingress/codex/testdata/fixtures/pre_tool_use_0_146_0.json")
	require.NoError(t, err)
	manifest := registration.Codex0_146_0()
	var event registration.Event
	for _, candidate := range manifest.Events {
		if candidate.Kind == registration.EventCodexPreToolUse {
			event = candidate
			break
		}
	}
	require.NotZero(t, event.Kind)
	capture := codexingress.Parse(raw, event, "0.146.0", model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	l1, identities, err := codexfrontend.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
	require.NoError(t, err)
	l2, err := l1.NewEvent(identities)
	require.NoError(t, err)
	derivation, err := middleend.Derive(l2, metamodel.Active())
	require.NoError(t, err)
	response := derivation.Response()
	require.True(t, response.IsValid(), "the PreToolUse gate must derive a valid Proceed response")
	return response
}
