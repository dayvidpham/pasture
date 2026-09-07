package nativeresponse_test

import (
	"errors"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	codexfrontend "github.com/dayvidpham/pasture/internal/lifecycle/frontend/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	codexingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/nativeresponse"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// TestEncodeGoldenNativeContinuationBytes pins the exact native bytes the
// per-host byte adapters emit for every
// (harness, response-validity) pair — the encoders the frontendRegistry encode
// members reference. The Codex shapes are
// derived from the pinned Codex 0.153.0 command-hook output contract:
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
// OpenCode keeps its canonical Pasture response. Claude emits no directive.
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
		{name: "claude gate proceed", harness: ir.HarnessClaudeCode, response: proceed, want: nil},
		{name: "claude observation no stdout", harness: ir.HarnessClaudeCode, response: observation, want: nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Exercise the exact per-host encoder the registry row references:
			// The production dispatch owns the harness selection; these are its
			// byte adapters, not a central native-response dispatcher.
			var (
				got []byte
				err error
			)
			switch tc.harness {
			case ir.HarnessClaudeCode:
				got, err = nativeresponse.ClaudeContinuation(tc.response)
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
	raw, err := os.ReadFile("../ingress/codex/testdata/fixtures/pre_tool_use_0_153_0.json")
	require.NoError(t, err)
	manifest := registration.Codex0_153_0()
	var event registration.Event
	for _, candidate := range manifest.Events {
		if candidate.Kind == registration.EventCodexPreToolUse {
			event = candidate
			break
		}
	}
	require.NotZero(t, event.Kind)
	capture := codexingress.Parse(raw, event, registration.Codex0_153_0().Version, model.OccurrenceEnvelopeRef{})
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
// fail-open FAULT emits, for EVERY harness the hook command dispatches on
// crossed with EVERY event class, the undeclared class included.
//
// The defect this locks out: a fault used to emit an EMPTY standard output on
// every harness. That is a proceed only where the host reads the process exit
// code. On OpenCode the pasture-generated plugin parses standard output, and an
// empty body MADE it throw INSIDE the callback chain, which stops the user's
// tool call — the exact opposite of the fail-open default. The plugin this
// build generates reports and continues instead; these bytes are still owed,
// because PASTURE CANNOT KNOW WHICH PLUGIN IS INSTALLED and an
// ALREADY-INSTALLED OLDER ONE STILL THROWS.
//
// The bytes asserted here are the SAME bytes an evaluated proceed emits, and
// that is intended: the host reads one channel, so it cannot be asked to tell
// them apart. The distinction is carried on standard error and in the durable
// fault record, which classifies the invocation as a fault.
//
// WHAT IT VISITS: the harness axis is DERIVED from the hook command's own
// registry (handlers.LifecycleHarnessCoordinates, the same source the
// built-binary proofs in cmd/pasture range over), and the class axis is derived
// from EventSemantic's own IsValid plus its zero value, which is what an
// undeclared event carries. The rows below are written by hand and PINNED to
// that product: a harness added to the registry, a class added to the enum,
// or a row deleted here fails by name until the table says what those bytes
// are. The first version was eight rows over a set nothing derived, so a
// harness added to FaultContinuation was described by nothing and a row
// deleted here stayed green.
// WHAT IT DOES NOT READ: whether a host really reads those bytes as a proceed.
// That is the question of the built-binary and Bun-plugin proofs in
// cmd/pasture.
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
			name:     "claude explicit human response takes no bytes, for the same reason as its gate",
			harness:  ir.HarnessClaudeCode,
			semantic: pastureruntime.SemanticExplicitHumanResponse,
			want:     nil,
		},
		{
			name:     "an undeclared claude event takes no bytes, which the host reads as a proceed",
			harness:  ir.HarnessClaudeCode,
			semantic: 0,
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
			name:     "codex explicit human response takes the universal continue object, as every non-observation does",
			harness:  ir.HarnessCodex,
			semantic: pastureruntime.SemanticExplicitHumanResponse,
			want:     []byte(`{"continue":true}`),
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
			name:     "an opencode observation takes no bytes, exactly as its success path does",
			harness:  ir.HarnessOpenCode,
			semantic: pastureruntime.SemanticObservation,
			want:     nil,
		},
		{
			name:     "opencode explicit human response takes the canonical proceed object, as every named callback does",
			harness:  ir.HarnessOpenCode,
			semantic: pastureruntime.SemanticExplicitHumanResponse,
			want:     []byte(`{"decision":"proceed"}`),
		},
		{
			name:     "an undeclared opencode event takes the object a named callback accepts",
			harness:  ir.HarnessOpenCode,
			semantic: 0,
			want:     []byte(`{"decision":"proceed"}`),
		},
	}

	// THE POPULATION IS DERIVED, AND THE TABLE IS HELD TO IT.
	coordinates, err := handlers.LifecycleHarnessCoordinates()
	require.NoError(t, err, "the harness set is derived from the hook command's own registry")
	classes := []pastureruntime.EventSemantic{0}
	for candidate := 1; candidate < eventSemanticScanBound; candidate++ {
		if class := pastureruntime.EventSemantic(candidate); class.IsValid() {
			classes = append(classes, class)
		}
	}
	require.Greater(t, len(classes), 1,
		"EventSemantic declares no valid class, so the product below is the undeclared class alone "+
			"and every gate and observation row would be refused as a row for no pair")
	derived := []string{}
	for _, coordinate := range coordinates {
		for _, class := range classes {
			derived = append(derived, coordinate.Harness+" / "+eventClassLabel(class))
		}
	}
	written := []string{}
	for _, test := range tests {
		written = append(written, string(test.harness)+" / "+eventClassLabel(test.semantic))
	}
	sort.Strings(derived)
	sort.Strings(written)
	require.Equal(t, derived, written,
		"the rows here must cover EXACTLY every harness the command dispatches on, crossed with "+
			"every event class and the undeclared class. A pair with no row has bytes nothing "+
			"describes, and a row for no pair describes nothing")

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

// eventSemanticScanBound is how far the class derivation above looks for
// declared members of EventSemantic, which is a uint8.
const eventSemanticScanBound = 1 << 8

// eventClassLabel names a class for the product pin, and names the zero value,
// which EventSemantic.String leaves empty because nothing declares it.
func eventClassLabel(class pastureruntime.EventSemantic) string {
	if !class.IsValid() {
		return "undeclared"
	}
	return class.String()
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
// package doc makes rather than restating it: for each pair below, the bytes a
// FAULT emits are the bytes a successful evaluation of the same event class
// emits. If the two ever diverge, a host is being handed a shape one of the two
// paths never produces.
//
// WHAT IT VISITS: the five (harness, class) pairs whose evaluated proceed this
// package can build in-process — the Codex gate and observation, the OpenCode
// gate and observation, and the Claude observation.
// WHAT IT DOES NOT READ: the Claude gate, where an evaluated proceed writes the
// canonical object and a fault writes nothing, because Claude reads the exit
// code and the two need not agree there (the built-binary proofs in cmd/pasture
// pin the Claude gate bytes); the explicit-human-response class, for which no
// evaluated proceed exists in this package; and the undeclared class, which has
// no evaluation at all. The table above pins the bytes of every one of those.
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

	openCodeObservationFault, err := nativeresponse.FaultContinuation(ir.HarnessOpenCode, pastureruntime.SemanticObservation)
	require.NoError(t, err)
	openCodeObservation, err := nativeresponse.CanonicalProceed(backend.HostResponse{})
	require.NoError(t, err)
	require.Equal(t, openCodeObservation, openCodeObservationFault.Bytes(),
		"an OpenCode observation says nothing when it succeeds, so a fault must not say more")

	claudeFault, err := nativeresponse.FaultContinuation(ir.HarnessClaudeCode, pastureruntime.SemanticObservation)
	require.NoError(t, err)
	claudeObservation, err := nativeresponse.ClaudeContinuation(backend.HostResponse{})
	require.NoError(t, err)
	require.Equal(t, claudeObservation, claudeFault.Bytes(),
		"Claude reads an empty body as a proceed, and that is what a fault emits there")
}

func TestPureEncodersHonorCapabilitiesAndPreserveObservationIdentities(t *testing.T) {
	t.Parallel()

	claude := pastureruntime.ClaudeCode2_1_261Lifecycle()
	codex := pastureruntime.Codex0_153_0Lifecycle()
	openCode := pastureruntime.OpenCode1_18_29Lifecycle()

	claudeGate, err := claude.Mapping(pastureruntime.ClaudeEventPreToolUse)
	require.NoError(t, err)
	claudeObservation, err := claude.Mapping(pastureruntime.ClaudeEventSessionStart)
	require.NoError(t, err)
	claudePostHoc, err := claude.Mapping(pastureruntime.ClaudeEventPostToolBatch)
	require.NoError(t, err)
	codexGate, err := codex.Mapping(pastureruntime.CodexEventPreToolUse)
	require.NoError(t, err)
	codexObservation, err := codex.Mapping(pastureruntime.CodexEventSessionStart)
	require.NoError(t, err)
	openCodeGate, err := openCode.Mapping(pastureruntime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	openCodeObservation, err := openCode.Mapping(pastureruntime.OpenCodeEventSessionCreated)
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		mapping pastureruntime.LifecycleEventMapping
		encode  func(pastureruntime.LifecycleEventMapping, backend.HostResponse) (hostexit.Outcome, error)
		proceed string
		blocks  bool
	}{
		{
			name: "claude gate", mapping: claudeGate,
			encode: nativeresponse.EncodeClaude, blocks: true,
		},
		{
			name: "claude observation", mapping: claudeObservation,
			encode: nativeresponse.EncodeClaude,
		},
		{
			name: "claude post-hoc", mapping: claudePostHoc,
			encode: nativeresponse.EncodeClaude,
		},
		{
			name: "codex gate", mapping: codexGate,
			encode: nativeresponse.EncodeCodex, proceed: `{"continue":true}`,
		},
		{
			name: "codex observation", mapping: codexObservation,
			encode: nativeresponse.EncodeCodex, proceed: `{}`,
		},
		{
			name: "opencode gate", mapping: openCodeGate,
			encode: nativeresponse.EncodeOpenCode, proceed: `{"decision":"proceed"}`,
		},
		{
			name: "opencode observation", mapping: openCodeObservation,
			encode: nativeresponse.EncodeOpenCode,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, kind := range []backend.DecisionKind{backend.DecisionProceed, backend.DecisionDeny, backend.DecisionRequireHuman} {
				reason := backend.ReasonNoActiveAssignment
				if kind == backend.DecisionProceed {
					reason = backend.ReasonLegal
				}
				decision, err := backend.NewDecision(kind, reason)
				require.NoError(t, err)
				response, err := backend.NewHostResponse(decision)
				require.NoError(t, err)

				out, err := tc.encode(tc.mapping, response)

				require.NoError(t, err)
				if tc.blocks && kind != backend.DecisionProceed {
					require.Equal(t, hostexit.ExitBlock, out.Exit, "RequireHuman must downgrade to Deny where Ask is absent")
					require.Empty(t, out.Stdout, "Claude denial is stderr-only")
					require.Equal(t, reason.Message(), out.Stderr)
					require.Contains(t, out.Stderr, "no active assignment")
				} else {
					require.Equal(t, hostexit.ExitContinue, out.Exit)
					require.Equal(t, tc.proceed, string(out.Stdout), "unsupported denial must preserve the proceed identity")
					require.Empty(t, out.Stderr)
				}
			}

			// Only an observation may supply an absent response.
			out, err := tc.encode(tc.mapping, backend.HostResponse{})
			if tc.mapping.Semantic() == pastureruntime.SemanticObservation {
				require.NoError(t, err)
				require.Equal(t, tc.proceed, string(out.Stdout))
			} else {
				require.Error(t, err)
				require.Equal(t, hostexit.ExitStatusUnset, out.Exit)
			}

			// An unevaluated refusal needs its own evidenced channel.
			out, err = tc.encode(tc.mapping, backend.NewEvaluationFaultResponse())
			if tc.blocks {
				require.NoError(t, err)
				require.Equal(t, hostexit.ExitBlock, out.Exit)
				require.Empty(t, out.Stdout)
				require.Contains(t, out.Stderr, "could not evaluate")
				require.Contains(t, out.Stderr, "not a policy denial")
			} else {
				var unsupported *nativeresponse.UnsupportedResponseError
				require.ErrorAs(t, err, &unsupported)
				require.Equal(t, tc.mapping.NativeName(), unsupported.Event)
				require.Equal(t, hostexit.ExitStatusUnset, out.Exit)
				require.Empty(t, out.Stdout)
			}

			_, err = tc.encode(pastureruntime.LifecycleEventMapping{}, backend.HostResponse{})
			require.Error(t, err)
		})
	}

	_, err = nativeresponse.EncodeClaude(codexGate, proceedResponse(t))
	require.ErrorContains(t, err, "does not match this encoder")
}

func TestByteOnlyAdaptersRefuseNewDenialArms(t *testing.T) {
	t.Parallel()

	for _, kind := range []backend.DecisionKind{backend.DecisionDeny, backend.DecisionRequireHuman} {
		decision, err := backend.NewDecision(kind, backend.ReasonRoleForbidsAction)
		require.NoError(t, err)
		response, err := backend.NewHostResponse(decision)
		require.NoError(t, err)

		for _, encode := range []func(backend.HostResponse) ([]byte, error){
			nativeresponse.ClaudeContinuation,
			nativeresponse.CodexContinuation,
			nativeresponse.CanonicalProceed,
		} {
			raw, err := encode(response)

			require.Error(t, err)
			require.Empty(t, raw)
		}
	}
}

func TestOnlyEffectiveFailureModeCanChooseAnExit(t *testing.T) {
	t.Parallel()

	// Evidence can exist without an effective exit channel (for example a
	// post-hoc response). Keep this distinction testable independently of
	// today's static rows, whose declarations and effective modes correlate.
	out, ok := hostexit.ForFault(hostexit.Fault{
		Mode:         pastureruntime.FailureReportAndContinue,
		DeclaredMode: pastureruntime.FailureExitTwoBlocks,
		Evidence:     pastureruntime.FailureEvidence{Source: "https://docs.claude.com/en/docs/claude-code/hooks"},
		Policy:       hostexit.FaultFailClosed,
		Stage:        hostexit.FaultStageNotRecorded,
		Continuation: hostexit.EmptyContinuation(),
		Cause:        errors.New("evaluation failed"),
	})

	require.True(t, ok)
	require.Equal(t, hostexit.ExitContinue, out.Exit, "declared mode explains, only effective mode chooses the exit")
}
