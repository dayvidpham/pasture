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
			What:     "label name is required",
			Why:      "no label argument was passed to `pasture task label add`",
			Impact:   "no label can be added without a name",
			Fix:      "supply the label as the second positional argument: pasture task label add ID LABEL",
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
			What:     "label name is required",
			Why:      "no label argument was passed to `pasture task label remove`",
			Impact:   "no label can be removed without a name",
			Fix:      "supply the label as the second positional argument: pasture task label remove ID LABEL",
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
			What:     "comment body is required",
			Why:      "no body argument was passed to `pasture task comment add`",
			Impact:   "no comment can be added without a body",
			Fix:      "supply the body as the last positional argument: pasture task comment add ID --author=AGENT \"text\"",
		}
		return pasterrors.ExitCode(se), se
	}
	if in.AuthorID == "" {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "comment author is required",
			Why:      "no --author flag was provided",
			Impact:   "Provenance comments must reference a registered agent (PROV-O wasAttributedTo)",
			Fix:      "pass --author <agent-id> with the wire-format ID of a registered agent",
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
