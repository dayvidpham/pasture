package temporal_test

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── State Machine Tests ──────────────────────────────────────────────────────

func TestStateMachine_InitialState(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-1", nil)
	state := sm.State()
	if state.CurrentPhase != protocol.PhaseRequest {
		t.Errorf("initial phase = %q, want %q", state.CurrentPhase, protocol.PhaseRequest)
	}
	if state.EpochID != "epoch-1" {
		t.Errorf("epoch ID = %q, want %q", state.EpochID, "epoch-1")
	}
	if state.BlockerCount != 0 {
		t.Errorf("initial blocker count = %d, want 0", state.BlockerCount)
	}
	if len(state.ReviewVotes) != 0 {
		t.Errorf("initial review votes = %v, want empty", state.ReviewVotes)
	}
}

func TestStateMachine_Advance_HappyPath(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-2", nil)
	now := time.Now()

	record, err := sm.Advance(protocol.PhaseElicit, "architect", "classification confirmed", now)
	if err != nil {
		t.Fatalf("Advance to p2: unexpected error: %v", err)
	}
	if record.ToPhase != protocol.PhaseElicit {
		t.Errorf("record.ToPhase = %q, want %q", record.ToPhase, protocol.PhaseElicit)
	}
	if !record.Success {
		t.Error("record.Success = false, want true")
	}
	if sm.State().CurrentPhase != protocol.PhaseElicit {
		t.Errorf("current phase = %q, want %q", sm.State().CurrentPhase, protocol.PhaseElicit)
	}
	if len(sm.State().CompletedPhases) != 1 || sm.State().CompletedPhases[0] != protocol.PhaseRequest {
		t.Errorf("completed phases = %v, want [p1]", sm.State().CompletedPhases)
	}
}

func TestStateMachine_Advance_InvalidTransition(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-3", nil)

	// Attempt to jump to p3 from p1 (only p2 is valid).
	_, err := sm.Advance(protocol.PhasePropose, "bad-actor", "skip elicit", time.Now())
	if err == nil {
		t.Fatal("expected error for invalid transition p1 → p3, got nil")
	}
	// Current phase should remain p1.
	if sm.State().CurrentPhase != protocol.PhaseRequest {
		t.Errorf("current phase after failed advance = %q, want %q", sm.State().CurrentPhase, protocol.PhaseRequest)
	}
}

func TestStateMachine_ConsensusGate_P4ToP5(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-4", nil)
	now := time.Now()

	// Advance to p4.
	for _, phase := range []protocol.PhaseId{protocol.PhaseElicit, protocol.PhasePropose, protocol.PhaseReview} {
		if _, err := sm.Advance(phase, "architect", "ok", now); err != nil {
			t.Fatalf("advance to %q: %v", phase, err)
		}
	}
	if sm.State().CurrentPhase != protocol.PhaseReview {
		t.Fatalf("want p4, got %q", sm.State().CurrentPhase)
	}

	// Without consensus, p4→p5 should fail.
	violations := sm.ValidateAdvance(protocol.PhasePlanReview)
	if len(violations) == 0 {
		t.Error("expected consensus gate violation for p4→p5 with no votes, got none")
	}

	// Add partial votes (only 2 axes).
	_ = sm.RecordVote(types.AxisCorrectness, types.VoteAccept)
	_ = sm.RecordVote(types.AxisTestQuality, types.VoteAccept)

	violations = sm.ValidateAdvance(protocol.PhasePlanReview)
	if len(violations) == 0 {
		t.Error("expected consensus gate violation for p4→p5 with 2/3 votes, got none")
	}

	// Add 3rd vote — now consensus reached.
	_ = sm.RecordVote(types.AxisElegance, types.VoteAccept)

	violations = sm.ValidateAdvance(protocol.PhasePlanReview)
	if len(violations) != 0 {
		t.Errorf("unexpected violations for p4→p5 after consensus: %v", violations)
	}

	if _, err := sm.Advance(protocol.PhasePlanReview, "reviewer", "all 3 vote ACCEPT", now); err != nil {
		t.Fatalf("advance to p5 after consensus: %v", err)
	}
}

func TestStateMachine_ReviseGate_P4BackToP3(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-5", nil)
	now := time.Now()

	// Advance to p4.
	for _, phase := range []protocol.PhaseId{protocol.PhaseElicit, protocol.PhasePropose, protocol.PhaseReview} {
		if _, err := sm.Advance(phase, "architect", "ok", now); err != nil {
			t.Fatalf("advance to %q: %v", phase, err)
		}
	}

	// Record a REVISE vote.
	_ = sm.RecordVote(types.AxisCorrectness, types.VoteRevise)

	// Available transitions should only include backward (p3), not p5.
	avail := sm.AvailableTransitions()
	for _, a := range avail {
		if a == protocol.PhasePlanReview {
			t.Error("REVISE gate: p5 should NOT be available when any axis voted REVISE")
		}
	}
	hasP3 := false
	for _, a := range avail {
		if a == protocol.PhasePropose {
			hasP3 = true
		}
	}
	if !hasP3 {
		t.Error("REVISE gate: p3 should be available as revision loop target")
	}
}

func TestStateMachine_BlockerGate_P10ToP11(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-6", nil)
	now := time.Now()

	// Advance straight through to p10.
	phases := []protocol.PhaseId{
		protocol.PhaseElicit, protocol.PhasePropose, protocol.PhaseReview,
		protocol.PhasePlanReview, protocol.PhaseRatify, protocol.PhaseHandoff,
		protocol.PhaseImplPlan, protocol.PhaseWorkerSlices, protocol.PhaseCodeReview,
	}
	// p4→p5 needs consensus first.
	for i, phase := range phases {
		if i == 3 { // p5 — needs consensus from p4.
			_ = sm.RecordVote(types.AxisCorrectness, types.VoteAccept)
			_ = sm.RecordVote(types.AxisTestQuality, types.VoteAccept)
			_ = sm.RecordVote(types.AxisElegance, types.VoteAccept)
		}
		if _, err := sm.Advance(phase, "test", "ok", now); err != nil {
			t.Fatalf("advance to %q: %v", phase, err)
		}
	}

	if sm.State().CurrentPhase != protocol.PhaseCodeReview {
		t.Fatalf("want p10, got %q", sm.State().CurrentPhase)
	}

	// Record a blocker.
	sm.RecordBlocker(false) // +1 blocker

	// Add consensus votes.
	_ = sm.RecordVote(types.AxisCorrectness, types.VoteAccept)
	_ = sm.RecordVote(types.AxisTestQuality, types.VoteAccept)
	_ = sm.RecordVote(types.AxisElegance, types.VoteAccept)

	// p10→p11 should fail due to blocker.
	violations := sm.ValidateAdvance(protocol.PhaseImplUAT)
	hasBlockerViolation := false
	for _, v := range violations {
		if len(v) > 0 {
			hasBlockerViolation = true
		}
	}
	if !hasBlockerViolation {
		t.Error("expected BLOCKER gate violation for p10→p11 with unresolved blocker, got none")
	}

	// Resolve the blocker.
	sm.RecordBlocker(true) // -1 blocker

	violations = sm.ValidateAdvance(protocol.PhaseImplUAT)
	if len(violations) != 0 {
		t.Errorf("unexpected violations for p10→p11 after resolving blocker: %v", violations)
	}
}

func TestStateMachine_HasConsensus(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-7", nil)

	if sm.HasConsensus() {
		t.Error("HasConsensus() = true with no votes, want false")
	}

	_ = sm.RecordVote(types.AxisCorrectness, types.VoteAccept)
	_ = sm.RecordVote(types.AxisTestQuality, types.VoteAccept)
	if sm.HasConsensus() {
		t.Error("HasConsensus() = true with 2/3 votes, want false")
	}

	_ = sm.RecordVote(types.AxisElegance, types.VoteAccept)
	if !sm.HasConsensus() {
		t.Error("HasConsensus() = false with 3/3 ACCEPT votes, want true")
	}

	// A REVISE vote breaks consensus.
	_ = sm.RecordVote(types.AxisCorrectness, types.VoteRevise)
	if sm.HasConsensus() {
		t.Error("HasConsensus() = true with a REVISE vote, want false")
	}
}

func TestStateMachine_RecordVote_InvalidAxis(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-8", nil)
	err := sm.RecordVote(types.ReviewAxis("invalid_axis"), types.VoteAccept)
	if err == nil {
		t.Error("expected error for invalid review axis, got nil")
	}
}

func TestStateMachine_RecordBlocker_ClampedToZero(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-9", nil)
	sm.RecordBlocker(true) // resolve when count = 0 — should stay 0
	if sm.State().BlockerCount != 0 {
		t.Errorf("blocker count after clamped resolve = %d, want 0", sm.State().BlockerCount)
	}
}

func TestStateMachine_VotesCleared_AfterAdvance(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-10", nil)
	now := time.Now()

	_ = sm.RecordVote(types.AxisCorrectness, types.VoteAccept)
	if _, err := sm.Advance(protocol.PhaseElicit, "test", "ok", now); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(sm.State().ReviewVotes) != 0 {
		t.Errorf("review votes not cleared after advance: %v", sm.State().ReviewVotes)
	}
}

func TestStateMachine_CompletePhase_NoFurtherTransitions(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-11", nil)
	// Manually inject COMPLETE to test gate.
	sm.State().CurrentPhase = protocol.PhaseComplete

	violations := sm.ValidateAdvance(protocol.PhaseRequest)
	if len(violations) == 0 {
		t.Error("expected violation for COMPLETE epoch, got none")
	}

	avail := sm.AvailableTransitions()
	if len(avail) != 0 {
		t.Errorf("AvailableTransitions on COMPLETE = %v, want empty", avail)
	}
}

func TestStateMachine_CustomSpecs(t *testing.T) {
	t.Parallel()
	// Inject a tiny custom spec for testability.
	customSpecs := map[protocol.PhaseId]temporal.PhaseSpec{
		protocol.PhaseRequest: {Transitions: []protocol.PhaseId{protocol.PhasePropose}},
		protocol.PhasePropose: {Transitions: []protocol.PhaseId{protocol.PhaseComplete}},
	}
	sm := temporal.NewEpochStateMachine("epoch-custom", customSpecs)

	if _, err := sm.Advance(protocol.PhasePropose, "test", "custom spec", time.Now()); err != nil {
		t.Fatalf("advance with custom spec: %v", err)
	}
	if sm.State().CurrentPhase != protocol.PhasePropose {
		t.Errorf("phase = %q, want %q", sm.State().CurrentPhase, protocol.PhasePropose)
	}
}

// ─── Activity Tests ───────────────────────────────────────────────────────────

func TestCheckConstraints_ValidTransition(t *testing.T) {
	t.Parallel()
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.CheckConstraints)

	state := types.EpochState{
		EpochID:      "epoch-act-1",
		CurrentPhase: protocol.PhaseRequest,
		ReviewVotes:  make(map[types.ReviewAxis]types.VoteType),
	}

	val, err := env.ExecuteActivity(temporal.CheckConstraints, state, protocol.PhaseElicit)
	if err != nil {
		t.Fatalf("CheckConstraints activity failed: %v", err)
	}
	var violations []temporal.ConstraintViolation
	if err := val.Get(&violations); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations for valid transition, got: %v", violations)
	}
}

func TestCheckConstraints_InvalidTransition(t *testing.T) {
	t.Parallel()
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.CheckConstraints)

	state := types.EpochState{
		EpochID:      "epoch-act-2",
		CurrentPhase: protocol.PhaseRequest,
		ReviewVotes:  make(map[types.ReviewAxis]types.VoteType),
	}

	val, err := env.ExecuteActivity(temporal.CheckConstraints, state, protocol.PhasePropose)
	if err != nil {
		t.Fatalf("CheckConstraints activity failed: %v", err)
	}
	var violations []temporal.ConstraintViolation
	if err := val.Get(&violations); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(violations) == 0 {
		t.Error("expected violations for invalid transition p1→p3, got none")
	}
}

func TestRecordTransition_UninitializedTrail(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	temporal.InitAuditTrail(nil)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.RecordTransition)

	record := types.TransitionRecord{
		FromPhase: protocol.PhaseRequest,
		ToPhase:   protocol.PhaseElicit,
		Timestamp: time.Now(),
		Success:   true,
	}
	_, err := env.ExecuteActivity(temporal.RecordTransition, "epoch-uninitialized", record)
	// Expect a non-retryable ApplicationError.
	if err == nil {
		t.Error("expected error from RecordTransition with uninitialized trail, got nil")
	}
}

func TestRecordTransition_WithTrail(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.RecordTransition)

	record := types.TransitionRecord{
		FromPhase:    protocol.PhaseRequest,
		ToPhase:      protocol.PhaseElicit,
		Timestamp:    time.Now(),
		TriggeredBy:  "test",
		ConditionMet: "test ok",
		Success:      true,
	}
	_, err := env.ExecuteActivity(temporal.RecordTransition, "epoch-test-trail", record)
	if err != nil {
		t.Fatalf("RecordTransition: unexpected error: %v", err)
	}

	events := trail.Events()
	if len(events) != 1 {
		t.Errorf("expected 1 audit event, got %d", len(events))
	}
	if events[0].EpochID != "epoch-test-trail" {
		t.Errorf("audit event EpochID = %q, want %q", events[0].EpochID, "epoch-test-trail")
	}
}

func TestInMemoryAuditTrail_RecordAndQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	trail := audit.NewInMemoryAuditTrail()

	event1 := protocol.AuditEvent{
		EpochID:   "epoch-trail-1",
		Phase:     protocol.PhaseRequest,
		EventType: protocol.EventPhaseTransition,
		Timestamp: time.Now(),
	}
	event2 := protocol.AuditEvent{
		EpochID:   "epoch-trail-2",
		Phase:     protocol.PhaseElicit,
		EventType: protocol.EventVoteRecorded,
		Timestamp: time.Now(),
	}

	if err := trail.RecordEvent(ctx, event1); err != nil {
		t.Fatalf("RecordEvent 1: %v", err)
	}
	if err := trail.RecordEvent(ctx, event2); err != nil {
		t.Fatalf("RecordEvent 2: %v", err)
	}

	// Query by epoch ID.
	results, err := trail.QueryEvents(ctx, "epoch-trail-1", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("QueryEvents by epochId: got %d, want 1", len(results))
	}

	// Query by epoch ID and phase.
	p2 := protocol.PhaseElicit
	results, err = trail.QueryEvents(ctx, "epoch-trail-2", &p2, nil)
	if err != nil {
		t.Fatalf("QueryEvents by phase: %v", err)
	}
	if len(results) != 1 || results[0].Phase != protocol.PhaseElicit {
		t.Errorf("QueryEvents by phase: got %d events", len(results))
	}

	// Query all.
	all := trail.Events()
	if len(all) != 2 {
		t.Errorf("Events(): got %d, want 2", len(all))
	}
}

// ─── Workflow Tests (using Temporal test suite) ────────────────────────────────

func TestEpochWorkflow_P1ToP2_Signal(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(temporal.CheckConstraints)
	env.RegisterActivity(temporal.RecordTransition)
	env.RegisterActivity(temporal.RecordAuditEvent)

	// Register a delayed signal to advance from p1 to p2.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(temporal.SignalAdvancePhase, types.PhaseAdvanceSignal{
			ToPhase:      protocol.PhaseElicit,
			TriggeredBy:  "architect",
			ConditionMet: "classification confirmed",
		})
	}, time.Millisecond*10)

	// Register a signal to advance from p2 to complete (short circuit for test).
	// Instead, we cancel the workflow after the first transition.
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Millisecond*100)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochID:            "epoch-wf-1",
		RequestDescription: "test workflow",
	})

	// Workflow should be cancelled (not error).
	// We care that the signal was processed.
	if !env.IsWorkflowCompleted() {
		t.Error("workflow should be completed (cancelled) after CancelWorkflow")
	}
}

func TestEpochWorkflow_AdvancePhase_InvalidIgnored(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(temporal.CheckConstraints)
	env.RegisterActivity(temporal.RecordTransition)
	env.RegisterActivity(temporal.RecordAuditEvent)

	// Send an invalid advance signal (p3 from p1 is invalid).
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(temporal.SignalAdvancePhase, types.PhaseAdvanceSignal{
			ToPhase:      protocol.PhasePropose, // invalid from p1
			TriggeredBy:  "bad-actor",
			ConditionMet: "skipping elicit",
		})
	}, time.Millisecond*10)

	// Cancel after invalid signal processed.
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Millisecond*100)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochID:            "epoch-wf-invalid",
		RequestDescription: "test invalid advance",
	})

	// Workflow completes (cancelled). The invalid advance was recorded as failed.
	if !env.IsWorkflowCompleted() {
		t.Error("workflow should be completed after cancel")
	}
}

func TestEpochWorkflow_SubmitVote_Signal(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(temporal.CheckConstraints)
	env.RegisterActivity(temporal.RecordTransition)
	env.RegisterActivity(temporal.RecordAuditEvent)

	// Submit a vote signal.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(temporal.SignalSubmitVote, types.ReviewVoteSignal{
			Axis:       types.AxisCorrectness,
			Vote:       types.VoteAccept,
			ReviewerID: "reviewer-1",
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Millisecond*100)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochID:            "epoch-wf-vote",
		RequestDescription: "test vote signal",
	})

	if !env.IsWorkflowCompleted() {
		t.Error("workflow should be completed after cancel")
	}
}

func TestEpochWorkflow_RegisterSession_Idempotent(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(temporal.CheckConstraints)
	env.RegisterActivity(temporal.RecordTransition)
	env.RegisterActivity(temporal.RecordAuditEvent)

	// Send the same session registration twice.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(temporal.SignalRegisterSession, types.RegisterSessionSignal{
			EpochID:   "epoch-wf-session",
			SessionID: "session-42",
			Role:      "worker",
		})
		env.SignalWorkflow(temporal.SignalRegisterSession, types.RegisterSessionSignal{
			EpochID:   "epoch-wf-session",
			SessionID: "session-42", // duplicate
			Role:      "worker",
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		// Query active sessions before cancel.
		val, err := env.QueryWorkflow(temporal.QueryActiveSessions)
		if err == nil {
			var sessions []types.RegisterSessionSignal
			if decErr := val.Get(&sessions); decErr == nil {
				// Idempotent: should be 1 session, not 2.
				if len(sessions) > 1 {
					t.Errorf("expected 1 active session (idempotent), got %d", len(sessions))
				}
			}
		}
		env.CancelWorkflow()
	}, time.Millisecond*100)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochID:            "epoch-wf-session",
		RequestDescription: "test session registration",
	})
}

func TestEpochWorkflow_SliceProgress_Signal(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(temporal.CheckConstraints)
	env.RegisterActivity(temporal.RecordTransition)
	env.RegisterActivity(temporal.RecordAuditEvent)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(temporal.SignalSliceProgress, types.SliceProgressSignal{
			SliceID:    "slice-1",
			LeafTaskID: "leaf-a",
			StageName:  "execute",
			Completed:  true,
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		// Query slice progress.
		val, err := env.QueryWorkflow(temporal.QuerySliceProgressState)
		if err == nil {
			var log []types.SliceProgressSignal
			if decErr := val.Get(&log); decErr == nil {
				if len(log) != 1 {
					t.Errorf("expected 1 slice progress event, got %d", len(log))
				}
			}
		}
		env.CancelWorkflow()
	}, time.Millisecond*100)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochID:            "epoch-wf-slice",
		RequestDescription: "test slice progress",
	})
}

// ─── SliceWorkflow Tests ──────────────────────────────────────────────────────

func TestSliceWorkflow_MockMode_Default(t *testing.T) {
	t.Parallel()
	// Reset hooks singleton so hook dispatch is a no-op in this test.
	hooks.InitHooksManager(nil)
	t.Cleanup(func() { hooks.InitHooksManager(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(temporal.SliceWorkflowFn)
	env.RegisterActivity(hooks.DispatchHook)

	// Mock SignalExternalWorkflow to avoid error when parent workflow not present.
	env.OnSignalExternalWorkflow("", "", "", temporal.SignalSliceProgress, nil).Return(nil).Maybe()

	env.ExecuteWorkflow(temporal.SliceWorkflowFn, temporal.SliceInput{
		EpochID:          "epoch-slice-1",
		SliceID:          "slice-1",
		PhaseSpec:        "p9",
		ParentWorkflowID: "", // empty: skip parent signaling
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("slice workflow should be completed in mock mode")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("slice workflow error: %v", err)
	}

	var result temporal.SliceResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if !result.Success {
		t.Errorf("slice result.Success = false, want true")
	}
	if result.SliceID != "slice-1" {
		t.Errorf("slice result.SliceID = %q, want %q", result.SliceID, "slice-1")
	}
}

func TestSliceWorkflow_CompleteSliceOverride(t *testing.T) {
	t.Parallel()
	// Reset hooks singleton so hook dispatch is a no-op in this test.
	hooks.InitHooksManager(nil)
	t.Cleanup(func() { hooks.InitHooksManager(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(temporal.SliceWorkflowFn)
	env.RegisterActivity(hooks.DispatchHook)

	// Send a complete_slice signal that overrides with success=false.
	errMsg := "external override error"
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(temporal.SignalCompleteSlice, temporal.SliceCompleteSignal{
			Success: false,
			Error:   &errMsg,
		})
	}, time.Millisecond*1)

	env.ExecuteWorkflow(temporal.SliceWorkflowFn, temporal.SliceInput{
		EpochID:          "epoch-slice-override",
		SliceID:          "slice-override",
		PhaseSpec:        "p9",
		ParentWorkflowID: "",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("slice workflow should be completed")
	}
	var result temporal.SliceResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.Success {
		t.Error("expected Success=false from override signal, got true")
	}
	if result.Error == nil || *result.Error != errMsg {
		t.Errorf("expected error %q from override signal, got %v", errMsg, result.Error)
	}
}

// ─── ReviewPhaseWorkflow Tests ────────────────────────────────────────────────

func TestReviewWorkflow_AllVotesReceived(t *testing.T) {
	t.Parallel()
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(temporal.ReviewWorkflowFn)

	// Send all 3 votes.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(temporal.SignalSubmitVote, types.ReviewVoteSignal{
			Axis: types.AxisCorrectness, Vote: types.VoteAccept, ReviewerID: "r1",
		})
		env.SignalWorkflow(temporal.SignalSubmitVote, types.ReviewVoteSignal{
			Axis: types.AxisTestQuality, Vote: types.VoteAccept, ReviewerID: "r2",
		})
		env.SignalWorkflow(temporal.SignalSubmitVote, types.ReviewVoteSignal{
			Axis: types.AxisElegance, Vote: types.VoteRevise, ReviewerID: "r3",
		})
	}, time.Millisecond*10)

	env.ExecuteWorkflow(temporal.ReviewWorkflowFn, temporal.ReviewInput{
		EpochID: "epoch-review-1",
		PhaseID: "p10",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("review workflow should be completed after all 3 votes")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("review workflow error: %v", err)
	}

	var result temporal.ReviewResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get result: %v", err)
	}
	if !result.Success {
		t.Error("ReviewResult.Success = false, want true")
	}
	if result.PhaseID != "p10" {
		t.Errorf("ReviewResult.PhaseID = %q, want %q", result.PhaseID, "p10")
	}
	if len(result.VoteResult) != 3 {
		t.Errorf("expected 3 votes in result, got %d", len(result.VoteResult))
	}
	if result.VoteResult[types.AxisElegance] != types.VoteRevise {
		t.Errorf("expected REVISE for elegance axis, got %q", result.VoteResult[types.AxisElegance])
	}
}

// ─── QueryAuditEvents Activity Tests ─────────────────────────────────────────

func TestQueryAuditEvents_UninitializedTrail(t *testing.T) {
	temporal.InitAuditTrail(nil)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.QueryAuditEvents)

	_, err := env.ExecuteActivity(temporal.QueryAuditEvents, "epoch-q-1", nil, nil)
	if err == nil {
		t.Error("expected error from QueryAuditEvents with uninitialized trail, got nil")
	}
}

func TestQueryAuditEvents_WithTrail(t *testing.T) {
	ctx := context.Background()
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	// Pre-populate trail.
	_ = trail.RecordEvent(ctx, protocol.AuditEvent{
		EpochID:   "epoch-q-2",
		Phase:     protocol.PhaseRequest,
		EventType: protocol.EventPhaseTransition,
		Timestamp: time.Now(),
	})

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.QueryAuditEvents)

	val, err := env.ExecuteActivity(temporal.QueryAuditEvents, "epoch-q-2", nil, nil)
	if err != nil {
		t.Fatalf("QueryAuditEvents: unexpected error: %v", err)
	}
	var events []protocol.AuditEvent
	if err := val.Get(&events); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

func TestRecordAuditEvent_WithTrail(t *testing.T) {
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.RecordAuditEvent)

	event := protocol.AuditEvent{
		EpochID:   "epoch-audit-1",
		Phase:     protocol.PhaseElicit,
		EventType: protocol.EventVoteRecorded,
		Timestamp: time.Now(),
	}
	_, err := env.ExecuteActivity(temporal.RecordAuditEvent, event)
	if err != nil {
		t.Fatalf("RecordAuditEvent: unexpected error: %v", err)
	}
	events := trail.Events()
	if len(events) != 1 {
		t.Errorf("expected 1 audit event, got %d", len(events))
	}
}

// ─── State Machine AvailableTransitions additional coverage ───────────────────

func TestStateMachine_AvailableTransitions_ConsensusNotYetReached(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-avail-1", nil)
	now := time.Now()

	// Advance to p4.
	for _, phase := range []protocol.PhaseId{protocol.PhaseElicit, protocol.PhasePropose, protocol.PhaseReview} {
		if _, err := sm.Advance(phase, "test", "ok", now); err != nil {
			t.Fatalf("advance to %q: %v", phase, err)
		}
	}

	// No votes yet — p5 should not be available (consensus gate).
	avail := sm.AvailableTransitions()
	for _, a := range avail {
		if a == protocol.PhasePlanReview {
			t.Error("p5 should not be available without consensus")
		}
	}
	// Backward transition (p3) should be available.
	hasP3 := false
	for _, a := range avail {
		if a == protocol.PhasePropose {
			hasP3 = true
		}
	}
	if !hasP3 {
		t.Error("p3 should be available from p4 without consensus")
	}
}

func TestStateMachine_AvailableTransitions_NoSpec(t *testing.T) {
	t.Parallel()
	// Use an empty custom spec — current phase has no entry.
	customSpecs := map[protocol.PhaseId]temporal.PhaseSpec{}
	sm := temporal.NewEpochStateMachine("epoch-nospec", customSpecs)
	// p1 has no spec in customSpecs.
	avail := sm.AvailableTransitions()
	if len(avail) != 0 {
		t.Errorf("expected empty transitions for phase with no spec, got %v", avail)
	}
}

func TestTransitionError_MultipleViolations(t *testing.T) {
	t.Parallel()
	sm := temporal.NewEpochStateMachine("epoch-violations", nil)
	now := time.Now()

	// Advance to p10 manually (inject state).
	sm.State().CurrentPhase = protocol.PhaseCodeReview
	sm.State().ReviewVotes = make(map[types.ReviewAxis]types.VoteType)
	sm.State().BlockerCount = 1 // unresolved blocker

	// p10→p11 needs consensus AND 0 blockers. Both should fail.
	violations := sm.ValidateAdvance(protocol.PhaseImplUAT)
	if len(violations) < 2 {
		t.Errorf("expected at least 2 violations (consensus + blocker), got %d: %v", len(violations), violations)
	}

	// Trigger Advance to get the TransitionError.
	_, err := sm.Advance(protocol.PhaseImplUAT, "test", "force", now)
	if err == nil {
		t.Fatal("expected TransitionError, got nil")
	}
	terr, ok := err.(*temporal.TransitionError)
	if !ok {
		t.Fatalf("expected *temporal.TransitionError, got %T", err)
	}
	if len(terr.Violations) < 2 {
		t.Errorf("expected 2+ violations in TransitionError, got %d", len(terr.Violations))
	}
	// Error() should contain the violations joined.
	msg := terr.Error()
	if msg == "" {
		t.Error("TransitionError.Error() returned empty string")
	}
}

// ─── EpochWorkflow Full Lifecycle Test ────────────────────────────────────────

func TestEpochWorkflow_FullLifecycle_ThroughP2(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(temporal.CheckConstraints)
	env.RegisterActivity(temporal.RecordTransition)
	env.RegisterActivity(temporal.RecordAuditEvent)

	// Advance p1→p2, then cancel.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(temporal.SignalAdvancePhase, types.PhaseAdvanceSignal{
			ToPhase:      protocol.PhaseElicit,
			TriggeredBy:  "architect",
			ConditionMet: "classification confirmed",
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		// Query current state after advance.
		val, qErr := env.QueryWorkflow(temporal.QueryCurrentState)
		if qErr == nil {
			var state types.EpochState
			if decErr := val.Get(&state); decErr == nil {
				if state.CurrentPhase != protocol.PhaseElicit {
					t.Errorf("after advance, current phase = %q, want %q",
						state.CurrentPhase, protocol.PhaseElicit)
				}
			}
		}
		env.CancelWorkflow()
	}, time.Millisecond*200)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochID:            "epoch-lifecycle",
		RequestDescription: "full lifecycle test",
	})
}

// ─── RecordAuditEvent uninitialized error path ────────────────────────────────

func TestRecordAuditEvent_UninitializedTrail(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	temporal.InitAuditTrail(nil)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.RecordAuditEvent)

	event := protocol.AuditEvent{
		EpochID:   "epoch-nil-trail",
		Phase:     protocol.PhaseRequest,
		EventType: protocol.EventPhaseTransition,
		Timestamp: time.Now(),
	}
	_, err := env.ExecuteActivity(temporal.RecordAuditEvent, event)
	if err == nil {
		t.Error("expected non-retryable error from RecordAuditEvent with uninitialized trail, got nil")
	}
}

// ─── AvailableTransitionsQuery and FullState workflow query handler tests ─────

func TestEpochWorkflow_QueryAvailableTransitions(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(temporal.CheckConstraints)
	env.RegisterActivity(temporal.RecordTransition)
	env.RegisterActivity(temporal.RecordAuditEvent)

	env.RegisterDelayedCallback(func() {
		// At p1, only p2 should be available.
		val, err := env.QueryWorkflow(temporal.QueryAvailableTransitions)
		if err != nil {
			t.Errorf("QueryAvailableTransitions failed: %v", err)
			return
		}
		var transitions []protocol.PhaseId
		if decErr := val.Get(&transitions); decErr != nil {
			t.Errorf("decode QueryAvailableTransitions: %v", decErr)
			return
		}
		if len(transitions) != 1 || transitions[0] != protocol.PhaseElicit {
			t.Errorf("QueryAvailableTransitions at p1 = %v, want [p2]", transitions)
		}
		env.CancelWorkflow()
	}, time.Millisecond*50)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochID:            "epoch-query-avail",
		RequestDescription: "test available transitions query",
	})
}

func TestEpochWorkflow_QueryFullState(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(temporal.CheckConstraints)
	env.RegisterActivity(temporal.RecordTransition)
	env.RegisterActivity(temporal.RecordAuditEvent)

	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow(temporal.QueryFullState)
		if err != nil {
			t.Errorf("QueryFullState failed: %v", err)
			return
		}
		var result types.QueryStateResult
		if decErr := val.Get(&result); decErr != nil {
			t.Errorf("decode QueryFullState: %v", decErr)
			return
		}
		if result.CurrentPhase != protocol.PhaseRequest {
			t.Errorf("QueryFullState.CurrentPhase = %q, want %q", result.CurrentPhase, protocol.PhaseRequest)
		}
		if len(result.AvailableTransitions) == 0 {
			t.Error("QueryFullState.AvailableTransitions is empty, want at least one transition")
		}
		env.CancelWorkflow()
	}, time.Millisecond*50)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochID:            "epoch-query-full",
		RequestDescription: "test full state query",
	})
}

// ─── RunAgentSession Activity Tests ──────────────────────────────────────────

// TestRunAgentSession_UninitializedTrail verifies that RunAgentSession returns
// a non-retryable ApplicationError when the audit trail singleton has not been
// initialized via InitAuditTrail.
func TestRunAgentSession_UninitializedTrail(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	temporal.InitAuditTrail(nil)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.RunAgentSession)

	input := temporal.RunAgentSessionInput{
		AgentCmd:  "claude",
		AgentArgs: []string{"--mcp-server", "test"},
		EpochID:   "epoch-uninitialized-session",
	}
	_, err := env.ExecuteActivity(temporal.RunAgentSession, input)
	if err == nil {
		t.Error("expected error from RunAgentSession with uninitialized trail, got nil")
	}
}

// TestRunAgentSession_ConnectError verifies that RunAgentSession wraps
// connection errors (e.g. binary not found) and returns them to the caller.
func TestRunAgentSession_ConnectError(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	trail := audit.NewInMemoryAuditTrail()
	temporal.InitAuditTrail(trail)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.RunAgentSession)

	// Use a clearly non-existent binary to force a connection error.
	input := temporal.RunAgentSessionInput{
		AgentCmd:  "/no-such-binary-pasture-test-xyz",
		AgentArgs: []string{},
		EpochID:   "epoch-connect-error",
	}
	_, err := env.ExecuteActivity(temporal.RunAgentSession, input)
	if err == nil {
		t.Error("expected error from RunAgentSession with bogus agent command, got nil")
	}
}

// ─── RecordSessionEntries Activity Tests ─────────────────────────────────────

// TestRecordSessionEntries_UninitializedTrail mirrors TestRecordAuditEvent_UninitializedTrail
// for the RecordSessionEntries activity.
func TestRecordSessionEntries_UninitializedTrail(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	temporal.InitAuditTrail(nil)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.RecordSessionEntries)

	entries := []protocol.SessionEntry{
		{SessionID: "s-nil", EntryIndex: 0, Provider: "anthropic", EntryType: "message", Role: "user"},
	}
	_, err := env.ExecuteActivity(temporal.RecordSessionEntries, entries)
	if err == nil {
		t.Error("expected non-retryable error from RecordSessionEntries with uninitialized trail, got nil")
	}
}

// TestQuerySessionEntries_UninitializedTrail mirrors TestRecordAuditEvent_UninitializedTrail
// for the QuerySessionEntries activity.
func TestQuerySessionEntries_UninitializedTrail(t *testing.T) {
	// Not parallel: shares global auditTrail singleton.
	temporal.InitAuditTrail(nil)
	t.Cleanup(func() { temporal.InitAuditTrail(nil) })

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(temporal.QuerySessionEntries)

	_, err := env.ExecuteActivity(temporal.QuerySessionEntries, "session-nil-trail")
	if err == nil {
		t.Error("expected non-retryable error from QuerySessionEntries with uninitialized trail, got nil")
	}
}
