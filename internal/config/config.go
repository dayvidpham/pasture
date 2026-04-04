// Package config provides configuration structs and resolution logic for pasture daemons and CLI tools.
//
// Config values are resolved in priority order: CLI flags > environment variables > YAML file > built-in defaults.
// Viper handles the merge; see viper.go for binding details.
package config

import (
	"os"
	"path/filepath"

	"github.com/dayvidpham/pasture/internal/types"
)

// ConnectionConfig holds Temporal connection parameters shared by pastured and pasture-msg.
type ConnectionConfig struct {
	// Namespace is the Temporal namespace to use (default: "default").
	Namespace string `yaml:"namespace" mapstructure:"namespace"`
	// TaskQueue is the Temporal task queue name (default: "pasture").
	TaskQueue string `yaml:"task_queue" mapstructure:"task_queue"`
	// ServerAddress is the Temporal frontend gRPC address (default: "localhost:7233").
	ServerAddress string `yaml:"server_address" mapstructure:"server_address"`
}

// PasturedConfig holds the full configuration for the pastured daemon.
type PasturedConfig struct {
	// Connection groups all Temporal connection parameters.
	Connection ConnectionConfig `yaml:"connection" mapstructure:"connection"`
	// AuditTrail selects the audit event persistence backend.
	AuditTrail types.AuditTrailBackend `yaml:"audit_trail" mapstructure:"audit_trail"`
	// AuditDBPath is the filesystem path for the SQLite audit database.
	AuditDBPath string `yaml:"audit_db_path" mapstructure:"audit_db_path"`
}

// PastureMsgConfig holds the configuration for the pasture-msg CLI.
type PastureMsgConfig struct {
	// Connection groups all Temporal connection parameters.
	Connection ConnectionConfig `yaml:"connection" mapstructure:"connection"`
	// DefaultFormat sets the default output serialisation format.
	DefaultFormat types.OutputFormat `yaml:"default_format" mapstructure:"default_format"`
}

// Environment variable names read by Viper when resolving config.
const (
	// EnvNamespace is the env var for the Temporal namespace.
	EnvNamespace = "TEMPORAL_NAMESPACE"
	// EnvTaskQueue is the env var for the Temporal task queue.
	EnvTaskQueue = "TEMPORAL_TASK_QUEUE"
	// EnvAddress is the env var for the Temporal server address.
	EnvAddress = "TEMPORAL_ADDRESS"
	// EnvAuditTrail is the env var for selecting the audit trail backend.
	EnvAuditTrail = "PASTURE_AUDIT_TRAIL"
	// EnvAuditDBPath is the env var for the SQLite audit database path.
	EnvAuditDBPath = "PASTURE_AUDIT_DB_PATH"
	// EnvProvenanceDBPath is the env var for the provenance tracker database path.
	EnvProvenanceDBPath = "PROVENANCE_DB_PATH"
)

// ProvenanceConfig holds configuration for the provenance task tracker.
type ProvenanceConfig struct {
	// DBPath is the filesystem path for the provenance SQLite database.
	// Default: ~/.local/share/pasture/provenance.db
	// Override via PROVENANCE_DB_PATH env var or [provenance] db_path in config.
	DBPath string `yaml:"db_path" mapstructure:"db_path"`
}

// DefaultDBPath returns the canonical location for the provenance database:
// ~/.local/share/pasture/provenance.db
//
// The path follows the XDG Base Directory Specification for user data files.
// Override at runtime via the PROVENANCE_DB_PATH environment variable or
// the [provenance] db_path key in pasture's config file.
//
// On systems where $HOME is unset the function falls back to
// "./.local/share/pasture/provenance.db" so the caller always receives a
// non-empty path.
func DefaultDBPath() string {
	if envPath := os.Getenv(EnvProvenanceDBPath); envPath != "" {
		return envPath
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".local", "share", "pasture", "provenance.db")
	}
	return filepath.Join(home, ".local", "share", "pasture", "provenance.db")
}

// DefaultConfigPath returns the canonical location for the pasture config file:
// ~/.config/pasture/config.yaml
//
// On systems where $HOME is unset the function falls back to "." so the caller
// always receives a non-empty path (Viper will simply not find the file and will
// rely on environment variables and defaults).
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".config", "pasture", "config.yaml")
	}
	return filepath.Join(home, ".config", "pasture", "config.yaml")
}
