package storage

import (
	"context"
	"errors"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// OpenInitialized serializes first-run migrations, opens the complete task and
// audit store, registers every built-in agent, and creates the engine-owned
// projection and DBOS schemas without launching an executor. The caller owns
// the returned tracker and must close it.
func OpenInitialized(ctx context.Context, dbPath string) (protocol.TaskTracker, *tasks.WellKnownAgentCache, error) {
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	lock, err := acquireInitializationLock(ctx, dbPath)
	if err != nil {
		return nil, nil, err
	}
	defer lock.Close()

	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		return nil, nil, err
	}

	cache := tasks.NewWellKnownAgentCache()
	if err := tasks.RegisterWellKnownAgents(ctx, tracker, cache); err != nil {
		return nil, nil, errors.Join(err, tracker.Close())
	}
	if err := engine.InitializeStorage(ctx, dbPath); err != nil {
		return nil, nil, errors.Join(err, tracker.Close())
	}
	return tracker, cache, nil
}
