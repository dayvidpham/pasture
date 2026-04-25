package handlers_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

// createTask is a small helper to keep dep-tests focused on dep logic.
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
	return extractIDFromJSON(t, buf.String())
}

func TestTaskReady_ExcludesBlocked(t *testing.T) {
	path := dbPath(t)

	parentID := createTask(t, path, "parent")
	childID := createTask(t, path, "child")

	// "parent is blocked by child"
	if _, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, parentID, childID, provenance.EdgeBlockedBy, types.OutputText); err != nil {
		t.Fatalf("dep add failed: %v", err)
	}

	var readyOut bytes.Buffer
	if _, err := handlers.TaskReady(&readyOut, path, types.OutputText); err != nil {
		t.Fatalf("ready failed: %v", err)
	}
	body := readyOut.String()
	if !strings.Contains(body, "child") {
		t.Fatalf("expected child to be ready, got %q", body)
	}
	if strings.Contains(body, "parent") {
		t.Fatalf("expected parent to be blocked, got %q", body)
	}

	var blockedOut bytes.Buffer
	if _, err := handlers.TaskBlocked(&blockedOut, path, types.OutputText); err != nil {
		t.Fatalf("blocked failed: %v", err)
	}
	if !strings.Contains(blockedOut.String(), "parent") {
		t.Fatalf("expected parent in blocked list, got %q", blockedOut.String())
	}
}

func TestTaskDepAdd_RejectsCycle(t *testing.T) {
	path := dbPath(t)

	a := createTask(t, path, "A")
	b := createTask(t, path, "B")

	// A blocked by B
	if _, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, a, b, provenance.EdgeBlockedBy, types.OutputText); err != nil {
		t.Fatalf("first dep add failed: %v", err)
	}

	// B blocked by A would form a cycle
	code, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, b, a, provenance.EdgeBlockedBy, types.OutputText)
	if err == nil {
		t.Fatal("expected cycle rejection")
	}
	if code != 3 {
		t.Fatalf("expected exit 3 (workflow), got %d", code)
	}
}

func TestTaskDepTree_RootWithChildren(t *testing.T) {
	path := dbPath(t)

	root := createTask(t, path, "root")
	c1 := createTask(t, path, "c1")
	c2 := createTask(t, path, "c2")
	gc := createTask(t, path, "gc")

	mustAdd := func(src, tgt string) {
		t.Helper()
		if _, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, src, tgt, provenance.EdgeBlockedBy, types.OutputText); err != nil {
			t.Fatalf("dep add %s -> %s failed: %v", src, tgt, err)
		}
	}
	mustAdd(root, c1)
	mustAdd(root, c2)
	mustAdd(c1, gc)

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, root, types.OutputText)
	if err != nil {
		t.Fatalf("dep tree failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	body := out.String()
	if !strings.Contains(body, root) {
		t.Fatalf("expected root in output: %q", body)
	}
	for _, want := range []string{c1, c2, gc} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected child %s in tree output: %q", want, body)
		}
	}
}

func TestTaskDepTree_EmptyForLeaf(t *testing.T) {
	path := dbPath(t)
	leaf := createTask(t, path, "leaf")

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, leaf, types.OutputText)
	if err != nil {
		t.Fatalf("dep tree failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "no blocked-by edges") {
		t.Fatalf("expected leaf message, got %q", out.String())
	}
}
