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

// PasturedConfig holds the full configuration for the pastured daemon.
type PasturedConfig struct {
	// AuditTrail selects the audit event persistence backend.
	AuditTrail types.AuditTrailBackend `yaml:"audit_trail" mapstructure:"audit_trail"`
	// AuditDBPath is the filesystem path for the SQLite audit database.
	AuditDBPath string `yaml:"audit_db_path" mapstructure:"audit_db_path"`
}

// Environment variable names read by Viper when resolving config.
const (
	// EnvAuditTrail is the env var for selecting the audit trail backend.
	EnvAuditTrail = "PASTURE_AUDIT_TRAIL"
	// EnvAuditDBPath is the env var for the SQLite audit database path.
	EnvAuditDBPath = "PASTURE_AUDIT_DB_PATH"
	// EnvProvenanceDBPath is the env var for the provenance tracker database path.
	EnvProvenanceDBPath = "PASTURE_PROVENANCE_DB_PATH"
)

// ProvenanceConfig holds configuration for the provenance task tracker.
type ProvenanceConfig struct {
	// DBPath is the filesystem path for the provenance SQLite database.
	// Default: ~/.local/share/pasture/provenance.db
	// Override via PASTURE_PROVENANCE_DB_PATH env var or [provenance] db_path in config.
	// NOTE: Viper resolver for [provenance] config section not yet wired —
	// use DefaultProvenanceDBPath() as the fallback until ResolveProvenanceConfig is added.
	DBPath string `yaml:"db_path" mapstructure:"db_path"`
}

// DefaultProvenanceDBPath returns the canonical location for the provenance database:
// ~/.local/share/pasture/provenance.db
//
// This function returns only the XDG default path. Environment variable
// overrides (PASTURE_PROVENANCE_DB_PATH) and config file values
// ([provenance] db_path) are resolved by the Viper layer, not by this
// function — matching the pattern established by DefaultConfigPath.
//
// On systems where $HOME is unset the function falls back to
// "./.local/share/pasture/provenance.db" so the caller always receives a
// non-empty path.
func DefaultProvenanceDBPath() string {
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
