package engine_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"

	_ "modernc.org/sqlite"
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

// blockEngineAgentRegistration installs a trigger that makes every insert into
// agents_software fail, which is what engine.New's agent resolution does when
// the engine's own software agent is absent. It is the least invasive way to
// fail construction AFTER the durable context and the allocator binding exist,
// and dropTrigger removes it again so the same tracker can be reused.
func blockEngineAgentRegistration(t *testing.T, dbPath string) (dropTrigger func()) {
	t.Helper()
	// Same busy timeout the engine's own handle uses, so installing and
	// dropping the trigger waits for a writer instead of failing on a lock.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open %q to install the failure trigger: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const create = `CREATE TRIGGER pasture_test_block_software_agent BEFORE INSERT ON agents_software
	                BEGIN SELECT RAISE(ABORT, 'agents_software writes are blocked by this test'); END`
	if _, err := db.Exec(create); err != nil {
		t.Fatalf("install the failure trigger on %q: %v", dbPath, err)
	}
	return func() {
		if _, err := db.Exec(`DROP TRIGGER pasture_test_block_software_agent`); err != nil {
			t.Fatalf("drop the failure trigger on %q: %v", dbPath, err)
		}
	}
}

// Coverage of the construction-abort path, and why it stops where it does.
//
// engine.New calls one shared abort closure from five failure sites. The two
// tests below reach two of them: the engine-agent resolution failure, and the
// refusal to bind a second engine to one tracker. The other three — the
// allocator construction and the two queue registrations — run the SAME closure
// body, and they are deliberately left uncovered: Config cannot reach them.
// A slice concurrency of zero or less is clamped to the default before the
// queue is registered, a negative polling interval is dropped rather than
// passed down, and both queue names are constants, so the durable runtime's own
// configuration rejections are unreachable from the public constructor. A
// test-only seam in production code would be the only way in, and that trade is
// refused: it would add a second code path to buy coverage of a body these two
// tests already execute.

// TestEngineNew_AbortsCleanlyWhenConstructionFailsAfterTheDurableContext pins
// the abort path in engine.New: a failure raised after the durable context and
// the allocator binding exist must stop that context and hand the tracker's
// engine-owned allocator slot back, so the caller can construct another engine
// on the same tracker.
//
// Without the unbind, the second engine.New below fails to bind, because the
// tracker still points at the allocator of the engine that was never built.
func TestEngineNew_AbortsCleanlyWhenConstructionFailsAfterTheDurableContext(t *testing.T) {
	t.Parallel()
	tracker, dbPath := testutil.OpenGoldenTaskTracker(t)
	executorID, appVersion := testEngineIdentity(t)
	cfg := engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
		Trail:                    tracker,
		Tracker:                  tracker,
	}

	dropTrigger := blockEngineAgentRegistration(t, dbPath)

	e, err := engine.New(context.Background(), cfg)
	if err == nil {
		e.Shutdown(5 * time.Second)
		t.Fatal("engine.New succeeded while the engine agent could not be registered; wanted a construction failure")
	}
	if e != nil {
		t.Fatalf("engine.New returned a non-nil engine together with error %v", err)
	}
	if !strings.Contains(err.Error(), "forensic software agent") {
		t.Fatalf("engine.New error = %v, want the engine-agent registration failure", err)
	}

	// The abort path ran to the end: the allocator slot is free, so the same
	// tracker accepts a second engine.
	dropTrigger()
	second, err := engine.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("engine.New after an aborted construction on the same tracker: %v", err)
	}
	t.Cleanup(func() { second.Shutdown(5 * time.Second) })
	if err := second.Launch(); err != nil {
		t.Fatalf("engine.Launch on the replacement engine: %v", err)
	}
}

// TestEngineNew_AbortsWhenTheTrackerAlreadyDrivesAnEngine covers the abort call
// site on the allocator-binding failure: a tracker accepts exactly one
// engine-owned allocator, so a second engine on a tracker that already drives a
// live one must be refused and torn down.
//
// It also pins the limit of the unwind added for the failure paths below the
// binding: this abort installed nothing, so it must leave the FIRST engine's
// binding in place. A third construction therefore fails the same way.
func TestEngineNew_AbortsWhenTheTrackerAlreadyDrivesAnEngine(t *testing.T) {
	t.Parallel()
	tracker, dbPath := testutil.OpenGoldenTaskTracker(t)
	executorID, appVersion := testEngineIdentity(t)
	cfg := engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
		Trail:                    tracker,
		Tracker:                  tracker,
	}

	first, err := engine.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("engine.New for the first engine: %v", err)
	}
	t.Cleanup(func() { first.Shutdown(5 * time.Second) })

	for attempt := 1; attempt <= 2; attempt++ {
		second, err := engine.New(context.Background(), cfg)
		if err == nil {
			second.Shutdown(5 * time.Second)
			t.Fatalf("engine.New attempt %d on a tracker that already drives an engine succeeded; wanted a refusal", attempt)
		}
		if second != nil {
			t.Fatalf("engine.New attempt %d returned a non-nil engine together with error %v", attempt, err)
		}
		if !strings.Contains(err.Error(), "slice allocator") {
			t.Fatalf("engine.New attempt %d error = %v, want the allocator-binding refusal", attempt, err)
		}
	}

	// The refused constructions did not disturb the engine that owns the
	// tracker: it still runs an epoch to completion.
	if err := first.Launch(); err != nil {
		t.Fatalf("engine.Launch on the surviving engine: %v", err)
	}
	final := runEpoch(t, first, "epoch-abort-bind", fullEpochPlan())
	if final.CurrentPhase != protocol.PhaseComplete {
		t.Errorf("surviving engine final phase = %q, want %q", final.CurrentPhase, protocol.PhaseComplete)
	}
}

// blockingTransitionEngine builds and launches an engine whose transition hook
// stops the FIRST transition inside its durable step and holds it there. It
// returns the engine, a channel closed once the hook is inside the step, and a
// release function the test must call to let the held work finish.
//
// This is the production seam Config.OnTransition, not a test-only hook: the
// same field carries idempotent activity recording in the daemon. Holding it
// is the only way to put real work in flight across a shutdown.
func blockingTransitionEngine(t *testing.T) (e *engine.Engine, entered <-chan struct{}, release func()) {
	t.Helper()
	enteredCh := make(chan struct{})
	releaseCh := make(chan struct{})
	var holdOnce sync.Once
	var releaseOnce sync.Once

	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)
	built, err := engine.New(context.Background(), engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
		OnTransition: func(context.Context, string, *protocol.TransitionRecord, string) error {
			holdOnce.Do(func() {
				close(enteredCh)
				select {
				case <-releaseCh:
				case <-time.After(30 * time.Second):
					// A ceiling, never a wait for its own sake: if the test
					// fails before releasing, the held step still unwinds
					// instead of pinning a goroutine for the whole run.
				}
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := built.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	release = func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)
	return built, enteredCh, release
}

// TestEngineShutdown_ReturnsNilWhenTheRuntimeStopsInsideItsBudget pins the
// clean path: an engine with nothing left to do reports success, so a caller
// that acts on the returned error only acts on a real failure.
func TestEngineShutdown_ReturnsNilWhenTheRuntimeStopsInsideItsBudget(t *testing.T) {
	t.Parallel()
	e := newEngine(t)

	final := runEpoch(t, e, "epoch-clean-shutdown", fullEpochPlan())
	if final.CurrentPhase != protocol.PhaseComplete {
		t.Fatalf("final phase = %q, want %q", final.CurrentPhase, protocol.PhaseComplete)
	}

	if err := e.Shutdown(10 * time.Second); err != nil {
		t.Fatalf("Shutdown after a completed epoch = %v, want nil", err)
	}
}

// TestEngineShutdown_NamesTheWorkStillRunningWhenTheBudgetExpires pins the
// timeout path end to end: work that is still running when the budget expires
// must produce an error that says WHICH part of the runtime was still busy,
// carries the machine-readable detail, and maps to the workflow exit code.
//
// It also pins the budget's meaning. The argument is a per-component budget,
// so a caller could reasonably fear paying it once per part. Measured on the
// pinned runtime, only the in-flight-work wait expires here, so the whole
// shutdown costs about one budget; the ceiling below fails if a later runtime
// starts spending it again on the parts that follow.
func TestEngineShutdown_NamesTheWorkStillRunningWhenTheBudgetExpires(t *testing.T) {
	t.Parallel()
	e, entered, release := blockingTransitionEngine(t)

	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochWorkflow,
		engine.EpochInput{EpochId: "epoch-stuck-shutdown", Advances: fullEpochPlan()},
		dbos.WithWorkflowID("epoch-stuck-shutdown")); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the transition hook never reached its durable step, so no work was in flight to hold the shutdown")
	}

	const budget = time.Second
	start := time.Now()
	err := e.Shutdown(budget)
	elapsed := time.Since(start)
	release()

	if err == nil {
		t.Fatal("Shutdown with work still running = nil, want an incomplete-shutdown error")
	}

	var incomplete *engine.ShutdownIncompleteError
	if !errors.As(err, &incomplete) {
		t.Fatalf("Shutdown error %v does not carry *engine.ShutdownIncompleteError", err)
	}
	if incomplete.PerComponentTimeout != budget {
		t.Errorf("PerComponentTimeout = %v, want the budget %v", incomplete.PerComponentTimeout, budget)
	}
	if !slices.Contains(incomplete.Pending, engine.ShutdownComponentWorkflows) {
		t.Errorf("Pending = %v, want it to name %q", incomplete.Pending, engine.ShutdownComponentWorkflows)
	}

	// The message an operator reads must name the part AND say what it is.
	msg := err.Error()
	for _, want := range []string{
		string(engine.ShutdownComponentWorkflows),
		engine.ShutdownComponentWorkflows.Meaning(),
		budget.String(),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Shutdown error message does not mention %q:\n%s", want, msg)
		}
	}

	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("Shutdown error %v is not actionable (no structured error in the chain)", err)
	}
	if structured.Category != pasterrors.CategoryWorkflow {
		t.Errorf("category = %q, want %q", structured.Category, pasterrors.CategoryWorkflow)
	}
	if got, want := pasterrors.ExitCode(err), 3; got != want {
		t.Errorf("exit code = %d, want %d", got, want)
	}

	if elapsed < budget {
		t.Errorf("Shutdown returned after %v, sooner than the %v budget it was given", elapsed, budget)
	}
	if ceiling := engine.WorstCaseShutdownDuration(budget); elapsed >= ceiling {
		t.Errorf("Shutdown took %v, at or beyond the worst case %v: the budget is now being spent on more than the in-flight-work wait", elapsed, ceiling)
	}
}

// TestShutdownComponent_ReportsAnUnrecognisedPart pins the open end of the
// component list: a runtime build that adds a part must still be reported by
// name, and described as one this build does not know, rather than dropped.
func TestShutdownComponent_ReportsAnUnrecognisedPart(t *testing.T) {
	t.Parallel()
	const invented = engine.ShutdownComponent("telemetry exporter")
	if got := invented.Meaning(); !strings.Contains(got, "does not recognise") {
		t.Errorf("Meaning() for an unknown part = %q, want it to say the part is unrecognised", got)
	}
	if got := engine.ShutdownComponentWorkflows.Meaning(); strings.Contains(got, "does not recognise") {
		t.Errorf("Meaning() for a known part = %q, want its plain description", got)
	}
}
