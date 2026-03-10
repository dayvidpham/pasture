package hooks_test

import (
	"context"
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
	delay    time.Duration
	called   atomic.Int32
	events   []hooks.HookEvent
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
		EpochID: "test-epoch-001",
		Phase:   protocol.P9_Slice,
		Data:    map[string]any{"slice": "S4"},
	}
}

// ─── AllHookEvents ───────────────────────────────────────────────────────────

func TestAllHookEvents_Count(t *testing.T) {
	if len(hooks.AllHookEvents) != 12 {
		t.Errorf("AllHookEvents: want 12 events, got %d", len(hooks.AllHookEvents))
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

func TestManager_Dispatch_NonBlocking_FastReturn(t *testing.T) {
	m := hooks.NewManager()
	// Register a handler that sleeps longer than our test timeout budget.
	slow := newSlowHandler(200*time.Millisecond, hooks.HookReviewCycle)
	m.Register(slow)

	start := time.Now()
	// Dispatch must return before slow handler completes.
	// We run Dispatch with a very short deadline on the returned goroutines
	// — actually Dispatch waits for all handlers (with per-handler timeout).
	// What we test: Dispatch respects handler timeout so it doesn't block forever.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = m.Dispatch(ctx, samplePayload(hooks.HookReviewCycle))
	elapsed := time.Since(start)

	// Handler takes 200ms; dispatch should return within 500ms budget.
	if elapsed > 450*time.Millisecond {
		t.Errorf("Dispatch took %v, expected completion within 450ms", elapsed)
	}
	if slow.called.Load() != 1 {
		t.Errorf("slow handler was not called, want called=1, got=%d", slow.called.Load())
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
		if p := h.payloads(); len(p) > 0 && p[0].EpochID != "test-epoch-001" {
			t.Errorf("handler[%d] received wrong EpochID: %q", i, p[0].EpochID)
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

// ─── HookPayload fields ───────────────────────────────────────────────────────

func TestHookPayload_ZeroPhase_IsAcceptable(t *testing.T) {
	// Session events do not carry a Phase.
	m := hooks.NewManager()
	h := newRecordingHandler(hooks.HookSessionStarted)
	m.Register(h)

	payload := hooks.HookPayload{
		Event:   hooks.HookSessionStarted,
		EpochID: "epoch-xyz",
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
