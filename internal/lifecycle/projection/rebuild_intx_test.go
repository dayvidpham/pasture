package projection_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/provenance"
)

// TestRebuildOccurrencesInTxRecreatesTheProjectionFromTransactionEvidence drives
// the migration's non-reclaiming replay entry directly: it reads converted
// journal truth through a transaction-bound evidence reader, rewrites the
// disposable projection inside the caller's transaction, and then the caller
// compares the result against that same transaction view.
func TestRebuildOccurrencesInTxRecreatesTheProjectionFromTransactionEvidence(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	named := seedBlob(t, db, []byte("named body carrying an old URI file://project--u"), snapshotEpoch.Add(-1000*time.Second))
	commitOccurrenceNaming(t, tracker, named, "intx.replay.named")

	// A stale projection row that the replay must replace: journal truth has one
	// occurrence, so a leftover row must not survive the replay.
	_, err := db.Exec(`DELETE FROM lifecycle_occurrences`)
	require.NoError(t, err)

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	reader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	require.NoError(t, err)

	require.NoError(t, projection.RebuildOccurrencesInTx(ctx, tx, reader))
	require.NoError(t, projection.CompareOccurrencesToEvidence(ctx, tx, reader))

	var rows int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM lifecycle_occurrences`).Scan(&rows))
	assert.Equal(t, 1, rows, "the replay must recreate exactly the journal truth")

	// Reclaim is structurally absent from this entry, so the aged blob the
	// ordinary reclaim would delete is still present.
	var blobs int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM lifecycle_payload_blobs WHERE digest=?`, named.String()).Scan(&blobs))
	assert.Equal(t, 1, blobs, "the non-reclaiming replay must keep every raw payload blob")
}

// TestCompareOccurrencesToEvidenceRejectsAProjectionThatDisagreesWithTheJournal
// proves the comparison is real: a projection row with a wrong actor is caught
// inside the caller's transaction.
func TestCompareOccurrencesToEvidenceRejectsAProjectionThatDisagreesWithTheJournal(t *testing.T) {
	t.Parallel()
	tracker, db := openReclaimStore(t)
	named := seedBlob(t, db, []byte("named body"), snapshotEpoch.Add(-1000*time.Second))
	commitOccurrenceNaming(t, tracker, named, "intx.replay.mismatch")

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	reader, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	require.NoError(t, err)
	require.NoError(t, projection.RebuildOccurrencesInTx(ctx, tx, reader))

	_, err = tx.Exec(`UPDATE lifecycle_occurrences SET actor_id = ?`, "file://elsewhere/01968a3c-9d4f-7c8a-bc12-feedfacecafe")
	require.NoError(t, err)

	assert.Error(t, projection.CompareOccurrencesToEvidence(ctx, tx, reader), "a projection that disagrees with the journal must be rejected")
}
