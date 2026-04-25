package handlers_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// dbPath returns a fresh SQLite path under a temp dir. We use file-backed
// SQLite (not in-memory) because each handler call opens its own tracker; an
// in-memory DB would not be shared across calls.
func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "pasture.db")
}

// taskJSONShape mirrors the formatter's JSON wire shape. Tests decode into
// this rather than substring-grepping the marshalled output, so changes to
// pretty-print spacing or key order do not falsely fail tests.
type taskJSONShape struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	Type        string `json:"type"`
	Phase       string `json:"phase"`
	Owner       string `json:"owner,omitempty"`
	Notes       string `json:"notes,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	ClosedAt    string `json:"closedAt,omitempty"`
	CloseReason string `json:"closeReason,omitempty"`
}

// commentJSONShape mirrors the comment formatter's JSON wire shape.
type commentJSONShape struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	AuthorID  string `json:"authorId"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

// labelsJSONShape mirrors the labels formatter's JSON wire shape.
type labelsJSONShape struct {
	TaskID string   `json:"taskId"`
	Labels []string `json:"labels"`
}

// edgeJSONShape mirrors the edge formatter's JSON wire shape.
type edgeJSONShape struct {
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
	Kind     string `json:"kind"`
}

// depTreeJSONShape mirrors the dep tree formatter's JSON wire shape.
type depTreeJSONShape struct {
	Root  string          `json:"root"`
	Edges []edgeJSONShape `json:"edges"`
}

// decodeTask decodes the JSON output of a TaskCreate / TaskShow / TaskUpdate
// / TaskClose call into a typed struct. Fails the test if decoding fails.
func decodeTask(t *testing.T, body string) taskJSONShape {
	t.Helper()
	var got taskJSONShape
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode task json: %v\nbody:\n%s", err, body)
	}
	return got
}

// decodeTaskList decodes the JSON output of a TaskList call.
func decodeTaskList(t *testing.T, body string) []taskJSONShape {
	t.Helper()
	var got []taskJSONShape
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode task list json: %v\nbody:\n%s", err, body)
	}
	return got
}

// decodeComment decodes a single-comment JSON document.
func decodeComment(t *testing.T, body string) commentJSONShape {
	t.Helper()
	var got commentJSONShape
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode comment json: %v\nbody:\n%s", err, body)
	}
	return got
}

// decodeComments decodes a comments-array JSON document.
func decodeComments(t *testing.T, body string) []commentJSONShape {
	t.Helper()
	var got []commentJSONShape
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode comments json: %v\nbody:\n%s", err, body)
	}
	return got
}

// decodeLabels decodes a labels JSON document.
func decodeLabels(t *testing.T, body string) labelsJSONShape {
	t.Helper()
	var got labelsJSONShape
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode labels json: %v\nbody:\n%s", err, body)
	}
	return got
}

// decodeEdge decodes an edge JSON document.
func decodeEdge(t *testing.T, body string) edgeJSONShape {
	t.Helper()
	var got edgeJSONShape
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode edge json: %v\nbody:\n%s", err, body)
	}
	return got
}

// decodeDepTree decodes a dep tree JSON document.
func decodeDepTree(t *testing.T, body string) depTreeJSONShape {
	t.Helper()
	var got depTreeJSONShape
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode dep tree json: %v\nbody:\n%s", err, body)
	}
	return got
}

// createTask creates a default task and returns its wire-format ID. Used by
// tests that don't care about the create-time fields beyond the ID.
func createTask(t *testing.T, dbPath, title string) string {
	t.Helper()
	var buf bytes.Buffer
	code, err := handlers.TaskCreate(&buf, handlers.TaskCreateInput{
		DBPath:    dbPath,
		Namespace: "test",
		Title:     title,
		Type:      provenance.TaskTypeTask,
		Priority:  provenance.PriorityMedium,
		Phase:     provenance.PhaseUnscoped,
	}, types.OutputJSON)
	if err != nil {
		t.Fatalf("createTask(%q) failed: %v (code=%d)", title, err, code)
	}
	return decodeTask(t, buf.String()).ID
}

// mustRegisterAgent opens a tracker against dbPath, registers a human agent,
// and returns the agent's wire-format ID. Tests use this to seed an author
// before exercising comment-related handlers.
func mustRegisterAgent(t *testing.T, dbPath, name, contact string) string {
	t.Helper()
	tr, err := tasks.OpenTracker(dbPath)
	if err != nil {
		t.Fatalf("open tracker for agent register: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	human, err := tr.RegisterHumanAgent("test", name, contact)
	if err != nil {
		t.Fatalf("register human agent: %v", err)
	}
	if cErr := tr.Close(); cErr != nil {
		t.Fatalf("close tracker after register: %v", cErr)
	}
	return human.ID.String()
}
