package main_test

// CLI-level subprocess tests for the folded epoch/signal/session/slice/phase
// verbs. Each test runs the compiled pasture binary through subprocess calls and
// asserts spec invariants (not implementation snapshots):
//
//   - Validation guards reject bad inputs (exit 1).
//   - Lifecycle verbs (epoch start/cancel) succeed against a fresh database.
//   - Signal/session/slice verbs reach the handler layer and either deliver
//     successfully or fail with the expected exit code.
//
// The query verbs already have comprehensive end-to-end tests in query_test.go;
// this file focuses on the lifecycle + signal surface.

import (
	"encoding/json"
	"strings"
	"testing"
)

// ─── epoch ────────────────────────────────────────────────────────────────────

// TestCLI_EpochHelp verifies the epoch subcommand is registered and has help.
func TestCLI_EpochHelp(t *testing.T) {
	out := runCLI(t, "epoch", "--help")
	if out.exitCode != 0 {
		t.Fatalf("epoch --help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	for _, want := range []string{"start", "cancel"} {
		if !strings.Contains(combined, want) {
			t.Errorf("epoch --help missing %q; got: %s", want, combined)
		}
	}
}

// TestCLI_EpochStart_MissingEpochIdRejectsWithExit1 verifies the required-flag
// guard on epoch start.
func TestCLI_EpochStart_MissingEpochIdRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "epoch", "start")
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --epoch-id; stdout=%s", out.stdout)
	}
}

// TestCLI_EpochStart_MalformedEpochIdRejectsWithExit1 verifies the epoch-id
// validation guard (must be "<namespace>--<uuid>" shape) AND that the
// structured plain-language error report appears on stderr.
//
// The stderr content assertions prove that printError / StructuredError.Report
// wiring is intact for validation errors: a regression that stripped the full
// report to a one-line Error() string would still produce exit 1 but would
// fail these checks.
func TestCLI_EpochStart_MalformedEpochIdRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "epoch", "start", "--epoch-id", "not-a-valid-task-id")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 for malformed epoch id; exit=%d stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	// The full structured error report must reach stderr. Assert on stable section
	// labels that StructuredError.Report always emits, not on exact error text.
	for _, want := range []string{
		"Problem:",    // StructuredError.Report What section label
		"How to fix:", // StructuredError.Report Fix section label
	} {
		if !strings.Contains(out.stderr, want) {
			t.Errorf("stderr missing structured error section %q; stderr=%s", want, out.stderr)
		}
	}
}

// TestCLI_EpochStart_ValidIdSucceeds verifies that a well-formed epoch id
// reaches the handler without error.
func TestCLI_EpochStart_ValidIdSucceeds(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000001"
	out := runCLI(t, "--db", db, "epoch", "start", "--epoch-id", epochId)
	if out.exitCode != 0 {
		t.Fatalf("epoch start exit %d; stdout=%s stderr=%s", out.exitCode, out.stdout, out.stderr)
	}
}

// TestCLI_EpochCancel_MissingEpochIdRejectsWithExit1 verifies the required-flag
// guard on epoch cancel.
func TestCLI_EpochCancel_MissingEpochIdRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "epoch", "cancel")
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --epoch-id; stdout=%s", out.stdout)
	}
}

// TestCLI_EpochTerminate_IsRegistered verifies the terminate subcommand is
// registered and documents its purpose.
func TestCLI_EpochTerminate_IsRegistered(t *testing.T) {
	out := runCLI(t, "epoch", "terminate", "--help")
	if out.exitCode != 0 {
		t.Fatalf("epoch terminate --help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
}

// TestCLI_EpochTerminate_WithReason_RecordsAuditEvent verifies that running
// "epoch terminate --reason <msg>" records an EpochCancelled audit event
// containing the reason in its payload before attempting cancellation.
//
// The epoch used here was never started, so CancelWorkflow returns a workflow
// error (exit 3). We assert exit 3 AND that the audit event was written —
// confirming record-before-cancel order.
func TestCLI_EpochTerminate_WithReason_RecordsAuditEvent(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000201"
	const reason = "test termination reason"

	out := runCLI(t, "--db", db, "epoch", "terminate",
		"--epoch-id", epochId,
		"--reason", reason)
	// Cancel of a nonexistent workflow → exit 3 (workflow error).
	if out.exitCode != 3 {
		t.Fatalf("expected exit 3 for terminate of nonexistent epoch; exit=%d stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}

	// Query the audit trail: the EpochCancelled event must have been written
	// before the cancel was attempted.
	evOut := runCLI(t, "--db", db, "--format", "json",
		"task", "events", "--epoch-id", epochId)
	if evOut.exitCode != 0 {
		t.Fatalf("task events exit %d; stderr=%s", evOut.exitCode, evOut.stderr)
	}
	var events []struct {
		EventType string         `json:"eventType"`
		Payload   map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(evOut.stdout), &events); err != nil {
		t.Fatalf("decode events json: %v\nbody: %s", err, evOut.stdout)
	}
	var found bool
	for _, ev := range events {
		if ev.EventType == "EpochCancelled" {
			if got, ok := ev.Payload["reason"].(string); ok && got == reason {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("expected EpochCancelled event with reason=%q; events: %+v", reason, events)
	}
}

// TestCLI_EpochTerminate_EmptyReason_StillRecordsEvent verifies that omitting
// --reason (empty reason) still writes the EpochCancelled audit event.
func TestCLI_EpochTerminate_EmptyReason_StillRecordsEvent(t *testing.T) {
	db := newDB(t)
	const epochId = "demo--01960000-0000-7000-8000-000000000202"

	out := runCLI(t, "--db", db, "epoch", "terminate",
		"--epoch-id", epochId)
	// Cancel of a nonexistent workflow → exit 3 (workflow error).
	if out.exitCode != 3 {
		t.Fatalf("expected exit 3 for terminate of nonexistent epoch; exit=%d stderr=%s",
			out.exitCode, out.stderr)
	}

	// The event must exist even with no reason supplied.
	evOut := runCLI(t, "--db", db, "--format", "json",
		"task", "events", "--epoch-id", epochId)
	if evOut.exitCode != 0 {
		t.Fatalf("task events exit %d; stderr=%s", evOut.exitCode, evOut.stderr)
	}
	var events []struct {
		EventType string `json:"eventType"`
	}
	if err := json.Unmarshal([]byte(evOut.stdout), &events); err != nil {
		t.Fatalf("decode events json: %v\nbody: %s", err, evOut.stdout)
	}
	var found bool
	for _, ev := range events {
		if ev.EventType == "EpochCancelled" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected EpochCancelled event even with empty reason; events: %+v", events)
	}
}

// ─── phase ────────────────────────────────────────────────────────────────────

// TestCLI_PhaseHelp verifies the phase subcommand is registered.
func TestCLI_PhaseHelp(t *testing.T) {
	out := runCLI(t, "phase", "--help")
	if out.exitCode != 0 {
		t.Fatalf("phase --help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stdout+out.stderr, "advance") {
		t.Errorf("phase --help missing 'advance'; got: %s", out.stdout+out.stderr)
	}
}

// TestCLI_PhaseAdvance_MissingFlagsRejectWithExit1 verifies the required-flag
// guards.
func TestCLI_PhaseAdvance_MissingFlagsRejectWithExit1(t *testing.T) {
	db := newDB(t)
	// missing both
	out := runCLI(t, "--db", db, "phase", "advance")
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing flags; stdout=%s", out.stdout)
	}
	// missing --to
	out2 := runCLI(t, "--db", db, "phase", "advance", "--epoch-id", "demo--abc")
	if out2.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --to; stdout=%s", out2.stdout)
	}
}

// TestCLI_PhaseAdvance_BadPhaseRejectsWithExit1 verifies the invalid-phase
// validation guard at the CLI boundary.
func TestCLI_PhaseAdvance_BadPhaseRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "phase", "advance",
		"--epoch-id", "demo--01960000-0000-7000-8000-000000000002",
		"--to", "not-a-real-phase")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 for bad --to; exit=%d stderr=%s", out.exitCode, out.stderr)
	}
}

// ─── signal ───────────────────────────────────────────────────────────────────

// TestCLI_SignalHelp verifies the signal subcommand is registered.
func TestCLI_SignalHelp(t *testing.T) {
	out := runCLI(t, "signal", "--help")
	if out.exitCode != 0 {
		t.Fatalf("signal --help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	for _, want := range []string{"vote", "complete"} {
		if !strings.Contains(combined, want) {
			t.Errorf("signal --help missing %q; got: %s", want, combined)
		}
	}
}

// TestCLI_SignalVote_MissingRequiredFlagsRejectWithExit1 checks the required
// flag guards for signal vote.
func TestCLI_SignalVote_MissingRequiredFlagsRejectWithExit1(t *testing.T) {
	db := newDB(t)
	// Missing --axis and --vote
	out := runCLI(t, "--db", db, "signal", "vote",
		"--epoch-id", "demo--01960000-0000-7000-8000-000000000003")
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --axis/--vote; stdout=%s", out.stdout)
	}
}

// TestCLI_SignalVote_BadAxisRejectsWithExit1 proves axis validation at the CLI
// boundary (note: cobra required-flag check fires first, so we pass dummy
// values for --vote to reach the axis check).
func TestCLI_SignalVote_BadAxisRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "signal", "vote",
		"--epoch-id", "demo--01960000-0000-7000-8000-000000000004",
		"--axis", "badaxis",
		"--vote", "ACCEPT")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 for bad --axis; exit=%d stderr=%s", out.exitCode, out.stderr)
	}
}

// TestCLI_SignalVote_BadVoteRejectsWithExit1 proves vote value validation.
func TestCLI_SignalVote_BadVoteRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "signal", "vote",
		"--epoch-id", "demo--01960000-0000-7000-8000-000000000005",
		"--axis", "correctness",
		"--vote", "MAYBE")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 for bad --vote; exit=%d stderr=%s", out.exitCode, out.stderr)
	}
}

// TestCLI_SignalComplete_MissingEpochOrSliceRejectsWithExit1 checks required
// flags for signal complete.
func TestCLI_SignalComplete_MissingEpochOrSliceRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	// Missing --slice-id
	out := runCLI(t, "--db", db, "signal", "complete",
		"--epoch-id", "demo--01960000-0000-7000-8000-000000000006")
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --slice-id; stdout=%s", out.stdout)
	}
}

// ─── session ──────────────────────────────────────────────────────────────────

// TestCLI_SessionHelp verifies the session subcommand is registered.
func TestCLI_SessionHelp(t *testing.T) {
	out := runCLI(t, "session", "--help")
	if out.exitCode != 0 {
		t.Fatalf("session --help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	if !strings.Contains(out.stdout+out.stderr, "register") {
		t.Errorf("session --help missing 'register'; got: %s", out.stdout+out.stderr)
	}
}

// TestCLI_SessionRegister_MissingRequiredFlagsRejectWithExit1 checks required
// flag guards for session register.
func TestCLI_SessionRegister_MissingRequiredFlagsRejectWithExit1(t *testing.T) {
	db := newDB(t)
	// Missing --session-id and --role
	out := runCLI(t, "--db", db, "session", "register",
		"--epoch-id", "demo--01960000-0000-7000-8000-000000000007")
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --session-id/--role; stdout=%s", out.stdout)
	}
}

// ─── slice ────────────────────────────────────────────────────────────────────

// TestCLI_SliceHelp verifies the slice subcommand is registered.
func TestCLI_SliceHelp(t *testing.T) {
	out := runCLI(t, "slice", "--help")
	if out.exitCode != 0 {
		t.Fatalf("slice --help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	for _, want := range []string{"start", "complete"} {
		if !strings.Contains(combined, want) {
			t.Errorf("slice --help missing %q; got: %s", want, combined)
		}
	}
}

// TestCLI_SliceStart_MissingRequiredFlagsRejectWithExit1 checks required flag
// guards for slice start.
func TestCLI_SliceStart_MissingRequiredFlagsRejectWithExit1(t *testing.T) {
	db := newDB(t)
	// Missing --mode
	out := runCLI(t, "--db", db, "slice", "start", "--slice-id", "s-1")
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --mode; stdout=%s", out.stdout)
	}
	// Missing --slice-id
	out2 := runCLI(t, "--db", db, "slice", "start", "--mode", "mock")
	if out2.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --slice-id; stdout=%s", out2.stdout)
	}
}

// TestCLI_SliceStart_BadModeRejectsWithExit1 verifies the mode allow-list at
// the CLI boundary (note: a DBOS-unaddressable slice-id is expected to produce
// exit 3, so we need a db with a running workflow; for mode validation alone,
// the validation fires before the network call).
func TestCLI_SliceStart_BadModeRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "slice", "start",
		"--slice-id", "demo--01960000-0000-7000-8000-000000000008",
		"--mode", "docker")
	if out.exitCode != 1 {
		t.Fatalf("expected exit 1 for bad --mode; exit=%d stderr=%s", out.exitCode, out.stderr)
	}
}

// TestCLI_SliceComplete_MissingSliceIdRejectsWithExit1 checks required flag
// guard for slice complete.
func TestCLI_SliceComplete_MissingSliceIdRejectsWithExit1(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "slice", "complete")
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for missing --slice-id; stdout=%s", out.stdout)
	}
}

// ─── workflow-error → exit 3 ──────────────────────────────────────────────────

// TestCLI_EpochCancel_WorkflowError_NonexistentEpoch verifies that cancelling
// an epoch that was never started returns exit 3 (CategoryWorkflow) AND that
// the structured plain-language error report appears on stderr.
//
// The stderr content assertion proves that printError / StructuredError.Report
// wiring is intact: a regression that stripped the full report to a one-line
// Error() string would still produce exit 3 but would fail this check.
func TestCLI_EpochCancel_WorkflowError_NonexistentEpoch(t *testing.T) {
	db := newDB(t)
	out := runCLI(t, "--db", db, "epoch", "cancel",
		"--epoch-id", "demo--01960000-0000-7000-8000-000000000099")
	if out.exitCode != 3 {
		t.Fatalf("expected exit 3 for cancel of nonexistent epoch; exit=%d stdout=%s stderr=%s",
			out.exitCode, out.stdout, out.stderr)
	}
	// The full structured error report must reach stderr.
	for _, want := range []string{
		"Problem:",    // StructuredError.Report What section label
		"How to fix:", // StructuredError.Report Fix section label
	} {
		if !strings.Contains(out.stderr, want) {
			t.Errorf("stderr missing structured error section %q; stderr=%s", want, out.stderr)
		}
	}
}

// ─── help surface ─────────────────────────────────────────────────────────────

// TestCLI_TopLevelHelp_RegistersAllNewSubcommands verifies that the folded
// verbs (epoch, phase, signal, session, slice, query) all appear in the top-
// level help output.
func TestCLI_TopLevelHelp_RegistersAllNewSubcommands(t *testing.T) {
	out := runCLI(t, "--help")
	if out.exitCode != 0 {
		t.Fatalf("--help exit %d; stderr=%s", out.exitCode, out.stderr)
	}
	combined := out.stdout + out.stderr
	for _, want := range []string{"epoch", "phase", "signal", "session", "slice", "query", "migrate"} {
		if !strings.Contains(combined, want) {
			t.Errorf("top-level help missing %q; got: %s", want, combined)
		}
	}
}
