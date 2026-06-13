package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

func TestVersionConstant(t *testing.T) {
	if version == "" {
		t.Fatal("version constant must not be empty")
	}
	if !strings.HasPrefix(version, "v") {
		t.Errorf("version %q should start with 'v'", version)
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
	t.Setenv(config.EnvAuditTrail, string(types.BackendMemory))

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
	t.Setenv(config.EnvAuditTrail, string(types.BackendMemory))

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
	t.Setenv("HOME", tmpDir)
	t.Setenv("PASTURE_DB_PATH", "")
	t.Setenv("XDG_DATA_HOME", "")

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
	t.Setenv("HOME", tmpDir)
	t.Setenv("PASTURE_DB_PATH", "")
	t.Setenv("XDG_DATA_HOME", "")

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
	t.Setenv("PASTURE_DB_PATH", "/env/pasture.db")

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
	t.Setenv("PASTURE_DB_PATH", "/env/pasture.db")

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
