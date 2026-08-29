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
// opens its OWN database file: testutil.GoldenUnifiedDBPath copies the golden
// database into a fresh temporary directory on every call. The engines
// therefore never queue for the same file lock.
const spikeWorkers = 8

// spikeTimeoutProfile widens the three inner waits of the production profile by
// the number of engines the test starts.
//
// WHAT THE MULTIPLIER IS FOR, stated exactly, because the obvious reading is
// wrong: it is NOT lock contention. Each engine has its own file, so no two
// engines wait for one lock. What the multiplier answers is processor
// oversubscription. Each engine keeps two writers on its own file, the audit
// trail and the durable runtime, so the test asks about 2*spikeWorkers writer
// goroutines to share the machine's cores, against two writers in production.
// A writer that already HOLDS its file lock is descheduled far more often under
// that load, so it holds the lock for longer, and the peer on the same file
// must absorb a wait that grows with the oversubscription. The budget is scaled
// by the same factor as the load, which is the only factor the test controls.
//
// The outermost wait is deliberately NOT scaled. It is the ceiling a caller
// uses to notice that a workflow is stuck, and multiplying it would turn a
// four-minute hang into a passing test. It keeps the production value.
//
// timeouts.ProductionProfile is not changed. The profile reaches the audit trail
// and the durable-runtime handle through the public seam engine.Config.Timeouts.
// See https://github.com/dayvidpham/pasture/issues/104.
func spikeTimeoutProfile(t *testing.T) timeouts.Profile {
	t.Helper()
	base := timeouts.ProductionProfile()
	profile, err := timeouts.New(
		timeouts.Test,
		spikeWorkers*base.SQLiteBusy(),
		spikeWorkers*base.Ingress(),
		spikeWorkers*base.StartSlice(),
		spikeWorkers*base.HookInvocation(),
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

// holdWriteLock takes the SQLite write lock on dbPath and holds it until the
// caller releases it. _txlock=immediate in the shared connection string makes
// Begin take the write lock at once, so the lock is already held when this
// function returns.
//
// The caller decides when to let go, so no assertion depends on wall-clock
// timing. release frees the lock; waitUntilFree returns once the lock is gone
// and the blocking handle is closed. Calling release more than once is safe.
func holdWriteLock(t *testing.T, dbPath string, profile timeouts.Profile) (release func(), waitUntilFree func()) {
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
	let := make(chan struct{})
	freed := make(chan struct{})
	go func() {
		defer close(freed)
		<-let
		_ = tx.Rollback()
		_ = blocker.Close()
	}()
	return sync.OnceFunc(func() { close(let) }), func() { <-freed }
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
//
// Neither half depends on winning a race. The tight half holds the lock until
// the write has already given up, so the write cannot slip through. The
// generous half releases the lock after a small fraction of its budget, so the
// write cannot be starved.
//
// The tight half would otherwise pass with ANY budget, because a write blocked
// for its whole life always fails, so it also reads HOW LONG the write waited.
// A trail that had fallen back to a longer budget holds on past the ceiling
// below. That is what makes the tight half sensitive to the value under test
// and not merely to the lock.
func TestEngineAuditTrail_UsesTheConfiguredSQLiteLockBudget(t *testing.T) {
	t.Parallel()

	// The generous budget is four seconds and the tight budget is 25 ms. The
	// generous half releases the lock after generousLockHold, which is a
	// twentieth of that budget.
	const generousLockHold = 200 * time.Millisecond
	generous := spikeTimeoutProfile(t)
	tight := timeouts.DeadlineTestProfile()
	// tightWaitCeiling is the longest the write may take to report the lock.
	//
	// It sits between the two budgets in this test AND below the production
	// budget of 500 ms, so it separates the configured 25 ms from the generous
	// 4 s and from a silent fall back to production. cmd/pastured's
	// TestInitAuditTrail_GivesTheDaemonTrailTheConfiguredLockBudget bounds the
	// same kind of write by the same rule, at half the production budget.
	//
	// The warm-up above is what makes so tight a ceiling honest. Measured at
	// engine -race -count=3, three times, on a busy 32-core machine: nine
	// samples between 25.5 ms and 33.7 ms, one budget plus scheduling. Without
	// the warm-up the same runs gave 82 ms to 322 ms, because the first write
	// for a role issues several statements and each waits its own budget. The
	// ceiling therefore has about twelve times the observed maximum in hand.
	const tightWaitCeiling = 400 * time.Millisecond

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

			// Warm the write path before the lock is taken. The FIRST forensic
			// write for a role also finds or creates that role's agent rows, so
			// it issues several statements in turn and each one can wait its own
			// budget. Warming leaves the measured write below as a single
			// blocked insert, so the wait it reports is one budget rather than a
			// sum of them.
			if err := e.Trail().RecordEvent(t.Context(), spikeAuditEvent("supervisor")); err != nil {
				t.Fatalf("warm the forensic write path before taking the lock: %v", err)
			}

			release, waitUntilFree := holdWriteLock(t, dbPath, generous)
			defer waitUntilFree()
			defer release()
			if !tc.wantLocked {
				// Let go while the write is still inside its budget.
				timer := time.AfterFunc(generousLockHold, release)
				defer timer.Stop()
			}

			start := time.Now()
			err = e.Trail().RecordEvent(t.Context(), spikeAuditEvent("supervisor"))
			waited := time.Since(start)
			// The tight half holds the lock for the whole write, so releasing
			// here cannot have helped the write that already returned.
			release()

			locked := err != nil && strings.Contains(err.Error(), "database is locked")
			switch {
			case tc.wantLocked && !locked:
				t.Fatalf("recording an audit event with a %s lock budget succeeded (err=%v) while another writer held the lock for the whole call; want a database-is-locked failure, which is what proves the configured budget is the one in force",
					tc.profile.SQLiteBusy(), err)
			case !tc.wantLocked && err != nil:
				t.Fatalf("recording an audit event with a %s lock budget failed while the lock was held for only %s: %v; the budget is far above the hold, so the write had to wait and then succeed",
					tc.profile.SQLiteBusy(), generousLockHold, err)
			}
			if tc.wantLocked {
				if waited > tightWaitCeiling {
					t.Errorf("the write waited %s before reporting the lock, above the ceiling of %s; it was configured with a budget of %s, so a wait this long means that budget is not the one in force",
						waited, tightWaitCeiling, tc.profile.SQLiteBusy())
				}
				var structured *pasterrors.StructuredError
				if !errors.As(err, &structured) {
					t.Fatalf("lock failure is %T, want a structured error a reader can act on", err)
				}
				if structured.Category != pasterrors.CategoryStorage {
					t.Errorf("lock failure category = %v, want %v; the caller routes on the category, so a wrong one sends the reader to the wrong fix",
						structured.Category, pasterrors.CategoryStorage)
				}
			}
		})
	}
}
