package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func openCloseTestTracker(t *testing.T) *trackerImpl {
	t.Helper()
	opened, err := OpenTaskTracker(filepath.Join(t.TempDir(), "pasture.db"))
	require.NoError(t, err)
	tracker, ok := opened.(*trackerImpl)
	require.True(t, ok)
	// Explicitly clean the retained pool even on the pre-fix regression failure.
	t.Cleanup(func() {
		require.NoError(t, tracker.Close())
		require.NoError(t, tracker.auditDB.Close())
	})
	return tracker
}

func TestTrackerCloseReleasesRealOpenerOwnedPool(t *testing.T) {
	t.Parallel()
	tracker := openCloseTestTracker(t)
	pool := tracker.auditDB
	require.NoError(t, pool.PingContext(context.Background()))

	err := tracker.Close()

	require.NoError(t, err)
	require.ErrorContains(t, pool.PingContext(context.Background()), "database is closed",
		"the real opener's owned pool must reject access after its tracker closes")
	require.Zero(t, pool.Stats().OpenConnections)
	require.NoError(t, tracker.Close(), "repeated Close returns the cached result")
}

func TestTrackerCloseKeepsBorrowerAndOwnerLifetimesDistinct(t *testing.T) {
	t.Parallel()
	tracker := openCloseTestTracker(t)
	pool := tracker.auditDB

	err := tracker.prov.Close()

	require.NoError(t, err)
	require.NoError(t, pool.PingContext(context.Background()), "closing the real borrower must not close its parent's pool")
	require.NoError(t, tracker.Close())
	require.ErrorContains(t, pool.PingContext(context.Background()), "database is closed",
		"the owning tracker must still close the pool after the borrower closed itself")
}

func TestTrackerCloseWithoutOwnedPoolLeavesParentOpen(t *testing.T) {
	t.Parallel()
	owner := openCloseTestTracker(t)
	borrower, err := provenance.OpenBorrowedSQLite(owner.auditDB)
	require.NoError(t, err)
	// This wrapper owns no SQL pool. Only the explicit owner may close it.
	child := &trackerImpl{prov: borrower}

	require.NoError(t, child.Close())

	require.NoError(t, owner.auditDB.PingContext(context.Background()))
	require.NoError(t, child.Close())
	require.NoError(t, (&trackerImpl{}).Close(), "an empty test construction has no resources to close")
}

type closeTrackerDependency struct {
	provenance.Tracker
	close func() error
}

func (tracker closeTrackerDependency) Close() error { return tracker.close() }

type closeTrailDependency struct {
	audit.Trail
	close func() error
}

func (trail closeTrailDependency) Close() error { return trail.close() }

func TestTrackerCloseWaitsForBorrowerBeforeClosingOwnedPool(t *testing.T) {
	t.Parallel()
	tracker := openCloseTestTracker(t)
	borrower := tracker.prov
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	tracker.prov = closeTrackerDependency{
		Tracker: borrower,
		close: func() error {
			close(entered)
			<-release
			return borrower.Close()
		},
	}
	finished := make(chan error, 1)
	go func() { finished <- tracker.Close() }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("tracker Close did not enter its borrower shutdown")
	}
	// The dependency barrier models an unfinished borrower drain. It proves the
	// owner orders its shutdown after Close returns, not a database timing claim.
	require.NoError(t, tracker.auditDB.PingContext(context.Background()), "the borrower must retain a live parent pool while draining")
	select {
	case err := <-finished:
		t.Fatalf("tracker Close returned before its borrower settled: %v", err)
	default:
	}
	unblock()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("tracker Close did not finish after its borrower returned")
	}
	require.ErrorContains(t, tracker.auditDB.PingContext(context.Background()), "database is closed")
}

// The database/sql connector is a close-failure boundary double, not a second
// tracker implementation. It lets the real sql.DB.Close return a driver error.
type closeConnector struct {
	err   error
	calls atomic.Int32
}

func (connector *closeConnector) Connect(context.Context) (driver.Conn, error) {
	return closeConnection{connector: connector}, nil
}

func (connector *closeConnector) Driver() driver.Driver { return connector }

func (connector *closeConnector) Open(string) (driver.Conn, error) {
	return connector.Connect(context.Background())
}

type closeConnection struct{ connector *closeConnector }

func (connection closeConnection) Close() error {
	connection.connector.calls.Add(1)
	return connection.connector.err
}

func (closeConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("close-only test connector does not prepare statements")
}

func (closeConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("close-only test connector does not open transactions")
}

func TestTrackerCloseAggregatesEveryResourceErrorOnce(t *testing.T) {
	t.Parallel()
	borrowerErr := errors.New("borrower drain failed")
	trailErr := errors.New("audit trail close failed")
	poolErr := errors.New("owned pool driver close failed")
	connector := &closeConnector{err: poolErr}
	pool := sql.OpenDB(connector)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, pool.PingContext(context.Background()))
	var borrowerCalls, trailCalls atomic.Int32
	tracker := &trackerImpl{
		prov: closeTrackerDependency{close: func() error {
			borrowerCalls.Add(1)
			return borrowerErr
		}},
		trail: closeTrailDependency{close: func() error {
			trailCalls.Add(1)
			return trailErr
		}},
		auditDB: pool,
	}
	const callers = 8
	results := make(chan error, callers)
	var workers sync.WaitGroup
	for range callers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- tracker.Close()
		}()
	}
	workers.Wait()
	close(results)
	first := tracker.Close()
	for err := range results {
		require.Same(t, first, err, "concurrent Close callers must receive the same completed error result")
		require.ErrorIs(t, err, borrowerErr)
		require.ErrorIs(t, err, trailErr)
		require.ErrorIs(t, err, poolErr)
		var structured *pasterrors.StructuredError
		require.ErrorAs(t, err, &structured)
		require.Equal(t, pasterrors.CategoryStorage, structured.Category)
		require.Contains(t, err.Error(), "close Provenance tracker")
		require.Contains(t, err.Error(), "close audit trail")
		require.Contains(t, err.Error(), "close owned database pool")
	}
	require.EqualValues(t, 1, borrowerCalls.Load())
	require.EqualValues(t, 1, trailCalls.Load())
	require.EqualValues(t, 1, connector.calls.Load())
}

func TestTrackerCloseSupportsAliasedTrailPool(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("aliased pool close failed")
	connector := &closeConnector{err: closeErr}
	pool := sql.OpenDB(connector)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, pool.PingContext(context.Background()))
	tracker := &trackerImpl{
		trail:   closeTrailDependency{close: pool.Close},
		auditDB: pool,
	}

	err := tracker.Close()

	require.ErrorIs(t, err, closeErr, "an aliased pool's first close error must not be discarded")
	require.Same(t, err, tracker.Close())
	require.EqualValues(t, 1, connector.calls.Load(), "sql.DB.Close is idempotent even when the first driver Close fails")
	require.ErrorContains(t, pool.PingContext(context.Background()), "database is closed")
}
