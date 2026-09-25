package tasks

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

type taskAssignmentTransferFixture struct {
	tracker *trackerImpl
	task    provenance.TaskID
	actorA  provenance.ActorID
	actorB  provenance.ActorID
}

func newTaskAssignmentTransferFixture(t *testing.T) taskAssignmentTransferFixture {
	t.Helper()
	opened, err := OpenTaskTracker(filepath.Join(t.TempDir(), "pasture.db"))
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	tracker, ok := opened.(*trackerImpl)
	if !ok {
		t.Fatalf("OpenTaskTracker type = %T, want *trackerImpl", opened)
	}
	t.Cleanup(func() { _ = tracker.Close() })

	register := func(name string) provenance.ActorID {
		t.Helper()
		agent, err := tracker.RegisterSoftwareAgent("assignment-transfer", name, "1", "test")
		if err != nil {
			t.Fatalf("RegisterSoftwareAgent(%q): %v", name, err)
		}
		return agent.ID
	}
	actorA := register("actor-a")
	actorB := register("actor-b")
	task, err := tracker.Create("assignment-transfer", "transfer target", "test target", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseWorkerSlices)
	if err != nil {
		t.Fatalf("Create target task: %v", err)
	}
	return taskAssignmentTransferFixture{tracker: tracker, task: task.ID, actorA: actorA, actorB: actorB}
}

func (f taskAssignmentTransferFixture) seedOwnerAssignment(t *testing.T, assignment provenance.AssignmentID) {
	t.Helper()
	session, err := f.tracker.systemSession()
	if err != nil {
		t.Fatalf("systemSession: %v", err)
	}
	started, err := MapMaterialEvent(AssignmentStartedEvent{Task: f.task, Assignment: assignment, Role: RoleOwnerResponsibility, Occupant: f.actorA})
	if err != nil {
		t.Fatalf("MapMaterialEvent: %v", err)
	}
	if _, err := session.Atomic(func(operation *provenance.Operation) {
		operation.Add(provenance.Effect{Sort: provenance.EffectAssignmentStart, TaskID: f.task, AssignmentID: assignment, SlotID: provenance.SlotOwnerResponsibility, Occupant: f.actorA})
		operation.Add(started)
	}, provenance.WithOperationID("pasture.task-assignment-transfer.seed-owner")); err != nil {
		t.Fatalf("seed owner assignment: %v", err)
	}
}

func TestTransferTaskAssignmentSuccessAndExactReplay(t *testing.T) {
	fixture := newTaskAssignmentTransferFixture(t)
	fixture.seedOwnerAssignment(t, "owner-a")
	request := protocol.TransferTaskAssignmentRequest{
		TaskID:           fixture.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "owner-b",
		ActorID:          fixture.actorA,
		NextOccupant:     fixture.actorB,
	}

	first, err := fixture.tracker.TransferTaskAssignment(context.Background(), request)
	if err != nil {
		t.Fatalf("TransferTaskAssignment(first): %v", err)
	}
	want := protocol.TransferTaskAssignmentResult{
		Previous: protocol.TaskAssignmentState{TaskID: fixture.task, Slot: provenance.SlotOwnerResponsibility, AssignmentID: "owner-a", Occupant: fixture.actorA},
		Next:     protocol.TaskAssignmentState{TaskID: fixture.task, Slot: provenance.SlotOwnerResponsibility, AssignmentID: "owner-b", Occupant: fixture.actorB},
	}
	if first != want {
		t.Fatalf("TransferTaskAssignment(first) = %+v, want %+v", first, want)
	}

	replayed, err := fixture.tracker.TransferTaskAssignment(context.Background(), request)
	if err != nil {
		t.Fatalf("TransferTaskAssignment(exact replay): %v", err)
	}
	want.Replayed = true
	if replayed != want {
		t.Fatalf("TransferTaskAssignment(replay) = %+v, want %+v", replayed, want)
	}

	task, err := fixture.tracker.Show(fixture.task)
	if err != nil {
		t.Fatalf("Show transferred task: %v", err)
	}
	if task.Owner == nil || *task.Owner != fixture.actorB {
		t.Fatalf("transferred owner = %v, want %s", task.Owner, fixture.actorB)
	}
}

func TestTransferTaskAssignmentRejectsMissingSelectionWithoutWrite(t *testing.T) {
	fixture := newTaskAssignmentTransferFixture(t)
	request := protocol.TransferTaskAssignmentRequest{
		TaskID:           fixture.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "owner-b",
		ActorID:          fixture.actorA,
		NextOccupant:     fixture.actorB,
	}

	_, err := fixture.tracker.TransferTaskAssignment(context.Background(), request)
	assertTaskAssignmentTransferError(t, err, protocol.TaskAssignmentTransferMissingAssignment)
	assertTaskAssignmentTransferAbsent(t, fixture.tracker, request)
}

func TestSelectTaskAssignmentTransferRejectsAmbiguousSelection(t *testing.T) {
	_, err := selectTaskAssignmentTransfer([]taskAssignmentResolution{
		{assignmentID: "owner-a"},
		{assignmentID: "owner-b"},
	}, protocol.TaskAssignmentTransferMissingAssignment)
	assertTaskAssignmentTransferError(t, err, protocol.TaskAssignmentTransferAmbiguousAssignment)
}

func TestTransferTaskAssignmentRejectsWrongTaskAssignmentRecordWithoutWrite(t *testing.T) {
	fixture := newTaskAssignmentTransferFixture(t)
	other, err := fixture.tracker.Create("assignment-transfer", "other task", "other task", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseWorkerSlices)
	if err != nil {
		t.Fatalf("Create other task: %v", err)
	}
	session, err := fixture.tracker.systemSession()
	if err != nil {
		t.Fatalf("systemSession: %v", err)
	}
	started, err := MapMaterialEvent(AssignmentStartedEvent{Task: fixture.task, Assignment: "wrong-task-owner", Role: RoleOwnerResponsibility, Occupant: fixture.actorA})
	if err != nil {
		t.Fatalf("MapMaterialEvent: %v", err)
	}
	if _, err := session.Atomic(func(operation *provenance.Operation) {
		operation.Add(provenance.Effect{Sort: provenance.EffectAssignmentStart, TaskID: other.ID, AssignmentID: "other-owner", SlotID: provenance.SlotOwnerResponsibility, Occupant: fixture.actorA})
		operation.Add(started)
	}, provenance.WithOperationID("pasture.task-assignment-transfer.seed-wrong-task")); err != nil {
		t.Fatalf("seed wrong-task assignment record: %v", err)
	}
	request := protocol.TransferTaskAssignmentRequest{
		TaskID:           fixture.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "owner-b",
		ActorID:          fixture.actorA,
		NextOccupant:     fixture.actorB,
	}

	_, err = fixture.tracker.TransferTaskAssignment(context.Background(), request)
	assertTaskAssignmentTransferError(t, err, protocol.TaskAssignmentTransferMismatchedAssignment)
	assertTaskAssignmentTransferAbsent(t, fixture.tracker, request)
}

func TestTransferTaskAssignmentRejectsStaleAssignmentWithoutWrite(t *testing.T) {
	fixture := newTaskAssignmentTransferFixture(t)
	fixture.seedOwnerAssignment(t, "owner-a")
	session, err := fixture.tracker.systemSession()
	if err != nil {
		t.Fatalf("systemSession: %v", err)
	}
	if _, err := session.Atomic(func(operation *provenance.Operation) {
		operation.Add(provenance.Effect{Sort: provenance.EffectAssignmentEnd, TaskID: fixture.task, AssignmentID: "owner-a", SlotID: provenance.SlotOwnerResponsibility})
	}, provenance.WithOperationID("pasture.task-assignment-transfer.end-owner")); err != nil {
		t.Fatalf("end owner assignment: %v", err)
	}
	request := protocol.TransferTaskAssignmentRequest{
		TaskID:           fixture.task,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: "owner-b",
		ActorID:          fixture.actorA,
		NextOccupant:     fixture.actorB,
	}

	_, err = fixture.tracker.TransferTaskAssignment(context.Background(), request)
	assertTaskAssignmentTransferError(t, err, protocol.TaskAssignmentTransferStaleAssignment)
	if !errors.Is(err, provenance.ErrStaleEpisode) {
		t.Fatalf("stale transfer error = %v, want errors.Is(..., ErrStaleEpisode)", err)
	}
	assertTaskAssignmentTransferAbsent(t, fixture.tracker, request)
}

// TestTransferAuthorityErrorDescribesTheTransfer holds the transfer's authority
// refusal to its six parts and to the two facts an operator acts on: the
// transfer IS committed, and the SAME transfer retried writes the fact once. The
// text names no retired command and no retired machinery, because an operator
// cannot act on either.
//
// RED when: a part is dropped, the task is unnamed, the impact no longer says
// the transfer is committed, or the fix names a command that no longer exists.
func TestTransferAuthorityErrorDescribesTheTransfer(t *testing.T) {
	t.Parallel()
	task := deterministicTask("transfer-authority-error", "task")
	_, err := transferAssignmentAuthority(nil, 0, 0, task)
	if err == nil {
		t.Fatalf("a transfer with no anchor was accepted; there is nothing to scan from")
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("error type = %T (%v), want the structured storage refusal", err, err)
	}
	if structured.Category != pasterrors.CategoryStorage {
		t.Fatalf("category = %s, want %s: a failed write of a durable fact is storage, not a policy denial",
			structured.Category, pasterrors.CategoryStorage)
	}
	var report bytes.Buffer
	structured.Report(&report)
	rendered := report.String()
	if rendered == "" {
		t.Fatalf("the structured refusal rendered an empty report")
	}
	if !strings.Contains(structured.What, task.String()) {
		t.Errorf("What does not name the task the transfer gave away: %q", structured.What)
	}
	if !strings.Contains(structured.Why, "no anchor") {
		t.Errorf("Why does not say what was wrong: %q", structured.Why)
	}
	if !strings.Contains(structured.Where, "internal/tasks/assignment_authority.go") ||
		!strings.Contains(structured.Where, "tasks.transferAssignmentAuthority") {
		t.Errorf("Where does not resolve to the function that refused: %q", structured.Where)
	}
	if !strings.Contains(structured.Impact, "transfer is committed") {
		t.Errorf("Impact does not tell the operator the transfer itself survived: %q", structured.Impact)
	}
	if !strings.Contains(structured.Fix, "same transfer again") {
		t.Errorf("Fix does not tell the operator what to do: %q", structured.Fix)
	}
	for _, retired := range []string{"rebuild-index", "index", "certif", "generation"} {
		if strings.Contains(strings.ToLower(rendered), retired) {
			t.Errorf("the refusal names retired machinery an operator can no longer act on: %q in\n%s", retired, rendered)
		}
	}
}

func assertTaskAssignmentTransferError(t *testing.T, err error, want protocol.TaskAssignmentTransferErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var transferErr *protocol.TaskAssignmentTransferError
	if !errors.As(err, &transferErr) {
		t.Fatalf("error type = %T (%v), want *TaskAssignmentTransferError", err, err)
	}
	if transferErr.Kind != want {
		t.Fatalf("transfer error kind = %s, want %s", transferErr.Kind, want)
	}
}

func assertTaskAssignmentTransferAbsent(t *testing.T, tracker *trackerImpl, request protocol.TransferTaskAssignmentRequest) {
	t.Helper()
	committed, err := tracker.Journal().LookupCommitted(taskAssignmentTransferOperationID(request))
	if err != nil {
		t.Fatalf("LookupCommitted: %v", err)
	}
	if committed.Kind != provenance.CommittedAbsent {
		t.Fatalf("transfer operation result = %+v, want CommittedAbsent", committed)
	}
}
