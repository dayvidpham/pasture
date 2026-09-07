package codex_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/codex"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/runtime"
)

const codexFixtureDir = "testdata/fixtures"

func clearedCodexBodies(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(codexFixtureDir, "*.provenance.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	bodies := map[string][]byte{}
	for _, path := range paths {
		wire, err := os.ReadFile(path)
		require.NoError(t, err)
		var sidecar acceptance.CaptureProvenance
		require.NoError(t, json.Unmarshal(wire, &sidecar))
		fixture := strings.TrimSuffix(path, ".provenance.json") + ".json"
		raw, err := os.ReadFile(fixture)
		require.NoError(t, err)
		require.NoError(t, sidecar.ValidateCommittedFixtureBytes(fixture, raw))
		require.Equal(t, acceptance.HarnessCodexCLI, sidecar.Harness)
		require.Equal(t, registration.Codex0_153_0().Version, sidecar.HarnessVersion)
		require.NotContains(t, bodies, sidecar.Event)
		bodies[sidecar.Event] = raw
	}
	return bodies
}

// Read expected values from the captured body, independently of the catalogue
// declarations under test. Only these native correlation fields enter the IR.
func capturedCodexBindings(t *testing.T, raw []byte) []model.NativeBinding {
	t.Helper()
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &payload))
	var bindings []model.NativeBinding
	for _, field := range []struct {
		name string
		kind model.NativeBindingKind
	}{
		{"session_id", model.BindingSession},
		{"turn_id", model.BindingTurn},
		{"tool_use_id", model.BindingToolCall},
		{"agent_id", model.BindingAgent},
	} {
		if wire, present := payload[field.name]; present {
			var value string
			require.NoError(t, json.Unmarshal(wire, &value))
			bindings = append(bindings, model.NativeBinding{Kind: field.kind, NativeName: field.name, Value: value})
		}
	}
	return bindings
}

func TestProductionIngressPreservesExactBytesAndCodexIdentities(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_153_0()
	bodies := clearedCodexBodies(t)
	require.Len(t, bodies, len(manifest.Entries()))
	for _, event := range manifest.Entries() {
		t.Run(event.NativeName, func(t *testing.T) {
			raw, present := bodies[event.NativeName]
			require.True(t, present)
			// An observed version remains provenance, not a substituted pin or a
			// new live admission rule.
			observedVersion := "0.153.1"
			capture := codex.Parse(raw, event, observedVersion, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureValid, capture.Disposition)
			require.Equal(t, model.CaptureValid, capture.Delivery.Capture)
			require.Equal(t, digest.FromBytes(raw), capture.Digest)
			require.Equal(t, raw, capture.Delivery.Body)
			require.Equal(t, manifest.Contract, capture.Delivery.Contract)
			require.Equal(t, manifest.Contract, capture.Delivery.Envelope.Runtime.Contract)
			require.Equal(t, observedVersion, capture.Delivery.Envelope.HostVersion)
			require.Equal(t, event.Kind, capture.Delivery.Event)
			require.Equal(t, capturedCodexBindings(t, raw), capture.Delivery.Bindings)
		})
	}
}

func TestCodexCatalogIdentitiesMatchClearedCaptures(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_153_0()
	require.Equal(t, runtime.Codex0_153_0().Versions().Min().String(), manifest.Version)
	source := hostcontract.Codex0_153_0()
	require.Len(t, manifest.Events, len(source.Events))
	bodies := clearedCodexBodies(t)
	fields := map[model.NativeFieldID]string{
		registration.FieldCodexSessionID: "session_id",
		registration.FieldCodexTurnID:    "turn_id",
		registration.FieldCodexToolUseID: "tool_use_id",
		registration.FieldCodexAgentID:   "agent_id",
	}
	for _, event := range manifest.Entries() {
		t.Run(event.NativeName, func(t *testing.T) {
			want := capturedCodexBindings(t, bodies[event.NativeName])
			require.Len(t, event.Identities, len(want))
			for i, identity := range event.Identities {
				require.True(t, identity.Required)
				require.Equal(t, want[i].Kind, identity.Binding)
				require.Equal(t, want[i].NativeName, fields[identity.Field])
			}
		})
	}
}

func TestCodexParserRejectsMissingAndMalformedRequiredIdentities(t *testing.T) {
	t.Parallel()
	bodies := clearedCodexBodies(t)
	for _, event := range registration.Codex0_153_0().Entries() {
		t.Run(event.NativeName, func(t *testing.T) {
			raw := bodies[event.NativeName]
			for _, binding := range capturedCodexBindings(t, raw) {
				for _, mutation := range []struct {
					name   string
					value  any
					remove bool
					want   model.CaptureDisposition
				}{
					{name: "missing", remove: true, want: model.CaptureUnsupportedSchema},
					{name: "null", value: nil, want: model.CaptureUnsupportedSchema},
					{name: "empty", value: "", want: model.CaptureUnsupportedSchema},
					{name: "number", value: 1, want: model.CaptureMalformed},
					{name: "padding", value: " id ", want: model.CaptureUnsupportedSchema},
					{name: "control", value: "a\x00b", want: model.CaptureUnsupportedSchema},
					{name: "oversized", value: strings.Repeat("x", 513), want: model.CaptureUnsupportedSchema},
				} {
					t.Run(binding.NativeName+"/"+mutation.name, func(t *testing.T) {
						var payload map[string]any
						require.NoError(t, json.Unmarshal(raw, &payload))
						if mutation.remove {
							delete(payload, binding.NativeName)
						} else {
							payload[binding.NativeName] = mutation.value
						}
						wire, err := json.Marshal(payload)
						require.NoError(t, err)
						capture := codex.Parse(wire, event, "0.153.0", model.OccurrenceEnvelopeRef{})
						require.Equal(t, mutation.want, capture.Disposition)
						require.Empty(t, capture.Delivery.Bindings, "invalid input must not leave partial identities")
						require.Equal(t, wire, capture.Delivery.Body)
						require.Equal(t, digest.FromBytes(wire), capture.Digest)
					})
				}
			}
		})
	}
}

func TestCodexParserUsesPinnedEventAndRequiresMatchingPayloadEvent(t *testing.T) {
	t.Parallel()
	manifest := registration.Codex0_153_0()
	event, err := ingress.EventByNativeName(manifest, "SessionStart")
	require.NoError(t, err)
	raw := clearedCodexBodies(t)[event.NativeName]
	for name, selected := range map[string]registration.Event{
		"unknown":    {Kind: event.Kind, NativeName: "NotRegistered"},
		"wrong-kind": {Kind: registration.EventCodexStop, NativeName: event.NativeName},
		"foreign":    registration.OpenCode1_18_29().Entries()[0],
	} {
		t.Run(name, func(t *testing.T) {
			capture := codex.Parse(raw, selected, manifest.Version, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureUnsupportedSchema, capture.Disposition)
			require.Empty(t, capture.Delivery.Bindings)
			require.Equal(t, raw, capture.Delivery.Body)
		})
	}
	// Caller-supplied metadata cannot weaken required pinned identities.
	selected := registration.Event{Kind: event.Kind, NativeName: event.NativeName}
	capture := codex.Parse(raw, selected, manifest.Version, model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	require.Equal(t, capturedCodexBindings(t, raw), capture.Delivery.Bindings)
	for _, name := range []string{"", "Stop"} {
		t.Run("payload-event/"+name, func(t *testing.T) {
			var payload map[string]any
			require.NoError(t, json.Unmarshal(raw, &payload))
			payload["hook_event_name"] = name
			wire, err := json.Marshal(payload)
			require.NoError(t, err)
			capture := codex.Parse(wire, event, manifest.Version, model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureUnsupportedSchema, capture.Disposition)
			require.Empty(t, capture.Delivery.Bindings)
		})
	}
}

func TestClearedCodexCapturesAreAdmittedAndCarryNoUnsubstitutedFreeText(t *testing.T) {
	t.Parallel()
	for event, raw := range clearedCodexBodies(t) {
		t.Run(event, func(t *testing.T) {
			require.Equal(t, model.CaptureValid, ingress.Validate(raw).Disposition)
			refusals, err := ingress.RefusedFields(raw)
			require.NoError(t, err)
			require.Empty(t, refusals)
			substituted, _, err := ingress.SubstituteFreeText(raw)
			require.NoError(t, err)
			require.Equal(t, raw, substituted, "cleared input must be a fixed point of free-text substitution")
		})
	}
}
