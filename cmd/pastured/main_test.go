package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/engine"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"

	_ "modernc.org/sqlite"
)

// buildPasturedBinary builds the production pastured command (this package)
// into binary, optionally passing extra linker flags. A subprocess build is
// used deliberately: stamping is a link-time property, so it can only be proven
// by linking a binary and asking it for its version — inspecting the `version`
// variable from inside the test binary proves nothing about -ldflags, and could
// not observe the unstamped default if the test binary were itself stamped.
func buildPasturedBinary(t *testing.T, binary string, ldflags string) {
	t.Helper()
	args := []string{"build"}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "-o", binary, ".")
	build := exec.Command("go", args...)
	build.Dir = "."
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build pastured: %v\n%s", err, output)
	}
}

// runPasturedVersion runs `<binary> --version` and returns its stdout verbatim.
func runPasturedVersion(t *testing.T, binary string) string {
	t.Helper()
	command := exec.Command(binary, "--version")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run pastured --version: %v\n%s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("--version must report on stdout only, got stderr %q", stderr.String())
	}
	return stdout.String()
}

// TestUnstampedBuildReportsDevelMarker proves an ordinary `go build` (CI, dev
// checkouts) reports an honest development marker rather than a fabricated
// release tag. "devel" carries no "v" and no dotted triple, so a consumer
// scraping the line for a release tag finds nothing and cannot freeze a fiction
// into a compatibility floor.
func TestUnstampedBuildReportsDevelMarker(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pastured")
	buildPasturedBinary(t, binary, "")

	if got, want := runPasturedVersion(t, binary), "pastured devel\n"; got != want {
		t.Errorf("unstamped --version = %q, want %q", got, want)
	}
}

// TestStampedBuildReportsTheStampedVersion proves the release path: the value
// the release workflow, the Makefile and the Nix build pass as -X main.version
// is exactly what the daemon reports.
func TestStampedBuildReportsTheStampedVersion(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pastured")
	buildPasturedBinary(t, binary, "-X main.version=v1.2.3")

	if got, want := runPasturedVersion(t, binary), "pastured v1.2.3\n"; got != want {
		t.Errorf("stamped --version = %q, want %q", got, want)
	}
}

func TestRootCmdFlagRegistration(t *testing.T) {
	root := newRootCmd()

	requiredFlags := []string{
		"config",
		"db",
		"audit-trail",
		"slice-concurrency",
		"version",
	}
	for _, name := range requiredFlags {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("flag --%s not registered on root command", name)
		}
	}

	forbiddenFlags := []string{
		"namespace",
		"task-queue",
		"address",
		"audit-db-path",
		"idle-after-migrate",
	}
	for _, name := range forbiddenFlags {
		if root.PersistentFlags().Lookup(name) != nil {
			t.Errorf("retired flag --%s must not be registered on the DBOS daemon", name)
		}
	}
}

func TestRootCmdFlagDefaults(t *testing.T) {
	root := newRootCmd()
	flags := root.PersistentFlags()

	cases := []struct {
		flag string
		want string
	}{
		{"config", config.DefaultConfigPath()},
		{"db", ""},
		{"audit-trail", string(types.BackendSqlite)},
		{"slice-concurrency", "0"},
	}

	for _, tc := range cases {
		got := flags.Lookup(tc.flag).DefValue
		if got != tc.want {
			t.Errorf("flag --%s default = %q, want %q", tc.flag, got, tc.want)
		}
	}
}

func TestResolvePasturedConfigFromFile_DefaultsToSqlite(t *testing.T) {
	root := newRootCmd()
	if err := root.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	cfg, err := config.ResolvePasturedConfigFromFile(root, "")
	if err != nil {
		t.Fatalf("ResolvePasturedConfigFromFile: unexpected error: %v", err)
	}
	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendSqlite)
	}
}

func TestResolvePasturedConfig_AuditTrailEnvOverride(t *testing.T) {
	testutil.SetEnv(t, config.EnvAuditTrail, string(types.BackendMemory))

	root := newRootCmd()
	if err := root.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	cfg, err := config.ResolvePasturedConfigFromFile(root, "")
	if err != nil {
		t.Fatalf("ResolvePasturedConfigFromFile: unexpected error: %v", err)
	}
	if cfg.AuditTrail != types.BackendMemory {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendMemory)
	}
}

func TestResolvePasturedConfig_AuditTrailCLIOverridesEnv(t *testing.T) {
	testutil.SetEnv(t, config.EnvAuditTrail, string(types.BackendMemory))

	root := newRootCmd()
	if err := root.ParseFlags([]string{"--audit-trail", string(types.BackendSqlite)}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	cfg, err := config.ResolvePasturedConfigFromFile(root, "")
	if err != nil {
		t.Fatalf("ResolvePasturedConfigFromFile: unexpected error: %v", err)
	}
	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want %q (CLI should override env)", cfg.AuditTrail, types.BackendSqlite)
	}
}

func TestInitAuditTrail_Memory(t *testing.T) {
	cfg := config.PasturedConfig{AuditTrail: types.BackendMemory}
	trail, cache, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(memory): unexpected error: %v", err)
	}
	if trail == nil {
		t.Fatal("initAuditTrail(memory): trail is nil")
	}
	if closer != nil {
		t.Error("initAuditTrail(memory): closer should be nil for in-memory backend")
	}
	if cache == nil || cache.Len() != 0 {
		t.Fatalf("initAuditTrail(memory): cache len = %d, want 0", cache.Len())
	}
}

func TestInitAuditTrail_EmptyFallsBackToMemory(t *testing.T) {
	cfg := config.PasturedConfig{AuditTrail: ""}
	trail, cache, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(empty): unexpected error: %v", err)
	}
	if trail == nil {
		t.Fatal("initAuditTrail(empty): trail is nil")
	}
	if closer != nil {
		t.Error("initAuditTrail(empty): closer should be nil for memory backend")
	}
	if cache == nil {
		t.Fatal("initAuditTrail(empty): cache is nil")
	}
}

func TestInitAuditTrail_Sqlite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pasture.db")
	cfg := config.PasturedConfig{
		AuditTrail:  types.BackendSqlite,
		AuditDBPath: dbPath,
	}

	trail, cache, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(sqlite): %v", err)
	}
	if trail == nil {
		t.Fatal("initAuditTrail(sqlite): trail is nil")
	}
	if closer == nil {
		t.Fatal("initAuditTrail(sqlite): closer is nil for SQLite backend")
	}
	if cache == nil || cache.Len() != 15 {
		t.Fatalf("initAuditTrail(sqlite): cache len = %d, want 15", cache.Len())
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("SQLite database file not created at %q", dbPath)
	}
	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}
}

func TestInitAuditTrail_Sqlite_DefaultPath(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.SetEnv(t, "HOME", tmpDir)
	testutil.SetEnv(t, "PASTURE_DB_PATH", "")
	testutil.SetEnv(t, "XDG_DATA_HOME", "")

	cfg := config.PasturedConfig{
		AuditTrail:  types.BackendSqlite,
		AuditDBPath: "",
	}
	trail, cache, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(sqlite, default path): %v", err)
	}
	if trail == nil || closer == nil {
		t.Fatalf("trail nil=%t closer nil=%t, want both non-nil", trail == nil, closer == nil)
	}
	if cache == nil || cache.Len() != 15 {
		t.Fatalf("cache len = %d, want 15", cache.Len())
	}
	expectedPath := filepath.Join(tmpDir, ".local", "share", "pasture", "pasture.db")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("unified pasture.db not created at default path %q", expectedPath)
	}
	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}
}

func TestInitAuditTrail_UnknownBackend(t *testing.T) {
	cfg := config.PasturedConfig{AuditTrail: types.AuditTrailBackend("postgres")}
	_, _, _, err := initAuditTrail(cfg)
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"postgres", "memory", "sqlite"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q does not mention %q", msg, want)
		}
	}
}

func TestResolveDBPath_Default(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.SetEnv(t, "HOME", tmpDir)
	testutil.SetEnv(t, "PASTURE_DB_PATH", "")
	testutil.SetEnv(t, "XDG_DATA_HOME", "")

	root := newRootCmd()
	if err := root.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	got, err := resolveDBPath(root)
	if err != nil {
		t.Fatalf("resolveDBPath: %v", err)
	}
	if want := tasks.DefaultDBPath(); got != want {
		t.Errorf("path = %q, want default %q", got, want)
	}
}

func TestResolveDBPath_Env(t *testing.T) {
	testutil.SetEnv(t, "PASTURE_DB_PATH", "/env/pasture.db")

	root := newRootCmd()
	if err := root.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	got, err := resolveDBPath(root)
	if err != nil {
		t.Fatalf("resolveDBPath: %v", err)
	}
	if got != "/env/pasture.db" {
		t.Errorf("path = %q, want env path", got)
	}
}

func TestResolveDBPath_DBFlagBeatsEnv(t *testing.T) {
	testutil.SetEnv(t, "PASTURE_DB_PATH", "/env/pasture.db")

	root := newRootCmd()
	if err := root.ParseFlags([]string{"--db", "/cli/pasture.db"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	got, err := resolveDBPath(root)
	if err != nil {
		t.Fatalf("resolveDBPath: %v", err)
	}
	if got != "/cli/pasture.db" {
		t.Errorf("path = %q, want CLI path", got)
	}
}

func TestNewEngineConfigWiresRecoveryConstantsAndHooks(t *testing.T) {
	trail := audit.NewInMemoryAuditTrail()
	hooksMgr := hooks.NewManager()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := newEngineConfig("/tmp/pasture.db", 3, trail, nil, hooksMgr, logger)

	if cfg.ExecutorID != engine.DefaultExecutorID {
		t.Errorf("ExecutorID = %q, want engine.DefaultExecutorID %q", cfg.ExecutorID, engine.DefaultExecutorID)
	}
	if cfg.AppName != engine.DefaultAppName {
		t.Errorf("AppName = %q, want engine.DefaultAppName %q", cfg.AppName, engine.DefaultAppName)
	}
	if cfg.ApplicationVersion != engine.DefaultApplicationVersion {
		t.Errorf("ApplicationVersion = %q, want engine.DefaultApplicationVersion %q", cfg.ApplicationVersion, engine.DefaultApplicationVersion)
	}
	if cfg.HooksMgr != hooksMgr {
		t.Fatal("HooksMgr was not wired into engine.Config")
	}
	if cfg.SliceConcurrency != 3 {
		t.Errorf("SliceConcurrency = %d, want 3", cfg.SliceConcurrency)
	}
}

func TestBuildDaemonRuntime_SqliteWiresHooksMgr(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.PasturedConfig{
		AuditTrail:  types.BackendSqlite,
		AuditDBPath: filepath.Join(t.TempDir(), "pasture.db"),
	}

	rt, err := buildDaemonRuntime(context.Background(), cfg, 2, logger)
	if err != nil {
		t.Fatalf("buildDaemonRuntime: %v", err)
	}
	defer rt.Close(logger)

	if rt.HooksMgr == nil {
		t.Fatal("runtime HooksMgr is nil")
	}
	if rt.RegisteredRecorders != 1 {
		t.Errorf("RegisteredRecorders = %d, want 1", rt.RegisteredRecorders)
	}
	if rt.SliceConcurrency != 2 {
		t.Errorf("SliceConcurrency = %d, want 2", rt.SliceConcurrency)
	}
}

func TestRootCmdHelp(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	_ = root.Execute()

	help := buf.String()
	for _, keyword := range []string{"DBOS", "db", "audit-trail", "slice-concurrency"} {
		if !strings.Contains(help, keyword) {
			t.Errorf("help output does not mention %q:\n%s", keyword, help)
		}
	}
	for _, retired := range []string{"namespace", "task-queue", "address", "audit-db-path"} {
		if strings.Contains(help, retired) {
			t.Errorf("help output still mentions retired flag %q:\n%s", retired, help)
		}
	}
}

// TestDaemonRuntimeClose_ReportsAWorkerThatOutlivedTheStopBudget pins the
// daemon's stop contract: when work is still running as the daemon stops, the
// runtime's stop error reaches the daemon's own Close, names the part that was
// still busy, and maps to the workflow exit code the package comment
// documents. Without this the daemon would exit 0 on a stop that cut work off.
func TestDaemonRuntimeClose_ReportsAWorkerThatOutlivedTheStopBudget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	entered := make(chan struct{})
	released := make(chan struct{})
	var holdOnce sync.Once
	defer close(released)

	engCfg := newEngineConfig(testutil.GoldenUnifiedDBPath(t), 2, audit.NewInMemoryAuditTrail(), nil, hooks.NewManager(), logger)
	engCfg.OnTransition = func(context.Context, string, *protocol.TransitionRecord, string) error {
		holdOnce.Do(func() {
			close(entered)
			select {
			case <-released:
			case <-time.After(30 * time.Second):
				// A ceiling only: the held step unwinds even if the test
				// fails before it releases.
			}
		})
		return nil
	}

	eng, err := engine.New(context.Background(), engCfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	rt := &daemonRuntime{Engine: eng}
	if err := eng.Launch(); err != nil {
		rt.Close(logger)
		t.Fatalf("engine.Launch: %v", err)
	}

	if _, err := dbos.RunWorkflow(eng.DBOS(), eng.EpochWorkflow,
		engine.EpochInput{
			EpochId: "epoch-stop-budget",
			Advances: []engine.AdvanceStep{
				{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch", ConditionMet: "classified"},
			},
		},
		dbos.WithWorkflowID("epoch-stop-budget")); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("no work reached its durable step, so nothing was in flight when the daemon stopped")
	}

	closeErr := rt.Close(logger)
	if closeErr == nil {
		t.Fatal("daemonRuntime.Close with work still running = nil, want the incomplete-stop error")
	}

	var incomplete *engine.ShutdownIncompleteError
	if !errors.As(closeErr, &incomplete) {
		t.Fatalf("Close error %v does not carry the parts that were still running", closeErr)
	}
	if !slices.Contains(incomplete.Pending, engine.ShutdownComponentWorkflows) {
		t.Errorf("Pending = %v, want it to name %q", incomplete.Pending, engine.ShutdownComponentWorkflows)
	}
	if got, want := pasterrors.ExitCode(closeErr), 3; got != want {
		t.Errorf("exit code for an incomplete stop = %d, want %d", got, want)
	}
}

// TestPasturedExitCodeComesFromTheErrorCategory proves the real binary chooses
// its exit code from the error's category rather than reporting 1 for
// everything: an unusable database path is a connection failure and must exit
// 2. The incomplete-stop code (3) rides the same single mapping, which
// TestDaemonRuntimeClose_ReportsAWorkerThatOutlivedTheStopBudget pins.
func TestPasturedExitCodeComesFromTheErrorCategory(t *testing.T) {
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "pastured")
	buildPasturedBinary(t, binary, "")

	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte("audit_trail: sqlite\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// A plain file where the daemon needs a directory: creating the database's
	// parent folder then fails, which is the connection failure under test.
	blocker := filepath.Join(tmp, "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write the blocking file: %v", err)
	}
	unusable := filepath.Join(blocker, "child", "pasture.db")

	command := exec.Command(binary, "--config", configPath, "--db", unusable)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("pastured with an unusable database path exited with %v, want a non-zero exit", err)
	}
	if got, want := exitErr.ExitCode(), 2; got != want {
		t.Errorf("exit code = %d, want %d (connection):\n%s", got, want, stderr.String())
	}
}

// startPastured starts the daemon and returns the running command together
// with a channel of its log lines. The caller reads the channel to wait on a
// condition; the channel closes when the daemon's output ends.
func startPastured(t *testing.T, tmp string) (*exec.Cmd, <-chan string) {
	t.Helper()
	binary := filepath.Join(tmp, "pastured")
	buildPasturedBinary(t, binary, "")

	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte("audit_trail: sqlite\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	command := exec.Command(binary, "--config", configPath, "--db", filepath.Join(tmp, "pasture.db"))
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start pastured: %v", err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })

	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	return command, lines
}

// awaitLogLine reads log lines until one carries want, and fails the test if
// the daemon's output ends or the ceiling passes first.
func awaitLogLine(t *testing.T, lines <-chan string, want string, ceiling time.Duration) {
	t.Helper()
	deadline := time.After(ceiling)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("pastured stopped producing output before it reported %q", want)
			}
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("pastured did not report %q within %v", want, ceiling)
		}
	}
}

// TestPasturedStopsCleanlyOnSignal pins the orderly path of the daemon's stop
// contract from the outside: a real daemon that receives SIGTERM stops its
// engine, reports a clean stop, and exits 0.
//
// COVERAGE LIMIT, recorded rather than papered over: this test and
// TestDaemonRuntimeClose_ReportsAWorkerThatOutlivedTheStopBudget together
// cover both outcomes of the daemon's stop, but not the one line that joins
// them — run's decision to return the stop error instead of logging a clean
// stop. Forcing a real daemon to hold work past its budget needs a way to
// stall work from outside the process, and adding one would be a second code
// path bought only for the test. The two outcomes either side of that line
// are pinned, and the line itself is one `if err != nil { return err }`.
func TestPasturedStopsCleanlyOnSignal(t *testing.T) {
	command, lines := startPastured(t, t.TempDir())
	awaitLogLine(t, lines, "waiting for shutdown", 60*time.Second)

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal pastured: %v", err)
	}

	sawCleanStop := false
	for line := range lines {
		if strings.Contains(line, "pastured stopped cleanly") {
			sawCleanStop = true
		}
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("pastured exited with %v, want a clean exit 0 after SIGTERM", err)
	}
	if !sawCleanStop {
		t.Error("pastured exited 0 but never reported a clean stop")
	}
}

// TestPasturedAnswersASignalWhileItIsStillStarting pins the startup window: a
// stop signal that arrives after the daemon reports that it is starting, but
// before its engine is ready, must end the process in an orderly way.
//
// The daemon listens for the signals before it logs that line, so the line is
// the earliest point a test can pin. Startup is short here (an empty database
// on a local disk), so the signal may land either inside the window or just
// after the engine is ready. Both are stops the operator asked for, so both
// end with exit 0 and a clean-stop line. The failure this test exists to
// catch is the third ending: a process killed by the default action, which
// leaves the operator with a daemon that answers to nothing but a kill.
func TestPasturedAnswersASignalWhileItIsStillStarting(t *testing.T) {
	command, lines := startPastured(t, t.TempDir())
	awaitLogLine(t, lines, "pastured starting", 60*time.Second)

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal pastured: %v", err)
	}

	var output strings.Builder
	for line := range lines {
		output.WriteString(line)
		output.WriteString("\n")
	}

	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	var waitErr error
	select {
	case waitErr = <-exited:
	case <-time.After(60 * time.Second):
		t.Fatal("pastured did not exit after a stop signal sent while it was starting")
	}

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() < 0 {
			t.Fatalf("pastured was killed by a signal instead of ending itself:\n%s", output.String())
		}
		t.Fatalf("pastured exited with %v, want exit 0 for a stop the operator asked for:\n%s", waitErr, output.String())
	}
	if !strings.Contains(output.String(), "stopped cleanly") {
		t.Errorf("pastured exited 0 without reporting a clean stop:\n%s", output.String())
	}
}

// TestLaunchFailureKeepsItsOwnExitCode pins which fault decides the exit code
// when a launch fails AND the half-started engine then does not stop cleanly.
// The launch failure is what the operator must act on, so an uncategorised
// launch failure must not borrow the stop failure's code.
func TestLaunchFailureKeepsItsOwnExitCode(t *testing.T) {
	t.Parallel()

	plainLaunch := errors.New("the engine could not replay unfinished work")
	stopFailure := &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     "The durable engine didn't finish stopping in the time it was given.",
	}

	joined := launchFailure(plainLaunch, stopFailure)
	if got, want := pasterrors.ExitCode(joined), 1; got != want {
		t.Errorf("exit code = %d, want %d (the launch failure's own code, not the stop failure's)", got, want)
	}
	if !errors.Is(joined, plainLaunch) {
		t.Error("the launch failure is no longer reachable in the error chain")
	}
	for _, want := range []string{plainLaunch.Error(), stopFailure.What} {
		if !strings.Contains(joined.Error(), want) {
			t.Errorf("joined message does not carry %q:\n%s", want, joined.Error())
		}
	}

	if got := launchFailure(nil, stopFailure); got != error(stopFailure) {
		t.Errorf("with no launch failure the stop failure must be returned as it is, got %v", got)
	}
	if got := launchFailure(plainLaunch, nil); got != plainLaunch {
		t.Errorf("with a clean stop the launch failure must be returned as it is, got %v", got)
	}
}

// TestRunKeepsTheSignalOffTheEngineContext guards two wirings a compiler
// cannot check and a test cannot easily observe from outside.
//
//  1. run builds a stop-signal context and ENDS ITS WAIT on it, so a signal
//     that arrives while startup blocks is answered.
//  2. the engine is built under engineLifetimeContext, NOT under the signal
//     context. Handing the signal context straight to the startup work
//     compiles, passes every other test that does not exercise recovery, and
//     silently turns a stop signal into a cancellation of the epoch in
//     flight — work the recovery sweep then skips for good.
func TestRunKeepsTheSignalOffTheEngineContext(t *testing.T) {
	t.Parallel()

	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var runBody *ast.BlockStmt
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "run" && fn.Recv == nil {
			runBody = fn.Body
		}
	}
	if runBody == nil {
		t.Fatal("main.go no longer declares run; this guard needs updating with it")
	}

	signalContextName := ""
	startupContextArg := ""
	waitsOnTheSignal := false
	ast.Inspect(runBody, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch selectorName(call.Fun) {
		case "signal.NotifyContext":
			if assign, ok := parentAssign(runBody, call); ok && len(assign.Lhs) > 0 {
				if ident, ok := assign.Lhs[0].(*ast.Ident); ok {
					signalContextName = ident.Name
				}
			}
		case "buildDaemonRuntime":
			if len(call.Args) > 0 {
				startupContextArg = describeContextArgument(call.Args[0])
			}
		}
		return true
	})
	ast.Inspect(runBody, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && signalContextName != "" && selectorName(call.Fun) == signalContextName+".Done" {
			waitsOnTheSignal = true
		}
		return true
	})

	if signalContextName == "" {
		t.Fatal("run no longer builds a context from signal.NotifyContext, so a stop signal cannot end a blocked startup")
	}
	if !waitsOnTheSignal {
		t.Fatalf("run never waits on %s.Done(), so a signal would only be buffered while startup blocks", signalContextName)
	}
	if want := "engineLifetimeContext(" + signalContextName + ")"; startupContextArg != want {
		t.Fatalf("run passes %q to buildDaemonRuntime, want %q; the engine must not live under the signal context, or a stop cancels the epoch in flight instead of leaving it to be finished",
			startupContextArg, want)
	}
}

// describeContextArgument renders the context argument of a call as source-like
// text, so the guard can report exactly what it found.
func describeContextArgument(arg ast.Expr) string {
	switch value := arg.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.CallExpr:
		inner := "..."
		if len(value.Args) == 1 {
			if ident, ok := value.Args[0].(*ast.Ident); ok {
				inner = ident.Name
			}
		}
		return selectorName(value.Fun) + "(" + inner + ")"
	}
	return "<an expression this guard cannot name>"
}

// selectorName renders a call target as "package.Function" or "Function".
func selectorName(fun ast.Expr) string {
	switch target := fun.(type) {
	case *ast.Ident:
		return target.Name
	case *ast.SelectorExpr:
		if pkg, ok := target.X.(*ast.Ident); ok {
			return pkg.Name + "." + target.Sel.Name
		}
		return target.Sel.Name
	}
	return ""
}

// parentAssign finds the assignment whose right-hand side is call.
func parentAssign(body *ast.BlockStmt, call *ast.CallExpr) (*ast.AssignStmt, bool) {
	var found *ast.AssignStmt
	ast.Inspect(body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, rhs := range assign.Rhs {
			if rhs == ast.Expr(call) {
				found = assign
				return false
			}
		}
		return true
	})
	return found, found != nil
}

// readWorkflowStatus reads one workflow's recorded status straight from the
// database, without a running engine. The status is what decides whether the
// next start finishes the work or skips it for good, so the test reads the
// stored row rather than any in-memory view of it.
func readWorkflowStatus(t *testing.T, dbPath, workflowID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open the database to read the workflow row: %v", err)
	}
	defer db.Close()

	var status string
	if err := db.QueryRow("SELECT status FROM workflow_status WHERE workflow_uuid = ?", workflowID).Scan(&status); err != nil {
		t.Fatalf("read the status of workflow %q: %v", workflowID, err)
	}
	return status
}

// TestAStopSignalLeavesTheWorkForTheNextStartToFinish is the contract that
// makes every "nothing is lost" statement in this daemon true.
//
// A stop signal must end the daemon, not the epoch. The durable runtime marks
// work as cancelled when its context is cancelled for any reason OTHER than
// the runtime's own stop, and the recovery sweep skips cancelled work for
// good — so a signal wired into the engine's parent context would quietly
// destroy an epoch that was in flight. This test delivers the signal exactly
// as run does, then proves both halves: the work is not written as cancelled,
// and a new engine on the same database finishes it.
func TestAStopSignalLeavesTheWorkForTheNextStartToFinish(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dbPath := testutil.GoldenUnifiedDBPath(t)
	const epochID = "epoch-stop-then-recover"

	plan := []engine.AdvanceStep{
		{ToPhase: protocol.PhaseElicit, TriggeredBy: "epoch", ConditionMet: "classified"},
		{ToPhase: protocol.PhasePropose, TriggeredBy: "architect", ConditionMet: "elicited"},
	}

	// The first engine holds its first transition inside the durable step, so
	// there is real work in flight when the signal arrives.
	entered := make(chan struct{})
	released := make(chan struct{})
	var holdOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	defer release()

	firstCfg := newEngineConfig(dbPath, 2, audit.NewInMemoryAuditTrail(), nil, hooks.NewManager(), logger)
	firstCfg.OnTransition = func(context.Context, string, *protocol.TransitionRecord, string) error {
		holdOnce.Do(func() {
			close(entered)
			select {
			case <-released:
			case <-time.After(60 * time.Second):
			}
		})
		return nil
	}

	// The signal, delivered the way run delivers it: it cancels the startup
	// context, and the engine lives under engineLifetimeContext of that.
	startupCtx, signalArrives := context.WithCancel(context.Background())
	defer signalArrives()

	first, err := engine.New(engineLifetimeContext(startupCtx), firstCfg)
	if err != nil {
		t.Fatalf("engine.New for the first daemon: %v", err)
	}
	if err := first.Launch(); err != nil {
		first.Shutdown(perComponentShutdownTimeout)
		t.Fatalf("engine.Launch for the first daemon: %v", err)
	}

	if _, err := dbos.RunWorkflow(first.DBOS(), first.EpochWorkflow,
		engine.EpochInput{EpochId: epochID, Advances: plan},
		dbos.WithWorkflowID(epochID)); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(60 * time.Second):
		t.Fatal("no work reached its durable step, so nothing was in flight when the signal arrived")
	}

	signalArrives()
	// A short budget: the point is a stop that ends while work is still
	// running, which is the case that used to destroy the work.
	if stopErr := first.Shutdown(2 * time.Second); stopErr == nil {
		t.Error("the first engine stopped cleanly although work was still running; the test no longer covers the case it was written for")
	}
	release()

	if status := readWorkflowStatus(t, dbPath, epochID); status == string(dbos.WorkflowStatusCancelled) {
		t.Fatalf("the epoch was written as %q after a stop signal; the next start skips cancelled work, so the epoch is lost", status)
	}

	// A new daemon on the same database must finish what the first one left.
	finished := make(chan struct{})
	var finishOnce sync.Once
	secondCfg := newEngineConfig(dbPath, 2, audit.NewInMemoryAuditTrail(), nil, hooks.NewManager(), logger)
	secondCfg.OnTransition = func(_ context.Context, _ string, rec *protocol.TransitionRecord, _ string) error {
		if rec.ToPhase == protocol.PhasePropose {
			finishOnce.Do(func() { close(finished) })
		}
		return nil
	}

	second, err := engine.New(context.Background(), secondCfg)
	if err != nil {
		t.Fatalf("engine.New for the second daemon: %v", err)
	}
	t.Cleanup(func() { second.Shutdown(perComponentShutdownTimeout) })
	if err := second.Launch(); err != nil {
		t.Fatalf("engine.Launch for the second daemon: %v", err)
	}

	select {
	case <-finished:
	case <-time.After(120 * time.Second):
		t.Fatalf("the second daemon did not finish the epoch the first one left; its recorded status is %q",
			readWorkflowStatus(t, dbPath, epochID))
	}
}

// TestEngineLifetimeContextDropsTheSignal states the rule the test above
// proves the consequence of: the context handed to the engine must not be
// cancelled by the stop signal.
func TestEngineLifetimeContextDropsTheSignal(t *testing.T) {
	t.Parallel()

	startupCtx, signalArrives := context.WithCancel(context.Background())
	engineCtx := engineLifetimeContext(startupCtx)
	signalArrives()

	if startupCtx.Err() == nil {
		t.Fatal("the startup context was not cancelled; this test cannot prove anything")
	}
	if err := engineCtx.Err(); err != nil {
		t.Errorf("the engine's context was cancelled by the stop signal (%v); a stop would cancel the epoch in flight instead of leaving it to be finished", err)
	}
}

// TestAbandonedStartupEndsWithoutFault pins the two ordinary endings of a
// startup the operator stopped: whatever the startup step reports afterwards,
// a stop the operator asked for is not a fault, and the half-built daemon is
// closed rather than left open.
func TestAbandonedStartupEndsWithoutFault(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("the abandoned step ended with a failure", func(t *testing.T) {
		built := make(chan startupOutcome, 1)
		built <- startupOutcome{err: errors.New("the database could not be opened")}
		if err := abandonStartup(logger, built); err != nil {
			t.Errorf("abandonStartup = %v, want nil: the operator asked for the stop", err)
		}
	})

	t.Run("the abandoned step produced a daemon that must be closed", func(t *testing.T) {
		cfg := newEngineConfig(testutil.GoldenUnifiedDBPath(t), 2, audit.NewInMemoryAuditTrail(), nil, hooks.NewManager(), logger)
		eng, err := engine.New(context.Background(), cfg)
		if err != nil {
			t.Fatalf("engine.New: %v", err)
		}
		built := make(chan startupOutcome, 1)
		built <- startupOutcome{runtime: &daemonRuntime{Engine: eng}}

		if err := abandonStartup(logger, built); err != nil {
			t.Errorf("abandonStartup = %v, want nil for a daemon that was never launched", err)
		}
		if err := eng.DB().Ping(); err == nil {
			t.Error("the abandoned daemon was left with an open database handle")
		}
	})
}
