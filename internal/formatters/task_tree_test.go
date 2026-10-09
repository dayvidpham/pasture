package formatters_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func TestFormatDepTree_DiamondCycleAndLongChain(t *testing.T) {
	t.Parallel()

	// A diamond with a legacy cycle and duplicate input. Nonblocking and
	// unreachable input must not become tree edges.
	edges := []provenance.Edge{
		{SourceID: "root", TargetID: "c", Kind: provenance.EdgeBlockedBy},
		{SourceID: "root", TargetID: "b", Kind: provenance.EdgeBlockedBy},
		{SourceID: "b", TargetID: "d", Kind: provenance.EdgeBlockedBy},
		{SourceID: "c", TargetID: "d", Kind: provenance.EdgeBlockedBy},
		{SourceID: "d", TargetID: "root", Kind: provenance.EdgeBlockedBy},
		{SourceID: "root", TargetID: "b", Kind: provenance.EdgeBlockedBy},
		{SourceID: "root", TargetID: "hidden", Kind: provenance.EdgeDerivedFrom},
		{SourceID: "other", TargetID: "unreachable", Kind: provenance.EdgeBlockedBy},
	}
	out, err := formatters.FormatDepTree("root", edges, types.OutputJSON)
	require.NoError(t, err)
	var got struct {
		Root  string
		Edges []struct {
			SourceID string
			TargetID string
			Kind     string
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, "root", got.Root)
	var pairs []string
	for _, e := range got.Edges {
		pairs = append(pairs, e.SourceID+":"+e.TargetID)
		require.Equal(t, "blocked_by", e.Kind)
	}
	require.Equal(t, []string{"root:b", "b:d", "d:root", "root:c", "c:d"}, pairs)

	out, err = formatters.FormatDepTree("root", edges, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, "root\n    ├── blocked by b\n    │   └── blocked by d\n    │       └── blocked by root\n    └── blocked by c\n        └── blocked by d", out)

	chain := make([]provenance.Edge, 2048)
	for i := range chain {
		chain[i] = provenance.Edge{
			SourceID: fmt.Sprint(i),
			TargetID: fmt.Sprint(i + 1),
			Kind:     provenance.EdgeBlockedBy,
		}
	}
	out, err = formatters.FormatDepTree("0", chain, types.OutputJSON)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got.Edges, len(chain))

	out, err = formatters.FormatDepTree("0", chain, types.OutputText)
	require.NoError(t, err)
	require.Equal(t, len(chain), strings.Count(out, "blocked by"))
}

func TestFormatWorkflowEmptyCollections(t *testing.T) {
	t.Parallel()

	out, err := formatters.FormatLabels("task", nil, types.OutputJSON)
	require.NoError(t, err)
	require.Contains(t, out, `"labels": []`)

	out, err = formatters.FormatComments(nil, types.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, "[]", out)

	out, err = formatters.FormatTasks(nil, types.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, "[]", out)

	out, err = formatters.FormatDepTree("task", nil, types.OutputJSON)
	require.NoError(t, err)
	require.Contains(t, out, `"edges": []`)
}
