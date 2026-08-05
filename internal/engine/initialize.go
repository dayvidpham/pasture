package engine

import (
	"context"
	"io"
	"log/slog"
	"time"
)

// InitializeStorage creates or upgrades the projection and DBOS-owned schemas
// without launching workflow recovery, queues, schedulers, or an executor.
func InitializeStorage(ctx context.Context, dbPath string) error {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine, err := New(ctx, Config{
		DBPath:             dbPath,
		ApplicationVersion: DefaultApplicationVersion,
		ExecutorID:         DefaultExecutorID,
		AppName:            DefaultAppName,
		Logger:             logger,
	})
	if err != nil {
		return err
	}
	engine.Shutdown(5 * time.Second)
	return nil
}
