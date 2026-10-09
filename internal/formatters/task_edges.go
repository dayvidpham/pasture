package formatters

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/types"
)

type edgeJSON struct {
	SourceId string `json:"sourceId"`
	TargetId string `json:"targetId"`
	Kind     string `json:"kind"`
	// Repeated marks an edge whose target was already shown earlier in a typed
	// traversal. It is omitempty so the legacy blocked_by-only output (and the
	// single-edge confirmation from `task dep add`) stays byte-identical.
	Repeated bool `json:"repeated,omitempty"`
}

type depTreeJSON struct {
	Root  string     `json:"root"`
	Edges []edgeJSON `json:"edges"`
}

// FormatEdge renders a single edge in the requested output format. Used by
// `task dep add` to confirm the just-created edge.
func FormatEdge(e provenance.Edge, format types.OutputFormat) (string, error) {
	switch format {
	case types.OutputJSON:
		b, err := json.MarshalIndent(edgeJSON{
			SourceId: e.SourceID,
			TargetId: e.TargetID,
			Kind:     e.Kind.String(),
		}, "", "  ")
		if err != nil {
			return "", fmt.Errorf("formatters.FormatEdge: marshal failed: %w", err)
		}
		return string(b), nil
	case types.OutputText:
		return fmt.Sprintf("added edge: %s --[%s]--> %s", e.SourceID, e.Kind, e.TargetID), nil
	default:
		return "", fmt.Errorf("formatters.FormatEdge: unknown output format %q — valid values: json, text", format)
	}
}

// FormatDepTree renders the blocked-by edges reachable from rootId. JSON
// output preserves the DFS-ordered edge list so consumers can rebuild the
// tree. Text output renders an indented tree, deduplicating shared subtrees
// the same way DFS visits them.
func FormatDepTree(rootId string, edges []provenance.Edge, format types.OutputFormat) (string, error) {
	edges = normalizeDepTree(rootId, edges)
	switch format {
	case types.OutputJSON:
		jt := depTreeJSON{Root: rootId, Edges: make([]edgeJSON, len(edges))}
		for i, e := range edges {
			jt.Edges[i] = edgeJSON{SourceId: e.SourceID, TargetId: e.TargetID, Kind: e.Kind.String()}
		}
		b, err := json.MarshalIndent(jt, "", "  ")
		if err != nil {
			return "", fmt.Errorf("formatters.FormatDepTree: marshal failed: %w", err)
		}
		return string(b), nil

	case types.OutputText:
		return renderDepTreeText(rootId, edges), nil

	default:
		return "", fmt.Errorf("formatters.FormatDepTree: unknown output format %q — valid values: json, text", format)
	}
}

// renderDepTreeText walks the edge list and produces an indented blocked-by
// tree starting at rootId.
//
// The Tracker returns edges in DFS order, but does not group them by parent
// — we rebuild adjacency from the raw list and then print depth-first
// ourselves. Repeated targets print as leaves; each node is expanded once.
func renderDepTreeText(rootId string, edges []provenance.Edge) string {
	if len(edges) == 0 {
		return rootId + " (no blocked-by edges)"
	}

	adj := map[string][]string{}
	for _, e := range edges {
		if e.Kind == provenance.EdgeBlockedBy {
			adj[e.SourceID] = append(adj[e.SourceID], e.TargetID)
		}
	}

	var b strings.Builder
	visited := map[string]bool{rootId: true}
	fmt.Fprintln(&b, rootId)
	type frame struct {
		id, prefix string
		next       int
	}
	stack := []frame{{id: rootId, prefix: "    "}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		children := adj[f.id]
		if f.next == len(children) {
			stack = stack[:len(stack)-1]
			continue
		}
		id := children[f.next]
		isLast := f.next == len(children)-1
		f.next++
		prefix := f.prefix
		marker := "├── "
		nextPrefix := prefix + "│   "
		if isLast {
			marker = "└── "
			nextPrefix = prefix + "    "
		}
		fmt.Fprintln(&b, prefix+marker+"blocked by "+id)
		if visited[id] {
			continue
		}
		visited[id] = true

		stack = append(stack, frame{id: id, prefix: nextPrefix})
	}
	return strings.TrimRight(b.String(), "\n")
}

// Normalize the finite collected graph once for both output formats.
func normalizeDepTree(root string, edges []provenance.Edge) []provenance.Edge {
	adj := map[string][]provenance.Edge{}
	seen := map[struct{ source, target string }]bool{}
	for _, e := range edges {
		if e.Kind != provenance.EdgeBlockedBy {
			continue
		}
		key := struct{ source, target string }{e.SourceID, e.TargetID}
		if seen[key] {
			continue
		}
		seen[key] = true
		adj[e.SourceID] = append(adj[e.SourceID], e)
	}
	for _, children := range adj {
		sort.Slice(children, func(i, j int) bool {
			return children[i].TargetID < children[j].TargetID
		})
	}
	visited := map[string]bool{root: true}
	type frame struct {
		id   string
		next int
	}
	stack := []frame{{id: root}}
	out := make([]provenance.Edge, 0, len(edges))
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.next == len(adj[f.id]) {
			stack = stack[:len(stack)-1]
			continue
		}
		e := adj[f.id][f.next]
		f.next++
		out = append(out, e)
		if !visited[e.TargetID] {
			visited[e.TargetID] = true
			stack = append(stack, frame{id: e.TargetID})
		}
	}
	return out
}
