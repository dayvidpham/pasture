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
	decisionResultSlot = provenance.ResultSlotID("decision")
	activityResultSlot = provenance.ResultSlotID("activity")
	eventResultSlot    = provenance.ResultSlotID("event")
)

type epochHumanService struct {
	tracker   *trackerImpl
	policy    PolicySet
	authority provenance.JournalID
	barrier   EpochRaceBarrier
}

var _ EpochHumanService = (*epochHumanService)(nil)
var _ EpochHumanServiceFactory = (*trackerImpl)(nil)

// NewEpochHumanService constructs the production journal-backed human-decision service.
// Bootstrap authority is established at construction so a rejected command never performs
// a preparatory write before its single canonical Apply.
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
	if replay, ok, err := s.replay(ctx, in.Meta.OperationID, epoch, epoch, in.Actor.ActorID, DecisionInteractionModeChanged, func(detail json.RawMessage) bool {
		var v InteractionModeChanged
		return json.Unmarshal(detail, &v) == nil && v.To == in.Mode
	}); ok || err != nil {
		return replay, err
	}
	cursor, err := s.showInteractionModeTask(epoch)
	if err != nil {
		return DecisionResult{}, err
	}
	draft, err := s.policy.DraftModeChange(InteractionModeChanged{From: cursor.Mode, To: in.Mode})
	if err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationSetInteractionMode, draft, json.RawMessage(draft.encoding().Payload), []evidenceRef{{Kind: "pasture.epoch.subject.v1", Value: epoch.String()}}, nil)
}

func (s *epochHumanService) ShowInteractionMode(_ context.Context, epoch EpochRootID) (InteractionModeCursor, error) {
	task, err := parseTaskID("ShowInteractionMode", string(epoch), "epoch")
	if err != nil {
		return InteractionModeCursor{}, err
	}
	if _, err := s.tracker.prov.Show(task); err != nil {
		return InteractionModeCursor{}, humanServiceErr("ShowInteractionMode", fmt.Sprintf("epoch %q could not be read", epoch), "the requested epoch task is missing or unreadable", "supply an existing epoch task and retry")
	}
	return s.showInteractionModeTask(task)
}

func (s *epochHumanService) showInteractionModeTask(epoch provenance.TaskID) (InteractionModeCursor, error) {
	events, err := s.taskEvents(epoch, FamilyEpochDecisionRecorded.EventKind())
	if err != nil {
		return InteractionModeCursor{}, err
	}
	cursor := InteractionModeCursor{Mode: InteractionNormal}
	for _, row := range events {
		record, err := decodeDecisionEvent(row.Payload)
		if err != nil {
			return InteractionModeCursor{}, err
		}
		if record.Kind != string(DecisionInteractionModeChanged) {
			continue
		}
		var changed InteractionModeChanged
		if err := json.Unmarshal(record.Detail, &changed); err != nil {
			return InteractionModeCursor{}, humanServiceErr("ShowInteractionMode", "a persisted interaction-mode event has malformed detail", "the journal projection cannot be folded safely", "repair the malformed event before retrying")
		}
		if changed.From != cursor.Mode {
			return InteractionModeCursor{}, humanServiceErr("ShowInteractionMode", fmt.Sprintf("persisted mode decision %q starts from %q but current mode is %q", record.Decision, changed.From, cursor.Mode), "the persisted interaction-mode chain is inconsistent", "resolve the competing mode decisions before retrying")
		}
		id := DecisionLedgerEntryID(record.Decision)
		cursor = InteractionModeCursor{Entry: &id, Mode: changed.To}
	}
	return cursor, nil
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
	detail, draft, err := s.planUATDraft(in, payload, epoch)
	if err != nil {
		return DecisionResult{}, err
	}
	if replay, ok, err := s.replay(ctx, in.Meta.OperationID, epoch, in.Proposal, in.Actor.ActorID, draft.encoding().Kind, func(got json.RawMessage) bool { return bytes.Equal(got, detail) }); ok || err != nil {
		return replay, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRecordPlanUAT, draft, detail, []evidenceRef{{Kind: "pasture.plan.proposal.v1", Value: in.Proposal.String()}}, nil)
}

func (s *epochHumanService) planUATDraft(in PlanUATInput, payload PlanUATPayload, epoch provenance.TaskID) (json.RawMessage, DecisionDraft, error) {
	if !in.Outcome.valid() {
		return nil, DecisionDraft{}, humanServiceErr("RecordPlanUAT", fmt.Sprintf("outcome %q is unknown", in.Outcome), "Plan UAT must be accepted, changes_requested, or deferred_by_afk", "supply a known Plan UAT outcome")
	}
	if err := validateInteractions("PlanUATPayload.Interactions", payload.Interactions); err != nil {
		return nil, DecisionDraft{}, err
	}
	if err := validateFeedback("PlanUATPayload.Feedback", payload.Feedback); err != nil {
		return nil, DecisionDraft{}, err
	}
	if err := validateHeldQuestions("PlanUATPayload.HeldQuestions", payload.HeldQuestions); err != nil {
		return nil, DecisionDraft{}, err
	}
	if in.Outcome == PlanUATAccepted && hasFixNowFeedback(payload.Feedback) {
		return nil, DecisionDraft{}, humanServiceErr("RecordPlanUAT", "accepted Plan UAT contains FIX-NOW feedback", "blocking feedback requires changes_requested", "change the outcome or remove resolved blocking feedback")
	}
	var draft DecisionDraft
	var err error
	switch in.Outcome {
	case PlanUATAccepted:
		draft, err = s.policy.planAccepted.Draft(PlanAccepted{Snapshot: servicePlanSnapshot(in, epoch), Interactions: payload.Interactions, Feedback: payload.Feedback})
	case PlanUATChangesRequested:
		draft, err = s.policy.planChanges.Draft(PlanChangesRequested{Snapshot: servicePlanSnapshot(in, epoch), Interactions: payload.Interactions, Feedback: payload.Feedback})
	case PlanUATDeferredByAFK:
		cursor, e := s.showInteractionModeTask(epoch)
		if e != nil {
			return nil, DecisionDraft{}, e
		}
		if e = EvaluatePlanDeferral(PlanDeferralInput{Mode: cursor, HeldQuestions: payload.HeldQuestions, Feedback: payload.Feedback, Snapshot: servicePlanSnapshot(in, epoch)}); e != nil {
			return nil, DecisionDraft{}, e
		}
		draft, err = s.policy.planDeferred.Draft(PlanDeferredByAFK{Snapshot: servicePlanSnapshot(in, epoch), Interactions: payload.Interactions, Feedback: payload.Feedback, HeldQuestions: payload.HeldQuestions, ModeEntry: *cursor.Entry})
	}
	if err != nil {
		return nil, DecisionDraft{}, err
	}
	detail, err := canonicalJSON(struct {
		Outcome string         `json:"outcome"`
		Payload PlanUATPayload `json:"payload"`
	}{in.Outcome.String(), payload})
	return detail, draft, err
}

func servicePlanSnapshot(in PlanUATInput, epoch provenance.TaskID) PlanUATSnapshot {
	id := DecisionLedgerEntryID("decision:" + string(in.Meta.OperationID))
	return PlanUATSnapshot{ID: PlanUATDecisionID(id), UATTaskID: in.Proposal, Proposal: DocumentRevisionID(in.Proposal.String()), DecisionEntry: id, InputLedger: DocumentRevisionID(epoch.String()), OutputLedger: DocumentRevisionID(string(in.Meta.OperationID))}
}

func (s *epochHumanService) RatifyPlan(ctx context.Context, in RatifyPlanInput) (DecisionResult, error) {
	epoch, err := s.preflightCommon(in.Meta, in.Epoch, in.Actor)
	if err != nil {
		return DecisionResult{}, err
	}
	draft, err := s.policy.DraftPlanRatified(PlanRatified{Proposal: in.Proposal.String(), ReviewRound: in.ReviewRound, PlanUAT: in.PlanUAT})
	if err != nil {
		return DecisionResult{}, err
	}
	detail := json.RawMessage(draft.encoding().Payload)
	if replay, ok, err := s.replay(ctx, in.Meta.OperationID, epoch, in.Proposal, in.Actor.ActorID, DecisionPlanRatified, func(got json.RawMessage) bool { return bytes.Equal(got, detail) }); ok || err != nil {
		return replay, err
	}
	if err := s.requireAcceptedReview(in.Proposal, epoch, in.ReviewRound); err != nil {
		return DecisionResult{}, err
	}
	if err := s.requireAcceptedDecision(in.Proposal, epoch, in.PlanUAT, DecisionPlanUATAccepted, "RatifyPlan"); err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRatifyPlan, draft, detail,
		[]evidenceRef{{Kind: "pasture.review.round.v1", Value: string(in.ReviewRound)}, {Kind: "pasture.plan-uat.decision.v1", Value: string(in.PlanUAT)}},
		[]provenance.Effect{lifecycleEffect(in.Proposal, provenance.EventKindTaskClosed)})
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
	detail, err := canonicalJSON(struct {
		Candidate IntegrationCandidateSetID `json:"candidate"`
		Outcome   string                    `json:"outcome"`
		Payload   ImplUATPayload            `json:"payload"`
	}{in.Candidate, in.Outcome.String(), payload})
	if err != nil {
		return DecisionResult{}, err
	}
	if replay, ok, err := s.replay(ctx, in.Meta.OperationID, epoch, candidate, in.Actor.ActorID, DecisionImplementationUAT, func(got json.RawMessage) bool { return bytes.Equal(got, detail) }); ok || err != nil {
		return replay, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, candidate, in.Actor, MutationRecordImplementationUAT, draft, detail, []evidenceRef{{Kind: "pasture.integration.candidate.v1", Value: string(in.Candidate)}}, nil)
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
	draft, err := s.policy.DraftLanded(EpochLanded{Candidate: in.Candidate, ImplementationUAT: in.ImplementationUAT})
	if err != nil {
		return DecisionResult{}, err
	}
	detail := json.RawMessage(draft.encoding().Payload)
	if replay, ok, err := s.replay(ctx, in.Meta.OperationID, epoch, epoch, in.Actor.ActorID, DecisionLanded, func(got json.RawMessage) bool { return bytes.Equal(got, detail) }); ok || err != nil {
		return replay, err
	}
	if err := s.requireAcceptedImplementationUAT(candidate, epoch, in.Candidate, in.ImplementationUAT); err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationLand, draft, detail,
		[]evidenceRef{{Kind: "pasture.implementation-uat.decision.v1", Value: string(in.ImplementationUAT)}, {Kind: "pasture.integration.candidate.v1", Value: string(in.Candidate)}},
		[]provenance.Effect{lifecycleEffect(epoch, provenance.EventKindTaskClosed)})
}

type evidenceRef struct {
	Kind  provenance.EvidenceKind `json:"kind"`
	Value string                  `json:"value"`
}

func (s *epochHumanService) commit(ctx context.Context, meta CommandMeta, epochID EpochRootID, epoch, subject provenance.TaskID, actor AssertedHumanActor, mutation EpochMutationKind, draft DecisionDraft, detail json.RawMessage, refs []evidenceRef, trailing []provenance.Effect) (DecisionResult, error) {
	decisionID := DecisionLedgerEntryID("decision:" + string(meta.OperationID))
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
		payload, e := canonicalJSON(struct{ Epoch, Subject, Decision, Reference string }{epoch.String(), subject.String(), string(decisionID), ref.Value})
		if e != nil {
			return DecisionResult{}, e
		}
		digest := sha256.Sum256(payload)
		effects = append(effects, provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID(fmt.Sprintf("evidence-%d", i)), TaskID: subject, EvidenceKind: ref.Kind, ContentDigest: digest[:], Payload: payload})
	}
	activity, err := MapMaterialEvent(HumanDecisionActivityEvent{Subject: subject, Epoch: epoch, Activity: activityID, Actor: actor.ActorID, Decision: decisionID, Kind: draft.encoding().Kind})
	if err != nil {
		return DecisionResult{}, err
	}
	activity.ResultSlot = activityResultSlot
	event, err := MapMaterialEvent(EpochDecisionRecordedEvent{Subject: subject, Epoch: epoch, Activity: activityID, Actor: actor.ActorID, Decision: decisionID, Kind: draft.encoding().Kind, Detail: detail})
	if err != nil {
		return DecisionResult{}, err
	}
	event.ResultSlot = eventResultSlot
	effects = append(effects, activity, event)
	effects = append(effects, trailing...)
	command, err := canonicalJSON(struct {
		Operation string          `json:"operation"`
		Epoch     string          `json:"epoch"`
		Subject   string          `json:"subject"`
		Actor     string          `json:"actor"`
		Kind      string          `json:"kind"`
		Detail    json.RawMessage `json:"detail"`
		Evidence  []evidenceRef   `json:"evidence"`
	}{string(meta.OperationID), string(epochID), subject.String(), actor.ActorID.String(), string(draft.encoding().Kind), detail, refs})
	if err != nil {
		return DecisionResult{}, err
	}
	if s.barrier != nil {
		if err := s.barrier.AfterPreflight(ctx, mutation); err != nil {
			return DecisionResult{}, humanServiceErr("commit", "the operation was cancelled at the post-preflight barrier", "the injected synchronization boundary rejected the operation before Apply", "retry after the synchronization condition is resolved")
		}
	}
	unlock := s.tracker.lockWrite()
	result, applyErr := s.tracker.prov.Journal().Apply(provenance.OperationInput{OperationID: meta.OperationID, ActorID: actor.ActorID, AuthorityJournalID: &s.authority, CommandDigest: command, RecordedAt: time.Now().UTC().UnixNano(), Effects: effects})
	unlock()
	if applyErr != nil {
		return DecisionResult{}, fmt.Errorf("epoch human decision %q failed during its single atomic Apply; no partial effects committed: %w", draft.encoding().Kind, applyErr)
	}
	return decisionResult(meta.OperationID, epochID, actor.ActorID, decisionID, activityID, result), nil
}

func decisionResult(op provenance.OperationID, epoch EpochRootID, actor provenance.ActorID, decision DecisionLedgerEntryID, activity provenance.ActivityID, result provenance.CommittedResult) DecisionResult {
	events := append([]provenance.JournalID(nil), result.EmittedEvents...)
	return DecisionResult{CommandResult: CommandResult{OperationID: op, Replayed: result.ShortCircuited, Epoch: epoch, ActivityID: activity, EventIDs: events}, DecisionID: decision, ActorID: actor}
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

func (s *epochHumanService) taskEvents(task provenance.TaskID, kind provenance.EventKind) ([]provenance.TaskEventRow, error) {
	var out []provenance.TaskEventRow
	var after provenance.JournalID
	for {
		page, err := s.tracker.prov.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{task}, EventKinds: []provenance.EventKind{kind}, AfterJournalID: after})
		if err != nil {
			return nil, fmt.Errorf("query persisted epoch decision events for task %q: %w", task, err)
		}
		out = append(out, page.Events...)
		if page.Next == nil {
			return out, nil
		}
		after = page.Next.AfterJournalID
	}
}

type persistedDecisionEvent struct {
	Epoch, Activity, Actor, Decision, Kind string
	Detail                                 json.RawMessage
}

func decodeDecisionEvent(payload json.RawMessage) (persistedDecisionEvent, error) {
	var v persistedDecisionEvent
	if err := json.Unmarshal(payload, &v); err != nil {
		return v, humanServiceErr("decodeDecisionEvent", "a persisted epoch decision event is malformed", "the projection cannot safely establish gate state", "repair the malformed journal event")
	}
	return v, nil
}

func (s *epochHumanService) replay(_ context.Context, op provenance.OperationID, epoch, subject provenance.TaskID, actor provenance.ActorID, kind DecisionKindID, match func(json.RawMessage) bool) (DecisionResult, bool, error) {
	committed, err := s.tracker.prov.Journal().LookupCommitted(op)
	if err != nil {
		return DecisionResult{}, false, fmt.Errorf("lookup epoch operation %q: %w", op, err)
	}
	if committed.Kind == provenance.CommittedAbsent {
		return DecisionResult{}, false, nil
	}
	events, err := s.taskEvents(subject, FamilyEpochDecisionRecorded.EventKind())
	if err != nil {
		return DecisionResult{}, true, err
	}
	emitted := make(map[provenance.JournalID]bool, len(committed.EmittedEvents))
	for _, id := range committed.EmittedEvents {
		emitted[id] = true
	}
	for _, row := range events {
		if !emitted[row.JournalID] {
			continue
		}
		v, err := decodeDecisionEvent(row.Payload)
		if err != nil {
			return DecisionResult{}, true, err
		}
		if v.Epoch != epoch.String() || v.Actor != actor.String() || v.Kind != string(kind) || !match(v.Detail) {
			return DecisionResult{}, true, fmt.Errorf("%w: operation %q was already committed with different actor, subject, command, or evidence; mint a new operation identity for different work", provenance.ErrOperationConflict, op)
		}
		activity, err := provenance.ParseActivityID(v.Activity)
		if err != nil {
			return DecisionResult{}, true, humanServiceErr("replay", "the committed activity identity is malformed", "exact replay cannot reconstruct its original result", "repair the committed event")
		}
		return decisionResult(op, EpochRootID(v.Epoch), actor, DecisionLedgerEntryID(v.Decision), activity, provenance.CommittedResult{EmittedEvents: committed.EmittedEvents, ShortCircuited: true}), true, nil
	}
	if len(events) == 0 {
		return DecisionResult{}, true, fmt.Errorf("%w: operation %q was already committed for a different subject; mint a new operation identity for different work", provenance.ErrOperationConflict, op)
	}
	return DecisionResult{}, true, humanServiceErr("replay", fmt.Sprintf("operation %q has no linked epoch decision event among %d matching task events", op, len(events)), "the committed operation result is incomplete for this service", "verify journal integrity and repair the operation before retrying")
}

func (s *epochHumanService) requireAcceptedReview(subject, epoch provenance.TaskID, round ReviewRoundID) error {
	events, err := s.taskEvents(subject, FamilyReviewRoundFinalized.EventKind())
	if err != nil {
		return err
	}
	for _, row := range events {
		var v struct {
			Epoch   string `json:"epoch"`
			Round   string `json:"round"`
			Verdict string `json:"verdict"`
		}
		if json.Unmarshal(row.Payload, &v) == nil && v.Epoch == epoch.String() && v.Round == string(round) && v.Verdict == VerdictAccept.String() {
			return nil
		}
	}
	return humanServiceErr("RatifyPlan", fmt.Sprintf("review round %q is not an accepted finalized round for proposal %q", round, subject), "ratification requires exact accepted review evidence", "finalize and accept that review round before ratifying")
}

func (s *epochHumanService) requireAcceptedDecision(subject, epoch provenance.TaskID, id DecisionLedgerEntryID, kind DecisionKindID, where string) error {
	events, err := s.taskEvents(subject, FamilyEpochDecisionRecorded.EventKind())
	if err != nil {
		return err
	}
	for _, row := range events {
		v, e := decodeDecisionEvent(row.Payload)
		if e == nil && v.Epoch == epoch.String() && v.Decision == string(id) && v.Kind == string(kind) {
			return nil
		}
	}
	return humanServiceErr(where, fmt.Sprintf("decision %q is not accepted evidence for subject %q", id, subject), "the gate requires the exact persisted accepted decision", "record an accepted decision for this subject and reference its returned id")
}

func (s *epochHumanService) requireAcceptedImplementationUAT(subject, epoch provenance.TaskID, candidate IntegrationCandidateSetID, id DecisionLedgerEntryID) error {
	events, err := s.taskEvents(subject, FamilyEpochDecisionRecorded.EventKind())
	if err != nil {
		return err
	}
	for _, row := range events {
		v, e := decodeDecisionEvent(row.Payload)
		if e != nil || v.Epoch != epoch.String() || v.Decision != string(id) || v.Kind != string(DecisionImplementationUAT) {
			continue
		}
		var d struct {
			Candidate IntegrationCandidateSetID `json:"candidate"`
			Outcome   string                    `json:"outcome"`
		}
		if json.Unmarshal(v.Detail, &d) == nil && d.Candidate == candidate && d.Outcome == ImplUATAccepted.String() {
			return nil
		}
	}
	return humanServiceErr("Land", fmt.Sprintf("Implementation UAT decision %q is not an accepted decision bound to candidate %q", id, candidate), "landing requires exact accepted UAT evidence for the same candidate", "record accepted Implementation UAT for this candidate and reference its returned id")
}

func humanServiceErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "Pasture rejected an epoch human-decision operation: " + what + ".", Why: why + ".", Where: "Epoch human service (internal/tasks/epoch_human_service.go, " + where + ").", Impact: "The command did not reach its atomic Apply; no decision, evidence, activity, event, or lifecycle effect was written.", Fix: fix + "."}
}
