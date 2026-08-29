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
//
// 12. Junk-vote guard: an invalid-axis or invalid-vote signal sent mid-review
//     does not flip or poison the consensus verdict.
//
// 13. Controller boundary: the client-backed controller does not construct an
//     engine and does not own slice-queue configuration.
//
// 14. Registration conflict policy: a second process that registers the same
//     queue with a different concurrency limit re-configures the queue the FIRST
//     process is already running work on. Measured live: the running engine is
//     saturated at 2, the peer registers 4, the running engine then runs 4.
//
// 15. Operator-driven concurrency change: SetSliceConcurrency raises the limit
//     on a running engine, the stored settings are read back, and the running
//     engine goes on to run at the new limit. Its refusal of a non-positive
//     limit and its reporting of a storage failure are covered too.
//
// 16. Recovery and queues: a recovered slice goes back onto the pasture slice
//     queue and completes there, while a recovered epoch workflow — which never
//     ran on a queue — goes onto the runtime's reserved internal queue. The
//     proof is a read-back of the stored queue name for each.
//
// 17. The operator path end to end: a concurrency change written by the
//     command-line code path, from a separate client on the same database,
//     reaches an engine that is already running.
//
// 18. Epoch recovery in the shape a shipped start produces: the epoch control
//     workflow is ENQUEUED on pasture's control queue, so recovery returns it
//     to that queue, under pasture's cadence and the control queue's limit.
//     Read back from the stored queue name, with the pick-up wait held to a
//     documented ceiling.
//
// 19. The off-queue variant, which no shipped command produces: work that ran
//     on NO queue falls back to the runtime's reserved queue, which has no
//     stored settings row and so is not pasture's to configure.
//
// 20. Recovery limit for work that did run on a queue: recovered slices obey
//     the worker limit the RESTARTED engine registers, not the limit the
//     crashed engine ran at. Read back from the engine, from the stored queue
//     row, and from how many slices the queue itself reports running.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ── Fixtures ──────────────────────────────────────────────────────────────────

// queueEngineOpts describes one engine for the queue tests. The zero value is
// not usable: dbPath, executorID and appVersion are always required.
//
// Two engines that share dbPath share the queue rows, which is what the
// registration-conflict test needs; two engines that also share executorID and
// appVersion share crash-recovery ownership, which is what the recovery test
// needs.
type queueEngineOpts struct {
	dbPath     string
	k          int // <= 0 uses DefaultSliceQueueConcurrency
	executorID string
	appVersion string
	mgr        *hooks.Manager // nil ⇒ no hook dispatch
	// manualShutdown leaves shutdown to the test instead of registering it with
	// t.Cleanup. Use it when the test must stop one engine before starting
	// another on the same database file.
	manualShutdown bool
	// skipLaunch constructs the engine (which registers the queues) without
	// launching it, so it never polls a queue or recovers a workflow. Use it
	// for a peer whose only role is to register.
	skipLaunch bool
}

// newQueueEngineFrom builds an engine, launches it unless skipLaunch is set,
// and registers its shutdown with the test unless manualShutdown is set.
func newQueueEngineFrom(t *testing.T, o queueEngineOpts) *engine.Engine {
	t.Helper()
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:                   o.dbPath,
		ApplicationVersion:       o.appVersion,
		ExecutorID:               o.executorID,
		SliceConcurrency:         o.k,
		HooksMgr:                 o.mgr,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if !o.manualShutdown {
		t.Cleanup(func() { e.Shutdown(10 * time.Second) })
	}
	if o.skipLaunch {
		return e
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch: %v", err)
	}
	return e
}

// newQueueEngine opens an engine with the given concurrency limit K on a fresh
// copy of the golden database. k <= 0 uses the default
// (DefaultSliceQueueConcurrency).
func newQueueEngine(t *testing.T, k int) *engine.Engine {
	t.Helper()
	return newQueueEngineWithHooks(t, k, nil)
}

// newQueueEngineWithHooks is like newQueueEngine but wires the given hooks
// manager so dispatchHook delivers events to it.
func newQueueEngineWithHooks(t *testing.T, k int, mgr *hooks.Manager) *engine.Engine {
	t.Helper()
	executorID, appVersion := testEngineIdentity(t)
	return newQueueEngineFrom(t, queueEngineOpts{
		dbPath:     testutil.GoldenUnifiedDBPath(t),
		k:          k,
		executorID: executorID,
		appVersion: appVersion,
		mgr:        mgr,
	})
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

	// Deliver each start signal serially before waiting on results. This preserves
	// the queue-concurrency behavior under test without making signal delivery
	// compete with the DBOS queue runners for CPU and SQLite access.
	for _, sliceId := range sliceIds {
		sendMockStartSignal(t, e, sliceId, 30*time.Second)
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

// backpressureStartSliceDeadline is the start-signal deadline the backpressure
// test runs under, in place of the production default.
//
// The deadline belongs to the LOAD this test creates, not to the code it
// tests. The test asks 30 slices to receive their start signal through one
// SQLite writer while the rest of the package runs beside it in parallel under
// the race detector, so signal delivery is serialised behind every other
// writer in the package. The production default is sized for one epoch on a
// live daemon and is far too short for that shape: a few of the 30 slices
// miss it whenever the package grows, which reads as a defect in the queue and
// is only ever machine load.
//
// The production default is deliberately left alone. Only this test's own
// engine takes the longer deadline.
const backpressureStartSliceDeadline = 10 * time.Second

// newBackpressureEngine is newQueueEngine with the deadline above. It is
// separate rather than a parameter on the shared helper so that no other test
// silently inherits a deadline that hides a real delay.
func newBackpressureEngine(t *testing.T, k int) *engine.Engine {
	t.Helper()
	profile, err := timeouts.New(
		timeouts.Test,
		500*time.Millisecond, // SQLite lock wait, unchanged
		time.Second,          // ingress, the production window; only the start-signal deadline moves
		backpressureStartSliceDeadline,
		30*time.Second, // workflow-result wait, the outermost tier
	)
	if err != nil {
		t.Fatalf("build the timeout profile for the backpressure test: %v", err)
	}

	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		SliceConcurrency:         k,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
		Timeouts:                 profile,
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

// TestSliceQueue_BackpressureAllEventuallyComplete verifies that 30 mock slices
// all eventually complete exactly once when dispatched via the slice queue, even
// when K < 30. Excess slices are held in the DBOS queues table and dequeued as
// earlier ones finish. This is the single-process drain invariant.
//
// All slices receive explicit mock start_slice signals dispatched concurrently.
// The signals are pre-populated in the DBOS notifications table; each slice
// consumes its signal when its queue slot opens.
func TestSliceQueue_BackpressureAllEventuallyComplete(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping 30-slice backpressure test in short mode")
	}
	const K = 4
	const N = 30

	e := newBackpressureEngine(t, K)

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

	// Deliver signals serially. Concurrent spin-polling here creates 30 writers
	// against the same SQLite-backed DBOS store and can starve a sender beyond
	// the workflow's start-signal deadline under full-tree race-test load. Signal
	// delivery concurrency is not part of this test's backpressure invariant.
	for _, sliceId := range sliceIds {
		sendMockStartSignal(t, e, sliceId, 90*time.Second)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
// The SliceQueue().GetName() check is the real wiring assertion (the queue was
// registered under that name in the DBOS system, and the accessor returns the
// persisted configuration read back from it). SliceConcurrency() is the stored
// resolved value (not a re-derivation).
func TestSliceQueue_DefaultConcurrency(t *testing.T) {
	t.Parallel()
	e := newQueueEngine(t, 0) // 0 → DefaultSliceQueueConcurrency
	if got := e.SliceConcurrency(); got != engine.DefaultSliceQueueConcurrency {
		t.Errorf("SliceConcurrency() = %d, want %d (default)", got, engine.DefaultSliceQueueConcurrency)
	}
	if e.SliceQueue().GetName() != engine.SliceQueueName {
		t.Errorf("SliceQueue().GetName() = %q, want %q", e.SliceQueue().GetName(), engine.SliceQueueName)
	}
	if e.ControlQueue().GetName() != engine.ControlQueueName {
		t.Errorf("ControlQueue().GetName() = %q, want %q", e.ControlQueue().GetName(), engine.ControlQueueName)
	}
	// The limit the engine resolved must be the limit the queue row carries:
	// the runner reads its concurrency from that row, not from the engine.
	requireWorkerConcurrency(t, "SliceQueue", e.SliceQueue().GetWorkerConcurrency(), engine.DefaultSliceQueueConcurrency)
	requireWorkerConcurrency(t, "ControlQueue", e.ControlQueue().GetWorkerConcurrency(), 1)
}

// requireWorkerConcurrency asserts the per-worker concurrency persisted in the
// queue's own row, which is what the queue runner dequeues against.
func requireWorkerConcurrency(t *testing.T, queue string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Errorf("%s().GetWorkerConcurrency() = nil (unlimited), want %d", queue, want)
		return
	}
	if *got != want {
		t.Errorf("%s().GetWorkerConcurrency() = %d, want %d", queue, *got, want)
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
				testutil.SetEnv(t, engine.SliceConcurrencyEnv, tc.envVal)
			} else {
				// Ensure the env var is unset for this test case.
				testutil.SetEnv(t, engine.SliceConcurrencyEnv, "")
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	testutil.SetEnv(t, engine.SliceConcurrencyEnv, "not-a-number")

	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	ctrl, err := handlers.OpenEpochController(dbPath)
	if err != nil {
		t.Fatalf("OpenEpochController must ignore %s because it no longer owns engine queues; got: %v", engine.SliceConcurrencyEnv, err)
	}
	defer ctrl.Close()
}

// ── Test 14: Queue configuration is shared state ──────────────────────────────

// dbosInternalQueueName is the reserved queue that the durable runtime
// re-enqueues a recovered workflow onto when that workflow was not running on a
// queue of its own. The runtime keeps the name in an internal package
// (dbos/internal/models/queue.go) and does not export it, so it is repeated
// here; the recovery path that uses it is dbos/recovery.go.
const dbosInternalQueueName = "_dbos_internal_queue"

// storedSliceQueueConcurrency reads the slice queue's per-executor limit back
// out of the database rather than off an in-process handle. That row is what
// every queue worker reads its limit from, so it is the only reading that
// proves what the running system will do.
func storedSliceQueueConcurrency(t *testing.T, e *engine.Engine) *int {
	t.Helper()
	q, err := dbos.RetrieveQueue(e.DBOS(), engine.SliceQueueName)
	if err != nil {
		t.Fatalf("RetrieveQueue(%q): %v", engine.SliceQueueName, err)
	}
	return q.GetWorkerConcurrency()
}

// startGatedSlices starts the parent epoch workflow, enqueues n mock slices and
// delivers each start signal. Every slice blocks in its SliceStarted hook until
// the caller's gate is released, so the number of slices that reach the hook is
// the number the queue is willing to run at once.
func startGatedSlices(t *testing.T, e *engine.Engine, epochId string, n int) []dbos.WorkflowHandle[engine.SliceResult] {
	t.Helper()
	if _, err := dbos.RunWorkflow(e.DBOS(), e.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}
	handles := make([]dbos.WorkflowHandle[engine.SliceResult], n)
	sliceIds := make([]string, n)
	for i := 0; i < n; i++ {
		sliceId := fmt.Sprintf("%s--gated-%02x", epochId, i)
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
	for _, sliceId := range sliceIds {
		sendMockStartSignal(t, e, sliceId, 30*time.Second)
	}
	return handles
}

// TestSliceQueue_PeerRegistrationReconfiguresTheRunningQueue pins the
// registration conflict policy the engine chooses, which is only observable
// when two processes register the same queue.
//
// A queue's settings are a row in the shared database, and a worker reloads
// that row on every poll iteration (dbos/queue.go, queueRunner.runQueue). The
// engine registers with the policy that always overwrites the row, so a second
// process that starts against the same database re-configures the queue that
// the first process is already running work on — without restarting it.
//
// The test makes that live effect visible: the running engine is saturated at
// its start-up limit of 2, a second engine registers the same queue with a
// limit of 4, and the FIRST engine then runs 4 slices at once. It also reads
// the stored limit back, because that row is what every worker obeys.
//
// The second engine is deliberately not launched. A launched peer would also
// poll the shared queue, and then it would be ambiguous which process ran a
// slice. Registration alone is the behaviour under test.
func TestSliceQueue_PeerRegistrationReconfiguresTheRunningQueue(t *testing.T) {
	t.Parallel()
	const startK = 2
	const peerK = 4
	const N = peerK

	gater := &gatingConcurrencyHandler{release: make(chan struct{})}
	mgr := hooks.NewManager(hooks.WithDispatchTimeout(60 * time.Second))
	mgr.Register(gater)

	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)
	host := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: startK, executorID: executorID, appVersion: appVersion, mgr: mgr,
	})
	releaseGate := sync.OnceFunc(func() { close(gater.release) })
	defer releaseGate()

	const epochId = "queue--peer-registration"
	handles := startGatedSlices(t, host, epochId, N)

	// The start-up limit holds: exactly startK slices reach the gate.
	waitUntil(t, 30*time.Second, func() bool { return gater.hwm.Load() >= int64(startK) })
	if hwm := gater.hwm.Load(); hwm != int64(startK) {
		t.Fatalf("high-water mark before the peer registered = %d, want %d", hwm, startK)
	}
	requireWorkerConcurrency(t, "stored SliceQueue before the peer registered",
		storedSliceQueueConcurrency(t, host), startK)

	// A second process registers the same queue with a different limit.
	newQueueEngineFrom(t, queueEngineOpts{
		dbPath:     dbPath,
		k:          peerK,
		executorID: executorID + "-peer",
		appVersion: appVersion,
		skipLaunch: true,
	})

	requireWorkerConcurrency(t, "stored SliceQueue after the peer registered",
		storedSliceQueueConcurrency(t, host), peerK)

	// The already-running engine adopts the peer's limit: it now runs peerK
	// slices at once, which its own start-up limit forbade.
	waitUntil(t, 30*time.Second, func() bool { return gater.hwm.Load() >= int64(peerK) })
	if hwm := gater.hwm.Load(); hwm > int64(peerK) {
		t.Errorf("high-water mark = %d, want at most %d", hwm, peerK)
	}

	releaseGate()
	for i, h := range handles {
		if res := waitSliceResult(t, h, 60*time.Second); !res.Success {
			errVal := "<nil>"
			if res.Error != nil {
				errVal = *res.Error
			}
			t.Errorf("slice[%d] Success=false; error=%s", i, errVal)
		}
	}
}

// ── Test 15: Operator-driven concurrency change ───────────────────────────────

// TestSliceQueue_SetSliceConcurrencyReconfiguresTheRunningQueue verifies the
// operator path for changing how much slice and review work runs at once,
// without restarting the engine.
//
// The engine starts limited to 2 concurrent slices and is saturated at that
// limit. SetSliceConcurrency(4) then changes the stored limit; the assertion
// that matters is not the return value but that the ALREADY RUNNING engine goes
// on to run 4 slices at once.
func TestSliceQueue_SetSliceConcurrencyReconfiguresTheRunningQueue(t *testing.T) {
	t.Parallel()
	const startK = 2
	const raisedK = 4
	const N = raisedK

	gater := &gatingConcurrencyHandler{release: make(chan struct{})}
	mgr := hooks.NewManager(hooks.WithDispatchTimeout(60 * time.Second))
	mgr.Register(gater)

	e := newQueueEngineWithHooks(t, startK, mgr)
	releaseGate := sync.OnceFunc(func() { close(gater.release) })
	defer releaseGate()

	const epochId = "queue--set-concurrency"
	handles := startGatedSlices(t, e, epochId, N)

	waitUntil(t, 30*time.Second, func() bool { return gater.hwm.Load() >= int64(startK) })
	if hwm := gater.hwm.Load(); hwm != int64(startK) {
		t.Fatalf("high-water mark before the change = %d, want %d", hwm, startK)
	}

	inForce, err := e.SetSliceConcurrency(raisedK)
	if err != nil {
		t.Fatalf("SetSliceConcurrency(%d): %v", raisedK, err)
	}
	if inForce != raisedK {
		t.Errorf("SetSliceConcurrency returned %d, want %d (the value read back from storage)", inForce, raisedK)
	}
	if got := e.SliceConcurrency(); got != raisedK {
		t.Errorf("SliceConcurrency() = %d, want %d after the change", got, raisedK)
	}
	requireWorkerConcurrency(t, "stored SliceQueue after the change",
		storedSliceQueueConcurrency(t, e), raisedK)

	// The running engine honours the new limit on its next poll.
	waitUntil(t, 30*time.Second, func() bool { return gater.hwm.Load() >= int64(raisedK) })
	if hwm := gater.hwm.Load(); hwm > int64(raisedK) {
		t.Errorf("high-water mark = %d, want at most %d", hwm, raisedK)
	}

	releaseGate()
	for i, h := range handles {
		if res := waitSliceResult(t, h, 60*time.Second); !res.Success {
			errVal := "<nil>"
			if res.Error != nil {
				errVal = *res.Error
			}
			t.Errorf("slice[%d] Success=false; error=%s", i, errVal)
		}
	}
}

// TestSliceQueue_SetSliceConcurrencyRejectsAnUnusableLimit verifies that a
// limit of zero or less is refused with an actionable validation error, and
// that the stored limit is left alone.
func TestSliceQueue_SetSliceConcurrencyRejectsAnUnusableLimit(t *testing.T) {
	t.Parallel()
	e := newQueueEngine(t, 2)

	for _, k := range []int{0, -1} {
		got, err := e.SetSliceConcurrency(k)
		if err == nil {
			t.Fatalf("SetSliceConcurrency(%d) returned no error; want a validation error", k)
		}
		if got != 0 {
			t.Errorf("SetSliceConcurrency(%d) = %d, want 0 alongside the error", k, got)
		}
		var se *pasterrors.StructuredError
		if !errors.As(err, &se) {
			t.Fatalf("SetSliceConcurrency(%d) error is %T, want a structured error", k, err)
		}
		if se.Category != pasterrors.CategoryValidation {
			t.Errorf("error category = %v, want %v", se.Category, pasterrors.CategoryValidation)
		}
		if se.Fix == "" {
			t.Error("error carries no fix; an operator error must say how to correct it")
		}
	}

	requireWorkerConcurrency(t, "stored SliceQueue after the rejected changes",
		storedSliceQueueConcurrency(t, e), 2)
	if got := e.SliceConcurrency(); got != 2 {
		t.Errorf("SliceConcurrency() = %d, want 2 (unchanged)", got)
	}
}

// TestSliceQueue_SetSliceConcurrencyReportsAStorageFailure verifies the error
// path of the queue runtime beyond registration: when the queue's settings row
// is gone, the change fails with a storage error that names the cause, rather
// than with the runtime's own bare message.
//
// Deleting the row is how the failure is produced, and it is also a real one:
// the settings are shared, so another process can remove them at any time.
func TestSliceQueue_SetSliceConcurrencyReportsAStorageFailure(t *testing.T) {
	t.Parallel()
	e := newQueueEngine(t, 2)

	if err := dbos.DeleteQueue(e.DBOS(), engine.SliceQueueName); err != nil {
		t.Fatalf("DeleteQueue(%q): %v", engine.SliceQueueName, err)
	}

	got, err := e.SetSliceConcurrency(4)
	if err == nil {
		t.Fatalf("SetSliceConcurrency(4) = %d with no error; want a storage error", got)
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("SetSliceConcurrency error is %T, want a structured error", err)
	}
	if se.Category != pasterrors.CategoryStorage {
		t.Errorf("error category = %v, want %v", se.Category, pasterrors.CategoryStorage)
	}
	if se.Cause == nil {
		t.Error("error drops the underlying cause; the operator loses the reason it failed")
	}
	if se.Fix == "" {
		t.Error("error carries no fix; a storage failure must say what to check")
	}
}

// ── Test 16: Recovery keeps a workflow on the queue it belongs to ─────────────

// markWorkflowPending puts a workflow row back into the state a killed process
// leaves behind: still marked as running, with no result recorded. It is the
// same state the durable runtime's own recovery tests reproduce, and it is the
// only state that crash recovery acts on.
// markWorkflowPending records the state a stopped process leaves behind: the
// workflow is marked as still running with no result and no dequeue time.
//
// It REFUSES to touch a workflow that already finished. The WHERE clause matches
// only a row that is still marked as running, so a slice that completed while
// the engine shut down cannot be quietly resurrected and then "recovered", which
// would make a test pass without exercising recovery at all.
//
// One difference from a real crash is worth naming: a killed process leaves a
// queued workflow's dequeue time in place, and this clears it. That is harmless
// for the tests here because recovery clears the dequeue time itself
// (dbos/internal/sysdb/system_database.go, ReenqueueForRecovery).
func markWorkflowPending(t *testing.T, db *sql.DB, workflowID string) {
	t.Helper()
	writePendingRow(t, db, workflowID, true)
}

// rewindWorkflowToPending forces a workflow back to the interrupted state even
// if it already FINISHED. It is the loud form: a caller that uses it is saying
// that it deliberately un-finishes completed work so recovery has something to
// act on. Prefer markWorkflowPending, which refuses to do that by accident.
//
// It clears the workflow's own result, NOT the results its durable steps
// recorded. A rewound workflow therefore runs again from those memoized steps
// rather than doing the work a second time, so a test must not read its
// re-completion as proof that the work itself ran again.
func rewindWorkflowToPending(t *testing.T, db *sql.DB, workflowID string) {
	t.Helper()
	writePendingRow(t, db, workflowID, false)
}

func writePendingRow(t *testing.T, db *sql.DB, workflowID string, onlyIfUnfinished bool) {
	t.Helper()
	query := `UPDATE workflow_status
		    SET status = ?, output = NULL, error = NULL, started_at_epoch_ms = NULL, updated_at = ?
		  WHERE workflow_uuid = ?`
	args := []any{string(dbos.WorkflowStatusPending), time.Now().UnixMilli(), workflowID}
	if onlyIfUnfinished {
		query += ` AND status = ?`
		args = append(args, string(dbos.WorkflowStatusPending))
	}
	res, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("mark %q pending: %v", workflowID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("mark %q pending, row count: %v", workflowID, err)
	}
	if n != 1 {
		if onlyIfUnfinished {
			t.Fatalf("mark %q pending updated %d rows, want 1; the row is no longer marked as running, so it finished instead of being interrupted and recovery would have nothing to do",
				workflowID, n)
		}
		t.Fatalf("mark %q pending updated %d rows, want 1", workflowID, n)
	}
}

// workflowStatusOf reads one workflow's stored status, including the queue it
// belongs to.
func workflowStatusOf(t *testing.T, e *engine.Engine, workflowID string) dbos.WorkflowStatus {
	t.Helper()
	rows, err := dbos.ListWorkflows(e.DBOS(), dbos.WithFilterWorkflowIDs(workflowID))
	if err != nil {
		t.Fatalf("ListWorkflows(%q): %v", workflowID, err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListWorkflows(%q) returned %d rows, want 1", workflowID, len(rows))
	}
	return rows[0]
}

// TestSliceQueue_RecoveryKeepsEachWorkflowOnItsOwnQueue pins the queue side of
// crash recovery, which the durable runtime changed: a recovered workflow is
// always put back on a queue, never restarted in place.
//
// Which queue it lands on decides which limits and which polling cadence apply
// to it, so both cases are pinned here:
//
//   - a slice, which ran on the pasture slice queue, goes back onto that same
//     queue, so it is still governed by the concurrency limit and the polling
//     cadence pasture configured;
//   - an epoch workflow, which never ran on a queue, goes onto the runtime's
//     reserved internal queue instead. That queue is not pasture's to configure:
//     it carries no concurrency limit and polls at the runtime's own one-second
//     cadence, not at the interval pasture sets for its own queues.
//
// The proof is a read-back of the stored queue name for each workflow, plus the
// recovered slice actually completing.
func TestSliceQueue_RecoveryKeepsEachWorkflowOnItsOwnQueue(t *testing.T) {
	t.Parallel()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)

	// The engine that "crashes". Its identity is reused by the second engine,
	// because recovery only acts on the work its own executor left behind.
	first := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: engine.DefaultSliceQueueConcurrency,
		executorID: executorID, appVersion: appVersion, manualShutdown: true,
	})
	firstStopped := false
	defer func() {
		if !firstStopped {
			first.Shutdown(10 * time.Second)
		}
	}()

	const epochId = "queue--recovery-readback"
	if _, err := dbos.RunWorkflow(first.DBOS(), first.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}

	sliceId := epochId + "--slice"
	h, err := first.EnqueueSlice(engine.SliceInput{
		EpochId:          epochId,
		SliceId:          sliceId,
		ParentWorkflowId: epochId,
	})
	if err != nil {
		t.Fatalf("EnqueueSlice: %v", err)
	}
	sendMockStartSignal(t, first, sliceId, 30*time.Second)
	if res := waitSliceResult(t, h, 60*time.Second); !res.Success {
		errVal := "<nil>"
		if res.Error != nil {
			errVal = *res.Error
		}
		t.Fatalf("slice Success=false before the crash; error=%s", errVal)
	}
	if got := workflowStatusOf(t, first, sliceId).QueueName; got != engine.SliceQueueName {
		t.Fatalf("slice ran on queue %q, want %q", got, engine.SliceQueueName)
	}
	// The epoch workflow is still waiting for signals, so it is genuinely
	// in flight, and it never ran on a queue.
	if got := workflowStatusOf(t, first, epochId).QueueName; got != "" {
		t.Fatalf("epoch workflow ran on queue %q, want no queue", got)
	}

	// The crash: the slice is left marked as running with no result, exactly as
	// a killed process would leave it. The epoch workflow is already in that
	// state on its own.
	// This slice COMPLETED above, on purpose, so that the queue it completed on
	// is read back before the crash. Un-finishing it is therefore deliberate,
	// and it uses the loud form that says so.
	rewindWorkflowToPending(t, first.DB(), sliceId)
	first.Shutdown(10 * time.Second)
	firstStopped = true

	// The replacement process. Recovery runs inside Launch, before it serves
	// any queue.
	second := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: engine.DefaultSliceQueueConcurrency,
		executorID: executorID, appVersion: appVersion,
	})

	if got := workflowStatusOf(t, second, sliceId).QueueName; got != engine.SliceQueueName {
		t.Errorf("recovered slice is on queue %q, want %q (its own queue, with pasture's limits)", got, engine.SliceQueueName)
	}
	if got := workflowStatusOf(t, second, epochId).QueueName; got != dbosInternalQueueName {
		t.Errorf("recovered epoch workflow is on queue %q, want %q (the reserved queue for work that had none)", got, dbosInternalQueueName)
	}

	// The recovered slice is dequeued from the pasture slice queue and finishes.
	recovered, err := dbos.RetrieveWorkflow[engine.SliceResult](second.DBOS(), sliceId)
	if err != nil {
		t.Fatalf("RetrieveWorkflow(%q): %v", sliceId, err)
	}
	res, err := recovered.GetResult(dbos.WithHandleTimeout(60 * time.Second))
	if err != nil {
		t.Fatalf("GetResult(recovered slice): %v", err)
	}
	if !res.Success {
		errVal := "<nil>"
		if res.Error != nil {
			errVal = *res.Error
		}
		t.Errorf("recovered slice Success=false; error=%s", errVal)
	}
	final := workflowStatusOf(t, second, sliceId)
	if final.Status != dbos.WorkflowStatusSuccess {
		t.Errorf("recovered slice status = %v, want %v", final.Status, dbos.WorkflowStatusSuccess)
	}
	if final.QueueName != engine.SliceQueueName {
		t.Errorf("recovered slice completed on queue %q, want %q", final.QueueName, engine.SliceQueueName)
	}
}

// TestSliceQueue_SetSliceConcurrencyNeedsABuiltEngine verifies that an engine
// value that was never built by engine.New reports the problem instead of
// panicking on its missing queue.
func TestSliceQueue_SetSliceConcurrencyNeedsABuiltEngine(t *testing.T) {
	t.Parallel()
	var unbuilt engine.Engine

	got, err := unbuilt.SetSliceConcurrency(4)
	if err == nil {
		t.Fatalf("SetSliceConcurrency(4) = %d with no error; want an error naming the missing queue", got)
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want a structured error", err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("error category = %v, want %v", se.Category, pasterrors.CategoryValidation)
	}
}

// ── Test 17: A change made from outside the process ───────────────────────────

// TestSliceQueue_OperatorCommandReconfiguresTheRunningQueue is the end-to-end
// proof of the operator path: a change written by a SEPARATE code path, the one
// the command line uses, reaches a running engine.
//
// The command does not contact the daemon. It writes the queue's settings row,
// which the daemon's workers reload as they poll. So the assertion that matters
// is not that the row changed — the handler's own tests cover that — but that
// the engine which was already saturated at 2 goes on to run 4 at once.
func TestSliceQueue_OperatorCommandReconfiguresTheRunningQueue(t *testing.T) {
	t.Parallel()
	const startK = 2
	const raisedK = 4
	const N = raisedK

	gater := &gatingConcurrencyHandler{release: make(chan struct{})}
	mgr := hooks.NewManager(hooks.WithDispatchTimeout(60 * time.Second))
	mgr.Register(gater)

	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)
	e := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: startK, executorID: executorID, appVersion: appVersion, mgr: mgr,
	})
	releaseGate := sync.OnceFunc(func() { close(gater.release) })
	defer releaseGate()

	const epochId = "queue--operator-command"
	handles := startGatedSlices(t, e, epochId, N)

	waitUntil(t, 30*time.Second, func() bool { return gater.hwm.Load() >= int64(startK) })
	if hwm := gater.hwm.Load(); hwm != int64(startK) {
		t.Fatalf("high-water mark before the change = %d, want %d", hwm, startK)
	}

	// The operator's path: a separate client on the same database file.
	code, err := handlers.SetQueueConcurrency(handlers.QueueConcurrencyInput{
		DBPath: dbPath,
		Queue:  string(handlers.QueueSelectorSlice),
		Limit:  raisedK,
	}, types.OutputText)
	if err != nil {
		t.Fatalf("SetQueueConcurrency: %v", err)
	}
	if code != 0 {
		t.Fatalf("SetQueueConcurrency exit code = %d, want 0", code)
	}

	requireWorkerConcurrency(t, "stored SliceQueue after the operator command",
		storedSliceQueueConcurrency(t, e), raisedK)

	// The running engine adopts it on its next poll.
	waitUntil(t, 30*time.Second, func() bool { return gater.hwm.Load() >= int64(raisedK) })
	if hwm := gater.hwm.Load(); hwm > int64(raisedK) {
		t.Errorf("high-water mark = %d, want at most %d", hwm, raisedK)
	}

	releaseGate()
	for i, h := range handles {
		if res := waitSliceResult(t, h, 60*time.Second); !res.Success {
			errVal := "<nil>"
			if res.Error != nil {
				errVal = *res.Error
			}
			t.Errorf("slice[%d] Success=false; error=%s", i, errVal)
		}
	}
}

// ── Recovery behaviour: which queue, which cadence, and which limit ───────────

// reservedQueuePollingInterval is the cadence the reserved internal queue polls
// at. It is the runtime's own default (dbos/internal/models/queue.go,
// DefaultBasePollingInterval), and pasture cannot change it: the reserved queue
// is the one queue the runtime keeps in process instead of in the queues table
// (dbos/queue.go, queueRunner.internalQueue), so Config.QueueBasePollingInterval
// reaches pasture's two queues only.
const reservedQueuePollingInterval = time.Second

// reservedQueueFactsVerifiedAt is the durable-runtime version whose source the
// two facts above were read from: the reserved queue's NAME and its POLLING
// CADENCE. Both live in a package the runtime keeps internal, so no import can
// hold this file to them and a version bump could silently make the ceiling
// meaningless.
const reservedQueueFactsVerifiedAt = "github.com/dbos-inc/dbos-transact-golang v1.2.0"

// TestReservedQueueFactsMatchThePinnedRuntime fails on a runtime bump so the two
// reserved-queue facts are re-read instead of assumed.
//
// WHEN THIS FAILS, DO NOT JUST BUMP THE CONSTANT. Re-read the new version's
// dbos/internal/models/queue.go for InternalQueueName and
// DefaultBasePollingInterval, and dbos/queue.go for whether the reserved queue
// is still kept in process rather than in the queues table. Then update
// dbosInternalQueueName, reservedQueuePollingInterval and this constant
// together. internal/engine/dbosinit_test.go pins the same runtime version for
// a different reason and must be re-verified in the same change.
func TestReservedQueueFactsMatchThePinnedRuntime(t *testing.T) {
	t.Parallel()
	gomod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod to verify the durable-runtime version pin: %v", err)
	}
	if !strings.Contains(string(gomod), reservedQueueFactsVerifiedAt) {
		t.Fatalf("go.mod no longer pins %q. The reserved queue's name (%q) and its polling cadence (%s) were read from that version's source, and the recovery pick-up ceiling is derived from the cadence, so both must be re-read before the pin moves",
			reservedQueueFactsVerifiedAt, dbosInternalQueueName, reservedQueuePollingInterval)
	}
}

// recoveryPickupCeiling is the upper bound these tests allow for a recovered
// workflow to be picked up again.
//
// Recovery re-enqueues the work while Launch runs and clears its start time
// (dbos/internal/sysdb/system_database.go, ReenqueueForRecovery), so the work
// then waits for the next poll of whichever queue it returned to. One cadence
// plus one dequeue round is the expected cost. The slowest queue in play is the
// reserved one at one second, so the ceiling is five times that.
//
// A queue worker does NOT slow down because it found nothing to do. Its interval
// grows only after a database-contention error and scales back on every clean
// poll (dbos/queue.go, queueRunner.runQueue), and a restart builds a fresh
// runner that starts at the base interval and has never backed off. That is why
// one cadence bounds the wait here, and why a test on a long-lived process could
// not assume the same.
//
// The margin is deliberately wide. The number is a CEILING, and a tight one
// would report machine load as a defect.
const recoveryPickupCeiling = 5 * reservedQueuePollingInterval

// waitForRecoveryPickup waits until workflowID has been dequeued again and
// returns how long that took, measured from the moment the caller says the
// replacement engine finished starting.
//
// The read-back is stored state, not an inference: recovery clears the start
// time, so a non-zero start time afterwards can only have been written by a
// dequeue after recovery.
func waitForRecoveryPickup(t *testing.T, e *engine.Engine, workflowID string, launched time.Time) time.Duration {
	t.Helper()
	waitUntil(t, recoveryPickupCeiling, func() bool {
		return !workflowStatusOf(t, e, workflowID).StartedAt.IsZero()
	})
	return time.Since(launched)
}

// assertPickupWithinCeiling reports a pick-up that took longer than the ceiling.
func assertPickupWithinCeiling(t *testing.T, workflowID, queueName string, waited time.Duration) {
	t.Helper()
	if waited > recoveryPickupCeiling {
		t.Errorf("the recovered workflow %q on queue %q was picked up after %s, above the ceiling of %s; recovery re-enqueues during Launch, so a wait this long means it did not simply wait for that queue's next poll",
			workflowID, queueName, waited, recoveryPickupCeiling)
	}
}

// assertStoppedMidFlight reads back the state a stopped process leaves behind:
// the workflow is still marked as running and has no result. Recovery acts only
// on rows in that state, so a test that skips this check could "recover" a
// workflow that had in fact finished.
//
// It opens its OWN database handle because the engine that owned the workflow
// has already been shut down and closed its handle. It returns the stored
// dequeue time, which is zero for work that never ran on a queue.
func assertStoppedMidFlight(t *testing.T, dbPath, workflowID string) time.Time {
	t.Helper()
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		t.Fatalf("open the database to read the stopped state of %q: %v", workflowID, err)
	}
	defer db.Close()

	var status string
	var output sql.NullString
	var startedAtMillis sql.NullInt64
	if err := db.QueryRow(
		`SELECT status, output, started_at_epoch_ms FROM workflow_status WHERE workflow_uuid = ?`,
		workflowID,
	).Scan(&status, &output, &startedAtMillis); err != nil {
		t.Fatalf("read the stopped state of %q: %v", workflowID, err)
	}
	if status != string(dbos.WorkflowStatusPending) {
		t.Fatalf("after the engine stopped, workflow %q has status %q, want %q; recovery only re-enqueues rows in that state, so nothing below would be exercised",
			workflowID, status, dbos.WorkflowStatusPending)
	}
	if output.Valid {
		t.Fatalf("after the engine stopped, workflow %q already has a result; it was supposed to be interrupted", workflowID)
	}
	if !startedAtMillis.Valid {
		return time.Time{}
	}
	return time.UnixMilli(startedAtMillis.Int64)
}

// TestRecovery_EpochControlWorkflowReturnsToItsOwnQueue is the PRODUCTION shape
// of epoch recovery.
//
// A shipped epoch start does not run the control workflow in process: it
// ENQUEUES it on pasture's control queue (internal/handlers/controller.go, in
// dbosController.StartEpoch). This test starts it the same way. Because the
// workflow ran on a queue, recovery returns it to THAT queue, so it stays under
// pasture's own cadence and under the control queue's limit of one at a time. It
// never touches the runtime's reserved queue.
//
// The test reads the queue back and measures how long the recovered workflow
// waited to be picked up.
func TestRecovery_EpochControlWorkflowReturnsToItsOwnQueue(t *testing.T) {
	t.Parallel()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)

	first := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: engine.DefaultSliceQueueConcurrency,
		executorID: executorID, appVersion: appVersion, manualShutdown: true,
	})
	firstStopped := false
	defer func() {
		if !firstStopped {
			first.Shutdown(10 * time.Second)
		}
	}()

	const epochId = "queue--recovery-control-queue"
	// The same call the shipped start command makes, by name and by queue. The
	// application version is this test engine's, because recovery only acts on
	// work that matches the running engine's version.
	if _, err := dbos.Enqueue[protocol.EpochState, engine.ControlInput](first.DBOS(),
		engine.ControlQueueName,
		engine.EpochControlWorkflowName,
		engine.ControlInput{EpochId: epochId},
		dbos.WithEnqueueWorkflowID(epochId),
		dbos.WithEnqueueApplicationVersion(appVersion),
	); err != nil {
		t.Fatalf("Enqueue(epoch control workflow): %v", err)
	}
	waitUntil(t, 30*time.Second, func() bool {
		return !workflowStatusOf(t, first, epochId).StartedAt.IsZero()
	})
	if got := workflowStatusOf(t, first, epochId).QueueName; got != engine.ControlQueueName {
		t.Fatalf("the epoch control workflow ran on queue %q, want %q", got, engine.ControlQueueName)
	}

	// The crash. Stopping the engine leaves the workflow marked as running with
	// no result, which is the state recovery acts on. Unlike an off-queue start,
	// a queued workflow keeps its dequeue time here; recovery clears it.
	first.Shutdown(10 * time.Second)
	firstStopped = true
	stoppedAt := assertStoppedMidFlight(t, dbPath, epochId)
	if stoppedAt.IsZero() {
		t.Fatalf("the stopped epoch control workflow lost its dequeue time; a queued workflow keeps it, and recovery is what clears it")
	}

	launched := time.Now()
	second := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: engine.DefaultSliceQueueConcurrency,
		executorID: executorID, appVersion: appVersion,
	})
	constructed := time.Now()
	t.Logf("building and launching the replacement engine took %s", constructed.Sub(launched))

	waited := waitForRecoveryPickup(t, second, epochId, constructed)

	if got := workflowStatusOf(t, second, epochId).QueueName; got != engine.ControlQueueName {
		t.Fatalf("the recovered epoch control workflow is on queue %q, want %q; work that ran on a queue must return to it",
			got, engine.ControlQueueName)
	}
	assertPickupWithinCeiling(t, epochId, engine.ControlQueueName, waited)
	t.Logf("recovered epoch control workflow picked up after %s, ceiling %s", waited, recoveryPickupCeiling)
}

// TestRecovery_OffQueueEpochWorkflowLandsOnTheReservedQueue covers an
// IN-PROCESS, OFF-QUEUE start: a caller inside the daemon that runs the epoch
// workflow directly instead of enqueuing it.
//
// NO SHIPPED COMMAND PRODUCES THIS STATE. The epoch start command enqueues on
// pasture's control queue, which the test above covers. This case is kept
// because it pins a runtime rule that decides the cost of the other case:
// recovery returns work to the queue it ran on, and only work that ran on NO
// queue falls back to the runtime's reserved queue. The reserved queue is not
// pasture's to configure, which the read-back below shows directly.
func TestRecovery_OffQueueEpochWorkflowLandsOnTheReservedQueue(t *testing.T) {
	t.Parallel()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)

	first := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: engine.DefaultSliceQueueConcurrency,
		executorID: executorID, appVersion: appVersion, manualShutdown: true,
	})
	firstStopped := false
	defer func() {
		if !firstStopped {
			first.Shutdown(10 * time.Second)
		}
	}()

	const epochId = "queue--recovery-off-queue"
	if _, err := dbos.RunWorkflow(first.DBOS(), first.EpochControlWorkflow,
		engine.ControlInput{EpochId: epochId}, dbos.WithWorkflowID(epochId)); err != nil {
		t.Fatalf("RunWorkflow(control): %v", err)
	}
	if got := workflowStatusOf(t, first, epochId).QueueName; got != "" {
		t.Fatalf("the off-queue epoch workflow ran on queue %q, want no queue; the whole test depends on it having none", got)
	}
	// The start time is the DEQUEUE time, so work that never ran on a queue has
	// none. That is what makes the measurement below sound: a non-zero start
	// time after recovery can only come from a dequeue off the reserved queue.
	if got := workflowStatusOf(t, first, epochId).StartedAt; !got.IsZero() {
		t.Fatalf("the off-queue epoch workflow already has a start time of %s; it never ran on a queue, so it should have none", got)
	}

	// The crash. Stopping the engine is enough: it leaves the workflow marked as
	// running with no result, which is read back rather than written here.
	first.Shutdown(10 * time.Second)
	firstStopped = true
	if stoppedAt := assertStoppedMidFlight(t, dbPath, epochId); !stoppedAt.IsZero() {
		t.Fatalf("the stopped off-queue workflow has a dequeue time of %s; it never ran on a queue, so it should have none", stoppedAt)
	}

	launched := time.Now()
	second := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: engine.DefaultSliceQueueConcurrency,
		executorID: executorID, appVersion: appVersion,
	})
	constructed := time.Now()
	t.Logf("building and launching the replacement engine took %s", constructed.Sub(launched))

	waited := waitForRecoveryPickup(t, second, epochId, constructed)

	if got := workflowStatusOf(t, second, epochId).QueueName; got != dbosInternalQueueName {
		t.Fatalf("the recovered off-queue workflow is on queue %q, want %q", got, dbosInternalQueueName)
	}
	// Read back WHY pasture cannot change that cadence or add a limit to it: the
	// reserved queue has no settings row at all, so there is nothing to write.
	if q, err := dbos.RetrieveQueue(second.DBOS(), dbosInternalQueueName); err == nil {
		t.Errorf("the reserved queue %q has a stored settings row (worker concurrency %v); pasture is expected to have no way to configure it",
			dbosInternalQueueName, q.GetWorkerConcurrency())
	} else if !errors.Is(err, dbos.ErrQueueNotFound) {
		t.Errorf("looking up the reserved queue %q failed with %v; want the runtime's queue-not-found result, which is what shows it has no settings row. Any other error means the lookup itself broke and proves nothing",
			dbosInternalQueueName, err)
	}
	assertPickupWithinCeiling(t, epochId, dbosInternalQueueName, waited)
	t.Logf("recovered off-queue workflow picked up after %s, ceiling %s, reserved queue cadence %s",
		waited, recoveryPickupCeiling, reservedQueuePollingInterval)
}

// runningSliceCount reads back how many of ids the queue is running right now:
// dequeued (a start time is set) and not yet finished (still marked as running).
// This is the queue's own state, so it does not depend on any hook firing.
func runningSliceCount(t *testing.T, e *engine.Engine, ids []string) int {
	t.Helper()
	rows, err := dbos.ListWorkflows(e.DBOS(), dbos.WithFilterWorkflowIDs(ids...))
	if err != nil {
		t.Fatalf("ListWorkflows for the recovered slices: %v", err)
	}
	running := 0
	for _, row := range rows {
		if row.Status == dbos.WorkflowStatusPending && !row.StartedAt.IsZero() {
			running++
		}
	}
	return running
}

// TestRecovery_RecoveredSlicesObeyTheLimitTheRestartedEngineSets proves which
// concurrency limit governs slices that come back after a crash.
//
// A slice ran on the pasture slice queue, so recovery puts it back on that
// queue, where the worker-concurrency limit still applies. The limit in force is
// the one the RESTARTED engine registers, not the one the crashed engine used:
// the queue settings are a shared row and pasture registers with the policy that
// always overwrites it (internal/engine/queue.go). So an operator changes the
// limit for recovered work by restarting at the new limit, and the change takes
// effect on work that was already interrupted.
//
// The bound is measured from the QUEUE's own read-back, not from hook counts. A
// slice dispatches its start hook inside a durable step, so a recovered slice
// whose hook step already recorded a result does not fire it again; counting
// hooks would therefore measure step memoization rather than concurrency. The
// gate is kept only to hold running slices still long enough to be observed.
func TestRecovery_RecoveredSlicesObeyTheLimitTheRestartedEngineSets(t *testing.T) {
	t.Parallel()
	const crashedLimit = 4
	const recoveredLimit = 2
	const slices = 6

	dbPath := testutil.GoldenUnifiedDBPath(t)
	executorID, appVersion := testEngineIdentity(t)

	firstGate := &gatingConcurrencyHandler{release: make(chan struct{})}
	firstMgr := hooks.NewManager(hooks.WithDispatchTimeout(4 * time.Second))
	firstMgr.Register(firstGate)

	first := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: crashedLimit,
		executorID: executorID, appVersion: appVersion,
		mgr: firstMgr, manualShutdown: true,
	})
	firstStopped := false
	defer func() {
		if !firstStopped {
			first.Shutdown(10 * time.Second)
		}
	}()

	const epochId = "queue--recovery-limit"
	sliceIds := make([]string, slices)
	for i := range sliceIds {
		sliceIds[i] = fmt.Sprintf("%s--gated-%02x", epochId, i)
	}
	startGatedSlices(t, first, epochId, slices)
	waitUntil(t, 30*time.Second, func() bool {
		return runningSliceCount(t, first, sliceIds) >= crashedLimit
	})
	if got := runningSliceCount(t, first, sliceIds); got != crashedLimit {
		t.Fatalf("before the crash the queue was running %d slices at once, want %d; the contrast with the limit after recovery is the point of this test",
			got, crashedLimit)
	}
	startedIds := make([]string, 0, slices)
	for _, sliceId := range sliceIds {
		if !workflowStatusOf(t, first, sliceId).StartedAt.IsZero() {
			startedIds = append(startedIds, sliceId)
		}
	}

	// The crash. The engine stops FIRST, then every slice it had dequeued is
	// marked as still running with no result. The order matters: marking first
	// would race with a slice that finishes while the engine shuts down and
	// writes its own result over the mark.
	//
	// A real killed process keeps the dequeue time on a queued workflow, which
	// this mark clears. That difference does not matter to what is asserted
	// below, because recovery clears the dequeue time itself.
	first.Shutdown(10 * time.Second)
	firstStopped = true
	crashDB, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		t.Fatalf("open the database to record the crash: %v", err)
	}
	for _, sliceId := range startedIds {
		markWorkflowPending(t, crashDB, sliceId)
	}
	if err := crashDB.Close(); err != nil {
		t.Fatalf("close the crash-recording handle: %v", err)
	}

	// The replacement process registers the queue at the lower limit. Recovery
	// runs inside Launch, before the queue is served.
	secondGate := &gatingConcurrencyHandler{release: make(chan struct{})}
	secondMgr := hooks.NewManager(hooks.WithDispatchTimeout(4 * time.Second))
	secondMgr.Register(secondGate)
	second := newQueueEngineFrom(t, queueEngineOpts{
		dbPath: dbPath, k: recoveredLimit,
		executorID: executorID, appVersion: appVersion,
		mgr: secondMgr,
	})

	// Read back the limit that is actually stored, not the one we asked for.
	if got := second.SliceConcurrency(); got != recoveredLimit {
		t.Errorf("restarted engine reports a limit of %d, want %d", got, recoveredLimit)
	}
	stored := storedSliceQueueConcurrency(t, second)
	switch {
	case stored == nil:
		t.Fatalf("the slice queue has no stored worker limit after the restart, want %d", recoveredLimit)
	case *stored != recoveredLimit:
		t.Fatalf("stored slice-queue limit after the restart = %d, want %d", *stored, recoveredLimit)
	}

	// The queue runs the slices again, and never more than the new limit at
	// once. The high-water mark is sampled from stored state while the gate
	// holds the running slices.
	highWater := 0
	waitUntil(t, 60*time.Second, func() bool {
		if running := runningSliceCount(t, second, sliceIds); running > highWater {
			highWater = running
		}
		return highWater >= recoveredLimit
	})
	if highWater > recoveredLimit {
		t.Errorf("the queue ran %d slices at once after the restart, want at most %d (the limit the restarted engine registered)",
			highWater, recoveredLimit)
	}
	for _, sliceId := range startedIds {
		if got := workflowStatusOf(t, second, sliceId).QueueName; got != engine.SliceQueueName {
			t.Errorf("recovered slice %q is on queue %q, want %q; the limit only applies on that queue",
				sliceId, got, engine.SliceQueueName)
		}
	}

	close(secondGate.release)
	close(firstGate.release)
	// The handles the crashed engine returned died with it, so the results are
	// read back through the restarted engine.
	for _, sliceId := range sliceIds {
		h, err := dbos.RetrieveWorkflow[engine.SliceResult](second.DBOS(), sliceId)
		if err != nil {
			t.Fatalf("RetrieveWorkflow(%q): %v", sliceId, err)
		}
		res := waitSliceResult(t, h, 60*time.Second)
		if !res.Success {
			errVal := "<nil>"
			if res.Error != nil {
				errVal = *res.Error
			}
			t.Errorf("slice %q Success=false after recovery; error=%s", sliceId, errVal)
		}
	}
}
