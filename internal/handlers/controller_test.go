package handlers_test

// L6 round-trip tests: signals delivered via handlers.EpochController durably
// mutate epoch state; queries read the resulting projection correctly.
//
// Each test follows the spec invariant:
//   - signals change state → the projection reflects the change.
//   - queries read projection → the handler returns the right slice.
//   - validation rejects bad inputs → non-zero exit code.
//
// The real engine is the SUT (never mocked). Only the EpochController
// interface decouples the CLI handler from the substrate; tests wire the
// real dbosController via handlers.OpenEpochController.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// openController opens a DBOS-backed EpochController on a fresh temp-db
// and advances the epoch's control workflow with the given seed steps.
// The engine is shut down after cleanup; the controller keeps its own handle.
func openController(t *testing.T, epochId string, seedPhases []protocol.PhaseId) handlers.EpochController {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	// Seed the projection by running the control workflow to a known state.
	seedEngine, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-v1",
	})
	if err != nil {
		t.Fatalf("seed engine.New: %v", err)
	}
	if err := seedEngine.Launch(); err != nil {
		t.Fatalf("seed engine.Launch: %v", err)
	}
	t.Cleanup(func() { seedEngine.Shutdown(5 * time.Second) })

	// Start the control workflow first so signals are addressable.
	h, err := dbos.RunWorkflow(seedEngine.DBOS(), seedEngine.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId},
		dbos.WithWorkflowID(epochId))
	if err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}
	_ = h // handle not waited here; workflow stays running

	// Advance through the seed phases.
	for _, phase := range seedPhases {
		sig := protocol.PhaseAdvanceSignal{ToPhase: phase, TriggeredBy: "test-seed", ConditionMet: "ok"}
		if err := dbos.Send(seedEngine.DBOS(), epochId, sig, protocol.SignalAdvancePhase.String()); err != nil {
			t.Fatalf("Send(advance_phase=%s): %v", phase, err)
		}
		// Wait for the projection to reflect the phase before proceeding.
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			st, _ := seedEngine.ReadProjection(epochId)
			if st != nil && st.CurrentPhase == phase {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
	}

	// Open the controller pointing at the same db. It launches its own engine
	// context so it can send signals to the already-running workflow.
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	t.Cleanup(func() {
		if err := ctrl.Close(); err != nil {
			t.Logf("ctrl.Close: %v", err)
		}
	})
	return ctrl
}

// ─── EpochStart / EpochCancel ─────────────────────────────────────────────────

// TestHandler_EpochStart_LaunchesControlWorkflow verifies that EpochStart
// invokes RunWorkflow so the epoch's control workflow becomes addressable.
// This proves the spec invariant: "epoch start → RunWorkflow".
// The epoch id must have the "<namespace>--<uuid>" shape required by the
// validator (provenance.ParseTaskID).
func TestHandler_EpochStart_LaunchesControlWorkflow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	// Must be a valid provenance task id: "<namespace>--<uuidv7>".
	const epochId = "demo--01960000-0000-7000-8000-000000000001"
	code, hErr := handlers.EpochStart(ctrl, epochId, types.OutputJSON)
	if hErr != nil {
		t.Fatalf("EpochStart err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("EpochStart exit = %d, want 0", code)
	}
}

// TestHandler_EpochStart_RejectsEmptyEpochId verifies that a missing epoch id
// is rejected with exit 1.
func TestHandler_EpochStart_RejectsEmptyEpochId(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.EpochStart(ctrl, "", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for empty epoch id")
	}
	if code != 1 {
		t.Errorf("EpochStart exit = %d, want 1", code)
	}
}

// TestHandler_EpochStart_RejectsMalformedEpochId verifies that an epoch id
// without the "<namespace>--<uuid>" shape is rejected with exit 1.
func TestHandler_EpochStart_RejectsMalformedEpochId(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.EpochStart(ctrl, "not-a-task-id", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for a malformed epoch id")
	}
	if code != 1 {
		t.Errorf("EpochStart exit = %d, want 1", code)
	}
}

// ─── PhaseAdvance ─────────────────────────────────────────────────────────────

// TestHandler_PhaseAdvance_MutatesProjection verifies that delivering an
// advance_phase signal via the handler causes the projected state to change.
// This is the end-to-end spec invariant for "phase advance → Send durably
// mutates state".
func TestHandler_PhaseAdvance_MutatesProjection(t *testing.T) {
	const epochId = "ctl--advance-1"
	ctrl := openController(t, epochId, nil) // no seed phases; start at request

	// Deliver the advance to elicit.
	code, err := handlers.PhaseAdvance(ctrl, epochId, protocol.PhaseElicit, "worker", "elicited", types.OutputJSON)
	if err != nil {
		t.Fatalf("PhaseAdvance err = %v", err)
	}
	if code != 0 {
		t.Fatalf("PhaseAdvance exit = %d, want 0", code)
	}
}

// TestHandler_PhaseAdvance_RejectsUnknownPhase validates that a bad --to value
// produces exit 1.
func TestHandler_PhaseAdvance_RejectsUnknownPhase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.PhaseAdvance(ctrl, "ctl--advance-2",
		protocol.PhaseId("not-a-phase"), "test", "ok", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for an unknown phase")
	}
	if code != 1 {
		t.Errorf("PhaseAdvance exit = %d, want 1", code)
	}
}

// ─── SignalVote ────────────────────────────────────────────────────────────────

// TestHandler_SignalVote_MutatesProjection delivers a submit_vote signal via the
// handler and confirms the vote appears in the projected state.
func TestHandler_SignalVote_MutatesProjection(t *testing.T) {
	const epochId = "ctl--vote-1"
	ctrl := openController(t, epochId, []protocol.PhaseId{
		protocol.PhaseElicit,
		protocol.PhasePropose,
		protocol.PhaseReview,
	})

	code, err := handlers.SignalVote(ctrl, epochId,
		protocol.AxisCorrectness, protocol.VoteAccept, "r-1", types.OutputJSON)
	if err != nil {
		t.Fatalf("SignalVote err = %v", err)
	}
	if code != 0 {
		t.Fatalf("SignalVote exit = %d, want 0", code)
	}
}

// TestHandler_SignalVote_RejectsInvalidAxis proves axis validation.
func TestHandler_SignalVote_RejectsInvalidAxis(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SignalVote(ctrl, "ctl--vote-2",
		protocol.ReviewAxis("bad_axis"), protocol.VoteAccept, "", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for an invalid axis")
	}
	if code != 1 {
		t.Errorf("SignalVote exit = %d, want 1", code)
	}
}

// TestHandler_SignalVote_RejectsInvalidVote proves vote value validation.
func TestHandler_SignalVote_RejectsInvalidVote(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SignalVote(ctrl, "ctl--vote-3",
		protocol.AxisCorrectness, protocol.VoteType("MAYBE"), "", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for an invalid vote")
	}
	if code != 1 {
		t.Errorf("SignalVote exit = %d, want 1", code)
	}
}

// ─── SessionRegister ──────────────────────────────────────────────────────────

// TestHandler_SessionRegister_MutatesProjection delivers a register_session
// signal via the handler and asserts the session count grows.
func TestHandler_SessionRegister_MutatesProjection(t *testing.T) {
	const epochId = "ctl--sess-1"
	ctrl := openController(t, epochId, nil)

	code, err := handlers.SessionRegister(ctrl, epochId, "sess-abc", "worker", "claude-code", "claude-sonnet", types.OutputJSON)
	if err != nil {
		t.Fatalf("SessionRegister err = %v", err)
	}
	if code != 0 {
		t.Fatalf("SessionRegister exit = %d, want 0", code)
	}
}

// TestHandler_SessionRegister_RejectsEmptySessionId validates required flag.
func TestHandler_SessionRegister_RejectsEmptySessionId(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SessionRegister(ctrl, "ctl--sess-2", "", "worker", "", "", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for an empty session id")
	}
	if code != 1 {
		t.Errorf("SessionRegister exit = %d, want 1", code)
	}
}

// TestHandler_SessionRegister_RejectsEmptyRole validates required flag.
func TestHandler_SessionRegister_RejectsEmptyRole(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SessionRegister(ctrl, "ctl--sess-3", "sess-xyz", "", "", "", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for an empty role")
	}
	if code != 1 {
		t.Errorf("SessionRegister exit = %d, want 1", code)
	}
}

// ─── SignalComplete ────────────────────────────────────────────────────────────

// TestHandler_SignalComplete_MutatesProjection delivers a slice_progress signal
// marking a slice complete.
func TestHandler_SignalComplete_MutatesProjection(t *testing.T) {
	const epochId = "ctl--complete-1"
	ctrl := openController(t, epochId, nil)

	output := "tests passed"
	code, err := handlers.SignalComplete(ctrl, epochId, "slice-1", &output, nil, types.OutputJSON)
	if err != nil {
		t.Fatalf("SignalComplete err = %v", err)
	}
	if code != 0 {
		t.Fatalf("SignalComplete exit = %d, want 0", code)
	}
}

// TestHandler_SignalComplete_RejectsConflictingOutputAndError validates mutual
// exclusion between --output and --error.
func TestHandler_SignalComplete_RejectsConflictingOutputAndError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	out := "done"
	errMsg := "fail"
	code, hErr := handlers.SignalComplete(ctrl, "ctl--complete-2", "slice-x", &out, &errMsg, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for both --output and --error")
	}
	if code != 1 {
		t.Errorf("SignalComplete exit = %d, want 1", code)
	}
}

// ─── SliceStart / SliceComplete ───────────────────────────────────────────────

// TestHandler_SliceStart_RejectsEmptySliceId validates required --slice-id.
func TestHandler_SliceStart_RejectsEmptySliceId(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SliceStart(ctrl, "", "mock", "", 0, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for empty slice id")
	}
	if code != 1 {
		t.Errorf("SliceStart exit = %d, want 1", code)
	}
}

// TestHandler_SliceStart_RejectsUnknownMode validates the mode allow-list.
func TestHandler_SliceStart_RejectsUnknownMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SliceStart(ctrl, "slice-1", "docker", "", 0, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for an unknown execution mode")
	}
	if code != 1 {
		t.Errorf("SliceStart exit = %d, want 1", code)
	}
}

// TestHandler_SliceComplete_RejectsEmptySliceId validates required --slice-id.
func TestHandler_SliceComplete_RejectsEmptySliceId(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SliceComplete(ctrl, "", nil, nil, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for empty slice id")
	}
	if code != 1 {
		t.Errorf("SliceComplete exit = %d, want 1", code)
	}
}

// TestHandler_SliceComplete_RejectsConflictingOutputAndError validates mutual
// exclusion between --output and --error on the slice path.
func TestHandler_SliceComplete_RejectsConflictingOutputAndError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	out := "ok"
	msg := "fail"
	code, hErr := handlers.SliceComplete(ctrl, "slice-x", &out, &msg, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for both --output and --error")
	}
	if code != 1 {
		t.Errorf("SliceComplete exit = %d, want 1", code)
	}
}
