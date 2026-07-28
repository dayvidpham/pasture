package tasks

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/dbconn"
)

func TestEpochAssignmentServiceCreatesSliceThroughOneApply(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	bindTestGovernedAllocation(t, tracker)
	actor, err := tracker.RegisterHumanAgent("assignment-test", "Assignment Worker", "worker@example.test")
	if err != nil {
		t.Fatalf("register actor: %v", err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	plan := createHumanTestTask(t, tracker, "plan")
	_, authority, found, err := readSystemIdentity(tracker.auditDB)
	if err != nil || !found {
		t.Fatalf("read system identity: found=%t err=%v", found, err)
	}
	started, err := MapMaterialEvent(AssignmentStartedEvent{Task: plan, Assignment: "plan-supervisor", Role: RoleGoverningSupervisor, Occupant: actor.ID})
	if err != nil {
		t.Fatalf("map assignment event: %v", err)
	}
	started.ResultSlot = "event"
	if _, err := tracker.prov.Journal().Apply(provenance.OperationInput{
		OperationID:        "assignment-test-start",
		ActorID:            actor.ID,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte("assignment-test-start"),
		Effects: []provenance.Effect{{
			Sort:         provenance.EffectAssignmentStart,
			ResultSlot:   "authority",
			TaskID:       plan,
			AssignmentID: "plan-supervisor",
			SlotID:       provenance.SlotOwnerResponsibility,
			Occupant:     actor.ID,
		}, started},
	}); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	service, err := tracker.NewEpochService(EpochServiceOptions{})
	if err != nil {
		t.Fatalf("construct epoch service: %v", err)
	}
	result, err := service.CreateSlice(context.Background(), CreateSliceInput{
		Meta:       CommandMeta{OperationID: "assignment-test-slice"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "plan-supervisor",
	})
	if err != nil {
		t.Fatalf("create slice: %v", err)
	}
	if result.Slice == (provenance.TaskID{}) || result.ActivityID == (provenance.ActivityID{}) || len(result.EventIDs) == 0 {
		t.Fatalf("incomplete slice result: %+v", result)
	}
	if _, err := tracker.prov.Show(result.Slice); err != nil {
		t.Fatalf("created slice was not persisted: %v", err)
	}
	wantProducer := provenance.GovernedAllocationSupplementOperationID("assignment-test-slice")
	planBindings, err := service.(*epochService).EpochAssignmentService.(*epochAssignmentService).exactAssignmentBindings(plan, assignmentPlanEpochBindingKind)
	if err != nil || len(planBindings) != 1 || planBindings[0].Operation != wantProducer {
		t.Fatalf("plan-epoch bindings = %+v, err=%v; want exactly one produced by %q", planBindings, err, wantProducer)
	}
	sliceBindings, err := service.(*epochService).EpochAssignmentService.(*epochAssignmentService).exactAssignmentBindings(plan, assignmentPlanSliceBindingKind)
	if err != nil || len(sliceBindings) != 1 || sliceBindings[0].Slice != result.Slice || sliceBindings[0].Operation != wantProducer {
		t.Fatalf("plan-slice bindings = %+v, err=%v; want exactly one for %q produced by %q", sliceBindings, err, result.Slice, wantProducer)
	}
	edges, err := tracker.prov.Edges(plan, func() *provenance.EdgeKind { kind := provenance.EdgeBlockedBy; return &kind }())
	if err != nil || len(edges) != 1 || edges[0].TargetID != result.Slice.String() {
		t.Fatalf("plan-to-slice edges = %+v, err=%v; want exactly one edge to %q", edges, err, result.Slice)
	}
	retried, err := service.CreateSlice(context.Background(), CreateSliceInput{
		Meta: CommandMeta{OperationID: "assignment-test-slice"}, Epoch: EpochRootID(epoch.String()), Plan: plan, Assignment: "plan-supervisor",
	})
	if err != nil {
		t.Fatalf("retry create slice: %v", err)
	}
	if retried.Slice != result.Slice || retried.ActivityID != result.ActivityID || len(retried.EventIDs) != len(result.EventIDs) {
		t.Fatalf("retry returned a different closure: first=%+v retry=%+v", result, retried)
	}
	if result.Replayed || !retried.Replayed {
		t.Fatalf("replay flags = first %t, retry %t; want false, true", result.Replayed, retried.Replayed)
	}
	var auditRows int
	if err := tracker.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_governed_allocation_audit WHERE operation_id = ?`, "assignment-test-slice").Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("expected one participant audit row, count=%d err=%v", auditRows, err)
	}
}

func TestEpochAssignmentServiceFinalizesCleanReviewAuthority(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	bindTestGovernedAllocation(t, tracker)
	supervisor, err := tracker.RegisterHumanAgent("review-supervisor", "Review Supervisor", "supervisor@example.test")
	if err != nil {
		t.Fatalf("register supervisor: %v", err)
	}
	reviewer, err := tracker.RegisterHumanAgent("reviewer", "Axis Reviewer", "reviewer@example.test")
	if err != nil {
		t.Fatalf("register reviewer: %v", err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	plan := createHumanTestTask(t, tracker, "plan")
	seedAssignmentEpisode(t, tracker, plan, "review-supervisor", RoleGoverningSupervisor, supervisor.ID, "review-supervisor-seed")
	service, err := tracker.NewEpochService(EpochServiceOptions{})
	if err != nil {
		t.Fatalf("construct epoch service: %v", err)
	}
	slice, err := service.CreateSlice(context.Background(), CreateSliceInput{Meta: CommandMeta{OperationID: "review-slice"}, Epoch: EpochRootID(epoch.String()), Plan: plan, Assignment: "review-supervisor"})
	if err != nil {
		t.Fatalf("create review slice: %v", err)
	}
	endAssignmentEpisode(t, tracker, plan, "review-supervisor", supervisor.ID, "review-supervisor-end")
	seedAssignmentEpisode(t, tracker, slice.Slice, "review-owner", RoleOwnerResponsibility, reviewer.ID, "review-owner-seed")
	member, err := service.SetSliceCandidate(context.Background(), SetSliceCandidateInput{
		Meta: CommandMeta{OperationID: "review-candidate"}, Epoch: EpochRootID(epoch.String()), Slice: slice.Slice,
		Repository: "review-repo", Commit: "0123456789abcdef0123456789abcdef01234567", Assignment: "review-owner",
	})
	if err != nil {
		t.Fatalf("create review candidate: %v", err)
	}
	candidate, err := provenance.ParseTaskID(string(member.Candidate))
	if err != nil {
		t.Fatalf("parse review candidate: %v", err)
	}
	endAssignmentEpisode(t, tracker, candidate, "review-candidate-candidate-owner", reviewer.ID, "review-candidate-candidate-owner-end")
	seedAssignmentEpisode(t, tracker, candidate, "review-supervisor-candidate", RoleGoverningSupervisor, supervisor.ID, "review-supervisor-candidate-seed")
	started, err := service.StartReview(context.Background(), StartReviewInput{Meta: CommandMeta{OperationID: "review-start"}, Epoch: EpochRootID(epoch.String()), Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: candidate.String()}})
	if err != nil {
		t.Fatalf("start review: %v", err)
	}
	implementationPlan, err := PlanReviewRound(candidate, started.Subject, SubjectImplementation)
	if err != nil {
		t.Fatalf("plan implementation review: %v", err)
	}
	if len(implementationPlan.Tasks) != 13 {
		t.Fatalf("implementation review generated %d tasks, want 13", len(implementationPlan.Tasks))
	}
	for i, task := range implementationPlan.Tasks {
		id := deterministicTask("review-start", task.Handle)
		if _, err := tracker.Show(id); err != nil {
			t.Fatalf("ordered child %d (%s) was not committed: %v", i, task.Handle, err)
		}
	}
	replayed, err := service.StartReview(context.Background(), StartReviewInput{Meta: CommandMeta{OperationID: "review-start"}, Epoch: EpochRootID(epoch.String()), Subject: started.Subject})
	if err != nil || !replayed.Replayed || replayed.Round != started.Round {
		t.Fatalf("exact StartReview replay = %+v, err=%v; want same replayed result", replayed, err)
	}
	var reviewAuditRows int
	if err := tracker.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_governed_allocation_audit WHERE operation_id = ?`, "review-start").Scan(&reviewAuditRows); err != nil || reviewAuditRows != 1 {
		t.Fatalf("review replay audit rows=%d err=%v, want exactly one", reviewAuditRows, err)
	}
	for i, axis := range canonicalReviewAxes() {
		assignment := provenance.AssignmentID(fmt.Sprintf("axis-%d", i))
		seedAssignmentEpisode(t, tracker, assignmentTaskForStartedReview(started.Round, axis), assignment, RoleAxisReviewer, reviewer.ID, provenance.OperationID(fmt.Sprintf("axis-seed-%d", i)))
		_, err := service.SubmitReview(context.Background(), SubmitReviewInput{
			Meta:       CommandMeta{OperationID: provenance.OperationID(fmt.Sprintf("review-submit-%d", i))},
			Epoch:      EpochRootID(epoch.String()),
			Round:      started.Round,
			Axis:       axis,
			Assignment: assignment,
			Submission: ImplementationReviewSubmission{Verdict: VerdictAccept},
		})
		if err != nil {
			t.Fatalf("submit %s review: %v", axis, err)
		}
	}
	finalized, err := service.FinalizeReview(context.Background(), FinalizeReviewInput{Meta: CommandMeta{OperationID: "review-finalize"}, Epoch: EpochRootID(epoch.String()), Round: started.Round, Assignment: "review-supervisor-candidate"})
	if err != nil {
		t.Fatalf("finalize review: %v", err)
	}
	if finalized.ReviewEvents == [3]provenance.JournalID{} || len(finalized.EventIDs) == 0 {
		t.Fatalf("incomplete finalization result: %+v", finalized)
	}
	current, err := service.(*epochService).EpochAssignmentService.(*epochAssignmentService).currentReviewAuthority(startedSubjectTask(started.Subject))
	if err != nil || current.value.State != reviewFinalizedClean {
		t.Fatalf("current review authority = %+v, err=%v; want clean", current, err)
	}
}

func TestEpochAssignmentServiceStartsInitialPlanReviewAndBindsEpoch(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	bindTestGovernedAllocation(t, tracker)
	supervisor, err := tracker.RegisterHumanAgent("initial-plan-review", "Initial Plan Reviewer", "initial-plan@example.test")
	if err != nil {
		t.Fatalf("register supervisor: %v", err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	plan := createHumanTestTask(t, tracker, "unbound-plan")
	seedAssignmentEpisode(t, tracker, plan, "initial-plan-supervisor", RoleGoverningSupervisor, supervisor.ID, "initial-plan-supervisor-seed")
	service, err := tracker.NewEpochService(EpochServiceOptions{})
	if err != nil {
		t.Fatalf("construct epoch service: %v", err)
	}
	in := StartReviewInput{Meta: CommandMeta{OperationID: "initial-plan-review-start"}, Epoch: EpochRootID(epoch.String()), Subject: ReviewSubjectRef{Kind: ReviewSubjectDocumentRevision, SnapshotID: plan.String()}}
	started, err := service.StartReview(context.Background(), in)
	if err != nil {
		t.Fatalf("start initial plan review: %v", err)
	}
	shape, err := PlanReviewRound(plan, in.Subject, SubjectPlan)
	if err != nil {
		t.Fatalf("plan review shape: %v", err)
	}
	if len(shape.Tasks) != 4 {
		t.Fatalf("plan review generated %d tasks, want 4", len(shape.Tasks))
	}
	for i, child := range shape.Tasks {
		if _, err := tracker.Show(deterministicTask(in.Meta.OperationID, child.Handle)); err != nil {
			t.Fatalf("ordered plan review child %d (%s) was not committed: %v", i, child.Handle, err)
		}
	}
	bindings, err := service.(*epochService).EpochAssignmentService.(*epochAssignmentService).exactAssignmentBindings(plan, assignmentPlanEpochBindingKind)
	wantProducer := provenance.GovernedAllocationSupplementOperationID(in.Meta.OperationID)
	if err != nil || len(bindings) != 1 || bindings[0].Epoch != in.Epoch || bindings[0].Operation != wantProducer {
		t.Fatalf("initial plan binding = %+v, err=%v; want one binding produced by %q", bindings, err, wantProducer)
	}
	replayed, err := service.StartReview(context.Background(), in)
	if err != nil || !replayed.Replayed || replayed.Round != started.Round {
		t.Fatalf("initial plan review replay = %+v, err=%v; want exact replay", replayed, err)
	}
}

func bindTestGovernedAllocation(t *testing.T, tracker *trackerImpl) {
	t.Helper()
	var seq int
	var name, path string
	if err := tracker.auditDB.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatalf("resolve test database path: %v", err)
	}
	bound, err := provenance.OpenBoundGovernedAllocator(context.Background(), provenance.FusedGovernedAllocatorConfig{SQLiteDSN: dbconn.SharedDSN(path), AppName: "pasture-task-test", ApplicationVersion: "test-v1", Participant: CreateSliceAuditParticipant})
	if err != nil {
		t.Fatalf("bind test governed allocator: %v", err)
	}
	if err := BindEngineGovernedAllocation(tracker, bound); err != nil {
		t.Fatalf("install test governed allocator: %v", err)
	}
	if err := bound.Launch(); err != nil {
		t.Fatalf("launch test DBOS root: %v", err)
	}
	t.Cleanup(func() { _ = bound.Close(5 * time.Second) })
}

func startedSubjectTask(subject ReviewSubjectRef) provenance.TaskID {
	id, _ := provenance.ParseTaskID(subject.SnapshotID)
	return id
}

func assignmentTaskForStartedReview(round ReviewRoundID, axis ReviewAxis) provenance.TaskID {
	roundTask, _ := provenance.ParseTaskID(string(round))
	operation := provenance.OperationID("review-start")
	_ = roundTask
	return deterministicTask(operation, "axis-"+axis.String())
}

func seedAssignmentEpisode(t *testing.T, tracker *trackerImpl, task provenance.TaskID, assignment provenance.AssignmentID, role AssignmentRole, occupant provenance.ActorID, operation provenance.OperationID) {
	t.Helper()
	_, authority, found, err := readSystemIdentity(tracker.auditDB)
	if err != nil || !found {
		t.Fatalf("read system identity for assignment: found=%t err=%v", found, err)
	}
	event, err := MapMaterialEvent(AssignmentStartedEvent{Task: task, Assignment: assignment, Role: role, Occupant: occupant})
	if err != nil {
		t.Fatalf("map assignment start: %v", err)
	}
	event.ResultSlot = "event"
	if _, err := tracker.prov.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            occupant,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte(operation),
		Effects:            []provenance.Effect{{Sort: provenance.EffectAssignmentStart, ResultSlot: "authority", TaskID: task, AssignmentID: assignment, SlotID: provenance.SlotOwnerResponsibility, Occupant: occupant}, event},
	}); err != nil {
		t.Fatalf("seed assignment %q: %v", assignment, err)
	}
}

func endAssignmentEpisode(t *testing.T, tracker *trackerImpl, task provenance.TaskID, assignment provenance.AssignmentID, actor provenance.ActorID, operation provenance.OperationID) {
	t.Helper()
	_, authority, found, err := readSystemIdentity(tracker.auditDB)
	if err != nil || !found {
		t.Fatalf("read system identity for assignment end: found=%t err=%v", found, err)
	}
	if _, err := tracker.prov.Journal().Apply(provenance.OperationInput{
		OperationID:        operation,
		ActorID:            actor,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte(operation),
		Effects: []provenance.Effect{{
			Sort:         provenance.EffectAssignmentEnd,
			ResultSlot:   "assignment-end",
			TaskID:       task,
			AssignmentID: assignment,
			SlotID:       provenance.SlotOwnerResponsibility,
		}},
	}); err != nil {
		t.Fatalf("end assignment %q: %v", assignment, err)
	}
}
