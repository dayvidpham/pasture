package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/runtime"
)

// TestLifecycleEventMappingDeclaredField pins the semantics the four deleted
// private findDeclaredField copies used to provide, now consolidated onto the
// pinned contract type per UAT resolution 3. The mapping is drawn from a real
// pinned profile (Claude PreToolUse declares session_id and tool_use_id) so the
// method is exercised against production data, not a fabricated fixture.
func TestLifecycleEventMappingDeclaredField(t *testing.T) {
	t.Parallel()

	mapping, err := runtime.ClaudeCode2_1_261Lifecycle().Mapping(runtime.ClaudeEventPreToolUse)
	require.NoError(t, err)

	t.Run("declared field is found and returned by exact name", func(t *testing.T) {
		t.Parallel()
		field, found := mapping.DeclaredField("session_id")
		require.True(t, found)
		require.Equal(t, "session_id", field.NativeName())
		require.Equal(t, runtime.IdentitySession, field.Kind())
	})

	t.Run("second declared field is found", func(t *testing.T) {
		t.Parallel()
		field, found := mapping.DeclaredField("tool_use_id")
		require.True(t, found)
		require.Equal(t, "tool_use_id", field.NativeName())
		require.Equal(t, runtime.IdentityToolCall, field.Kind())
	})

	t.Run("undeclared name is not found", func(t *testing.T) {
		t.Parallel()
		field, found := mapping.DeclaredField("not_a_field")
		require.False(t, found)
		require.False(t, field.IsValid())
	})

	t.Run("empty name is not found", func(t *testing.T) {
		t.Parallel()
		field, found := mapping.DeclaredField("")
		require.False(t, found)
		require.False(t, field.IsValid())
	})
}
