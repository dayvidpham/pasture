package formatters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/types"
)

// markedEdge pairs a traversed edge with whether its target had already been
// shown earlier in the same ordered traversal. The repeat marker and the
// repeat JSON flag both derive from this one value, so text and JSON can never
// disagree about which occurrence is the repeat.
type markedEdge struct {
	edge     provenance.Edge
	repeated bool
}

// FormatTypedTree renders a typed relation tree for a non-legacy selection: a
// single non-blocked_by kind, or every kind. The legacy blocked_by-only mode
// keeps its own path (FormatDepTree) so its bytes never change.
//
// The input edges must already be in the traversal order produced by the
// handler walk (kind-major, then target ID ascending, DFS pre-order, repeats
// included). Text and JSON consume the same marked list, so edge order and
// repeat markers always agree.
func FormatTypedTree(rootId string, edges []provenance.Edge, kinds []provenance.EdgeKind, format types.OutputFormat) (string, error) {
	marked := markRepeats(rootId, edges)
	switch format {
	case types.OutputJSON:
		jt := depTreeJSON{Root: rootId, Edges: make([]edgeJSON, len(marked))}
		for i, m := range marked {
			jt.Edges[i] = edgeJSON{
				SourceId: m.edge.SourceID,
				TargetId: m.edge.TargetID,
				Kind:     m.edge.Kind.String(),
				Repeated: m.repeated,
			}
		}
		b, err := json.MarshalIndent(jt, "", "  ")
		if err != nil {
			return "", fmt.Errorf("formatters.FormatTypedTree: marshal failed: %w", err)
		}
		return string(b), nil

	case types.OutputText:
		return renderTypedTreeText(rootId, marked, kinds), nil

	default:
		return "", fmt.Errorf("formatters.FormatTypedTree: unknown output format %q — valid values: json, text", format)
	}
}

// markRepeats walks the ordered edge list once. A node is "shown" when it is
// the root or when it first appears as an edge target; every later edge into a
// shown target is marked repeated. The first occurrence is never marked, so a
// caller can read a repeated edge as "this target was already displayed".
func markRepeats(rootId string, edges []provenance.Edge) []markedEdge {
	shown := map[string]bool{rootId: true}
	marked := make([]markedEdge, len(edges))
	for i, e := range edges {
		marked[i] = markedEdge{edge: e, repeated: shown[e.TargetID]}
		shown[e.TargetID] = true
	}
	return marked
}

// renderTypedTreeText rebuilds adjacency from the ordered marked list and
// prints an indented tree. Each node expands at most once; a repeated target
// prints as a marked leaf. The frame-stack and prefix style mirror
// renderDepTreeText so both tree renderers look alike.
func renderTypedTreeText(rootId string, marked []markedEdge, kinds []provenance.EdgeKind) string {
	if len(marked) == 0 {
		return rootId + " " + emptyTypedMessage(kinds)
	}

	adj := map[string][]markedEdge{}
	for _, m := range marked {
		adj[m.edge.SourceID] = append(adj[m.edge.SourceID], m)
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
		m := children[f.next]
		isLast := f.next == len(children)-1
		f.next++
		prefix := f.prefix
		marker := "├── "
		nextPrefix := prefix + "│   "
		if isLast {
			marker = "└── "
			nextPrefix = prefix + "    "
		}
		line := prefix + marker + typedKindLabel(m.edge.Kind) + " " + m.edge.TargetID
		if m.repeated {
			line += " (already shown)"
		}
		fmt.Fprintln(&b, line)
		if visited[m.edge.TargetID] {
			continue
		}
		visited[m.edge.TargetID] = true
		stack = append(stack, frame{id: m.edge.TargetID, prefix: nextPrefix})
	}
	return strings.TrimRight(b.String(), "\n")
}

// typedKindLabel is the text label for a task-to-task relation kind. The four
// task kinds are the only ones a tree traverses; any other value falls back to
// the wire name so a future kind is at least legible.
func typedKindLabel(kind provenance.EdgeKind) string {
	switch kind {
	case provenance.EdgeBlockedBy:
		return "blocked by"
	case provenance.EdgeDerivedFrom:
		return "derived from"
	case provenance.EdgeSupersedes:
		return "supersedes"
	case provenance.EdgeDiscoveredFrom:
		return "discovered from"
	default:
		return kind.String()
	}
}

// emptyTypedMessage is the root line's suffix when a selection finds no edges.
// A single kind names itself; a multi-kind selection says only that no typed
// edges were found.
func emptyTypedMessage(kinds []provenance.EdgeKind) string {
	if len(kinds) == 1 {
		switch kinds[0] {
		case provenance.EdgeBlockedBy:
			return "(no blocked-by edges)"
		case provenance.EdgeDerivedFrom:
			return "(no derived-from edges)"
		case provenance.EdgeSupersedes:
			return "(no supersedes edges)"
		case provenance.EdgeDiscoveredFrom:
			return "(no discovered-from edges)"
		}
	}
	return "(no typed edges)"
}
