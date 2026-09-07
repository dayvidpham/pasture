package model_test

import (
	"encoding/json"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/stretchr/testify/require"
)

func TestCaptureCauseIsTransientAndConsistent(t *testing.T) {
	cause := model.NewCaptureCause(model.CauseWrongKind, "event", model.JSONObject, model.CaptureMalformed)
	require.NoError(t, cause.Check(model.CaptureMalformed))
	require.Error(t, cause.Check(model.CaptureValid))
	require.Error(t, cause.Check(model.CaptureUnsupportedSchema))
	reason, fix, err := cause.Advice(model.CaptureMalformed)
	require.NoError(t, err)
	require.Equal(t, `member "event" has the wrong JSON kind; required kind is object`, reason)
	require.Contains(t, fix, `Send "event" as a JSON object`)
	raw, err := json.Marshal(cause)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(raw), "the transient cause has no exported durable fields")
	require.Panics(t, func() {
		model.NewCaptureCause(model.CauseNonObject, "event", model.JSONObject, model.CaptureValid)
	})
	require.Panics(t, func() {
		model.NewCaptureCause(model.CaptureCauseKind(255), "event", model.JSONObject, model.CaptureMalformed)
	})
}

func TestCaptureCauseEscapesHostMemberNames(t *testing.T) {
	cause := model.NewCaptureCause(model.CauseWrongKind, "host\nmember", model.JSONString, model.CaptureUnsupportedSchema)
	reason, _, err := cause.Advice(model.CaptureUnsupportedSchema)
	require.NoError(t, err)
	require.Contains(t, reason, `"host\nmember"`)
	require.NotContains(t, reason, "\n")
}
