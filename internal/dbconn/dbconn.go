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
