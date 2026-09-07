package claude_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/claude"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/stretchr/testify/require"
)

func TestClaudeCaptureNamesTheRefusalCause(t *testing.T) {
	fixture, err := os.ReadFile("testdata/fixtures/session_start_2_1_261.json")
	require.NoError(t, err)
	event, err := ingress.EventByNativeName(registration.ClaudeCode2_1_261(), "SessionStart")
	require.NoError(t, err)
	parse := func(raw []byte) claude.Capture {
		return claude.Parse(raw, event, "2.1.261", model.OccurrenceEnvelopeRef{})
	}
	control := parse(fixture)
	require.Equal(t, model.CaptureValid, control.Disposition)
	require.Equal(t, model.CauseUnknown, control.Cause.Kind())
	check := func(t *testing.T, edit func(map[string]json.RawMessage), kind model.CaptureCauseKind, field string, required model.JSONKind) {
		t.Helper()
		var members map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fixture, &members))
		edit(members)
		raw, err := json.Marshal(members)
		require.NoError(t, err)
		capture := parse(raw)
		require.Equal(t, kind, capture.Cause.Kind(), "wrong refusal cause")
		require.Equal(t, field, capture.Cause.Field(), "wrong refusal field")
		require.Equal(t, required, capture.Cause.RequiredKind(), "wrong required kind")
		require.Equal(t, model.CaptureUnsupportedSchema, capture.Disposition)
		require.Equal(t, capture.Disposition, capture.Delivery.Capture)
		require.Equal(t, raw, capture.Delivery.Body)
		require.Empty(t, capture.Delivery.Bindings)
	}
	t.Run("undeclared", func(t *testing.T) {
		check(t, func(m map[string]json.RawMessage) { m["added_member"] = json.RawMessage(`"private-value"`) }, model.CauseUndeclaredMember, "added_member", model.JSONKindUnknown)
	})
	t.Run("missing", func(t *testing.T) {
		check(t, func(m map[string]json.RawMessage) { delete(m, "session_id") }, model.CauseMissingMember, "session_id", model.JSONString)
	})
	t.Run("wrong-kind", func(t *testing.T) {
		check(t, func(m map[string]json.RawMessage) { m["session_id"] = json.RawMessage(`42`) }, model.CauseWrongKind, "session_id", model.JSONString)
	})
	t.Run("empty", func(t *testing.T) {
		check(t, func(m map[string]json.RawMessage) { m["session_id"] = json.RawMessage(`""`) }, model.CauseEmptyIdentity, "session_id", model.JSONString)
	})
	t.Run("overlength", func(t *testing.T) {
		check(t, func(m map[string]json.RawMessage) {
			m["session_id"], err = json.Marshal(strings.Repeat("x", 513))
			require.NoError(t, err)
		}, model.CauseOverlengthIdentity, "session_id", model.JSONString)
	})
	t.Run("unusable", func(t *testing.T) {
		check(t, func(m map[string]json.RawMessage) { m["session_id"] = json.RawMessage(`" padded"`) }, model.CauseUnusableIdentity, "session_id", model.JSONString)
	})
	t.Run("deterministic-first-defect", func(t *testing.T) {
		for range 50 {
			check(t, func(m map[string]json.RawMessage) {
				m["z_added"] = json.RawMessage(`true`)
				m["a_added"] = json.RawMessage(`true`)
				delete(m, "session_id")
			}, model.CauseUndeclaredMember, "a_added", model.JSONKindUnknown)
		}
	})
}
