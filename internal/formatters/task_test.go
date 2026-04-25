package formatters_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
)

// fixedTime is a deterministic timestamp used in formatter tests so output
// strings do not change between runs.
var fixedTime = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func sampleTask() provenance.Task {
	return provenance.Task{
		ID:          provenance.TaskID{Namespace: "test"},
		Title:       "Hello",
		Description: "world",
		Status:      provenance.StatusOpen,
		Priority:    provenance.PriorityHigh,
		Type:        provenance.TaskTypeFeature,
		Phase:       provenance.PhaseRequest,
		CreatedAt:   fixedTime,
		UpdatedAt:   fixedTime,
	}
}

func TestFormatTask_JSON(t *testing.T) {
	task := sampleTask()
	out, err := formatters.FormatTask(task, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatTask json: %v", err)
	}

	var got struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
		Type     string `json:"type"`
		Phase    string `json:"phase"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, out)
	}
	if got.Title != "Hello" || got.Status != "open" || got.Priority != "high" ||
		got.Type != "feature" || got.Phase != "request" {
		t.Errorf("unexpected wire shape: %+v\nraw: %s", got, out)
	}
}

func TestFormatTask_Text(t *testing.T) {
	task := sampleTask()
	out, err := formatters.FormatTask(task, types.OutputText)
	if err != nil {
		t.Fatalf("FormatTask text: %v", err)
	}
	for _, want := range []string{"Title:", "Hello", "Status:", "open", "Priority:", "high"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in text output, got:\n%s", want, out)
		}
	}
}

func TestFormatTasks_JSONArray(t *testing.T) {
	tasks := []provenance.Task{sampleTask(), sampleTask()}
	out, err := formatters.FormatTasks(tasks, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatTasks json: %v", err)
	}
	var got []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
}

func TestFormatTasks_TextEmpty(t *testing.T) {
	out, err := formatters.FormatTasks(nil, types.OutputText)
	if err != nil {
		t.Fatalf("FormatTasks empty: %v", err)
	}
	if out != "(no tasks)" {
		t.Errorf("expected '(no tasks)', got %q", out)
	}
}

func TestFormatTask_RejectsUnknownFormat(t *testing.T) {
	if _, err := formatters.FormatTask(sampleTask(), types.OutputFormat("yaml")); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestFormatLabels_JSON(t *testing.T) {
	out, err := formatters.FormatLabels("test--abc", []string{"a", "b"}, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatLabels json: %v", err)
	}
	var got struct {
		TaskID string   `json:"taskId"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TaskID != "test--abc" || len(got.Labels) != 2 {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestFormatLabels_TextEmpty(t *testing.T) {
	out, err := formatters.FormatLabels("test--abc", nil, types.OutputText)
	if err != nil {
		t.Fatalf("FormatLabels empty: %v", err)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("expected '(none)' for empty labels, got %q", out)
	}
}

func TestFormatComment_JSON(t *testing.T) {
	c := provenance.Comment{
		ID:        provenance.CommentID{Namespace: "test"},
		TaskID:    provenance.TaskID{Namespace: "test"},
		AuthorID:  provenance.AgentID{Namespace: "test"},
		Body:      "hello",
		CreatedAt: fixedTime,
	}
	out, err := formatters.FormatComment(c, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatComment: %v", err)
	}
	var got struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Body != "hello" {
		t.Errorf("body: got %q, want %q", got.Body, "hello")
	}
}

func TestFormatEdge_JSON(t *testing.T) {
	e := provenance.Edge{SourceID: "test--a", TargetID: "test--b", Kind: provenance.EdgeBlockedBy}
	out, err := formatters.FormatEdge(e, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatEdge json: %v", err)
	}
	var got struct {
		SourceID string `json:"sourceId"`
		TargetID string `json:"targetId"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SourceID != "test--a" || got.TargetID != "test--b" || got.Kind != "blocked_by" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestFormatDepTree_JSONShape(t *testing.T) {
	edges := []provenance.Edge{
		{SourceID: "test--a", TargetID: "test--b", Kind: provenance.EdgeBlockedBy},
	}
	out, err := formatters.FormatDepTree("test--a", edges, types.OutputJSON)
	if err != nil {
		t.Fatalf("FormatDepTree json: %v", err)
	}
	var got struct {
		Root  string `json:"root"`
		Edges []struct {
			SourceID string `json:"sourceId"`
			TargetID string `json:"targetId"`
			Kind     string `json:"kind"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Root != "test--a" {
		t.Errorf("root: got %q, want %q", got.Root, "test--a")
	}
	if len(got.Edges) != 1 || got.Edges[0].Kind != "blocked_by" {
		t.Errorf("edges shape unexpected: %+v", got.Edges)
	}
}

func TestFormatDepTree_TextEmpty(t *testing.T) {
	out, err := formatters.FormatDepTree("test--a", nil, types.OutputText)
	if err != nil {
		t.Fatalf("FormatDepTree empty: %v", err)
	}
	if !strings.Contains(out, "test--a") {
		t.Errorf("expected root id in empty output, got %q", out)
	}
}
