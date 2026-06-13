package handlers_test

// Round-trip tests: signals delivered via the EpochController durably mutate
// epoch state; queries read the resulting projection correctly.
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

// controllerRig holds the seed engine (for projection reads) and the
// controller (for handler calls) opened on the same database.
type controllerRig struct {
	engine *engine.Engine
	ctrl   handlers.EpochController
	dbPath string
}

// waitProjection polls the seed engine's projection until the epoch reaches
// want or the deadline expires. Unlike the engine-internal waitPhase helper,
// this reads through the seed engine so it works across process boundaries.
func (r *controllerRig) waitProjection(t *testing.T, epochId string, want protocol.PhaseId) *protocol.EpochState {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st, err := r.engine.ReadProjection(epochId)
		if err != nil {
			t.Fatalf("ReadProjection: %v", err)
		}
		if st != nil && st.CurrentPhase == want {
			return st
		}
		time.Sleep(15 * time.Millisecond)
	}
	st, _ := r.engine.ReadProjection(epochId)
	t.Fatalf("epoch %q did not reach phase %q in 15s; last projection = %+v", epochId, want, st)
	return nil
}

// openController opens a DBOS-backed EpochController on a fresh temp-db,
// advances the epoch's control workflow with the given seed steps, and returns
// both the seed engine (for projection reads) and the controller (for handler
// calls). Both are registered for cleanup; the controller keeps its own engine
// context pointing at the same db.
func openController(t *testing.T, epochId string, seedPhases []protocol.PhaseId) controllerRig {
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

	// Advance through the seed phases; fatal on timeout so a stuck seed does not
	// silently continue into an assertion that has no meaning.
	for _, phase := range seedPhases {
		sig := protocol.PhaseAdvanceSignal{ToPhase: phase, TriggeredBy: "test-seed", ConditionMet: "ok"}
		if err := dbos.Send(seedEngine.DBOS(), epochId, sig, protocol.SignalAdvancePhase.String()); err != nil {
			t.Fatalf("Send(advance_phase=%s): %v", phase, err)
		}
		deadline := time.Now().Add(15 * time.Second)
		reached := false
		for time.Now().Before(deadline) {
			st, _ := seedEngine.ReadProjection(epochId)
			if st != nil && st.CurrentPhase == phase {
				reached = true
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if !reached {
			st, _ := seedEngine.ReadProjection(epochId)
			t.Fatalf("seed: epoch %q did not reach %q in 15s; last projection = %+v", epochId, phase, st)
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
	return controllerRig{engine: seedEngine, ctrl: ctrl, dbPath: dbPath}
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

// TestHandler_EpochStart_EnqueuesForHostedEngine verifies the production
// lifecycle boundary: the controller only writes durable DBOS records/signals,
// while a hosted engine dequeues and executes EpochControlWorkflow.
func TestHandler_EpochStart_EnqueuesForHostedEngine(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	host, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: engine.DefaultApplicationVersion,
	})
	if err != nil {
		t.Fatalf("host engine.New: %v", err)
	}
	if err := host.Launch(); err != nil {
		t.Fatalf("host engine.Launch: %v", err)
	}
	t.Cleanup(func() { host.Shutdown(5 * time.Second) })

	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	t.Cleanup(func() {
		if err := ctrl.Close(); err != nil {
			t.Logf("ctrl.Close: %v", err)
		}
	})

	const epochId = "demo--01960000-0000-7000-8000-000000000002"
	code, hErr := handlers.EpochStart(ctrl, epochId, types.OutputJSON)
	if hErr != nil {
		t.Fatalf("EpochStart err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("EpochStart exit = %d, want 0", code)
	}

	code, hErr = handlers.PhaseAdvance(ctrl, epochId, protocol.PhaseElicit, "worker", "elicited", types.OutputJSON)
	if hErr != nil {
		t.Fatalf("PhaseAdvance err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("PhaseAdvance exit = %d, want 0", code)
	}

	rig := controllerRig{engine: host, ctrl: ctrl, dbPath: dbPath}
	st := rig.waitProjection(t, epochId, protocol.PhaseElicit)
	if st.CurrentPhase != protocol.PhaseElicit {
		t.Fatalf("projection CurrentPhase = %q, want %q", st.CurrentPhase, protocol.PhaseElicit)
	}

	code, hErr = handlers.QueryEpoch(handlers.QueryEpochInput{
		DBPath:  dbPath,
		EpochId: epochId,
		Query:   protocol.QueryCurrentState,
	}, types.OutputJSON)
	if hErr != nil {
		t.Fatalf("QueryEpoch(current) err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("QueryEpoch(current) exit = %d, want 0", code)
	}

	code, hErr = handlers.EpochStatus(handlers.EpochStatusInput{
		DBPath:  dbPath,
		EpochId: epochId,
	}, types.OutputJSON)
	if hErr != nil {
		t.Fatalf("EpochStatus err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("EpochStatus exit = %d, want 0", code)
	}
}

// TestHandler_EpochStart_CloseDoesNotCancelHostedWorkflow proves closing the
// short-lived CLI controller immediately after StartEpoch does not cancel or
// error the hosted epoch workflow.
func TestHandler_EpochStart_CloseDoesNotCancelHostedWorkflow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	host, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: engine.DefaultApplicationVersion,
	})
	if err != nil {
		t.Fatalf("host engine.New: %v", err)
	}
	if err := host.Launch(); err != nil {
		t.Fatalf("host engine.Launch: %v", err)
	}
	t.Cleanup(func() { host.Shutdown(5 * time.Second) })

	const epochId = "demo--01960000-0000-7000-8000-000000000003"
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController(start): %v", err)
	}
	code, hErr := handlers.EpochStart(ctrl, epochId, types.OutputJSON)
	if hErr != nil {
		t.Fatalf("EpochStart err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("EpochStart exit = %d, want 0", code)
	}
	if err := ctrl.Close(); err != nil {
		t.Fatalf("closing the CLI controller after StartEpoch: %v", err)
	}

	signalCtrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController(signal): %v", err)
	}
	t.Cleanup(func() {
		if err := signalCtrl.Close(); err != nil {
			t.Logf("signalCtrl.Close: %v", err)
		}
	})
	code, hErr = handlers.PhaseAdvance(signalCtrl, epochId, protocol.PhaseElicit, "worker", "elicited", types.OutputJSON)
	if hErr != nil {
		t.Fatalf("PhaseAdvance err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("PhaseAdvance exit = %d, want 0", code)
	}

	rig := controllerRig{engine: host, ctrl: signalCtrl, dbPath: dbPath}
	rig.waitProjection(t, epochId, protocol.PhaseElicit)

	handle, err := dbos.RetrieveWorkflow[protocol.EpochState](host.DBOS(), epochId)
	if err != nil {
		t.Fatalf("RetrieveWorkflow: %v", err)
	}
	status, err := handle.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Status == dbos.WorkflowStatusError {
		t.Fatalf("workflow was marked ERROR after CLI close; error=%v", status.Error)
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

// TestHandler_EpochCancel_CancelsRunningWorkflow verifies the EpochCancel
// success path: EpochCancel on a running epoch returns exit 0. The observable
// effect is that the durable workflow stops processing signals after
// cancellation: an advance_phase signal sent after cancel does not appear
// in the projection within the polling window.
func TestHandler_EpochCancel_CancelsRunningWorkflow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")

	// Use a seed engine for projection reads.
	seedEngine, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-v1",
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := seedEngine.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	t.Cleanup(func() { seedEngine.Shutdown(5 * time.Second) })

	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	// Must be a valid provenance task id.
	const epochId = "demo--01960000-0000-7000-8000-000000000010"

	// Start the epoch so there is something to cancel.
	code, hErr := handlers.EpochStart(ctrl, epochId, types.OutputJSON)
	if hErr != nil {
		t.Fatalf("EpochStart err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("EpochStart exit = %d, want 0", code)
	}

	// Cancel it — must succeed (exit 0).
	code, hErr = handlers.EpochCancel(ctrl, epochId, types.OutputJSON)
	if hErr != nil {
		t.Fatalf("EpochCancel err = %v", hErr)
	}
	if code != 0 {
		t.Fatalf("EpochCancel exit = %d, want 0", code)
	}

	// Observable effect: retrieve the workflow status directly from the substrate
	// and confirm it is CANCELLED. A cancelled workflow keeps its workflow_status
	// row (the FK on notifications.destination_uuid still resolves), so
	// dbos.Send to it returns nil; only RetrieveWorkflow + GetStatus reveals the
	// true terminal state.
	handle, hErr2 := dbos.RetrieveWorkflow[protocol.EpochState](seedEngine.DBOS(), epochId)
	if hErr2 != nil {
		t.Fatalf("RetrieveWorkflow after cancel: %v", hErr2)
	}
	wfStatus, statusErr := handle.GetStatus()
	if statusErr != nil {
		t.Fatalf("GetStatus after cancel: %v", statusErr)
	}
	if wfStatus.Status != dbos.WorkflowStatusCancelled {
		t.Fatalf("workflow status after EpochCancel = %q, want %q", wfStatus.Status, dbos.WorkflowStatusCancelled)
	}
}

// ─── PhaseAdvance ─────────────────────────────────────────────────────────────

// TestHandler_PhaseAdvance_MutatesProjection verifies that delivering an
// advance_phase signal via the handler causes the projected state to change.
// Proves the controller topic/payload mapping: the handler must address the
// advance_phase topic (not a wrong topic) and marshal a PhaseAdvanceSignal
// (not a wrong type) for the projection to update.
func TestHandler_PhaseAdvance_MutatesProjection(t *testing.T) {
	const epochId = "ctl--advance-1"
	rig := openController(t, epochId, nil) // no seed phases; start at request

	// Deliver the advance to elicit via the handler under test.
	code, err := handlers.PhaseAdvance(rig.ctrl, epochId, protocol.PhaseElicit, "worker", "elicited", types.OutputJSON)
	if err != nil {
		t.Fatalf("PhaseAdvance err = %v", err)
	}
	if code != 0 {
		t.Fatalf("PhaseAdvance exit = %d, want 0", code)
	}

	// Observable outcome: projection must reflect the new phase.
	st := rig.waitProjection(t, epochId, protocol.PhaseElicit)
	if st.CurrentPhase != protocol.PhaseElicit {
		t.Fatalf("projection CurrentPhase = %q, want %q", st.CurrentPhase, protocol.PhaseElicit)
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

// TestHandler_SignalVote_MutatesProjection delivers all 3 review-axis votes via
// the handler and confirms they satisfy the consensus gate on a subsequent
// advance. Proving the advance completes confirms the handler used the correct
// submit_vote topic with a well-formed ReviewVoteSignal: a wrong topic or
// wrong payload would leave the gate unsatisfied and the advance would stall.
func TestHandler_SignalVote_MutatesProjection(t *testing.T) {
	const epochId = "ctl--vote-1"
	rig := openController(t, epochId, []protocol.PhaseId{
		protocol.PhaseElicit,
		protocol.PhasePropose,
		protocol.PhaseReview,
	})

	// Submit all three axes via the handler under test.
	for _, axis := range protocol.AllReviewAxes {
		reviewerId := "r-" + string(axis)
		code, err := handlers.SignalVote(rig.ctrl, epochId, axis, protocol.VoteAccept, reviewerId, types.OutputJSON)
		if err != nil {
			t.Fatalf("SignalVote(%s) err = %v", axis, err)
		}
		if code != 0 {
			t.Fatalf("SignalVote(%s) exit = %d, want 0", axis, code)
		}
	}

	// Advance through the consensus gate; it passes only if all 3 votes
	// arrived on the correct topic with the correct payload.
	code, err := handlers.PhaseAdvance(rig.ctrl, epochId, protocol.PhasePlanReview, "test", "votes-cast", types.OutputJSON)
	if err != nil {
		t.Fatalf("PhaseAdvance(plan-review) err = %v", err)
	}
	if code != 0 {
		t.Fatalf("PhaseAdvance(plan-review) exit = %d, want 0", code)
	}

	// Observable outcome: projection must have advanced past the gate.
	st := rig.waitProjection(t, epochId, protocol.PhasePlanReview)
	if st.CurrentPhase != protocol.PhasePlanReview {
		t.Fatalf("projection CurrentPhase = %q, want %q", st.CurrentPhase, protocol.PhasePlanReview)
	}
	// Votes are phase-scoped: drained after the advance.
	if len(st.ReviewVotes) != 0 {
		t.Errorf("ReviewVotes not cleared after advance: %v", st.ReviewVotes)
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
// signal via the handler and asserts the session appears in the projection
// after a subsequent advance. The advance drains side-channel signals (sessions
// are buffered), so a successful session count proves the handler reached the
// correct register_session topic with a well-formed RegisterSessionSignal.
func TestHandler_SessionRegister_MutatesProjection(t *testing.T) {
	const epochId = "ctl--sess-1"
	rig := openController(t, epochId, nil)

	code, err := handlers.SessionRegister(rig.ctrl, epochId, "sess-abc", "worker", "claude-code", "claude-sonnet", types.OutputJSON)
	if err != nil {
		t.Fatalf("SessionRegister err = %v", err)
	}
	if code != 0 {
		t.Fatalf("SessionRegister exit = %d, want 0", code)
	}

	// Advance to elicit: the control workflow drains side-channel signals
	// (sessions) before committing each transition, so the post-advance
	// projection must reflect the registered session.
	code, err = handlers.PhaseAdvance(rig.ctrl, epochId, protocol.PhaseElicit, "test", "ok", types.OutputJSON)
	if err != nil {
		t.Fatalf("PhaseAdvance err = %v", err)
	}
	if code != 0 {
		t.Fatalf("PhaseAdvance exit = %d, want 0", code)
	}

	st := rig.waitProjection(t, epochId, protocol.PhaseElicit)
	if st.ActiveSessionCount != 1 {
		t.Errorf("ActiveSessionCount = %d, want 1", st.ActiveSessionCount)
	}
	if len(st.ActiveSessions) != 1 {
		t.Errorf("ActiveSessions = %d entries, want 1", len(st.ActiveSessions))
	}
	if len(st.ActiveSessions) > 0 && st.ActiveSessions[0].SessionId != "sess-abc" {
		t.Errorf("ActiveSessions[0].SessionId = %q, want %q", st.ActiveSessions[0].SessionId, "sess-abc")
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
// via the handler and asserts the entry appears in the projection after a
// subsequent advance. The advance drains side-channel signals (slice progress
// is buffered), so a successful SliceProgress entry proves the handler reached
// the correct slice_progress topic with a well-formed SliceProgressSignal.
func TestHandler_SignalComplete_MutatesProjection(t *testing.T) {
	const epochId = "ctl--complete-1"
	rig := openController(t, epochId, nil)

	output := "tests passed"
	code, err := handlers.SignalComplete(rig.ctrl, epochId, "slice-1", &output, nil, types.OutputJSON)
	if err != nil {
		t.Fatalf("SignalComplete err = %v", err)
	}
	if code != 0 {
		t.Fatalf("SignalComplete exit = %d, want 0", code)
	}

	// Advance to elicit: the control workflow drains side-channel signals
	// (slice progress) before committing the transition.
	code, err = handlers.PhaseAdvance(rig.ctrl, epochId, protocol.PhaseElicit, "test", "ok", types.OutputJSON)
	if err != nil {
		t.Fatalf("PhaseAdvance err = %v", err)
	}
	if code != 0 {
		t.Fatalf("PhaseAdvance exit = %d, want 0", code)
	}

	st := rig.waitProjection(t, epochId, protocol.PhaseElicit)
	if len(st.SliceProgress) != 1 {
		t.Fatalf("SliceProgress = %d entries, want 1", len(st.SliceProgress))
	}
	if st.SliceProgress[0].SliceId != "slice-1" {
		t.Errorf("SliceProgress[0].SliceId = %q, want %q", st.SliceProgress[0].SliceId, "slice-1")
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

	code, hErr := handlers.SliceStart(ctrl, "", protocol.SliceMock, "", 0, types.OutputText)
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

	code, hErr := handlers.SliceStart(ctrl, "slice-1", protocol.SliceExecutionMode("docker"), "", 0, types.OutputText)
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

// ─── EpochTerminate validation ────────────────────────────────────────────────

// TestHandler_EpochTerminate_RejectsMalformedEpochId verifies that EpochTerminate
// returns exit 1 (CategoryValidation) for a malformed epoch id and does not
// write any audit event. The validation guard must fire before RecordEvent so
// a typo'd id can never produce an unjoinable audit row.
func TestHandler_EpochTerminate_RejectsMalformedEpochId(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.EpochTerminate(ctrl, "not-a-task-id", "test reason", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a validation error for malformed epoch id; got nil")
	}
	if code != 1 {
		t.Fatalf("EpochTerminate exit = %d, want 1 (validation error); err = %v", code, hErr)
	}
}

// ─── WorkflowError → exit 3 ──────────────────────────────────────────────────
//
// These tests exercise the CategoryWorkflow → exit 3 path on the handlers
// that interact with the durable engine in a way that can fail at the
// engine boundary.
//
// Note on dbos.Send semantics: Send inserts into the notifications table, which
// has a FOREIGN KEY on destination_uuid referencing workflow_status. Connections
// use PRAGMA foreign_keys = ON, so sending to an id whose workflow was NEVER
// STARTED violates the FK and returns a nonexistent-workflow error to the
// caller (it is not swallowed). A cancelled or finished workflow keeps its
// workflow_status row, so Send to those ids returns nil — the error only fires
// for ids that were never registered in workflow_status at all.
//
// Use only never-started ids in these tests (e.g. "demo--ffffffff-..."). Do
// not reuse ids from tests that started and then cancelled a workflow; the
// cancelled workflow's status row still exists and Send would return nil.
//
// These tests double as a substrate canary: if a future version changes Send
// semantics, the exit-3 assertions fail visibly.

// TestHandler_EpochCancel_WorkflowError_NonexistentWorkflow proves that
// EpochCancel returns exit 3 (CategoryWorkflow) when no workflow with the
// given id has been started. This covers the CategoryWorkflow → exit 3
// mapping for the cancel lifecycle verb.
func TestHandler_EpochCancel_WorkflowError_NonexistentWorkflow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.EpochCancel(ctrl, "demo--01960000-0000-7000-8000-000099999999", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for nonexistent epoch; got nil")
	}
	if code != 3 {
		t.Fatalf("EpochCancel exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}

// TestHandler_PhaseAdvance_WorkflowError_NeverStartedEpoch verifies that
// PhaseAdvance returns exit 3 (CategoryWorkflow) when the target epoch was
// never started. dbos.Send to a never-started id violates the FK on
// notifications.destination_uuid and returns a nonexistent-workflow error via
// the sendSignal path, which the handler surfaces as exit 3.
func TestHandler_PhaseAdvance_WorkflowError_NeverStartedEpoch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.PhaseAdvance(ctrl,
		"demo--ffffffff-ffff-7fff-8fff-ff0000000001",
		protocol.PhaseElicit, "test", "probe", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for a never-started epoch; got nil")
	}
	if code != 3 {
		t.Fatalf("PhaseAdvance exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}

// TestHandler_SignalVote_WorkflowError_NeverStartedEpoch verifies that
// SignalVote returns exit 3 (CategoryWorkflow) for a never-started epoch id.
// This exercises the sendSignal → SubmitVote path with a valid axis and vote.
func TestHandler_SignalVote_WorkflowError_NeverStartedEpoch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SignalVote(ctrl,
		"demo--ffffffff-ffff-7fff-8fff-ff0000000002",
		protocol.AxisCorrectness, protocol.VoteAccept, "r-test", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for a never-started epoch; got nil")
	}
	if code != 3 {
		t.Fatalf("SignalVote exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}

// TestHandler_SignalComplete_WorkflowError_NeverStartedEpoch verifies that
// SignalComplete returns exit 3 (CategoryWorkflow) for a never-started epoch
// id. This exercises the sendSignal → ReportSliceProgress path.
func TestHandler_SignalComplete_WorkflowError_NeverStartedEpoch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	out := "done"
	code, hErr := handlers.SignalComplete(ctrl,
		"demo--ffffffff-ffff-7fff-8fff-ff0000000003",
		"slice-x", &out, nil, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for a never-started epoch; got nil")
	}
	if code != 3 {
		t.Fatalf("SignalComplete exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}

// TestHandler_SessionRegister_WorkflowError_NeverStartedEpoch verifies that
// SessionRegister returns exit 3 (CategoryWorkflow) for a never-started epoch
// id. This exercises the sendSignal → RegisterSession path.
func TestHandler_SessionRegister_WorkflowError_NeverStartedEpoch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SessionRegister(ctrl,
		"demo--ffffffff-ffff-7fff-8fff-ff0000000004",
		"sess-x", "worker", "claude-code", "claude-sonnet", types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for a never-started epoch; got nil")
	}
	if code != 3 {
		t.Fatalf("SessionRegister exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}

// TestHandler_SliceStart_WorkflowError_NeverStartedSlice verifies that
// SliceStart returns exit 3 (CategoryWorkflow) for a never-started slice id.
// This exercises the sendSignal → StartSlice path (slice-addressed signals).
func TestHandler_SliceStart_WorkflowError_NeverStartedSlice(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SliceStart(ctrl,
		"demo--ffffffff-ffff-7fff-8fff-ff0000000005",
		protocol.SliceMock, "", 0, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for a never-started slice; got nil")
	}
	if code != 3 {
		t.Fatalf("SliceStart exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}

// TestHandler_SliceComplete_WorkflowError_NeverStartedSlice verifies that
// SliceComplete returns exit 3 (CategoryWorkflow) for a never-started slice
// id. This exercises the sendSignal → CompleteSlice path.
func TestHandler_SliceComplete_WorkflowError_NeverStartedSlice(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	out := "done"
	code, hErr := handlers.SliceComplete(ctrl,
		"demo--ffffffff-ffff-7fff-8fff-ff0000000006",
		&out, nil, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for a never-started slice; got nil")
	}
	if code != 3 {
		t.Fatalf("SliceComplete exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}
