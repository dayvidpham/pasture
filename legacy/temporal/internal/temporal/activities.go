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
//     Tracker.RecordEventReturningId + AttachContext to land both the
//     audit_events row and its context_edges(EpochContext, epochId) row;
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
// AgentId). May be nil when activities are constructed for tests that do not
// exercise attribution; the activity must check before calling into the cache.
type Activities struct {
	Trail           audit.Trail
	Tracker         protocol.TaskTracker
	HooksMgr        *hooks.Manager
	WellKnownAgents *tasks.WellKnownAgentCache
}

// ─── Epoch-ID validation (PROPOSAL-2 §7.12) ──────────────────────────────────

// validateEpochId parses epochId via provenance.ParseTaskID and returns a
// *pasterrors.StructuredError on failure. The error matches the §7.12 example
// shape verbatim: Category=CategoryValidation, What contains "not a valid
// Provenance TaskId", Why contains the underlying ParseTaskId error, Fix
// contains "pasture task create REQUEST".
//
// Used at three boundaries:
//
//   - cmd/pasture/epoch.go (CLI entry — rejects malformed --epoch-id
//     before any signal/workflow start);
//   - internal/handlers/epoch.go EpochStart (handler boundary — rejects
//     malformed epochId before c.ExecuteWorkflow is called);
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
func validateEpochId(epochId, caller string) error {
	if _, err := provenance.ParseTaskID(epochId); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The epoch ID %q is not valid.", epochId),
			Why: "Epoch IDs need the shape \"yourproject--01968a3c-...\" — a project name\n" +
				"followed by \"--\" and a UUID. The value you passed couldn't be split\n" +
				"into those two parts because the \"--\" separator was missing.",
			Where: fmt.Sprintf("Recording a workflow event (internal/temporal/activities.go in %s).", caller),
			Impact: "The epoch can't be started. Without a properly-formatted ID, the audit\n" +
				"log can't link events back to any task, which would leave a broken trail.",
			Fix: "1. Create a task first to get a valid ID:\n" +
				"     pasture task create REQUEST --type=feature \"<title>\"\n" +
				"2. Or find one that already exists:\n" +
				"     pasture task list --status=open --type=feature\n" +
				"3. Pass the returned ID as --epoch-id when starting the epoch.",
			Cause: err,
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
func (a *Activities) CheckConstraints(ctx context.Context, state protocol.EpochState, toPhase protocol.PhaseId) ([]ConstraintViolation, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("CheckConstraints", "from", state.CurrentPhase, "to", toPhase)

	// Reconstruct a temporary state machine to run ValidateAdvance.
	// We do not call Advance — only validation.
	sm := protocol.NewEpochStateMachineFromState(&state, nil)
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
			EpochId: state.EpochId,
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
				"epochId", state.EpochId,
			)
		}
	}

	return result, nil
}

// RecordTransition persists a transition record to the audit trail.
//
// Activity: non-deterministic I/O boundary. PROPOSAL-2 §7.11 wiring:
//
//  1. Validate epochId via provenance.ParseTaskID (§7.12 defense in depth — a
//     malformed ID that slipped past CLI/handler validation is rejected here
//     so no row leaks to audit_events / context_edges / tasks; Scenario 13).
//  2. Resolve the agent_id for the canonical "transition-gate/consensus"
//     well-known automaton (S7 cache lookup) so the recorded event is
//     attributed to a SoftwareAgent. The Role field on the AuditEvent is set
//     to the well-known agent's logical name; SqliteAuditTrail's
//     resolveLegacyRoleAgentId re-uses the SAME agents_software row that S7
//     registered (find-by-name short-circuits before any INSERT runs), so the
//     agent_id stamped on audit_events.agent_id matches the cache entry
//     verifying Scenario 8b.
//  3. Persist via Tracker.RecordEventReturningId — bundles the audit_events
//     INSERT and the LastInsertId recovery into a single connection round-trip.
//  4. Attach the epoch context via Tracker.AttachContext(eventId, ContextEpoch,
//     epochId) so the event is reachable via Timeline lookups by epoch
//     (PROPOSAL-2 §7.4 / §7.8 / Scenario 1).
//
// Tracker may be nil for tests that exercise only the legacy Trail path; in
// that case the activity falls back to the pre-S8 Trail.RecordEvent code with
// no AttachContext. Production wiring (cmd/pastured/main.go) always populates
// Tracker against the unified pasture.db so the §7.11 happy path runs.
//
// epochId is required to make audit events queryable by epoch. Pass the workflow
// input EpochId so events can be retrieved via QueryAuditEvents(epochId, ...).
func (a *Activities) RecordTransition(ctx context.Context, epochId string, record protocol.TransitionRecord) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordTransition",
		slog.String("epochId", epochId),
		slog.String("from", string(record.FromPhase)),
		slog.String("to", string(record.ToPhase)),
		slog.String("triggeredBy", record.TriggeredBy),
		slog.Bool("success", record.Success),
	)

	// §7.12 defence-in-depth: only enforce the TaskId shape when the unified
	// Tracker is wired (production path). Tests that exercise only the legacy
	// Trail use synthetic free-string epoch IDs (e.g. "epoch-test-trail") and
	// would otherwise fail; the validation belongs at the boundary where
	// context_edges rows would actually be written.
	if a.Tracker != nil {
		if err := validateEpochId(epochId, "Activities.RecordTransition"); err != nil {
			return err
		}
	}

	// Attribute the event to the canonical "transition-gate/consensus"
	// well-known automaton (PROPOSAL-2 §7.7.2 row 2). RecordTransition fires
	// at the transition boundary AFTER the state machine accepts the advance
	// — semantically the consensus gate has approved. If the cache is unset
	// (e.g. in-memory test backend), fall back to the legacy "automaton-checker"
	// role string so SqliteAuditTrail.resolveLegacyRoleAgentId still finds or
	// creates a SoftwareAgent without crashing.
	transitionRole := resolveAutomatonRoleString(a.WellKnownAgents, "pasture/automaton/transition-gate/consensus", "automaton-checker")

	event := protocol.AuditEvent{
		EpochId:   epochId,
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
		eventId, err := a.Tracker.RecordEventReturningId(ctx, event)
		if err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "The phase transition couldn't be saved to the audit log.",
				Why: fmt.Sprintf(
					"Writing the transition %q → %q (epoch %q, triggered by %q) to the\n"+
						"database failed.",
					record.FromPhase, record.ToPhase, epochId, record.TriggeredBy,
				),
				Where: "Recording a phase transition (internal/temporal/activities.go in Activities.RecordTransition).",
				Cause: err,
				Impact: "The workflow's transition history will diverge from the saved record,\n" +
					"and later queries on this epoch's timeline will be incomplete.",
				Fix: "1. Make sure the database is writable and at the latest schema version:\n" +
					"     pasture migrate\n" +
					"2. Check the daemon configuration so the audit database path is correct:\n" +
					"     pastured --config <your-config>\n" +
					"3. Retry the transition once the database is healthy.",
			}
		}
		if attachErr := a.Tracker.AttachContext(ctx, eventId, protocol.ContextEpoch, epochId); attachErr != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "The phase transition was saved but couldn't be linked to its epoch.",
				Why: fmt.Sprintf(
					"The audit event (id %d) was written, but linking it to epoch %q failed.",
					eventId, epochId,
				),
				Where: "Recording a phase transition (internal/temporal/activities.go in Activities.RecordTransition).",
				Impact: "The event is still in the database but won't show up when you ask for the\n" +
					"epoch's timeline, which leaves a hole in the recorded history.",
				Fix: "1. Repair the link by re-attaching the event to its epoch:\n" +
					fmt.Sprintf("     pasture task contexts attach %d EpochContext %q\n", eventId, epochId) +
					"2. If repair keeps failing, run a migration to make sure the schema is up\n" +
					"   to date:\n" +
					"     pasture migrate",
				Cause: attachErr,
			}
		}
	} else {
		// Legacy fallback (Trail-only path; tests that don't wire Tracker).
		if err := a.Trail.RecordEvent(ctx, event); err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "The phase transition couldn't be saved to the audit log.",
				Why: fmt.Sprintf(
					"Writing the transition %q → %q (epoch %q, triggered by %q) to the\n"+
						"database failed.",
					record.FromPhase, record.ToPhase, epochId, record.TriggeredBy,
				),
				Where: "Recording a phase transition (internal/temporal/activities.go in Activities.RecordTransition).",
				Cause: err,
				Impact: "The transition isn't in the audit log, so timeline queries for this\n" +
					"epoch will be missing this entry.",
				Fix: "1. Verify the audit database is reachable and at the latest schema:\n" +
					"     pasture migrate\n" +
					"2. Retry the transition once the database is healthy.",
			}
		}
	}

	// Best-effort: fire HookPhaseTransition after successful audit recording.
	// Errors are logged but do not fail the activity — hooks are optional.
	hookPayload := hooks.HookPayload{
		Event:   hooks.HookPhaseTransition,
		EpochId: epochId,
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
			"epochId", epochId,
		)
	}

	return nil
}

// RecordAuditEvent persists a generic AuditEvent to the trail.
//
// Activity: non-deterministic I/O boundary. Used for non-transition events
// (vote recorded, session registered, etc.). When Tracker is wired (production
// path), this method also attaches a context_edges(EpochContext, epochId) row
// keyed by event.EpochId so the event is reachable via Timeline lookups
// alongside the transition events recorded by RecordTransition.
//
// If event.EpochId is empty, the event is treated as free-floating: it lands
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
		// §7.12 defence-in-depth (only when an EpochId is present —
		// free-floating events with empty EpochId skip the validation).
		if event.EpochId != "" {
			if err := validateEpochId(event.EpochId, "Activities.RecordAuditEvent"); err != nil {
				return err
			}
		}
		eventId, err := a.Tracker.RecordEventReturningId(ctx, event)
		if err != nil {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryStorage,
				What:     "An audit event couldn't be saved to the database.",
				Why: fmt.Sprintf(
					"Writing the %q event (epoch %q, phase %q) to the database failed.",
					event.EventType, event.EpochId, event.Phase,
				),
				Where: "Recording an audit event (internal/temporal/activities.go in Activities.RecordAuditEvent).",
				Cause: err,
				Impact: "The event isn't in the audit log, so it won't show up in this epoch's\n" +
					"timeline or in event queries for this epoch.",
				Fix: "1. Make sure the database is writable and at the latest schema version:\n" +
					"     pasture migrate\n" +
					"2. Confirm the daemon is using the right database file:\n" +
					"     pastured --config <your-config>\n" +
					"3. Retry the operation that produced this event.",
			}
		}
		// Only attach EpochContext when an EpochId is set (workflow events).
		// Free-floating events (EpochId="") use the helpers in
		// internal/tasks/free_floating.go which attach Git/Skill/Session
		// contexts via the same Tracker.AttachContext API.
		if event.EpochId != "" {
			if attachErr := a.Tracker.AttachContext(ctx, eventId, protocol.ContextEpoch, event.EpochId); attachErr != nil {
				return &pasterrors.StructuredError{
					Category: pasterrors.CategoryStorage,
					What:     "The audit event was saved but couldn't be linked to its epoch.",
					Why: fmt.Sprintf(
						"The event (id %d) was written, but linking it to epoch %q failed.",
						eventId, event.EpochId,
					),
					Where: "Recording an audit event (internal/temporal/activities.go in Activities.RecordAuditEvent).",
					Impact: "The event is in the database but won't appear in this epoch's timeline,\n" +
						"which leaves a gap in the recorded history.",
					Fix: "1. Repair the link by re-attaching the event to its epoch:\n" +
						fmt.Sprintf("     pasture task contexts attach %d EpochContext %q\n", eventId, event.EpochId) +
						"2. If repair keeps failing, run a migration:\n" +
						"     pasture migrate",
					Cause: attachErr,
				}
			}
		}
		return nil
	}

	// Legacy fallback (Trail-only path; tests that don't wire Tracker).
	if err := a.Trail.RecordEvent(ctx, event); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "An audit event couldn't be saved to the database.",
			Why: fmt.Sprintf(
				"Writing the %q event (epoch %q, phase %q) to the database failed: %s",
				event.EventType, event.EpochId, event.Phase, err,
			),
			Impact: "The event isn't in the audit log, so it won't show up in event queries\n" +
				"for this epoch.",
			Fix: "1. Make sure the database is writable and at the latest schema version:\n" +
				"     pasture migrate\n" +
				"2. Retry the operation that produced this event.",
		}
	}
	return nil
}

// ─── Well-known agent role-string resolution ─────────────────────────────────

// resolveAutomatonRoleString returns the canonical role string for an audit
// event when the Activities was constructed with a WellKnownAgentCache (S7).
// When the cache is unset OR the well-known name is missing, the function
// falls back to legacyDefault — chosen so the SqliteAuditTrail's
// resolveLegacyRoleAgentId find-or-create path still mints a SoftwareAgent
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
//     this function returns the AgentId directly and the SHADOW row goes
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
//	epochId: Required — the epoch to query events for.
//	phase:   Optional phase filter (nil = all phases).
//	role:    Optional role filter (nil = all roles).
func (a *Activities) QueryAuditEvents(ctx context.Context, epochId string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	events, err := a.Trail.QueryEvents(ctx, epochId, phase, role)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "The audit log couldn't be read for that epoch.",
			Why: fmt.Sprintf(
				"Looking up events for epoch %q from the database failed.",
				epochId,
			),
			Where: "Reading audit events (internal/temporal/activities.go in Activities.QueryAuditEvents).",
			Impact: "Tools that show this epoch's history won't be able to display its events\n" +
				"until the read succeeds.",
			Fix: "1. Make sure the database is at the latest schema and is reachable:\n" +
				"     pasture migrate\n" +
				"2. Retry the query once the database is healthy.",
			Cause: err,
		}
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
	// EpochId is the Pasture epoch this session belongs to.
	// Used to correlate session entries with epoch audit events.
	EpochId string `json:"epochId"`
}

// RunAgentSessionResult summarises the outcome of a RunAgentSession activity call.
type RunAgentSessionResult struct {
	// EntriesRecorded is the total number of SessionEntry rows written to the audit trail.
	EntriesRecorded int `json:"entriesRecorded"`
	// SessionId is the ACP session identifier for the completed session.
	// Empty when the agent produced no session/update messages.
	SessionId string `json:"sessionId,omitempty"`
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
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "An agent session's entries couldn't be saved to the audit log.",
			Why: fmt.Sprintf(
				"Writing %d session entries to the database failed.",
				len(entries),
			),
			Where: "Recording agent session entries (internal/temporal/activities.go in Activities.RecordSessionEntries).",
			Impact: "The agent's session history is missing from the audit log, so you won't\n" +
				"be able to replay or inspect what happened in this session.",
			Fix: "1. Make sure the database is writable and at the latest schema:\n" +
				"     pasture migrate\n" +
				"2. Re-run the agent session once the database is healthy.",
			Cause: err,
		}
	}
	return nil
}

// QuerySessionEntries retrieves all session entries for the given sessionId.
//
// Activity: non-deterministic I/O boundary (reads from external store).
//
// Returns an empty (non-nil) slice when no entries exist for sessionId.
func (a *Activities) QuerySessionEntries(ctx context.Context, sessionId string) ([]protocol.SessionEntry, error) {
	entries, err := a.Trail.QuerySessionEntries(ctx, sessionId)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "An agent session's entries couldn't be read from the audit log.",
			Why: fmt.Sprintf(
				"Looking up entries for session %q from the database failed.",
				sessionId,
			),
			Where: "Reading agent session entries (internal/temporal/activities.go in Activities.QuerySessionEntries).",
			Impact: "Tools that show this session's history won't be able to display it until\n" +
				"the read succeeds.",
			Fix: "1. Make sure the database is at the latest schema and is reachable:\n" +
				"     pasture migrate\n" +
				"2. Retry the query once the database is healthy.",
			Cause: err,
		}
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
		slog.String("epochId", input.EpochId),
	)

	indexer := acp.NewSharedIndexer()
	handler := acp.NewIndexingSessionHandler(indexer, a.Trail, a.HooksMgr, input.EpochId)
	acpClient := acp.NewClient(handler)

	if err := acpClient.Connect(ctx, input.AgentCmd, input.AgentArgs...); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "The agent process couldn't be started.",
			Why: fmt.Sprintf(
				"Launching the %q agent for epoch %q failed.",
				input.AgentCmd, input.EpochId,
			),
			Where: "Running an agent session (internal/temporal/activities.go in Activities.RunAgentSession).",
			Impact: "No agent session can run for this epoch until the agent binary is\n" +
				"reachable, so the workflow step that needs the agent will not progress.",
			Fix: "1. Check that the agent binary is installed and on PATH:\n" +
				fmt.Sprintf("     which %s\n", input.AgentCmd) +
				"2. Confirm the binary is executable:\n" +
				fmt.Sprintf("     test -x \"$(command -v %s)\" && echo OK\n", input.AgentCmd) +
				"3. Re-run the workflow step once the agent is available.",
			Cause: err,
		}
	}

	// Summarise results. The IndexingSessionHandler records the last session ID
	// and stop reason on each HandleSessionEnd call; we surface these in the
	// result so callers can correlate the activity output with audit trail data.
	result := &RunAgentSessionResult{
		EntriesRecorded: handler.EntriesRecorded(),
		SessionId:       handler.LastSessionId(),
		StopReason:      string(handler.LastStopReason()),
	}

	logger.Info("RunAgentSession: complete",
		slog.Int("sessions", acpClient.SessionCount()),
		slog.Int("entriesRecorded", result.EntriesRecorded),
		slog.String("lastSessionId", result.SessionId),
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
	if _, err := a.HooksMgr.Dispatch(ctx, payload); err != nil {
		slog.Warn("hook dispatch failed",
			"what", fmt.Sprintf("hook dispatch failed for event %s", payload.Event),
			"why", err.Error(),
			"impact", "hook handlers for this event did not execute",
			"fix", "check handler registration and handler implementation",
			"event", string(payload.Event),
			"epochId", payload.EpochId,
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
	_, err := a.HooksMgr.Dispatch(ctx, payload)
	return err
}

// ─── Compile-time assertion ───────────────────────────────────────────────────

// Compile-time assertion: activity package is used for logger access.
var _ = activity.GetLogger
