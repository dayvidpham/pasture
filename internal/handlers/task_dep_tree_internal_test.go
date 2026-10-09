package handlers

import (
	"fmt"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

// treeID builds a valid wire-format task ID for a small numeric suffix, so the
// walk can parse and expand it.
func treeID(n byte) string {
	return fmt.Sprintf("tree--00000000-0000-0000-0000-%012x", n)
}

// fetchTable maps a source ID and kind to that source's outgoing target IDs,
// emulating the tracker's per-kind Edges read.
type fetchTable map[string]map[provenance.EdgeKind][]string

func (t fetchTable) fetch(source provenance.TaskID, kind provenance.EdgeKind) ([]provenance.Edge, error) {
	var out []provenance.Edge
	for _, target := range t[source.String()][kind] {
		out = append(out, provenance.Edge{SourceID: source.String(), TargetID: target, Kind: kind})
	}
	return out, nil
}

func edgePairs(edges []provenance.Edge) []string {
	out := make([]string, len(edges))
	for i, e := range edges {
		out[i] = e.SourceID + "->" + e.TargetID + ":" + e.Kind.String()
	}
	return out
}

func TestNormalizeTreeKinds(t *testing.T) {
	t.Parallel()

	got := normalizeTreeKinds([]provenance.EdgeKind{
		provenance.EdgeDiscoveredFrom,
		provenance.EdgeBlockedBy,
		provenance.EdgeBlockedBy,
		provenance.EdgeSupersedes,
	})
	require.Equal(t, []provenance.EdgeKind{
		provenance.EdgeBlockedBy,
		provenance.EdgeSupersedes,
		provenance.EdgeDiscoveredFrom,
	}, got)

	require.Empty(t, normalizeTreeKinds(nil))
	require.True(t, isBlockedByOnly([]provenance.EdgeKind{provenance.EdgeBlockedBy}))
	require.False(t, isBlockedByOnly([]provenance.EdgeKind{provenance.EdgeBlockedBy, provenance.EdgeSupersedes}))
	require.False(t, isBlockedByOnly([]provenance.EdgeKind{provenance.EdgeSupersedes}))
}

func TestCollectTypedTree_ScratchScenarioOrderAndExpansion(t *testing.T) {
	t.Parallel()

	a, b, c, d, x := treeID(0xa), treeID(0xb), treeID(0xc), treeID(0xd), treeID(0x10)
	table := fetchTable{
		a: {
			provenance.EdgeSupersedes:  {b},
			provenance.EdgeDerivedFrom: {d},
		},
		b: {
			provenance.EdgeDerivedFrom: {c},
			provenance.EdgeSupersedes:  {d},
		},
		c: {provenance.EdgeSupersedes: {a}},
		d: {provenance.EdgeDiscoveredFrom: {c}},
		x: {provenance.EdgeSupersedes: {a}}, // incoming to A: never reached from A
	}

	edges, err := collectTypedTree(a, canonicalTreeKinds, table.fetch)
	require.NoError(t, err)
	require.Equal(t, []string{
		a + "->" + d + ":derived_from",
		d + "->" + c + ":discovered_from",
		c + "->" + a + ":supersedes",
		a + "->" + b + ":supersedes",
		b + "->" + c + ":derived_from",
		b + "->" + d + ":supersedes",
	}, edgePairs(edges))
}

func TestCollectTypedTree_Deterministic(t *testing.T) {
	t.Parallel()

	a, b, c := treeID(0xa), treeID(0xb), treeID(0xc)
	table := fetchTable{
		a: {provenance.EdgeBlockedBy: {c, b}},
		b: {provenance.EdgeBlockedBy: {c}},
	}
	first, err := collectTypedTree(a, []provenance.EdgeKind{provenance.EdgeBlockedBy}, table.fetch)
	require.NoError(t, err)
	second, err := collectTypedTree(a, []provenance.EdgeKind{provenance.EdgeBlockedBy}, table.fetch)
	require.NoError(t, err)
	require.Equal(t, edgePairs(first), edgePairs(second))
	// DFS pre-order: b is expanded (b->c) before A's next child c, and A->c is
	// the repeat. Children are target-sorted: b before c.
	require.Equal(t, []string{
		a + "->" + b + ":blocked_by",
		b + "->" + c + ":blocked_by",
		a + "->" + c + ":blocked_by",
	}, edgePairs(first))
}

func TestCollectTypedTree_KindMajorThenTargetOrder(t *testing.T) {
	t.Parallel()

	a, b, c, d := treeID(0xa), treeID(0xb), treeID(0xc), treeID(0xd)
	// Targets are chosen so a target-major order (b, c, d) would differ from
	// the canonical kind-major order (blocked_by d, derived_from c, supersedes b).
	table := fetchTable{
		a: {
			provenance.EdgeBlockedBy:   {d},
			provenance.EdgeDerivedFrom: {c},
			provenance.EdgeSupersedes:  {b},
		},
	}
	edges, err := collectTypedTree(a, canonicalTreeKinds, table.fetch)
	require.NoError(t, err)
	require.Equal(t, []string{
		a + "->" + d + ":blocked_by",
		a + "->" + c + ":derived_from",
		a + "->" + b + ":supersedes",
	}, edgePairs(edges))
}

func TestCollectTypedTree_PerKindDedup(t *testing.T) {
	t.Parallel()

	a, b, c := treeID(0xa), treeID(0xb), treeID(0xc)
	table := fetchTable{
		a: {provenance.EdgeSupersedes: {b, b, c}},
	}
	edges, err := collectTypedTree(a, []provenance.EdgeKind{provenance.EdgeSupersedes}, table.fetch)
	require.NoError(t, err)
	require.Equal(t, []string{
		a + "->" + b + ":supersedes",
		a + "->" + c + ":supersedes",
	}, edgePairs(edges))
}

func TestCollectTypedTree_NonTaskTargetIsLeaf(t *testing.T) {
	t.Parallel()

	a := treeID(0xa)
	table := fetchTable{
		a: {provenance.EdgeSupersedes: {"not-a-task-identity"}},
	}
	edges, err := collectTypedTree(a, []provenance.EdgeKind{provenance.EdgeSupersedes}, table.fetch)
	require.NoError(t, err)
	require.Equal(t, []string{a + "->not-a-task-identity:supersedes"}, edgePairs(edges))
}

func TestCollectTypedTree_FetchErrorWrapsNodeAndKind(t *testing.T) {
	t.Parallel()

	a := treeID(0xa)
	fetch := func(source provenance.TaskID, kind provenance.EdgeKind) ([]provenance.Edge, error) {
		if kind == provenance.EdgeDerivedFrom {
			return nil, fmt.Errorf("boom")
		}
		return []provenance.Edge{{SourceID: source.String(), TargetID: treeID(0xb), Kind: kind}}, nil
	}
	_, err := collectTypedTree(a, canonicalTreeKinds, fetch)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dep tree: read derived_from edges of "+a)
	require.Contains(t, err.Error(), "boom")
}

func TestCollectTypedTree_CycleTerminatesWithoutError(t *testing.T) {
	t.Parallel()

	a, b := treeID(0xa), treeID(0xb)
	table := fetchTable{
		a: {provenance.EdgeSupersedes: {b}},
		b: {provenance.EdgeSupersedes: {a}},
	}
	edges, err := collectTypedTree(a, []provenance.EdgeKind{provenance.EdgeSupersedes}, table.fetch)
	require.NoError(t, err)
	require.Equal(t, []string{
		a + "->" + b + ":supersedes",
		b + "->" + a + ":supersedes",
	}, edgePairs(edges))
}

func TestCollectTypedTree_SingleKindDoesNotCrossIntoOthers(t *testing.T) {
	t.Parallel()

	a, b, c := treeID(0xa), treeID(0xb), treeID(0xc)
	table := fetchTable{
		a: {provenance.EdgeSupersedes: {b}},
		b: {provenance.EdgeDerivedFrom: {c}},
	}
	edges, err := collectTypedTree(a, []provenance.EdgeKind{provenance.EdgeSupersedes}, table.fetch)
	require.NoError(t, err)
	require.Equal(t, []string{a + "->" + b + ":supersedes"}, edgePairs(edges))
}
