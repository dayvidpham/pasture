package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// ShowTaskAssignment resolves the one active owner-responsibility assignment
// for a task. It is the read side of the handoff contract: a worker can verify
// the exact assignment and occupant before touching code.
func (t *trackerImpl) ShowTaskAssignment(ctx context.Context, task provenance.TaskID) (protocol.TaskAssignmentState, error) {
	if ctx == nil {
		return protocol.TaskAssignmentState{}, fmt.Errorf("show assignment: no execution context; invoke it through the CLI")
	}
	if err := ctx.Err(); err != nil {
		return protocol.TaskAssignmentState{}, err
	}
	if _, err := provenance.ParseTaskID(task.String()); err != nil {
		return protocol.TaskAssignmentState{}, fmt.Errorf("show assignment: invalid task id: %w", err)
	}
	if _, err := t.prov.Show(task); err != nil {
		if errors.Is(err, provenance.ErrNotFound) {
			return protocol.TaskAssignmentState{}, fmt.Errorf("show assignment: task %q does not exist", task)
		}
		return protocol.TaskAssignmentState{}, fmt.Errorf("show assignment: read task %q: %w", task, err)
	}
	resolution, err := t.resolveTaskAssignmentTransfer(ctx, task, provenance.SlotOwnerResponsibility, false, "")
	if err != nil {
		var transferErr *protocol.TaskAssignmentTransferError
		if errors.As(err, &transferErr) {
			switch transferErr.Kind {
			case protocol.TaskAssignmentTransferMissingAssignment, protocol.TaskAssignmentTransferStaleAssignment:
				activeID, _, active, activeErr := t.activeSlotAssignment(task, provenance.SlotOwnerResponsibility)
				if activeErr != nil {
					return protocol.TaskAssignmentState{}, activeErr
				}
				if active {
					return protocol.TaskAssignmentState{}, fmt.Errorf("task %q has active assignment %q on the owner-responsibility slot, but it is not an owner-responsibility role; inspect it with the epoch assignment command or end it with the exact id", task, activeID)
				}
				return protocol.TaskAssignmentState{}, fmt.Errorf("task %q has no active owner-responsibility assignment; start one with task assignment start or inspect the task history", task)
			case protocol.TaskAssignmentTransferAmbiguousAssignment:
				return protocol.TaskAssignmentState{}, fmt.Errorf("task %q has multiple active owner-responsibility assignments; repair the duplicate assignment records", task)
			case protocol.TaskAssignmentTransferMismatchedAssignment:
				return protocol.TaskAssignmentState{}, fmt.Errorf("task %q has an inconsistent owner-responsibility assignment record; repair it before retrying", task)
			}
		}
		return protocol.TaskAssignmentState{}, err
	}
	return protocol.TaskAssignmentState{
		TaskID:       task,
		Slot:         provenance.SlotOwnerResponsibility,
		AssignmentID: resolution.assignmentID,
		Occupant:     resolution.occupant,
	}, nil
}

// EndTaskAssignment ends the exact active owner-responsibility episode. It
// refuses a stale or mismatched assignment id so an operator cannot silently
// remove a different worker's ownership.
func (t *trackerImpl) EndTaskAssignment(ctx context.Context, request protocol.EndTaskAssignmentRequest) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("end assignment: no execution context; invoke it through the CLI")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := provenance.ParseTaskID(request.TaskID.String()); err != nil {
		return false, fmt.Errorf("end assignment: invalid task id: %w", err)
	}
	if _, err := provenance.ParseActorID(request.ActorID.String()); err != nil {
		return false, fmt.Errorf("end assignment: invalid actor id: %w", err)
	}
	if err := validateAssignmentScalar("assignment", string(request.AssignmentID)); err != nil {
		return false, fmt.Errorf("end assignment: %w", err)
	}

	unlock, err := t.lockWriteContext(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()

	end := provenance.Effect{
		Sort:         provenance.EffectAssignmentEnd,
		TaskID:       request.TaskID,
		AssignmentID: request.AssignmentID,
		SlotID:       provenance.SlotOwnerResponsibility,
	}
	operationID := endAssignmentOperationID(request)

	authority, err := t.systemAuthority()
	if err != nil {
		return false, err
	}

	committed, err := t.prov.Journal().LookupCommitted(operationID)
	if err != nil {
		return false, fmt.Errorf("end assignment: inspect the operation journal: %w", err)
	}
	replay := false
	switch committed.Kind {
	case provenance.CommittedAbsent:
	case provenance.CommittedExact:
		replay = true
	default:
		return false, fmt.Errorf("end assignment: operation id %q already committed differently; resubmit the exact original request", operationID)
	}

	if !replay {
		if _, err := t.prov.Agent(request.ActorID); err != nil {
			return false, fmt.Errorf("end assignment: actor %s is not registered: %w", request.ActorID, err)
		}
		resolution, err := t.resolveTaskAssignmentTransfer(ctx, request.TaskID, provenance.SlotOwnerResponsibility, false, "")
		if err != nil {
			return false, fmt.Errorf("end assignment: resolve the active owner: %w", err)
		}
		if resolution.assignmentID != request.AssignmentID {
			return false, fmt.Errorf("end assignment: task %q is owned by assignment %q, not %q; show the current assignment and retry with that id", request.TaskID, resolution.assignmentID, request.AssignmentID)
		}
	}

	result, err := t.prov.As(request.ActorID, authority).Atomic(func(operation *provenance.Operation) {
		operation.Add(end)
	}, provenance.WithOperationID(operationID))
	if err != nil {
		if errors.Is(err, provenance.ErrOperationConflict) {
			return false, fmt.Errorf("end assignment: operation id %q already committed differently; resubmit the exact original request", operationID)
		}
		return false, fmt.Errorf("end assignment: %w; verify the exact active assignment and retry", err)
	}
	return result.ShortCircuited, nil
}

func endAssignmentOperationID(request protocol.EndTaskAssignmentRequest) provenance.OperationID {
	hash := sha256.New()
	for _, field := range []string{
		"pasture.task-assignment-end.v1",
		request.TaskID.String(),
		string(request.AssignmentID),
		request.ActorID.String(),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(field))
	}
	return provenance.OperationID(fmt.Sprintf("pasture.assignment-end.%x", hash.Sum(nil)))
}
