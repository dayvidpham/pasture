package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/types"
)

// ─── --version flag ────────────────────────────────────────────────────────────

// TestVersionFlag verifies that pastured --version prints the version string
// and exits 0. Because --version calls os.Exit(0) inside PersistentPreRunE we
// cannot call it through Execute(); we verify the flag is registered and the
// version constant is well-formed instead.
func TestVersionConstant(t *testing.T) {
	if version == "" {
		t.Fatal("version constant must not be empty")
	}
	if !strings.HasPrefix(version, "v") {
		t.Errorf("version %q should start with 'v'", version)
	}
}

// TestRootCmdFlagRegistration verifies that all required CLI flags are
// registered on the root command. This exercises the production code path
// (newRootCmd) and catches typos or missing flag registrations without
// requiring a live Temporal server.
func TestRootCmdFlagRegistration(t *testing.T) {
	root := newRootCmd()

	requiredFlags := []string{
		"config",
		"namespace",
		"task-queue",
		"address",
		"audit-trail",
		"audit-db-path",
		"version",
	}

	for _, name := range requiredFlags {
		flag := root.PersistentFlags().Lookup(name)
		if flag == nil {
			t.Errorf("flag --%s not registered on root command", name)
		}
	}
}

// TestRootCmdFlagDefaults verifies that the default values for each flag match
// the documented defaults.
func TestRootCmdFlagDefaults(t *testing.T) {
	root := newRootCmd()
	flags := root.PersistentFlags()

	cases := []struct {
		flag string
		want string
	}{
		{"namespace", "default"},
		{"task-queue", "pasture"},
		{"address", "localhost:7233"},
		{"audit-trail", string(types.BackendMemory)},
		{"audit-db-path", ""},
		{"config", config.DefaultConfigPath()},
	}

	for _, tc := range cases {
		got := flags.Lookup(tc.flag).DefValue
		if got != tc.want {
			t.Errorf("flag --%s default = %q, want %q", tc.flag, got, tc.want)
		}
	}
}

// ─── Config resolution ─────────────────────────────────────────────────────────

// TestResolvePasturedConfigFromFile verifies that config.ResolvePasturedConfigFromFile
// applies CLI > env > YAML > defaults priority. We test the happy path (defaults
// when no flag/env/file is set) to verify the function is callable from main.
func TestResolvePasturedConfigFromFile(t *testing.T) {
	root := newRootCmd()
	// Parse with no arguments — all defaults should apply.
	root.SetArgs([]string{})
	// We don't Execute() because that would attempt a Temporal connection.
	// Instead we call ParseFlags to populate the flag state, then resolve.
	if err := root.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	cfg := config.ResolvePasturedConfigFromFile(root, "")
	if cfg.Connection.Namespace != "default" {
		t.Errorf("Namespace = %q, want %q", cfg.Connection.Namespace, "default")
	}
	if cfg.Connection.TaskQueue != "pasture" {
		t.Errorf("TaskQueue = %q, want %q", cfg.Connection.TaskQueue, "pasture")
	}
	if cfg.Connection.ServerAddress != "localhost:7233" {
		t.Errorf("ServerAddress = %q, want %q", cfg.Connection.ServerAddress, "localhost:7233")
	}
}

// TestResolvePasturedConfig_EnvOverride verifies that environment variables
// override built-in defaults.
func TestResolvePasturedConfig_EnvOverride(t *testing.T) {
	t.Setenv("TEMPORAL_NAMESPACE", "test-ns")
	t.Setenv("TEMPORAL_TASK_QUEUE", "test-queue")
	t.Setenv("TEMPORAL_ADDRESS", "testhost:7233")

	root := newRootCmd()
	if err := root.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	cfg := config.ResolvePasturedConfigFromFile(root, "")
	if cfg.Connection.Namespace != "test-ns" {
		t.Errorf("Namespace = %q, want %q", cfg.Connection.Namespace, "test-ns")
	}
	if cfg.Connection.TaskQueue != "test-queue" {
		t.Errorf("TaskQueue = %q, want %q", cfg.Connection.TaskQueue, "test-queue")
	}
	if cfg.Connection.ServerAddress != "testhost:7233" {
		t.Errorf("ServerAddress = %q, want %q", cfg.Connection.ServerAddress, "testhost:7233")
	}
}

// TestResolvePasturedConfig_CLIOverridesEnv verifies that explicit CLI flags
// take precedence over environment variables.
func TestResolvePasturedConfig_CLIOverridesEnv(t *testing.T) {
	t.Setenv("TEMPORAL_NAMESPACE", "env-ns")

	root := newRootCmd()
	// Simulate user passing --namespace on the CLI.
	if err := root.ParseFlags([]string{"--namespace", "cli-ns"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	cfg := config.ResolvePasturedConfigFromFile(root, "")
	if cfg.Connection.Namespace != "cli-ns" {
		t.Errorf("Namespace = %q, want %q (CLI should override env)", cfg.Connection.Namespace, "cli-ns")
	}
}

// ─── initAuditTrail ────────────────────────────────────────────────────────────

// TestInitAuditTrail_Memory verifies that the memory backend is returned for
// BackendMemory and that the closer is nil (no resource to release).
func TestInitAuditTrail_Memory(t *testing.T) {
	cfg := config.PasturedConfig{
		AuditTrail: types.BackendMemory,
	}
	trail, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(memory): unexpected error: %v", err)
	}
	if trail == nil {
		t.Fatal("initAuditTrail(memory): trail is nil")
	}
	if closer != nil {
		t.Error("initAuditTrail(memory): closer should be nil for in-memory backend")
	}
}

// TestInitAuditTrail_EmptyFallsBackToMemory verifies that an empty string
// backend (e.g. unset env var) falls back to the in-memory trail.
func TestInitAuditTrail_EmptyFallsBackToMemory(t *testing.T) {
	cfg := config.PasturedConfig{
		AuditTrail: "",
	}
	trail, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(empty): unexpected error: %v", err)
	}
	if trail == nil {
		t.Fatal("initAuditTrail(empty): trail is nil")
	}
	if closer != nil {
		t.Error("initAuditTrail(empty): closer should be nil for memory backend")
	}
}

// TestInitAuditTrail_Sqlite verifies that the SQLite backend creates a database
// file at the specified path and returns a valid closer.
func TestInitAuditTrail_Sqlite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/audit.db"

	cfg := config.PasturedConfig{
		AuditTrail:  types.BackendSqlite,
		AuditDBPath: dbPath,
	}
	trail, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(sqlite): %v", err)
	}
	if trail == nil {
		t.Fatal("initAuditTrail(sqlite): trail is nil")
	}
	if closer == nil {
		t.Fatal("initAuditTrail(sqlite): closer is nil for SQLite backend")
	}
	// Verify the database file was created.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("SQLite database file not created at %q", dbPath)
	}
	// Clean up.
	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}
}

// TestInitAuditTrail_Sqlite_DefaultPath verifies that when AuditDBPath is empty
// the default ~/.local/share/pasture/audit.db path is used (we verify the trail
// is non-nil and the path contains "pasture/audit.db" by using env manipulation).
func TestInitAuditTrail_Sqlite_DefaultPath(t *testing.T) {
	tmpDir := t.TempDir()
	// Override HOME so the default path resolves inside our temp dir.
	t.Setenv("HOME", tmpDir)

	cfg := config.PasturedConfig{
		AuditTrail:  types.BackendSqlite,
		AuditDBPath: "", // empty → use default
	}
	trail, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(sqlite, default path): %v", err)
	}
	if trail == nil {
		t.Fatal("trail is nil")
	}
	if closer == nil {
		t.Fatal("closer is nil")
	}
	// Verify the default db file was created inside our temp HOME.
	expectedPath := tmpDir + "/.local/share/pasture/audit.db"
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("SQLite database file not created at default path %q", expectedPath)
	}
	if err := closer(); err != nil {
		t.Errorf("closer: %v", err)
	}
}

// TestInitAuditTrail_UnknownBackend verifies that an unrecognised backend name
// returns a descriptive error.
func TestInitAuditTrail_UnknownBackend(t *testing.T) {
	cfg := config.PasturedConfig{
		AuditTrail: types.AuditTrailBackend("postgres"),
	}
	_, _, err := initAuditTrail(cfg)
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

// ─── newRootCmd output ─────────────────────────────────────────────────────────

// TestRootCmdHelp verifies that the help output is well-formed and mentions key
// flags. This is a smoke test on the Cobra configuration.
func TestRootCmdHelp(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	// --help causes Cobra to print help and return a nil error (cobra exits 0).
	_ = root.Execute()

	help := buf.String()
	for _, keyword := range []string{"namespace", "task-queue", "address", "audit-trail"} {
		if !strings.Contains(help, keyword) {
			t.Errorf("help output does not mention flag %q:\n%s", keyword, help)
		}
	}
}
