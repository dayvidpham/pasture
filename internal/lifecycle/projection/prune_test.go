package projection_test

import (
	"context"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
)

// This file is the operator prune's retention boundary, proved on the same
// rebuild entry the read path uses. Every case reads which blobs SURVIVED and
// which were deleted, because the delete is the thing that loses data and a
// count alone would not say whose data went.

func TestPrunePolicyRefusesNegativeBounds(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	cases := []struct {
		name   string
		policy projection.ReclaimPolicy
	}{
		{"negative retention age", projection.ReclaimPolicy{MaxAge: -time.Second}},
		{"negative row budget", projection.ReclaimPolicy{Cap: -1}},
	}
	for _, tc := range cases {
		_, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, projection.RebuildOptions{Clock: &sequenceClock{instants: []time.Time{snapshotEpoch}}, Window: reclaimWindow, Reclaim: tc.policy})
		require.Error(t, err, tc.name)
	}
}

// TestPrunePolicyDropsOldOrphansAndKeepsTheNamedTheNewestAndTheUnknown is the
// retention boundary: the two oldest orphans are deleted, oldest first, and
// every row the boundary must protect survives — the blob an occurrence names,
// the newest row in the store (whatever its age), and the row whose stamp is
// unknown. The second rebuild proves the named blob's absence would have been
// fatal: the projection rebuild re-inserts the occurrence's digest against the
// blob table's foreign key, so a named blob deleted by the prune would fail
// the very next read command.
func TestPrunePolicyDropsOldOrphansAndKeepsTheNamedTheNewestAndTheUnknown(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	older := seedBlob(t, db, []byte("older orphan"), snapshotEpoch.Add(-1000*time.Second))
	middle := seedBlob(t, db, []byte("middle orphan"), snapshotEpoch.Add(-900*time.Second))
	newest := seedBlob(t, db, []byte("newest orphan"), snapshotEpoch.Add(-800*time.Second))
	unstamped := seedBlob(t, db, []byte("unstamped orphan"), time.Time{})
	named := seedBlob(t, db, []byte("named blob"), snapshotEpoch.Add(-1000*time.Second))
	commitOccurrenceNaming(t, tracker, named, "prune.named")

	outcome, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, projection.RebuildOptions{
		Clock: &sequenceClock{instants: []time.Time{snapshotEpoch}}, Window: reclaimWindow,
		Reclaim: projection.ReclaimPolicy{MaxAge: reclaimWindow, Cap: projection.PruneCap, KeepNewest: true},
	})
	require.NoError(t, err)
	require.NoError(t, outcome.Failure)
	assert.ElementsMatch(t, []digest.Digest{older, middle}, outcome.Reclaimed,
		"the two orphans older than the retention age, and nothing else: not the newest row, not the unknown-age row, not the occurrence-named row")
	assert.Equal(t, int64(len("older orphan")+len("middle orphan")), outcome.ReclaimedBytes,
		"the byte total is summed from the deleted rows themselves")
	assert.False(t, blobExists(t, db, older))
	assert.False(t, blobExists(t, db, middle))
	assert.True(t, blobExists(t, db, newest), "THE NEWEST ROW IS NEVER DROPPED, whatever its age")
	assert.True(t, blobExists(t, db, unstamped), "an orphan whose written_at is 0 has an unknown age and is never dropped")
	assert.True(t, blobExists(t, db, named), "a blob an occurrence names is never dropped, at any age")

	// The repair path: a later read command rebuilds the projection from the
	// journal and re-inserts every occurrence's digest. With the named blob
	// gone the foreign key would fail here, so this is the proof that a pruned
	// store is still readable.
	again, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, projection.RebuildOptions{
		Clock: &sequenceClock{instants: []time.Time{snapshotEpoch}}, Window: reclaimWindow,
	})
	require.NoError(t, err, "a store the prune touched must still project every occurrence")
	require.NoError(t, again.Failure)
	var projected int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&projected))
	assert.Equal(t, 1, projected)
}

// TestPruneKeepsAnOrphanInsideTheRetentionAge pins the age bound alone, with
// the newest-row refusal switched OFF: only the age keeps the recent orphan, so
// a prune that ignored its retention age would delete it here.
func TestPruneKeepsAnOrphanInsideTheRetentionAge(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	old := seedBlob(t, db, []byte("old orphan"), snapshotEpoch.Add(-1000*time.Second))
	recent := seedBlob(t, db, []byte("recent orphan"), snapshotEpoch.Add(-time.Second))

	outcome, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, projection.RebuildOptions{
		Clock: &sequenceClock{instants: []time.Time{snapshotEpoch}}, Window: reclaimWindow,
		Reclaim: projection.ReclaimPolicy{MaxAge: reclaimWindow, Cap: projection.PruneCap},
	})
	require.NoError(t, err)
	require.NoError(t, outcome.Failure)
	assert.Equal(t, []digest.Digest{old}, outcome.Reclaimed)
	assert.True(t, blobExists(t, db, recent), "an orphan younger than the retention age is kept: its writer may still be between its two writes")
}

// TestPruneKeepsTheNewestRowOnlyBecauseItIsTheNewest is the mutation control
// for the newest-row refusal: the same store and the same age bound, with
// KeepNewest off, hands the newest orphan to the delete. Without this case the
// first test would pass for a prune that never had the refusal at all.
func TestPruneKeepsTheNewestRowOnlyBecauseItIsTheNewest(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	older := seedBlob(t, db, []byte("older orphan"), snapshotEpoch.Add(-1000*time.Second))
	newest := seedBlob(t, db, []byte("newest orphan"), snapshotEpoch.Add(-800*time.Second))

	outcome, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, projection.RebuildOptions{
		Clock: &sequenceClock{instants: []time.Time{snapshotEpoch}}, Window: reclaimWindow,
		Reclaim: projection.ReclaimPolicy{MaxAge: reclaimWindow, Cap: projection.PruneCap},
	})
	require.NoError(t, err)
	require.NoError(t, outcome.Failure)
	assert.ElementsMatch(t, []digest.Digest{older, newest}, outcome.Reclaimed,
		"with KeepNewest off the newest orphan is eligible, so it is the refusal and not the age that protected it above")
}

// TestPruneCapBoundsOneBatchOldestFirst is the batch budget proof: one batch
// deletes exactly the cap, oldest first, and the next batch takes the rest.
// The newest row stays throughout.
func TestPruneCapBoundsOneBatchOldestFirst(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	const cap = 2
	refs := make([]digest.Digest, 0, 5)
	for index := 0; index < 5; index++ {
		refs = append(refs, seedBlob(t, db, []byte(string(rune('a'+index))+": orphan"), snapshotEpoch.Add(-time.Duration(1000-index)*time.Second)))
	}
	policy := projection.ReclaimPolicy{MaxAge: reclaimWindow, Cap: cap, KeepNewest: true}
	first, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, projection.RebuildOptions{Clock: &sequenceClock{instants: []time.Time{snapshotEpoch}}, Window: reclaimWindow, Reclaim: policy})
	require.NoError(t, err)
	require.NoError(t, first.Failure)
	assert.Len(t, first.Reclaimed, cap, "one batch deletes exactly the cap")
	for index := 0; index < cap; index++ {
		assert.False(t, blobExists(t, db, refs[index]), "the batch takes the OLDEST rows first: refs[%d]", index)
	}
	for index := cap; index < len(refs); index++ {
		assert.True(t, blobExists(t, db, refs[index]), "the batch never reaches past its cap: refs[%d]", index)
	}
	second, err := projection.RebuildOccurrences(context.Background(), tracker.Journal(), db, projection.RebuildOptions{Clock: &sequenceClock{instants: []time.Time{snapshotEpoch}}, Window: reclaimWindow, Reclaim: policy})
	require.NoError(t, err)
	require.NoError(t, second.Failure)
	assert.Len(t, second.Reclaimed, cap, "the next batch takes the next-oldest rows")
	assert.False(t, blobExists(t, db, refs[2]))
	assert.False(t, blobExists(t, db, refs[3]))
	assert.True(t, blobExists(t, db, refs[4]), "the newest row survives every batch")
}
