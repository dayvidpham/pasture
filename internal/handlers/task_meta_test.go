package handlers_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

func TestTaskLabelAddRemove_RoundTrip(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "labelable")

	var addOut bytes.Buffer
	if _, err := handlers.TaskLabelAdd(&addOut, path, id, "important", types.OutputText); err != nil {
		t.Fatalf("label add failed: %v", err)
	}
	if !strings.Contains(addOut.String(), "important") {
		t.Fatalf("expected label in output, got %q", addOut.String())
	}

	var rmOut bytes.Buffer
	if _, err := handlers.TaskLabelRemove(&rmOut, path, id, "important", types.OutputText); err != nil {
		t.Fatalf("label remove failed: %v", err)
	}
	if strings.Contains(rmOut.String(), "important") {
		t.Fatalf("expected label gone after remove, got %q", rmOut.String())
	}
}

func TestTaskLabelAdd_RejectsEmptyLabel(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "x")

	var out bytes.Buffer
	code, err := handlers.TaskLabelAdd(&out, path, id, "", types.OutputText)
	if err == nil {
		t.Fatal("expected validation error for empty label")
	}
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
}

func TestTaskCommentAddAndList_RoundTrip(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "commentable")

	// Register a human agent directly via the tracker — comment add requires a
	// registered author. Mirrors how the future agent ergonomics layer will
	// auto-resolve the CLI user.
	tr, err := tasks.OpenTracker(path)
	if err != nil {
		t.Fatalf("open tracker: %v", err)
	}
	human, err := tr.RegisterHumanAgent("test", "Tester", "tester@example.com")
	if err != nil {
		t.Fatalf("register human agent: %v", err)
	}
	_ = tr.Close()

	var addOut bytes.Buffer
	code, err := handlers.TaskCommentAdd(&addOut, handlers.TaskCommentAddInput{
		DBPath:   path,
		IDStr:    id,
		AuthorID: human.ID.String(),
		Body:     "first thoughts",
	}, types.OutputText)
	if err != nil {
		t.Fatalf("comment add failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(addOut.String(), "first thoughts") {
		t.Fatalf("expected body in output, got %q", addOut.String())
	}

	var listOut bytes.Buffer
	if _, err := handlers.TaskComments(&listOut, path, id, types.OutputText); err != nil {
		t.Fatalf("comments failed: %v", err)
	}
	if !strings.Contains(listOut.String(), "first thoughts") {
		t.Fatalf("expected comment in list, got %q", listOut.String())
	}
}

func TestTaskCommentAdd_RequiresAuthor(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "needs-author")

	var out bytes.Buffer
	code, err := handlers.TaskCommentAdd(&out, handlers.TaskCommentAddInput{
		DBPath: path,
		IDStr:  id,
		Body:   "no author",
	}, types.OutputText)
	if err == nil {
		t.Fatal("expected validation error when author is missing")
	}
	if code != 1 {
		t.Fatalf("expected exit 1 (validation), got %d", code)
	}
}

func TestTaskCommentAdd_RejectsUnknownAuthor(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "ghost-author")

	var out bytes.Buffer
	// Build a syntactically valid AgentID for an agent that does not exist.
	bogus := provenance.AgentID{Namespace: "test"} // zero-UUID
	code, err := handlers.TaskCommentAdd(&out, handlers.TaskCommentAddInput{
		DBPath:   path,
		IDStr:    id,
		AuthorID: bogus.String(),
		Body:     "ghost",
	}, types.OutputText)
	if err == nil {
		t.Fatal("expected workflow error for unknown agent")
	}
	if code != 3 {
		t.Fatalf("expected exit 3 (workflow), got %d", code)
	}
}
