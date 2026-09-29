package handlers_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// TestTaskAgentsHandlersNameTheExactFileWhenThePathCarriesURIDelimiters proves
// both agent handlers open the file the operator named when --db carries a URI
// delimiter. An unescaped open truncates the DSN at the "?" and reads a
// different (here, freshly created) file, so the registry lookup returns
// nothing: the list prints "(no registered agents)" and show leaves the
// well-known name empty.
func TestTaskAgentsHandlersNameTheExactFileWhenThePathCarriesURIDelimiters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "agents?probe#db.db")
	truncated := filepath.Join(dir, "agents")

	// Build a real audit/category database at the delimited path and register
	// the well-known agents so both handlers have something to read.
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err, "open/create the database at the escaped path")
	cache := tasks.NewWellKnownAgentCache()
	require.NoError(t, tasks.RegisterWellKnownAgents(context.Background(), tracker, cache))
	require.NoError(t, tracker.Close())

	agentId, ok := cache.Get("pasture/automaton/check-constraints")
	require.True(t, ok, "the registry must register the constraint checker")

	var listOut bytes.Buffer
	code, err := handlers.TaskAgentsList(&listOut, dbPath, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, listOut.String(), "pasture/automaton/check-constraints",
		"the list must read the agent registry from the exact file")
	require.Equal(t, tasks.WellKnownAgentCount, strings.Count(strings.TrimSpace(listOut.String()), "\n")+1,
		"the list must read EVERY registered agent from the exact file, not only the one named above")

	var showOut bytes.Buffer
	code, err = handlers.TaskAgentsShow(&showOut, dbPath, agentId.String(), types.OutputText)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, showOut.String(), "WellKnownName:  pasture/automaton/check-constraints",
		"show must resolve the well-known name from the exact file")

	_, err = os.Stat(truncated)
	require.True(t, os.IsNotExist(err), "no database may appear at the truncated path %q", truncated)
}
