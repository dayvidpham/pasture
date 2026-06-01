package handlers_test

import (
	"bytes"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

func TestTaskReady_ExcludesBlocked(t *testing.T) {
	path := dbPath(t)

	parentId := createTask(t, path, "parent")
	childId := createTask(t, path, "child")

	// "parent is blocked by child"
	if _, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, parentId, childId, provenance.EdgeBlockedBy, types.OutputText); err != nil {
		t.Fatalf("dep add failed: %v", err)
	}

	var readyOut bytes.Buffer
	if _, err := handlers.TaskReady(&readyOut, path, types.OutputJSON); err != nil {
		t.Fatalf("ready failed: %v", err)
	}
	ready := decodeTaskList(t, readyOut.String())
	if !containsTask(ready, childId) {
		t.Fatalf("expected child %q to be ready, got %+v", childId, ready)
	}
	if containsTask(ready, parentId) {
		t.Fatalf("expected parent %q to not appear in ready list, got %+v", parentId, ready)
	}

	var blockedOut bytes.Buffer
	if _, err := handlers.TaskBlocked(&blockedOut, path, types.OutputJSON); err != nil {
		t.Fatalf("blocked failed: %v", err)
	}
	blocked := decodeTaskList(t, blockedOut.String())
	if !containsTask(blocked, parentId) {
		t.Fatalf("expected parent %q in blocked list, got %+v", parentId, blocked)
	}
}

func TestTaskDepAdd_RejectsCycle(t *testing.T) {
	path := dbPath(t)

	a := createTask(t, path, "A")
	b := createTask(t, path, "B")

	if _, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, a, b, provenance.EdgeBlockedBy, types.OutputText); err != nil {
		t.Fatalf("first dep add failed: %v", err)
	}

	code, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, b, a, provenance.EdgeBlockedBy, types.OutputText)
	if err == nil {
		t.Fatal("expected cycle rejection")
	}
	if code != 3 {
		t.Fatalf("expected exit 3 (workflow), got %d", code)
	}
}

func TestTaskDepAdd_JSONOutput(t *testing.T) {
	path := dbPath(t)
	a := createTask(t, path, "A")
	b := createTask(t, path, "B")

	var out bytes.Buffer
	if _, err := handlers.TaskDepAdd(&out, path, a, b, provenance.EdgeBlockedBy, types.OutputJSON); err != nil {
		t.Fatalf("dep add json: %v", err)
	}
	got := decodeEdge(t, out.String())
	if got.SourceId != a {
		t.Errorf("sourceId: got %q, want %q", got.SourceId, a)
	}
	if got.TargetId != b {
		t.Errorf("targetId: got %q, want %q", got.TargetId, b)
	}
	if got.Kind != "blocked_by" {
		t.Errorf("kind: got %q, want %q", got.Kind, "blocked_by")
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
	code, err := handlers.TaskDepTree(&out, path, root, types.OutputJSON)
	if err != nil {
		t.Fatalf("dep tree failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	tree := decodeDepTree(t, out.String())
	if tree.Root != root {
		t.Errorf("root: got %q, want %q", tree.Root, root)
	}
	if len(tree.Edges) != 3 {
		t.Fatalf("expected 3 edges, got %d (%+v)", len(tree.Edges), tree.Edges)
	}
	for _, want := range [][2]string{{root, c1}, {root, c2}, {c1, gc}} {
		if !containsEdge(tree.Edges, want[0], want[1]) {
			t.Errorf("missing edge %s -> %s in %+v", want[0], want[1], tree.Edges)
		}
	}
}

func TestTaskDepTree_EmptyForLeaf(t *testing.T) {
	path := dbPath(t)
	leaf := createTask(t, path, "leaf")

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, leaf, types.OutputJSON)
	if err != nil {
		t.Fatalf("dep tree failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	tree := decodeDepTree(t, out.String())
	if tree.Root != leaf {
		t.Errorf("root: got %q, want %q", tree.Root, leaf)
	}
	if len(tree.Edges) != 0 {
		t.Errorf("expected zero edges for leaf, got %+v", tree.Edges)
	}
}

func containsTask(list []taskJSONShape, id string) bool {
	for _, t := range list {
		if t.ID == id {
			return true
		}
	}
	return false
}

func containsEdge(edges []edgeJSONShape, src, tgt string) bool {
	for _, e := range edges {
		if e.SourceId == src && e.TargetId == tgt {
			return true
		}
	}
	return false
}
