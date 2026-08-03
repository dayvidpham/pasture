package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

type taskAssignmentResolution struct {
	assignmentID provenance.AssignmentID
	occupant     provenance.ActorID
	authority    provenance.JournalID
}

// TransferTaskAssignment transfers the one active v1 owner-responsibility
// assignment selected from Pasture's material assignment-start history. The
// underlying Provenance primitive owns transfer authorization, replay admission,
// and the transaction-local successor lease.
func (t *trackerImpl) TransferTaskAssignment(ctx context.Context, request protocol.TransferTaskAssignmentRequest) (protocol.TransferTaskAssignmentResult, error) {
	if ctx == nil {
		return protocol.TransferTaskAssignmentResult{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferInvalidRequest, nil)
	}
	defer t.lockWrite()()

	operationID := taskAssignmentTransferOperationID(request)
	committed, err := t.prov.Journal().LookupCommitted(operationID)
	if err != nil {
		return protocol.TransferTaskAssignmentResult{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
	}
	replay := false
	switch committed.Kind {
	case provenance.CommittedAbsent:
	case provenance.CommittedExact:
		replay = true
	default:
		return protocol.TransferTaskAssignmentResult{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferReplayConflict, provenance.ErrOperationConflict)
	}

	if err := ctx.Err(); err != nil {
		return protocol.TransferTaskAssignmentResult{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
	}
	if err := validateTaskAssignmentTransferRequest(request); err != nil {
		return protocol.TransferTaskAssignmentResult{}, err
	}

	resolution, err := t.resolveTaskAssignmentTransfer(ctx, request.TaskID, request.Slot, replay)
	if err != nil {
		return protocol.TransferTaskAssignmentResult{}, err
	}
	if request.NextAssignmentID == resolution.assignmentID {
		return protocol.TransferTaskAssignmentResult{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferInvalidRequest, provenance.ErrCanonicalMutation)
	}

	transferred, err := t.prov.As(request.ActorID, resolution.authority).TransferAssignment(provenance.AssignmentTransferRequest{
		TaskID:               request.TaskID,
		SlotID:               request.Slot,
		PreviousAssignmentID: resolution.assignmentID,
		NextAssignmentID:     request.NextAssignmentID,
		NextOccupant:         request.NextOccupant,
	}, provenance.WithOperationID(operationID))
	if err != nil {
		return protocol.TransferTaskAssignmentResult{}, classifyTaskAssignmentTransferError(err)
	}
	if replay && !transferred.Replayed {
		return protocol.TransferTaskAssignmentResult{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferReplayConflict, provenance.ErrOperationConflict)
	}

	return protocol.TransferTaskAssignmentResult{
		Previous: protocol.TaskAssignmentState{
			TaskID:       request.TaskID,
			Slot:         request.Slot,
			AssignmentID: transferred.PreviousAssignmentID,
			Occupant:     resolution.occupant,
		},
		Next: protocol.TaskAssignmentState{
			TaskID:       request.TaskID,
			Slot:         request.Slot,
			AssignmentID: transferred.NextAssignmentID,
			Occupant:     transferred.NextOccupant,
		},
		Replayed: transferred.Replayed,
	}, nil
}

func validateTaskAssignmentTransferRequest(request protocol.TransferTaskAssignmentRequest) error {
	if _, err := provenance.ParseTaskID(request.TaskID.String()); err != nil {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferInvalidRequest, err)
	}
	if request.Slot != provenance.SlotOwnerResponsibility {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferUnsupportedSlot, nil)
	}
	if request.NextAssignmentID == "" {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferInvalidRequest, provenance.ErrCanonicalMutation)
	}
	if _, err := provenance.ParseActorID(request.ActorID.String()); err != nil {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferInvalidRequest, err)
	}
	if _, err := provenance.ParseActorID(request.NextOccupant.String()); err != nil {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferInvalidRequest, err)
	}
	return nil
}

// resolveTaskAssignmentTransfer reads only the owner-responsibility material
// records for this task. A valid v1 record is paired with the immediately
// preceding assignment-start authority; no general authority lookup is exposed.
func (t *trackerImpl) resolveTaskAssignmentTransfer(ctx context.Context, taskID provenance.TaskID, slot provenance.AssignmentSlotID, replay bool) (taskAssignmentResolution, error) {
	if slot != provenance.SlotOwnerResponsibility {
		return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferUnsupportedSlot, nil)
	}
	query := provenance.JournalQueryV1{
		OrderBy:    provenance.OrderByJournalID,
		TaskIDs:    []provenance.TaskID{taskID},
		EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()},
		Limit:      provenance.MaxFactPageSize,
	}
	var active, ended, historical []taskAssignmentResolution

	for {
		if err := ctx.Err(); err != nil {
			return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
		}
		page, err := t.prov.Journal().QueryTaskEvents(query)
		if err != nil {
			return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
		}
		for _, row := range page.Events {
			if row.TaskID != taskID {
				return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferMismatchedAssignment, provenance.ErrAuthorityScope)
			}
			started, err := decodeAssignmentStart(row.Payload)
			if err != nil {
				return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferMismatchedAssignment, err)
			}
			if started.Role != RoleOwnerResponsibility.String() {
				continue
			}
			occupant, err := provenance.ParseActorID(started.Occupant)
			if err != nil || occupant == (provenance.ActorID{}) {
				return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferMismatchedAssignment, err)
			}
			if row.JournalID <= 1 {
				return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferMismatchedAssignment, provenance.ErrAuthorityScope)
			}
			authority := row.JournalID - 1
			direct, err := t.prov.Journal().AuthorityGovernsTaskAt(authority, taskID, row.JournalID)
			if err != nil {
				return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
			}
			if !direct {
				return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferMismatchedAssignment, provenance.ErrAuthorityScope)
			}
			resolution := taskAssignmentResolution{
				assignmentID: provenance.AssignmentID(started.Assignment),
				occupant:     occupant,
				authority:    authority,
			}
			if replay {
				// Replay must not be gated on current assignment liveness. The
				// historic pairing is enough to bind the original predecessor;
				// Provenance then performs exact replay admission before liveness.
				historical = append(historical, resolution)
				continue
			}
			isActive, err := t.prov.Journal().AuthorityGovernsTaskAt(authority, taskID, provenance.JournalID(math.MaxInt64))
			if err != nil {
				return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
			}
			if isActive {
				active = append(active, resolution)
			} else {
				ended = append(ended, resolution)
			}
		}
		if page.Next == nil {
			break
		}
		query.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.AfterJournalID = page.Next.AfterJournalID
	}

	if replay {
		return selectTaskAssignmentTransfer(historical, protocol.TaskAssignmentTransferReplayConflict)
	}
	if len(active) == 0 && len(ended) != 0 {
		return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferStaleAssignment, provenance.ErrStaleEpisode)
	}
	return selectTaskAssignmentTransfer(active, protocol.TaskAssignmentTransferMissingAssignment)
}

func selectTaskAssignmentTransfer(candidates []taskAssignmentResolution, missingKind protocol.TaskAssignmentTransferErrorKind) (taskAssignmentResolution, error) {
	switch len(candidates) {
	case 0:
		return taskAssignmentResolution{}, taskAssignmentTransferError(missingKind, nil)
	case 1:
		return candidates[0], nil
	default:
		return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferAmbiguousAssignment, nil)
	}
}

func classifyTaskAssignmentTransferError(err error) error {
	switch {
	case errors.Is(err, provenance.ErrStaleEpisode):
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferStaleAssignment, err)
	case errors.Is(err, provenance.ErrAuthorityScope), errors.Is(err, provenance.ErrAssignmentLifecycle), errors.Is(err, provenance.ErrOrphanedEvidence):
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferMismatchedAssignment, err)
	case errors.Is(err, provenance.ErrCanonicalMutation):
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferInvalidRequest, err)
	case errors.Is(err, provenance.ErrOperationConflict):
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferReplayConflict, err)
	default:
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
	}
}

func taskAssignmentTransferError(kind protocol.TaskAssignmentTransferErrorKind, cause error) error {
	return protocol.NewTaskAssignmentTransferError(kind, cause)
}

// taskAssignmentTransferOperationID is deliberately private: callers retry by
// resubmitting the same semantic request rather than supplying storage identity.
func taskAssignmentTransferOperationID(request protocol.TransferTaskAssignmentRequest) provenance.OperationID {
	hash := sha256.New()
	for _, field := range []string{
		"pasture.task-assignment-transfer.v1",
		request.TaskID.String(),
		string(request.Slot),
		string(request.NextAssignmentID),
		request.ActorID.String(),
		request.NextOccupant.String(),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(field))
	}
	return provenance.OperationID("pasture.task-assignment-transfer." + hex.EncodeToString(hash.Sum(nil)))
}

var _ interface {
	TransferTaskAssignment(context.Context, protocol.TransferTaskAssignmentRequest) (protocol.TransferTaskAssignmentResult, error)
} = (*trackerImpl)(nil)
