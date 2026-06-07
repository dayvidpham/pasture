package hooks_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	stderrors "errors"

	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Test helpers ────────────────────────────────────────────────────────────

// recordingHandler records every payload it receives. Safe for concurrent use.
type recordingHandler struct {
	mu       sync.Mutex
	received []hooks.HookPayload
	events   []hooks.HookEvent
	err      error // if non-nil, Handle returns this error
}

func newRecordingHandler(events ...hooks.HookEvent) *recordingHandler {
	return &recordingHandler{events: events}
}

func (h *recordingHandler) Handle(_ context.Context, payload hooks.HookPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.received = append(h.received, payload)
	return h.err
}

func (h *recordingHandler) Events() []hooks.HookEvent { return h.events }

func (h *recordingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.received)
}

func (h *recordingHandler) payloads() []hooks.HookPayload {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]hooks.HookPayload, len(h.received))
	copy(out, h.received)
	return out
}

// slowHandler blocks for duration before completing, to test non-blocking semantics.
type slowHandler struct {
	delay  time.Duration
	called atomic.Int32
	events []hooks.HookEvent
}

func newSlowHandler(delay time.Duration, events ...hooks.HookEvent) *slowHandler {
	return &slowHandler{delay: delay, events: events}
}

func (h *slowHandler) Handle(ctx context.Context, _ hooks.HookPayload) error {
	h.called.Add(1)
	select {
	case <-time.After(h.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *slowHandler) Events() []hooks.HookEvent { return h.events }

// gateHandler signals when Handle is entered and then blocks until released, so
// tests can assert dispatch ordering deterministically (handler ran; dispatch
// waited for it; dispatch returned once it completed) without wall-clock thresholds.
type gateHandler struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	called  atomic.Int32
	events  []hooks.HookEvent
}

func newGateHandler(events ...hooks.HookEvent) *gateHandler {
	return &gateHandler{started: make(chan struct{}), release: make(chan struct{}), events: events}
}

func (h *gateHandler) Handle(ctx context.Context, _ hooks.HookPayload) error {
	h.called.Add(1)
	h.once.Do(func() { close(h.started) })
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *gateHandler) Events() []hooks.HookEvent { return h.events }

// errorHandler always returns a fixed error.
type errorHandler struct {
	err    error
	events []hooks.HookEvent
}

func newErrorHandler(err error, events ...hooks.HookEvent) *errorHandler {
	return &errorHandler{err: err, events: events}
}

func (h *errorHandler) Handle(_ context.Context, _ hooks.HookPayload) error { return h.err }
func (h *errorHandler) Events() []hooks.HookEvent                           { return h.events }

// samplePayload builds a HookPayload for the given event.
func samplePayload(event hooks.HookEvent) hooks.HookPayload {
	return hooks.HookPayload{
		Event:   event,
		EpochId: "test-epoch-001",
		Phase:   protocol.PhaseWorkerSlices,
		Data:    map[string]any{"slice": "S4"},
	}
}

// ─── AllHookEvents ───────────────────────────────────────────────────────────

func TestAllHookEvents_Count(t *testing.T) {
	if len(hooks.AllHookEvents) != 13 {
		t.Errorf("AllHookEvents: want 13 events, got %d", len(hooks.AllHookEvents))
	}
}

func TestAllHookEvents_Unique(t *testing.T) {
	seen := make(map[hooks.HookEvent]bool)
	for _, e := range hooks.AllHookEvents {
		if seen[e] {
			t.Errorf("duplicate HookEvent value: %q", e)
		}
		seen[e] = true
	}
}

func TestAllHookEvents_ContainsExpected(t *testing.T) {
	expected := []hooks.HookEvent{
		hooks.HookPhaseTransition,
		hooks.HookVoteRecorded,
		hooks.HookSliceStarted,
		hooks.HookSliceCompleted,
		hooks.HookEpochStarted,
		hooks.HookEpochCompleted,
		hooks.HookReviewCycle,
		hooks.HookSliceFailed,
		hooks.HookConstraintViolation,
		hooks.HookConnectionLost,
		hooks.HookSessionStarted,
		hooks.HookSessionEnded,
		hooks.HookGitCommit,
	}
	set := make(map[hooks.HookEvent]bool)
	for _, e := range hooks.AllHookEvents {
		set[e] = true
	}
	for _, e := range expected {
		if !set[e] {
			t.Errorf("AllHookEvents: missing expected event %q", e)
		}
	}
}

// TestHookGitCommit_WireValueAndMembership pins the wire value of the
// free-floating git-commit event and asserts it is a member of AllHookEvents
// (L1 — the shared contract consumed by GitRecorder's subscription, the CLI
// handler's payload Event, and the dispatch tests).
func TestHookGitCommit_WireValueAndMembership(t *testing.T) {
	if hooks.HookGitCommit != "git_commit" {
		t.Errorf("HookGitCommit wire value = %q, want %q", hooks.HookGitCommit, "git_commit")
	}
	if !slices.Contains(hooks.AllHookEvents, hooks.HookGitCommit) {
		t.Errorf("AllHookEvents does not contain HookGitCommit (%q)", hooks.HookGitCommit)
	}
}

// ─── Manager.Register ────────────────────────────────────────────────────────

func TestManager_Register_SingleHandler(t *testing.T) {
	m := hooks.NewManager()
	h := newRecordingHandler(hooks.HookEpochStarted)
	m.Register(h)

	payload := samplePayload(hooks.HookEpochStarted)
	if err := m.Dispatch(context.Background(), payload); err != nil {
		t.Fatalf("Dispatch: unexpected error: %v", err)
	}
	if h.count() != 1 {
		t.Errorf("handler called %d times, want 1", h.count())
	}
}

func TestManager_Register_MultipleHandlersSameEvent(t *testing.T) {
	m := hooks.NewManager()
	h1 := newRecordingHandler(hooks.HookSliceStarted)
	h2 := newRecordingHandler(hooks.HookSliceStarted)
	m.Register(h1)
	m.Register(h2)

	payload := samplePayload(hooks.HookSliceStarted)
	if err := m.Dispatch(context.Background(), payload); err != nil {
		t.Fatalf("Dispatch: unexpected error: %v", err)
	}
	if h1.count() != 1 {
		t.Errorf("h1 called %d times, want 1", h1.count())
	}
	if h2.count() != 1 {
		t.Errorf("h2 called %d times, want 1", h2.count())
	}
}

func TestManager_Register_HandlerSubscribedToMultipleEvents(t *testing.T) {
	m := hooks.NewManager()
	h := newRecordingHandler(hooks.HookSliceStarted, hooks.HookSliceCompleted)
	m.Register(h)

	m.Dispatch(context.Background(), samplePayload(hooks.HookSliceStarted))   //nolint
	m.Dispatch(context.Background(), samplePayload(hooks.HookSliceCompleted)) //nolint

	if h.count() != 2 {
		t.Errorf("handler called %d times, want 2", h.count())
	}
}

// ─── Manager.Dispatch — unsubscribed events ──────────────────────────────────

func TestManager_Dispatch_UnsubscribedEvent_NoError(t *testing.T) {
	m := hooks.NewManager()
	h := newRecordingHandler(hooks.HookEpochStarted)
	m.Register(h)

	// Dispatch an event that h is NOT subscribed to.
	if err := m.Dispatch(context.Background(), samplePayload(hooks.HookSliceFailed)); err != nil {
		t.Fatalf("Dispatch unsubscribed event: want nil error, got %v", err)
	}
	if h.count() != 0 {
		t.Errorf("handler called %d times for unsubscribed event, want 0", h.count())
	}
}

func TestManager_Dispatch_EmptyManager_NoError(t *testing.T) {
	m := hooks.NewManager()
	if err := m.Dispatch(context.Background(), samplePayload(hooks.HookEpochCompleted)); err != nil {
		t.Fatalf("Dispatch on empty manager: want nil, got %v", err)
	}
}

// ─── Manager.Dispatch — non-blocking ─────────────────────────────────────────

func TestManager_Dispatch_WaitsForHandlerThenReturns(t *testing.T) {
	m := hooks.NewManager()
	h := newGateHandler(hooks.HookReviewCycle)
	m.Register(h)

	// Generous ctx — this test asserts causal ordering via channels, not a deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- m.Dispatch(ctx, samplePayload(hooks.HookReviewCycle)) }()

	// 1. The handler must actually be invoked.
	select {
	case <-h.started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was never invoked")
	}

	// 2. Dispatch must still be in-flight while the handler is blocked — it waits
	//    for its handlers rather than returning early.
	select {
	case <-done:
		t.Fatal("Dispatch returned before the handler completed; it must wait for handlers")
	case <-time.After(50 * time.Millisecond):
	}

	// 3. Releasing the handler lets Dispatch return — driven by handler completion,
	//    not a wall-clock budget.
	close(h.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch did not return after the handler completed")
	}

	if h.called.Load() != 1 {
		t.Errorf("handler called %d times, want 1", h.called.Load())
	}
}

func TestManager_Dispatch_HandlerTimedOut_ContextCancelled(t *testing.T) {
	m := hooks.NewManager()
	// Handler sleeps longer than DefaultDispatchTimeout (5s).
	// We override the per-handler deadline by passing a very short parent ctx.
	// Since Dispatch uses DefaultDispatchTimeout, a very-slow handler will be
	// abandoned after DefaultDispatchTimeout.
	// In tests, we use a handler that sleeps 10s but context cancels at 50ms.
	slow := newSlowHandler(10*time.Second, hooks.HookConnectionLost)
	m.Register(slow)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := m.Dispatch(ctx, samplePayload(hooks.HookConnectionLost))
	// Expect an error since the handler's context was cancelled.
	if err == nil {
		t.Fatal("expected error when handler context is cancelled, got nil")
	}
}

// ─── Manager.Dispatch — concurrent ───────────────────────────────────────────

func TestManager_Dispatch_Concurrent_AllHandlersReceivePayload(t *testing.T) {
	m := hooks.NewManager()

	const numHandlers = 10
	handlers := make([]*recordingHandler, numHandlers)
	for i := range handlers {
		h := newRecordingHandler(hooks.HookPhaseTransition)
		handlers[i] = h
		m.Register(h)
	}

	payload := samplePayload(hooks.HookPhaseTransition)
	if err := m.Dispatch(context.Background(), payload); err != nil {
		t.Fatalf("Dispatch: unexpected error: %v", err)
	}

	for i, h := range handlers {
		if h.count() != 1 {
			t.Errorf("handler[%d] called %d times, want 1", i, h.count())
		}
		if p := h.payloads(); len(p) > 0 && p[0].EpochId != "test-epoch-001" {
			t.Errorf("handler[%d] received wrong EpochId: %q", i, p[0].EpochId)
		}
	}
}

func TestManager_Dispatch_Concurrent_MultipleDispatches(t *testing.T) {
	m := hooks.NewManager()
	h := newRecordingHandler(hooks.HookVoteRecorded)
	m.Register(h)

	const numDispatches = 50
	var wg sync.WaitGroup
	for i := 0; i < numDispatches; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Dispatch(context.Background(), samplePayload(hooks.HookVoteRecorded)) //nolint
		}()
	}
	wg.Wait()

	if h.count() != numDispatches {
		t.Errorf("handler called %d times, want %d", h.count(), numDispatches)
	}
}

// ─── Manager.Dispatch — error handling ───────────────────────────────────────

func TestManager_Dispatch_HandlerError_ReturnedToDispatch(t *testing.T) {
	m := hooks.NewManager()
	handlerErr := stderrors.New("hook handler failed")
	eh := newErrorHandler(handlerErr, hooks.HookSliceFailed)
	m.Register(eh)

	err := m.Dispatch(context.Background(), samplePayload(hooks.HookSliceFailed))
	if err == nil {
		t.Fatal("expected error from handler, got nil")
	}
}

func TestManager_Dispatch_OneHandlerErrors_OtherStillCalled(t *testing.T) {
	m := hooks.NewManager()
	handlerErr := stderrors.New("partial handler error")
	eh := newErrorHandler(handlerErr, hooks.HookConstraintViolation)
	good := newRecordingHandler(hooks.HookConstraintViolation)
	m.Register(eh)
	m.Register(good)

	err := m.Dispatch(context.Background(), samplePayload(hooks.HookConstraintViolation))
	if err == nil {
		t.Error("expected error from failing handler, got nil")
	}
	if good.count() != 1 {
		t.Errorf("good handler called %d times, want 1 (error from other handler must not prevent good handler from running)", good.count())
	}
}

// ─── dispatchErrors.Error and Unwrap ────────────────────────────────────────

func TestDispatchErrors_Error_MultipleHandlers(t *testing.T) {
	m := hooks.NewManager()

	// Register two handlers that both return errors on the same event.
	err1 := stderrors.New("first handler error")
	err2 := stderrors.New("second handler error")
	h1 := newErrorHandler(err1, hooks.HookEpochStarted)
	h2 := newErrorHandler(err2, hooks.HookEpochStarted)
	m.Register(h1)
	m.Register(h2)

	// Dispatch the event; expect a combined error with both handler errors.
	err := m.Dispatch(context.Background(), samplePayload(hooks.HookEpochStarted))
	if err == nil {
		t.Fatal("expected error from handlers, got nil")
	}

	// Check that the error message contains numbered lines for both errors.
	errMsg := err.Error()
	if !contains(errMsg, "[1]") {
		t.Errorf("Error message missing [1] prefix: %s", errMsg)
	}
	if !contains(errMsg, "[2]") {
		t.Errorf("Error message missing [2] prefix: %s", errMsg)
	}
	if !contains(errMsg, "first handler error") {
		t.Errorf("Error message missing 'first handler error': %s", errMsg)
	}
	if !contains(errMsg, "second handler error") {
		t.Errorf("Error message missing 'second handler error': %s", errMsg)
	}
}

func TestDispatchErrors_Unwrap(t *testing.T) {
	m := hooks.NewManager()

	// Register two handlers that return errors.
	err1 := stderrors.New("handler 1 failed")
	err2 := stderrors.New("handler 2 failed")
	h1 := newErrorHandler(err1, hooks.HookSliceCompleted)
	h2 := newErrorHandler(err2, hooks.HookSliceCompleted)
	m.Register(h1)
	m.Register(h2)

	// Dispatch and get the combined error.
	combinedErr := m.Dispatch(context.Background(), samplePayload(hooks.HookSliceCompleted))
	if combinedErr == nil {
		t.Fatal("expected error from handlers, got nil")
	}

	// The dispatchErrors type implements Unwrap() []error, which allows errors.Is and errors.As
	// to traverse the error chain. Verify the error message contains both numbered entries.
	errMsg := combinedErr.Error()
	if !contains(errMsg, "[1]") {
		t.Errorf("Error() should contain [1], got: %s", errMsg)
	}
	if !contains(errMsg, "[2]") {
		t.Errorf("Error() should contain [2], got: %s", errMsg)
	}
	if !contains(errMsg, "handler 1 failed") {
		t.Errorf("Error() should contain 'handler 1 failed', got: %s", errMsg)
	}
	if !contains(errMsg, "handler 2 failed") {
		t.Errorf("Error() should contain 'handler 2 failed', got: %s", errMsg)
	}

	// Verify that errors.Is can detect the original errors in the wrapped error chain.
	// The Unwrap() []error method enables the error chain traversal.
	if !stderrors.Is(combinedErr, err1) {
		t.Error("errors.Is should find err1 in combined error via Unwrap() []error")
	}
	if !stderrors.Is(combinedErr, err2) {
		t.Error("errors.Is should find err2 in combined error via Unwrap() []error")
	}
}

// ─── HookPayload fields ───────────────────────────────────────────────────────

func TestHookPayload_ZeroPhase_IsAcceptable(t *testing.T) {
	// Session events do not carry a Phase.
	m := hooks.NewManager()
	h := newRecordingHandler(hooks.HookSessionStarted)
	m.Register(h)

	payload := hooks.HookPayload{
		Event:   hooks.HookSessionStarted,
		EpochId: "epoch-xyz",
		// Phase intentionally omitted (zero value "")
		Data: map[string]any{"sessionId": "sess-001"},
	}
	if err := m.Dispatch(context.Background(), payload); err != nil {
		t.Fatalf("Dispatch with zero Phase: unexpected error: %v", err)
	}
	if h.count() != 1 {
		t.Errorf("handler called %d times, want 1", h.count())
	}
	if p := h.payloads(); len(p) > 0 && p[0].Phase != "" {
		t.Errorf("Phase should be empty for session event, got %q", p[0].Phase)
	}
}

// ─── Manager — configurable dispatch timeout ──────────────────────────────────

// TestManager_WithDispatchTimeout_CustomTimeoutUsed verifies that a Manager
// created with WithDispatchTimeout uses that timeout instead of the default.
// A handler that sleeps 80 ms should complete without error under a 200 ms
// timeout, but time out (and return an error) under a 30 ms timeout.
func TestManager_WithDispatchTimeout_CustomTimeoutUsed(t *testing.T) {
	const handlerDelay = 80 * time.Millisecond

	t.Run("custom timeout long enough — handler completes", func(t *testing.T) {
		m := hooks.NewManager(hooks.WithDispatchTimeout(200 * time.Millisecond))
		slow := newSlowHandler(handlerDelay, hooks.HookSliceStarted)
		m.Register(slow)

		err := m.Dispatch(context.Background(), samplePayload(hooks.HookSliceStarted))
		if err != nil {
			t.Errorf("expected no error with generous timeout, got: %v", err)
		}
		if slow.called.Load() != 1 {
			t.Errorf("slow handler called %d times, want 1", slow.called.Load())
		}
	})

	t.Run("custom timeout too short — handler times out", func(t *testing.T) {
		m := hooks.NewManager(hooks.WithDispatchTimeout(30 * time.Millisecond))
		slow := newSlowHandler(handlerDelay, hooks.HookSliceStarted)
		m.Register(slow)

		err := m.Dispatch(context.Background(), samplePayload(hooks.HookSliceStarted))
		if err == nil {
			t.Error("expected timeout error with tight dispatch timeout, got nil")
		}
	})
}

// TestNewManager_DefaultTimeout_IsFiveSeconds confirms that a Manager created
// without options uses DefaultDispatchTimeout as its per-handler deadline.
func TestNewManager_DefaultTimeout_IsFiveSeconds(t *testing.T) {
	if hooks.DefaultDispatchTimeout != 5*time.Second {
		t.Errorf("DefaultDispatchTimeout: want 5s, got %v", hooks.DefaultDispatchTimeout)
	}

	// A handler that completes well within 5 s should never time out on a
	// default-timeout Manager.
	m := hooks.NewManager()
	fast := newRecordingHandler(hooks.HookEpochStarted)
	m.Register(fast)

	if err := m.Dispatch(context.Background(), samplePayload(hooks.HookEpochStarted)); err != nil {
		t.Errorf("unexpected error with default timeout: %v", err)
	}
}

// ─── Test helpers (continued) ────────────────────────────────────────────────

// contains checks if needle is present in haystack.
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
