package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// TestWithheldCodexEventIsNotAdmittedByBuiltCLI proves the built production CLI
// dispatches the codex harness and gates its events off by default. Codex
// activation stays default-off in the committed tree through implementation and
// independent review (ratified proposal step 6, "activation last"): even the two
// selected events (SessionStart, PreToolUse) remain withheld through the built
// CLI, which exposes no activation-injection seam, until the committed default
// is flipped to activation.Codex0_146_0() after M3 Implementation UAT. The built
// binary must therefore emit no host continuation and persist no evidence for a
// Codex event, while still reporting the withheld reason on stderr.
//
// The enabled Codex durable path and native continuation bytes are proven on the
// real production handler path with the committed catalog injected through the
// sanctioned pre-activation seam in
// cmd/pasture/hook_lifecycle_production_test.go:TestEnabledCodexHandlersToDurableReadBack
// and internal/handlers/hook_lifecycle_codex_test.go, and pinned by the
// nativeresponse golden-byte tests; the built CLI cannot enable Codex without a
// production backdoor, so this subprocess test verifies the safe default state
// for both selected events.
func TestWithheldCodexEventIsNotAdmittedByBuiltCLI(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pasture")
	buildLifecycleBinary(t, binary)

	for _, tc := range []struct{ event, fixture string }{
		{event: "SessionStart", fixture: "session_start_0_146_0.json"},
		{event: "PreToolUse", fixture: "pre_tool_use_0_146_0.json"},
	} {
		tc := tc
		t.Run(tc.event, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)

			raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "lifecycle", "ingress", "codex", "testdata", "fixtures", tc.fixture))
			require.NoError(t, err)

			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "codex", "--event", tc.event, "--host-version", "0.146.0")
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.Empty(t, stdout.String(), "a withheld Codex event must emit no native continuation on stdout")
			require.Contains(t, stderr.String(), `Codex event "`+tc.event+`" is withheld (reason production-proof-missing)`)

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
		})
	}
}

// erroringWriter fails on every Write, standing in for a closed stdout pipe.
type erroringWriter struct{ writes int }

func (w *erroringWriter) Write(p []byte) (int, error) {
	w.writes++
	return 0, errors.New("closed output")
}

// TestLifecycleCommandReportsStdoutWriteFailureAfterDurableCommit exercises the
// harness-neutral native-continuation write-failure branch in the production
// lifecycle command RunE (cmd/pasture/hook_lifecycle.go): the durable receipt
// has already committed, so a failed stdout write must be reported with an
// actionable diagnostic and the hook must still exit 0 rather than signalling
// failure to the host. It drives the real command in-process with a stdout
// writer that fails on Write, using an enabled Claude PreToolUse gate (which
// produces native continuation bytes; Codex is default-off and cannot reach the
// write path through the built CLI). This restores the output-failure assertion
// lost when the canonical-only writeLifecycleResponse helper was removed.
func TestLifecycleCommandReportsStdoutWriteFailureAfterDurableCommit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	raw := readProductionClaudeFixture(t, "pre_tool_use_2_1_222.json", "PreToolUse")

	failing := &erroringWriter{}
	var stderr bytes.Buffer
	rootCmd.SetArgs([]string{databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "claude-code", "--event", "PreToolUse", "--host-version", "2.1.222"})
	rootCmd.SetIn(bytes.NewReader(raw))
	rootCmd.SetOut(failing)
	rootCmd.SetErr(&stderr)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetIn(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	require.NoError(t, rootCmd.Execute(), "the hook must exit 0 after the durable commit even when the stdout write fails")
	require.NotZero(t, failing.writes, "the failing writer must have been asked to write the native continuation")
	require.Contains(t, stderr.String(), "could not write its committed host continuation")
	require.Contains(t, stderr.String(), "the event was recorded but the host received no continuation; inspect the database and retry the hook input")

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	consultation := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
	require.Len(t, occurrences, 1, "durable occurrence evidence must be committed before the failed stdout write")
	require.Len(t, interpreted, 1)
	require.Len(t, consultation, 1)
	require.Equal(t, interpreted[0].ProducingOperationJournalID, consultation[0].ProducingOperationJournalID, "one durable operation groups interpreted and consultation evidence")
	require.Less(t, interpreted[0].JournalID, consultation[0].JournalID, "interpreted evidence precedes consultation evidence in the committed operation")
}
