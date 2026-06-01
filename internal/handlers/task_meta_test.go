package handlers_test

import (
	"bytes"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

func TestTaskLabelAddRemove_RoundTrip(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "labelable")

	var addOut bytes.Buffer
	if _, err := handlers.TaskLabelAdd(&addOut, path, id, "important", types.OutputJSON); err != nil {
		t.Fatalf("label add failed: %v", err)
	}
	got := decodeLabels(t, addOut.String())
	if got.TaskId != id {
		t.Errorf("taskId: got %q, want %q", got.TaskId, id)
	}
	if !containsString(got.Labels, "important") {
		t.Errorf("expected 'important' in labels, got %+v", got.Labels)
	}

	var rmOut bytes.Buffer
	if _, err := handlers.TaskLabelRemove(&rmOut, path, id, "important", types.OutputJSON); err != nil {
		t.Fatalf("label remove failed: %v", err)
	}
	got = decodeLabels(t, rmOut.String())
	if containsString(got.Labels, "important") {
		t.Errorf("expected 'important' gone after remove, got %+v", got.Labels)
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
	authorId := mustRegisterAgent(t, path, "Tester", "tester@example.com")

	var addOut bytes.Buffer
	code, err := handlers.TaskCommentAdd(&addOut, handlers.TaskCommentAddInput{
		DBPath:   path,
		IdStr:    id,
		AuthorId: authorId,
		Body:     "first thoughts",
	}, types.OutputJSON)
	if err != nil {
		t.Fatalf("comment add failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	added := decodeComment(t, addOut.String())
	if added.Body != "first thoughts" {
		t.Errorf("body: got %q, want %q", added.Body, "first thoughts")
	}
	if added.AuthorId != authorId {
		t.Errorf("authorId: got %q, want %q", added.AuthorId, authorId)
	}
	if added.TaskId != id {
		t.Errorf("taskId: got %q, want %q", added.TaskId, id)
	}

	var listOut bytes.Buffer
	if _, err := handlers.TaskComments(&listOut, path, id, types.OutputJSON); err != nil {
		t.Fatalf("comments failed: %v", err)
	}
	cs := decodeComments(t, listOut.String())
	if len(cs) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(cs))
	}
	if cs[0].Body != "first thoughts" {
		t.Errorf("body in list: got %q", cs[0].Body)
	}
}

func TestTaskCommentAdd_RequiresAuthor(t *testing.T) {
	path := dbPath(t)
	id := createTask(t, path, "needs-author")

	var out bytes.Buffer
	code, err := handlers.TaskCommentAdd(&out, handlers.TaskCommentAddInput{
		DBPath: path,
		IdStr:  id,
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

	bogus := provenance.AgentID{Namespace: "test"}
	var out bytes.Buffer
	code, err := handlers.TaskCommentAdd(&out, handlers.TaskCommentAddInput{
		DBPath:   path,
		IdStr:    id,
		AuthorId: bogus.String(),
		Body:     "ghost",
	}, types.OutputText)
	if err == nil {
		t.Fatal("expected workflow error for unknown agent")
	}
	if code != 3 {
		t.Fatalf("expected exit 3 (workflow), got %d", code)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
