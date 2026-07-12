package config_test

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/types"
)

// newPasturedTestCmd builds a command exposing the audit flags.
func newPasturedTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("audit-trail", "", "Audit trail backend")
	cmd.PersistentFlags().String("audit-db-path", "", "Audit database path")
	return cmd
}

// TestPublicResolvers_ReadProcessEnv is the single serial, env-touching test for
// the config-resolution seam: it proves BOTH public entry points —
// ResolvePasturedConfig and ResolvePasturedConfigFromFile — wire os.LookupEnv as
// their environment source, at value level (not just non-nil). It mutates the
// process environment via t.Setenv, so it stays serial (t.Setenv forbids
// t.Parallel by design). The full resolution matrix — defaults, env, CLI, YAML,
// and their precedence — is covered in parallel by the white-box tests in
// viper_internal_test.go, which inject env as a fixture instead of touching
// os.Environ.
func TestPublicResolvers_ReadProcessEnv(t *testing.T) {
	t.Setenv(config.EnvAuditTrail, string(types.BackendMemory))
	t.Setenv(config.EnvAuditDBPath, "/tmp/env-audit.db")

	// ResolvePasturedConfig: with no config file, the process env drives values.
	cfg := config.ResolvePasturedConfig(newPasturedTestCmd())
	if cfg.AuditTrail != types.BackendMemory {
		t.Errorf("ResolvePasturedConfig AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendMemory)
	}
	if cfg.AuditDBPath != "/tmp/env-audit.db" {
		t.Errorf("ResolvePasturedConfig AuditDBPath = %q, want /tmp/env-audit.db", cfg.AuditDBPath)
	}

	// ResolvePasturedConfigFromFile: an empty config path means the same process
	// env (not a file) drives the values, so its os.LookupEnv wiring is proven at
	// value level too. A fresh command avoids any flag-state carryover.
	cfgFromFile, err := config.ResolvePasturedConfigFromFile(newPasturedTestCmd(), "")
	if err != nil {
		t.Fatalf("ResolvePasturedConfigFromFile(empty path) unexpected error: %v", err)
	}
	if cfgFromFile.AuditTrail != types.BackendMemory {
		t.Errorf("ResolvePasturedConfigFromFile AuditTrail = %q, want %q", cfgFromFile.AuditTrail, types.BackendMemory)
	}
	if cfgFromFile.AuditDBPath != "/tmp/env-audit.db" {
		t.Errorf("ResolvePasturedConfigFromFile AuditDBPath = %q, want /tmp/env-audit.db", cfgFromFile.AuditDBPath)
	}
}

// TestResolvePasturedConfigFromFile_SurfacesFileError covers the public
// ResolvePasturedConfigFromFile path directly. Unlike ResolvePasturedConfig
// (which swallows a config-file read error), FromFile surfaces it — that is its
// distinguishing contract. The assertion is on the error alone, which is
// independent of the process environment, so this test stays parallel-safe; the
// resolved-value matrix (malformed / missing / valid YAML) is covered
// deterministically by TestResolve_YAMLFixtures in viper_internal_test.go.
func TestResolvePasturedConfigFromFile_SurfacesFileError(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	if _, err := config.ResolvePasturedConfigFromFile(newPasturedTestCmd(), missing); err == nil {
		t.Fatal("ResolvePasturedConfigFromFile must surface a file-read error for a missing config file")
	}
}
