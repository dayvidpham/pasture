package formatters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/types"
)

type edgeJSON struct {
	SourceId string `json:"sourceId"`
	TargetId string `json:"targetId"`
	Kind     string `json:"kind"`
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
// ourselves. Cycles are broken on second visit (each node prints once).
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
	visited := map[string]bool{}
	var walk func(id string, prefix string, isLast bool, depth int)
	walk = func(id string, prefix string, isLast bool, depth int) {
		marker := "├── "
		nextPrefix := prefix + "│   "
		if isLast {
			marker = "└── "
			nextPrefix = prefix + "    "
		}
		if depth == 0 {
			fmt.Fprintln(&b, id)
		} else {
			fmt.Fprintln(&b, prefix+marker+"blocked by "+id)
		}
		if visited[id] {
			return
		}
		visited[id] = true

		children := adj[id]
		for i, child := range children {
			walk(child, nextPrefix, i == len(children)-1, depth+1)
		}
	}
	walk(rootId, "", true, 0)
	return strings.TrimRight(b.String(), "\n")
}
