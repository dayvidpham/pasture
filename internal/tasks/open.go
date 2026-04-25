package tasks

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// OpenTracker opens the Provenance tracker at dbPath, creating any missing
// parent directories along the way. An empty dbPath resolves to DefaultDBPath.
//
// On failure the returned error is a *pasterrors.StructuredError with
// CategoryConnection, so callers can map it to exit code 2 via
// pasterrors.ExitCode.
func OpenTracker(dbPath string) (provenance.Tracker, error) {
	if dbPath == "" {
		dbPath = DefaultDBPath()
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("could not create directory for provenance database %q", dbPath),
			Why:      err.Error(),
			Impact:   "tracker cannot be opened until the parent directory is writable",
			Fix:      fmt.Sprintf("create the directory manually with `mkdir -p %q` or override with --db <path> / $%s", filepath.Dir(dbPath), DBPathEnv),
		}
	}

	tr, err := provenance.OpenSQLite(dbPath)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("could not open provenance database %q", dbPath),
			Why:      err.Error(),
			Impact:   "no task operations can be performed without a working tracker",
			Fix:      fmt.Sprintf("verify the file exists and is a valid Provenance database, or remove it to start fresh; override the path with --db <path> or $%s", DBPathEnv),
		}
	}
	return tr, nil
}

// ResolveNamespace returns the namespace to use for a `create` operation.
// Explicit takes precedence; an empty value falls back to DefaultNamespace.
//
// Errors from DefaultNamespace are wrapped as CategoryValidation so that the
// user is prompted to supply --namespace explicitly.
func ResolveNamespace(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	ns, err := DefaultNamespace()
	if err != nil {
		return "", &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "could not derive default namespace from git remote or working directory",
			Why:      err.Error(),
			Impact:   "task creation requires a namespace URI but none was provided and none could be inferred",
			Fix:      "pass --namespace <uri> explicitly, or run inside a directory with a git remote",
		}
	}
	return ns, nil
}
