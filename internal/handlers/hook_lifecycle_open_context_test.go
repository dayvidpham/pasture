package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestLifecycleOpenerReceivesInvocationContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	raw := gateFixture(t, gateClaudeSessionStartFixture)
	reader := &gateFakeReader{}
	called := false
	open := func(got context.Context, _ string) (protocol.TaskTracker, error) {
		called = true
		require.Same(t, ctx, got)
		wantDeadline, _ := ctx.Deadline()
		gotDeadline, ok := got.Deadline()
		require.True(t, ok)
		require.Equal(t, wantDeadline, gotDeadline)
		cancel()
		require.ErrorIs(t, got.Err(), context.Canceled)
		return nil, got.Err()
	}
	_, err := hookLifecycle(ctx, gateInput(t, "unused.db", "SessionStart", raw), open, reader.gateFactory)
	require.True(t, called)
	require.ErrorIs(t, err, context.Canceled)
}

func TestNativeLifecycleOpenerWorkerFinishesUnderRealLock(t *testing.T) {
	// Measure the opener, not runnable delay behind parallel engine fixtures.
	path := gateStore(t)
	db, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
	require.NoError(t, err)
	defer db.Close()
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Exec("CREATE TABLE hook_lock_holder (v INTEGER)")
	require.NoError(t, err)
	in := gateInput(t, path, "SessionStart", gateFixture(t, gateClaudeSessionStartFixture))
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = HookLifecycleNative(ctx, in)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	elapsed := time.Since(start)
	t.Logf("native opener: 25ms caller, %s SQLite busy tier, elapsed=%s with holder retained", timeouts.ProductionProfile().SQLiteBusy(), elapsed)
	require.Less(t, elapsed, timeouts.ProductionProfile().SQLiteBusy()+100*time.Millisecond)
	// Holder has never been released: return proves worker completion, not
	// the command's outer select abandoning a Background opener.
}
