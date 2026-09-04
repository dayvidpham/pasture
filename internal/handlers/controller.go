package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	// The durable runtime resolves its SQLite backend through a driver
	// registry. Every binary that hands it a SQLite handle must link the
	// driver itself: without this import the client construction below fails
	// at run time, and the runtime loses the error-code extractor that tells a
	// busy or locked database apart from a permanent failure. Do not rely on
	// another package's import to pull it in. The guard test in
	// internal/handlers/controller_test.go fails if this import is removed.
	_ "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// EpochController is the substrate-neutral control surface the epoch lifecycle
// and signal handlers depend on. Keeping handlers behind this narrow interface
// (instead of the durable engine directly) keeps them unit-testable with a fake
// and isolates them from the substrate. The production implementation is
// DBOS-backed; lifecycle verbs map to RunWorkflow/CancelWorkflow and signals map
// to Send by topic.
type EpochController interface {
	// StartEpoch launches the durable control workflow for epochId (its
	// workflow id is the epoch id, so signals address it by that id).
	StartEpoch(ctx context.Context, epochId string) error
	// CancelEpoch requests cancellation of the running epoch workflow.
	CancelEpoch(ctx context.Context, epochId string) error
	// TerminateEpoch records an EpochCancelled audit event carrying the
	// operator's reason, then requests cancellation of the running epoch
	// workflow. reason may be empty (event is still recorded, with an empty
	// reason payload). Record-before-cancel order is intentional: cancel often
	// targets a wedged workflow where a subsequent signal would not fire.
	TerminateEpoch(ctx context.Context, epochId, reason string) error
	// AdvancePhase delivers an advance-phase signal.
	AdvancePhase(ctx context.Context, epochId string, sig protocol.PhaseAdvanceSignal) error
	// SubmitVote delivers a review-vote signal.
	SubmitVote(ctx context.Context, epochId string, sig protocol.ReviewVoteSignal) error
	// ReportSliceProgress delivers a slice-progress signal.
	ReportSliceProgress(ctx context.Context, epochId string, sig protocol.SliceProgressSignal) error
	// RegisterSession delivers a register-session signal.
	RegisterSession(ctx context.Context, epochId string, sig protocol.RegisterSessionSignal) error
	// StartSlice delivers a start-slice configuration signal to a slice
	// sub-workflow (addressed by its slice workflow id).
	StartSlice(ctx context.Context, sliceId string, sig protocol.SliceStartSignal) error
	// CompleteSlice delivers a complete-slice override signal to a slice
	// sub-workflow (addressed by its slice workflow id).
	CompleteSlice(ctx context.Context, sliceId string, sig protocol.SliceCompleteSignal) error
	// Close releases the controller's resources.
	Close() error
}

// dbosController is the DBOS-backed EpochController. It owns a lightweight
// database-backed client and maps each operation onto durable DBOS records.
type dbosController struct {
	client      dbos.Client
	db          *sql.DB
	trail       audit.Trail
	trailCloser interface{ Close() error }

	// closeOnce and closeErr make Close one-shot: the real shutdown runs on the
	// first call and every later call replays its result. See Close.
	closeOnce sync.Once
	closeErr  error
}

// clientSite names the operation that opened a durable client.
//
// ONE typed value per caller, and every caller-specific report is derived from
// it: the Where line when the client refuses to START, and the whole report when
// it refuses to STOP. A second per-caller parameter would let the two disagree —
// a caller could describe itself as the controller in one and as the queue
// command in the other — so there is deliberately only this.
//
// The reports must differ because the same runtime failure means different
// things to the two callers. After an epoch lifecycle command, work in flight
// may never have been recorded and the epoch state has to be re-read. After a
// work-queue command, the answer was already read back from the database before
// the stop began, so it stands, and an operator who never gave an epoch id
// cannot act on a fix that asks for one.
type clientSite int

const (
	// clientSiteUnset is the zero value, and it names NO caller. It exists so
	// that forgetting to pass a site is a loud failure rather than a silent
	// promotion to whichever caller happened to be written first: every
	// consumer below rejects it, and a third site added later is rejected the
	// same way until it is handled deliberately.
	clientSiteUnset clientSite = iota
	// clientSiteEpochController is the epoch controller: OpenEpochController
	// opening the client, and dbosController.Close releasing it.
	clientSiteEpochController
	// clientSiteQueueCommand is a work-queue command opening a client to read or
	// change a queue setting, and releasing it afterwards.
	clientSiteQueueCommand
)

// String names the site for a message about a site that is not handled.
func (s clientSite) String() string {
	switch s {
	case clientSiteEpochController:
		return "epoch controller"
	case clientSiteQueueCommand:
		return "work-queue command"
	case clientSiteUnset:
		return "unset"
	default:
		return fmt.Sprintf("unknown site %d", int(s))
	}
}

// startupWhere is the Where line for a client that refused to start at this
// site. It answers the operator's first question — which command was this? — so
// it names the operation, not only the file.
//
// An unhandled site is an error, never a default: a Where line that names the
// wrong command sends the operator to the wrong place, and it would look exactly
// as authoritative as a right one.
func (s clientSite) startupWhere() (string, error) {
	switch s {
	case clientSiteEpochController:
		return "Opening the epoch controller (internal/handlers/controller.go in OpenEpochController).", nil
	case clientSiteQueueCommand:
		return "Opening a durable client for a work-queue command (internal/handlers/queue.go in handlers.withQueueClient).", nil
	default:
		return "", unhandledClientSiteError(s, "describing where a durable client refused to start")
	}
}

// unhandledClientSiteError reports a caller that this code cannot describe.
//
// It is a defect in pasture, not something the operator did, and the message
// says so: they cannot fix it, and the failure they were actually chasing is
// carried alongside it so it is not lost.
func unhandledClientSiteError(site clientSite, doing string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("pasture couldn't describe which command was using the database (%s).", site),
		Why: "Every command that opens the database names itself, so its failures can be explained in its own " +
			"terms. This one did not, which means a command was added without naming itself.",
		Where:  fmt.Sprintf("Reporting a durable-client failure (internal/handlers/controller.go, %s).", doing),
		Impact: "The failure below is real and is reported unchanged; only pasture's explanation of which command hit it is missing.",
		Fix: "This is a defect in pasture, not in the database or the machine — an operator can't work around it.\n" +
			"Report it, with the command you ran and the version of pasture:\n" +
			"     pasture --version",
	}
}

// OpenEpochController opens a DBOS-backed controller on the unified database.
// Empty dbPath resolves to tasks.DefaultDBPath(). The returned controller does
// not construct or launch an engine: lifecycle verbs enqueue durable DBOS
// records and signals against the shared SQLite file, while pastured hosts and
// executes the registered workflows.
func OpenEpochController(dbPath string) (EpochController, error) {
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}

	// The client comes FIRST, and nothing may write to the file before it.
	//
	// This ordering is load-bearing, not a preference. Opening the forensic
	// trail migrates the file: it creates the trail's own tables and raises the
	// recorded trail schema version. Doing that to a database the schema gate
	// inside openClient then refuses would leave the operator with a file that
	// NEITHER build can read — this build refuses its durable layout, and an
	// older build refuses the trail schema this one just wrote (see
	// internal/audit/migrate.go, which rejects a database recorded at a newer
	// version than the build knows). The refusal promises that no pasture data
	// was written and that the older build can still read the history, so that
	// promise must be true.
	client, db, release, err := openClient(dbPath, clientSiteEpochController)
	if err != nil {
		return nil, err
	}
	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		// The client owns the handle and closes it, so releasing the client is
		// the whole cleanup.
		if releaseErr := release(); releaseErr != nil {
			return nil, errors.Join(err, releaseErr)
		}
		return nil, err
	}
	return &dbosController{client: client, db: db, trail: trail, trailCloser: trail}, nil
}

// unnamedClientSiteWhere is the Where line for a durable client whose caller
// could not be named. It names the function instead of the command, which is
// all that is left to say, and it is only ever used beside the naming defect
// itself.
const unnamedClientSiteWhere = "Opening a durable client (internal/handlers/controller.go in openClient)."

// gateDurableSchema refuses a database whose durable layout an older build
// wrote, described for the caller named by site, and waits out one that
// another process is writing at this moment.
//
// The handle was opened by dbconn.OpenSharedDB, which gives it the production
// timeout profile; the gate's wait runs on that same profile, so the lock wait
// the handle already made and the windows the gate uses come from one place.
//
// An unnamed caller does NOT switch the gate off. Refusing such a database does
// not depend on knowing which command asked for it, and running it anyway is
// the safe half of the failure: the gate runs with the function as its location
// and the naming defect is joined to the refusal, so neither is lost. The
// refusal leads: see the note at the join.
func gateDurableSchema(db *sql.DB, dbPath string, site clientSite) error {
	profile := timeouts.ProductionProfile()
	where, siteErr := site.startupWhere()
	if siteErr != nil {
		if err := engine.RequireSupportedDurableSchema(
			context.Background(), unnamedClientSiteWhere, db, dbPath, profile); err != nil {
			// The REFUSAL leads and the naming defect follows. Both are true,
			// but only one of them is the operator's to act on, and the joined
			// error is read from the top: leading with pasture's own naming
			// defect would bury the file they must replace. The order also
			// decides the exit code, because it is read from the first
			// structured error in the chain, so this way a refused database
			// still exits as a storage failure rather than as a validation one.
			return errors.Join(err, siteErr)
		}
		return nil
	}
	return engine.RequireSupportedDurableSchema(context.Background(), where, db, dbPath, profile)
}

// durableClientLogger is the logger every durable client opened here is given.
//
// STANDARD ERROR, at warning level, and both halves are deliberate.
//
// The writer: a command's standard output is its ANSWER, and with --format json
// it is one machine-readable document. The durable runtime logs through the
// logger it is configured with, and when it is given none it builds its own on
// standard OUTPUT at info level (see dbos/dbos.go in the pinned version), so its
// start-up notes land inside that document and a pipe into a JSON reader fails.
// Standard error keeps the runtime's account of itself apart from the command's
// answer, which is what those two streams are for.
//
// The level: a runtime warning or error is something the operator should see,
// so it is not silenced. Its routine start-up notes are not news to someone
// running a single command, so they are not shown. A daemon, which is a
// long-running process where those notes ARE the record of what happened, keeps
// its own logger and is unaffected by this one.
func durableClientLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// openClient opens a database-backed durable client on the unified database and
// returns it, the handle it was built on, and the function that releases it.
// Empty dbPath resolves to tasks.DefaultDBPath().
//
// Every command that talks to the durable runtime WITHOUT hosting an engine
// comes through here — the epoch controller and the work-queue commands — so
// the process identity below is stated in one place and cannot drift between
// them. A drift would be silent and expensive: see the ownership note on
// AppName.
//
// The release function shuts the client down within controllerShutdownTimeout
// and reports an incomplete shutdown rather than dropping it, described for the
// caller named by site. The client owns the handle it was given and closes it,
// so releasing the client is the whole release; closing the handle separately
// would close the same database twice.
func openClient(dbPath string, site clientSite) (dbos.Client, *sql.DB, func() error, error) {
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	db, err := dbconn.OpenSharedDB(dbPath)
	if err != nil {
		return nil, nil, nil, err
	}
	// Schema gate. A database whose durable layout an older build wrote is
	// refused HERE, on this exact handle, before the client below exists: the
	// client builds a durable context of its own and migrates such a file in
	// place, so no refusal is possible afterwards. Nothing in this function
	// writes to the file before this point, so a refused database keeps the
	// records it arrived with. The gate is the same one the engine runs; see
	// internal/engine/dbosinit.go.
	if err := gateDurableSchema(db, dbPath, site); err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	client, err := dbos.NewClient(context.Background(), dbos.ClientConfig{
		SQLiteSystemDB: db,
		// Logger keeps the runtime's own log off this command's answer; see
		// durableClientLogger.
		Logger: durableClientLogger(),
		// AppName is the durable runtime's process identity, and it is
		// load-bearing in three separate places:
		//
		//  1. Ownership. The runtime stamps this name into the application_name
		//     column of every row it writes, including workflow_status, queues,
		//     workflow_schedules, operation_outputs and application_versions.
		//     Left empty, a client is "nameless": it writes NULL there and reads
		//     every application's rows. Named, it writes "pasture" and its reads are scoped to
		//     rows owned by "pasture" plus still-unclaimed (NULL) ones.
		//  2. Queue claiming and recovery. Queue dequeue and the recovery sweep
		//     both filter on application_name. A controller and a daemon that
		//     pin the SAME AppName therefore share one queue and one recovery
		//     scope: work this controller enqueues is claimed and, after a
		//     crash, recovered by the daemon. Two different names would split
		//     that scope in two and the enqueued work would never be picked up.
		//  3. Derived application version. When ApplicationVersion is left
		//     empty the runtime derives it from the binary hash AND the app
		//     name, so same-binary peers under different names land in
		//     different recovery cohorts. This controller does not rely on that
		//     derivation: every enqueue below pins
		//     engine.DefaultApplicationVersion explicitly.
		//
		// engine.DefaultAppName is the one value every pasture process pins —
		// the daemon passes it through engine.Config, and engine.New falls back
		// to it — so the controller must use it too, not the runtime's own
		// unnamed-client default.
		AppName: engine.DefaultAppName,
	})
	if err != nil {
		_ = db.Close()
		// The client refuses to start for causes the runtime reports as bare
		// text. engine.DescribeDurableStartupFailure replaces the ones pasture
		// can name with actionable guidance, and returns every other one
		// unchanged.
		//
		// It sits HERE, not at a caller, so that every command which opens a
		// client is told the same thing about the same failure. The trail is
		// closed by the caller that opened it.
		where, siteErr := site.startupWhere()
		if siteErr != nil {
			// The caller could not be named. The runtime's own failure is still
			// the thing that stopped the command, so it is joined rather than
			// replaced.
			return nil, nil, nil, errors.Join(siteErr, err)
		}
		return nil, nil, nil, engine.DescribeDurableStartupFailure(where, err)
	}
	return client, db, func() error { return releaseClient(client, site) }, nil
}

// releaseClient shuts a durable client down within controllerShutdownTimeout and
// reports an incomplete shutdown rather than dropping it. It is the one
// definition of what releasing a client means, used by the epoch controller's
// Close and by the release function openClient hands to the work-queue commands.
//
// site decides how the failure is described, because the caller is the only one
// who knows what the operator was doing.
func releaseClient(client dbos.Client, site clientSite) error {
	if client == nil {
		return nil
	}
	if err := dbos.Shutdown(client, controllerShutdownTimeout); err != nil {
		return incompleteShutdownError(site, err)
	}
	return nil
}

func (c *dbosController) StartEpoch(ctx context.Context, epochId string) error {
	_, err := dbos.Enqueue[protocol.EpochState, engine.ControlInput](c.client,
		engine.ControlQueueName,
		engine.EpochControlWorkflowName,
		engine.ControlInput{EpochId: epochId},
		dbos.WithEnqueueWorkflowID(epochId),
		dbos.WithEnqueueApplicationVersion(engine.DefaultApplicationVersion),
	)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("The epoch %q couldn't be started.", epochId),
			Why:      "The durable engine rejected the start request — likely a storage or engine initialisation failure.",
			Where:    "Starting the epoch (internal/handlers/controller.go in dbosController.StartEpoch).",
			Impact:   "The epoch did not start, so no phase transitions will run for it.",
			Fix: "1. Confirm the database is readable and writable:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"2. Retry once you've confirmed the database is healthy.\n" +
				"   Note: starting an epoch with an id that is already running is an idempotent no-op.",
			Cause: err,
		}
	}
	return nil
}

func (c *dbosController) CancelEpoch(ctx context.Context, epochId string) error {
	if err := dbos.CancelWorkflow(c.client, epochId); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't stop the epoch %q.", epochId),
			Why:      "The durable engine rejected the cancellation (the epoch may not be running).",
			Where:    "Cancelling the epoch (internal/handlers/controller.go in dbosController.CancelEpoch).",
			Impact:   "The epoch is unchanged; the cancellation did not take effect.",
			Fix: "1. Confirm the epoch is running:\n" +
				"     pasture query state --epoch-id " + epochId + "\n" +
				"2. Retry once you've confirmed the epoch id.",
			Cause: err,
		}
	}
	return nil
}

// TerminateEpoch records an EpochCancelled audit event carrying the operator's
// reason (empty string is allowed — the payload will contain key "reason" with
// an empty value), then requests cancellation of the running epoch workflow.
//
// The event is written via the non-dedup RecordEvent path (NULL dedup_key)
// because a CLI terminate is a one-shot action, not a replayed durable step.
// Record-before-cancel order is deliberate: cancel often targets a wedged
// workflow where a subsequent signal would not fire; the audit record must
// survive even when cancellation itself fails.
//
// The event is attributed to the engine automaton agent (find-or-created by
// the legacy-role bridge inside the audit trail). If recording fails, the
// method returns the record error without attempting cancellation.
func (c *dbosController) TerminateEpoch(ctx context.Context, epochId, reason string) error {
	ev := protocol.AuditEvent{
		EpochId:   epochId,
		Role:      engine.EngineAgentName,
		EventType: protocol.EventEpochCancelled,
		Payload:   map[string]any{"reason": reason},
		Timestamp: time.Now().UTC(),
	}
	if err := c.trail.RecordEvent(ctx, ev); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't record the cancellation event for epoch %q before stopping it.", epochId),
			Why:      "The audit trail rejected the write (storage error or database not accessible).",
			Where:    "Terminating the epoch (internal/handlers/controller.go in dbosController.TerminateEpoch).",
			Impact:   "The epoch was not cancelled. No audit record was written.",
			Fix: "1. Confirm the database is readable and writable:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"2. Retry once the database is healthy.",
			Cause: err,
		}
	}
	if err := dbos.CancelWorkflow(c.client, epochId); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't stop the epoch %q (the cancellation event was recorded).", epochId),
			Why:      "The durable engine rejected the cancellation (the epoch may not be running).",
			Where:    "Terminating the epoch (internal/handlers/controller.go in dbosController.TerminateEpoch).",
			Impact:   "The epoch is unchanged; the cancellation did not take effect. The audit record was written.",
			Fix: "1. Confirm the epoch is running:\n" +
				"     pasture query state --epoch-id " + epochId + "\n" +
				"2. Retry once you've confirmed the epoch id.",
			Cause: err,
		}
	}
	return nil
}

// sendSignal is the shared send path; topic names come from the typed constants.
func (c *dbosController) sendSignal(epochId string, topic protocol.SignalTopic, send func() error) error {
	if err := send(); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't deliver the %q signal to epoch %q.", topic, epochId),
			Why:      "The durable engine rejected the signal (the epoch may not be running).",
			Where:    "Delivering an epoch signal (internal/handlers/controller.go in dbosController.sendSignal).",
			Impact:   "The signal was not delivered, so the epoch's state is unchanged.",
			Fix: "1. Confirm the epoch is running:\n" +
				"     pasture query state --epoch-id " + epochId + "\n" +
				"2. Retry once you've confirmed the epoch id.",
			Cause: err,
		}
	}
	return nil
}

func (c *dbosController) AdvancePhase(ctx context.Context, epochId string, sig protocol.PhaseAdvanceSignal) error {
	return c.sendSignal(epochId, protocol.SignalAdvancePhase, func() error {
		return dbos.Send(c.client, epochId, sig, protocol.SignalAdvancePhase.String())
	})
}

func (c *dbosController) SubmitVote(ctx context.Context, epochId string, sig protocol.ReviewVoteSignal) error {
	return c.sendSignal(epochId, protocol.SignalSubmitVote, func() error {
		return dbos.Send(c.client, epochId, sig, protocol.SignalSubmitVote.String())
	})
}

func (c *dbosController) ReportSliceProgress(ctx context.Context, epochId string, sig protocol.SliceProgressSignal) error {
	return c.sendSignal(epochId, protocol.SignalSliceProgress, func() error {
		return dbos.Send(c.client, epochId, sig, protocol.SignalSliceProgress.String())
	})
}

func (c *dbosController) RegisterSession(ctx context.Context, epochId string, sig protocol.RegisterSessionSignal) error {
	return c.sendSignal(epochId, protocol.SignalRegisterSession, func() error {
		return dbos.Send(c.client, epochId, sig, protocol.SignalRegisterSession.String())
	})
}

func (c *dbosController) StartSlice(ctx context.Context, sliceId string, sig protocol.SliceStartSignal) error {
	return c.sendSignal(sliceId, protocol.SignalStartSlice, func() error {
		return dbos.Send(c.client, sliceId, sig, protocol.SignalStartSlice.String())
	})
}

func (c *dbosController) CompleteSlice(ctx context.Context, sliceId string, sig protocol.SliceCompleteSignal) error {
	return c.sendSignal(sliceId, protocol.SignalCompleteSlice, func() error {
		return dbos.Send(c.client, sliceId, sig, protocol.SignalCompleteSlice.String())
	})
}

// controllerShutdownTimeout is the budget Close gives the durable client to
// stop each of its own components. It is a PER-COMPONENT budget inside the
// runtime, not a total for the whole close, so a fully wedged client can take a
// multiple of this value to return.
//
// The value is generous on purpose: a controller is short-lived and has no
// hosted work of its own to drain, so reaching this budget means the runtime is
// genuinely stuck rather than merely busy.
const controllerShutdownTimeout = 5 * time.Second

// incompleteShutdownError is the contract for a durable client that did not
// finish shutting down inside controllerShutdownTimeout.
//
// It is reported instead of dropped because an incomplete shutdown is not
// cosmetic: the runtime closes the shared database handle unconditionally when
// its budget expires, so any component still running loses the handle mid-flight
// and the records it would have written are never made durable.
//
// Retrying cannot help — the runtime reopens its own shutdown guard after a
// timeout, so an unguarded retry would spend the whole budget again on a client
// whose database handle is already gone. The controller's Close refuses that
// retry itself rather than only warning against it.
func incompleteShutdownError(site clientSite, cause error) error {
	why := fmt.Sprintf("At least one part of the durable runtime was still running when its %s shutdown budget expired, "+
		"so the shutdown was cut short.", controllerShutdownTimeout)

	switch site {
	case clientSiteQueueCommand:
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "The work-queue command finished, but the durable client it used did not stop cleanly.",
			Why:      why,
			Where:    "Releasing the client after a work-queue command (internal/handlers/queue.go in handlers.withQueueClient).",
			Impact: "The setting shown above was read back from the database before the stop began, so it is what the " +
				"database holds. Nothing this command did was left half-finished. What the failure does say is that " +
				"the database is under strain or held by something that will not let go, which will affect the next " +
				"command too.",
			Fix: "1. Confirm the setting by asking again:\n" +
				"     pasture queue concurrency get <queue>\n" +
				"2. Find what else is holding the database:\n" +
				"     pgrep -fa 'pasture|pastured'\n" +
				"3. Confirm the database file is present and not held exclusively:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"4. If this repeats, stop the other writers and run the command again.",
			Cause: cause,
		}

	case clientSiteEpochController:
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "The epoch controller stopped, but its durable client did not shut down cleanly.",
			Why:      why,
			Where:    "Closing the epoch controller (internal/handlers/controller.go in dbosController.Close).",
			Impact: "The database handle is closed and the controller is unusable, but the work that was still running was " +
				"interrupted before it could record its result. Epoch state read straight after this may be stale or " +
				"incomplete. Nothing is lost permanently: the daemon recovers interrupted work on its next start.",
			Fix: "1. Do not treat this as a clean stop, and do not reuse the controller.\n" +
				"2. Check the daemon is running and healthy; a wedged or unreachable database is the usual cause:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"3. Re-read the epoch state through the daemon before acting on it:\n" +
				"     pasture status --epoch-id <epoch-id>\n" +
				"4. If this repeats, stop other writers to the database file and retry the command.",
			Cause: cause,
		}

	default:
		// Never fall through to a caller's text. Each of these reports tells the
		// operator what to do next, and the two say opposite things: one says the
		// value you were shown stands, the other says re-read the state because
		// work may have been lost. Guessing between them is worse than saying
		// nothing.
		return errors.Join(unhandledClientSiteError(site, "reporting a durable client that did not stop"), cause)
	}
}

// Close shuts the durable client down and then releases the trail handle.
//
// Close is one-shot and safe to call from several places at once: the shutdown
// runs on the first call, and every later call replays that first result
// immediately. A deferred Close after an explicit one therefore costs nothing,
// and a Close that reported an incomplete shutdown never spends the budget a
// second time on a client whose database handle the runtime has already closed.
// The runtime's own guard would permit that retry; this one does not.
//
// An incomplete shutdown is REPORTED, not swallowed — see
// incompleteShutdownError for what the caller must do with it. The trail is
// closed on that path too: it is a separate handle, and leaving it open would
// leak it on every failed shutdown.
func (c *dbosController) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.shutdown() })
	return c.closeErr
}

// shutdown performs the one real close. Only Close calls it, exactly once.
func (c *dbosController) shutdown() error {
	var errs []error
	if c.client != nil {
		// The client owns and closes the SQLiteSystemDB handle supplied at
		// construction; closing c.db separately would double-close the same DB.
		// The shutdown reaches that handle through sqlPoolAdapter.Close in
		// dbos/internal/sysdb/dbq.go, on the timeout path too. Engine.Shutdown
		// in internal/engine/engine.go states the same ownership for the
		// engine's handle; the two must stay in agreement.
		if err := releaseClient(c.client, clientSiteEpochController); err != nil {
			errs = append(errs, err)
		}
	}
	if c.trailCloser != nil {
		if err := c.trailCloser.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
