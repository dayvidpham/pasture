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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	// The durable runtime resolves its SQLite backend through a driver
	// registry. Every binary that hands it a SQLite handle must link the
	// driver itself: without this import the construction below fails at run
	// time, and the runtime loses the error-code extractor that tells a busy
	// or locked database apart from a permanent failure. Do not rely on
	// another package's import to pull it in. The guard test in
	// internal/engine/dbosinit_test.go fails if this import is removed.
	_ "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// DefaultExecutorID is the pinned DBOS executor id. DBOS filters crash-recovery
// by ExecutorID + ApplicationVersion; pinning the executor id keeps recovery
// attributable to "pasture" across restarts rather than a per-process default.
const DefaultExecutorID = "pasture"

// DefaultAppName is the pinned DBOS application name.
const DefaultAppName = "pasture"

// constructionAbortShutdownTimeout bounds each wait when a durable context is
// stopped because engine construction failed after creating it. Nothing has
// been launched at that point, so there is no in-flight work to drain and the
// wait exists only to let the runtime's own goroutines exit.
//
// It is a PER-COMPONENT budget, not a total, exactly like the argument to
// Engine.Shutdown: the runtime spends it again on every part it stops. See
// SequentialShutdownWaits and WorstCaseShutdownDuration for the bound. An
// un-launched context has none of those parts running, so every wait returns
// at once in practice.
const constructionAbortShutdownTimeout = 2 * time.Second

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
	// SkipMigrations opens DBPath as a pre-migrated database when Trail is nil.
	// The audit layer still asserts the schema version. This is intended for
	// tests that copy a current golden database; production callers should leave
	// it false so the real migrator runs.
	SkipMigrations bool
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
	// QueueBasePollingInterval overrides the DBOS queue base polling interval.
	// Zero keeps the DBOS production default. Tests may set a shorter interval
	// to keep bounded-concurrency assertions fast without changing production
	// queue cadence.
	QueueBasePollingInterval time.Duration
	// HooksMgr, when set, receives slice lifecycle events (SliceStarted,
	// SliceCompleted, SliceFailed) dispatched by slice sub-workflows. nil ⇒
	// hook dispatch is skipped (no observability events; the sub-workflow still
	// runs correctly).
	//
	// pastured wires HooksMgr when it hosts the engine. Callers that don't need
	// slice lifecycle observability (e.g. the local CLI, unit tests) may leave
	// this nil.
	HooksMgr *hooks.Manager
	Timeouts timeouts.Profile
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
	cfg             Config
	logger          *slog.Logger
	db              *sql.DB
	dbosCtx         dbos.Context
	trail           audit.Trail
	trailCloser     io.Closer
	specs           map[protocol.PhaseId]protocol.PhaseSpec
	activityAgentID provenance.AgentID
	launched        bool
	controlQueue    dbos.Queue
	sliceQueue      dbos.Queue
	// sliceConcurrency is the per-executor concurrency limit currently in force
	// for the slice queue. New stores the resolved start-up value;
	// SetSliceConcurrency replaces it after the queue row is updated. It is
	// atomic because an operator can change it while workflows read it.
	sliceConcurrency atomic.Int64
}

// New constructs an Engine: opens the shared handle with the WAL/busy-timeout
// DSN, ensures the projection table, opens (or adopts) the forensic trail,
// creates the DBOS context with the shared handle as SQLiteSystemDB and the
// pinned ExecutorID + ApplicationVersion, and registers EpochWorkflow.
//
// The returned Engine is NOT yet launched; call Launch to run the recovery
// sweep and accept work. Always call Shutdown to release handles.
func New(ctx context.Context, cfg Config) (*Engine, error) {
	if cfg.Timeouts.IsZero() {
		cfg.Timeouts = timeouts.ProductionProfile()
	}
	if err := cfg.Timeouts.Validate(); err != nil {
		return nil, fmt.Errorf("engine timeout profile: %w", err)
	}
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
		var st *audit.SqliteAuditTrail
		var err error
		if cfg.SkipMigrations {
			st, err = audit.NewSqliteAuditTrailWithOptions(cfg.DBPath, audit.WithSkipMigrations())
		} else {
			st, err = audit.NewSqliteAuditTrail(cfg.DBPath)
		}
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

	db, err := dbconn.OpenSharedDBWithProfile(cfg.DBPath, cfg.Timeouts)
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

	// SCHEMA GATE — NOT WIRED YET. The refusal of a system database written by
	// the superseded durable runtime belongs HERE: call
	// provenance.RequireSupportedDBOSSystemSchema(ctx, db, cfg.DBPath) on this
	// exact handle, BEFORE the context below is constructed. Constructing the
	// context migrates such a database in place, so the gate is worthless after
	// this point. provenance.ErrSupersededDBOSSystemSchema is permanent: the
	// bounded retry below must not re-attempt it. Tracked in
	// https://github.com/dayvidpham/pasture/issues/104.
	//
	// Bounded retry: two processes opening the same fresh database can race
	// DBOS's non-atomic schema bootstrap, and the loser's error is repaired by
	// re-running it. See internal/engine/dbosinit.go for the full analysis.
	dbosCtx, err := newDurableContext(ctx, dbos.NewContext, dbos.Config{
		AppName:            appName,
		SQLiteSystemDB:     db,
		ExecutorID:         executorID,
		ApplicationVersion: cfg.ApplicationVersion,
		Logger:             logger,
	}, defaultDBOSRetryPolicy())
	if err != nil {
		_ = db.Close()
		if trailCloser != nil {
			_ = trailCloser.Close()
		}
		// newDurableContext already produces an actionable, more specific
		// error for the failures it recognises (a schema-bootstrap race that
		// never converged, or a cancelled wait); don't bury it under the
		// generic one.
		var structured *pasterrors.StructuredError
		if errors.As(err, &structured) {
			return nil, err
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

	// Every failure below owns a live durable context. Abandoning it would leak
	// its queue runner and notification goroutines for the life of the process,
	// and would leave them writing through the shared handle. Stop it first,
	// then release what the runtime does not own.
	//
	// Order matters. The allocator binding installed below points a caller's
	// tracker at this context; it must be removed before the context stops, or
	// the tracker keeps a runner on a dead context and refuses to accept a
	// replacement engine.
	//
	// The runtime owns the shared handle it was given and closes it during
	// shutdown, so db.Close here is a second close of an already-closed handle
	// — harmless, and still correct if that ownership ever changes.
	//
	// A shutdown that does not finish inside its budget is joined into the
	// returned error rather than dropped: it means goroutines from this
	// abandoned context may still be running.
	var unbindAllocator func()
	abort := func(cause error) (*Engine, error) {
		if unbindAllocator != nil {
			unbindAllocator()
		}
		shutdownErr := dbos.Shutdown(dbosCtx, constructionAbortShutdownTimeout)
		_ = db.Close()
		if trailCloser != nil {
			_ = trailCloser.Close()
		}
		if shutdownErr != nil {
			return nil, errors.Join(cause, fmt.Errorf(
				"engine.New: the durable context created for this engine did not stop while construction was being abandoned; "+
					"its goroutines may keep running until the process exits; "+
					"restart the process if epochs behave inconsistently: %w",
				newShutdownIncomplete(constructionAbortShutdownTimeout, shutdownErr),
			))
		}
		return nil, cause
	}

	e := &Engine{
		cfg:         cfg,
		logger:      logger,
		db:          db,
		dbosCtx:     dbosCtx,
		trail:       trail,
		trailCloser: trailCloser,
		specs:       specs,
	}

	if cfg.Tracker != nil && tasks.SupportsEngineGovernedAllocation(cfg.Tracker) {
		allocator, bindErr := provenance.NewHostBoundGovernedAllocator(ctx, dbosCtx, db, tasks.GovernedAllocationAuditParticipant)
		if bindErr != nil {
			return abort(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow, What: "Couldn't bind governed slice allocation to the durable engine.", Why: "The engine-owned DBOS root rejected the governed allocator before launch.", Where: "Constructing the engine (internal/engine/engine.go in engine.New).", Impact: "CreateSlice cannot run atomically on the engine's existing root and database handle.", Fix: "Construct one engine with a unified task tracker and ensure governed allocation is registered before Launch.", Cause: bindErr})
		}
		if bindErr := tasks.BindEngineGovernedAllocation(cfg.Tracker, allocator); bindErr != nil {
			return abort(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow, What: "Couldn't install the engine-owned slice allocator.", Why: "The configured tracker rejected the narrow composed-allocation capability before launch.", Where: "Constructing the engine (internal/engine/engine.go in engine.New).", Impact: "CreateSlice would otherwise have no safe path to the engine-owned transaction.", Fix: "Pass the unified tracker returned by tasks.OpenTaskTracker as engine.Config.Tracker and do not reuse it across engines.", Cause: bindErr})
		}
		// The tracker accepts exactly one engine-owned runner. Give every later
		// failure path a way to hand that slot back, so the caller can retry
		// construction with the same tracker.
		unbindAllocator = func() { tasks.UnbindEngineGovernedAllocation(cfg.Tracker, allocator) }
	}

	// When an activity sink is configured, resolve the engine's stable agent id
	// once (so every deterministic activity insert references a present agent
	// row) and compose activity recording into the OnTransition seam. The
	// activity write runs BEFORE any caller-supplied hook, so a consumer's own
	// hook (e.g. the recovery probe's stall) still runs afterward.
	if cfg.Tracker != nil {
		agentID, err := resolveEngineAgentID(db, cfg.Tracker)
		if err != nil {
			return abort(err)
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
	dbos.RegisterWorkflow(dbosCtx, e.EpochControlWorkflow, dbos.WithWorkflowName(EpochControlWorkflowName))

	// Register sub-workflows for slice and review dispatch. These are queued via
	// the slice queue (registered below) so they execute with bounded concurrency.
	dbos.RegisterWorkflow(dbosCtx, e.SliceSubWorkflow)
	dbos.RegisterWorkflow(dbosCtx, e.ReviewSubWorkflow)

	// Register the queues during construction, so both exist before the first
	// enqueue. Registration writes a row to the system database and can fail;
	// a failed registration leaves the engine with no queue to dispatch on, so
	// it aborts construction rather than being ignored.
	//
	// The control queue is where CLI clients submit epoch-control workflows for
	// the hosted pastured process to execute.
	controlQueue, err := newControlQueue(dbosCtx, cfg.QueueBasePollingInterval)
	if err != nil {
		return abort(queueRegistrationError(ControlQueueName, err))
	}
	e.controlQueue = controlQueue

	// Resolve the concurrency limit K once here; store it on the Engine so
	// SliceConcurrency() can return the actual configured value without
	// re-deriving it from the config (two copies of the <=0 fallback logic
	// would drift independently).
	k := cfg.SliceConcurrency
	if k <= 0 {
		k = DefaultSliceQueueConcurrency
	}
	sliceQueue, err := newSliceQueue(dbosCtx, k, cfg.QueueBasePollingInterval)
	if err != nil {
		return abort(queueRegistrationError(SliceQueueName, err))
	}
	e.sliceQueue = sliceQueue
	e.sliceConcurrency.Store(int64(k))

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

// ShutdownComponent names one part of the durable runtime that a shutdown
// waits for. The runtime stops its parts one after another and reports the
// ones that were still running when their wait expired, so an operator can
// tell "work was still in flight" apart from "a background loop would not
// stop".
//
// The values are the names the runtime itself reports. They are typed so a
// caller matches on a constant instead of re-spelling a string. A runtime
// build that adds a part reports a name this list does not carry: such a name
// is passed through unchanged rather than dropped, and Meaning reports it as
// unrecognised.
type ShutdownComponent string

// The parts a shutdown waits for, in the order the runtime waits for them.
// Two of them can never appear for this engine, and say so in Meaning: the
// engine configures neither an administrative listener nor a remote-control
// connection.
const (
	ShutdownComponentScheduleReconciler   ShutdownComponent = "schedule reconciler"
	ShutdownComponentQueueRunner          ShutdownComponent = "queue runner"
	ShutdownComponentWorkflowScheduler    ShutdownComponent = "workflow scheduler"
	ShutdownComponentAdminServer          ShutdownComponent = "admin server"
	ShutdownComponentWorkflows            ShutdownComponent = "workflows"
	ShutdownComponentConductor            ShutdownComponent = "conductor"
	ShutdownComponentNotificationListener ShutdownComponent = "system database notification listener"
	ShutdownComponentNotifier             ShutdownComponent = "system database notifier"
	ShutdownComponentConnectionPool       ShutdownComponent = "system database connection pool"
)

// shutdownComponentMeaning translates each part into one plain sentence an
// operator can act on. It is deliberately a lookup rather than a method body
// full of cases so the set stays visibly aligned with the constants above.
var shutdownComponentMeaning = map[ShutdownComponent]string{
	ShutdownComponentScheduleReconciler:   "the loop that keeps scheduled work in step with the database",
	ShutdownComponentQueueRunner:          "the loop that hands queued slice and review work to workers",
	ShutdownComponentWorkflowScheduler:    "the timer that starts work on a schedule",
	ShutdownComponentAdminServer:          "an administrative listener, which this engine never starts",
	ShutdownComponentWorkflows:            "epoch, slice or review work that was still running",
	ShutdownComponentConductor:            "a remote-control connection, which this engine never starts",
	ShutdownComponentNotificationListener: "the loop that delivers signals between running work",
	ShutdownComponentNotifier:             "the loop that flushes the last signals before the database closes",
	ShutdownComponentConnectionPool:       "the shared database connections",
}

// Meaning returns one plain sentence describing the part, for an operator who
// does not read this code. An unrecognised name (a newer runtime build) is
// reported as such instead of being hidden.
func (c ShutdownComponent) Meaning() string {
	if m, ok := shutdownComponentMeaning[c]; ok {
		return m
	}
	return "a part of the durable runtime this build does not recognise"
}

// SequentialShutdownWaits is how many waits one shutdown of THIS engine can
// spend the per-component budget on, one after another: the schedule
// reconciler, the queue runner, the work scheduler, in-flight work, and the
// database's notification listener, notifier and connection pool. The engine
// configures neither an administrative listener nor a remote-control
// connection, which would add two more.
//
// Measured on the pinned runtime: a shutdown held up by work that is still
// running spends the budget ONCE (only the in-flight-work wait expires; every
// other wait returns at once). Seven is therefore the worst case an operator
// must budget for, not the normal cost.
const SequentialShutdownWaits = 7

// WorstCaseShutdownDuration reports how long a Shutdown can take in the worst
// case for a given per-component budget. Callers that must fit inside an
// external stop deadline (a service manager, a container runtime) size the
// budget with this rather than with the raw value.
func WorstCaseShutdownDuration(perComponentTimeout time.Duration) time.Duration {
	return perComponentTimeout * SequentialShutdownWaits
}

// shutdownPendingMarker is the only place the runtime names the parts that
// were still running: it returns a plain message of the shape
//
//	shutdown timed out after 1s waiting for: workflows, queue runner
//
// and carries no structured field to read them from. The names are therefore
// recovered from the text after this marker. A runtime that changes the shape
// costs the caller the component list, not the error: Pending is left empty,
// the cause is still reported verbatim, and the timeout-path test in
// internal/engine/engine_test.go fails so the change is noticed.
const shutdownPendingMarker = " waiting for: "

// ShutdownIncompleteError reports a durable shutdown that ran out of time.
//
// It is the typed detail behind the error Engine.Shutdown returns; reach it
// with errors.As when the caller must act on WHICH parts were still running
// (an operator report, a metric) rather than only on the fact of the failure.
type ShutdownIncompleteError struct {
	// PerComponentTimeout is the budget each wait was given — not the total
	// the shutdown was allowed to take. See Engine.Shutdown.
	PerComponentTimeout time.Duration
	// Pending lists the parts still running when their wait expired, in the
	// order the runtime waited for them. Empty when the runtime reported a
	// failure in a shape this build could not read; Cause is then the only
	// account of it.
	Pending []ShutdownComponent
	// Cause is the runtime's own error, kept verbatim for diagnosis.
	Cause error
}

// Error reports which parts were still running and what each of them is.
func (e *ShutdownIncompleteError) Error() string {
	if len(e.Pending) == 0 {
		return fmt.Sprintf(
			"the durable runtime did not stop within %v per part, and did not name which parts were still running: %v",
			e.PerComponentTimeout, e.Cause,
		)
	}
	parts := make([]string, 0, len(e.Pending))
	for _, c := range e.Pending {
		parts = append(parts, fmt.Sprintf("%s (%s)", c, c.Meaning()))
	}
	return fmt.Sprintf(
		"the durable runtime did not stop within %v per part; still running: %s",
		e.PerComponentTimeout, strings.Join(parts, ", "),
	)
}

// Unwrap exposes the runtime's own error so errors.Is and errors.As reach it.
func (e *ShutdownIncompleteError) Unwrap() error { return e.Cause }

// parsePendingShutdownComponents recovers the component names from the
// runtime's message. It returns nil when the message does not carry the
// expected marker; see shutdownPendingMarker.
//
// The names are separated on the comma alone, and each one is trimmed. The
// runtime writes ", " today, but a build that writes "," would otherwise turn
// the whole list into one invented component name.
func parsePendingShutdownComponents(err error) []ShutdownComponent {
	if err == nil {
		return nil
	}
	msg := err.Error()
	idx := strings.LastIndex(msg, shutdownPendingMarker)
	if idx < 0 {
		return nil
	}
	list := strings.TrimSpace(msg[idx+len(shutdownPendingMarker):])
	if list == "" {
		return nil
	}
	var pending []ShutdownComponent
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		pending = append(pending, ShutdownComponent(name))
	}
	return pending
}

// newShutdownIncomplete builds the typed detail from the runtime's error.
func newShutdownIncomplete(perComponentTimeout time.Duration, cause error) *ShutdownIncompleteError {
	return &ShutdownIncompleteError{
		PerComponentTimeout: perComponentTimeout,
		Pending:             parsePendingShutdownComponents(cause),
		Cause:               cause,
	}
}

// shutdownIncompleteError wraps the typed detail in the actionable surface the
// command line reports and maps to an exit code. Both layers stay reachable:
// errors.As finds the StructuredError for the exit code and the operator
// block, and the *ShutdownIncompleteError underneath it for the part list.
func shutdownIncompleteError(detail *ShutdownIncompleteError) *pasterrors.StructuredError {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     "The durable engine didn't finish stopping in the time it was given.",
		Why: "Parts of the engine were still busy when their stop budget of " +
			detail.PerComponentTimeout.String() + " ran out.",
		Where:  "Stopping the durable engine (internal/engine/engine.go in engine.Engine.Shutdown).",
		Impact: "Work that was still running was cut off, and the engine had already closed the shared database file, so a late write from that work fails. The work itself is not lost: it stays pending rather than cancelled, so the next start of the daemon finishes it.",
		Fix: "1. Read the log lines just before this one: they name the epoch and the work that was still running.\n" +
			"2. Start the daemon again. It resumes the work that was left pending:\n" +
			"     pastured\n" +
			"3. Confirm the epoch is moving again:\n" +
			"     pasture status --epoch-id <epoch-id>",
		Cause: detail,
	}
}

// Shutdown stops the durable runtime, then releases the handles the runtime
// does not own. Call it once.
//
// perComponentTimeout is the budget for EACH wait inside the runtime, NOT a
// total for the whole shutdown. The runtime stops its parts one after another
// and gives every one of them the full value, so the worst case is
// SequentialShutdownWaits times this argument; use WorstCaseShutdownDuration
// to size it against an external stop deadline. Measured on the pinned
// runtime, a shutdown held up by work that is still running spends the budget
// once, because every other wait returns at once.
//
// It returns nil when the runtime stopped inside its budget. Otherwise it
// returns an actionable error carrying a *ShutdownIncompleteError with the
// parts that were still running; the handles are released either way. The
// error is ALSO logged, because many callers (tests, probes, deferred cleanup)
// discard the return value and the failure must never go unreported.
//
// The durable runtime owns the shared SQLite handle it was constructed with:
// its shutdown closes that handle unconditionally, on the timeout path too
// (dbos/internal/sysdb/dbq.go, sqlPoolAdapter.Close). The engine therefore
// cannot hold the handle open for a worker that outlives the timeout, and the
// close below is a harmless second close of an already-closed handle.
// internal/handlers/controller.go records the same ownership for the client;
// the two must stay in agreement.
//
// The trail is closed either way: it is a separate handle that the runtime
// never writes through.
func (e *Engine) Shutdown(perComponentTimeout time.Duration) error {
	var incomplete error
	if e.dbosCtx != nil {
		if err := dbos.Shutdown(e.dbosCtx, perComponentTimeout); err != nil {
			incomplete = shutdownIncompleteError(newShutdownIncomplete(perComponentTimeout, err))
			e.logShutdownFailure(incomplete)
		}
	}
	if e.db != nil {
		_ = e.db.Close()
	}
	if e.trailCloser != nil {
		_ = e.trailCloser.Close()
	}
	return incomplete
}

// DBOS returns the underlying DBOS context so callers (and later slices) can
// RunWorkflow / Send / ListWorkflows against the engine's registered workflow.
func (e *Engine) DBOS() dbos.Context { return e.dbosCtx }

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

// SliceQueue returns the DBOS queue used for slice and review
// sub-workflow dispatch. Tests may inspect the queue name to verify wiring.
func (e *Engine) SliceQueue() dbos.Queue { return e.sliceQueue }

// ControlQueue returns the DBOS queue used for epoch control workflows.
func (e *Engine) ControlQueue() dbos.Queue { return e.controlQueue }

// SliceConcurrency returns the per-executor concurrency limit K currently in
// force for the slice queue. New stores the resolved start-up value (after
// applying the DefaultSliceQueueConcurrency fallback) rather than re-deriving
// it from the config, so the fallback logic exists in one place only.
// SetSliceConcurrency replaces the value after it has updated the stored queue
// configuration, so this getter always reports the last value this process
// wrote, not necessarily the value a peer process wrote afterwards. Read the
// stored configuration back with SliceQueueWorkerConcurrency for that.
func (e *Engine) SliceConcurrency() int {
	return int(e.sliceConcurrency.Load())
}

// queueRegistrationError wraps a failed queue registration. A queue
// configuration is a row in the system database, so registration can fail for
// the same reasons any write can, and the engine cannot dispatch without it.
func queueRegistrationError(queueName string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("Couldn't register the %q work queue in the pasture database.", queueName),
		Why: "The durable engine stores each queue's configuration as a row in the database and " +
			"rejected the write — usually because the database is unwritable, is held by another " +
			"process, or the configured concurrency limit is invalid.",
		Where:  "Constructing the engine (internal/engine/engine.go in engine.New).",
		Impact: "The engine did not start, so no epoch, slice, or review work can be dispatched from it.",
		Fix: "1. Confirm the database file is present and writable:\n" +
			"     ls -l ~/.local/share/pasture/pasture.db\n" +
			"2. Confirm no other pasture process is holding it:\n" +
			"     pgrep -fa 'pasture|pastured'\n" +
			"3. Confirm the slice concurrency limit is a positive integer\n" +
			"   (--slice-concurrency or $PASTURE_SLICE_CONCURRENCY), then start again.",
		Cause: cause,
	}
}

// logShutdownFailure reports an incomplete durable shutdown on the engine's
// own logger. Shutdown returns the same error to its caller; it is logged as
// well because callers that stop an engine from deferred cleanup discard the
// return value, and an incomplete shutdown must never be silent.
func (e *Engine) logShutdownFailure(err error) {
	logger := e.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error(
		"the durable engine did not stop within its shutdown budget; "+
			"some work may still have been running, and the durable runtime has already closed the shared database handle it owns, "+
			"so a late write through that handle will fail; the work stays pending rather than cancelled, so start the daemon again to finish it",
		"error", err,
	)
}
