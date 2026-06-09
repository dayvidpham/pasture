package engine

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

const (
	// activityNamespace is the namespace component of every engine-emitted
	// ActivityID and of the engine's stable software agent.
	activityNamespace = "pasture"

	// engineAgentName is the stable software-agent name the engine attributes
	// its phase-transition activities to. It is deliberately NOT one of the
	// well-known automaton agents, so adding it does not change the well-known
	// agent count or its registration tests.
	engineAgentName    = "pasture/automaton/epoch-engine"
	engineAgentVersion = "0"
	engineAgentSource  = "internal/engine"

	// ActivityKindPhaseTransition is the discriminator the engine passes to
	// protocol.DedupKey for a phase-transition activity. It is DELIBERATELY
	// distinct from the audit tier's event_type ("PhaseTransition"): an activity
	// is a different PROV-O entity (a unit of work owned by the engine's software
	// agent) than a system audit event, so they occupy independent id-spaces.
	// Both tiers use the SAME derivation mechanism (this one DedupKey encoder),
	// but the distinct kind makes the activity id differ from the audit dedup_key
	// for the same transition — id-equality across tiers would be a fragile
	// implicit join. Exactly-once still holds: each table is keyed on its own
	// kind. Exported so a cross-tier replay test derives the identical activity
	// id from the same const.
	ActivityKindPhaseTransition = "activity:phase-transition"
)

// recordActivity records exactly one PROV-O activity for a completed transition.
// The activity id is the deterministic UUIDv5 from the single pinned encoder
// protocol.DedupKey(epochID, toPhase, kind, stepSeq); a crash-replay of the
// emitting step re-derives the same id, so StartActivityWithID's
// ON CONFLICT(id) DO NOTHING collapses it to one row. Runs inside the durable
// step (composed into OnTransition), so it shares the step's replay semantics.
func (e *Engine) recordActivity(_ context.Context, epochId string, rec *protocol.TransitionRecord, stepSeq string) error {
	key := protocol.DedupKey(epochId, string(rec.ToPhase), ActivityKindPhaseTransition, stepSeq)
	u, err := uuid.Parse(key)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't build the activity id for epoch %q at phase %q.", epochId, rec.ToPhase),
			Why:      "The deduplication key was not a parseable UUID — this is an internal derivation bug.",
			Where:    "Recording a transition activity (internal/engine/activities.go in engine.recordActivity).",
			Impact:   "This transition's activity can't be recorded, so the durable step fails and retries.",
			Fix:      "Report this; the dedup-key derivation in pkg/protocol changed unexpectedly.",
			Cause:    err,
		}
	}
	id := provenance.ActivityID{Namespace: activityNamespace, UUID: u}

	if _, err := e.cfg.Tracker.StartActivityWithID(
		id,
		e.activityAgentID,
		provenancePhase(rec.ToPhase),
		provenance.StageInProgress,
		"phase transition to "+string(rec.ToPhase),
	); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't record the activity for epoch %q transition to %q.", epochId, rec.ToPhase),
			Why:      "The provenance tracker rejected the idempotent activity insert.",
			Where:    "Recording a transition activity (internal/engine/activities.go in engine.recordActivity).",
			Impact:   "The forensic activity row is missing for this transition; the durable step fails and retries.",
			Fix: "1. Confirm the engine's software agent exists (activities.agent_id is a NOT-NULL FK).\n" +
				"2. Confirm the provenance tables are healthy in pasture.db.",
			Cause: err,
		}
	}
	return nil
}

// provenancePhase maps a pasture PhaseId to a provenance Phase with an EXPLICIT
// pair-by-pair switch. The two enums describe the same 12-phase lifecycle but
// are independent types with some differing NAMES (e.g. pasture "plan-review"
// vs provenance "plan_uat", "impl-uat" vs "impl_uat"), so the mapping is spelled
// out rather than inferred from ordering — a future reordering of either enum
// then fails the lock test loudly instead of silently mis-mapping. PhaseComplete
// is terminal and has no provenance phase, so it maps to "unscoped".
func provenancePhase(pid protocol.PhaseId) provenance.Phase {
	switch pid {
	case protocol.PhaseRequest:
		return provenance.PhaseRequest
	case protocol.PhaseElicit:
		return provenance.PhaseElicit
	case protocol.PhasePropose:
		return provenance.PhasePropose
	case protocol.PhaseReview:
		return provenance.PhaseReview
	case protocol.PhasePlanReview:
		return provenance.PhasePlanUAT
	case protocol.PhaseRatify:
		return provenance.PhaseRatify
	case protocol.PhaseHandoff:
		return provenance.PhaseHandoff
	case protocol.PhaseImplPlan:
		return provenance.PhaseImplPlan
	case protocol.PhaseWorkerSlices:
		return provenance.PhaseWorkerSlices
	case protocol.PhaseCodeReview:
		return provenance.PhaseCodeReview
	case protocol.PhaseImplUAT:
		return provenance.PhaseImplUAT
	case protocol.PhaseLanding:
		return provenance.PhaseLanding
	case protocol.PhaseComplete:
		return provenance.PhaseUnscoped
	default:
		return provenance.PhaseUnscoped
	}
}

// resolveEngineAgentID find-or-creates the engine's stable software agent and
// returns its AgentID. The find reads agents_software through the shared modernc
// handle; the create goes through the provenance sink (which owns the agents
// tables). Resolved once at New() and cached so every deterministic activity
// insert references a present agent row.
func resolveEngineAgentID(db *sql.DB, sink ActivitySink) (provenance.AgentID, error) {
	if id, found, err := findEngineAgentID(db); err != nil {
		return provenance.AgentID{}, err
	} else if found {
		return id, nil
	}

	sa, err := sink.RegisterSoftwareAgent(activityNamespace, engineAgentName, engineAgentVersion, engineAgentSource)
	if err != nil {
		// A concurrent opener may have registered the agent between our find
		// and our create (agents_software.name is unique). Re-find before
		// surfacing the error.
		if id, found, ferr := findEngineAgentID(db); ferr == nil && found {
			return id, nil
		}
		return provenance.AgentID{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "Couldn't register the engine's forensic software agent.",
			Why:      "The provenance tracker rejected RegisterSoftwareAgent for the engine agent.",
			Where:    "Resolving the engine agent (internal/engine/activities.go in engine.resolveEngineAgentID).",
			Impact:   "Activities can't be attributed, so activity recording can't start.",
			Fix:      "Confirm the provenance tables in pasture.db are healthy and writable.",
			Cause:    err,
		}
	}
	return sa.ID, nil
}

// findEngineAgentID looks up the engine's software agent by name via the shared
// handle. Returns (id, true, nil) when present, (zero, false, nil) when absent,
// or (zero, false, err) on a query failure.
func findEngineAgentID(db *sql.DB) (provenance.AgentID, bool, error) {
	var idStr string
	err := db.QueryRow(
		`SELECT a.id FROM agents a JOIN agents_software s ON a.id = s.agent_id
		 WHERE s.name = ? LIMIT 1`,
		engineAgentName,
	).Scan(&idStr)
	switch {
	case err == sql.ErrNoRows:
		return provenance.AgentID{}, false, nil
	case err != nil:
		return provenance.AgentID{}, false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Couldn't look up the engine's forensic software agent.",
			Why:      "The query against the agent registry failed.",
			Where:    "Resolving the engine agent (internal/engine/activities.go in engine.findEngineAgentID).",
			Impact:   "Activity recording can't start until the engine agent resolves.",
			Fix:      "Confirm the agents and agents_software tables exist in pasture.db.",
			Cause:    err,
		}
	}
	id, perr := provenance.ParseAgentID(idStr)
	if perr != nil {
		return provenance.AgentID{}, false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The engine agent's stored id %q is malformed.", idStr),
			Why:      "The agent id in the registry could not be parsed.",
			Where:    "Resolving the engine agent (internal/engine/activities.go in engine.findEngineAgentID).",
			Impact:   "Activities can't be attributed to a valid agent.",
			Fix:      "Inspect the agents table; the id should be in 'namespace--uuid' form.",
			Cause:    perr,
		}
	}
	return id, true, nil
}
