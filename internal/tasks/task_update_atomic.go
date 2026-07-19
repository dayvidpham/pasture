// Package tasks — atomic metadata+status task update (#43).
//
// task_update_atomic.go owns the combined `pasture task update` write path. The
// journaled backend splits a task's METADATA (title/description/priority/phase/notes,
// one provenance.task.updated event) from its STATUS lifecycle (the dedicated FSM verbs
// Start/Stop/CloseTask/Reopen). A CLI `update` call that changes both must not leave the
// metadata half committed when the status half is FSM-illegal, so this file folds the
// metadata effect and the mapped lifecycle transition into ONE provenance Session.Atomic
// operation: the whole operation commits or nothing does. A metadata-only or status-only
// request routes through the same single journaled verb it always used.

package tasks

import (
	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// AtomicTaskUpdater is the capability the journaled task tracker exposes for a combined
// metadata+status `pasture task update`. OpenTaskTracker's concrete tracker satisfies it;
// the CLI handler type-asserts the returned protocol.TaskTracker to drive the update so a
// rejected (FSM-illegal) status change also rolls back the metadata write.
type AtomicTaskUpdater interface {
	// UpdateTaskAtomic applies metadata fields and an optional target status to a task
	// as one all-or-none journaled operation. See the method for full semantics.
	UpdateTaskAtomic(id provenance.TaskID, fields provenance.UpdateFields, targetStatus *provenance.Status) (provenance.Task, error)
}

// UpdateTaskAtomic applies the metadata fields (title/description/priority/phase/notes)
// and an optional status transition to a task, preserving the pre-journal single-command
// atomicity of `pasture task update`.
//
// When BOTH a metadata change and a status transition are requested they fold into ONE
// provenance Session.Atomic operation — metadata first, then the mapped lifecycle
// transition — so an FSM-illegal status target rejects the WHOLE operation and NOTHING
// commits (the metadata half is never left applied). A metadata-only request commits one
// provenance.task.updated event; a status-only request commits one lifecycle transition
// through its dedicated verb; a request that changes nothing returns the current task
// without writing.
//
// The target status is mapped onto the journaled lifecycle transition that reaches it
// from the task's current status (open→in_progress = started, in_progress→open = stopped,
// closed→open = reopened, {open,in_progress}→closed = closed); a target equal to the
// current status is a status no-op. The static FSM in the journal reducer is the single
// authority that accepts or rejects the transition, surfacing the typed
// ErrStatusTransition unchanged on an illegal one.
//
// The whole call holds the cross-connection write mutex once, so the metadata and status
// effects of a combined update never interleave with an unrelated concurrent writer.
func (t *trackerImpl) UpdateTaskAtomic(id provenance.TaskID, fields provenance.UpdateFields, targetStatus *provenance.Status) (provenance.Task, error) {
	defer t.lockWrite()()
	s, err := t.systemSession()
	if err != nil {
		return provenance.Task{}, err
	}

	current, err := t.prov.Show(id)
	if err != nil {
		return provenance.Task{}, err
	}

	metaEffect, hasMeta := metadataUpdateEffect(id, fields)

	kind, hasLifecycle, err := lifecycleKindFor(current.Status, targetStatus)
	if err != nil {
		return provenance.Task{}, err
	}

	switch {
	case hasMeta && hasLifecycle:
		// Fold both into one atomic operation: the metadata task.updated effect folds
		// first, then the lifecycle transition. If the FSM rejects the transition the
		// whole Apply fails and the metadata effect is rolled back with it — nothing
		// commits, so an errored combined update never leaves a partial write.
		lifecycle := lifecycleEffect(id, kind)
		if _, err := s.Atomic(func(op *provenance.Operation) {
			op.Add(metaEffect)
			op.Add(lifecycle)
		}); err != nil {
			return provenance.Task{}, err
		}
	case hasMeta:
		if _, err := s.Update(id, fields); err != nil {
			return provenance.Task{}, err
		}
	case hasLifecycle:
		if err := applyLifecycleVerb(s, id, kind); err != nil {
			return provenance.Task{}, err
		}
	default:
		// Neither metadata nor a status change was requested (or the requested status
		// already matches): journal-honest no-op.
		return current, nil
	}

	updated, err := t.prov.Show(id)
	if err != nil {
		return provenance.Task{}, err
	}
	return updated, nil
}

// metadataUpdateEffect builds the single provenance.task.updated effect for the set
// metadata fields, or reports hasMeta=false when no metadata field was supplied. It is
// the same effect shape provenance.Session.Update commits, so folding it inside an Atomic
// operation is byte-equivalent to a standalone Update of the same fields.
func metadataUpdateEffect(id provenance.TaskID, fields provenance.UpdateFields) (effect provenance.Effect, hasMeta bool) {
	if fields.Title == nil && fields.Description == nil && fields.Priority == nil &&
		fields.Phase == nil && fields.Notes == nil {
		return provenance.Effect{}, false
	}
	return provenance.Effect{
		Sort:              provenance.EffectTaskEvent,
		TaskID:            id,
		EventKind:         provenance.EventKindTaskUpdated,
		UpdateTitle:       fields.Title,
		UpdateDescription: fields.Description,
		UpdatePriority:    fields.Priority,
		UpdatePhase:       fields.Phase,
		UpdateNotes:       fields.Notes,
	}, true
}

// lifecycleKindFor maps a requested target status onto the journaled lifecycle EventKind
// that reaches it from current. hasLifecycle is false when targetStatus is nil or already
// equals current (a status no-op that commits nothing). An unrecognised target status
// yields an actionable validation error rather than an ad-hoc string.
func lifecycleKindFor(current provenance.Status, targetStatus *provenance.Status) (provenance.EventKind, bool, error) {
	if targetStatus == nil || *targetStatus == current {
		return "", false, nil
	}
	switch *targetStatus {
	case provenance.StatusInProgress:
		return provenance.EventKindTaskStarted, true, nil
	case provenance.StatusClosed:
		return provenance.EventKindTaskClosed, true, nil
	case provenance.StatusOpen:
		// open is reachable from closed (Reopen) or in_progress (Stop).
		if current == provenance.StatusClosed {
			return provenance.EventKindTaskReopened, true, nil
		}
		return provenance.EventKindTaskStopped, true, nil
	default:
		return "", false, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Pasture can't move a task to an unrecognised status.",
			Why:      "No journaled lifecycle transition reaches the requested target status; the CLI only supports open, in_progress, and closed.",
			Where:    "Applying a task status change (internal/tasks/task_update_atomic.go in tasks.lifecycleKindFor).",
			Impact:   "The status change was not applied and nothing was committed.",
			Fix:      "Pass one of open, in_progress, or closed as the target status.",
		}
	}
}

// lifecycleEffect builds the single lifecycle transition effect of the given kind. The
// close reason is empty here — a combined update expresses metadata + a bare status move;
// closing with a reason is the dedicated `pasture task close` path.
func lifecycleEffect(id provenance.TaskID, kind provenance.EventKind) provenance.Effect {
	return provenance.Effect{
		Sort:      provenance.EffectTaskEvent,
		TaskID:    id,
		EventKind: kind,
	}
}

// applyLifecycleVerb commits a status-only transition through the Session's dedicated
// lifecycle verb, preserving the exact single-operation behaviour (and typed
// ErrStatusTransition) of a standalone status change.
func applyLifecycleVerb(s *provenance.Session, id provenance.TaskID, kind provenance.EventKind) error {
	var err error
	switch kind {
	case provenance.EventKindTaskStarted:
		_, err = s.Start(id)
	case provenance.EventKindTaskStopped:
		_, err = s.Stop(id)
	case provenance.EventKindTaskReopened:
		_, err = s.Reopen(id)
	case provenance.EventKindTaskClosed:
		_, err = s.CloseTask(id, "")
	default:
		// Unreachable: lifecycleKindFor only returns the four kinds above.
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Pasture derived an unrecognised lifecycle transition for a task status change.",
			Why:      "The internal status-to-transition mapping produced a kind with no dedicated journaled verb.",
			Where:    "Applying a task status change (internal/tasks/task_update_atomic.go in tasks.applyLifecycleVerb).",
			Impact:   "The status change was not applied and nothing was committed.",
			Fix:      "This indicates a programming error in the status mapping; please file a bug.",
		}
	}
	return err
}
