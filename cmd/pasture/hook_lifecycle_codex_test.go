package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/activation"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
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

// initializePostCommitTestDatabase publishes immutable owners for the actual
// delivery contract, not the Claude-only lane fixture's owners. These tests
// must reach a post-commit failure; cold definition publication is a separate
// precondition, not work to race against the hook's unchanged request budget.
func initializePostCommitTestDatabase(t *testing.T, dbPath string, contract ir.RuntimeContractID) {
	t.Helper()
	initializeLifecycleTestDatabase(t, dbPath)
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	profile := timeouts.ProductionProfile()
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, tasks.LaneClock{}, tasks.LaneOperationIDs{}, profile)
	require.NoError(t, err)
	setup := service
	setup.Appender.Deadline = profile.WorkflowResult()
	ctx, cancel := context.WithTimeout(t.Context(), profile.WorkflowResult())
	defer cancel()
	_, err = receipt.EnsureActiveMetamodel(ctx, setup)
	require.NoError(t, err, "prepare post-commit fixture metamodel")
	_, err = receipt.EnsureOccurrenceEnvelope(ctx, setup, contract, model.OccurrenceEnvelopeRef{})
	require.NoError(t, err, "prepare post-commit fixture occurrence owners for %s", contract)
	runtimeContract, err := definitions.RuntimeContractForDelivery(contract)
	require.NoError(t, err)
	documents, err := definitions.ActiveForContract(runtimeContract)
	require.NoError(t, err)
	_, err = receipt.EnsureActiveDefinition(ctx, setup, documents.Interpreter)
	require.NoError(t, err, "prepare post-commit fixture interpreter")
	require.Equal(t, profile.Ingress(), service.Appender.Deadline, "setup must not widen the request's ingress budget")
	require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), definitions.EvidenceKind), 5,
		"setup publishes only the delivery's four occurrence owners and interpreter")
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind), "owner setup must not manufacture a receipt")
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind))
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind))
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
	initializePostCommitTestDatabase(t, dbPath, registration.Codex0_153_0().Contract)
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
	require.NotContains(t, stderr.String(), "could not evaluate", "a fail-open continuation write is not evidence of a committed receipt: %s", stderr.String())

	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer tracker.Close()
	occurrences := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	interpreted := queryLifecycleEvidence(t, tracker.Journal(), interpretedEvidenceKind)
	consultation := queryLifecycleEvidence(t, tracker.Journal(), consultationEvidenceKind)
	require.Len(t, occurrences, 1, "durable occurrence evidence must be committed before the failed stdout write")
	require.Len(t, interpreted, 1)
	require.Len(t, consultation, 1)
	require.Len(t, queryLifecycleEvidence(t, tracker.Journal(), definitions.EvidenceKind), 5, "the request must not publish missing cold owners")
	require.Equal(t, occurrences[0].ProducingOperationJournalID, interpreted[0].ProducingOperationJournalID, "the occurrence and interpretation must belong to one durable operation")
	require.Equal(t, interpreted[0].ProducingOperationJournalID, consultation[0].ProducingOperationJournalID, "one durable operation groups interpreted and consultation evidence")
	require.Less(t, interpreted[0].JournalID, consultation[0].JournalID, "interpreted evidence precedes consultation evidence in the committed operation")
}
