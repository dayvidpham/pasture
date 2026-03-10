package temporal

import (
	"context"
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Module-level singleton ───────────────────────────────────────────────────

var auditTrail audit.Trail

const uninitializedMsg = "AuditTrail not initialized — call InitAuditTrail() before starting pastured worker. " +
	"Inject a concrete audit.Trail (e.g. audit.NewInMemoryAuditTrail()) via InitAuditTrail() in worker startup code."

// InitAuditTrail injects the audit.Trail implementation for this worker process.
// Must be called once before the Temporal worker starts. Safe to call multiple
// times (e.g. between test cases). Passing nil resets the singleton (useful
// in tests to isolate state between test cases).
func InitAuditTrail(trail audit.Trail) {
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
// delegates to the injected audit.Trail; v2 may write to Beads or a durable DB.
//
// epochID is required to make audit events queryable by epoch. Pass the workflow
// input EpochID so events can be retrieved via QueryAuditEvents(epochID, ...).
//
// Returns a non-retryable ApplicationError if InitAuditTrail was never called.
func RecordTransition(ctx context.Context, epochID string, record types.TransitionRecord) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordTransition",
		slog.String("epochID", epochID),
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
		EpochID:   epochID,
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
				"(epochID=%q, triggeredBy=%q): %w",
			record.FromPhase, record.ToPhase, epochID, record.TriggeredBy, err,
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

// RecordSessionEntries persists a batch of SessionEntry records to the audit trail.
//
// Activity: non-deterministic I/O boundary.  Nil or empty slices are no-ops.
// All entries are written atomically where the backend supports transactions.
//
// Returns a non-retryable ApplicationError if InitAuditTrail was never called.
func RecordSessionEntries(ctx context.Context, entries []protocol.SessionEntry) error {
	if auditTrail == nil {
		return temporal.NewNonRetryableApplicationError(
			uninitializedMsg,
			"AuditTrailUninitialized",
			nil,
		)
	}
	if err := auditTrail.RecordSessionEntries(ctx, entries); err != nil {
		return fmt.Errorf(
			"temporal.RecordSessionEntries: batch write failed (%d entries): %w",
			len(entries), err,
		)
	}
	return nil
}

// QuerySessionEntries retrieves all session entries for the given sessionID.
//
// Activity: non-deterministic I/O boundary (reads from external store).
//
// Returns an empty (non-nil) slice when no entries exist for sessionID.
// Returns a non-retryable ApplicationError if InitAuditTrail was never called.
func QuerySessionEntries(ctx context.Context, sessionID string) ([]protocol.SessionEntry, error) {
	if auditTrail == nil {
		return nil, temporal.NewNonRetryableApplicationError(
			uninitializedMsg,
			"AuditTrailUninitialized",
			nil,
		)
	}
	entries, err := auditTrail.QuerySessionEntries(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf(
			"temporal.QuerySessionEntries: query failed for sessionID=%q: %w",
			sessionID, err,
		)
	}
	return entries, nil
}
