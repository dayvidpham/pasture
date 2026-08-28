package engine_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// spikeWorkers is how many engines this test runs at the same time. Each engine
// has its own database file, and each file has two writers on it: the audit
// trail and the durable runtime.
const spikeWorkers = 8

// spikeTimeoutProfile multiplies every wait in the production profile by the
// number of engines the test starts.
//
// The test creates the overlap, so the test owns the budget. Sixteen writers
// share one machine here, against two in production, so a writer can wait about
// spikeWorkers times longer for its turn. With the production budget a writer
// gave up after 500 ms and reported "database is locked", which is a property of
// the load this test creates and not a defect in the code under test. See
// https://github.com/dayvidpham/pasture/issues/104.
//
// timeouts.ProductionProfile is not changed. The profile reaches the audit trail
// and the durable-runtime handle through the public seam engine.Config.Timeouts.
func spikeTimeoutProfile(t *testing.T) timeouts.Profile {
	t.Helper()
	base := timeouts.ProductionProfile()
	profile, err := timeouts.New(
		timeouts.Test,
		spikeWorkers*base.SQLiteBusy(),
		spikeWorkers*base.Ingress(),
		spikeWorkers*base.StartSlice(),
		spikeWorkers*base.WorkflowResult(),
	)
	if err != nil {
		t.Fatalf("build the timeout profile for the overlap spike: %v", err)
	}
	return profile
}

func TestDBOSConcurrencySpike_IsolatedEnginesCanOverlap(t *testing.T) {
	t.Parallel()
	const workers = spikeWorkers
	profile := spikeTimeoutProfile(t)
	type workerCase struct {
		index int
		db    string
	}

	cases := make([]workerCase, workers)
	for i := range cases {
		cases[i] = workerCase{
			index: i,
			db:    testutil.GoldenUnifiedDBPath(t),
		}
	}

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for _, tc := range cases {
		tc := tc
		go func() {
			defer wg.Done()
			<-start
			if err := runDBOSConcurrencySpikeWorker(t.Context(), tc.index, tc.db, profile); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func runDBOSConcurrencySpikeWorker(ctx context.Context, worker int, dbPath string, profile timeouts.Profile) error {
	executorID := fmt.Sprintf("spike-executor-%02d", worker)
	appVersion := fmt.Sprintf("spike-app-%02d", worker)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	e, err := engine.New(ctx, engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
		Logger:                   logger,
		Timeouts:                 profile,
	})
	if err != nil {
		return fmt.Errorf("worker %d first engine.New: %w", worker, err)
	}
	if err := e.Launch(); err != nil {
		e.Shutdown(5 * time.Second)
		return fmt.Errorf("worker %d first Launch: %w", worker, err)
	}

	const fullEpochID = "spike-shared-full"
	final, err := runSpikeEpoch(ctx, e, fullEpochID)
	if err != nil {
		e.Shutdown(5 * time.Second)
		return fmt.Errorf("worker %d full workflow: %w", worker, err)
	}
	if final.CurrentPhase != protocol.PhaseComplete {
		e.Shutdown(5 * time.Second)
		return fmt.Errorf("worker %d final phase = %q, want %q", worker, final.CurrentPhase, protocol.PhaseComplete)
	}
	e.Shutdown(5 * time.Second)

	recovered, err := engine.New(ctx, engine.Config{
		DBPath:                   dbPath,
		ApplicationVersion:       appVersion,
		ExecutorID:               executorID,
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
		Logger:                   logger,
		Timeouts:                 profile,
	})
	if err != nil {
		return fmt.Errorf("worker %d recovery engine.New: %w", worker, err)
	}
	if err := recovered.Launch(); err != nil {
		recovered.Shutdown(5 * time.Second)
		return fmt.Errorf("worker %d recovery Launch: %w", worker, err)
	}
	defer recovered.Shutdown(5 * time.Second)

	proj, err := recovered.ReadProjection(fullEpochID)
	if err != nil {
		return fmt.Errorf("worker %d read recovered projection: %w", worker, err)
	}
	if proj == nil || proj.CurrentPhase != protocol.PhaseComplete {
		return fmt.Errorf("worker %d recovered projection = %+v, want complete", worker, proj)
	}

	const controlEpochID = "spike-shared-control"
	if _, err := dbos.RunWorkflow(recovered.DBOS(), recovered.EpochControlWorkflow,
		engine.ControlInput{EpochId: controlEpochID},
		dbos.WithWorkflowID(controlEpochID),
	); err != nil {
		return fmt.Errorf("worker %d control RunWorkflow: %w", worker, err)
	}
	sig := protocol.PhaseAdvanceSignal{ToPhase: protocol.PhaseElicit, TriggeredBy: "spike", ConditionMet: "overlap"}
	if err := dbos.Send(recovered.DBOS(), controlEpochID, sig, protocol.SignalAdvancePhase.String()); err != nil {
		return fmt.Errorf("worker %d control Send: %w", worker, err)
	}
	if err := waitSpikeProjection(recovered, controlEpochID, protocol.PhaseElicit, profile.WorkflowResult()); err != nil {
		return fmt.Errorf("worker %d control projection: %w", worker, err)
	}
	return nil
}

func runSpikeEpoch(ctx context.Context, e *engine.Engine, epochID string) (protocol.EpochState, error) {
	h, err := dbos.RunWorkflow(e.DBOS(), e.EpochWorkflow,
		engine.EpochInput{EpochId: epochID, Advances: fullEpochPlan()},
		dbos.WithWorkflowID(epochID),
	)
	if err != nil {
		return protocol.EpochState{}, err
	}
	return h.GetResult(dbos.WithHandleTimeout(e.Timeouts().WorkflowResult()))
}

func waitSpikeProjection(e *engine.Engine, epochID string, want protocol.PhaseId, ceiling time.Duration) error {
	deadline := time.NewTimer(ceiling)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		st, err := e.ReadProjection(epochID)
		if err != nil {
			return err
		}
		if st != nil && st.CurrentPhase == want {
			return nil
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			st, _ := e.ReadProjection(epochID)
			return fmt.Errorf("epoch %q did not reach %q within %s; last projection = %+v", epochID, want, ceiling, st)
		}
	}
}

// holdWriteLock takes the SQLite write lock on dbPath and holds it for hold.
// It returns a function that waits until the lock is free again.
//
// The hold is a deliberate stimulus, not a wait for a condition: the test needs
// a writer that is busy for a known time so it can compare that time against
// the lock budget the engine was configured with. _txlock=immediate in the
// shared connection string makes Begin take the write lock at once, so the lock
// is already held when this function returns.
func holdWriteLock(t *testing.T, dbPath string, profile timeouts.Profile, hold time.Duration) (release func()) {
	t.Helper()
	blocker, err := dbconn.OpenSharedDBWithProfile(dbPath, profile)
	if err != nil {
		t.Fatalf("open the blocking writer on %q: %v", dbPath, err)
	}
	tx, err := blocker.Begin()
	if err != nil {
		blocker.Close()
		t.Fatalf("take the SQLite write lock on %q: %v", dbPath, err)
	}
	freed := make(chan struct{})
	go func() {
		defer close(freed)
		time.Sleep(hold)
		_ = tx.Rollback()
		_ = blocker.Close()
	}()
	return func() { <-freed }
}

// spikeAuditEvent is one ordinary forensic write, the same shape the engine
// records for a phase transition.
func spikeAuditEvent(role string) protocol.AuditEvent {
	return protocol.AuditEvent{
		EpochId:   "spike-lock-budget",
		Phase:     protocol.PhaseElicit,
		Role:      role,
		EventType: protocol.EventPhaseTransition,
		Payload:   map[string]any{"from": "request", "to": "elicit"},
		Timestamp: time.Now().UTC(),
	}
}

// TestEngineAuditTrail_UsesTheConfiguredSQLiteLockBudget proves that the audit
// trail the engine opens for itself waits for the SQLite write lock exactly as
// long as Config.Timeouts says, in both directions.
//
// This is the write path that failed in continuous integration with
// "database is locked (5)" while several engines overlapped: the trail wrote
// the audit event, the agent row, and the agent name through a plain handle
// whose only defence is the busy timeout in the connection string. Before this
// wiring the trail always used the production profile and ignored
// Config.Timeouts, so a caller could not widen the budget for the load it
// created. See https://github.com/dayvidpham/pasture/issues/104.
func TestEngineAuditTrail_UsesTheConfiguredSQLiteLockBudget(t *testing.T) {
	t.Parallel()

	// One hold time, two profiles on either side of it with a wide margin:
	// the generous budget is about 5x the hold, the tight budget about 1/24 of
	// it. Neither verdict can flip on ordinary scheduling noise.
	const lockHold = 600 * time.Millisecond
	generous := spikeTimeoutProfile(t) // 4s SQLite lock wait
	tight := timeouts.DeadlineTestProfile()

	cases := []struct {
		name       string
		profile    timeouts.Profile
		wantLocked bool
	}{
		{
			name:       "budget above the lock hold: the write waits and succeeds",
			profile:    generous,
			wantLocked: false,
		},
		{
			name:       "budget below the lock hold: the write gives up and reports the lock",
			profile:    tight,
			wantLocked: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dbPath := testutil.GoldenUnifiedDBPath(t)
			executorID, appVersion := testEngineIdentity(t)
			e, err := engine.New(t.Context(), engine.Config{
				DBPath:             dbPath,
				ApplicationVersion: appVersion,
				ExecutorID:         executorID,
				SkipMigrations:     true,
				Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
				Timeouts:           tc.profile,
			})
			if err != nil {
				t.Fatalf("engine.New: %v", err)
			}
			t.Cleanup(func() { e.Shutdown(5 * time.Second) })

			if got := e.Timeouts().SQLiteBusy(); got != tc.profile.SQLiteBusy() {
				t.Fatalf("engine SQLite lock budget = %s, want the configured %s", got, tc.profile.SQLiteBusy())
			}

			waitForFreeLock := holdWriteLock(t, dbPath, generous, lockHold)
			err = e.Trail().RecordEvent(t.Context(), spikeAuditEvent("supervisor"))
			waitForFreeLock()

			locked := err != nil && strings.Contains(err.Error(), "database is locked")
			switch {
			case tc.wantLocked && !locked:
				t.Fatalf("recording an audit event with a %s lock budget behind a %s lock hold returned %v; want a database-is-locked failure, which is what proves the configured budget is the one in force",
					tc.profile.SQLiteBusy(), lockHold, err)
			case !tc.wantLocked && err != nil:
				t.Fatalf("recording an audit event with a %s lock budget behind a %s lock hold failed: %v; the budget is above the hold, so the write had to wait and then succeed",
					tc.profile.SQLiteBusy(), lockHold, err)
			}
			if tc.wantLocked {
				var structured *pasterrors.StructuredError
				if !errors.As(err, &structured) {
					t.Fatalf("lock failure is %T, want a structured error a reader can act on", err)
				}
			}
		})
	}
}
