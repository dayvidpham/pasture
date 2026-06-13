package engine_test

// Tests for slice/review sub-workflows dispatched via the DBOS slice queue.
//
// Test plan:
//
//  1. SliceSubWorkflow round-trips: mock-mode slice (with explicit start_slice
//     signal) completes and the parent receives a slice_progress signal (basic
//     round-trip + row-count invariant).
//
//  2. SliceSubWorkflow signal integration: start_slice sets a non-default mode
//     (subprocess with a command) and the result reflects it; complete_slice
//     overrides the computed result and the assert is deterministic (the gate
//     hook holds the slice until the override signal is delivered).
//
//  3. Bounded concurrency: with K=2 and N=4 enqueued slices (all with explicit
//     mock start signals), the high-water-mark of concurrent in-flight slices
//     is exactly K and never exceeds K. Measured via a gating hooks.Manager:
//     HookSliceStarted increments an atomic counter, records the high-water
//     mark, and blocks on a release channel; the test verifies HWM==K while
//     N-K remain unstarted, then releases and asserts all N complete.
//
//  4. ReviewSubWorkflow round-trips: submitting all three review-axis votes
//     unblocks the sub-workflow; a REVISE vote sets Success=false.
//
//  5. Review vote-gate semantics: last-writer-wins re-vote (REVISE→ACCEPT on
//     the same axis = Success=true) and partial-vote gate-hold (2-of-3 axes
//     voted → workflow still pending).
//
//  6. ReviewSubWorkflow round-2 runs a FRESH sub-workflow after a REVISE round
//     (proves the round component prevents memoized stale results).
//
//  7. Exit-3 for a never-started slice id at the handler level (both
//     SliceStart and SliceComplete return exit 3 when the slice workflow id
//     has never been created as a DBOS workflow).
//
//  8. Hook surface: SliceStarted/SliceCompleted/SliceFailed fire exactly when
//     specified; a failing slice (via complete_slice override Success=false)
//     fires SliceFailed and NOT SliceCompleted.
//
//  9. runSlice mode table-test: all four branches (mock success, tmux/subprocess
//     not-implemented failure, unrecognised-mode failure) assert Success, output
//     prefix, and error contents. Each branch is exercised by delivering an
//     explicit start_slice signal with the target mode.
//
// 10. Queue wiring: default concurrency wires the correct queue name; the
//     concurrency-resolution precedence (flag > env > default) is table-tested
//     across all flag/env combinations including error paths.
//
// 11. No-signal failure: a slice enqueued with no start_slice signal within the
//     2s window returns Success=false with an actionable error message, fires
//     SliceFailed (not SliceCompleted), and the parent receives Completed=false.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ── Fixtures ──────────────────────────────────────────────────────────────────

// newQueueEngine opens an engine with the given concurrency limit K.
// k <= 0 uses the default (DefaultSliceQueueConcurrency).
func newQueueEngine(t *testing.T, k int) *engine.Engine {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-v1",
		SliceConcurrency:   k,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(10 * time.Second) })
	return e
}

// newQueueEngineWithHooks is like newQueueEngine but wires the given hooks
// manager so dispatchHook delivers events to it.
func newQueueEngineWithHooks(t *testing.T, k int, mgr *hooks.Manager) *engine.Engine {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-v1",
		SliceConcurrency:   k,
		HooksMgr:           mgr,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(10 * time.Second) })
	return e
}

// waitSliceResult calls GetResult with a timeout and fails the test on error.
func waitSliceResult(t *testing.T, h dbos.WorkflowHandle[engine.SliceResult], timeout time.Duration) engine.SliceResult {
	t.Helper()
	res, err := h.GetResult(dbos.WithHandleTimeout(timeout))
	if err != nil {
		t.Fatalf("GetResult(slice): %v", err)
	}
	return res
}

// waitReviewResult calls GetResult with a timeout and fails the test on error.
func waitReviewResult(t *testing.T, h dbos.WorkflowHandle[engine.ReviewResult], timeout time.Duration) engine.ReviewResult {
	t.Helper()
	res, err := h.GetResult(dbos.WithHandleTimeout(timeout))
	if err != nil {
		t.Fatalf("GetResult(review): %v", err)
	}
	return res
}

// recordingHandler is a HookHandler that records every payload it receives
// and optionally blocks on a gate channel before returning (simulating a
// slow or gating handler for concurrency tests).
type recordingHandler struct {
	mu         sync.Mutex
	events     []hooks.HookPayload
	gate       chan struct{} // if non-nil, Handle blocks until gate is closed
	subscribed []hooks.HookEvent
}

func newRecordingHandler(gate chan struct{}, events ...hooks.HookEvent) *recordingHandler {
	return &recordingHandler{gate: gate, subscribed: events}
}

func (h *recordingHandler) Events() []hooks.HookEvent { return h.subscribed }

func (h *recordingHandler) Handle(ctx context.Context, p hooks.HookPayload) (hooks.HandleOutcome, error) {
	if h.gate != nil {
		select {
		case <-h.gate: // released
		case <-ctx.Done(): // dispatch timeout hit
		}
	}
	h.mu.Lock()
	h.events = append(h.events, p)
	h.mu.Unlock()
	return hooks.HandleOutcome{}, nil
}

func (h *recordingHandler) recorded() []hooks.HookPayload {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]hooks.HookPayload, len(h.events))
	copy(out, h.events)
	return out
}

func (h *recordingHandler) countOf(event hooks.HookEvent) int {
	count := 0
	for _, p := range h.recorded() {
		if p.Event == event {
			count++
		}
	}
	return count
}

// gatingConcurrencyHandler is a HookHandler that gates HookSliceStarted events:
// it increments an atomic in-flight counter, records the high-water mark, and
// blocks on the release channel until it is closed. Used by the bounded-
// concurrency test to observe how many sub-workflows are simultaneously started
// while the remainder are still queued.
type gatingConcurrencyHandler struct {
	inFlight atomic.Int64
	hwm      atomic.Int64
	release  chan struct{}
}

func (h *gatingConcurrencyHandler) Events() []hooks.HookEvent {
	return []hooks.HookEvent{hooks.HookSliceStarted}
}

func (h *gatingConcurrencyHandler) Handle(ctx context.Context, p hooks.HookPayload) (hooks.HandleOutcome, error) {
	cur := h.inFlight.Add(1)
	for {
		old := h.hwm.Load()
		if cur <= old || h.hwm.CompareAndSwap(old, cur) {
			break
		}
	}
	// Block until released or the dispatch context deadline fires (5s).
	select {
	case <-h.release:
	case <-ctx.Done():
	}
	h.inFlight.Add(-1)
	return hooks.HandleOutcome{}, nil
}

// ── Test 1: Round-trip ────────────────────────────────────────────────────────

// TestSliceSubWorkflow_MockMode_CompletesAndReportsProgress verifies that a
// mock-mode slice enqueued via Engine.EnqueueSlice completes successfully and
// the sub-workflow delivers a slice_progress signal to the parent epoch workflow.
// A start_slice signal with mode=mock is delivered explicitly: without it the
// sub-workflow returns an honest failure (no-signal path), which is pinned by
// TestSliceSubWorkflow_NoStartSignal_FailsHonestly.
func TestSliceSubWorkflow_MockMode_CompletesAndReportsProgress(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	const epochId = "queue--slice-mock-1"
	h, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId))
	if err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}
	_ = h

	sliceId := epochId + "--slice-a"
	sh, err := e.EnqueueSlice(engine.SliceInput{
		EpochId:          epochId,
		SliceId:          sliceId,
		ParentWorkflowId: epochId,
	})
	if err != nil {
		t.Fatalf("EnqueueSlice: %v", err)
	}

	// Deliver an explicit mock start_slice signal so the sub-workflow takes the
	// mock-success path rather than the no-signal failure path.
	sendMockStartSignal(t, e, sliceId, 10*time.Second)

	result := waitSliceResult(t, sh, 20*time.Second)
	if !result.Success {
		errVal := "<nil>"
		if result.Error != nil {
			errVal = *result.Error
		}
		t.Fatalf("slice result Success=false; error=%s", errVal)
	}
	if result.SliceId != sliceId {
		t.Errorf("result.SliceId = %q, want %q", result.SliceId, sliceId)
	}

	// Verify the progress signal reached the parent.
	sig := protocol.PhaseAdvanceSignal{ToPhase: protocol.PhaseElicit, TriggeredBy: "test", ConditionMet: "ok"}
	if err := dbos.Send(e.DBOS(), epochId, sig, protocol.SignalAdvancePhase.String()); err != nil {
		t.Fatalf("Send(advance_phase): %v", err)
	}
	st := waitPhase(t, e, epochId, protocol.PhaseElicit)
	if len(st.SliceProgress) != 1 {
		t.Errorf("SliceProgress entries = %d, want 1", len(st.SliceProgress))
	}
	if len(st.SliceProgress) > 0 && st.SliceProgress[0].SliceId != sliceId {
		t.Errorf("SliceProgress[0].SliceId = %q, want %q", st.SliceProgress[0].SliceId, sliceId)
	}
}

// ── Test 2: Signal integration ────────────────────────────────────────────────

// TestSliceSubWorkflow_StartSignalSetsMode verifies that a start_slice signal
// with a NON-DEFAULT mode (subprocess) is consumed by the Recv loop and the
// result reflects the mode that was signalled (subprocess returns a
// not-implemented failure, proving the signal path was taken rather than the
// default mock path which would succeed).
//
// The signal is delivered via a spin-poll loop immediately after EnqueueSlice
// so it lands in the notifications table before the 2s Recv window closes.
// This makes the outcome deterministic: subprocess mode → Success=false with a
// not-yet-implemented error (distinct from the default mock → Success=true).
func TestSliceSubWorkflow_StartSignalSetsMode(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	const epochId = "queue--slice-start-sig-v2"
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	sliceId := epochId + "--slice-b"
	sh, err := e.EnqueueSlice(engine.SliceInput{
		EpochId:          epochId,
		SliceId:          sliceId,
		ParentWorkflowId: epochId,
	})
	if err != nil {
		t.Fatalf("EnqueueSlice: %v", err)
	}

	// Spin-poll until the sub-workflow is addressable (workflow row exists), then
	// deliver the start_slice signal with subprocess mode. The workflow's Recv
	// window is 2s; we send as soon as the workflow is alive so the signal is
	// consumed before the window closes.
	startSig := protocol.SliceStartSignal{
		Mode:    protocol.SliceSubprocess,
		Command: "echo test-command",
	}
	deadline := time.Now().Add(10 * time.Second)
	sent := false
	for time.Now().Before(deadline) {
		if serr := dbos.Send(e.DBOS(), sliceId, startSig, protocol.SignalStartSlice.String()); serr == nil {
			sent = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sent {
		t.Fatal("start_slice signal could not be delivered within 10s — sub-workflow never became addressable")
	}

	result := waitSliceResult(t, sh, 20*time.Second)
	// The signal was delivered before the 2s Recv window closed; subprocess
	// mode returns a not-implemented failure (Success=false), proving the signal
	// path was taken rather than the default mock path.
	if result.Success {
		t.Errorf("expected Success=false (subprocess mode → not-implemented error); got true")
	}
	if result.Error == nil {
		t.Errorf("expected Error to be set for subprocess mode; got nil")
	} else if !strings.Contains(*result.Error, "not yet implemented") {
		t.Errorf("expected Error to contain %q; got: %s", "not yet implemented", *result.Error)
	}
	if result.SliceId != sliceId {
		t.Errorf("result.SliceId = %q, want %q", result.SliceId, sliceId)
	}
}

// TestSliceSubWorkflow_CompleteSignalOverridesResult verifies that a
// complete_slice signal with Success=false deterministically overrides the
// mock-mode success result.
//
// Approach: use a gating hooks.Manager that blocks on HookSliceStarted. The
// gate fires after the start_slice Recv but BEFORE the durable step runs.
// While the sub-workflow is held at the gate we enqueue the complete_slice
// signal. The post-step Recv window (1s) then finds the queued signal and
// applies the override.
func TestSliceSubWorkflow_CompleteSignalOverridesResult(t *testing.T) {
	gate := make(chan struct{})
	rec := newRecordingHandler(gate, hooks.HookSliceStarted)
	mgr := hooks.NewManager(hooks.WithDispatchTimeout(4 * time.Second))
	mgr.Register(rec)

	e := newQueueEngineWithHooks(t, engine.DefaultSliceQueueConcurrency, mgr)

	const epochId = "queue--slice-override-v2"
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	sliceId := epochId + "--slice-c"
	sh, err := e.EnqueueSlice(engine.SliceInput{
		EpochId:          epochId,
		SliceId:          sliceId,
		ParentWorkflowId: epochId,
	})
	if err != nil {
		t.Fatalf("EnqueueSlice: %v", err)
	}

	sendMockStartSignal(t, e, sliceId, 10*time.Second)

	// Wait until the sub-workflow fires HookSliceStarted (it is gated here).
	waitUntil(t, 10*time.Second, func() bool { return rec.countOf(hooks.HookSliceStarted) > 0 })

	// Deliver the complete_slice override while the sub-workflow is gated.
	// The post-step Recv will pick this up after the gate releases.
	errMsg := "override: forced failure"
	override := protocol.SliceCompleteSignal{Success: false, Error: &errMsg}
	if err := dbos.Send(e.DBOS(), sliceId, override, protocol.SignalCompleteSlice.String()); err != nil {
		t.Fatalf("Send(complete_slice) while gated: %v", err)
	}

	// Release the gate — step runs, then the post-step Recv finds the override.
	close(gate)

	result := waitSliceResult(t, sh, 20*time.Second)

	// The override was delivered before the step ran; post-step Recv must find it.
	if result.Success {
		t.Errorf("expected Success=false after complete_slice override; got true")
	}
	if result.Error == nil || *result.Error != errMsg {
		t.Errorf("result.Error = %v, want %q", result.Error, errMsg)
	}
	if result.SliceId != sliceId {
		t.Errorf("result.SliceId = %q, want %q", result.SliceId, sliceId)
	}
}

// ── Test 3: Bounded concurrency ───────────────────────────────────────────────

// TestSliceQueue_BoundedConcurrency verifies that with K=2 and N=4 enqueued
// slices, the maximum number of simultaneously in-flight sub-workflows never
// exceeds K and equals exactly K at peak.
//
// Measurement: a gating hooks.Manager that blocks HookSliceStarted handlers.
// Each handler increments an in-flight counter (recording the high-water mark)
// then blocks until released. K sub-workflows will reach HookSliceStarted and
// block; the remaining N-K must stay in the DBOS queue (they cannot fire
// HookSliceStarted because the queue's K slots are occupied). The test asserts:
//   - high-water mark == K (proves real concurrency, not full serialisation)
//   - high-water mark <= K (proves the bound is enforced)
//   - while K sub-workflows are gated, N-K have NOT reached HookSliceStarted
//   - after release, all N complete successfully
//   - exactly N SliceProgress rows reach the parent epoch (no drops, no doubles)
//
// All slices receive explicit mock start_slice signals; the no-signal failure
// path (pinned by TestSliceSubWorkflow_NoStartSignal_FailsHonestly) is not
// exercised here.
func TestSliceQueue_BoundedConcurrency(t *testing.T) {
	const K = 2
	const N = 4

	gater := &gatingConcurrencyHandler{release: make(chan struct{})}
	mgr := hooks.NewManager(hooks.WithDispatchTimeout(4 * time.Second))
	mgr.Register(gater)

	e := newQueueEngineWithHooks(t, K, mgr)

	const epochId = "queue--bounded-cc-v2"
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	handles := make([]dbos.WorkflowHandle[engine.SliceResult], N)
	sliceIds := make([]string, N)
	for i := 0; i < N; i++ {
		sliceId := epochId + "--cc-" + fmt.Sprintf("%02x", i)
		sliceIds[i] = sliceId
		h, err := e.EnqueueSlice(engine.SliceInput{
			EpochId:          epochId,
			SliceId:          sliceId,
			ParentWorkflowId: epochId,
		})
		if err != nil {
			t.Fatalf("EnqueueSlice[%d]: %v", i, err)
		}
		handles[i] = h
	}

	// Deliver explicit mock start_slice signals to all N slices concurrently so
	// each sub-workflow takes the mock-success path when its queue slot opens.
	for _, sliceId := range sliceIds {
		sliceId := sliceId
		go sendMockStartSignal(t, e, sliceId, 30*time.Second)
	}

	// Wait until exactly K sub-workflows are gated at HookSliceStarted.
	waitUntil(t, 15*time.Second, func() bool {
		return gater.hwm.Load() >= int64(K)
	})

	// With K slots occupied, N-K sub-workflows must NOT have started yet
	// (their HookSliceStarted has not fired — their gater.inFlight contribution
	// is zero because DBOS has not dequeued them).
	hwm := gater.hwm.Load()
	if hwm > int64(K) {
		t.Errorf("high-water mark = %d, want <= %d (concurrency bound exceeded)", hwm, K)
	}
	if hwm < int64(K) {
		t.Errorf("high-water mark = %d, want >= %d (expected K concurrent in-flight)", hwm, K)
	}

	// Release the gate — all blocked handlers unblock and sub-workflows complete.
	close(gater.release)

	// All N slices must eventually complete.
	for i, h := range handles {
		res := waitSliceResult(t, h, 30*time.Second)
		if !res.Success {
			errVal := "<nil>"
			if res.Error != nil {
				errVal = *res.Error
			}
			t.Errorf("slice[%d] result Success=false; error=%s", i, errVal)
		}
	}

	// Row-count invariant: exactly N SliceProgress signals reach the parent.
	sig := protocol.PhaseAdvanceSignal{ToPhase: protocol.PhaseElicit, TriggeredBy: "test", ConditionMet: "ok"}
	if err := dbos.Send(e.DBOS(), epochId, sig, protocol.SignalAdvancePhase.String()); err != nil {
		t.Fatalf("Send(advance_phase): %v", err)
	}
	st := waitPhase(t, e, epochId, protocol.PhaseElicit)
	if len(st.SliceProgress) != N {
		t.Errorf("SliceProgress entries = %d, want %d (row-count invariant)", len(st.SliceProgress), N)
	}
}

// TestSliceQueue_BackpressureAllEventuallyComplete verifies that 30 mock slices
// all eventually complete exactly once when dispatched via the slice queue, even
// when K < 30. Excess slices are held in the DBOS queues table and dequeued as
// earlier ones finish. This is the single-process drain invariant.
//
// All slices receive explicit mock start_slice signals dispatched concurrently.
// The signals are pre-populated in the DBOS notifications table; each slice
// consumes its signal when its queue slot opens.
func TestSliceQueue_BackpressureAllEventuallyComplete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 30-slice backpressure test in short mode")
	}
	const K = 4
	const N = 30

	e := newQueueEngine(t, K)

	const epochId = "queue--backpressure"
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	handles := make([]dbos.WorkflowHandle[engine.SliceResult], N)
	sliceIds := make([]string, N)
	for i := 0; i < N; i++ {
		sliceId := epochId + "--bp-" + fmt.Sprintf("%02x", i)
		sliceIds[i] = sliceId
		h, err := e.EnqueueSlice(engine.SliceInput{
			EpochId:          epochId,
			SliceId:          sliceId,
			ParentWorkflowId: epochId,
		})
		if err != nil {
			t.Fatalf("EnqueueSlice[%d]: %v", i, err)
		}
		handles[i] = h
	}

	// Deliver explicit mock start_slice signals to all N slices concurrently.
	// The spin-poll inside sendMockStartSignal handles the timing window: each
	// sub-workflow consumes its signal when its queue slot opens (DBOS holds
	// the signal in the notifications table until the workflow dequeues).
	var sigWg sync.WaitGroup
	for _, sliceId := range sliceIds {
		sliceId := sliceId
		sigWg.Add(1)
		go func() {
			defer sigWg.Done()
			sendMockStartSignal(t, e, sliceId, 90*time.Second)
		}()
	}

	var wg sync.WaitGroup
	var failures atomic.Int64
	for i, h := range handles {
		wg.Add(1)
		go func(idx int, h dbos.WorkflowHandle[engine.SliceResult]) {
			defer wg.Done()
			res, err := h.GetResult(dbos.WithHandleTimeout(60 * time.Second))
			if err != nil {
				t.Logf("slice[%d] GetResult error: %v", idx, err)
				failures.Add(1)
				return
			}
			if !res.Success {
				errVal := "<nil>"
				if res.Error != nil {
					errVal = *res.Error
				}
				t.Logf("slice[%d] Success=false; error=%s", idx, errVal)
				failures.Add(1)
			}
		}(i, h)
	}
	wg.Wait()
	sigWg.Wait()

	if got := failures.Load(); got != 0 {
		t.Errorf("%d of %d slices failed", got, N)
	}

	sig := protocol.PhaseAdvanceSignal{ToPhase: protocol.PhaseElicit, TriggeredBy: "test", ConditionMet: "ok"}
	if err := dbos.Send(e.DBOS(), epochId, sig, protocol.SignalAdvancePhase.String()); err != nil {
		t.Fatalf("Send(advance_phase): %v", err)
	}
	st := waitPhase(t, e, epochId, protocol.PhaseElicit)
	if len(st.SliceProgress) != N {
		t.Errorf("SliceProgress entries = %d, want %d (row-count invariant: no drops, no doubles)", len(st.SliceProgress), N)
	}
}

// ── Test 4: Review round-trip ─────────────────────────────────────────────────

// TestReviewSubWorkflow_AllVotesUnblocksResult verifies that submitting all
// three review-axis votes via dbos.Send unblocks the review sub-workflow and
// returns a ReviewResult with the correct per-axis vote map.
func TestReviewSubWorkflow_AllVotesUnblocksResult(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	const epochId = "queue--review-1"
	const phaseId = "review"

	rh, err := e.EnqueueReview(engine.ReviewInput{
		EpochId: epochId,
		PhaseId: phaseId,
	})
	if err != nil {
		t.Fatalf("EnqueueReview: %v", err)
	}

	// Round defaults to 1 when ReviewInput.Round is 0.
	reviewWfID := protocol.ReviewWorkflowID(epochId, phaseId, 1)

	// Poll until the sub-workflow is addressable.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		probeSig := protocol.ReviewVoteSignal{
			Axis:       protocol.AxisCorrectness,
			Vote:       protocol.VoteAccept,
			ReviewerId: "r-probe",
		}
		if err := dbos.Send(e.DBOS(), reviewWfID, probeSig, protocol.SignalSubmitVote.String()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	for _, ax := range []protocol.ReviewAxis{protocol.AxisTestQuality, protocol.AxisElegance} {
		sig := protocol.ReviewVoteSignal{Axis: ax, Vote: protocol.VoteAccept, ReviewerId: "r-" + string(ax)}
		if err := dbos.Send(e.DBOS(), reviewWfID, sig, protocol.SignalSubmitVote.String()); err != nil {
			t.Fatalf("Send(submit_vote %s): %v", ax, err)
		}
	}

	result := waitReviewResult(t, rh, 20*time.Second)
	if !result.Success {
		t.Fatalf("review result Success=false; votes=%v", result.VoteResult)
	}
	if len(result.VoteResult) != len(protocol.AllReviewAxes) {
		t.Errorf("VoteResult len = %d, want %d", len(result.VoteResult), len(protocol.AllReviewAxes))
	}
}

// TestReviewSubWorkflow_ReviseSetsSuccessFalse verifies that a REVISE vote on
// any axis causes the ReviewResult to have Success=false.
func TestReviewSubWorkflow_ReviseSetsSuccessFalse(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	const epochId = "queue--review-revise"
	const phaseId = "code-review"

	rh, err := e.EnqueueReview(engine.ReviewInput{
		EpochId: epochId,
		PhaseId: phaseId,
	})
	if err != nil {
		t.Fatalf("EnqueueReview: %v", err)
	}

	reviewWfID := protocol.ReviewWorkflowID(epochId, phaseId, 1)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		probeSig := protocol.ReviewVoteSignal{Axis: protocol.AxisCorrectness, Vote: protocol.VoteRevise, ReviewerId: "r-1"}
		if err := dbos.Send(e.DBOS(), reviewWfID, probeSig, protocol.SignalSubmitVote.String()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, ax := range []protocol.ReviewAxis{protocol.AxisTestQuality, protocol.AxisElegance} {
		sig := protocol.ReviewVoteSignal{Axis: ax, Vote: protocol.VoteAccept, ReviewerId: "r-" + string(ax)}
		if err := dbos.Send(e.DBOS(), reviewWfID, sig, protocol.SignalSubmitVote.String()); err != nil {
			t.Fatalf("Send(submit_vote %s): %v", ax, err)
		}
	}

	result := waitReviewResult(t, rh, 20*time.Second)
	if result.Success {
		t.Fatalf("review result Success=true despite a REVISE vote; votes=%v", result.VoteResult)
	}
}

// ── Test 5: Review vote-gate semantics ────────────────────────────────────────

// TestReviewSubWorkflow_LastWriterWins verifies that a re-vote on the same axis
// supersedes the earlier vote. REVISE then ACCEPT on correctness must produce
// Success=true (all axes ACCEPT).
func TestReviewSubWorkflow_LastWriterWins(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	const epochId = "queue--review-lww"
	const phaseId = "review"

	rh, err := e.EnqueueReview(engine.ReviewInput{
		EpochId: epochId,
		PhaseId: phaseId,
	})
	if err != nil {
		t.Fatalf("EnqueueReview: %v", err)
	}

	reviewWfID := protocol.ReviewWorkflowID(epochId, phaseId, 1)

	// Poll until addressable, sending the first REVISE vote.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		probeSig := protocol.ReviewVoteSignal{Axis: protocol.AxisCorrectness, Vote: protocol.VoteRevise, ReviewerId: "r-1"}
		if err := dbos.Send(e.DBOS(), reviewWfID, probeSig, protocol.SignalSubmitVote.String()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Re-vote correctness with ACCEPT (must overwrite the REVISE).
	for _, sig := range []protocol.ReviewVoteSignal{
		{Axis: protocol.AxisCorrectness, Vote: protocol.VoteAccept, ReviewerId: "r-1"},
		{Axis: protocol.AxisTestQuality, Vote: protocol.VoteAccept, ReviewerId: "r-2"},
		{Axis: protocol.AxisElegance, Vote: protocol.VoteAccept, ReviewerId: "r-3"},
	} {
		if err := dbos.Send(e.DBOS(), reviewWfID, sig, protocol.SignalSubmitVote.String()); err != nil {
			t.Fatalf("Send(submit_vote %s): %v", sig.Axis, err)
		}
	}

	result := waitReviewResult(t, rh, 20*time.Second)
	if !result.Success {
		t.Errorf("expected Success=true (last-writer-wins: ACCEPT supersedes REVISE); votes=%v", result.VoteResult)
	}
	if got := result.VoteResult[protocol.AxisCorrectness]; got != protocol.VoteAccept {
		t.Errorf("VoteResult[correctness] = %q, want ACCEPT (last-writer-wins)", got)
	}
}

// TestReviewSubWorkflow_PartialVoteGateHolds verifies that submitting only 2 of
// 3 axes does NOT unblock the sub-workflow: GetResult must time out because the
// loop keeps polling.
func TestReviewSubWorkflow_PartialVoteGateHolds(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	const epochId = "queue--review-partial"
	const phaseId = "review"

	rh, err := e.EnqueueReview(engine.ReviewInput{
		EpochId: epochId,
		PhaseId: phaseId,
	})
	if err != nil {
		t.Fatalf("EnqueueReview: %v", err)
	}

	reviewWfID := protocol.ReviewWorkflowID(epochId, phaseId, 1)

	// Poll until addressable.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		probeSig := protocol.ReviewVoteSignal{Axis: protocol.AxisCorrectness, Vote: protocol.VoteAccept, ReviewerId: "r-1"}
		if err := dbos.Send(e.DBOS(), reviewWfID, probeSig, protocol.SignalSubmitVote.String()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Send only 2 of 3 votes — stop before elegance.
	sig2 := protocol.ReviewVoteSignal{Axis: protocol.AxisTestQuality, Vote: protocol.VoteAccept, ReviewerId: "r-2"}
	if err := dbos.Send(e.DBOS(), reviewWfID, sig2, protocol.SignalSubmitVote.String()); err != nil {
		t.Fatalf("Send(submit_vote test_quality): %v", err)
	}

	// With only 2 votes, GetResult must time out (the workflow is still polling).
	_, err = rh.GetResult(dbos.WithHandleTimeout(2 * time.Second))
	if err == nil {
		t.Fatal("expected GetResult to time out with only 2 of 3 votes; got a result")
	}

	// Now send the third vote to unblock.
	sig3 := protocol.ReviewVoteSignal{Axis: protocol.AxisElegance, Vote: protocol.VoteAccept, ReviewerId: "r-3"}
	if serr := dbos.Send(e.DBOS(), reviewWfID, sig3, protocol.SignalSubmitVote.String()); serr != nil {
		t.Fatalf("Send(submit_vote elegance): %v", serr)
	}
	result := waitReviewResult(t, rh, 10*time.Second)
	if !result.Success {
		t.Errorf("expected Success=true after all 3 votes; got false; votes=%v", result.VoteResult)
	}
}

// ── Test 6: Round-2 runs a fresh sub-workflow ─────────────────────────────────

// TestReviewSubWorkflow_Round2RunsFreshWorkflow verifies that after a REVISE
// round completes, enqueuing a round-2 review (ReviewInput.Round=2) runs a
// FRESH sub-workflow with a different workflow id, and its result is independent
// of the round-1 result. This proves the round component prevents DBOS from
// returning the memoized round-1 (REVISE) result for the iterate-until-ACCEPT loop.
func TestReviewSubWorkflow_Round2RunsFreshWorkflow(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	const epochId = "queue--review-round2"
	const phaseId = "review"

	// ── Round 1: all REVISE → Success=false.
	rh1, err := e.EnqueueReview(engine.ReviewInput{EpochId: epochId, PhaseId: phaseId, Round: 1})
	if err != nil {
		t.Fatalf("EnqueueReview(round=1): %v", err)
	}

	reviewWfID1 := protocol.ReviewWorkflowID(epochId, phaseId, 1)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		probeSig := protocol.ReviewVoteSignal{Axis: protocol.AxisCorrectness, Vote: protocol.VoteRevise, ReviewerId: "r1-c"}
		if err := dbos.Send(e.DBOS(), reviewWfID1, probeSig, protocol.SignalSubmitVote.String()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, ax := range []protocol.ReviewAxis{protocol.AxisTestQuality, protocol.AxisElegance} {
		sig := protocol.ReviewVoteSignal{Axis: ax, Vote: protocol.VoteRevise, ReviewerId: "r1-" + string(ax)}
		if err := dbos.Send(e.DBOS(), reviewWfID1, sig, protocol.SignalSubmitVote.String()); err != nil {
			t.Fatalf("Send(round1 vote %s): %v", ax, err)
		}
	}
	r1 := waitReviewResult(t, rh1, 20*time.Second)
	if r1.Success {
		t.Fatalf("round-1 expected Success=false (all REVISE); got true")
	}

	// ── Round 2: different workflow id; all ACCEPT → Success=true.
	reviewWfID2 := protocol.ReviewWorkflowID(epochId, phaseId, 2)
	if reviewWfID2 == reviewWfID1 {
		t.Fatalf("round-2 workflow id equals round-1 id %q — round component not differentiating", reviewWfID1)
	}

	rh2, err := e.EnqueueReview(engine.ReviewInput{EpochId: epochId, PhaseId: phaseId, Round: 2})
	if err != nil {
		t.Fatalf("EnqueueReview(round=2): %v", err)
	}

	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		probeSig := protocol.ReviewVoteSignal{Axis: protocol.AxisCorrectness, Vote: protocol.VoteAccept, ReviewerId: "r2-c"}
		if err := dbos.Send(e.DBOS(), reviewWfID2, probeSig, protocol.SignalSubmitVote.String()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, ax := range []protocol.ReviewAxis{protocol.AxisTestQuality, protocol.AxisElegance} {
		sig := protocol.ReviewVoteSignal{Axis: ax, Vote: protocol.VoteAccept, ReviewerId: "r2-" + string(ax)}
		if err := dbos.Send(e.DBOS(), reviewWfID2, sig, protocol.SignalSubmitVote.String()); err != nil {
			t.Fatalf("Send(round2 vote %s): %v", ax, err)
		}
	}
	r2 := waitReviewResult(t, rh2, 20*time.Second)
	if !r2.Success {
		t.Errorf("round-2 expected Success=true (all ACCEPT); got false; votes=%v", r2.VoteResult)
	}
}

// ── Test 7: Exit-3 for never-started slice id at handler level ────────────────

// TestHandler_SliceStart_WorkflowError_NeverStartedSlice_Exit3 verifies that
// SliceStart returns exit 3 (CategoryWorkflow) when the target slice id has
// never been started as a DBOS workflow.
func TestHandler_SliceStart_WorkflowError_NeverStartedSlice_Exit3(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	code, hErr := handlers.SliceStart(ctrl,
		"demo--ffffffff-ffff-7fff-8fff-ff0000000099",
		protocol.SliceMock, "", 0, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for a never-started slice; got nil")
	}
	if code != 3 {
		t.Fatalf("SliceStart exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}

// TestHandler_SliceComplete_WorkflowError_NeverStartedSlice_Exit3 verifies
// that SliceComplete returns exit 3 for a never-started slice id.
func TestHandler_SliceComplete_WorkflowError_NeverStartedSlice_Exit3(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController: %v", err)
	}
	defer ctrl.Close()

	out := "done"
	code, hErr := handlers.SliceComplete(ctrl,
		"demo--ffffffff-ffff-7fff-8fff-ff0000000098",
		&out, nil, types.OutputText)
	if hErr == nil {
		t.Fatal("expected a workflow error for a never-started slice; got nil")
	}
	if code != 3 {
		t.Fatalf("SliceComplete exit = %d, want 3 (workflow error); err = %v", code, hErr)
	}
}

// ── Test 8: Hook surface coverage ────────────────────────────────────────────

// TestSliceSubWorkflow_HookSliceStartedAndCompleted verifies that a successful
// mock slice fires exactly HookSliceStarted then HookSliceCompleted, and NOT
// HookSliceFailed. An explicit mock start_slice signal is delivered so the
// sub-workflow takes the mock-success path.
func TestSliceSubWorkflow_HookSliceStartedAndCompleted(t *testing.T) {
	rec := newRecordingHandler(nil,
		hooks.HookSliceStarted, hooks.HookSliceCompleted, hooks.HookSliceFailed)
	mgr := hooks.NewManager()
	mgr.Register(rec)

	e := newQueueEngineWithHooks(t, engine.DefaultSliceQueueConcurrency, mgr)

	const epochId = "queue--hook-success"
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	sliceId := epochId + "--hook-s"
	sh, err := e.EnqueueSlice(engine.SliceInput{
		EpochId:          epochId,
		SliceId:          sliceId,
		ParentWorkflowId: epochId,
	})
	if err != nil {
		t.Fatalf("EnqueueSlice: %v", err)
	}

	sendMockStartSignal(t, e, sliceId, 10*time.Second)

	res := waitSliceResult(t, sh, 20*time.Second)
	if !res.Success {
		t.Fatalf("expected Success=true; err=%v", res.Error)
	}

	// Give hooks a moment to be recorded (they run in durable steps).
	waitUntil(t, 5*time.Second, func() bool {
		return rec.countOf(hooks.HookSliceCompleted) > 0
	})

	if rec.countOf(hooks.HookSliceStarted) != 1 {
		t.Errorf("HookSliceStarted count = %d, want 1", rec.countOf(hooks.HookSliceStarted))
	}
	if rec.countOf(hooks.HookSliceCompleted) != 1 {
		t.Errorf("HookSliceCompleted count = %d, want 1", rec.countOf(hooks.HookSliceCompleted))
	}
	if rec.countOf(hooks.HookSliceFailed) != 0 {
		t.Errorf("HookSliceFailed count = %d, want 0 (success path should not fire SliceFailed)", rec.countOf(hooks.HookSliceFailed))
	}

	// Verify payload fields.
	events := rec.recorded()
	for _, p := range events {
		if p.EpochId != epochId {
			t.Errorf("hook payload EpochId = %q, want %q", p.EpochId, epochId)
		}
	}
}

// TestSliceSubWorkflow_HookSliceFailed verifies that a slice that fails (via
// complete_slice override Success=false) fires HookSliceFailed and NOT
// HookSliceCompleted (after the HookSliceStarted that always fires).
func TestSliceSubWorkflow_HookSliceFailed(t *testing.T) {
	gate := make(chan struct{})
	rec := newRecordingHandler(gate,
		hooks.HookSliceStarted, hooks.HookSliceCompleted, hooks.HookSliceFailed)
	// Non-gating recorder for completed/failed (gate only on started).
	recFail := newRecordingHandler(nil,
		hooks.HookSliceCompleted, hooks.HookSliceFailed)
	mgr := hooks.NewManager(hooks.WithDispatchTimeout(4 * time.Second))
	mgr.Register(rec)
	mgr.Register(recFail)

	e := newQueueEngineWithHooks(t, engine.DefaultSliceQueueConcurrency, mgr)

	const epochId = "queue--hook-fail"
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	sliceId := epochId + "--hook-f"
	sh, err := e.EnqueueSlice(engine.SliceInput{
		EpochId:          epochId,
		SliceId:          sliceId,
		ParentWorkflowId: epochId,
	})
	if err != nil {
		t.Fatalf("EnqueueSlice: %v", err)
	}

	sendMockStartSignal(t, e, sliceId, 10*time.Second)

	// Wait until HookSliceStarted fires (rec handler is gated here).
	waitUntil(t, 10*time.Second, func() bool { return rec.countOf(hooks.HookSliceStarted) > 0 })

	// Deliver complete_slice override with Success=false.
	errMsg := "hook-test forced failure"
	override := protocol.SliceCompleteSignal{Success: false, Error: &errMsg}
	if err := dbos.Send(e.DBOS(), sliceId, override, protocol.SignalCompleteSlice.String()); err != nil {
		t.Fatalf("Send(complete_slice): %v", err)
	}

	// Release the gate.
	close(gate)

	res := waitSliceResult(t, sh, 20*time.Second)
	if res.Success {
		t.Fatalf("expected Success=false after failure override; got true")
	}

	// Give hooks time to be recorded.
	waitUntil(t, 5*time.Second, func() bool {
		return recFail.countOf(hooks.HookSliceFailed) > 0
	})

	if recFail.countOf(hooks.HookSliceFailed) != 1 {
		t.Errorf("HookSliceFailed count = %d, want 1", recFail.countOf(hooks.HookSliceFailed))
	}
	if recFail.countOf(hooks.HookSliceCompleted) != 0 {
		t.Errorf("HookSliceCompleted count = %d, want 0 (failure path should not fire SliceCompleted)", recFail.countOf(hooks.HookSliceCompleted))
	}
}

// TestSliceSubWorkflow_HookNilManagerIsNoop verifies that a nil HooksMgr
// causes no panics and the slice completes normally (best-effort, non-fatal).
// An explicit mock start_slice signal is delivered so the sub-workflow takes
// the mock-success path.
func TestSliceSubWorkflow_HookNilManagerIsNoop(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency) // no HooksMgr

	const epochId = "queue--hook-nil"
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	sliceId := epochId + "--nil"
	sh, err := e.EnqueueSlice(engine.SliceInput{
		EpochId:          epochId,
		SliceId:          sliceId,
		ParentWorkflowId: epochId,
	})
	if err != nil {
		t.Fatalf("EnqueueSlice: %v", err)
	}

	sendMockStartSignal(t, e, sliceId, 10*time.Second)

	res := waitSliceResult(t, sh, 20*time.Second)
	if !res.Success {
		t.Fatalf("expected Success=true with nil HooksMgr; err=%v", res.Error)
	}
}

// ── Test 9: runSlice mode table-test ─────────────────────────────────────────

// TestRunSlice_AllModes is a table-test for all four runSlice mode branches,
// exercised through the full sub-workflow dispatch path (EnqueueSlice +
// start_slice signal + GetResult). Each sub-test enqueues a slice and delivers
// an explicit start_slice signal with the target mode; the result reflects the
// mode-specific branch without relying on any default fallback.
//
//   - mock → Success=true, Output="mock: completed"
//   - tmux with command → Success=false, Error mentions not-yet-implemented
//   - subprocess with command → Success=false, Error mentions not-yet-implemented
//   - unrecognised mode → Success=false, Error mentions the mode and valid modes
func TestRunSlice_AllModes(t *testing.T) {
	tests := []struct {
		name        string
		mode        protocol.SliceExecutionMode
		command     string
		wantSuccess bool
		wantOutput  string   // prefix check (non-empty to assert)
		wantErrSubs []string // substrings that must appear in *Error
	}{
		{
			name:        "mock success",
			mode:        protocol.SliceMock,
			wantSuccess: true,
			wantOutput:  "mock: completed",
		},
		{
			name:        "tmux not-implemented",
			mode:        protocol.SliceTmux,
			command:     "echo hi",
			wantSuccess: false,
			wantErrSubs: []string{"not yet implemented", "complete --slice-id"},
		},
		{
			name:        "subprocess not-implemented",
			mode:        protocol.SliceSubprocess,
			command:     "bash -c 'exit 0'",
			wantSuccess: false,
			wantErrSubs: []string{"not yet implemented", "complete --slice-id"},
		},
		{
			name:        "unrecognised mode",
			mode:        protocol.SliceExecutionMode("docker"),
			wantSuccess: false,
			wantErrSubs: []string{"unrecognised execution mode", "mock, tmux, subprocess"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

			const epochId = "queue--runslice-table"
			if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
				engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
				t.Fatalf("RunWorkflow(control): %v", err)
			}

			sliceId := epochId + "--" + string(tc.mode)

			// Set the mode via start_slice signal BEFORE the sub-workflow dequeues.
			// Pre-populate the notifications table by spinning until send succeeds.
			sh, err := e.EnqueueSlice(engine.SliceInput{
				EpochId:          epochId,
				SliceId:          sliceId,
				ParentWorkflowId: epochId,
			})
			if err != nil {
				t.Fatalf("EnqueueSlice: %v", err)
			}

			// Always deliver an explicit start_slice signal for the target mode.
			// Every sub-test requires a signal — without it the sub-workflow returns
			// the no-signal failure (pinned by TestSliceSubWorkflow_NoStartSignal_FailsHonestly).
			startSig := protocol.SliceStartSignal{Mode: tc.mode, Command: tc.command}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if serr := dbos.Send(e.DBOS(), sliceId, startSig, protocol.SignalStartSlice.String()); serr == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}

			res := waitSliceResult(t, sh, 25*time.Second)
			if res.Success != tc.wantSuccess {
				t.Errorf("Success = %v, want %v; error=%v", res.Success, tc.wantSuccess, res.Error)
			}
			if tc.wantOutput != "" && res.Output != tc.wantOutput {
				t.Errorf("Output = %q, want %q", res.Output, tc.wantOutput)
			}
			if len(tc.wantErrSubs) > 0 {
				if res.Error == nil {
					t.Errorf("expected Error to contain %v; got nil", tc.wantErrSubs)
				} else {
					for _, sub := range tc.wantErrSubs {
						if !strings.Contains(*res.Error, sub) {
							t.Errorf("Error does not contain %q; got: %s", sub, *res.Error)
						}
					}
				}
			}
		})
	}
}

// ── Test 10: Queue wiring ─────────────────────────────────────────────────────

// TestSliceQueue_DefaultConcurrency verifies that the default concurrency is
// applied when Config.SliceConcurrency is 0, and that the queue name is correct.
// The SliceQueue().Name check is the real wiring assertion (the queue was
// created with that name in the DBOS system). SliceConcurrency() is the stored
// resolved value (not a re-derivation).
func TestSliceQueue_DefaultConcurrency(t *testing.T) {
	e := newQueueEngine(t, 0) // 0 → DefaultSliceQueueConcurrency
	if got := e.SliceConcurrency(); got != engine.DefaultSliceQueueConcurrency {
		t.Errorf("SliceConcurrency() = %d, want %d (default)", got, engine.DefaultSliceQueueConcurrency)
	}
	if e.SliceQueue().Name != engine.SliceQueueName {
		t.Errorf("SliceQueue().Name = %q, want %q", e.SliceQueue().Name, engine.SliceQueueName)
	}
	if e.ControlQueue().Name != engine.ControlQueueName {
		t.Errorf("ControlQueue().Name = %q, want %q", e.ControlQueue().Name, engine.ControlQueueName)
	}
}

// TestResolveSliceConcurrency_Precedence table-tests the flag > env > default
// resolution rule for the slice-queue concurrency knob.
func TestResolveSliceConcurrency_Precedence(t *testing.T) {
	tests := []struct {
		name    string
		flagVal int
		envVal  string
		want    int
		wantErr bool
	}{
		{
			name:    "flag wins over env and default",
			flagVal: 5,
			envVal:  "3",
			want:    5,
		},
		{
			name:    "env wins over default when flag is 0",
			flagVal: 0,
			envVal:  "3",
			want:    3,
		},
		{
			name:    "default when both unset",
			flagVal: 0,
			envVal:  "",
			want:    engine.DefaultSliceQueueConcurrency,
		},
		{
			name:    "invalid env value returns error",
			flagVal: 0,
			envVal:  "not-an-int",
			wantErr: true,
		},
		{
			name:    "zero env value returns error",
			flagVal: 0,
			envVal:  "0",
			wantErr: true,
		},
		{
			name:    "negative env value returns error",
			flagVal: 0,
			envVal:  "-1",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.envVal != "" {
				t.Setenv(engine.SliceConcurrencyEnv, tc.envVal)
			} else {
				// Ensure the env var is unset for this test case.
				t.Setenv(engine.SliceConcurrencyEnv, "")
			}

			got, err := engine.ResolveSliceConcurrency(tc.flagVal)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error; got nil (result=%d)", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tc.want {
				t.Errorf("ResolveSliceConcurrency(%d) with env=%q = %d, want %d", tc.flagVal, tc.envVal, got, tc.want)
			}
		})
	}
}

// TestEnqueueSlice_EmptyIdRejectsWithValidationError verifies that EnqueueSlice
// returns a CategoryValidation error when SliceId or EpochId is empty.
func TestEnqueueSlice_EmptyIdRejectsWithValidationError(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	// Empty SliceId.
	_, err := e.EnqueueSlice(engine.SliceInput{EpochId: "ep-1", SliceId: ""})
	if err == nil {
		t.Fatal("expected error for empty SliceId; got nil")
	}

	// Empty EpochId.
	_, err = e.EnqueueSlice(engine.SliceInput{EpochId: "", SliceId: "sl-1"})
	if err == nil {
		t.Fatal("expected error for empty EpochId; got nil")
	}
}

// TestEnqueueReview_EmptyIdRejectsWithValidationError verifies that EnqueueReview
// returns a CategoryValidation error when EpochId or PhaseId is empty.
func TestEnqueueReview_EmptyIdRejectsWithValidationError(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	// Empty EpochId.
	_, err := e.EnqueueReview(engine.ReviewInput{EpochId: "", PhaseId: "review"})
	if err == nil {
		t.Fatal("expected error for empty EpochId; got nil")
	}

	// Empty PhaseId.
	_, err = e.EnqueueReview(engine.ReviewInput{EpochId: "ep-1", PhaseId: ""})
	if err == nil {
		t.Fatal("expected error for empty PhaseId; got nil")
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// waitUntil polls cond every 20ms until it returns true or the deadline is
// exceeded, at which point it fails the test.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// Note: use strings.Contains from the standard library for substring checks
// in this file. The hand-rolled contains helper has been removed.

// sendMockStartSignal spin-polls until the slice sub-workflow at sliceId is
// addressable, then delivers a start_slice signal with mode=mock. It reports
// failure via t.Errorf (safe to call from goroutines) if the workflow does not
// become addressable within timeout.
func sendMockStartSignal(t *testing.T, e *engine.Engine, sliceId string, timeout time.Duration) {
	t.Helper()
	sig := protocol.SliceStartSignal{Mode: protocol.SliceMock}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := dbos.Send(e.DBOS(), sliceId, sig, protocol.SignalStartSlice.String()); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("start_slice(mock) signal for %q not delivered within %s — sub-workflow never became addressable", sliceId, timeout)
}

// ── Test 11: No-signal honest failure ─────────────────────────────────────────

// TestSliceSubWorkflow_NoStartSignal_FailsHonestly pins the no-signal failure
// path: a slice enqueued with no start_slice signal within the 2s window must
// return Success=false with an actionable error message, fire HookSliceFailed
// (not HookSliceCompleted), and deliver Completed=false to the parent.
//
// This test deliberately does NOT send a start_slice signal.
func TestSliceSubWorkflow_NoStartSignal_FailsHonestly(t *testing.T) {
	recFail := newRecordingHandler(nil,
		hooks.HookSliceCompleted, hooks.HookSliceFailed)
	mgr := hooks.NewManager(hooks.WithDispatchTimeout(4 * time.Second))
	mgr.Register(recFail)

	e := newQueueEngineWithHooks(t, engine.DefaultSliceQueueConcurrency, mgr)

	const epochId = "queue--no-signal-fail"
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	sliceId := epochId + "--no-sig"
	sh, err := e.EnqueueSlice(engine.SliceInput{
		EpochId:          epochId,
		SliceId:          sliceId,
		ParentWorkflowId: epochId,
	})
	if err != nil {
		t.Fatalf("EnqueueSlice: %v", err)
	}

	// Do NOT send a start_slice signal. The sub-workflow must time out and
	// record an honest failure within the 2s window + execution time.
	// Allow a generous 15s for the sub-workflow to reach the timeout and complete.
	res := waitSliceResult(t, sh, 15*time.Second)

	if res.Success {
		t.Errorf("expected Success=false when no start_slice signal is sent; got true")
	}
	if res.Error == nil {
		t.Fatalf("expected Error to be set for no-signal failure; got nil")
	}
	if !strings.Contains(*res.Error, "no start_slice signal received") {
		t.Errorf("Error must contain %q; got: %s", "no start_slice signal received", *res.Error)
	}
	if !strings.Contains(*res.Error, sliceId) {
		t.Errorf("Error must mention the slice id %q; got: %s", sliceId, *res.Error)
	}
	if res.SliceId != sliceId {
		t.Errorf("result.SliceId = %q, want %q", res.SliceId, sliceId)
	}

	// Give hooks a moment to be recorded.
	waitUntil(t, 5*time.Second, func() bool {
		return recFail.countOf(hooks.HookSliceFailed) > 0
	})

	if recFail.countOf(hooks.HookSliceFailed) != 1 {
		t.Errorf("HookSliceFailed count = %d, want 1 (no-signal failure must fire SliceFailed)", recFail.countOf(hooks.HookSliceFailed))
	}
	if recFail.countOf(hooks.HookSliceCompleted) != 0 {
		t.Errorf("HookSliceCompleted count = %d, want 0 (no-signal failure must NOT fire SliceCompleted)", recFail.countOf(hooks.HookSliceCompleted))
	}

	// Verify Completed=false reached the parent: advance_phase unblocks the
	// control workflow, then inspect SliceProgress.
	sig := protocol.PhaseAdvanceSignal{ToPhase: protocol.PhaseElicit, TriggeredBy: "test", ConditionMet: "ok"}
	if err := dbos.Send(e.DBOS(), epochId, sig, protocol.SignalAdvancePhase.String()); err != nil {
		t.Fatalf("Send(advance_phase): %v", err)
	}
	st := waitPhase(t, e, epochId, protocol.PhaseElicit)
	if len(st.SliceProgress) != 1 {
		t.Errorf("SliceProgress entries = %d, want 1", len(st.SliceProgress))
	}
	if len(st.SliceProgress) > 0 && st.SliceProgress[0].Completed {
		t.Errorf("SliceProgress[0].Completed = true, want false (no-signal failure must report Completed=false)")
	}
}

// ── Test 12: Junk-vote guard does not poison consensus ────────────────────────

// TestReviewSubWorkflow_JunkVoteDropped verifies that an invalid-axis or
// invalid-vote signal sent mid-review does not flip or poison the consensus
// verdict. After the junk vote, three canonical ACCEPT votes must produce
// Success=true with exactly three canonical axes in VoteResult (junk key absent).
//
// This test pins the workflow-level validation guard in review.go that drops
// signals where !sig.Axis.IsValid() || !sig.Vote.IsValid().
func TestReviewSubWorkflow_JunkVoteDropped(t *testing.T) {
	e := newQueueEngine(t, engine.DefaultSliceQueueConcurrency)

	const epochId = "queue--review-junk-vote"
	const phaseId = "code-review"

	rh, err := e.EnqueueReview(engine.ReviewInput{
		EpochId: epochId,
		PhaseId: phaseId,
	})
	if err != nil {
		t.Fatalf("EnqueueReview: %v", err)
	}

	reviewWfID := protocol.ReviewWorkflowID(epochId, phaseId, 1)

	// Poll until addressable, delivering a junk-axis REVISE as the first vote.
	// The guard at review.go must drop it (axis "bad_axis" is not in AllReviewAxes).
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		junkSig := protocol.ReviewVoteSignal{
			Axis:       protocol.ReviewAxis("bad_axis"),
			Vote:       protocol.VoteRevise,
			ReviewerId: "r-junk",
		}
		if err := dbos.Send(e.DBOS(), reviewWfID, junkSig, protocol.SignalSubmitVote.String()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Now send the three canonical ACCEPT votes.
	for _, ax := range protocol.AllReviewAxes {
		sig := protocol.ReviewVoteSignal{
			Axis:       ax,
			Vote:       protocol.VoteAccept,
			ReviewerId: "r-" + string(ax),
		}
		if err := dbos.Send(e.DBOS(), reviewWfID, sig, protocol.SignalSubmitVote.String()); err != nil {
			t.Fatalf("Send(submit_vote %s): %v", ax, err)
		}
	}

	result := waitReviewResult(t, rh, 20*time.Second)
	if !result.Success {
		t.Errorf("expected Success=true after three canonical ACCEPTs following a junk vote; got false; votes=%v", result.VoteResult)
	}
	// The junk axis must not appear in VoteResult.
	if _, ok := result.VoteResult[protocol.ReviewAxis("bad_axis")]; ok {
		t.Errorf("junk axis %q must not appear in VoteResult; got %v", "bad_axis", result.VoteResult)
	}
	// Exactly the three canonical axes must be present.
	if len(result.VoteResult) != len(protocol.AllReviewAxes) {
		t.Errorf("VoteResult len = %d, want %d (canonical axes only)", len(result.VoteResult), len(protocol.AllReviewAxes))
	}
}

// ── Test 13: OpenEpochController does not own engine queue config ─────────────

// TestOpenEpochController_DoesNotResolveSliceConcurrency verifies the CLI
// controller no longer constructs an engine or owns slice-queue configuration.
// Invalid PASTURE_SLICE_CONCURRENCY values belong to pastured startup, not
// client-backed signal submission.
func TestOpenEpochController_DoesNotResolveSliceConcurrency(t *testing.T) {
	t.Setenv(engine.SliceConcurrencyEnv, "not-a-number")

	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController must ignore %s because it no longer owns engine queues; got: %v", engine.SliceConcurrencyEnv, err)
	}
	defer ctrl.Close()
}
