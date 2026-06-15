package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/types"
)

func newPasturedTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("audit-trail", "", "Audit trail backend")
	cmd.PersistentFlags().String("audit-db-path", "", "Audit database path")
	return cmd
}

func TestResolvePasturedConfig_Defaults(t *testing.T) {
	clearAllEnvVars(t)

	cfg := config.ResolvePasturedConfig(newPasturedTestCmd())

	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendSqlite)
	}
	if cfg.AuditDBPath != "" {
		t.Errorf("AuditDBPath = %q, want empty", cfg.AuditDBPath)
	}
}

func TestResolvePasturedConfig_EnvOverride(t *testing.T) {
	testutil.SetEnv(t, config.EnvAuditTrail, string(types.BackendMemory))
	testutil.SetEnv(t, config.EnvAuditDBPath, "/tmp/env-audit.db")

	cfg := config.ResolvePasturedConfig(newPasturedTestCmd())

	if cfg.AuditTrail != types.BackendMemory {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendMemory)
	}
	if cfg.AuditDBPath != "/tmp/env-audit.db" {
		t.Errorf("AuditDBPath = %q, want /tmp/env-audit.db", cfg.AuditDBPath)
	}
}

func TestResolvePasturedConfig_CLIFlagsOverrideEnvVars(t *testing.T) {
	testutil.SetEnv(t, config.EnvAuditTrail, string(types.BackendMemory))
	testutil.SetEnv(t, config.EnvAuditDBPath, "/tmp/env-audit.db")

	cmd := newPasturedTestCmd()
	if err := cmd.PersistentFlags().Set("audit-trail", string(types.BackendSqlite)); err != nil {
		t.Fatalf("setting --audit-trail flag: %v", err)
	}
	if err := cmd.PersistentFlags().Set("audit-db-path", "/tmp/cli-audit.db"); err != nil {
		t.Fatalf("setting --audit-db-path flag: %v", err)
	}

	cfg := config.ResolvePasturedConfig(cmd)

	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendSqlite)
	}
	if cfg.AuditDBPath != "/tmp/cli-audit.db" {
		t.Errorf("AuditDBPath = %q, want /tmp/cli-audit.db", cfg.AuditDBPath)
	}
}

func TestResolvePasturedConfig_YAMLOverridesDefaults(t *testing.T) {
	clearAllEnvVars(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yamlContent := `
audit_trail: memory
audit_db_path: /tmp/yaml-audit.db
`
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	cfg, err := config.ResolvePasturedConfigFromFile(newPasturedTestCmd(), cfgPath)
	if err != nil {
		t.Fatalf("ResolvePasturedConfigFromFile: %v", err)
	}

	if cfg.AuditTrail != types.BackendMemory {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendMemory)
	}
	if cfg.AuditDBPath != "/tmp/yaml-audit.db" {
		t.Errorf("AuditDBPath = %q, want /tmp/yaml-audit.db", cfg.AuditDBPath)
	}
}

func clearAllEnvVars(t *testing.T) {
	t.Helper()
	for _, key := range []string{config.EnvAuditTrail, config.EnvAuditDBPath} {
		testutil.UnsetEnv(t, key)
	}
}
