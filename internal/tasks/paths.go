// Package tasks provides the small wiring layer between pasture's CLI and the
// Provenance task tracker library. It resolves the default database path,
// derives the default namespace, and centralises Tracker open semantics so
// every subcommand uses the same conventions.
package tasks

import (
	"os"
	"path/filepath"

	"github.com/dayvidpham/provenance/pkg/namespace"
)

// DBPathEnv is the environment variable users can set to override the default
// Provenance database location.
const DBPathEnv = "PASTURE_DB_PATH"

// DefaultDBPath returns the conventional Provenance database location.
//
// Resolution order:
//  1. $PASTURE_DB_PATH
//  2. $XDG_DATA_HOME/pasture/provenance.db
//  3. $HOME/.local/share/pasture/provenance.db
//
// The directory is NOT created here; the caller (typically OpenSQLite) ensures
// the parent directory exists when the file is opened for writing.
func DefaultDBPath() string {
	if v := os.Getenv(DBPathEnv); v != "" {
		return v
	}
	if dataDir := os.Getenv("XDG_DATA_HOME"); dataDir != "" {
		return filepath.Join(dataDir, "pasture", "provenance.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to relative path. The handler will surface a clearer error
		// when SQLite fails to open the file.
		return filepath.Join(".pasture", "provenance.db")
	}
	return filepath.Join(home, ".local", "share", "pasture", "provenance.db")
}

// DefaultNamespace returns the namespace URI that pasture uses when the user
// has not specified --namespace explicitly. It delegates to the Provenance
// namespace package so the same derivation rules apply across the toolkit.
func DefaultNamespace() (string, error) {
	return namespace.DefaultNamespace()
}
