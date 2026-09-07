package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// Invalid native names retain the real pre-storage refusal proof. Stop and
// PostToolUse now have cleared activation evidence and are tested below instead
// of being artificially withheld to preserve this guard.
func TestUnsupportedCodexEventIsNotAdmittedByBuiltCLI(t *testing.T) {
	t.Parallel()

	binary := lifecycleBinary(t)

	for _, event := range []string{"NotRegistered", "SessionStarted"} {
		event := event
		t.Run(event, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "unopened", tasks.DefaultDBFilename.String())

			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "codex", "--event", event, "--host-version", "0.153.0")
			command.Env = append(os.Environ(), "PASTURE_DB_PATH="+dbPath, "PASTURE_CAPTURE_DIR=")
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
			require.Contains(t, stderr.String(), `declares no native event named "`+event+`"`)

			_, statErr := os.Stat(dbPath)
			require.ErrorIs(t, statErr, os.ErrNotExist, "a withheld Codex event must be refused before any storage access")
		})
	}
}

func TestClearedCodexStopAndPostToolUseAreAdmittedByBuiltCLI(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	for _, tc := range []struct {
		event   string
		fixture string
		proof   activation.CaptureProof
	}{
		{event: "Stop", fixture: "stop_0_153_0.json", proof: activation.CaptureProofCodexStop},
		{event: "PostToolUse", fixture: "post_tool_use_0_153_0.json", proof: activation.CaptureProofCodexPostToolUse},
	} {
		t.Run(tc.event, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
			initializeLifecycleTestDatabase(t, dbPath)
			raw := readCodexProductionFixture(t, tc.fixture, tc.event, tc.proof)
			command := exec.Command(binary, databaseFlagName.Argument(), dbPath, "hook", "lifecycle", "--harness", "codex", "--event", tc.event, "--host-version", registration.Codex0_153_0().Version)
			command.Env = append(os.Environ(), "PASTURE_DB_PATH="+dbPath, "PASTURE_CAPTURE_DIR=", "PASTURE_ACTOR_ID=")
			command.Stdin = bytes.NewReader(raw)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			require.NoError(t, command.Run(), stderr.String())
			require.Empty(t, stderr.String(), "the event must be evaluated, not fail open")
			require.Equal(t, `{"continue":true}`, stdout.String())
			tracker, err := tasks.OpenTaskTracker(dbPath)
			require.NoError(t, err)
			defer tracker.Close()
			require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind), 1)
			require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind), 1)
			require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind), 1)
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
// writer that fails on Write, using the enabled Codex PreToolUse gate. Codex
// emits a continuation object and reaches this shared write branch. Claude's
// evaluated Proceed emits empty stdout and must not attempt this write.
//
// SERIAL: this test executes the shared rootCmd in-process and sets its
// streams, so it must not use t.Parallel.
func TestLifecycleCommandReportsStdoutWriteFailureAfterDurableCommit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	t.Setenv("PASTURE_DB_PATH", dbPath)
	initializeLifecycleTestDatabase(t, dbPath)
	raw := readCodexProductionFixture(t, "pre_tool_use_0_153_0.json", "PreToolUse", activation.CaptureProofCodexPreToolUse)

	failing := &erroringWriter{}
	var stderr bytes.Buffer
	rootCmd.SetArgs([]string{
		databaseFlagName.Argument(), dbPath, "hook", "lifecycle",
		"--harness", "codex", "--event", "PreToolUse",
		"--host-version", registration.Codex0_153_0().Version,
	})
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
