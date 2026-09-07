package opencode_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/opencode"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeCaptureNamesTheRefusalCause(t *testing.T) {
	event, err := ingress.EventByNativeName(registration.OpenCode1_18_29(), "tool.execute.before")
	require.NoError(t, err)
	check := func(t *testing.T, raw string, disposition model.CaptureDisposition, cause model.CaptureCauseKind, field string, kind model.JSONKind) {
		t.Helper()
		capture := opencode.Parse([]byte(raw), event, "1.18.29", model.OccurrenceEnvelopeRef{})
		require.Equal(t, cause, capture.Cause.Kind(), "wrong refusal cause")
		require.Equal(t, field, capture.Cause.Field(), "wrong refusal field")
		require.Equal(t, kind, capture.Cause.RequiredKind(), "wrong required kind")
		require.Equal(t, disposition, capture.Disposition)
		require.Equal(t, disposition, capture.Delivery.Capture)
		require.Equal(t, []byte(raw), capture.Delivery.Body)
	}
	t.Run("malformed", func(t *testing.T) {
		check(t, `{`, model.CaptureMalformed, model.CauseMalformedJSON, "", model.JSONKindUnknown)
	})
	t.Run("nonobject", func(t *testing.T) {
		check(t, `[]`, model.CaptureMalformed, model.CauseNonObject, "", model.JSONKindUnknown)
	})
	t.Run("retired-wrapper", func(t *testing.T) {
		check(t, `{"harness":"opencode","event":"tool.execute.before","payload":{}}`, model.CaptureMalformed, model.CauseWrongKind, "event", model.JSONObject)
	})
	t.Run("wrong-leaf-kind", func(t *testing.T) {
		check(t, `{"input":{"sessionID":42,"callID":"call"}}`, model.CaptureMalformed, model.CauseWrongKind, "input.sessionID", model.JSONString)
	})
	t.Run("missing-container", func(t *testing.T) {
		check(t, `{}`, model.CaptureUnsupportedSchema, model.CauseMissingMember, "input", model.JSONObject)
	})
	t.Run("missing-leaf", func(t *testing.T) {
		check(t, `{"input":{"sessionID":"session"}}`, model.CaptureUnsupportedSchema, model.CauseMissingMember, "input.callID", model.JSONString)
	})
	t.Run("null-leaf", func(t *testing.T) {
		check(t, `{"input":{"sessionID":null,"callID":"call"}}`, model.CaptureUnsupportedSchema, model.CauseWrongKind, "input.sessionID", model.JSONString)
	})
	t.Run("empty-leaf", func(t *testing.T) {
		check(t, `{"input":{"sessionID":"","callID":"call"}}`, model.CaptureUnsupportedSchema, model.CauseEmptyIdentity, "input.sessionID", model.JSONString)
	})
	t.Run("struct-match-control", func(t *testing.T) {
		check(t, `{"INPUT":{"SESSIONID":"session","CALLID":"call"},"extra":true}`, model.CaptureValid, model.CauseUnknown, "", model.JSONKindUnknown)
	})
	t.Run("merged-object-control", func(t *testing.T) {
		check(t, `{"input":{"sessionID":"session"},"INPUT":{"callID":"call"}}`, model.CaptureValid, model.CauseUnknown, "", model.JSONKindUnknown)
	})
	t.Run("merged-object-missing-leaf", func(t *testing.T) {
		check(t, `{"input":{"sessionID":"session"},"INPUT":{}}`, model.CaptureUnsupportedSchema, model.CauseMissingMember, "input.callID", model.JSONString)
	})
	t.Run("null-container", func(t *testing.T) {
		check(t, `{"input":null}`, model.CaptureUnsupportedSchema, model.CauseWrongKind, "input", model.JSONObject)
	})
	t.Run("overlength", func(t *testing.T) {
		check(t, `{"input":{"sessionID":"`+strings.Repeat("x", 513)+`","callID":"call"}}`, model.CaptureUnsupportedSchema, model.CauseOverlengthIdentity, "input.sessionID", model.JSONString)
	})
	t.Run("unusable", func(t *testing.T) {
		check(t, `{"input":{"sessionID":"padded ","callID":"call"}}`, model.CaptureUnsupportedSchema, model.CauseUnusableIdentity, "input.sessionID", model.JSONString)
	})
	t.Run("deterministic-first-required-path", func(t *testing.T) {
		for range 50 {
			check(t, `{"input":{}}`, model.CaptureUnsupportedSchema, model.CauseMissingMember, "input.sessionID", model.JSONString)
		}
	})
	t.Run("deterministic-first-decode-error", func(t *testing.T) {
		check(t, `{"input":{"callID":42,"sessionID":false},"event":[]}`, model.CaptureMalformed, model.CauseWrongKind, "input.callID", model.JSONString)
	})
}
