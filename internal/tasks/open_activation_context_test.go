//go:build provenance_testhooks

package tasks

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
)

// No fake opener: the barrier pauses the real unified constructor only after
// audit migration and defensive DDL. Provenance's tagged observer proves a
// real contended BEGIN before cancellation, with the holder still locked.
func TestUnifiedPostAuditActivationCancellation(t *testing.T) {
	t.Parallel()
	t.Run("cancel without deadline", func(t *testing.T) { testUnifiedActivationCancellation(t, false) })
	t.Run("deadline", func(t *testing.T) { testUnifiedActivationCancellation(t, true) })
}

func testUnifiedActivationCancellation(t *testing.T, deadline bool) {
	path := filepath.Join(t.TempDir(), "activation.db")
	holder, err := dbconn.OpenDefaultDB(path)
	require.NoError(t, err)
	holder.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, holder.Close()) })
	ctx, cancel := context.WithCancel(t.Context())
	if deadline {
		cancel()
		ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
	}
	defer cancel()
	ready, release, contended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	type result struct {
		tracker protocol.TaskTracker
		err     error
	}
	done := make(chan result, 1)
	joined, barrierReleased, locked := false, false, false
	t.Cleanup(func() {
		cancel()
		if !barrierReleased {
			close(release)
		}
		if locked {
			_, _ = holder.Exec("ROLLBACK")
		}
		if !joined {
			select {
			case got := <-done:
				if got.tracker != nil {
					_ = got.tracker.Close()
				}
			case <-time.After(3 * time.Second):
				t.Error("opener did not join")
				got := <-done
				if got.tracker != nil {
					_ = got.tracker.Close()
				}
			}
		}
	})
	cfg := productionOpenDefaults()
	cfg.diagnostics = io.Discard
	cfg.beforeActivation = func(ctx context.Context, db *sql.DB) error {
		var version int
		if err := db.QueryRowContext(ctx, "SELECT MAX(version) FROM audit_schema_meta").Scan(&version); err != nil {
			return err
		}
		if version != 9 {
			return io.ErrUnexpectedEOF
		}
		close(ready)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	seen := false
	cfg.activationObserver = func(phase string) {
		if phase == "contended" && !seen {
			seen = true
			close(contended)
		}
	}
	go func() { tracker, err := openTaskTrackerWithOptions(ctx, path, cfg); done <- result{tracker, err} }()
	select {
	case <-ready:
	case got := <-done:
		joined = true
		if got.tracker != nil {
			_ = got.tracker.Close()
		}
		t.Fatalf("before barrier: %v", got.err)
	case <-time.After(5 * time.Second):
		t.Fatal("setup did not reach barrier")
	}
	_, err = holder.Exec("BEGIN IMMEDIATE; CREATE TABLE holder (v INTEGER); INSERT INTO holder VALUES (1)")
	require.NoError(t, err)
	locked = true
	close(release)
	barrierReleased = true
	select {
	case <-contended:
	case got := <-done:
		joined = true
		if got.tracker != nil {
			_ = got.tracker.Close()
		}
		t.Fatalf("activation ended without contention: %v", got.err)
	case <-time.After(2 * time.Second):
		t.Fatal("no real contended activation")
	}
	// Cancellation occurs only after an observed real failed BEGIN.
	cause := context.Canceled
	if deadline {
		<-ctx.Done()
		cause = context.DeadlineExceeded
	} else {
		cancel()
	}
	start := time.Now()
	select {
	case got := <-done:
		joined = true
		if got.tracker != nil {
			_ = got.tracker.Close()
		}
		require.Nil(t, got.tracker)
		require.ErrorIs(t, got.err, cause)
		const schedulerTolerance = 30 * time.Millisecond
		require.Less(t, time.Since(start), timeouts.ProductionProfile().SQLiteBusy()+schedulerTolerance)
	case <-time.After(530 * time.Millisecond):
		t.Fatal("opener outlived cancellation tier including cleanup while holder remained locked")
	}
	_, err = holder.Exec("ROLLBACK")
	require.NoError(t, err)
	locked = false
	pool, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
	require.NoError(t, err)
	defer pool.Close()
	var count int
	require.NoError(t, pool.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='holder'").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, pool.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='journal'").Scan(&count))
	require.Zero(t, count, "incomplete activation must not publish its schema or replay seeds")
	require.NoError(t, pool.QueryRow("SELECT count(*) FROM audit_events").Scan(&count))
	require.Zero(t, count, "no false lifecycle receipt")
	require.NoError(t, pool.QueryRow("SELECT MAX(version) FROM audit_schema_meta").Scan(&count))
	require.Equal(t, 9, count, "earlier audit migration remains committed")
}
