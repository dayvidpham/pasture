package engine_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

var controlPhaseEvents sync.Map

func countEvents(rows []protocol.AuditEvent, eventType protocol.EventType) int {
	count := 0
	for _, row := range rows {
		if row.EventType == eventType {
			count++
		}
	}
	return count
}

// newControlEngine launches an engine for the signal-driven control workflow.
// engine.New registers EpochControlWorkflow, so no test-side registration is
// needed.
func newControlEngine(t *testing.T) *engine.Engine {
	t.Helper()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	phaseEvents := make(chan protocol.PhaseId, 32)
	executorID, appVersion := testEngineIdentity(t)
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
		OnTransition: func(_ context.Context, _ string, rec *protocol.TransitionRecord, _ string) error {
			select {
			case phaseEvents <- rec.ToPhase:
			default:
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	controlPhaseEvents.Store(e, phaseEvents)
	t.Cleanup(func() {
		controlPhaseEvents.Delete(e)
		e.Shutdown(5 * time.Second)
	})
	return e
}

// startControl launches the control workflow for epochId and returns its handle.
func startControl(t *testing.T, e *engine.Engine, epochId string) dbos.WorkflowHandle[protocol.EpochState] {
	t.Helper()
	h, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId))
	if err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}
	return h
}

func sendAdvance(t *testing.T, e *engine.Engine, epochId string, to protocol.PhaseId) {
	t.Helper()
	sig := protocol.PhaseAdvanceSignal{ToPhase: to, TriggeredBy: "test", ConditionMet: "ok"}
	if err := dbos.Send(e.DBOS(), epochId, sig, protocol.SignalAdvancePhase.String()); err != nil {
		t.Fatalf("Send(advance_phase=%s): %v", to, err)
	}
}

func sendVote(t *testing.T, e *engine.Engine, epochId string, axis protocol.ReviewAxis) {
	t.Helper()
	sig := protocol.ReviewVoteSignal{Axis: axis, Vote: protocol.VoteAccept, ReviewerId: "r-" + string(axis)}
	if err := dbos.Send(e.DBOS(), epochId, sig, protocol.SignalSubmitVote.String()); err != nil {
		t.Fatalf("Send(submit_vote=%s): %v", axis, err)
	}
}

func sendAllVotes(t *testing.T, e *engine.Engine, epochId string) {
	t.Helper()
	for _, ax := range protocol.AllReviewAxes {
		sendVote(t, e, epochId, ax)
	}
}

// waitPhase polls the projection until the epoch reaches want, or fails after a
// timeout. Signal processing is asynchronous (the workflow wakes on delivery),
// so observation is by polling the durable projection the workflow writes.
func waitPhase(t *testing.T, e *engine.Engine, epochId string, want protocol.PhaseId) *protocol.EpochState {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var phaseEvents <-chan protocol.PhaseId
	if ch, ok := controlPhaseEvents.Load(e); ok {
		phaseEvents = ch.(chan protocol.PhaseId)
	}
	for {
		st, err := e.ReadProjection(epochId)
		if err != nil {
			t.Fatalf("ReadProjection: %v", err)
		}
		if st != nil && st.CurrentPhase == want {
			return st
		}
		if phaseEvents == nil {
			select {
			case <-tick.C:
			case <-deadline.C:
				st, _ := e.ReadProjection(epochId)
				t.Fatalf("epoch %q did not reach %q in time; last projection = %+v", epochId, want, st)
			}
			continue
		}
		select {
		case <-phaseEvents:
		case <-tick.C:
		case <-deadline.C:
			st, _ := e.ReadProjection(epochId)
			t.Fatalf("epoch %q did not reach %q in time; last projection = %+v", epochId, want, st)
		}
	}
}

// advanceTo sends the advance and waits for the projection to reflect it.
func advanceTo(t *testing.T, e *engine.Engine, epochId string, to protocol.PhaseId) *protocol.EpochState {
	t.Helper()
	sendAdvance(t, e, epochId, to)
	return waitPhase(t, e, epochId, to)
}

// TestControl_SignalDrivenAdvanceIsDurable proves an advance_phase signal sent
// via the substrate durably mutates epoch state (the projection + a forensic
// row), driven by the running control workflow rather than a scripted plan.
func TestControl_SignalDrivenAdvanceIsDurable(t *testing.T) {
	t.Parallel()
	e := newControlEngine(t)
	const epochId = "ctl--advance"
	startControl(t, e, epochId)

	advanceTo(t, e, epochId, protocol.PhaseElicit)
	st := advanceTo(t, e, epochId, protocol.PhasePropose)

	if st.CurrentPhase != protocol.PhasePropose {
		t.Fatalf("CurrentPhase = %q, want propose", st.CurrentPhase)
	}
	if len(st.TransitionHistory) != 2 {
		t.Errorf("TransitionHistory = %d, want 2", len(st.TransitionHistory))
	}
	rows, err := e.Trail().QueryEvents(context.Background(), epochId, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("audit rows = %d, want 2 (one per transition)", len(rows))
	}
}

// TestControl_VotesSatisfyConsensusGate proves submit_vote signals recorded
// ahead of a gated advance let it through; without them the gate would block.
func TestControl_VotesSatisfyConsensusGate(t *testing.T) {
	t.Parallel()
	e := newControlEngine(t)
	const epochId = "ctl--votes"
	startControl(t, e, epochId)

	advanceTo(t, e, epochId, protocol.PhaseElicit)
	advanceTo(t, e, epochId, protocol.PhasePropose)
	advanceTo(t, e, epochId, protocol.PhaseReview)

	// Vote on all three axes, THEN request the gated advance.
	sendAllVotes(t, e, epochId)
	st := advanceTo(t, e, epochId, protocol.PhasePlanReview)

	if st.CurrentPhase != protocol.PhasePlanReview {
		t.Fatalf("gated advance did not pass: phase = %q", st.CurrentPhase)
	}
	// Votes are phase-scoped: cleared after the advance.
	if len(st.ReviewVotes) != 0 {
		t.Errorf("ReviewVotes not cleared after advance: %v", st.ReviewVotes)
	}
}

// TestControl_VoteRecordedAuditRows proves each accepted review vote emits its
// own durable forensic row with reviewer identity and vote value preserved.
func TestControl_VoteRecordedAuditRows(t *testing.T) {
	t.Parallel()
	e := newControlEngine(t)
	const epochId = "ctl--vote-audit"
	startControl(t, e, epochId)

	advanceTo(t, e, epochId, protocol.PhaseElicit)
	advanceTo(t, e, epochId, protocol.PhasePropose)
	advanceTo(t, e, epochId, protocol.PhaseReview)

	sendAllVotes(t, e, epochId)
	advanceTo(t, e, epochId, protocol.PhasePlanReview)

	rows, err := e.Trail().QueryEvents(context.Background(), epochId, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}

	seen := map[string]bool{}
	for _, row := range rows {
		if row.EventType != protocol.EventVoteRecorded {
			continue
		}
		axis, _ := row.Payload["axis"].(string)
		vote, _ := row.Payload["vote"].(string)
		reviewerId, _ := row.Payload["reviewerId"].(string)
		if vote != string(protocol.VoteAccept) {
			t.Errorf("VoteRecorded payload vote = %q, want %q", vote, protocol.VoteAccept)
		}
		if reviewerId == "" {
			t.Errorf("VoteRecorded payload missing reviewerId: %#v", row.Payload)
		}
		if row.Role != reviewerId {
			t.Errorf("VoteRecorded role = %q, want reviewerId %q", row.Role, reviewerId)
		}
		seen[axis] = true
	}

	if len(seen) != len(protocol.AllReviewAxes) {
		t.Fatalf("VoteRecorded rows covered %d axes, want %d: %v", len(seen), len(protocol.AllReviewAxes), seen)
	}
	for _, axis := range protocol.AllReviewAxes {
		if !seen[string(axis)] {
			t.Errorf("missing VoteRecorded row for axis %q", axis)
		}
	}
	if got := countEvents(rows, protocol.EventVoteRecorded); got != 3 {
		t.Errorf("VoteRecorded rows = %d, want 3", got)
	}

	var dedupRows, distinctKeys int
	if err := e.DB().QueryRow(
		`SELECT COUNT(*), COUNT(DISTINCT dedup_key)
		   FROM audit_events
		  WHERE event_type = ? AND dedup_key IS NOT NULL`,
		string(protocol.EventVoteRecorded),
	).Scan(&dedupRows, &distinctKeys); err != nil {
		t.Fatalf("count VoteRecorded dedup keys: %v", err)
	}
	if dedupRows != 3 || distinctKeys != 3 {
		t.Fatalf("VoteRecorded dedup rows = %d distinct keys = %d, want 3/3", dedupRows, distinctKeys)
	}
}

// TestControl_RegisterSessionIsIdempotent proves register_session accumulates
// distinct sessions and ignores duplicate session ids.
func TestControl_RegisterSessionIsIdempotent(t *testing.T) {
	t.Parallel()
	e := newControlEngine(t)
	const epochId = "ctl--sessions"
	startControl(t, e, epochId)

	dup := protocol.RegisterSessionSignal{EpochId: epochId, SessionId: "sess-1", Role: "worker"}
	if err := dbos.Send(e.DBOS(), epochId, dup, protocol.SignalRegisterSession.String()); err != nil {
		t.Fatalf("Send(register_session): %v", err)
	}
	if err := dbos.Send(e.DBOS(), epochId, dup, protocol.SignalRegisterSession.String()); err != nil {
		t.Fatalf("Send(register_session dup): %v", err)
	}
	other := protocol.RegisterSessionSignal{EpochId: epochId, SessionId: "sess-2", Role: "reviewer"}
	if err := dbos.Send(e.DBOS(), epochId, other, protocol.SignalRegisterSession.String()); err != nil {
		t.Fatalf("Send(register_session 2): %v", err)
	}

	// Advance drains the side channels before applying, so the post-advance
	// projection reflects the de-duplicated session set.
	st := advanceTo(t, e, epochId, protocol.PhaseElicit)
	if st.ActiveSessionCount != 2 {
		t.Errorf("ActiveSessionCount = %d, want 2 (duplicate ignored)", st.ActiveSessionCount)
	}
	if len(st.ActiveSessions) != 2 {
		t.Errorf("ActiveSessions = %d entries, want 2", len(st.ActiveSessions))
	}
}

// TestControl_SliceProgressAccumulates proves slice_progress events reach the
// projection.
func TestControl_SliceProgressAccumulates(t *testing.T) {
	t.Parallel()
	e := newControlEngine(t)
	const epochId = "ctl--progress"
	startControl(t, e, epochId)

	prog := protocol.SliceProgressSignal{SliceId: "slice-1", LeafTaskId: "leaf-1", StageName: "impl", Completed: true}
	if err := dbos.Send(e.DBOS(), epochId, prog, protocol.SignalSliceProgress.String()); err != nil {
		t.Fatalf("Send(slice_progress): %v", err)
	}
	st := advanceTo(t, e, epochId, protocol.PhaseElicit)
	if len(st.SliceProgress) != 1 {
		t.Fatalf("SliceProgress = %d entries, want 1", len(st.SliceProgress))
	}
	if st.SliceProgress[0].SliceId != "slice-1" {
		t.Errorf("SliceProgress[0].SliceId = %q, want slice-1", st.SliceProgress[0].SliceId)
	}
}

// TestControl_FullEpochDurableRoundTrip drives all 12 phases purely via signals
// and asserts the workflow completes with one forensic row per transition plus
// one row per accepted review vote.
func TestControl_FullEpochDurableRoundTrip(t *testing.T) {
	t.Parallel()
	e := newControlEngine(t)
	const epochId = "ctl--full"
	h := startControl(t, e, epochId)

	advanceTo(t, e, epochId, protocol.PhaseElicit)
	advanceTo(t, e, epochId, protocol.PhasePropose)
	advanceTo(t, e, epochId, protocol.PhaseReview)
	sendAllVotes(t, e, epochId)
	advanceTo(t, e, epochId, protocol.PhasePlanReview)
	advanceTo(t, e, epochId, protocol.PhaseRatify)
	advanceTo(t, e, epochId, protocol.PhaseHandoff)
	advanceTo(t, e, epochId, protocol.PhaseImplPlan)
	advanceTo(t, e, epochId, protocol.PhaseWorkerSlices)
	advanceTo(t, e, epochId, protocol.PhaseCodeReview)
	sendAllVotes(t, e, epochId)
	advanceTo(t, e, epochId, protocol.PhaseImplUAT)
	advanceTo(t, e, epochId, protocol.PhaseLanding)
	sendAdvance(t, e, epochId, protocol.PhaseComplete)

	final, err := h.GetResult(dbos.WithHandleTimeout(30 * time.Second))
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if final.CurrentPhase != protocol.PhaseComplete {
		t.Fatalf("final phase = %q, want complete", final.CurrentPhase)
	}
	rows, err := e.Trail().QueryEvents(context.Background(), epochId, nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if got := countEvents(rows, protocol.EventPhaseTransition); got != 12 {
		t.Errorf("PhaseTransition rows = %d, want 12", got)
	}
	if got := countEvents(rows, protocol.EventVoteRecorded); got != 6 {
		t.Errorf("VoteRecorded rows = %d, want 6", got)
	}
}
