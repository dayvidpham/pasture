package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// newEngine spins up an engine against a fresh file-backed pasture.db and
// launches it; the engine is shut down on cleanup.
func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// dedupKeysForPhase returns the engine-emitted (dedup_key NOT NULL) keys for a
// given phase across all epochs in the file, read through the shared handle.
func dedupKeysForPhase(t *testing.T, e *engine.Engine, phase string) []string {
	t.Helper()
	rows, err := e.DB().Query(
		`SELECT dedup_key FROM audit_events WHERE phase = ? AND dedup_key IS NOT NULL ORDER BY id`, phase)
	if err != nil {
		t.Fatalf("query dedup keys for phase %q: %v", phase, err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, k)
	}
	return keys
}

func allDedupKeys(t *testing.T, e *engine.Engine) []string {
	t.Helper()
	rows, err := e.DB().Query(`SELECT dedup_key FROM audit_events WHERE dedup_key IS NOT NULL ORDER BY id`)
	if err != nil {
		t.Fatalf("query all dedup keys: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, k)
	}
	return keys
}

func distinct(keys []string) int {
	set := map[string]struct{}{}
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return len(set)
}

// TestEngine_CyclicBounce_DistinctKeys exercises the headline dedup property at
// the ENGINE level (not the pure DedupKey hash): a p4→p3→p4 bounce re-enters the
// review phase, and each re-entry MUST produce a distinct forensic row/key. This
// is what the step_seq basis buys — a re-entered phase is a new durable step, so
// its key differs from the first entry's. It also verifies same-kind multiplicity
// (every PhaseTransition across distinct steps yields a distinct key).
func TestEngine_CyclicBounce_DistinctKeys(t *testing.T) {
	t.Parallel()
	e := newEngine(t)
	const epochId = "epoch-bounce"

	// request → elicit → propose → review → (bounce) propose → review → plan-review
	plan := []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch"},
		{ToPhase: protocol.PhasePropose, TriggeredBy: "architect"},
		{ToPhase: protocol.PhaseReview, TriggeredBy: "architect"}, // review entry #1
		{ToPhase: protocol.PhasePropose, TriggeredBy: "reviewer"}, // bounce back (ungated)
		{ToPhase: protocol.PhaseReview, TriggeredBy: "architect"}, // review entry #2
		{ToPhase: protocol.PhasePlanReview, TriggeredBy: "reviewer", Votes: allAccept()},
	}
	final := runEpoch(t, e, epochId, plan)
	if final.CurrentPhase != protocol.PhasePlanReview {
		t.Fatalf("final phase = %q, want %q", final.CurrentPhase, protocol.PhasePlanReview)
	}

	// Re-entered review phase → 2 distinct forensic rows (cyclic distinctness
	// via distinct step_seq per durable step).
	reviewKeys := dedupKeysForPhase(t, e, "review")
	if len(reviewKeys) != 2 {
		t.Errorf("review dedup rows = %d, want 2 (one per re-entry)", len(reviewKeys))
	}
	if distinct(reviewKeys) != len(reviewKeys) {
		t.Errorf("review re-entries collapsed to one row: keys=%v — cyclic false-dedup", reviewKeys)
	}

	// Every PhaseTransition across distinct steps gets a distinct dedup_key.
	all := allDedupKeys(t, e)
	if len(all) != 6 {
		t.Errorf("total engine-emitted rows = %d, want 6 (one per transition)", len(all))
	}
	if distinct(all) != len(all) {
		t.Errorf("same-kind multiplicity violated: %d rows but only %d distinct keys", len(all), distinct(all))
	}
}

// TestEngine_CrossEpochDistinctKeys: two epochs driven to the same step produce
// distinct rows — the epoch id is hashed into the key, so two epochs at the same
// (phase, step_seq) get distinct keys (no cross-epoch false dedup).
func TestEngine_CrossEpochDistinctKeys(t *testing.T) {
	t.Parallel()
	e := newEngine(t)

	shortPlan := []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch"},
		{ToPhase: protocol.PhasePropose, TriggeredBy: "architect"},
	}
	runEpoch(t, e, "epoch-x", shortPlan)
	runEpoch(t, e, "epoch-y", shortPlan)

	// Each epoch emits a 'propose' transition at the same step_seq; the two keys
	// must differ.
	proposeKeys := dedupKeysForPhase(t, e, "propose")
	if len(proposeKeys) != 2 {
		t.Fatalf("propose dedup rows = %d, want 2 (one per epoch)", len(proposeKeys))
	}
	if proposeKeys[0] == proposeKeys[1] {
		t.Errorf("two epochs at the same (phase, step) produced the same key %q — cross-epoch false dedup", proposeKeys[0])
	}
}

func TestEngine_ReadProjectionUnknownEpoch(t *testing.T) {
	t.Parallel()
	e := newEngine(t)
	proj, err := e.ReadProjection("never-ran")
	if err != nil {
		t.Fatalf("ReadProjection error for unknown epoch: %v", err)
	}
	if proj != nil {
		t.Errorf("ReadProjection = %+v, want nil for an epoch that never ran", proj)
	}
}
