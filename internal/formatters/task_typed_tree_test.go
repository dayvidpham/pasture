package formatters_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

// scratchTypedEdges is the ordered edge list a typed walk emits for the scratch
// scenario (root A): A supersedes B; B derived_from C; C supersedes A (cycle);
// A derived_from D; B supersedes D (diamond); D discovered_from C.
var scratchTypedEdges = []provenance.Edge{
	{SourceID: "A", TargetID: "D", Kind: provenance.EdgeDerivedFrom},
	{SourceID: "D", TargetID: "C", Kind: provenance.EdgeDiscoveredFrom},
	{SourceID: "C", TargetID: "A", Kind: provenance.EdgeSupersedes},
	{SourceID: "A", TargetID: "B", Kind: provenance.EdgeSupersedes},
	{SourceID: "B", TargetID: "C", Kind: provenance.EdgeDerivedFrom},
	{SourceID: "B", TargetID: "D", Kind: provenance.EdgeSupersedes},
}

var allTreeKinds = []provenance.EdgeKind{
	provenance.EdgeBlockedBy,
	provenance.EdgeDerivedFrom,
	provenance.EdgeSupersedes,
	provenance.EdgeDiscoveredFrom,
}

type typedTreeJSON struct {
	Root  string `json:"root"`
	Edges []struct {
		SourceID string `json:"sourceId"`
		TargetID string `json:"targetId"`
		Kind     string `json:"kind"`
		Repeated bool   `json:"repeated"`
	} `json:"edges"`
}

func decodeTypedTree(t *testing.T, body string) typedTreeJSON {
	t.Helper()
	var got typedTreeJSON
	require.NoError(t, json.Unmarshal([]byte(body), &got), "body:\n%s", body)
	return got
}

func TestFormatTypedTree_AllKindsTextAndJSON(t *testing.T) {
	t.Parallel()

	const wantText = "A\n" +
		"    ├── derived from D\n" +
		"    │   └── discovered from C\n" +
		"    │       └── supersedes A (already shown)\n" +
		"    └── supersedes B\n" +
		"        ├── derived from C (already shown)\n" +
		"        └── supersedes D (already shown)"

	text, err := formatters.FormatTypedTree("A", scratchTypedEdges, allTreeKinds, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, wantText, text)

	raw, err := formatters.FormatTypedTree("A", scratchTypedEdges, allTreeKinds, types.OutputJSON)
	require.NoError(t, err)
	got := decodeTypedTree(t, raw)
	require.Equal(t, "A", got.Root)

	type edgeWant struct {
		source, target, kind string
		repeated             bool
	}
	want := []edgeWant{
		{"A", "D", "derived_from", false},
		{"D", "C", "discovered_from", false},
		{"C", "A", "supersedes", true},
		{"A", "B", "supersedes", false},
		{"B", "C", "derived_from", true},
		{"B", "D", "supersedes", true},
	}
	require.Len(t, got.Edges, len(want))
	for i, w := range want {
		require.Equal(t, w.source, got.Edges[i].SourceID, "edge %d source", i)
		require.Equal(t, w.target, got.Edges[i].TargetID, "edge %d target", i)
		require.Equal(t, w.kind, got.Edges[i].Kind, "edge %d kind", i)
		require.Equal(t, w.repeated, got.Edges[i].Repeated, "edge %d repeated", i)
	}

	// Text and JSON must agree on order: each edge's label appears in the same
	// sequence as the JSON edges.
	labelByKind := map[string]string{
		"blocked_by":      "blocked by",
		"derived_from":    "derived from",
		"supersedes":      "supersedes",
		"discovered_from": "discovered from",
	}
	cursor := 0
	for _, e := range got.Edges {
		needle := labelByKind[e.Kind] + " " + e.TargetID
		idx := strings.Index(text[cursor:], needle)
		require.GreaterOrEqual(t, idx, 0, "text is missing %q after offset %d", needle, cursor)
		cursor += idx + len(needle)
	}
}

func TestFormatTypedTree_SingleKindLabelsAndMarkers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		kinds []provenance.EdgeKind
		edges []provenance.Edge
		want  string
	}{
		{
			name:  "supersedes",
			kinds: []provenance.EdgeKind{provenance.EdgeSupersedes},
			edges: []provenance.Edge{
				{SourceID: "A", TargetID: "B", Kind: provenance.EdgeSupersedes},
				{SourceID: "B", TargetID: "D", Kind: provenance.EdgeSupersedes},
			},
			want: "A\n    └── supersedes B\n        └── supersedes D",
		},
		{
			name:  "derived_from",
			kinds: []provenance.EdgeKind{provenance.EdgeDerivedFrom},
			edges: []provenance.Edge{
				{SourceID: "A", TargetID: "D", Kind: provenance.EdgeDerivedFrom},
			},
			want: "A\n    └── derived from D",
		},
		{
			name:  "discovered_from",
			kinds: []provenance.EdgeKind{provenance.EdgeDiscoveredFrom},
			edges: []provenance.Edge{
				{SourceID: "A", TargetID: "C", Kind: provenance.EdgeDiscoveredFrom},
			},
			want: "A\n    └── discovered from C",
		},
		{
			name:  "blocked_by label",
			kinds: []provenance.EdgeKind{provenance.EdgeBlockedBy},
			edges: []provenance.Edge{
				{SourceID: "A", TargetID: "B", Kind: provenance.EdgeBlockedBy},
			},
			want: "A\n    └── blocked by B",
		},
		{
			name:  "single kind cycle marks root",
			kinds: []provenance.EdgeKind{provenance.EdgeSupersedes},
			edges: []provenance.Edge{
				{SourceID: "A", TargetID: "B", Kind: provenance.EdgeSupersedes},
				{SourceID: "B", TargetID: "A", Kind: provenance.EdgeSupersedes},
			},
			want: "A\n    └── supersedes B\n        └── supersedes A (already shown)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := formatters.FormatTypedTree("A", tc.edges, tc.kinds, types.OutputText)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestFormatTypedTree_EmptyMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		kinds []provenance.EdgeKind
		want  string
	}{
		{"blocked_by", []provenance.EdgeKind{provenance.EdgeBlockedBy}, "A (no blocked-by edges)"},
		{"derived_from", []provenance.EdgeKind{provenance.EdgeDerivedFrom}, "A (no derived-from edges)"},
		{"supersedes", []provenance.EdgeKind{provenance.EdgeSupersedes}, "A (no supersedes edges)"},
		{"discovered_from", []provenance.EdgeKind{provenance.EdgeDiscoveredFrom}, "A (no discovered-from edges)"},
		{"all", allTreeKinds, "A (no typed edges)"},
		{"empty selection", nil, "A (no typed edges)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := formatters.FormatTypedTree("A", nil, tc.kinds, types.OutputText)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			// JSON stays an empty array in every mode.
			raw, err := formatters.FormatTypedTree("A", nil, tc.kinds, types.OutputJSON)
			require.NoError(t, err)
			require.Contains(t, raw, `"edges": []`)
		})
	}
}

func TestFormatTypedTree_JSONRepeatedIsOmitempty(t *testing.T) {
	t.Parallel()

	raw, err := formatters.FormatTypedTree("A", scratchTypedEdges, allTreeKinds, types.OutputJSON)
	require.NoError(t, err)

	// Exactly the three repeated edges carry the flag.
	require.Equal(t, 3, strings.Count(raw, `"repeated": true`))
	require.NotContains(t, raw, `"repeated": false`)

	// A non-repeated edge keeps the legacy three-key shape.
	require.Contains(t, raw, "\"kind\": \"derived_from\"\n    }")
}

func TestFormatTypedTree_Deterministic(t *testing.T) {
	t.Parallel()

	firstText, err := formatters.FormatTypedTree("A", scratchTypedEdges, allTreeKinds, types.OutputText)
	require.NoError(t, err)
	secondText, err := formatters.FormatTypedTree("A", scratchTypedEdges, allTreeKinds, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, firstText, secondText)

	firstJSON, err := formatters.FormatTypedTree("A", scratchTypedEdges, allTreeKinds, types.OutputJSON)
	require.NoError(t, err)
	secondJSON, err := formatters.FormatTypedTree("A", scratchTypedEdges, allTreeKinds, types.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, firstJSON, secondJSON)
}

func TestFormatTypedTree_UnknownFormat(t *testing.T) {
	t.Parallel()

	_, err := formatters.FormatTypedTree("A", scratchTypedEdges, allTreeKinds, types.OutputFormat("yaml"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown output format")
}

// The legacy blocked_by-only renderer must stay byte-identical: no repeat
// markers and no JSON flag.
func TestFormatDepTree_LegacyBytesHaveNoRepeatFlag(t *testing.T) {
	t.Parallel()

	edges := []provenance.Edge{
		{SourceID: "root", TargetID: "b", Kind: provenance.EdgeBlockedBy},
		{SourceID: "b", TargetID: "root", Kind: provenance.EdgeBlockedBy},
	}
	text, err := formatters.FormatDepTree("root", edges, types.OutputText)
	require.NoError(t, err)
	require.NotContains(t, text, "(already shown)")
	require.Equal(t, "root\n    └── blocked by b\n        └── blocked by root", text)

	raw, err := formatters.FormatDepTree("root", edges, types.OutputJSON)
	require.NoError(t, err)
	require.NotContains(t, raw, "repeated")
}
