// Package dbconn centralizes how every pasture component opens a modernc
// SQLite handle on the shared pasture.db file. Putting the connection-string
// contract in one leaf package (no pasture deps beyond errors) lets the audit
// trail, the task tracker, and the durable engine all open the file with the
// identical WAL/concurrency configuration without an import cycle.
package dbconn

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver; CGO_ENABLED=0 compatible

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// SharedDSN builds the connection string used for every modernc handle on the
// unified pasture.db file. It encodes the concurrency contract as DSN params
// rather than runtime PRAGMAs so a caller-supplied handle (the one passed to
// DBOS as SqliteSystemDB, which skips DBOS's own PRAGMA setup) is configured
// identically to the audit and provenance handles:
//
//   - journal_mode(WAL)      multi-reader / single-writer without reader stalls
//   - busy_timeout(5000)     auto-retry a locked write for up to 5s
//   - synchronous(NORMAL)    durable under WAL without an fsync per commit
//   - foreign_keys(ON)       enforce FK constraints (provenance relies on them)
//   - _txlock=immediate      BEGIN IMMEDIATE so a write txn holds the lock from
//     its first statement (migration concurrency-safety)
func SharedDSN(path string) string {
	return "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_txlock=immediate"
}

// ReadOnlyDSN builds a connection string that opens the file without creating
// it and without modifying any data. mode=ro prevents any write and prevents
// file creation (SQLite returns SQLITE_CANTOPEN if the file does not exist).
//
// busy_timeout mirrors the writer DSN's 5 s auto-retry. WAL readers normally
// don't block on writers, but they can receive SQLITE_BUSY during wal-index
// recovery or a checkpoint restart. The primary use case — reading status
// while the daemon is actively writing — demands the same retry window as the
// shared path; without it, those rare contention windows surface as raw lock
// errors instead of a transparent 5 s retry.
func ReadOnlyDSN(path string) string {
	return "file:" + path +
		"?mode=ro" +
		"&_pragma=busy_timeout(5000)"
}

// OpenSharedDB opens a modernc *sql.DB on path using SharedDSN. Unlike the
// pre-DBOS handles, it does NOT pin MaxOpenConns(1): the WAL multi-writer model
// (busy_timeout + _txlock=immediate) serializes writers at the file level, and
// the DBOS notification poller needs a second connection to make progress while
// a workflow step holds one.
func OpenSharedDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", SharedDSN(path))
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     "Couldn't open the shared pasture database handle for the durable engine.",
			Why: fmt.Sprintf(
				"Tried to open %q with the WAL/busy-timeout connection string the engine and DBOS share, but it failed.",
				path,
			),
			Where:  "Opening the shared database handle (internal/dbconn/dbconn.go in dbconn.OpenSharedDB).",
			Impact: "The durable engine can't start, so epochs can't run or resume until the handle opens.",
			Fix: fmt.Sprintf("1. Confirm the database file and its folder are writable:\n"+
				"     ls -l %q\n"+
				"2. Confirm no other process holds it exclusively:\n"+
				"     pgrep -af pasture\n"+
				"3. Point pasture at a writable file if needed (PASTURE_DB_PATH).", path),
			Cause: err,
		}
	}
	return db, nil
}

// OpenReadOnlyDB opens a modernc *sql.DB on path in read-only mode (mode=ro).
// Unlike OpenSharedDB, it never creates the file: if path does not exist the
// call fails immediately with a CategoryConnection error. The returned handle
// must not be used for any write or DDL operation.
func OpenReadOnlyDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", ReadOnlyDSN(path))
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("Couldn't open %q in read-only mode.", path),
			Why:      "The SQLite driver returned an error when opening the file read-only.",
			Where:    "Opening the read-only database handle (internal/dbconn/dbconn.go in dbconn.OpenReadOnlyDB).",
			Impact:   "The operation that needs to read this file can't proceed.",
			Fix: fmt.Sprintf("1. Confirm the file exists and is readable:\n"+
				"     ls -l %q\n"+
				"2. If the file was never created, start the daemon first:\n"+
				"     pastured --db %q\n"+
				"3. To override the path, set PASTURE_DB_PATH or pass --db.", path, path),
			Cause: err,
		}
	}
	// Ping to detect a missing file early (mode=ro fails to open a non-existent
	// file at first use, not at sql.Open time).
	if pingErr := db.Ping(); pingErr != nil {
		db.Close()
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("No pasture database found at %q.", path),
			Why:      "The database file does not exist or is not readable in read-only mode.",
			Where:    "Opening the read-only database handle (internal/dbconn/dbconn.go in dbconn.OpenReadOnlyDB).",
			Impact:   "No epoch state or audit history can be read — no epochs have run, or the daemon has not started yet.",
			Fix: fmt.Sprintf("1. Start the daemon to create and initialize the database:\n"+
				"     pastured\n"+
				"2. Then run an epoch:\n"+
				"     pasture epoch start --epoch-id <id>\n"+
				"3. To use a different database path, pass --db or set PASTURE_DB_PATH.\n"+
				"   Expected location: %q", path),
			Cause: pingErr,
		}
	}
	return db, nil
}
