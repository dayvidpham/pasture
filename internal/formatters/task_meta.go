package formatters

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/types"
)

type labelsJSON struct {
	TaskId string   `json:"taskId"`
	Labels []string `json:"labels"`
}

// FormatLabels prints the label set for a task.
func FormatLabels(taskId string, labels []string, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(labelsJSON{TaskId: taskId, Labels: labels}, "", "  ")
		if err != nil {
			return "", fmt.Errorf("formatters.FormatLabels: marshal failed: %w", err)
		}
		return string(b), nil
	case types.OutputText:
		if len(labels) == 0 {
			return fmt.Sprintf("%s\nlabels: (none)", taskId), nil
		}
		return fmt.Sprintf("%s\nlabels: %s", taskId, strings.Join(labels, ", ")), nil
	default:
		return "", fmt.Errorf("formatters.FormatLabels: unknown output format %q — valid values: json, text", format)
	}
}

type commentJSON struct {
	ID        string `json:"id"`
	TaskId    string `json:"taskId"`
	AuthorId  string `json:"authorId"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

func toCommentJSON(c provenance.Comment) commentJSON {
	return commentJSON{
		ID:        c.ID.String(),
		TaskId:    c.TaskID.String(),
		AuthorId:  c.AuthorID.String(),
		Body:      c.Body,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// FormatComment renders a single comment.
func FormatComment(c provenance.Comment, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(toCommentJSON(c), "", "  ")
		if err != nil {
			return "", fmt.Errorf("formatters.FormatComment: marshal failed: %w", err)
		}
		return string(b), nil
	case types.OutputText:
		return renderCommentText(c), nil
	default:
		return "", fmt.Errorf("formatters.FormatComment: unknown output format %q — valid values: json, text", format)
	}
}

// FormatComments renders all comments on a task in chronological order.
func FormatComments(cs []provenance.Comment, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		js := make([]commentJSON, len(cs))
		for i, c := range cs {
			js[i] = toCommentJSON(c)
		}
		b, err := json.MarshalIndent(js, "", "  ")
		if err != nil {
			return "", fmt.Errorf("formatters.FormatComments: marshal failed: %w", err)
		}
		return string(b), nil
	case types.OutputText:
		if len(cs) == 0 {
			return "(no comments)", nil
		}
		var parts []string
		for _, c := range cs {
			parts = append(parts, renderCommentText(c))
		}
		return strings.Join(parts, "\n\n"), nil
	default:
		return "", fmt.Errorf("formatters.FormatComments: unknown output format %q — valid values: json, text", format)
	}
}

func renderCommentText(c provenance.Comment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s by %s\n", c.CreatedAt.UTC().Format(time.RFC3339), c.AuthorID.String())
	fmt.Fprintln(&b, c.Body)
	return strings.TrimRight(b.String(), "\n")
}
