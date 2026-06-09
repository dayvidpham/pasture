package engine_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// newEngine spins up an engine against a fresh file-backed pasture.db and
// launches it; the engine is shut down on cleanup.
func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-v1",
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(5 * time.Second) })
	return e
}

// allAccept is the three-axis consensus needed to pass the p4/p10 gates.
func allAccept() []protocol.ReviewVoteSignal {
	return []protocol.ReviewVoteSignal{
		{Axis: protocol.AxisCorrectness, Vote: protocol.VoteAccept},
		{Axis: protocol.AxisTestQuality, Vote: protocol.VoteAccept},
		{Axis: protocol.AxisElegance, Vote: protocol.VoteAccept},
	}
}

// fullEpochPlan drives request → complete through all 12 phases, supplying the
// consensus votes the review gates require.
func fullEpochPlan() []engine.AdvanceStep {
	return []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch", ConditionMet: "classified"},
		{ToPhase: protocol.PhasePropose, TriggeredBy: "architect", ConditionMet: "elicited"},
		{ToPhase: protocol.PhaseReview, TriggeredBy: "architect", ConditionMet: "proposed"},
		{ToPhase: protocol.PhasePlanReview, TriggeredBy: "reviewer", ConditionMet: "consensus", Votes: allAccept()},
		{ToPhase: protocol.PhaseRatify, TriggeredBy: "architect", ConditionMet: "reviewed"},
		{ToPhase: protocol.PhaseHandoff, TriggeredBy: "architect", ConditionMet: "ratified"},
		{ToPhase: protocol.PhaseImplPlan, TriggeredBy: "supervisor", ConditionMet: "handed off"},
		{ToPhase: protocol.PhaseWorkerSlices, TriggeredBy: "supervisor", ConditionMet: "planned"},
		{ToPhase: protocol.PhaseCodeReview, TriggeredBy: "worker", ConditionMet: "implemented"},
		{ToPhase: protocol.PhaseImplUAT, TriggeredBy: "reviewer", ConditionMet: "consensus", Votes: allAccept()},
		{ToPhase: protocol.PhaseLanding, TriggeredBy: "epoch", ConditionMet: "accepted"},
		{ToPhase: protocol.PhaseComplete, TriggeredBy: "epoch", ConditionMet: "landed"},
	}
}

func runEpoch(t *testing.T, e *engine.Engine, epochId string, plan []engine.AdvanceStep) protocol.EpochState {
	t.Helper()
	h, err := dbos.RunWorkflow(e.DBOS(), e.EpochWorkflow,
		engine.EpochInput{EpochId: epochId, Advances: plan},
		dbos.WithWorkflowID(epochId))
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	final, err := h.GetResult(dbos.WithHandleTimeout(30 * time.Second))
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	return final
}

func TestEngine_Full12PhaseEpoch(t *testing.T) {
	e := newEngine(t)
	const epochId = "epoch-full"

	final := runEpoch(t, e, epochId, fullEpochPlan())

	// Reached the terminal phase.
	if final.CurrentPhase != protocol.PhaseComplete {
		t.Fatalf("final phase = %q, want %q", final.CurrentPhase, protocol.PhaseComplete)
	}

	// Phase sequence: 12 successful transitions recorded.
	if got := len(final.TransitionHistory); got != 12 {
		t.Errorf("transition count = %d, want 12", got)
	}
	for _, rec := range final.TransitionHistory {
		if !rec.Success {
			t.Errorf("transition %s→%s recorded as failure", rec.FromPhase, rec.ToPhase)
		}
	}

	// Votes are phase-scoped and cleared after each advance.
	if len(final.ReviewVotes) != 0 {
		t.Errorf("ReviewVotes not cleared: %v", final.ReviewVotes)
	}

	// One audit row per transition.
	rows, err := e.Trail().QueryEvents(context.Background(), epochId, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(rows) != 12 {
		t.Errorf("audit row count = %d, want 12 (one per transition)", len(rows))
	}

	// Projection reflects the terminal state.
	proj, err := e.ReadProjection(epochId)
	if err != nil {
		t.Fatalf("ReadProjection: %v", err)
	}
	if proj == nil {
		t.Fatal("ReadProjection returned nil for a completed epoch")
	}
	if proj.CurrentPhase != protocol.PhaseComplete {
		t.Errorf("projection phase = %q, want %q", proj.CurrentPhase, protocol.PhaseComplete)
	}
}

func TestEngine_ConsensusGateBlocksWithoutVotes(t *testing.T) {
	e := newEngine(t)
	const epochId = "epoch-gate"

	// Drive to review, then attempt the gated transition WITHOUT votes.
	plan := []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch"},
		{ToPhase: protocol.PhasePropose, TriggeredBy: "architect"},
		{ToPhase: protocol.PhaseReview, TriggeredBy: "architect"},
		{ToPhase: protocol.PhasePlanReview, TriggeredBy: "reviewer"}, // no votes → gate blocks
	}
	final := runEpoch(t, e, epochId, plan)

	// The gated advance failed; the epoch is still at review.
	if final.CurrentPhase != protocol.PhaseReview {
		t.Errorf("final phase = %q, want %q (consensus gate should block)", final.CurrentPhase, protocol.PhaseReview)
	}
	if final.LastError == nil {
		t.Error("expected LastError to be set by the blocked transition")
	}

	// 3 successful transitions + 1 failed attempt recorded.
	var success, failed int
	for _, rec := range final.TransitionHistory {
		if rec.Success {
			success++
		} else {
			failed++
		}
	}
	if success != 3 || failed != 1 {
		t.Errorf("transitions: success=%d failed=%d, want success=3 failed=1", success, failed)
	}

	// Only the 3 successful transitions produced forensic rows.
	rows, err := e.Trail().QueryEvents(context.Background(), epochId, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("audit row count = %d, want 3 (failed transitions are not emitted)", len(rows))
	}
}

func TestEngine_BlockerGateAtCodeReview(t *testing.T) {
	e := newEngine(t)
	const epochId = "epoch-blocker"

	plan := fullEpochPlan()
	// Inject an unresolved blocker on the code-review→impl-uat step (index 9),
	// while still supplying consensus votes — the blocker gate must still block.
	plan[9].BlockerDelta = 1
	// Truncate the plan at the gated transition so the rest doesn't run.
	plan = plan[:10]

	final := runEpoch(t, e, epochId, plan)
	if final.CurrentPhase != protocol.PhaseCodeReview {
		t.Errorf("final phase = %q, want %q (blocker gate should block impl-uat)", final.CurrentPhase, protocol.PhaseCodeReview)
	}
}

func TestEngine_ReadProjectionUnknownEpoch(t *testing.T) {
	e := newEngine(t)
	proj, err := e.ReadProjection("never-ran")
	if err != nil {
		t.Fatalf("ReadProjection error for unknown epoch: %v", err)
	}
	if proj != nil {
		t.Errorf("ReadProjection = %+v, want nil for an epoch that never ran", proj)
	}
}
