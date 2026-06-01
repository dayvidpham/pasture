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
			What:     "A title is required to create a task.",
			Why:      "No title was passed to `pasture task create` as a positional argument.",
			Impact:   "The task can't be created without a short, human-readable title to identify it by.",
			Fix: "1. Pass the title as the first positional argument:\n" +
				"     pasture task create \"<title>\"\n" +
				"2. Use quotes if the title contains spaces:\n" +
				"     pasture task create \"Add login screen\"",
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
		return wrapInvalidId("task show", idStr, err)
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
	IdStr       string
	Title       *string
	Description *string
	Status      *provenance.Status
	Priority    *provenance.Priority
	Phase       *provenance.Phase
	Notes       *string
}

// TaskUpdate applies partial updates to an existing task and prints the result.
func TaskUpdate(w io.Writer, in TaskUpdateInput, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(in.IdStr)
	if err != nil {
		return wrapInvalidId("task update", in.IdStr, err)
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
		return wrapInvalidId("task close", idStr, err)
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

// wrapInvalidId maps an ID parse failure to a CategoryValidation error.
//
// The Why field translates the underlying parser error into plain English
// rather than surfacing the raw "provenance: invalid ID format: ParseTaskId
// — no '--' separator found in ..." chain. The user only needs to know that
// IDs need a "--" separator and that the value they passed didn't have one.
func wrapInvalidId(op, id string, err error) (int, error) {
	se := &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("The task ID %q isn't in the expected format.", id),
		Why: "Task IDs look like \"yourproject--01968a3c-...\" (a project name, " +
			"two dashes, and a UUID — e.g., aura-plugins--01968a3c-9d4f-7c8a-bc12-feedfacecafe).\n" +
			"The value you passed couldn't be split into those two parts.",
		Where:  fmt.Sprintf("Running %q (handlers/task_crud.go in handlers.wrapInvalidId).", op),
		Impact: fmt.Sprintf("The %q command can't run because there's no way to know which task you meant.", op),
		Fix: "1. Pass a valid task ID. Use list to find one:\n" +
			"     pasture task list\n" +
			"2. Then retry your command with the correct ID.",
	}
	// Preserve the underlying parse error via the Cause field so logs and
	// errors.Is/As can still inspect the raw failure, but keep it out of
	// the user-visible Why above (which would otherwise surface package
	// qualifiers and Go function names like "provenance: ... ParseTaskId").
	se.Cause = err
	return pasterrors.ExitCode(se), se
}

// wrapTaskOpError maps a tracker operation error to the standard exit code.
// Tracker errors are surfaced as CategoryWorkflow (exit 3) — they represent
// state-dependent failures rather than input validation problems.
//
// The underlying tracker error is intentionally NOT surfaced verbatim in the
// Why field — it typically contains Go symbol names ("OpenTaskTracker",
// "tasks: ...", SQLite column names) that aren't useful to a non-specialist.
// The Fix field guides the user toward the most likely causes instead.
func wrapTaskOpError(op string, err error) (int, error) {
	se := &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     fmt.Sprintf("The task %q operation didn't complete.", op),
		Why:      "The task store rejected the request. The most likely causes are listed under \"How to fix\" below.",
		Where:    fmt.Sprintf("Running %q (handlers/task_crud.go in handlers.wrapTaskOpError).", op),
		Impact:   "The change you asked for wasn't applied.",
		Fix: "1. Confirm the task exists and check its current state:\n" +
			"     pasture task list\n" +
			"     pasture task show <task-id>\n" +
			"2. Common causes:\n" +
			"   - The task ID doesn't exist (look it up with `pasture task list`).\n" +
			"   - The task is already closed and can't be changed further.\n" +
			"   - You tried to add a dependency that would create a cycle.\n" +
			"3. Re-run the command after fixing the underlying cause.",
	}
	se.Cause = err // Preserved for logs / errors.Is — not surfaced to user.
	return pasterrors.ExitCode(se), se
}
