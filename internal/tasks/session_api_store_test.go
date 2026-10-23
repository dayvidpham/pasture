package tasks_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

func TestSessionAPIStoreBindUnbind(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch, err := tracker.Create("file://session-api-store", "Epoch one", "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	epochTwo, err := tracker.Create("file://session-api-store", "Epoch two", "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	store := tasks.NewSessionAPIStoreWithDB(db, timeouts.TestProfile(), claimClock{})
	ctx := context.Background()

	first, err := store.Bind(ctx, "claude-code", "store-session", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
	require.NoError(t, err)
	require.True(t, first.Created)
	require.Equal(t, epoch.ID.String(), first.EpochID)
	require.Equal(t, int64(1), first.Revision)

	var open, total int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM pasture_session_epoch_binding
		WHERE harness='claude-code' AND session='store-session' AND unbound_at IS NULL`).Scan(&open))
	require.Equal(t, 1, open)
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM pasture_session_epoch_binding
		WHERE harness='claude-code' AND session='store-session'`).Scan(&total))
	require.Equal(t, 1, total)

	var firstSeen int64
	require.NoError(t, db.QueryRow(`
		SELECT first_seen FROM pasture_session_state WHERE harness='claude-code' AND session='store-session'`).Scan(&firstSeen))
	require.Equal(t, claimClock{}.Now().UnixNano(), firstSeen)

	// Same epoch and role is idempotent: no second row and no revision bump.
	again, err := store.Bind(ctx, "claude-code", "store-session", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
	require.NoError(t, err)
	require.False(t, again.Created)
	require.Equal(t, first.BindingID, again.BindingID)
	require.Equal(t, first.Revision, again.Revision)

	// A different epoch closes the old row and opens exactly one new row.
	moved, err := store.Bind(ctx, "claude-code", "store-session", tasks.SessionBindRequest{EpochID: epochTwo.ID.String(), Role: "reviewer"})
	require.NoError(t, err)
	require.True(t, moved.Created)
	require.Greater(t, moved.Revision, first.Revision)
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM pasture_session_epoch_binding
		WHERE harness='claude-code' AND session='store-session' AND unbound_at IS NULL`).Scan(&open))
	require.Equal(t, 1, open)
	var closedReason string
	require.NoError(t, db.QueryRow(`
		SELECT unbound_reason FROM pasture_session_epoch_binding WHERE binding_id = ?`, first.BindingID).Scan(&closedReason))
	require.Equal(t, "unbind", closedReason)

	// A stale expected binding is a typed conflict.
	_, err = store.Bind(ctx, "claude-code", "store-session", tasks.SessionBindRequest{
		EpochID: epoch.ID.String(), Role: "worker", ExpectedBinding: first.BindingID,
	})
	require.True(t, errors.Is(err, tasks.ErrBindingConflict))

	ended, err := store.Unbind(ctx, "claude-code", "store-session", moved.BindingID)
	require.NoError(t, err)
	require.NotNil(t, ended.UnboundAt)
	require.Equal(t, "unbind", ended.EndReason)
	_, err = store.Unbind(ctx, "claude-code", "store-session", "")
	require.True(t, errors.Is(err, tasks.ErrNoOpenBinding))
}

func TestSessionAPIStoreRejectsUnknownEpochAndRole(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch, err := tracker.Create("file://session-api-store", "Epoch", "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	store := tasks.NewSessionAPIStoreWithDB(db, timeouts.TestProfile(), claimClock{})
	_, err = store.Bind(context.Background(), "claude-code", "unknown-epoch", tasks.SessionBindRequest{EpochID: "file://missing", Role: "worker"})
	require.True(t, errors.Is(err, tasks.ErrUnknownEpoch))
	_, err = store.Bind(context.Background(), "claude-code", "bad-role", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "implementer"})
	require.Error(t, err)
}

func TestSessionAPIStoreConcurrentBindsKeepOneOpenRow(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch, err := tracker.Create("file://session-api-store", "Epoch", "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	store := tasks.NewSessionAPIStoreWithDB(db, timeouts.TestProfile(), claimClock{})
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Bind(context.Background(), "claude-code", "race-session", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	var open int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM pasture_session_epoch_binding
		WHERE harness='claude-code' AND session='race-session' AND unbound_at IS NULL`).Scan(&open))
	require.Equal(t, 1, open)
}
