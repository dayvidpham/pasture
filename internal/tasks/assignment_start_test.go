package tasks

import (
	"context"
	"errors"
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
	}
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "second",
		ActorID:      f.actorA,
		Occupant:     f.actorA,
	}); err == nil {
		t.Fatal("second active owner accepted")
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
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "first",
		ActorID:      f.actorA,
		Occupant:     f.actorB,
	}); err != nil {
		t.Fatalf("exact start replay after end: %v", err)
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
		var typed *protocol.TaskAssignmentStartError
		if !errors.As(err, &typed) || typed.Kind != protocol.TaskAssignmentStartUnregisteredActor {
			t.Fatalf("err = %v, want unregistered actor", err)
		}
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
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:           f.task,
		Slot:             provenance.SlotOwnerResponsibility,
		AssignmentID:     "wrong-parent",
		ActorID:          f.actorA,
		Occupant:         f.actorB,
		ParentTask:       parent.ID,
		ParentAssignment: "missing-parent-owner",
	}); err == nil {
		t.Fatal("mismatched parent accepted on a fresh assignment id")
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
	if _, err := f.tracker.StartTaskAssignment(ctx, protocol.StartTaskAssignmentRequest{
		TaskID:       f.task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: "closed-task",
		ActorID:      f.actorA,
		Occupant:     f.actorA,
	}); err == nil {
		t.Fatal("start on a closed task succeeded")
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
