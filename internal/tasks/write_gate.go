package tasks

import (
	"context"
	"sync"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/writeprofile"
)

// writeGate is a zero-value-ready single-writer semaphore. Both compatibility
// methods and context-bearing methods acquire the same token; cancellation does
// not detach a waiter or leave a goroutine acquiring the gate later.
// Unlock must be called exactly once after each successful acquisition.
type writeGate struct {
	once  sync.Once
	token chan struct{}
}

func (g *writeGate) acquire(ctx context.Context) error {
	defer writeprofile.Region("go.write_gate.wait")()
	if err := ctx.Err(); err != nil {
		return err
	}
	g.once.Do(func() { g.token = make(chan struct{}, 1) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case g.token <- struct{}{}:
		// Cancellation wins if it raced a release and both select arms were
		// ready. No dependency is called after a cancelled acquisition.
		if err := ctx.Err(); err != nil {
			g.Unlock()
			return err
		}
		return nil
	}
}

func (g *writeGate) Lock() { _ = g.acquire(context.Background()) }
func (g *writeGate) Unlock() {
	// Synchronize even a mistaken first unlock against token initialization,
	// and retain the old mutex's loud failure instead of deadlocking on misuse.
	g.once.Do(func() { g.token = make(chan struct{}, 1) })
	select {
	case <-g.token:
	default:
		panic("tasks: writeGate.Unlock called without a matching acquire")
	}
}

func (t *trackerImpl) lockWriteContext(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Couldn't acquire the task store's write gate.",
			Why:      "The caller supplied a nil context before write acquisition.",
			Where:    "Acquiring trackerImpl's write gate (internal/tasks/write_gate.go in lockWriteContext).",
			Impact:   "No write was delegated or performed.",
			Fix:      "Supply a live context for the operation, or context.Background for a caller without a cancellation budget.",
		}
	}
	if err := t.writeMu.acquire(ctx); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     "Stopped waiting for the task store's write gate.",
			Why:      "The caller's context was cancelled or expired before write acquisition completed.",
			Where:    "Acquiring trackerImpl's write gate (internal/tasks/write_gate.go in lockWriteContext).",
			Impact:   "No write was delegated or performed by this operation.",
			Fix:      "Retry with a live context after the current writer finishes; investigate contention if the caller's budget keeps expiring.",
			Cause:    err,
		}
	}
	return t.writeMu.Unlock, nil
}
