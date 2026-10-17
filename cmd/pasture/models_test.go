package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCLI_ModelsRootOfflineAllowanceAndRegistration(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	badDB := filepath.Join(home, "not-a-directory")
	require.NoError(t, os.WriteFile(badDB, []byte("not a store"), 0o600))
	badDB = filepath.Join(badDB, "never.db")
	help := runCLIWithHome(t, home, "--db", badDB, "--help")
	require.Zero(t, help.exitCode, help.stderr)
	require.Contains(t, commandNames(help.stdout), "models")
	for _, verb := range []string{"allow", "refresh"} {
		out := runCLIWithHome(t, home, "models", verb, "--help")
		require.Zero(t, out.exitCode, out.stderr)
		require.Contains(t, out.stdout, "--format")
	}
	for _, pair := range [][2]string{{"openai", "gpt-6.1-sol-ultrafast"}, {"new-provider", "openai/gpt-5"}} {
		args := []string{"--db", badDB, "models", "allow", "--provider", pair[0], "--model", pair[1], "--format", "json"}
		out := runCLIWithHome(t, home, args...)
		require.Zero(t, out.exitCode, out.stderr)
		var allowed map[string]string
		require.NoError(t, json.Unmarshal([]byte(out.stdout), &allowed))
		require.Equal(t, "allow", allowed["operation"])
		require.Equal(t, pair[0], allowed["provider"])
		require.Equal(t, pair[1], allowed["model"])
		require.FileExists(t, allowed["path"])
		require.Zero(t, runCLIWithHome(t, home, args...).exitCode, "allow is idempotent")
		db := filepath.Join(home, "tasks.db")
		args = []string{"--db", db, "task", "agents", "register", "ml", "--namespace", "fixture", "--role", "architect", "--provider", pair[0], "--model", pair[1], "--format", "json"}
		out = runCLIWithHome(t, home, args...)
		require.Zero(t, out.exitCode, out.stderr)
		var agent map[string]any
		require.NoError(t, json.Unmarshal([]byte(out.stdout), &agent))
		require.Contains(t, agent["agentId"], "fixture/")
		dup := runCLIWithHome(t, home, args...)
		require.Equal(t, 1, dup.exitCode)
		require.Contains(t, dup.stderr, agent["agentId"])
	}
	out := runCLIWithHome(t, home, "--db", badDB, "models", "allow", "--provider", "openrouter", "--model", "openai/gpt-5#high")
	require.Equal(t, 1, out.exitCode)
	require.Contains(t, out.stderr, "cannot store execution variants")
	require.NoFileExists(t, badDB)
}
