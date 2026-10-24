package formatters_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/types"
)

func sampleProjectView() sessionapi.ProjectView {
	boundAt := time.Date(2026, 10, 24, 1, 2, 3, 0, time.UTC)
	unboundAt := boundAt.Add(time.Hour)
	return sessionapi.ProjectView{
		ProjectHash: strings.Repeat("a", 64),
		Binding: &sessionapi.ProjectBindingView{
			BindingID:  "binding-open",
			Role:       "worker",
			BoundAt:    unboundAt,
			Source:     sessionapi.ProjectBindingSourceProject,
			Confidence: sessionapi.ConfidenceVerified,
		},
		Epoch: &sessionapi.EpochRef{ID: "file://epoch/two", Title: "Epoch two"},
		Intervals: []sessionapi.ProjectBindingInterval{
			{
				BindingID:  "binding-closed",
				Role:       "reviewer",
				BoundAt:    boundAt,
				UnboundAt:  &unboundAt,
				EndReason:  "rebind",
				Source:     sessionapi.ProjectBindingSourceProject,
				Confidence: sessionapi.ConfidenceVerified,
				Epoch:      &sessionapi.EpochRef{ID: "file://epoch/one", Title: "Epoch one"},
			},
			{
				BindingID:  "binding-open",
				Role:       "worker",
				BoundAt:    unboundAt,
				Source:     sessionapi.ProjectBindingSourceProject,
				Confidence: sessionapi.ConfidenceVerified,
				Epoch:      &sessionapi.EpochRef{ID: "file://epoch/two", Title: "Epoch two"},
			},
		},
	}
}

func TestFormatProjectPageJSONAndText(t *testing.T) {
	t.Parallel()
	empty, err := formatters.FormatProjectPage(sessionapi.ProjectPage{}, types.OutputJSON)
	require.NoError(t, err)
	require.Contains(t, empty, `"items": []`, "an empty project page is an array, not null")

	view := sampleProjectView()
	raw, err := formatters.FormatProjectPage(sessionapi.ProjectPage{Items: []sessionapi.ProjectView{view}}, types.OutputJSON)
	require.NoError(t, err)
	var page sessionapi.ProjectPage
	require.NoError(t, json.Unmarshal([]byte(raw), &page))
	require.Len(t, page.Items, 1)
	require.Equal(t, view.ProjectHash, page.Items[0].ProjectHash)

	text, err := formatters.FormatProjectPage(sessionapi.ProjectPage{Items: []sessionapi.ProjectView{view}}, types.OutputText)
	require.NoError(t, err)
	require.Contains(t, text, "PROJECT HASH")
	require.Contains(t, text, view.ProjectHash)
	require.Contains(t, text, "file://epoch/two")
	require.Contains(t, text, "worker")

	emptyText, err := formatters.FormatProjectPage(sessionapi.ProjectPage{}, types.OutputText)
	require.NoError(t, err)
	require.Contains(t, emptyText, "No projects have an active epoch mapping")
	require.Contains(t, emptyText, "pasture project bind")
}

func TestFormatProjectDetailJSONAndText(t *testing.T) {
	t.Parallel()
	view := sampleProjectView()
	raw, err := formatters.FormatProjectDetail(view, types.OutputJSON)
	require.NoError(t, err)
	var decoded sessionapi.ProjectView
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	require.Equal(t, view.ProjectHash, decoded.ProjectHash)
	require.Len(t, decoded.Intervals, 2)

	text, err := formatters.FormatProjectDetail(view, types.OutputText)
	require.NoError(t, err)
	require.Contains(t, text, "Project:    "+view.ProjectHash)
	require.Contains(t, text, "Binding:    binding-open")
	require.Contains(t, text, "Source:     project")
	require.Contains(t, text, "Confidence: verified")
	require.Contains(t, text, "Epoch:      file://epoch/two")
	require.Contains(t, text, "Epoch title: Epoch two")
	require.Contains(t, text, "Intervals:  2")
	require.Contains(t, text, "endReason=rebind")
	require.Contains(t, text, "unbound 2026-10-24T02:02:03Z")
	require.Contains(t, text, "2. bound 2026-10-24T02:02:03Z; open")
}

func TestFormatProjectRejectsUnknownFormat(t *testing.T) {
	t.Parallel()
	_, err := formatters.FormatProjectPage(sessionapi.ProjectPage{}, types.OutputFormat("yaml"))
	require.Error(t, err)
	_, err = formatters.FormatProjectDetail(sessionapi.ProjectView{}, types.OutputFormat("yaml"))
	require.Error(t, err)
}

func TestFormatProjectDetailUnboundProject(t *testing.T) {
	t.Parallel()
	view := sampleProjectView()
	view.Binding = nil
	view.Epoch = nil
	text, err := formatters.FormatProjectDetail(view, types.OutputText)
	require.NoError(t, err)
	require.Contains(t, text, "Binding:    -")
	require.Contains(t, text, "Epoch:      -")
	require.Contains(t, text, "Intervals:  2")
}
