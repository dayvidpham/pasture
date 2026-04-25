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
	"time"

	"github.com/spf13/cobra"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
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

	// Idle-after-migrate flag (S7; unblocks S3's Scenario 12 concurrent-migrator
	// race test). When set to a positive duration, after migration completes
	// (and after well-known automaton registration completes) the daemon idles
	// for the duration before starting the Temporal worker. Default `0` means
	// "no idle window — proceed straight to worker start", which is the
	// production behaviour. Tests set this to e.g. `--idle-after-migrate=2s`
	// to widen the window during which a second migrator can race the first.
	root.PersistentFlags().Duration("idle-after-migrate", 0,
		"after migration + well-known agent registration, idle for the given duration before starting the Temporal worker (default 0 = disabled; used by S3 Scenario 12 race test)")

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
//  2. Initialise the audit trail backend (and, for sqlite, the unified
//     protocol.TaskTracker + well-known automaton-agent registration).
//  3. Optional idle window (`--idle-after-migrate`) for race-test scenarios.
//  4. Connect to the Temporal server.
//  5. Auto-register search attributes (idempotent).
//  6. Initialise the hooks Manager (no default handlers in v1; plugins add them).
//  7. Construct Activities struct with injected trail, hooks Manager, and
//     well-known agent cache.
//  8. Create the Temporal worker and register workflows + activities.
//  9. Start the worker; block until SIGINT/SIGTERM; drain and shut down.
func run(cmd *cobra.Command, configFile string) error {
	logger := slog.Default()

	// ── 1. Config resolution ─────────────────────────────────────────────────
	cfg, cfgErr := config.ResolvePasturedConfigFromFile(cmd, configFile)
	if cfgErr != nil {
		return fmt.Errorf(
			"pastured: configuration error"+
				" — falling back to defaults is not safe for a daemon"+
				": %w",
			cfgErr,
		)
	}
	logger.Info("pastured starting",
		"version", version,
		"namespace", cfg.Connection.Namespace,
		"taskQueue", cfg.Connection.TaskQueue,
		"serverAddress", cfg.Connection.ServerAddress,
		"auditTrail", cfg.AuditTrail,
	)

	// ── 2. Audit trail + well-known agent registration ──────────────────────
	// initAuditTrail returns the audit.Trail (used by Activities) and, for
	// the sqlite backend, also the populated WellKnownAgentCache (S7).
	// For the in-memory backend the cache is empty (the in-memory trail
	// does not back a Provenance subsystem so there are no AgentIDs to mint).
	trail, wellKnownCache, closer, err := initAuditTrail(cfg)
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

	logger.Info("audit trail ready",
		"backend", cfg.AuditTrail,
		"wellKnownAgents", wellKnownCache.Len(),
	)

	// ── 3. Optional idle-after-migrate window ────────────────────────────────
	// Used by S3 Scenario 12 (concurrent-migrator race) to widen the window
	// during which a second migrator can race the first. Production paths
	// pass 0 (default) and skip this branch entirely.
	idleDuration, err := cmd.Flags().GetDuration("idle-after-migrate")
	if err != nil {
		return fmt.Errorf(
			"pastured: cannot read --idle-after-migrate flag value"+
				" — this is a programming error in flag registration: %w",
			err,
		)
	}
	if idleDuration > 0 {
		logger.Info("idling after migration as requested by --idle-after-migrate",
			"duration", idleDuration,
		)
		select {
		case <-time.After(idleDuration):
			logger.Info("idle window elapsed; proceeding to worker start")
		case sig := <-signalChannel():
			logger.Info("shutdown signal during idle window; exiting before worker start", "signal", sig)
			return nil
		}
	}

	// ── 4. Connect to Temporal ────────────────────────────────────────────────
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

	// ── 5. Auto-register search attributes ───────────────────────────────────
	ctx := context.Background()
	if err := temporal.EnsureSearchAttributes(ctx, temporalClient, cfg.Connection.Namespace, logger); err != nil {
		// Non-fatal: log and continue — search attributes may already exist or
		// the namespace may not support custom attributes in all Temporal versions.
		logger.Warn("search attribute registration failed — some observability queries may not work",
			"err", err,
		)
	}

	// ── 6. Initialise hooks Manager ───────────────────────────────────────────
	// Plugin integrations (e.g. Claude Code hooks) register handlers by
	// importing pastured as a library or via the hooks API. The in-tree
	// free-floating event recorders (currently: GitRecorder, S9) are
	// registered conditionally below — only when the audit backend is sqlite,
	// because the recorders require a durable *sql.DB to recover the
	// just-inserted audit_events row id (PROPOSAL-2 §7.11; see
	// internal/tasks/free_floating.go for the rationale).
	hooksMgr := hooks.NewManager()
	registeredRecorders := 0
	if tracker, ok := trail.(protocol.TaskTracker); ok && cfg.AuditTrail == types.BackendSqlite {
		dbPath := cfg.AuditDBPath
		if dbPath == "" {
			home, herr := os.UserHomeDir()
			if herr != nil {
				home = "."
			}
			dbPath = filepath.Join(home, ".local", "share", "pasture", "audit.db")
		}
		auditDB, derr := tasks.OpenAuditDBForFreeFloating(dbPath)
		if derr != nil {
			return fmt.Errorf(
				"pastured: cannot open auxiliary audit handle for free-floating event recorders"+
					" (path=%q)"+
					" — the unified pasture.db opened cleanly but a second handle to the same file failed; verify the file is not held by another process: %w",
				dbPath, derr,
			)
		}
		defer func() {
			if cerr := auditDB.Close(); cerr != nil {
				logger.Error("auxiliary audit handle close error", "err", cerr)
			}
		}()
		if _, err := hooks.RegisterDefaultRecorders(hooksMgr, tracker, auditDB); err != nil {
			return fmt.Errorf(
				"pastured: cannot register default free-floating event recorders"+
					" — daemon startup cannot proceed with hooks half-wired: %w",
				err,
			)
		}
		registeredRecorders = 1
	}
	logger.Info("hooks manager ready", "handlers", registeredRecorders)

	// ── 7. Construct Activities with injected dependencies ────────────────────
	// Activities receives trail, hooksMgr, and the populated well-known agent
	// cache (for S8 attribution) via constructor injection rather than
	// singletons — this makes the wiring explicit and testable.
	acts := &temporal.Activities{
		Trail:           trail,
		HooksMgr:        hooksMgr,
		WellKnownAgents: wellKnownCache,
	}

	// ── 8. Create worker and register workflows + activities ──────────────────
	w := worker.New(temporalClient, cfg.Connection.TaskQueue, worker.Options{})
	temporal.RegisterWorkflows(w, acts)
	logger.Info("registered workflows and activities",
		"taskQueue", cfg.Connection.TaskQueue,
	)

	// ── 9. Start worker, block, graceful shutdown ─────────────────────────────
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

// initAuditTrail creates the appropriate Trail implementation from config and,
// for the sqlite backend, also opens the unified protocol.TaskTracker and
// runs the well-known automaton-agent registration (PROPOSAL-2 §7.7.3, S7).
//
// Return contract:
//   - trail: the audit.Trail used by Activities. For sqlite this is the
//     unified TaskTracker (which satisfies audit.Trail by exposing the four
//     audit method signatures); for memory this is a fresh in-memory trail.
//   - cache: a populated WellKnownAgentCache for sqlite (15 entries on
//     successful registration), or an empty cache for memory (the in-memory
//     trail does not back a Provenance subsystem so there are no AgentIDs
//     to mint). The cache is always non-nil so Activities.WellKnownAgents
//     may be safely dereferenced for length checks.
//   - closer: a cleanup function that must be called on daemon shutdown.
//     For sqlite this calls TaskTracker.Close (which releases both the
//     audit and Provenance handles exactly once). For memory this is nil.
//   - error: initialisation failure (CategoryStorage / CategoryConnection /
//     CategoryConfig).
func initAuditTrail(cfg config.PasturedConfig) (audit.Trail, *tasks.WellKnownAgentCache, func() error, error) {
	emptyCache := tasks.NewWellKnownAgentCache()

	switch cfg.AuditTrail {
	case types.BackendMemory, "":
		// In-memory backend: no Provenance handle to mint AgentIDs against,
		// so the cache stays empty. Activities that require attribution must
		// either short-circuit on cache.Len() == 0 or skip the in-memory
		// configuration in production deployments.
		return audit.NewInMemoryAuditTrail(), emptyCache, nil, nil

	case types.BackendSqlite:
		dbPath := cfg.AuditDBPath
		if dbPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				home = "."
			}
			dbPath = filepath.Join(home, ".local", "share", "pasture", "audit.db")
		}

		// Open the unified TaskTracker. This runs the audit migrator (v1→v3)
		// and creates the pasture-side tables. The returned tracker satisfies
		// audit.Trail because its method set includes the four audit
		// signatures inline.
		tracker, err := tasks.OpenTaskTracker(dbPath)
		if err != nil {
			return nil, emptyCache, nil, fmt.Errorf(
				"pastured.initAuditTrail: cannot open unified TaskTracker at %q"+
					" — verify the path is writable and the on-disk schema is at v3 or compatible: %w",
				dbPath, err,
			)
		}

		// Register the canonical 15 well-known automaton agents (S7,
		// idempotent across restarts). Run with a background context — the
		// startup path is bounded (15 entries) and cancellation is signalled
		// out-of-band via the OS signal handler in step 9.
		cache := tasks.NewWellKnownAgentCache()
		if err := tasks.RegisterWellKnownAgents(context.Background(), tracker, cache); err != nil {
			// On failure, close the tracker we just opened so the file
			// handle is released before propagating the error.
			_ = tracker.Close()
			return nil, emptyCache, nil, fmt.Errorf(
				"pastured.initAuditTrail: well-known automaton agent registration failed at %q"+
					" — daemon startup cannot proceed without the cache populated: %w",
				dbPath, err,
			)
		}

		return tracker, cache, tracker.Close, nil

	default:
		return nil, emptyCache, nil, fmt.Errorf(
			"unknown audit trail backend %q"+
				" — valid values are %q and %q"+
				" — set via --audit-trail flag or PASTURE_AUDIT_TRAIL env var",
			cfg.AuditTrail, types.BackendMemory, types.BackendSqlite,
		)
	}
}

// signalChannel returns a buffered channel that fires on SIGINT/SIGTERM. It
// is used by the optional --idle-after-migrate window so the daemon can exit
// cleanly without proceeding to worker start if the operator hits Ctrl-C
// during the idle period (the default worker-start path uses its own signal
// channel — see step 9).
func signalChannel() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch
}
