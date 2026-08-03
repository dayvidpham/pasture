package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
)

type reviewStartedPayload struct {
	Epoch      string     `json:"epoch"`
	Round      string     `json:"round"`
	Subject    string     `json:"subject"`
	Kind       string     `json:"kind"`
	AxisTasks  []string   `json:"axis_tasks"`
	AxisGroups [][]string `json:"axis_groups,omitempty"`
}

type reviewStartedRecord struct {
	eventID   provenance.JournalID
	epoch     EpochRootID
	round     ReviewRoundID
	subject   provenance.TaskID
	kind      SubjectKind
	axisTasks [3]provenance.TaskID
	groups    [3][3]provenance.TaskID
	graph     reviewRoundGraph
}

type reviewRecordedPayload struct {
	AxisTask string `json:"axis_task"`
	Kind     string `json:"kind"`
	Verdict  string `json:"verdict"`
}

type reviewAxisEvidenceRecord struct {
	Epoch      EpochRootID             `json:"epoch"`
	Round      ReviewRoundID           `json:"round"`
	Subject    provenance.TaskID       `json:"subject"`
	Axis       ReviewAxis              `json:"axis"`
	AxisTask   provenance.TaskID       `json:"axis_task"`
	Verdict    Verdict                 `json:"verdict"`
	Assignment provenance.AssignmentID `json:"assignment"`
	Actor      provenance.ActorID      `json:"actor"`
	Operation  provenance.OperationID  `json:"operation"`
	journalID  provenance.JournalID
}

type reviewRoundState uint8

const (
	reviewRoundInvalid reviewRoundState = iota
	reviewRoundStarted
	reviewRoundFinalizedClean
	reviewRoundFinalizedRevising
	reviewRoundInvalidated
)

type reviewRoundAuthority struct {
	Epoch      EpochRootID             `json:"epoch"`
	Round      ReviewRoundID           `json:"round"`
	Subject    provenance.TaskID       `json:"subject"`
	Kind       SubjectKind             `json:"kind"`
	State      reviewRoundState        `json:"state"`
	Graph      reviewRoundGraph        `json:"graph"`
	AxisEvents [3]provenance.JournalID `json:"axis_events"`
	Operation  provenance.OperationID  `json:"operation"`
}

const (
	reviewRoundAuthorityEvidenceKind provenance.EvidenceKind = "pasture.review.round-authority.v1"
	reviewAxisSubmissionEvidenceKind provenance.EvidenceKind = "pasture.review.axis-submission.v1"
	reviewRoundAuthorityResultSlot   provenance.ResultSlotID = "review-round-authority"
	reviewAxisSubmissionResultSlot   provenance.ResultSlotID = "review-axis-submission"
)

func reviewRoundAuthorityEffect(subject provenance.TaskID, value reviewRoundAuthority, slot provenance.ResultSlotID) (provenance.Effect, error) {
	if err := validateReviewRoundAuthority(value, subject); err != nil {
		return provenance.Effect{}, err
	}
	payload, err := canonicalJSON(value)
	if err != nil {
		return provenance.Effect{}, fmt.Errorf("encode review-round authority: %w", err)
	}
	digest := sha256.Sum256(payload)
	return provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: slot, TaskID: subject, EvidenceKind: reviewRoundAuthorityEvidenceKind, ContentDigest: digest[:], Payload: payload}, nil
}

func validateReviewRoundAuthority(value reviewRoundAuthority, subject provenance.TaskID) error {
	if subject == (provenance.TaskID{}) || value.Subject != subject {
		return assignmentErr("validateReviewRoundAuthority", "the review-round authority is not bound to its subject", "current review authority must identify one exact subject", "construct the authority from the reviewed task")
	}
	if _, err := epochTaskID(value.Epoch); err != nil {
		return err
	}
	if value.Round == "" || !value.Kind.valid() || value.Operation == "" {
		return assignmentErr("validateReviewRoundAuthority", "the review-round authority is incomplete", "current review authority must identify one epoch, round, kind, state, and producer", "construct the authority from the started round")
	}
	if err := provenance.ValidateOperationID(value.Operation); err != nil {
		return assignmentErr("validateReviewRoundAuthority", fmt.Sprintf("the review-round authority operation %q is malformed", value.Operation), "current review authority requires a stable producer identity", "repair the authority with its valid producing operation")
	}
	switch value.State {
	case reviewRoundStarted:
		if value.AxisEvents != [3]provenance.JournalID{} {
			return assignmentErr("validateReviewRoundAuthority", "a started review round carries axis events", "started rounds have no finalized axis history", "clear the axis event identities before persisting the round state")
		}
		if err := value.Graph.validate(value.Kind); err != nil {
			return err
		}
	case reviewRoundFinalizedClean, reviewRoundFinalizedRevising:
		if err := value.Graph.validate(value.Kind); err != nil {
			return err
		}
		seen := map[provenance.JournalID]struct{}{}
		for _, event := range value.AxisEvents {
			if event <= 0 {
				return assignmentErr("validateReviewRoundAuthority", "a finalized review round has a missing axis event", "finalized rounds require three distinct committed axis events", "persist all canonical axis event identities")
			}
			if _, duplicate := seen[event]; duplicate {
				return assignmentErr("validateReviewRoundAuthority", "a finalized review round repeats an axis event", "each canonical axis must have one distinct committed event", "persist the correct event identity for each axis")
			}
			seen[event] = struct{}{}
		}
	case reviewRoundInvalidated:
		if value.AxisEvents != [3]provenance.JournalID{} {
			return assignmentErr("validateReviewRoundAuthority", "an invalidated review round carries axis events", "invalidated rounds no longer expose finalized axis authority", "clear the axis event identities before persisting invalidation")
		}
		if value.Graph != (reviewRoundGraph{}) {
			if err := value.Graph.validate(value.Kind); err != nil {
				return err
			}
		}
	default:
		return assignmentErr("validateReviewRoundAuthority", "the review-round authority state is unknown", "review rounds have a closed lifecycle", "use started, finalized-clean, finalized-revising, or invalidated")
	}
	return nil
}

func reviewAxisSubmissionEffect(subject, axisTask provenance.TaskID, start reviewStartedRecord, axis ReviewAxis, verdict Verdict, operation provenance.OperationID, assignment provenance.AssignmentID, actor provenance.ActorID, slot provenance.ResultSlotID) (provenance.Effect, error) {
	if !axis.valid() || !verdict.valid() || assignment == "" || actor == (provenance.ActorID{}) {
		return provenance.Effect{}, assignmentErr("reviewAxisSubmissionEffect", "the axis submission authority is incomplete", "one typed submission requires a canonical axis, verdict, assignment, and actor", "construct the submission from the resolved axis assignment")
	}
	if err := provenance.ValidateOperationID(operation); err != nil {
		return provenance.Effect{}, assignmentErr("reviewAxisSubmissionEffect", fmt.Sprintf("operation %q is malformed", operation), "axis submission authority requires a stable producer", "supply a valid operation identity")
	}
	if subject != start.subject || start.graph[axis-1].Task != axisTask {
		return provenance.Effect{}, assignmentErr("reviewAxisSubmissionEffect", "the axis submission does not match its started-round graph", "axis authority is exact for one round, subject, and generated axis task", "use the canonical axis binding from the current started round")
	}
	value := reviewAxisEvidenceRecord{Epoch: start.epoch, Round: start.round, Subject: subject, Axis: axis, AxisTask: axisTask, Verdict: verdict, Assignment: assignment, Actor: actor, Operation: operation}
	payload, err := canonicalJSON(value)
	if err != nil {
		return provenance.Effect{}, fmt.Errorf("encode review-axis authority: %w", err)
	}
	digest := sha256.Sum256(payload)
	return provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: slot, TaskID: axisTask, EvidenceKind: reviewAxisSubmissionEvidenceKind, ContentDigest: digest[:], Payload: payload}, nil
}

func reviewRoundCurrentCondition(subject provenance.TaskID, asserted provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionCurrentFact, Selector: provenance.FactSelector{Kind: provenance.FactEvidence, Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, EvidenceKind: reviewRoundAuthorityEvidenceKind}, AssertedJournalID: asserted}
}

func reviewAxisCurrentCondition(axisTask provenance.TaskID, asserted provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionCurrentFact, Selector: provenance.FactSelector{Kind: provenance.FactEvidence, Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: axisTask}}, EvidenceKind: reviewAxisSubmissionEvidenceKind}, AssertedJournalID: asserted}
}

func reviewAxisExactCondition(axisTask provenance.TaskID, operation provenance.OperationID, asserted provenance.JournalID) provenance.Condition {
	return provenance.Condition{Kind: provenance.ConditionExactFact, Selector: provenance.FactSelector{Kind: provenance.FactEvidence, Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: axisTask}, OperationIDs: []provenance.OperationID{operation}}, EvidenceKind: reviewAxisSubmissionEvidenceKind}, AssertedJournalID: asserted}
}

func reviewFindingGraphEffect(graph reviewRoundGraph, axis ReviewAxis, finding ReviewFinding) (provenance.Effect, error) {
	if !axis.valid() || finding.Task == (provenance.TaskID{}) || !finding.Severity.valid() {
		return provenance.Effect{}, assignmentErr("reviewFindingGraphEffect", "the implementation finding graph binding is incomplete", "one typed finding requires a canonical axis, severity, and existing task identity", "supply a validated implementation finding")
	}
	if err := graph.validate(SubjectImplementation); err != nil {
		return provenance.Effect{}, err
	}
	group := graph[axis-1].Groups[finding.Severity-1]
	return provenance.Effect{Sort: provenance.EffectEdgeAdd, TaskID: group.Task, EdgeTargetID: finding.Task.String(), EdgeRelKind: provenance.EdgeBlockedBy}, nil
}

type reviewRoundSnapshot struct {
	value     reviewRoundAuthority
	journalID provenance.JournalID
}

type reviewAuthorityAbsentError struct{ subject provenance.TaskID }

func (e *reviewAuthorityAbsentError) Error() string {
	return fmt.Sprintf("candidate %q has no current implementation review authority", e.subject)
}

type reviewRoundAbsentError struct{ subject provenance.TaskID }

func (e *reviewRoundAbsentError) Error() string {
	return fmt.Sprintf("subject %q has no current review-round authority", e.subject)
}

func (s *epochAssignmentService) currentReviewRoundAuthority(subject provenance.TaskID) (reviewRoundSnapshot, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, Kinds: []provenance.EvidenceKind{reviewRoundAuthorityEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var current reviewRoundSnapshot
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return reviewRoundSnapshot{}, fmt.Errorf("query current review-round authority for %q: %w", subject, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil || *row.TaskID != subject || row.ProducingOperationID == "" {
				return reviewRoundSnapshot{}, assignmentErr("currentReviewRoundAuthority", "a review-round authority row has inconsistent metadata", "round authority must be one exact subject-scoped producer", "repair the malformed review-round fact before retrying")
			}
			var value reviewRoundAuthority
			if err := strictJSON(row.Payload, &value); err != nil {
				return reviewRoundSnapshot{}, assignmentErr("currentReviewRoundAuthority", fmt.Sprintf("review-round authority for %q is malformed", subject), "review commands require the typed current-round contract", "repair the malformed review-round fact before retrying")
			}
			if err := validateReviewRoundAuthority(value, subject); err != nil || (value.Operation != row.ProducingOperationID && provenance.GovernedAllocationSupplementOperationID(value.Operation) != row.ProducingOperationID) {
				return reviewRoundSnapshot{}, assignmentErr("currentReviewRoundAuthority", fmt.Sprintf("review-round authority for %q is malformed", subject), "review commands require the typed current-round contract", "repair the malformed review-round fact before retrying")
			}
			if current.journalID == 0 || row.JournalID > current.journalID {
				current = reviewRoundSnapshot{value: value, journalID: row.JournalID}
			}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if current.journalID == 0 {
		return reviewRoundSnapshot{}, &reviewRoundAbsentError{subject: subject}
	}
	return current, nil
}

func (s *epochAssignmentService) StartReview(ctx context.Context, in StartReviewInput) (ReviewStartResult, error) {
	subjectTask, err := provenance.ParseTaskID(in.Subject.SnapshotID)
	if err != nil {
		return ReviewStartResult{}, assignmentErr("StartReview", fmt.Sprintf("review subject %q is malformed", in.Subject.SnapshotID), "review subjects must identify an existing Provenance task", "supply the canonical subject task identity")
	}
	kind := SubjectPlan
	if in.Subject.Kind == ReviewSubjectImplementationCandidate {
		kind = SubjectImplementation
	} else if in.Subject.Kind != ReviewSubjectDocumentRevision {
		return ReviewStartResult{}, assignmentErr("StartReview", "the review subject kind is unknown", "review start accepts only document revisions and implementation candidates", "derive a valid ReviewSubjectRef from a concrete subject")
	}
	_, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationStartReview, in.Epoch, struct {
		Subject ReviewSubjectRef `json:"subject"`
		Kind    SubjectKind      `json:"kind"`
	}{in.Subject, kind})
	if err != nil {
		return ReviewStartResult{}, err
	}
	if found {
		supplement, lookupErr := s.tracker.prov.Journal().LookupCommitted(provenance.GovernedAllocationSupplementOperationID(in.Meta.OperationID))
		if lookupErr != nil || supplement.Kind != provenance.CommittedExact {
			return ReviewStartResult{}, assignmentErr("StartReview", fmt.Sprintf("exact replay %q has no committed supplemental receipt", in.Meta.OperationID), "a governed review replay must preserve its events and activity", "repair the governed operation output before retrying")
		}
		command, err := commandResultFromCommitted(in.Meta, in.Epoch, supplement)
		if err != nil {
			return ReviewStartResult{}, err
		}
		return ReviewStartResult{CommandResult: command, Round: ReviewRoundID(deterministicTask(in.Meta.OperationID, "review-round").String()), Subject: in.Subject}, nil
	}
	if _, err := s.tracker.prov.Show(subjectTask); err != nil {
		return ReviewStartResult{}, assignmentErr("StartReview", fmt.Sprintf("review subject %q could not be read", in.Subject.SnapshotID), "a review can only start for an existing subject", "supply an existing plan or implementation candidate")
	}
	plan, err := PlanReviewRound(subjectTask, in.Subject, kind)
	if err != nil {
		return ReviewStartResult{}, err
	}
	var resolution assignmentResolution
	bindInitialPlan := false
	if kind == SubjectPlan {
		_, bound, bindingErr := s.planEpochBinding(subjectTask, in.Epoch)
		if bindingErr != nil {
			return ReviewStartResult{}, bindingErr
		}
		if !bound {
			resolution, err = s.resolveUniqueAssignment(ctx, []provenance.TaskID{subjectTask}, RoleGoverningSupervisor)
			bindInitialPlan = err == nil
		} else {
			var scope []provenance.TaskID
			scope, err = s.assignmentScope(subjectTask, in.Epoch)
			if err == nil {
				resolution, err = s.resolveUniqueAssignment(ctx, scope, RoleGoverningSupervisor)
			}
		}
	} else {
		var scope []provenance.TaskID
		scope, err = s.assignmentScope(subjectTask, in.Epoch)
		if err == nil {
			resolution, err = s.resolveUniqueAssignment(ctx, scope, RoleGoverningSupervisor)
		}
	}
	if err != nil {
		return ReviewStartResult{}, err
	}
	roundTask := deterministicTask(in.Meta.OperationID, "review-round")
	axisTasks := [3]provenance.TaskID{
		deterministicTask(in.Meta.OperationID, "axis-correctness"),
		deterministicTask(in.Meta.OperationID, "axis-test-quality"),
		deterministicTask(in.Meta.OperationID, "axis-elegance"),
	}
	planByHandle := map[string]provenance.TaskID{reviewedTaskHandle: subjectTask, plan.RoundHandle: roundTask}
	for i, handle := range plan.AxisHandles {
		planByHandle[handle] = axisTasks[i]
	}
	for _, task := range plan.Tasks {
		if _, ok := planByHandle[task.Handle]; !ok {
			planByHandle[task.Handle] = deterministicTask(in.Meta.OperationID, task.Handle)
		}
	}
	effects := make([]provenance.Effect, 0, len(plan.Edges)+4)
	if bindInitialPlan {
		binding, bindingErr := assignmentBindingEffect(subjectTask, relationshipBinding(assignmentPlanEpochBindingKind, in.Epoch, subjectTask, provenance.TaskID{}, provenance.TaskID{}, "", "", provenance.GovernedAllocationSupplementOperationID(in.Meta.OperationID), nil), assignmentBindingSlotPlan)
		if bindingErr != nil {
			return ReviewStartResult{}, bindingErr
		}
		effects = append(effects, binding)
	}
	for _, edge := range plan.Edges {
		from := subjectTask
		if edge.From != reviewedTaskHandle {
			from = planByHandle[edge.From]
		}
		to := planByHandle[edge.To]
		if edge.Relation == RelationSubject || edge.Relation == RelationContains {
			effects = append(effects, provenance.Effect{Sort: provenance.EffectEdgeAdd, TaskID: from, EdgeTargetID: to.String(), EdgeRelKind: provenance.EdgeDerivedFrom})
		} else {
			effects = append(effects, edgeEffect(from, to))
		}
	}
	round := ReviewRoundID(roundTask.String())
	axisGroups := make([][]string, 3)
	var severityGroups [3][3]provenance.TaskID
	if kind == SubjectImplementation {
		for i, axis := range plan.AxisHandles {
			for j, severity := range canonicalFindingSeverities() {
				handle := axis + ".group-" + severity.String()
				group, ok := planByHandle[handle]
				if !ok {
					return ReviewStartResult{}, assignmentErr("StartReview", fmt.Sprintf("the review plan omits the %s %s severity group", canonicalReviewAxes()[i], severity), "implementation review allocation requires all nine canonical severity groups", "repair the deterministic review-round plan before retrying")
				}
				severityGroups[i][j] = group
				axisGroups[i] = append(axisGroups[i], group.String())
			}
		}
	}
	graph, err := newReviewRoundGraph(kind, axisTasks, severityGroups)
	if err != nil {
		return ReviewStartResult{}, err
	}
	startPayload, err := canonicalJSON(reviewStartedPayload{Epoch: string(in.Epoch), Round: string(round), Subject: subjectTask.String(), Kind: kind.String(), AxisTasks: []string{axisTasks[0].String(), axisTasks[1].String(), axisTasks[2].String()}, AxisGroups: axisGroups})
	if err != nil {
		return ReviewStartResult{}, fmt.Errorf("encode review-start payload: %w", err)
	}
	startEvent, err := epochTaskEvent(subjectTask, reviewStartedEventKind, startPayload)
	if err != nil {
		return ReviewStartResult{}, err
	}
	effects = append(effects, startEvent)
	startAuthority, err := reviewRoundAuthorityEffect(subjectTask, reviewRoundAuthority{Epoch: in.Epoch, Round: round, Subject: subjectTask, Kind: kind, State: reviewRoundStarted, Graph: graph, Operation: in.Meta.OperationID}, reviewRoundAuthorityResultSlot)
	if err != nil {
		return ReviewStartResult{}, err
	}
	effects = append(effects, startAuthority)
	var conditions []provenance.Condition
	currentRound, roundErr := s.currentReviewRoundAuthority(subjectTask)
	if roundErr != nil {
		var absent *reviewRoundAbsentError
		if !errors.As(roundErr, &absent) {
			return ReviewStartResult{}, roundErr
		}
		conditions = append(conditions, reviewRoundCurrentCondition(subjectTask, 0))
	} else {
		if currentRound.value.Epoch != in.Epoch {
			return ReviewStartResult{}, assignmentErr("StartReview", fmt.Sprintf("subject %q has a current review in epoch %q, not %q", subjectTask, currentRound.value.Epoch, in.Epoch), "a subject cannot be reviewed across epoch ownership boundaries", "start the review in the subject's owning epoch")
		}
		if currentRound.value.State == reviewRoundStarted {
			return ReviewStartResult{}, assignmentErr("StartReview", fmt.Sprintf("subject %q already has current started review round %q", subjectTask, currentRound.value.Round), "a subject may have only one concurrent review round", "submit and finalize or invalidate the current round before starting another")
		}
		conditions = append(conditions, reviewRoundCurrentCondition(subjectTask, currentRound.journalID))
	}
	if kind == SubjectImplementation {
		authority, err := newReviewStartedAuthority(in.Epoch, IntegrationCandidateSetID(subjectTask.String()), round, in.Meta.OperationID)
		if err != nil {
			return ReviewStartResult{}, err
		}
		authorityEffect, err := newReviewAuthorityEvidenceEffect(subjectTask, authority, reviewEvidenceResultSlot)
		if err != nil {
			return ReviewStartResult{}, err
		}
		effects = append(effects, authorityEffect)
	}
	result, err := s.allocateReviewBatch(ctx, in, resolution, plan, planByHandle, struct {
		Subject ReviewSubjectRef `json:"subject"`
		Kind    SubjectKind      `json:"kind"`
	}{in.Subject, kind}, conditions, effects)
	if err != nil {
		return ReviewStartResult{}, err
	}
	return ReviewStartResult{CommandResult: result, Round: round, Subject: in.Subject}, nil
}

func (s *epochAssignmentService) allocateReviewBatch(ctx context.Context, in StartReviewInput, resolution assignmentResolution, plan ReviewRoundPlan, ids map[string]provenance.TaskID, payload any, conditions []provenance.Condition, effects []provenance.Effect) (CommandResult, error) {
	request, err := assignmentRequestCommand(MutationStartReview, in.Epoch, payload)
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused StartReview command: %w", err)
	}
	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused StartReview payload: %w", err)
	}
	record, err := canonicalJSON(assignmentCommandRecord{Mutation: MutationStartReview, Epoch: in.Epoch, Payload: payloadBytes, Request: request, Assignment: resolution.id, Role: resolution.role, Occupant: resolution.occupant, Authority: resolution.authority, Task: resolution.task})
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused StartReview authority: %w", err)
	}
	digest := sha256.Sum256(record)
	effects = append([]provenance.Effect{{Sort: provenance.EffectEvidence, ResultSlot: assignmentCommandResultSlot, TaskID: resolution.task, EvidenceKind: assignmentCommandEvidenceKind, ContentDigest: digest[:], Payload: record}}, effects...)
	activityID := provenance.ActivityID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("assignment-activity:"+string(in.Meta.OperationID)))}
	effects = append(effects, provenance.Effect{Sort: provenance.EffectActivityCreate, ResultSlot: reviewActivityResultSlot, ActivityID: activityID, ActivityAgentID: resolution.occupant, ActivityPhase: provenance.PhaseReview, ActivityStage: provenance.StageComplete, ActivityNotes: "assignment-controlled epoch operation"})
	children := make([]provenance.GovernedChildSpec, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		title := "review round"
		if task.Kind == ReviewTaskAxis {
			title = "review axis " + task.Axis.String()
		} else if task.Kind == ReviewTaskGroup {
			title = "review " + task.Axis.String() + " " + task.Severity.String() + " findings"
		}
		children = append(children, provenance.GovernedChildSpec{TaskID: ids[task.Handle], AssignmentID: provenance.AssignmentID(string(in.Meta.OperationID) + "-" + task.Handle), Occupant: resolution.occupant, Title: title, Description: title, Type: provenance.TaskTypeTask, Priority: provenance.PriorityMedium, Phase: provenance.PhaseReview})
	}
	allocator := s.tracker.allocationRunner
	if allocator == nil {
		return CommandResult{}, assignmentErr("allocateReviewBatch", "the tracker has no engine-owned composed-batch allocator", "StartReview must allocate every generated review task in the engine-owned transaction", "construct and launch the engine with this tracker before starting a review")
	}
	composed := provenance.GovernedAllocationComposedBatchRequest{Version: provenance.GovernedAllocationCompositionV1, Allocation: provenance.GovernedAllocationRequest{OperationID: in.Meta.OperationID, ActorID: resolution.occupant, Command: "pasture.epoch.start-review.v1", ParentAssignmentID: resolution.id, Children: children}, Conditions: conditions, SupplementalEffects: effects}
	subject := ids[reviewedTaskHandle]
	if subject != resolution.task {
		composed.ReferenceScope = provenance.GovernedAllocationReferenceScope{Kind: provenance.GovernedAllocationReferenceDescendants, Subjects: []provenance.TaskID{subject}}
	}
	if s.barrier != nil {
		if err := s.barrier.AfterPreflight(ctx, MutationStartReview); err != nil {
			return CommandResult{}, assignmentErr("allocateReviewBatch", "the operation was rejected at the injected pre-commit barrier", "the synchronization seam stopped StartReview before its governed transaction", "retry after the competing review start has settled")
		}
	}
	result, err := allocator.RunAllocateComposedBatch(ctx, "pasture-start-review:"+string(in.Meta.OperationID), resolution.authority, composed)
	if err != nil {
		return CommandResult{}, fmt.Errorf("StartReview operation %q failed in its fused governed-allocation transaction; no partial review task, journal, audit, or DBOS output committed: %w", in.Meta.OperationID, err)
	}
	closure := result.Closure().Children()
	if len(closure) != len(children) {
		return CommandResult{}, assignmentErr("allocateReviewBatch", "the composed result omitted generated review children", "StartReview returns the complete ordered governed closure", "repair the composed batch receipt before retrying")
	}
	for i := range children {
		if closure[i].Ordinal != i || closure[i].TaskID != children[i].TaskID || closure[i].AssignmentID != children[i].AssignmentID || closure[i].Occupant != children[i].Occupant {
			return CommandResult{}, assignmentErr("allocateReviewBatch", fmt.Sprintf("the composed result child %d did not match the requested review binding", i), "StartReview preserves exact child order and identity", "repair the composed batch receipt before retrying")
		}
	}
	return CommandResult{OperationID: in.Meta.OperationID, Replayed: result.Replayed(), Epoch: in.Epoch, ActivityID: activityID, EventIDs: result.SupplementalEmittedEvents()}, nil
}

func (s *epochAssignmentService) SubmitReview(ctx context.Context, in SubmitReviewInput) (ReviewSubmitResult, error) {
	payload := struct {
		Round      ReviewRoundID           `json:"round"`
		Axis       ReviewAxis              `json:"axis"`
		Assignment provenance.AssignmentID `json:"assignment"`
		Submission ReviewSubmission        `json:"submission"`
	}{in.Round, in.Axis, in.Assignment, in.Submission}
	replay, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationSubmitReview, in.Epoch, payload)
	if err != nil {
		return ReviewSubmitResult{}, err
	}
	if found {
		command, err := commandResultFromCommitted(in.Meta, in.Epoch, replay)
		if err != nil {
			return ReviewSubmitResult{}, err
		}
		eventID := journalIDForSlot(replay, reviewEventResultSlot)
		if eventID == 0 {
			return ReviewSubmitResult{}, assignmentErr("SubmitReview", "the replayed review submission has no event result binding", "exact replay must restore the immutable recorded event without consulting mutable review state", "repair the committed operation result before retrying")
		}
		return ReviewSubmitResult{CommandResult: command, Round: in.Round, Axis: in.Axis, Event: eventID}, nil
	}
	if in.Submission == nil {
		return ReviewSubmitResult{}, assignmentErr("SubmitReview", "the review submission is nil", "review authority requires one closed typed submission", "supply a PlanReviewSubmission or ImplementationReviewSubmission")
	}
	if err := in.Submission.Validate(); err != nil {
		return ReviewSubmitResult{}, err
	}
	if !in.Axis.valid() {
		return ReviewSubmitResult{}, assignmentErr("SubmitReview", fmt.Sprintf("review axis %d is unknown", in.Axis), "a review round has exactly correctness, test-quality, and elegance axes", "supply one of the canonical review axes")
	}
	start, err := s.findReviewStart(ctx, in.Epoch, in.Round)
	if err != nil {
		return ReviewSubmitResult{}, err
	}
	currentRound, err := s.currentReviewRoundAuthority(start.subject)
	if err != nil {
		return ReviewSubmitResult{}, err
	}
	if currentRound.value.Round != in.Round || currentRound.value.Kind != start.kind || currentRound.value.State != reviewRoundStarted {
		return ReviewSubmitResult{}, assignmentErr("SubmitReview", fmt.Sprintf("review round %q is not the current started review", in.Round), "axis submissions cannot append to a finalized or invalidated review", "start a fresh review round before submitting")
	}
	if currentRound.value.Graph != start.graph {
		return ReviewSubmitResult{}, assignmentErr("SubmitReview", fmt.Sprintf("review round %q has inconsistent start-event and authority bindings", in.Round), "axis submission requires one exact canonical round graph", "repair the malformed review start before submitting")
	}
	axisTask := currentRound.value.Graph[in.Axis-1].Task
	resolution, err := s.resolveAssignment(ctx, axisTask, in.Assignment, RoleAxisReviewer)
	if err != nil {
		return ReviewSubmitResult{}, err
	}
	switch start.kind {
	case SubjectPlan:
		if _, ok := in.Submission.(PlanReviewSubmission); !ok {
			return ReviewSubmitResult{}, assignmentErr("SubmitReview", "an implementation review submission was supplied for a plan subject", "review submission payloads are closed by subject kind", "supply PlanReviewSubmission for a plan review")
		}
	case SubjectImplementation:
		if _, ok := in.Submission.(ImplementationReviewSubmission); !ok {
			return ReviewSubmitResult{}, assignmentErr("SubmitReview", "a plan review submission was supplied for an implementation subject", "review submission payloads are closed by subject kind", "supply ImplementationReviewSubmission for an implementation review")
		}
	}
	verdict := submissionVerdict(in.Submission)
	event, err := MapMaterialEvent(ReviewRecordedEvent{ReviewedTask: start.subject, AxisTask: axisTask, Kind: start.kind, Verdict: verdict})
	if err != nil {
		return ReviewSubmitResult{}, err
	}
	// The axis assignment owns the mutation; the mapped contexts still bind the
	// event to the reviewed subject for timeline and provenance correlation.
	event.TaskID = axisTask
	event.ResultSlot = reviewEventResultSlot
	submissionPayload, err := canonicalJSON(struct {
		Epoch      EpochRootID             `json:"epoch"`
		Round      ReviewRoundID           `json:"round"`
		Axis       ReviewAxis              `json:"axis"`
		Assignment provenance.AssignmentID `json:"assignment"`
		Actor      provenance.ActorID      `json:"actor"`
		Kind       SubjectKind             `json:"kind"`
		Submission ReviewSubmission        `json:"submission"`
	}{in.Epoch, in.Round, in.Axis, in.Assignment, resolution.occupant, start.kind, in.Submission})
	if err != nil {
		return ReviewSubmitResult{}, fmt.Errorf("encode review submission evidence: %w", err)
	}
	digest := sha256.Sum256(submissionPayload)
	submissionEffect := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: reviewEvidenceResultSlot, TaskID: axisTask, EvidenceKind: reviewSubmissionEvidenceKind, ContentDigest: digest[:], Payload: submissionPayload}
	conditions := []provenance.Condition{reviewRoundCurrentCondition(start.subject, currentRound.journalID), reviewAxisCurrentCondition(axisTask, 0)}
	if start.kind == SubjectImplementation {
		current, err := s.currentReviewAuthority(start.subject)
		if err != nil {
			return ReviewSubmitResult{}, err
		}
		if current.value.State != reviewStarted || current.value.Round != in.Round {
			return ReviewSubmitResult{}, assignmentErr("SubmitReview", fmt.Sprintf("review round %q is not the current started review", in.Round), "axis submissions cannot append to a finalized or invalidated review", "start a fresh review round before submitting")
		}
		conditions = append(conditions, reviewAuthorityCurrentCondition(start.subject, current.journalID))
	}
	axisEffect, err := reviewAxisSubmissionEffect(start.subject, axisTask, start, in.Axis, verdict, in.Meta.OperationID, in.Assignment, resolution.occupant, reviewAxisSubmissionResultSlot)
	if err != nil {
		return ReviewSubmitResult{}, err
	}
	effects := []provenance.Effect{submissionEffect, axisEffect, event}
	if implementation, ok := in.Submission.(ImplementationReviewSubmission); ok {
		seen := map[provenance.TaskID]struct{}{}
		for i, finding := range implementation.Findings {
			if _, duplicate := seen[finding.Task]; duplicate {
				return ReviewSubmitResult{}, assignmentErr("SubmitReview", fmt.Sprintf("finding %q is submitted more than once", finding.Task), "one closed finding identity can have one severity edge in a review axis", "remove duplicate findings before submitting")
			}
			seen[finding.Task] = struct{}{}
			if _, err := s.tracker.prov.Show(finding.Task); err != nil {
				return ReviewSubmitResult{}, assignmentErr("SubmitReview", fmt.Sprintf("finding task %q could not be read", finding.Task), "review findings must bind existing task identities", fmt.Sprintf("repair finding %d to name an existing task", i))
			}
			findingEffect, err := reviewFindingGraphEffect(currentRound.value.Graph, in.Axis, finding)
			if err != nil {
				return ReviewSubmitResult{}, err
			}
			effects = append(effects, findingEffect)
		}
	}
	result, committed, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationSubmitReview, payload, conditions, effects)
	if err != nil {
		return ReviewSubmitResult{}, err
	}
	eventID := journalIDForSlot(committed, reviewEventResultSlot)
	if eventID == 0 {
		return ReviewSubmitResult{}, assignmentErr("SubmitReview", "the committed review submission has no event result binding", "review results must identify the immutable recorded event", "verify journal result-slot integrity and retry")
	}
	return ReviewSubmitResult{CommandResult: result, Round: in.Round, Axis: in.Axis, Event: eventID}, nil
}

func (s *epochAssignmentService) FinalizeReview(ctx context.Context, in FinalizeReviewInput) (ReviewFinalizeResult, error) {
	payload := struct {
		Round      ReviewRoundID           `json:"round"`
		Assignment provenance.AssignmentID `json:"assignment"`
	}{in.Round, in.Assignment}
	replay, found, err := s.lookupAssignmentReplay(ctx, in.Meta, MutationFinalizeReview, in.Epoch, payload)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	if found {
		return s.finalizedReviewFromCommitted(in, replay)
	}
	start, err := s.findReviewStart(ctx, in.Epoch, in.Round)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	roundCurrent, err := s.currentReviewRoundAuthority(start.subject)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	if roundCurrent.value.Round != in.Round || roundCurrent.value.Kind != start.kind || roundCurrent.value.State != reviewRoundStarted {
		return ReviewFinalizeResult{}, assignmentErr("FinalizeReview", fmt.Sprintf("review round %q is not the current started review", in.Round), "finalization requires one current started round", "finalize the current round or start a new review")
	}
	if roundCurrent.value.Graph != start.graph {
		return ReviewFinalizeResult{}, assignmentErr("FinalizeReview", fmt.Sprintf("review round %q has inconsistent start-event and authority bindings", in.Round), "finalization requires one exact canonical round graph", "repair the malformed review start before finalizing")
	}
	resolution, err := s.resolveAssignment(ctx, start.subject, in.Assignment, RoleGoverningSupervisor)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	axisEvents, err := s.reviewAxisEvents(ctx, start)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	verdict := VerdictAccept
	for _, axis := range axisEvents {
		if axis.verdict == VerdictRevise {
			verdict = VerdictRevise
		}
	}
	epoch, err := epochTaskID(in.Epoch)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	finalEvent, err := MapMaterialEvent(ReviewRoundFinalizedEvent{Subject: start.subject, Epoch: epoch, Round: in.Round, Verdict: verdict})
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	finalEvent.ResultSlot = reviewEventResultSlot
	effects := []provenance.Effect{finalEvent}
	conditions := []provenance.Condition{reviewRoundCurrentCondition(start.subject, roundCurrent.journalID)}
	var axisIDs [3]provenance.JournalID
	for i := range axisEvents {
		axisIDs[i] = axisEvents[i].event
		conditions = append(conditions, reviewAxisExactCondition(axisEvents[i].axisTask, axisEvents[i].operation, axisEvents[i].evidence))
	}
	roundState := reviewRoundFinalizedClean
	if verdict == VerdictRevise {
		roundState = reviewRoundFinalizedRevising
	}
	roundEffect, err := reviewRoundAuthorityEffect(start.subject, reviewRoundAuthority{Epoch: in.Epoch, Round: in.Round, Subject: start.subject, Kind: start.kind, State: roundState, Graph: roundCurrent.value.Graph, AxisEvents: axisIDs, Operation: in.Meta.OperationID}, reviewRoundAuthorityResultSlot)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	effects = append(effects, roundEffect)
	if start.kind == SubjectImplementation {
		current, err := s.currentReviewAuthority(start.subject)
		if err != nil {
			return ReviewFinalizeResult{}, err
		}
		if current.value.State != reviewStarted || current.value.Round != in.Round {
			return ReviewFinalizeResult{}, assignmentErr("FinalizeReview", fmt.Sprintf("review round %q is not the current started review", in.Round), "finalization cannot race a later finalization or rework", "finalize the current started review or start a fresh round")
		}
		var axes [3]reviewAxisAuthority
		for i := range axes {
			axes[i] = reviewAxisAuthority{Axis: canonicalReviewAxes()[i], Event: axisEvents[i].event, Verdict: axisEvents[i].verdict}
		}
		authority, err := newFinalizedReviewAuthority(in.Epoch, IntegrationCandidateSetID(start.subject.String()), in.Round, axes, in.Meta.OperationID)
		if err != nil {
			return ReviewFinalizeResult{}, err
		}
		authorityEffect, err := newReviewAuthorityEvidenceEffect(start.subject, authority, reviewEvidenceResultSlot)
		if err != nil {
			return ReviewFinalizeResult{}, err
		}
		effects = append(effects, authorityEffect)
		conditions = append(conditions, reviewAuthorityCurrentCondition(start.subject, current.journalID))
	}
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationFinalizeReview, payload, conditions, effects)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	var events [3]provenance.JournalID
	for i := range axisEvents {
		events[i] = axisEvents[i].event
	}
	return ReviewFinalizeResult{CommandResult: result, Round: in.Round, ReviewEvents: events}, nil
}

func (s *epochAssignmentService) finalizedReviewFromCommitted(in FinalizeReviewInput, committed provenance.CommittedResult) (ReviewFinalizeResult, error) {
	command, err := commandResultFromCommitted(in.Meta, in.Epoch, committed)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	authorityJournalID := journalIDForSlot(committed, reviewRoundAuthorityResultSlot)
	if authorityJournalID == 0 {
		return ReviewFinalizeResult{}, assignmentErr("finalizedReviewFromCommitted", "the replayed finalization has no round-authority result binding", "exact retry must restore finalization solely from its immutable committed receipt", "repair the committed operation result before retrying")
	}
	page, err := s.tracker.prov.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{
			TaskScope:    provenance.FactTaskScope{Kind: provenance.FactTaskAny},
			OperationIDs: []provenance.OperationID{in.Meta.OperationID},
		},
		Kinds: []provenance.EvidenceKind{reviewRoundAuthorityEvidenceKind},
		Page:  provenance.FactPageRequest{Limit: 2},
	})
	if err != nil {
		return ReviewFinalizeResult{}, fmt.Errorf("restore finalized review %q from committed authority: %w", in.Meta.OperationID, err)
	}
	if len(page.Rows) != 1 || page.Next != nil || page.Rows[0].JournalID != authorityJournalID || page.Rows[0].TaskID == nil {
		return ReviewFinalizeResult{}, assignmentErr("finalizedReviewFromCommitted", fmt.Sprintf("the replayed finalization has %d matching authority facts", len(page.Rows)), "one committed finalization must bind exactly one typed round authority and result slot", "repair the malformed committed receipt before retrying")
	}
	row := page.Rows[0]
	var authority reviewRoundAuthority
	if err := strictJSON(row.Payload, &authority); err != nil {
		return ReviewFinalizeResult{}, assignmentErr("finalizedReviewFromCommitted", "the replayed round authority is malformed", "exact retry requires the original typed finalization payload", "repair the malformed committed authority before retrying")
	}
	if err := validateReviewRoundAuthority(authority, *row.TaskID); err != nil {
		return ReviewFinalizeResult{}, err
	}
	if authority.Epoch != in.Epoch || authority.Round != in.Round || authority.Operation != in.Meta.OperationID || row.ProducingOperationID != in.Meta.OperationID || (authority.State != reviewRoundFinalizedClean && authority.State != reviewRoundFinalizedRevising) {
		return ReviewFinalizeResult{}, assignmentErr("finalizedReviewFromCommitted", "the replayed round authority does not match the requested finalization", "exact retry cannot derive identifiers from later mutable review state", "retry with the original epoch, round, assignment, and operation identity")
	}
	return ReviewFinalizeResult{CommandResult: command, Round: in.Round, ReviewEvents: authority.AxisEvents}, nil
}

func (s *epochAssignmentService) findReviewStart(ctx context.Context, epoch EpochRootID, round ReviewRoundID) (reviewStartedRecord, error) {
	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, EventKinds: []provenance.EventKind{reviewStartedEventKind}, Limit: provenance.MaxFactPageSize}
	var match reviewStartedRecord
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return reviewStartedRecord{}, fmt.Errorf("find review start: context ended: %w", err)
		}
		page, err := s.tracker.prov.Journal().QueryTaskEvents(query)
		if err != nil {
			return reviewStartedRecord{}, fmt.Errorf("query review-start events: %w", err)
		}
		for _, row := range page.Events {
			var payload reviewStartedPayload
			if err := strictJSON(row.Payload, &payload); err != nil {
				return reviewStartedRecord{}, fmt.Errorf("decode review-start event %d: %w", row.JournalID, err)
			}
			if payload.Epoch != string(epoch) || payload.Round != string(round) {
				continue
			}
			subject, err := provenance.ParseTaskID(payload.Subject)
			if err != nil || len(payload.AxisTasks) != 3 {
				return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review-start event %d has malformed subject or axes", row.JournalID), "review submissions require one canonical started-round record", "repair the review-start event before retrying")
			}
			if row.TaskID != subject {
				return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review-start event %d is attached to task %q instead of %q", row.JournalID, row.TaskID, subject), "a review round event must bind its exact subject task", "repair the review-start event before retrying")
			}
			var axes [3]provenance.TaskID
			for i, raw := range payload.AxisTasks {
				axes[i], err = provenance.ParseTaskID(raw)
				if err != nil {
					return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review-start event %d has malformed axis task %q", row.JournalID, raw), "each review axis is a real Provenance task", "repair the review-start event before retrying")
				}
			}
			kind := SubjectPlan
			if payload.Kind == SubjectImplementation.String() {
				kind = SubjectImplementation
			} else if payload.Kind != SubjectPlan.String() {
				return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review-start event %d has unknown subject kind %q", row.JournalID, payload.Kind), "review rounds are plan or implementation rounds", "repair the review-start event before retrying")
			}
			var groups [3][3]provenance.TaskID
			if kind == SubjectImplementation {
				if len(payload.AxisGroups) != 3 {
					return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review-start event %d omits implementation severity groups", row.JournalID), "implementation reviews eagerly materialize blocker, important, and minor groups for each axis", "repair the review-start event before retrying")
				}
				for i := range groups {
					if len(payload.AxisGroups[i]) != 3 {
						return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review-start event %d has an incomplete severity group set", row.JournalID), "each implementation axis has exactly three typed severity groups", "repair the review-start event before retrying")
					}
					for j, raw := range payload.AxisGroups[i] {
						groups[i][j], err = provenance.ParseTaskID(raw)
						if err != nil {
							return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review-start event %d has malformed severity group %q", row.JournalID, raw), "each severity group is a real Provenance task", "repair the review-start event before retrying")
						}
					}
				}
			}
			graph, err := newReviewRoundGraph(kind, axes, groups)
			if err != nil {
				return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review-start event %d has an invalid typed review graph", row.JournalID), "a started round must bind the canonical axis and severity task identities", "repair the review-start event before retrying")
			}
			count++
			match = reviewStartedRecord{eventID: row.JournalID, epoch: epoch, round: round, subject: subject, kind: kind, axisTasks: axes, groups: groups, graph: graph}
		}
		if page.Next == nil {
			break
		}
		query.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.AfterJournalID = page.Next.AfterJournalID
	}
	if count == 0 {
		return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review round %q does not exist for epoch %q", round, epoch), "review operations must bind an existing started round", "start the review round before submitting or finalizing")
	}
	if count != 1 {
		return reviewStartedRecord{}, assignmentErr("findReviewStart", fmt.Sprintf("review round %q has %d started records", round, count), "one round identity must resolve to one immutable start record", "repair duplicate review-start facts before retrying")
	}
	return match, nil
}

type reviewAxisEvent struct {
	event     provenance.JournalID
	evidence  provenance.JournalID
	axisTask  provenance.TaskID
	verdict   Verdict
	actor     provenance.ActorID
	operation provenance.OperationID
}

func (s *epochAssignmentService) reviewAxisEvents(ctx context.Context, start reviewStartedRecord) ([3]reviewAxisEvent, error) {
	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{start.axisTasks[0], start.axisTasks[1], start.axisTasks[2]}, EventKinds: []provenance.EventKind{FamilyReviewRecorded.EventKind()}, Limit: provenance.MaxFactPageSize}
	var found [3]reviewAxisEvent
	seen := [3]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return found, fmt.Errorf("read review-axis events: context ended: %w", err)
		}
		page, err := s.tracker.prov.Journal().QueryTaskEvents(query)
		if err != nil {
			return found, fmt.Errorf("query review-axis events: %w", err)
		}
		for _, row := range page.Events {
			var payload reviewRecordedPayload
			if err := strictJSON(row.Payload, &payload); err != nil {
				return found, fmt.Errorf("decode review-axis event %d: %w", row.JournalID, err)
			}
			axisTask, err := provenance.ParseTaskID(payload.AxisTask)
			if err != nil || payload.Kind != start.kind.String() {
				continue
			}
			idx := -1
			for i, want := range start.axisTasks {
				if want == axisTask {
					idx = i
					break
				}
			}
			if idx < 0 {
				continue
			}
			if row.TaskID != axisTask {
				return found, assignmentErr("reviewAxisEvents", fmt.Sprintf("review event %d is attached to task %q instead of axis task %q", row.JournalID, row.TaskID, axisTask), "axis history must be owned by the exact axis assignment while retaining reviewed-subject context", "repair the review event before finalizing")
			}
			verdict, err := parseVerdict(payload.Verdict)
			if err != nil {
				return found, err
			}
			if seen[idx] {
				return found, assignmentErr("reviewAxisEvents", fmt.Sprintf("axis %s has multiple submissions", canonicalReviewAxes()[idx]), "finalization requires exactly one immutable submission for each axis", "submit each review axis once")
			}
			axisEvidence, err := s.axisEvidenceForEvent(axisTask, start, ReviewAxis(idx+1), verdict, row.ActorID)
			if err != nil {
				return found, err
			}
			if axisEvidence.Verdict != verdict || axisEvidence.Subject != start.subject || axisEvidence.AxisTask != axisTask || axisEvidence.Epoch != start.epoch || axisEvidence.Round != start.round || axisEvidence.Actor != row.ActorID {
				return found, assignmentErr("reviewAxisEvents", fmt.Sprintf("review event %d has mismatched round, subject, actor, or evidence binding", row.JournalID), "finalization consumes only the exact current-round axis submission", "repair the review evidence and event pair before finalizing")
			}
			seen[idx] = true
			found[idx] = reviewAxisEvent{event: row.JournalID, evidence: axisEvidence.journalID, axisTask: axisTask, verdict: verdict, actor: row.ActorID, operation: axisEvidence.Operation}
		}
		if page.Next == nil {
			break
		}
		query.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.AfterJournalID = page.Next.AfterJournalID
	}
	for i := range seen {
		if !seen[i] {
			return found, assignmentErr("reviewAxisEvents", fmt.Sprintf("review axis %s has no submission", canonicalReviewAxes()[i]), "finalization requires all three canonical axes", "submit correctness, test-quality, and elegance reviews before finalizing")
		}
	}
	return found, nil
}

func (s *epochAssignmentService) axisEvidenceForEvent(axisTask provenance.TaskID, start reviewStartedRecord, axis ReviewAxis, verdict Verdict, actor provenance.ActorID) (reviewAxisEvidenceRecord, error) {
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: axisTask}}, Kinds: []provenance.EvidenceKind{reviewAxisSubmissionEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var found reviewAxisEvidenceRecord
	count := 0
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return found, fmt.Errorf("query axis submission evidence for %q: %w", axisTask, err)
		}
		for _, row := range page.Rows {
			var candidate reviewAxisEvidenceRecord
			if row.TaskID == nil || *row.TaskID != axisTask || row.ProducingOperationID == "" {
				return found, assignmentErr("axisEvidenceForEvent", "axis evidence metadata is inconsistent", "axis evidence must be task-scoped and operation-produced", "repair the malformed axis evidence before finalizing")
			}
			if err := strictJSON(row.Payload, &candidate); err != nil {
				return found, fmt.Errorf("decode axis submission evidence: %w", err)
			}
			if !candidate.Axis.valid() || !candidate.Verdict.valid() || candidate.Assignment == "" || candidate.Actor == (provenance.ActorID{}) || provenance.ValidateOperationID(candidate.Operation) != nil {
				return found, assignmentErr("axisEvidenceForEvent", fmt.Sprintf("axis evidence row %d is incomplete", row.JournalID), "axis evidence is a closed round, axis, assignment, actor, verdict, and producer binding", "repair the malformed axis evidence before finalizing")
			}
			if candidate.Axis != axis || candidate.AxisTask != axisTask || candidate.Subject != start.subject || candidate.Epoch != start.epoch || candidate.Round != start.round || candidate.Operation != row.ProducingOperationID || candidate.Verdict != verdict || candidate.Actor != actor {
				continue
			}
			candidate.journalID = row.JournalID
			count++
			if count > 1 {
				return found, assignmentErr("axisEvidenceForEvent", fmt.Sprintf("axis task %q has duplicate matching evidence", axisTask), "one axis submission must have one exact evidence binding", "repair duplicate axis evidence before finalizing")
			}
			found = candidate
			if found.Axis != axis || found.AxisTask != axisTask || found.Subject != start.subject || found.Epoch != start.epoch || found.Round != start.round || found.Operation != row.ProducingOperationID {
				return found, assignmentErr("axisEvidenceForEvent", fmt.Sprintf("axis evidence for %q is bound to another round or producer", axisTask), "finalization requires exact round and operation identity", "repair the malformed axis evidence before finalizing")
			}
		}
		if page.Next == nil {
			break
		}
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	if count == 0 {
		return found, assignmentErr("axisEvidenceForEvent", fmt.Sprintf("axis task %q has no matching evidence for the recorded event", axisTask), "a review event must be paired with one exact axis submission fact", "repair the review submission before finalizing")
	}
	return found, nil
}

func (s *epochAssignmentService) currentReviewAuthority(candidate provenance.TaskID) (reviewAuthoritySnapshot, error) {
	// This reader remains private to the assignment aggregate.  HumanEpochService
	// owns its separate consumer reader and frozen lifecycle API; merging those
	// files would cross the reviewed aggregate boundary.  Both readers consume
	// the same typed Evidence contract and CurrentFact semantics.
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}}, Kinds: []provenance.EvidenceKind{implementationReviewAuthorityEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	var current reviewAuthoritySnapshot
	for {
		page, err := s.tracker.prov.Journal().Facts().QueryEvidence(query)
		if err != nil {
			return reviewAuthoritySnapshot{}, fmt.Errorf("query current review authority for %q: %w", candidate, err)
		}
		for _, row := range page.Rows {
			if row.TaskID == nil || *row.TaskID != candidate || row.ProducingOperationID == "" {
				return reviewAuthoritySnapshot{}, assignmentErr("currentReviewAuthority", "a review authority row has inconsistent metadata", "CurrentFact must identify one exact candidate-scoped producer", "repair the malformed review authority fact before retrying")
			}
			value, err := decodeImplementationReviewAuthority(row.Payload)
			if err != nil || value.Candidate != IntegrationCandidateSetID(candidate.String()) || (value.Operation != row.ProducingOperationID && provenance.GovernedAllocationSupplementOperationID(value.Operation) != row.ProducingOperationID) {
				return reviewAuthoritySnapshot{}, assignmentErr("currentReviewAuthority", fmt.Sprintf("review authority for %q is malformed", candidate), "review operations require the typed frozen authority contract", "repair the review authority fact before retrying")
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
		return reviewAuthoritySnapshot{}, &reviewAuthorityAbsentError{subject: candidate}
	}
	return current, nil
}

func submissionVerdict(submission ReviewSubmission) Verdict {
	switch value := submission.(type) {
	case PlanReviewSubmission:
		return value.Verdict
	case ImplementationReviewSubmission:
		return value.Verdict
	default:
		return verdictInvalid
	}
}

func parseVerdict(raw string) (Verdict, error) {
	switch raw {
	case VerdictAccept.String():
		return VerdictAccept, nil
	case VerdictRevise.String():
		return VerdictRevise, nil
	default:
		return verdictInvalid, assignmentErr("parseVerdict", fmt.Sprintf("verdict %q is unknown", raw), "review verdicts are a closed accept/revise enum", "use accept or revise")
	}
}

func strictJSON(payload []byte, target any) error {
	if err := validateAuthorityJSONUTF8(payload, "review payload"); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("payload contains trailing JSON")
		}
		return fmt.Errorf("payload contains trailing data: %w", err)
	}
	return nil
}

func journalIDForSlot(result provenance.CommittedResult, slot provenance.ResultSlotID) provenance.JournalID {
	for _, binding := range result.ResultSlots {
		if binding.Slot == slot {
			return binding.ProducedJournalID
		}
	}
	return 0
}
