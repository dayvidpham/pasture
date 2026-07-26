package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/provenance"
)

type reviewStartedPayload struct {
	Epoch     string   `json:"epoch"`
	Round     string   `json:"round"`
	Subject   string   `json:"subject"`
	Kind      string   `json:"kind"`
	AxisTasks []string `json:"axis_tasks"`
}

type reviewStartedRecord struct {
	eventID   provenance.JournalID
	epoch     EpochRootID
	round     ReviewRoundID
	subject   provenance.TaskID
	kind      SubjectKind
	axisTasks [3]provenance.TaskID
}

type reviewRecordedPayload struct {
	AxisTask string `json:"axis_task"`
	Kind     string `json:"kind"`
	Verdict  string `json:"verdict"`
}

func (s *epochAssignmentService) StartReview(ctx context.Context, in StartReviewInput) (ReviewStartResult, error) {
	subjectTask, err := provenance.ParseTaskID(in.Subject.SnapshotID)
	if err != nil {
		return ReviewStartResult{}, assignmentErr("StartReview", fmt.Sprintf("review subject %q is malformed", in.Subject.SnapshotID), "review subjects must identify an existing Provenance task", "supply the canonical subject task identity")
	}
	if _, err := s.tracker.prov.Show(subjectTask); err != nil {
		return ReviewStartResult{}, assignmentErr("StartReview", fmt.Sprintf("review subject %q could not be read", in.Subject.SnapshotID), "a review can only start for an existing subject", "supply an existing plan or implementation candidate")
	}
	kind := SubjectPlan
	if in.Subject.Kind == ReviewSubjectImplementationCandidate {
		kind = SubjectImplementation
	} else if in.Subject.Kind != ReviewSubjectDocumentRevision {
		return ReviewStartResult{}, assignmentErr("StartReview", "the review subject kind is unknown", "review start accepts only document revisions and implementation candidates", "derive a valid ReviewSubjectRef from a concrete subject")
	}
	plan, err := PlanReviewRound(subjectTask, in.Subject, kind)
	if err != nil {
		return ReviewStartResult{}, err
	}
	scope, err := s.assignmentScope(subjectTask, in.Epoch)
	if err != nil {
		return ReviewStartResult{}, err
	}
	resolution, err := s.resolveUniqueAssignment(ctx, scope, RoleGoverningSupervisor)
	if err != nil {
		return ReviewStartResult{}, err
	}
	roundTask := deterministicTask(in.Meta.OperationID, "review-round")
	axisTasks := [3]provenance.TaskID{
		deterministicTask(in.Meta.OperationID, "axis-correctness"),
		deterministicTask(in.Meta.OperationID, "axis-test-quality"),
		deterministicTask(in.Meta.OperationID, "axis-elegance"),
	}
	planByHandle := map[string]provenance.TaskID{plan.RoundHandle: roundTask}
	for i, handle := range plan.AxisHandles {
		planByHandle[handle] = axisTasks[i]
	}
	for _, task := range plan.Tasks {
		if _, ok := planByHandle[task.Handle]; !ok {
			planByHandle[task.Handle] = deterministicTask(in.Meta.OperationID, task.Handle)
		}
	}
	effects := make([]provenance.Effect, 0, len(plan.Tasks)+len(plan.Edges)+4)
	for _, task := range plan.Tasks {
		id := planByHandle[task.Handle]
		title := "review round"
		phase := provenance.PhaseReview
		if task.Kind == ReviewTaskAxis {
			title = "review axis " + task.Axis.String()
		}
		effects = append(effects, taskCreateEffect(id, title, phase))
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
	startPayload, err := canonicalJSON(reviewStartedPayload{Epoch: string(in.Epoch), Round: string(round), Subject: subjectTask.String(), Kind: kind.String(), AxisTasks: []string{axisTasks[0].String(), axisTasks[1].String(), axisTasks[2].String()}})
	if err != nil {
		return ReviewStartResult{}, fmt.Errorf("encode review-start payload: %w", err)
	}
	startEvent, err := epochTaskEvent(subjectTask, reviewStartedEventKind, startPayload)
	if err != nil {
		return ReviewStartResult{}, err
	}
	effects = append(effects, startEvent)
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
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationStartReview, reviewStartedPayload{Epoch: string(in.Epoch), Round: string(round), Subject: subjectTask.String(), Kind: kind.String()}, nil, effects)
	if err != nil {
		return ReviewStartResult{}, err
	}
	return ReviewStartResult{CommandResult: result, Round: round, Subject: in.Subject}, nil
}

func (s *epochAssignmentService) SubmitReview(ctx context.Context, in SubmitReviewInput) (ReviewSubmitResult, error) {
	if in.Submission == nil {
		return ReviewSubmitResult{}, assignmentErr("SubmitReview", "the review submission is nil", "review authority requires one closed typed submission", "supply a PlanReviewSubmission or ImplementationReviewSubmission")
	}
	if err := in.Submission.Validate(); err != nil {
		return ReviewSubmitResult{}, err
	}
	start, err := s.findReviewStart(ctx, in.Epoch, in.Round)
	if err != nil {
		return ReviewSubmitResult{}, err
	}
	if !in.Axis.valid() {
		return ReviewSubmitResult{}, assignmentErr("SubmitReview", fmt.Sprintf("review axis %d is unknown", in.Axis), "a review round has exactly correctness, test-quality, and elegance axes", "supply one of the canonical review axes")
	}
	axisTask := start.axisTasks[in.Axis-1]
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
	event.ResultSlot = reviewEventResultSlot
	submissionPayload, err := canonicalJSON(struct {
		Kind       SubjectKind      `json:"kind"`
		Submission ReviewSubmission `json:"submission"`
	}{start.kind, in.Submission})
	if err != nil {
		return ReviewSubmitResult{}, fmt.Errorf("encode review submission evidence: %w", err)
	}
	digest := sha256.Sum256(submissionPayload)
	submissionEffect := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: reviewEvidenceResultSlot, TaskID: start.subject, EvidenceKind: reviewSubmissionEvidenceKind, ContentDigest: digest[:], Payload: submissionPayload}
	conditions := []provenance.Condition(nil)
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
	result, committed, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationSubmitReview, struct {
		Round   ReviewRoundID `json:"round"`
		Axis    ReviewAxis    `json:"axis"`
		Verdict Verdict       `json:"verdict"`
	}{in.Round, in.Axis, verdict}, conditions, []provenance.Effect{submissionEffect, event})
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
	start, err := s.findReviewStart(ctx, in.Epoch, in.Round)
	if err != nil {
		return ReviewFinalizeResult{}, err
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
	conditions := []provenance.Condition(nil)
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
	result, _, err := s.apply(ctx, in.Meta, in.Epoch, resolution, MutationFinalizeReview, struct {
		Round   ReviewRoundID `json:"round"`
		Verdict Verdict       `json:"verdict"`
	}{in.Round, verdict}, conditions, effects)
	if err != nil {
		return ReviewFinalizeResult{}, err
	}
	var events [3]provenance.JournalID
	for i := range axisEvents {
		events[i] = axisEvents[i].event
	}
	return ReviewFinalizeResult{CommandResult: result, Round: in.Round, ReviewEvents: events}, nil
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
			count++
			match = reviewStartedRecord{eventID: row.JournalID, epoch: epoch, round: round, subject: subject, kind: kind, axisTasks: axes}
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
	event   provenance.JournalID
	verdict Verdict
}

func (s *epochAssignmentService) reviewAxisEvents(ctx context.Context, start reviewStartedRecord) ([3]reviewAxisEvent, error) {
	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{start.subject}, EventKinds: []provenance.EventKind{FamilyReviewRecorded.EventKind()}, Limit: provenance.MaxFactPageSize}
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
			verdict, err := parseVerdict(payload.Verdict)
			if err != nil {
				return found, err
			}
			if seen[idx] {
				return found, assignmentErr("reviewAxisEvents", fmt.Sprintf("axis %s has multiple submissions", canonicalReviewAxes()[idx]), "finalization requires exactly one immutable submission for each axis", "submit each review axis once")
			}
			seen[idx] = true
			found[idx] = reviewAxisEvent{event: row.JournalID, verdict: verdict}
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

func (s *epochAssignmentService) currentReviewAuthority(candidate provenance.TaskID) (reviewAuthoritySnapshot, error) {
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
			if err != nil || value.Candidate != IntegrationCandidateSetID(candidate.String()) || value.Operation != row.ProducingOperationID {
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
		return reviewAuthoritySnapshot{}, assignmentErr("currentReviewAuthority", fmt.Sprintf("candidate %q has no review authority", candidate), "implementation review operations require a started current review", "start a review for the candidate before submitting or finalizing")
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
