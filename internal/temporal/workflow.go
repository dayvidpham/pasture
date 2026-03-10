// Package temporal implements the Temporal workflow layer for the Pasture epoch
// protocol. EpochWorkflow is the durable top-level workflow; it wraps
// EpochStateMachine with signal/query handlers and Temporal search attributes.
//
// Port of Python EpochWorkflow in scripts/aura_protocol/workflow.py.
package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	temporalsdk "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Search Attribute Keys (typed) ───────────────────────────────────────────

var (
	saEpochIDKey     = temporalsdk.NewSearchAttributeKeyString(SAEpochID)
	saPhaseKey       = temporalsdk.NewSearchAttributeKeyKeyword(SAPhase)
	saRoleKey        = temporalsdk.NewSearchAttributeKeyKeyword(SARole)
	saStatusKey      = temporalsdk.NewSearchAttributeKeyKeyword(SAStatus)
	saDomainKey      = temporalsdk.NewSearchAttributeKeyKeyword(SADomain)
	saLastEventKey   = temporalsdk.NewSearchAttributeKeyKeyword(SALastEventType)
)

// phaseDomain maps PhaseId to its protocol domain string.
var phaseDomain = map[protocol.PhaseId]string{
	protocol.PhaseRequest:      "user",
	protocol.PhaseElicit:       "user",
	protocol.PhasePropose:      "plan",
	protocol.PhaseReview:       "plan",
	protocol.PhasePlanReview:   "plan",
	protocol.PhaseRatify:       "plan",
	protocol.PhaseHandoff:      "impl",
	protocol.PhaseImplPlan:     "impl",
	protocol.PhaseWorkerSlices: "impl",
	protocol.PhaseCodeReview:   "impl",
	protocol.PhaseImplUAT:      "impl",
	protocol.PhaseLanding:      "impl",
	protocol.PhaseComplete:     "impl",
}

// ─── Workflow I/O types ───────────────────────────────────────────────────────

// EpochInput is the workflow input for EpochWorkflow.
type EpochInput struct {
	EpochID            string `json:"epochId"`
	RequestDescription string `json:"requestDescription"`
}

// EpochResult is the return value of EpochWorkflow when the epoch reaches COMPLETE.
type EpochResult struct {
	EpochID                    string           `json:"epochId"`
	FinalPhase                 protocol.PhaseId `json:"finalPhase"`
	TransitionCount            int              `json:"transitionCount"`
	SuccessfulTransitionCount  int              `json:"successfulTransitionCount"`
	ConstraintViolationsTotal  int              `json:"constraintViolationsTotal"`
}

// SliceInput is the workflow input for SliceWorkflow.
type SliceInput struct {
	EpochID          string `json:"epochId"`
	SliceID          string `json:"sliceId"`
	PhaseSpec        string `json:"phaseSpec"`         // human-readable; serializable future
	ParentWorkflowID string `json:"parentWorkflowId"`
}

// SliceResult is the return value of SliceWorkflow.
type SliceResult struct {
	SliceID string  `json:"sliceId"`
	Success bool    `json:"success"`
	Output  string  `json:"output,omitempty"`
	Error   *string `json:"error,omitempty"`
}

// ReviewInput is the workflow input for ReviewPhaseWorkflow.
type ReviewInput struct {
	EpochID string `json:"epochId"`
	PhaseID string `json:"phaseId"`
}

// ReviewResult is the return value of ReviewPhaseWorkflow.
type ReviewResult struct {
	PhaseID    string                        `json:"phaseId"`
	Success    bool                          `json:"success"`
	VoteResult map[types.ReviewAxis]types.VoteType `json:"voteResult"`
}

// ─── EpochWorkflow ────────────────────────────────────────────────────────────

// EpochWorkflow is the durable Temporal workflow that drives the 12-phase epoch
// lifecycle.  It wraps EpochStateMachine with Temporal signal/query handlers and
// updates search attributes on every phase transition for forensic queryability.
//
// Signals (4):
//   - advance_phase (PhaseAdvanceSignal) — request a phase transition
//   - submit_vote (ReviewVoteSignal)     — record a reviewer vote
//   - slice_progress (SliceProgressSignal) — receive progress from child SliceWorkflow
//   - register_session (RegisterSessionSignal) — register a Claude Code session
//
// Queries (5):
//   - current_state  → *types.EpochState
//   - available_transitions → []protocol.PhaseId
//   - full_state → *types.QueryStateResult
//   - slice_progress_state → []types.SliceProgressSignal
//   - active_sessions → []types.RegisterSessionSignal
//
// Design invariants:
//   - No time.Now() in workflow code — use workflow.Now().
//   - No I/O in workflow code — all I/O goes through activities.
//   - Signal handlers enqueue; transitions happen in the run loop.
//   - Search attributes updated via UpsertTypedSearchAttributes on every transition.
type EpochWorkflow struct {
	pendingAdvance  []types.PhaseAdvanceSignal
	pendingVotes    []types.ReviewVoteSignal
	totalViolations int
	sm              *EpochStateMachine
	sliceProgressLog []types.SliceProgressSignal
	activeSessions  []types.RegisterSessionSignal
}

// Run is the EpochWorkflow entry point. Initializes the state machine, sets
// initial search attributes, then drives the main signal loop until COMPLETE.
func (w *EpochWorkflow) Run(ctx workflow.Context, input EpochInput) (*EpochResult, error) {
	w.sm = NewEpochStateMachine(input.EpochID, nil)

	// Set initial search attributes (immutable SA_EPOCH_ID set once).
	initialPhase := w.sm.State().CurrentPhase
	domain := phaseDomain[initialPhase]
	if err := workflow.UpsertTypedSearchAttributes(ctx,
		saEpochIDKey.ValueSet(input.EpochID),
		saPhaseKey.ValueSet(string(initialPhase)),
		saRoleKey.ValueSet(string(w.sm.State().CurrentRole)),
		saStatusKey.ValueSet("running"),
		saDomainKey.ValueSet(domain),
	); err != nil {
		return nil, fmt.Errorf("EpochWorkflow: failed to set initial search attributes: %w", err)
	}

	// Main signal-driven loop.
	for w.sm.State().CurrentPhase != protocol.PhaseComplete {
		// Wait until there is work to do.
		_ = workflow.Await(ctx, func() bool {
			return len(w.pendingAdvance) > 0 || len(w.pendingVotes) > 0
		})
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// 1. Drain all pending votes.
		for len(w.pendingVotes) > 0 {
			vote := w.pendingVotes[0]
			w.pendingVotes = w.pendingVotes[1:]
			if err := w.sm.RecordVote(vote.Axis, vote.Vote); err != nil {
				workflow.GetLogger(ctx).Warn("EpochWorkflow: invalid vote signal ignored",
					"axis", vote.Axis, "error", err)
			}
		}

		// 2. Process the next advance signal.
		if len(w.pendingAdvance) == 0 {
			continue
		}
		sig := w.pendingAdvance[0]
		w.pendingAdvance = w.pendingAdvance[1:]

		// 2a. Check constraints (activity — non-deterministic).
		var violations []ConstraintViolation
		actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Second,
		})
		if err := workflow.ExecuteActivity(actCtx, CheckConstraints,
			*w.sm.State(), sig.ToPhase,
		).Get(actCtx, &violations); err != nil {
			workflow.GetLogger(ctx).Warn("EpochWorkflow: CheckConstraints activity failed", "error", err)
		}
		w.totalViolations += len(violations)

		// 2b. Advance state machine (pure, deterministic).
		record, err := w.sm.Advance(
			sig.ToPhase,
			sig.TriggeredBy,
			sig.ConditionMet,
			workflow.Now(ctx),
		)
		if err != nil {
			// Record a failed attempt via the state machine method (never mutate State() directly).
			w.sm.RecordFailedTransition(
				w.sm.State().CurrentPhase,
				sig.ToPhase,
				workflow.Now(ctx),
				sig.TriggeredBy,
				err,
			)
			continue
		}

		// 2c. Record transition (activity — I/O boundary).
		// Pass epochID so audit events are queryable by epoch.
		if actErr := workflow.ExecuteActivity(actCtx, RecordTransition, input.EpochID, *record).
			Get(actCtx, nil); actErr != nil {
			workflow.GetLogger(ctx).Warn("EpochWorkflow: RecordTransition activity failed", "error", actErr)
		}

		// 2d. Record audit event (activity — I/O boundary).
		auditEvent := protocol.AuditEvent{
			EpochID:   input.EpochID,
			Phase:     record.ToPhase,
			Role:      string(w.sm.State().CurrentRole),
			EventType: protocol.EventPhaseTransition,
			Payload: map[string]any{
				"from": string(record.FromPhase),
				"to":   string(record.ToPhase),
			},
			Timestamp: record.Timestamp,
		}
		if actErr := workflow.ExecuteActivity(actCtx, RecordAuditEvent, auditEvent).
			Get(actCtx, nil); actErr != nil {
			workflow.GetLogger(ctx).Warn("EpochWorkflow: RecordAuditEvent activity failed", "error", actErr)
		}

		// 2e. Upsert search attributes atomically with the transition.
		current := w.sm.State().CurrentPhase
		status := "running"
		if current == protocol.PhaseComplete {
			status = "complete"
		}
		if upsertErr := workflow.UpsertTypedSearchAttributes(ctx,
			saPhaseKey.ValueSet(string(current)),
			saRoleKey.ValueSet(string(w.sm.State().CurrentRole)),
			saStatusKey.ValueSet(status),
			saDomainKey.ValueSet(phaseDomain[current]),
			saLastEventKey.ValueSet(string(protocol.EventPhaseTransition)),
		); upsertErr != nil {
			workflow.GetLogger(ctx).Warn("EpochWorkflow: failed to upsert search attributes", "error", upsertErr)
		}
	}

	history := w.sm.State().TransitionHistory
	successful := 0
	for _, r := range history {
		if r.Success {
			successful++
		}
	}
	return &EpochResult{
		EpochID:                   input.EpochID,
		FinalPhase:                w.sm.State().CurrentPhase,
		TransitionCount:           len(history),
		SuccessfulTransitionCount: successful,
		ConstraintViolationsTotal: w.totalViolations,
	}, nil
}

// ── Signal handlers ─────────────────────────────────────────────────────────

// AdvancePhase is the signal handler for advance_phase signals.
func (w *EpochWorkflow) AdvancePhase(ctx workflow.Context, sig types.PhaseAdvanceSignal) {
	w.pendingAdvance = append(w.pendingAdvance, sig)
}

// SubmitVote is the signal handler for submit_vote signals.
func (w *EpochWorkflow) SubmitVote(ctx workflow.Context, sig types.ReviewVoteSignal) {
	w.pendingVotes = append(w.pendingVotes, sig)
}

// SliceProgress is the signal handler for slice_progress signals from child SliceWorkflows.
func (w *EpochWorkflow) SliceProgress(ctx workflow.Context, sig types.SliceProgressSignal) {
	w.sliceProgressLog = append(w.sliceProgressLog, sig)
}

// RegisterSession is the signal handler for register_session signals.
// Idempotent: duplicate session IDs are silently ignored.
func (w *EpochWorkflow) RegisterSession(ctx workflow.Context, sig types.RegisterSessionSignal) {
	for _, s := range w.activeSessions {
		if s.SessionID == sig.SessionID {
			return
		}
	}
	w.activeSessions = append(w.activeSessions, sig)
}

// ── Query handlers ──────────────────────────────────────────────────────────

// CurrentState returns a snapshot of the current epoch runtime state.
func (w *EpochWorkflow) CurrentState() (*types.EpochState, error) {
	if w.sm == nil {
		return nil, fmt.Errorf("EpochWorkflow: workflow not yet initialized — run() has not started")
	}
	return w.sm.State(), nil
}

// AvailableTransitionsQuery returns the list of currently available phase transitions.
func (w *EpochWorkflow) AvailableTransitionsQuery() []protocol.PhaseId {
	if w.sm == nil {
		return nil
	}
	return w.sm.AvailableTransitions()
}

// FullState returns a serialization-safe snapshot of epoch state for CLI consumers.
func (w *EpochWorkflow) FullState() (*types.QueryStateResult, error) {
	if w.sm == nil {
		return nil, fmt.Errorf("EpochWorkflow: workflow not yet initialized — run() has not started")
	}
	state := w.sm.State()
	var lastErr *string
	if state.LastError != nil {
		cp := *state.LastError
		lastErr = &cp
	}

	// Defensive copy of transition history.
	history := make([]types.TransitionRecord, len(state.TransitionHistory))
	copy(history, state.TransitionHistory)

	// Defensive copy of votes.
	votes := make(map[types.ReviewAxis]types.VoteType, len(state.ReviewVotes))
	for k, v := range state.ReviewVotes {
		votes[k] = v
	}

	return &types.QueryStateResult{
		CurrentPhase:         state.CurrentPhase,
		CurrentRole:          state.CurrentRole,
		TransitionHistory:    history,
		Votes:                votes,
		LastError:            lastErr,
		AvailableTransitions: w.sm.AvailableTransitions(),
		ActiveSessionCount:   len(w.activeSessions),
	}, nil
}

// SliceProgressState returns all accumulated slice progress signals.
func (w *EpochWorkflow) SliceProgressState() []types.SliceProgressSignal {
	cp := make([]types.SliceProgressSignal, len(w.sliceProgressLog))
	copy(cp, w.sliceProgressLog)
	return cp
}

// ActiveSessions returns all registered sessions for this epoch.
func (w *EpochWorkflow) ActiveSessions() []types.RegisterSessionSignal {
	cp := make([]types.RegisterSessionSignal, len(w.activeSessions))
	copy(cp, w.activeSessions)
	return cp
}

// ─── Temporal workflow registration ─────────────────────────────────────────

// EpochWorkflowFn is the top-level function registered with Temporal worker.
// It creates a fresh EpochWorkflow instance per execution (Temporal guarantee).
//
// Signal routing uses workflow.Go goroutines (one per signal channel) because
// the Go SDK does not provide SetSignalHandlerWithContext. Query registration
// uses workflow.SetQueryHandler which does not pass ctx to the handler.
//
// Exported so that tests can register and execute it directly via
// TestWorkflowEnvironment.RegisterWorkflow(temporal.EpochWorkflowFn).
func EpochWorkflowFn(ctx workflow.Context, input EpochInput) (*EpochResult, error) {
	w := &EpochWorkflow{}

	// Register signal handlers via goroutine-per-channel pattern.
	// Each goroutine drains its channel for the full lifecycle of the workflow.
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch := workflow.GetSignalChannel(ctx, SignalAdvancePhase)
		for {
			var sig types.PhaseAdvanceSignal
			ch.Receive(ctx, &sig)
			w.AdvancePhase(ctx, sig)
		}
	})
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch := workflow.GetSignalChannel(ctx, SignalSubmitVote)
		for {
			var sig types.ReviewVoteSignal
			ch.Receive(ctx, &sig)
			w.SubmitVote(ctx, sig)
		}
	})
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch := workflow.GetSignalChannel(ctx, SignalSliceProgress)
		for {
			var sig types.SliceProgressSignal
			ch.Receive(ctx, &sig)
			w.SliceProgress(ctx, sig)
		}
	})
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch := workflow.GetSignalChannel(ctx, SignalRegisterSession)
		for {
			var sig types.RegisterSessionSignal
			ch.Receive(ctx, &sig)
			w.RegisterSession(ctx, sig)
		}
	})

	// Register query handlers. SetQueryHandler does not pass ctx to the handler.
	if err := workflow.SetQueryHandler(ctx, QueryCurrentState,
		func() (*types.EpochState, error) {
			return w.CurrentState()
		}); err != nil {
		return nil, fmt.Errorf("EpochWorkflow: failed to register query %q: %w", QueryCurrentState, err)
	}
	if err := workflow.SetQueryHandler(ctx, QueryAvailableTransitions,
		func() ([]protocol.PhaseId, error) {
			return w.AvailableTransitionsQuery(), nil
		}); err != nil {
		return nil, fmt.Errorf("EpochWorkflow: failed to register query %q: %w", QueryAvailableTransitions, err)
	}
	if err := workflow.SetQueryHandler(ctx, QueryFullState,
		func() (*types.QueryStateResult, error) {
			return w.FullState()
		}); err != nil {
		return nil, fmt.Errorf("EpochWorkflow: failed to register query %q: %w", QueryFullState, err)
	}
	if err := workflow.SetQueryHandler(ctx, QuerySliceProgressState,
		func() ([]types.SliceProgressSignal, error) {
			return w.SliceProgressState(), nil
		}); err != nil {
		return nil, fmt.Errorf("EpochWorkflow: failed to register query %q: %w", QuerySliceProgressState, err)
	}
	if err := workflow.SetQueryHandler(ctx, QueryActiveSessions,
		func() ([]types.RegisterSessionSignal, error) {
			return w.ActiveSessions(), nil
		}); err != nil {
		return nil, fmt.Errorf("EpochWorkflow: failed to register query %q: %w", QueryActiveSessions, err)
	}

	return w.Run(ctx, input)
}

// RegisterWorkflows registers all Temporal workflows with the given registry.
// Call this in pastured worker setup before starting the worker.
func RegisterWorkflows(r interface {
	RegisterWorkflow(interface{})
	RegisterActivity(interface{})
}) {
	r.RegisterWorkflow(EpochWorkflowFn)
	r.RegisterWorkflow(SliceWorkflowFn)
	r.RegisterWorkflow(reviewWorkflowFn)
	r.RegisterActivity(CheckConstraints)
	r.RegisterActivity(RecordTransition)
	r.RegisterActivity(RecordAuditEvent)
	r.RegisterActivity(QueryAuditEvents)
}

// WorkflowName returns the Temporal workflow type name for a given function.
// Used by pasture-msg signal/query commands to address the workflow.
const (
	EpochWorkflowType  = "EpochWorkflowFn"
	SliceWorkflowType  = "SliceWorkflowFn"
	ReviewWorkflowType = "reviewWorkflowFn"
)

// ActivityInfo is a helper that returns activity registration info.
// Used to register activities with the Temporal worker.
var ActivityFunctions = []interface{}{
	CheckConstraints,
	RecordTransition,
	RecordAuditEvent,
	QueryAuditEvents,
}

// ─── Activity options helper ──────────────────────────────────────────────────

// defaultActivityOptions returns standard activity options used throughout
// EpochWorkflow.
func defaultActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporalsdk.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
}

// Compile-time assertion: activity functions satisfy the activity.Context signature.
var _ = activity.GetLogger // ensure activity package is used
