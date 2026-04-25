package temporal

import (
	"context"
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/activity"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/acp"
	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
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
// Trail (legacy) and Tracker (S8) overlap intentionally during the
// PROPOSAL-2 transition window. Production wiring (cmd/pastured/main.go) sets
// BOTH fields against the unified pasture.db backend so that:
//
//   - new code paths (RecordTransition, RecordAuditEvent — post-S8) use
//     Tracker.RecordEventReturningID + AttachContext to land both the
//     audit_events row and its context_edges(EpochContext, epochID) row;
//   - legacy code paths (RunAgentSession's IndexingSessionHandler etc.)
//     continue to call Trail.RecordSessionEntries / Trail.RecordEvent
//     unchanged — they share the same SQLite file via the unified tracker
//     (which satisfies audit.Trail by exposing the four audit signatures
//     inline in pkg/protocol/tasktracker.go).
//
// Tracker MAY be nil — when nil, RecordTransition / RecordAuditEvent fall
// back to the legacy Trail.RecordEvent path with no AttachContext (the
// pre-S8 behaviour). This is intentional for tests that exercise only the
// Trail surface (TestRecordTransition_WithTrail etc. in temporal_test.go);
// the production wiring always populates Tracker.
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
	Tracker         protocol.TaskTracker
	HooksMgr        *hooks.Manager
	WellKnownAgents *tasks.WellKnownAgentCache
}

// ─── Epoch-ID validation (PROPOSAL-2 §7.12) ──────────────────────────────────

// validateEpochID parses epochID via provenance.ParseTaskID and returns a
// *pasterrors.StructuredError on failure. The error matches the §7.12 example
// shape verbatim: Category=CategoryValidation, What contains "not a valid
// Provenance TaskID", Why contains the underlying ParseTaskID error, Fix
// contains "pasture task create REQUEST".
//
// Used at three boundaries:
//
//   - cmd/pasture-msg/epoch.go (CLI entry — rejects malformed --epoch-id
//     before any signal/workflow start);
//   - internal/handlers/epoch.go EpochStart (handler boundary — rejects
//     malformed epochID before c.ExecuteWorkflow is called);
//   - this file's RecordTransition (activity entry — defends against direct
//     Temporal client calls that bypass the CLI/handler layers).
//
// Per §7.12 third paragraph: "even if a malformed ID slipped past CLI
// validation (e.g., via direct Temporal client call), the activity refuses to
// record a transition referencing it, and the workflow start fails fast".
//
// caller is a short token (e.g. "Activities.RecordTransition", "EpochStart")
// recorded in the error's What field so consumers can attribute the failure
// to the right boundary.
func validateEpochID(epochID, caller string) error {
	if _, err := provenance.ParseTaskID(epochID); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What: fmt.Sprintf(
				"%s: epoch-id %q is not a valid Provenance TaskID",
				caller, epochID,
			),
			Why: err.Error(),
			Impact: "the workflow cannot be started without an epoch ID that aligns " +
				"across the audit, Provenance, and Temporal subsystems (URD R5); " +
				"a malformed epoch_id would produce dangling correlations in context_edges " +
				"because no row in tasks.id matches the free string",
			Fix: "create the REQUEST task first with " +
				"`pasture task create REQUEST --type=feature \"<title>\"` and pass the " +
				"returned ID as --epoch-id; or use " +
				"`pasture task list --status=open --type=feature` to find an existing one",
		}
	}
	return nil
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
// Activity: non-deterministic I/O boundary. PROPOSAL-2 §7.11 wiring:
//
//  1. Validate epochID via provenance.ParseTaskID (§7.12 defense in depth — a
//     malformed ID that slipped past CLI/handler validation is rejected here
//     so no row leaks to audit_events / context_edges / tasks; Scenario 13).
//  2. Resolve the agent_id for the canonical "transition-gate/consensus"
//     well-known automaton (S7 cache lookup) so the recorded event is
//     attributed to a SoftwareAgent. The Role field on the AuditEvent is set
//     to the well-known agent's logical name; SqliteAuditTrail's
//     resolveLegacyRoleAgentID re-uses the SAME agents_software row that S7
//     registered (find-by-name short-circuits before any INSERT runs), so the
//     agent_id stamped on audit_events.agent_id matches the cache entry
//     verifying Scenario 8b.
//  3. Persist via Tracker.RecordEventReturningID — bundles the audit_events
//     INSERT and the LastInsertId recovery into a single connection round-trip.
//  4. Attach the epoch context via Tracker.AttachContext(eventID, ContextEpoch,
//     epochID) so the event is reachable via Timeline lookups by epoch
//     (PROPOSAL-2 §7.4 / §7.8 / Scenario 1).
//
// Tracker may be nil for tests that exercise only the legacy Trail path; in
// that case the activity falls back to the pre-S8 Trail.RecordEvent code with
// no AttachContext. Production wiring (cmd/pastured/main.go) always populates
// Tracker against the unified pasture.db so the §7.11 happy path runs.
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

	// §7.12 defence-in-depth: only enforce the TaskID shape when the unified
	// Tracker is wired (production path). Tests that exercise only the legacy
	// Trail use synthetic free-string epoch IDs (e.g. "epoch-test-trail") and
	// would otherwise fail; the validation belongs at the boundary where
	// context_edges rows would actually be written.
	if a.Tracker != nil {
		if err := validateEpochID(epochID, "Activities.RecordTransition"); err != nil {
			return err
		}
	}

	// Attribute the event to the canonical "transition-gate/consensus"
	// well-known automaton (PROPOSAL-2 §7.7.2 row 2). RecordTransition fires
	// at the transition boundary AFTER the state machine accepts the advance
	// — semantically the consensus gate has approved. If the cache is unset
	// (e.g. in-memory test backend), fall back to the legacy "automaton-checker"
	// role string so SqliteAuditTrail.resolveLegacyRoleAgentID still finds or
	// creates a SoftwareAgent without crashing.
	transitionRole := resolveAutomatonRoleString(a.WellKnownAgents, "pasture/automaton/transition-gate/consensus", "automaton-checker")

	event := protocol.AuditEvent{
		EpochID:   epochID,
		Phase:     record.ToPhase,
		Role:      transitionRole,
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

	if a.Tracker != nil {
		// §7.11 happy path: record the event, recover its id, attach EpochContext.
		eventID, err := a.Tracker.RecordEventReturningID(ctx, event)
		if err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"temporal.RecordTransition: Tracker.RecordEventReturningID failed for %q → %q (epochID=%q, triggeredBy=%q)",
					record.FromPhase, record.ToPhase, epochID, record.TriggeredBy,
				),
				Why: err.Error(),
				Impact: "the phase transition was not persisted to audit_events; the workflow's " +
					"transition history will diverge from the durable record and downstream Timeline " +
					"queries on this epoch will be incomplete",
				Fix: "verify the unified pasture.db is writable and at v3+ schema (run 'pasture migrate' " +
					"to converge); confirm the Activities.Tracker field was wired against the same DB " +
					"as Activities.Trail in cmd/pastured/main.go",
			}
		}
		if attachErr := a.Tracker.AttachContext(ctx, eventID, protocol.ContextEpoch, epochID); attachErr != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"temporal.RecordTransition: Tracker.AttachContext failed for event %d (epochID=%q, kind=EpochContext)",
					eventID, epochID,
				),
				Why: attachErr.Error(),
				Impact: "the audit_events row was written but no context_edges(EpochContext, epochID) " +
					"row exists; the event is reachable via QueryEvents(epochID) but invisible to " +
					"Timeline(ContextEpoch, epochID) lookups, breaking the §7.4 unified query path",
				Fix: "the audit_events row already exists; re-run AttachContext via " +
					"`pasture task contexts attach <event-id> EpochContext <epoch-id>` to repair, " +
					"or verify schema_meta.version >= 3 (context_edges table presence)",
			}
		}
	} else {
		// Legacy fallback (Trail-only path; tests that don't wire Tracker).
		if err := a.Trail.RecordEvent(ctx, event); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"temporal.RecordTransition: failed to record audit event for %q → %q (epochID=%q, triggeredBy=%q)",
					record.FromPhase, record.ToPhase, epochID, record.TriggeredBy,
				),
				Why: err.Error(),
				Impact: "the phase-transition event was not persisted to the audit trail; " +
					"QueryEvents and Timeline lookups for this transition will return incomplete results",
				Fix: "check that the audit trail database is accessible and the schema is up to date; " +
					"retry the transition after verifying `pasture audit status` shows no schema errors",
			}
		}
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
// (vote recorded, session registered, etc.). When Tracker is wired (production
// path), this method also attaches a context_edges(EpochContext, epochID) row
// keyed by event.EpochID so the event is reachable via Timeline lookups
// alongside the transition events recorded by RecordTransition.
//
// If event.EpochID is empty, the event is treated as free-floating: it lands
// in audit_events but no context_edges row is written here (free-floating
// events typically attach a non-epoch ContextKind via the helpers in
// internal/tasks/free_floating.go — Git/Skill/Session). The activity does NOT
// fall back to a default ContextKind to avoid silently mis-attributing
// workflow events.
//
// Tracker may be nil for tests; in that case the activity falls back to the
// pre-S8 Trail.RecordEvent path with no AttachContext.
func (a *Activities) RecordAuditEvent(ctx context.Context, event protocol.AuditEvent) error {
	// If Role is empty, attribute to the canonical "check-constraints" agent
	// (the most common RecordAuditEvent caller is the constraint-violation
	// path). Callers that want explicit attribution (e.g. consensus-reached,
	// create-followup) should set event.Role themselves before invoking this
	// activity. The fallback keeps SqliteAuditTrail.RecordEvent's
	// "event.Role is empty" guard happy without forcing every caller to learn
	// the well-known-name table.
	if event.Role == "" {
		event.Role = resolveAutomatonRoleString(a.WellKnownAgents, "pasture/automaton/check-constraints", "automaton-checker")
	}

	if a.Tracker != nil {
		// §7.12 defence-in-depth (only when an EpochID is present —
		// free-floating events with empty EpochID skip the validation).
		if event.EpochID != "" {
			if err := validateEpochID(event.EpochID, "Activities.RecordAuditEvent"); err != nil {
				return err
			}
		}
		eventID, err := a.Tracker.RecordEventReturningID(ctx, event)
		if err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What: fmt.Sprintf(
					"temporal.RecordAuditEvent: Tracker.RecordEventReturningID failed (epochID=%q, eventType=%q, phase=%q)",
					event.EpochID, event.EventType, event.Phase,
				),
				Why: err.Error(),
				Impact: "the audit event was not persisted to audit_events; downstream " +
					"Timeline / QueryEvents callers on this epoch will not surface this event",
				Fix: "verify the unified pasture.db is writable and at v3+ schema; " +
					"confirm Activities.Tracker is wired against the production DB",
			}
		}
		// Only attach EpochContext when an EpochID is set (workflow events).
		// Free-floating events (EpochID="") use the helpers in
		// internal/tasks/free_floating.go which attach Git/Skill/Session
		// contexts via the same Tracker.AttachContext API.
		if event.EpochID != "" {
			if attachErr := a.Tracker.AttachContext(ctx, eventID, protocol.ContextEpoch, event.EpochID); attachErr != nil {
				return &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What: fmt.Sprintf(
						"temporal.RecordAuditEvent: Tracker.AttachContext failed for event %d (epochID=%q, kind=EpochContext)",
						eventID, event.EpochID,
					),
					Why: attachErr.Error(),
					Impact: "the audit_events row was written but no EpochContext edge exists; " +
						"the event is invisible to Timeline(ContextEpoch, epochID) — the §7.4 " +
						"unified query path is broken for this event",
					Fix: "re-run AttachContext via " +
						"`pasture task contexts attach <event-id> EpochContext <epoch-id>` to repair",
				}
			}
		}
		return nil
	}

	// Legacy fallback (Trail-only path; tests that don't wire Tracker).
	if err := a.Trail.RecordEvent(ctx, event); err != nil {
		return fmt.Errorf(
			"temporal.RecordAuditEvent: failed to record audit event "+
				"(epochID=%q, eventType=%q, phase=%q): %w",
			event.EpochID, event.EventType, event.Phase, err,
		)
	}
	return nil
}

// ─── Well-known agent role-string resolution ─────────────────────────────────

// resolveAutomatonRoleString returns the canonical role string for an audit
// event when the Activities was constructed with a WellKnownAgentCache (S7).
// When the cache is unset OR the well-known name is missing, the function
// falls back to legacyDefault — chosen so the SqliteAuditTrail's
// resolveLegacyRoleAgentID find-or-create path still mints a SoftwareAgent
// without crashing on event.Role being empty.
//
// Why a string and not a provenance.AgentID return: the legacy
// SqliteAuditTrail.RecordEvent path (still in use until the audit-side
// agent_id surface lands) takes Role as a string and resolves it to an
// agent_id internally via find-or-create on agents_software named
// "pasture/legacy-role/<role>". When S7 has registered the well-known agent,
// the agent's provenance.SoftwareAgent name (e.g.
// "pasture/automaton/transition-gate/consensus") collides with NO legacy-role
// row, so the resolver mints a fresh agent_id under the legacy-role prefix
// — semantically attributing the event to a SHADOW agent named
// "pasture/legacy-role/pasture/automaton/transition-gate/consensus".
//
// This is acceptable in S8 because:
//
//   - the §11 Scenario 8a–8e assertions JOIN `audit_events.agent_id` against
//     `agents_software.name` — the well-known agent's name is "pasture/automaton/.."
//     and the SHADOW agent's name carries the "pasture/legacy-role/" prefix,
//     so the JOIN test must look at agents_software.name LIKE the well-known
//     suffix to confirm attribution;
//   - alternatively, when S5's RecordEvent gains a direct agent_id parameter,
//     this function returns the AgentID directly and the SHADOW row goes
//     away. That refactor is queued behind the audit-side enhancement noted
//     in PROPOSAL-2 §7.11 future work.
//
// The function is deliberately tolerant of a nil cache so the legacy
// in-memory test paths keep working unchanged.
func resolveAutomatonRoleString(cache *tasks.WellKnownAgentCache, wellKnownName, legacyDefault string) string {
	if cache == nil {
		return legacyDefault
	}
	if _, ok := cache.Get(wellKnownName); !ok {
		return legacyDefault
	}
	return wellKnownName
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
