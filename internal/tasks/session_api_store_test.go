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
	require.True(t, errors.Is(err, tasks.ErrBindingRequest), "an unknown role is a request fault, not a store outcome: %v", err)
}

// TestSessionAPIStoreClassifiesRequestAndStoreErrors pins the two sentinels the
// HTTP layer keys its status codes on: request mistakes are ErrBindingRequest
// (400), and transaction/handle failures are ErrBindingStoreFault (500). The
// domain outcomes (unknown epoch, conflict, no open binding) stay distinct.
func TestSessionAPIStoreClassifiesRequestAndStoreErrors(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch, err := tracker.Create("file://session-api-store", "Epoch", "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	store := tasks.NewSessionAPIStoreWithDB(db, timeouts.TestProfile(), claimClock{})
	ctx := context.Background()

	requestCases := []struct {
		name string
		call func() error
	}{
		{name: "missing epoch", call: func() error {
			_, err := store.Bind(ctx, "claude-code", "request", tasks.SessionBindRequest{Role: "worker"})
			return err
		}},
		{name: "invalid role", call: func() error {
			_, err := store.Bind(ctx, "claude-code", "request", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "implementer"})
			return err
		}},
		{name: "padded bind expectedBinding", call: func() error {
			_, err := store.Bind(ctx, "claude-code", "request", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker", ExpectedBinding: " padded "})
			return err
		}},
		{name: "padded unbind expectedBinding", call: func() error {
			_, err := store.Unbind(ctx, "claude-code", "request", " padded ")
			return err
		}},
		{name: "invalid session key", call: func() error {
			_, err := store.Bind(ctx, "claude-code", "bad/key", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
			return err
		}},
	}
	for _, testCase := range requestCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.call()
			require.Error(t, err)
			require.True(t, errors.Is(err, tasks.ErrBindingRequest), "want ErrBindingRequest, got %v", err)
			require.False(t, errors.Is(err, tasks.ErrBindingStoreFault), "a request fault must not be classified as a store fault: %v", err)
		})
	}

	// An expired caller deadline is a transaction failure, not a request fault.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = store.Bind(cancelled, "claude-code", "store-fault", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
	require.True(t, errors.Is(err, tasks.ErrBindingStoreFault), "want ErrBindingStoreFault, got %v", err)
	require.True(t, errors.Is(err, context.Canceled), "the cause must stay reachable: %v", err)
	require.False(t, errors.Is(err, tasks.ErrBindingRequest))
}

// TestSessionAPIStoreClosedHandleIsAStoreFault pins the closed-handle route: no
// statement can run, and the caller is told the store failed rather than that
// its request was invalid.
func TestSessionAPIStoreClosedHandleIsAStoreFault(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch, err := tracker.Create("file://session-api-store", "Epoch", "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	store := tasks.NewSessionAPIStoreWithDB(db, timeouts.TestProfile(), claimClock{})
	require.NoError(t, db.Close())

	_, err = store.Bind(context.Background(), "claude-code", "closed-handle", tasks.SessionBindRequest{EpochID: epoch.ID.String(), Role: "worker"})
	require.True(t, errors.Is(err, tasks.ErrBindingStoreFault), "want ErrBindingStoreFault, got %v", err)
	require.False(t, errors.Is(err, tasks.ErrBindingRequest))

	_, err = store.Unbind(context.Background(), "claude-code", "closed-handle", "")
	require.True(t, errors.Is(err, tasks.ErrBindingStoreFault), "want ErrBindingStoreFault, got %v", err)
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
