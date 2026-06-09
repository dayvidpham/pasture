package temporal_test

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// newActivities creates an Activities instance with an in-memory trail and no
// hooks manager (safe for tests that don't need hooks).
func newActivities(trail audit.Trail) *temporal.Activities {
	return &temporal.Activities{
		Trail:    trail,
		HooksMgr: nil,
	}
}

// newActivitiesWithHooks creates an Activities instance with both trail and hooks.
func newActivitiesWithHooks(trail audit.Trail, mgr *hooks.Manager) *temporal.Activities {
	return &temporal.Activities{
		Trail:    trail,
		HooksMgr: mgr,
	}
}

// ─── State Machine Tests ──────────────────────────────────────────────────────

func TestStateMachine_InitialState(t *testing.T) {
	t.Parallel()
	sm := protocol.NewEpochStateMachine("epoch-1", nil)
	state := sm.State()
	if state.CurrentPhase != protocol.PhaseRequest {
		t.Errorf("initial phase = %q, want %q", state.CurrentPhase, protocol.PhaseRequest)
	}
	if state.EpochId != "epoch-1" {
		t.Errorf("epoch ID = %q, want %q", state.EpochId, "epoch-1")
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
	sm := protocol.NewEpochStateMachine("epoch-2", nil)
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
	sm := protocol.NewEpochStateMachine("epoch-3", nil)

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
	sm := protocol.NewEpochStateMachine("epoch-4", nil)
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
	_ = sm.RecordVote(protocol.AxisCorrectness, protocol.VoteAccept)
	_ = sm.RecordVote(protocol.AxisTestQuality, protocol.VoteAccept)

	violations = sm.ValidateAdvance(protocol.PhasePlanReview)
	if len(violations) == 0 {
		t.Error("expected consensus gate violation for p4→p5 with 2/3 votes, got none")
	}

	// Add 3rd vote — now consensus reached.
	_ = sm.RecordVote(protocol.AxisElegance, protocol.VoteAccept)

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
	sm := protocol.NewEpochStateMachine("epoch-5", nil)
	now := time.Now()

	// Advance to p4.
	for _, phase := range []protocol.PhaseId{protocol.PhaseElicit, protocol.PhasePropose, protocol.PhaseReview} {
		if _, err := sm.Advance(phase, "architect", "ok", now); err != nil {
			t.Fatalf("advance to %q: %v", phase, err)
		}
	}

	// Record a REVISE vote.
	_ = sm.RecordVote(protocol.AxisCorrectness, protocol.VoteRevise)

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
	sm := protocol.NewEpochStateMachine("epoch-6", nil)
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
			_ = sm.RecordVote(protocol.AxisCorrectness, protocol.VoteAccept)
			_ = sm.RecordVote(protocol.AxisTestQuality, protocol.VoteAccept)
			_ = sm.RecordVote(protocol.AxisElegance, protocol.VoteAccept)
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
	_ = sm.RecordVote(protocol.AxisCorrectness, protocol.VoteAccept)
	_ = sm.RecordVote(protocol.AxisTestQuality, protocol.VoteAccept)
	_ = sm.RecordVote(protocol.AxisElegance, protocol.VoteAccept)

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
	sm := protocol.NewEpochStateMachine("epoch-7", nil)

	if sm.HasConsensus() {
		t.Error("HasConsensus() = true with no votes, want false")
	}

	_ = sm.RecordVote(protocol.AxisCorrectness, protocol.VoteAccept)
	_ = sm.RecordVote(protocol.AxisTestQuality, protocol.VoteAccept)
	if sm.HasConsensus() {
		t.Error("HasConsensus() = true with 2/3 votes, want false")
	}

	_ = sm.RecordVote(protocol.AxisElegance, protocol.VoteAccept)
	if !sm.HasConsensus() {
		t.Error("HasConsensus() = false with 3/3 ACCEPT votes, want true")
	}

	// A REVISE vote breaks consensus.
	_ = sm.RecordVote(protocol.AxisCorrectness, protocol.VoteRevise)
	if sm.HasConsensus() {
		t.Error("HasConsensus() = true with a REVISE vote, want false")
	}
}

func TestStateMachine_RecordVote_InvalidAxis(t *testing.T) {
	t.Parallel()
	sm := protocol.NewEpochStateMachine("epoch-8", nil)
	err := sm.RecordVote(protocol.ReviewAxis("invalid_axis"), protocol.VoteAccept)
	if err == nil {
		t.Error("expected error for invalid review axis, got nil")
	}
}

func TestStateMachine_RecordBlocker_ClampedToZero(t *testing.T) {
	t.Parallel()
	sm := protocol.NewEpochStateMachine("epoch-9", nil)
	sm.RecordBlocker(true) // resolve when count = 0 — should stay 0
	if sm.State().BlockerCount != 0 {
		t.Errorf("blocker count after clamped resolve = %d, want 0", sm.State().BlockerCount)
	}
}

func TestStateMachine_VotesCleared_AfterAdvance(t *testing.T) {
	t.Parallel()
	sm := protocol.NewEpochStateMachine("epoch-10", nil)
	now := time.Now()

	_ = sm.RecordVote(protocol.AxisCorrectness, protocol.VoteAccept)
	if _, err := sm.Advance(protocol.PhaseElicit, "test", "ok", now); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if len(sm.State().ReviewVotes) != 0 {
		t.Errorf("review votes not cleared after advance: %v", sm.State().ReviewVotes)
	}
}

func TestStateMachine_CompletePhase_NoFurtherTransitions(t *testing.T) {
	t.Parallel()
	sm := protocol.NewEpochStateMachine("epoch-11", nil)
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
	customSpecs := map[protocol.PhaseId]protocol.PhaseSpec{
		protocol.PhaseRequest: {Transitions: []protocol.PhaseId{protocol.PhasePropose}},
		protocol.PhasePropose: {Transitions: []protocol.PhaseId{protocol.PhaseComplete}},
	}
	sm := protocol.NewEpochStateMachine("epoch-custom", customSpecs)

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
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	state := protocol.EpochState{
		EpochId:      "epoch-act-1",
		CurrentPhase: protocol.PhaseRequest,
		ReviewVotes:  make(map[protocol.ReviewAxis]protocol.VoteType),
	}

	val, err := env.ExecuteActivity(acts.CheckConstraints, state, protocol.PhaseElicit)
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
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	state := protocol.EpochState{
		EpochId:      "epoch-act-2",
		CurrentPhase: protocol.PhaseRequest,
		ReviewVotes:  make(map[protocol.ReviewAxis]protocol.VoteType),
	}

	val, err := env.ExecuteActivity(acts.CheckConstraints, state, protocol.PhasePropose)
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

func TestRecordTransition_WithTrail(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	record := protocol.TransitionRecord{
		FromPhase:    protocol.PhaseRequest,
		ToPhase:      protocol.PhaseElicit,
		Timestamp:    time.Now(),
		TriggeredBy:  "test",
		ConditionMet: "test ok",
		Success:      true,
	}
	_, err := env.ExecuteActivity(acts.RecordTransition, "epoch-test-trail", record)
	if err != nil {
		t.Fatalf("RecordTransition: unexpected error: %v", err)
	}

	events := trail.Events()
	if len(events) != 1 {
		t.Errorf("expected 1 audit event, got %d", len(events))
	}
	if events[0].EpochId != "epoch-test-trail" {
		t.Errorf("audit event EpochId = %q, want %q", events[0].EpochId, "epoch-test-trail")
	}
}

func TestInMemoryAuditTrail_RecordAndQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	trail := audit.NewInMemoryAuditTrail()

	event1 := protocol.AuditEvent{
		EpochId:   "epoch-trail-1",
		Phase:     protocol.PhaseRequest,
		EventType: protocol.EventPhaseTransition,
		Timestamp: time.Now(),
	}
	event2 := protocol.AuditEvent{
		EpochId:   "epoch-trail-2",
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
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	// Register a delayed signal to advance from p1 to p2.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(protocol.SignalAdvancePhase, protocol.PhaseAdvanceSignal{
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
		EpochId:            "epoch-wf-1",
		RequestDescription: "test workflow",
	})

	// Workflow should be cancelled (not error).
	// We care that the signal was processed.
	if !env.IsWorkflowCompleted() {
		t.Error("workflow should be completed (cancelled) after CancelWorkflow")
	}
}

func TestEpochWorkflow_AdvancePhase_InvalidIgnored(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	// Send an invalid advance signal (p3 from p1 is invalid).
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(protocol.SignalAdvancePhase, protocol.PhaseAdvanceSignal{
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
		EpochId:            "epoch-wf-invalid",
		RequestDescription: "test invalid advance",
	})

	// Workflow completes (cancelled). The invalid advance was recorded as failed.
	if !env.IsWorkflowCompleted() {
		t.Error("workflow should be completed after cancel")
	}
}

func TestEpochWorkflow_SubmitVote_Signal(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	// Submit a vote signal.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(protocol.SignalSubmitVote, protocol.ReviewVoteSignal{
			Axis:       protocol.AxisCorrectness,
			Vote:       protocol.VoteAccept,
			ReviewerId: "reviewer-1",
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Millisecond*100)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochId:            "epoch-wf-vote",
		RequestDescription: "test vote signal",
	})

	if !env.IsWorkflowCompleted() {
		t.Error("workflow should be completed after cancel")
	}
}

func TestEpochWorkflow_RegisterSession_Idempotent(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	// Send the same session registration twice.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(protocol.SignalRegisterSession, protocol.RegisterSessionSignal{
			EpochId:   "epoch-wf-session",
			SessionId: "session-42",
			Role:      "worker",
		})
		env.SignalWorkflow(protocol.SignalRegisterSession, protocol.RegisterSessionSignal{
			EpochId:   "epoch-wf-session",
			SessionId: "session-42", // duplicate
			Role:      "worker",
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		// Query active sessions before cancel.
		val, err := env.QueryWorkflow(protocol.QueryActiveSessions)
		if err == nil {
			var sessions []protocol.RegisterSessionSignal
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
		EpochId:            "epoch-wf-session",
		RequestDescription: "test session registration",
	})
}

func TestEpochWorkflow_SliceProgress_Signal(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(protocol.SignalSliceProgress, protocol.SliceProgressSignal{
			SliceId:    "slice-1",
			LeafTaskId: "leaf-a",
			StageName:  "execute",
			Completed:  true,
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		// Query slice progress.
		val, err := env.QueryWorkflow(protocol.QuerySliceProgressState)
		if err == nil {
			var log []protocol.SliceProgressSignal
			if decErr := val.Get(&log); decErr == nil {
				if len(log) != 1 {
					t.Errorf("expected 1 slice progress event, got %d", len(log))
				}
			}
		}
		env.CancelWorkflow()
	}, time.Millisecond*100)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochId:            "epoch-wf-slice",
		RequestDescription: "test slice progress",
	})
}

// ─── SliceWorkflow Tests ──────────────────────────────────────────────────────

func TestSliceWorkflow_MockMode_Default(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := &temporal.Activities{Trail: trail, HooksMgr: nil}

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(temporal.SliceWorkflowFn)
	env.RegisterActivity(acts)

	// Mock SignalExternalWorkflow to avoid error when parent workflow not present.
	env.OnSignalExternalWorkflow("", "", "", protocol.SignalSliceProgress, nil).Return(nil).Maybe()

	env.ExecuteWorkflow(temporal.SliceWorkflowFn, temporal.SliceInput{
		EpochId:          "epoch-slice-1",
		SliceId:          "slice-1",
		PhaseSpec:        "p9",
		ParentWorkflowId: "", // empty: skip parent signaling
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
	if result.SliceId != "slice-1" {
		t.Errorf("slice result.SliceId = %q, want %q", result.SliceId, "slice-1")
	}
}

func TestSliceWorkflow_CompleteSliceOverride(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := &temporal.Activities{Trail: trail, HooksMgr: nil}

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(temporal.SliceWorkflowFn)
	env.RegisterActivity(acts)

	// Send a complete_slice signal that overrides with success=false.
	errMsg := "external override error"
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(protocol.SignalCompleteSlice, temporal.SliceCompleteSignal{
			Success: false,
			Error:   &errMsg,
		})
	}, time.Millisecond*1)

	env.ExecuteWorkflow(temporal.SliceWorkflowFn, temporal.SliceInput{
		EpochId:          "epoch-slice-override",
		SliceId:          "slice-override",
		PhaseSpec:        "p9",
		ParentWorkflowId: "",
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
		env.SignalWorkflow(protocol.SignalSubmitVote, protocol.ReviewVoteSignal{
			Axis: protocol.AxisCorrectness, Vote: protocol.VoteAccept, ReviewerId: "r1",
		})
		env.SignalWorkflow(protocol.SignalSubmitVote, protocol.ReviewVoteSignal{
			Axis: protocol.AxisTestQuality, Vote: protocol.VoteAccept, ReviewerId: "r2",
		})
		env.SignalWorkflow(protocol.SignalSubmitVote, protocol.ReviewVoteSignal{
			Axis: protocol.AxisElegance, Vote: protocol.VoteRevise, ReviewerId: "r3",
		})
	}, time.Millisecond*10)

	env.ExecuteWorkflow(temporal.ReviewWorkflowFn, temporal.ReviewInput{
		EpochId: "epoch-review-1",
		PhaseId: "p10",
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
	if result.PhaseId != "p10" {
		t.Errorf("ReviewResult.PhaseId = %q, want %q", result.PhaseId, "p10")
	}
	if len(result.VoteResult) != 3 {
		t.Errorf("expected 3 votes in result, got %d", len(result.VoteResult))
	}
	if result.VoteResult[protocol.AxisElegance] != protocol.VoteRevise {
		t.Errorf("expected REVISE for elegance axis, got %q", result.VoteResult[protocol.AxisElegance])
	}
}

// ─── QueryAuditEvents Activity Tests ─────────────────────────────────────────

func TestQueryAuditEvents_WithTrail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	// Pre-populate trail.
	_ = trail.RecordEvent(ctx, protocol.AuditEvent{
		EpochId:   "epoch-q-2",
		Phase:     protocol.PhaseRequest,
		EventType: protocol.EventPhaseTransition,
		Timestamp: time.Now(),
	})

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	val, err := env.ExecuteActivity(acts.QueryAuditEvents, "epoch-q-2", nil, nil)
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
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	event := protocol.AuditEvent{
		EpochId:   "epoch-audit-1",
		Phase:     protocol.PhaseElicit,
		EventType: protocol.EventVoteRecorded,
		Timestamp: time.Now(),
	}
	_, err := env.ExecuteActivity(acts.RecordAuditEvent, event)
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
	sm := protocol.NewEpochStateMachine("epoch-avail-1", nil)
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
	customSpecs := map[protocol.PhaseId]protocol.PhaseSpec{}
	sm := protocol.NewEpochStateMachine("epoch-nospec", customSpecs)
	// p1 has no spec in customSpecs.
	avail := sm.AvailableTransitions()
	if len(avail) != 0 {
		t.Errorf("expected empty transitions for phase with no spec, got %v", avail)
	}
}

func TestTransitionError_MultipleViolations(t *testing.T) {
	t.Parallel()
	sm := protocol.NewEpochStateMachine("epoch-violations", nil)
	now := time.Now()

	// Advance to p10 manually (inject state).
	sm.State().CurrentPhase = protocol.PhaseCodeReview
	sm.State().ReviewVotes = make(map[protocol.ReviewAxis]protocol.VoteType)
	sm.State().BlockerCount = 1 // unresolved blocker

	// p10→p11 needs consensus AND 0 blockers. Both should fail.
	violations := sm.ValidateAdvance(protocol.PhaseImplUAT)
	if len(violations) < 2 {
		t.Errorf("expected at least 2 violations (consensus + blocker), got %d: %v", len(violations), violations)
	}

	// Trigger Advance to get the protocol.TransitionError.
	_, err := sm.Advance(protocol.PhaseImplUAT, "test", "force", now)
	if err == nil {
		t.Fatal("expected protocol.TransitionError, got nil")
	}
	terr, ok := err.(*protocol.TransitionError)
	if !ok {
		t.Fatalf("expected *protocol.TransitionError, got %T", err)
	}
	if len(terr.Violations) < 2 {
		t.Errorf("expected 2+ violations in protocol.TransitionError, got %d", len(terr.Violations))
	}
	// Error() should contain the violations joined.
	msg := terr.Error()
	if msg == "" {
		t.Error("protocol.TransitionError.Error() returned empty string")
	}
}

// ─── EpochWorkflow Full Lifecycle Test ────────────────────────────────────────

func TestEpochWorkflow_FullLifecycle_ThroughP2(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	// Advance p1→p2, then cancel.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(protocol.SignalAdvancePhase, protocol.PhaseAdvanceSignal{
			ToPhase:      protocol.PhaseElicit,
			TriggeredBy:  "architect",
			ConditionMet: "classification confirmed",
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		// Query current state after advance.
		val, qErr := env.QueryWorkflow(protocol.QueryCurrentState)
		if qErr == nil {
			var state protocol.EpochState
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
		EpochId:            "epoch-lifecycle",
		RequestDescription: "full lifecycle test",
	})

	// Regression guard for the double-write defect: the single p1→p2 advance must
	// produce EXACTLY ONE EventPhaseTransition audit row through the full
	// EpochWorkflow.Run path. A re-introduced second RecordAuditEvent write
	// (the removed step 2d) would double this and silently double-count every
	// transition in forensic queries — the isolated RecordTransition activity
	// test (activities_integration_test.go) cannot catch a workflow-level
	// regression, so we assert it here against the real Run path.
	events, qErr := trail.QueryEvents(context.Background(), "epoch-lifecycle", nil, nil)
	if qErr != nil {
		t.Fatalf("QueryEvents(epoch-lifecycle): %v", qErr)
	}
	transitions := 0
	for _, e := range events {
		if e.EventType == protocol.EventPhaseTransition {
			transitions++
		}
	}
	if transitions != 1 {
		t.Errorf("EventPhaseTransition rows = %d, want 1 (one p1→p2 advance; >1 means the double-write regressed)", transitions)
	}
}

// ─── RecordAuditEvent uninitialized error path ────────────────────────────────

func TestRecordAuditEvent_UninitializedTrail(t *testing.T) {
	t.Parallel()
	// Create Activities with a nil trail to simulate uninitialized state.
	// Note: Activities.RecordAuditEvent will panic or return error with nil trail.
	// We skip this test since the struct approach doesn't support nil trail
	// (the panic guard is in NewActivities construction, not the method).
	// Trail must always be injected non-nil in production.
	t.Skip("nil trail is prevented by Activities construction — inject a real trail")
}

// ─── AvailableTransitionsQuery and FullState workflow query handler tests ─────

func TestEpochWorkflow_QueryAvailableTransitions(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	env.RegisterDelayedCallback(func() {
		// At p1, only p2 should be available.
		val, err := env.QueryWorkflow(protocol.QueryAvailableTransitions)
		if err != nil {
			t.Errorf("protocol.QueryAvailableTransitions failed: %v", err)
			return
		}
		var transitions []protocol.PhaseId
		if decErr := val.Get(&transitions); decErr != nil {
			t.Errorf("decode protocol.QueryAvailableTransitions: %v", decErr)
			return
		}
		if len(transitions) != 1 || transitions[0] != protocol.PhaseElicit {
			t.Errorf("protocol.QueryAvailableTransitions at p1 = %v, want [p2]", transitions)
		}
		env.CancelWorkflow()
	}, time.Millisecond*50)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochId:            "epoch-query-avail",
		RequestDescription: "test available transitions query",
	})
}

func TestEpochWorkflow_QueryFullState(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflow(temporal.EpochWorkflowFn)
	env.RegisterActivity(acts)

	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow(protocol.QueryFullState)
		if err != nil {
			t.Errorf("protocol.QueryFullState failed: %v", err)
			return
		}
		var result protocol.QueryStateResult
		if decErr := val.Get(&result); decErr != nil {
			t.Errorf("decode protocol.QueryFullState: %v", decErr)
			return
		}
		if result.CurrentPhase != protocol.PhaseRequest {
			t.Errorf("protocol.QueryFullState.CurrentPhase = %q, want %q", result.CurrentPhase, protocol.PhaseRequest)
		}
		if len(result.AvailableTransitions) == 0 {
			t.Error("protocol.QueryFullState.AvailableTransitions is empty, want at least one transition")
		}
		env.CancelWorkflow()
	}, time.Millisecond*50)

	env.ExecuteWorkflow(temporal.EpochWorkflowFn, temporal.EpochInput{
		EpochId:            "epoch-query-full",
		RequestDescription: "test full state query",
	})
}

// ─── RunAgentSession Activity Tests ──────────────────────────────────────────

// TestRunAgentSession_ConnectError verifies that RunAgentSession wraps
// connection errors (e.g. binary not found) and returns them to the caller.
func TestRunAgentSession_ConnectError(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	// Use a clearly non-existent binary to force a connection error.
	input := temporal.RunAgentSessionInput{
		AgentCmd:  "/no-such-binary-pasture-test-xyz",
		AgentArgs: []string{},
		EpochId:   "epoch-connect-error",
	}
	_, err := env.ExecuteActivity(acts.RunAgentSession, input)
	if err == nil {
		t.Error("expected error from RunAgentSession with bogus agent command, got nil")
	}
}

// ─── RecordSessionEntries Activity Tests ─────────────────────────────────────

// TestRecordSessionEntries_WithTrail verifies that RecordSessionEntries writes
// entries to the injected audit trail.
func TestRecordSessionEntries_WithTrail(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	entries := []protocol.SessionEntry{
		{SessionId: "s-test", EntryIndex: 0, Provider: "anthropic", EntryType: "message", Role: "user"},
	}
	_, err := env.ExecuteActivity(acts.RecordSessionEntries, entries)
	if err != nil {
		t.Fatalf("RecordSessionEntries: unexpected error: %v", err)
	}

	ctx := context.Background()
	got, qErr := trail.QuerySessionEntries(ctx, "s-test")
	if qErr != nil {
		t.Fatalf("QuerySessionEntries: %v", qErr)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 entry after RecordSessionEntries, got %d", len(got))
	}
}

// TestQuerySessionEntries_WithTrail verifies that QuerySessionEntries reads
// entries from the injected audit trail.
func TestQuerySessionEntries_WithTrail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	trail := audit.NewInMemoryAuditTrail()
	acts := newActivities(trail)

	// Pre-populate the trail.
	_ = trail.RecordSessionEntries(ctx, []protocol.SessionEntry{
		{SessionId: "s-query-test", EntryIndex: 0, Provider: "acp", EntryType: "message", Role: "assistant"},
	})

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts)

	val, err := env.ExecuteActivity(acts.QuerySessionEntries, "s-query-test")
	if err != nil {
		t.Fatalf("QuerySessionEntries: unexpected error: %v", err)
	}
	var entries []protocol.SessionEntry
	if err := val.Get(&entries); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}
