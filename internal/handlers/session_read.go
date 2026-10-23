// Package handlers — session_read.go
//
// Read-only session inspection for `pasture session list` and
// `pasture session show`.
//
// The read path mirrors the serve setup: open the unified store so the
// journal-aware cache rebuild can run, ensure the session cache is current,
// then reopen the same file read-only and serve the projection from
// internal/sessionapi.
//
// "Read-only" describes the operator surface, not the bytes on disk: refreshing
// the cache rewrites the disposable pasture_session_state rows from the journal
// and may close a binding the journal already ended. These commands never
// record an explicit bind or unbind, start a workflow, or contact a daemon, and
// they refuse to create a store that does not already exist.
package handlers

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
)

// SessionListInput captures the inputs for `pasture session list`.
type SessionListInput struct {
	// DBPath is the unified pasture.db path. Empty resolves to
	// tasks.DefaultDBPath().
	DBPath string
	// Harness filters by harness identity (for example claude-code).
	Harness string
	// Epoch filters by the session's effective epoch binding (explicit when the
	// session has one, otherwise the open inferred derivation).
	Epoch string
	// State filters by derived activity state. Empty means every state.
	State string
	// Limit caps the returned page. It must be between 1 and 100; an explicit
	// zero is rejected rather than silently replaced by a default.
	Limit int
	// Cursor resumes after a previous page's next cursor.
	Cursor string
	// Output receives the rendered page. Nil writes to standard output.
	Output io.Writer
}

// SessionShowInput captures the inputs for `pasture session show`.
type SessionShowInput struct {
	// DBPath is the unified pasture.db path. Empty resolves to
	// tasks.DefaultDBPath().
	DBPath string
	// Harness is the canonical harness identity.
	Harness string
	// Session is the harness-native session ID.
	Session string
	// Output receives the rendered detail. Nil writes to standard output.
	Output io.Writer
}

// SessionList reads one page of recorded sessions and renders it in the
// requested format.
//
// The list path first ensures the session cache reflects the lifecycle journal,
// exactly as serve does, so a freshly recorded event is visible without a
// separate backfill command.
//
// Exit codes: 0=success, 1=validation, 2=connection (open failed),
// 3=workflow (not found), 5=storage (schema or cache failure).
func SessionList(ctx context.Context, in SessionListInput, format types.OutputFormat) (int, error) {
	state, err := sessionStateFilter(in.State)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	if in.Limit < 1 || in.Limit > 100 {
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The --limit value %d is outside the supported range.", in.Limit),
			Why:      "One session page carries between 1 and 100 rows; zero is not a request for the default.",
			Where:    "Listing sessions (internal/handlers/session_read.go in handlers.SessionList).",
			Impact:   "No sessions were read.",
			Fix:      "Pass --limit with a value from 1 to 100, or omit it to use the default of 50.",
		}
		return pasterrors.ExitCode(e), e
	}

	service, cleanup, err := openSessionReadService(ctx, in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer cleanup()

	page, err := service.List(nonNilContext(ctx), sessionapi.ListOptions{
		Harness: in.Harness,
		Epoch:   in.Epoch,
		State:   state,
		Limit:   in.Limit,
		Cursor:  in.Cursor,
	})
	if err != nil {
		wrapped := sessionReadError(err, in.DBPath)
		return pasterrors.ExitCode(wrapped), wrapped
	}

	out, err := formatters.FormatSessionPage(page, format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(sessionOutput(in.Output), out)
	return 0, nil
}

// SessionShow reads one recorded session and renders its detail view.
//
// A session that the cache does not know is a non-zero exit: the caller asked
// for a specific identity and did not get it.
//
// Exit codes: 0=success, 1=validation, 2=connection (open failed),
// 3=workflow (not found), 5=storage (schema or cache failure).
func SessionShow(ctx context.Context, in SessionShowInput, format types.OutputFormat) (int, error) {
	harness := strings.TrimSpace(in.Harness)
	session := strings.TrimSpace(in.Session)
	if harness == "" || session == "" {
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Both a harness and a session ID are required.",
			Why:      "The show command identifies one session by its harness and native session ID.",
			Where:    "Showing a session (internal/handlers/session_read.go in handlers.SessionShow).",
			Impact:   "No session was read.",
			Fix:      "Pass the harness and session ID, for example:\n     pasture session show claude-code <session-id>",
		}
		return pasterrors.ExitCode(e), e
	}

	service, cleanup, err := openSessionReadService(ctx, in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer cleanup()

	detail, err := service.Get(nonNilContext(ctx), harness, session)
	if err != nil {
		var wrapped error
		if stderrors.Is(err, sessionapi.ErrNotFound) {
			wrapped = &pasterrors.StructuredError{
				Category: pasterrors.CategoryWorkflow,
				What:     fmt.Sprintf("No recorded session matches harness %q and session %q.", harness, session),
				Why:      "The session cache has no row for the requested identity; the session was never observed, or an identifier is misspelled.",
				Where:    "Showing a session (internal/handlers/session_read.go in handlers.SessionShow).",
				Impact:   "There is no detail view to render.",
				Fix: "1. List the recorded sessions to confirm the identity:\n" +
					"     pasture session list\n" +
					"2. Retry with the exact harness and native session ID.",
				Cause: err,
			}
		} else {
			wrapped = sessionReadError(err, in.DBPath)
		}
		return pasterrors.ExitCode(wrapped), wrapped
	}

	out, err := formatters.FormatSessionDetail(detail, format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(sessionOutput(in.Output), out)
	return 0, nil
}

// openSessionReadService mirrors the serve read setup: open the store so the
// journal-aware rebuild can run, ensure the cache is current, then open the
// same file read-only for the projection reads.
//
// The caller must already have an existing store. These commands never create
// one: a read against an absent --db exits before the tracker opener, which
// would otherwise migrate a brand-new file into existence.
func openSessionReadService(ctx context.Context, dbPath string) (*sessionapi.Service, func(), error) {
	if dbPath == "" {
		dbPath = tasks.DefaultDBPath()
	}
	if err := requireExistingStore(dbPath); err != nil {
		return nil, nil, err
	}
	workCtx := nonNilContext(ctx)

	tracker, err := tasks.OpenTaskTrackerContext(workCtx, dbPath)
	if err != nil {
		return nil, nil, sessionOpenError(dbPath, err)
	}
	if _, _, err := tasks.EnsureSessionState(workCtx, tracker); err != nil {
		_ = tracker.Close()
		return nil, nil, sessionCacheError(dbPath, err)
	}

	readDB, err := dbconn.OpenReadOnlyDB(dbPath)
	if err != nil {
		_ = tracker.Close()
		return nil, nil, sessionOpenError(dbPath, err)
	}
	if err := tasks.CheckSchemaVersion(readDB, dbPath); err != nil {
		_ = readDB.Close()
		_ = tracker.Close()
		return nil, nil, err
	}
	service := sessionapi.NewService(readDB)
	return service, func() {
		_ = readDB.Close()
		_ = tracker.Close()
	}, nil
}

// requireExistingStore refuses to start a session read against a path that is
// not already a readable file. The tracker opener creates and migrates missing
// stores, so without this check a mistyped --db would leave a brand-new 1 MB
// database behind and exit 0 with an empty list.
func requireExistingStore(dbPath string) error {
	info, err := os.Stat(dbPath)
	if err != nil {
		if stderrors.Is(err, fs.ErrNotExist) {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryConnection,
				What:     fmt.Sprintf("No pasture database exists at %q.", dbPath),
				Why:      "The session commands read an existing store; they never create one.",
				Where:    "Checking for the database before reading sessions (internal/handlers/session_read.go in handlers.requireExistingStore).",
				Impact:   "No sessions can be listed or shown until a store exists at that path.",
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
			Where:    "Checking for the database before reading sessions (internal/handlers/session_read.go in handlers.requireExistingStore).",
			Impact:   "No sessions can be listed or shown until the path is accessible.",
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
			Where:    "Checking for the database before reading sessions (internal/handlers/session_read.go in handlers.requireExistingStore).",
			Impact:   "No sessions can be listed or shown from that path.",
			Fix: fmt.Sprintf("Point --db or PASTURE_DB_PATH at the database file itself, for example:\n"+
				"     %q", filepath.Join(dbPath, "pasture.db")),
		}
	}
	file, err := os.Open(dbPath)
	if err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("The pasture database at %q is not readable.", dbPath),
			Why:      "The file exists but this user cannot open it for reading.",
			Where:    "Checking for the database before reading sessions (internal/handlers/session_read.go in handlers.requireExistingStore).",
			Impact:   "No sessions can be listed or shown until the file is readable.",
			Fix: fmt.Sprintf("1. Confirm the file permissions allow reading:\n"+
				"     ls -l %q\n"+
				"2. Correct the permissions or run the command as the file's owner, then retry.", dbPath),
			Cause: err,
		}
	}
	_ = file.Close()
	return nil
}

func sessionStateFilter(value string) (sessionapi.ActivityState, error) {
	if value == "" {
		return "", nil
	}
	switch state := sessionapi.ActivityState(value); state {
	case sessionapi.StateActive, sessionapi.StateIdle, sessionapi.StateEnded, sessionapi.StateUnknown:
		return state, nil
	default:
		e := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Unknown session state %q.", value),
			Why:      "The --state filter accepts only active, idle, ended, or unknown.",
			Where:    "Filtering sessions (internal/handlers/session_read.go in handlers.sessionStateFilter).",
			Impact:   "No sessions were read.",
			Fix:      "Pass --state active, --state idle, --state ended, or --state unknown.",
		}
		return "", e
	}
}

// sessionOpenError translates a store-open or schema failure into an actionable
// connection error while preserving an already-structured cause.
func sessionOpenError(dbPath string, err error) error {
	var se *pasterrors.StructuredError
	if stderrors.As(err, &se) {
		return err
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryConnection,
		What:     fmt.Sprintf("Couldn't open the pasture database at %q to read sessions.", dbPath),
		Why:      "The database file could not be opened for the session read.",
		Where:    "Opening the database for session reads (internal/handlers/session_read.go in handlers.openSessionReadService).",
		Impact:   "No recorded sessions can be read until the database opens.",
		Fix: fmt.Sprintf("1. Confirm the database path exists and is readable:\n"+
			"     ls -l %q\n"+
			"2. Initialize a new store with any write command, for example:\n"+
			"     pasture task list --namespace <namespace>\n"+
			"3. Or point --db or PASTURE_DB_PATH at an existing pasture database.", dbPath),
		Cause: err,
	}
}

// sessionCacheError translates a session-cache rebuild failure. The rebuild is
// the only write the read command performs, and a failure there means the cache
// cannot be trusted rather than that the requested session is missing.
func sessionCacheError(dbPath string, err error) error {
	var se *pasterrors.StructuredError
	if stderrors.As(err, &se) {
		return err
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("Couldn't refresh the session cache at %q.", dbPath),
		Why:      "The journal-aware session-state rebuild failed, so the cache may not reflect the recorded lifecycle events.",
		Where:    "Refreshing the session cache (internal/handlers/session_read.go in handlers.openSessionReadService).",
		Impact:   "Session reads stop rather than showing a stale or partial cache.",
		Fix: fmt.Sprintf("1. Confirm the database file is writable and has free space:\n"+
			"     ls -l %q\n"+
			"2. Retry after any other pasture writer finishes.\n"+
			"3. If the store is damaged, restore a backup.", dbPath),
		Cause: err,
	}
}

// sessionReadError maps the session API's sentinel errors onto actionable
// categories. ErrNotFound is a workflow error because the caller asked for a
// specific session that the store does not record.
func sessionReadError(err error, dbPath string) error {
	var se *pasterrors.StructuredError
	if stderrors.As(err, &se) {
		return err
	}
	switch {
	case stderrors.Is(err, sessionapi.ErrInvalidFilter):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A session filter value is not supported.",
			Why:      err.Error(),
			Where:    "Filtering sessions (internal/handlers/session_read.go in handlers.sessionReadError).",
			Impact:   "No sessions were read.",
			Fix:      "Check --harness, --epoch, --state, and --limit, then retry.",
			Cause:    err,
		}
	case stderrors.Is(err, sessionapi.ErrInvalidPageToken):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The --cursor value is not a valid page cursor.",
			Why:      err.Error(),
			Where:    "Resuming a session page (internal/handlers/session_read.go in handlers.sessionReadError).",
			Impact:   "No sessions were read.",
			Fix:      "Omit --cursor to start from the first page, or pass the exact cursor printed by a previous list.",
			Cause:    err,
		}
	case stderrors.Is(err, sessionapi.ErrStaleCache):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("The session cache at %q is marked stale.", dbPath),
			Why:      "The cache changed between the refresh and the read, so the projection cannot be trusted for this request.",
			Where:    "Reading sessions (internal/handlers/session_read.go in handlers.sessionReadError).",
			Impact:   "No session list was returned.",
			Fix:      "Retry the command, which refreshes the cache; if it keeps failing, run `pasture serve --backfill` to rebuild the cache from the journal.",
			Cause:    err,
		}
	default:
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryStorage,
			What:     fmt.Sprintf("Couldn't read sessions from %q.", dbPath),
			Why:      "The session projection query failed.",
			Where:    "Reading sessions (internal/handlers/session_read.go in handlers.sessionReadError).",
			Impact:   "No session result is available.",
			Fix:      "1. Confirm the database file is readable and its schema is current:\n     pasture migrate\n2. Retry the command.",
			Cause:    err,
		}
	}
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func sessionOutput(w io.Writer) io.Writer {
	if w == nil {
		return os.Stdout
	}
	return w
}
