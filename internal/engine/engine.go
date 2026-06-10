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

	"github.com/dayvidpham/provenance"
	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/hooks"
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
	// Tracker, when set, makes the engine record one PROV-O activity per
	// transition with a deterministic id (exactly-once across replay). nil ⇒
	// activities are not recorded and the engine behaves as it did without this
	// field. The engine resolves a stable software-agent id at New() so the
	// deterministic insert always references a present agent row.
	Tracker ActivitySink
	// SliceConcurrency is the per-executor concurrency limit K for the slice
	// queue. It bounds the number of slice and review sub-workflows that the
	// local executor runs concurrently, providing backpressure on the single
	// SQLite WAL writer bottleneck. <= 0 uses DefaultSliceQueueConcurrency.
	//
	// See DefaultSliceQueueConcurrency in internal/engine/queue.go for the
	// full trade-off rationale and tuning guidance.
	SliceConcurrency int
	// HooksMgr, when set, receives slice lifecycle events (SliceStarted,
	// SliceCompleted, SliceFailed) dispatched by slice sub-workflows. nil ⇒
	// hook dispatch is skipped (no observability events; the sub-workflow still
	// runs correctly).
	//
	// HooksMgr is wired by the daemon at startup. Callers that don't need
	// slice lifecycle observability (e.g. the local CLI, unit tests) may
	// leave this nil.
	HooksMgr *hooks.Manager
}

// ActivitySink is the narrow provenance surface the engine needs to record
// activities idempotently. protocol.TaskTracker satisfies it (via the embedded
// provenance.Tracker), as does provenance.Tracker directly.
type ActivitySink interface {
	// RegisterSoftwareAgent find-or-creates is the caller's concern; the engine
	// only registers its own stable agent once if absent.
	RegisterSoftwareAgent(namespace, name, version, source string) (provenance.SoftwareAgent, error)
	// StartActivityWithID records an activity under a caller-supplied id with
	// ON CONFLICT(id) DO NOTHING, so a replayed emission collapses to one row.
	StartActivityWithID(id provenance.ActivityID, agentID provenance.AgentID, phase provenance.Phase, stage provenance.Stage, notes string) (provenance.Activity, error)
}

// Engine owns the shared modernc handle, the DBOS context, and the forensic
// trail. It registers and drives the EpochStateMachine over durable steps.
//
// Lifecycle: New → Launch → (run workflows) → Shutdown.
type Engine struct {
	cfg              Config
	db               *sql.DB
	dbosCtx          dbos.DBOSContext
	trail            audit.Trail
	trailCloser      io.Closer
	specs            map[protocol.PhaseId]protocol.PhaseSpec
	activityAgentID  provenance.AgentID
	launched         bool
	sliceQueue       dbos.WorkflowQueue
	sliceConcurrency int // resolved value stored once in New; returned by SliceConcurrency()
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

	// When an activity sink is configured, resolve the engine's stable agent id
	// once (so every deterministic activity insert references a present agent
	// row) and compose activity recording into the OnTransition seam. The
	// activity write runs BEFORE any caller-supplied hook, so a consumer's own
	// hook (e.g. the recovery probe's stall) still runs afterward.
	if cfg.Tracker != nil {
		agentID, err := resolveEngineAgentID(db, cfg.Tracker)
		if err != nil {
			_ = db.Close()
			if trailCloser != nil {
				_ = trailCloser.Close()
			}
			return nil, err
		}
		e.activityAgentID = agentID

		userHook := e.cfg.OnTransition
		e.cfg.OnTransition = func(c context.Context, epochId string, rec *protocol.TransitionRecord, stepSeq string) error {
			if err := e.recordActivity(c, epochId, rec, stepSeq); err != nil {
				return err
			}
			if userHook != nil {
				return userHook(c, epochId, rec, stepSeq)
			}
			return nil
		}
	}

	// Register the durable workflows before Launch so the recovery sweep can
	// resume in-flight epochs. The method values are stable across builds, which
	// (with the pinned ApplicationVersion) is what makes recovery survive
	// rebuilds. EpochWorkflow drives a scripted plan; EpochControlWorkflow is the
	// signal-driven driver the lifecycle/signal CLI verbs start and send to.
	dbos.RegisterWorkflow(dbosCtx, e.EpochWorkflow)
	dbos.RegisterWorkflow(dbosCtx, e.EpochControlWorkflow)

	// Register sub-workflows for slice and review dispatch. These are queued via
	// the slice queue (registered below) so they execute with bounded concurrency.
	dbos.RegisterWorkflow(dbosCtx, e.SliceSubWorkflow)
	dbos.RegisterWorkflow(dbosCtx, e.ReviewSubWorkflow)

	// Create the slice queue BEFORE Launch (NewWorkflowQueue panics after Launch).
	// Resolve the concurrency limit K once here; store it on the Engine so
	// SliceConcurrency() can return the actual configured value without
	// re-deriving it from the config (two copies of the <=0 fallback logic
	// would drift independently).
	k := cfg.SliceConcurrency
	if k <= 0 {
		k = DefaultSliceQueueConcurrency
	}
	sliceQueue, err := newSliceQueue(dbosCtx, k)
	if err != nil {
		_ = db.Close()
		if trailCloser != nil {
			_ = trailCloser.Close()
		}
		return nil, err
	}
	e.sliceQueue = sliceQueue
	e.sliceConcurrency = k

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

// SliceQueue returns the DBOS WorkflowQueue used for slice and review
// sub-workflow dispatch. Tests may inspect the queue name to verify wiring.
func (e *Engine) SliceQueue() dbos.WorkflowQueue { return e.sliceQueue }

// SliceConcurrency returns the effective per-executor concurrency limit K that
// was used to configure the slice queue. This is the resolved value (after
// applying the DefaultSliceQueueConcurrency fallback) stored once in New —
// not re-derived from the config to avoid having two copies of the fallback
// logic that could drift.
func (e *Engine) SliceConcurrency() int {
	return e.sliceConcurrency
}
