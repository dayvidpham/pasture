package handlers

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

func TestTaskTransferAssignmentValidatesBeforeOpeningStore(t *testing.T) {
	t.Parallel()

	opened := false
	err := taskTransferAssignment(context.Background(), TaskTransferAssignmentInput{
		TaskID:         "not-a-task-id",
		Slot:           "owner-responsibility",
		NextAssignment: "assignment/next",
		Actor:          "cli--01960000-0000-7000-8000-000000000001",
		Occupant:       "cli--01960000-0000-7000-8000-000000000002",
		Output:         &bytes.Buffer{},
	}, func(string) (taskAssignmentTransferStore, error) {
		opened = true
		return nil, errors.New("unexpected store open")
	})
	if err == nil {
		t.Fatal("taskTransferAssignment succeeded with an invalid task ID")
	}
	if opened {
		t.Fatal("task transfer opened the store before validating the task ID")
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || structured.Category != pasterrors.CategoryValidation {
		t.Fatalf("error = %T %v, want validation structured error", err, err)
	}
	if structured.Impact != "The Pasture store was not opened and no assignment changed." {
		t.Fatalf("validation impact = %q", structured.Impact)
	}
}

func TestTaskTransferAssignmentUsesOnlySemanticRequestAndResult(t *testing.T) {
	t.Parallel()

	taskID := mustTaskAssignmentTransferTask(t, "cli--01960000-0000-7000-8000-000000000010")
	actor := mustTaskAssignmentTransferActor(t, "cli--01960000-0000-7000-8000-000000000011")
	occupant := mustTaskAssignmentTransferActor(t, "cli--01960000-0000-7000-8000-000000000012")
	store := &taskAssignmentTransferTestStore{result: protocol.TransferTaskAssignmentResult{
		Previous: protocol.TaskAssignmentState{
			TaskID:       taskID,
			Slot:         provenance.SlotOwnerResponsibility,
			AssignmentID: "assignment/previous",
			Occupant:     actor,
		},
		Next: protocol.TaskAssignmentState{
			TaskID:       taskID,
			Slot:         provenance.SlotOwnerResponsibility,
			AssignmentID: "assignment/next",
			Occupant:     occupant,
		},
		Replayed: true,
	}}
	var output bytes.Buffer
	err := taskTransferAssignment(context.Background(), TaskTransferAssignmentInput{
		TaskID:         taskID.String(),
		Slot:           "owner-responsibility",
		NextAssignment: "assignment/next",
		Actor:          actor.String(),
		Occupant:       occupant.String(),
		Output:         &output,
	}, func(string) (taskAssignmentTransferStore, error) {
		return store, nil
	})
	if err != nil {
		t.Fatalf("taskTransferAssignment: %v", err)
	}
	if !store.closed {
		t.Fatal("task transfer did not close the tracker")
	}
	wantRequest := protocol.TransferTaskAssignmentRequest{
		TaskID:           taskID,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "assignment/next",
		ActorID:          actor,
		NextOccupant:     occupant,
	}
	if store.request != wantRequest {
		t.Fatalf("request = %+v, want %+v", store.request, wantRequest)
	}
	const wantOutput = `{"previous":{"taskId":"cli--01960000-0000-7000-8000-000000000010","slot":"owner-responsibility","assignmentId":"assignment/previous","occupant":"cli--01960000-0000-7000-8000-000000000011"},"next":{"taskId":"cli--01960000-0000-7000-8000-000000000010","slot":"owner-responsibility","assignmentId":"assignment/next","occupant":"cli--01960000-0000-7000-8000-000000000012"},"replayed":true}` + "\n"
	if got := output.String(); got != wantOutput {
		t.Fatalf("output = %q, want %q", got, wantOutput)
	}
}

func TestTaskTransferAssignmentPreservesTypedTransferError(t *testing.T) {
	t.Parallel()

	store := &taskAssignmentTransferTestStore{err: protocol.NewTaskAssignmentTransferError(protocol.TaskAssignmentTransferStaleAssignment, errors.New("stale"))}
	err := taskTransferAssignment(context.Background(), validTaskAssignmentTransferInput(&bytes.Buffer{}), func(string) (taskAssignmentTransferStore, error) {
		return store, nil
	})
	if err == nil {
		t.Fatal("taskTransferAssignment succeeded with a transfer failure")
	}
	var transferErr *protocol.TaskAssignmentTransferError
	if !errors.As(err, &transferErr) || transferErr.Kind != protocol.TaskAssignmentTransferStaleAssignment {
		t.Fatalf("error = %T %v, want wrapped stale TaskAssignmentTransferError", err, err)
	}
	if !store.closed {
		t.Fatal("task transfer did not close the tracker after a transfer failure")
	}
}

type taskAssignmentTransferTestStore struct {
	request protocol.TransferTaskAssignmentRequest
	result  protocol.TransferTaskAssignmentResult
	err     error
	closed  bool
}

func (s *taskAssignmentTransferTestStore) TransferTaskAssignment(_ context.Context, request protocol.TransferTaskAssignmentRequest) (protocol.TransferTaskAssignmentResult, error) {
	s.request = request
	return s.result, s.err
}

func (s *taskAssignmentTransferTestStore) Close() error {
	s.closed = true
	return nil
}

func validTaskAssignmentTransferInput(output *bytes.Buffer) TaskTransferAssignmentInput {
	return TaskTransferAssignmentInput{
		TaskID:         "cli--01960000-0000-7000-8000-000000000020",
		Slot:           "owner-responsibility",
		NextAssignment: "assignment/next",
		Actor:          "cli--01960000-0000-7000-8000-000000000021",
		Occupant:       "cli--01960000-0000-7000-8000-000000000022",
		Output:         output,
	}
}

func mustTaskAssignmentTransferTask(t *testing.T, value string) provenance.TaskID {
	t.Helper()
	id, err := provenance.ParseTaskID(value)
	if err != nil {
		t.Fatalf("ParseTaskID(%q): %v", value, err)
	}
	return id
}

func mustTaskAssignmentTransferActor(t *testing.T, value string) provenance.ActorID {
	t.Helper()
	id, err := provenance.ParseActorID(value)
	if err != nil {
		t.Fatalf("ParseActorID(%q): %v", value, err)
	}
	return id
}
