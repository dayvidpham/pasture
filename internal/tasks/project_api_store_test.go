package tasks_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// projectClock advances one second per observation so interval order is
// deterministic without inventing timestamps inside production code.
type projectClock struct {
	mu sync.Mutex
	at time.Time
}

func newProjectClock() *projectClock {
	return &projectClock{at: time.Unix(1700000000, 0).UTC()}
}

func (c *projectClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.at
	c.at = c.at.Add(time.Second)
	return current
}

func projectHash(seed string) string {
	return strings.Repeat(seed, 64/len(seed))
}

func projectEpoch(t *testing.T, tracker protocol.TaskTracker, title string) string {
	t.Helper()
	epoch, err := tracker.Create("file://project-store", title, "epoch", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	return epoch.ID.String()
}

func TestProjectAPIStoreBindRebindUnbindKeepsHistory(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epochOne := projectEpoch(t, tracker, "Epoch one")
	epochTwo := projectEpoch(t, tracker, "Epoch two")
	store := tasks.NewProjectAPIStoreWithDB(db, timeouts.TestProfile(), newProjectClock())
	ctx := context.Background()
	hash := projectHash("a")

	_, err := store.Get(ctx, hash)
	require.True(t, errors.Is(err, tasks.ErrProjectNotFound), "an unmapped project has no detail: %v", err)

	first, err := store.Bind(ctx, hash, tasks.ProjectBindRequest{EpochID: epochOne})
	require.NoError(t, err)
	require.NotNil(t, first.Binding)
	require.Equal(t, epochOne, first.Binding.EpochID)
	require.Equal(t, "Epoch one", first.Binding.EpochTitle)
	require.Equal(t, string(tasks.DefaultProjectBindingRole), first.Binding.Role)
	require.Nil(t, first.Binding.UnboundAt)
	require.Len(t, first.Intervals, 1)

	var open, total int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_project_epoch_binding WHERE project_hash = ? AND unbound_at IS NULL`, hash).Scan(&open))
	require.Equal(t, 1, open)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_project_epoch_binding WHERE project_hash = ?`, hash).Scan(&total))
	require.Equal(t, 1, total)

	// Same epoch and role is idempotent: no second interval.
	again, err := store.Bind(ctx, hash, tasks.ProjectBindRequest{EpochID: epochOne, Role: "worker"})
	require.NoError(t, err)
	require.Equal(t, first.Binding.BindingID, again.Binding.BindingID)
	require.Len(t, again.Intervals, 1)

	// A different epoch closes the first interval and opens exactly one new one.
	moved, err := store.Bind(ctx, hash, tasks.ProjectBindRequest{EpochID: epochTwo, Role: "reviewer"})
	require.NoError(t, err)
	require.NotNil(t, moved.Binding)
	require.NotEqual(t, first.Binding.BindingID, moved.Binding.BindingID)
	require.Len(t, moved.Intervals, 2)
	require.Equal(t, first.Binding.BindingID, moved.Intervals[0].BindingID)
	require.NotNil(t, moved.Intervals[0].UnboundAt)
	require.Equal(t, "rebind", moved.Intervals[0].EndReason)
	require.Equal(t, moved.Binding.BindingID, moved.Intervals[1].BindingID)
	require.Nil(t, moved.Intervals[1].UnboundAt)
	require.Equal(t, "Epoch two", moved.Intervals[1].EpochTitle)

	// A stale expectedBinding is a typed conflict and changes nothing.
	_, err = store.Bind(ctx, hash, tasks.ProjectBindRequest{EpochID: epochOne, ExpectedBinding: first.Binding.BindingID})
	require.True(t, errors.Is(err, tasks.ErrProjectBindingConflict), "want conflict, got %v", err)

	// An expectedBinding naming the open mapping permits the change.
	switched, err := store.Bind(ctx, hash, tasks.ProjectBindRequest{EpochID: epochOne, Role: "supervisor", ExpectedBinding: moved.Binding.BindingID})
	require.NoError(t, err)
	require.NotNil(t, switched.Binding)
	require.Equal(t, epochOne, switched.Binding.EpochID)
	require.Len(t, switched.Intervals, 3)
	openID := switched.Binding.BindingID

	ended, err := store.Unbind(ctx, hash, openID)
	require.NoError(t, err)
	require.Nil(t, ended.Binding)
	require.Len(t, ended.Intervals, 3)
	require.NotNil(t, ended.Intervals[2].UnboundAt)
	require.Equal(t, "unbind", ended.Intervals[2].EndReason)

	list, err := store.List(ctx)
	require.NoError(t, err)
	require.Empty(t, list, "a project with no active mapping is omitted from the list")

	history, err := store.Get(ctx, hash)
	require.NoError(t, err)
	require.Nil(t, history.Binding)
	require.Len(t, history.Intervals, 3, "unbind keeps the full interval history")
	require.Less(t, history.Intervals[0].BoundAt, history.Intervals[1].BoundAt)
	require.Less(t, history.Intervals[1].BoundAt, history.Intervals[2].BoundAt)

	_, err = store.Unbind(ctx, hash, "")
	require.True(t, errors.Is(err, tasks.ErrProjectNoOpenBinding), "a second unbind has nothing to close: %v", err)
}

func TestProjectAPIStoreListShowsOnlyActiveMappings(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch := projectEpoch(t, tracker, "Epoch")
	store := tasks.NewProjectAPIStoreWithDB(db, timeouts.TestProfile(), newProjectClock())
	ctx := context.Background()
	active := projectHash("b")
	closed := projectHash("c")

	_, err := store.Bind(ctx, active, tasks.ProjectBindRequest{EpochID: epoch, Role: "worker"})
	require.NoError(t, err)
	bound, err := store.Bind(ctx, closed, tasks.ProjectBindRequest{EpochID: epoch, Role: "reviewer"})
	require.NoError(t, err)
	_, err = store.Unbind(ctx, closed, bound.Binding.BindingID)
	require.NoError(t, err)

	list, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, active, list[0].ProjectHash)
	require.NotNil(t, list[0].Binding)
	require.Empty(t, list[0].Intervals, "list rows do not carry history")

	detail, err := store.Get(ctx, closed)
	require.NoError(t, err)
	require.Nil(t, detail.Binding)
	require.Len(t, detail.Intervals, 1)
}

func TestProjectAPIStoreRejectsBadRequestAndUnknownEpoch(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch := projectEpoch(t, tracker, "Epoch")
	store := tasks.NewProjectAPIStoreWithDB(db, timeouts.TestProfile(), newProjectClock())
	ctx := context.Background()
	hash := projectHash("d")

	cases := []struct {
		name   string
		hash   string
		req    tasks.ProjectBindRequest
		target error
	}{
		{name: "unknown epoch", hash: hash, req: tasks.ProjectBindRequest{EpochID: "file://missing"}, target: tasks.ErrUnknownProjectEpoch},
		{name: "missing epoch", hash: hash, req: tasks.ProjectBindRequest{}, target: tasks.ErrProjectRequest},
		{name: "malformed hash", hash: "not-a-hash", req: tasks.ProjectBindRequest{EpochID: epoch}, target: tasks.ErrProjectRequest},
		{name: "uppercase hash", hash: strings.Repeat("A", 64), req: tasks.ProjectBindRequest{EpochID: epoch}, target: tasks.ErrProjectRequest},
		{name: "invalid role", hash: hash, req: tasks.ProjectBindRequest{EpochID: epoch, Role: "implementer"}, target: tasks.ErrProjectRequest},
		{name: "padded expected binding", hash: hash, req: tasks.ProjectBindRequest{EpochID: epoch, ExpectedBinding: " padded "}, target: tasks.ErrProjectRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := store.Bind(ctx, testCase.hash, testCase.req)
			require.True(t, errors.Is(err, testCase.target), "want %v, got %v", testCase.target, err)
		})
	}
	var total int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pasture_project_epoch_binding`).Scan(&total))
	require.Zero(t, total, "a refused request must not write a mapping")

	_, err := store.Unbind(ctx, hash, " padded ")
	require.True(t, errors.Is(err, tasks.ErrProjectRequest), "want request error, got %v", err)
	_, err = store.Get(ctx, "not-a-hash")
	require.True(t, errors.Is(err, tasks.ErrProjectRequest), "want request error, got %v", err)
}

func TestProjectAPIStoreOneOpenMappingConstraint(t *testing.T) {
	t.Parallel()
	_, db, _ := claimStore(t)
	hash := projectHash("e")
	now := time.Now().UnixNano()
	_, err := db.Exec(`
		INSERT INTO pasture_project_epoch_binding
			(binding_id, project_hash, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES ('open-one', ?, 'file://epoch/one', 'worker', ?, NULL, NULL)`, hash, now)
	require.NoError(t, err)
	_, err = db.Exec(`
		INSERT INTO pasture_project_epoch_binding
			(binding_id, project_hash, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES ('open-two', ?, 'file://epoch/two', 'worker', ?, NULL, NULL)`, hash, now+1)
	require.Error(t, err, "the partial unique index must refuse a second open mapping for one project")
}

func TestProjectAPIStoreBorrowsTheTrackerHandle(t *testing.T) {
	t.Parallel()
	tracker, _, _ := claimStore(t)
	epoch := projectEpoch(t, tracker, "Epoch")
	store, err := tasks.NewProjectAPIStore(tracker)
	require.NoError(t, err)
	hash := projectHash("e")
	mapping, err := store.Bind(context.Background(), hash, tasks.ProjectBindRequest{EpochID: epoch})
	require.NoError(t, err)
	require.NotNil(t, mapping.Binding)
	require.Equal(t, epoch, mapping.Binding.EpochID)
}

func TestNewProjectAPIStoreFromSessionAPIStoreRequiresHandle(t *testing.T) {
	t.Parallel()
	_, err := tasks.NewProjectAPIStoreFromSessionAPIStore(nil)
	require.True(t, errors.Is(err, tasks.ErrProjectStoreFault), "nil session store must be a store fault: %v", err)
	_, err = tasks.NewProjectAPIStoreFromSessionAPIStore(&tasks.SessionAPIStore{})
	require.True(t, errors.Is(err, tasks.ErrProjectStoreFault), "a session store without a handle must be a store fault: %v", err)
}

// TestProjectAPIStoreLeavesSessionBindingsUntouched proves the project-level
// relationship is separate: a real session binding is written first, then
// project bind/rebind/unbind run against the same database and the session row
// (and the session API revision) must stay byte-for-byte unchanged.
func TestProjectAPIStoreLeavesSessionBindingsUntouched(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epochOne := projectEpoch(t, tracker, "Epoch one")
	epochTwo := projectEpoch(t, tracker, "Epoch two")
	ctx := context.Background()

	sessionStore := tasks.NewSessionAPIStoreWithDB(db, timeouts.TestProfile(), claimClock{})
	seeded, err := sessionStore.Bind(ctx, "claude-code", "untouched-session", tasks.SessionBindRequest{EpochID: epochOne, Role: "worker"})
	require.NoError(t, err)
	require.True(t, seeded.Created)

	type sessionBindingRow struct {
		BindingID string
		EpochID   string
		BoundAt   int64
		UnboundAt sql.NullInt64
	}
	readBinding := func() sessionBindingRow {
		var row sessionBindingRow
		require.NoError(t, db.QueryRow(`
			SELECT binding_id, epoch_id, bound_at, unbound_at
			FROM pasture_session_epoch_binding
			WHERE harness = 'claude-code' AND session = 'untouched-session'`).
			Scan(&row.BindingID, &row.EpochID, &row.BoundAt, &row.UnboundAt))
		return row
	}
	readRevision := func() int64 {
		var revision int64
		require.NoError(t, db.QueryRow(`SELECT revision FROM pasture_session_api_state WHERE singleton_id = 0`).Scan(&revision))
		return revision
	}
	before := readBinding()
	revisionBefore := readRevision()

	projectStore := tasks.NewProjectAPIStoreWithDB(db, timeouts.TestProfile(), newProjectClock())
	hash := projectHash("b")
	bound, err := projectStore.Bind(ctx, hash, tasks.ProjectBindRequest{EpochID: epochOne})
	require.NoError(t, err)
	rebound, err := projectStore.Bind(ctx, hash, tasks.ProjectBindRequest{EpochID: epochTwo})
	require.NoError(t, err)
	require.NotNil(t, rebound.Binding)
	_, err = projectStore.Unbind(ctx, hash, rebound.Binding.BindingID)
	require.NoError(t, err)
	require.NotNil(t, bound.Binding)

	require.Equal(t, before, readBinding(), "project bind/rebind/unbind must not touch the session binding row")
	require.Equal(t, revisionBefore, readRevision(), "project writes must not bump the session API revision")
}

func TestProjectAPIStoreConcurrentBindsKeepOneOpenMapping(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epochOne := projectEpoch(t, tracker, "Epoch one")
	epochTwo := projectEpoch(t, tracker, "Epoch two")
	store := tasks.NewProjectAPIStoreWithDB(db, timeouts.TestProfile(), newProjectClock())
	hash := projectHash("c")

	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, epoch := range []string{epochOne, epochTwo} {
		wg.Add(1)
		go func(epoch string) {
			defer wg.Done()
			_, err := store.Bind(context.Background(), hash, tasks.ProjectBindRequest{EpochID: epoch})
			results <- err
		}(epoch)
	}
	wg.Wait()
	close(results)

	// The shared DSN serializes writers (immediate transactions), so both binds
	// may succeed one after the other; a bind that does fail must be a typed
	// conflict or store fault, and the one-open index must never leave two open
	// rows.
	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		require.True(t,
			errors.Is(err, tasks.ErrProjectBindingConflict) || errors.Is(err, tasks.ErrProjectStoreFault),
			"a concurrent project bind must lose with a typed conflict or store fault, got: %v", err)
	}
	require.GreaterOrEqual(t, successes, 1, "at least one concurrent project bind must succeed")

	var open int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM pasture_project_epoch_binding
		WHERE project_hash = ? AND unbound_at IS NULL`, hash).Scan(&open))
	require.Equal(t, 1, open, "concurrent binds must leave exactly one open project mapping")
}

func TestProjectAPIStoreClassifiesStoreFaults(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	epoch := projectEpoch(t, tracker, "Epoch")
	store := tasks.NewProjectAPIStoreWithDB(db, timeouts.TestProfile(), newProjectClock())
	ctx := context.Background()

	// An expired caller deadline is a transaction failure, not a request fault.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := store.Bind(cancelled, projectHash("f"), tasks.ProjectBindRequest{EpochID: epoch})
	require.True(t, errors.Is(err, tasks.ErrProjectStoreFault), "want store fault, got %v", err)
	require.True(t, errors.Is(err, context.Canceled), "the cause must stay reachable: %v", err)
	require.False(t, errors.Is(err, tasks.ErrProjectRequest))

	// A closed handle cannot run any statement.
	require.NoError(t, db.Close())
	_, err = store.Bind(ctx, projectHash("f"), tasks.ProjectBindRequest{EpochID: epoch})
	require.True(t, errors.Is(err, tasks.ErrProjectStoreFault), "want store fault, got %v", err)
	require.False(t, errors.Is(err, tasks.ErrProjectRequest))

	_, err = store.Unbind(ctx, projectHash("f"), "")
	require.True(t, errors.Is(err, tasks.ErrProjectStoreFault), "want store fault, got %v", err)
}
