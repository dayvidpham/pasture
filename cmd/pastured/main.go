// Command pastured is the Pasture daemon that hosts the DBOS durable engine.
//
// pastured opens the unified pasture.db file, wires the engine's audit,
// provenance, and hook dependencies, launches DBOS recovery, then blocks until
// SIGINT or SIGTERM. It does not require an external workflow server.
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

const version = "v0.1.0"

const engineShutdownTimeout = 10 * time.Second

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra prints the error itself; we only need to set the exit code.
		os.Exit(1)
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

	logger.Info("pastured starting",
		"version", version,
		"dbPath", cfg.AuditDBPath,
		"auditTrail", cfg.AuditTrail,
		"sliceConcurrency", sliceConcurrency,
	)

	runtime, err := buildDaemonRuntime(context.Background(), cfg, sliceConcurrency, logger)
	if err != nil {
		return err
	}

	if err := runtime.Engine.Launch(); err != nil {
		runtime.Close(logger)
		return err
	}

	logger.Info("DBOS engine launched, waiting for shutdown",
		"dbPath", runtime.DBPath,
		"sliceConcurrency", runtime.SliceConcurrency,
		"hookRecorders", runtime.RegisteredRecorders,
	)

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopCh)

	sig := <-stopCh
	logger.Info("shutdown signal received, stopping DBOS engine", "signal", sig)
	runtime.Close(logger)
	logger.Info("pastured stopped cleanly")
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

func (r *daemonRuntime) Close(logger *slog.Logger) {
	if r == nil {
		return
	}
	if r.Engine != nil {
		r.Engine.Shutdown(engineShutdownTimeout)
	}
	for _, closeFn := range r.closeDeps {
		if closeFn == nil {
			continue
		}
		if err := closeFn(); err != nil {
			logger.Error("Couldn't cleanly close a daemon resource; the database may need an integrity check before next startup", "err", err)
		}
	}
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
