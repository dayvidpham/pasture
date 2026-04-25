package handlers_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

func TestTaskCreate_PopulatesAllFields(t *testing.T) {
	var out bytes.Buffer
	code, err := handlers.TaskCreate(&out, handlers.TaskCreateInput{
		DBPath:      dbPath(t),
		Namespace:   "test",
		Title:       "First task",
		Description: "hello",
		Type:        provenance.TaskTypeFeature,
		Priority:    provenance.PriorityHigh,
		Phase:       provenance.PhaseRequest,
	}, types.OutputJSON)
	if err != nil {
		t.Fatalf("TaskCreate failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	got := decodeTask(t, out.String())
	if got.Title != "First task" {
		t.Errorf("title: got %q, want %q", got.Title, "First task")
	}
	if got.Description != "hello" {
		t.Errorf("description: got %q, want %q", got.Description, "hello")
	}
	if got.Status != "open" {
		t.Errorf("status: got %q, want %q", got.Status, "open")
	}
	if got.Priority != "high" {
		t.Errorf("priority: got %q, want %q", got.Priority, "high")
	}
	if got.Type != "feature" {
		t.Errorf("type: got %q, want %q", got.Type, "feature")
	}
	if got.Phase != "request" {
		t.Errorf("phase: got %q, want %q", got.Phase, "request")
	}
	if !strings.HasPrefix(got.ID, "test--") {
		t.Errorf("id: expected prefix %q, got %q", "test--", got.ID)
	}
}

func TestTaskCreate_RejectsEmptyTitle(t *testing.T) {
	var out bytes.Buffer
	code, err := handlers.TaskCreate(&out, handlers.TaskCreateInput{
		DBPath:    dbPath(t),
		Namespace: "test",
	}, types.OutputText)
	if err == nil {
		t.Fatal("expected validation error for empty title")
	}
	if code != 1 {
		t.Fatalf("expected exit code 1 (validation), got %d", code)
	}
}

func TestTaskShow_RoundTrip(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "Round trip")

	var showOut bytes.Buffer
	code, err := handlers.TaskShow(&showOut, path, id, types.OutputJSON)
	if err != nil {
		t.Fatalf("show failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	got := decodeTask(t, showOut.String())
	if got.Title != "Round trip" {
		t.Errorf("title: got %q, want %q", got.Title, "Round trip")
	}
	if got.ID != id {
		t.Errorf("id: got %q, want %q", got.ID, id)
	}
}

func TestTaskShow_InvalidID(t *testing.T) {
	var out bytes.Buffer
	code, err := handlers.TaskShow(&out, dbPath(t), "not-a-real-id", types.OutputText)
	if err == nil {
		t.Fatal("expected error for malformed ID")
	}
	if code != 1 {
		t.Fatalf("expected exit code 1 (validation), got %d", code)
	}
}

func TestTaskUpdate_AllFields(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "Original")

	newTitle := "Updated title"
	newDescription := "Updated body"
	inProgress := provenance.StatusInProgress
	highPriority := provenance.PriorityHigh
	codeReview := provenance.PhaseCodeReview
	notes := "claimed by tester"

	var out bytes.Buffer
	if _, err := handlers.TaskUpdate(&out, handlers.TaskUpdateInput{
		DBPath:      path,
		IDStr:       id,
		Title:       &newTitle,
		Description: &newDescription,
		Status:      &inProgress,
		Priority:    &highPriority,
		Phase:       &codeReview,
		Notes:       &notes,
	}, types.OutputJSON); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	got := decodeTask(t, out.String())
	if got.Title != newTitle {
		t.Errorf("title: got %q, want %q", got.Title, newTitle)
	}
	if got.Description != newDescription {
		t.Errorf("description: got %q, want %q", got.Description, newDescription)
	}
	if got.Status != "in_progress" {
		t.Errorf("status: got %q, want %q", got.Status, "in_progress")
	}
	if got.Priority != "high" {
		t.Errorf("priority: got %q, want %q", got.Priority, "high")
	}
	if got.Phase != "code_review" {
		t.Errorf("phase: got %q, want %q", got.Phase, "code_review")
	}
	if got.Notes != "claimed by tester" {
		t.Errorf("notes: got %q, want %q", got.Notes, "claimed by tester")
	}
}

func TestTaskClose_AndDoubleCloseRejected(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "To be closed")

	var closeOut bytes.Buffer
	code, err := handlers.TaskClose(&closeOut, path, id, "all done", types.OutputJSON)
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	got := decodeTask(t, closeOut.String())
	if got.Status != "closed" {
		t.Errorf("expected status closed, got %q", got.Status)
	}
	if got.CloseReason != "all done" {
		t.Errorf("expected close reason %q, got %q", "all done", got.CloseReason)
	}

	code2, err := handlers.TaskClose(&bytes.Buffer{}, path, id, "again", types.OutputText)
	if err == nil {
		t.Fatal("expected error closing an already-closed task")
	}
	if code2 != 3 {
		t.Fatalf("expected exit 3 (workflow), got %d", code2)
	}
}

func TestTaskList_NoFilter(t *testing.T) {
	path := dbPath(t)
	for _, title := range []string{"alpha", "beta", "gamma"} {
		_ = createTask(t, path, title)
	}

	var listOut bytes.Buffer
	code, err := handlers.TaskList(&listOut, handlers.TaskListInput{DBPath: path}, types.OutputJSON)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	got := decodeTaskList(t, listOut.String())
	if len(got) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(got))
	}
	titles := map[string]bool{}
	for _, tk := range got {
		titles[tk.Title] = true
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !titles[want] {
			t.Errorf("missing %q in list output", want)
		}
	}
}

func TestTaskList_FilterByStatus(t *testing.T) {
	path := dbPath(t)
	open := createTask(t, path, "still open")
	toClose := createTask(t, path, "will close")

	if _, err := handlers.TaskClose(&bytes.Buffer{}, path, toClose, "done", types.OutputJSON); err != nil {
		t.Fatalf("close: %v", err)
	}

	closedFilter := provenance.StatusClosed
	var out bytes.Buffer
	if _, err := handlers.TaskList(&out, handlers.TaskListInput{
		DBPath: path,
		Status: &closedFilter,
	}, types.OutputJSON); err != nil {
		t.Fatalf("list closed: %v", err)
	}
	got := decodeTaskList(t, out.String())
	if len(got) != 1 {
		t.Fatalf("expected 1 closed task, got %d", len(got))
	}
	if got[0].ID != toClose {
		t.Errorf("expected closed task id %q, got %q", toClose, got[0].ID)
	}
	for _, tk := range got {
		if tk.ID == open {
			t.Errorf("open task %q should not appear in closed filter", open)
		}
	}
}

func TestTaskList_FilterByType(t *testing.T) {
	path := dbPath(t)

	mkType := func(title string, tt provenance.TaskType) string {
		var buf bytes.Buffer
		if _, err := handlers.TaskCreate(&buf, handlers.TaskCreateInput{
			DBPath:    path,
			Namespace: "test",
			Title:     title,
			Type:      tt,
			Priority:  provenance.PriorityMedium,
			Phase:     provenance.PhaseUnscoped,
		}, types.OutputJSON); err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return decodeTask(t, buf.String()).ID
	}
	bug := mkType("bug-task", provenance.TaskTypeBug)
	mkType("feature-task", provenance.TaskTypeFeature)

	bugFilter := provenance.TaskTypeBug
	var out bytes.Buffer
	if _, err := handlers.TaskList(&out, handlers.TaskListInput{
		DBPath: path,
		Type:   &bugFilter,
	}, types.OutputJSON); err != nil {
		t.Fatalf("list type=bug: %v", err)
	}
	got := decodeTaskList(t, out.String())
	if len(got) != 1 || got[0].ID != bug {
		t.Fatalf("expected only bug task %q, got %+v", bug, got)
	}
}

func TestTaskList_FilterByLabel(t *testing.T) {
	path := dbPath(t)
	flagged := createTask(t, path, "flagged")
	unflagged := createTask(t, path, "unflagged")

	if _, err := handlers.TaskLabelAdd(&bytes.Buffer{}, path, flagged, "important", types.OutputJSON); err != nil {
		t.Fatalf("label add: %v", err)
	}

	var out bytes.Buffer
	if _, err := handlers.TaskList(&out, handlers.TaskListInput{
		DBPath: path,
		Label:  "important",
	}, types.OutputJSON); err != nil {
		t.Fatalf("list label=important: %v", err)
	}
	got := decodeTaskList(t, out.String())
	if len(got) != 1 || got[0].ID != flagged {
		t.Fatalf("expected only flagged task %q, got %+v", flagged, got)
	}
	for _, tk := range got {
		if tk.ID == unflagged {
			t.Errorf("unflagged task should not match label filter")
		}
	}
}
