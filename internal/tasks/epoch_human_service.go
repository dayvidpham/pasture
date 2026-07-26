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
	planSubjectEvidenceKind  = provenance.EvidenceKind("pasture.plan.proposal.v1")
	candidateEvidenceKind    = provenance.EvidenceKind("pasture.integration.candidate.v1")
)

// epochHumanService is the single journal-backed aggregate for explicit-human
// workflow decisions. Decisions and evidence are canonical facts; the one typed
// task event is a material reference to those facts and the real Activity row.
type epochHumanService struct {
	tracker   *trackerImpl
	policy    PolicySet
	authority provenance.JournalID
	barrier   EpochRaceBarrier
	now       func() time.Time
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
	now := opts.now
	if now == nil {
		now = time.Now
	}
	return &epochHumanService{tracker: t, policy: policy, authority: authority, barrier: opts.Synchronization.RaceBarrier, now: now}, nil
}

func (s *epochHumanService) SetInteractionMode(ctx context.Context, in SetInteractionModeInput) (DecisionResult, error) {
	epoch, err := s.preflightCommon(in.Meta, in.Epoch, in.Actor)
	if err != nil {
		return DecisionResult{}, err
	}
	if !in.Mode.valid() {
		return DecisionResult{}, humanServiceErr("SetInteractionMode", fmt.Sprintf("mode %q is unknown", in.Mode), "interaction mode must be normal or afk", "supply normal or afk")
	}
	subjectRef := []evidenceRef{{Kind: "pasture.epoch.subject.v1", Value: epoch.String()}}
	if stored, found, err := s.committedOperationDecision(in.Meta.OperationID, []DecisionKindID{DecisionInteractionModeChanged}); err != nil {
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
				subjectRef, nil, nil)
		}
		state, err := s.interactionModeState(epoch)
		if err != nil {
			return DecisionResult{}, err
		}
		draft, err := s.policy.DraftModeChange(InteractionModeChanged{From: state.cursor.Mode, To: in.Mode})
		if err != nil {
			return DecisionResult{}, err
		}
		return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationSetInteractionMode, draft, subjectRef, nil, nil)
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
		subjectRef, []provenance.Condition{state.condition}, nil)
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
	payload := PlanUATPayload{}
	if in.Payload != nil {
		payload = *in.Payload
	}
	if stored, found, err := s.committedOperationDecision(in.Meta.OperationID, planUATDecisionKinds()); err != nil {
		return DecisionResult{}, err
	} else if found {
		draft, err := s.planUATReplayDraft(in, payload, epoch, stored)
		if err != nil {
			return DecisionResult{}, err
		}
		return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRecordPlanUAT, draft, nil, nil, nil)
	}
	if _, err := s.tracker.prov.Show(in.Proposal); err != nil {
		return DecisionResult{}, humanServiceErr("RecordPlanUAT", fmt.Sprintf("proposal %q could not be read", in.Proposal), "Plan UAT cannot target a missing proposal", "supply an existing proposal")
	}
	state, err := s.planSubjectState(epoch, in.Proposal)
	if err != nil {
		return DecisionResult{}, err
	}
	if state.terminal() {
		return DecisionResult{}, humanServiceErr("RecordPlanUAT", fmt.Sprintf("proposal %q is already in terminal state %q", in.Proposal, state.state), "a terminal proposal cannot receive another Plan UAT decision", "start a new proposal or use the existing terminal decision")
	}
	draft, conditions, err := s.planUATDraft(in, payload, epoch, state)
	if err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRecordPlanUAT, draft,
		nil, conditions, nil)
}

func (s *epochHumanService) planUATDraft(in PlanUATInput, payload PlanUATPayload, epoch provenance.TaskID, subjectState subjectStateSnapshot) (DecisionDraft, []provenance.Condition, error) {
	snapshot := servicePlanSnapshot(in, epoch)
	mode := InteractionModeCursor{Mode: InteractionNormal}
	conditions := []provenance.Condition{subjectState.conditionCurrent()}
	if in.Outcome == PlanUATDeferredByAFK {
		modeState, err := s.interactionModeState(epoch)
		if err != nil {
			return DecisionDraft{}, nil, err
		}
		mode = modeState.cursor
		conditions = append(conditions, modeState.condition)
	}
	draft, err := s.policy.DraftPlanUAT(PlanUATDecision{Snapshot: snapshot, ReportedVerdict: in.Outcome, Interactions: payload.Interactions, Feedback: payload.Feedback, HeldQuestions: payload.HeldQuestions, Mode: mode})
	return draft, conditions, err
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
	draft, err := s.policy.DraftPlanRatified(PlanRatified{Proposal: in.Proposal.String(), ReviewRound: in.ReviewRound, PlanUAT: in.PlanUAT})
	if err != nil {
		return DecisionResult{}, err
	}
	if stored, found, err := s.committedOperationDecision(in.Meta.OperationID, []DecisionKindID{DecisionPlanRatified}); err != nil {
		return DecisionResult{}, err
	} else if found && sameDecisionEncoding(stored.record.Decision, draft.encoding()) {
		replay, err := s.draftFromStored(stored.record.Decision)
		if err != nil {
			return DecisionResult{}, err
		}
		return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRatifyPlan, replay,
			[]evidenceRef{{Kind: "pasture.review.round.v1", Value: string(in.ReviewRound)}, {Kind: "pasture.plan-uat.decision.v1", Value: string(in.PlanUAT)}},
			nil, []provenance.Effect{lifecycleEffect(in.Proposal, provenance.EventKindTaskClosed)})
	} else if found {
		return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRatifyPlan, draft, nil, nil, nil)
	}
	if _, err := s.tracker.prov.Show(in.Proposal); err != nil {
		return DecisionResult{}, humanServiceErr("RatifyPlan", fmt.Sprintf("proposal %q could not be read", in.Proposal), "ratification must target an existing proposal", "supply an existing proposal")
	}
	if err := s.requireAcceptedReview(in.Proposal, epoch, in.ReviewRound); err != nil {
		return DecisionResult{}, err
	}
	state, err := s.planSubjectState(epoch, in.Proposal)
	if err != nil {
		return DecisionResult{}, err
	}
	if state.state != subjectStatePlanAccepted || state.decision != in.PlanUAT {
		return DecisionResult{}, humanServiceErr("RatifyPlan", fmt.Sprintf("Plan UAT decision %q is not the current accepted state for proposal %q", in.PlanUAT, in.Proposal), "ratification must bind the latest accepted Plan UAT state", "record a fresh accepted Plan UAT before ratifying")
	}
	if err := s.requireAcceptedDecision(in.Proposal, epoch, in.PlanUAT, DecisionPlanUATAccepted, "RatifyPlan"); err != nil {
		return DecisionResult{}, err
	}
	return s.commit(ctx, in.Meta, in.Epoch, epoch, in.Proposal, in.Actor, MutationRatifyPlan, draft,
		[]evidenceRef{{Kind: "pasture.review.round.v1", Value: string(in.ReviewRound)}, {Kind: "pasture.plan-uat.decision.v1", Value: string(in.PlanUAT)}},
		[]provenance.Condition{state.conditionExact()}, []provenance.Effect{lifecycleEffect(in.Proposal, provenance.EventKindTaskClosed)})
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
	payload := ImplUATPayload{}
	if in.Payload != nil {
		payload = *in.Payload
	}
	draft, err := s.policy.DraftImplementationUAT(in.Outcome, payload)
	if err != nil {
		return DecisionResult{}, err
	}
	if stored, found, err := s.committedOperationDecision(in.Meta.OperationID, []DecisionKindID{DecisionImplementationUAT}); err != nil {
		return DecisionResult{}, err
	} else if found && sameDecisionEncoding(stored.record.Decision, draft.encoding()) && stored.row.TaskID != nil && *stored.row.TaskID == candidate {
		replay, err := s.draftFromStored(stored.record.Decision)
		if err != nil {
			return DecisionResult{}, err
		}
		binding, err := s.implementationUATReviewBindingForOperation(epoch, candidate, in.Meta.OperationID)
		if err != nil {
			return DecisionResult{}, err
		}
		bindingEffect, err := newImplementationUATReviewBindingEvidenceEffect(candidate, binding.value, provenance.ResultSlotID("evidence-1"))
		if err != nil {
			return DecisionResult{}, err
		}
		return s.commitWithAuthority(ctx, in.Meta, in.Epoch, epoch, candidate, in.Actor, MutationRecordImplementationUAT, replay,
			nil, nil, nil, []provenance.Effect{bindingEffect})
	} else if found {
		return s.commit(ctx, in.Meta, in.Epoch, epoch, candidate, in.Actor, MutationRecordImplementationUAT, draft, nil, nil, nil)
	}
	if _, err := s.tracker.prov.Show(candidate); err != nil {
		return DecisionResult{}, humanServiceErr("RecordImplementationUAT", fmt.Sprintf("candidate %q could not be read", in.Candidate), "Implementation UAT cannot target a missing candidate", "supply an existing integration candidate")
	}
	state, err := s.candidateSubjectState(epoch, candidate)
	if err != nil {
		return DecisionResult{}, err
	}
	if err := requireEligibleCandidateLifecycle("RecordImplementationUAT", candidate, state); err != nil {
		return DecisionResult{}, err
	}
	review, err := s.currentImplementationReviewAuthority(epoch, candidate)
	if err != nil {
		return DecisionResult{}, err
	}
	if review.value.State != reviewFinalizedClean {
		return DecisionResult{}, humanServiceErr("RecordImplementationUAT", fmt.Sprintf("candidate %q has current review state %d", in.Candidate, review.value.State), "Implementation UAT requires one current finalized clean implementation review", "finalize a clean three-axis review for this candidate before recording Implementation UAT")
	}
	bindingEffect, err := newImplementationUATReviewBindingEvidenceEffect(candidate, implementationUATReviewBinding{
		Epoch:       EpochRootID(epoch.String()),
		Candidate:   in.Candidate,
		ReviewRound: review.value.Round,
		ReviewFact:  review.journalID,
		Operation:   in.Meta.OperationID,
	}, provenance.ResultSlotID("evidence-1"))
	if err != nil {
		return DecisionResult{}, err
	}
	return s.commitWithAuthority(ctx, in.Meta, in.Epoch, epoch, candidate, in.Actor, MutationRecordImplementationUAT, draft,
		nil, []provenance.Condition{state.conditionCurrent(), reviewAuthorityCurrentCondition(candidate, review.journalID)}, nil, []provenance.Effect{bindingEffect})
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
	if stored, found, err := s.committedOperationDecision(in.Meta.OperationID, []DecisionKindID{DecisionLanded}); err != nil {
		return DecisionResult{}, err
	} else if found && sameDecisionEncoding(stored.record.Decision, draft.encoding()) {
		replay, err := s.draftFromStored(stored.record.Decision)
		if err != nil {
			return DecisionResult{}, err
		}
		binding, err := s.landAuthorityBindingForOperation(stored)
		if err != nil {
			return DecisionResult{}, err
		}
		if binding.value.Epoch == EpochRootID(epoch.String()) && binding.value.Candidate == IntegrationCandidateSetID(candidate.String()) && binding.value.ImplementationUAT == in.ImplementationUAT {
			bindingEffect, err := newLandAuthorityBindingEvidenceEffect(epoch, binding.value, provenance.ResultSlotID("evidence-2"))
			if err != nil {
				return DecisionResult{}, err
			}
			return s.commitWithAuthority(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationLand, replay,
				[]evidenceRef{{Kind: "pasture.implementation-uat.decision.v1", Value: string(in.ImplementationUAT), stateSubject: candidate}},
				nil, []provenance.Effect{lifecycleEffect(epoch, provenance.EventKindTaskClosed)}, []provenance.Effect{bindingEffect})
		}
		return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationLand, replay,
			[]evidenceRef{{Kind: "pasture.implementation-uat.decision.v1", Value: string(in.ImplementationUAT), stateSubject: candidate}},
			nil, []provenance.Effect{lifecycleEffect(epoch, provenance.EventKindTaskClosed)})
	} else if found {
		return s.commit(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationLand, draft, nil, nil, nil)
	}
	if _, err := s.tracker.prov.Show(candidate); err != nil {
		return DecisionResult{}, humanServiceErr("Land", fmt.Sprintf("candidate %q could not be read", in.Candidate), "landing must target an existing integration candidate", "supply an existing integration candidate")
	}
	state, err := s.candidateSubjectState(epoch, candidate)
	if err != nil {
		return DecisionResult{}, err
	}
	if state.state != subjectStateImplementationAccepted || state.decision != in.ImplementationUAT || state.source != subjectStateSourceHumanDecision || state.operation == "" {
		return DecisionResult{}, humanServiceErr("Land", fmt.Sprintf("Implementation UAT decision %q is not the current accepted state for candidate %q", in.ImplementationUAT, in.Candidate), "landing must bind the latest accepted Implementation UAT state", "record a fresh accepted Implementation UAT before landing")
	}
	implementationUAT, err := s.acceptedImplementationUATFact(candidate, epoch, in.Candidate, in.ImplementationUAT)
	if err != nil {
		return DecisionResult{}, err
	}
	if implementationUAT.row.ProducingOperationID != state.operation {
		return DecisionResult{}, humanServiceErr("Land", fmt.Sprintf("Implementation UAT decision %q and current candidate state have different producing operations", in.ImplementationUAT), "landing requires the exact accepted UAT decision and state emitted by one operation", "record a fresh accepted Implementation UAT before landing")
	}
	binding, err := s.implementationUATReviewBindingForOperation(epoch, candidate, state.operation)
	if err != nil {
		return DecisionResult{}, err
	}
	review, err := s.currentImplementationReviewAuthority(epoch, candidate)
	if err != nil {
		return DecisionResult{}, err
	}
	if review.value.State != reviewFinalizedClean || binding.value.ReviewFact != review.journalID || binding.value.ReviewRound != review.value.Round {
		return DecisionResult{}, humanServiceErr("Land", fmt.Sprintf("Implementation UAT decision %q is not bound to the current clean review for candidate %q", in.ImplementationUAT, in.Candidate), "landing requires the exact review authority accepted by Implementation UAT to remain current and clean", "record a fresh accepted Implementation UAT after a clean finalized review")
	}
	manifest, err := s.exactCandidateManifest(epoch, candidate)
	if err != nil {
		return DecisionResult{}, err
	}
	publications, err := s.currentCandidatePublicationSet(epoch, candidate)
	if err != nil {
		return DecisionResult{}, err
	}
	if err := validatePublicationSetAgainstManifest(manifest.value, publications.value); err != nil {
		return DecisionResult{}, humanServiceErr("Land", fmt.Sprintf("candidate %q has incomplete or mismatched publication authority", in.Candidate), "every immutable candidate manifest member must have one current repository, ref, and commit verification", "publish every manifest member at its exact immutable commit before landing")
	}
	landBinding, err := newLandAuthorityBinding(landAuthorityBinding{
		Epoch:                              EpochRootID(epoch.String()),
		Candidate:                          IntegrationCandidateSetID(candidate.String()),
		LandOperation:                      in.Meta.OperationID,
		ImplementationUAT:                  in.ImplementationUAT,
		ImplementationUATOperation:         state.operation,
		ImplementationUATDecisionFact:      implementationUAT.row.JournalID,
		ImplementationUATStateFact:         state.journalID,
		ImplementationUATReviewBindingFact: binding.journalID,
		ReviewFact:                         review.journalID,
		ReviewRound:                        review.value.Round,
		ReviewAxes:                         review.value.Axes,
		ManifestFact:                       manifest.journalID,
		Members:                            manifest.value.Members,
		PublicationSetFact:                 publications.journalID,
		Publications:                       publications.value.Publications,
	})
	if err != nil {
		return DecisionResult{}, err
	}
	landBindingEffect, err := newLandAuthorityBindingEvidenceEffect(epoch, landBinding, provenance.ResultSlotID("evidence-2"))
	if err != nil {
		return DecisionResult{}, err
	}
	return s.commitWithAuthority(ctx, in.Meta, in.Epoch, epoch, epoch, in.Actor, MutationLand, draft,
		[]evidenceRef{{Kind: "pasture.implementation-uat.decision.v1", Value: string(in.ImplementationUAT), stateSubject: candidate}},
		[]provenance.Condition{state.conditionCurrent(), reviewAuthorityCurrentCondition(candidate, review.journalID), candidateManifestExactCondition(candidate, manifest.journalID), candidatePublicationSetCurrentCondition(candidate, publications.journalID)}, []provenance.Effect{lifecycleEffect(epoch, provenance.EventKindTaskClosed)}, []provenance.Effect{landBindingEffect})
}

type evidenceRef struct {
	Kind         provenance.EvidenceKind `json:"kind"`
	Value        string                  `json:"value"`
	State        subjectState            `json:"state,omitempty"`
	stateSubject provenance.TaskID       `json:"-"`
}

// conditionSnapshot is canonical evidence for the preconditions selected by a
// command. It is internal operation data, never a caller-supplied revision token.
// On an exact retry it restores the original operation input before direct Apply.
type conditionSnapshot struct {
	Kind              provenance.ConditionKind `json:"kind"`
	FactKind          provenance.FactKind      `json:"factKind"`
	Task              string                   `json:"task"`
	DecisionKind      provenance.DecisionKind  `json:"decisionKind,omitempty"`
	EvidenceKind      provenance.EvidenceKind  `json:"evidenceKind,omitempty"`
	AssertedJournalID provenance.JournalID     `json:"assertedJournalId"`
}

type conditionEvidence struct {
	Conditions []conditionSnapshot `json:"conditions"`
}

func (s *epochHumanService) commit(ctx context.Context, meta CommandMeta, epochID EpochRootID, epoch, subject provenance.TaskID, actor AssertedHumanActor, mutation EpochMutationKind, draft DecisionDraft, refs []evidenceRef, conditions []provenance.Condition, trailing []provenance.Effect) (DecisionResult, error) {
	return s.commitWithAuthority(ctx, meta, epochID, epoch, subject, actor, mutation, draft, refs, conditions, trailing, nil)
}

// commitWithAuthority extends the standard human-decision effect set with
// private typed authority evidence. Callers construct those values through the
// shared authority constructors; user inputs never nominate fact identities.
func (s *epochHumanService) commitWithAuthority(ctx context.Context, meta CommandMeta, epochID EpochRootID, epoch, subject provenance.TaskID, actor AssertedHumanActor, mutation EpochMutationKind, draft DecisionDraft, refs []evidenceRef, conditions []provenance.Condition, trailing, authorityEvidence []provenance.Effect) (DecisionResult, error) {
	decisionID := decisionIDForOperation(meta.OperationID)
	state, family, err := subjectStateForMutation(mutation, draft.encoding())
	if err != nil {
		return DecisionResult{}, err
	}
	if state != subjectStateInvalid {
		stateSubject := subject
		for _, ref := range refs {
			if ref.stateSubject != (provenance.TaskID{}) {
				stateSubject = ref.stateSubject
				break
			}
		}
		refs = append([]evidenceRef{{Kind: family, State: state, stateSubject: stateSubject}}, refs...)
	}
	conditions, err = s.restoreCommittedConditions(meta.OperationID, conditions)
	if err != nil {
		return DecisionResult{}, err
	}
	conditionPayload, err := encodeConditionEvidence(conditions)
	if err != nil {
		return DecisionResult{}, err
	}
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
		var payload []byte
		if ref.State != subjectStateInvalid {
			stateSubject := subject
			if ref.stateSubject != (provenance.TaskID{}) {
				stateSubject = ref.stateSubject
			}
			stateEvidence, stateErr := newHumanSubjectStateEvidence(epoch, stateSubject, ref.State, decisionID, draft.encoding().Kind, meta.OperationID)
			if stateErr != nil {
				return DecisionResult{}, stateErr
			}
			payload, err = canonicalJSON(stateEvidence)
		} else {
			payload, err = canonicalJSON(struct{ Epoch, Subject, Decision, Reference string }{epoch.String(), subject.String(), string(decisionID), ref.Value})
		}
		if err != nil {
			return DecisionResult{}, err
		}
		digest := sha256.Sum256(payload)
		evidenceSubject := subject
		if ref.State != subjectStateInvalid && ref.stateSubject != (provenance.TaskID{}) {
			evidenceSubject = ref.stateSubject
		}
		effects = append(effects, provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: provenance.ResultSlotID(fmt.Sprintf("evidence-%d", i)), TaskID: evidenceSubject, EvidenceKind: ref.Kind, ContentDigest: digest[:], Payload: payload})
	}
	for i, effect := range authorityEvidence {
		wantSlot := provenance.ResultSlotID(fmt.Sprintf("evidence-%d", len(refs)+i))
		if effect.Sort != provenance.EffectEvidence || effect.TaskID != subject || effect.ResultSlot != wantSlot {
			return DecisionResult{}, humanServiceErr("commitWithAuthority", fmt.Sprintf("authority evidence %d has an unexpected effect shape", i), "human-decision authority evidence must be a canonical candidate-scoped evidence result", "construct authority evidence with the expected candidate subject and evidence result slot")
		}
		effects = append(effects, effect)
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
	result, err := s.tracker.prov.Journal().Apply(provenance.OperationInput{OperationID: meta.OperationID, ActorID: actor.ActorID, AuthorityJournalID: &s.authority, CommandDigest: command, RecordedAt: s.now().UTC().UnixNano(), Conditions: conditions, Effects: effects})
	if err != nil {
		return DecisionResult{}, fmt.Errorf("epoch human decision %q failed during its single atomic Apply; no partial effects committed: %w", draft.encoding().Kind, err)
	}
	return decisionResult(meta.OperationID, epochID, subject, actor.ActorID, decisionID, activityID, result)
}

func subjectStateForMutation(mutation EpochMutationKind, encoding DecisionEncoding) (subjectState, provenance.EvidenceKind, error) {
	switch mutation {
	case MutationRecordPlanUAT:
		switch encoding.Kind {
		case DecisionPlanUATAccepted:
			return subjectStatePlanAccepted, planSubjectEvidenceKind, nil
		case DecisionPlanUATChangesRequested:
			return subjectStatePlanChangesRequested, planSubjectEvidenceKind, nil
		case DecisionPlanUATDeferredByAFK:
			return subjectStatePlanDeferred, planSubjectEvidenceKind, nil
		}
	case MutationRatifyPlan:
		return subjectStatePlanRatified, planSubjectEvidenceKind, nil
	case MutationRecordImplementationUAT:
		var record implementationUATRecord
		if err := json.Unmarshal(encoding.Payload, &record); err != nil {
			return subjectStateInvalid, "", humanServiceErr("subjectStateForMutation", "the Implementation UAT decision payload is malformed", "the candidate current state cannot be derived from an invalid decision", "repair the decision payload before retrying")
		}
		if record.Outcome == ImplUATAccepted {
			return subjectStateImplementationAccepted, candidateEvidenceKind, nil
		}
		if record.Outcome == ImplUATChangesRequested {
			return subjectStateImplementationChanges, candidateEvidenceKind, nil
		}
	case MutationLand:
		return subjectStateImplementationLanded, candidateEvidenceKind, nil
	}
	return subjectStateInvalid, "", nil
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

func planUATDecisionKinds() []DecisionKindID {
	return []DecisionKindID{DecisionPlanUATAccepted, DecisionPlanUATChangesRequested, DecisionPlanUATDeferredByAFK}
}

func decisionKindID(kind provenance.DecisionKind) (DecisionKindID, bool) {
	for _, candidate := range []DecisionKindID{DecisionInteractionModeChanged, DecisionPlanUATAccepted, DecisionPlanUATChangesRequested, DecisionPlanUATDeferredByAFK, DecisionImplementationUAT, DecisionPlanRatified, DecisionLanded} {
		if journalDecisionKind(candidate) == kind {
			return candidate, true
		}
	}
	return "", false
}

func (s *epochHumanService) foldDecisionFacts(subject provenance.TaskID, kind DecisionKindID, visit func(decisionFact) (bool, error)) error {
	query := provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, Kinds: []provenance.DecisionKind{journalDecisionKind(kind)}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryDecisions(query)
		if err != nil {
			return fmt.Errorf("query bounded decision facts for task %q: %w", subject, err)
		}
		for _, row := range page.Rows {
			fact, err := s.decodeDecisionFact(row, subject, kind)
			if err != nil {
				return err
			}
			stop, err := visit(fact)
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
		if page.Next == nil {
			return nil
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
}

// committedOperationDecision finds the one decision produced by an operation across
// all relevant subjects. Limit=2 is intentional: a second row is enough to reject a
// corrupt operation-local singleton without accumulating history in memory.
func (s *epochHumanService) committedOperationDecision(op provenance.OperationID, kinds []DecisionKindID) (decisionFact, bool, error) {
	journalKinds := make([]provenance.DecisionKind, len(kinds))
	for i, kind := range kinds {
		journalKinds[i] = journalDecisionKind(kind)
	}
	query := provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: []provenance.OperationID{op}}, Kinds: journalKinds, Page: provenance.FactPageRequest{Limit: 2}}
	var match decisionFact
	count := 0
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryDecisions(query)
		if err != nil {
			return decisionFact{}, false, fmt.Errorf("query bounded committed decision for operation %q: %w", op, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil {
				return decisionFact{}, false, humanServiceErr("committedOperationDecision", fmt.Sprintf("operation %q produced an unscoped decision fact", op), "human decisions must be tied to their epoch subject", "repair the decision fact before retrying")
			}
			kind, ok := decisionKindID(row.DecisionKind)
			if !ok {
				return decisionFact{}, false, humanServiceErr("committedOperationDecision", fmt.Sprintf("operation %q produced unknown decision kind %q", op, row.DecisionKind), "replay can only reconstruct catalog-issued human decisions", "repair the decision fact before retrying")
			}
			fact, err := s.decodeDecisionFact(row, *row.TaskID, kind)
			if err != nil {
				return decisionFact{}, false, err
			}
			count++
			if count > 1 {
				return decisionFact{}, false, humanServiceErr("committedOperationDecision", fmt.Sprintf("operation %q has multiple canonical decision facts", op), "one human-decision operation must produce exactly one decision fact", "repair the duplicate decision facts before retrying")
			}
			match = fact
		}
		if page.Next == nil {
			return match, count == 1, nil
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
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

func (s *epochHumanService) planUATReplayDraft(in PlanUATInput, payload PlanUATPayload, epoch provenance.TaskID, stored decisionFact) (DecisionDraft, error) {
	mode := InteractionModeCursor{Mode: InteractionNormal}
	if stored.record.Decision.Kind == DecisionPlanUATDeferredByAFK {
		var existing PlanDeferredByAFK
		if err := json.Unmarshal(stored.record.Decision.Payload, &existing); err != nil {
			return DecisionDraft{}, humanServiceErr("planUATReplayDraft", "the committed AFK Plan UAT payload is malformed", "an exact retry cannot verify its original user decision", "repair the malformed decision fact before retrying")
		}
		entry := existing.ModeEntry
		mode = InteractionModeCursor{Entry: &entry, Mode: InteractionAFK}
	} else if in.Outcome == PlanUATDeferredByAFK {
		// A changed retry is still a complete, policy-valid candidate before it
		// reaches Apply. Use the live cursor here; the stored ModeEntry is only
		// authoritative for an exact replay of a previously deferred decision.
		state, err := s.interactionModeState(epoch)
		if err != nil {
			return DecisionDraft{}, err
		}
		mode = state.cursor
	}
	expected, err := s.policy.DraftPlanUAT(PlanUATDecision{
		Snapshot: servicePlanSnapshot(in, epoch), ReportedVerdict: in.Outcome,
		Interactions: payload.Interactions, Feedback: payload.Feedback,
		HeldQuestions: payload.HeldQuestions, Mode: mode,
	})
	if err != nil {
		return DecisionDraft{}, err
	}
	if !sameDecisionEncoding(stored.record.Decision, expected.encoding()) {
		return expected, nil
	}
	draft, err := s.draftFromStored(stored.record.Decision)
	return draft, err
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
	if _, err := provenance.ParseTaskID(record.Subject); err != nil || record.Subject != subject.String() || row.ProducingOperationID == "" {
		return decisionFact{}, humanServiceErr("decodeDecisionFact", "a canonical decision fact has malformed subject or operation identity", "the decision envelope must identify the exact producing subject and operation", "repair the inconsistent decision fact before retrying")
	}
	if err := s.policy.Catalog.ValidateStored(record.Decision); err != nil {
		return decisionFact{}, fmt.Errorf("decode canonical decision fact %q: %w", record.ID, err)
	}
	return decisionFact{row: row, record: record}, nil
}

func (s *epochHumanService) planSubjectState(epoch, proposal provenance.TaskID) (subjectStateSnapshot, error) {
	return s.currentSubjectState(epoch, proposal, planSubjectEvidenceKind)
}

func (s *epochHumanService) candidateSubjectState(epoch, candidate provenance.TaskID) (subjectStateSnapshot, error) {
	return s.currentSubjectState(epoch, candidate, candidateEvidenceKind)
}

func (s *epochHumanService) currentSubjectState(epoch, subject provenance.TaskID, family provenance.EvidenceKind) (subjectStateSnapshot, error) {
	state := subjectStateSnapshot{subject: subject, family: family}
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, Kinds: []provenance.EvidenceKind{family}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return subjectStateSnapshot{}, fmt.Errorf("query bounded current subject state for task %q: %w", subject, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil || *row.TaskID != subject || row.EvidenceKind != family || row.ProducingOperationID == "" {
				return subjectStateSnapshot{}, humanServiceErr("currentSubjectState", "a subject-state evidence row has an unexpected task, family, or operation", "current-subject conditions require one exact task-scoped evidence authority", "repair the inconsistent evidence fact before retrying")
			}
			evidence, err := decodeSubjectStateEvidence(row.Payload, epoch, subject, family, row.ProducingOperationID)
			if err != nil {
				return subjectStateSnapshot{}, humanServiceErr("currentSubjectState", "a subject-state evidence payload is malformed or uses an impossible source", "lifecycle eligibility requires a validated typed source union", "repair the inconsistent subject-state evidence before retrying")
			}
			state = subjectStateSnapshot{subject: subject, state: evidence.State, decision: evidence.Decision, decisionKind: evidence.DecisionKind, operation: evidence.Operation, source: evidence.Source, journalID: row.JournalID, family: family}
		}
		if page.Next == nil {
			return state, nil
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
}

type reviewAuthoritySnapshot struct {
	value     implementationReviewAuthority
	journalID provenance.JournalID
}

type candidateManifestSnapshot struct {
	value     integrationCandidateManifest
	journalID provenance.JournalID
}

type candidatePublicationSetSnapshot struct {
	value     candidatePublicationSet
	journalID provenance.JournalID
}

type implementationUATReviewBindingSnapshot struct {
	value     implementationUATReviewBinding
	journalID provenance.JournalID
}

type landAuthorityBindingSnapshot struct {
	value     landAuthorityBinding
	journalID provenance.JournalID
}

func requireEligibleCandidateLifecycle(where string, candidate provenance.TaskID, state subjectStateSnapshot) error {
	if state.journalID == 0 || state.state == subjectStateInvalid {
		return humanServiceErr(where, fmt.Sprintf("candidate %q has no current lifecycle authority", candidate), "Implementation UAT requires an assignment-produced or current human candidate lifecycle fact", "create the integration candidate through the assignment-controlled aggregate before recording Implementation UAT")
	}
	if state.state == subjectStateReworked || state.state == subjectStateImplementationLanded {
		return humanServiceErr(where, fmt.Sprintf("candidate %q is in ineligible lifecycle state %q", candidate, state.state), "reworked and landed candidates cannot receive a new Implementation UAT decision", "use the current replacement candidate or the existing landed decision")
	}
	return nil
}

func (s *epochHumanService) currentImplementationReviewAuthority(epoch, candidate provenance.TaskID) (reviewAuthoritySnapshot, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}}, Kinds: []provenance.EvidenceKind{implementationReviewAuthorityEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var current reviewAuthoritySnapshot
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return reviewAuthoritySnapshot{}, fmt.Errorf("query bounded implementation review authority for candidate %q: %w", candidate, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil || *row.TaskID != candidate || row.EvidenceKind != implementationReviewAuthorityEvidenceKind || row.ProducingOperationID == "" {
				return reviewAuthoritySnapshot{}, humanServiceErr("currentImplementationReviewAuthority", "a review authority row has inconsistent task, kind, or producer identity", "Implementation UAT requires a canonical candidate-scoped review authority fact", "repair the malformed authority fact before retrying")
			}
			value, err := decodeImplementationReviewAuthority(row.Payload)
			if err != nil || value.Epoch != EpochRootID(epoch.String()) || value.Candidate != IntegrationCandidateSetID(candidate.String()) || value.Operation != row.ProducingOperationID {
				return reviewAuthoritySnapshot{}, humanServiceErr("currentImplementationReviewAuthority", "a review authority payload is inconsistent with its fact metadata", "the current review must be scoped to this epoch and candidate and identify its producing operation", "repair the malformed authority fact before retrying")
			}
			if row.JournalID > current.journalID {
				current = reviewAuthoritySnapshot{value: value, journalID: row.JournalID}
			}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if current.journalID == 0 {
		return reviewAuthoritySnapshot{}, humanServiceErr("currentImplementationReviewAuthority", fmt.Sprintf("candidate %q has no implementation review authority", candidate), "Implementation UAT requires one current finalized clean three-axis review", "finalize a clean implementation review for this candidate before recording Implementation UAT")
	}
	return current, nil
}

func (s *epochHumanService) exactCandidateManifest(epoch, candidate provenance.TaskID) (candidateManifestSnapshot, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}}, Kinds: []provenance.EvidenceKind{candidateManifestEvidenceKind}, Page: provenance.FactPageRequest{Limit: 2}}
	var manifest candidateManifestSnapshot
	count := 0
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return candidateManifestSnapshot{}, fmt.Errorf("query bounded candidate manifest for %q: %w", candidate, err)
		}
		for _, row := range page.Rows {
			count++
			if count > 1 {
				return candidateManifestSnapshot{}, humanServiceErr("exactCandidateManifest", fmt.Sprintf("candidate %q has multiple immutable manifests", candidate), "landing requires exactly one immutable candidate membership authority", "create a replacement candidate instead of appending another manifest")
			}
			if row.TaskID == nil || *row.TaskID != candidate || row.EvidenceKind != candidateManifestEvidenceKind || row.ProducingOperationID == "" {
				return candidateManifestSnapshot{}, humanServiceErr("exactCandidateManifest", "a candidate manifest row has inconsistent task, kind, or producer identity", "landing requires one canonical candidate-scoped immutable manifest", "repair the malformed manifest fact before retrying")
			}
			value, err := decodeCandidateManifest(row.Payload)
			if err != nil || value.Epoch != EpochRootID(epoch.String()) || value.Candidate != IntegrationCandidateSetID(candidate.String()) || value.Operation != row.ProducingOperationID {
				return candidateManifestSnapshot{}, humanServiceErr("exactCandidateManifest", "a candidate manifest payload is inconsistent with its fact metadata", "landing requires immutable membership for this exact epoch and candidate", "repair the malformed manifest fact before retrying")
			}
			manifest = candidateManifestSnapshot{value: value, journalID: row.JournalID}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if count == 0 {
		return candidateManifestSnapshot{}, humanServiceErr("exactCandidateManifest", fmt.Sprintf("candidate %q has no immutable manifest", candidate), "landing requires exact repository and commit membership before publication can be validated", "create the integration candidate manifest before landing")
	}
	return manifest, nil
}

func (s *epochHumanService) currentCandidatePublicationSet(epoch, candidate provenance.TaskID) (candidatePublicationSetSnapshot, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}}, Kinds: []provenance.EvidenceKind{candidatePublicationSetEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var current candidatePublicationSetSnapshot
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return candidatePublicationSetSnapshot{}, fmt.Errorf("query bounded candidate publication authority for %q: %w", candidate, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil || *row.TaskID != candidate || row.EvidenceKind != candidatePublicationSetEvidenceKind || row.ProducingOperationID == "" {
				return candidatePublicationSetSnapshot{}, humanServiceErr("currentCandidatePublicationSet", "a publication authority row has inconsistent task, kind, or producer identity", "landing requires one canonical candidate-scoped publication snapshot", "repair the malformed publication authority before retrying")
			}
			value, err := decodeCandidatePublicationSet(row.Payload)
			if err != nil || value.Epoch != EpochRootID(epoch.String()) || value.Candidate != IntegrationCandidateSetID(candidate.String()) || value.Operation != row.ProducingOperationID {
				return candidatePublicationSetSnapshot{}, humanServiceErr("currentCandidatePublicationSet", "a publication snapshot payload is inconsistent with its fact metadata", "landing requires current publication authority for this exact epoch and candidate", "repair the malformed publication fact before retrying")
			}
			if row.JournalID > current.journalID {
				current = candidatePublicationSetSnapshot{value: value, journalID: row.JournalID}
			}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if current.journalID == 0 {
		return candidatePublicationSetSnapshot{}, humanServiceErr("currentCandidatePublicationSet", fmt.Sprintf("candidate %q has no current publication set", candidate), "landing requires a complete current repository publication snapshot", "verify every candidate manifest member before landing")
	}
	return current, nil
}

func (s *epochHumanService) implementationUATReviewBindingForOperation(epoch, candidate provenance.TaskID, operation provenance.OperationID) (implementationUATReviewBindingSnapshot, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}, OperationIDs: []provenance.OperationID{operation}}, Kinds: []provenance.EvidenceKind{implementationUATReviewBindingEvidenceKind}, Page: provenance.FactPageRequest{Limit: 2}}
	var binding implementationUATReviewBindingSnapshot
	count := 0
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return implementationUATReviewBindingSnapshot{}, fmt.Errorf("query bounded Implementation UAT review binding for operation %q: %w", operation, err)
		}
		for _, row := range page.Rows {
			count++
			if count > 1 {
				return implementationUATReviewBindingSnapshot{}, humanServiceErr("implementationUATReviewBindingForOperation", fmt.Sprintf("Implementation UAT operation %q has multiple review bindings", operation), "one Implementation UAT must bind exactly one finalized clean review authority", "repair the duplicate binding evidence before retrying")
			}
			if row.TaskID == nil || *row.TaskID != candidate || row.EvidenceKind != implementationUATReviewBindingEvidenceKind || row.ProducingOperationID != operation {
				return implementationUATReviewBindingSnapshot{}, humanServiceErr("implementationUATReviewBindingForOperation", "an Implementation UAT review binding has inconsistent task, kind, or producer identity", "landing and replay require one canonical candidate-scoped review binding", "repair the malformed binding evidence before retrying")
			}
			value, err := decodeImplementationUATReviewBinding(row.Payload)
			if err != nil || value.Epoch != EpochRootID(epoch.String()) || value.Candidate != IntegrationCandidateSetID(candidate.String()) || value.Operation != operation {
				return implementationUATReviewBindingSnapshot{}, humanServiceErr("implementationUATReviewBindingForOperation", "an Implementation UAT review-binding payload is inconsistent with its fact metadata", "the binding must identify this exact epoch, candidate, and UAT operation", "repair the malformed binding evidence before retrying")
			}
			binding = implementationUATReviewBindingSnapshot{value: value, journalID: row.JournalID}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if count == 0 {
		return implementationUATReviewBindingSnapshot{}, humanServiceErr("implementationUATReviewBindingForOperation", fmt.Sprintf("Implementation UAT operation %q has no review binding", operation), "landing and exact replay require the review authority recorded with the accepted Implementation UAT", "record a fresh accepted Implementation UAT after a clean finalized review")
	}
	return binding, nil
}

// landAuthorityBindingForOperation restores the immutable Land evidence from
// the committed operation itself. It deliberately does not consult current
// review, manifest, publication, or UAT facts, because an exact replay must
// retain the authority selected by the original Apply.
func (s *epochHumanService) landAuthorityBindingForOperation(stored decisionFact) (landAuthorityBindingSnapshot, error) {
	operation := stored.row.ProducingOperationID
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: []provenance.OperationID{operation}}, Kinds: []provenance.EvidenceKind{landAuthorityBindingEvidenceKind}, Page: provenance.FactPageRequest{Limit: 2}}
	var binding landAuthorityBindingSnapshot
	count := 0
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return landAuthorityBindingSnapshot{}, fmt.Errorf("query bounded Land authority binding for operation %q: %w", operation, err)
		}
		for _, row := range page.Rows {
			count++
			if count > 1 {
				return landAuthorityBindingSnapshot{}, humanServiceErr("landAuthorityBindingForOperation", fmt.Sprintf("Land operation %q has multiple authority bindings", operation), "one Land decision must persist exactly one immutable authority binding", "repair the duplicate Land authority evidence before retrying")
			}
			if row.TaskID == nil || row.EvidenceKind != landAuthorityBindingEvidenceKind || row.ProducingOperationID != operation {
				return landAuthorityBindingSnapshot{}, humanServiceErr("landAuthorityBindingForOperation", "a Land authority binding has inconsistent task, kind, or producer identity", "exact Land replay requires one canonical epoch-scoped operation-local authority binding", "repair the malformed Land authority evidence before retrying")
			}
			value, err := decodeLandAuthorityBinding(row.Payload)
			if err != nil {
				return landAuthorityBindingSnapshot{}, humanServiceErr("landAuthorityBindingForOperation", "the Land authority binding payload is malformed", "exact Land replay requires a strict canonical authority binding", "repair the malformed Land authority evidence before retrying")
			}
			epoch, err := parseAuthorityTaskID(string(value.Epoch), "Land authority epoch")
			if err != nil || *row.TaskID != epoch || value.LandOperation != operation {
				return landAuthorityBindingSnapshot{}, humanServiceErr("landAuthorityBindingForOperation", "the Land authority binding disagrees with its persisted fact metadata", "the binding must be epoch-scoped and produced by the Land operation it records", "repair the inconsistent Land authority evidence before retrying")
			}
			binding = landAuthorityBindingSnapshot{value: value, journalID: row.JournalID}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if count == 0 {
		return landAuthorityBindingSnapshot{}, humanServiceErr("landAuthorityBindingForOperation", fmt.Sprintf("Land operation %q has no authority binding", operation), "exact Land replay requires the immutable authority record emitted with the original decision", "repair the committed Land operation before retrying")
	}
	var landed EpochLanded
	if err := json.Unmarshal(stored.record.Decision.Payload, &landed); err != nil || binding.value.ImplementationUAT != landed.ImplementationUAT || binding.value.Candidate != landed.Candidate || stored.record.ID != decisionIDForOperation(operation) {
		return landAuthorityBindingSnapshot{}, humanServiceErr("landAuthorityBindingForOperation", "the Land authority binding disagrees with its stored Land decision", "operation-local authority evidence must bind the exact landed candidate and Implementation UAT decision", "repair the inconsistent Land decision or authority binding before retrying")
	}
	return binding, nil
}

func subjectStateDecisionKind(state subjectState) (DecisionKindID, bool) {
	switch state {
	case subjectStatePlanAccepted:
		return DecisionPlanUATAccepted, true
	case subjectStatePlanChangesRequested:
		return DecisionPlanUATChangesRequested, true
	case subjectStatePlanDeferred:
		return DecisionPlanUATDeferredByAFK, true
	case subjectStatePlanRatified:
		return DecisionPlanRatified, true
	case subjectStateImplementationAccepted, subjectStateImplementationChanges:
		return DecisionImplementationUAT, true
	case subjectStateImplementationLanded:
		return DecisionLanded, true
	case subjectStateCandidateCurrent, subjectStateReworked:
		return "", false
	default:
		return "", false
	}
}

func subjectStateBelongsToFamily(state subjectState, family provenance.EvidenceKind) bool {
	if state == subjectStateReworked {
		return family == planSubjectEvidenceKind || family == candidateEvidenceKind
	}
	if state == subjectStateCandidateCurrent {
		return family == candidateEvidenceKind
	}
	kind, ok := subjectStateDecisionKind(state)
	if !ok {
		return false
	}
	if family == planSubjectEvidenceKind {
		return kind == DecisionPlanUATAccepted || kind == DecisionPlanUATChangesRequested || kind == DecisionPlanUATDeferredByAFK || kind == DecisionPlanRatified
	}
	return family == candidateEvidenceKind && (kind == DecisionImplementationUAT || kind == DecisionLanded)
}

type interactionModeState struct {
	cursor    InteractionModeCursor
	condition provenance.Condition
}

func (s *epochHumanService) interactionModeState(epoch provenance.TaskID) (interactionModeState, error) {
	state := interactionModeState{cursor: InteractionModeCursor{Mode: InteractionNormal}, condition: currentDecisionCondition(epoch, DecisionInteractionModeChanged, 0)}
	err := s.foldDecisionFacts(epoch, DecisionInteractionModeChanged, func(fact decisionFact) (bool, error) {
		if fact.record.Epoch != epoch.String() {
			return false, humanServiceErr("interactionModeState", "a mode decision is scoped to a different epoch", "an epoch mode fold can only consume decisions for that exact epoch", "repair the inconsistent decision fact before retrying")
		}
		var changed InteractionModeChanged
		if err := json.Unmarshal(fact.record.Decision.Payload, &changed); err != nil {
			return false, humanServiceErr("interactionModeState", "a mode decision payload is malformed", "the canonical decision fact cannot be folded", "repair the malformed decision fact before retrying")
		}
		if err := validateInteractionModeChanged(changed); err != nil {
			return false, fmt.Errorf("fold canonical mode decision %q: %w", fact.record.ID, err)
		}
		if changed.From != state.cursor.Mode {
			return false, humanServiceErr("interactionModeState", fmt.Sprintf("mode decision %q starts from %q but current mode is %q", fact.record.ID, changed.From, state.cursor.Mode), "the canonical interaction-mode decision chain is inconsistent", "resolve the competing mode decisions before retrying")
		}
		id := fact.record.ID
		state.cursor = InteractionModeCursor{Entry: &id, Mode: changed.To}
		state.condition = currentDecisionCondition(epoch, DecisionInteractionModeChanged, fact.row.JournalID)
		return false, nil
	})
	if err != nil {
		return interactionModeState{}, err
	}
	return state, nil
}

func currentDecisionCondition(subject provenance.TaskID, kind DecisionKindID, journalID provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionCurrentFact, Selector: provenance.FactSelector{Kind: provenance.FactDecision, Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, DecisionKind: journalDecisionKind(kind)}, AssertedJournalID: journalID}
}

func (s *epochHumanService) requireAcceptedReview(subject, epoch provenance.TaskID, round ReviewRoundID) error {
	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{subject}, EventKinds: []provenance.EventKind{FamilyReviewRoundFinalized.EventKind()}, Limit: provenance.MaxFactPageSize}
	for {
		page, err := s.tracker.prov.Journal().QueryTaskEvents(query)
		if err != nil {
			return fmt.Errorf("query bounded accepted review events for task %q: %w", subject, err)
		}
		for _, row := range page.Events {
			var value struct {
				Epoch   string `json:"epoch"`
				Round   string `json:"round"`
				Verdict string `json:"verdict"`
			}
			if json.Unmarshal(row.Payload, &value) == nil && value.Epoch == epoch.String() && value.Round == string(round) && value.Verdict == VerdictAccept.String() {
				return nil
			}
		}
		if page.Next == nil {
			break
		}
		query.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.AfterJournalID = page.Next.AfterJournalID
	}
	return humanServiceErr("RatifyPlan", fmt.Sprintf("review round %q is not an accepted finalized round for proposal %q", round, subject), "ratification requires exact accepted review evidence", "finalize and accept that review round before ratifying")
}

func (s *epochHumanService) requireAcceptedDecision(subject, epoch provenance.TaskID, id DecisionLedgerEntryID, kind DecisionKindID, where string) error {
	var found bool
	err := s.foldDecisionFacts(subject, kind, func(fact decisionFact) (bool, error) {
		if fact.record.Epoch == epoch.String() && fact.record.ID == id {
			found = true
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	return humanServiceErr(where, fmt.Sprintf("decision %q is not accepted evidence for subject %q", id, subject), "the gate requires the exact persisted accepted decision", "record an accepted decision for this subject and reference its returned id")
}

func (s *epochHumanService) acceptedImplementationUATFact(subject, epoch provenance.TaskID, candidate IntegrationCandidateSetID, id DecisionLedgerEntryID) (decisionFact, error) {
	var accepted decisionFact
	var found bool
	err := s.foldDecisionFacts(subject, DecisionImplementationUAT, func(fact decisionFact) (bool, error) {
		if fact.record.Epoch != epoch.String() || fact.record.ID != id {
			return false, nil
		}
		var record implementationUATRecord
		if err := json.Unmarshal(fact.record.Decision.Payload, &record); err == nil && record.Outcome == ImplUATAccepted {
			accepted = fact
			found = true
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return decisionFact{}, err
	}
	if found {
		return accepted, nil
	}
	return decisionFact{}, humanServiceErr("Land", fmt.Sprintf("Implementation UAT decision %q is not an accepted decision bound to candidate %q", id, candidate), "landing requires exact accepted UAT evidence for the same candidate", "record accepted Implementation UAT for this candidate and reference its returned id")
}

func encodeConditionEvidence(conditions []provenance.Condition) (json.RawMessage, error) {
	snapshots := make([]conditionSnapshot, len(conditions))
	for i, condition := range conditions {
		if condition.Selector.Filter.TaskScope.Kind != provenance.FactTaskExact || condition.Selector.Filter.TaskScope.TaskID == (provenance.TaskID{}) {
			return nil, humanServiceErr("encodeConditionEvidence", "a command condition has an unsupported task selector", "human-decision conditions must be exact task-scoped facts", "construct the condition through the typed state or decision condition helpers")
		}
		snapshot := conditionSnapshot{Kind: condition.Kind, FactKind: condition.Selector.Kind, Task: condition.Selector.Filter.TaskScope.TaskID.String(), AssertedJournalID: condition.AssertedJournalID}
		switch condition.Selector.Kind {
		case provenance.FactDecision:
			if condition.Selector.DecisionKind == "" || condition.Selector.EvidenceKind != "" {
				return nil, humanServiceErr("encodeConditionEvidence", fmt.Sprintf("condition %d does not select exactly one decision arm", i), "replay must preserve the closed selector arm and its decision kind", "construct a decision selector with one non-empty DecisionKind")
			}
			snapshot.DecisionKind = condition.Selector.DecisionKind
		case provenance.FactEvidence:
			if condition.Selector.EvidenceKind == "" || condition.Selector.DecisionKind != "" {
				return nil, humanServiceErr("encodeConditionEvidence", fmt.Sprintf("condition %d does not select exactly one evidence arm", i), "replay must preserve the closed selector arm and its evidence kind", "construct an evidence selector with one non-empty EvidenceKind")
			}
			snapshot.EvidenceKind = condition.Selector.EvidenceKind
		default:
			return nil, humanServiceErr("encodeConditionEvidence", fmt.Sprintf("condition %d has unknown fact kind", i), "human-decision conditions use only decision or evidence facts", "construct a typed FactDecision or FactEvidence selector")
		}
		snapshots[i] = snapshot
	}
	return canonicalJSON(conditionEvidence{Conditions: snapshots})
}

func (s *epochHumanService) restoreCommittedConditions(op provenance.OperationID, current []provenance.Condition) ([]provenance.Condition, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: []provenance.OperationID{op}}, Kinds: []provenance.EvidenceKind{preconditionEvidenceKind}, Page: provenance.FactPageRequest{Limit: 2}}
	var row provenance.EvidenceRow
	count := 0
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return nil, fmt.Errorf("query bounded operation precondition evidence for %q: %w", op, err)
		}
		for _, candidate := range page.Rows {
			count++
			if count > 1 {
				return nil, fmt.Errorf("%w: operation %q has multiple precondition evidence facts; expected one", provenance.ErrOperationConflict, op)
			}
			row = candidate
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if count == 0 {
		return append([]provenance.Condition(nil), current...), nil
	}
	var stored conditionEvidence
	if err := json.Unmarshal(row.Payload, &stored); err != nil {
		return nil, humanServiceErr("restoreCommittedConditions", "the committed precondition evidence is malformed", "an exact operation retry cannot reconstruct its original transaction-local conditions", "repair the malformed evidence fact before retrying")
	}
	restored := make([]provenance.Condition, len(stored.Conditions))
	for i, snapshot := range stored.Conditions {
		condition, err := conditionFromSnapshot(snapshot)
		if err != nil {
			return nil, fmt.Errorf("%w: operation %q has malformed persisted condition %d: %v", provenance.ErrOperationConflict, op, i, err)
		}
		restored[i] = condition
	}
	return restored, nil
}

func conditionFromSnapshot(snapshot conditionSnapshot) (provenance.Condition, error) {
	task, err := provenance.ParseTaskID(snapshot.Task)
	if err != nil {
		return provenance.Condition{}, fmt.Errorf("task %q is malformed: %w", snapshot.Task, err)
	}
	selector := provenance.FactSelector{Kind: snapshot.FactKind, Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: task}}}
	switch snapshot.FactKind {
	case provenance.FactDecision:
		if snapshot.DecisionKind == "" || snapshot.EvidenceKind != "" {
			return provenance.Condition{}, fmt.Errorf("decision selector must contain exactly one DecisionKind")
		}
		selector.DecisionKind = snapshot.DecisionKind
	case provenance.FactEvidence:
		if snapshot.EvidenceKind == "" || snapshot.DecisionKind != "" {
			return provenance.Condition{}, fmt.Errorf("evidence selector must contain exactly one EvidenceKind")
		}
		selector.EvidenceKind = snapshot.EvidenceKind
	default:
		return provenance.Condition{}, fmt.Errorf("fact kind %d is not supported", snapshot.FactKind)
	}
	return provenance.Condition{Kind: snapshot.Kind, Selector: selector, AssertedJournalID: snapshot.AssertedJournalID}, nil
}

func humanServiceErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "Pasture rejected an epoch human-decision operation: " + what + ".", Why: why + ".", Where: "Epoch human service (internal/tasks/epoch_human_service.go, " + where + ").", Impact: "The command did not reach its atomic Apply; no decision, evidence, activity, event, or lifecycle effect was written.", Fix: fix + "."}
}
