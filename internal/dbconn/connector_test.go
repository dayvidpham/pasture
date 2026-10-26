package dbconn

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/stretchr/testify/require"
)

func TestContextPoolSettingsAndReplacement(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	db, err := OpenSharedDBContextWithProfile(ctx, filepath.Join(t.TempDir(), "escaped?#%3F.db"), timeouts.ProductionProfile())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	cancel() // a replacement must use its own context, not this expired open.
	db.SetMaxIdleConns(0)
	for range 3 {
		conn, err := db.Conn(t.Context())
		require.NoError(t, err)
		var busy, sync, fk int
		var journal string
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busy))
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&sync))
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&fk))
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journal))
		wantBusy := int(timeouts.ProductionProfile().SQLiteBusy().Milliseconds())
		require.Equal(t, 1500, wantBusy, "production SQLite busy tier must be 1.5s; a stale 500ms pragma must not pass")
		require.Equal(t, wantBusy, busy)
		require.Equal(t, 1, sync)
		require.Equal(t, 1, fk)
		require.Equal(t, "wal", journal)
		require.NoError(t, conn.Close())
	}
}

func TestContextFirstConnectionLockedRollbackJournal(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "locked.db")
	holder, err := OpenDefaultDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, holder.Close()) })
	_, err = holder.Exec("CREATE TABLE held (v INTEGER)")
	require.NoError(t, err)
	_, err = holder.Exec("BEGIN IMMEDIATE; INSERT INTO held VALUES (1)")
	require.NoError(t, err) // real lock acquired before the first connector connection.
	t.Cleanup(func() { _, err := holder.Exec("ROLLBACK"); require.NoError(t, err) })
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	db, err := OpenSharedDBContextWithProfile(ctx, path, timeouts.ProductionProfile())
	require.Nil(t, db)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "%v", err)
	// The caller deadline above is 25ms. This ceiling is the fixed 500ms
	// lock-acquisition bound plus the scheduling allowance. It must stay
	// strictly below the production busy tier so a missed cancellation that
	// waits out that 1.5s tier cannot pass.
	const schedulerTolerance = 100 * time.Millisecond
	ceiling := 500*time.Millisecond + schedulerTolerance
	require.Less(t, ceiling, timeouts.ProductionProfile().SQLiteBusy())
	require.Less(t, time.Since(start), ceiling)
}

func TestContextConnectRejectsCancelledAndNil(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	db, err := OpenSharedDBContextWithProfile(ctx, filepath.Join(t.TempDir(), "unused.db"), timeouts.ProductionProfile())
	require.Nil(t, db)
	require.ErrorIs(t, err, context.Canceled)
	db, err = OpenSharedDBContextWithProfile(nil, "unused.db", timeouts.ProductionProfile())
	require.Nil(t, db)
	require.Error(t, err)
}

func TestContextPoolExpansionPreservesImmediateTransactions(t *testing.T) {
	t.Parallel()
	db, err := OpenSharedDBContextWithProfile(t.Context(), filepath.Join(t.TempDir(), "expanded.db"), timeouts.DeadlineTestProfile())
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(2)
	first, err := db.Conn(t.Context())
	require.NoError(t, err)
	defer first.Close()
	tx, err := first.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	second, err := db.Conn(t.Context()) // a new driver connection, not an idle pool hit.
	require.NoError(t, err)
	defer second.Close()
	var busy, fk, sync int
	var journal string
	require.NoError(t, second.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busy))
	require.NoError(t, second.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&fk))
	require.NoError(t, second.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&sync))
	require.NoError(t, second.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journal))
	require.Equal(t, 25, busy)
	require.Equal(t, 1, fk)
	require.Equal(t, 1, sync)
	require.Equal(t, "wal", journal)
	other, err := second.BeginTx(t.Context(), nil)
	if other != nil {
		_ = other.Rollback()
	}
	require.Error(t, err, "_txlock=immediate must take file write ownership at BEGIN, not at a later statement")
}
