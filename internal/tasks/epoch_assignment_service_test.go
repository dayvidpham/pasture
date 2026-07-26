package tasks

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
)

func TestEpochAssignmentServiceCreatesSliceThroughOneApply(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
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
}

func TestEpochAssignmentServiceFinalizesCleanReviewAuthority(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	supervisor, err := tracker.RegisterHumanAgent("review-supervisor", "Review Supervisor", "supervisor@example.test")
	if err != nil {
		t.Fatalf("register supervisor: %v", err)
	}
	reviewer, err := tracker.RegisterHumanAgent("reviewer", "Axis Reviewer", "reviewer@example.test")
	if err != nil {
		t.Fatalf("register reviewer: %v", err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	candidate := createHumanTestTask(t, tracker, "candidate")
	seedAssignmentEpisode(t, tracker, candidate, "review-supervisor", RoleGoverningSupervisor, supervisor.ID, "review-supervisor-seed")
	service, err := tracker.NewEpochService(EpochServiceOptions{})
	if err != nil {
		t.Fatalf("construct epoch service: %v", err)
	}
	started, err := service.StartReview(context.Background(), StartReviewInput{Meta: CommandMeta{OperationID: "review-start"}, Epoch: EpochRootID(epoch.String()), Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: candidate.String()}})
	if err != nil {
		t.Fatalf("start review: %v", err)
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
	finalized, err := service.FinalizeReview(context.Background(), FinalizeReviewInput{Meta: CommandMeta{OperationID: "review-finalize"}, Epoch: EpochRootID(epoch.String()), Round: started.Round, Assignment: "review-supervisor"})
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
