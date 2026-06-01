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
// unified pasture database location (see DefaultDBPath).
const DBPathEnv = "PASTURE_DB_PATH"

// DefaultDBFilename is the filename portion of the unified pasture database.
// PROPOSAL-2 §7.1 binds this to `pasture.db` so both the Provenance subsystem
// (task CRUD, agents, edges, labels, comments, activities) and the audit
// subsystem (audit_events, context_edges, sessions) open the same file.
//
// Pre-PROPOSAL-2 the `pasture` CLI defaulted to `provenance.db` and `pastured`
// defaulted to `audit.db`; SLICE-10 collapses both to `pasture.db`. See
// PROPOSAL-2 §7.1 + §11 Scenario 9 for the unification rationale and the
// hjsdt-CLI byte-identical-output requirement that drives the migration.
const DefaultDBFilename = "pasture.db"

// DefaultDBPath returns the conventional path for the unified pasture
// database, used by both the `pasture` CLI and the `pastured` daemon
// (PROPOSAL-2 §7.1).
//
// Resolution order:
//  1. $PASTURE_DB_PATH
//  2. $XDG_DATA_HOME/pasture/pasture.db
//  3. $HOME/.local/share/pasture/pasture.db
//  4. .pasture/pasture.db (last-resort relative fallback when $HOME is unset)
//
// The directory is NOT created here; the caller (typically OpenTaskTracker
// or OpenTracker) calls os.MkdirAll on filepath.Dir of the result before the
// file is opened for writing.
func DefaultDBPath() string {
	if v := os.Getenv(DBPathEnv); v != "" {
		return v
	}
	if dataDir := os.Getenv("XDG_DATA_HOME"); dataDir != "" {
		return filepath.Join(dataDir, "pasture", DefaultDBFilename)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to relative path. The handler will surface a clearer error
		// when SQLite fails to open the file.
		return filepath.Join(".pasture", DefaultDBFilename)
	}
	return filepath.Join(home, ".local", "share", "pasture", DefaultDBFilename)
}

// DefaultNamespace returns the namespace URI that pasture uses when the user
// has not specified --namespace explicitly. It delegates to the Provenance
// namespace package so the same derivation rules apply across the toolkit.
func DefaultNamespace() (string, error) {
	return namespace.DefaultNamespace()
}
