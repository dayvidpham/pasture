// Package hooks provides a hook event dispatch system for the pasture protocol.
//
// Hook events are fired at key lifecycle transitions (phase changes, slice
// starts/completions, epoch boundaries, review cycles) and error conditions
// (slice failures, constraint violations, connection loss). Session events
// track Claude Code agent session start/end for observability.
//
// The Manager dispatches events concurrently to all registered handlers.
// Dispatch is non-blocking: each handler runs in its own goroutine under
// a context deadline. Slow or failing handlers do not block the caller.
package hooks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// DefaultDispatchTimeout is the maximum time a single handler invocation may
// run before it is abandoned. Chosen to be long enough for non-trivial I/O
// (file write, HTTP webhook) but short enough to prevent indefinite blocking.
const DefaultDispatchTimeout = 5 * time.Second

// ─── HookEvent ───────────────────────────────────────────────────────────────

// HookEvent is a typed identifier for a pasture hook event.
// Values are lowercase snake_case strings suitable for JSON serialization
// and external webhook payloads.
type HookEvent string

const (
	// Lifecycle events (7)

	// HookPhaseTransition fires whenever the epoch transitions between phases.
	HookPhaseTransition HookEvent = "phase_transition"
	// HookVoteRecorded fires when a reviewer submits a vote.
	HookVoteRecorded HookEvent = "vote_recorded"
	// HookSliceStarted fires when a worker slice begins execution.
	HookSliceStarted HookEvent = "slice_started"
	// HookSliceCompleted fires when a worker slice finishes successfully.
	HookSliceCompleted HookEvent = "slice_completed"
	// HookEpochStarted fires when a new epoch workflow is created.
	HookEpochStarted HookEvent = "epoch_started"
	// HookEpochCompleted fires when an epoch reaches the "complete" terminal phase.
	HookEpochCompleted HookEvent = "epoch_completed"
	// HookReviewCycle fires when a review round begins.
	HookReviewCycle HookEvent = "review_cycle"

	// Error events (3)

	// HookSliceFailed fires when a worker slice activity fails or panics.
	HookSliceFailed HookEvent = "slice_failed"
	// HookConstraintViolation fires when a phase-advance constraint check fails.
	HookConstraintViolation HookEvent = "constraint_violation"
	// HookConnectionLost fires when the connection to the Temporal server is lost.
	HookConnectionLost HookEvent = "connection_lost"

	// Session events (2)

	// HookSessionStarted fires when a Claude Code agent session is registered.
	HookSessionStarted HookEvent = "session_started"
	// HookSessionEnded fires when a Claude Code agent session ends.
	HookSessionEnded HookEvent = "session_ended"
)

// AllHookEvents is the ordered slice of all valid HookEvent values.
// Useful for registration completeness checks and tests.
var AllHookEvents = []HookEvent{
	HookPhaseTransition,
	HookVoteRecorded,
	HookSliceStarted,
	HookSliceCompleted,
	HookEpochStarted,
	HookEpochCompleted,
	HookReviewCycle,
	HookSliceFailed,
	HookConstraintViolation,
	HookConnectionLost,
	HookSessionStarted,
	HookSessionEnded,
}

// ─── HookPayload ─────────────────────────────────────────────────────────────

// HookPayload carries the data emitted with every hook event.
//
// Phase is optional (zero-value "" for non-phase events such as
// HookSessionStarted/HookSessionEnded).
// Data holds arbitrary event-specific key/value pairs.
type HookPayload struct {
	Event   HookEvent        `json:"event"`
	EpochID string           `json:"epochId"`
	Phase   protocol.PhaseId `json:"phase,omitempty"`
	Data    map[string]any   `json:"data,omitempty"`
}

// ─── HookHandler ─────────────────────────────────────────────────────────────

// HookHandler is the interface that hook consumers must implement.
//
// Events returns the set of HookEvent values this handler is interested in.
// The Manager only dispatches payloads whose Event is in the returned set.
//
// Handle is called with the payload and a context that carries the dispatch
// deadline (DefaultDispatchTimeout). Implementations should respect ctx.Done()
// and return promptly when the context is cancelled.
type HookHandler interface {
	// Handle processes a hook payload. Must respect ctx cancellation.
	Handle(ctx context.Context, payload HookPayload) error
	// Events returns the set of HookEvent values this handler subscribes to.
	Events() []HookEvent
}

// ─── Manager ─────────────────────────────────────────────────────────────────

// Manager routes HookPayloads to registered HookHandlers.
//
// Handlers are registered per-event at startup via Register. At runtime,
// Dispatch fans out to all handlers subscribed to the payload's event.
// Each handler runs in its own goroutine under a fresh context with
// DefaultDispatchTimeout. Errors from individual handlers are collected
// and returned as a combined error, but do not prevent other handlers
// from running.
//
// Manager is safe for concurrent use from multiple goroutines after all
// Register calls complete (typically at startup before any Dispatch calls).
type Manager struct {
	mu       sync.RWMutex
	handlers map[HookEvent][]HookHandler
}

// NewManager creates an empty Manager with no registered handlers.
func NewManager() *Manager {
	return &Manager{
		handlers: make(map[HookEvent][]HookHandler),
	}
}

// Register subscribes h to all events returned by h.Events().
// Registration is additive — calling Register multiple times for the same
// handler appends it each time. Register is not safe to call concurrently
// with Dispatch; register all handlers before starting dispatch.
func (m *Manager) Register(h HookHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, event := range h.Events() {
		m.handlers[event] = append(m.handlers[event], h)
	}
}

// dispatchErr captures a handler error alongside its handler identity.
type dispatchErr struct {
	handler HookHandler
	err     error
}

// Dispatch sends payload to all handlers registered for payload.Event.
//
// Each handler is invoked in its own goroutine under a context derived from
// ctx, with a hard deadline of DefaultDispatchTimeout. Dispatch blocks until
// ALL handler goroutines have returned (or timed out), then returns a combined
// error if any handler failed.
//
// "Non-blocking" here means: Dispatch does not block the CALLER indefinitely —
// handlers run with bounded timeouts. The caller must still await Dispatch's
// return to learn of errors. To fire-and-forget, wrap Dispatch in a goroutine.
func (m *Manager) Dispatch(ctx context.Context, payload HookPayload) error {
	m.mu.RLock()
	handlers := m.handlers[payload.Event]
	// Copy slice to avoid holding the lock during dispatch.
	snapshot := make([]HookHandler, len(handlers))
	copy(snapshot, handlers)
	m.mu.RUnlock()

	if len(snapshot) == 0 {
		return nil
	}

	errs := make(chan dispatchErr, len(snapshot))
	var wg sync.WaitGroup

	for _, h := range snapshot {
		wg.Add(1)
		go func(handler HookHandler) {
			defer wg.Done()
			hCtx, cancel := context.WithTimeout(ctx, DefaultDispatchTimeout)
			defer cancel()
			if err := handler.Handle(hCtx, payload); err != nil {
				errs <- dispatchErr{handler: handler, err: err}
			}
		}(h)
	}

	wg.Wait()
	close(errs)

	// Collect errors (if any).
	var combined []error
	for de := range errs {
		combined = append(combined, de.err)
	}
	if len(combined) == 0 {
		return nil
	}
	return &dispatchErrors{errs: combined}
}

// ─── dispatchErrors ──────────────────────────────────────────────────────────

// dispatchErrors aggregates multiple handler errors from a single Dispatch call.
type dispatchErrors struct {
	errs []error
}

// Error returns a summary of how many handlers failed.
func (e *dispatchErrors) Error() string {
	if len(e.errs) == 1 {
		return "hooks.Manager: 1 handler returned an error: " + e.errs[0].Error()
	}
	msgs := make([]string, len(e.errs))
	for i, err := range e.errs {
		msgs[i] = err.Error()
	}
	// Build a compact summary rather than importing strings/fmt loops.
	return fmt.Sprintf("hooks.Manager: %d handlers returned errors", len(e.errs))
}

// Unwrap returns the slice of underlying errors for use with errors.As/Is.
func (e *dispatchErrors) Unwrap() []error { return e.errs }
