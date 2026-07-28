package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
)

const (
	assignmentPlanEpochBindingKind       provenance.EvidenceKind = "pasture.assignment.plan-epoch.v1"
	assignmentPlanSliceBindingKind       provenance.EvidenceKind = "pasture.assignment.plan-slice.v1"
	assignmentSliceCandidateBindingKind  provenance.EvidenceKind = "pasture.assignment.slice-candidate.v1"
	assignmentPlanIntegrationBindingKind provenance.EvidenceKind = "pasture.assignment.plan-integration-candidate.v1"
	assignmentBindingSlotPlan            provenance.ResultSlotID = "assignment-binding-plan"
	assignmentBindingSlotRelationship    provenance.ResultSlotID = "assignment-binding-relationship"
	assignmentBindingSlotCandidate       provenance.ResultSlotID = "assignment-binding-candidate"
)

func relationshipBinding(kind provenance.EvidenceKind, epoch EpochRootID, plan, slice, candidate provenance.TaskID, repository RepositoryID, commit provenance.GitOID, operation provenance.OperationID, members []candidateMember) assignmentBinding {
	return assignmentBinding{Kind: kind, Epoch: epoch, Plan: plan, Slice: slice, Candidate: candidate, Repository: repository, Commit: commit, Operation: operation, Members: append([]candidateMember(nil), members...)}
}

func (s *epochAssignmentService) exactAssignmentBindings(subject provenance.TaskID, kind provenance.EvidenceKind) ([]assignmentBinding, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, Kinds: []provenance.EvidenceKind{assignmentBindingEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var out []assignmentBinding
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return nil, fmt.Errorf("query assignment bindings for %q: %w", subject, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil || *row.TaskID != subject || row.ProducingOperationID == "" {
				return nil, assignmentErr("exactAssignmentBindings", "an assignment binding fact has inconsistent task or producer metadata", "graph ownership consumes exact task-scoped binding facts", "repair the malformed assignment binding before retrying")
			}
			var value assignmentBinding
			if err := strictJSON(row.Payload, &value); err != nil {
				return nil, fmt.Errorf("decode assignment binding for %q: %w", subject, err)
			}
			if value.Kind != kind || value.Operation != row.ProducingOperationID {
				continue
			}
			if _, err := epochTaskID(value.Epoch); err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	return out, nil
}

func (s *epochAssignmentService) subjectEpochBinding(subject provenance.TaskID, epoch EpochRootID) error {
	if _, err := epochTaskID(epoch); err != nil {
		return err
	}
	ancestors, err := s.tracker.prov.Descendants(subject)
	if err != nil {
		return fmt.Errorf("resolve bounded subject ancestry for %q: %w", subject, err)
	}
	owners := make([]provenance.TaskID, 0, len(ancestors)+1)
	owners = append(owners, subject)
	for _, ancestor := range ancestors {
		owners = append(owners, ancestor.ID)
	}
	matched := 0
	for _, owner := range owners {
		for _, kind := range []provenance.EvidenceKind{assignmentPlanEpochBindingKind, assignmentPlanSliceBindingKind, assignmentSliceCandidateBindingKind, assignmentPlanIntegrationBindingKind} {
			bindings, err := s.exactAssignmentBindings(owner, kind)
			if err != nil {
				return err
			}
			for _, binding := range bindings {
				if binding.Epoch != epoch {
					continue
				}
				related, err := s.bindingRelatesToSubject(owner, subject, binding)
				if err != nil {
					return err
				}
				if related {
					matched++
				}
			}
		}
	}
	if matched == 0 {
		return assignmentErr("subjectEpochBinding", fmt.Sprintf("subject %q has no exact durable relationship to epoch %q", subject, epoch), "assignment resolution cannot authorize an unrelated epoch and subject pair", "create the subject through the assignment aggregate for the requested epoch")
	}
	if matched != 1 {
		return assignmentErr("subjectEpochBinding", fmt.Sprintf("subject %q has %d exact relationships to epoch %q", subject, matched, epoch), "assignment resolution requires one unambiguous durable epoch relationship", "repair duplicate or conflicting graph bindings before retrying")
	}
	return nil
}

func (s *epochAssignmentService) bindingRelatesToSubject(owner, subject provenance.TaskID, binding assignmentBinding) (bool, error) {
	hasEdge := func(source, target provenance.TaskID) (bool, error) {
		edgeKind := provenance.EdgeBlockedBy
		edges, err := s.tracker.prov.Edges(source, &edgeKind)
		if err != nil {
			return false, fmt.Errorf("read epoch relationship edge %q -> %q: %w", source, target, err)
		}
		for _, edge := range edges {
			if edge.TargetID == target.String() {
				return true, nil
			}
		}
		return false, nil
	}
	planEpochBinding := func(plan provenance.TaskID) (bool, error) {
		bindings, err := s.exactAssignmentBindings(plan, assignmentPlanEpochBindingKind)
		if err != nil {
			return false, err
		}
		if len(bindings) != 1 {
			return false, nil
		}
		return bindings[0].Plan == plan && bindings[0].Epoch == binding.Epoch, nil
	}
	subjectEdge, err := hasEdge(owner, subject)
	if err != nil {
		return false, err
	}
	switch binding.Kind {
	case assignmentPlanEpochBindingKind:
		if owner != subject || binding.Plan != subject {
			return false, nil
		}
		return true, nil
	case assignmentPlanSliceBindingKind:
		if owner != binding.Plan || binding.Slice != subject || !subjectEdge {
			return false, nil
		}
		return planEpochBinding(binding.Plan)
	case assignmentSliceCandidateBindingKind:
		if owner != binding.Slice || binding.Candidate != subject || !subjectEdge {
			return false, nil
		}
		return planEpochBinding(binding.Plan)
	case assignmentPlanIntegrationBindingKind:
		if owner != binding.Plan || binding.Candidate != subject || !subjectEdge {
			return false, nil
		}
		return planEpochBinding(binding.Plan)
	default:
		return false, assignmentErr("bindingRelatesToSubject", fmt.Sprintf("binding kind %q is unknown", binding.Kind), "epoch membership consumes a closed relationship contract", "repair the assignment binding kind before retrying")
	}
}

func (s *epochAssignmentService) planEpochBinding(plan provenance.TaskID, epoch EpochRootID) (assignmentBinding, bool, error) {
	bindings, err := s.exactAssignmentBindings(plan, assignmentPlanEpochBindingKind)
	if err != nil {
		return assignmentBinding{}, false, err
	}
	if len(bindings) == 0 {
		return assignmentBinding{}, false, nil
	}
	if len(bindings) != 1 {
		return assignmentBinding{}, false, assignmentErr("planEpochBinding", fmt.Sprintf("plan %q has %d epoch bindings", plan, len(bindings)), "one plan must belong to one immutable epoch", "repair duplicate plan-epoch facts before retrying")
	}
	if bindings[0].Epoch != epoch {
		return assignmentBinding{}, false, assignmentErr("planEpochBinding", fmt.Sprintf("plan %q belongs to epoch %q, not %q", plan, bindings[0].Epoch, epoch), "a command cannot cross epoch ownership boundaries", "use the plan's owning epoch")
	}
	return bindings[0], true, nil
}

func (s *epochAssignmentService) sliceBinding(slice provenance.TaskID, epoch EpochRootID) (assignmentBinding, error) {
	ancestors, err := s.tracker.prov.Descendants(slice)
	if err != nil {
		return assignmentBinding{}, fmt.Errorf("resolve bounded slice ancestry for %q: %w", slice, err)
	}
	var matches []assignmentBinding
	for _, ancestor := range ancestors {
		bindings, err := s.exactAssignmentBindings(ancestor.ID, assignmentPlanSliceBindingKind)
		if err != nil {
			return assignmentBinding{}, err
		}
		for _, binding := range bindings {
			if binding.Slice == slice && binding.Epoch == epoch {
				matches = append(matches, binding)
			}
		}
	}
	if len(matches) != 1 {
		return assignmentBinding{}, assignmentErr("sliceBinding", fmt.Sprintf("slice %q has %d exact plan/epoch bindings", slice, len(matches)), "candidate operations require one durable slice ancestry", "create or repair the slice through the assignment aggregate")
	}
	return matches[0], nil
}

func (s *epochAssignmentService) sliceCandidateBinding(slice, candidate provenance.TaskID, epoch EpochRootID) (assignmentBinding, error) {
	bindings, err := s.exactAssignmentBindings(slice, assignmentSliceCandidateBindingKind)
	if err != nil {
		return assignmentBinding{}, err
	}
	var matches []assignmentBinding
	for _, binding := range bindings {
		if binding.Candidate == candidate && binding.Epoch == epoch {
			matches = append(matches, binding)
		}
	}
	if len(matches) != 1 {
		return assignmentBinding{}, assignmentErr("sliceCandidateBinding", fmt.Sprintf("slice %q has %d bindings for candidate %q", slice, len(matches), candidate), "candidate lifecycle commands require one exact slice binding", "use the candidate returned by the current slice")
	}
	return matches[0], nil
}

func (s *epochAssignmentService) candidateBelongsToPlan(candidate provenance.TaskID, epoch EpochRootID, plan provenance.TaskID) (assignmentBinding, assignmentBinding, error) {
	ancestors, err := s.tracker.prov.Descendants(candidate)
	if err != nil {
		return assignmentBinding{}, assignmentBinding{}, fmt.Errorf("resolve bounded candidate ancestry for %q: %w", candidate, err)
	}
	var matchSlice, matchCandidate assignmentBinding
	found := 0
	for _, ancestor := range ancestors {
		edges, err := s.tracker.prov.Edges(ancestor.ID, func() *provenance.EdgeKind { k := provenance.EdgeBlockedBy; return &k }())
		if err != nil {
			return assignmentBinding{}, assignmentBinding{}, fmt.Errorf("read candidate parent edges for %q: %w", candidate, err)
		}
		for _, edge := range edges {
			if edge.TargetID != candidate.String() {
				continue
			}
			sliceBinding, err := s.sliceBinding(ancestor.ID, epoch)
			if err != nil {
				continue
			}
			candidateBinding, err := s.sliceCandidateBinding(ancestor.ID, candidate, epoch)
			if err != nil {
				continue
			}
			if sliceBinding.Plan == plan {
				matchSlice, matchCandidate, found = sliceBinding, candidateBinding, found+1
			}
		}
	}
	if found != 1 {
		return assignmentBinding{}, assignmentBinding{}, assignmentErr("candidateBelongsToPlan", fmt.Sprintf("candidate %q has %d exact slice/plan memberships", candidate, found), "integration manifests may only consume candidates from their exact plan and epoch", "use a current candidate from a slice in the requested plan")
	}
	return matchSlice, matchCandidate, nil
}

func (s *epochAssignmentService) integrationBindingForCandidate(candidate provenance.TaskID, epoch EpochRootID) (provenance.TaskID, assignmentBinding, error) {
	ancestors, err := s.tracker.prov.Descendants(candidate)
	if err != nil {
		return provenance.TaskID{}, assignmentBinding{}, fmt.Errorf("resolve bounded integration ancestry for %q: %w", candidate, err)
	}
	var found []assignmentBinding
	var plan provenance.TaskID
	for _, ancestor := range ancestors {
		edges, err := s.tracker.prov.Edges(ancestor.ID, func() *provenance.EdgeKind { k := provenance.EdgeBlockedBy; return &k }())
		if err != nil {
			return provenance.TaskID{}, assignmentBinding{}, fmt.Errorf("read integration candidate edges for %q: %w", candidate, err)
		}
		for _, edge := range edges {
			if edge.TargetID != candidate.String() {
				continue
			}
			bindings, err := s.exactAssignmentBindings(ancestor.ID, assignmentPlanIntegrationBindingKind)
			if err != nil {
				return provenance.TaskID{}, assignmentBinding{}, err
			}
			for _, binding := range bindings {
				if binding.Candidate == candidate && binding.Epoch == epoch {
					found = append(found, binding)
					plan = ancestor.ID
				}
			}
		}
	}
	if len(found) != 1 {
		return provenance.TaskID{}, assignmentBinding{}, assignmentErr("integrationBindingForCandidate", fmt.Sprintf("candidate %q has %d exact plan bindings", candidate, len(found)), "integration rework requires one durable plan-to-candidate relationship", "use the current integration candidate for its owning plan")
	}
	return plan, found[0], nil
}

type candidateEventPayload struct {
	Epoch      string            `json:"epoch"`
	Candidate  string            `json:"candidate"`
	Slice      string            `json:"slice,omitempty"`
	Repository string            `json:"repository,omitempty"`
	Commit     provenance.GitOID `json:"commit,omitempty"`
}

func (s *epochAssignmentService) composedAssignmentReplay(in CommandMeta, epoch EpochRootID) (CommandResult, error) {
	operation := provenance.GovernedAllocationSupplementOperationID(in.OperationID)
	committed, err := s.tracker.prov.Journal().LookupCommitted(operation)
	if err != nil {
		return CommandResult{}, fmt.Errorf("lookup composed assignment supplement %q: %w", operation, err)
	}
	if committed.Kind != provenance.CommittedExact {
		return CommandResult{}, assignmentErr("composedAssignmentReplay", fmt.Sprintf("exact allocation replay %q has no exact supplemental receipt", in.OperationID), "a composed assignment command must preserve its command, event, evidence, and activity bindings", "repair the composed operation receipt before retrying")
	}
	return commandResultFromCommitted(in, epoch, committed)
}

func replacementAssignmentEvent(candidate provenance.TaskID, assignment provenance.AssignmentID, role AssignmentRole, occupant provenance.ActorID) (provenance.Effect, error) {
	payload, err := canonicalJSON(assignmentStartPayload{Assignment: string(assignment), Role: role.String(), Occupant: occupant.String()})
	if err != nil {
		return provenance.Effect{}, fmt.Errorf("encode replacement candidate assignment-start payload: %w", err)
	}
	event, err := epochTaskEvent(candidate, FamilyAssignmentStarted.EventKind(), payload)
	if err != nil {
		return provenance.Effect{}, err
	}
	event.ResultSlot = candidateAssignmentEventSlot
	return event, nil
}

func (s *epochAssignmentService) allocateReplacementComposed(ctx context.Context, meta CommandMeta, epoch EpochRootID, resolution assignmentResolution, mutation EpochMutationKind, payload any, candidate provenance.TaskID, assignment provenance.AssignmentID, title string, phase provenance.Phase, conditions []provenance.Condition, referencedDescendants []provenance.TaskID, effects []provenance.Effect) (CommandResult, error) {
	if resolution.id == "" || !resolution.role.valid() || resolution.occupant == (provenance.ActorID{}) || resolution.authority <= 0 || resolution.task == (provenance.TaskID{}) {
		return CommandResult{}, assignmentErr("allocateReplacementComposed", "the resolved replacement authority is incomplete", "governed replacement allocation requires one exact assignment, role, occupant, authority, and parent task", "resolve the command against one active parent assignment before allocating its replacement")
	}
	request, err := assignmentRequestCommand(mutation, epoch, payload)
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused replacement mutation %d command: %w", mutation, err)
	}
	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused replacement mutation %d payload: %w", mutation, err)
	}
	commandRecord, err := canonicalJSON(assignmentCommandRecord{Mutation: mutation, Epoch: epoch, Payload: payloadBytes, Request: request, Assignment: resolution.id, Role: resolution.role, Occupant: resolution.occupant, Authority: resolution.authority, Task: resolution.task})
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused replacement mutation %d authority: %w", mutation, err)
	}
	commandDigest := sha256.Sum256(commandRecord)
	effects = append([]provenance.Effect{{Sort: provenance.EffectEvidence, ResultSlot: assignmentCommandResultSlot, TaskID: resolution.task, EvidenceKind: assignmentCommandEvidenceKind, ContentDigest: commandDigest[:], Payload: commandRecord}}, effects...)
	activityID := provenance.ActivityID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("assignment-activity:"+string(meta.OperationID)))}
	effects = append(effects, provenance.Effect{Sort: provenance.EffectActivityCreate, ResultSlot: reviewActivityResultSlot, ActivityID: activityID, ActivityAgentID: resolution.occupant, ActivityPhase: phase, ActivityStage: provenance.StageComplete, ActivityNotes: "assignment-controlled epoch operation"})
	needed := make(map[provenance.ResultSlotID]bool)
	for _, effect := range effects {
		if effect.ResultSlot == "" {
			continue
		}
		if _, duplicate := needed[effect.ResultSlot]; duplicate {
			return CommandResult{}, assignmentErr("allocateReplacementComposed", fmt.Sprintf("replacement effects duplicate result slot %q", effect.ResultSlot), "every replacement fact, event, command, and activity requires one typed result binding", "assign a unique result slot to every supplemental effect")
		}
		needed[effect.ResultSlot] = false
	}

	if s.barrier != nil {
		if err := s.barrier.AfterPreflight(ctx, mutation); err != nil {
			return CommandResult{}, assignmentErr("allocateReplacementComposed", "the replacement operation was rejected at the injected pre-commit barrier", "the synchronization seam stopped the command before governed allocation", "retry after the competing operation has settled")
		}
	}
	allocator := s.tracker.allocationRunner
	if allocator == nil {
		return CommandResult{}, assignmentErr("allocateReplacementComposed", "the tracker has no engine-owned composed-allocation runner", "replacement candidates must be allocated with their state changes in the durable engine's transaction", "construct and launch the engine with this tracker before reworking a candidate")
	}
	composed := provenance.GovernedAllocationComposedRequest{
		Version: provenance.GovernedAllocationCompositionV1,
		Allocation: provenance.GovernedAllocationRequest{
			OperationID: meta.OperationID, ActorID: resolution.occupant, Command: fmt.Sprintf("pasture.epoch.mutation-%d.v1", mutation), ParentAssignmentID: resolution.id,
			Children: []provenance.GovernedChildSpec{{TaskID: candidate, AssignmentID: assignment, Occupant: resolution.occupant, Title: title, Description: title, Type: provenance.TaskTypeTask, Priority: provenance.PriorityMedium, Phase: phase}},
		},
		Conditions:          append([]provenance.Condition(nil), conditions...),
		SupplementalEffects: effects,
	}
	if len(referencedDescendants) > 0 {
		composed.ReferenceScope = provenance.GovernedAllocationReferenceScope{Kind: provenance.GovernedAllocationReferenceDescendants, Subjects: append([]provenance.TaskID(nil), referencedDescendants...)}
	}
	result, err := allocator.RunAllocateComposed(ctx, "pasture-candidate-rework:"+string(meta.OperationID), resolution.authority, composed)
	if err != nil {
		return CommandResult{}, fmt.Errorf("replacement mutation %d operation %q failed in its fused governed-allocation transaction; no partial allocation, state, review, graph, audit, or DBOS output committed: %w", mutation, meta.OperationID, err)
	}
	children := result.Closure().Children()
	if len(children) != 1 || children[0].TaskID != candidate || children[0].AssignmentID != assignment || children[0].Occupant != resolution.occupant {
		return CommandResult{}, assignmentErr("allocateReplacementComposed", "the composed result did not contain the exact replacement candidate, assignment, and occupant", "replacement commands return only their caller-stable governed child closure", "repair the composed receipt before retrying")
	}

	var activity provenance.ActivityID
	for _, slot := range result.SupplementalResultSlots() {
		if _, required := needed[slot.Slot]; required {
			needed[slot.Slot] = true
		}
		if slot.Slot == reviewActivityResultSlot && slot.ActivityID != nil {
			activity = *slot.ActivityID
		}
	}
	for slot, present := range needed {
		if !present {
			return CommandResult{}, assignmentErr("allocateReplacementComposed", fmt.Sprintf("the composed result omitted canonical slot %q", slot), "replacement results must bind every supplemental effect", "repair the composed receipt before retrying")
		}
	}
	if activity == (provenance.ActivityID{}) {
		return CommandResult{}, assignmentErr("allocateReplacementComposed", "the composed result omitted the replacement activity binding", "replacement command results must identify their complete activity", "repair the composed receipt before retrying")
	}
	return CommandResult{OperationID: meta.OperationID, Replayed: result.Replayed(), Epoch: epoch, ActivityID: activity, EventIDs: result.SupplementalEmittedEvents()}, nil
}

func (s *epochAssignmentService) CreateSlice(ctx context.Context, in CreateSliceInput) (SliceResult, error) {
	commandPayload := struct {
		Plan       provenance.TaskID       `json:"plan"`
		Assignment provenance.AssignmentID `json:"assignment"`
	}{in.Plan, in.Assignment}
	_, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationCreateSlice, in.Epoch, commandPayload)
	if err != nil {
		return SliceResult{}, err
	}
	if found {
		command, err := s.composedAssignmentReplay(in.Meta, in.Epoch)
		if err != nil {
			return SliceResult{}, err
		}
		return SliceResult{CommandResult: command, Slice: deterministicTask(in.Meta.OperationID, "slice")}, nil
	}
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return SliceResult{}, err
	}
	if _, err := s.tracker.prov.Show(in.Plan); err != nil {
		return SliceResult{}, assignmentErr("CreateSlice", fmt.Sprintf("plan %q could not be read", in.Plan), "a slice must be attached to an existing plan task", "supply an existing plan task")
	}
	resolution, err := s.resolveAssignment(ctx, in.Plan, in.Assignment, RoleGoverningSupervisor)
	if err != nil {
		return SliceResult{}, err
	}
	planBinding, hasPlanBinding, err := s.planEpochBinding(in.Plan, in.Epoch)
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
	event.ResultSlot = createSliceEventSlot
	assignmentPayload, err := canonicalJSON(assignmentStartPayload{Assignment: string(in.Meta.OperationID) + "-slice-owner", Role: RoleOwnerResponsibility.String(), Occupant: resolution.occupant.String()})
	if err != nil {
		return SliceResult{}, fmt.Errorf("encode child assignment-start payload: %w", err)
	}
	assignmentEvent, err := epochTaskEvent(slice, FamilyAssignmentStarted.EventKind(), assignmentPayload)
	if err != nil {
		return SliceResult{}, err
	}
	assignmentEvent.ResultSlot = createSliceAssignmentEventSlot
	effects := make([]provenance.Effect, 0, 8)
	supplementalOperation := provenance.GovernedAllocationSupplementOperationID(in.Meta.OperationID)
	if !hasPlanBinding || planBinding.Operation == supplementalOperation {
		bindingEffect, err := assignmentBindingEffect(in.Plan, relationshipBinding(assignmentPlanEpochBindingKind, in.Epoch, in.Plan, provenance.TaskID{}, provenance.TaskID{}, "", "", supplementalOperation, nil), assignmentBindingSlotPlan)
		if err != nil {
			return SliceResult{}, err
		}
		effects = append(effects, bindingEffect)
	} else if planBinding.Epoch != EpochRootID(epoch.String()) {
		return SliceResult{}, assignmentErr("CreateSlice", fmt.Sprintf("plan %q is bound to another epoch", in.Plan), "slice creation cannot cross epoch ancestry", "use the plan's exact owning epoch")
	}
	sliceBindingEffect, err := assignmentBindingEffect(in.Plan, relationshipBinding(assignmentPlanSliceBindingKind, in.Epoch, in.Plan, slice, provenance.TaskID{}, "", "", supplementalOperation, nil), assignmentBindingSlotRelationship)
	if err != nil {
		return SliceResult{}, err
	}
	effects = append(effects, edgeEffect(in.Plan, slice), sliceBindingEffect, event, assignmentEvent)
	return s.createSliceComposed(ctx, in, resolution, effects)
}

func (s *epochAssignmentService) SetSliceCandidate(ctx context.Context, in SetSliceCandidateInput) (CandidateResult, error) {
	commandPayload := struct {
		Slice      provenance.TaskID       `json:"slice"`
		Repository RepositoryID            `json:"repository"`
		Commit     provenance.GitOID       `json:"commit"`
		Assignment provenance.AssignmentID `json:"assignment"`
	}{in.Slice, in.Repository, in.Commit, in.Assignment}
	_, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationSetSliceCandidate, in.Epoch, commandPayload)
	if err != nil {
		return CandidateResult{}, err
	}
	if found {
		command, err := s.composedAssignmentReplay(in.Meta, in.Epoch)
		if err != nil {
			return CandidateResult{}, err
		}
		candidate := deterministicTask(in.Meta.OperationID, "slice-candidate")
		return CandidateResult{CommandResult: command, Slice: in.Slice, Candidate: ImplementationCandidateID(candidate.String())}, nil
	}
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
	sliceBinding, err := s.sliceBinding(in.Slice, in.Epoch)
	if err != nil {
		return CandidateResult{}, err
	}
	resolution, err := s.resolveAssignment(ctx, in.Slice, in.Assignment, RoleOwnerResponsibility)
	if err != nil {
		return CandidateResult{}, err
	}
	resolution, err = s.exactCandidateParentAuthority(ctx, resolution)
	if err != nil {
		return CandidateResult{}, err
	}
	candidate := deterministicTask(in.Meta.OperationID, "slice-candidate")
	supplementalOperation := provenance.GovernedAllocationSupplementOperationID(in.Meta.OperationID)
	state, err := newAssignmentSubjectStateEvidence(epoch, candidate, subjectStateCandidateCurrent, supplementalOperation)
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
	event.ResultSlot = candidateCreatedEventSlot
	childAssignment := provenance.AssignmentID(string(in.Meta.OperationID) + "-candidate-owner")
	assignmentPayload, err := canonicalJSON(assignmentStartPayload{Assignment: string(childAssignment), Role: RoleOwnerResponsibility.String(), Occupant: resolution.occupant.String()})
	if err != nil {
		return CandidateResult{}, fmt.Errorf("encode candidate assignment-start payload: %w", err)
	}
	assignmentEvent, err := epochTaskEvent(candidate, FamilyAssignmentStarted.EventKind(), assignmentPayload)
	if err != nil {
		return CandidateResult{}, err
	}
	assignmentEvent.ResultSlot = candidateAssignmentEventSlot
	bindingEffect, err := assignmentBindingEffect(in.Slice, relationshipBinding(assignmentSliceCandidateBindingKind, in.Epoch, sliceBinding.Plan, in.Slice, candidate, in.Repository, in.Commit, supplementalOperation, nil), assignmentBindingSlotCandidate)
	if err != nil {
		return CandidateResult{}, err
	}
	effects := []provenance.Effect{stateEffect, edgeEffect(in.Slice, candidate), bindingEffect, event, assignmentEvent}
	result, err := s.allocateCandidateComposed(ctx, in.Meta, in.Epoch, resolution, MutationSetSliceCandidate, commandPayload, candidate, childAssignment, "slice implementation candidate", provenance.PhaseWorkerSlices, effects)
	if err != nil {
		return CandidateResult{}, err
	}
	return CandidateResult{CommandResult: result, Slice: in.Slice, Candidate: ImplementationCandidateID(candidate.String())}, nil
}

func (s *epochAssignmentService) ReworkSlice(ctx context.Context, in ReworkSliceInput) (CandidateResult, error) {
	newCandidate := deterministicTask(in.Meta.OperationID, "slice-candidate-replacement")
	commandPayload := struct {
		Slice            provenance.TaskID         `json:"slice"`
		Candidate        ImplementationCandidateID `json:"candidate"`
		Replacement      ImplementationCandidateID `json:"replacement"`
		Assignment       provenance.AssignmentID   `json:"assignment"`
		ReplacementValue SliceCandidateReplacement `json:"replacement_value"`
		Rework           ReworkSubmission          `json:"rework"`
	}{in.Slice, in.Candidate, ImplementationCandidateID(newCandidate.String()), in.Assignment, in.Replacement, in.Rework}
	_, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationReworkSlice, in.Epoch, commandPayload)
	if err != nil {
		return CandidateResult{}, err
	}
	if found {
		command, err := s.composedAssignmentReplay(in.Meta, in.Epoch)
		if err != nil {
			return CandidateResult{}, err
		}
		return CandidateResult{CommandResult: command, Slice: in.Slice, Candidate: ImplementationCandidateID(newCandidate.String())}, nil
	}
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
	sliceBinding, err := s.sliceBinding(in.Slice, in.Epoch)
	if err != nil {
		return CandidateResult{}, err
	}
	oldBinding, err := s.sliceCandidateBinding(in.Slice, oldCandidate, in.Epoch)
	if err != nil {
		return CandidateResult{}, err
	}
	if oldBinding.Plan != sliceBinding.Plan || oldBinding.Slice != in.Slice {
		return CandidateResult{}, assignmentErr("ReworkSlice", fmt.Sprintf("candidate %q is not bound to slice %q", in.Candidate, in.Slice), "rework must remain within one exact slice graph", "use the slice's current candidate")
	}
	resolution, err := s.resolveAssignment(ctx, in.Slice, in.Assignment, RoleOwnerResponsibility)
	if err != nil {
		return CandidateResult{}, err
	}
	resolution, err = s.exactCandidateParentAuthority(ctx, resolution)
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
	supplementalOperation := provenance.GovernedAllocationSupplementOperationID(in.Meta.OperationID)
	newState, err := newAssignmentSubjectStateEvidence(epoch, newCandidate, subjectStateCandidateCurrent, supplementalOperation)
	if err != nil {
		return CandidateResult{}, err
	}
	oldReworked, err := newAssignmentSubjectStateEvidence(epoch, oldCandidate, subjectStateReworked, supplementalOperation)
	if err != nil {
		return CandidateResult{}, err
	}
	oldEffect, err := newSubjectStateEvidenceEffect(oldCandidate, oldReworked, reworkOldStateResultSlot)
	if err != nil {
		return CandidateResult{}, err
	}
	newEffect, err := newSubjectStateEvidenceEffect(newCandidate, newState, reworkNewStateResultSlot)
	if err != nil {
		return CandidateResult{}, err
	}
	replacementBinding, err := assignmentBindingEffect(in.Slice, relationshipBinding(assignmentSliceCandidateBindingKind, in.Epoch, sliceBinding.Plan, in.Slice, newCandidate, in.Replacement.Repository, in.Replacement.Commit, supplementalOperation, nil), reworkBindingResultSlot)
	if err != nil {
		return CandidateResult{}, err
	}
	conditions := []provenance.Condition{oldState.conditionCurrent()}
	review, err := s.validateReworkFindings(ctx, in.Epoch, oldCandidate, in.Rework)
	if err != nil {
		return CandidateResult{}, err
	}
	invalidated, err := newInvalidatedReviewAuthority(in.Epoch, IntegrationCandidateSetID(oldCandidate.String()), review.value.Round, supplementalOperation)
	if err != nil {
		return CandidateResult{}, err
	}
	invalidatedEffect, err := newReviewAuthorityEvidenceEffect(oldCandidate, invalidated, reworkReviewResultSlot)
	if err != nil {
		return CandidateResult{}, err
	}
	conditions = append(conditions, reviewAuthorityCurrentCondition(oldCandidate, review.journalID))
	round, err := s.currentReviewRoundAuthority(oldCandidate)
	if err != nil {
		return CandidateResult{}, err
	}
	wantRoundState := reviewRoundFinalizedClean
	if review.value.State == reviewFinalizedRevising {
		wantRoundState = reviewRoundFinalizedRevising
	}
	if round.value.Round != review.value.Round || round.value.Kind != SubjectImplementation || round.value.State != wantRoundState {
		return CandidateResult{}, assignmentErr("ReworkSlice", fmt.Sprintf("candidate %q has inconsistent current review and round authority", in.Candidate), "rework must invalidate one exact finalized implementation review", "repair or finalize the current review round before reworking")
	}
	invalidatedRound := reviewRoundAuthority{Epoch: in.Epoch, Round: round.value.Round, Subject: oldCandidate, Kind: SubjectImplementation, State: reviewRoundInvalidated, Operation: supplementalOperation}
	roundEffect, err := reviewRoundAuthorityEffect(oldCandidate, invalidatedRound, reworkRoundResultSlot)
	if err != nil {
		return CandidateResult{}, err
	}
	conditions = append(conditions, reviewRoundCurrentCondition(oldCandidate, round.journalID))
	payload, err := canonicalJSON(struct {
		Slice            provenance.TaskID         `json:"slice"`
		Candidate        ImplementationCandidateID `json:"candidate"`
		Replacement      ImplementationCandidateID `json:"replacement"`
		Repository       RepositoryID              `json:"repository"`
		Commit           provenance.GitOID         `json:"commit"`
		Assignment       provenance.AssignmentID   `json:"assignment"`
		ReplacementValue SliceCandidateReplacement `json:"replacement_value"`
		Rework           ReworkSubmission          `json:"rework"`
	}{in.Slice, in.Candidate, ImplementationCandidateID(newCandidate.String()), in.Replacement.Repository, in.Replacement.Commit, in.Assignment, in.Replacement, in.Rework})
	if err != nil {
		return CandidateResult{}, fmt.Errorf("encode slice rework payload: %w", err)
	}
	event, err := epochTaskEvent(oldCandidate, candidateReworkedEventKind, payload)
	if err != nil {
		return CandidateResult{}, err
	}
	event.ResultSlot = reviewEventResultSlot
	createdPayload, err := canonicalJSON(candidateEventPayload{Epoch: string(in.Epoch), Candidate: newCandidate.String(), Slice: in.Slice.String(), Repository: string(in.Replacement.Repository), Commit: in.Replacement.Commit})
	if err != nil {
		return CandidateResult{}, fmt.Errorf("encode replacement slice-candidate payload: %w", err)
	}
	createdEvent, err := epochTaskEvent(newCandidate, candidateCreatedEventKind, createdPayload)
	if err != nil {
		return CandidateResult{}, err
	}
	createdEvent.ResultSlot = candidateCreatedEventSlot
	childAssignment := provenance.AssignmentID(string(in.Meta.OperationID) + "-candidate-owner")
	assignmentEvent, err := replacementAssignmentEvent(newCandidate, childAssignment, RoleOwnerResponsibility, resolution.occupant)
	if err != nil {
		return CandidateResult{}, err
	}
	effects := []provenance.Effect{oldEffect, invalidatedEffect, roundEffect, newEffect, edgeEffect(in.Slice, newCandidate), replacementBinding, createdEvent, assignmentEvent, event}
	result, err := s.allocateReplacementComposed(ctx, in.Meta, in.Epoch, resolution, MutationReworkSlice, commandPayload, newCandidate, childAssignment, "replacement slice implementation candidate", provenance.PhaseWorkerSlices, conditions, []provenance.TaskID{oldCandidate}, effects)
	if err != nil {
		return CandidateResult{}, err
	}
	return CandidateResult{CommandResult: result, Slice: in.Slice, Candidate: ImplementationCandidateID(newCandidate.String())}, nil
}

func (s *epochAssignmentService) CloseSlice(ctx context.Context, in CloseSliceInput) (CommandResult, error) {
	replay, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationCloseSlice, in.Epoch, struct {
		Slice       provenance.TaskID         `json:"slice"`
		Candidate   ImplementationCandidateID `json:"candidate"`
		ReviewRound ReviewRoundID             `json:"review_round"`
		Assignment  provenance.AssignmentID   `json:"assignment"`
	}{in.Slice, in.Candidate, in.ReviewRound, in.Assignment})
	if err != nil {
		return CommandResult{}, err
	}
	if found {
		return commandResultFromCommitted(in.Meta, in.Epoch, replay)
	}
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return CommandResult{}, err
	}
	if _, err := s.sliceBinding(in.Slice, in.Epoch); err != nil {
		return CommandResult{}, err
	}
	candidate, err := provenance.ParseTaskID(string(in.Candidate))
	if err != nil {
		return CommandResult{}, assignmentErr("CloseSlice", fmt.Sprintf("candidate %q is malformed", in.Candidate), "slice close requires an existing implementation candidate", "supply the candidate task identity")
	}
	if _, err := s.sliceCandidateBinding(in.Slice, candidate, in.Epoch); err != nil {
		return CommandResult{}, err
	}
	resolution, err := s.resolveAssignment(ctx, in.Slice, in.Assignment, RoleOwnerResponsibility)
	if err != nil {
		return CommandResult{}, err
	}
	state, err := s.currentCandidateState(epoch, candidate)
	if err != nil {
		return CommandResult{}, err
	}
	if state.state != subjectStateCandidateCurrent {
		return CommandResult{}, assignmentErr("CloseSlice", fmt.Sprintf("candidate %q is in ineligible lifecycle state %q", in.Candidate, state.state), "reworked or landed candidates cannot close their former slice", "close the slice while its reviewed candidate is current, or use the current replacement candidate")
	}
	review, err := s.currentReviewAuthority(candidate)
	if err != nil {
		return CommandResult{}, err
	}
	if review.value.State != reviewFinalizedClean || review.value.Round != in.ReviewRound {
		return CommandResult{}, assignmentErr("CloseSlice", fmt.Sprintf("candidate %q does not have the requested clean review", in.Candidate), "closing a slice requires the current finalized clean review", "finalize the current review cleanly before closing")
	}
	round, err := s.currentReviewRoundAuthority(candidate)
	if err != nil {
		return CommandResult{}, err
	}
	if round.value.Round != in.ReviewRound || round.value.State != reviewRoundFinalizedClean {
		return CommandResult{}, assignmentErr("CloseSlice", fmt.Sprintf("candidate %q does not have the requested current clean round", in.Candidate), "slice close requires one current clean review-round authority", "finalize the current review cleanly before closing")
	}
	conditions := []provenance.Condition{state.conditionCurrent(), reviewAuthorityCurrentCondition(candidate, review.journalID), reviewRoundCurrentCondition(candidate, round.journalID)}
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
		Assignment  provenance.AssignmentID   `json:"assignment"`
	}{in.Slice, in.Candidate, in.ReviewRound, in.Assignment}, conditions, []provenance.Effect{end, closed, lifecycle})
	if err != nil {
		return CommandResult{}, err
	}
	return result, nil
}

func (s *epochAssignmentService) CreateIntegrationCandidate(ctx context.Context, in CreateIntegrationCandidateInput) (IntegrationCandidateResult, error) {
	commandPayload := struct {
		Plan         provenance.TaskID       `json:"plan"`
		Repositories []RepositoryCandidate   `json:"repositories"`
		Assignment   provenance.AssignmentID `json:"assignment"`
	}{in.Plan, in.Repositories, in.Assignment}
	_, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationCreateIntegrationCandidate, in.Epoch, commandPayload)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	if found {
		command, err := s.composedAssignmentReplay(in.Meta, in.Epoch)
		if err != nil {
			return IntegrationCandidateResult{}, err
		}
		candidate := deterministicTask(in.Meta.OperationID, "integration-candidate")
		return IntegrationCandidateResult{CommandResult: command, Candidate: IntegrationCandidateSetID(candidate.String())}, nil
	}
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	if _, err := s.tracker.prov.Show(in.Plan); err != nil {
		return IntegrationCandidateResult{}, assignmentErr("CreateIntegrationCandidate", fmt.Sprintf("plan %q could not be read", in.Plan), "an integration candidate must be attached to an existing plan", "supply an existing plan task")
	}
	planBinding, hasPlanBinding, err := s.planEpochBinding(in.Plan, in.Epoch)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	if hasPlanBinding && planBinding.Epoch != in.Epoch {
		return IntegrationCandidateResult{}, assignmentErr("CreateIntegrationCandidate", fmt.Sprintf("plan %q belongs to another epoch", in.Plan), "integration candidates cannot cross epoch ancestry", "use the plan's exact owning epoch")
	}
	resolution, err := s.resolveAssignment(ctx, in.Plan, in.Assignment, RoleGoverningSupervisor)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	resolution, err = s.exactCandidateParentAuthority(ctx, resolution)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	candidate := deterministicTask(in.Meta.OperationID, "integration-candidate")
	supplementalOperation := provenance.GovernedAllocationSupplementOperationID(in.Meta.OperationID)
	members := make([]candidateMember, len(in.Repositories))
	for i, repository := range in.Repositories {
		members[i] = candidateMember{Repository: repository.Repository, Candidate: repository.Candidate, Commit: repository.Commit}
		member, err := provenance.ParseTaskID(string(repository.Candidate))
		if err != nil {
			return IntegrationCandidateResult{}, assignmentErr("CreateIntegrationCandidate", fmt.Sprintf("member candidate %q is malformed", repository.Candidate), "an integration member must identify an existing slice candidate", "supply the candidate returned by SetSliceCandidate")
		}
		_, candidateBinding, err := s.candidateBelongsToPlan(member, in.Epoch, in.Plan)
		if err != nil {
			return IntegrationCandidateResult{}, err
		}
		if candidateBinding.Repository != repository.Repository || candidateBinding.Commit != repository.Commit {
			return IntegrationCandidateResult{}, assignmentErr("CreateIntegrationCandidate", fmt.Sprintf("member candidate %q does not match repository %q and commit %q", repository.Candidate, repository.Repository, repository.Commit), "integration membership must preserve the immutable slice-candidate repository and commit", "use the repository and commit returned for that candidate")
		}
	}
	manifest, err := newIntegrationCandidateManifest(in.Epoch, IntegrationCandidateSetID(candidate.String()), members, supplementalOperation)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	manifestEffect, err := newCandidateManifestEvidenceEffect(candidate, manifest, reviewEvidenceResultSlot)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	state, err := newAssignmentSubjectStateEvidence(epoch, candidate, subjectStateCandidateCurrent, supplementalOperation)
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
	event.ResultSlot = candidateCreatedEventSlot
	childAssignment := provenance.AssignmentID(string(in.Meta.OperationID) + "-candidate-owner")
	assignmentPayload, err := canonicalJSON(assignmentStartPayload{Assignment: string(childAssignment), Role: RoleGoverningSupervisor.String(), Occupant: resolution.occupant.String()})
	if err != nil {
		return IntegrationCandidateResult{}, fmt.Errorf("encode integration candidate assignment-start payload: %w", err)
	}
	assignmentEvent, err := epochTaskEvent(candidate, FamilyAssignmentStarted.EventKind(), assignmentPayload)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	assignmentEvent.ResultSlot = candidateAssignmentEventSlot
	effects := make([]provenance.Effect, 0, 10)
	if !hasPlanBinding || planBinding.Operation == supplementalOperation {
		bindingEffect, err := assignmentBindingEffect(in.Plan, relationshipBinding(assignmentPlanEpochBindingKind, in.Epoch, in.Plan, provenance.TaskID{}, provenance.TaskID{}, "", "", supplementalOperation, nil), assignmentBindingSlotPlan)
		if err != nil {
			return IntegrationCandidateResult{}, err
		}
		effects = append(effects, bindingEffect)
	}
	integrationBinding, err := assignmentBindingEffect(in.Plan, relationshipBinding(assignmentPlanIntegrationBindingKind, in.Epoch, in.Plan, provenance.TaskID{}, candidate, "", "", supplementalOperation, members), assignmentBindingSlotRelationship)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	effects = append(effects, manifestEffect, stateEffect, edgeEffect(in.Plan, candidate), integrationBinding, event, assignmentEvent)
	result, err := s.allocateCandidateComposed(ctx, in.Meta, in.Epoch, resolution, MutationCreateIntegrationCandidate, commandPayload, candidate, childAssignment, "integration candidate set", provenance.PhaseImplUAT, effects)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	return IntegrationCandidateResult{CommandResult: result, Candidate: IntegrationCandidateSetID(candidate.String())}, nil
}

func (s *epochAssignmentService) ReworkIntegrationCandidate(ctx context.Context, in ReworkIntegrationCandidateInput) (IntegrationCandidateResult, error) {
	newCandidate := deterministicTask(in.Meta.OperationID, "integration-candidate-replacement")
	commandPayload := struct {
		Candidate        IntegrationCandidateSetID       `json:"candidate"`
		Replacement      IntegrationCandidateSetID       `json:"replacement"`
		Assignment       provenance.AssignmentID         `json:"assignment"`
		ReplacementValue IntegrationCandidateReplacement `json:"replacement_value"`
		Rework           ReworkSubmission                `json:"rework"`
	}{in.Candidate, IntegrationCandidateSetID(newCandidate.String()), in.Assignment, in.Replacement, in.Rework}
	_, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationReworkIntegrationCandidate, in.Epoch, commandPayload)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	if found {
		command, err := s.composedAssignmentReplay(in.Meta, in.Epoch)
		if err != nil {
			return IntegrationCandidateResult{}, err
		}
		return IntegrationCandidateResult{CommandResult: command, Candidate: IntegrationCandidateSetID(newCandidate.String())}, nil
	}
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
	plan, _, err := s.integrationBindingForCandidate(oldCandidate, in.Epoch)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	resolution, err := s.resolveAssignment(ctx, oldCandidate, in.Assignment, RoleGoverningSupervisor)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	resolution, err = s.exactCandidateParentAuthority(ctx, resolution)
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
	replacementMembers := make([]candidateMember, len(in.Replacement.Repositories))
	for i, repository := range in.Replacement.Repositories {
		replacementMembers[i] = candidateMember{Repository: repository.Repository, Candidate: repository.Candidate, Commit: repository.Commit}
		member, err := provenance.ParseTaskID(string(repository.Candidate))
		if err != nil {
			return IntegrationCandidateResult{}, assignmentErr("ReworkIntegrationCandidate", fmt.Sprintf("replacement member candidate %q is malformed", repository.Candidate), "replacement membership must name existing slice candidates", "supply candidate identities from the exact plan")
		}
		_, candidateBinding, err := s.candidateBelongsToPlan(member, in.Epoch, plan)
		if err != nil {
			return IntegrationCandidateResult{}, err
		}
		if candidateBinding.Repository != repository.Repository || candidateBinding.Commit != repository.Commit {
			return IntegrationCandidateResult{}, assignmentErr("ReworkIntegrationCandidate", fmt.Sprintf("replacement member candidate %q does not match repository %q and commit %q", repository.Candidate, repository.Repository, repository.Commit), "replacement membership must preserve immutable slice-candidate identity", "use the repository and commit returned for that candidate")
		}
	}
	supplementalOperation := provenance.GovernedAllocationSupplementOperationID(in.Meta.OperationID)
	manifest, err := newIntegrationCandidateManifest(in.Epoch, IntegrationCandidateSetID(newCandidate.String()), replacementMembers, supplementalOperation)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	newManifestEffect, err := newCandidateManifestEvidenceEffect(newCandidate, manifest, reworkManifestResultSlot)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	oldReworked, err := newAssignmentSubjectStateEvidence(epoch, oldCandidate, subjectStateReworked, supplementalOperation)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	oldStateEffect, err := newSubjectStateEvidenceEffect(oldCandidate, oldReworked, reworkOldStateResultSlot)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	newState, err := newAssignmentSubjectStateEvidence(epoch, newCandidate, subjectStateCandidateCurrent, supplementalOperation)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	newStateEffect, err := newSubjectStateEvidenceEffect(newCandidate, newState, reworkNewStateResultSlot)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	conditions := []provenance.Condition{oldState.conditionCurrent(), candidateManifestExactCondition(oldCandidate, oldManifest.journalID)}
	replacementBinding, err := assignmentBindingEffect(plan, relationshipBinding(assignmentPlanIntegrationBindingKind, in.Epoch, plan, provenance.TaskID{}, newCandidate, "", "", supplementalOperation, replacementMembers), reworkBindingResultSlot)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	review, err := s.validateReworkFindings(ctx, in.Epoch, oldCandidate, in.Rework)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	invalidated, err := newInvalidatedReviewAuthority(in.Epoch, IntegrationCandidateSetID(oldCandidate.String()), review.value.Round, supplementalOperation)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	invalidatedEffect, err := newReviewAuthorityEvidenceEffect(oldCandidate, invalidated, reworkReviewResultSlot)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	conditions = append(conditions, reviewAuthorityCurrentCondition(oldCandidate, review.journalID))
	round, err := s.currentReviewRoundAuthority(oldCandidate)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	wantRoundState := reviewRoundFinalizedClean
	if review.value.State == reviewFinalizedRevising {
		wantRoundState = reviewRoundFinalizedRevising
	}
	if round.value.Round != review.value.Round || round.value.Kind != SubjectImplementation || round.value.State != wantRoundState {
		return IntegrationCandidateResult{}, assignmentErr("ReworkIntegrationCandidate", fmt.Sprintf("candidate %q has inconsistent current review and round authority", in.Candidate), "rework must invalidate one exact finalized implementation review", "repair or finalize the current review round before reworking")
	}
	invalidatedRound := reviewRoundAuthority{Epoch: in.Epoch, Round: round.value.Round, Subject: oldCandidate, Kind: SubjectImplementation, State: reviewRoundInvalidated, Operation: supplementalOperation}
	roundEffect, err := reviewRoundAuthorityEffect(oldCandidate, invalidatedRound, reworkRoundResultSlot)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	conditions = append(conditions, reviewRoundCurrentCondition(oldCandidate, round.journalID))
	payload, err := canonicalJSON(struct {
		Candidate    IntegrationCandidateSetID `json:"candidate"`
		Replacement  IntegrationCandidateSetID `json:"replacement"`
		Repositories []RepositoryCandidate     `json:"repositories"`
		Assignment   provenance.AssignmentID   `json:"assignment"`
		Rework       ReworkSubmission          `json:"rework"`
	}{in.Candidate, IntegrationCandidateSetID(newCandidate.String()), in.Replacement.Repositories, in.Assignment, in.Rework})
	if err != nil {
		return IntegrationCandidateResult{}, fmt.Errorf("encode integration rework payload: %w", err)
	}
	event, err := epochTaskEvent(oldCandidate, candidateReworkedEventKind, payload)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	event.ResultSlot = reviewEventResultSlot
	createdPayload, err := canonicalJSON(struct {
		Epoch     EpochRootID               `json:"epoch"`
		Plan      provenance.TaskID         `json:"plan"`
		Candidate IntegrationCandidateSetID `json:"candidate"`
	}{in.Epoch, plan, IntegrationCandidateSetID(newCandidate.String())})
	if err != nil {
		return IntegrationCandidateResult{}, fmt.Errorf("encode replacement integration-candidate payload: %w", err)
	}
	createdEvent, err := epochTaskEvent(newCandidate, candidateCreatedEventKind, createdPayload)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	createdEvent.ResultSlot = candidateCreatedEventSlot
	childAssignment := provenance.AssignmentID(string(in.Meta.OperationID) + "-candidate-owner")
	assignmentEvent, err := replacementAssignmentEvent(newCandidate, childAssignment, RoleGoverningSupervisor, resolution.occupant)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	effects := []provenance.Effect{oldStateEffect, invalidatedEffect, roundEffect, newManifestEffect, newStateEffect, edgeEffect(plan, newCandidate), replacementBinding, createdEvent, assignmentEvent, event}
	result, err := s.allocateReplacementComposed(ctx, in.Meta, in.Epoch, resolution, MutationReworkIntegrationCandidate, commandPayload, newCandidate, childAssignment, "replacement integration candidate set", provenance.PhaseImplUAT, conditions, nil, effects)
	if err != nil {
		return IntegrationCandidateResult{}, err
	}
	return IntegrationCandidateResult{CommandResult: result, Candidate: IntegrationCandidateSetID(newCandidate.String())}, nil
}

func (s *epochAssignmentService) PublishRepository(ctx context.Context, in PublishRepositoryInput) (PublicationResult, error) {
	replay, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationPublishRepository, in.Epoch, struct {
		Candidate  IntegrationCandidateSetID `json:"candidate"`
		Repository RepositoryID              `json:"repository"`
		Ref        GitRef                    `json:"ref"`
		Commit     provenance.GitOID         `json:"commit"`
		Assignment provenance.AssignmentID   `json:"assignment"`
	}{in.Candidate, in.Repository, in.Ref, in.Commit, in.Assignment})
	if err != nil {
		return PublicationResult{}, err
	}
	if found {
		command, err := commandResultFromCommitted(in.Meta, in.Epoch, replay)
		if err != nil {
			return PublicationResult{}, err
		}
		return PublicationResult{CommandResult: command, Candidate: in.Candidate, Repository: in.Repository, Evidence: journalIDForSlot(replay, reviewEvidenceResultSlot)}, nil
	}
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
	plan, _, err := s.integrationBindingForCandidate(candidate, in.Epoch)
	if err != nil {
		return PublicationResult{}, err
	}
	state, err := s.currentCandidateState(epoch, candidate)
	if err != nil {
		return PublicationResult{}, err
	}
	if state.state != subjectStateCandidateCurrent {
		return PublicationResult{}, assignmentErr("PublishRepository", fmt.Sprintf("candidate %q is not current", in.Candidate), "publication is only valid for the current candidate state", "publish the current replacement candidate")
	}
	for _, manifestMember := range manifest.value.Members {
		memberTask, err := provenance.ParseTaskID(string(manifestMember.Candidate))
		if err != nil {
			return PublicationResult{}, assignmentErr("PublishRepository", fmt.Sprintf("manifest member %q is malformed", manifestMember.Candidate), "publication members must identify canonical candidate tasks", "repair the candidate manifest before publishing")
		}
		_, candidateBinding, err := s.candidateBelongsToPlan(memberTask, in.Epoch, plan)
		if err != nil {
			return PublicationResult{}, err
		}
		if candidateBinding.Repository != manifestMember.Repository || candidateBinding.Commit != manifestMember.Commit {
			return PublicationResult{}, assignmentErr("PublishRepository", fmt.Sprintf("manifest member %q does not match its slice-candidate repository and commit", manifestMember.Candidate), "publication must preserve immutable candidate membership", "repair the integration manifest before publishing")
		}
		memberState, err := s.currentCandidateState(epoch, memberTask)
		if err != nil {
			return PublicationResult{}, err
		}
		if memberState.state != subjectStateCandidateCurrent {
			return PublicationResult{}, assignmentErr("PublishRepository", fmt.Sprintf("manifest member %q is not current", manifestMember.Candidate), "publication requires every manifest member to be current", "rework the stale member before publishing")
		}
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
	conditions := []provenance.Condition{candidateStateCurrentCondition(candidate, state.journalID), candidateManifestExactCondition(candidate, manifest.journalID), candidatePublicationSetCurrentCondition(candidate, publications.journalID)}
	result, committed, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationPublishRepository, struct {
		Candidate  IntegrationCandidateSetID `json:"candidate"`
		Repository RepositoryID              `json:"repository"`
		Ref        GitRef                    `json:"ref"`
		Commit     provenance.GitOID         `json:"commit"`
		Assignment provenance.AssignmentID   `json:"assignment"`
	}{in.Candidate, in.Repository, in.Ref, in.Commit, in.Assignment}, conditions, []provenance.Effect{publicationEffect, verified})
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

func (s *epochAssignmentService) validateReworkFindings(ctx context.Context, epoch EpochRootID, candidate provenance.TaskID, submission ReworkSubmission) (reviewAuthoritySnapshot, error) {
	review, err := s.currentReviewAuthority(candidate)
	if err != nil {
		return reviewAuthoritySnapshot{}, err
	}
	if review.value.State != reviewFinalizedClean && review.value.State != reviewFinalizedRevising {
		return reviewAuthoritySnapshot{}, assignmentErr("validateReworkFindings", fmt.Sprintf("candidate %q has no finalized review finding set", candidate), "rework must resolve the exact closed review findings", "finalize an implementation review before reworking")
	}
	start, err := s.findReviewStart(ctx, epoch, review.value.Round)
	if err != nil {
		return reviewAuthoritySnapshot{}, err
	}
	axisEvents, err := s.reviewAxisEvents(ctx, start)
	if err != nil {
		return reviewAuthoritySnapshot{}, err
	}
	wanted := map[provenance.TaskID]struct{}{}
	for i, axisEvent := range axisEvents {
		axisTask := start.axisTasks[i]
		query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}}, Kinds: []provenance.EvidenceKind{reviewSubmissionEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
		found := false
		for {
			page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
			if err != nil {
				return reviewAuthoritySnapshot{}, fmt.Errorf("query review finding evidence for %q: %w", candidate, err)
			}
			for _, row := range page.Rows {
				var envelope struct {
					Epoch      EpochRootID        `json:"epoch"`
					Round      ReviewRoundID      `json:"round"`
					Axis       ReviewAxis         `json:"axis"`
					Actor      provenance.ActorID `json:"actor"`
					Kind       SubjectKind        `json:"kind"`
					Submission json.RawMessage    `json:"submission"`
				}
				if err := strictJSON(row.Payload, &envelope); err != nil {
					return reviewAuthoritySnapshot{}, fmt.Errorf("decode review finding evidence: %w", err)
				}
				if envelope.Epoch != epoch || envelope.Round != review.value.Round || envelope.Axis != ReviewAxis(i+1) || envelope.Kind != SubjectImplementation || envelope.Actor != axisEvent.actor || row.ProducingOperationID != axisEvent.operation {
					continue
				}
				var implementation ImplementationReviewSubmission
				if err := strictJSON(envelope.Submission, &implementation); err != nil {
					return reviewAuthoritySnapshot{}, fmt.Errorf("decode implementation findings: %w", err)
				}
				for _, finding := range implementation.Findings {
					wanted[finding.Task] = struct{}{}
				}
				found = true
			}
			if page.Next == nil {
				break
			}
			query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
			query.Page.AfterJournalID = page.Next.AfterJournalID
		}
		if !found {
			return reviewAuthoritySnapshot{}, assignmentErr("validateReworkFindings", fmt.Sprintf("axis %q has no closed finding submission", canonicalReviewAxes()[i]), "the exact prior finding set must be materialized before rework", "repair the finalized review submission before reworking")
		}
		_ = axisTask
	}
	seen := map[provenance.TaskID]struct{}{}
	for i, resolution := range submission.Findings {
		if _, duplicate := seen[resolution.Finding]; duplicate {
			return reviewAuthoritySnapshot{}, assignmentErr("validateReworkFindings", fmt.Sprintf("finding %q is disposed more than once", resolution.Finding), "rework requires one disposition per finding", fmt.Sprintf("remove duplicate disposition %d", i))
		}
		seen[resolution.Finding] = struct{}{}
		if _, expected := wanted[resolution.Finding]; !expected {
			return reviewAuthoritySnapshot{}, assignmentErr("validateReworkFindings", fmt.Sprintf("finding %q is not in the finalized review", resolution.Finding), "rework cannot add or substitute findings", "dispose only the exact prior finding identities")
		}
		if resolution.Outcome == FindingFixed {
			for _, evidence := range resolution.Evidence {
				if !s.evidenceJournalRowExists(evidence) {
					return reviewAuthoritySnapshot{}, assignmentErr("validateReworkFindings", fmt.Sprintf("fixed finding %q cites missing evidence %d", resolution.Finding, evidence), "fixed dispositions must cite committed evidence rows", "cite a committed evidence journal row produced by the fix")
				}
			}
		}
	}
	if len(seen) != len(wanted) {
		return reviewAuthoritySnapshot{}, assignmentErr("validateReworkFindings", fmt.Sprintf("rework disposes %d of %d finalized findings", len(seen), len(wanted)), "every prior finding requires exactly one disposition", "add the missing finding dispositions")
	}
	return review, nil
}

func (s *epochAssignmentService) evidenceJournalRowExists(id provenance.JournalID) bool {
	if id <= 0 {
		return false
	}
	for _, kind := range []provenance.EvidenceKind{candidateEvidenceKind, implementationReviewAuthorityEvidenceKind, candidateManifestEvidenceKind, candidatePublicationSetEvidenceKind, reviewSubmissionEvidenceKind, reviewAxisSubmissionEvidenceKind, assignmentBindingEvidenceKind, assignmentCommandEvidenceKind} {
		query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}}, Kinds: []provenance.EvidenceKind{kind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
		for {
			page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
			if err != nil {
				return false
			}
			for _, row := range page.Rows {
				if row.JournalID == id {
					return true
				}
			}
			if page.Next == nil {
				break
			}
			query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
			query.Page.AfterJournalID = page.Next.AfterJournalID
		}
	}
	return false
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
