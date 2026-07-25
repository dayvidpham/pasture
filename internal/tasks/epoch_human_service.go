package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

const (
	decisionResultSlot       = provenance.ResultSlotID("decision")
	activityResultSlot       = provenance.ResultSlotID("activity")
	eventResultSlot          = provenance.ResultSlotID("event")
	preconditionEvidenceKind = provenance.EvidenceKind("pasture.epoch.precondition.v1")
)

// epochHumanService is the single journal-backed aggregate for explicit-human
// workflow decisions. Decisions and evidence are canonical facts; the one typed
// task event is a material reference to those facts and the real Activity row.
type epochHumanService struct {
	tracker   *trackerImpl
	policy    PolicySet
	authority provenance.JournalID
	barrier   EpochRaceBarrier
}

var _ EpochHumanService = (*epochHumanService)(nil)
var _ EpochHumanServiceFactory = (*trackerImpl)(nil)

func (t *trackerImpl) NewEpochHumanService(opts EpochServiceOptions) (EpochHumanService, error) {
	if _, err := t.systemSession(); err != nil {
		return nil, fmt.Errorf("construct epoch human service: establish journal authority: %w", err)
	}
	_, authority, found, err := readSystemIdentity(t.auditDB)
	if err != nil {
		return nil, fmt.Errorf("construct epoch human service: read journal authority: %w", err)
	}
	if !found {
		return nil, humanServiceErr("NewEpochHumanService", "the journal authority was not persisted", "the service cannot atomically authorize human decisions without its bootstrap authority", "reopen the tracker and retry construction")
	}
	policy, err := NewProductionPolicySet()
	if err != nil {
		return nil, fmt.Errorf("construct epoch human service: build decision policy: %w", err)
	}
	return &epochHumanService{tracker: t, policy: policy, authority: authority, barrier: opts.Synchronization.RaceBarrier}, nil
}

func (s *epochHumanService) SetInteractionMode(ctx context.Context, in SetInteractionModeInput) (DecisionResult, error) {
	epoch, err := s.preflightCommon(in.Meta, in.Epoch, in.Actor)
	if err != nil {
		return DecisionResult{}, err
	}
	if !in.Mode.valid() {
		return DecisionResult{}, humanServiceErr("SetInteractionMode", fmt.Sprintf("mode %q is unknown", in.Mode), "interaction mode must be normal or afk", "supply normal or afk")
	}
	if stored, found, err := s.committedDecision(in.Meta.OperationID, epoch, DecisionInteractionModeChanged); err != nil {
		return DecisionResult{}, err
	} else if found {
		var changed InteractionModeChanged
		if err := json.Unmarshal(stored.record.Decision.Payload, &changed); err != nil {
			return DecisionResult{}, humanServiceErr("SetInteractionMode", "the committed mode-decision payload is malformed", "an exact retry cannot reconstruct its original canonical decision", "repair the malformed decision fact before retrying")
		}
		if changed.To == in.Mode {
			draft, err := s.draftFromStored(stored.record.Decision)
			if err != nil {
				return DecisionResult{}, err
			}
			return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationSetInteractionMode, draft,
				[]evidenceRef{{Kind: "pasture.epoch.subject.v1", Value: epoch.String()}}, []provenance.Condition{currentDecisionCondition(epoch, DecisionInteractionModeChanged, 0)}, nil)
		}
	}
	state, err := s.interactionModeState(epoch)
	if err != nil {
		return DecisionResult{}, err
	}
	draft, err := s.policy.DraftModeChange(InteractionModeChanged{From: state.cursor.Mode, To: in.Mode})
	if err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationSetInteractionMode, draft,
		[]evidenceRef{{Kind: "pasture.epoch.subject.v1", Value: epoch.String()}}, []provenance.Condition{state.condition}, nil)
}

func (s *epochHumanService) ShowInteractionMode(_ context.Context, epoch EpochRootID) (InteractionModeCursor, error) {
	task, err := parseTaskID("ShowInteractionMode", string(epoch), "epoch")
	if err != nil {
		return InteractionModeCursor{}, err
	}
	if _, err := s.tracker.prov.Show(task); err != nil {
		return InteractionModeCursor{}, humanServiceErr("ShowInteractionMode", fmt.Sprintf("epoch %q could not be read", epoch), "the requested epoch task is missing or unreadable", "supply an existing epoch task and retry")
	}
	state, err := s.interactionModeState(task)
	if err != nil {
		return InteractionModeCursor{}, err
	}
	return state.cursor, nil
}

func (s *epochHumanService) RecordPlanUAT(ctx context.Context, in PlanUATInput) (DecisionResult, error) {
	epoch, err := s.preflightCommon(in.Meta, in.Epoch, in.Actor)
	if err != nil {
		return DecisionResult{}, err
	}
	if in.Proposal == (provenance.TaskID{}) {
		return DecisionResult{}, humanServiceErr("RecordPlanUAT", "the proposal is empty", "Plan UAT must target an existing proposal", "supply the proposal task")
	}
	if _, err := s.tracker.prov.Show(in.Proposal); err != nil {
		return DecisionResult{}, humanServiceErr("RecordPlanUAT", fmt.Sprintf("proposal %q could not be read", in.Proposal), "Plan UAT cannot target a missing proposal", "supply an existing proposal")
	}
	payload := PlanUATPayload{}
	if in.Payload != nil {
		payload = *in.Payload
	}
	if replay, found, err := s.planUATReplayDraft(in, payload, epoch); err != nil {
		return DecisionResult{}, err
	} else if found {
		conditions := []provenance.Condition(nil)
		if in.Outcome == PlanUATDeferredByAFK {
			conditions = []provenance.Condition{currentDecisionCondition(epoch, DecisionInteractionModeChanged, 0)}
		}
		return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRecordPlanUAT, replay,
			[]evidenceRef{{Kind: "pasture.plan.proposal.v1", Value: in.Proposal.String()}}, conditions, nil)
	}
	draft, conditions, err := s.planUATDraft(in, payload, epoch)
	if err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRecordPlanUAT, draft,
		[]evidenceRef{{Kind: "pasture.plan.proposal.v1", Value: in.Proposal.String()}}, conditions, nil)
}

func (s *epochHumanService) planUATDraft(in PlanUATInput, payload PlanUATPayload, epoch provenance.TaskID) (DecisionDraft, []provenance.Condition, error) {
	if !in.Outcome.valid() {
		return DecisionDraft{}, nil, humanServiceErr("RecordPlanUAT", fmt.Sprintf("outcome %q is unknown", in.Outcome), "Plan UAT must be accepted, changes_requested, or deferred_by_afk", "supply a known Plan UAT outcome")
	}
	if err := validateInteractions("PlanUATPayload.Interactions", payload.Interactions); err != nil {
		return DecisionDraft{}, nil, err
	}
	if err := validateFeedback("PlanUATPayload.Feedback", payload.Feedback); err != nil {
		return DecisionDraft{}, nil, err
	}
	if err := validateHeldQuestions("PlanUATPayload.HeldQuestions", payload.HeldQuestions); err != nil {
		return DecisionDraft{}, nil, err
	}
	if in.Outcome == PlanUATAccepted && hasFixNowFeedback(payload.Feedback) {
		return DecisionDraft{}, nil, humanServiceErr("RecordPlanUAT", "accepted Plan UAT contains FIX-NOW feedback", "blocking feedback requires changes_requested", "change the outcome or remove resolved blocking feedback")
	}
	snapshot := servicePlanSnapshot(in, epoch)
	switch in.Outcome {
	case PlanUATAccepted:
		draft, err := s.policy.planAccepted.Draft(PlanAccepted{Snapshot: snapshot, Interactions: payload.Interactions, Feedback: payload.Feedback})
		return draft, nil, err
	case PlanUATChangesRequested:
		draft, err := s.policy.planChanges.Draft(PlanChangesRequested{Snapshot: snapshot, Interactions: payload.Interactions, Feedback: payload.Feedback})
		return draft, nil, err
	case PlanUATDeferredByAFK:
		state, err := s.interactionModeState(epoch)
		if err != nil {
			return DecisionDraft{}, nil, err
		}
		if err := EvaluatePlanDeferral(PlanDeferralInput{Mode: state.cursor, HeldQuestions: payload.HeldQuestions, Feedback: payload.Feedback, Snapshot: snapshot}); err != nil {
			return DecisionDraft{}, nil, err
		}
		draft, err := s.policy.planDeferred.Draft(PlanDeferredByAFK{Snapshot: snapshot, Interactions: payload.Interactions, Feedback: payload.Feedback, HeldQuestions: payload.HeldQuestions, ModeEntry: *state.cursor.Entry})
		return draft, []provenance.Condition{state.condition}, err
	default:
		return DecisionDraft{}, nil, humanServiceErr("RecordPlanUAT", fmt.Sprintf("outcome %q is not handled", in.Outcome), "every Plan UAT outcome must lower to a catalog-issued decision", "supply a known Plan UAT outcome")
	}
}

func servicePlanSnapshot(in PlanUATInput, epoch provenance.TaskID) PlanUATSnapshot {
	id := decisionIDForOperation(in.Meta.OperationID)
	return PlanUATSnapshot{ID: PlanUATDecisionID(id), UATTaskID: in.Proposal, Proposal: DocumentRevisionID(in.Proposal.String()), DecisionEntry: id, InputLedger: DocumentRevisionID(epoch.String()), OutputLedger: DocumentRevisionID(string(in.Meta.OperationID))}
}

func (s *epochHumanService) RatifyPlan(ctx context.Context, in RatifyPlanInput) (DecisionResult, error) {
	epoch, err := s.preflightCommon(in.Meta, in.Epoch, in.Actor)
	if err != nil {
		return DecisionResult{}, err
	}
	if _, err := s.tracker.prov.Show(in.Proposal); err != nil {
		return DecisionResult{}, humanServiceErr("RatifyPlan", fmt.Sprintf("proposal %q could not be read", in.Proposal), "ratification must target an existing proposal", "supply an existing proposal")
	}
	draft, err := s.policy.DraftPlanRatified(PlanRatified{Proposal: in.Proposal.String(), ReviewRound: in.ReviewRound, PlanUAT: in.PlanUAT})
	if err != nil {
		return DecisionResult{}, err
	}
	if stored, found, err := s.committedDecision(in.Meta.OperationID, in.Proposal, DecisionPlanRatified); err != nil {
		return DecisionResult{}, err
	} else if found && sameDecisionEncoding(stored.record.Decision, draft.encoding()) {
		replay, err := s.draftFromStored(stored.record.Decision)
		if err != nil {
			return DecisionResult{}, err
		}
		return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRatifyPlan, replay,
			[]evidenceRef{{Kind: "pasture.review.round.v1", Value: string(in.ReviewRound)}, {Kind: "pasture.plan-uat.decision.v1", Value: string(in.PlanUAT)}},
			[]provenance.Condition{currentDecisionCondition(in.Proposal, DecisionPlanUATAccepted, 0)}, []provenance.Effect{lifecycleEffect(in.Proposal, provenance.EventKindTaskClosed)})
	}
	if err := s.requireAcceptedReview(in.Proposal, epoch, in.ReviewRound); err != nil {
		return DecisionResult{}, err
	}
	planUATCondition, err := s.requireAcceptedDecision(in.Proposal, epoch, in.PlanUAT, DecisionPlanUATAccepted, "RatifyPlan")
	if err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRatifyPlan, draft,
		[]evidenceRef{{Kind: "pasture.review.round.v1", Value: string(in.ReviewRound)}, {Kind: "pasture.plan-uat.decision.v1", Value: string(in.PlanUAT)}},
		[]provenance.Condition{planUATCondition}, []provenance.Effect{lifecycleEffect(in.Proposal, provenance.EventKindTaskClosed)})
}

func (s *epochHumanService) RecordImplementationUAT(ctx context.Context, in ImplementationUATInput) (DecisionResult, error) {
	candidate, err := parseTaskID("RecordImplementationUAT", string(in.Candidate), "candidate")
	if err != nil {
		return DecisionResult{}, err
	}
	epoch, err := s.preflightCommon(in.Meta, in.Epoch, in.Actor)
	if err != nil {
		return DecisionResult{}, err
	}
	if _, err := s.tracker.prov.Show(candidate); err != nil {
		return DecisionResult{}, humanServiceErr("RecordImplementationUAT", fmt.Sprintf("candidate %q could not be read", in.Candidate), "Implementation UAT cannot target a missing candidate", "supply an existing integration candidate")
	}
	payload := ImplUATPayload{}
	if in.Payload != nil {
		payload = *in.Payload
	}
	draft, err := s.policy.DraftImplementationUAT(in.Outcome, payload)
	if err != nil {
		return DecisionResult{}, err
	}
	if stored, found, err := s.committedDecision(in.Meta.OperationID, candidate, DecisionImplementationUAT); err != nil {
		return DecisionResult{}, err
	} else if found && sameDecisionEncoding(stored.record.Decision, draft.encoding()) {
		replay, err := s.draftFromStored(stored.record.Decision)
		if err != nil {
			return DecisionResult{}, err
		}
		return s.commit(ctx, in.Meta, in.Epoch, epoch, candidate, in.Actor, MutationRecordImplementationUAT, replay,
			[]evidenceRef{{Kind: "pasture.integration.candidate.v1", Value: string(in.Candidate)}}, nil, nil)
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, candidate, in.Actor, MutationRecordImplementationUAT, draft,
		[]evidenceRef{{Kind: "pasture.integration.candidate.v1", Value: string(in.Candidate)}}, nil, nil)
}

func (s *epochHumanService) Land(ctx context.Context, in LandInput) (DecisionResult, error) {
	candidate, err := parseTaskID("Land", string(in.Candidate), "candidate")
	if err != nil {
		return DecisionResult{}, err
	}
	epoch, err := s.preflightCommon(in.Meta, in.Epoch, in.Actor)
	if err != nil {
		return DecisionResult{}, err
	}
	if _, err := s.tracker.prov.Show(candidate); err != nil {
		return DecisionResult{}, humanServiceErr("Land", fmt.Sprintf("candidate %q could not be read", in.Candidate), "landing must target an existing integration candidate", "supply an existing integration candidate")
	}
	draft, err := s.policy.DraftLanded(EpochLanded{Candidate: in.Candidate, ImplementationUAT: in.ImplementationUAT})
	if err != nil {
		return DecisionResult{}, err
	}
	if stored, found, err := s.committedDecision(in.Meta.OperationID, epoch, DecisionLanded); err != nil {
		return DecisionResult{}, err
	} else if found && sameDecisionEncoding(stored.record.Decision, draft.encoding()) {
		replay, err := s.draftFromStored(stored.record.Decision)
		if err != nil {
			return DecisionResult{}, err
		}
		return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationLand, replay,
			[]evidenceRef{{Kind: "pasture.implementation-uat.decision.v1", Value: string(in.ImplementationUAT)}, {Kind: "pasture.integration.candidate.v1", Value: string(in.Candidate)}},
			[]provenance.Condition{currentDecisionCondition(candidate, DecisionImplementationUAT, 0)}, []provenance.Effect{lifecycleEffect(epoch, provenance.EventKindTaskClosed)})
	}
	implementationUATCondition, err := s.requireAcceptedImplementationUAT(candidate, epoch, in.Candidate, in.ImplementationUAT)
	if err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationLand, draft,
		[]evidenceRef{{Kind: "pasture.implementation-uat.decision.v1", Value: string(in.ImplementationUAT)}, {Kind: "pasture.integration.candidate.v1", Value: string(in.Candidate)}},
		[]provenance.Condition{implementationUATCondition}, []provenance.Effect{lifecycleEffect(epoch, provenance.EventKindTaskClosed)})
}

type evidenceRef struct {
	Kind  provenance.EvidenceKind `json:"kind"`
	Value string                  `json:"value"`
}

// conditionSnapshot is canonical evidence for the preconditions selected by a
// command. It is internal operation data, never a caller-supplied revision token.
// On an exact retry it restores the original operation input before direct Apply.
type conditionSnapshot struct {
	Kind              provenance.ConditionKind `json:"kind"`
	Task              string                   `json:"task"`
	DecisionKind      provenance.DecisionKind  `json:"decisionKind"`
	AssertedJournalID provenance.JournalID     `json:"assertedJournalId"`
}

type conditionEvidence struct {
	Conditions []conditionSnapshot `json:"conditions"`
}

func (s *epochHumanService) commit(ctx context.Context, meta CommandMeta, epochID EpochRootID, epoch, subject provenance.TaskID, actor AssertedHumanActor, mutation EpochMutationKind, draft DecisionDraft, refs []evidenceRef, conditions []provenance.Condition, trailing []provenance.Effect) (DecisionResult, error) {
	conditions, err := s.restoreCommittedConditions(meta.OperationID, conditions)
	if err != nil {
		return DecisionResult{}, err
	}
	conditionPayload, err := encodeConditionEvidence(conditions)
	if err != nil {
		return DecisionResult{}, err
	}
	decisionID := decisionIDForOperation(meta.OperationID)
	activityID := provenance.ActivityID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("activity:"+string(meta.OperationID)))}
	envelope, err := canonicalJSON(struct {
		ID       DecisionLedgerEntryID `json:"id"`
		Epoch    string                `json:"epoch"`
		Subject  string                `json:"subject"`
		Actor    string                `json:"actor"`
		Decision DecisionEncoding      `json:"decision"`
	}{decisionID, epoch.String(), subject.String(), actor.ActorID.String(), draft.encoding()})
	if err != nil {
		return DecisionResult{}, err
	}
	effects := []provenance.Effect{{Sort: provenance.EffectDecision, ResultSlot: decisionResultSlot, TaskID: subject, DecisionKind: journalDecisionKind(draft.encoding().Kind), Payload: envelope}}
	for i, ref := range refs {
		payload, err := canonicalJSON(struct{ Epoch, Subject, Decision, Reference string }{epoch.String(), subject.String(), string(decisionID), ref.Value})
		if err != nil {
			return DecisionResult{}, err
		}
		digest := sha256.Sum256(payload)
		effects = append(effects, provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID(fmt.Sprintf("evidence-%d", i)), TaskID: subject, EvidenceKind: ref.Kind, ContentDigest: digest[:], Payload: payload})
	}
	conditionDigest := sha256.Sum256(conditionPayload)
	effects = append(effects, provenance.Effect{Sort: provenance.EffectEvidence, TaskID: subject, EvidenceKind: preconditionEvidenceKind, ContentDigest: conditionDigest[:], Payload: conditionPayload})
	phase, notes := humanDecisionActivityMetadata(mutation, draft.encoding().Kind)
	effects = append(effects, provenance.Effect{Sort: provenance.EffectActivityCreate, ResultSlot: activityResultSlot, ActivityID: activityID, ActivityAgentID: actor.ActorID, ActivityPhase: phase, ActivityStage: provenance.StageComplete, ActivityNotes: notes})
	event, err := MapMaterialEvent(EpochDecisionRecordedEvent{Subject: subject, Epoch: epoch, Activity: activityID, Actor: actor.ActorID, Decision: decisionID, Kind: draft.encoding().Kind})
	if err != nil {
		return DecisionResult{}, err
	}
	event.ResultSlot = eventResultSlot
	effects = append(effects, event)
	effects = append(effects, trailing...)
	command, err := canonicalJSON(struct {
		Epoch    string           `json:"epoch"`
		Subject  string           `json:"subject"`
		Actor    string           `json:"actor"`
		Decision DecisionEncoding `json:"decision"`
		Evidence []evidenceRef    `json:"evidence"`
	}{string(epochID), subject.String(), actor.ActorID.String(), draft.encoding(), refs})
	if err != nil {
		return DecisionResult{}, err
	}
	if s.barrier != nil {
		if err := s.barrier.AfterPreflight(ctx, mutation); err != nil {
			return DecisionResult{}, humanServiceErr("commit", "the operation was cancelled at the post-preflight barrier", "the injected synchronization boundary rejected the operation before Apply", "retry after the synchronization condition is resolved")
		}
	}
	result, err := s.tracker.prov.Journal().Apply(provenance.OperationInput{OperationID: meta.OperationID, ActorID: actor.ActorID, AuthorityJournalID: &s.authority, CommandDigest: command, RecordedAt: time.Now().UTC().UnixNano(), Conditions: conditions, Effects: effects})
	if err != nil {
		return DecisionResult{}, fmt.Errorf("epoch human decision %q failed during its single atomic Apply; no partial effects committed: %w", draft.encoding().Kind, err)
	}
	return decisionResult(meta.OperationID, epochID, subject, actor.ActorID, decisionID, activityID, result)
}

func humanDecisionActivityMetadata(mutation EpochMutationKind, kind DecisionKindID) (provenance.Phase, string) {
	switch mutation {
	case MutationSetInteractionMode:
		return provenance.PhaseRequest, "explicit interaction-mode decision"
	case MutationRecordPlanUAT:
		return provenance.PhasePlanUAT, "explicit Plan UAT decision"
	case MutationRatifyPlan:
		return provenance.PhaseRatify, "explicit plan-ratification decision"
	case MutationRecordImplementationUAT:
		return provenance.PhaseImplUAT, "explicit Implementation UAT decision"
	case MutationLand:
		return provenance.PhaseLanding, "explicit landing decision"
	default:
		return provenance.PhaseUnscoped, "explicit human decision: " + string(kind)
	}
}

func decisionResult(op provenance.OperationID, epoch EpochRootID, subject provenance.TaskID, actor provenance.ActorID, decision DecisionLedgerEntryID, expectedActivity provenance.ActivityID, result provenance.CommittedResult) (DecisionResult, error) {
	var decisionFound, eventFound bool
	var activity provenance.ActivityID
	for _, slot := range result.ResultSlots {
		switch slot.Slot {
		case decisionResultSlot:
			decisionFound = slot.Kind == provenance.JournalKindDecision && slot.TaskID == nil && slot.ActivityID == nil
		case activityResultSlot:
			if slot.Kind == provenance.JournalKindActivity && slot.ActivityID != nil {
				activity = *slot.ActivityID
			}
		case eventResultSlot:
			eventFound = slot.Kind == provenance.JournalKindTaskEvent && slot.TaskID != nil && *slot.TaskID == subject && slot.ActivityID == nil
		}
	}
	if !decisionFound || !eventFound || activity == (provenance.ActivityID{}) || activity != expectedActivity {
		return DecisionResult{}, humanServiceErr("decisionResult", "the committed operation omitted a canonical decision, activity, or lifecycle-event result binding", "the human-decision result cannot be reconstructed safely without its journal-owned bindings", "verify journal integrity and retry with the same operation identity")
	}
	events := append([]provenance.JournalID(nil), result.EmittedEvents...)
	return DecisionResult{CommandResult: CommandResult{OperationID: op, Replayed: result.ShortCircuited, Epoch: epoch, ActivityID: activity, EventIDs: events}, DecisionID: decision, ActorID: actor}, nil
}

func (s *epochHumanService) preflightCommon(meta CommandMeta, epochID EpochRootID, actor AssertedHumanActor) (provenance.TaskID, error) {
	if meta.OperationID == "" {
		return provenance.TaskID{}, humanServiceErr("preflight", "the internal operation identity is empty", "idempotent mutation requires a stable non-empty identity", "mint an operation identity before invoking the service")
	}
	epoch, err := parseTaskID("preflight", string(epochID), "epoch")
	if err != nil {
		return provenance.TaskID{}, err
	}
	if _, err := s.tracker.prov.Show(epoch); err != nil {
		return provenance.TaskID{}, humanServiceErr("preflight", fmt.Sprintf("epoch %q could not be read", epochID), "the user gate must belong to an existing epoch task", "supply an existing epoch task")
	}
	if actor.ActorID == (provenance.ActorID{}) {
		return provenance.TaskID{}, humanServiceErr("preflight", "no human actor was supplied", "every user gate requires an explicitly selected registered human", "supply a registered human actor")
	}
	agent, err := s.tracker.prov.Agent(actor.ActorID)
	if err != nil {
		return provenance.TaskID{}, humanServiceErr("preflight", fmt.Sprintf("actor %q is not registered", actor.ActorID), "asserted attribution must resolve to a real registered agent", "register the human actor and retry")
	}
	if agent.Kind != provenance.AgentKindHuman {
		return provenance.TaskID{}, humanServiceErr("preflight", fmt.Sprintf("actor %q is registered as %s, not human", actor.ActorID, agent.Kind), "ML, software, and reserved system actors cannot make human decisions", "select a registered human actor")
	}
	return epoch, nil
}

func parseTaskID(where, raw, label string) (provenance.TaskID, error) {
	id, err := provenance.ParseTaskID(raw)
	if err != nil {
		return provenance.TaskID{}, humanServiceErr(where, fmt.Sprintf("%s id %q is malformed", label, raw), "journal-backed epoch operations require an existing Provenance task identity", fmt.Sprintf("supply %s as namespace--uuid", label))
	}
	return id, nil
}

func journalDecisionKind(kind DecisionKindID) provenance.DecisionKind {
	return provenance.DecisionKind(strings.ReplaceAll(string(kind), "/", "."))
}

func decisionIDForOperation(op provenance.OperationID) DecisionLedgerEntryID {
	return DecisionLedgerEntryID("decision:" + string(op))
}

type persistedDecision struct {
	ID       DecisionLedgerEntryID `json:"id"`
	Epoch    string                `json:"epoch"`
	Subject  string                `json:"subject"`
	Actor    string                `json:"actor"`
	Decision DecisionEncoding      `json:"decision"`
}

type decisionFact struct {
	row    provenance.DecisionRow
	record persistedDecision
}

func (s *epochHumanService) decisionFacts(subject provenance.TaskID, kind DecisionKindID) ([]decisionFact, error) {
	query := provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, Kinds: []provenance.DecisionKind{journalDecisionKind(kind)}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var out []decisionFact
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryDecisions(query)
		if err != nil {
			return nil, fmt.Errorf("query bounded decision facts for task %q: %w", subject, err)
		}
		for _, row := range page.Rows {
			fact, err := s.decodeDecisionFact(row, subject, kind)
			if err != nil {
				return nil, err
			}
			out = append(out, fact)
		}
		if page.Next == nil {
			return out, nil
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
}

// committedDecision returns the canonical decision fact already produced by one
// operation. It is bounded by the Journal Facts API and is used only to rebuild the
// exact internal Apply input for an idempotent retry; normal state reconstruction never
// uses operation lookup as an authority source.
func (s *epochHumanService) committedDecision(op provenance.OperationID, subject provenance.TaskID, kind DecisionKindID) (decisionFact, bool, error) {
	query := provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}, OperationIDs: []provenance.OperationID{op}}, Kinds: []provenance.DecisionKind{journalDecisionKind(kind)}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var matches []decisionFact
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryDecisions(query)
		if err != nil {
			return decisionFact{}, false, fmt.Errorf("query bounded committed decision for operation %q: %w", op, err)
		}
		for _, row := range page.Rows {
			fact, err := s.decodeDecisionFact(row, subject, kind)
			if err != nil {
				return decisionFact{}, false, err
			}
			matches = append(matches, fact)
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if len(matches) == 0 {
		return decisionFact{}, false, nil
	}
	if len(matches) != 1 {
		return decisionFact{}, false, humanServiceErr("committedDecision", fmt.Sprintf("operation %q has %d canonical decision facts", op, len(matches)), "one human-decision operation must produce exactly one decision fact", "repair the duplicate decision facts before retrying")
	}
	return matches[0], true, nil
}

func (s *epochHumanService) draftFromStored(encoding DecisionEncoding) (DecisionDraft, error) {
	if err := s.policy.Catalog.ValidateStored(encoding); err != nil {
		return DecisionDraft{}, fmt.Errorf("reconstruct catalog-issued decision draft %q: %w", encoding.Kind, err)
	}
	binding, found := s.policy.Catalog.byKind[encoding.Kind]
	if !found {
		return DecisionDraft{}, humanServiceErr("draftFromStored", fmt.Sprintf("stored decision kind %q is absent from the production catalog", encoding.Kind), "an exact retry must use a current catalog-issued decision", "restore the matching production decision descriptor before retrying")
	}
	return DecisionDraft{kind: encoding.Kind, codec: encoding.Codec, schema: encoding.Schema, token: binding.token(), payload: copyPayload(encoding.Payload)}, nil
}

func sameDecisionEncoding(left, right DecisionEncoding) bool {
	return left.Kind == right.Kind && left.Codec == right.Codec && left.Schema == right.Schema && bytes.Equal(left.Payload, right.Payload)
}

func (s *epochHumanService) planUATReplayDraft(in PlanUATInput, payload PlanUATPayload, epoch provenance.TaskID) (DecisionDraft, bool, error) {
	var kind DecisionKindID
	switch in.Outcome {
	case PlanUATAccepted:
		kind = DecisionPlanUATAccepted
	case PlanUATChangesRequested:
		kind = DecisionPlanUATChangesRequested
	case PlanUATDeferredByAFK:
		kind = DecisionPlanUATDeferredByAFK
	default:
		return DecisionDraft{}, false, nil
	}
	stored, found, err := s.committedDecision(in.Meta.OperationID, in.Proposal, kind)
	if err != nil || !found {
		return DecisionDraft{}, false, err
	}
	snapshot := servicePlanSnapshot(in, epoch)
	matched := false
	switch in.Outcome {
	case PlanUATAccepted:
		expected, err := s.policy.planAccepted.Draft(PlanAccepted{Snapshot: snapshot, Interactions: payload.Interactions, Feedback: payload.Feedback})
		matched = err == nil && sameDecisionEncoding(stored.record.Decision, expected.encoding())
	case PlanUATChangesRequested:
		expected, err := s.policy.planChanges.Draft(PlanChangesRequested{Snapshot: snapshot, Interactions: payload.Interactions, Feedback: payload.Feedback})
		matched = err == nil && sameDecisionEncoding(stored.record.Decision, expected.encoding())
	case PlanUATDeferredByAFK:
		var existing PlanDeferredByAFK
		if err := json.Unmarshal(stored.record.Decision.Payload, &existing); err != nil {
			return DecisionDraft{}, false, humanServiceErr("planUATReplayDraft", "the committed AFK Plan UAT payload is malformed", "an exact retry cannot verify its original user decision", "repair the malformed decision fact before retrying")
		}
		expected, err := canonicalJSON(struct {
			Snapshot      PlanUATSnapshot   `json:"snapshot"`
			Interactions  []UATInteraction  `json:"interactions"`
			Feedback      []UATFeedbackItem `json:"feedback"`
			HeldQuestions []HeldUATQuestion `json:"heldQuestions"`
		}{snapshot, payload.Interactions, payload.Feedback, payload.HeldQuestions})
		actual, actualErr := canonicalJSON(struct {
			Snapshot      PlanUATSnapshot   `json:"snapshot"`
			Interactions  []UATInteraction  `json:"interactions"`
			Feedback      []UATFeedbackItem `json:"feedback"`
			HeldQuestions []HeldUATQuestion `json:"heldQuestions"`
		}{existing.Snapshot, existing.Interactions, existing.Feedback, existing.HeldQuestions})
		matched = err == nil && actualErr == nil && bytes.Equal(expected, actual)
	}
	if !matched {
		return DecisionDraft{}, false, nil
	}
	draft, err := s.draftFromStored(stored.record.Decision)
	return draft, err == nil, err
}

func (s *epochHumanService) decodeDecisionFact(row provenance.DecisionRow, subject provenance.TaskID, expectedKind DecisionKindID) (decisionFact, error) {
	if row.TaskID == nil || *row.TaskID != subject || row.DecisionKind != journalDecisionKind(expectedKind) {
		return decisionFact{}, humanServiceErr("decodeDecisionFact", "a queried decision fact has an unexpected task or kind", "authoritative decision reconstruction requires the exact task-scoped decision family", "verify journal integrity and retry")
	}
	var record persistedDecision
	if err := json.Unmarshal(row.Payload, &record); err != nil {
		return decisionFact{}, humanServiceErr("decodeDecisionFact", "a canonical decision fact has malformed payload", "the persisted decision cannot be used as authoritative workflow state", "repair the malformed decision fact before retrying")
	}
	actor, err := provenance.ParseActorID(record.Actor)
	if err != nil || actor != row.EffectiveActorID || record.ID != decisionIDForOperation(row.ProducingOperationID) || record.Subject != subject.String() || record.Decision.Kind != expectedKind {
		return decisionFact{}, humanServiceErr("decodeDecisionFact", "a canonical decision fact has inconsistent identity or attribution", "the decision envelope must agree with its immutable Provenance fact metadata", "repair the inconsistent decision fact before retrying")
	}
	if _, err := provenance.ParseTaskID(record.Epoch); err != nil {
		return decisionFact{}, humanServiceErr("decodeDecisionFact", "a canonical decision fact has malformed epoch identity", "the decision cannot be scoped to an existing epoch", "repair the malformed decision fact before retrying")
	}
	if err := s.policy.Catalog.ValidateStored(record.Decision); err != nil {
		return decisionFact{}, fmt.Errorf("decode canonical decision fact %q: %w", record.ID, err)
	}
	return decisionFact{row: row, record: record}, nil
}

type interactionModeState struct {
	cursor    InteractionModeCursor
	condition provenance.Condition
}

func (s *epochHumanService) interactionModeState(epoch provenance.TaskID) (interactionModeState, error) {
	facts, err := s.decisionFacts(epoch, DecisionInteractionModeChanged)
	if err != nil {
		return interactionModeState{}, err
	}
	state := interactionModeState{cursor: InteractionModeCursor{Mode: InteractionNormal}, condition: currentDecisionCondition(epoch, DecisionInteractionModeChanged, 0)}
	for _, fact := range facts {
		if fact.record.Epoch != epoch.String() {
			return interactionModeState{}, humanServiceErr("interactionModeState", "a mode decision is scoped to a different epoch", "an epoch mode fold can only consume decisions for that exact epoch", "repair the inconsistent decision fact before retrying")
		}
		var changed InteractionModeChanged
		if err := json.Unmarshal(fact.record.Decision.Payload, &changed); err != nil {
			return interactionModeState{}, humanServiceErr("interactionModeState", "a mode decision payload is malformed", "the canonical decision fact cannot be folded", "repair the malformed decision fact before retrying")
		}
		if err := validateInteractionModeChanged(changed); err != nil {
			return interactionModeState{}, fmt.Errorf("fold canonical mode decision %q: %w", fact.record.ID, err)
		}
		if changed.From != state.cursor.Mode {
			return interactionModeState{}, humanServiceErr("interactionModeState", fmt.Sprintf("mode decision %q starts from %q but current mode is %q", fact.record.ID, changed.From, state.cursor.Mode), "the canonical interaction-mode decision chain is inconsistent", "resolve the competing mode decisions before retrying")
		}
		id := fact.record.ID
		state.cursor = InteractionModeCursor{Entry: &id, Mode: changed.To}
		state.condition = currentDecisionCondition(epoch, DecisionInteractionModeChanged, fact.row.JournalID)
	}
	return state, nil
}

func currentDecisionCondition(subject provenance.TaskID, kind DecisionKindID, journalID provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionCurrentFact, Selector: provenance.FactSelector{Kind: provenance.FactDecision, Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, DecisionKind: journalDecisionKind(kind)}, AssertedJournalID: journalID}
}

func (s *epochHumanService) taskEvents(task provenance.TaskID, kind provenance.EventKind) ([]provenance.TaskEventRow, error) {
	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{task}, EventKinds: []provenance.EventKind{kind}, Limit: provenance.MaxFactPageSize}
	var out []provenance.TaskEventRow
	for {
		page, err := s.tracker.prov.Journal().QueryTaskEvents(query)
		if err != nil {
			return nil, fmt.Errorf("query bounded task events for task %q: %w", task, err)
		}
		out = append(out, page.Events...)
		if page.Next == nil {
			return out, nil
		}
		query.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.AfterJournalID = page.Next.AfterJournalID
	}
}

func (s *epochHumanService) requireAcceptedReview(subject, epoch provenance.TaskID, round ReviewRoundID) error {
	events, err := s.taskEvents(subject, FamilyReviewRoundFinalized.EventKind())
	if err != nil {
		return err
	}
	for _, row := range events {
		var value struct {
			Epoch   string `json:"epoch"`
			Round   string `json:"round"`
			Verdict string `json:"verdict"`
		}
		if json.Unmarshal(row.Payload, &value) == nil && value.Epoch == epoch.String() && value.Round == string(round) && value.Verdict == VerdictAccept.String() {
			return nil
		}
	}
	return humanServiceErr("RatifyPlan", fmt.Sprintf("review round %q is not an accepted finalized round for proposal %q", round, subject), "ratification requires exact accepted review evidence", "finalize and accept that review round before ratifying")
}

func (s *epochHumanService) requireAcceptedDecision(subject, epoch provenance.TaskID, id DecisionLedgerEntryID, kind DecisionKindID, where string) (provenance.Condition, error) {
	facts, err := s.decisionFacts(subject, kind)
	if err != nil {
		return provenance.Condition{}, err
	}
	for _, fact := range facts {
		if fact.record.Epoch == epoch.String() && fact.record.ID == id {
			return currentDecisionCondition(subject, kind, fact.row.JournalID), nil
		}
	}
	return provenance.Condition{}, humanServiceErr(where, fmt.Sprintf("decision %q is not accepted evidence for subject %q", id, subject), "the gate requires the exact persisted accepted decision", "record an accepted decision for this subject and reference its returned id")
}

func (s *epochHumanService) requireAcceptedImplementationUAT(subject, epoch provenance.TaskID, candidate IntegrationCandidateSetID, id DecisionLedgerEntryID) (provenance.Condition, error) {
	facts, err := s.decisionFacts(subject, DecisionImplementationUAT)
	if err != nil {
		return provenance.Condition{}, err
	}
	for _, fact := range facts {
		if fact.record.Epoch != epoch.String() || fact.record.ID != id {
			continue
		}
		var record struct {
			Outcome ImplementationUATVerdict `json:"outcome"`
		}
		if json.Unmarshal(fact.record.Decision.Payload, &record) == nil && record.Outcome == ImplUATAccepted {
			return currentDecisionCondition(subject, DecisionImplementationUAT, fact.row.JournalID), nil
		}
	}
	return provenance.Condition{}, humanServiceErr("Land", fmt.Sprintf("Implementation UAT decision %q is not an accepted decision bound to candidate %q", id, candidate), "landing requires exact accepted UAT evidence for the same candidate", "record accepted Implementation UAT for this candidate and reference its returned id")
}

func encodeConditionEvidence(conditions []provenance.Condition) (json.RawMessage, error) {
	snapshots := make([]conditionSnapshot, len(conditions))
	for i, condition := range conditions {
		if condition.Selector.Kind != provenance.FactDecision || condition.Selector.Filter.TaskScope.Kind != provenance.FactTaskExact {
			return nil, humanServiceErr("encodeConditionEvidence", "a command condition has an unsupported fact selector", "human-decision conditions must be exact task-scoped decision facts", "construct the condition through currentDecisionCondition")
		}
		snapshots[i] = conditionSnapshot{Kind: condition.Kind, Task: condition.Selector.Filter.TaskScope.TaskID.String(), DecisionKind: condition.Selector.DecisionKind, AssertedJournalID: condition.AssertedJournalID}
	}
	return canonicalJSON(conditionEvidence{Conditions: snapshots})
}

func (s *epochHumanService) restoreCommittedConditions(op provenance.OperationID, current []provenance.Condition) ([]provenance.Condition, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: []provenance.OperationID{op}}, Kinds: []provenance.EvidenceKind{preconditionEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var rows []provenance.EvidenceRow
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return nil, fmt.Errorf("query bounded operation precondition evidence for %q: %w", op, err)
		}
		rows = append(rows, page.Rows...)
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if len(rows) == 0 {
		return append([]provenance.Condition(nil), current...), nil
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("%w: operation %q has %d precondition evidence facts; expected one", provenance.ErrOperationConflict, op, len(rows))
	}
	var stored conditionEvidence
	if err := json.Unmarshal(rows[0].Payload, &stored); err != nil {
		return nil, humanServiceErr("restoreCommittedConditions", "the committed precondition evidence is malformed", "an exact operation retry cannot reconstruct its original transaction-local conditions", "repair the malformed evidence fact before retrying")
	}
	if len(stored.Conditions) != len(current) {
		return nil, fmt.Errorf("%w: operation %q was already committed with a different condition count", provenance.ErrOperationConflict, op)
	}
	restored := append([]provenance.Condition(nil), current...)
	for i, snapshot := range stored.Conditions {
		condition := &restored[i]
		if condition.Kind != snapshot.Kind || condition.Selector.Kind != provenance.FactDecision || condition.Selector.Filter.TaskScope.Kind != provenance.FactTaskExact || condition.Selector.Filter.TaskScope.TaskID.String() != snapshot.Task || condition.Selector.DecisionKind != snapshot.DecisionKind {
			return nil, fmt.Errorf("%w: operation %q was already committed with different prerequisite semantics", provenance.ErrOperationConflict, op)
		}
		condition.AssertedJournalID = snapshot.AssertedJournalID
	}
	return restored, nil
}

func humanServiceErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "Pasture rejected an epoch human-decision operation: " + what + ".", Why: why + ".", Where: "Epoch human service (internal/tasks/epoch_human_service.go, " + where + ").", Impact: "The command did not reach its atomic Apply; no decision, evidence, activity, event, or lifecycle effect was written.", Fix: fix + "."}
}
