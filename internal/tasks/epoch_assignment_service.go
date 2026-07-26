package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

const (
	reviewStartedEventKind       provenance.EventKind    = "pasture.review.started.v1"
	candidateCreatedEventKind    provenance.EventKind    = "pasture.integration.candidate-created.v1"
	candidateReworkedEventKind   provenance.EventKind    = "pasture.integration.candidate-reworked.v1"
	reviewSubmissionEvidenceKind provenance.EvidenceKind = "pasture.review.submission.v1"
	reviewActivityResultSlot     provenance.ResultSlotID = "activity"
	reviewEventResultSlot        provenance.ResultSlotID = "event"
	reviewEvidenceResultSlot     provenance.ResultSlotID = "evidence"
	reviewEvidenceResultSlotNext provenance.ResultSlotID = "evidence-1"
)

// epochAssignmentService is the assignment-authorized half of EpochService.  It
// deliberately has no state other than the tracker and the fixed journal
// authority: eligibility is reconstructed from Provenance facts on every call,
// and the mutation is always one Journal.Apply.
type epochAssignmentService struct {
	tracker   *trackerImpl
	authority provenance.JournalID
	barrier   EpochRaceBarrier
	now       func() time.Time
}

type epochService struct {
	EpochHumanService
	EpochAssignmentService
}

var _ EpochAssignmentService = (*epochAssignmentService)(nil)
var _ EpochService = (*epochService)(nil)
var _ EpochServiceFactory = (*trackerImpl)(nil)

func (t *trackerImpl) NewEpochService(opts EpochServiceOptions) (EpochService, error) {
	human, err := t.NewEpochHumanService(opts)
	if err != nil {
		return nil, err
	}
	if _, err := t.systemSession(); err != nil {
		return nil, fmt.Errorf("construct epoch service: establish journal authority: %w", err)
	}
	_, authority, found, err := readSystemIdentity(t.auditDB)
	if err != nil {
		return nil, fmt.Errorf("construct epoch service: read journal authority: %w", err)
	}
	if !found {
		return nil, assignmentErr("NewEpochService", "the journal authority was not persisted", "assignment commands need one stable Provenance authority", "reopen the tracker and retry")
	}
	now := opts.now
	if now == nil {
		now = time.Now
	}
	assignment := &epochAssignmentService{tracker: t, authority: authority, barrier: opts.Synchronization.RaceBarrier, now: now}
	return &epochService{EpochHumanService: human, EpochAssignmentService: assignment}, nil
}

type assignmentResolution struct {
	id        provenance.AssignmentID
	role      AssignmentRole
	occupant  provenance.ActorID
	authority provenance.JournalID
	task      provenance.TaskID
}

type assignmentStartPayload struct {
	Assignment string `json:"assignment"`
	Role       string `json:"role"`
	Occupant   string `json:"occupant"`
}

// resolveAssignment validates the exact active assignment episode.  Assignment
// authority rows are intentionally not reimplemented here: the Provenance
// AuthorityGovernsTaskAt predicate remains the authority check.  The companion
// pasture.assignment.started event supplies the closed Pasture role and the
// occupant needed for attribution.
func (s *epochAssignmentService) resolveAssignment(ctx context.Context, task provenance.TaskID, id provenance.AssignmentID, want AssignmentRole) (assignmentResolution, error) {
	if id == "" {
		return assignmentResolution{}, assignmentErr("resolveAssignment", "the assignment id is empty", "assignment-controlled commands cannot infer an authority", "supply the exact active assignment id")
	}
	resolutions, err := s.findAssignments(ctx, []provenance.TaskID{task}, id, want)
	if err != nil {
		return assignmentResolution{}, err
	}
	if len(resolutions) == 0 {
		return assignmentResolution{}, assignmentErr("resolveAssignment", fmt.Sprintf("assignment %q is absent, ended, or has the wrong role for task %q", id, task), "the command requires one exact active assignment episode", "use the current assignment id with the role required by this operation")
	}
	if len(resolutions) != 1 {
		return assignmentResolution{}, assignmentErr("resolveAssignment", fmt.Sprintf("assignment %q is ambiguous for task %q", id, task), "one assignment id must resolve to one active episode", "remove duplicate assignment history before retrying")
	}
	return resolutions[0], nil
}

func (s *epochAssignmentService) resolveUniqueAssignment(ctx context.Context, tasks []provenance.TaskID, want AssignmentRole) (assignmentResolution, error) {
	resolutions, err := s.findAssignments(ctx, tasks, "", want)
	if err != nil {
		return assignmentResolution{}, err
	}
	if len(resolutions) == 0 {
		return assignmentResolution{}, assignmentErr("resolveUniqueAssignment", "no active governing assignment was found", "this operation needs an unambiguous assignment-controlled authority", "start the required assignment episode before retrying")
	}
	if len(resolutions) != 1 {
		return assignmentResolution{}, assignmentErr("resolveUniqueAssignment", fmt.Sprintf("found %d active governing assignments", len(resolutions)), "the command cannot choose between multiple active authorities", "end the competing assignment episodes and retry")
	}
	return resolutions[0], nil
}

func (s *epochAssignmentService) assignmentScope(subject provenance.TaskID, epoch EpochRootID) ([]provenance.TaskID, error) {
	scope := []provenance.TaskID{subject}
	if parsed, err := epochTaskID(epoch); err == nil {
		scope = append(scope, parsed)
	}
	all, err := s.tracker.prov.List(provenance.ListFilter{})
	if err != nil {
		return nil, fmt.Errorf("list task graph for assignment resolution: %w", err)
	}
	for _, task := range all {
		scope = append(scope, task.ID)
	}
	return scope, nil
}

func (s *epochAssignmentService) findAssignments(ctx context.Context, tasks []provenance.TaskID, wanted provenance.AssignmentID, want AssignmentRole) ([]assignmentResolution, error) {
	if len(tasks) == 0 {
		return nil, assignmentErr("findAssignments", "the target task set is empty", "assignment authority must be scoped to a task", "supply an existing epoch or subject task")
	}
	validTasks := make(map[provenance.TaskID]struct{}, len(tasks))
	for _, task := range tasks {
		if task == (provenance.TaskID{}) {
			continue
		}
		validTasks[task] = struct{}{}
	}
	if len(validTasks) == 0 {
		return nil, assignmentErr("findAssignments", "all target task identities are zero", "assignment authority cannot be resolved without a task", "supply valid Provenance task ids")
	}
	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()}, Limit: provenance.MaxFactPageSize}
	var found []assignmentResolution
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("resolve assignment: context ended while reading assignment history: %w", err)
		}
		page, err := s.tracker.prov.Journal().QueryTaskEvents(query)
		if err != nil {
			return nil, fmt.Errorf("query assignment-start events: %w", err)
		}
		for _, row := range page.Events {
			value, err := decodeAssignmentStart(row.Payload)
			if err != nil {
				return nil, fmt.Errorf("decode assignment-start event %d: %w", row.JournalID, err)
			}
			if provenance.AssignmentID(value.Assignment) == "" || value.Role != want.String() {
				continue
			}
			if wanted != "" && provenance.AssignmentID(value.Assignment) != wanted {
				continue
			}
			occupant, err := provenance.ParseActorID(value.Occupant)
			if err != nil || occupant == (provenance.ActorID{}) {
				return nil, assignmentErr("findAssignments", fmt.Sprintf("assignment-start event %d has an invalid occupant", row.JournalID), "assignment attribution must resolve to the event's registered actor", "repair the assignment-start fact before retrying")
			}
			authority, ok, err := s.assignmentAuthorityForEvent(row, row.TaskID)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			for target := range validTasks {
				governs, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(authority, target, row.JournalID)
				if err != nil {
					return nil, fmt.Errorf("check governing assignment %q for task %q: %w", value.Assignment, target, err)
				}
				if !governs {
					continue
				}
				active, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(authority, target, provenance.JournalID(math.MaxInt64))
				if err != nil {
					return nil, fmt.Errorf("check current governing assignment %q for task %q: %w", value.Assignment, target, err)
				}
				if active {
					duplicate := false
					for _, prior := range found {
						duplicate = duplicate || (prior.id == provenance.AssignmentID(value.Assignment) && prior.authority == authority)
					}
					if !duplicate {
						found = append(found, assignmentResolution{id: provenance.AssignmentID(value.Assignment), role: want, occupant: occupant, authority: authority, task: row.TaskID})
					}
				}
			}
		}
		if page.Next == nil {
			break
		}
		query.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.AfterJournalID = page.Next.AfterJournalID
	}
	return found, nil
}

func decodeAssignmentStart(payload []byte) (assignmentStartPayload, error) {
	var value assignmentStartPayload
	if err := validateAuthorityJSONUTF8(payload, "assignment-start payload"); err != nil {
		return assignmentStartPayload{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return assignmentStartPayload{}, fmt.Errorf("malformed assignment-start payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return assignmentStartPayload{}, fmt.Errorf("assignment-start payload contains trailing JSON")
		}
		return assignmentStartPayload{}, fmt.Errorf("assignment-start payload has trailing data: %w", err)
	}
	if value.Assignment == "" || value.Role == "" || value.Occupant == "" {
		return assignmentStartPayload{}, fmt.Errorf("assignment-start payload omits assignment, role, or occupant")
	}
	return value, nil
}

func (s *epochAssignmentService) assignmentAuthorityForEvent(row provenance.TaskEventRow, task provenance.TaskID) (provenance.JournalID, bool, error) {
	start := provenance.JournalID(1)
	if row.ProducedByOperationJournalID != nil {
		start = *row.ProducedByOperationJournalID + 1
	}
	if start >= row.JournalID {
		start = row.JournalID - 1
	}
	for candidate := start; candidate > 0 && candidate < row.JournalID; candidate++ {
		governs, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(candidate, task, row.JournalID)
		if err != nil {
			return 0, false, fmt.Errorf("check assignment authority %d for task %q: %w", candidate, task, err)
		}
		if !governs {
			continue
		}
		active, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(candidate, task, provenance.JournalID(math.MaxInt64))
		if err != nil {
			return 0, false, fmt.Errorf("check current assignment authority %d for task %q: %w", candidate, task, err)
		}
		if active {
			return candidate, true, nil
		}
	}
	return 0, false, nil
}

func (s *epochAssignmentService) apply(ctx context.Context, in CommandMeta, epoch EpochRootID, resolution assignmentResolution, mutation EpochMutationKind, payload any, conditions []provenance.Condition, effects []provenance.Effect) (CommandResult, provenance.CommittedResult, error) {
	if err := provenance.ValidateOperationID(in.OperationID); err != nil {
		return CommandResult{}, provenance.CommittedResult{}, assignmentErr("apply", fmt.Sprintf("operation %q is invalid", in.OperationID), "assignment operations require stable replay identities", "supply a non-empty operation id without control characters")
	}
	epochTask, err := epochTaskID(epoch)
	if err != nil {
		return CommandResult{}, provenance.CommittedResult{}, err
	}
	if _, err := s.tracker.prov.Show(epochTask); err != nil {
		return CommandResult{}, provenance.CommittedResult{}, assignmentErr("apply", fmt.Sprintf("epoch %q could not be read", epoch), "assignment operations cannot write against a missing epoch", "supply an existing epoch task")
	}
	command, err := canonicalJSON(struct {
		Mutation EpochMutationKind `json:"mutation"`
		Epoch    EpochRootID       `json:"epoch"`
		Payload  any               `json:"payload"`
	}{mutation, epoch, payload})
	if err != nil {
		return CommandResult{}, provenance.CommittedResult{}, fmt.Errorf("encode assignment command: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return CommandResult{}, provenance.CommittedResult{}, fmt.Errorf("assignment operation %q was cancelled before Apply: %w", in.OperationID, err)
	}
	if s.barrier != nil {
		if err := s.barrier.AfterPreflight(ctx, mutation); err != nil {
			return CommandResult{}, provenance.CommittedResult{}, assignmentErr("apply", "the operation was rejected at the injected pre-commit barrier", "the synchronization seam stopped the command before Provenance Apply", "retry after the competing operation has settled")
		}
	}
	activityID := provenance.ActivityID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("assignment-activity:"+string(in.OperationID)))}
	effects = append(effects, provenance.Effect{Sort: provenance.EffectActivityCreate, ResultSlot: reviewActivityResultSlot, ActivityID: activityID, ActivityAgentID: resolution.occupant, ActivityPhase: assignmentPhase(mutation), ActivityStage: provenance.StageComplete, ActivityNotes: "assignment-controlled epoch operation"})
	result, err := s.tracker.prov.Journal().Apply(provenance.OperationInput{OperationID: in.OperationID, ActorID: resolution.occupant, AuthorityJournalID: &s.authority, CommandDigest: command, RecordedAt: s.now().UTC().UnixNano(), Conditions: conditions, Effects: effects})
	if err != nil {
		return CommandResult{}, result, fmt.Errorf("assignment operation %q failed during its single atomic Apply; no partial effects committed: %w", in.OperationID, err)
	}
	activity, err := activityFromResult(result)
	if err != nil {
		return CommandResult{}, result, err
	}
	return CommandResult{OperationID: in.OperationID, Replayed: result.ShortCircuited, Epoch: epoch, ActivityID: activity, EventIDs: append([]provenance.JournalID(nil), result.EmittedEvents...)}, result, nil
}

func activityFromResult(result provenance.CommittedResult) (provenance.ActivityID, error) {
	for _, slot := range result.ResultSlots {
		if slot.Slot == reviewActivityResultSlot && slot.Kind == provenance.JournalKindActivity && slot.ActivityID != nil {
			return *slot.ActivityID, nil
		}
	}
	return provenance.ActivityID{}, assignmentErr("activityFromResult", "the committed operation has no activity result binding", "assignment command results must identify their complete activity record", "verify journal result-slot integrity and retry")
}

func assignmentPhase(mutation EpochMutationKind) provenance.Phase {
	switch mutation {
	case MutationStartReview, MutationSubmitReview, MutationFinalizeReview:
		return provenance.PhaseReview
	case MutationCreateSlice, MutationSetSliceCandidate, MutationReworkSlice, MutationCloseSlice:
		return provenance.PhaseWorkerSlices
	default:
		return provenance.PhaseImplUAT
	}
}

func epochTaskID(epoch EpochRootID) (provenance.TaskID, error) {
	id, err := provenance.ParseTaskID(string(epoch))
	if err != nil {
		return provenance.TaskID{}, assignmentErr("epochTaskID", fmt.Sprintf("epoch %q is malformed", epoch), "assignment-controlled operations are scoped to an existing epoch task", "supply the canonical epoch task identity")
	}
	return id, nil
}

func deterministicTask(operation provenance.OperationID, label string) provenance.TaskID {
	return provenance.TaskID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("epoch-task:"+string(operation)+":"+label))}
}

func taskCreateEffect(id provenance.TaskID, title string, phase provenance.Phase) provenance.Effect {
	return provenance.Effect{Sort: provenance.EffectTaskCreateAllocated, ResultSlot: provenance.ResultSlotID("task-" + id.String()), TaskID: id, Title: title, Description: title, Type: provenance.TaskTypeTask, Priority: provenance.PriorityMedium, Phase: phase}
}

func epochTaskEvent(task provenance.TaskID, kind provenance.EventKind, payload []byte) (provenance.Effect, error) {
	contexts, err := buildContexts(taskCtx(task))
	if err != nil {
		return provenance.Effect{}, err
	}
	return provenance.Effect{Sort: provenance.EffectTaskEvent, ResultSlot: reviewEventResultSlot, TaskID: task, EventKind: kind, Payload: payload, Contexts: contexts}, nil
}

func edgeEffect(source provenance.TaskID, target provenance.TaskID) provenance.Effect {
	return provenance.Effect{Sort: provenance.EffectEdgeAdd, TaskID: source, EdgeTargetID: target.String(), EdgeRelKind: provenance.EdgeBlockedBy}
}

func assignmentErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation, What: "Pasture rejected an assignment-controlled epoch operation: " + what + ".", Why: why + ".", Where: "Epoch assignment service (internal/tasks/epoch_assignment_service.go, " + where + ").", Impact: "The command did not reach Provenance Apply; no task, evidence, event, activity, or projection was written.", Fix: fix + "."}
}
