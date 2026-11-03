package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/lifecycle/execution"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// TestBuiltHookExecutionReachesTheSessionAPI is the end-to-end proof on both
// transport routes: the built CLI hook delivers one bounded execution
// declaration, the receipt is occurrence.v3, full and suffix replay publish the
// immutable execution cache, a reopen keeps it, and the session API serves the
// summary and the snapshot-pinned history.
func TestBuiltHookExecutionReachesTheSessionAPI(t *testing.T) {
	binary := lifecycleBinary(t)
	raw := claudeFixture(t, "user_prompt_submit_2_1_261.json")
	session := captureSessionIDOf(t, raw, ir.HarnessClaudeCode)

	// The direct path writes on its own store; the served path asks a real
	// in-process daemon lane over a private socket. Both must end at the same
	// durable v3 record and the same API view.
	for _, route := range []string{"direct", "served"} {
		t.Run(route, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeWarmLaneTestDatabase(t, dbPath)
			const envPrefix = "PASTURE_EXECUTION_"
			env := []string{
				envPrefix + "PROVIDER=test-route",
				envPrefix + "MODEL=claude-test-model",
				envPrefix + "ROLE=worker",
				"PASTURE_CAPTURE_DIR=",
			}
			if route == "direct" {
				env = append(env, "PASTURE_WRITE_LANE=direct")
			} else {
				socket := startHookTestWriteLane(t, dbPath)
				env = append(env, "PASTURE_WRITE_LANE=auto", "PASTURE_WRITE_LANE_SOCKET="+socket)
			}

			first := runLifecycleHook(t, binary, dbPath, "UserPromptSubmit", raw, env...)
			require.Zero(t, first.ExitCode, first.Stderr)
			require.Empty(t, first.Stdout)
			require.Empty(t, first.Stderr)

			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
			require.Len(t, occurrences, 1, "the route must commit exactly one occurrence")
			var payload struct {
				Execution json.RawMessage `json:"execution"`
			}
			require.NoError(t, json.Unmarshal(occurrences[0].Payload, &payload))
			require.NotEmpty(t, payload.Execution, "a valid session delivery with declarations must be occurrence.v3 with execution")
			firstOccurrence := int64(occurrences[0].JournalID)

			// Full replay publishes the first observation.
			_, err = tasks.RebuildSessionState(context.Background(), tracker)
			require.NoError(t, err)

			// A second delivery on the same route is applied by suffix replay.
			second := runLifecycleHook(t, binary, dbPath, "UserPromptSubmit", raw, env...)
			require.Zero(t, second.ExitCode, second.Stderr)
			require.Empty(t, second.Stderr)
			_, rebuilt, err := tasks.EnsureSessionState(context.Background(), tracker)
			require.NoError(t, err)
			require.True(t, rebuilt, "a committed tail must be replayed by the suffix path")
			require.NoError(t, tracker.Close())

			// Reopen the store and confirm a clean ensure keeps the cache.
			reopened, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer func() { require.NoError(t, reopened.Close()) }()
			_, rebuilt, err = tasks.EnsureSessionState(context.Background(), reopened)
			require.NoError(t, err)
			require.False(t, rebuilt, "a clean reopened cache must not need another publication")

			readDB, err := dbconn.OpenReadOnlyDB(dbPath)
			require.NoError(t, err)
			defer func() { require.NoError(t, readDB.Close()) }()
			service := sessionapi.NewService(readDB)
			service.ActiveWindow = 30 * time.Minute
			service.IdleWindow = 24 * time.Hour

			detail, err := service.Get(context.Background(), "claude-code", session)
			require.NoError(t, err)
			require.NotNil(t, detail.Session.Execution, "the summary must come from the committed v3 receipt")
			require.Equal(t, "claude-test-model", detail.Session.Execution.Model.Value)
			require.Equal(t, execution.Environment, detail.Session.Execution.Model.Source)
			require.Equal(t, "test-route", detail.Session.Execution.Provider.Value)
			require.Equal(t, execution.Environment, detail.Session.Execution.Provider.Source)
			require.Equal(t, "worker", detail.Session.Execution.ProtocolRole.Value)
			require.Equal(t, execution.PastureRole, detail.Session.Execution.ProtocolRole.Vocabulary)

			history, err := service.ExecutionHistory(context.Background(), "claude-code", session,
				sessionapi.ExecutionHistoryOptions{})
			require.NoError(t, err)
			require.Len(t, history.Items, 2, "both receipts must be in the history")
			require.Greater(t, history.Items[0].OccurrenceID, history.Items[1].OccurrenceID,
				"history is descending by occurrence id")
			require.Equal(t, detail.Session.Execution.OccurrenceID, history.Items[0].OccurrenceID,
				"the summary is the newest observation in the history")
			require.Equal(t, firstOccurrence, history.Items[1].OccurrenceID,
				"the first receipt remains the older history row")
		})
	}
}
