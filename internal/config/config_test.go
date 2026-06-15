package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/testutil"
)

// ---- DefaultConfigPath() ---------------------------------------------------

func TestDefaultConfigPath_ContainsPasture(t *testing.T) {
	path := config.DefaultConfigPath()
	if !strings.Contains(path, "pasture") {
		t.Errorf("DefaultConfigPath() = %q, expected to contain 'pasture'", path)
	}
}

func TestDefaultConfigPath_EndsWithConfigYaml(t *testing.T) {
	path := config.DefaultConfigPath()
	if filepath.Base(path) != "config.yaml" {
		t.Errorf("DefaultConfigPath() = %q, expected base name 'config.yaml'", path)
	}
}

func TestDefaultConfigPath_UnderHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory, skipping")
	}
	path := config.DefaultConfigPath()
	if !strings.HasPrefix(path, home) {
		t.Errorf("DefaultConfigPath() = %q, expected prefix %q", path, home)
	}
}

func TestDefaultConfigPath_FullPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory, skipping")
	}
	expected := filepath.Join(home, ".config", "pasture", "config.yaml")
	got := config.DefaultConfigPath()
	if got != expected {
		t.Errorf("DefaultConfigPath() = %q, want %q", got, expected)
	}
}

// ---- DefaultProvenanceDBPath() ---------------------------------------------------------

func TestDefaultProvenanceDBPath_ContainsPasture(t *testing.T) {
	path := config.DefaultProvenanceDBPath()
	if !strings.Contains(path, "pasture") {
		t.Errorf("DefaultProvenanceDBPath() = %q, expected to contain 'pasture'", path)
	}
}

func TestDefaultProvenanceDBPath_EndsWithProvenanceDB(t *testing.T) {
	path := config.DefaultProvenanceDBPath()
	if filepath.Base(path) != "provenance.db" {
		t.Errorf("DefaultProvenanceDBPath() = %q, expected base name 'provenance.db'", path)
	}
}

func TestDefaultProvenanceDBPath_XDGDataDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory, skipping")
	}
	expected := filepath.Join(home, ".local", "share", "pasture", "provenance.db")
	got := config.DefaultProvenanceDBPath()
	if got != expected {
		t.Errorf("DefaultProvenanceDBPath() = %q, want %q", got, expected)
	}
}

func TestDefaultProvenanceDBPath_HomeUnset(t *testing.T) {
	// Clear HOME and USERPROFILE (the two env vars os.UserHomeDir consults)
	testutil.SetEnv(t, "HOME", "")
	testutil.SetEnv(t, "USERPROFILE", "")
	got := config.DefaultProvenanceDBPath()
	want := filepath.Join(".", ".local", "share", "pasture", "provenance.db")
	if got != want {
		t.Errorf("DefaultProvenanceDBPath() with HOME unset = %q, want %q", got, want)
	}
}

// ---- ProvenanceConfig struct -------------------------------------------------

func TestProvenanceConfig_ZeroValues(t *testing.T) {
	var pc config.ProvenanceConfig
	if pc.DBPath != "" {
		t.Errorf("ProvenanceConfig.DBPath zero value = %q, want empty string", pc.DBPath)
	}
}

// ---- Environment variable constant names -----------------------------------

func TestEnvConstants(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{"EnvAuditTrail", config.EnvAuditTrail},
		{"EnvAuditDBPath", config.EnvAuditDBPath},
		{"EnvProvenanceDBPath", config.EnvProvenanceDBPath},
	}
	for _, tc := range cases {
		if tc.val == "" {
			t.Errorf("constant %s is empty", tc.name)
		}
	}
	if config.EnvAuditTrail != "PASTURE_AUDIT_TRAIL" {
		t.Errorf("EnvAuditTrail = %q, want PASTURE_AUDIT_TRAIL", config.EnvAuditTrail)
	}
	if config.EnvAuditDBPath != "PASTURE_AUDIT_DB_PATH" {
		t.Errorf("EnvAuditDBPath = %q, want PASTURE_AUDIT_DB_PATH", config.EnvAuditDBPath)
	}
	if config.EnvProvenanceDBPath != "PASTURE_PROVENANCE_DB_PATH" {
		t.Errorf("EnvProvenanceDBPath = %q, want PASTURE_PROVENANCE_DB_PATH", config.EnvProvenanceDBPath)
	}
}
