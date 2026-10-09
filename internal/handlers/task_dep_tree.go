package handlers

import (
	"fmt"
	"sort"

	"github.com/dayvidpham/provenance"
)

// canonicalTreeKinds fixes the traversal order for typed relation trees:
// blocked_by, derived_from, supersedes, discovered_from. A selection is
// normalized into this order and deduped before any fetch.
var canonicalTreeKinds = []provenance.EdgeKind{
	provenance.EdgeBlockedBy,
	provenance.EdgeDerivedFrom,
	provenance.EdgeSupersedes,
	provenance.EdgeDiscoveredFrom,
}

// edgeFetcher reads the outgoing edges of source for one kind. Production
// wiring passes the tracker's Edges method; tests pass a fake.
type edgeFetcher func(source provenance.TaskID, kind provenance.EdgeKind) ([]provenance.Edge, error)

// normalizeTreeKinds puts a selection into canonical order and removes
// duplicates. Kinds outside the task-to-task set are dropped rather than
// rejected: the CLI owns the allowlist, so the walk only ever sees task kinds.
func normalizeTreeKinds(kinds []provenance.EdgeKind) []provenance.EdgeKind {
	selected := make(map[provenance.EdgeKind]bool, len(kinds))
	for _, kind := range kinds {
		selected[kind] = true
	}
	out := make([]provenance.EdgeKind, 0, len(canonicalTreeKinds))
	for _, kind := range canonicalTreeKinds {
		if selected[kind] {
			out = append(out, kind)
		}
	}
	return out
}

// isBlockedByOnly reports whether a normalized selection is exactly the legacy
// blocked_by kind, which the handler renders through the legacy path.
func isBlockedByOnly(kinds []provenance.EdgeKind) bool {
	return len(kinds) == 1 && kinds[0] == provenance.EdgeBlockedBy
}

// collectTypedTree walks the outgoing typed edges reachable from root in a
// deterministic, finite order and returns every traversed edge in DFS
// pre-order, including repeated edges. Rules:
//
//   - kinds are normalized to canonical order before the walk;
//   - each node expands at most once, so cycles and diamonds terminate;
//   - a node's children are fetched per kind in canonical order, deduped by
//     target within a kind, and sorted by target ID ascending;
//   - a target is expanded only when it parses as a task ID, so a non-task
//     target (legal via `dep add --target`) renders as a leaf;
//   - a fetch failure is wrapped with the node and kind and fails the walk.
func collectTypedTree(root string, kinds []provenance.EdgeKind, fetch edgeFetcher) ([]provenance.Edge, error) {
	ordered := normalizeTreeKinds(kinds)
	shown := map[string]bool{root: true}
	out := make([]provenance.Edge, 0)

	type frame struct {
		id       string
		children []provenance.Edge
		fetched  bool
		next     int
	}
	stack := []frame{{id: root}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if !f.fetched {
			children, err := fetchTreeChildren(f.id, ordered, fetch)
			if err != nil {
				return nil, err
			}
			f.children = children
			f.fetched = true
		}
		if f.next == len(f.children) {
			stack = stack[:len(stack)-1]
			continue
		}
		edge := f.children[f.next]
		f.next++
		out = append(out, edge)
		if shown[edge.TargetID] {
			continue
		}
		shown[edge.TargetID] = true
		if _, err := provenance.ParseTaskID(edge.TargetID); err != nil {
			continue
		}
		stack = append(stack, frame{id: edge.TargetID})
	}
	return out, nil
}

// fetchTreeChildren reads and orders one node's children: per kind in the
// given order, deduped by target within a kind, sorted by target ID ascending.
func fetchTreeChildren(node string, kinds []provenance.EdgeKind, fetch edgeFetcher) ([]provenance.Edge, error) {
	id, err := provenance.ParseTaskID(node)
	if err != nil {
		// A non-task node is never expanded by collectTypedTree; the root is
		// parsed by the handler. This guard is defensive.
		return nil, nil
	}
	children := make([]provenance.Edge, 0)
	for _, kind := range kinds {
		edges, err := fetch(id, kind)
		if err != nil {
			return nil, fmt.Errorf("dep tree: read %s edges of %s: %w", kind, node, err)
		}
		seen := make(map[string]bool, len(edges))
		kindEdges := make([]provenance.Edge, 0, len(edges))
		for _, e := range edges {
			if seen[e.TargetID] {
				continue
			}
			seen[e.TargetID] = true
			kindEdges = append(kindEdges, e)
		}
		sort.Slice(kindEdges, func(i, j int) bool {
			return kindEdges[i].TargetID < kindEdges[j].TargetID
		})
		children = append(children, kindEdges...)
	}
	return children, nil
}
