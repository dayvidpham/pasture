package claude_test

import (
	"encoding/json"
	"os"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

func TestAuthenticSessionStartCapturePreservesExactBytes(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/fixtures/session_start_2_1_210.json")
	require.NoError(t, err)
	event := registration.ClaudeCode2_1_210().Events[0]
	capture := claude.Parse(raw, event, "2.1.220", model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	require.Equal(t, digest.FromBytes(raw), capture.Digest)
	require.Equal(t, raw, capture.Delivery.Body)
	require.Equal(t, "2.1.220", capture.Delivery.Envelope.HostVersion)
	wire, err := json.Marshal(capture.Delivery.Envelope)
	require.NoError(t, err)
	var roundTrip model.OccurrenceEnvelopeRef
	require.NoError(t, json.Unmarshal(wire, &roundTrip))
	require.True(t, roundTrip.Runtime.Contract.IsValid())
	require.Len(t, capture.Delivery.Bindings, 1)
	raw[0] = '!'
	require.Equal(t, byte('{'), capture.Delivery.Body[0], "delivery owns a defensive byte copy")
}

func TestCaptureClassifiesBeforeExtraction(t *testing.T) {
	t.Parallel()
	event := registration.ClaudeCode2_1_210().Events[0]
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
			require.Equal(t, test.raw, got.Delivery.Body)
			require.Equal(t, digest.FromBytes(test.raw), got.Digest)
		})
	}
}
