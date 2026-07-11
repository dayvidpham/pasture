package config

// White-box (package config) tests for the config-resolution seam. They inject
// the env source (lookupEnv) as a fixture map instead of touching the process
// environment, so every case runs in parallel with no os.Setenv races. The thin
// os.LookupEnv wiring of the public entry point is covered separately by the
// serial test in viper_test.go.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/spf13/cobra"
)

// envMap returns a lookupEnv func backed by a fixed map — no process env.
func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// resolveTestCmd builds a command exposing the audit flags, optionally marking
// some as explicitly set (Changed) so the CLI-override tier can be exercised.
func resolveTestCmd(t *testing.T, changed map[string]string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.PersistentFlags().String("audit-trail", "", "Audit trail backend")
	cmd.PersistentFlags().String("audit-db-path", "", "Audit database path")
	for name, val := range changed {
		if err := cmd.PersistentFlags().Set(name, val); err != nil {
			t.Fatalf("setting --%s flag: %v", name, err)
		}
	}
	return cmd
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
	return path
}

func TestResolve_Defaults(t *testing.T) {
	t.Parallel()
	cfg, err := resolvePasturedConfigWithFile(resolveTestCmd(t, nil), "", envMap(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendSqlite)
	}
	if cfg.AuditDBPath != "" {
		t.Errorf("AuditDBPath = %q, want empty", cfg.AuditDBPath)
	}
}

func TestResolve_EnvOverridesDefault(t *testing.T) {
	t.Parallel()
	env := envMap(map[string]string{
		EnvAuditTrail:  string(types.BackendMemory),
		EnvAuditDBPath: "/tmp/env-audit.db",
	})
	cfg, err := resolvePasturedConfigWithFile(resolveTestCmd(t, nil), "", env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuditTrail != types.BackendMemory {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendMemory)
	}
	if cfg.AuditDBPath != "/tmp/env-audit.db" {
		t.Errorf("AuditDBPath = %q, want /tmp/env-audit.db", cfg.AuditDBPath)
	}
}

func TestResolve_CLIOverridesEnv(t *testing.T) {
	t.Parallel()
	env := envMap(map[string]string{
		EnvAuditTrail:  string(types.BackendMemory),
		EnvAuditDBPath: "/tmp/env-audit.db",
	})
	cmd := resolveTestCmd(t, map[string]string{
		"audit-trail":   string(types.BackendSqlite),
		"audit-db-path": "/tmp/cli-audit.db",
	})
	cfg, err := resolvePasturedConfigWithFile(cmd, "", env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CLI flag must win over env.
	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, types.BackendSqlite)
	}
	if cfg.AuditDBPath != "/tmp/cli-audit.db" {
		t.Errorf("AuditDBPath = %q, want /tmp/cli-audit.db", cfg.AuditDBPath)
	}
}

// Note: the valid-YAML-loads-both-fields scenario (audit_trail + audit_db_path
// resolved from a config file) is covered by the "pastured-yaml-loading" case in
// TestResolve_YAMLFixtures, which asserts both fields — so no separate inline
// TestResolve_YAMLOverridesDefault is kept here.

func TestResolve_EnvOverridesYAML(t *testing.T) {
	t.Parallel()
	// env sits above the config file: env should win.
	path := writeYAML(t, "audit_trail: sqlite\naudit_db_path: /tmp/yaml-audit.db\n")
	env := envMap(map[string]string{EnvAuditTrail: string(types.BackendMemory)})
	cfg, err := resolvePasturedConfigWithFile(resolveTestCmd(t, nil), path, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuditTrail != types.BackendMemory {
		t.Errorf("AuditTrail = %q, want %q (env must beat YAML)", cfg.AuditTrail, types.BackendMemory)
	}
	// audit_db_path had no env override, so the YAML value applies.
	if cfg.AuditDBPath != "/tmp/yaml-audit.db" {
		t.Errorf("AuditDBPath = %q, want /tmp/yaml-audit.db", cfg.AuditDBPath)
	}
}

func TestResolve_FileErrorStillResolvesDefaultsAndEnv(t *testing.T) {
	t.Parallel()
	// A missing/unreadable config file returns an error, but resolution must
	// still fall through to env and defaults (never block them).
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	env := envMap(map[string]string{EnvAuditDBPath: "/tmp/env-audit.db"})
	cfg, err := resolvePasturedConfigWithFile(resolveTestCmd(t, nil), missing, env)
	if err == nil {
		t.Fatal("expected a file-read error for a missing config file, got nil")
	}
	// audit_trail: no env, bad file → default. audit_db_path: from env.
	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want default %q despite file error", cfg.AuditTrail, types.BackendSqlite)
	}
	if cfg.AuditDBPath != "/tmp/env-audit.db" {
		t.Errorf("AuditDBPath = %q, want env value despite file error", cfg.AuditDBPath)
	}
}

// TestResolve_EmptySetEnvIsUnset proves that a SET-BUT-EMPTY env var is treated
// as UNSET, so it does not mask the default. bindEnvVar replaced viper.BindEnv,
// whose default (allowEmptyEnv=false) already ignored empty env values; this
// guards against a regression where an exported-but-blank PASTURE_AUDIT_TRAIL
// would land in the override tier and resolve to "" (which maps to the
// non-durable in-memory audit backend) instead of the sqlite default.
func TestResolve_EmptySetEnvIsUnset(t *testing.T) {
	t.Parallel()
	// lookupEnv reports the var as present ("", true) — the set-but-empty case.
	env := envMap(map[string]string{EnvAuditTrail: ""})
	cfg, err := resolvePasturedConfigWithFile(resolveTestCmd(t, nil), "", env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuditTrail != types.BackendSqlite {
		t.Errorf("AuditTrail = %q, want default %q (a set-but-empty env var must be treated as unset)",
			cfg.AuditTrail, types.BackendSqlite)
	}
}

// configLoadingFixture mirrors one entry in testdata/config_loading.yaml.
type configLoadingFixture struct {
	Name           string `yaml:"name"`
	YAMLContent    string `yaml:"yaml_content"`
	UseMissingPath bool   `yaml:"use_missing_path"`
	WantError      bool   `yaml:"want_error"`
	WantAuditTrail string `yaml:"want_audit_trail"`
	WantAuditDB    string `yaml:"want_audit_db_path"`
}

// TestResolve_YAMLFixtures drives the config-file loading matrix (malformed YAML,
// missing file, and a valid file) through the resolvePasturedConfigWithFile seam
// with an injected empty env (envMap(nil)). Injecting the env instead of clearing
// the process environment lets every case run in parallel with no os.Setenv
// races — the reason this matrix moved here from a former serial black-box test.
// The public ResolvePasturedConfigFromFile wrapper's error-surfacing contract is
// covered by a dedicated case in viper_test.go.
func TestResolve_YAMLFixtures(t *testing.T) {
	t.Parallel()
	var fixtures []configLoadingFixture
	testutil.LoadFixtures(t, testutil.ConfigLoading, &fixtures)
	if len(fixtures) == 0 {
		t.Fatal("config_loading fixture is empty — expected the YAML loading matrix")
	}

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.Name, func(t *testing.T) {
			t.Parallel()

			var configFile string
			if fx.UseMissingPath {
				configFile = filepath.Join(t.TempDir(), "does-not-exist.yaml")
			} else {
				configFile = writeYAML(t, fx.YAMLContent)
			}

			cfg, err := resolvePasturedConfigWithFile(resolveTestCmd(t, nil), configFile, envMap(nil))

			if fx.WantError && err == nil {
				t.Errorf("want non-nil error, got nil")
			}
			if !fx.WantError && err != nil {
				t.Errorf("want nil error, got: %v", err)
			}
			if cfg.AuditTrail != types.AuditTrailBackend(fx.WantAuditTrail) {
				t.Errorf("AuditTrail = %q, want %q", cfg.AuditTrail, fx.WantAuditTrail)
			}
			if cfg.AuditDBPath != fx.WantAuditDB {
				t.Errorf("AuditDBPath = %q, want %q", cfg.AuditDBPath, fx.WantAuditDB)
			}
		})
	}
}
