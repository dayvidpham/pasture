package main_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
)

// TestCLI_TaskAgentsRegisterThenComment is the end-to-end production path for
// the registration verb: a fresh user registers an author, then comments with
// the printed ID. It exercises the human, software, and machine-learning
// registration paths.
func TestCLI_TaskAgentsRegisterThenComment(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	run := func(args ...string) runOutcome {
		return runCLI(t, append([]string{"--db", db, "--format", "json", "task"}, args...)...)
	}
	agentID := func(out runOutcome) string {
		t.Helper()
		require.Zero(t, out.exitCode, out.stderr)
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(out.stdout), &got))
		id, _ := got["agentId"].(string)
		require.NotEmpty(t, id)
		return id
	}

	human := agentID(run("agents", "register", "human", "--name", "Ada Lovelace", "--contact", "ada@example.com", "--namespace", "acme"))
	require.True(t, strings.HasPrefix(human, "acme--"))

	software := agentID(run("agents", "register", "software", "--name", "pasture-cli", "--version", "0.0.13", "--namespace", "acme"))
	ml := agentID(run("agents", "register", "ml", "--role", "worker", "--provider", "anthropic", "--model", "claude-opus-4-6", "--namespace", "acme"))

	created := run("create", "authored work", "--namespace", "acme")
	require.Zero(t, created.exitCode, created.stderr)
	var task map[string]any
	require.NoError(t, json.Unmarshal([]byte(created.stdout), &task))
	taskID := task["id"].(string)

	for _, author := range []string{human, software, ml} {
		add := run("comment", "add", taskID, "signed by "+author, "--author", author)
		require.Zero(t, add.exitCode, add.stderr)
		var comment map[string]any
		require.NoError(t, json.Unmarshal([]byte(add.stdout), &comment))
		require.Equal(t, author, comment["authorId"])
	}
}

// TestCLI_TaskAgentsRegisterValidation rejects malformed registrations before
// touching the store, and the error explains how to fix it.
func TestCLI_TaskAgentsRegisterValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"human missing name", []string{"agents", "register", "human"}, "register human"},
		{"software missing version", []string{"agents", "register", "software", "--name", "cli"}, "register software"},
		{"ml missing role", []string{"agents", "register", "ml", "--provider", "anthropic", "--model", "x"}, "register ml"},
		{"ml unknown role", []string{"agents", "register", "ml", "--role", "wizard", "--provider", "anthropic", "--model", "x"}, "architect"},
		{"ml unknown provider", []string{"agents", "register", "ml", "--role", "worker", "--provider", "not-a-provider", "--model", "x"}, "provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "never-created.db")
			out := runCLI(t, append([]string{"--db", path, "task"}, tc.args...)...)
			require.Equal(t, 1, out.exitCode, out.stdout)
			require.Contains(t, out.stderr, tc.wantMsg)
			require.Empty(t, out.stdout)
			require.NoFileExists(t, path, "validation must reject before opening the store")
		})
	}
}

// TestCLI_TaskEventsAndContexts drives the restored query verbs against the
// real handlers over a seeded store.
func TestCLI_TaskEventsAndContexts(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	const (
		epochID = "acme--01968a3c-0000-7000-8000-000000000001"
		sha     = "deadbeefcafebabe1234567890abcdef12345678"
	)

	tr, err := tasks.OpenTaskTracker(db)
	require.NoError(t, err)
	ctx := context.Background()
	eventID, err := tr.RecordEventReturningId(ctx, protocol.AuditEvent{
		EpochId:   epochID,
		Phase:     protocol.PhaseWorkerSlices,
		Role:      "supervisor",
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{"from": "p8", "to": "p9"},
		Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, tr.AttachContext(ctx, eventID, protocol.ContextEpoch, epochID))
	require.NoError(t, tr.AttachContext(ctx, eventID, protocol.ContextGit, sha))
	require.NoError(t, tr.Close())

	// --epoch-id path.
	out := runCLI(t, "--db", db, "--format", "json", "task", "events", "--epoch-id", epochID)
	require.Zero(t, out.exitCode, out.stderr)
	var events []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &events))
	require.Len(t, events, 1)
	require.Equal(t, epochID, events[0]["epochId"])
	require.Equal(t, "PhaseTransition", events[0]["eventType"])

	// --context-kind/--context-id path.
	out = runCLI(t, "--db", db, "--format", "json", "task", "events", "--context-kind", "GitContext", "--context-id", sha)
	require.Zero(t, out.exitCode, out.stderr)
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &events))
	require.Len(t, events, 1)

	// contexts path.
	out = runCLI(t, "--db", db, "--format", "json", "task", "contexts", strconv.FormatInt(eventID, 10))
	require.Zero(t, out.exitCode, out.stderr)
	var contexts []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &contexts))
	require.Len(t, contexts, 2)
}

// TestCLI_TaskEventsContextsValidation proves the restored verbs surface
// actionable validation errors with the expected exit code.
func TestCLI_TaskEventsContextsValidation(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"events no filter", []string{"task", "events"}, "top-level filter"},
		{"events half context pair", []string{"task", "events", "--context-kind", "GitContext"}, "must be passed together"},
		{"events bad phase", []string{"task", "events", "--epoch-id", "x", "--phase", "nope"}, "phase"},
		{"events bad kind", []string{"task", "events", "--context-kind", "Nope", "--context-id", "x"}, "context-kind"},
		{"events bad since", []string{"task", "events", "--epoch-id", "x", "--since", "nope"}, "since"},
		{"contexts non-numeric", []string{"task", "contexts", "abc"}, "event ID"},
		{"contexts non-positive", []string{"task", "contexts", "0"}, "event ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := runCLI(t, append([]string{"--db", db}, tc.args...)...)
			require.Equal(t, 1, out.exitCode, out.stdout)
			require.Contains(t, strings.ToLower(out.stderr), strings.ToLower(tc.wantMsg))
			require.Empty(t, out.stdout)
		})
	}
}

// TestCLI_TaskHelpAdvertisesResidualVerbs pins the task-level help so the new
// verbs are discoverable and stay listed.
func TestCLI_TaskHelpAdvertisesResidualVerbs(t *testing.T) {
	t.Parallel()

	out := runCLI(t, "task", "--help")
	require.Zero(t, out.exitCode, out.stderr)
	for _, verb := range []string{"agents", "contexts", "events"} {
		require.Contains(t, out.stdout, verb)
	}

	agents := runCLI(t, "task", "agents", "--help")
	require.Zero(t, agents.exitCode, agents.stderr)
	for _, sub := range []string{"register", "list", "show"} {
		require.Contains(t, agents.stdout, sub)
	}
}
