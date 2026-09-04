package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// TestUnselectedCodexEventIsNotAdmittedByBuiltCLI proves the built production
// CLI still refuses every NON-selected Codex catalog event after the M3-UAT
// activation flip. The committed dispatch now enables the two accepted events
// (SessionStart, PreToolUse) — their enabled built-CLI path is proven end-to-end
// in internal/codegen TestCodexGeneratedRunnerDrivesBuiltCLI — but the remaining
// catalog events stay withheld (reason outside-target-set): the built binary
// emits no native continuation, reports the actionable withheld diagnostic on
// stderr, exits 0, and opens no database (admission enforced before any storage
// access). Stop and PostToolUse are representative of the eight withheld events.
//
// WHAT IT VISITS: those TWO events, named here as representatives.
// WHAT IT DOES NOT READ: the other six withheld events. The withheld SET is
// derived and held by the activation support report; this drives a sample of it
// through the built binary to show the admission decision reaches the host.
func TestUnselectedCodexEventIsNotAdmittedByBuiltCLI(t *testing.T) {
	t.Parallel()

	binary := lifecycleBinary(t)

	for _, event := range []string{"Stop", "PostToolUse"} {
		event := event
		t.Run(event, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())

			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "codex", "--event", event, "--host-version", "0.146.0")
			command.Stdin = bytes.NewReader([]byte(`{"hook_event_name":"` + event + `"}`))
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stderr.String())
			// A withheld event is a FAULT: pasture deliberately did not
			// evaluate it. Under the fail-open default the host must still
			// proceed, and on Codex a proceed is a byte shape, so the hook
			// emits the harness continue bytes. Emitting nothing would be a
			// proceed only on a host that reads the exit code.
			require.Equal(t, `{"continue":true}`, stdout.String(),
				"a withheld Codex event must let the host continue with the Codex continue object")
			require.Contains(t, stderr.String(), `Codex event "`+event+`" is withheld (reason outside-target-set)`)

			_, statErr := os.Stat(dbPath)
			require.ErrorIs(t, statErr, os.ErrNotExist, "a withheld Codex event must be refused before any storage access")
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
// produces native continuation bytes; Codex now enables two events and could
// reach the write path through the built CLI; we use Claude to keep the test
// harness-neutral and focused on the write-failure branch).
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
