package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/stretchr/testify/require"
)

func TestEngineProjectionSetupReceivesCancelledContext(t *testing.T) {
	t.Parallel()
	db, err := dbconn.OpenSharedDBContextWithProfile(t.Context(), filepath.Join(t.TempDir(), "projection.db"), timeouts.ProductionProfile())
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var setupErr error
	t.Cleanup(func() { cancel(); _ = conn.Close(); joinProjectionWorker(t, done); _ = db.Close() })
	cancel()
	go func() { defer close(done); setupErr = ensureProjectionTableContext(ctx, db) }()
	const schedulingTolerance = 75 * time.Millisecond
	select {
	case <-done:
	case <-time.After(schedulingTolerance):
		_ = conn.Close()
		joinProjectionWorker(t, done)
		t.Fatal("engine projection setup ignored cancelled context while waiting for its one-connection pool")
	}
	require.ErrorIs(t, setupErr, context.Canceled)
}

func joinProjectionWorker(t *testing.T, done <-chan struct{}) {
	t.Helper()
	const joinDiagnosticCeiling = 5 * time.Second
	select {
	case <-done:
		return
	case <-time.After(joinDiagnosticCeiling):
		t.Errorf("projection worker failed to join within %s after cancellation and connection release; retaining the store until it stops", joinDiagnosticCeiling)
		// Retain the store until join, rather than abandoning a worker at the
		// diagnostic ceiling and letting TempDir cleanup race its SQL.
		<-done
	}
}
