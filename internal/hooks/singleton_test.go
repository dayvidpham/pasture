package hooks_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Singleton helpers ────────────────────────────────────────────────────────

// resetSingleton resets the hooks singleton to nil between tests.
// Must be deferred at the start of every test that calls InitHooksManager.
func resetSingleton(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { hooks.InitHooksManager(nil) })
}

// ─── InitHooksManager / GetManager ───────────────────────────────────────────

func TestInitHooksManager_SetAndGet(t *testing.T) {
	resetSingleton(t)

	mgr := hooks.NewManager()
	hooks.InitHooksManager(mgr)

	got := hooks.GetManager()
	if got == nil {
		t.Fatal("GetManager() = nil, want the initialized Manager")
	}
	if got != mgr {
		t.Error("GetManager() returned a different Manager than was injected")
	}
}

func TestGetManager_BeforeInit_ReturnsNil(t *testing.T) {
	// No InitHooksManager call — should be nil from previous test cleanup or
	// this test running in isolation.
	hooks.InitHooksManager(nil)
	t.Cleanup(func() { hooks.InitHooksManager(nil) })

	if got := hooks.GetManager(); got != nil {
		t.Errorf("GetManager() before init = %v, want nil", got)
	}
}

func TestInitHooksManager_NilResetsState(t *testing.T) {
	resetSingleton(t)

	mgr := hooks.NewManager()
	hooks.InitHooksManager(mgr)
	if hooks.GetManager() == nil {
		t.Fatal("expected non-nil manager after init")
	}

	hooks.InitHooksManager(nil)
	if hooks.GetManager() != nil {
		t.Error("GetManager() after InitHooksManager(nil) = non-nil, want nil")
	}
}

func TestInitHooksManager_CanReplace(t *testing.T) {
	resetSingleton(t)

	mgr1 := hooks.NewManager()
	mgr2 := hooks.NewManager()
	hooks.InitHooksManager(mgr1)
	hooks.InitHooksManager(mgr2)

	if hooks.GetManager() != mgr2 {
		t.Error("GetManager() should return the most recently injected Manager")
	}
}

// ─── DispatchHook — nil singleton (no-op) ────────────────────────────────────

func TestDispatchHook_NilManager_IsNoOp(t *testing.T) {
	hooks.InitHooksManager(nil)
	t.Cleanup(func() { hooks.InitHooksManager(nil) })

	payload := hooks.HookPayload{
		Event:   hooks.HookPhaseTransition,
		EpochID: "epoch-noop",
		Phase:   protocol.PhaseWorkerSlices,
	}

	err := hooks.DispatchHook(context.Background(), payload)
	if err != nil {
		t.Errorf("DispatchHook with nil manager: want nil error, got %v", err)
	}
}

func TestDispatchHook_NilManager_NilPayload_IsNoOp(t *testing.T) {
	hooks.InitHooksManager(nil)
	t.Cleanup(func() { hooks.InitHooksManager(nil) })

	// Even with an empty payload and nil manager — must not panic or error.
	err := hooks.DispatchHook(context.Background(), hooks.HookPayload{})
	if err != nil {
		t.Errorf("DispatchHook with nil manager + empty payload: want nil, got %v", err)
	}
}

// ─── DispatchHook — with initialized manager ─────────────────────────────────

func TestDispatchHook_WithManager_DispatchesToHandler(t *testing.T) {
	resetSingleton(t)

	handler := newRecordingHandler(hooks.HookPhaseTransition)
	mgr := hooks.NewManager()
	mgr.Register(handler)
	hooks.InitHooksManager(mgr)

	payload := hooks.HookPayload{
		Event:   hooks.HookPhaseTransition,
		EpochID: "epoch-dispatch-1",
		Phase:   protocol.PhaseElicit,
		Data:    map[string]any{"from": "p1", "to": "p2"},
	}

	err := hooks.DispatchHook(context.Background(), payload)
	if err != nil {
		t.Fatalf("DispatchHook: unexpected error: %v", err)
	}
	if handler.count() != 1 {
		t.Errorf("handler called %d times, want 1", handler.count())
	}
	got := handler.payloads()
	if len(got) == 0 {
		t.Fatal("no payloads received by handler")
	}
	if got[0].EpochID != "epoch-dispatch-1" {
		t.Errorf("payload EpochID = %q, want %q", got[0].EpochID, "epoch-dispatch-1")
	}
}

func TestDispatchHook_WithManager_UnsubscribedEvent_NoError(t *testing.T) {
	resetSingleton(t)

	handler := newRecordingHandler(hooks.HookEpochStarted)
	mgr := hooks.NewManager()
	mgr.Register(handler)
	hooks.InitHooksManager(mgr)

	// Dispatch an event the handler is not subscribed to.
	err := hooks.DispatchHook(context.Background(), hooks.HookPayload{
		Event:   hooks.HookSliceFailed,
		EpochID: "epoch-unsub",
	})
	if err != nil {
		t.Errorf("DispatchHook unsubscribed event: want nil, got %v", err)
	}
	if handler.count() != 0 {
		t.Errorf("handler called %d times for unsubscribed event, want 0", handler.count())
	}
}

func TestDispatchHook_WithManager_HandlerError_Propagated(t *testing.T) {
	resetSingleton(t)

	handlerErr := errors.New("handler failed in dispatch hook")
	eh := newErrorHandler(handlerErr, hooks.HookConstraintViolation)
	mgr := hooks.NewManager()
	mgr.Register(eh)
	hooks.InitHooksManager(mgr)

	err := hooks.DispatchHook(context.Background(), hooks.HookPayload{
		Event:   hooks.HookConstraintViolation,
		EpochID: "epoch-err",
	})
	if err == nil {
		t.Error("DispatchHook: expected error from handler, got nil")
	}
}

func TestDispatchHook_WithManager_MultipleHandlers_AllReceive(t *testing.T) {
	resetSingleton(t)

	h1 := newRecordingHandler(hooks.HookSliceStarted)
	h2 := newRecordingHandler(hooks.HookSliceStarted)
	mgr := hooks.NewManager()
	mgr.Register(h1)
	mgr.Register(h2)
	hooks.InitHooksManager(mgr)

	payload := hooks.HookPayload{
		Event:   hooks.HookSliceStarted,
		EpochID: "epoch-multi",
		Phase:   protocol.PhaseWorkerSlices,
	}
	if err := hooks.DispatchHook(context.Background(), payload); err != nil {
		t.Fatalf("DispatchHook: unexpected error: %v", err)
	}
	if h1.count() != 1 {
		t.Errorf("h1 called %d times, want 1", h1.count())
	}
	if h2.count() != 1 {
		t.Errorf("h2 called %d times, want 1", h2.count())
	}
}

// ─── Hook dispatch fires correct HookEvent types ──────────────────────────────

// TestDispatchHook_HookPhaseTransition verifies that dispatching a
// HookPhaseTransition payload reaches only handlers subscribed to that event,
// matching the RecordTransition activity usage.
func TestDispatchHook_HookPhaseTransition_CorrectEvent(t *testing.T) {
	resetSingleton(t)

	transitionHandler := newRecordingHandler(hooks.HookPhaseTransition)
	otherHandler := newRecordingHandler(hooks.HookSliceStarted)
	mgr := hooks.NewManager()
	mgr.Register(transitionHandler)
	mgr.Register(otherHandler)
	hooks.InitHooksManager(mgr)

	payload := hooks.HookPayload{
		Event:   hooks.HookPhaseTransition,
		EpochID: "epoch-pt",
		Phase:   protocol.PhaseElicit,
		Data: map[string]any{
			"from":        string(protocol.PhaseRequest),
			"to":          string(protocol.PhaseElicit),
			"triggeredBy": "architect",
			"success":     true,
		},
	}
	if err := hooks.DispatchHook(context.Background(), payload); err != nil {
		t.Fatalf("DispatchHook HookPhaseTransition: unexpected error: %v", err)
	}
	if transitionHandler.count() != 1 {
		t.Errorf("transitionHandler called %d times, want 1", transitionHandler.count())
	}
	if otherHandler.count() != 0 {
		t.Errorf("otherHandler called %d times, want 0 (wrong event)", otherHandler.count())
	}

	// Verify payload contents.
	payloads := transitionHandler.payloads()
	if len(payloads) == 0 {
		t.Fatal("transitionHandler received no payloads")
	}
	if payloads[0].Event != hooks.HookPhaseTransition {
		t.Errorf("payload.Event = %q, want %q", payloads[0].Event, hooks.HookPhaseTransition)
	}
	if payloads[0].Phase != protocol.PhaseElicit {
		t.Errorf("payload.Phase = %q, want %q", payloads[0].Phase, protocol.PhaseElicit)
	}
}

// TestDispatchHook_HookConstraintViolation verifies that dispatching a
// HookConstraintViolation payload is received only by the appropriate handler,
// matching the CheckConstraints activity usage.
func TestDispatchHook_HookConstraintViolation_CorrectEvent(t *testing.T) {
	resetSingleton(t)

	violationHandler := newRecordingHandler(hooks.HookConstraintViolation)
	mgr := hooks.NewManager()
	mgr.Register(violationHandler)
	hooks.InitHooksManager(mgr)

	payload := hooks.HookPayload{
		Event:   hooks.HookConstraintViolation,
		EpochID: "epoch-cv",
		Phase:   protocol.PhaseReview,
		Data: map[string]any{
			"violations": []string{"consensus-gate: missing votes"},
		},
	}
	if err := hooks.DispatchHook(context.Background(), payload); err != nil {
		t.Fatalf("DispatchHook HookConstraintViolation: unexpected error: %v", err)
	}
	if violationHandler.count() != 1 {
		t.Errorf("violationHandler called %d times, want 1", violationHandler.count())
	}

	payloads := violationHandler.payloads()
	if len(payloads) == 0 {
		t.Fatal("violationHandler received no payloads")
	}
	if payloads[0].Event != hooks.HookConstraintViolation {
		t.Errorf("payload.Event = %q, want %q", payloads[0].Event, hooks.HookConstraintViolation)
	}
}

// TestDispatchHook_SliceLifecycle verifies that the three slice lifecycle
// events (HookSliceStarted, HookSliceCompleted, HookSliceFailed) can each be
// dispatched independently to their respective handlers.
func TestDispatchHook_SliceLifecycle_AllThreeEvents(t *testing.T) {
	resetSingleton(t)

	startedH := newRecordingHandler(hooks.HookSliceStarted)
	completedH := newRecordingHandler(hooks.HookSliceCompleted)
	failedH := newRecordingHandler(hooks.HookSliceFailed)

	mgr := hooks.NewManager()
	mgr.Register(startedH)
	mgr.Register(completedH)
	mgr.Register(failedH)
	hooks.InitHooksManager(mgr)

	base := hooks.HookPayload{EpochID: "epoch-slice-lifecycle"}

	// Dispatch HookSliceStarted.
	p1 := base
	p1.Event = hooks.HookSliceStarted
	p1.Data = map[string]any{"sliceId": "S1"}
	if err := hooks.DispatchHook(context.Background(), p1); err != nil {
		t.Fatalf("DispatchHook HookSliceStarted: %v", err)
	}

	// Dispatch HookSliceCompleted.
	p2 := base
	p2.Event = hooks.HookSliceCompleted
	p2.Data = map[string]any{"sliceId": "S1", "success": true}
	if err := hooks.DispatchHook(context.Background(), p2); err != nil {
		t.Fatalf("DispatchHook HookSliceCompleted: %v", err)
	}

	// Dispatch HookSliceFailed.
	p3 := base
	p3.Event = hooks.HookSliceFailed
	p3.Data = map[string]any{"sliceId": "S2", "error": "activity timeout"}
	if err := hooks.DispatchHook(context.Background(), p3); err != nil {
		t.Fatalf("DispatchHook HookSliceFailed: %v", err)
	}

	if startedH.count() != 1 {
		t.Errorf("startedH count = %d, want 1", startedH.count())
	}
	if completedH.count() != 1 {
		t.Errorf("completedH count = %d, want 1", completedH.count())
	}
	if failedH.count() != 1 {
		t.Errorf("failedH count = %d, want 1", failedH.count())
	}
}

// TestDispatchHook_ContextCancelled_NoError ensures a cancelled context is
// handled gracefully when the manager is nil (no-op path).
func TestDispatchHook_ContextCancelled_NilManager_IsNoOp(t *testing.T) {
	hooks.InitHooksManager(nil)
	t.Cleanup(func() { hooks.InitHooksManager(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	// Let the context expire.
	<-ctx.Done()

	err := hooks.DispatchHook(ctx, hooks.HookPayload{Event: hooks.HookSliceStarted})
	if err != nil {
		t.Errorf("DispatchHook with cancelled ctx + nil manager: want nil, got %v", err)
	}
}
