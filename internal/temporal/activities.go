package temporal

import (
	"context"
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/dayvidpham/pasture/internal/acp"
	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/hooks"
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

	// Best-effort: fire HookConstraintViolation only when violations are found.
	// Errors are logged but do not fail the activity — hooks are optional.
	if len(result) > 0 {
		msgs := make([]string, len(result))
		for i, cv := range result {
			msgs[i] = cv.Message
		}
		hookPayload := hooks.HookPayload{
			Event:   hooks.HookConstraintViolation,
			EpochID: state.EpochID,
			Phase:   toPhase,
			Data: map[string]any{
				"from":       string(state.CurrentPhase),
				"to":         string(toPhase),
				"violations": msgs,
			},
		}
		if err := hooks.DispatchHook(ctx, hookPayload); err != nil {
			logger.Warn("CheckConstraints: hook dispatch failed (best-effort, non-fatal)",
				"event", string(hooks.HookConstraintViolation),
				"epochID", state.EpochID,
				"error", err,
			)
		}
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

	// Best-effort: fire HookPhaseTransition after successful audit recording.
	// Errors are logged but do not fail the activity — hooks are optional.
	hookPayload := hooks.HookPayload{
		Event:   hooks.HookPhaseTransition,
		EpochID: epochID,
		Phase:   record.ToPhase,
		Data: map[string]any{
			"from":         string(record.FromPhase),
			"to":           string(record.ToPhase),
			"triggeredBy":  record.TriggeredBy,
			"conditionMet": record.ConditionMet,
			"success":      record.Success,
		},
	}
	if err := hooks.DispatchHook(ctx, hookPayload); err != nil {
		logger.Warn("RecordTransition: hook dispatch failed (best-effort, non-fatal)",
			"event", string(hooks.HookPhaseTransition),
			"epochID", epochID,
			"from", string(record.FromPhase),
			"to", string(record.ToPhase),
			"error", err,
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

// ─── RunAgentSession ──────────────────────────────────────────────────────────

// RunAgentSessionInput is the input for the RunAgentSession activity.
type RunAgentSessionInput struct {
	// AgentCmd is the agent binary or command to execute (e.g. "claude").
	AgentCmd string `json:"agentCmd"`
	// AgentArgs are the command-line arguments passed to the agent binary.
	AgentArgs []string `json:"agentArgs"`
	// EpochID is the Pasture epoch this session belongs to.
	// Used to correlate session entries with epoch audit events.
	EpochID string `json:"epochId"`
}

// RunAgentSessionResult summarises the outcome of a RunAgentSession activity call.
type RunAgentSessionResult struct {
	// EntriesRecorded is the total number of SessionEntry rows written to the audit trail.
	EntriesRecorded int `json:"entriesRecorded"`
	// SessionID is the ACP session identifier for the completed session.
	// Empty when the agent produced no session/update messages.
	SessionID string `json:"sessionId,omitempty"`
	// StopReason is the ACP stop_reason from the final session update.
	// Empty when the session ended without an explicit stop reason.
	StopReason string `json:"stopReason,omitempty"`
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

// RunAgentSession starts an ACP-compatible agent process, streams its session
// updates, indexes them, and persists them to the audit trail.
//
// Activity: long-running I/O boundary. Blocks until the agent process exits or
// ctx is cancelled. All session data (messages, tool calls, token usage) is
// accumulated by the IndexingSessionHandler and written atomically to the audit
// trail at session end.
//
// Returns a non-retryable ApplicationError if InitAuditTrail was never called.
//
// Args:
//
//	input.AgentCmd:  Agent binary to execute (e.g. "claude").
//	input.AgentArgs: Arguments passed verbatim to the agent binary.
//	input.EpochID:   Pasture epoch context for audit correlation and hooks.
func RunAgentSession(ctx context.Context, input RunAgentSessionInput) (*RunAgentSessionResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RunAgentSession: starting",
		slog.String("agentCmd", input.AgentCmd),
		slog.String("epochID", input.EpochID),
	)

	if auditTrail == nil {
		return nil, temporal.NewNonRetryableApplicationError(
			uninitializedMsg,
			"AuditTrailUninitialized",
			nil,
		)
	}

	indexer := acp.NewSharedIndexer()
	handler := acp.NewIndexingSessionHandler(indexer, auditTrail, hooks.GetManager(), input.EpochID)
	acpClient := acp.NewClient(handler)

	if err := acpClient.Connect(ctx, input.AgentCmd, input.AgentArgs...); err != nil {
		return nil, fmt.Errorf(
			"temporal.RunAgentSession: ACP client failed"+
				" (agentCmd=%q, epochID=%q)"+
				" — check that the agent binary is installed and executable: %w",
			input.AgentCmd, input.EpochID, err,
		)
	}

	// Summarise results.
	result := &RunAgentSessionResult{
		EntriesRecorded: handler.EntriesRecorded(),
	}

	// Retrieve session stats for the summary. There may be zero or one session
	// when running a single agent (the ACP client supports multi-session but
	// most agents open one). We return the first session ID seen.
	if count := acpClient.SessionCount(); count > 0 {
		logger.Info("RunAgentSession: complete",
			slog.Int("sessions", count),
			slog.Int("entriesRecorded", result.EntriesRecorded),
		)
	}

	return result, nil
}
