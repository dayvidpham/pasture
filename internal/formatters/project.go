// Package formatters — project.go
//
// Output formatting for `pasture project list`, `show`, `bind`, and `unbind`.
//
// JSON mode keeps the wire shapes owned by internal/sessionapi (ProjectPage and
// ProjectView), so the CLI and the local API describe a mapping identically.
// Text mode renders a compact active-mapping table and a labelled detail block
// with the full interval history.
package formatters

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/types"
)

// FormatProjectPage renders the projects with an active epoch mapping.
func FormatProjectPage(page sessionapi.ProjectPage, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		if page.Items == nil {
			page.Items = []sessionapi.ProjectView{}
		}
		b, err := json.MarshalIndent(page, "", "  ")
		if err != nil {
			return "", sessionJSONError("FormatProjectPage", err)
		}
		return string(b), nil
	case types.OutputText:
		return formatProjectPageText(page), nil
	default:
		return "", unknownFormatErr("FormatProjectPage", format)
	}
}

// FormatProjectDetail renders one project's active mapping and full history.
func FormatProjectDetail(view sessionapi.ProjectView, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return "", sessionJSONError("FormatProjectDetail", err)
		}
		return string(b), nil
	case types.OutputText:
		return formatProjectDetailText(view), nil
	default:
		return "", unknownFormatErr("FormatProjectDetail", format)
	}
}

func formatProjectPageText(page sessionapi.ProjectPage) string {
	if len(page.Items) == 0 {
		return "No projects have an active epoch mapping.\n\n" +
			"A project appears here after an explicit bind:\n" +
			"     pasture project bind <projectHash> --epoch <epoch-id>"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT HASH\tEPOCH\tROLE\tBOUND AT")
	for _, project := range page.Items {
		epoch := sessionMissing
		role := sessionMissing
		boundAt := sessionMissing
		if project.Epoch != nil && project.Epoch.ID != "" {
			epoch = project.Epoch.ID
		}
		if project.Binding != nil {
			role = project.Binding.Role
			boundAt = sessionInstant(&project.Binding.BoundAt)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", project.ProjectHash, epoch, role, boundAt)
	}
	_ = tw.Flush()
	return strings.TrimRight(b.String(), "\n")
}

func formatProjectDetailText(view sessionapi.ProjectView) string {
	lines := []string{fmt.Sprintf("Project:    %s", view.ProjectHash)}
	if view.Binding != nil {
		lines = append(lines,
			fmt.Sprintf("Binding:    %s", view.Binding.BindingID),
			fmt.Sprintf("Role:       %s", view.Binding.Role),
			fmt.Sprintf("Bound at:   %s", sessionInstant(&view.Binding.BoundAt)),
			fmt.Sprintf("Source:     %s", view.Binding.Source),
			fmt.Sprintf("Confidence: %s", view.Binding.Confidence),
		)
	} else {
		lines = append(lines, "Binding:    "+sessionMissing)
	}
	if view.Epoch != nil {
		lines = append(lines, fmt.Sprintf("Epoch:      %s", view.Epoch.ID))
		if view.Epoch.Title != "" {
			lines = append(lines, fmt.Sprintf("Epoch title: %s", view.Epoch.Title))
		}
	} else {
		lines = append(lines, "Epoch:      "+sessionMissing)
	}
	lines = append(lines, formatProjectIntervals(view.Intervals)...)
	return strings.Join(lines, "\n")
}

func formatProjectIntervals(intervals []sessionapi.ProjectBindingInterval) []string {
	if len(intervals) == 0 {
		return []string{"Intervals:  0"}
	}
	lines := []string{fmt.Sprintf("Intervals:  %d", len(intervals))}
	for i, interval := range intervals {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, formatProjectInterval(interval)))
	}
	return lines
}

func formatProjectInterval(interval sessionapi.ProjectBindingInterval) string {
	unbound := "open"
	if interval.UnboundAt != nil {
		unbound = "unbound " + sessionInstant(interval.UnboundAt)
	}
	epoch := sessionMissing
	title := sessionMissing
	if interval.Epoch != nil {
		if interval.Epoch.ID != "" {
			epoch = interval.Epoch.ID
		}
		if interval.Epoch.Title != "" {
			title = interval.Epoch.Title
		}
	}
	return fmt.Sprintf(
		"bound %s; %s; endReason=%s; source=%s; confidence=%s; role=%s; epoch=%s; title=%s",
		sessionInstant(&interval.BoundAt),
		unbound,
		sessionTextOrMissing(interval.EndReason),
		interval.Source,
		interval.Confidence,
		sessionTextOrMissing(interval.Role),
		epoch,
		title,
	)
}
