package protocol_test

import (
	"testing"
	"time"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// These FSM unit tests are re-homed unchanged with the EpochStateMachine into
// pkg/protocol. They exercise the pure 12-phase state machine — phase
// sequencing, the consensus/REVISE/blocker gates, vote clearing, and the
// transition error — independent of any durable substrate.

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
