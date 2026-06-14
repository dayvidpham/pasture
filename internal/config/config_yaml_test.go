package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/types"
)

// configLoadingFixture mirrors one entry in testdata/config_loading.yaml.
type configLoadingFixture struct {
	Name           string `yaml:"name"`
	YAMLContent    string `yaml:"yaml_content"`
	UseMissingPath bool   `yaml:"use_missing_path"`
	WantError      bool   `yaml:"want_error"`
	WantAuditTrail string `yaml:"want_audit_trail"`
	WantAuditDB    string `yaml:"want_audit_db_path"`
}

func buildConfigFilePath(t *testing.T, fx configLoadingFixture) string {
	t.Helper()
	if fx.UseMissingPath {
		return filepath.Join(t.TempDir(), "does-not-exist.yaml")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(fx.YAMLContent), 0o644); err != nil {
		t.Fatalf("buildConfigFilePath: could not write temp config file: %v", err)
	}
	return path
}

func TestResolvePasturedConfigFromFile_YAMLFixtures(t *testing.T) {
	var fixtures []configLoadingFixture
	testutil.LoadFixtures(t, testutil.ConfigLoading, &fixtures)

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.Name, func(t *testing.T) {
			clearAllEnvVars(t)

			cfg, err := config.ResolvePasturedConfigFromFile(newPasturedTestCmd(), buildConfigFilePath(t, fx))

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
