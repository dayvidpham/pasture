package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func TestTaskAgents_AllKindsAndUnknownAuthor(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	tr, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	defer tr.Close()

	h, err := tr.RegisterHumanAgent("test", "Human name", "")
	require.NoError(t, err)
	s, err := tr.RegisterSoftwareAgent("test", "Software name", "1", "")
	require.NoError(t, err)
	model := provenance.DefaultModelRegistry().Models()[0]
	ml, err := tr.RegisterMLAgent("test", provenance.RoleWorker, model.Provider, model.Name)
	require.NoError(t, err)
	require.NoError(t, tasks.RegisterWellKnownAgents(context.Background(), tr, tasks.NewWellKnownAgentCache()))
	unknown := provenance.AgentID{Namespace: "missing"}.String()
	require.NoError(t, tr.SetAgentCategories(mustAgentID(t, unknown), protocol.AutomatonRoleNone, protocol.PastureRoleWorker))

	var out bytes.Buffer
	code, err := handlers.TaskAgentsList(&out, path, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	var entries []struct {
		AgentID       string
		Kind          string
		Name          string
		WellKnownName string
		AutomatonRole string
		PastureRole   string
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &entries))
	require.Len(t, entries, tasks.WellKnownAgentCount+3)

	byID := make(map[string]string)
	for _, e := range entries {
		require.NotEmpty(t, e.Kind)
		require.NotEmpty(t, e.Name)
		require.NotContains(t, byID, e.AgentID)
		byID[e.AgentID] = e.Kind

		var show bytes.Buffer
		code, err := handlers.TaskAgentsShow(&show, path, e.AgentID, types.OutputJSON)
		require.NoError(t, err)
		require.Zero(t, code)
		var got map[string]any
		require.NoError(t, json.Unmarshal(show.Bytes(), &got))
		require.Equal(t, e.Name, got["name"])
		require.Equal(t, e.Kind, got["kind"])
	}

	require.Equal(t, "human", byID[h.ID.String()])
	require.Equal(t, "software", byID[s.ID.String()])
	require.Equal(t, "machine_learning", byID[ml.ID.String()])
	require.NotContains(t, byID, unknown, "category-only rows must not create an agent")

	var text bytes.Buffer
	code, err = handlers.TaskAgentsList(&text, path, types.OutputText)
	require.NoError(t, err)
	require.Zero(t, code)
	for id := range byID {
		require.Equal(t, 1, strings.Count(text.String(), id))
	}
	require.Contains(t, text.String(), "Human name")
	require.Contains(t, text.String(), "Software name")
	require.Contains(t, text.String(), string(model.Name))

	id := createTask(t, path, "comments")
	for _, author := range []string{"", "malformed", unknown} {
		var out bytes.Buffer
		code, err := handlers.TaskCommentAdd(&out, handlers.TaskCommentAddInput{
			DBPath:   path,
			IdStr:    id,
			AuthorId: author,
			Body:     "test",
		}, types.OutputJSON)
		require.Equal(t, 1, code)
		var se *pasterrors.StructuredError
		require.ErrorAs(t, err, &se)
		require.Contains(t, se.Fix, "pasture task agents list")
		require.Empty(t, out.String())
	}

	cs, err := tr.Comments(mustTaskID(t, id))
	require.NoError(t, err)
	require.Empty(t, cs)
	_, err = tr.Agent(mustAgentID(t, unknown))
	require.ErrorIs(t, err, provenance.ErrNotFound)

	out.Reset()
	code, err = handlers.TaskAgentsShow(&out, path, unknown, types.OutputJSON)
	require.Equal(t, 1, code)
	var se *pasterrors.StructuredError
	require.ErrorAs(t, err, &se)
	require.Contains(t, se.Fix, "pasture task agents list")
	require.Empty(t, out.String())
}

func mustTaskID(t *testing.T, s string) provenance.TaskID {
	t.Helper()
	id, err := provenance.ParseTaskID(s)
	require.NoError(t, err)
	return id
}

func mustAgentID(t *testing.T, s string) provenance.AgentID {
	t.Helper()
	id, err := provenance.ParseAgentID(s)
	require.NoError(t, err)
	return id
}

func TestTaskReadiness_UsesAllBlockersBeforeLabelFilter(t *testing.T) {
	t.Parallel()

	path := dbPath(t)
	tr, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	defer tr.Close()

	a := mustTaskID(t, createTask(t, path, "A"))
	b := mustTaskID(t, createTask(t, path, "B"))
	c := mustTaskID(t, createTask(t, path, "C"))
	d := mustTaskID(t, createTask(t, path, "Closed source"))
	_, err = tr.Start(a)
	require.NoError(t, err)
	_, err = tr.Start(b)
	require.NoError(t, err)
	require.NoError(t, tr.AddEdge(a, b.String(), provenance.EdgeBlockedBy))
	require.NoError(t, tr.AddEdge(a, c.String(), provenance.EdgeBlockedBy))
	require.NoError(t, tr.AddEdge(d, b.String(), provenance.EdgeBlockedBy))
	_, err = tr.CloseTask(d, "done")
	require.NoError(t, err)
	require.NoError(t, tr.AddLabel(a, "work"))

	check := func(blocked bool) {
		t.Helper()
		var out bytes.Buffer
		code, err := handlers.TaskBlocked(&out, path, types.OutputJSON, "work")
		require.NoError(t, err)
		require.Zero(t, code)
		got := decodeTaskList(t, out.String())
		if blocked {
			require.Len(t, got, 1)
			require.Equal(t, a.String(), got[0].ID)
		} else {
			require.Empty(t, got)
		}

		out.Reset()
		code, err = handlers.TaskReady(&out, path, types.OutputJSON, "work")
		require.NoError(t, err)
		require.Zero(t, code)
		got = decodeTaskList(t, out.String())
		if blocked {
			require.Empty(t, got)
		} else {
			require.Len(t, got, 1)
			require.Equal(t, a.String(), got[0].ID)
		}
	}

	check(true)
	_, err = tr.CloseTask(b, "done")
	require.NoError(t, err)
	check(true)
	_, err = tr.CloseTask(c, "done")
	require.NoError(t, err)
	check(false)

	// A non-blocking typed edge to an open task does not change readiness.
	e := mustTaskID(t, createTask(t, path, "provenance source"))
	require.NoError(t, tr.AddEdge(a, e.String(), provenance.EdgeDerivedFrom))
	check(false)

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, provenance.TaskID{Namespace: "unknown"}.String(), []provenance.EdgeKind{provenance.EdgeBlockedBy}, types.OutputJSON)
	require.Equal(t, 3, code)
	require.Error(t, err)
	require.Empty(t, out.String())
}

func TestTaskAgents_EmptyRegistry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.db")
	tr, err := tasks.OpenTaskTracker(path)
	require.NoError(t, err)
	require.NoError(t, tr.Close())

	var out bytes.Buffer
	code, err := handlers.TaskAgentsList(&out, path, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	require.Equal(t, "[]\n", out.String())
}

func TestTaskAgents_RegistryWithoutOptionalTables(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy?registry.db")
	tr, err := provenance.OpenSQLite(path)
	require.NoError(t, err)
	h, err := tr.RegisterHumanAgent("test", "Legacy human", "")
	require.NoError(t, err)
	require.NoError(t, tr.Close())

	var out bytes.Buffer
	code, err := handlers.TaskAgentsList(&out, path, types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &entries))
	require.Len(t, entries, 1)
	require.Equal(t, h.ID.String(), entries[0]["agentId"])
	require.Equal(t, "None", entries[0]["automatonRole"])
	require.Equal(t, "None", entries[0]["pastureRole"])

	out.Reset()
	code, err = handlers.TaskAgentsShow(&out, path, h.ID.String(), types.OutputJSON)
	require.NoError(t, err)
	require.Zero(t, code)
	var shown map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &shown))
	require.Equal(t, entries[0], shown)
}
