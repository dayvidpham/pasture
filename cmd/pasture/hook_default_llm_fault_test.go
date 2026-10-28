package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultLLMFailClosedDistinguishesContinuationFromFault(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	raw := claudeFixture(t, "pre_tool_use_2_1_261.json")
	path := filepath.Join(t.TempDir(), "pasture.db")
	initializeLifecycleTestDatabase(t, path)
	env := []string{"PASTURE_ACTOR_ID=", "PASTURE_HOOK_FAIL_CLOSED=1", "PASTURE_WRITE_LANE=direct", "PASTURE_CAPTURE_DIR="}
	for range 2 {
		run := runLifecycleHook(t, binary, path, "PreToolUse", raw, env...)
		require.Equal(t, 0, run.ExitCode, run.Stderr)
		require.Empty(t, run.Stdout)
		require.Empty(t, run.Stderr)
	}
	fault := runLifecycleHook(t, binary, unopenableDatabase(t, t.TempDir()), "PreToolUse", raw, env...)
	require.Equal(t, 2, fault.ExitCode)
	require.Empty(t, fault.Stdout)
	require.Contains(t, fault.Stderr, "could not evaluate")
}
