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
// relation; a converted relation missing from both the count list and the
// projection-derived list is silently unproven. The gate fails when any table
// named by a conversion mirror is absent from both, so the lists cannot drift
// from what the conversion touches.
//
// The disposable lifecycle projection (lifecycle_occurrences) is a conversion
// mirror that must NOT be count-compared: the conversion deletes and
// re-inserts it from the converted journal (see projectionDerivedTables), so
// its count legitimately moves from stale or empty to journal truth. It is
// verified semantically against the converted journal instead.
func TestIdentityCountTablesCoverEveryConversionMirror(t *testing.T) {
	t.Parallel()
	mirrors := idmigrate.DeclaredConversionMirrors()

	present := map[string]bool{}
	for _, table := range identityCountTables {
		present[table] = true
	}
	projected := map[string]bool{}
	for _, table := range projectionDerivedTables {
		projected[table] = true
	}

	// The projection-derived set is closed: exactly the two disposable
	// projection relations the conversion replays. A new re-derived relation
	// must be added here on purpose, not drift in silently.
	got := append([]string(nil), projectionDerivedTables...)
	sort.Strings(got)
	require.Equal(t, []string{"lifecycle_occurrence_bindings", "lifecycle_occurrences"}, got,
		"projectionDerivedTables must stay exactly the disposable replay relations")

	// lifecycle_occurrences is the mirror that moved: it must be verified
	// semantically, not by count. lifecycle_payload_blobs is raw evidence and
	// must stay count-verified.
	require.True(t, projected["lifecycle_occurrences"],
		"lifecycle_occurrences must be in projectionDerivedTables")
	require.False(t, present["lifecycle_occurrences"],
		"lifecycle_occurrences must not be in identityCountTables")
	require.True(t, present["lifecycle_payload_blobs"],
		"lifecycle_payload_blobs is preserved evidence and must stay in identityCountTables")

	missing := map[string]bool{}
	add := func(list []idmigrate.IdentityColumn) {
		for _, col := range list {
			if !present[col.Table] && !projected[col.Table] {
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
	require.Empty(t, names, "converted relations missing from identityCountTables and projectionDerivedTables: %v", names)
}

// TestCompareIdentityCountsStillCatchesRealChange pins that the count check is
// not vacuous after the projection exclusion: a genuine identity-row change
// still fails, while equal counts pass.
func TestCompareIdentityCountsStillCatchesRealChange(t *testing.T) {
	t.Parallel()
	require.NoError(t, compareIdentityCounts(
		map[string]int64{"tasks": 3, "comments": 1},
		map[string]int64{"tasks": 3, "comments": 1},
	))

	err := compareIdentityCounts(
		map[string]int64{"tasks": 3},
		map[string]int64{"tasks": 2},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"tasks"`)
	require.Contains(t, err.Error(), "row count changed")

	err = compareIdentityCounts(
		map[string]int64{"tasks": 3},
		map[string]int64{},
	)
	require.Error(t, err, "a table missing after reopen must still fail")
}
