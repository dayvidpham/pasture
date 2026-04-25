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
//
// PROPOSAL-2 §7.1: --db is the canonical flag for the unified pasture
// database; --audit-db-path is preserved as a deprecated alias for
// backwards compatibility with pre-PROPOSAL-2 deployments.
func TestRootCmdFlagRegistration(t *testing.T) {
	root := newRootCmd()

	requiredFlags := []string{
		"config",
		"namespace",
		"task-queue",
		"address",
		"audit-trail",
		"db",            // canonical (PROPOSAL-2 §7.1)
		"audit-db-path", // deprecated alias
		"idle-after-migrate",
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
		{"db", ""},
		{"audit-db-path", ""},
		{"idle-after-migrate", "0s"},
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

	cfg, err := config.ResolvePasturedConfigFromFile(root, "")
	if err != nil {
		t.Fatalf("ResolvePasturedConfigFromFile: unexpected error: %v", err)
	}
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

	cfg, err := config.ResolvePasturedConfigFromFile(root, "")
	if err != nil {
		t.Fatalf("ResolvePasturedConfigFromFile: unexpected error: %v", err)
	}
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

	cfg, err := config.ResolvePasturedConfigFromFile(root, "")
	if err != nil {
		t.Fatalf("ResolvePasturedConfigFromFile: unexpected error: %v", err)
	}
	if cfg.Connection.Namespace != "cli-ns" {
		t.Errorf("Namespace = %q, want %q (CLI should override env)", cfg.Connection.Namespace, "cli-ns")
	}
}

// ─── initAuditTrail ────────────────────────────────────────────────────────────

// TestInitAuditTrail_Memory verifies that the memory backend is returned for
// BackendMemory and that the closer is nil (no resource to release). The
// well-known cache is empty for the in-memory backend (no Provenance subsystem
// to mint AgentIDs against).
func TestInitAuditTrail_Memory(t *testing.T) {
	cfg := config.PasturedConfig{
		AuditTrail: types.BackendMemory,
	}
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
	if cache == nil {
		t.Fatal("initAuditTrail(memory): cache is nil; expected an empty cache (defensive non-nil)")
	}
	if cache.Len() != 0 {
		t.Errorf("initAuditTrail(memory): cache has %d entries, want 0 (no Provenance subsystem)", cache.Len())
	}
}

// TestInitAuditTrail_EmptyFallsBackToMemory verifies that an empty string
// backend (e.g. unset env var) falls back to the in-memory trail.
func TestInitAuditTrail_EmptyFallsBackToMemory(t *testing.T) {
	cfg := config.PasturedConfig{
		AuditTrail: "",
	}
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
		t.Fatal("initAuditTrail(empty): cache is nil; expected an empty cache")
	}
}

// TestInitAuditTrail_Sqlite verifies that the SQLite backend creates a database
// file at the specified path, returns a valid closer, and populates the
// well-known agent cache with all 15 entries (S7 Scenario 14 round 1).
func TestInitAuditTrail_Sqlite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/audit.db"

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
	if cache == nil {
		t.Fatal("initAuditTrail(sqlite): cache is nil; expected populated cache")
	}
	if got, want := cache.Len(), 15; got != want {
		t.Errorf("initAuditTrail(sqlite): cache has %d entries, want %d", got, want)
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
// the unified default ~/.local/share/pasture/pasture.db path is used
// (PROPOSAL-2 §7.1). Pre-PROPOSAL-2 the daemon defaulted to "audit.db" and
// `pasture` defaulted to "provenance.db"; the unified default collapses both
// to the single "pasture.db" file so OpenTaskTracker, OpenTracker, and the
// daemon's audit handle all land on the same on-disk file.
func TestInitAuditTrail_Sqlite_DefaultPath(t *testing.T) {
	tmpDir := t.TempDir()
	// Override HOME and unset PASTURE_DB_PATH / XDG_DATA_HOME so the default
	// path resolves into our temp dir's $HOME-based fallback.
	t.Setenv("HOME", tmpDir)
	t.Setenv("PASTURE_DB_PATH", "")
	t.Setenv("XDG_DATA_HOME", "")

	cfg := config.PasturedConfig{
		AuditTrail:  types.BackendSqlite,
		AuditDBPath: "", // empty → use tasks.DefaultDBPath()
	}
	trail, cache, closer, err := initAuditTrail(cfg)
	if err != nil {
		t.Fatalf("initAuditTrail(sqlite, default path): %v", err)
	}
	if trail == nil {
		t.Fatal("trail is nil")
	}
	if closer == nil {
		t.Fatal("closer is nil")
	}
	if cache == nil || cache.Len() != 15 {
		t.Errorf("cache is %v with len=%d; want non-nil with 15 entries", cache, cache.Len())
	}
	// Verify the unified pasture.db file was created inside our temp HOME.
	// PROPOSAL-2 §7.1 binds the filename to pasture.db.
	expectedPath := tmpDir + "/.local/share/pasture/pasture.db"
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("unified pasture.db not created at default path %q", expectedPath)
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
