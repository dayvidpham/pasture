// Package handlers — project_read.go
//
// The explicit project-to-epoch mapping surface for `pasture project list`,
// `show`, `bind`, and `unbind`.
//
// The mapping is a project-level relationship owned by Pasture; Peasant owns the
// project hash. These commands never read or write session bindings, and the
// session read model never consults the project mapping table.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// ProjectListInput captures the inputs for `pasture project list`.
type ProjectListInput struct {
	// DBPath is the unified pasture.db path. Empty resolves to
	// tasks.DefaultDBPath().
	DBPath string
	// Output receives the rendered page. Nil writes to standard output.
	Output io.Writer
}

// ProjectShowInput captures the inputs for `pasture project show`.
type ProjectShowInput struct {
	DBPath      string
	ProjectHash string
	Output      io.Writer
}

// ProjectBindInput captures the inputs for `pasture project bind`.
type ProjectBindInput struct {
	DBPath          string
	ProjectHash     string
	EpochID         string
	Role            string
	ExpectedBinding string
	Output          io.Writer
}

// ProjectUnbindInput captures the inputs for `pasture project unbind`.
type ProjectUnbindInput struct {
	DBPath          string
	ProjectHash     string
	ExpectedBinding string
	Output          io.Writer
}

// ProjectList renders every project with an active epoch mapping.
//
// Exit codes: 0=success, 1=validation, 2=connection (open failed),
// 5=storage (schema or transaction failure).
func ProjectList(ctx context.Context, in ProjectListInput, format types.OutputFormat) (int, error) {
	store, cleanup, err := openProjectStore(ctx, in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer cleanup()

	mappings, err := store.List(nonNilContext(ctx))
	if err != nil {
		wrapped := projectCommandError(err, in.DBPath, "listing projects")
		return pasterrors.ExitCode(wrapped), wrapped
	}
	out, err := formatters.FormatProjectPage(sessionapi.ProjectPageFromMappings(mappings), format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(sessionOutput(in.Output), out)
	return 0, nil
}

// ProjectShow renders one project's active mapping and full interval history.
// A project hash with no mapping history is a workflow error.
//
// Exit codes: 0=success, 1=validation, 2=connection, 3=not found,
// 5=storage.
func ProjectShow(ctx context.Context, in ProjectShowInput, format types.OutputFormat) (int, error) {
	store, cleanup, err := openProjectStore(ctx, in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer cleanup()

	mapping, err := store.Get(nonNilContext(ctx), in.ProjectHash)
	if err != nil {
		wrapped := projectCommandError(err, in.DBPath, "showing a project")
		return pasterrors.ExitCode(wrapped), wrapped
	}
	out, err := formatters.FormatProjectDetail(sessionapi.ProjectViewFromMapping(mapping), format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(sessionOutput(in.Output), out)
	return 0, nil
}

// ProjectBind maps a project to an epoch. A previous active mapping is closed;
// the returned view carries the new active binding and the full history.
//
// Exit codes: 0=success, 1=validation, 2=connection, 3=unknown epoch or
// conflict, 5=storage.
func ProjectBind(ctx context.Context, in ProjectBindInput, format types.OutputFormat) (int, error) {
	store, cleanup, err := openProjectStore(ctx, in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer cleanup()

	mapping, err := store.Bind(nonNilContext(ctx), in.ProjectHash, tasks.ProjectBindRequest{
		EpochID:         in.EpochID,
		Role:            in.Role,
		ExpectedBinding: in.ExpectedBinding,
	})
	if err != nil {
		wrapped := projectCommandError(err, in.DBPath, "binding a project")
		return pasterrors.ExitCode(wrapped), wrapped
	}
	out, err := formatters.FormatProjectDetail(sessionapi.ProjectViewFromMapping(mapping), format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(sessionOutput(in.Output), out)
	return 0, nil
}

// ProjectUnbind closes a project's active epoch mapping and keeps the history.
//
// Exit codes: 0=success, 1=validation, 2=connection, 3=no open mapping or
// conflict, 5=storage.
func ProjectUnbind(ctx context.Context, in ProjectUnbindInput, format types.OutputFormat) (int, error) {
	store, cleanup, err := openProjectStore(ctx, in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer cleanup()

	mapping, err := store.Unbind(nonNilContext(ctx), in.ProjectHash, in.ExpectedBinding)
	if err != nil {
		wrapped := projectCommandError(err, in.DBPath, "unbinding a project")
		return pasterrors.ExitCode(wrapped), wrapped
	}
	out, err := formatters.FormatProjectDetail(sessionapi.ProjectViewFromMapping(mapping), format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(sessionOutput(in.Output), out)
	return 0, nil
}

// openProjectStore opens the existing unified store and borrows its write handle
// for the project mapping. The project commands never create a store: a mistyped
// --db must fail instead of leaving a fresh database behind.
func openProjectStore(ctx context.Context, dbPath string) (*tasks.ProjectAPIStore, func(), error) {
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	if err := requireExistingProjectStore(dbPath); err != nil {
		return nil, nil, err
	}
	if err := ensureSQLiteStoreFile(dbPath); err != nil {
		return nil, nil, projectOpenError(dbPath, err)
	}
	workCtx := nonNilContext(ctx)
	tracker, err := tasks.OpenTaskTrackerContext(workCtx, dbPath)
	if err != nil {
		return nil, nil, projectOpenError(dbPath, err)
	}
	store, err := tasks.NewProjectAPIStore(tracker)
	if err != nil {
		_ = tracker.Close()
		return nil, nil, projectOpenError(dbPath, err)
	}
	return store, func() { _ = tracker.Close() }, nil
}

// requireExistingProjectStore refuses to create a database for a project
// command. A read against a missing path is a connection error, and a write
// against it would otherwise initialize an empty store before failing.
func requireExistingProjectStore(dbPath string) error {
	info, err := os.Stat(dbPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryConnection,
				What:     fmt.Sprintf("No pasture database exists at %q.", dbPath),
				Why:      "The project commands operate on an existing unified store; they never create one.",
				Where:    "Checking for the database before reading or writing project mappings (internal/handlers/project_read.go in handlers.requireExistingProjectStore).",
				Impact:   "No project mapping can be read or changed until a store exists at that path.",
				Fix: fmt.Sprintf("1. Create and initialize the store with any write command, for example:\n"+
					"     pasture task list --namespace <namespace>\n"+
					"2. Or point --db or PASTURE_DB_PATH at an existing pasture database.\n"+
					"   Checked path: %q", dbPath),
				Cause: err,
			}
		}
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't inspect the pasture database path %q.", dbPath),
			Why:      "The path, or one of its parent directories, is not accessible.",
			Where:    "Checking for the database before reading or writing project mappings (internal/handlers/project_read.go in handlers.requireExistingProjectStore).",
			Impact:   "No project mapping can be read or changed until the path is accessible.",
			Fix: fmt.Sprintf("1. Confirm the path and every parent directory exist and are searchable:\n"+
				"     ls -ld %q\n"+
				"2. Correct the permission or path problem, then retry.\n"+
				"3. Or point --db or PASTURE_DB_PATH at an existing pasture database.", dbPath),
			Cause: err,
		}
	}
	if info.IsDir() {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("The pasture database path %q is a directory.", dbPath),
			Why:      "A directory cannot be opened as a SQLite database.",
			Where:    "Checking for the database before reading or writing project mappings (internal/handlers/project_read.go in handlers.requireExistingProjectStore).",
			Impact:   "No project mapping can be read or changed from that path.",
			Fix: fmt.Sprintf("Point --db or PASTURE_DB_PATH at the database file itself, for example:\n"+
				"     %q", dbPath+"/pasture.db"),
		}
	}
	return nil
}

// ensureSQLiteStoreFile refuses a non-empty file whose bytes are not a SQLite
// database. The tracker's auto-on-open path can initialize an empty file, so an
// empty file is left to it. A non-SQLite file cannot become a store; reporting
// that here keeps the failure in the connection category instead of surfacing
// as a storage fault from deep inside the migrator.
func ensureSQLiteStoreFile(dbPath string) error {
	info, err := os.Stat(dbPath)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	file, err := os.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	const sqliteHeader = "SQLite format 3\x00"
	header := make([]byte, len(sqliteHeader))
	n, readErr := io.ReadFull(file, header)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if n > 0 && string(header[:n]) != sqliteHeader {
		return fmt.Errorf("%q is not a SQLite database: its first %d bytes are not the SQLite file header; point --db or PASTURE_DB_PATH at the pasture database file, or restore the damaged file", dbPath, n)
	}
	return nil
}

func projectOpenError(dbPath string, err error) error {
	var se *pasterrors.StructuredError
	if errors.As(err, &se) {
		return err
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryConnection,
		What:     fmt.Sprintf("Couldn't open the pasture database at %q for project mappings.", dbPath),
		Why:      "The unified database file could not be opened for the project mapping command.",
		Where:    "Opening the database for project mappings (internal/handlers/project_read.go in handlers.openProjectStore).",
		Impact:   "No project mapping was read or changed.",
		Fix: fmt.Sprintf("1. Confirm the database path exists and is readable:\n"+
			"     ls -l %q\n"+
			"2. Retry once any other pasture writer finishes.\n"+
			"3. If the store is damaged, restore a backup.", dbPath),
		Cause: err,
	}
}

// projectCommandError maps the project store sentinels onto actionable
// categories. Request mistakes are validation; unknown epochs, missing
// projects, and conflicts are workflow outcomes; handle/transaction failures
// are storage.
func projectCommandError(err error, dbPath, action string) error {
	var se *pasterrors.StructuredError
	if errors.As(err, &se) {
		return err
	}
	switch {
	case errors.Is(err, tasks.ErrProjectRequest):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A project mapping value is not valid.",
			Why:      err.Error(),
			Where:    "Validating a project mapping request (internal/handlers/project_read.go in handlers.projectCommandError).",
			Impact:   "No project mapping was read or changed.",
			Fix:      "Pass the 64-character lowercase project hash reported by Peasant and a valid epoch ID; --role accepts epoch, architect, reviewer, supervisor, or worker.",
			Cause:    err,
		}
	case errors.Is(err, tasks.ErrUnknownProjectEpoch):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "The requested epoch does not exist in the task store.",
			Why:      err.Error(),
			Where:    "Resolving the epoch while binding a project (internal/handlers/project_read.go in handlers.projectCommandError).",
			Impact:   "No project mapping was changed.",
			Fix:      "Create the epoch first, or correct --epoch to an existing epoch task ID, then retry.",
			Cause:    err,
		}
	case errors.Is(err, tasks.ErrProjectNotFound):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "That project has no mapping history in this store.",
			Why:      err.Error(),
			Where:    "Reading a project mapping (internal/handlers/project_read.go in handlers.projectCommandError).",
			Impact:   "There is no project detail to render.",
			Fix:      "Bind the project first, for example:\n     pasture project bind <projectHash> --epoch <epoch-id>",
			Cause:    err,
		}
	case errors.Is(err, tasks.ErrProjectNoOpenBinding):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "The project has no active mapping to close.",
			Why:      err.Error(),
			Where:    "Unbinding a project (internal/handlers/project_read.go in handlers.projectCommandError).",
			Impact:   "No project mapping was changed.",
			Fix:      "Run `pasture project show <projectHash>` to inspect its history, or bind an epoch first.",
			Cause:    err,
		}
	case errors.Is(err, tasks.ErrProjectBindingConflict):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     "The project mapping changed since the expected binding was read.",
			Why:      err.Error(),
			Where:    "Applying a conditional project binding change (internal/handlers/project_read.go in handlers.projectCommandError).",
			Impact:   "No project mapping was changed.",
			Fix:      "Read the current mapping with `pasture project show <projectHash>`, then retry with the current binding ID or omit --expected-binding.",
			Cause:    err,
		}
	default:
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't %s in %q.", action, dbPath),
			Why:      "The project mapping store could not complete the operation.",
			Where:    "Reading or writing project mappings (internal/handlers/project_read.go in handlers.projectCommandError).",
			Impact:   "No project mapping result is available.",
			Fix:      "1. Confirm the database file is readable and writable and has free space.\n2. Retry once any other pasture writer finishes.\n3. If the problem persists, run `pasture migrate` and check the serve log.",
			Cause:    err,
		}
	}
}
