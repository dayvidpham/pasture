package audit

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/stretchr/testify/require"
)

func TestCancelledMigrationStepRollsBackWholeStep(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "atomic.db")
	db, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, ensureSchema(db))
	require.NoError(t, runStep(t.Context(), db, migrationSteps()[0])) // earlier step remains committed.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	written, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	joined, released := false, false
	t.Cleanup(func() {
		cancel()
		if !released {
			close(release)
		}
		if !joined {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("migration worker did not join")
				<-done
			}
		}
	})
	step := migrationStep{fromVersion: 2, toVersion: 3, apply: func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec("CREATE TABLE incomplete_step (v INTEGER)")
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO incomplete_step VALUES (7)")
		if err != nil {
			return err
		}
		close(written) // both schema and data written; stamp not written.
		<-release
		return writeVersion(tx, 3, now)
	}}
	go func() { done <- runStep(ctx, db, step) }()
	select {
	case <-written:
	case <-time.After(2 * time.Second):
		t.Fatal("step did not reach first-write boundary")
	}
	cancel()
	close(release)
	released = true
	select {
	case err := <-done:
		joined = true
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(600 * time.Millisecond):
		t.Fatal("step did not finish within cancellation tier")
	}
	// Acquiring a fresh connection joins database/sql's cancellation rollback.
	version, err := readVersion(db)
	require.NoError(t, err)
	require.Equal(t, 2, version)
	var count int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='incomplete_step'").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.Close())
	reopened, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
	require.NoError(t, err)
	defer reopened.Close()
	version, err = readVersion(reopened)
	require.NoError(t, err)
	require.Equal(t, 2, version)
}

type backoffObservedContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *backoffObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestOpenRetryCancellationDuringBackoff(t *testing.T) {
	t.Parallel()
	parent, cancel := context.WithCancel(t.Context())
	ctx := &backoffObservedContext{Context: parent, waiting: make(chan struct{})}
	done := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("retry did not join")
				<-done
			}
		}
	})
	attempts := 0
	go func() {
		done <- retryOnBusy(ctx, busyRetryPolicy{ceiling: time.Minute, initialDelay: time.Minute, maxDelay: time.Minute}, "test.db", "initialize schema", func() error { attempts++; return errBusy })
	}()
	select {
	case <-ctx.waiting:
	case <-time.After(time.Second):
		t.Fatal("retry never entered cancellation-selecting backoff")
	}
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		joined = true
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cancelled backoff did not finish promptly")
	}
	require.Less(t, time.Since(start), 100*time.Millisecond)
	require.Equal(t, 1, attempts)
}

func TestContextMigrationAndAuditPoolCancellation(t *testing.T) {
	t.Parallel()
	db, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), filepath.Join(t.TempDir(), "pool.db"), timeouts.ProductionProfile())
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.ErrorIs(t, MigrateContext(ctx, db), context.DeadlineExceeded)
	require.Less(t, time.Since(start), 125*time.Millisecond)
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, MigrateContext(ctx, db), context.Canceled)
	trail, err := NewSqliteAuditTrailContext(ctx, filepath.Join(t.TempDir(), "cancelled.db"))
	require.Nil(t, trail)
	require.ErrorIs(t, err, context.Canceled)
	require.Error(t, MigrateContext(nil, db))
	trail, err = NewSqliteAuditTrailContext(nil, "unused.db")
	require.Nil(t, trail)
	require.Error(t, err)
}

func TestOpenRetryCancellationPreventsAnotherAttempt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attempts := 0
	err := retryOnBusy(ctx, defaultBusyRetryPolicy(), "test.db", "set up schema", func() error {
		attempts++
		cancel()
		return context.Canceled
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, attempts)
	require.Equal(t, 30*time.Second, busyRetryCeiling)
}

func TestMigrateContextRealWriteLockCancelsWithoutReleasingHolder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "migration-lock.db")
	db, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, ensureSchema(db))
	holder, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
	require.NoError(t, err)
	defer holder.Close()
	tx, err := holder.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Exec("CREATE TABLE held_migration (v INTEGER)")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.ErrorIs(t, MigrateContext(ctx, db), context.DeadlineExceeded)
	require.Less(t, time.Since(start), timeouts.ProductionProfile().SQLiteBusy()+100*time.Millisecond)
	version, err := readVersion(db)
	require.NoError(t, err)
	require.Equal(t, 1, version, "the waiting migrator must not stamp a partial step")
}
