package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// TestWithheldCodexEventIsNotAdmittedByBuiltCLI proves the built production CLI
// dispatches the codex harness and gates its events off by default. Codex
// activation stays default-off in the committed tree until a later wave, so the
// built binary must emit no host continuation and persist no evidence for a
// Codex event, while still reporting the withheld reason on stderr.
//
// The enabled Codex durable path and native continuation bytes are proven on
// the in-process production path in
// internal/handlers/hook_lifecycle_codex_test.go (with the injected activation
// configuration the committed manifest will later supply) and pinned by the
// nativeresponse golden-byte tests; the built CLI cannot enable Codex without a
// production backdoor, so this subprocess test verifies the safe default state.
//
// FAILS until the L3 static Codex dispatch lands.
func TestWithheldCodexEventIsNotAdmittedByBuiltCLI(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "codex", "testdata", "fixtures", "pre_tool_use_0_146_0.json"))
	require.NoError(t, err)

	command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "codex", "--event", "PreToolUse", "--host-version", "0.146.0")
	command.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stderr.String())
	require.Empty(t, stdout.String(), "a withheld Codex event must emit no native continuation on stdout")
	require.Contains(t, stderr.String(), `Codex event "PreToolUse" is withheld (reason production-proof-missing)`)

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind), "withheld Codex ingress must persist no occurrence evidence")
	require.NoError(t, tracker.Close())

	list := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "list", "--format", "json")
	stdout.Reset()
	stderr.Reset()
	list.Stdout = &stdout
	list.Stderr = &stderr
	require.NoError(t, list.Run(), stderr.String())
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &page))
	require.Empty(t, page.Items, "withheld Codex ingress must persist no public lifecycle record")
}
