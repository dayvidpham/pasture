package sessionapi

import (
	"time"

	"github.com/dayvidpham/pasture/internal/tasks"
)

// ProjectBindingSource is the provenance label every project-to-epoch mapping
// carries. A project mapping is an explicit operator relation, never inference.
type ProjectBindingSource string

// ProjectBindingSourceProject is the only source a project mapping reports.
const ProjectBindingSourceProject ProjectBindingSource = "project"

// ProjectBindingView is the active mapping of one project to one epoch.
type ProjectBindingView struct {
	BindingID  string               `json:"bindingId"`
	Role       string               `json:"role"`
	BoundAt    time.Time            `json:"boundAt"`
	Source     ProjectBindingSource `json:"source"`
	Confidence Confidence           `json:"confidence"`
}

// ProjectBindingInterval is one historical project mapping. UnboundAt is nil
// while the interval is the active mapping.
type ProjectBindingInterval struct {
	BindingID  string               `json:"bindingId"`
	Role       string               `json:"role"`
	BoundAt    time.Time            `json:"boundAt"`
	UnboundAt  *time.Time           `json:"unboundAt,omitempty"`
	EndReason  string               `json:"endReason,omitempty"`
	Source     ProjectBindingSource `json:"source"`
	Confidence Confidence           `json:"confidence"`
	Epoch      *EpochRef            `json:"epoch,omitempty"`
}

// ProjectView is one project's mapping. Binding and Epoch describe the active
// mapping; Intervals is the full interval history and is present on detail
// responses. Project identity is Peasant-owned; Pasture only records which
// epoch the project is explicitly mapped to.
type ProjectView struct {
	ProjectHash string                   `json:"projectHash"`
	Binding     *ProjectBindingView      `json:"binding,omitempty"`
	Epoch       *EpochRef                `json:"epoch,omitempty"`
	Intervals   []ProjectBindingInterval `json:"intervals,omitempty"`
}

// ProjectPage is the project list response. Items contains only projects with
// an active mapping.
type ProjectPage struct {
	Items []ProjectView `json:"items"`
}

// ProjectPageFromMappings renders the active mappings for the list route. List
// rows deliberately omit interval history; the detail route carries it.
func ProjectPageFromMappings(mappings []tasks.ProjectMapping) ProjectPage {
	items := make([]ProjectView, 0, len(mappings))
	for _, mapping := range mappings {
		items = append(items, projectView(mapping, false))
	}
	return ProjectPage{Items: items}
}

// ProjectViewFromMapping renders one project with its full interval history.
func ProjectViewFromMapping(mapping tasks.ProjectMapping) ProjectView {
	return projectView(mapping, true)
}

func projectView(mapping tasks.ProjectMapping, withIntervals bool) ProjectView {
	view := ProjectView{ProjectHash: mapping.ProjectHash}
	if binding := mapping.Binding; binding != nil {
		view.Binding = &ProjectBindingView{
			BindingID:  binding.BindingID,
			Role:       binding.Role,
			BoundAt:    binding.BoundAt,
			Source:     ProjectBindingSourceProject,
			Confidence: ConfidenceVerified,
		}
		if binding.EpochID != "" {
			view.Epoch = &EpochRef{ID: binding.EpochID, Title: binding.EpochTitle}
		}
	}
	if withIntervals {
		view.Intervals = make([]ProjectBindingInterval, 0, len(mapping.Intervals))
		for _, interval := range mapping.Intervals {
			projectInterval := ProjectBindingInterval{
				BindingID:  interval.BindingID,
				Role:       interval.Role,
				BoundAt:    interval.BoundAt,
				UnboundAt:  interval.UnboundAt,
				EndReason:  interval.EndReason,
				Source:     ProjectBindingSourceProject,
				Confidence: ConfidenceVerified,
			}
			if interval.EpochID != "" {
				projectInterval.Epoch = &EpochRef{ID: interval.EpochID, Title: interval.EpochTitle}
			}
			view.Intervals = append(view.Intervals, projectInterval)
		}
	}
	return view
}
