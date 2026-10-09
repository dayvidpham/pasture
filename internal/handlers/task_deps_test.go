package handlers_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/types"
)

func TestTaskReady_ExcludesBlocked(t *testing.T) {
	t.Parallel()
	path := dbPath(t)

	parentId := createTask(t, path, "parent")
	childId := createTask(t, path, "child")

	// "parent is blocked by child"
	if _, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, parentId, childId, provenance.EdgeBlockedBy, types.OutputText); err != nil {
		t.Fatalf("dep add failed: %v", err)
	}

	var readyOut bytes.Buffer
	if _, err := handlers.TaskReady(&readyOut, path, types.OutputJSON, ""); err != nil {
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
	if _, err := handlers.TaskBlocked(&blockedOut, path, types.OutputJSON, ""); err != nil {
		t.Fatalf("blocked failed: %v", err)
	}
	blocked := decodeTaskList(t, blockedOut.String())
	if !containsTask(blocked, parentId) {
		t.Fatalf("expected parent %q in blocked list, got %+v", parentId, blocked)
	}
}

func TestTaskDepAdd_RejectsCycle(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	mustAdd(c2, gc)
	if _, err := handlers.TaskClose(&bytes.Buffer{}, path, gc, "done", types.OutputText); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, root, []provenance.EdgeKind{provenance.EdgeBlockedBy}, types.OutputJSON)
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
	if len(tree.Edges) != 4 {
		t.Fatalf("expected 4 edges including both paths to the closed shared blocker, got %d (%+v)", len(tree.Edges), tree.Edges)
	}
	for _, want := range [][2]string{{root, c1}, {root, c2}, {c1, gc}, {c2, gc}} {
		if !containsEdge(tree.Edges, want[0], want[1]) {
			t.Errorf("missing edge %s -> %s in %+v", want[0], want[1], tree.Edges)
		}
	}
}

func TestTaskDepTree_EmptyForLeaf(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	leaf := createTask(t, path, "leaf")

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, leaf, []provenance.EdgeKind{provenance.EdgeBlockedBy}, types.OutputJSON)
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

var allTypedTreeKinds = []provenance.EdgeKind{
	provenance.EdgeBlockedBy,
	provenance.EdgeDerivedFrom,
	provenance.EdgeSupersedes,
	provenance.EdgeDiscoveredFrom,
}

// mustAddEdge adds an edge through the production handler, failing the test on
// any rejection.
func mustAddEdge(t *testing.T, path, src, tgt string, kind provenance.EdgeKind) {
	t.Helper()
	if _, err := handlers.TaskDepAdd(&bytes.Buffer{}, path, src, tgt, kind, types.OutputText); err != nil {
		t.Fatalf("dep add %s -[%s]-> %s failed: %v", src, kind, tgt, err)
	}
}

// scratchTypedFixture builds the cross-kind cycle and diamond used by the tree
// tests: A supersedes B; B derived_from C; C supersedes A; A derived_from D;
// B supersedes D; D discovered_from C; X supersedes A (incoming).
func scratchTypedFixture(t *testing.T, path string) (a, b, c, d, x string) {
	t.Helper()
	a = createTask(t, path, "A")
	b = createTask(t, path, "B")
	c = createTask(t, path, "C")
	d = createTask(t, path, "D")
	x = createTask(t, path, "X")
	mustAddEdge(t, path, a, b, provenance.EdgeSupersedes)
	mustAddEdge(t, path, b, c, provenance.EdgeDerivedFrom)
	mustAddEdge(t, path, c, a, provenance.EdgeSupersedes)
	mustAddEdge(t, path, a, d, provenance.EdgeDerivedFrom)
	mustAddEdge(t, path, b, d, provenance.EdgeSupersedes)
	mustAddEdge(t, path, d, c, provenance.EdgeDiscoveredFrom)
	mustAddEdge(t, path, x, a, provenance.EdgeSupersedes)
	return a, b, c, d, x
}

func TestTaskDepTree_TypedAllKindsScratch(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	a, b, c, d, x := scratchTypedFixture(t, path)

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, a, allTypedTreeKinds, types.OutputText)
	if err != nil {
		t.Fatalf("dep tree text failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	wantText := a + "\n" +
		"    ├── derived from " + d + "\n" +
		"    │   └── discovered from " + c + "\n" +
		"    │       └── supersedes " + a + " (already shown)\n" +
		"    └── supersedes " + b + "\n" +
		"        ├── derived from " + c + " (already shown)\n" +
		"        └── supersedes " + d + " (already shown)"
	if out.String() != wantText+"\n" {
		t.Fatalf("text mismatch:\n--- got ---\n%s\n--- want ---\n%s", out.String(), wantText)
	}
	if strings.Contains(out.String(), x) {
		t.Fatalf("incoming edge source %s must never appear:\n%s", x, out.String())
	}

	out.Reset()
	code, err = handlers.TaskDepTree(&out, path, a, allTypedTreeKinds, types.OutputJSON)
	if err != nil {
		t.Fatalf("dep tree json failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	tree := decodeDepTree(t, out.String())
	want := []edgeJSONShape{
		{SourceId: a, TargetId: d, Kind: "derived_from"},
		{SourceId: d, TargetId: c, Kind: "discovered_from"},
		{SourceId: c, TargetId: a, Kind: "supersedes", Repeated: true},
		{SourceId: a, TargetId: b, Kind: "supersedes"},
		{SourceId: b, TargetId: c, Kind: "derived_from", Repeated: true},
		{SourceId: b, TargetId: d, Kind: "supersedes", Repeated: true},
	}
	if len(tree.Edges) != len(want) {
		t.Fatalf("edge count: got %d want %d (%+v)", len(tree.Edges), len(want), tree.Edges)
	}
	for i, w := range want {
		if tree.Edges[i] != w {
			t.Errorf("edge %d: got %+v want %+v", i, tree.Edges[i], w)
		}
	}
}

func TestTaskDepTree_SingleKindExcludesOtherKinds(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	a, b, _, d, _ := scratchTypedFixture(t, path)

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, a, []provenance.EdgeKind{provenance.EdgeSupersedes}, types.OutputText)
	if err != nil {
		t.Fatalf("dep tree supersedes failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	wantText := a + "\n" +
		"    └── supersedes " + b + "\n" +
		"        └── supersedes " + d
	if out.String() != wantText+"\n" {
		t.Fatalf("text mismatch:\n--- got ---\n%s\n--- want ---\n%s", out.String(), wantText)
	}
	if strings.Contains(out.String(), "derived from") || strings.Contains(out.String(), "discovered from") {
		t.Fatalf("single kind must not expand into other kinds:\n%s", out.String())
	}

	out.Reset()
	_, err = handlers.TaskDepTree(&out, path, a, []provenance.EdgeKind{provenance.EdgeDerivedFrom}, types.OutputJSON)
	if err != nil {
		t.Fatalf("dep tree derived_from failed: %v", err)
	}
	tree := decodeDepTree(t, out.String())
	if len(tree.Edges) != 1 {
		t.Fatalf("derived_from from A should reach only D, got %+v", tree.Edges)
	}
	if tree.Edges[0].Kind != "derived_from" || tree.Edges[0].TargetId != d {
		t.Fatalf("unexpected derived_from edge: %+v", tree.Edges[0])
	}
}

func TestTaskDepTree_BlockedByOnlyLegacyBytes(t *testing.T) {
	t.Parallel()
	path := dbPath(t)

	e := createTask(t, path, "E")
	f := createTask(t, path, "F")
	g := createTask(t, path, "G")
	h := createTask(t, path, "H")
	mustAddEdge(t, path, e, f, provenance.EdgeBlockedBy)
	mustAddEdge(t, path, e, g, provenance.EdgeBlockedBy)
	mustAddEdge(t, path, f, h, provenance.EdgeBlockedBy)
	mustAddEdge(t, path, g, h, provenance.EdgeBlockedBy)

	wantText := e + "\n" +
		"    ├── blocked by " + f + "\n" +
		"    │   └── blocked by " + h + "\n" +
		"    └── blocked by " + g + "\n" +
		"        └── blocked by " + h

	var defaultText, explicitText bytes.Buffer
	if _, err := handlers.TaskDepTree(&defaultText, path, e, []provenance.EdgeKind{provenance.EdgeBlockedBy}, types.OutputText); err != nil {
		t.Fatalf("legacy text failed: %v", err)
	}
	if _, err := handlers.TaskDepTree(&explicitText, path, e, []provenance.EdgeKind{provenance.EdgeBlockedBy}, types.OutputText); err != nil {
		t.Fatalf("explicit text failed: %v", err)
	}
	if defaultText.String() != explicitText.String() {
		t.Fatalf("default and explicit blocked_by differ:\n%s\n%s", defaultText.String(), explicitText.String())
	}
	if defaultText.String() != wantText+"\n" {
		t.Fatalf("legacy text mismatch:\n--- got ---\n%s\n--- want ---\n%s", defaultText.String(), wantText)
	}

	var legacyJSON bytes.Buffer
	if _, err := handlers.TaskDepTree(&legacyJSON, path, e, []provenance.EdgeKind{provenance.EdgeBlockedBy}, types.OutputJSON); err != nil {
		t.Fatalf("legacy json failed: %v", err)
	}
	if strings.Contains(legacyJSON.String(), "repeated") {
		t.Fatalf("legacy blocked_by JSON must not carry a repeat flag:\n%s", legacyJSON.String())
	}
	legacy := decodeDepTree(t, legacyJSON.String())
	wantPairs := [][2]string{{e, f}, {f, h}, {e, g}, {g, h}}
	if len(legacy.Edges) != len(wantPairs) {
		t.Fatalf("legacy edge count: got %d want %d", len(legacy.Edges), len(wantPairs))
	}
	for i, w := range wantPairs {
		if legacy.Edges[i].SourceId != w[0] || legacy.Edges[i].TargetId != w[1] {
			t.Errorf("legacy edge %d: got %s->%s want %s->%s", i, legacy.Edges[i].SourceId, legacy.Edges[i].TargetId, w[0], w[1])
		}
	}

	// --kind all on a blocked_by-only store must reach the same edges.
	var allJSON bytes.Buffer
	if _, err := handlers.TaskDepTree(&allJSON, path, e, allTypedTreeKinds, types.OutputJSON); err != nil {
		t.Fatalf("all-kinds json failed: %v", err)
	}
	all := decodeDepTree(t, allJSON.String())
	if len(all.Edges) != len(legacy.Edges) {
		t.Fatalf("all-kinds edge count on a blocked_by-only store: got %d want %d", len(all.Edges), len(legacy.Edges))
	}
	for i := range all.Edges {
		if all.Edges[i].SourceId != legacy.Edges[i].SourceId || all.Edges[i].TargetId != legacy.Edges[i].TargetId {
			t.Errorf("all-kinds edge %d: got %s->%s want %s->%s", i, all.Edges[i].SourceId, all.Edges[i].TargetId, legacy.Edges[i].SourceId, legacy.Edges[i].TargetId)
		}
	}
}

// A non-blocking typed edge must leave readiness unchanged. The source is
// otherwise ready, and the before/after sets are compared per kind.
func TestTaskDepTree_TypedEdgesDoNotChangeReadiness(t *testing.T) {
	t.Parallel()

	for _, kind := range []provenance.EdgeKind{
		provenance.EdgeDerivedFrom,
		provenance.EdgeSupersedes,
		provenance.EdgeDiscoveredFrom,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()
			path := dbPath(t)

			src := createTask(t, path, "src")
			tgt := createTask(t, path, "tgt")

			before := readyTaskSet(t, path)
			if !before[src] || !before[tgt] {
				t.Fatalf("fixture must start ready: %+v", before)
			}

			mustAddEdge(t, path, src, tgt, kind)

			after := readyTaskSet(t, path)
			if !after[src] {
				t.Fatalf("a non-blocking %s edge must not block its source: %+v", kind, after)
			}
			if len(after) != len(before) {
				t.Fatalf("readiness set changed size after a %s edge: before=%v after=%v", kind, before, after)
			}
			for id := range before {
				if !after[id] {
					t.Fatalf("task %s left the ready set after a %s edge", id, kind)
				}
			}
		})
	}
}

// readyTaskSet runs the production ready handler and returns the set of IDs it
// reports as ready.
func readyTaskSet(t *testing.T, path string) map[string]bool {
	t.Helper()
	var out bytes.Buffer
	if _, err := handlers.TaskReady(&out, path, types.OutputJSON, ""); err != nil {
		t.Fatalf("ready failed: %v", err)
	}
	set := map[string]bool{}
	for _, task := range decodeTaskList(t, out.String()) {
		set[task.ID] = true
	}
	return set
}

func TestTaskDepTree_NonTaskTargetRendersAsLeaf(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	a := createTask(t, path, "A")
	mustAddEdge(t, path, a, "not-a-task-identity", provenance.EdgeSupersedes)

	var out bytes.Buffer
	code, err := handlers.TaskDepTree(&out, path, a, []provenance.EdgeKind{provenance.EdgeSupersedes}, types.OutputText)
	if err != nil {
		t.Fatalf("dep tree failed: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	want := a + "\n    └── supersedes not-a-task-identity\n"
	if out.String() != want {
		t.Fatalf("text mismatch:\n--- got ---\n%s\n--- want ---\n%s", out.String(), want)
	}
}
