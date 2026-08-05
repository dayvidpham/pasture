package handlers

import (
	"context"
	"fmt"
	"io"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/storage"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// InitInput captures the inputs for `pasture init`.
type InitInput struct {
	// DBPath is the unified database path. Empty resolves through
	// tasks.DefaultDBPath.
	DBPath string
}

// Initialize creates or upgrades the complete unified database and registers
// Pasture's built-in agents. Repeated calls are idempotent.
func Initialize(ctx context.Context, w io.Writer, in InitInput, format types.OutputFormat) (int, error) {
	dbPath := in.DBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}

	tracker, cache, err := storage.OpenInitialized(ctx, dbPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}

	if err := tracker.Close(); err != nil {
		se := &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The pasture database at %q was initialized, but its handles couldn't be released.", dbPath),
			Why:      "Closing the unified TaskTracker failed after schema creation and built-in agent registration completed.",
			Where:    "Finalizing database initialization (internal/handlers/init.go in handlers.Initialize).",
			Impact:   "Initialization completed, but another process may briefly see the database as locked.",
			Fix:      "Wait a few seconds and retry the next command. If locking persists, stop the process holding the database and run pasture init again.",
			Cause:    err,
		}
		return pasterrors.ExitCode(se), se
	}
	out, err := formatters.FormatInitResult(formatters.InitResult{
		DBPath:        dbPath,
		BuiltInAgents: cache.Len(),
	}, format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(w, out)
	return 0, nil
}
