package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// TaskTransferAssignmentInput carries only the semantic values accepted by
// `pasture task assignment transfer`.
type TaskTransferAssignmentInput struct {
	DBPath         string
	TaskID         string
	Slot           string
	NextAssignment string
	Actor          string
	Occupant       string
	Output         io.Writer
}

type taskAssignmentTransferStore interface {
	TransferTaskAssignment(context.Context, protocol.TransferTaskAssignmentRequest) (protocol.TransferTaskAssignmentResult, error)
	Close() error
}

type taskAssignmentTransferStoreOpener func(string) (taskAssignmentTransferStore, error)

type taskAssignmentTransferStateResult struct {
	TaskID       string `json:"taskId"`
	Slot         string `json:"slot"`
	AssignmentID string `json:"assignmentId"`
	Occupant     string `json:"occupant"`
}

type taskAssignmentTransferResult struct {
	Previous taskAssignmentTransferStateResult `json:"previous"`
	Next     taskAssignmentTransferStateResult `json:"next"`
	Replayed bool                              `json:"replayed"`
}

// TaskTransferAssignment validates and executes the semantic assignment
// transfer through protocol.TaskTracker. Storage authority and operation
// identities remain entirely inside the tracker implementation.
func TaskTransferAssignment(ctx context.Context, in TaskTransferAssignmentInput) error {
	return taskTransferAssignment(ctx, in, func(path string) (taskAssignmentTransferStore, error) {
		return tasks.OpenTaskTracker(path)
	})
}

func taskTransferAssignment(ctx context.Context, in TaskTransferAssignmentInput, open taskAssignmentTransferStoreOpener) error {
	if ctx == nil {
		return taskAssignmentTransferValidationError("The task assignment transfer has no execution context.", "The command needs a live context before it can validate or open the task store", "invoke the command through the Pasture CLI", nil)
	}
	if in.Output == nil {
		return taskAssignmentTransferValidationError("The task assignment transfer has no result output stream.", "A successful transfer must emit one semantic JSON result", "provide a writable standard-output stream", nil)
	}
	if open == nil {
		return taskAssignmentTransferValidationError("The task assignment transfer store opener is not configured.", "The command cannot open its production TaskTracker boundary", "wire the command through tasks.OpenTaskTracker", nil)
	}
	if err := ctx.Err(); err != nil {
		return taskAssignmentTransferWorkflowError("The task assignment transfer was cancelled before validation completed.", "The supplied execution context was already cancelled", "retry with a live command context", err)
	}

	request, err := parseTaskAssignmentTransferRequest(in)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return taskAssignmentTransferWorkflowError("The task assignment transfer was cancelled after validation.", "The execution context ended before the task store could be opened", "retry with a live command context", err)
	}

	store, err := open(in.DBPath)
	if err != nil {
		return taskAssignmentTransferStorageError("The task assignment transfer could not open the Pasture store after validating its input.", "The transfer requires the configured unified task store", "verify --db or PASTURE_DB_PATH is writable and retry the command", err)
	}
	result, transferErr := store.TransferTaskAssignment(ctx, request)
	closeErr := store.Close()
	if transferErr != nil {
		return taskAssignmentTransferWorkflowError("The task assignment transfer was rejected.", "The active assignment could not be transferred using the requested semantic successor", "correct the reported assignment state and retry the command", transferErr)
	}
	if closeErr != nil {
		return taskAssignmentTransferStorageError("The task assignment transfer completed but the Pasture store did not close cleanly.", "The database handle reported a close failure after the transfer returned", "inspect the database before retrying; an exact retry will not duplicate the transfer", closeErr)
	}

	wire, err := json.Marshal(taskAssignmentTransferResult{
		Previous: taskAssignmentTransferStateResultFrom(result.Previous),
		Next:     taskAssignmentTransferStateResultFrom(result.Next),
		Replayed: result.Replayed,
	})
	if err != nil {
		return taskAssignmentTransferWorkflowError("The task assignment transfer result could not be encoded as JSON.", "The fixed semantic result schema could not represent the completed transfer", "report the result-schema bug and retry after upgrading Pasture", err)
	}
	return writeEpochCommandResult("task assignment transfer", in.Output, append(wire, '\n'))
}

func parseTaskAssignmentTransferRequest(in TaskTransferAssignmentInput) (protocol.TransferTaskAssignmentRequest, error) {
	taskID, err := parseTaskAssignmentTransferTask(in.TaskID)
	if err != nil {
		return protocol.TransferTaskAssignmentRequest{}, err
	}
	if in.Slot != string(provenance.SlotOwnerResponsibility) {
		return protocol.TransferTaskAssignmentRequest{}, taskAssignmentTransferValidationError(fmt.Sprintf("The %q field has invalid value %q.", "slot", in.Slot), "The task transfer command supports only the owner-responsibility slot", "use --slot owner-responsibility", nil)
	}
	nextAssignment, err := parseTaskAssignmentTransferScalar("assignment", in.NextAssignment)
	if err != nil {
		return protocol.TransferTaskAssignmentRequest{}, err
	}
	actor, err := parseTaskAssignmentTransferActor("actor", in.Actor)
	if err != nil {
		return protocol.TransferTaskAssignmentRequest{}, err
	}
	occupant, err := parseTaskAssignmentTransferActor("occupant", in.Occupant)
	if err != nil {
		return protocol.TransferTaskAssignmentRequest{}, err
	}
	return protocol.TransferTaskAssignmentRequest{
		TaskID:           taskID,
		Slot:             provenance.SlotOwnerResponsibility,
		NextAssignmentID: provenance.AssignmentID(nextAssignment),
		ActorID:          actor,
		NextOccupant:     occupant,
	}, nil
}

func parseTaskAssignmentTransferTask(value string) (provenance.TaskID, error) {
	id, err := provenance.ParseTaskID(value)
	if err != nil || id.String() != value {
		return provenance.TaskID{}, taskAssignmentTransferValidationError(fmt.Sprintf("The %q field has invalid value %q.", "task", value), "The task assignment transfer requires the canonical task identity", "supply the canonical namespace--UUID task identity", err)
	}
	return id, nil
}

func parseTaskAssignmentTransferActor(field, value string) (provenance.ActorID, error) {
	id, err := provenance.ParseActorID(value)
	if err != nil || id.String() != value {
		return provenance.ActorID{}, taskAssignmentTransferValidationError(fmt.Sprintf("The %q field has invalid value %q.", field, value), "The task assignment transfer requires a canonical registered actor identity", "supply the canonical namespace--UUID registered actor identity", err)
	}
	return id, nil
}

func parseTaskAssignmentTransferScalar(field, value string) (string, error) {
	if value == "" || !utf8.ValidString(value) {
		return "", taskAssignmentTransferValidationError(fmt.Sprintf("The %q field is empty.", field), "The task assignment transfer requires a non-empty successor assignment identity", "supply a non-empty assignment value without whitespace", nil)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", taskAssignmentTransferValidationError(fmt.Sprintf("The %q field has invalid value %q.", field, value), "The task assignment transfer requires a canonical assignment identity without whitespace or control characters", "remove whitespace and control characters from the assignment value", nil)
		}
	}
	return value, nil
}

func taskAssignmentTransferStateResultFrom(state protocol.TaskAssignmentState) taskAssignmentTransferStateResult {
	return taskAssignmentTransferStateResult{
		TaskID:       state.TaskID.String(),
		Slot:         string(state.Slot),
		AssignmentID: string(state.AssignmentID),
		Occupant:     state.Occupant.String(),
	}
}

func taskAssignmentTransferValidationError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why + ".",
		Where:    "Validating task assignment transfer (internal/handlers/epoch_task_assignment.go).",
		Impact:   "The Pasture store was not opened and no assignment changed.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}

func taskAssignmentTransferWorkflowError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     what,
		Why:      why + ".",
		Where:    "Executing task assignment transfer (internal/handlers/epoch_task_assignment.go).",
		Impact:   "The assignment transfer did not return a successful semantic result.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}

func taskAssignmentTransferStorageError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     what,
		Why:      why + ".",
		Where:    "Executing task assignment transfer (internal/handlers/epoch_task_assignment.go).",
		Impact:   "The assignment transfer was not confirmed as a complete successful command.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}
