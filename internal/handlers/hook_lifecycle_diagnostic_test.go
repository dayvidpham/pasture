package handlers

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/stretchr/testify/require"
)

func TestCaptureDiagnosticDoesNotInventAnUnknownCause(t *testing.T) {
	dispatch := frontendRegistry[ir.HarnessCodex]
	event, err := ingress.EventByNativeName(dispatch.manifest, "PreToolUse")
	require.NoError(t, err)
	err = unbindableCaptureError(dispatch, event, HookLifecycleInput{HostVersion: "0.153.0"}, lifecycleCapture{
		disposition: model.CaptureUnsupportedSchema,
	})
	require.Contains(t, err.Error(), "adapter supplied no specific cause")
	require.NotContains(t, err.Error(), "required member")
	require.NotContains(t, err.Error(), "is absent")
	require.NotContains(t, err.Error(), "wrong JSON kind")
	require.NotContains(t, err.Error(), "identity field is missing")
}

func TestCaptureDiagnosticRejectsMismatchedCause(t *testing.T) {
	dispatch := frontendRegistry[ir.HarnessClaudeCode]
	event, err := ingress.EventByNativeName(dispatch.manifest, "SessionStart")
	require.NoError(t, err)
	cause := model.NewCaptureCause(model.CauseMissingMember, "session_id", model.JSONString, model.CaptureUnsupportedSchema)
	err = unbindableCaptureError(dispatch, event, HookLifecycleInput{}, lifecycleCapture{
		disposition: model.CaptureMalformed,
		cause:       cause,
	})
	require.Contains(t, err.Error(), "disagrees with its disposition")
	require.NotContains(t, err.Error(), "is absent")
}
