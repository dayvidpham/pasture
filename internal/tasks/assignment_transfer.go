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

	resolution, err := t.resolveTaskAssignmentTransfer(ctx, request.TaskID, request.Slot, replay, request.NextAssignmentID)
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

	// A transfer starts an episode, so the gate must learn about it, and the
	// history must be able to show it later. Neither is free here, because a
	// transfer is not shaped like the other assignment commands: it binds no
	// result slot and writes no event of its own, so the id of the authority it
	// just started is in neither its result nor any material fact.
	//
	// Two things therefore happen after the transfer commits, both still inside
	// the write lock this method holds:
	//   1. the authority id is recovered from the transfer's own rows and the
	//      started-episode record is written, so the next gate sees the new
	//      holder;
	//   2. pasture's own assignment-start fact is written for the new episode,
	//      as A SECOND OPERATION, carrying that id, so a later rebuild of the
	//      record can find this episode at all.
	// Step 2 is a second operation and not part of the transfer, because the
	// transfer is one atomic operation owned by the journal and pasture cannot
	// add an effect to it. If the process stops between the two, the record
	// leads the history by this one transfer, and the next transfer of the same
	// task, or a rebuild after one, closes the gap.
	if err := t.recordTransferredEpisode(ctx, request, operationID); err != nil {
		return protocol.TransferTaskAssignmentResult{}, err
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
// records for this task and picks the one the request is about.
//
// ON A REPLAY it EXCLUDES the record of the episode the transfer CREATED, and
// requires exactly one of the rest. It used to require exactly one record
// outright, which was true only while transfers wrote none: once a transfer
// records its own episode, a task transferred once has two records and every
// later replay would be refused as ambiguous.
//
// The successor is what identifies the replay, because it is the only party to
// the transfer the request names. The request carries the SUCCESSOR
// (NextAssignmentID) and not the predecessor: which episode is being replaced
// is something this resolver works out, and that is the whole reason it exists.
// Removing the successor from the candidates leaves the predecessor, and the
// uniqueness requirement then applies to what remains, so an ambiguous task is
// still refused as ambiguous.
//
// ON A FIRST ATTEMPT it still requires exactly one ACTIVE record, which is the
// real constraint there: two live owner episodes on one task is a conflict, not
// an ambiguity to resolve from the request.
func (t *trackerImpl) resolveTaskAssignmentTransfer(ctx context.Context, taskID provenance.TaskID, slot provenance.AssignmentSlotID, replay bool, successor provenance.AssignmentID) (taskAssignmentResolution, error) {
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
			// WHERE THE GOVERNING AUTHORITY OF THIS RECORD IS.
			//
			// Most assignment-start records are written in the same operation
			// that opened the episode, and the authority sits in the row just
			// below. That inference is what this resolver has always used.
			//
			// A record written for a TRANSFERRED episode breaks it: the transfer
			// opens the episode in its own operation and pasture's record is
			// written afterwards, so the authority is several rows below, not
			// one. Such a record therefore CARRIES its authority id, and it is
			// read here rather than inferred. The inference remains for every
			// record that does not carry one, so nothing already written changes
			// meaning.
			var authority provenance.JournalID
			if started.AuthorityJournalID > 0 {
				authority = provenance.JournalID(started.AuthorityJournalID)
			} else {
				if row.JournalID <= 1 {
					return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferMismatchedAssignment, provenance.ErrAuthorityScope)
				}
				authority = row.JournalID - 1
			}
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
		return selectReplayedTaskAssignmentTransfer(historical, successor)
	}
	if len(active) == 0 && len(ended) != 0 {
		return taskAssignmentResolution{}, taskAssignmentTransferError(protocol.TaskAssignmentTransferStaleAssignment, provenance.ErrStaleEpisode)
	}
	return selectTaskAssignmentTransfer(active, protocol.TaskAssignmentTransferMissingAssignment)
}

// selectReplayedTaskAssignmentTransfer removes the episode the replayed
// transfer CREATED from the candidates and applies the ordinary uniqueness rule
// to the rest. A task with no other record is refused with the same not-found
// answer as before, so a replay of a transfer that never happened is still
// refused rather than answered with some other episode.
func selectReplayedTaskAssignmentTransfer(candidates []taskAssignmentResolution, successor provenance.AssignmentID) (taskAssignmentResolution, error) {
	if successor == "" {
		return selectTaskAssignmentTransfer(candidates, protocol.TaskAssignmentTransferReplayConflict)
	}
	remaining := make([]taskAssignmentResolution, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.assignmentID == successor {
			continue
		}
		remaining = append(remaining, candidate)
	}
	return selectTaskAssignmentTransfer(remaining, protocol.TaskAssignmentTransferReplayConflict)
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

// transferMaterialFactOperationID is the identity of the second operation, the
// one that writes pasture's assignment-start fact for a transferred episode. It
// is DERIVED from the transfer's own operation id, so a retried or replayed
// transfer writes the fact once and not twice.
func transferMaterialFactOperationID(transfer provenance.OperationID) provenance.OperationID {
	return provenance.OperationID(string(transfer) + ".assignment-start")
}

// recordTransferredEpisode writes the started-episode record and pasture's
// material assignment-start fact for the episode a transfer just started.
//
// It runs after the transfer commits and INSIDE the write lock the caller
// holds. The lock is what makes the authority recovery sound: no other pasture
// writer can commit between the transfer and the journal maximum read here, so
// that maximum is the transfer operation's own last row.
func (t *trackerImpl) recordTransferredEpisode(
	ctx context.Context,
	request protocol.TransferTaskAssignmentRequest,
	operationID provenance.OperationID,
) error {
	committed, err := t.prov.Journal().LookupCommitted(operationID)
	if err != nil {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
	}
	page, err := t.prov.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, Limit: 1})
	if err != nil {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
	}
	authority, err := transferAssignmentAuthority(t.prov.Journal(), committed.AnchorJournalID, page.SnapshotMaxJournalID, request.TaskID)
	if err != nil {
		return err
	}

	if err := recordAssignmentStart(ctx, t.auditDB, startedEpisode{
		Assignment: request.NextAssignmentID,
		Actor:      request.NextOccupant,
		Task:       request.TaskID,
		Role:       RoleOwnerResponsibility,
		Authority:  authority,
	}); err != nil {
		return err
	}

	payload, err := canonicalJSON(assignmentStartPayload{
		Assignment:         string(request.NextAssignmentID),
		Role:               RoleOwnerResponsibility.String(),
		Occupant:           request.NextOccupant.String(),
		AuthorityJournalID: int64(authority),
	})
	if err != nil {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
	}
	event, err := epochTaskEvent(request.TaskID, FamilyAssignmentStarted.EventKind(), payload)
	if err != nil {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
	}
	event.ResultSlot = "event"
	if _, err := t.prov.As(request.NextOccupant, authority).Atomic(func(operation *provenance.Operation) {
		operation.Add(event)
	}, provenance.WithOperationID(transferMaterialFactOperationID(operationID))); err != nil {
		return taskAssignmentTransferError(protocol.TaskAssignmentTransferUnavailable, err)
	}
	return nil
}
