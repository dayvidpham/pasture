// Command pastured is the Pasture daemon that hosts the DBOS durable engine.
//
// pastured opens the unified pasture.db file, wires the engine's audit,
// provenance, and hook dependencies, launches DBOS recovery, then blocks until
// SIGINT or SIGTERM. It does not require an external workflow server.
//
// Exit codes (from internal/errors):
//
//	0  success: the daemon started and later stopped in an orderly way
//	1  validation error (bad flags or arguments), or an error with no category
//	2  connection error (the database file cannot be opened)
//	3  workflow error, including a stop that did not finish inside its budget
//	   while work was still running
//	4  config error
//	5  storage error (migration or schema failure)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// version is the release identity reported by `pastured --version` (and by the
// startup log line). It is a package-level var (not a const) precisely so the
// linker can stamp it at build time:
//
//	go build -ldflags "-X main.version=v0.0.4" ./cmd/pastured
//
// The two output shapes are:
//
//	stamped (release / nix build): pastured v0.0.4
//	unstamped (plain `go build`):  pastured devel
//
// The unstamped default is the bare word "devel" rather than a synthetic
// "v0.0.0-*" pseudo-tag so that no consumer scraping the output for a release
// tag can mistake a development build for a released version: "devel" matches
// no vX.Y.Z pattern at all.
//
// Source-of-truth chain for a stamped build:
//
//	.claude-plugin/plugin.json .version
//	  -> tag v<version>            (.github/workflows/release.yml, detect job)
//	  -> -X main.version=v<version> (release.yml build job; flake.nix ldflags)
//	  -> this variable             -> `pastured v<version>`
var version = "devel"

// perComponentShutdownTimeout is the budget the daemon gives EACH part of the
// durable runtime when it stops. It is not a total: the runtime stops its
// parts one after another and gives every one of them the full value, so the
// worst case is engine.WorstCaseShutdownDuration of this value (70s at 10s).
// A stop held up by work that is still running costs one budget in practice,
// because only the in-flight-work wait expires.
//
// Keep the worst case inside the stop deadline of whatever supervises the
// daemon (a service manager kills the process when its own deadline passes,
// which loses the orderly close this budget exists to buy).
const perComponentShutdownTimeout = 10 * time.Second

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra prints the error itself; we only choose the exit code. The
		// code comes from the error's own category, so an operator's script
		// can tell a bad configuration from an unreachable database and from
		// a stop that did not finish. See the table in the package comment.
		os.Exit(pasterrors.ExitCode(err))
	}
}

// newRootCmd builds and returns the pastured Cobra root command.
// Extracted for testability.
func newRootCmd() *cobra.Command {
	var configFile string

	root := &cobra.Command{
		Use:   "pastured",
		Short: "Pasture daemon - DBOS engine host for epoch orchestration",
		Long: `pastured hosts the Pasture DBOS durable engine.

It opens the unified pasture.db file, wires audit/provenance/hook dependencies,
launches DBOS recovery for in-flight epochs and queued slice/review work, and
then blocks until SIGINT or SIGTERM. No external workflow server is required.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, configFile)
		},
	}

	root.PersistentFlags().StringVar(&configFile, "config", config.DefaultConfigPath(),
		"path to YAML config file")
	root.PersistentFlags().String("db", "",
		"Path to the unified pasture SQLite database (env: PASTURE_DB_PATH, default: ~/.local/share/pasture/pasture.db)")
	root.PersistentFlags().String("audit-trail", string(types.BackendSqlite),
		`audit persistence backend: "sqlite" (durable, default) or "memory" (non-durable; env: PASTURE_AUDIT_TRAIL)`)
	root.PersistentFlags().Int("slice-concurrency", 0,
		"max concurrent slice/review sub-workflows per executor (0 = default 8; env: PASTURE_SLICE_CONCURRENCY)")
	root.PersistentFlags().Bool("version", false, "print version and exit")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		showVersion, _ := cmd.Flags().GetBool("version")
		if showVersion {
			fmt.Printf("pastured %s\n", version)
			os.Exit(0)
		}
		return nil
	}

	return root
}

// run is the main daemon logic, separated from Cobra wiring for testability.
func run(cmd *cobra.Command, configFile string) error {
	logger := slog.Default()

	cfg, cfgErr := config.ResolvePasturedConfigFromFile(cmd, configFile)
	if cfgErr != nil {
		return fmt.Errorf(
			"pastured: configuration error - falling back to defaults is not safe for a daemon: %w",
			cfgErr,
		)
	}

	dbPath, dbErr := resolveDBPath(cmd)
	if dbErr != nil {
		return dbErr
	}
	cfg.AuditDBPath = dbPath

	sliceConcurrency, scErr := resolveSliceConcurrency(cmd)
	if scErr != nil {
		return scErr
	}

	// Listen for the stop signals BEFORE anything is started, so a signal
	// that arrives during startup is answered instead of killing the process
	// by the default action. Two listeners are registered from one signal:
	//
	//	startupCtx  cancelled on the first signal. It bounds the WAIT for
	//	            startup, and nothing else. See engineLifetimeContext for
	//	            why the engine must not live under it.
	//	stopCh      carries the same signal to the running daemon below. A
	//	            signal that arrives during startup waits in the channel.
	startupCtx, releaseStartupSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer releaseStartupSignals()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopCh)

	// The starting line is logged only after both listeners are in place, so
	// its appearance means a stop signal is now answered rather than fatal.
	logger.Info("pastured starting",
		"version", version,
		"dbPath", cfg.AuditDBPath,
		"auditTrail", cfg.AuditTrail,
		"sliceConcurrency", sliceConcurrency,
	)

	// Build the daemon beside the wait, not inside it. The build runs under a
	// context the signal never cancels (engineLifetimeContext), so a stop can
	// never reach in-flight work through it; the signal instead ends the WAIT
	// for the build, and the build that lands afterwards is closed.
	built := make(chan startupOutcome, 1)
	go func() {
		runtime, err := buildDaemonRuntime(engineLifetimeContext(startupCtx), cfg, sliceConcurrency, logger)
		built <- startupOutcome{runtime: runtime, err: err}
	}()

	var runtime *daemonRuntime
	select {
	case outcome := <-built:
		if outcome.err != nil {
			return outcome.err
		}
		runtime = outcome.runtime
	case <-startupCtx.Done():
		logger.Info("stop signal received while starting; waiting for the startup step to finish so it can be closed",
			"budget", startupAbandonTimeout)
		return abandonStartup(logger, built)
	}

	// A signal that arrived while the engine was being built: stop it rather
	// than launch a daemon the operator has already asked to end.
	if startupCtx.Err() != nil {
		return stopBeforeLaunch(logger, runtime)
	}

	if launchErr := runtime.Engine.Launch(); launchErr != nil {
		return launchFailure(launchErr, runtime.Close(logger))
	}

	logger.Info("DBOS engine launched, waiting for shutdown",
		"dbPath", runtime.DBPath,
		"sliceConcurrency", runtime.SliceConcurrency,
		"hookRecorders", runtime.RegisteredRecorders,
	)

	sig := <-stopCh
	logger.Info("stop signal received, stopping the durable engine",
		"signal", sig,
		"perComponentBudget", perComponentShutdownTimeout,
		"worstCase", engine.WorstCaseShutdownDuration(perComponentShutdownTimeout),
	)
	// An incomplete stop is reported to the operator, not swallowed: the
	// engine already logged which parts were still running, and returning the
	// error here gives the process a non-zero exit code (3, workflow) so a
	// service manager or script can see that the stop was not orderly.
	if err := runtime.Close(logger); err != nil {
		return err
	}
	logger.Info("pastured stopped cleanly")
	return nil
}

// launchFailure reports a failed launch, together with an engine that then
// did not stop cleanly.
//
// The launch failure is the primary fault, so it alone stays in the error
// chain and decides the exit code: a launch failure with no category must
// keep its own code rather than borrow the stop error's. The stop failure is
// kept as text, and the engine has already logged it in full with the parts
// that were still running.
func launchFailure(launchErr, stopErr error) error {
	if launchErr == nil {
		return stopErr
	}
	if stopErr == nil {
		return launchErr
	}
	return fmt.Errorf("%w (the engine also did not stop cleanly afterwards: %v)", launchErr, stopErr)
}

// startupAbandonTimeout bounds how long the daemon waits for an abandoned
// startup step to finish once a stop signal has ended the wait for it. The
// step itself is not interruptible (see engineLifetimeContext), so this is
// the difference between a process that ends and a process that hangs.
//
// It is generous on purpose: the step is usually a database open or a schema
// migration, and closing it properly is worth more than exiting a few seconds
// sooner. A service manager's own stop deadline is the outer bound.
const startupAbandonTimeout = 30 * time.Second

// startupOutcome is what the startup goroutine reports back: a built daemon or
// the reason it could not be built.
type startupOutcome struct {
	runtime *daemonRuntime
	err     error
}

// engineLifetimeContext returns the context the durable engine lives under.
//
// It deliberately DROPS cancellation from the startup context. The durable
// runtime derives its own base context from what it is given, and it cancels
// that base itself when it stops, with a private cause that means "we are
// stopping". Every running workflow watches for that cause: a cancellation
// with any OTHER cause is read as "this work was cancelled", and the work's
// row is written as cancelled — which the recovery sweep then skips for good.
//
// So a stop signal that reached the engine through its parent context would
// not stop the epoch in flight; it would destroy it. Startup interruption is
// therefore bounded by ending the WAIT for startup (see run), never by
// cancelling the engine's own context.
func engineLifetimeContext(startupCtx context.Context) context.Context {
	return context.WithoutCancel(startupCtx)
}

// abandonStartup ends a startup the operator stopped. It waits for the
// startup step to finish so the half-built daemon can be closed, and reports
// nothing wrong: a stop the operator asked for is a normal ending.
//
// It reports a failure only when the abandoned step does not finish inside
// startupAbandonTimeout — that IS a stop that did not finish in its budget,
// and the operator must know the process ended with work still running.
func abandonStartup(logger *slog.Logger, built <-chan startupOutcome) error {
	select {
	case outcome := <-built:
		if outcome.err != nil {
			logger.Info("the startup step ended with a failure while the daemon was stopping; the stop was what the operator asked for",
				"err", outcome.err)
			return nil
		}
		return stopBeforeLaunch(logger, outcome.runtime)
	case <-time.After(startupAbandonTimeout):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "The daemon stopped before the step it was starting had finished.",
			Why: "A stop signal arrived while the daemon was starting, and the startup step did not finish within " +
				startupAbandonTimeout.String() + ", so it could not be closed in an orderly way.",
			Where:  "Starting the daemon (cmd/pastured/main.go in run).",
			Impact: "The daemon is not running. The step that was still working held the database when the process ended, so the database may need a check before the next start.",
			Fix: "1. Confirm no other daemon holds the database:\n" +
				"     pgrep -fa pastured\n" +
				"2. Start the daemon again and let it finish starting before you stop it:\n" +
				"     pastured",
		}
	}
}

// stopBeforeLaunch closes a daemon that was built but never launched, because
// the operator stopped it first. The engine has nothing in flight at that
// point, so a clean close is the normal ending and reports nothing wrong.
func stopBeforeLaunch(logger *slog.Logger, runtime *daemonRuntime) error {
	if closeErr := runtime.Close(logger); closeErr != nil {
		return closeErr
	}
	logger.Info("pastured stopped cleanly before it finished starting")
	return nil
}

type daemonRuntime struct {
	Engine              *engine.Engine
	HooksMgr            *hooks.Manager
	DBPath              string
	SliceConcurrency    int
	RegisteredRecorders int
	closeDeps           []func() error
}

// Close stops the engine and releases the daemon's remaining handles. It
// returns the engine's stop error (nil when the engine stopped inside its
// budget) so the caller can report an incomplete stop and pick an exit code;
// see engine.ShutdownIncompleteError for the parts it names.
//
// The dependency handles are closed whatever the engine reported, and a
// failure to close one of them is logged rather than returned: by that point
// the durable runtime has already closed the shared database file it owns, so
// a second close failing tells the operator nothing they can act on.
func (r *daemonRuntime) Close(logger *slog.Logger) error {
	if r == nil {
		return nil
	}
	var shutdownErr error
	if r.Engine != nil {
		shutdownErr = r.Engine.Shutdown(perComponentShutdownTimeout)
	}
	for _, closeFn := range r.closeDeps {
		if closeFn == nil {
			continue
		}
		if err := closeFn(); err != nil {
			logger.Error("Couldn't cleanly close a daemon resource; the database may need an integrity check before next startup", "err", err)
		}
	}
	return shutdownErr
}

func buildDaemonRuntime(ctx context.Context, cfg config.PasturedConfig, sliceConcurrency int, logger *slog.Logger) (*daemonRuntime, error) {
	trail, wellKnownCache, trailCloser, err := initAuditTrail(cfg)
	if err != nil {
		return nil, fmt.Errorf(
			"pastured: audit trail initialisation failed (backend=%q, path=%q) - check PASTURE_AUDIT_TRAIL and PASTURE_DB_PATH: %w",
			cfg.AuditTrail, cfg.AuditDBPath, err,
		)
	}

	closeDeps := []func() error{}
	if trailCloser != nil {
		closeDeps = append(closeDeps, trailCloser)
	}

	hooksMgr, registeredRecorders, hooksCloser, err := initHooksManager(cfg, trail)
	if err != nil {
		closeAll(logger, closeDeps)
		return nil, err
	}
	if hooksCloser != nil {
		closeDeps = append(closeDeps, hooksCloser)
	}

	var tracker protocol.TaskTracker
	if t, ok := trail.(protocol.TaskTracker); ok {
		tracker = t
	}

	engCfg := newEngineConfig(cfg.AuditDBPath, sliceConcurrency, trail, tracker, hooksMgr, logger)
	eng, err := engine.New(ctx, engCfg)
	if err != nil {
		closeAll(logger, closeDeps)
		return nil, err
	}

	logger.Info("daemon runtime ready",
		"dbPath", cfg.AuditDBPath,
		"wellKnownAgents", wellKnownCache.Len(),
		"hookRecorders", registeredRecorders,
		"hasTracker", tracker != nil,
	)

	return &daemonRuntime{
		Engine:              eng,
		HooksMgr:            hooksMgr,
		DBPath:              cfg.AuditDBPath,
		SliceConcurrency:    sliceConcurrency,
		RegisteredRecorders: registeredRecorders,
		closeDeps:           closeDeps,
	}, nil
}

func closeAll(logger *slog.Logger, closeDeps []func() error) {
	for _, closeFn := range closeDeps {
		if closeFn == nil {
			continue
		}
		if err := closeFn(); err != nil {
			logger.Error("Couldn't cleanly close a daemon resource after startup failure", "err", err)
		}
	}
}

func newEngineConfig(dbPath string, sliceConcurrency int, trail audit.Trail, tracker protocol.TaskTracker, hooksMgr *hooks.Manager, logger *slog.Logger) engine.Config {
	return engine.Config{
		DBPath:             dbPath,
		ExecutorID:         engine.DefaultExecutorID,
		AppName:            engine.DefaultAppName,
		ApplicationVersion: engine.DefaultApplicationVersion,
		Trail:              trail,
		Tracker:            tracker,
		SliceConcurrency:   sliceConcurrency,
		HooksMgr:           hooksMgr,
		Logger:             logger,
	}
}

func initHooksManager(cfg config.PasturedConfig, trail audit.Trail) (*hooks.Manager, int, func() error, error) {
	hooksMgr := hooks.NewManager()

	tracker, ok := trail.(protocol.TaskTracker)
	if !ok || cfg.AuditTrail != types.BackendSqlite {
		return hooksMgr, 0, nil, nil
	}

	dbPath := cfg.AuditDBPath
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}

	auditDB, err := tasks.OpenAuditDBForFreeFloating(dbPath)
	if err != nil {
		return nil, 0, nil, fmt.Errorf(
			"pastured: cannot open auxiliary audit handle for hook recorders (path=%q) - the unified pasture.db opened cleanly but a second handle to the same file failed: %w",
			dbPath, err,
		)
	}

	if _, err := hooks.RegisterDefaultRecorders(hooksMgr, tracker, auditDB); err != nil {
		_ = auditDB.Close()
		return nil, 0, nil, fmt.Errorf(
			"pastured: cannot register default free-floating event recorders - daemon startup cannot proceed with hooks half-wired: %w",
			err,
		)
	}

	return hooksMgr, 1, auditDB.Close, nil
}

// initAuditTrail creates the audit trail and, for sqlite, registers the
// well-known automaton agents in the unified task tracker.
func initAuditTrail(cfg config.PasturedConfig) (audit.Trail, *tasks.WellKnownAgentCache, func() error, error) {
	emptyCache := tasks.NewWellKnownAgentCache()

	switch cfg.AuditTrail {
	case types.BackendMemory, "":
		return audit.NewInMemoryAuditTrail(), emptyCache, nil, nil

	case types.BackendSqlite:
		dbPath := cfg.AuditDBPath
		if dbPath == "" {
			dbPath = tasks.DefaultDBPath()
		}

		tracker, err := tasks.OpenTaskTracker(dbPath)
		if err != nil {
			return nil, emptyCache, nil, fmt.Errorf(
				"pastured.initAuditTrail: cannot open unified TaskTracker at %q - verify the path is writable and the on-disk schema is compatible: %w",
				dbPath, err,
			)
		}

		cache := tasks.NewWellKnownAgentCache()
		if err := tasks.RegisterWellKnownAgents(context.Background(), tracker, cache); err != nil {
			_ = tracker.Close()
			return nil, emptyCache, nil, fmt.Errorf(
				"pastured.initAuditTrail: well-known automaton agent registration failed at %q - daemon startup cannot proceed without the cache populated: %w",
				dbPath, err,
			)
		}

		return tracker, cache, tracker.Close, nil

	default:
		return nil, emptyCache, nil, fmt.Errorf(
			"%q is not a recognised audit-trail backend. The supported values are %q (in-memory, non-durable) and %q (durable, on-disk). Pass one of these via --audit-trail or set PASTURE_AUDIT_TRAIL.",
			cfg.AuditTrail, types.BackendMemory, types.BackendSqlite,
		)
	}
}

func resolveSliceConcurrency(cmd *cobra.Command) (int, error) {
	flagVal, err := cmd.Flags().GetInt("slice-concurrency")
	if err != nil {
		return 0, fmt.Errorf(
			"pastured: cannot read --slice-concurrency flag value - this is a programming error in flag registration: %w",
			err,
		)
	}
	return engine.ResolveSliceConcurrency(flagVal)
}

// resolveDBPath resolves the unified pasture.db path for the daemon.
//
// Precedence:
//  1. --db CLI flag when explicitly set.
//  2. PASTURE_DB_PATH.
//  3. tasks.DefaultDBPath().
func resolveDBPath(cmd *cobra.Command) (string, error) {
	flag, err := cmd.Flags().GetString("db")
	if err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     "Couldn't read the --db flag value.",
			Why:      "The Cobra command did not expose the expected --db flag.",
			Where:    "Resolving pastured database path (cmd/pastured/main.go in resolveDBPath).",
			Impact:   "The daemon cannot choose a database path safely.",
			Fix:      "Report this as a programming error; the root command must register --db before run().",
			Cause:    err,
		}
	}
	if cmd.Flags().Changed("db") && flag != "" {
		return flag, nil
	}
	if env := os.Getenv("PASTURE_DB_PATH"); env != "" {
		return env, nil
	}
	return tasks.DefaultDBPath(), nil
}
