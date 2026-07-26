package tasks

import (
	"context"
	"fmt"

	"github.com/dayvidpham/provenance"
)

type candidateEventPayload struct {
	Epoch      string            `json:"epoch"`
	Candidate  string            `json:"candidate"`
	Slice      string            `json:"slice,omitempty"`
	Repository string            `json:"repository,omitempty"`
	Commit     provenance.GitOID `json:"commit,omitempty"`
}

func (s *epochAssignmentService) CreateSlice(ctx context.Context, in CreateSliceInput) (SliceResult, error) {
	if _, err := s.tracker.prov.Show(in.Plan); err != nil {
		return SliceResult{}, assignmentErr("CreateSlice", fmt.Sprintf("plan %q could not be read", in.Plan), "a slice must be attached to an existing plan task", "supply an existing plan task")
	}
	resolution, err := s.resolveAssignment(ctx, in.Plan, in.Assignment, RoleGoverningSupervisor)
	if err != nil {
		return SliceResult{}, err
	}
	slice := deterministicTask(in.Meta.OperationID, "slice")
	payload, err := canonicalJSON(candidateEventPayload{Epoch: string(in.Epoch), Slice: slice.String()})
	if err != nil {
		return SliceResult{}, fmt.Errorf("encode slice payload: %w", err)
	}
	event, err := epochTaskEvent(slice, "pasture.slice.created.v1", payload)
	if err != nil {
		return SliceResult{}, err
	}
	effects := []provenance.Effect{taskCreateEffect(slice, "implementation slice", provenance.PhaseWorkerSlices), edgeEffect(in.Plan, slice), event}
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationCreateSlice, struct {
		Plan       provenance.TaskID       `json:"plan"`
		Assignment provenance.AssignmentID `json:"assignment"`
	}{in.Plan, in.Assignment}, nil, effects)
	if err != nil {
		return SliceResult{}, err
	}
	return SliceResult{CommandResult: result, Slice: slice}, nil
}

func (s *epochAssignmentService) SetSliceCandidate(ctx context.Context, in SetSliceCandidateInput) (CandidateResult, error) {
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return CandidateResult{}, err
	}
	if err := validateRepositoryID(in.Repository); err != nil {
		return CandidateResult{}, err
	}
	if err := validateGitOID(in.Commit); err != nil {
		return CandidateResult{}, err
	}
	if _, err := s.tracker.prov.Show(in.Slice); err != nil {
		return CandidateResult{}, assignmentErr("SetSliceCandidate", fmt.Sprintf("slice %q could not be read", in.Slice), "a candidate must belong to an existing slice task", "supply an existing slice task")
	}
	resolution, err := s.resolveAssignment(ctx, in.Slice, in.Assignment, RoleOwnerResponsibility)
	if err != nil {
		return CandidateResult{}, err
	}
	candidate := deterministicTask(in.Meta.OperationID, "slice-candidate")
	state, err := newAssignmentSubjectStateEvidence(epoch, candidate, subjectStateCandidateCurrent, in.Meta.OperationID)
	if err != nil {
		return CandidateResult{}, err
	}
	stateEffect, err := newSubjectStateEvidenceEffect(candidate, state, reviewEvidenceResultSlot)
	if err != nil {
		return CandidateResult{}, err
	}
	payload, err := canonicalJSON(candidateEventPayload{Epoch: string(in.Epoch), Candidate: candidate.String(), Slice: in.Slice.String(), Repository: string(in.Repository), Commit: in.Commit})
	if err != nil {
		return CandidateResult{}, fmt.Errorf("encode slice-candidate payload: %w", err)
	}
	event, err := epochTaskEvent(candidate, candidateCreatedEventKind, payload)
	if err != nil {
		return CandidateResult{}, err
	}
	effects := []provenance.Effect{taskCreateEffect(candidate, "slice implementation candidate", provenance.PhaseWorkerSlices), stateEffect, event}
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationSetSliceCandidate, struct {
		Slice      provenance.TaskID       `json:"slice"`
		Repository RepositoryID            `json:"repository"`
		Commit     provenance.GitOID       `json:"commit"`
		Assignment provenance.AssignmentID `json:"assignment"`
	}{in.Slice, in.Repository, in.Commit, in.Assignment}, nil, effects)
	if err != nil {
		return CandidateResult{}, err
	}
	return CandidateResult{CommandResult: result, Slice: in.Slice, Candidate: ImplementationCandidateID(candidate.String())}, nil
}

func (s *epochAssignmentService) ReworkSlice(ctx context.Context, in ReworkSliceInput) (CandidateResult, error) {
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return CandidateResult{}, err
	}
	if err := validateReworkSubmission(in.Rework); err != nil {
		return CandidateResult{}, err
	}
	if err := validateRepositoryID(in.Replacement.Repository); err != nil {
		return CandidateResult{}, err
	}
	if err := validateGitOID(in.Replacement.Commit); err != nil {
		return CandidateResult{}, err
	}
	oldCandidate, err := provenance.ParseTaskID(string(in.Candidate))
	if err != nil {
		return CandidateResult{}, assignmentErr("ReworkSlice", fmt.Sprintf("candidate %q is malformed", in.Candidate), "rework must name an existing candidate task", "supply the candidate task identity returned by SetSliceCandidate")
	}
	resolution, err := s.resolveAssignment(ctx, in.Slice, in.Assignment, RoleOwnerResponsibility)
	if err != nil {
		return CandidateResult{}, err
	}
	oldState, err := s.currentCandidateState(epoch, oldCandidate)
	if err != nil {
		return CandidateResult{}, err
	}
	if oldState.state == subjectStateImplementationLanded || oldState.state == subjectStateReworked {
		return CandidateResult{}, assignmentErr("ReworkSlice", fmt.Sprintf("candidate %q is already terminal or reworked", in.Candidate), "rework requires the current live candidate", "use the replacement candidate or start a new slice")
	}
	newCandidate := deterministicTask(in.Meta.OperationID, "slice-candidate-replacement")
	newState, err := newAssignmentSubjectStateEvidence(epoch, newCandidate, subjectStateCandidateCurrent, in.Meta.OperationID)
	if err != nil {
		return CandidateResult{}, err
	}
	oldReworked, err := newAssignmentSubjectStateEvidence(epoch, oldCandidate, subjectStateReworked, in.Meta.OperationID)
	if err != nil {
		return CandidateResult{}, err
	}
	oldEffect, err := newSubjectStateEvidenceEffect(oldCandidate, oldReworked, reviewEvidenceResultSlot)
	if err != nil {
		return CandidateResult{}, err
	}
	newEffect, err := newSubjectStateEvidenceEffect(newCandidate, newState, reviewEvidenceResultSlotNext)
	if err != nil {
		return CandidateResult{}, err
	}
	effects := []provenance.Effect{taskCreateEffect(newCandidate, "replacement slice implementation candidate", provenance.PhaseWorkerSlices), oldEffect, newEffect}
	conditions := []provenance.Condition{oldState.conditionCurrent()}
	if review, reviewErr := s.currentReviewAuthority(oldCandidate); reviewErr == nil {
		invalidated, err := newInvalidatedReviewAuthority(in.Epoch, IntegrationCandidateSetID(oldCandidate.String()), review.value.Round, in.Meta.OperationID)
		if err != nil {
			return CandidateResult{}, err
		}
		invalidatedEffect, err := newReviewAuthorityEvidenceEffect(oldCandidate, invalidated, reviewEvidenceResultSlot)
		if err != nil {
			return CandidateResult{}, err
		}
		// Rework invalidates the review before publishing the replacement state.
		oldEffect, err = newSubjectStateEvidenceEffect(oldCandidate, oldReworked, reviewEvidenceResultSlotNext)
		if err != nil {
			return CandidateResult{}, err
		}
		newEffect, err = newSubjectStateEvidenceEffect(newCandidate, newState, "evidence-2")
		if err != nil {
			return CandidateResult{}, err
		}
		effects = []provenance.Effect{invalidatedEffect, taskCreateEffect(newCandidate, "replacement slice implementation candidate", provenance.PhaseWorkerSlices), oldEffect, newEffect}
		conditions = append(conditions, reviewAuthorityCurrentCondition(oldCandidate, review.journalID))
	}
	payload, err := canonicalJSON(struct {
		Slice       provenance.TaskID         `json:"slice"`
		Candidate   ImplementationCandidateID `json:"candidate"`
		Replacement ImplementationCandidateID `json:"replacement"`
		Rework      ReworkSubmission          `json:"rework"`
	}{in.Slice, in.Candidate, ImplementationCandidateID(newCandidate.String()), in.Rework})
	if err != nil {
		return CandidateResult{}, fmt.Errorf("encode slice rework payload: %w", err)
	}
	event, err := epochTaskEvent(oldCandidate, candidateReworkedEventKind, payload)
	if err != nil {
		return CandidateResult{}, err
	}
	effects = append(effects, event)
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationReworkSlice, struct {
		Slice       provenance.TaskID         `json:"slice"`
		Candidate   ImplementationCandidateID `json:"candidate"`
		Replacement ImplementationCandidateID `json:"replacement"`
	}{in.Slice, in.Candidate, ImplementationCandidateID(newCandidate.String())}, conditions, effects)
	if err != nil {
		return CandidateResult{}, err
	}
	return CandidateResult{CommandResult: result, Slice: in.Slice, Candidate: ImplementationCandidateID(newCandidate.String())}, nil
}

func (s *epochAssignmentService) CloseSlice(ctx context.Context, in CloseSliceInput) (CommandResult, error) {
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return CommandResult{}, err
	}
	candidate, err := provenance.ParseTaskID(string(in.Candidate))
	if err != nil {
		return CommandResult{}, assignmentErr("CloseSlice", fmt.Sprintf("candidate %q is malformed", in.Candidate), "slice close requires an existing implementation candidate", "supply the candidate task identity")
	}
	resolution, err := s.resolveAssignment(ctx, in.Slice, in.Assignment, RoleOwnerResponsibility)
	if err != nil {
		return CommandResult{}, err
	}
	state, err := s.currentCandidateState(epoch, candidate)
	if err != nil {
		return CommandResult{}, err
	}
	review, err := s.currentReviewAuthority(candidate)
	if err != nil {
		return CommandResult{}, err
	}
	if review.value.State != reviewFinalizedClean || review.value.Round != in.ReviewRound {
		return CommandResult{}, assignmentErr("CloseSlice", fmt.Sprintf("candidate %q does not have the requested clean review", in.Candidate), "closing a slice requires the current finalized clean review", "finalize the current review cleanly before closing")
	}
	conditions := []provenance.Condition{state.conditionCurrent(), reviewAuthorityCurrentCondition(candidate, review.journalID)}
	closed, err := MapMaterialEvent(TaskClosedEvent{Task: in.Slice, Reason: "clean implementation review"})
	if err != nil {
		return CommandResult{}, err
	}
	closed.ResultSlot = reviewEventResultSlot
	lifecycle := provenance.Effect{Sort: provenance.EffectTaskEvent, TaskID: in.Slice, EventKind: provenance.EventKindTaskClosed, CloseReason: "clean implementation review", ResultSlot: "lifecycle"}
	end := provenance.Effect{Sort: provenance.EffectAssignmentEnd, TaskID: in.Slice, AssignmentID: in.Assignment, SlotID: provenance.SlotOwnerResponsibility, ResultSlot: "assignment-end"}
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationCloseSlice, struct {
		Slice       provenance.TaskID         `json:"slice"`
		Candidate   ImplementationCandidateID `json:"candidate"`
		ReviewRound ReviewRoundID             `json:"review_round"`
	}{in.Slice, in.Candidate, in.ReviewRound}, conditions, []provenance.Effect{end, closed, lifecycle})
	if err != nil {
		return CommandResult{}, err
	}
	return result, nil
}

func (s *epochAssignmentService) CreateIntegrationCandidate(ctx context.Context, in CreateIntegrationCandidateInput) (IntegrationCandidateResult, error) {
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	if _, err := s.tracker.prov.Show(in.Plan); err != nil {
		return IntegrationCandidateResult{}, assignmentErr("CreateIntegrationCandidate", fmt.Sprintf("plan %q could not be read", in.Plan), "an integration candidate must be attached to an existing plan", "supply an existing plan task")
	}
	resolution, err := s.resolveAssignment(ctx, in.Plan, in.Assignment, RoleGoverningSupervisor)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	candidate := deterministicTask(in.Meta.OperationID, "integration-candidate")
	members := make([]candidateMember, len(in.Repositories))
	for i, repository := range in.Repositories {
		members[i] = candidateMember{Repository: repository.Repository, Candidate: repository.Candidate, Commit: repository.Commit}
	}
	manifest, err := newIntegrationCandidateManifest(in.Epoch, IntegrationCandidateSetID(candidate.String()), members, in.Meta.OperationID)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	manifestEffect, err := newCandidateManifestEvidenceEffect(candidate, manifest, reviewEvidenceResultSlot)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	state, err := newAssignmentSubjectStateEvidence(epoch, candidate, subjectStateCandidateCurrent, in.Meta.OperationID)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	stateEffect, err := newSubjectStateEvidenceEffect(candidate, state, reviewEvidenceResultSlotNext)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	payload, err := canonicalJSON(struct {
		Epoch     EpochRootID               `json:"epoch"`
		Plan      provenance.TaskID         `json:"plan"`
		Candidate IntegrationCandidateSetID `json:"candidate"`
	}{in.Epoch, in.Plan, IntegrationCandidateSetID(candidate.String())})
	if err != nil {
		return IntegrationCandidateResult{}, fmt.Errorf("encode integration-candidate payload: %w", err)
	}
	event, err := epochTaskEvent(candidate, candidateCreatedEventKind, payload)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	effects := []provenance.Effect{taskCreateEffect(candidate, "integration candidate set", provenance.PhaseImplUAT), manifestEffect, stateEffect, event}
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationCreateIntegrationCandidate, struct {
		Plan         provenance.TaskID     `json:"plan"`
		Repositories []RepositoryCandidate `json:"repositories"`
	}{in.Plan, in.Repositories}, nil, effects)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	return IntegrationCandidateResult{CommandResult: result, Candidate: IntegrationCandidateSetID(candidate.String())}, nil
}

func (s *epochAssignmentService) ReworkIntegrationCandidate(ctx context.Context, in ReworkIntegrationCandidateInput) (IntegrationCandidateResult, error) {
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	if err := validateReworkSubmission(in.Rework); err != nil {
		return IntegrationCandidateResult{}, err
	}
	oldCandidate, err := provenance.ParseTaskID(string(in.Candidate))
	if err != nil {
		return IntegrationCandidateResult{}, assignmentErr("ReworkIntegrationCandidate", fmt.Sprintf("candidate %q is malformed", in.Candidate), "candidate rework requires an existing candidate task", "supply the candidate task identity")
	}
	resolution, err := s.resolveAssignment(ctx, oldCandidate, in.Assignment, RoleGoverningSupervisor)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	oldState, err := s.currentCandidateState(epoch, oldCandidate)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	if oldState.state == subjectStateImplementationLanded || oldState.state == subjectStateReworked {
		return IntegrationCandidateResult{}, assignmentErr("ReworkIntegrationCandidate", fmt.Sprintf("candidate %q is already terminal or reworked", in.Candidate), "rework must invalidate the current live candidate exactly once", "use the replacement candidate")
	}
	oldManifest, err := s.exactCandidateManifest(epoch, oldCandidate)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	newCandidate := deterministicTask(in.Meta.OperationID, "integration-candidate-replacement")
	replacementMembers := make([]candidateMember, len(in.Replacement.Repositories))
	for i, repository := range in.Replacement.Repositories {
		replacementMembers[i] = candidateMember{Repository: repository.Repository, Candidate: repository.Candidate, Commit: repository.Commit}
	}
	manifest, err := newIntegrationCandidateManifest(in.Epoch, IntegrationCandidateSetID(newCandidate.String()), replacementMembers, in.Meta.OperationID)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	newManifestEffect, err := newCandidateManifestEvidenceEffect(newCandidate, manifest, "evidence-2")
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	oldReworked, err := newAssignmentSubjectStateEvidence(epoch, oldCandidate, subjectStateReworked, in.Meta.OperationID)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	oldStateEffect, err := newSubjectStateEvidenceEffect(oldCandidate, oldReworked, reviewEvidenceResultSlotNext)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	newState, err := newAssignmentSubjectStateEvidence(epoch, newCandidate, subjectStateCandidateCurrent, in.Meta.OperationID)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	newStateEffect, err := newSubjectStateEvidenceEffect(newCandidate, newState, "evidence-3")
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	conditions := []provenance.Condition{oldState.conditionCurrent(), candidateManifestExactCondition(oldCandidate, oldManifest.journalID)}
	effects := []provenance.Effect{taskCreateEffect(newCandidate, "replacement integration candidate set", provenance.PhaseImplUAT), oldStateEffect, newManifestEffect, newStateEffect}
	if review, reviewErr := s.currentReviewAuthority(oldCandidate); reviewErr == nil {
		invalidated, err := newInvalidatedReviewAuthority(in.Epoch, IntegrationCandidateSetID(oldCandidate.String()), review.value.Round, in.Meta.OperationID)
		if err != nil {
			return IntegrationCandidateResult{}, err
		}
		invalidatedEffect, err := newReviewAuthorityEvidenceEffect(oldCandidate, invalidated, reviewEvidenceResultSlot)
		if err != nil {
			return IntegrationCandidateResult{}, err
		}
		effects = append([]provenance.Effect{invalidatedEffect}, effects...)
		conditions = append(conditions, reviewAuthorityCurrentCondition(oldCandidate, review.journalID))
	}
	payload, err := canonicalJSON(struct {
		Candidate   IntegrationCandidateSetID `json:"candidate"`
		Replacement IntegrationCandidateSetID `json:"replacement"`
		Rework      ReworkSubmission          `json:"rework"`
	}{in.Candidate, IntegrationCandidateSetID(newCandidate.String()), in.Rework})
	if err != nil {
		return IntegrationCandidateResult{}, fmt.Errorf("encode integration rework payload: %w", err)
	}
	event, err := epochTaskEvent(oldCandidate, candidateReworkedEventKind, payload)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	effects = append(effects, event)
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationReworkIntegrationCandidate, struct {
		Candidate   IntegrationCandidateSetID `json:"candidate"`
		Replacement IntegrationCandidateSetID `json:"replacement"`
	}{in.Candidate, IntegrationCandidateSetID(newCandidate.String())}, conditions, effects)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	return IntegrationCandidateResult{CommandResult: result, Candidate: IntegrationCandidateSetID(newCandidate.String())}, nil
}

func (s *epochAssignmentService) PublishRepository(ctx context.Context, in PublishRepositoryInput) (PublicationResult, error) {
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return PublicationResult{}, err
	}
	candidate, err := provenance.ParseTaskID(string(in.Candidate))
	if err != nil {
		return PublicationResult{}, assignmentErr("PublishRepository", fmt.Sprintf("candidate %q is malformed", in.Candidate), "publication verification must target an existing integration candidate task", "supply the candidate task identity")
	}
	resolution, err := s.resolveAssignment(ctx, candidate, in.Assignment, RoleGoverningSupervisor)
	if err != nil {
		return PublicationResult{}, err
	}
	manifest, err := s.exactCandidateManifest(epoch, candidate)
	if err != nil {
		return PublicationResult{}, err
	}
	var member candidateMember
	foundMember := false
	for _, candidateMember := range manifest.value.Members {
		if candidateMember.Repository == in.Repository {
			member = candidateMember
			foundMember = true
			break
		}
	}
	if !foundMember || member.Commit != in.Commit {
		return PublicationResult{}, assignmentErr("PublishRepository", fmt.Sprintf("repository %q and commit %q are not an exact manifest member", in.Repository, in.Commit), "publication must verify one immutable candidate-manifest member", "publish the repository with the manifest's exact candidate and commit")
	}
	publications, err := s.currentCandidatePublicationSet(epoch, candidate)
	if err != nil {
		return PublicationResult{}, err
	}
	merged := append([]repositoryPublication(nil), publications.value.Publications...)
	updated := repositoryPublication{Repository: in.Repository, Candidate: member.Candidate, Ref: in.Ref, Commit: in.Commit, VerificationOperation: in.Meta.OperationID}
	replaced := false
	for i := range merged {
		if merged[i].Repository == in.Repository {
			merged[i] = updated
			replaced = true
			break
		}
	}
	if !replaced {
		merged = append(merged, updated)
	}
	publicationSet, err := newCandidatePublicationSet(in.Epoch, IntegrationCandidateSetID(candidate.String()), merged, in.Meta.OperationID)
	if err != nil {
		return PublicationResult{}, err
	}
	publicationEffect, err := newCandidatePublicationSetEvidenceEffect(candidate, publicationSet, reviewEvidenceResultSlot)
	if err != nil {
		return PublicationResult{}, err
	}
	verified, err := MapMaterialEvent(GitRemoteRefVerifiedEvent{Task: candidate, Repository: string(in.Repository), Ref: string(in.Ref), CommitOID: in.Commit})
	if err != nil {
		return PublicationResult{}, err
	}
	verified.ResultSlot = reviewEventResultSlot
	conditions := []provenance.Condition{candidateManifestExactCondition(candidate, manifest.journalID), candidatePublicationSetCurrentCondition(candidate, publications.journalID)}
	result, committed, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationPublishRepository, struct {
		Candidate  IntegrationCandidateSetID `json:"candidate"`
		Repository RepositoryID              `json:"repository"`
		Ref        GitRef                    `json:"ref"`
		Commit     provenance.GitOID         `json:"commit"`
	}{in.Candidate, in.Repository, in.Ref, in.Commit}, conditions, []provenance.Effect{publicationEffect, verified})
	if err != nil {
		return PublicationResult{}, err
	}
	return PublicationResult{CommandResult: result, Candidate: in.Candidate, Repository: in.Repository, Evidence: journalIDForSlot(committed, reviewEvidenceResultSlot)}, nil
}

func validateReworkSubmission(submission ReworkSubmission) error {
	if len(submission.Findings) == 0 {
		return assignmentErr("validateReworkSubmission", "the rework submission has no finding resolutions", "rework must account for the prior review findings", "supply one fixed or deferred resolution per finding")
	}
	for i, finding := range submission.Findings {
		if finding.Finding == (provenance.TaskID{}) || (finding.Outcome != FindingFixed && finding.Outcome != FindingDeferred) {
			return assignmentErr("validateReworkSubmission", fmt.Sprintf("finding resolution %d is incomplete", i), "finding dispositions are a closed typed union", "supply a non-zero finding and FindingFixed or FindingDeferred")
		}
		if finding.Outcome == FindingFixed && len(finding.Evidence) == 0 {
			return assignmentErr("validateReworkSubmission", fmt.Sprintf("fixed finding resolution %d has no evidence", i), "a fixed finding must cite its committed fix evidence", "supply at least one positive evidence journal id")
		}
		for _, evidence := range finding.Evidence {
			if evidence <= 0 {
				return assignmentErr("validateReworkSubmission", fmt.Sprintf("finding resolution %d cites non-positive evidence %d", i, evidence), "rework evidence must reference committed journal rows", "supply positive journal ids")
			}
		}
	}
	return nil
}

func (s *epochAssignmentService) currentCandidateState(epoch, candidate provenance.TaskID) (subjectStateSnapshot, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}}, Kinds: []provenance.EvidenceKind{candidateEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var current subjectStateSnapshot
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return subjectStateSnapshot{}, fmt.Errorf("query current candidate state for %q: %w", candidate, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil || *row.TaskID != candidate || row.ProducingOperationID == "" {
				return subjectStateSnapshot{}, assignmentErr("currentCandidateState", "a candidate state row has inconsistent metadata", "current-subject predicates require one exact task-scoped authority", "repair the malformed candidate state before retrying")
			}
			value, err := decodeSubjectStateEvidence(row.Payload, epoch, candidate, candidateEvidenceKind, row.ProducingOperationID)
			if err != nil {
				return subjectStateSnapshot{}, assignmentErr("currentCandidateState", fmt.Sprintf("candidate state for %q is malformed", candidate), "candidate operations consume the frozen source-discriminated state contract", "repair the malformed candidate state before retrying")
			}
			if row.JournalID > current.journalID {
				current = subjectStateSnapshot{subject: candidate, state: value.State, decision: value.Decision, decisionKind: value.DecisionKind, operation: value.Operation, source: value.Source, journalID: row.JournalID, family: candidateEvidenceKind}
			}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if current.journalID == 0 {
		return subjectStateSnapshot{}, assignmentErr("currentCandidateState", fmt.Sprintf("candidate %q has no current lifecycle state", candidate), "candidate operations require the assignment-produced current candidate state", "create the candidate through the assignment-controlled aggregate")
	}
	return current, nil
}

func (s *epochAssignmentService) exactCandidateManifest(epoch, candidate provenance.TaskID) (candidateManifestSnapshot, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}}, Kinds: []provenance.EvidenceKind{candidateManifestEvidenceKind}, Page: provenance.FactPageRequest{Limit: 2}}
	var result candidateManifestSnapshot
	count := 0
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return candidateManifestSnapshot{}, fmt.Errorf("query exact candidate manifest for %q: %w", candidate, err)
		}
		for _, row := range page.Rows {
			count++
			if count > 1 {
				return candidateManifestSnapshot{}, assignmentErr("exactCandidateManifest", fmt.Sprintf("candidate %q has multiple manifests", candidate), "candidate membership is immutable and exact", "create a replacement candidate instead of another manifest")
			}
			if row.TaskID == nil || *row.TaskID != candidate || row.ProducingOperationID == "" {
				return candidateManifestSnapshot{}, assignmentErr("exactCandidateManifest", "the manifest fact metadata is inconsistent", "publication must bind one exact candidate task and producer", "repair the candidate manifest before retrying")
			}
			value, err := decodeCandidateManifest(row.Payload)
			if err != nil || value.Epoch != EpochRootID(epoch.String()) || value.Candidate != IntegrationCandidateSetID(candidate.String()) || value.Operation != row.ProducingOperationID {
				return candidateManifestSnapshot{}, assignmentErr("exactCandidateManifest", fmt.Sprintf("candidate %q has malformed manifest authority", candidate), "publication requires the frozen immutable manifest contract", "repair the candidate manifest before retrying")
			}
			result = candidateManifestSnapshot{value: value, journalID: row.JournalID}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if count == 0 {
		return candidateManifestSnapshot{}, assignmentErr("exactCandidateManifest", fmt.Sprintf("candidate %q has no immutable manifest", candidate), "publication requires exact repository and commit membership", "create the integration candidate before publishing")
	}
	return result, nil
}

func (s *epochAssignmentService) currentCandidatePublicationSet(epoch, candidate provenance.TaskID) (candidatePublicationSetSnapshot, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}}, Kinds: []provenance.EvidenceKind{candidatePublicationSetEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var result candidatePublicationSetSnapshot
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return candidatePublicationSetSnapshot{}, fmt.Errorf("query current publication set for %q: %w", candidate, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil || *row.TaskID != candidate || row.ProducingOperationID == "" {
				return candidatePublicationSetSnapshot{}, assignmentErr("currentCandidatePublicationSet", "the publication fact metadata is inconsistent", "CurrentFact publication predicates require one exact candidate and producer", "repair the publication-set fact before retrying")
			}
			value, err := decodeCandidatePublicationSet(row.Payload)
			if err != nil || value.Epoch != EpochRootID(epoch.String()) || value.Candidate != IntegrationCandidateSetID(candidate.String()) || value.Operation != row.ProducingOperationID {
				return candidatePublicationSetSnapshot{}, assignmentErr("currentCandidatePublicationSet", fmt.Sprintf("candidate %q has malformed publication authority", candidate), "publication replacement must consume the frozen complete-snapshot contract", "repair the publication-set fact before retrying")
			}
			if row.JournalID > result.journalID {
				result = candidatePublicationSetSnapshot{value: value, journalID: row.JournalID}
			}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	return result, nil
}
