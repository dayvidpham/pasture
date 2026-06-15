package engine_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

func TestDBOSConcurrencySpike_IsolatedEnginesCanOverlap(t *testing.T) {
	t.Parallel()
	const workers = 8
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
			if err := runDBOSConcurrencySpikeWorker(t.Context(), tc.index, tc.db); err != nil {
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

func runDBOSConcurrencySpikeWorker(ctx context.Context, worker int, dbPath string) error {
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
	if err := waitSpikeProjection(recovered, controlEpochID, protocol.PhaseElicit); err != nil {
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
	return h.GetResult(dbos.WithHandleTimeout(30 * time.Second))
}

func waitSpikeProjection(e *engine.Engine, epochID string, want protocol.PhaseId) error {
	deadline := time.NewTimer(15 * time.Second)
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
			return fmt.Errorf("epoch %q did not reach %q in time; last projection = %+v", epochID, want, st)
		}
	}
}
