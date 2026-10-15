package handlers

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/idmigrate"
)

// TestIdentityCountTablesCoverEveryConversionMirror gates the reopen proof's
// row-count table list against the conversion mirrors. The count comparison is
// the proof that conversion neither dropped nor duplicated rows in a converted
// relation; a converted relation missing from this list is silently unproven.
// The gate fails when any table named by a conversion mirror is absent, so the
// list cannot drift from what the conversion touches.
func TestIdentityCountTablesCoverEveryConversionMirror(t *testing.T) {
	t.Parallel()
	mirrors := idmigrate.DeclaredConversionMirrors()

	present := map[string]bool{}
	for _, table := range identityCountTables {
		present[table] = true
	}

	missing := map[string]bool{}
	add := func(list []idmigrate.IdentityColumn) {
		for _, col := range list {
			if !present[col.Table] {
				missing[col.Table] = true
			}
		}
	}
	add(mirrors.ProvenanceOwned)
	add(mirrors.ProvenancePreserved)
	add(mirrors.ProvenanceOwnedContext)
	add(mirrors.PastureOwned)
	add(mirrors.PastureOwnedArray)
	add(mirrors.PasturePreserved)
	add(mirrors.PastureSpecial)

	var names []string
	for table := range missing {
		names = append(names, table)
	}
	sort.Strings(names)
	require.Empty(t, names, "converted relations missing from identityCountTables: %v", names)
}
