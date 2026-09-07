package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
)

const (
	createSliceAssignmentEventSlot provenance.ResultSlotID = "child-assignment-event"
	createSliceEventSlot           provenance.ResultSlotID = "slice-event"
	candidateCreatedEventSlot      provenance.ResultSlotID = "candidate-created-event"
	candidateAssignmentEventSlot   provenance.ResultSlotID = "candidate-assignment-event"
)

type composedAllocationRunner interface {
	RunAllocateComposed(context.Context, string, provenance.JournalID, provenance.GovernedAllocationComposedRequest) (provenance.GovernedAllocationComposedResult, error)
	RunAllocateComposedBatch(context.Context, string, provenance.JournalID, provenance.GovernedAllocationComposedRequest) (provenance.GovernedAllocationComposedResult, error)
}

// SupportsEngineGovernedAllocation reports whether tracker can receive the
// engine-owned allocation capability. Activity-only sinks intentionally return
// false: recording transition activity must not require governed task creation.
func SupportsEngineGovernedAllocation(tracker interface{}) bool {
	_, ok := tracker.(*trackerImpl)
	return ok
}

// BindEngineGovernedAllocation installs the narrow allocation capability built
// by the durable engine. It intentionally accepts no DBOS or SQL handle.
func BindEngineGovernedAllocation(tracker interface{}, runner composedAllocationRunner) error {
	t, ok := tracker.(*trackerImpl)
	if !ok || t == nil {
		return fmt.Errorf("tasks.BindEngineGovernedAllocation: the engine tracker is %T, not Pasture's unified tracker; no CreateSlice capability was installed; configure engine.Config.Tracker with tasks.OpenTaskTracker", tracker)
	}
	if runner == nil {
		return fmt.Errorf("tasks.BindEngineGovernedAllocation: the engine supplied a nil composed-allocation runner; no CreateSlice capability was installed; bind Provenance on the engine root before Launch")
	}
	if t.allocationRunner != nil {
		return fmt.Errorf("tasks.BindEngineGovernedAllocation: this tracker already has an engine-owned composed-allocation runner; replacing it could split durable workflow ownership; construct one engine per tracker")
	}
	t.allocationRunner = runner
	return nil
}

// UnbindEngineGovernedAllocation removes a capability installed by
// BindEngineGovernedAllocation, so the tracker can accept another engine. Its
// caller is the durable engine's own construction-failure path: a half-built
// engine that kept the slot would leave the tracker pointing at a stopped
// durable context, and would make every later engine on that tracker fail to
// bind.
//
// It removes the slot only when runner is the exact value that occupies it, so
// a stale unbind cannot detach a live engine. Removing an already-empty or
// differently-owned slot is a no-op; there is no error to act on.
func UnbindEngineGovernedAllocation(tracker interface{}, runner composedAllocationRunner) {
	t, ok := tracker.(*trackerImpl)
	if !ok || t == nil || runner == nil {
		return
	}
	if t.allocationRunner != runner {
		return
	}
	t.allocationRunner = nil
}

// GovernedAllocationAuditParticipant persists Pasture's projection inside the governed
// allocation transaction. Engine.New is its sole production composition site.
func GovernedAllocationAuditParticipant(ctx context.Context, tx provenance.GovernedAllocationTransaction, request provenance.GovernedAllocationRequest, closure provenance.OperationClosure) error {
	children := closure.Children()
	if closure.OperationID() != request.OperationID || len(request.Children) == 0 || len(children) != len(request.Children) {
		return fmt.Errorf("CreateSlice participant rejected operation %q: request and committed closure differ; the fused transaction will roll back; retry with the exact canonical child binding", request.OperationID)
	}
	tasks := make([]string, len(children))
	assignments := make([]string, len(children))
	occupants := make([]string, len(children))
	for i, child := range children {
		want := request.Children[i]
		if child.Ordinal != i || child.TaskID != want.TaskID || child.AssignmentID != want.AssignmentID || child.Occupant != want.Occupant {
			return fmt.Errorf("CreateSlice participant rejected child %d of operation %q: request order or binding differs from the committed closure; the fused transaction will roll back; retry with the exact canonical batch", i, request.OperationID)
		}
		tasks[i], assignments[i], occupants[i] = child.TaskID.String(), string(child.AssignmentID), child.Occupant.String()
	}
	taskJSON, _ := canonicalJSON(tasks)
	assignmentJSON, _ := canonicalJSON(assignments)
	occupantJSON, _ := canonicalJSON(occupants)
	result, err := tx.Exec(ctx, `INSERT INTO pasture_governed_allocation_audit(operation_id, closure_anchor, child_task_id, child_assignment_id, occupant_id) VALUES(?, ?, ?, ?, ?) ON CONFLICT(operation_id) DO NOTHING`, request.OperationID, closure.AnchorJournalID(), taskJSON, assignmentJSON, occupantJSON)
	if err != nil {
		return fmt.Errorf("CreateSlice participant could not persist audit row for operation %q: %w; the fused transaction will roll back", request.OperationID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("CreateSlice participant could not inspect audit write for operation %q: %w; the fused transaction will roll back", request.OperationID, err)
	}
	if affected == 1 {
		return nil
	}
	var anchor int64
	var taskID, assignmentID, occupantID string
	if err := tx.QueryRow(ctx, `SELECT closure_anchor, child_task_id, child_assignment_id, occupant_id FROM pasture_governed_allocation_audit WHERE operation_id = ?`, request.OperationID).Scan(&anchor, &taskID, &assignmentID, &occupantID); err != nil {
		return fmt.Errorf("CreateSlice participant could not validate replay audit row for operation %q: %w; the fused transaction will roll back", request.OperationID, err)
	}
	if provenance.JournalID(anchor) != closure.AnchorJournalID() || taskID != string(taskJSON) || assignmentID != string(assignmentJSON) || occupantID != string(occupantJSON) {
		return fmt.Errorf("CreateSlice participant found conflicting audit row for operation %q; the fused transaction will roll back; repair the Pasture projection before retrying", request.OperationID)
	}
	return nil
}

// CreateSliceAuditParticipant is retained for callers compiled against the
// initial narrow binding name. All governed allocations share the participant.
func CreateSliceAuditParticipant(ctx context.Context, tx provenance.GovernedAllocationTransaction, request provenance.GovernedAllocationRequest, closure provenance.OperationClosure) error {
	return GovernedAllocationAuditParticipant(ctx, tx, request, closure)
}

// exactCandidateParentAuthority selects the assignment-start authority that is
// paired with the already-resolved material event. This matters when a task has
// historical overlapping assignment episodes: governed allocation requires the
// exact parent assignment row, not merely an older authority that governed the
// same task at the event timestamp.
func (s *epochAssignmentService) exactCandidateParentAuthority(ctx context.Context, resolution assignmentResolution) (assignmentResolution, error) {
	refuse := func(problem string) error {
		return assignmentErr("exactCandidateParentAuthority", problem,
			"a command parent must match an exact public assignment start and authenticated material, not a neighboring journal row",
			"supply the exact active assignment; repair inconsistent history or request an explicitly reviewed larger command proof budget")
	}
	if resolution.authority <= 0 || resolution.id == "" || resolution.task == (provenance.TaskID{}) {
		return assignmentResolution{}, refuse("the resolved parent identity is incomplete")
	}
	journal := s.tracker.prov.Journal()
	api, ok := journal.(provenance.AssignmentStartQueryAPI)
	if !ok {
		return assignmentResolution{}, refuse("the journal lacks public assignment-start queries")
	}
	if err := ctx.Err(); err != nil {
		return assignmentResolution{}, err
	}
	// The resolver supplies a candidate authority, not proof. Pin the identity
	// query to that exact position so unrelated later candidates cannot make a
	// point lookup incomplete. Current liveness is checked separately below.
	identity, err := api.QueryAssignmentStarts(provenance.AssignmentStartQuery{
		AssignmentIDs: []provenance.AssignmentID{resolution.id},
		TaskIDs:       []provenance.TaskID{resolution.task},
		ActorIDs:      []provenance.ActorID{resolution.occupant},
		SlotIDs:       []provenance.AssignmentSlotID{provenance.SlotOwnerResponsibility},
		Page: provenance.AssignmentStartPageRequest{
			Limit:                1,
			SnapshotPinned:       true,
			SnapshotMaxJournalID: resolution.authority,
			AfterJournalID:       resolution.authority - 1,
		},
	})
	if err != nil {
		return assignmentResolution{}, fmt.Errorf("read exact parent assignment %q: %w", resolution.id, err)
	}
	if len(identity.Rows) != 1 || identity.Next != nil || identity.Rows[0].AuthorityJournalID != resolution.authority {
		return assignmentResolution{}, refuse("the proposed authority is missing, mismatched or not an exhausted exact start")
	}
	starts := identity.Rows
	after := resolution.authority
	// Transfer parents need their historical predecessor proof, not a guessed
	// owner role. This command-only path has a finite history cap and no index
	// dependency. Ordinary/composed parents keep the direct point lookup.
	const maxParentProofPages = 16
	if starts[0].PredecessorAssignmentID != nil {
		birth, err := s.taskBirthJournalID(ctx, resolution.task)
		if err != nil {
			return assignmentResolution{}, err
		}
		after = birth
		starts = nil
		cursor := birth
		for pageNumber := 0; ; pageNumber++ {
			if pageNumber == maxParentProofPages {
				return assignmentResolution{}, refuse("transfer parent history exceeds the bounded command proof budget")
			}
			if err := ctx.Err(); err != nil {
				return assignmentResolution{}, err
			}
			page, err := api.QueryAssignmentStarts(provenance.AssignmentStartQuery{
				TaskIDs: []provenance.TaskID{resolution.task},
				Page: provenance.AssignmentStartPageRequest{
					Limit: 64, SnapshotPinned: true, SnapshotMaxJournalID: resolution.authority, AfterJournalID: cursor,
				},
			})
			if err != nil {
				return assignmentResolution{}, err
			}
			starts = append(starts, page.Rows...)
			if page.Next == nil {
				break
			}
			if page.Next.AfterJournalID <= cursor {
				return assignmentResolution{}, refuse("the parent history cursor made no progress")
			}
			cursor = page.Next.AfterJournalID
		}
	}
	var material []provenance.TaskEventRow
	var snapshot provenance.JournalID
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber == maxParentProofPages {
			return assignmentResolution{}, refuse("parent material history exceeds the bounded command proof budget")
		}
		if err := ctx.Err(); err != nil {
			return assignmentResolution{}, err
		}
		page, err := journal.QueryTaskEvents(provenance.JournalQueryV1{
			OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{resolution.task},
			EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()}, Limit: 64,
			AfterJournalID: after, SnapshotMaxJournalID: snapshot,
		})
		if err != nil {
			return assignmentResolution{}, err
		}
		snapshot = page.SnapshotMaxJournalID
		material = append(material, page.Events...)
		if page.Next == nil {
			break
		}
		if page.Next.AfterJournalID <= after {
			return assignmentResolution{}, refuse("the parent material cursor made no progress")
		}
		after = page.Next.AfterJournalID
	}
	decoded, err := decodeRecoveryMaterials(material, snapshot)
	if err != nil {
		return assignmentResolution{}, err
	}
	var operations []provenance.OperationID
	seen := map[provenance.OperationID]bool{}
	for _, start := range starts {
		fact, present := decoded[recoveryMaterialKey{start.TaskID, start.AssignmentID}]
		if start.PredecessorAssignmentID != nil || (present && *fact.Row.ProducedByOperationJournalID == start.ProducingOperationJournalID) {
			continue
		}
		operation := provenance.GovernedAllocationSupplementOperationID(start.ProducingOperationID)
		if !seen[operation] {
			seen[operation] = true
			operations = append(operations, operation)
		}
	}
	if len(operations) > provenance.MaxFactFilterValues {
		return assignmentResolution{}, refuse("parent supplement population exceeds the public filter bound")
	}
	var evidence []provenance.EvidenceRow
	if len(operations) > 0 {
		query := provenance.EvidenceQuery{
			Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: operations},
			Kinds:  []provenance.EvidenceKind{assignmentCommandEvidenceKind, reviewRoundAuthorityEvidenceKind},
			Page:   provenance.FactPageRequest{Limit: 64, SnapshotMaxJournalID: snapshot},
		}
		for pageNumber := 0; ; pageNumber++ {
			if pageNumber == maxParentProofPages {
				return assignmentResolution{}, refuse("parent evidence exceeds the bounded command proof budget")
			}
			if err := ctx.Err(); err != nil {
				return assignmentResolution{}, err
			}
			page, err := journal.Facts().QueryEvidence(query)
			if err != nil {
				return assignmentResolution{}, err
			}
			evidence = append(evidence, page.Rows...)
			if page.Next == nil {
				break
			}
			if page.Next.AfterJournalID <= query.Page.AfterJournalID {
				return assignmentResolution{}, refuse("the parent evidence cursor made no progress")
			}
			query.Page.AfterJournalID = page.Next.AfterJournalID
		}
	}
	proof, err := authenticateRecoveryRows(ctx, journal, provenance.AssignmentStartPage{
		Rows: starts, SnapshotPinned: true, SnapshotMaxJournalID: snapshot,
	}, material, evidence, map[provenance.AssignmentID]startedEpisode{}, snapshot)
	if err != nil {
		return assignmentResolution{}, err
	}
	matched := 0
	for _, row := range proof.Rows {
		if row.Assignment == resolution.id {
			if row.Task != resolution.task || row.Actor != resolution.occupant || row.Role != resolution.role || row.Authority != resolution.authority {
				return assignmentResolution{}, refuse("authenticated parent material differs from the resolved command parent")
			}
			matched++
		}
	}
	if matched != 1 {
		return assignmentResolution{}, refuse("the parent proof is missing or duplicated")
	}
	active, err := journal.AuthorityGovernsTaskAt(resolution.authority, resolution.task, provenance.JournalID(^uint64(0)>>1))
	if err != nil || !active {
		return assignmentResolution{}, assignmentErr("exactCandidateParentAuthority", fmt.Sprintf("assignment %q is not currently active", resolution.id), "governed candidate allocation cannot use an ended or malformed parent assignment", "start the required parent assignment and retry")
	}
	return resolution, nil
}

func (s *epochAssignmentService) allocateCandidateComposed(ctx context.Context, meta CommandMeta, epoch EpochRootID, resolution assignmentResolution, mutation EpochMutationKind, payload any, candidate provenance.TaskID, assignment provenance.AssignmentID, role AssignmentRole, title string, phase provenance.Phase, effects []provenance.Effect) (CommandResult, error) {
	request, err := assignmentRequestCommand(mutation, epoch, payload)
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused mutation %d command: %w", mutation, err)
	}
	payloadBytes, err := canonicalJSON(payload)
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused mutation %d payload: %w", mutation, err)
	}
	commandRecord, err := canonicalJSON(assignmentCommandRecord{Mutation: mutation, Epoch: epoch, Payload: payloadBytes, Request: request, Assignment: resolution.id, Role: resolution.role, Occupant: resolution.occupant, Authority: resolution.authority, Task: resolution.task})
	if err != nil {
		return CommandResult{}, fmt.Errorf("encode fused mutation %d authority: %w", mutation, err)
	}
	digest := sha256.Sum256(commandRecord)
	effects = append([]provenance.Effect{{Sort: provenance.EffectEvidence, ResultSlot: assignmentCommandResultSlot, TaskID: resolution.task, EvidenceKind: assignmentCommandEvidenceKind, ContentDigest: digest[:], Payload: commandRecord}}, effects...)
	activityID := provenance.ActivityID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("assignment-activity:"+string(meta.OperationID)))}
	effects = append(effects, provenance.Effect{Sort: provenance.EffectActivityCreate, ResultSlot: reviewActivityResultSlot, ActivityID: activityID, ActivityAgentID: resolution.occupant, ActivityPhase: phase, ActivityStage: provenance.StageComplete, ActivityNotes: "assignment-controlled epoch operation"})
	allocator := s.tracker.allocationRunner
	if allocator == nil {
		return CommandResult{}, assignmentErr("allocateCandidateComposed", "the tracker has no engine-owned composed-allocation runner", "fresh candidate creation must share the durable engine's DBOS root and SQLite handle", "construct and launch the engine with this tracker before creating a candidate")
	}
	composed := provenance.GovernedAllocationComposedRequest{Version: provenance.GovernedAllocationCompositionV1, Allocation: provenance.GovernedAllocationRequest{
		OperationID: meta.OperationID, ActorID: resolution.occupant, Command: fmt.Sprintf("pasture.epoch.mutation-%d.v1", mutation), ParentAssignmentID: resolution.id,
		Children: []provenance.GovernedChildSpec{{TaskID: candidate, AssignmentID: assignment, Occupant: resolution.occupant, Title: title, Description: title, Type: provenance.TaskTypeTask, Priority: provenance.PriorityMedium, Phase: phase}},
	}, SupplementalEffects: effects}
	result, err := allocator.RunAllocateComposed(ctx, "pasture-candidate:"+string(meta.OperationID), resolution.authority, composed)
	if err != nil {
		return CommandResult{}, fmt.Errorf("mutation %d operation %q failed in its fused governed-allocation transaction; no partial allocation, journal, audit, or DBOS output committed: %w", mutation, meta.OperationID, err)
	}
	children := result.Closure().Children()
	if len(children) != 1 || children[0].TaskID != candidate || children[0].AssignmentID != assignment || children[0].Occupant != resolution.occupant {
		return CommandResult{}, assignmentErr("allocateCandidateComposed", "the composed result did not contain the exact requested candidate, assignment, and occupant", "candidate commands return only their caller-stable governed child closure", "repair the composed receipt before retrying")
	}
	// The started-episode index is written HERE, after the commit, from the
	// closure this command just received. The authority id does not exist before
	// the commit, so this cannot move inside it.
	if err := s.indexComposedEpisode(ctx, result, candidate, assignment, role, resolution.occupant); err != nil {
		return CommandResult{}, err
	}

	needed := map[provenance.ResultSlotID]bool{assignmentCommandResultSlot: false, reviewActivityResultSlot: false, candidateCreatedEventSlot: false, candidateAssignmentEventSlot: false}
	for _, effect := range effects {
		if effect.Sort == provenance.EffectEvidence && effect.ResultSlot != assignmentCommandResultSlot {
			needed[effect.ResultSlot] = false
		}
		if effect.Sort == provenance.EffectEvidence && (effect.ResultSlot == assignmentBindingSlotPlan || effect.ResultSlot == assignmentBindingSlotRelationship || effect.ResultSlot == assignmentBindingSlotCandidate) {
			needed[effect.ResultSlot] = false
		}
	}
	var activity provenance.ActivityID
	for _, slot := range result.SupplementalResultSlots() {
		if _, ok := needed[slot.Slot]; ok {
			needed[slot.Slot] = true
		}
		if slot.Slot == reviewActivityResultSlot && slot.ActivityID != nil {
			activity = *slot.ActivityID
		}
	}
	for slot, present := range needed {
		if !present {
			return CommandResult{}, assignmentErr("allocateCandidateComposed", fmt.Sprintf("the composed result omitted canonical slot %q", slot), "candidate results must bind every command, evidence, event, assignment, and activity receipt", "repair the composed receipt before retrying")
		}
	}
	return CommandResult{OperationID: meta.OperationID, Replayed: result.Replayed(), Epoch: epoch, ActivityID: activity, EventIDs: result.SupplementalEmittedEvents()}, nil
}

func (s *epochAssignmentService) createSliceComposed(ctx context.Context, in CreateSliceInput, resolution assignmentResolution, role AssignmentRole, effects []provenance.Effect) (SliceResult, error) {
	requestPayload := struct {
		Plan       provenance.TaskID       `json:"plan"`
		Assignment provenance.AssignmentID `json:"assignment"`
	}{in.Plan, in.Assignment}
	request, err := assignmentRequestCommand(MutationCreateSlice, in.Epoch, requestPayload)
	if err != nil {
		return SliceResult{}, fmt.Errorf("encode fused CreateSlice command: %w", err)
	}
	payloadBytes, err := canonicalJSON(requestPayload)
	if err != nil {
		return SliceResult{}, fmt.Errorf("encode fused CreateSlice payload: %w", err)
	}
	commandRecord, err := canonicalJSON(assignmentCommandRecord{Mutation: MutationCreateSlice, Epoch: in.Epoch, Payload: payloadBytes, Request: request, Assignment: resolution.id, Role: resolution.role, Occupant: resolution.occupant, Authority: resolution.authority, Task: resolution.task})
	if err != nil {
		return SliceResult{}, fmt.Errorf("encode fused CreateSlice authority: %w", err)
	}
	commandPayloadDigest := sha256.Sum256(commandRecord)
	effects = append([]provenance.Effect{{Sort: provenance.EffectEvidence, ResultSlot: assignmentCommandResultSlot, TaskID: resolution.task, EvidenceKind: assignmentCommandEvidenceKind, ContentDigest: commandPayloadDigest[:], Payload: commandRecord}}, effects...)
	activityID := provenance.ActivityID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("assignment-activity:"+string(in.Meta.OperationID)))}
	effects = append(effects, provenance.Effect{Sort: provenance.EffectActivityCreate, ResultSlot: reviewActivityResultSlot, ActivityID: activityID, ActivityAgentID: resolution.occupant, ActivityPhase: provenance.PhaseWorkerSlices, ActivityStage: provenance.StageComplete, ActivityNotes: "assignment-controlled epoch operation"})

	slice := deterministicTask(in.Meta.OperationID, "slice")
	childAssignment := provenance.AssignmentID(string(in.Meta.OperationID) + "-slice-owner")
	allocator := s.tracker.allocationRunner
	if allocator == nil {
		return SliceResult{}, assignmentErr("createSliceComposed", "the tracker has no engine-owned composed-allocation runner", "CreateSlice must execute on the same DBOS root and SQLite system handle as the durable engine", "construct and launch the engine with this tracker before calling CreateSlice")
	}
	composed := provenance.GovernedAllocationComposedRequest{
		Version: provenance.GovernedAllocationCompositionV1,
		Allocation: provenance.GovernedAllocationRequest{
			OperationID: in.Meta.OperationID, ActorID: resolution.occupant, Command: "pasture.epoch.create-slice.v1", ParentAssignmentID: resolution.id,
			Children: []provenance.GovernedChildSpec{{TaskID: slice, AssignmentID: childAssignment, Occupant: resolution.occupant, Title: "implementation slice", Description: "implementation slice", Type: provenance.TaskTypeTask, Priority: provenance.PriorityMedium, Phase: provenance.PhaseWorkerSlices}},
		},
		SupplementalEffects: effects,
	}
	result, err := allocator.RunAllocateComposed(ctx, "pasture-create-slice:"+string(in.Meta.OperationID), resolution.authority, composed)
	if err != nil {
		return SliceResult{}, fmt.Errorf("CreateSlice operation %q failed in its fused governed-allocation transaction; no partial allocation, journal, audit, or DBOS output committed: %w", in.Meta.OperationID, err)
	}
	slots := result.SupplementalResultSlots()
	var activity provenance.ActivityID
	for _, slot := range slots {
		if slot.Slot == reviewActivityResultSlot && slot.ActivityID != nil {
			activity = *slot.ActivityID
		}
	}
	if activity == (provenance.ActivityID{}) {
		return SliceResult{}, assignmentErr("createSliceComposed", "the composed result omitted the activity binding", "CreateSlice results must map the canonical activity slot", "repair the composed receipt before retrying")
	}
	if err := s.indexComposedEpisode(ctx, result, slice, childAssignment, role, resolution.occupant); err != nil {
		return SliceResult{}, err
	}

	closure := result.Closure()
	children := closure.Children()
	if len(children) != 1 || children[0].TaskID != slice || children[0].AssignmentID != childAssignment || !bytes.Equal(request, commandRecordRequest(commandRecord)) {
		return SliceResult{}, assignmentErr("createSliceComposed", "the composed result did not match the requested child closure", "CreateSlice returns only its exact caller-stable task and assignment", "repair the composed receipt before retrying")
	}
	return SliceResult{CommandResult: CommandResult{OperationID: in.Meta.OperationID, Replayed: result.Replayed(), Epoch: in.Epoch, ActivityID: activity, EventIDs: result.SupplementalEmittedEvents()}, Slice: slice}, nil
}

func commandRecordRequest(encoded []byte) []byte {
	var record assignmentCommandRecord
	if err := strictJSON(encoded, &record); err != nil {
		return nil
	}
	return record.Request
}

// indexComposedEpisode records the episode a composed allocation just started.
//
// It is the ONE call every composed assignment command makes into the index, so
// the choke point has one entry from this side. The authority id is read from
// the closure the command already holds; nothing is scanned and nothing is
// re-read from the journal.
func (s *epochAssignmentService) indexComposedEpisode(
	ctx context.Context,
	result provenance.GovernedAllocationComposedResult,
	task provenance.TaskID,
	assignment provenance.AssignmentID,
	role AssignmentRole,
	occupant provenance.ActorID,
) error {
	authority, err := composedAssignmentAuthority(result.Closure(), task, assignment)
	if err != nil {
		return err
	}
	return recordAssignmentStart(ctx, s.tracker.auditDB, startedEpisode{
		Assignment: assignment,
		Actor:      occupant,
		Task:       task,
		Role:       role,
		Authority:  authority,
	})
}

// declaredEpisode is one child of a composed allocation together with the slot
// its DEFINITION says the occupant holds.
//
// The slot travels WITH the child from the place that defines the allocation,
// because the journal's own child specification has no room for it and because
// the alternative — deciding it where the index is written — would put the
// policy in the last place a reader looks for it.
type declaredEpisode struct {
	Task       provenance.TaskID
	Assignment provenance.AssignmentID
	Occupant   provenance.ActorID
	Role       AssignmentRole
}

// indexComposedBatch records every episode a composed BATCH allocation started.
//
// A batch allocates many children in one commit, and each one is an episode. It
// reads each child's authority from the same closure and writes one record per
// child, so a command that creates several holders is not half recorded.
func (s *epochAssignmentService) indexComposedBatch(
	ctx context.Context,
	result provenance.GovernedAllocationComposedResult,
	declared []declaredEpisode,
) error {
	if len(declared) == 0 {
		return indexError("indexComposedBatch", "the committed batch declared no episode to record", "a batch allocation always creates at least one child, and each child is an episode, so an empty list means the caller lost them")
	}
	closure := result.Closure()
	for _, child := range declared {
		authority, err := composedAssignmentAuthority(closure, child.Task, child.Assignment)
		if err != nil {
			return err
		}
		if err := recordAssignmentStart(ctx, s.tracker.auditDB, startedEpisode{
			Assignment: child.Assignment,
			Actor:      child.Occupant,
			Task:       child.Task,
			Role:       child.Role,
			Authority:  authority,
		}); err != nil {
			return err
		}
	}
	return nil
}
