package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/laneserve"
	"github.com/dayvidpham/pasture/internal/lifecycle/lanewire"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/writelane"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// daemonLane is the daemon's hosting of the lifecycle write lane: the elected
// socket, the connection server that reads and decides, and the ONE lane
// goroutine that commits.
type daemonLane struct {
	budget    writelane.WriteLaneBudget
	socket    string
	lane      *writelane.Lane
	listener  *laneserve.Lane
	cancel    context.CancelFunc
	serveDone chan error
}

// laneRequestTimeout bounds ONE served request on the daemon's side.
//
// IT IS THE WORKFLOW-RESULT TIER AND NOT THE HOOK-INVOCATION TIER. The hook
// abandons at its own hook-invocation deadline; the daemon's bound exists only
// so a peer that sends a frame and then stalls cannot pin a goroutine forever.
// A record already admitted still commits on the lane's own context after the
// connection ends, so this bound never turns a committed record into a lost
// one — the hook reports record-unknown, which is the truth.
func laneRequestTimeout(profile timeouts.Profile) time.Duration { return profile.WorkflowResult() }

// startDaemonLane wires and launches the lane. It returns nil when there is no
// tracker to serve from, which is the in-memory audit backend: the lane is a
// store-serializing role and has nothing to serialize without one.
func startDaemonLane(parent context.Context, tracker protocol.TaskTracker, dbPath string, profile timeouts.Profile, logger *slog.Logger) (*daemonLane, error) {
	if tracker == nil {
		logger.Info("lifecycle write lane not started", "reason", "this daemon has no unified store handle (audit trail is in-memory)")
		return nil, nil
	}

	budget, err := writelane.BudgetFromProfile(profile)
	if err != nil {
		return nil, fmt.Errorf("pastured: the lifecycle write lane budget could not be derived from the daemon's timeout profile, so no single-writer lane can be hosted: %w", err)
	}
	lane, err := writelane.New(budget)
	if err != nil {
		return nil, fmt.Errorf("pastured: the lifecycle write lane could not be constructed: %w", err)
	}

	host, err := tasks.NewLifecycleLaneHost(tracker, tasks.LaneClock{}, tasks.LaneOperationIDs{}, profile)
	if err != nil {
		_ = lane.Close(context.Background())
		return nil, fmt.Errorf("pastured: the lifecycle write lane host could not be assembled: %w", err)
	}
	server, err := handlers.NewHookLifecycleServer(handlers.HookLifecycleServerConfig{
		Tracker:     host.Tracker(),
		Lane:        lane,
		Clock:       host.Clock(),
		Operations:  host.Operations(),
		Profile:     host.Profile(),
		Readers:     host.NewGateReaderFactory(),
		Diagnostics: os.Stderr,
	})
	if err != nil {
		_ = lane.Close(context.Background())
		return nil, fmt.Errorf("pastured: the served lifecycle path could not be constructed: %w", err)
	}
	connServer, err := laneserve.NewServer(server, laneRequestTimeout(profile))
	if err != nil {
		_ = lane.Close(context.Background())
		return nil, fmt.Errorf("pastured: the lifecycle lane connection server could not be constructed: %w", err)
	}

	socketPath := lanewire.SocketPath(dbPath)
	listener, err := laneserve.Listen(socketPath, connServer)
	if err != nil {
		_ = lane.Close(context.Background())
		if errors.Is(err, laneserve.ErrLaneHeld) || errors.Is(err, laneserve.ErrNoTransport) {
			// TWO DAEMONS ON ONE STORE ARE LEGITIMATE (a handover, or the
			// concurrent-open race the audit migrator already handles), and
			// exactly one of them may hold the lane. The holder serves every
			// hook for this store through the one socket, so this process gives
			// up the LANE ROLE and keeps serving everything else rather than
			// refusing to start: exiting here would take the durable engine down
			// with it, which is a larger loss than the role it lost.
			// A platform with no lane transport is the same ending for the same
			// reason: this daemon serves everything except the lane, and the hook
			// writes directly.
			logger.Warn("this daemon runs without the lifecycle write lane; the holder of this store's lane, or the direct write path, serves its hooks",
				"socket", socketPath, "err", err)
			return nil, nil
		}
		return nil, fmt.Errorf("pastured: the lifecycle write lane could not take its socket: %w", err)
	}

	laneCtx, cancel := context.WithCancel(parent)
	serveDone := make(chan error, 1)
	go func() { serveDone <- listener.Serve(laneCtx) }()

	logger.Info("lifecycle write lane listening",
		"socket", socketPath,
		"queueRecords", budget.QueueRecords,
		"queueBytes", budget.QueueBytes,
		"batchRecords", budget.BatchRecords,
		"batchBytes", budget.BatchBytes,
		"flushInterval", budget.FlushInterval,
		"holdTarget", budget.HoldTarget,
	)
	return &daemonLane{budget: budget, socket: socketPath, lane: lane, listener: listener, cancel: cancel, serveDone: serveDone}, nil
}

// Close stops the lane in the order that keeps the daemon's promise: stop
// accepting, let in-flight requests finish inside the budget, and only then
// drain the lane. A record still queued when the budget ends is lost, and every
// hook waiting on it reports record-unknown — never recorded.
func (l *daemonLane) Close(ctx context.Context, logger *slog.Logger) error {
	if l == nil {
		return nil
	}
	l.cancel()
	var firstErr error
	if err := l.listener.Close(ctx); err != nil {
		firstErr = err
		logger.Error("the lifecycle write lane did not finish its in-flight connections inside its stop budget", "socket", l.socket, "err", err)
	}
	select {
	case err := <-l.serveDone:
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			logger.Error("the lifecycle write lane stopped with an accept failure", "socket", l.socket, "err", err)
		}
	default:
	}
	if err := l.lane.Close(ctx); err != nil {
		if firstErr == nil {
			firstErr = err
		}
		logger.Error("the lifecycle write lane did not drain its staged records inside its stop budget", "socket", l.socket, "err", err)
	}
	stats := l.lane.Stats()
	logger.Info("lifecycle write lane stopped",
		"socket", l.socket,
		"committedRecords", stats.Records,
		"batches", stats.Batches,
		"maxBatchRecords", stats.MaxBatchRecords,
		"maxHold", stats.MaxHold,
		"holdExceeded", stats.HoldExceeded,
	)
	return firstErr
}
