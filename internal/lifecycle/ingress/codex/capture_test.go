package codex_test

import (
	"os"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// Authentic Codex identity facts from the two cleared command-hook payloads.
// These are provider-specific and MUST survive ingress unflattened.
const (
	preToolSessionID = "019fc756-217c-7233-81f7-b5e979279345"
	preToolTurnID    = "019fc756-21b7-7f63-b8e2-4f4cd1ce0184"
	preToolUseID     = "exec-fe2dea40-82a3-410f-891e-a7f9e6295c6b"
	sessionStartID   = "019fc756-217c-7233-81f7-b5e979279345"
)

// TestProductionIngressPreservesExactBytesAndCodexIdentities drives both cleared
// fixtures through the PRODUCTION codex.Parse and asserts the durable delivery
// carries the exact raw stdin bytes (nothing flattened) plus the provider's
// native correlation identities. FAILS until the L3 implementation lands.
func TestProductionIngressPreservesExactBytesAndCodexIdentities(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_146_0()
	tests := []struct {
		name, file, sha256 string
		size               int
		event              model.ContractEventKind
		bindings           []model.NativeBinding
	}{
		{
			name:   "SessionStart",
			file:   "session_start_0_146_0.json",
			sha256: "69f56b0b3f98e7739828d64f1af6749931b750895eec433fa037600a623c7a04",
			size:   291,
			event:  registration.EventCodexSessionStart,
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: sessionStartID},
			},
		},
		{
			name:   "PreToolUse",
			file:   "pre_tool_use_0_146_0.json",
			sha256: "77ea0aa2a208418a2883db0cdb003e6fcf2c62856af515027dbe46270b7812e1",
			size:   507,
			event:  registration.EventCodexPreToolUse,
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: preToolSessionID},
				{Kind: model.BindingTurn, NativeName: "turn_id", Value: preToolTurnID},
				{Kind: model.BindingToolCall, NativeName: "tool_use_id", Value: preToolUseID},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile("testdata/fixtures/" + tc.file)
			require.NoError(t, err)
			require.Len(t, raw, tc.size)

			event := eventByKind(t, manifest, tc.event)
			capture := codex.Parse(raw, event, manifest.Version, model.OccurrenceEnvelopeRef{})

			require.Equal(t, model.CaptureValid, capture.Disposition, "cleared authentic payload must parse as valid")
			require.Equal(t, digest.FromBytes(raw), capture.Digest, "digest must be taken over the exact stdin bytes")
			require.Equal(t, manifest.Contract, capture.Delivery.Contract)
			require.Equal(t, tc.event, capture.Delivery.Event)
			require.Equal(t, manifest.Version, capture.Delivery.Envelope.HostVersion)
			require.Equal(t, model.CaptureValid, capture.Delivery.Capture)
			// The command-hook stdin bytes ARE the payload; nothing is unwrapped
			// or reformatted, so the durable body is byte-identical to the raw
			// capture (tool name/input, permission mode, cwd all preserved).
			require.Equal(t, raw, capture.Delivery.Body)
			require.Equal(t, tc.bindings, capture.Delivery.Bindings)
		})
	}
}

// TestCodexCatalogIsSourceDerivedAndOnlySelectedEventsAreProven mirrors the
// OpenCode guarantee: the generated Codex manifest is a source-reprofiled closed
// catalog, and ONLY the two authentically observed events (SessionStart,
// PreToolUse) carry native identities. Source-derived catalog entries must never
// imply authentic runtime proof. Passes on the L1 generated surface.
func TestCodexCatalogIsSourceDerivedAndOnlySelectedEventsAreProven(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_146_0()
	// Two roots record the host version: the runtime contract id and the host
	// contract the manifest is generated from. They are one version.
	require.Equal(t, runtime.Codex0_146_0().Versions().Min().String(), manifest.Version, "the registration manifest and the runtime contract record one host version")
	source := hostcontract.Codex0_146_0().Events
	require.NotEmpty(t, source, "the Codex host contract declares at least one event")
	require.Len(t, manifest.Events, len(source), "the generated manifest is the host contract's whole catalogue")
	proved := map[string]bool{"SessionStart": true, "PreToolUse": true}
	proofCount := 0
	for _, event := range manifest.Events {
		if proved[event.NativeName] {
			proofCount++
			require.NotEmpty(t, event.Identities, "authentic event %s must declare identities", event.NativeName)
		} else {
			require.Empty(t, event.Identities, "source-derived catalog entry %s must not imply authentic proof", event.NativeName)
		}
	}
	require.Equal(t, 2, proofCount)
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
