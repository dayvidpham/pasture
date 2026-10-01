package opencode_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	opencodeingress "github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// Constructed payloads below exercise the 2.0.20 admission shapes, not
// authentic host captures. They mirror the hook inputs the 2.0.20 plugin
// forwards verbatim: flat objects with a top-level sessionID, the call id on
// both tool hooks, and the bus type on session.created.

func v2Event(t *testing.T, name string) registration.Event {
	t.Helper()
	for _, event := range registration.OpenCode2_0_20().Entries() {
		if event.NativeName == name {
			return event
		}
	}
	t.Fatalf("no 2.0.20 registration event %q", name)
	return registration.Event{}
}

func TestParseV2SessionCreatedBindsDataSession(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"evt_constructed","created":1725549600,"type":"session.created","data":{"sessionID":"ses_constructed","projectID":"global","version":"2.0.20"}}`)
	capture := opencodeingress.ParseV2(raw, v2Event(t, "session.created"), "2.0.20", model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	require.NoError(t, capture.Cause.Check(capture.Disposition))
	require.Equal(t, []model.NativeBinding{
		{Kind: model.BindingSession, NativeName: "sessionID", Value: "ses_constructed"},
	}, capture.Delivery.Bindings)
	require.Equal(t, registration.OpenCode2_0_20().Contract, capture.Delivery.Contract)
}

func TestParseV2ToolHooksBindSessionPlusCallID(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"tool.execute.before", "tool.execute.after"} {
		raw := []byte(`{"tool":"task","sessionID":"ses_constructed","agent":"agent","messageID":"message","id":"call_constructed","input":{"path":"x"}}`)
		capture := opencodeingress.ParseV2(raw, v2Event(t, name), "2.0.20", model.OccurrenceEnvelopeRef{})
		require.Equal(t, model.CaptureValid, capture.Disposition, name)
		require.NoError(t, capture.Cause.Check(capture.Disposition), name)
		require.Equal(t, []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "sessionID", Value: "ses_constructed"},
			{Kind: model.BindingToolCall, NativeName: "id", Value: "call_constructed"},
		}, capture.Delivery.Bindings, name)
	}
}

func TestParseV2SessionHooksBindSession(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"session.prompt", "session.context", "session.compaction", "session.generate",
		"session.title", "session.model.request", "session.http.request", "session.http.response",
		"session.experimental.ws.handshake", "session.experimental.ws.send", "session.experimental.ws.receive",
		"session.retry", "permission.evaluate",
	} {
		raw := []byte(`{"sessionID":"ses_constructed"}`)
		capture := opencodeingress.ParseV2(raw, v2Event(t, name), "2.0.20", model.OccurrenceEnvelopeRef{})
		require.Equal(t, model.CaptureValid, capture.Disposition, name)
		require.NoError(t, capture.Cause.Check(capture.Disposition), name)
		require.Equal(t, []model.NativeBinding{
			{Kind: model.BindingSession, NativeName: "sessionID", Value: "ses_constructed"},
		}, capture.Delivery.Bindings, name)
	}
}

func TestParseV2ShellHookCarriesNoIdentity(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"command":"ls","cwd":"/tmp","timeout":30,"shell":"sh","env":{}}`)
	capture := opencodeingress.ParseV2(raw, v2Event(t, "shell.create.before"), "2.0.20", model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	require.NoError(t, capture.Cause.Check(capture.Disposition))
	require.Empty(t, capture.Delivery.Bindings)
}

func TestParseV2RefusesMissingIdentities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{"session.created", `{"type":"session.created","data":{}}`},
		{"session.created", `{"type":"session.deleted","data":{"sessionID":"ses_constructed"}}`},
		{"session.prompt", `{}`},
		{"tool.execute.before", `{"sessionID":"ses_constructed"}`},
		{"tool.execute.after", `{"id":"call_constructed"}`},
		{"permission.evaluate", `{"action":"edit"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			capture := opencodeingress.ParseV2([]byte(test.raw), v2Event(t, test.name), "2.0.20", model.OccurrenceEnvelopeRef{})
			require.Equal(t, model.CaptureUnsupportedSchema, capture.Disposition)
			require.NoError(t, capture.Cause.Check(capture.Disposition))
			require.Empty(t, capture.Delivery.Bindings)
		})
	}
}

func TestParseV2IsCaseInsensitiveAndIgnoresExtras(t *testing.T) {
	t.Parallel()
	// The struct decoder admits case-insensitive field names and ignores
	// undeclared members, exactly as the 1.18.29 admission authority does.
	raw := []byte(`{"SESSIONID":"ses_constructed","ID":"call_constructed","effect":"allow","unrelated":{"nested":true}}`)
	capture := opencodeingress.ParseV2(raw, v2Event(t, "tool.execute.before"), "2.0.20", model.OccurrenceEnvelopeRef{})
	require.Equal(t, model.CaptureValid, capture.Disposition)
	require.NoError(t, capture.Cause.Check(capture.Disposition))
}
