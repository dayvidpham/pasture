package claude_test

import (
	"encoding/json"
	"os"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/acceptance"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// authenticSessionStart reads the committed SessionStart fixture with its
// sidecar; the digest and the session id come from the corpus, never a literal.
func authenticSessionStart(t *testing.T) (raw []byte, sidecar acceptance.CaptureProvenance, sessionID string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/fixtures/session_start_2_1_261.json")
	require.NoError(t, err)
	sidecarBytes, err := os.ReadFile("testdata/fixtures/session_start_2_1_261.provenance.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(sidecarBytes, &sidecar))
	var payload struct {
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.NotEmpty(t, payload.SessionID)
	return raw, sidecar, payload.SessionID
}

func TestAuthenticSessionStartCapturePreservesExactBytes(t *testing.T) {
	t.Parallel()
	raw, sidecar, sessionID := authenticSessionStart(t)
	manifest := registration.ClaudeCode2_1_261()
	event := manifest.Events[0]
	capture := claude.Parse(raw, event, manifest.Version, model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	require.Equal(t, sidecar.RawFileDigest, digest.FromBytes(raw).String(), "the committed bytes are the cleared bytes the sidecar records")
	require.Equal(t, digest.FromBytes(raw), capture.Digest)
	require.Equal(t, raw, capture.Delivery.Body)
	require.Equal(t, manifest.Version, capture.Delivery.Envelope.HostVersion)
	wire, err := json.Marshal(capture.Delivery.Envelope)
	require.NoError(t, err)
	var roundTrip model.OccurrenceEnvelopeRef
	require.NoError(t, json.Unmarshal(wire, &roundTrip))
	require.True(t, roundTrip.Runtime.Contract.IsValid())
	require.Len(t, capture.Delivery.Bindings, 1)
	require.Equal(t, "session_id", capture.Delivery.Bindings[0].NativeName)
	require.Equal(t, sessionID, capture.Delivery.Bindings[0].Value)
	raw[0] = '!'
	require.Equal(t, byte('{'), capture.Delivery.Body[0], "delivery owns a defensive byte copy")
}

func TestCaptureClassifiesBeforeExtraction(t *testing.T) {
	t.Parallel()
	event := registration.ClaudeCode2_1_261().Events[0]
	tests := []struct {
		name string
		raw  []byte
		want model.CaptureDisposition
	}{
		{"malformed", []byte(`{"hook_event_name":`), model.CaptureMalformed},
		{"duplicate", []byte(`{"hook_event_name":"SessionStart","hook_event_name":"SessionStart"}`), model.CaptureDuplicateField},
		{"invalid utf8", []byte{0xff}, model.CaptureInvalidUTF8},
		{"event mismatch", []byte(`{"session_id":"s","hook_event_name":"SessionEnd"}`), model.CaptureEventMismatch},
		{"event-specific unknown", []byte(`{"session_id":"s","hook_event_name":"SessionStart","tool_input":{}}`), model.CaptureUnsupportedSchema},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := claude.Parse(test.raw, event, "9.9.9", model.OccurrenceEnvelopeRef{})
			require.Equal(t, test.want, got.Disposition)
			require.Equal(t, test.want, got.Delivery.Capture)
			require.Equal(t, test.raw, got.Delivery.Body)
			require.Equal(t, digest.FromBytes(test.raw), got.Digest)
			require.Empty(t, got.Delivery.Bindings, "invalid captures must retain zero native bindings")
		})
	}
}

func TestCaptureDropsBindingsWhenLaterRequiredIdentityFails(t *testing.T) {
	t.Parallel()
	var event registration.Event
	for _, candidate := range registration.ClaudeCode2_1_261().Events {
		if candidate.Kind == registration.EventPreToolUse {
			event = candidate
			break
		}
	}
	require.Equal(t, registration.EventPreToolUse, event.Kind)

	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "missing later required identity", raw: []byte(`{"session_id":"s","hook_event_name":"PreToolUse"}`)},
		{name: "invalid later required identity", raw: []byte(`{"session_id":"s","hook_event_name":"PreToolUse","tool_use_id":""}`)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := claude.Parse(test.raw, event, "2.1.220", model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureUnsupportedSchema, got.Disposition)
			require.Equal(t, model.CaptureUnsupportedSchema, got.Delivery.Capture)
			require.Empty(t, got.Delivery.Bindings, "later required identity failure must discard earlier bindings")
		})
	}
}
