package temporal

import (
	"context"
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── AuditTrail interface ─────────────────────────────────────────────────────

// AuditTrail is the dependency-injection interface for recording audit events.
// Implemented by InMemoryAuditTrail (tests/dev) and future durable backends.
// The worker injects a concrete implementation before starting via InitAuditTrail.
type AuditTrail interface {
	RecordEvent(ctx context.Context, event protocol.AuditEvent) error
	QueryEvents(ctx context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error)
}

// ─── Module-level singleton ───────────────────────────────────────────────────

var auditTrail AuditTrail

const uninitializedMsg = "AuditTrail not initialized — call InitAuditTrail() before starting pastured worker. " +
	"Inject a concrete AuditTrail (e.g. NewInMemoryAuditTrail()) via InitAuditTrail() in worker startup code."

// InitAuditTrail injects the AuditTrail implementation for this worker process.
// Must be called once before the Temporal worker starts. Safe to call multiple
// times (e.g. between test cases).
func InitAuditTrail(trail AuditTrail) {
	auditTrail = trail
}

// ─── ConstraintViolation ──────────────────────────────────────────────────────

// ConstraintViolation describes a single protocol constraint that was violated
// during a proposed phase transition.
type ConstraintViolation struct {
	// Constraint is the machine-readable constraint ID (e.g. "consensus-gate").
	Constraint string `json:"constraint"`
	// Message is a human-readable description of what violated the constraint.
	Message string `json:"message"`
}

// ─── Activities ───────────────────────────────────────────────────────────────

// CheckConstraints validates a proposed phase transition against current epoch
// state and returns any constraint violations.
//
// Activity: non-deterministic I/O boundary. Runs outside workflow code.
// Returns an empty slice when the transition is valid.
//
// In v1, delegates to the state machine's ValidateAdvance logic.  Future
// versions may incorporate external constraint sources (e.g. beads task state).
//
// Args:
//
//	state:   Current epoch state snapshot.
//	toPhase: Proposed target phase.
func CheckConstraints(ctx context.Context, state types.EpochState, toPhase protocol.PhaseId) ([]ConstraintViolation, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("CheckConstraints", "from", state.CurrentPhase, "to", toPhase)

	// Reconstruct a temporary state machine to run ValidateAdvance.
	// We do not call Advance — only validation.
	sm := &EpochStateMachine{state: &state, specs: PhaseSpecs}
	violations := sm.ValidateAdvance(toPhase)

	result := make([]ConstraintViolation, 0, len(violations))
	for _, v := range violations {
		result = append(result, ConstraintViolation{
			Constraint: "state-machine",
			Message:    v,
		})
	}
	return result, nil
}

// RecordTransition persists a transition record to the audit trail.
//
// Activity: non-deterministic I/O boundary.  In v1 this logs the transition and
// delegates to the injected AuditTrail; v2 may write to Beads or a durable DB.
//
// Returns a non-retryable ApplicationError if InitAuditTrail was never called.
func RecordTransition(ctx context.Context, record types.TransitionRecord) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordTransition",
		slog.String("from", string(record.FromPhase)),
		slog.String("to", string(record.ToPhase)),
		slog.String("triggeredBy", record.TriggeredBy),
		slog.Bool("success", record.Success),
	)

	if auditTrail == nil {
		return temporal.NewNonRetryableApplicationError(
			uninitializedMsg,
			"AuditTrailUninitialized",
			nil,
		)
	}

	event := protocol.AuditEvent{
		EpochID:   "",   // not available in TransitionRecord; set by caller if needed
		Phase:     record.ToPhase,
		EventType: protocol.EventPhaseTransition,
		Payload: map[string]any{
			"from":         string(record.FromPhase),
			"to":           string(record.ToPhase),
			"triggeredBy":  record.TriggeredBy,
			"conditionMet": record.ConditionMet,
			"success":      record.Success,
		},
		Timestamp: record.Timestamp,
	}
	if err := auditTrail.RecordEvent(ctx, event); err != nil {
		return fmt.Errorf(
			"temporal.RecordTransition: failed to record audit event for %q → %q "+
				"(triggeredBy=%q): %w",
			record.FromPhase, record.ToPhase, record.TriggeredBy, err,
		)
	}
	return nil
}

// RecordAuditEvent persists a generic AuditEvent to the trail.
//
// Activity: non-deterministic I/O boundary. Used for non-transition events
// (vote recorded, session registered, etc.).
//
// Returns a non-retryable ApplicationError if InitAuditTrail was never called.
func RecordAuditEvent(ctx context.Context, event protocol.AuditEvent) error {
	if auditTrail == nil {
		return temporal.NewNonRetryableApplicationError(
			uninitializedMsg,
			"AuditTrailUninitialized",
			nil,
		)
	}
	if err := auditTrail.RecordEvent(ctx, event); err != nil {
		return fmt.Errorf(
			"temporal.RecordAuditEvent: failed to record audit event "+
				"(epochID=%q, eventType=%q, phase=%q): %w",
			event.EpochID, event.EventType, event.Phase, err,
		)
	}
	return nil
}

// QueryAuditEvents retrieves audit events for an epoch, with optional filters.
//
// Activity: non-deterministic I/O boundary (reads from external store).
//
// Args:
//
//	epochID: Required — the epoch to query events for.
//	phase:   Optional phase filter (nil = all phases).
//	role:    Optional role filter (nil = all roles).
//
// Returns a non-retryable ApplicationError if InitAuditTrail was never called.
func QueryAuditEvents(ctx context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	if auditTrail == nil {
		return nil, temporal.NewNonRetryableApplicationError(
			uninitializedMsg,
			"AuditTrailUninitialized",
			nil,
		)
	}
	events, err := auditTrail.QueryEvents(ctx, epochID, phase, role)
	if err != nil {
		return nil, fmt.Errorf(
			"temporal.QueryAuditEvents: query failed for epochID=%q: %w",
			epochID, err,
		)
	}
	return events, nil
}

// ─── InMemoryAuditTrail ───────────────────────────────────────────────────────

// InMemoryAuditTrail is the test/dev AuditTrail implementation backed by an
// in-memory slice. Does not persist across worker restarts.
type InMemoryAuditTrail struct {
	events []protocol.AuditEvent
}

// NewInMemoryAuditTrail returns an initialized InMemoryAuditTrail.
func NewInMemoryAuditTrail() *InMemoryAuditTrail {
	return &InMemoryAuditTrail{}
}

// RecordEvent appends the event to the in-memory list.
func (t *InMemoryAuditTrail) RecordEvent(_ context.Context, event protocol.AuditEvent) error {
	t.events = append(t.events, event)
	return nil
}

// QueryEvents returns events matching the given filters, in insertion order.
func (t *InMemoryAuditTrail) QueryEvents(_ context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	var result []protocol.AuditEvent
	for _, e := range t.events {
		if epochID != "" && e.EpochID != epochID {
			continue
		}
		if phase != nil && e.Phase != *phase {
			continue
		}
		if role != nil && e.Role != *role {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

// Events returns a defensive copy of all recorded events (for assertions in tests).
func (t *InMemoryAuditTrail) Events() []protocol.AuditEvent {
	cp := make([]protocol.AuditEvent, len(t.events))
	copy(cp, t.events)
	return cp
}
