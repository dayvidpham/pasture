package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
)

func TestEnabledClaudeEventToOccurrenceReader(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "."
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	dbPath := filepath.Join(dir, "pasture.db")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = tracker.Create("file://lifecycle-production-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, tracker.Close())

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_210.json"))
	require.NoError(t, err)
	command := exec.Command(binary, "--db", dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.220")
	command.Stdin = bytes.NewReader(raw)
	output, err = command.CombinedOutput()
	require.NoError(t, err, string(output))

	tracker, err = tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	page, err := reader.Occurrences(context.Background(), model.OccurrenceQuery{Events: []model.ContractEventKind{registration.EventSessionStart}, Page: model.PageRequest{Size: 10}})
	require.NoError(t, err)
	require.Len(t, page.Records(), 1)
	record := page.Records()[0]
	require.Equal(t, registration.EventSessionStart, record.Kind)
	require.Equal(t, model.CaptureValid, record.Capture)
	require.Equal(t, "2.1.220", record.Envelope.HostVersion)
}

func TestInvalidLifecycleInvocationCreatesNoDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing", "pasture.db")
	err := handlers.HookLifecycle(context.Background(), handlers.HookLifecycleInput{DBPath: dbPath, Harness: "claude-code", Event: "Unknown", HostVersion: "2.1.220", Input: bytes.NewBufferString("{}"), Clock: lifecycleCLIClock{}, Operations: lifecycleCLIOperations{}})
	require.Error(t, err)
	_, statErr := os.Stat(dbPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}
