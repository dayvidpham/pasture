package lifecycle_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func observationBinding(t *testing.T) lifecycle.EventBinding {
	t.Helper()
	binding, err := lifecycle.BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeEventSessionStart)
	require.NoError(t, err)
	return binding
}

func gateBinding(t *testing.T) lifecycle.EventBinding {
	t.Helper()
	binding, err := lifecycle.BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeEventPreToolUse)
	require.NoError(t, err)
	return binding
}

func declaredField(t *testing.T, binding lifecycle.EventBinding, kind runtime.NativeIdentityKind) string {
	t.Helper()
	for _, field := range binding.DeclaredIdentities() {
		if field.Kind() == kind {
			return field.NativeName()
		}
	}
	t.Fatalf("binding has no identity of kind %s", kind)
	return ""
}

func identity(t *testing.T, kind runtime.NativeIdentityKind, name, value string) lifecycle.Identity {
	t.Helper()
	result, err := lifecycle.NewIdentity(kind, name, value)
	require.NoError(t, err)
	return result
}
