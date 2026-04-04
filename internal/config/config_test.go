package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/config"
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

// ---- DefaultDBPath() ---------------------------------------------------------

func TestDefaultDBPath_ContainsPasture(t *testing.T) {
	path := config.DefaultDBPath()
	if !strings.Contains(path, "pasture") {
		t.Errorf("DefaultDBPath() = %q, expected to contain 'pasture'", path)
	}
}

func TestDefaultDBPath_EndsWithProvenanceDB(t *testing.T) {
	path := config.DefaultDBPath()
	if filepath.Base(path) != "provenance.db" {
		t.Errorf("DefaultDBPath() = %q, expected base name 'provenance.db'", path)
	}
}

func TestDefaultDBPath_XDGDataDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory, skipping")
	}
	expected := filepath.Join(home, ".local", "share", "pasture", "provenance.db")
	got := config.DefaultDBPath()
	if got != expected {
		t.Errorf("DefaultDBPath() = %q, want %q", got, expected)
	}
}

func TestDefaultDBPath_HomeUnset(t *testing.T) {
	// Clear HOME and USERPROFILE (the two env vars os.UserHomeDir consults)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	got := config.DefaultDBPath()
	want := filepath.Join(".", ".local", "share", "pasture", "provenance.db")
	if got != want {
		t.Errorf("DefaultDBPath() with HOME unset = %q, want %q", got, want)
	}
}

// ---- ProvenanceConfig struct -------------------------------------------------

func TestProvenanceConfig_ZeroValues(t *testing.T) {
	var pc config.ProvenanceConfig
	if pc.DBPath != "" {
		t.Errorf("ProvenanceConfig.DBPath zero value = %q, want empty string", pc.DBPath)
	}
}

// ---- ConnectionConfig struct -----------------------------------------------

func TestConnectionConfig_ZeroValues(t *testing.T) {
	var cc config.ConnectionConfig
	// Zero values should be empty strings — defaults are applied by Viper, not the struct.
	if cc.Namespace != "" {
		t.Errorf("ConnectionConfig.Namespace zero value = %q, want empty string", cc.Namespace)
	}
	if cc.TaskQueue != "" {
		t.Errorf("ConnectionConfig.TaskQueue zero value = %q, want empty string", cc.TaskQueue)
	}
	if cc.ServerAddress != "" {
		t.Errorf("ConnectionConfig.ServerAddress zero value = %q, want empty string", cc.ServerAddress)
	}
}

func TestConnectionConfig_FieldAssignment(t *testing.T) {
	cc := config.ConnectionConfig{
		Namespace:     "my-namespace",
		TaskQueue:     "my-queue",
		ServerAddress: "temporal.example.com:7233",
	}
	if cc.Namespace != "my-namespace" {
		t.Errorf("Namespace = %q, want %q", cc.Namespace, "my-namespace")
	}
	if cc.TaskQueue != "my-queue" {
		t.Errorf("TaskQueue = %q, want %q", cc.TaskQueue, "my-queue")
	}
	if cc.ServerAddress != "temporal.example.com:7233" {
		t.Errorf("ServerAddress = %q, want %q", cc.ServerAddress, "temporal.example.com:7233")
	}
}

// ---- Environment variable constant names -----------------------------------

func TestEnvConstants(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{"EnvNamespace", config.EnvNamespace},
		{"EnvTaskQueue", config.EnvTaskQueue},
		{"EnvAddress", config.EnvAddress},
		{"EnvAuditTrail", config.EnvAuditTrail},
		{"EnvAuditDBPath", config.EnvAuditDBPath},
		{"EnvProvenanceDBPath", config.EnvProvenanceDBPath},
	}
	for _, tc := range cases {
		if tc.val == "" {
			t.Errorf("constant %s is empty", tc.name)
		}
	}
	if config.EnvNamespace != "TEMPORAL_NAMESPACE" {
		t.Errorf("EnvNamespace = %q, want TEMPORAL_NAMESPACE", config.EnvNamespace)
	}
	if config.EnvTaskQueue != "TEMPORAL_TASK_QUEUE" {
		t.Errorf("EnvTaskQueue = %q, want TEMPORAL_TASK_QUEUE", config.EnvTaskQueue)
	}
	if config.EnvAddress != "TEMPORAL_ADDRESS" {
		t.Errorf("EnvAddress = %q, want TEMPORAL_ADDRESS", config.EnvAddress)
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
