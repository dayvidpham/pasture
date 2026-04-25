package handlers_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// dbPath returns a fresh SQLite path under a temp dir. We use file-backed
// SQLite (not in-memory) because each handler call opens its own tracker; an
// in-memory DB would not be shared across calls.
func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "pasture.db")
}

func TestTaskCreate_Basic(t *testing.T) {
	var out bytes.Buffer
	code, err := handlers.TaskCreate(&out, handlers.TaskCreateInput{
		DBPath:      dbPath(t),
		Namespace:   "test",
		Title:       "First task",
		Description: "hello",
		Type:        provenance.TaskTypeFeature,
		Priority:    provenance.PriorityMedium,
		Phase:       provenance.PhaseUnscoped,
	}, types.OutputText)
	if err != nil {
		t.Fatalf("TaskCreate failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(out.String(), "First task") {
		t.Fatalf("expected output to contain title, got %q", out.String())
	}
	if !strings.Contains(out.String(), "test--") {
		t.Fatalf("expected output to contain wire ID, got %q", out.String())
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

	var createOut bytes.Buffer
	if _, err := handlers.TaskCreate(&createOut, handlers.TaskCreateInput{
		DBPath:    path,
		Namespace: "test",
		Title:     "Round trip",
		Type:      provenance.TaskTypeTask,
		Priority:  provenance.PriorityMedium,
		Phase:     provenance.PhaseUnscoped,
	}, types.OutputJSON); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	id := extractIDFromJSON(t, createOut.String())

	var showOut bytes.Buffer
	code, err := handlers.TaskShow(&showOut, path, id, types.OutputText)
	if err != nil {
		t.Fatalf("show failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(showOut.String(), "Round trip") {
		t.Fatalf("expected show to return the task title, got %q", showOut.String())
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

func TestTaskUpdate_StatusFlow(t *testing.T) {
	path := dbPath(t)

	var createOut bytes.Buffer
	if _, err := handlers.TaskCreate(&createOut, handlers.TaskCreateInput{
		DBPath:    path,
		Namespace: "test",
		Title:     "Updateable",
		Type:      provenance.TaskTypeTask,
		Priority:  provenance.PriorityMedium,
		Phase:     provenance.PhaseUnscoped,
	}, types.OutputJSON); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	id := extractIDFromJSON(t, createOut.String())

	inProgress := provenance.StatusInProgress
	notes := "claimed by tester"
	var updateOut bytes.Buffer
	if _, err := handlers.TaskUpdate(&updateOut, handlers.TaskUpdateInput{
		DBPath: path,
		IDStr:  id,
		Status: &inProgress,
		Notes:  &notes,
	}, types.OutputJSON); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !strings.Contains(updateOut.String(), `"status": "in_progress"`) {
		t.Fatalf("expected updated status in output, got %q", updateOut.String())
	}
	if !strings.Contains(updateOut.String(), "claimed by tester") {
		t.Fatalf("expected notes in output, got %q", updateOut.String())
	}
}

func TestTaskClose_AndReason(t *testing.T) {
	path := dbPath(t)

	var createOut bytes.Buffer
	if _, err := handlers.TaskCreate(&createOut, handlers.TaskCreateInput{
		DBPath:    path,
		Namespace: "test",
		Title:     "To be closed",
		Type:      provenance.TaskTypeTask,
		Priority:  provenance.PriorityMedium,
		Phase:     provenance.PhaseUnscoped,
	}, types.OutputJSON); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	id := extractIDFromJSON(t, createOut.String())

	var closeOut bytes.Buffer
	code, err := handlers.TaskClose(&closeOut, path, id, "all done", types.OutputText)
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(closeOut.String(), "all done") {
		t.Fatalf("expected close reason in output, got %q", closeOut.String())
	}

	// Closing again should fail with a workflow error (exit 3).
	code2, err := handlers.TaskClose(&bytes.Buffer{}, path, id, "again", types.OutputText)
	if err == nil {
		t.Fatal("expected error closing an already-closed task")
	}
	if code2 != 3 {
		t.Fatalf("expected exit 3 (workflow), got %d", code2)
	}
}

func TestTaskList_FilterByStatus(t *testing.T) {
	path := dbPath(t)

	for _, title := range []string{"alpha", "beta", "gamma"} {
		var buf bytes.Buffer
		if _, err := handlers.TaskCreate(&buf, handlers.TaskCreateInput{
			DBPath:    path,
			Namespace: "test",
			Title:     title,
			Type:      provenance.TaskTypeTask,
			Priority:  provenance.PriorityMedium,
			Phase:     provenance.PhaseUnscoped,
		}, types.OutputJSON); err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
	}

	open := provenance.StatusOpen
	var listOut bytes.Buffer
	code, err := handlers.TaskList(&listOut, handlers.TaskListInput{
		DBPath: path,
		Status: &open,
	}, types.OutputText)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(listOut.String(), want) {
			t.Fatalf("expected %q in list output, got %q", want, listOut.String())
		}
	}
}

// extractIDFromJSON parses the "id" field from a single-task JSON document.
// Tests use this rather than json.Unmarshal to keep the assertions tight to
// the wire format we expect (ID is a wire string, not the struct shape).
func extractIDFromJSON(t *testing.T, body string) string {
	t.Helper()
	const key = `"id": "`
	idx := strings.Index(body, key)
	if idx < 0 {
		t.Fatalf("could not find id field in JSON output: %q", body)
	}
	rest := body[idx+len(key):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("malformed id field in JSON output: %q", body)
	}
	return rest[:end]
}
