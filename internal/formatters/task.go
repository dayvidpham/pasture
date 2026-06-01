package formatters

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/types"
)

// taskJSON is the JSON wire representation of a Task. We mirror the
// provenance.Task JSON tags but render IDs as wire strings so consumers do
// not have to understand the {Namespace, UUID} shape.
type taskJSON struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Status      string  `json:"status"`
	Priority    string  `json:"priority"`
	Type        string  `json:"type"`
	Phase       string  `json:"phase"`
	Owner       string  `json:"owner,omitempty"`
	Notes       string  `json:"notes,omitempty"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	ClosedAt    *string `json:"closedAt,omitempty"`
	CloseReason string  `json:"closeReason,omitempty"`
}

func toTaskJSON(t provenance.Task) taskJSON {
	tj := taskJSON{
		ID:          t.ID.String(),
		Title:       t.Title,
		Description: t.Description,
		Status:      t.Status.String(),
		Priority:    t.Priority.String(),
		Type:        t.Type.String(),
		Phase:       t.Phase.String(),
		Notes:       t.Notes,
		CreatedAt:   t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.UTC().Format(time.RFC3339),
		CloseReason: t.CloseReason,
	}
	if t.Owner != nil {
		tj.Owner = t.Owner.String()
	}
	if t.ClosedAt != nil {
		s := t.ClosedAt.UTC().Format(time.RFC3339)
		tj.ClosedAt = &s
	}
	return tj
}

// FormatTask renders a single task in the requested output format.
func FormatTask(t provenance.Task, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(toTaskJSON(t), "", "  ")
		if err != nil {
			return "", fmt.Errorf("formatters.FormatTask: marshal failed: %w", err)
		}
		return string(b), nil
	case types.OutputText:
		return renderTaskText(t), nil
	default:
		return "", fmt.Errorf("formatters.FormatTask: unknown output format %q — valid values: json, text", format)
	}
}

// FormatTasks renders a list of tasks in the requested output format.
// JSON output is a top-level array. Text output is a one-line-per-task summary
// suitable for piping to grep / fzf.
func FormatTasks(ts []provenance.Task, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		js := make([]taskJSON, len(ts))
		for i, t := range ts {
			js[i] = toTaskJSON(t)
		}
		b, err := json.MarshalIndent(js, "", "  ")
		if err != nil {
			return "", fmt.Errorf("formatters.FormatTasks: marshal failed: %w", err)
		}
		return string(b), nil
	case types.OutputText:
		if len(ts) == 0 {
			return "(no tasks)", nil
		}
		var lines []string
		for _, t := range ts {
			lines = append(lines, renderTaskListLine(t))
		}
		return strings.Join(lines, "\n"), nil
	default:
		return "", fmt.Errorf("formatters.FormatTasks: unknown output format %q — valid values: json, text", format)
	}
}

func renderTaskText(t provenance.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ID:        %s\n", t.ID.String())
	fmt.Fprintf(&b, "Title:     %s\n", t.Title)
	fmt.Fprintf(&b, "Status:    %s\n", t.Status)
	fmt.Fprintf(&b, "Priority:  %s\n", t.Priority)
	fmt.Fprintf(&b, "Type:      %s\n", t.Type)
	fmt.Fprintf(&b, "Phase:     %s\n", t.Phase)
	if t.Owner != nil {
		fmt.Fprintf(&b, "Owner:     %s\n", t.Owner.String())
	}
	fmt.Fprintf(&b, "Created:   %s\n", t.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Updated:   %s\n", t.UpdatedAt.UTC().Format(time.RFC3339))
	if t.ClosedAt != nil {
		fmt.Fprintf(&b, "Closed:    %s\n", t.ClosedAt.UTC().Format(time.RFC3339))
		if t.CloseReason != "" {
			fmt.Fprintf(&b, "Reason:    %s\n", t.CloseReason)
		}
	}
	if t.Description != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", t.Description)
	}
	if t.Notes != "" {
		fmt.Fprintf(&b, "\nNotes:\n%s\n", t.Notes)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderTaskListLine(t provenance.Task) string {
	statusGlyph := taskStatusGlyph(t.Status)
	owner := ""
	if t.Owner != nil {
		owner = " @" + t.Owner.UUID.String()[:8]
	}
	return fmt.Sprintf("%s %s [%s] [%s] [%s]%s — %s",
		statusGlyph, t.ID.String(), t.Priority, t.Type, t.Phase, owner, t.Title)
}

func taskStatusGlyph(s provenance.Status) string {
	switch s {
	case provenance.StatusOpen:
		return "○"
	case provenance.StatusInProgress:
		return "◐"
	case provenance.StatusClosed:
		return "✓"
	default:
		return "?"
	}
}
