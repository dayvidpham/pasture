package engine_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// This fixture covers the documented activity-only construction boundary. The
// governed review path is exercised with the unified tracker in the task
// aggregate tests; a direct Provenance tracker must remain a valid sink without
// being treated as an allocator host.
func TestEngineNewLaunchAcceptsDirectActivitySink(t *testing.T) {
	t.Parallel()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker, err := provenance.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("open direct Provenance activity sink: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	executorID, appVersion := testEngineIdentity(t)
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		QueueBasePollingInterval: 100 * time.Millisecond,
		Tracker:                  tracker,
	})
	if err != nil {
		t.Fatalf("engine.New with direct activity sink: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch with direct activity sink: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(5 * time.Second) })

	final := runEpoch(t, e, "activity-only-engine", fullEpochPlan())
	if final.CurrentPhase != protocol.PhaseComplete {
		t.Fatalf("final phase = %q, want complete", final.CurrentPhase)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open activity verification database: %v", err)
	}
	defer db.Close()
	if got := countRows(t, db, `SELECT COUNT(*) FROM activities`); got != 12 {
		t.Fatalf("transition activities = %d, want 12", got)
	}
}

// TestEngineStartReviewUsesAttachedProvenanceAdapter exercises the production
// StartReview path from the unified tracker returned by OpenTaskTracker through
// engine.New/Launch. No allocator or DBOS context is constructed by the test:
// engine.New is the only production composition point that can install the
// host-bound Provenance capability.
func TestEngineStartReviewUsesAttachedProvenanceAdapter(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("open unified task tracker for StartReview E2E at %q: %v; impact: the production engine cannot share its file-backed task store; fix: provide a writable isolated database path and use tasks.OpenTaskTracker", dbPath, err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Errorf("close unified task tracker for StartReview E2E at %q: %v", dbPath, err)
		}
	})

	supervisor, err := tracker.RegisterHumanAgent("engine-review", "Engine Review Supervisor", "engine-review@example.test")
	if err != nil {
		t.Fatalf("register direct governing supervisor before StartReview: %v; impact: the production authority fixture cannot prove assignment-bound review; fix: register one human supervisor before seeding the assignment", err)
	}
	epoch, err := tracker.Create("engine-review", "epoch", "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	if err != nil {
		t.Fatalf("create epoch task before StartReview: %v; impact: review operations have no owning epoch; fix: use the unified task tracker to create a valid epoch fixture", err)
	}
	plan, err := tracker.Create("engine-review", "plan", "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	if err != nil {
		t.Fatalf("create unbound plan task before initial StartReview: %v; impact: the pre-slice plan path cannot prove same-operation epoch binding; fix: create the plan through the unified tracker before review", err)
	}
	const supervisorAssignment = provenance.AssignmentID("engine-review-supervisor")
	assignmentAuthority := seedEngineAssignment(t, tracker, plan.ID, supervisorAssignment, tasks.RoleGoverningSupervisor, supervisor.ID, "engine-review-supervisor-seed")
	governsPlan, err := tracker.Journal().AuthorityGovernsTaskAt(assignmentAuthority, plan.ID, provenance.JournalID(^uint64(0)>>1))
	if err != nil || !governsPlan {
		t.Fatalf("validate direct governing-supervisor authority %d for plan %q: governs=%t err=%v; impact: StartReview would not be exercising valid direct assignment authority; fix: repair the assignment-start journal fixture", assignmentAuthority, plan.ID, governsPlan, err)
	}

	executorID, appVersion := testEngineIdentity(t)
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		QueueBasePollingInterval: 100 * time.Millisecond,
		Trail:                    tracker,
		Tracker:                  tracker,
	})
	if err != nil {
		t.Fatalf("engine.New with the unified tracker: %v; impact: the engine-created Provenance adapter is unavailable to StartReview; fix: construct the engine with the same tracker and file-backed path", err)
	}
	t.Cleanup(func() { e.Shutdown(5 * time.Second) })
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch for the attached Provenance adapter: %v; impact: no production StartReview workflow can execute; fix: launch the one engine-owned DBOS context before constructing the service", err)
	}

	factory, ok := tracker.(tasks.EpochServiceFactory)
	if !ok {
		t.Fatalf("unified tracker %T does not implement the production EpochServiceFactory: impact: this test would bypass the real service path; fix: obtain the tracker from tasks.OpenTaskTracker", tracker)
	}
	service, err := factory.NewEpochService(tasks.EpochServiceOptions{})
	if err != nil {
		t.Fatalf("construct production EpochService after engine launch: %v; impact: StartReview cannot reach the attached adapter; fix: compose the service from the engine-bound unified tracker", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	planOperation := provenance.OperationID("engine-review-plan-start")
	planInput := tasks.StartReviewInput{
		Meta:  tasks.CommandMeta{OperationID: planOperation},
		Epoch: tasks.EpochRootID(epoch.ID.String()),
		Subject: tasks.ReviewSubjectRef{
			Kind:       tasks.ReviewSubjectDocumentRevision,
			SnapshotID: plan.ID.String(),
		},
	}
	planBefore := captureStartReviewFootprint(t, e.DB(), planOperation, tasks.ReviewStartResult{}, reviewClosure{})
	assertStartReviewFootprintEmpty(t, planOperation, planBefore)
	startedPlan, err := service.StartReview(ctx, planInput)
	if err != nil {
		t.Fatalf("StartReview plan operation %q through engine-owned adapter: %v; impact: a valid direct governing-supervisor plan cannot enter the review lifecycle; fix: preserve the fused plan binding plus four-child batch on the Engine.Launch path", planOperation, err)
	}
	if startedPlan.Replayed {
		t.Fatalf("first plan StartReview operation %q returned Replayed=true; impact: the production path treated a fresh request as an existing receipt; fix: isolate the operation identity and inspect the attached Provenance store", planOperation)
	}
	assertOperationAuthority(t, e.DB(), planOperation, assignmentAuthority)
	planClosure := assertReviewClosure(t, tracker, e.DB(), planOperation, plan.ID, planInput.Subject, tasks.SubjectPlan, 4, 0)
	planAfter := captureStartReviewFootprint(t, e.DB(), planOperation, startedPlan, planClosure)
	if planAfter.childTaskRows != 4 || planAfter.childAssignmentRows != 4 || planAfter.bindingRows != 1 {
		t.Fatalf("fresh plan StartReview operation %q footprint = %+v; impact: the atomic plan-to-epoch binding or ordered four-child closure is incomplete; fix: keep all plan StartReview effects in the one composed transaction", planOperation, planAfter)
	}
	assertReviewOperationDurability(t, planOperation, planAfter)
	assertStartReviewReplay(t, e.DB(), service, ctx, planInput, startedPlan, planClosure, planAfter)

	// Build the normal implementation lineage through production service methods.
	// The assignment episode below is only the authority fixture required by
	// SetSliceCandidate; slice and candidate tasks are both production outputs.
	sliceOperation := provenance.OperationID("engine-review-slice-create")
	slice, err := service.CreateSlice(ctx, tasks.CreateSliceInput{
		Meta:       tasks.CommandMeta{OperationID: sliceOperation},
		Epoch:      tasks.EpochRootID(epoch.ID.String()),
		Plan:       plan.ID,
		Assignment: supervisorAssignment,
	})
	if err != nil {
		t.Fatalf("CreateSlice operation %q for implementation lineage: %v; impact: the candidate cannot be a normal production descendant of the reviewed plan; fix: preserve the plan binding and active governing assignment", sliceOperation, err)
	}
	if _, err := tracker.Show(slice.Slice); err != nil {
		t.Fatalf("show production-created slice %q after operation %q: %v; impact: implementation review would not use a real candidate lineage; fix: retain the composed slice child task", slice.Slice, sliceOperation, err)
	}
	// CreateSlice's generated child assignment is retained as part of its
	// production closure. SetSliceCandidate additionally requires an active
	// parent-owner episode whose assignment-start authority is paired directly
	// with its material event; seed that normal command authority through the
	// same journal fixture used for the governing supervisor.
	ownerAssignment := provenance.AssignmentID("engine-review-slice-owner")
	seedEngineAssignment(t, tracker, slice.Slice, ownerAssignment, tasks.RoleOwnerResponsibility, supervisor.ID, "engine-review-slice-owner-seed")
	commit := provenance.GitOID("0123456789abcdef0123456789abcdef01234567")
	candidateOperation := provenance.OperationID("engine-review-candidate-create")
	candidate, err := service.SetSliceCandidate(ctx, tasks.SetSliceCandidateInput{
		Meta:       tasks.CommandMeta{OperationID: candidateOperation},
		Epoch:      tasks.EpochRootID(epoch.ID.String()),
		Slice:      slice.Slice,
		Repository: tasks.RepositoryID("engine-review-repository"),
		Commit:     commit,
		Assignment: ownerAssignment,
	})
	if err != nil {
		t.Fatalf("SetSliceCandidate operation %q for implementation lineage: %v; impact: the implementation review subject is not a valid production candidate; fix: preserve the active slice-owner assignment and immutable repository commit", candidateOperation, err)
	}
	candidateTask, err := provenance.ParseTaskID(string(candidate.Candidate))
	if err != nil {
		t.Fatalf("parse production implementation candidate %q: %v; impact: StartReview cannot bind the candidate subject; fix: preserve the candidate task identity returned by SetSliceCandidate", candidate.Candidate, err)
	}
	if _, err := tracker.Show(candidateTask); err != nil {
		t.Fatalf("show production-created implementation candidate %q: %v; impact: candidate review would not use the real task tracker output; fix: retain the candidate task created by SetSliceCandidate", candidateTask, err)
	}
	// Give the ancestor supervisor a deliberate governance path to this
	// descendant. The production candidate-owner assignment is intentionally
	// retained; this additional typed fixture represents the existing
	// parent-citation contract that lets StartReview resolve the plan authority
	// while reviewing the candidate subject.
	seedEngineAssignmentWithParent(t, tracker, candidateTask, "engine-review-candidate-owner", tasks.RoleOwnerResponsibility, supervisor.ID, "engine-review-candidate-owner-seed", supervisorAssignment)

	implementationOperation := provenance.OperationID("engine-review-implementation-start")
	implementationInput := tasks.StartReviewInput{
		Meta:  tasks.CommandMeta{OperationID: implementationOperation},
		Epoch: tasks.EpochRootID(epoch.ID.String()),
		Subject: tasks.ReviewSubjectRef{
			Kind:       tasks.ReviewSubjectImplementationCandidate,
			SnapshotID: string(candidate.Candidate),
		},
	}
	implementationBefore := captureStartReviewFootprint(t, e.DB(), implementationOperation, tasks.ReviewStartResult{}, reviewClosure{})
	assertStartReviewFootprintEmpty(t, implementationOperation, implementationBefore)
	startedImplementation, err := service.StartReview(ctx, implementationInput)
	if err != nil {
		t.Fatalf("StartReview implementation operation %q through engine-owned adapter with ancestor authority: %v; impact: a valid candidate descendant cannot start its thirteen-child review; fix: preserve the typed ancestor reference scope and direct governing assignment", implementationOperation, err)
	}
	if startedImplementation.Replayed {
		t.Fatalf("first implementation StartReview operation %q returned Replayed=true; impact: a fresh candidate review reused an existing receipt; fix: isolate the operation identity and inspect the attached Provenance store", implementationOperation)
	}
	assertOperationAuthority(t, e.DB(), implementationOperation, assignmentAuthority)
	implementationClosure := assertReviewClosure(t, tracker, e.DB(), implementationOperation, candidateTask, implementationInput.Subject, tasks.SubjectImplementation, 13, 9)
	implementationAfter := captureStartReviewFootprint(t, e.DB(), implementationOperation, startedImplementation, implementationClosure)
	if implementationAfter.childTaskRows != 13 || implementationAfter.childAssignmentRows != 13 || implementationAfter.bindingRows != 0 {
		t.Fatalf("fresh implementation StartReview operation %q footprint = %+v; impact: the thirteen-child review closure or authority composition is incomplete; fix: retain all three axes and nine eager severity groups in the governed batch", implementationOperation, implementationAfter)
	}
	assertReviewOperationDurability(t, implementationOperation, implementationAfter)
	assertStartReviewReplay(t, e.DB(), service, ctx, implementationInput, startedImplementation, implementationClosure, implementationAfter)
}

type reviewClosure struct {
	anchor      int64
	childIDs    []string
	assignments []string
}

type startReviewFootprint struct {
	childIDs                   []string
	childAssignments           []string
	childTaskRows              int
	childAssignmentRows        int
	bindingRows                int
	participantRows            int
	activityRows               int
	eventRows                  int
	auditEventRows             int
	operationRows              int
	operationSlotRows          int
	closureRows                int
	supplementOwnerRows        int
	workflowStatusRows         int
	operationOutputRows        int
	successfulOperationOutputs int
}

func seedEngineAssignment(t *testing.T, tracker protocol.TaskTracker, task provenance.TaskID, assignment provenance.AssignmentID, role tasks.AssignmentRole, actor provenance.ActorID, operation provenance.OperationID) provenance.JournalID {
	return seedEngineAssignmentWithParent(t, tracker, task, assignment, role, actor, operation, "")
}

func seedEngineAssignmentWithParent(t *testing.T, tracker protocol.TaskTracker, task provenance.TaskID, assignment provenance.AssignmentID, role tasks.AssignmentRole, actor provenance.ActorID, operation provenance.OperationID, parent provenance.AssignmentID) provenance.JournalID {
	t.Helper()
	genesis, err := tracker.Journal().LookupCommitted(provenance.OperationID("pasture.system.genesis.v1"))
	if err != nil || genesis.Kind != provenance.CommittedExact {
		t.Fatalf("lookup Pasture system genesis before assignment operation %q: kind=%s err=%v; impact: the direct assignment fixture has no valid parent authority; fix: initialize the unified task tracker through its production opener", operation, genesis.Kind, err)
	}
	genesisAuthority := resultSlotJournalID(t, genesis, provenance.ResultSlotID("auth"))
	started, err := tasks.MapMaterialEvent(tasks.AssignmentStartedEvent{Task: task, Assignment: assignment, Role: role, Occupant: actor})
	if err != nil {
		t.Fatalf("map direct assignment operation %q: %v; impact: the assignment authority event cannot be seeded; fix: use the closed AssignmentStartedEvent contract", operation, err)
	}
	started.ResultSlot = "event"
	result, err := tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            actor,
		AuthorityJournalID: &genesisAuthority,
		CommandDigest:      []byte(operation),
		Effects: []provenance.Effect{
			{Sort: provenance.EffectAssignmentStart, ResultSlot: "authority", TaskID: task, AssignmentID: assignment, SlotID: provenance.SlotOwnerResponsibility, Occupant: actor, Parent: parent},
			started,
		},
	})
	if err != nil {
		t.Fatalf("seed direct assignment operation %q: %v; impact: StartReview has no valid assignment authority; fix: commit the assignment start and typed material event through Journal.Apply", operation, err)
	}
	return resultSlotJournalID(t, result, provenance.ResultSlotID("authority"))
}

func resultSlotJournalID(t *testing.T, result provenance.CommittedResult, slot provenance.ResultSlotID) provenance.JournalID {
	t.Helper()
	for _, candidate := range result.ResultSlots {
		if candidate.Slot == slot {
			return candidate.ProducedJournalID
		}
	}
	t.Fatalf("committed operation result has no %q result slot: %+v; impact: the operation closure cannot be authenticated; fix: preserve the typed result-slot effect", slot, result)
	return 0
}

func assertReviewClosure(t *testing.T, tracker protocol.TaskTracker, db *sql.DB, operation provenance.OperationID, reviewedTask provenance.TaskID, subject tasks.ReviewSubjectRef, kind tasks.SubjectKind, wantChildren, wantGroups int) reviewClosure {
	t.Helper()
	closure := readReviewClosure(t, db, operation)
	if len(closure.childIDs) != wantChildren || len(closure.assignments) != wantChildren {
		t.Fatalf("StartReview operation %q closure has %d child tasks and %d assignments, want %d each; impact: the fused operation did not commit the complete ordered review shape; fix: retain every ReviewRoundPlan task in the composed request", operation, len(closure.childIDs), len(closure.assignments), wantChildren)
	}
	if len(uniqueStrings(closure.childIDs)) != len(closure.childIDs) || len(uniqueStrings(closure.assignments)) != len(closure.assignments) {
		t.Fatalf("StartReview operation %q closure contains duplicate child task or assignment identities: tasks=%v assignments=%v; impact: replay could conceal duplicate review work; fix: preserve deterministic unique child bindings", operation, closure.childIDs, closure.assignments)
	}
	plan, err := tasks.PlanReviewRound(reviewedTask, subject, kind)
	if err != nil {
		t.Fatalf("derive expected review shape for StartReview operation %q: %v; impact: the persisted closure cannot be compared with the accepted static plan; fix: use the valid review subject and kind", operation, err)
	}
	groups := 0
	if len(plan.Tasks) != wantChildren {
		t.Fatalf("expected ReviewRoundPlan for StartReview operation %q has %d tasks, want %d; impact: the test oracle no longer matches the accepted static review shape; fix: update the typed plan contract before changing this E2E", operation, len(plan.Tasks), wantChildren)
	}
	for i, childIDText := range closure.childIDs {
		childID, err := provenance.ParseTaskID(childIDText)
		if err != nil {
			t.Fatalf("parse ordered child %d %q for StartReview operation %q: %v; impact: persisted child identity is not a valid task; fix: preserve canonical TaskID values in the batch closure", i, childIDText, operation, err)
		}
		child, err := tracker.Show(childID)
		if err != nil {
			t.Fatalf("show ordered child %d %q for StartReview operation %q: %v; impact: the participant receipt names a task that the unified tracker cannot read; fix: keep task creation and participant receipt in one fused transaction", i, childID, operation, err)
		}
		wantTitle := reviewTaskTitle(plan.Tasks[i])
		if child.Title != wantTitle || child.Description != wantTitle {
			t.Fatalf("ordered child %d for StartReview operation %q has title=%q description=%q, want %q; impact: the persisted order/shape differs from the production review plan; fix: lower ReviewRoundPlan tasks without filtering or reordering", i, operation, child.Title, child.Description, wantTitle)
		}
		if plan.Tasks[i].Kind == tasks.ReviewTaskGroup {
			groups++
		}
		if countRows(t, db, `SELECT COUNT(*) FROM journal_authority_assignment_episodes WHERE assignment_id = ? AND task_id = ?`, closure.assignments[i], childID.String()) != 1 {
			t.Fatalf("ordered child %d for StartReview operation %q has assignment %q not bound exactly once to task %q; impact: child authority is not atomically attached to the intended review task; fix: preserve the governed child assignment closure", i, operation, closure.assignments[i], childID)
		}
	}
	if groups != wantGroups {
		t.Fatalf("StartReview operation %q materialized %d eager severity groups, want %d; impact: implementation review closure is missing canonical blocker/important/minor groups; fix: preserve all nine groups in the composed batch", operation, groups, wantGroups)
	}
	return closure
}

func readReviewClosure(t *testing.T, db *sql.DB, operation provenance.OperationID) reviewClosure {
	t.Helper()
	var closure reviewClosure
	var childrenJSON, assignmentsJSON string
	if err := db.QueryRow(`SELECT closure_anchor, child_task_id, child_assignment_id FROM pasture_governed_allocation_audit WHERE operation_id = ?`, operation).Scan(&closure.anchor, &childrenJSON, &assignmentsJSON); err != nil {
		t.Fatalf("read governed allocation closure for StartReview operation %q: %v; impact: the participant receipt cannot prove ordered child allocation; fix: preserve the engine-bound audit participant row", operation, err)
	}
	if err := json.Unmarshal([]byte(childrenJSON), &closure.childIDs); err != nil {
		t.Fatalf("decode ordered child-task closure for StartReview operation %q: %v; impact: child identity/order cannot be verified; fix: restore the canonical participant receipt", operation, err)
	}
	if err := json.Unmarshal([]byte(assignmentsJSON), &closure.assignments); err != nil {
		t.Fatalf("decode ordered child-assignment closure for StartReview operation %q: %v; impact: child authority/order cannot be verified; fix: restore the canonical participant receipt", operation, err)
	}
	return closure
}

func reviewTaskTitle(task tasks.PlannedTask) string {
	switch task.Kind {
	case tasks.ReviewTaskRound:
		return "review round"
	case tasks.ReviewTaskAxis:
		return "review axis " + task.Axis.String()
	case tasks.ReviewTaskGroup:
		return "review " + task.Axis.String() + " " + task.Severity.String() + " findings"
	default:
		return fmt.Sprintf("unknown review task kind %d", task.Kind)
	}
}

func captureStartReviewFootprint(t *testing.T, db *sql.DB, operation provenance.OperationID, result tasks.ReviewStartResult, closure reviewClosure) startReviewFootprint {
	t.Helper()
	supplement := provenance.GovernedAllocationSupplementOperationID(operation)
	workflowID := "pasture-start-review:" + string(operation)
	footprint := startReviewFootprint{
		childIDs:                   append([]string(nil), closure.childIDs...),
		childAssignments:           append([]string(nil), closure.assignments...),
		participantRows:            countRows(t, db, `SELECT COUNT(*) FROM pasture_governed_allocation_audit WHERE operation_id = ?`, operation),
		bindingRows:                countRows(t, db, `SELECT COUNT(*) FROM journal_evidence e JOIN journal j ON j.journal_id = e.journal_id JOIN journal_operations o ON o.journal_id = j.produced_by_operation_journal_id WHERE o.operation_id = ? AND e.evidence_kind = ? AND json_extract(e.payload, '$.epoch') IS NOT NULL`, supplement, provenance.EvidenceKind("pasture.assignment.binding.v1")),
		operationRows:              countRows(t, db, `SELECT COUNT(*) FROM journal_operations WHERE operation_id = ? OR operation_id = ?`, operation, supplement),
		operationSlotRows:          countRows(t, db, `SELECT COUNT(*) FROM journal_operation_result_slots WHERE journal_id IN (SELECT journal_id FROM journal_operations WHERE operation_id = ? OR operation_id = ?)`, operation, supplement),
		closureRows:                countRows(t, db, `SELECT COUNT(*) FROM journal WHERE produced_by_operation_journal_id IN (SELECT journal_id FROM journal_operations WHERE operation_id = ? OR operation_id = ?)`, operation, supplement),
		supplementOwnerRows:        countRows(t, db, `SELECT COUNT(*) FROM governed_composed_supplement_owners WHERE supplement_operation_id = ?`, supplement),
		workflowStatusRows:         countRows(t, db, `SELECT COUNT(*) FROM workflow_status WHERE workflow_uuid = ?`, workflowID),
		operationOutputRows:        countRows(t, db, `SELECT COUNT(*) FROM operation_outputs WHERE workflow_uuid = ?`, workflowID),
		successfulOperationOutputs: countRows(t, db, `SELECT COUNT(*) FROM operation_outputs WHERE workflow_uuid = ? AND error IS NULL`, workflowID),
		auditEventRows:             countRows(t, db, `SELECT COUNT(*) FROM audit_events`),
	}
	for _, childID := range closure.childIDs {
		footprint.childTaskRows += countRows(t, db, `SELECT COUNT(*) FROM tasks WHERE id = ?`, childID)
	}
	for i, assignmentID := range closure.assignments {
		if i < len(closure.childIDs) {
			footprint.childAssignmentRows += countRows(t, db, `SELECT COUNT(*) FROM journal_authority_assignment_episodes WHERE assignment_id = ? AND task_id = ?`, assignmentID, closure.childIDs[i])
		}
	}
	if result.ActivityID != (provenance.ActivityID{}) {
		footprint.activityRows = countRows(t, db, `SELECT COUNT(*) FROM activities WHERE id = ?`, result.ActivityID.String())
	}
	for _, eventID := range result.EventIDs {
		footprint.eventRows += countRows(t, db, `SELECT COUNT(*) FROM journal_task_events WHERE journal_id = ?`, eventID)
	}
	return footprint
}

func assertStartReviewFootprintEmpty(t *testing.T, operation provenance.OperationID, footprint startReviewFootprint) {
	t.Helper()
	if footprint.childTaskRows != 0 || footprint.childAssignmentRows != 0 || footprint.bindingRows != 0 || footprint.participantRows != 0 || footprint.activityRows != 0 || footprint.eventRows != 0 || footprint.operationRows != 0 || footprint.operationSlotRows != 0 || footprint.closureRows != 0 || footprint.supplementOwnerRows != 0 || footprint.workflowStatusRows != 0 || footprint.operationOutputRows != 0 {
		t.Fatalf("fresh StartReview operation %q was not empty before execution: %+v; impact: the replay oracle is contaminated by a prior receipt; fix: use a fresh isolated operation identity and file-backed store", operation, footprint)
	}
}

func assertReviewOperationDurability(t *testing.T, operation provenance.OperationID, footprint startReviewFootprint) {
	t.Helper()
	if footprint.participantRows != 1 || footprint.activityRows != 1 || footprint.operationRows != 2 || footprint.operationSlotRows == 0 || footprint.closureRows == 0 || footprint.supplementOwnerRows != 1 || footprint.workflowStatusRows != 1 || footprint.operationOutputRows == 0 || footprint.successfulOperationOutputs == 0 {
		t.Fatalf("fresh StartReview operation %q durable footprint = %+v; impact: the attached Provenance receipt, participant, activity, or DBOS checkpoint is incomplete; fix: keep the engine-owned composed transaction and its closure receipt intact", operation, footprint)
	}
}

func assertOperationAuthority(t *testing.T, db *sql.DB, operation provenance.OperationID, want provenance.JournalID) {
	t.Helper()
	var got sql.NullInt64
	if err := db.QueryRow(`SELECT authority_journal_id FROM journal_operations WHERE operation_id = ?`, operation).Scan(&got); err != nil {
		t.Fatalf("read authority for StartReview operation %q: %v; impact: the durable operation cannot prove which assignment governed the production batch; fix: preserve the journal operation authority row", operation, err)
	}
	if !got.Valid || got.Int64 != int64(want) {
		t.Fatalf("StartReview operation %q authority = %v, want ancestor assignment authority %d; impact: implementation review may have bypassed the intended ancestor-scoped governing assignment; fix: resolve the typed parent-citation authority before allocation", operation, got, want)
	}
}

func assertStartReviewReplay(t *testing.T, db *sql.DB, service tasks.EpochService, ctx context.Context, input tasks.StartReviewInput, first tasks.ReviewStartResult, closure reviewClosure, beforeReplay startReviewFootprint) {
	t.Helper()
	replayed, err := service.StartReview(ctx, input)
	if err != nil {
		t.Fatalf("exact StartReview replay operation %q: %v; impact: retry did not return the durable production receipt; fix: preserve operation identity lookup before mutable authority preflight", input.Meta.OperationID, err)
	}
	if !replayed.Replayed || replayed.OperationID != first.OperationID || replayed.Round != first.Round || replayed.Subject != first.Subject || replayed.ActivityID != first.ActivityID || !slicesEqualJournalIDs(replayed.EventIDs, first.EventIDs) {
		t.Fatalf("exact StartReview replay operation %q returned different identities: first=%+v replay=%+v; impact: callers cannot safely retry the production command; fix: reconstruct the original governed supplemental receipt", input.Meta.OperationID, first, replayed)
	}
	afterClosure := readReviewClosure(t, db, input.Meta.OperationID)
	if !reflect.DeepEqual(closure, afterClosure) {
		t.Fatalf("exact StartReview replay operation %q changed the participant closure: first=%+v replay=%+v; impact: retry produced different child or assignment identities; fix: reconstruct the original ordered governed closure", input.Meta.OperationID, closure, afterClosure)
	}
	afterReplay := captureStartReviewFootprint(t, db, input.Meta.OperationID, replayed, afterClosure)
	if !reflect.DeepEqual(beforeReplay, afterReplay) {
		t.Fatalf("exact StartReview replay operation %q changed the persisted footprint: first=%+v replay=%+v; impact: retry duplicated or altered tasks, bindings, assignments, participant, activity, event, operation, or DBOS closure rows; fix: return the original receipt before mutable preflight", input.Meta.OperationID, beforeReplay, afterReplay)
	}
}

func slicesEqualJournalIDs(left, right []provenance.JournalID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
