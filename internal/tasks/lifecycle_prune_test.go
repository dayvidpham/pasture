package tasks_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/dayvidpham/provenance"
)

// This file is the operator prune's bulk behavior at the wrapper the verb
// calls: it drains more orphans than one read command's reclaim can, it stops
// at the operator's byte budget, and it never deletes a blob a concurrent
// writer just wrote.

// openLifecyclePruneStore opens a fresh unified store with its persisted
// ingress identity bootstrapped, exactly as the production commands find one,
// and returns the tracker and its SQL handle.
func openLifecyclePruneStore(t *testing.T) (protocol.TaskTracker, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	bootstrap, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = bootstrap.Create("file://lifecycle-prune-test", "bootstrap", "initialize the persisted ingress identity", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	require.NoError(t, bootstrap.Close())
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tracker.Close() })
	reader, err := tasks.NewLifecycleReader(tracker)
	require.NoError(t, err)
	concrete, ok := reader.(projection.Reader)
	require.True(t, ok, "the production reader %T must expose the unified handle", reader)
	return tracker, concrete.DB
}

// seedOldOrphanBlobs writes count payload blobs stamped one hour in the past,
// each with a DISTINCT stamp so the newest row is well defined. They are older
// than any writer window and no occurrence names them, so a prune reclaims
// them.
func seedOldOrphanBlobs(t *testing.T, db *sql.DB, count int) []digest.Digest {
	t.Helper()
	base := time.Now().Add(-time.Hour)
	refs := make([]digest.Digest, 0, count)
	tx, err := db.Begin()
	require.NoError(t, err)
	for index := 0; index < count; index++ {
		body := []byte(fmt.Sprintf(`{"old-orphan":%d}`, index))
		ref := digest.FromBytes(body)
		// One millisecond apart keeps every stamp DISTINCT and every stamp
		// safely in the past, so even a few thousand rows stay older than the
		// writer window the prune ages against.
		_, err := tx.Exec(`INSERT INTO lifecycle_payload_blobs(digest, body, byte_count, written_at) VALUES(?,?,?,?)`, ref.String(), body, len(body), base.Add(time.Duration(index)*time.Millisecond).UnixNano())
		require.NoError(t, err)
		refs = append(refs, ref)
	}
	require.NoError(t, tx.Commit())
	return refs
}

func countBlobRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var rows int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_payload_blobs`).Scan(&rows))
	return rows
}

func blobRowExists(t *testing.T, db *sql.DB, ref digest.Digest) bool {
	t.Helper()
	var rows int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM lifecycle_payload_blobs WHERE digest=?`, ref.String()).Scan(&rows))
	return rows == 1
}

// TestPruneReclaimsInBulkBeyondTheReadCapAndKeepsTheNewest: the read path's
// reclaim is capped at 1024 rows per command; the operator verb exists to drain
// a backlog in one run. It reclaims every eligible orphan in one call, and it
// keeps the single newest row.
func TestPruneReclaimsInBulkBeyondTheReadCapAndKeepsTheNewest(t *testing.T) {
	t.Parallel()
	tracker, db := openLifecyclePruneStore(t)
	const total = projection.ReclaimCap + 5
	refs := seedOldOrphanBlobs(t, db, total)

	outcome, err := tasks.PruneLifecyclePayloads(context.Background(), tracker, tasks.PruneOptions{})
	require.NoError(t, err)
	require.NoError(t, outcome.Failure)
	assert.Equal(t, int64(total-1), outcome.Reclaimed,
		"one run drains the whole backlog, minus the single newest row, where a read command would take only %d", projection.ReclaimCap)
	assert.Greater(t, outcome.ReclaimedBytes, int64(0), "the run reports the bytes it actually freed")
	assert.False(t, outcome.Truncated, "the run drained the backlog, so it did not stop at a bound")
	assert.Equal(t, 1, countBlobRows(t, db), "only the newest row remains")
	assert.True(t, blobRowExists(t, db, refs[total-1]), "the newest row is the one that survives")

	// The store is still readable: a projection rebuild re-inserts every
	// occurrence's digest, so it would fail if the prune had removed a named
	// blob. There are no occurrences here, but the rebuild must still succeed.
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
}

// TestPruneStopsAtItsByteBudgetAndSaysSo: with an operator byte budget, the run
// stops after the batch that meets it and reports Truncated, so a second run is
// the operator's explicit choice rather than a surprise. The bound is a full
// batch (the run never starts a batch it cannot finish), so this seeds just
// past one batch.
func TestPruneStopsAtItsByteBudgetAndSaysSo(t *testing.T) {
	t.Parallel()
	tracker, db := openLifecyclePruneStore(t)
	const total = projection.PruneCap + 3
	seedOldOrphanBlobs(t, db, total)

	outcome, err := tasks.PruneLifecyclePayloads(context.Background(), tracker, tasks.PruneOptions{MaxBytes: 1})
	require.NoError(t, err)
	require.NoError(t, outcome.Failure)
	assert.True(t, outcome.Truncated, "the byte budget was met with eligible rows still present, so the run must say it stopped early")
	assert.Equal(t, int64(projection.PruneCap), outcome.Reclaimed, "the run finishes the batch that meets its budget and starts no further batch")
	assert.Greater(t, outcome.ReclaimedBytes, int64(1))
	assert.Greater(t, countBlobRows(t, db), 1, "eligible orphans remain for a second run")

	second, err := tasks.PruneLifecyclePayloads(context.Background(), tracker, tasks.PruneOptions{})
	require.NoError(t, err)
	require.NoError(t, second.Failure)
	assert.False(t, second.Truncated)
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker))
}

// TestPruneNeverDeletesABlobAConcurrentWriterJustPut is the concurrency
// requirement: while the prune runs, a writer keeps putting fresh payload
// blobs, as a hook invocation does before it appends its journal row. Every
// blob the writer put must survive, because its stamp is inside the writer
// window; a prune that aged an arriving blob against the wall clock at delete
// time, rather than against the snapshot instant minus the window, would delete
// one and the writer's append would then name an absent blob.
func TestPruneNeverDeletesABlobAConcurrentWriterJustPut(t *testing.T) {
	t.Parallel()
	tracker, db := openLifecyclePruneStore(t)
	refs := seedOldOrphanBlobs(t, db, 64)

	blobs, err := tasks.NewLifecycleBlobStore(tracker)
	require.NoError(t, err)
	ctx := context.Background()

	const writes = 200
	written := make([]digest.Digest, writes)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for index := 0; index < writes; index++ {
			body := []byte(fmt.Sprintf(`{"in-flight":%d}`, index))
			ref := digest.FromBytes(body)
			written[index] = ref
			if err := blobs.Put(ctx, ref, body); err != nil {
				t.Errorf("the concurrent writer could not put blob %d: %v", index, err)
				return
			}
		}
	}()

	outcome, err := tasks.PruneLifecyclePayloads(ctx, tracker, tasks.PruneOptions{})
	wait.Wait()
	require.NoError(t, err)
	require.NoError(t, outcome.Failure)

	for index, ref := range written {
		assert.True(t, blobRowExists(t, db, ref), "the prune deleted a blob the concurrent writer had just put (write %d, %s)", index, ref)
	}
	// The seeded old orphans were still reclaimed, so the prune really ran.
	for _, ref := range refs[:len(refs)-1] {
		assert.False(t, blobRowExists(t, db, ref), "an old orphan older than the retention age was not reclaimed (%s)", ref)
	}
	require.NoError(t, tasks.RebuildLifecycleOccurrences(context.Background(), tracker), "the store must still rebuild after a concurrent prune")
}
