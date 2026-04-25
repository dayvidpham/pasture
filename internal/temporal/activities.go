package temporal

import (
	"context"
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/activity"

	"github.com/dayvidpham/pasture/internal/acp"
	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Activities struct ────────────────────────────────────────────────────────

// Activities bundles the dependencies required by all Pasture Temporal
// activities. Register an instance with the worker via RegisterWorkflows(w, acts).
//
// All exported methods on *Activities are registered as Temporal activities.
// Temporal uses the simple method name as the activity type (e.g. "CheckConstraints").
//
// Trail must not be nil. HooksMgr may be nil — all hook dispatch is best-effort
// and a nil manager is a no-op.
//
// WellKnownAgents (PROPOSAL-2 §7.7.3, S7) holds the in-memory cache of
// well-known automaton-name → provenance.AgentID minted at pastured startup.
// S8 activities consult this cache to attribute audit events to the correct
// SoftwareAgent (e.g. CheckConstraints uses the "pasture/automaton/check-constraints"
// AgentID). May be nil when activities are constructed for tests that do not
// exercise attribution; the activity must check before calling into the cache.
type Activities struct {
	Trail           audit.Trail
	HooksMgr        *hooks.Manager
	WellKnownAgents *tasks.WellKnownAgentCache
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

// ─── Activities methods ───────────────────────────────────────────────────────

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
func (a *Activities) CheckConstraints(ctx context.Context, state types.EpochState, toPhase protocol.PhaseId) ([]ConstraintViolation, error) {
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
		if err := a.dispatchHookInternal(ctx, hookPayload); err != nil {
			slog.Warn("hook dispatch failed",
				"what", fmt.Sprintf("hook dispatch failed for event %s", hookPayload.Event),
				"why", err.Error(),
				"impact", "hook handlers for this event did not execute",
				"fix", "check handler registration and handler implementation",
				"event", string(hooks.HookConstraintViolation),
				"epochId", state.EpochID,
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
func (a *Activities) RecordTransition(ctx context.Context, epochID string, record types.TransitionRecord) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordTransition",
		slog.String("epochID", epochID),
		slog.String("from", string(record.FromPhase)),
		slog.String("to", string(record.ToPhase)),
		slog.String("triggeredBy", record.TriggeredBy),
		slog.Bool("success", record.Success),
	)

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
	if err := a.Trail.RecordEvent(ctx, event); err != nil {
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
	if err := a.dispatchHookInternal(ctx, hookPayload); err != nil {
		slog.Warn("hook dispatch failed",
			"what", fmt.Sprintf("hook dispatch failed for event %s", hookPayload.Event),
			"why", err.Error(),
			"impact", "hook handlers for this event did not execute",
			"fix", "check handler registration and handler implementation",
			"event", string(hooks.HookPhaseTransition),
			"epochId", epochID,
		)
	}

	return nil
}

// RecordAuditEvent persists a generic AuditEvent to the trail.
//
// Activity: non-deterministic I/O boundary. Used for non-transition events
// (vote recorded, session registered, etc.).
func (a *Activities) RecordAuditEvent(ctx context.Context, event protocol.AuditEvent) error {
	if err := a.Trail.RecordEvent(ctx, event); err != nil {
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
func (a *Activities) QueryAuditEvents(ctx context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	events, err := a.Trail.QueryEvents(ctx, epochID, phase, role)
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
func (a *Activities) RecordSessionEntries(ctx context.Context, entries []protocol.SessionEntry) error {
	if err := a.Trail.RecordSessionEntries(ctx, entries); err != nil {
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
func (a *Activities) QuerySessionEntries(ctx context.Context, sessionID string) ([]protocol.SessionEntry, error) {
	entries, err := a.Trail.QuerySessionEntries(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf(
			"temporal.QuerySessionEntries: query failed for sessionID=%q: %w",
			sessionID, err,
		)
	}
	return entries, nil
}

// RunAgentSession starts an ACP-compatible agent process, streams its session
// updates, indexes them, and persists them to the audit trail on every update.
//
// Activity: long-running I/O boundary. Blocks until the agent process exits or
// ctx is cancelled. Session entries are written incrementally on each update
// via the IndexingSessionHandler (per-update flush mode).
func (a *Activities) RunAgentSession(ctx context.Context, input RunAgentSessionInput) (*RunAgentSessionResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RunAgentSession: starting",
		slog.String("agentCmd", input.AgentCmd),
		slog.String("epochID", input.EpochID),
	)

	indexer := acp.NewSharedIndexer()
	handler := acp.NewIndexingSessionHandler(indexer, a.Trail, a.HooksMgr, input.EpochID)
	acpClient := acp.NewClient(handler)

	if err := acpClient.Connect(ctx, input.AgentCmd, input.AgentArgs...); err != nil {
		return nil, fmt.Errorf(
			"temporal.RunAgentSession: ACP client failed"+
				" (agentCmd=%q, epochID=%q)"+
				" — check that the agent binary is installed and executable: %w",
			input.AgentCmd, input.EpochID, err,
		)
	}

	// Summarise results. The IndexingSessionHandler records the last session ID
	// and stop reason on each HandleSessionEnd call; we surface these in the
	// result so callers can correlate the activity output with audit trail data.
	result := &RunAgentSessionResult{
		EntriesRecorded: handler.EntriesRecorded(),
		SessionID:       handler.LastSessionID(),
		StopReason:      string(handler.LastStopReason()),
	}

	logger.Info("RunAgentSession: complete",
		slog.Int("sessions", acpClient.SessionCount()),
		slog.Int("entriesRecorded", result.EntriesRecorded),
		slog.String("lastSessionID", result.SessionID),
		slog.String("lastStopReason", result.StopReason),
	)

	return result, nil
}

// DispatchHook is a Temporal activity that dispatches a HookPayload to all
// registered handlers via the injected HooksMgr.
//
// Behaviour:
//   - If HooksMgr is nil, returns nil immediately. Hooks are optional — their
//     absence must not fail workflows.
//   - Otherwise, delegates to HooksMgr.Dispatch(ctx, payload) and returns any
//     combined handler errors so the caller can log them.
//
// Callers in activities and workflows should treat a non-nil return as a
// best-effort log signal, not as a hard failure.
func (a *Activities) DispatchHook(ctx context.Context, payload hooks.HookPayload) error {
	if a.HooksMgr == nil {
		return nil
	}
	if err := a.HooksMgr.Dispatch(ctx, payload); err != nil {
		slog.Warn("hook dispatch failed",
			"what", fmt.Sprintf("hook dispatch failed for event %s", payload.Event),
			"why", err.Error(),
			"impact", "hook handlers for this event did not execute",
			"fix", "check handler registration and handler implementation",
			"event", string(payload.Event),
			"epochId", payload.EpochID,
		)
		return err
	}
	return nil
}

// dispatchHookInternal dispatches a hook payload without the structured slog.Warn
// wrapper — it returns the raw error so callers can add their own context.
// Used internally by activity methods that want to add activity-specific fields.
func (a *Activities) dispatchHookInternal(ctx context.Context, payload hooks.HookPayload) error {
	if a.HooksMgr == nil {
		return nil
	}
	return a.HooksMgr.Dispatch(ctx, payload)
}

// ─── Compile-time assertion ───────────────────────────────────────────────────

// Compile-time assertion: activity package is used for logger access.
var _ = activity.GetLogger
