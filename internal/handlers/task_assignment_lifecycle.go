package handlers

import (
	"context"
	"encoding/json"
	"io"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// TaskStartAssignmentInput carries only the semantic values accepted by
// `pasture task assignment start`.
type TaskStartAssignmentInput struct {
	DBPath           string
	TaskID           string
	Assignment       string
	Actor            string
	Occupant         string
	ParentTask       string
	ParentAssignment string
	Output           io.Writer
}

// TaskStartAssignment validates and executes an initial owner assignment
// through the production TaskTracker boundary.
func TaskStartAssignment(ctx context.Context, in TaskStartAssignmentInput) error {
	if ctx == nil {
		return taskAssignmentStartValidationError("The task assignment start has no execution context.", "The command needs a live context before it can validate or open the task store", "invoke the command through the Pasture CLI", nil)
	}
	if in.Output == nil {
		return taskAssignmentStartValidationError("The task assignment start has no result output stream.", "A successful start must emit one semantic JSON result", "provide a writable standard-output stream", nil)
	}
	task, err := parseTaskAssignmentTransferTask(in.TaskID)
	if err != nil {
		return err
	}
	assignment, err := parseTaskAssignmentTransferScalar("assignment", in.Assignment)
	if err != nil {
		return err
	}
	actor, err := parseTaskAssignmentTransferActor("actor", in.Actor)
	if err != nil {
		return err
	}
	occupant, err := parseTaskAssignmentTransferActor("occupant", in.Occupant)
	if err != nil {
		return err
	}
	request := protocol.StartTaskAssignmentRequest{
		TaskID:       task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: provenance.AssignmentID(assignment),
		ActorID:      actor,
		Occupant:     occupant,
	}
	if (in.ParentTask == "") != (in.ParentAssignment == "") {
		return taskAssignmentStartValidationError("The parent citation is incomplete.", "--parent-task and --parent-assignment must be supplied together", "supply both parent values or neither", nil)
	}
	if in.ParentTask != "" {
		request.ParentTask, err = parseTaskAssignmentTransferTask(in.ParentTask)
		if err != nil {
			return err
		}
		parent, err := parseTaskAssignmentTransferScalar("parent-assignment", in.ParentAssignment)
		if err != nil {
			return err
		}
		request.ParentAssignment = provenance.AssignmentID(parent)
	}

	store, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return taskAssignmentStartStorageError("The task assignment start could not open the Pasture store after validating its input.", "The start requires the configured unified task store", "verify --db or PASTURE_DB_PATH is writable and retry the command", err)
	}
	starter, ok := store.(protocol.TaskAssignmentStarter)
	if !ok {
		_ = store.Close()
		return taskAssignmentStartWorkflowError("The task store does not implement assignment start.", "The installed Pasture build is older than this command", "upgrade Pasture and retry", nil)
	}
	result, runErr := starter.StartTaskAssignment(ctx, request)
	closeErr := store.Close()
	if runErr != nil {
		return taskAssignmentStartWorkflowError("The task assignment start was rejected.", "The task could not be assigned under the requested identity and current ownership", "correct the reported assignment state and retry the command", runErr)
	}
	if closeErr != nil {
		return taskAssignmentStartStorageError("The task assignment start completed but the Pasture store did not close cleanly.", "The database handle reported a close failure after the start returned", "inspect the database before retrying; an exact retry will not duplicate the start", closeErr)
	}
	wire, err := json.Marshal(struct {
		Assignment taskAssignmentTransferStateResult `json:"assignment"`
		Replayed   bool                              `json:"replayed"`
	}{
		Assignment: taskAssignmentTransferStateResultFrom(result.Assignment),
		Replayed:   result.Replayed,
	})
	if err != nil {
		return taskAssignmentStartWorkflowError("The task assignment start result could not be encoded as JSON.", "The fixed semantic result schema could not represent the completed start", "report the result-schema bug and retry after upgrading Pasture", err)
	}
	return writeEpochCommandResult("task assignment start", in.Output, append(wire, '\n'))
}

// TaskShowAssignmentInput carries the task to inspect.
type TaskShowAssignmentInput struct {
	DBPath string
	TaskID string
	Output io.Writer
}

// TaskShowAssignment prints the exact active owner assignment for a task.
func TaskShowAssignment(ctx context.Context, in TaskShowAssignmentInput) error {
	if ctx == nil || in.Output == nil {
		return taskAssignmentStartValidationError("The task assignment show has no execution context or output.", "The command needs a live context and a result stream", "invoke the command through the Pasture CLI", nil)
	}
	task, err := parseTaskAssignmentTransferTask(in.TaskID)
	if err != nil {
		return err
	}
	store, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return taskAssignmentStartStorageError("The task assignment show could not open the Pasture store.", "The read requires the configured unified task store", "verify --db or PASTURE_DB_PATH is readable and retry the command", err)
	}
	reader, ok := store.(protocol.TaskAssignmentReader)
	if !ok {
		_ = store.Close()
		return taskAssignmentStartWorkflowError("The task store does not implement assignment show.", "The installed Pasture build is older than this command", "upgrade Pasture and retry", nil)
	}
	state, runErr := reader.ShowTaskAssignment(ctx, task)
	closeErr := store.Close()
	if runErr != nil {
		return taskAssignmentStartWorkflowError("The task assignment show was rejected.", "No single active owner-responsibility assignment could be resolved for the task", "inspect the task's assignment history or start its owner assignment first", runErr)
	}
	if closeErr != nil {
		return taskAssignmentStartStorageError("The assignment show completed but the Pasture store did not close cleanly.", "The database handle reported a close failure after the read returned", "inspect the database and retry", closeErr)
	}
	wire, err := json.Marshal(taskAssignmentTransferStateResultFrom(state))
	if err != nil {
		return taskAssignmentStartWorkflowError("The assignment show result could not be encoded as JSON.", "The fixed semantic result schema could not represent the resolved assignment", "report the result-schema bug and retry after upgrading Pasture", err)
	}
	return writeEpochCommandResult("task assignment show", in.Output, append(wire, '\n'))
}

// TaskEndAssignmentInput carries only the semantic values accepted by
// `pasture task assignment end`.
type TaskEndAssignmentInput struct {
	DBPath     string
	TaskID     string
	Assignment string
	Actor      string
	Output     io.Writer
}

// TaskEndAssignment ends the exact active owner assignment for a task.
func TaskEndAssignment(ctx context.Context, in TaskEndAssignmentInput) error {
	if ctx == nil || in.Output == nil {
		return taskAssignmentStartValidationError("The task assignment end has no execution context or output.", "The command needs a live context and a result stream", "invoke the command through the Pasture CLI", nil)
	}
	task, err := parseTaskAssignmentTransferTask(in.TaskID)
	if err != nil {
		return err
	}
	assignment, err := parseTaskAssignmentTransferScalar("assignment", in.Assignment)
	if err != nil {
		return err
	}
	actor, err := parseTaskAssignmentTransferActor("actor", in.Actor)
	if err != nil {
		return err
	}
	store, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return taskAssignmentStartStorageError("The task assignment end could not open the Pasture store after validating its input.", "The end requires the configured unified task store", "verify --db or PASTURE_DB_PATH is writable and retry the command", err)
	}
	ender, ok := store.(protocol.TaskAssignmentEnder)
	if !ok {
		_ = store.Close()
		return taskAssignmentStartWorkflowError("The task store does not implement assignment end.", "The installed Pasture build is older than this command", "upgrade Pasture and retry", nil)
	}
	replayed, runErr := ender.EndTaskAssignment(ctx, protocol.EndTaskAssignmentRequest{
		TaskID:       task,
		AssignmentID: provenance.AssignmentID(assignment),
		ActorID:      actor,
	})
	closeErr := store.Close()
	if runErr != nil {
		return taskAssignmentStartWorkflowError("The task assignment end was rejected.", "The exact active assignment could not be ended", "show the current assignment and retry with that id", runErr)
	}
	if closeErr != nil {
		return taskAssignmentStartStorageError("The task assignment end completed but the Pasture store did not close cleanly.", "The database handle reported a close failure after the end returned", "inspect the database before retrying; an exact retry will not duplicate the end", closeErr)
	}
	wire, err := json.Marshal(struct {
		TaskID       string `json:"taskId"`
		AssignmentID string `json:"assignmentId"`
		Replayed     bool   `json:"replayed"`
	}{task.String(), assignment, replayed})
	if err != nil {
		return taskAssignmentStartWorkflowError("The task assignment end result could not be encoded as JSON.", "The fixed semantic result schema could not represent the completed end", "report the result-schema bug and retry after upgrading Pasture", err)
	}
	return writeEpochCommandResult("task assignment end", in.Output, append(wire, '\n'))
}

func taskAssignmentStartValidationError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why + ".",
		Where:    "Validating task assignment start/show/end (internal/handlers/task_assignment_lifecycle.go).",
		Impact:   "The Pasture store was not opened and no assignment changed.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}

func taskAssignmentStartWorkflowError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     what,
		Why:      why + ".",
		Where:    "Executing task assignment start/show/end (internal/handlers/task_assignment_lifecycle.go).",
		Impact:   "The assignment lifecycle command did not return a successful semantic result.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}

func taskAssignmentStartStorageError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     what,
		Why:      why + ".",
		Where:    "Opening or closing the Pasture store (internal/handlers/task_assignment_lifecycle.go).",
		Impact:   "The task store operation could not complete safely.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}
