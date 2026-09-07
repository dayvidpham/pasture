package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGateRebuildIndexCommandUsesRealStoreAndGenerationGuard(t *testing.T) {
	old := flagDBPath
	flagDBPath = filepath.Join(t.TempDir(), "pasture.db")
	t.Cleanup(func() { flagDBPath = old })
	registered, _, err := rootCmd.Find([]string{"gate", "rebuild-index"})
	require.NoError(t, err)
	require.Equal(t, "rebuild-index", registered.Name())
	command := newGateRebuildIndexCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--generation=0"})
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.Contains(t, output.String(), "captured journal prefix verified")
	reset := newGateRebuildIndexCommand()
	reset.SetOut(&output)
	reset.SetErr(&output)
	reset.SetArgs([]string{"--reset", "--generation=0"})
	require.NoError(t, reset.ExecuteContext(t.Context()))
	stale := newGateRebuildIndexCommand()
	stale.SetOut(&output)
	stale.SetErr(&output)
	stale.SetArgs([]string{"--reset", "--generation=0"})
	require.ErrorContains(t, stale.ExecuteContext(t.Context()), "generation changed from 0 to 1")
}
