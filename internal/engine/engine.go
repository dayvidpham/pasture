// Package engine is the durable-execution adapter for the pasture epoch
// lifecycle. It owns the shared modernc SQLite handle, registers and drives the
// pure-Go EpochStateMachine over durable steps, persists an EpochState
// projection each transition, and records forensic rows exactly once.
//
// The state machine itself lives in pkg/protocol and has no substrate
// dependency; this package is the impure adapter around it.
package engine

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// DefaultExecutorID is the pinned DBOS executor id. DBOS filters crash-recovery
// by ExecutorID + ApplicationVersion; pinning the executor id keeps recovery
// attributable to "pasture" across restarts rather than a per-process default.
const DefaultExecutorID = "pasture"

// DefaultAppName is the pinned DBOS application name.
const DefaultAppName = "pasture"

// Config configures an Engine.
type Config struct {
	// DBPath is the unified pasture.db path. Required.
	DBPath string
	// ApplicationVersion is the pinned DBOS application version. REQUIRED:
	// DBOS recovery is filtered by it, and it defaults to a binary hash, so a
	// rebuilt binary would skip recovery of an in-flight epoch unless this is
	// pinned to a stable value across builds. New rejects an empty value.
	ApplicationVersion string
	// ExecutorID overrides DefaultExecutorID. Pinned across restarts.
	ExecutorID string
	// AppName overrides DefaultAppName.
	AppName string
	// Trail is the forensic sink for one audit row per transition. When nil,
	// New opens an owned SQLite trail on DBPath (also migrating the file to the
	// current schema, which creates the dedup_key column).
	Trail audit.Trail
	// Specs overrides the canonical phase transition table (for tests). nil →
	// protocol.PhaseSpecs.
	Specs map[protocol.PhaseId]protocol.PhaseSpec
	// Logger is the DBOS logger. nil → slog.Default().
	Logger *slog.Logger
	// OnTransition, when set, runs INSIDE the durable step for each successful
	// transition, AFTER the projection + forensic audit row are written and
	// BEFORE the step returns. It is the step-bracketing seam: idempotent
	// activity recording wires here (it shares the step's replay semantics, so
	// any external write it makes must be idempotent — e.g. a deterministic-id
	// ON CONFLICT insert). Returning an error fails the step (and so the
	// transition's durable commit).
	//
	// stepSeq is the deterministic per-transition step sequence (the same value
	// the audit dedup key is derived from). It is threaded in from the workflow
	// body because it cannot be recovered inside the hook: DBOS exposes it only
	// in the workflow body, and a replay re-runs only the crashed step, so a
	// hook-local counter would not be replay-stable. Hooks derive their own
	// deterministic keys from it via protocol.DedupKey.
	OnTransition func(ctx context.Context, epochId string, rec *protocol.TransitionRecord, stepSeq string) error
}

// Engine owns the shared modernc handle, the DBOS context, and the forensic
// trail. It registers and drives the EpochStateMachine over durable steps.
//
// Lifecycle: New → Launch → (run workflows) → Shutdown.
type Engine struct {
	cfg         Config
	db          *sql.DB
	dbosCtx     dbos.DBOSContext
	trail       audit.Trail
	trailCloser io.Closer
	specs       map[protocol.PhaseId]protocol.PhaseSpec
	launched    bool
}

// New constructs an Engine: opens the shared handle with the WAL/busy-timeout
// DSN, ensures the projection table, opens (or adopts) the forensic trail,
// creates the DBOS context with the shared handle as SqliteSystemDB and the
// pinned ExecutorID + ApplicationVersion, and registers EpochWorkflow.
//
// The returned Engine is NOT yet launched; call Launch to run the recovery
// sweep and accept work. Always call Shutdown to release handles.
func New(ctx context.Context, cfg Config) (*Engine, error) {
	if cfg.DBPath == "" {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The durable engine was started without a database path.",
			Why:      "Config.DBPath was empty; the engine needs a pasture.db file to open its shared handle.",
			Where:    "Constructing the engine (internal/engine/engine.go in engine.New).",
			Impact:   "The engine can't open storage, so no epoch can run.",
			Fix:      "Set Config.DBPath to the unified pasture.db path (e.g. tasks.DefaultDBPath()).",
		}
	}
	if cfg.ApplicationVersion == "" {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The durable engine was started without a pinned application version.",
			Why: "Config.ApplicationVersion was empty. DBOS filters crash-recovery by application\n" +
				"version and defaults it to a binary hash, so a rebuilt binary would silently skip\n" +
				"recovering an in-flight epoch.",
			Where:  "Constructing the engine (internal/engine/engine.go in engine.New).",
			Impact: "Without a pinned version, a redeploy would not resume epochs that were mid-flight.",
			Fix:    "Pin Config.ApplicationVersion to a stable build-independent value (e.g. a release tag).",
		}
	}

	specs := cfg.Specs
	if specs == nil {
		specs = protocol.PhaseSpecs
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	executorID := cfg.ExecutorID
	if executorID == "" {
		executorID = DefaultExecutorID
	}
	appName := cfg.AppName
	if appName == "" {
		appName = DefaultAppName
	}

	// Forensic trail first: opening the SQLite trail migrates the file to the
	// current schema (creating audit_events + the dedup_key column the engine
	// writes), so the shared handle below sees a ready database.
	trail := cfg.Trail
	var trailCloser io.Closer
	if trail == nil {
		st, err := audit.NewSqliteAuditTrail(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf(
				"engine.New: failed to open the forensic audit trail on %q: %w — "+
					"the engine records one audit row per transition and needs the trail open",
				cfg.DBPath, err,
			)
		}
		trail = st
		trailCloser = st
	}

	db, err := dbconn.OpenSharedDB(cfg.DBPath)
	if err != nil {
		if trailCloser != nil {
			_ = trailCloser.Close()
		}
		return nil, err
	}

	if err := ensureProjectionTable(db); err != nil {
		_ = db.Close()
		if trailCloser != nil {
			_ = trailCloser.Close()
		}
		return nil, err
	}

	dbosCtx, err := dbos.NewDBOSContext(ctx, dbos.Config{
		AppName:            appName,
		SqliteSystemDB:     db,
		ExecutorID:         executorID,
		ApplicationVersion: cfg.ApplicationVersion,
		Logger:             logger,
	})
	if err != nil {
		_ = db.Close()
		if trailCloser != nil {
			_ = trailCloser.Close()
		}
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     "Couldn't initialize the durable-execution context.",
			Why:      "DBOS rejected the shared SQLite handle or its configuration.",
			Where:    "Constructing the engine (internal/engine/engine.go in engine.New).",
			Impact:   "The engine can't register or run durable workflows.",
			Fix: "1. Confirm the database file is a healthy SQLite file.\n" +
				"2. Confirm ApplicationVersion and ExecutorID are non-empty and stable.",
			Cause: err,
		}
	}

	e := &Engine{
		cfg:         cfg,
		db:          db,
		dbosCtx:     dbosCtx,
		trail:       trail,
		trailCloser: trailCloser,
		specs:       specs,
	}

	// Register the durable workflow before Launch so the recovery sweep can
	// resume in-flight epochs. The method value is stable across builds, which
	// (with the pinned ApplicationVersion) is what makes recovery survive
	// rebuilds.
	dbos.RegisterWorkflow(dbosCtx, e.EpochWorkflow)

	return e, nil
}

// Launch runs the DBOS recovery sweep (resuming any in-flight epochs) and makes
// the engine ready to run new workflows. Call exactly once after New.
func (e *Engine) Launch() error {
	if err := dbos.Launch(e.dbosCtx); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "Couldn't launch the durable-execution engine.",
			Why:      "DBOS failed during Launch (recovery sweep or executor startup).",
			Where:    "Launching the engine (internal/engine/engine.go in engine.Launch).",
			Impact:   "Epochs can't run or resume until the engine launches.",
			Fix:      "Check the database is reachable and not held exclusively by another process, then retry.",
			Cause:    err,
		}
	}
	e.launched = true
	return nil
}

// Shutdown stops the DBOS context (waiting up to timeout for in-flight steps),
// then closes the shared handle and the owned trail. Safe to call once.
func (e *Engine) Shutdown(timeout time.Duration) {
	if e.dbosCtx != nil {
		dbos.Shutdown(e.dbosCtx, timeout)
	}
	if e.db != nil {
		_ = e.db.Close()
	}
	if e.trailCloser != nil {
		_ = e.trailCloser.Close()
	}
}

// DBOS returns the underlying DBOS context so callers (and later slices) can
// RunWorkflow / Send / ListWorkflows against the engine's registered workflow.
func (e *Engine) DBOS() dbos.DBOSContext { return e.dbosCtx }

// DB returns the shared modernc handle (projection + DBOS tables live here).
func (e *Engine) DB() *sql.DB { return e.db }

// Trail returns the forensic trail the engine records transitions into.
func (e *Engine) Trail() audit.Trail { return e.trail }

// ReadProjection returns the projected EpochState for epochId, or (nil, nil) if
// the epoch has not advanced yet. This is the read side of the projection that
// query and status surfaces consume.
func (e *Engine) ReadProjection(epochId string) (*protocol.EpochState, error) {
	return ReadProjection(e.db, epochId)
}
