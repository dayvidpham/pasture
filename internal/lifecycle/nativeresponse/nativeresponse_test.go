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
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
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

// TestFaultContinuationIsTheHostProceedForEveryHarness pins the bytes a
// fail-open FAULT emits.
//
// The defect this locks out: a fault used to emit an EMPTY standard output on
// every harness. That is a proceed only where the host reads the process exit
// code. On OpenCode the pasture-generated plugin parses standard output, and an
// empty body makes it throw INSIDE the callback chain, which stops the user's
// tool call — the exact opposite of the fail-open default.
//
// The bytes asserted here are the SAME bytes an evaluated proceed emits, and
// that is intended: the host reads one channel, so it cannot be asked to tell
// them apart. The distinction is carried on standard error and in the durable
// fault record, which classifies the invocation as a fault.
func TestFaultContinuationIsTheHostProceedForEveryHarness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		harness  ir.HarnessID
		semantic pastureruntime.EventSemantic
		want     []byte
	}{
		{
			name:     "claude gate takes no bytes, because the host reads the exit code",
			harness:  ir.HarnessClaudeCode,
			semantic: pastureruntime.SemanticGateConsultation,
			want:     nil,
		},
		{
			name:     "claude observation takes no bytes either",
			harness:  ir.HarnessClaudeCode,
			semantic: pastureruntime.SemanticObservation,
			want:     nil,
		},
		{
			name:     "codex gate takes the universal continue object, which a blocking gate requires",
			harness:  ir.HarnessCodex,
			semantic: pastureruntime.SemanticGateConsultation,
			want:     []byte(`{"continue":true}`),
		},
		{
			name:     "codex observation takes the default object",
			harness:  ir.HarnessCodex,
			semantic: pastureruntime.SemanticObservation,
			want:     []byte(`{}`),
		},
		{
			name:     "an undeclared codex event takes the universally accepted object",
			harness:  ir.HarnessCodex,
			semantic: 0,
			want:     []byte(`{"continue":true}`),
		},
		{
			name:     "opencode gate takes the canonical proceed object the generated plugin validates",
			harness:  ir.HarnessOpenCode,
			semantic: pastureruntime.SemanticGateConsultation,
			want:     []byte(`{"decision":"proceed"}`),
		},
		{
			name:     "an opencode observation takes the same object, which its callbacks ignore",
			harness:  ir.HarnessOpenCode,
			semantic: pastureruntime.SemanticObservation,
			want:     []byte(`{"decision":"proceed"}`),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			continuation, err := nativeresponse.FaultContinuation(test.harness, test.semantic)
			require.NoError(t, err)
			require.True(t, continuation.IsSet(),
				"a supported harness always has a continuation, even when it is empty")
			require.Equal(t, test.want, continuation.Bytes())
		})
	}
}

// TestFaultContinuationRefusesAnUnsupportedHarness pins the refusal. A caller
// that cannot name the host has no bytes to write, so it must be told that
// rather than handed a guessed shape the host would reject.
func TestFaultContinuationRefusesAnUnsupportedHarness(t *testing.T) {
	t.Parallel()

	continuation, err := nativeresponse.FaultContinuation(ir.HarnessID("gemini"), pastureruntime.SemanticGateConsultation)
	require.Error(t, err)
	require.False(t, continuation.IsSet(),
		"a refused mapping must not hand back a usable continuation")
	for _, part := range []string{"gemini", "no fail-open continuation", "empty continuation", "generated lifecycle support report"} {
		require.Contains(t, err.Error(), part,
			"the refusal must say what went wrong, what it means and how to fix it")
	}
}

// TestTheFaultContinuationMatchesTheEvaluatedProceed proves the claim the
// package doc makes rather than restating it: for each harness, the bytes a
// FAULT emits are the bytes a successful evaluation of the same event class
// emits. If the two ever diverge, a host is being handed a shape one of the two
// paths never produces.
func TestTheFaultContinuationMatchesTheEvaluatedProceed(t *testing.T) {
	t.Parallel()

	gateProceed := proceedResponse(t)

	codexGate, err := nativeresponse.CodexContinuation(gateProceed)
	require.NoError(t, err)
	codexGateFault, err := nativeresponse.FaultContinuation(ir.HarnessCodex, pastureruntime.SemanticGateConsultation)
	require.NoError(t, err)
	require.Equal(t, codexGate, codexGateFault.Bytes(),
		"the Codex gate fault continuation must be the Codex gate proceed")

	codexObservation, err := nativeresponse.CodexContinuation(backend.HostResponse{})
	require.NoError(t, err)
	codexObservationFault, err := nativeresponse.FaultContinuation(ir.HarnessCodex, pastureruntime.SemanticObservation)
	require.NoError(t, err)
	require.Equal(t, codexObservation, codexObservationFault.Bytes(),
		"the Codex observation fault continuation must be the Codex observation default")

	canonical, err := nativeresponse.CanonicalProceed(gateProceed)
	require.NoError(t, err)
	openCodeFault, err := nativeresponse.FaultContinuation(ir.HarnessOpenCode, pastureruntime.SemanticGateConsultation)
	require.NoError(t, err)
	require.Equal(t, canonical, openCodeFault.Bytes(),
		"the OpenCode fault continuation must be the canonical proceed the generated plugin validates")

	claudeFault, err := nativeresponse.FaultContinuation(ir.HarnessClaudeCode, pastureruntime.SemanticObservation)
	require.NoError(t, err)
	claudeObservation, err := nativeresponse.CanonicalProceed(backend.HostResponse{})
	require.NoError(t, err)
	require.Equal(t, claudeObservation, claudeFault.Bytes(),
		"Claude reads an empty body as a proceed, and that is what a fault emits there")
}
