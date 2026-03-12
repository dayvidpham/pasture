package config_test

// config_yaml_test.go tests YAML-driven config loading for both
// ResolvePasturedConfigFromFile and ResolvePastureMsgConfigFromFile.
//
// Fixture file: testdata/config_loading.yaml
// Each fixture entry exercises one named scenario. Tests verify:
//   - error presence/absence matches want_error
//   - config fields reflect YAML values (happy path) or built-in defaults (error path)

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
	WantNamespace  string `yaml:"want_namespace"`
	WantTaskQueue  string `yaml:"want_task_queue"`
	WantFormat     string `yaml:"want_format"`
}

// buildConfigFilePath creates a temporary YAML file from content (if non-empty)
// or returns a path to a file that does not exist (if use_missing_path is true).
// The caller is responsible for ensuring the temp dir is cleaned up via t.TempDir().
func buildConfigFilePath(t *testing.T, fx configLoadingFixture) string {
	t.Helper()
	if fx.UseMissingPath {
		// Return a path that does not exist — guaranteed by using a fresh temp dir.
		return filepath.Join(t.TempDir(), "does-not-exist.yaml")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(fx.YAMLContent), 0o644); err != nil {
		t.Fatalf("buildConfigFilePath: could not write temp config file: %v", err)
	}
	return path
}

// ---- ResolvePasturedConfigFromFile YAML-driven tests -----------------------

// TestResolvePasturedConfigFromFile_YAMLFixtures exercises ResolvePasturedConfigFromFile
// against the malformed-yaml-returns-error and missing-file-returns-error cases.
// (The remaining cases are pasture-msg specific.)
func TestResolvePasturedConfigFromFile_YAMLFixtures(t *testing.T) {
	var fixtures []configLoadingFixture
	testutil.LoadFixtures(t, testutil.ConfigLoading, &fixtures)

	// Only run the cases relevant to pastured (first two).
	pasturedCases := []string{
		"malformed-yaml-returns-error",
		"missing-file-returns-error",
	}

	for _, fx := range fixtures {
		fx := fx // capture loop var
		// Skip pasture-msg-specific fixtures.
		relevant := false
		for _, name := range pasturedCases {
			if fx.Name == name {
				relevant = true
				break
			}
		}
		if !relevant {
			continue
		}

		t.Run(fx.Name, func(t *testing.T) {
			clearAllEnvVars(t)

			cmd := newPasturedTestCmd()
			cfgPath := buildConfigFilePath(t, fx)

			cfg, err := config.ResolvePasturedConfigFromFile(cmd, cfgPath)

			// Error presence
			if fx.WantError && err == nil {
				t.Errorf("want non-nil error, got nil")
			}
			if !fx.WantError && err != nil {
				t.Errorf("want nil error, got: %v", err)
			}

			// Config fields should reflect defaults when file is unreadable.
			if cfg.Connection.Namespace != fx.WantNamespace {
				t.Errorf("Namespace = %q, want %q", cfg.Connection.Namespace, fx.WantNamespace)
			}
			if cfg.Connection.TaskQueue != fx.WantTaskQueue {
				t.Errorf("TaskQueue = %q, want %q", cfg.Connection.TaskQueue, fx.WantTaskQueue)
			}
		})
	}
}

// ---- ResolvePastureMsgConfigFromFile YAML-driven tests ---------------------

// TestResolvePastureMsgConfigFromFile_YAMLFixtures exercises all four fixture
// cases against ResolvePastureMsgConfigFromFile.
func TestResolvePastureMsgConfigFromFile_YAMLFixtures(t *testing.T) {
	var fixtures []configLoadingFixture
	testutil.LoadFixtures(t, testutil.ConfigLoading, &fixtures)

	for _, fx := range fixtures {
		fx := fx // capture loop var

		t.Run(fx.Name, func(t *testing.T) {
			clearAllEnvVars(t)

			cmd := newPastureMsgTestCmd()
			cfgPath := buildConfigFilePath(t, fx)

			cfg, err := config.ResolvePastureMsgConfigFromFile(cmd, cfgPath)

			// Error presence
			if fx.WantError && err == nil {
				t.Errorf("want non-nil error, got nil")
			}
			if !fx.WantError && err != nil {
				t.Errorf("want nil error, got: %v", err)
			}

			// Connection fields
			if cfg.Connection.Namespace != fx.WantNamespace {
				t.Errorf("Namespace = %q, want %q", cfg.Connection.Namespace, fx.WantNamespace)
			}
			if cfg.Connection.TaskQueue != fx.WantTaskQueue {
				t.Errorf("TaskQueue = %q, want %q", cfg.Connection.TaskQueue, fx.WantTaskQueue)
			}

			// DefaultFormat (only checked when the fixture specifies a value)
			if fx.WantFormat != "" {
				want := types.OutputFormat(fx.WantFormat)
				if cfg.DefaultFormat != want {
					t.Errorf("DefaultFormat = %q, want %q", cfg.DefaultFormat, want)
				}
			}
		})
	}
}
