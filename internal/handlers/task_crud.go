package handlers

import (
	"fmt"
	"io"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// TaskCreateInput captures the inputs for `pasture task create`.
// All fields are validated by the handler — empty Title returns
// CategoryValidation, missing namespace falls back to git remote derivation.
type TaskCreateInput struct {
	DBPath      string
	Namespace   string // explicit override; "" → DefaultNamespace
	Title       string
	Description string
	Type        provenance.TaskType
	Priority    provenance.Priority
	Phase       provenance.Phase
}

// TaskCreate creates a new task and prints its details. Returns the standard
// (exit code, error) tuple used by RunE handlers.
func TaskCreate(w io.Writer, in TaskCreateInput, format types.OutputFormat) (int, error) {
	if in.Title == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "task title is required",
			Why:      "no title argument was provided to `pasture task create`",
			Impact:   "task cannot be created without a human-readable title",
			Fix:      "pass the title as the first positional argument: pasture task create \"My title\"",
		}
		return pasterrors.ExitCode(err), err
	}

	ns, err := tasks.ResolveNamespace(in.Namespace)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	tr, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	t, err := tr.Create(ns, in.Title, in.Description, in.Type, in.Priority, in.Phase)
	if err != nil {
		return wrapTaskOpError("create", err)
	}

	out, fErr := formatters.FormatTask(t, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskShow looks up a task by its wire-format ID.
func TaskShow(w io.Writer, dbPath, idStr string, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(idStr)
	if err != nil {
		return wrapInvalidID("task show", idStr, err)
	}

	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	t, err := tr.Show(id)
	if err != nil {
		return wrapTaskOpError("show", err)
	}
	out, fErr := formatters.FormatTask(t, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskUpdateInput captures the optional fields supplied to `pasture task update`.
// Pointer fields are nil when the corresponding flag was not passed.
type TaskUpdateInput struct {
	DBPath      string
	IDStr       string
	Title       *string
	Description *string
	Status      *provenance.Status
	Priority    *provenance.Priority
	Phase       *provenance.Phase
	Notes       *string
}

// TaskUpdate applies partial updates to an existing task and prints the result.
func TaskUpdate(w io.Writer, in TaskUpdateInput, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(in.IDStr)
	if err != nil {
		return wrapInvalidID("task update", in.IDStr, err)
	}

	tr, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	t, err := tr.Update(id, provenance.UpdateFields{
		Title:       in.Title,
		Description: in.Description,
		Status:      in.Status,
		Priority:    in.Priority,
		Phase:       in.Phase,
		Notes:       in.Notes,
	})
	if err != nil {
		return wrapTaskOpError("update", err)
	}
	out, fErr := formatters.FormatTask(t, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskClose closes a task with the given reason.
func TaskClose(w io.Writer, dbPath, idStr, reason string, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(idStr)
	if err != nil {
		return wrapInvalidID("task close", idStr, err)
	}

	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	t, err := tr.CloseTask(id, reason)
	if err != nil {
		return wrapTaskOpError("close", err)
	}
	out, fErr := formatters.FormatTask(t, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskListInput captures filter inputs for `pasture task list`.
// Empty / nil fields are not applied as filters.
type TaskListInput struct {
	DBPath    string
	Status    *provenance.Status
	Priority  *provenance.Priority
	Type      *provenance.TaskType
	Phase     *provenance.Phase
	Label     string
	Namespace string
}

// TaskList prints tasks matching the given filter.
func TaskList(w io.Writer, in TaskListInput, format types.OutputFormat) (int, error) {
	tr, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	ts, err := tr.List(provenance.ListFilter{
		Status:    in.Status,
		Priority:  in.Priority,
		Type:      in.Type,
		Phase:     in.Phase,
		Label:     in.Label,
		Namespace: in.Namespace,
	})
	if err != nil {
		return wrapTaskOpError("list", err)
	}

	out, fErr := formatters.FormatTasks(ts, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// wrapInvalidID maps an ID parse failure to a CategoryValidation error.
func wrapInvalidID(op, id string, err error) (int, error) {
	se := &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("%s: invalid task ID %q", op, id),
		Why:      err.Error(),
		Impact:   "the operation cannot proceed without a parseable task ID",
		Fix:      "pass an ID in the form 'namespace--uuid' (e.g., aura-plugins-hjsdt)",
	}
	return pasterrors.ExitCode(se), se
}

// wrapTaskOpError maps a tracker operation error to the standard exit code.
// Tracker errors are surfaced as CategoryWorkflow (exit 3) — they represent
// state-dependent failures rather than input validation problems.
func wrapTaskOpError(op string, err error) (int, error) {
	se := &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     fmt.Sprintf("task %s failed", op),
		Why:      err.Error(),
		Impact:   "the requested task operation could not be completed",
		Fix:      "inspect the underlying error message above; common causes are missing tasks (run `pasture task list` to verify the ID), already-closed tasks, or cycles in the blocked-by graph",
	}
	return pasterrors.ExitCode(se), se
}
