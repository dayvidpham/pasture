package protocol

import "github.com/dayvidpham/provenance"

// TransferTaskAssignmentRequest describes a semantic task-assignment transfer.
// It intentionally contains no persistence identity or execution mechanism.
type TransferTaskAssignmentRequest struct {
	TaskID           provenance.TaskID
	Slot             provenance.AssignmentSlotID
	NextAssignmentID provenance.AssignmentID
	ActorID          provenance.ActorID
	NextOccupant     provenance.ActorID
}

// TaskAssignmentState is the user-meaningful state of one task assignment.
type TaskAssignmentState struct {
	TaskID       provenance.TaskID
	Slot         provenance.AssignmentSlotID
	AssignmentID provenance.AssignmentID
	Occupant     provenance.ActorID
}

// TransferTaskAssignmentResult reports the assignment state before and after a
// transfer. Replayed is true when the same semantic request was already applied.
type TransferTaskAssignmentResult struct {
	Previous TaskAssignmentState
	Next     TaskAssignmentState
	Replayed bool
}

// TaskAssignmentTransferErrorKind classifies semantic transfer failures without
// exposing persistence or execution details.
type TaskAssignmentTransferErrorKind uint8

const (
	TaskAssignmentTransferInvalidRequest TaskAssignmentTransferErrorKind = iota + 1
	TaskAssignmentTransferUnsupportedSlot
	TaskAssignmentTransferMissingAssignment
	TaskAssignmentTransferAmbiguousAssignment
	TaskAssignmentTransferMismatchedAssignment
	TaskAssignmentTransferStaleAssignment
	TaskAssignmentTransferReplayConflict
	TaskAssignmentTransferUnavailable
)

// String returns the stable diagnostic name for a transfer error kind.
func (k TaskAssignmentTransferErrorKind) String() string {
	switch k {
	case TaskAssignmentTransferInvalidRequest:
		return "invalid request"
	case TaskAssignmentTransferUnsupportedSlot:
		return "unsupported slot"
	case TaskAssignmentTransferMissingAssignment:
		return "missing assignment"
	case TaskAssignmentTransferAmbiguousAssignment:
		return "ambiguous assignment"
	case TaskAssignmentTransferMismatchedAssignment:
		return "mismatched assignment"
	case TaskAssignmentTransferStaleAssignment:
		return "stale assignment"
	case TaskAssignmentTransferReplayConflict:
		return "replay conflict"
	case TaskAssignmentTransferUnavailable:
		return "transfer unavailable"
	default:
		return "unknown transfer error"
	}
}

// TaskAssignmentTransferError is an actionable, typed transfer failure. Cause
// remains available through errors.Is and errors.As without becoming part of the
// semantic TaskTracker API.
type TaskAssignmentTransferError struct {
	Kind  TaskAssignmentTransferErrorKind
	cause error
}

// NewTaskAssignmentTransferError constructs a typed semantic transfer failure.
// It is exported for TaskTracker implementations outside this package.
func NewTaskAssignmentTransferError(kind TaskAssignmentTransferErrorKind, cause error) *TaskAssignmentTransferError {
	return &TaskAssignmentTransferError{Kind: kind, cause: cause}
}

// Unwrap preserves the underlying error classification for errors.Is and errors.As.
func (e *TaskAssignmentTransferError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Error explains what failed, why no assignment changed, and how to recover.
func (e *TaskAssignmentTransferError) Error() string {
	if e == nil {
		return "task assignment transfer failed"
	}
	switch e.Kind {
	case TaskAssignmentTransferInvalidRequest:
		return "task assignment transfer rejected an invalid request; where: TaskTracker.TransferTaskAssignment; when: before selection; impact: no assignment changed; fix: provide complete task, actor, occupant, and successor assignment values"
	case TaskAssignmentTransferUnsupportedSlot:
		return "task assignment transfer rejected an unsupported slot; where: TaskTracker.TransferTaskAssignment; when: before selection; impact: no assignment changed; fix: use the owner-responsibility slot"
	case TaskAssignmentTransferMissingAssignment:
		return "task assignment transfer found no active assignment for the task; where: TaskTracker.TransferTaskAssignment; when: selecting the current assignment; impact: no assignment changed; fix: start the task's owner-responsibility assignment before retrying"
	case TaskAssignmentTransferAmbiguousAssignment:
		return "task assignment transfer found more than one matching assignment; where: TaskTracker.TransferTaskAssignment; when: selecting the current assignment; impact: no assignment changed; fix: repair the duplicate assignment records before retrying"
	case TaskAssignmentTransferMismatchedAssignment:
		return "task assignment transfer found an assignment record that does not belong to the requested task; where: TaskTracker.TransferTaskAssignment; when: validating the selected assignment; impact: no assignment changed; fix: repair the task assignment record and retry"
	case TaskAssignmentTransferStaleAssignment:
		return "task assignment transfer lost the current assignment before it could be changed; where: TaskTracker.TransferTaskAssignment; when: committing the transfer; impact: no assignment changed; fix: resolve the task's current assignment and retry with a new successor"
	case TaskAssignmentTransferReplayConflict:
		return "task assignment transfer could not replay the requested change consistently; where: TaskTracker.TransferTaskAssignment; when: validating a prior transfer; impact: no assignment changed; fix: retry the exact original request or repair the inconsistent assignment record"
	case TaskAssignmentTransferUnavailable:
		return "task assignment transfer could not read or apply the task assignment; where: TaskTracker.TransferTaskAssignment; when: reading or committing the transfer; impact: no assignment changed; fix: verify the task store is available and retry"
	default:
		return "task assignment transfer failed with an unknown semantic error; where: TaskTracker.TransferTaskAssignment; impact: no assignment changed; fix: retry after verifying the task assignment"
	}
}
