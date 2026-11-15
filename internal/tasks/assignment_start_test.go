package tasks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

func TestStartTaskAssignmentShowTransferEndAndReplay(t *testing.T) {
	f := newTaskAssignmentTransferFixture(t)
	ctx := context.Background()
	request := protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "first",
		ActorID:      f.actorA,
		Occupant:     f.actorB,
	}
	first, err := f.tracker.StartTaskAssignment(ctx, request)
	if err != nil {
		t.Fatalf("StartTaskAssignment: %v", err)
	}
	if first.Replayed {
		t.Fatal("first start reported replay")
	}
	events, err := f.tracker.prov.Journal().QueryTaskEvents(provenance.JournalQueryV1{
		OrderBy:    provenance.OrderByJournalID,
		TaskIDs:    []provenance.TaskID{f.task},
		EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("query assignment material facts: %v", err)
	}
	foundMaterial := false
	for _, event := range events.Events {
		if strings.Contains(string(event.Payload), `"assignment":"first"`) && strings.Contains(string(event.Payload), `"occupant":"`+f.actorB.String()+`"`) {
			foundMaterial = true
		}
	}
	if !foundMaterial {
		t.Fatalf("start committed no assignment material fact: %+v", events.Events)
	}
	shown, err := f.tracker.ShowTaskAssignment(ctx, f.task)
	if err != nil {
		t.Fatalf("ShowTaskAssignment: %v", err)
	}
	if shown.AssignmentID != "first" || shown.Occupant != f.actorB {
		t.Fatalf("show = %+v, want first/%s", shown, f.actorB)
	}
	again, err := f.tracker.StartTaskAssignment(ctx, request)
	if err != nil || !again.Replayed {
		t.Fatalf("exact replay = %+v, %v; want replayed", again, err)
	}
	changed := request
	changed.Occupant = f.actorA
	if _, err := f.tracker.StartTaskAssignment(ctx, changed); err == nil {
		t.Fatal("changed request at the same assignment id succeeded")
	} else {
		assertTaskAssignmentStartError(t, err, protocol.TaskAssignmentStartAlreadyAssigned)
		assertTaskAssignmentStartAbsent(t, f.tracker, changed)
	}
	second := protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "second",
		ActorID:      f.actorA,
		Occupant:     f.actorA,
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, second); err == nil {
		t.Fatal("second active owner accepted")
	} else {
		assertTaskAssignmentStartError(t, err, protocol.TaskAssignmentStartAlreadyAssigned)
		assertTaskAssignmentStartAbsent(t, f.tracker, second)
	}
	transfer := protocol.TransferTaskAssignmentRequest{
		TaskID:           f.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "second",
		ActorID:          f.actorA,
		NextOccupant:     f.actorA,
	}
	if _, err := f.tracker.TransferTaskAssignment(ctx, transfer); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	shown, err = f.tracker.ShowTaskAssignment(ctx, f.task)
	if err != nil || shown.AssignmentID != "second" || shown.Occupant != f.actorA {
		t.Fatalf("show after transfer = %+v, %v", shown, err)
	}
	if _, err := f.tracker.EndTaskAssignment(ctx, protocol.EndTaskAssignmentRequest{
		TaskID:       f.task,
		AssignmentID: "first",
		ActorID:      f.actorA,
	}); err == nil {
		t.Fatal("stale assignment end accepted")
	}
	replayEnd, err := f.tracker.EndTaskAssignment(ctx, protocol.EndTaskAssignmentRequest{
		TaskID:       f.task,
		AssignmentID: "second",
		ActorID:      f.actorA,
	})
	if err != nil || replayEnd {
		t.Fatalf("end = %+v, %v; want first application", replayEnd, err)
	}
	replayEnd, err = f.tracker.EndTaskAssignment(ctx, protocol.EndTaskAssignmentRequest{
		TaskID:       f.task,
		AssignmentID: "second",
		ActorID:      f.actorA,
	})
	if err != nil || !replayEnd {
		t.Fatalf("end replay = %+v, %v; want replayed", replayEnd, err)
	}
	if _, err := f.tracker.ShowTaskAssignment(ctx, f.task); err == nil {
		t.Fatal("show after end returned an active assignment")
	}
	replayAfterEnd, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "first",
		ActorID:      f.actorA,
		Occupant:     f.actorB,
	})
	if err != nil || !replayAfterEnd.Replayed {
		t.Fatalf("exact start replay after end = %+v, %v; want replayed", replayAfterEnd, err)
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "third",
		ActorID:      f.actorA,
		Occupant:     f.actorA,
	}); err != nil {
		t.Fatalf("fresh start after end: %v", err)
	}
}

func TestStartTaskAssignmentRejectsUnregisteredActors(t *testing.T) {
	f := newTaskAssignmentTransferFixture(t)
	unknown, err := provenance.ParseActorID("unknown/00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []protocol.StartTaskAssignmentRequest{
		{TaskID: f.task, Slot: provenance.SlotOwnerResponsibility, AssignmentID: "actor-unknown", ActorID: unknown, Occupant: f.actorA},
		{TaskID: f.task, Slot: provenance.SlotOwnerResponsibility, AssignmentID: "occupant-unknown", ActorID: f.actorA, Occupant: unknown},
	} {
		_, err := f.tracker.StartTaskAssignment(context.Background(), request)
		assertTaskAssignmentStartError(t, err, protocol.TaskAssignmentStartUnregisteredActor)
		assertTaskAssignmentStartAbsent(t, f.tracker, request)
	}
}

func TestStartTaskAssignmentParentLineageAndCloseGate(t *testing.T) {
	f := newTaskAssignmentTransferFixture(t)
	ctx := context.Background()
	parent, err := f.tracker.Create("assignment-transfer", "parent", "parent", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseWorkerSlices)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       parent.ID,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "parent-owner",
		ActorID:      f.actorA,
		Occupant:     f.actorB,
	}); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:           f.task,
		Slot:             provenance.SlotOwnerResponsibility,
		AssignmentID:     "child-owner",
		ActorID:          f.actorA,
		Occupant:         f.actorB,
		ParentTask:       parent.ID,
		ParentAssignment: "parent-owner",
	}); err != nil {
		t.Fatalf("start child under active parent: %v / cause: %v", err, errors.Unwrap(err))
	}
	branch, err := f.tracker.Create("assignment-transfer", "parent branch", "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseWorkerSlices)
	if err != nil {
		t.Fatal(err)
	}
	wrongParent := protocol.StartTaskAssignmentRequest{
		TaskID:           branch.ID,
		Slot:             provenance.SlotOwnerResponsibility,
		AssignmentID:     "wrong-parent",
		ActorID:          f.actorA,
		Occupant:         f.actorB,
		ParentTask:       parent.ID,
		ParentAssignment: "missing-parent-owner",
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, wrongParent); err == nil {
		t.Fatal("mismatched parent accepted on a fresh assignment id")
	} else {
		assertTaskAssignmentStartError(t, err, protocol.TaskAssignmentStartParentMismatch)
		assertTaskAssignmentStartAbsent(t, f.tracker, wrongParent)
	}
	other, err := f.tracker.Create("assignment-transfer", "other parent", "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseWorkerSlices)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       other.ID,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "other-owner",
		ActorID:      f.actorA,
		Occupant:     f.actorB,
	}); err != nil {
		t.Fatalf("start other parent: %v", err)
	}
	foreignParent := protocol.StartTaskAssignmentRequest{
		TaskID:           branch.ID,
		Slot:             provenance.SlotOwnerResponsibility,
		AssignmentID:     "foreign-parent",
		ActorID:          f.actorA,
		Occupant:         f.actorB,
		ParentTask:       parent.ID,
		ParentAssignment: "other-owner",
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, foreignParent); err == nil {
		t.Fatal("parent assignment active on a different task was accepted")
	} else {
		assertTaskAssignmentStartError(t, err, protocol.TaskAssignmentStartParentMismatch)
		assertTaskAssignmentStartAbsent(t, f.tracker, foreignParent)
	}
	if _, err := f.tracker.EndTaskAssignment(ctx, protocol.EndTaskAssignmentRequest{TaskID: parent.ID, AssignmentID: "parent-owner", ActorID: f.actorA}); err != nil {
		t.Fatalf("end parent: %v", err)
	}
	endedParent := protocol.StartTaskAssignmentRequest{
		TaskID:           branch.ID,
		Slot:             provenance.SlotOwnerResponsibility,
		AssignmentID:     "ended-parent",
		ActorID:          f.actorA,
		Occupant:         f.actorB,
		ParentTask:       parent.ID,
		ParentAssignment: "parent-owner",
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, endedParent); err == nil {
		t.Fatal("ended parent assignment was accepted")
	} else {
		assertTaskAssignmentStartError(t, err, protocol.TaskAssignmentStartParentMismatch)
		assertTaskAssignmentStartAbsent(t, f.tracker, endedParent)
	}
	if _, err := f.tracker.CloseTask(f.task, "premature"); err == nil {
		t.Fatal("close with an active owner was accepted")
	}
	if _, err := f.tracker.EndTaskAssignment(ctx, protocol.EndTaskAssignmentRequest{
		TaskID:       f.task,
		AssignmentID: "child-owner",
		ActorID:      f.actorA,
	}); err != nil {
		t.Fatalf("end child: %v", err)
	}
	if _, err := f.tracker.CloseTask(f.task, "done"); err != nil {
		t.Fatalf("close after end: %v", err)
	}
	closedStart := protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "closed-task",
		ActorID:      f.actorA,
		Occupant:     f.actorA,
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, closedStart); err == nil {
		t.Fatal("start on a closed task succeeded")
	} else {
		assertTaskAssignmentStartError(t, err, protocol.TaskAssignmentStartClosedTask)
		assertTaskAssignmentStartAbsent(t, f.tracker, closedStart)
	}
}

func TestStartTaskAssignmentSeesNonOwnerRoleOnTheSlot(t *testing.T) {
	f := newTaskAssignmentTransferFixture(t)
	ctx := context.Background()
	session, err := f.tracker.systemSession()
	if err != nil {
		t.Fatal(err)
	}
	material, err := MapMaterialEvent(AssignmentStartedEvent{Task: f.task, Assignment: "review-child", Role: RoleAxisReviewer, Occupant: f.actorA})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Atomic(func(operation *provenance.Operation) {
		operation.Add(provenance.Effect{Sort: provenance.EffectAssignmentStart, TaskID: f.task, AssignmentID: "review-child", SlotID: provenance.SlotOwnerResponsibility, Occupant: f.actorA})
		operation.Add(material)
	}, provenance.WithOperationID("seed-review-child")); err != nil {
		t.Fatalf("seed axis-reviewer episode: %v", err)
	}
	_, err = f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "second-owner",
		ActorID:      f.actorA,
		Occupant:     f.actorB,
	})
	var typed *protocol.TaskAssignmentStartError
	if !errors.As(err, &typed) || typed.Kind != protocol.TaskAssignmentStartAlreadyAssigned {
		t.Fatalf("start err = %v, want already assigned for the non-owner role", err)
	}
	_, err = f.tracker.ShowTaskAssignment(ctx, f.task)
	if err == nil || !strings.Contains(err.Error(), "review-child") {
		t.Fatalf("show err = %v, want the live non-owner assignment id", err)
	}
	if _, err := f.tracker.CloseTask(f.task, "premature"); err == nil {
		t.Fatal("close with a live non-owner assignment was accepted")
	}
}

func TestStartTaskAssignmentRejectsNilUUIDOccupant(t *testing.T) {
	f := newTaskAssignmentTransferFixture(t)
	occupant, err := provenance.ParseActorID("pasture-system/00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.tracker.StartTaskAssignment(context.Background(), protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "system-occupant",
		ActorID:      f.actorA,
		Occupant:     occupant,
	})
	var typed *protocol.TaskAssignmentStartError
	if !errors.As(err, &typed) || typed.Kind != protocol.TaskAssignmentStartUnsupportedOccupant {
		t.Fatalf("err = %v, want unsupported occupant", err)
	}
}

func TestStartTaskAssignmentConcurrentStartsHaveOneWinner(t *testing.T) {
	f := newTaskAssignmentTransferFixture(t)
	ctx := context.Background()
	ready := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, assignment := range []provenance.AssignmentID{"race-a", "race-b"} {
		i, assignment := i, assignment
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			_, errs[i] = f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
				TaskID:       f.task,
				Slot:         provenance.SlotOwnerResponsibility,
				AssignmentID: assignment,
				ActorID:      f.actorA,
				Occupant:     f.actorB,
			})
		}()
	}
	close(ready)
	wg.Wait()
	successes, alreadyAssigned := 0, 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		var typed *protocol.TaskAssignmentStartError
		if errors.As(err, &typed) && typed.Kind == protocol.TaskAssignmentStartAlreadyAssigned {
			alreadyAssigned++
			continue
		}
		t.Fatalf("unexpected concurrent start error: %v", err)
	}
	if successes != 1 || alreadyAssigned != 1 {
		t.Fatalf("concurrent starts = %d success, %d already-assigned; want one of each", successes, alreadyAssigned)
	}
	if _, err := f.tracker.ShowTaskAssignment(ctx, f.task); err != nil {
		t.Fatalf("show after concurrent starts: %v", err)
	}
}

func TestStartTaskAssignmentRefusesIdentityReuseAndMissingTask(t *testing.T) {
	f := newTaskAssignmentTransferFixture(t)
	ctx := context.Background()
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "reused",
		ActorID:      f.actorA,
		Occupant:     f.actorB,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.tracker.EndTaskAssignment(ctx, protocol.EndTaskAssignmentRequest{TaskID: f.task, AssignmentID: "reused", ActorID: f.actorA}); err != nil {
		t.Fatal(err)
	}
	_, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "reused",
		ActorID:      f.actorA,
		Occupant:     f.actorA,
	})
	var typed *protocol.TaskAssignmentStartError
	if !errors.As(err, &typed) || typed.Kind != protocol.TaskAssignmentStartAssignmentIDReused {
		t.Fatalf("reuse err = %v, want assignment id reused", err)
	}
	missing, err := provenance.ParseTaskID("assignment-transfer/00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       missing,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "missing",
		ActorID:      f.actorA,
		Occupant:     f.actorA,
	})
	if !errors.As(err, &typed) || typed.Kind != protocol.TaskAssignmentStartMissingTask {
		t.Fatalf("missing task err = %v, want missing task", err)
	}
}

func assertTaskAssignmentStartError(t *testing.T, err error, want protocol.TaskAssignmentStartErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var startErr *protocol.TaskAssignmentStartError
	if !errors.As(err, &startErr) {
		t.Fatalf("error type = %T (%v), want *TaskAssignmentStartError", err, err)
	}
	if startErr.Kind != want {
		t.Fatalf("start error kind = %s, want %s", startErr.Kind, want)
	}
}

func assertTaskAssignmentStartAbsent(t *testing.T, tracker *trackerImpl, request protocol.StartTaskAssignmentRequest) {
	t.Helper()
	committed, err := tracker.Journal().LookupCommitted(startAssignmentOperationID(request))
	if err != nil {
		t.Fatalf("LookupCommitted: %v", err)
	}
	if committed.Kind != provenance.CommittedAbsent {
		t.Fatalf("start operation result = %+v, want CommittedAbsent", committed)
	}
}
