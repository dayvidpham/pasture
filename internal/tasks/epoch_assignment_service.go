package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

const (
	reviewStartedEventKind           provenance.EventKind    = "pasture.review.started.v1"
	candidateCreatedEventKind        provenance.EventKind    = "pasture.integration.candidate-created.v1"
	candidateReworkedEventKind       provenance.EventKind    = "pasture.integration.candidate-reworked.v1"
	reviewSubmissionEvidenceKind     provenance.EvidenceKind = "pasture.review.submission.v1"
	reviewActivityResultSlot         provenance.ResultSlotID = "activity"
	reviewEventResultSlot            provenance.ResultSlotID = "event"
	reviewEvidenceResultSlot         provenance.ResultSlotID = "evidence"
	reviewEvidenceResultSlotNext     provenance.ResultSlotID = "evidence-1"
	assignmentCommandResultSlot      provenance.ResultSlotID = "assignment-command"
	assignmentBindingEvidenceKind    provenance.EvidenceKind = "pasture.assignment.binding.v1"
	assignmentCommandEvidenceKind    provenance.EvidenceKind = "pasture.assignment.command.v1"
	reworkOldStateResultSlot         provenance.ResultSlotID = "rework-old-state"
	reworkNewStateResultSlot         provenance.ResultSlotID = "rework-new-state"
	reworkOldStateResultSlotReviewed provenance.ResultSlotID = "rework-old-state-reviewed"
	reworkNewStateResultSlotReviewed provenance.ResultSlotID = "rework-new-state-reviewed"
	reworkBindingResultSlot          provenance.ResultSlotID = "rework-binding"
	reworkRoundResultSlot            provenance.ResultSlotID = "rework-round-authority"
	reworkManifestResultSlot         provenance.ResultSlotID = "rework-manifest"
	reworkReviewResultSlot           provenance.ResultSlotID = "rework-review-authority"
)

// epochAssignmentService is the assignment-authorized half of EpochService.
// Eligibility and authority are reconstructed from Provenance facts on every
// call, and each non-allocating mutation is one Journal.Apply.
type epochAssignmentService struct {
	tracker *trackerImpl
	barrier EpochRaceBarrier
	now     func() time.Time
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
	now := opts.now
	if now == nil {
		now = time.Now
	}
	assignment := &epochAssignmentService{tracker: t, barrier: opts.Synchronization.RaceBarrier, now: now}
	return &epochService{EpochHumanService: human, EpochAssignmentService: assignment}, nil
}

type assignmentResolution struct {
	id        provenance.AssignmentID
	role      AssignmentRole
	occupant  provenance.ActorID
	authority provenance.JournalID
	task      provenance.TaskID
}

// assignmentBinding is the durable relationship contract used by the
// assignment aggregate.  Provenance edges make the relationship visible in
// the graph; this typed fact carries the epoch/parent identity that readers
// must validate before consuming an edge.  It is intentionally private: the
// public API exposes domain IDs, never fact IDs or revisions.
type assignmentBinding struct {
	Kind       provenance.EvidenceKind `json:"kind"`
	Epoch      EpochRootID             `json:"epoch"`
	Plan       provenance.TaskID       `json:"plan"`
	Slice      provenance.TaskID       `json:"slice,omitempty"`
	Candidate  provenance.TaskID       `json:"candidate,omitempty"`
	Repository RepositoryID            `json:"repository,omitempty"`
	Commit     provenance.GitOID       `json:"commit,omitempty"`
	Members    []candidateMember       `json:"members,omitempty"`
	Operation  provenance.OperationID  `json:"operation"`
}

type assignmentCommandRecord struct {
	Mutation   EpochMutationKind       `json:"mutation"`
	Epoch      EpochRootID             `json:"epoch"`
	Payload    json.RawMessage         `json:"payload"`
	Request    json.RawMessage         `json:"request"`
	Assignment provenance.AssignmentID `json:"assignment,omitempty"`
	Role       AssignmentRole          `json:"role,omitempty"`
	Occupant   provenance.ActorID      `json:"occupant,omitempty"`
	Authority  provenance.JournalID    `json:"authority"`
	Task       provenance.TaskID       `json:"task"`
}

func assignmentBindingEffect(subject provenance.TaskID, binding assignmentBinding, slot provenance.ResultSlotID) (provenance.Effect, error) {
	if subject == (provenance.TaskID{}) {
		return provenance.Effect{}, assignmentErr("assignmentBindingEffect", "the binding subject is zero", "durable assignment relationships must be task-scoped", "supply the exact plan, slice, or owning task")
	}
	if binding.Kind == "" || binding.Operation == "" {
		return provenance.Effect{}, assignmentErr("assignmentBindingEffect", "the binding kind or operation is empty", "durable relationships require a typed kind and producer", "construct the binding through an assignment command")
	}
	if _, err := epochTaskID(binding.Epoch); err != nil {
		return provenance.Effect{}, err
	}
	payload, err := canonicalJSON(binding)
	if err != nil {
		return provenance.Effect{}, fmt.Errorf("encode assignment binding: %w", err)
	}
	digest := sha256.Sum256(payload)
	return provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: slot, TaskID: subject, EvidenceKind: assignmentBindingEvidenceKind, ContentDigest: digest[:], Payload: payload}, nil
}

func assignmentRequestCommand(mutation EpochMutationKind, epoch EpochRootID, payload any) (json.RawMessage, error) {
	return canonicalJSON(struct {
		Mutation EpochMutationKind `json:"mutation"`
		Epoch    EpochRootID       `json:"epoch"`
		Payload  any               `json:"payload"`
	}{mutation, epoch, payload})
}

// lookupAssignmentReplay is deliberately the first store operation in every
// assignment command.  Mutable eligibility (assignment liveness, current
// review state, candidate state) must never hide an exact replay.  The
// command evidence is written by the same Apply as the operation and lets us
// compare changed input without inventing a second operation store.
func (s *epochAssignmentService) lookupAssignmentReplay(ctx context.Context, in CommandMeta, mutation EpochMutationKind, epoch EpochRootID, payload any) (provenance.CommittedResult, bool, error) {
	if err := provenance.ValidateOperationID(in.OperationID); err != nil {
		return provenance.CommittedResult{}, false, assignmentErr("lookupAssignmentReplay", fmt.Sprintf("operation %q is invalid", in.OperationID), "replay requires a stable operation identity", "supply a non-empty operation id without control characters")
	}
	if err := ctx.Err(); err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("assignment replay lookup for %q was cancelled: %w", in.OperationID, err)
	}
	committed, err := s.tracker.prov.Journal().LookupCommitted(in.OperationID)
	if err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("lookup committed assignment operation %q: %w", in.OperationID, err)
	}
	if committed.Kind == provenance.CommittedAbsent {
		return committed, false, nil
	}
	if committed.Kind != provenance.CommittedExact {
		return committed, false, assignmentErr("lookupAssignmentReplay", fmt.Sprintf("operation %q returned an unknown committed result %s", in.OperationID, committed.Kind), "replay identity must be a closed Provenance result", "repair the operation result before retrying")
	}
	page, err := s.tracker.prov.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: []provenance.OperationID{in.OperationID, provenance.GovernedAllocationSupplementOperationID(in.OperationID)}},
		Kinds:  []provenance.EvidenceKind{assignmentCommandEvidenceKind},
		Page:   provenance.FactPageRequest{Limit: 2},
	})
	if err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("query replay command evidence for %q: %w", in.OperationID, err)
	}
	if len(page.Rows) != 1 || (page.Rows[0].ProducingOperationID != in.OperationID && page.Rows[0].ProducingOperationID != provenance.GovernedAllocationSupplementOperationID(in.OperationID)) {
		return provenance.CommittedResult{}, false, assignmentErr("lookupAssignmentReplay", fmt.Sprintf("operation %q has %d command evidence rows", in.OperationID, len(page.Rows)), "exact replay requires one command binding produced by the committed operation", "repair the operation evidence before retrying")
	}
	var stored assignmentCommandRecord
	if err := strictJSON(page.Rows[0].Payload, &stored); err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("decode replay command evidence for %q: %w", in.OperationID, err)
	}
	if page.Rows[0].TaskID == nil || *page.Rows[0].TaskID != stored.Task {
		return provenance.CommittedResult{}, false, assignmentErr("lookupAssignmentReplay", fmt.Sprintf("operation %q has command evidence on a different task", in.OperationID), "exact replay requires one task-scoped assignment authority record", "repair the operation evidence from a consistent journal backup")
	}
	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("encode assignment replay payload: %w", err)
	}
	requestedValue, err := decodeAssignmentReplayValue(payloadBytes)
	if err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("decode requested assignment replay payload: %w", err)
	}
	storedValue, err := decodeAssignmentReplayValue(stored.Payload)
	if err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("decode stored assignment replay payload: %w", err)
	}
	var storedEnvelope struct {
		Mutation EpochMutationKind `json:"mutation"`
		Epoch    EpochRootID       `json:"epoch"`
		Payload  json.RawMessage   `json:"payload"`
	}
	if err := strictJSON(stored.Request, &storedEnvelope); err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("decode stored assignment command %q: %w", in.OperationID, err)
	}
	storedEnvelopeValue, err := decodeAssignmentReplayValue(storedEnvelope.Payload)
	if err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("decode stored assignment command payload %q: %w", in.OperationID, err)
	}
	if storedEnvelope.Mutation != stored.Mutation || storedEnvelope.Epoch != stored.Epoch || !reflect.DeepEqual(storedEnvelopeValue, storedValue) {
		return provenance.CommittedResult{}, false, assignmentErr("lookupAssignmentReplay", fmt.Sprintf("operation %q has inconsistent command and payload evidence", in.OperationID), "the durable replay record does not describe one canonical assignment command", "restore the operation and its command evidence from one consistent backup")
	}
	if stored.Assignment == "" || !stored.Role.valid() || stored.Occupant == (provenance.ActorID{}) || stored.Authority <= 0 || stored.Task == (provenance.TaskID{}) {
		return provenance.CommittedResult{}, false, assignmentErr("lookupAssignmentReplay", fmt.Sprintf("operation %q has an incomplete assignment authority record", in.OperationID), "exact replay requires the original assignment, role, occupant, authority, and task", "restore the complete command evidence before retrying")
	}
	birth, err := s.taskBirthJournalID(ctx, stored.Task)
	if err != nil {
		return provenance.CommittedResult{}, false, err
	}
	if stored.Authority <= birth {
		return provenance.CommittedResult{}, false, assignmentErr("lookupAssignmentReplay", fmt.Sprintf("operation %q cites authority %d from before task %q existed", in.OperationID, stored.Authority, stored.Task), "assignment replay cannot adopt bootstrap or unrelated authority", "restore the exact assignment-start authority in the command evidence")
	}
	direct, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(stored.Authority, stored.Task, stored.Authority+1)
	if err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("authenticate replay assignment authority %d for task %q: %w", stored.Authority, stored.Task, err)
	}
	committedUnderAssignment, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(stored.Authority, stored.Task, page.Rows[0].JournalID)
	if err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("authenticate replay command authority %d at evidence %d: %w", stored.Authority, page.Rows[0].JournalID, err)
	}
	if !direct || !committedUnderAssignment {
		return provenance.CommittedResult{}, false, assignmentErr("lookupAssignmentReplay", fmt.Sprintf("operation %q was not committed under its recorded direct assignment authority", in.OperationID), "the command evidence cannot authenticate assignment-controlled provenance", "restore the exact assignment operation and authority rows before retrying")
	}
	if stored.Mutation != mutation || stored.Epoch != epoch || !reflect.DeepEqual(requestedValue, storedValue) {
		conflict := &provenance.OperationConflict{OperationID: in.OperationID, Axis: provenance.ConflictCommand, Index: -1}
		return provenance.CommittedResult{Kind: provenance.CommittedConflict, Conflict: conflict}, false, fmt.Errorf("%w: %w", provenance.ErrOperationConflict, conflict)
	}
	if err := ctx.Err(); err != nil {
		return provenance.CommittedResult{}, false, fmt.Errorf("assignment replay authentication for %q was cancelled: %w", in.OperationID, err)
	}
	return committed, true, nil
}

func decodeAssignmentReplayValue(payload []byte) (any, error) {
	if err := validateAuthorityJSONUTF8(payload, "assignment replay payload"); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("assignment replay payload contains trailing JSON")
		}
		return nil, fmt.Errorf("assignment replay payload has trailing data: %w", err)
	}
	return value, nil
}

func commandResultFromCommitted(in CommandMeta, epoch EpochRootID, committed provenance.CommittedResult) (CommandResult, error) {
	activity, err := activityFromResult(committed)
	if err != nil {
		return CommandResult{}, err
	}
	return CommandResult{OperationID: in.OperationID, Replayed: true, Epoch: epoch, ActivityID: activity, EventIDs: append([]provenance.JournalID(nil), committed.EmittedEvents...)}, nil
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
	if _, err := s.tracker.prov.Show(subject); err != nil {
		return nil, assignmentErr("assignmentScope", fmt.Sprintf("subject %q could not be read", subject), "assignment resolution is scoped to an existing subject and epoch ancestry", "supply an existing subject task")
	}
	epochTask, err := epochTaskID(epoch)
	if err != nil {
		return nil, err
	}
	if _, err := s.tracker.prov.Show(epochTask); err != nil {
		return nil, assignmentErr("assignmentScope", fmt.Sprintf("epoch %q could not be read", epoch), "assignment resolution requires the exact existing epoch named by the durable relationship", "supply the subject's owning epoch")
	}
	relatedTasks, err := s.tracker.prov.Descendants(subject)
	if err != nil {
		return nil, fmt.Errorf("resolve subject ancestry for %q: %w", subject, err)
	}
	owners := make([]provenance.TaskID, 0, len(relatedTasks)+1)
	owners = append(owners, subject)
	for _, related := range relatedTasks {
		if related.ID == subject {
			return nil, assignmentErr("assignmentScope", fmt.Sprintf("subject %q occurs in its own graph ancestry", subject), "assignment relationships must be acyclic", "remove the cyclic task relationship before retrying")
		}
		owners = append(owners, related.ID)
	}

	var relationship assignmentBinding
	var relationshipOwner provenance.TaskID
	matches := 0
	conflictingEpochs := 0
	for _, owner := range owners {
		for _, kind := range []provenance.EvidenceKind{assignmentPlanEpochBindingKind, assignmentPlanSliceBindingKind, assignmentSliceCandidateBindingKind, assignmentPlanIntegrationBindingKind} {
			bindings, err := s.exactAssignmentBindings(owner, kind)
			if err != nil {
				return nil, err
			}
			for _, binding := range bindings {
				related, err := s.bindingRelatesToSubject(owner, subject, binding)
				if err != nil {
					return nil, err
				}
				if related {
					if binding.Epoch != epoch {
						conflictingEpochs++
						continue
					}
					matches++
					relationship = binding
					relationshipOwner = owner
				}
			}
		}
	}
	if conflictingEpochs != 0 {
		return nil, assignmentErr("assignmentScope", fmt.Sprintf("subject %q has %d durable relationships outside epoch %q", subject, conflictingEpochs, epoch), "one subject cannot be authorized through a different or overlapping epoch graph", "use the subject's sole owning epoch or repair the conflicting bindings")
	}
	if matches == 0 {
		return nil, assignmentErr("assignmentScope", fmt.Sprintf("subject %q has no exact durable relationship to epoch %q", subject, epoch), "assignment resolution cannot authorize an unrelated epoch and subject pair", "create the subject through the assignment aggregate for the requested epoch")
	}
	if matches != 1 {
		return nil, assignmentErr("assignmentScope", fmt.Sprintf("subject %q has %d exact relationships to epoch %q", subject, matches, epoch), "assignment resolution requires one unambiguous durable epoch relationship", "repair duplicate or conflicting graph bindings before retrying")
	}

	// The first task is always the command subject. resolveUniqueAssignment uses
	// it to prove that an assignment found on a typed owner or epoch ancestor
	// actually governs this exact subject through assignment-parent citations.
	scope := make([]provenance.TaskID, 0, 5)
	seen := map[provenance.TaskID]struct{}{}
	add := func(task provenance.TaskID) {
		if task == (provenance.TaskID{}) {
			return
		}
		if _, ok := seen[task]; ok {
			return
		}
		seen[task] = struct{}{}
		scope = append(scope, task)
	}
	add(subject)
	add(relationshipOwner)
	switch relationship.Kind {
	case assignmentPlanEpochBindingKind:
		add(relationship.Plan)
	case assignmentPlanSliceBindingKind:
		add(relationship.Slice)
		add(relationship.Plan)
	case assignmentSliceCandidateBindingKind:
		add(relationship.Candidate)
		add(relationship.Slice)
		add(relationship.Plan)
	case assignmentPlanIntegrationBindingKind:
		add(relationship.Candidate)
		add(relationship.Plan)
	default:
		return nil, assignmentErr("assignmentScope", fmt.Sprintf("subject %q has unknown relationship kind %q", subject, relationship.Kind), "assignment scope consumes a closed typed relationship", "repair the relationship binding before retrying")
	}
	add(epochTask)
	return scope, nil
}

func (s *epochAssignmentService) findAssignments(ctx context.Context, tasks []provenance.TaskID, wanted provenance.AssignmentID, want AssignmentRole) ([]assignmentResolution, error) {
	if len(tasks) == 0 {
		return nil, assignmentErr("findAssignments", "the target task set is empty", "assignment authority must be scoped to a task", "supply an existing epoch or subject task")
	}
	if !want.valid() {
		return nil, assignmentErr("findAssignments", fmt.Sprintf("assignment role %q is unknown", want.String()), "assignment resolution accepts only the closed Pasture role set", "use owner-responsibility, governing-supervisor, or axis-reviewer")
	}
	subject := tasks[0]
	if subject == (provenance.TaskID{}) {
		return nil, assignmentErr("findAssignments", "the command subject is zero", "the first assignment-scope task must be the exact command subject", "supply a valid subject task")
	}
	validTasks := make(map[provenance.TaskID]struct{}, len(tasks))
	queryTasks := make([]provenance.TaskID, 0, len(tasks))
	for _, task := range tasks {
		if task == (provenance.TaskID{}) {
			continue
		}
		if _, duplicate := validTasks[task]; duplicate {
			continue
		}
		validTasks[task] = struct{}{}
		queryTasks = append(queryTasks, task)
	}
	if len(validTasks) == 0 {
		return nil, assignmentErr("findAssignments", "all target task identities are zero", "assignment authority cannot be resolved without a task", "supply valid Provenance task ids")
	}
	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: queryTasks, EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()}, Limit: provenance.MaxFactPageSize}
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
			if _, inScope := validTasks[row.TaskID]; !inScope {
				return nil, assignmentErr("findAssignments", fmt.Sprintf("assignment-start event %d returned task %q outside the exact scope", row.JournalID, row.TaskID), "assignment queries must not admit unrelated task authorities", "retry after repairing the journal query boundary")
			}
			occupant, err := provenance.ParseActorID(value.Occupant)
			if err != nil || occupant == (provenance.ActorID{}) {
				return nil, assignmentErr("findAssignments", fmt.Sprintf("assignment-start event %d has an invalid occupant", row.JournalID), "assignment attribution must resolve to the event's registered actor", "repair the assignment-start fact before retrying")
			}
			authority, ok, err := s.assignmentAuthorityForEvent(ctx, row, row.TaskID)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			active, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(authority, row.TaskID, provenance.JournalID(math.MaxInt64))
			if err != nil {
				return nil, fmt.Errorf("check current governing assignment %q for task %q: %w", value.Assignment, row.TaskID, err)
			}
			if !active {
				continue
			}
			governsSubject, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(authority, subject, provenance.JournalID(math.MaxInt64))
			if err != nil {
				return nil, fmt.Errorf("check assignment %q from task %q against command subject %q: %w", value.Assignment, row.TaskID, subject, err)
			}
			if !governsSubject {
				continue
			}
			// Do not collapse duplicate material records. A repeated event or
			// overlapping role episode is ambiguous authority and must fail closed
			// in resolveAssignment/resolveUniqueAssignment.
			found = append(found, assignmentResolution{id: provenance.AssignmentID(value.Assignment), role: want, occupant: occupant, authority: authority, task: row.TaskID})
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

func (s *epochAssignmentService) assignmentAuthorityForEvent(ctx context.Context, row provenance.TaskEventRow, task provenance.TaskID) (provenance.JournalID, bool, error) {
	birth, err := s.taskBirthJournalID(ctx, task)
	if err != nil {
		return 0, false, err
	}
	if row.JournalID <= birth+1 {
		return 0, false, assignmentErr("assignmentAuthorityForEvent", fmt.Sprintf("assignment-start event %d does not follow task %q creation", row.JournalID, task), "an assignment authority must be committed after its task exists and before its material event", "repair the assignment operation chronology before retrying")
	}

	// Ordinary assignment starts place their authority between the operation
	// anchor and material event. Governed allocation creates the child authority
	// before its supplemental operation, so fall back across that earlier range.
	// Both searches walk newest-first and stop at task birth, which excludes the
	// bootstrap authority without retaining a bootstrap singleton in this service.
	scan := func(high, low provenance.JournalID) (provenance.JournalID, bool, error) {
		for candidate := high; candidate > low; candidate-- {
			if err := ctx.Err(); err != nil {
				return 0, false, fmt.Errorf("resolve assignment authority for event %d: context ended: %w", row.JournalID, err)
			}
			direct, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(candidate, task, candidate+1)
			if err != nil {
				return 0, false, fmt.Errorf("classify possible assignment authority %d for task %q: %w", candidate, task, err)
			}
			if !direct {
				continue
			}
			governsAtEvent, err := s.tracker.prov.Journal().AuthorityGovernsTaskAt(candidate, task, row.JournalID)
			if err != nil {
				return 0, false, fmt.Errorf("check assignment authority %d at material event %d for task %q: %w", candidate, row.JournalID, task, err)
			}
			if governsAtEvent {
				return candidate, true, nil
			}
		}
		return 0, false, nil
	}

	if row.ProducedByOperationJournalID != nil && *row.ProducedByOperationJournalID > birth && *row.ProducedByOperationJournalID < row.JournalID {
		if authority, found, err := scan(row.JournalID-1, *row.ProducedByOperationJournalID); err != nil || found {
			return authority, found, err
		}
		return scan(*row.ProducedByOperationJournalID-1, birth)
	}
	return scan(row.JournalID-1, birth)
}

func (s *epochAssignmentService) taskBirthJournalID(ctx context.Context, task provenance.TaskID) (provenance.JournalID, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("resolve creation journal for task %q: context ended: %w", task, err)
	}
	attributions, err := s.tracker.prov.Journal().TaskAttributions(task)
	if err != nil {
		return 0, fmt.Errorf("resolve creation attribution for task %q: %w", task, err)
	}
	var birth provenance.JournalID
	for _, attribution := range attributions {
		if attribution.TaskID != task || attribution.FirstJournalID <= 0 {
			return 0, assignmentErr("taskBirthJournalID", fmt.Sprintf("task %q has malformed attribution chronology", task), "assignment authority resolution requires an exact task creation boundary", "repair the task attribution projection before retrying")
		}
		if birth == 0 || attribution.FirstJournalID < birth {
			birth = attribution.FirstJournalID
		}
	}
	if birth == 0 {
		return 0, assignmentErr("taskBirthJournalID", fmt.Sprintf("task %q has no creation attribution", task), "assignment authority cannot be distinguished from bootstrap authority without the task's material creation boundary", "replay or repair the task attribution projection before retrying")
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("resolve creation journal for task %q: context ended after attribution read: %w", task, err)
	}
	return birth, nil
}

func (s *epochAssignmentService) apply(ctx context.Context, in CommandMeta, epoch EpochRootID, resolution assignmentResolution, mutation EpochMutationKind, payload any, conditions []provenance.Condition, effects []provenance.Effect) (CommandResult, provenance.CommittedResult, error) {
	if err := provenance.ValidateOperationID(in.OperationID); err != nil {
		return CommandResult{}, provenance.CommittedResult{}, assignmentErr("apply", fmt.Sprintf("operation %q is invalid", in.OperationID), "assignment operations require stable replay identities", "supply a non-empty operation id without control characters")
	}
	if resolution.id == "" || !resolution.role.valid() || resolution.occupant == (provenance.ActorID{}) || resolution.authority <= 0 || resolution.task == (provenance.TaskID{}) {
		return CommandResult{}, provenance.CommittedResult{}, assignmentErr("apply", "the resolved assignment authority is incomplete", "assignment-controlled writes require one exact assignment, role, occupant, authority, and task", "resolve the command against one active assignment before applying it")
	}
	epochTask, err := epochTaskID(epoch)
	if err != nil {
		return CommandResult{}, provenance.CommittedResult{}, err
	}
	if _, err := s.tracker.prov.Show(epochTask); err != nil {
		return CommandResult{}, provenance.CommittedResult{}, assignmentErr("apply", fmt.Sprintf("epoch %q could not be read", epoch), "assignment operations cannot write against a missing epoch", "supply an existing epoch task")
	}
	request, err := assignmentRequestCommand(mutation, epoch, payload)
	if err != nil {
		return CommandResult{}, provenance.CommittedResult{}, fmt.Errorf("encode assignment command: %w", err)
	}
	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return CommandResult{}, provenance.CommittedResult{}, fmt.Errorf("encode assignment command payload: %w", err)
	}
	commandRecord, err := canonicalJSON(assignmentCommandRecord{Mutation: mutation, Epoch: epoch, Payload: payloadBytes, Request: request, Assignment: resolution.id, Role: resolution.role, Occupant: resolution.occupant, Authority: resolution.authority, Task: resolution.task})
	if err != nil {
		return CommandResult{}, provenance.CommittedResult{}, fmt.Errorf("encode assignment command authority: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return CommandResult{}, provenance.CommittedResult{}, fmt.Errorf("assignment operation %q was cancelled before Apply: %w", in.OperationID, err)
	}
	if s.barrier != nil {
		if err := s.barrier.AfterPreflight(ctx, mutation); err != nil {
			return CommandResult{}, provenance.CommittedResult{}, assignmentErr("apply", "the operation was rejected at the injected pre-commit barrier", "the synchronization seam stopped the command before Provenance Apply", "retry after the competing operation has settled")
		}
	}
	commandDigest := sha256.Sum256(request)
	commandPayloadDigest := sha256.Sum256(commandRecord)
	commandEffect := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: assignmentCommandResultSlot, TaskID: resolution.task, EvidenceKind: assignmentCommandEvidenceKind, ContentDigest: commandPayloadDigest[:], Payload: commandRecord}
	effects = append([]provenance.Effect{commandEffect}, effects...)
	activityID := provenance.ActivityID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("assignment-activity:"+string(in.OperationID)))}
	effects = append(effects, provenance.Effect{Sort: provenance.EffectActivityCreate, ResultSlot: reviewActivityResultSlot, ActivityID: activityID, ActivityAgentID: resolution.occupant, ActivityPhase: assignmentPhase(mutation), ActivityStage: provenance.StageComplete, ActivityNotes: "assignment-controlled epoch operation"})
	result, err := s.tracker.prov.Journal().Apply(provenance.OperationInput{OperationID: in.OperationID, ActorID: resolution.occupant, AuthorityJournalID: &resolution.authority, CommandDigest: commandDigest[:], RecordedAt: s.now().UTC().UnixNano(), Conditions: conditions, Effects: effects})
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
