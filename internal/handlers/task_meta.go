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

// TaskLabelAdd attaches a label to a task. Label add is idempotent at the
// Tracker layer.
func TaskLabelAdd(w io.Writer, dbPath, idStr, label string, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(idStr)
	if err != nil {
		return wrapInvalidID("task label add", idStr, err)
	}
	if label == "" {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A label name is required to attach a label.",
			Why:      "No label was passed to `pasture task label add` as the second positional argument.",
			Where:    "Adding a label (internal/handlers/task_meta.go in handlers.TaskLabelAdd).",
			Impact:   "Nothing can be attached without knowing the label's name.",
			Fix: "1. Pass the label as the second positional argument:\n" +
				"     pasture task label add <task-id> <label>\n" +
				"2. For example:\n" +
				"     pasture task label add aura-plugins--01968a3c-9d4f-7c8a-bc12-feedfacecafe important",
		}
		return pasterrors.ExitCode(se), se
	}

	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	if err := tr.AddLabel(id, label); err != nil {
		return wrapTaskOpError("label add", err)
	}
	return printLabels(w, tr, id, format)
}

// TaskLabelRemove detaches a label from a task. Idempotent at the Tracker layer.
func TaskLabelRemove(w io.Writer, dbPath, idStr, label string, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(idStr)
	if err != nil {
		return wrapInvalidID("task label remove", idStr, err)
	}
	if label == "" {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A label name is required to detach a label.",
			Why:      "No label was passed to `pasture task label remove` as the second positional argument.",
			Where:    "Removing a label (internal/handlers/task_meta.go in handlers.TaskLabelRemove).",
			Impact:   "Nothing can be detached without knowing the label's name.",
			Fix: "1. Pass the label as the second positional argument:\n" +
				"     pasture task label remove <task-id> <label>\n" +
				"2. To see which labels are currently attached:\n" +
				"     pasture task show <task-id>",
		}
		return pasterrors.ExitCode(se), se
	}

	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	if err := tr.RemoveLabel(id, label); err != nil {
		return wrapTaskOpError("label remove", err)
	}
	return printLabels(w, tr, id, format)
}

// TaskCommentAddInput captures the inputs for `pasture task comment add`.
type TaskCommentAddInput struct {
	DBPath   string
	IDStr    string
	AuthorID string // wire-format AgentID; required for now (see hjsdt follow-up)
	Body     string
}

// TaskCommentAdd posts a comment to a task and prints the resulting comment.
//
// The author must already be registered; agent registration ergonomics are
// tracked as a follow-up to hjsdt.
func TaskCommentAdd(w io.Writer, in TaskCommentAddInput, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(in.IDStr)
	if err != nil {
		return wrapInvalidID("task comment add", in.IDStr, err)
	}
	if in.Body == "" {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Comment text is required to add a comment.",
			Why:      "No comment text was passed to `pasture task comment add` as the last positional argument.",
			Where:    "Adding a comment (internal/handlers/task_meta.go in handlers.TaskCommentAdd).",
			Impact:   "An empty comment can't be added — there's nothing to record.",
			Fix: "1. Pass the comment text as the last positional argument:\n" +
				"     pasture task comment add <task-id> --author <agent-id> \"<text>\"\n" +
				"2. Use quotes if the text contains spaces.",
		}
		return pasterrors.ExitCode(se), se
	}
	if in.AuthorID == "" {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "An author is required to add a comment.",
			Why:      "The --author flag was not provided.",
			Where:    "Adding a comment (internal/handlers/task_meta.go in handlers.TaskCommentAdd).",
			Impact:   "Comments must say who wrote them, so we know who to attribute the message to.",
			Fix: "1. Pass --author with the ID of a registered agent:\n" +
				"     pasture task comment add <task-id> --author <agent-id> \"<text>\"\n" +
				"2. To find your agent ID, list registered agents:\n" +
				"     pasture task agents list",
		}
		return pasterrors.ExitCode(se), se
	}
	authorID, err := provenance.ParseAgentID(in.AuthorID)
	if err != nil {
		return wrapInvalidID("task comment add (author)", in.AuthorID, err)
	}

	tr, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	c, err := tr.AddComment(id, authorID, in.Body)
	if err != nil {
		return wrapTaskOpError("comment add", err)
	}

	out, fErr := formatters.FormatComment(c, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// TaskComments prints all comments on a task.
func TaskComments(w io.Writer, dbPath, idStr string, format types.OutputFormat) (int, error) {
	id, err := provenance.ParseTaskID(idStr)
	if err != nil {
		return wrapInvalidID("task comments", idStr, err)
	}

	tr, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	cs, err := tr.Comments(id)
	if err != nil {
		return wrapTaskOpError("comments", err)
	}
	out, fErr := formatters.FormatComments(cs, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

// printLabels reads the current label set for id and writes a formatter view
// of it. Both `label add` and `label remove` print this so the user always
// sees the post-state of their change.
func printLabels(w io.Writer, tr provenance.Tracker, id provenance.TaskID, format types.OutputFormat) (int, error) {
	labels, err := tr.Labels(id)
	if err != nil {
		return wrapTaskOpError("labels", err)
	}
	out, fErr := formatters.FormatLabels(id.String(), labels, format)
	if fErr != nil {
		return pasterrors.ExitCode(fErr), fErr
	}
	fmt.Fprintln(w, out)
	return 0, nil
}
