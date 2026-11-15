package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// StartTaskAssignment starts the one owner-responsibility assignment for an
// existing task. It refuses a task that already has an active owner and never
// substitutes an owner label for an assignment. The episode start and its
// material fact are committed in one Provenance operation under the persisted
// genesis authority; the operation id is derived from every semantic field so
// an exact retry replays and a changed request conflicts.
//
// The store's write gate serializes starts inside this process. The released
// Provenance surface has no "no active assignment" precondition, so a second
// supervisor process that races the pre-check is not defended here; run one
// supervisor process per store and stop on an ambiguous show result.
func (t *trackerImpl) StartTaskAssignment(ctx context.Context, request protocol.StartTaskAssignmentRequest) (protocol.StartTaskAssignmentResult, error) {
	fail := func(kind protocol.TaskAssignmentStartErrorKind, cause error) (protocol.StartTaskAssignmentResult, error) {
		return protocol.StartTaskAssignmentResult{}, protocol.NewTaskAssignmentStartError(kind, cause)
	}
	if ctx == nil {
		return fail(protocol.TaskAssignmentStartInvalidRequest, nil)
	}
	if err := ctx.Err(); err != nil {
		return fail(protocol.TaskAssignmentStartUnavailable, err)
	}
	if request.Slot != provenance.SlotOwnerResponsibility {
		return fail(protocol.TaskAssignmentStartUnsupportedSlot, nil)
	}
	if _, err := provenance.ParseTaskID(request.TaskID.String()); err != nil {
		return fail(protocol.TaskAssignmentStartInvalidRequest, err)
	}
	if _, err := provenance.ParseActorID(request.ActorID.String()); err != nil {
		return fail(protocol.TaskAssignmentStartInvalidRequest, err)
	}
	if _, err := provenance.ParseActorID(request.Occupant.String()); err != nil {
		return fail(protocol.TaskAssignmentStartInvalidRequest, err)
	}
	if err := validateAssignmentScalar("assignment", string(request.AssignmentID)); err != nil {
		return fail(protocol.TaskAssignmentStartInvalidRequest, err)
	}
	hasParentTask := request.ParentTask != (provenance.TaskID{})
	if hasParentTask != (request.ParentAssignment != "") {
		return fail(protocol.TaskAssignmentStartInvalidRequest, fmt.Errorf("parent task and parent assignment must be supplied together"))
	}
	if hasParentTask {
		if _, err := provenance.ParseTaskID(request.ParentTask.String()); err != nil {
			return fail(protocol.TaskAssignmentStartInvalidRequest, err)
		}
		if err := validateAssignmentScalar("parent-assignment", string(request.ParentAssignment)); err != nil {
			return fail(protocol.TaskAssignmentStartInvalidRequest, err)
		}
		if request.ParentAssignment == request.AssignmentID {
			return fail(protocol.TaskAssignmentStartParentMismatch, fmt.Errorf("an assignment cannot cite itself as its parent"))
		}
	}

	unlock, err := t.lockWriteContext(ctx)
	if err != nil {
		return fail(protocol.TaskAssignmentStartUnavailable, err)
	}
	defer unlock()

	material, err := MapMaterialEvent(AssignmentStartedEvent{
		Task:       request.TaskID,
		Assignment: request.AssignmentID,
		Role:       RoleOwnerResponsibility,
		Occupant:   request.Occupant,
	})
	if err != nil {
		return fail(protocol.TaskAssignmentStartInvalidRequest, err)
	}
	start := provenance.Effect{
		Sort:         provenance.EffectAssignmentStart,
		TaskID:       request.TaskID,
		AssignmentID: request.AssignmentID,
		SlotID:       request.Slot,
		Occupant:     request.Occupant,
		Parent:       request.ParentAssignment,
	}
	operationID := startAssignmentOperationID(request)

	committed, err := t.prov.Journal().LookupCommitted(operationID)
	if err != nil {
		return fail(protocol.TaskAssignmentStartUnavailable, err)
	}
	replay := false
	switch committed.Kind {
	case provenance.CommittedAbsent:
	case provenance.CommittedExact:
		// Exact replay must not depend on current liveness. The operation id names
		// every semantic field, so a changed request cannot reach this branch.
		replay = true
	default:
		return fail(protocol.TaskAssignmentStartReplayConflict, provenance.ErrOperationConflict)
	}

	if !replay {
		task, err := t.prov.Show(request.TaskID)
		if err != nil {
			if errors.Is(err, provenance.ErrNotFound) {
				return fail(protocol.TaskAssignmentStartMissingTask, err)
			}
			return fail(protocol.TaskAssignmentStartUnavailable, err)
		}
		if task.Status == provenance.StatusClosed {
			return fail(protocol.TaskAssignmentStartClosedTask, nil)
		}
		for _, actor := range []provenance.ActorID{request.ActorID, request.Occupant} {
			if _, err := t.prov.Agent(actor); err != nil {
				return fail(protocol.TaskAssignmentStartUnregisteredActor, err)
			}
		}
		activeID, active, err := t.activeTaskAssignment(ctx, request.TaskID, request.Slot)
		if err != nil {
			return fail(protocol.TaskAssignmentStartUnavailable, err)
		}
		if active {
			return fail(protocol.TaskAssignmentStartAlreadyAssigned, fmt.Errorf("task %q already has active owner assignment %q; transfer it instead", request.TaskID, activeID))
		}
		used, err := t.assignmentIDUsed(request.AssignmentID)
		if err != nil {
			return fail(protocol.TaskAssignmentStartUnavailable, err)
		}
		if used {
			return fail(protocol.TaskAssignmentStartAssignmentIDReused, fmt.Errorf("assignment id %q was already used by this store; choose a fresh id", request.AssignmentID))
		}
		if hasParentTask {
			ok, err := t.parentAssignmentActive(request.ParentTask, request.ParentAssignment)
			if err != nil {
				return fail(protocol.TaskAssignmentStartUnavailable, err)
			}
			if !ok {
				return fail(protocol.TaskAssignmentStartParentMismatch, fmt.Errorf("parent assignment %q is not the active owner of parent task %q", request.ParentAssignment, request.ParentTask))
			}
		}
	}

	systemActor, authority, found, err := readSystemIdentity(t.auditDB)
	if err != nil {
		return fail(protocol.TaskAssignmentStartUnavailable, err)
	}
	if !found {
		return fail(protocol.TaskAssignmentStartUnavailable, fmt.Errorf("the persisted system identity is absent; initialize the store with a normal task command"))
	}
	if err := validatePersistedGenesisAuthority(t.prov.Journal(), systemActor, authority); err != nil {
		return fail(protocol.TaskAssignmentStartUnavailable, err)
	}

	result, err := t.prov.As(request.ActorID, authority).Atomic(func(operation *provenance.Operation) {
		operation.Add(start)
		operation.Add(material)
	}, provenance.WithOperationID(operationID))
	if err != nil {
		switch {
		case errors.Is(err, provenance.ErrOperationConflict):
			return fail(protocol.TaskAssignmentStartReplayConflict, err)
		case errors.Is(err, provenance.ErrParentCitation):
			return fail(protocol.TaskAssignmentStartParentMismatch, err)
		default:
			return fail(protocol.TaskAssignmentStartUnavailable, err)
		}
	}
	return protocol.StartTaskAssignmentResult{
		Assignment: protocol.TaskAssignmentState{
			TaskID:       request.TaskID,
			Slot:         request.Slot,
			AssignmentID: request.AssignmentID,
			Occupant:     request.Occupant,
		},
		Replayed: result.ShortCircuited,
	}, nil
}

// activeTaskAssignment uses the same resolver as transfer and show. A missing
// or stale result means no episode is active; ambiguity, a mismatched authority,
// or a store failure is reported separately so start never mislabels it as a
// second owner.
func (t *trackerImpl) activeTaskAssignment(ctx context.Context, task provenance.TaskID, slot provenance.AssignmentSlotID) (provenance.AssignmentID, bool, error) {
	resolution, err := t.resolveTaskAssignmentTransfer(ctx, task, slot, false, "")
	if err == nil {
		return resolution.assignmentID, true, nil
	}
	var transferErr *protocol.TaskAssignmentTransferError
	if errors.As(err, &transferErr) && (transferErr.Kind == protocol.TaskAssignmentTransferMissingAssignment || transferErr.Kind == protocol.TaskAssignmentTransferStaleAssignment) {
		return "", false, nil
	}
	return "", false, err
}

// assignmentIDUsed prevents an ended episode id from being reissued for a new
// episode. Assignment ids are durable identity, not a reusable label.
func (t *trackerImpl) assignmentIDUsed(assignment provenance.AssignmentID) (bool, error) {
	rows, err := t.queryAssignmentStarts(provenance.AssignmentStartQuery{
		AssignmentIDs: []provenance.AssignmentID{assignment},
	})
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

// parentAssignmentActive proves that the cited parent episode belongs to the
// cited task and is active now. Provenance rechecks both conditions inside the
// commit transaction.
func (t *trackerImpl) parentAssignmentActive(task provenance.TaskID, assignment provenance.AssignmentID) (bool, error) {
	rows, err := t.queryAssignmentStarts(provenance.AssignmentStartQuery{
		AssignmentIDs: []provenance.AssignmentID{assignment},
		TaskIDs:       []provenance.TaskID{task},
	})
	if err != nil {
		return false, err
	}
	if len(rows) != 1 || rows[0].AuthorityJournalID <= 0 {
		return false, nil
	}
	return t.prov.Journal().AuthorityGovernsTaskAt(rows[0].AuthorityJournalID, task, provenance.JournalID(math.MaxInt64))
}

// queryAssignmentStarts drains a pinned assignment-start query. A filtered page
// only examines Page.Limit candidates, so callers that need an exact match must
// follow Next instead of asking for one row.
func (t *trackerImpl) queryAssignmentStarts(query provenance.AssignmentStartQuery) ([]provenance.AssignmentStartRow, error) {
	api, ok := t.prov.Journal().(provenance.AssignmentStartQueryAPI)
	if !ok {
		return nil, fmt.Errorf("assignment start query capability is unavailable")
	}
	query.Page = provenance.AssignmentStartPageRequest{Limit: provenance.MaxFactPageSize}
	var rows []provenance.AssignmentStartRow
	for {
		page, err := api.QueryAssignmentStarts(query)
		if err != nil {
			return nil, err
		}
		rows = append(rows, page.Rows...)
		if page.Next == nil {
			return rows, nil
		}
		query.Page.SnapshotPinned = true
		query.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
}

// startAssignmentOperationID names the full semantic start so an exact retry
// replays and any changed field conflicts. Callers never supply storage ids.
func startAssignmentOperationID(request protocol.StartTaskAssignmentRequest) provenance.OperationID {
	hash := sha256.New()
	for _, field := range []string{
		"pasture.task-assignment-start.v1",
		request.TaskID.String(),
		string(request.Slot),
		string(request.AssignmentID),
		request.ActorID.String(),
		request.Occupant.String(),
		request.ParentTask.String(),
		string(request.ParentAssignment),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(field))
	}
	return provenance.OperationID(fmt.Sprintf("pasture.assignment-start.%x", hash.Sum(nil)))
}

func validateAssignmentScalar(field, value string) error {
	if value == "" || !utf8.ValidString(value) {
		return fmt.Errorf("%s is empty or not valid UTF-8", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("%s contains whitespace or control characters", field)
		}
	}
	if err := provenance.ValidateOperationID(provenance.OperationID(value)); err != nil {
		return fmt.Errorf("%s is not a valid assignment id: %w", field, err)
	}
	return nil
}
