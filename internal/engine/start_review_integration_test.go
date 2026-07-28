package engine_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// This fixture covers the documented activity-only construction boundary. The
// governed review path is exercised with the unified tracker in the task
// aggregate tests; a direct Provenance tracker must remain a valid sink without
// being treated as an allocator host.
func TestEngineNewLaunchAcceptsDirectActivitySink(t *testing.T) {
	t.Parallel()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	tracker, err := provenance.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("open direct Provenance activity sink: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	executorID, appVersion := testEngineIdentity(t)
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		QueueBasePollingInterval: 100 * time.Millisecond,
		Tracker:                  tracker,
	})
	if err != nil {
		t.Fatalf("engine.New with direct activity sink: %v", err)
	}
	if err := e.Launch(); err != nil {
		t.Fatalf("engine.Launch with direct activity sink: %v", err)
	}
	t.Cleanup(func() { e.Shutdown(5 * time.Second) })

	final := runEpoch(t, e, "activity-only-engine", fullEpochPlan())
	if final.CurrentPhase != protocol.PhaseComplete {
		t.Fatalf("final phase = %q, want complete", final.CurrentPhase)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open activity verification database: %v", err)
	}
	defer db.Close()
	if got := countRows(t, db, `SELECT COUNT(*) FROM activities`); got != 12 {
		t.Fatalf("transition activities = %d, want 12", got)
	}
}
