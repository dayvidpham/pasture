// Command pastured is the Pasture daemon — a Temporal worker that runs
// aura-protocol workflows and activities for multi-agent orchestration.
//
// pastured connects to a Temporal server, auto-registers required search
// attributes, registers all Pasture workflows and activities, and then blocks
// handling work until SIGINT or SIGTERM is received.
//
// Configuration resolution priority (highest → lowest):
//  1. CLI flags
//  2. Environment variables (TEMPORAL_NAMESPACE, TEMPORAL_TASK_QUEUE, TEMPORAL_ADDRESS,
//     PASTURE_AUDIT_TRAIL, PASTURE_AUDIT_DB_PATH)
//  3. YAML config file (default: ~/.config/pasture/config.yaml)
//  4. Built-in defaults
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/internal/types"
)

const version = "v0.1.0"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra prints the error itself; we only need to set the exit code.
		os.Exit(1)
	}
}

// newRootCmd builds and returns the pastured Cobra root command.
// Extracted for testability — tests can call newRootCmd().Execute() directly.
func newRootCmd() *cobra.Command {
	var configFile string

	root := &cobra.Command{
		Use:   "pastured",
		Short: "Pasture daemon — Temporal worker for multi-agent epoch orchestration",
		Long: `pastured connects to a Temporal server and runs the Pasture epoch protocol.

It registers EpochWorkflow, SliceWorkflow, and ReviewPhaseWorkflow together with
their supporting activities. Search attributes are auto-registered on every
startup (idempotent). The daemon blocks until SIGINT or SIGTERM is received, at
which point it drains in-flight tasks and exits cleanly.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, configFile)
		},
	}

	// ── Persistent flags ──────────────────────────────────────────────────────

	// Config file (read before all other flags so Viper can layer correctly).
	root.PersistentFlags().StringVar(&configFile, "config", config.DefaultConfigPath(),
		"path to YAML config file")

	// Temporal connection flags.
	root.PersistentFlags().String("namespace", "default",
		"Temporal namespace (env: TEMPORAL_NAMESPACE)")
	root.PersistentFlags().String("task-queue", "pasture",
		"Temporal task queue name (env: TEMPORAL_TASK_QUEUE)")
	root.PersistentFlags().String("address", "localhost:7233",
		"Temporal server gRPC address (env: TEMPORAL_ADDRESS)")

	// Audit trail flags.
	root.PersistentFlags().String("audit-trail", string(types.BackendMemory),
		`audit persistence backend: "memory" (non-durable, default) or "sqlite" (env: PASTURE_AUDIT_TRAIL)`)
	root.PersistentFlags().String("audit-db-path", "",
		"SQLite audit database path; defaults to ~/.local/share/pasture/audit.db (env: PASTURE_AUDIT_DB_PATH)")

	// Version flag.
	root.PersistentFlags().Bool("version", false, "print version and exit")

	// Pre-run hook: handle --version before RunE.
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
//
// Steps:
//  1. Resolve full PasturedConfig (CLI > env > YAML > defaults).
//  2. Initialise the audit trail backend.
//  3. Connect to the Temporal server.
//  4. Auto-register search attributes (idempotent).
//  5. Initialise the hooks Manager (no default handlers in v1; plugins add them).
//  6. Construct Activities struct with injected trail and hooks Manager.
//  7. Create the Temporal worker and register workflows + activities.
//  8. Start the worker; block until SIGINT/SIGTERM; drain and shut down.
func run(cmd *cobra.Command, configFile string) error {
	logger := slog.Default()

	// ── 1. Config resolution ─────────────────────────────────────────────────
	cfg := config.ResolvePasturedConfigFromFile(cmd, configFile)
	logger.Info("pastured starting",
		"version", version,
		"namespace", cfg.Connection.Namespace,
		"taskQueue", cfg.Connection.TaskQueue,
		"serverAddress", cfg.Connection.ServerAddress,
		"auditTrail", cfg.AuditTrail,
	)

	// ── 2. Audit trail initialisation ────────────────────────────────────────
	trail, closer, err := initAuditTrail(cfg)
	if err != nil {
		return fmt.Errorf(
			"pastured: audit trail initialisation failed"+
				" (backend=%q, path=%q)"+
				" — check the PASTURE_AUDIT_TRAIL and PASTURE_AUDIT_DB_PATH settings: %w",
			cfg.AuditTrail, cfg.AuditDBPath, err,
		)
	}
	if closer != nil {
		defer func() {
			if cerr := closer(); cerr != nil {
				logger.Error("audit trail close error", "err", cerr)
			}
		}()
	}

	logger.Info("audit trail ready", "backend", cfg.AuditTrail)

	// ── 3. Connect to Temporal ────────────────────────────────────────────────
	temporalClient, err := client.Dial(client.Options{
		HostPort:  cfg.Connection.ServerAddress,
		Namespace: cfg.Connection.Namespace,
	})
	if err != nil {
		return fmt.Errorf(
			"pastured: cannot connect to Temporal at %q (namespace=%q)"+
				" — verify the server is running and the address/namespace are correct: %w",
			cfg.Connection.ServerAddress, cfg.Connection.Namespace, err,
		)
	}
	defer temporalClient.Close()
	logger.Info("connected to Temporal", "address", cfg.Connection.ServerAddress)

	// ── 4. Auto-register search attributes ───────────────────────────────────
	ctx := context.Background()
	if err := temporal.EnsureSearchAttributes(ctx, temporalClient, cfg.Connection.Namespace, logger); err != nil {
		// Non-fatal: log and continue — search attributes may already exist or
		// the namespace may not support custom attributes in all Temporal versions.
		logger.Warn("search attribute registration failed — some observability queries may not work",
			"err", err,
		)
	}

	// ── 5. Initialise hooks Manager ───────────────────────────────────────────
	// No default handlers in v1. Plugin integrations (e.g. Claude Code hooks)
	// register handlers by importing pastured as a library or via the hooks API.
	hooksMgr := hooks.NewManager()
	logger.Info("hooks manager ready", "handlers", 0)

	// ── 6. Construct Activities with injected dependencies ────────────────────
	// Activities receives trail and hooksMgr via constructor injection rather
	// than singletons — this makes the wiring explicit and testable.
	acts := &temporal.Activities{
		Trail:    trail,
		HooksMgr: hooksMgr,
	}

	// ── 7. Create worker and register workflows + activities ──────────────────
	w := worker.New(temporalClient, cfg.Connection.TaskQueue, worker.Options{})
	temporal.RegisterWorkflows(w, acts)
	logger.Info("registered workflows and activities",
		"taskQueue", cfg.Connection.TaskQueue,
	)

	// ── 8. Start worker, block, graceful shutdown ─────────────────────────────
	// worker.Run() blocks internally and stops when the interrupt channel fires.
	// We use our own signal channel so we can log the shutdown reason.
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	workerErr := make(chan error, 1)
	go func() {
		logger.Info("worker started, waiting for tasks")
		workerErr <- w.Run(worker.InterruptCh())
	}()

	select {
	case sig := <-stopCh:
		logger.Info("shutdown signal received, draining worker", "signal", sig)
		w.Stop()
		// Drain the workerErr channel so the goroutine can exit cleanly.
		<-workerErr
	case err := <-workerErr:
		if err != nil {
			return fmt.Errorf(
				"pastured: worker terminated unexpectedly"+
					" (taskQueue=%q)"+
					" — check Temporal connectivity and worker logs: %w",
				cfg.Connection.TaskQueue, err,
			)
		}
	}

	logger.Info("pastured stopped cleanly")
	return nil
}

// initAuditTrail creates the appropriate Trail implementation from config.
//
// Returns the Trail, an optional closer func (non-nil for SQLite), and any
// initialisation error.
func initAuditTrail(cfg config.PasturedConfig) (audit.Trail, func() error, error) {
	switch cfg.AuditTrail {
	case types.BackendMemory, "":
		return audit.NewInMemoryAuditTrail(), nil, nil

	case types.BackendSqlite:
		dbPath := cfg.AuditDBPath
		if dbPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				home = "."
			}
			dbPath = filepath.Join(home, ".local", "share", "pasture", "audit.db")
		}
		sqliteTrail, err := audit.NewSqliteAuditTrail(dbPath)
		if err != nil {
			return nil, nil, err
		}
		return sqliteTrail, sqliteTrail.Close, nil

	default:
		return nil, nil, fmt.Errorf(
			"unknown audit trail backend %q"+
				" — valid values are %q and %q"+
				" — set via --audit-trail flag or PASTURE_AUDIT_TRAIL env var",
			cfg.AuditTrail, types.BackendMemory, types.BackendSqlite,
		)
	}
}
