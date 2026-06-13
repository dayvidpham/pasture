package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/types"
)

// newTestCmd builds a minimal cobra.Command with the standard connection flags
// registered, matching the flags that ResolveConnectionConfig expects.
func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("namespace", "", "Temporal namespace")
	cmd.PersistentFlags().String("task-queue", "", "Temporal task queue")
	cmd.PersistentFlags().String("address", "", "Temporal server address")
	return cmd
}

// newPasturedTestCmd adds pastured-specific flags.
func newPasturedTestCmd() *cobra.Command {
	cmd := newTestCmd()
	cmd.PersistentFlags().String("audit-trail", "", "Audit trail backend")
	cmd.PersistentFlags().String("audit-db-path", "", "Audit database path")
	return cmd
}

// newPastureMsgTestCmd adds pasture-msg-specific flags.
func newPastureMsgTestCmd() *cobra.Command {
	cmd := newTestCmd()
	cmd.PersistentFlags().String("format", "", "Default output format")
	return cmd
}

// ---- ResolveConnectionConfig — defaults ------------------------------------

func TestResolveConnectionConfig_Defaults(t *testing.T) {
	clearConnectionEnvVars(t)

	cmd := newTestCmd()
	cc := config.ResolveConnectionConfig(cmd)

	if cc.Namespace != "default" {
		t.Errorf("Namespace = %q, want %q", cc.Namespace, "default")
	}
	if cc.TaskQueue != "pasture" {
		t.Errorf("TaskQueue = %q, want %q", cc.TaskQueue, "pasture")
	}
	if cc.ServerAddress != "localhost:7233" {
		t.Errorf("ServerAddress = %q, want %q", cc.ServerAddress, "localhost:7233")
	}
}

// ---- ResolveConnectionConfig — environment variables override defaults -----

func TestResolveConnectionConfig_EnvVarsOverrideDefaults(t *testing.T) {
	t.Setenv(config.EnvNamespace, "env-namespace")
	t.Setenv(config.EnvTaskQueue, "env-queue")
	t.Setenv(config.EnvAddress, "env-host:7233")

	cmd := newTestCmd()
	cc := config.ResolveConnectionConfig(cmd)

	if cc.Namespace != "env-namespace" {
		t.Errorf("Namespace = %q, want %q", cc.Namespace, "env-namespace")
	}
	if cc.TaskQueue != "env-queue" {
		t.Errorf("TaskQueue = %q, want %q", cc.TaskQueue, "env-queue")
	}
	if cc.ServerAddress != "env-host:7233" {
		t.Errorf("ServerAddress = %q, want %q", cc.ServerAddress, "env-host:7233")
	}
}

// ---- ResolveConnectionConfig — CLI flags override env vars -----------------

func TestResolveConnectionConfig_CLIFlagsOverrideEnvVars(t *testing.T) {
	t.Setenv(config.EnvNamespace, "env-namespace")
	t.Setenv(config.EnvTaskQueue, "env-queue")
	t.Setenv(config.EnvAddress, "env-host:7233")

	cmd := newTestCmd()
	// Simulate the user passing flags explicitly.
	if err := cmd.PersistentFlags().Set("namespace", "cli-namespace"); err != nil {
		t.Fatalf("setting --namespace flag: %v", err)
	}
	if err := cmd.PersistentFlags().Set("task-queue", "cli-queue"); err != nil {
		t.Fatalf("setting --task-queue flag: %v", err)
	}
	if err := cmd.PersistentFlags().Set("address", "cli-host:7233"); err != nil {
		t.Fatalf("setting --address flag: %v", err)
	}

	cc := config.ResolveConnectionConfig(cmd)

	if cc.Namespace != "cli-namespace" {
		t.Errorf("Namespace = %q, want %q", cc.Namespace, "cli-namespace")
	}
	if cc.TaskQueue != "cli-queue" {
		t.Errorf("TaskQueue = %q, want %q", cc.TaskQueue, "cli-queue")
	}
	if cc.ServerAddress != "cli-host:7233" {
		t.Errorf("ServerAddress = %q, want %q", cc.ServerAddress, "cli-host:7233")
	}
}

// ---- ResolveConnectionConfig — YAML config file ----------------------------

func TestResolveConnectionConfig_YAMLOverridesDefaults(t *testing.T) {
	clearConnectionEnvVars(t)

	// Write a temporary YAML config file.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yamlContent := `
connection:
  namespace: yaml-namespace
  task_queue: yaml-queue
  server_address: yaml-host:7233
`
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	cmd := newTestCmd()
	cc := config.ResolveConnectionConfigFromFile(cmd, cfgPath)

	if cc.Namespace != "yaml-namespace" {
		t.Errorf("Namespace = %q, want %q", cc.Namespace, "yaml-namespace")
	}
	if cc.TaskQueue != "yaml-queue" {
		t.Errorf("TaskQueue = %q, want %q", cc.TaskQueue, "yaml-queue")
	}
	if cc.ServerAddress != "yaml-host:7233" {
		t.Errorf("ServerAddress = %q, want %q", cc.ServerAddress, "yaml-host:7233")
	}
}

func TestResolveConnectionConfig_CLIWinsOverYAML(t *testing.T) {
	clearConnectionEnvVars(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yamlContent := `
connection:
  namespace: yaml-namespace
  task_queue: yaml-queue
  server_address: yaml-host:7233
`
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	cmd := newTestCmd()
	if err := cmd.PersistentFlags().Set("namespace", "cli-namespace"); err != nil {
		t.Fatalf("setting --namespace flag: %v", err)
	}

	cc := config.ResolveConnectionConfigFromFile(cmd, cfgPath)

	if cc.Namespace != "cli-namespace" {
		t.Errorf("Namespace = %q, want %q (CLI must win over YAML)", cc.Namespace, "cli-namespace")
	}
	// Other fields should still come from YAML
	if cc.TaskQueue != "yaml-queue" {
		t.Errorf("TaskQueue = %q, want %q (YAML for unflagged field)", cc.TaskQueue, "yaml-queue")
	}
}

// ---- ResolvePasturedConfig -------------------------------------------------

func TestResolvePasturedConfig_Defaults(t *testing.T) {
	clearAllEnvVars(t)

	cmd := newPasturedTestCmd()
	cfg := config.ResolvePasturedConfig(cmd)

	if cfg.Connection.Namespace != "default" {
		t.Errorf("Namespace = %q, want %q", cfg.Connection.Namespace, "default")
	}
	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendSqlite)
	}
}

func TestResolvePasturedConfig_EnvOverride(t *testing.T) {
	t.Setenv(config.EnvAuditTrail, string(types.BackendMemory))

	cmd := newPasturedTestCmd()
	cfg := config.ResolvePasturedConfig(cmd)

	if cfg.AuditTrail != types.BackendMemory {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendMemory)
	}
}

// ---- ResolvePastureMsgConfig -----------------------------------------------

func TestResolvePastureMsgConfig_Defaults(t *testing.T) {
	clearAllEnvVars(t)

	cmd := newPastureMsgTestCmd()
	cfg := config.ResolvePastureMsgConfig(cmd)

	if cfg.Connection.Namespace != "default" {
		t.Errorf("Namespace = %q, want %q", cfg.Connection.Namespace, "default")
	}
	if cfg.DefaultFormat != types.OutputText {
		t.Errorf("DefaultFormat = %q, want %q", cfg.DefaultFormat, types.OutputText)
	}
}

func TestResolvePastureMsgConfig_CLIFormatFlag(t *testing.T) {
	clearAllEnvVars(t)

	cmd := newPastureMsgTestCmd()
	if err := cmd.PersistentFlags().Set("format", string(types.OutputJSON)); err != nil {
		t.Fatalf("setting --format flag: %v", err)
	}

	cfg := config.ResolvePastureMsgConfig(cmd)

	if cfg.DefaultFormat != types.OutputJSON {
		t.Errorf("DefaultFormat = %q, want %q", cfg.DefaultFormat, types.OutputJSON)
	}
}

// ---- helpers ---------------------------------------------------------------

func clearConnectionEnvVars(t *testing.T) {
	t.Helper()
	for _, key := range []string{config.EnvNamespace, config.EnvTaskQueue, config.EnvAddress} {
		t.Setenv(key, "")
		os.Unsetenv(key) //nolint:errcheck
	}
}

func clearAllEnvVars(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		config.EnvNamespace, config.EnvTaskQueue, config.EnvAddress,
		config.EnvAuditTrail, config.EnvAuditDBPath,
	} {
		t.Setenv(key, "")
		os.Unsetenv(key) //nolint:errcheck
	}
}
