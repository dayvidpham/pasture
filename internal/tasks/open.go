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
			What:     "Couldn't create the folder for the task database.",
			Why: fmt.Sprintf(
				"Tried to create %q so the database file %q could live there, but the\n"+
					"operating system rejected it: %s",
				filepath.Dir(dbPath), dbPath, err,
			),
			Impact: "No task commands will work until the folder exists and is writable.",
			Fix: fmt.Sprintf("1. Create the folder yourself:\n"+
				"     mkdir -p %q\n"+
				"2. Or point pasture at a folder you can write to:\n"+
				"     pasture task --db <writable-path> ...\n"+
				"   You can also set the environment variable %s.",
				filepath.Dir(dbPath), DBPathEnv),
		}
	}

	tr, err := provenance.OpenSQLite(dbPath)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     "Couldn't open the task database.",
			Why: fmt.Sprintf(
				"Tried to open the SQLite file at %q but it failed: %s",
				dbPath, err,
			),
			Impact: "Task commands need a working database — none will succeed until it opens.",
			Fix: fmt.Sprintf("1. Confirm the file exists and is a valid SQLite database:\n"+
				"     sqlite3 %q .schema\n"+
				"2. If the file is corrupt or empty, move it aside and retry to start fresh:\n"+
				"     mv %q %q.broken\n"+
				"3. Or point pasture at a different file:\n"+
				"     pasture task --db <path> ...\n"+
				"   You can also set the environment variable %s.",
				dbPath, dbPath, dbPath, DBPathEnv),
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
			What:     "Couldn't figure out which project this task belongs to.",
			Why: fmt.Sprintf(
				"You didn't pass --namespace, and we tried to guess one from the current\n"+
					"folder's git remote (or its path) but couldn't: %s",
				err,
			),
			Impact: "A task needs a project name, so creation can't continue without one.",
			Fix: "1. Pass a project name explicitly when creating the task:\n" +
				"     pasture task create --namespace <project> ...\n" +
				"2. Or run the command from inside a git checkout that has a remote set,\n" +
				"   so we can infer the project name from it:\n" +
				"     git remote -v",
		}
	}
	return ns, nil
}
