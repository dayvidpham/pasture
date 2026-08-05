package codex_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/frontend/codex"
	codexingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// Authentic Codex identity facts from the two cleared command-hook payloads.
const (
	preToolSessionID = "019fc756-217c-7233-81f7-b5e979279345"
	preToolTurnID    = "019fc756-21b7-7f63-b8e2-4f4cd1ce0184"
	preToolUseID     = "exec-fe2dea40-82a3-410f-891e-a7f9e6295c6b"
	sessionStartID   = "019fc756-217c-7233-81f7-b5e979279345"
)

// TestAuthenticCodexPayloadsProduceProviderCorrectVerifiedL2 drives both cleared
// fixtures through the PRODUCTION ingress and frontend to verified waist L2. It
// asserts Codex-specific semantics and identity facts:
//   - SessionStart is an observation smoke event carrying session_id only. It is
//     NEVER treated as semantically identical to OpenCode session.created.
//   - PreToolUse is a blocking gate carrying session_id, turn_id, and
//     tool_use_id — the full Codex correlation triple.
//
// FAILS until the L3 frontend implementation lands.
func TestAuthenticCodexPayloadsProduceProviderCorrectVerifiedL2(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_146_0()
	tests := []struct {
		name, file, nativeName string
		kind                   model.ContractEventKind
		semantic               runtime.EventSemantic
		blocking               runtime.BlockingMode
		identities             []waist.SemanticIdentity
	}{
		{
			name:       "SessionStart observation smoke",
			file:       "session_start_0_146_0.json",
			nativeName: "SessionStart",
			kind:       registration.EventCodexSessionStart,
			semantic:   runtime.SemanticObservation,
			blocking:   runtime.NonBlocking,
			identities: []waist.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: sessionStartID},
			},
		},
		{
			name:       "PreToolUse gate",
			file:       "pre_tool_use_0_146_0.json",
			nativeName: "PreToolUse",
			kind:       registration.EventCodexPreToolUse,
			semantic:   runtime.SemanticGateConsultation,
			blocking:   runtime.Blocking,
			// Sorted by (Kind, Value): session(1), turn(2), toolCall(4).
			identities: []waist.SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: preToolSessionID},
				{Kind: runtime.IdentityTurn, Value: preToolTurnID},
				{Kind: runtime.IdentityToolCall, Value: preToolUseID},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile("../../ingress/codex/testdata/fixtures/" + tc.file)
			require.NoError(t, err)

			event := eventByKind(t, manifest, tc.kind)
			capture := codexingress.Parse(raw, event, "0.146.0", model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)

			l1, identities, err := codex.Bind(capture.Delivery.Event, capture.Delivery.Bindings)
			require.NoError(t, err)
			require.True(t, l1.IsValid())

			l2, err := l1.NewEvent(identities)
			require.NoError(t, err)
			require.True(t, l2.IsValid())

			require.Equal(t, tc.semantic, l2.Semantics().Semantic())
			require.Equal(t, tc.blocking, l2.Semantics().Blocking())
			require.Equal(t, tc.identities, l2.Semantics().Identities())

			require.Equal(t, ir.HarnessCodex, l2.Origin().Harness())
			require.Equal(t, waist.NativeEventName(tc.nativeName), l2.Origin().NativeEventName())
			// IP-1-SWAP (M3-WAVE-1 consolidation): SLICE-1 replaces the runtime
			// Codex profile with runtime.Codex0_146_0Lifecycle() (codex@0.146.0).
			// This line and the frontend seam swap together at consolidation; the
			// selected events are version-stable so only the origin version
			// coordinate changes.
			require.Equal(t, runtime.Codex0_144_1Lifecycle().ID(), l2.Origin().Contract())
		})
	}
}

func eventByKind(t *testing.T, manifest registration.Manifest, kind model.ContractEventKind) registration.Event {
	t.Helper()
	for _, event := range manifest.Events {
		if event.Kind == kind {
			return event
		}
	}
	t.Fatalf("generated Codex manifest does not contain event kind %d", kind)
	return registration.Event{}
}
