package testutil

// durableschema.go builds and inspects the databases that the durable schema
// gate has to judge, for every package that opens a durable runtime.
//
// It lives here, not in one package's test file, because the same three
// databases are needed wherever a durable client or context is constructed —
// today the engine and the epoch controller — and a second copy of these facts
// would drift from the first.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/dbconn"
	_ "modernc.org/sqlite"
)

// SupersededDurableSchemaVersion is the layout version the superseded durable
// runtime stopped at, and therefore the version WriteSupersededDurableDatabase
// records.
const SupersededDurableSchemaVersion = 41

// FirstSupportedDurableSchemaVersion is the floor the gate enforces: the first
// layout version this build's durable runtime introduces. A refusal names it,
// so a floor that ever moves fails the tests that assert it instead of passing
// silently.
const FirstSupportedDurableSchemaVersion = 42

// DurableMigrationTable is the single-row table the durable runtime keeps its
// layout version in.
//
// Evidence in the library version pinned in go.mod:
// dbos/internal/sysdb/sqlite_migrations.go, where RunSqliteMigrations creates
// `dbos_migrations (version INTEGER NOT NULL PRIMARY KEY)` and reads a single
// row from it. BuildSqliteMigrations in the same file lists the SQLite
// migrations 1..41 and then continues at 42, so 41 is the last version the
// superseded runtime can leave behind.
const DurableMigrationTable = "dbos_migrations"

// WriteSupersededDurableDatabase writes a private database whose durable layout
// is the one the superseded runtime left behind, and returns its path with the
// digest of the whole database. The digest lets a caller prove that a refusal
// wrote nothing.
//
// The file is written through the PRODUCTION opener, so it arrives in the exact
// shape a real pasture database has: WAL journal mode and the same pragmas. A
// fixture written on plainer settings would be converted to WAL by the first
// production open, and that conversion alone rewrites the file header — which
// would read, to a digest assertion, as a writer the gate failed to hold back.
//
// That header rewrite is the ONE change a digest cannot cover by construction:
// the shared handle's connection string sets the journal mode, and that pragma
// runs on the gate's own first query, before any refusal is possible. Such a
// database carries no pasture data, and the refusal says exactly that rather
// than claiming the file is untouched.
func WriteSupersededDurableDatabase(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	db, err := dbconn.OpenSharedDB(path)
	if err != nil {
		t.Fatalf("open %s with the production opener: %v", path, err)
	}
	if _, err := db.ExecContext(t.Context(),
		"CREATE TABLE "+DurableMigrationTable+" (version INTEGER NOT NULL PRIMARY KEY)"); err != nil {
		_ = db.Close()
		t.Fatalf("create the %s table in %s: %v", DurableMigrationTable, path, err)
	}
	if _, err := db.ExecContext(t.Context(),
		"INSERT INTO "+DurableMigrationTable+" (version) VALUES (?)", SupersededDurableSchemaVersion); err != nil {
		_ = db.Close()
		t.Fatalf("record layout version %d in %s: %v", SupersededDurableSchemaVersion, path, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close %s after writing the superseded layout: %v", path, err)
	}
	return path, DatabaseDigest(t, path)
}

// DatabaseDigest hashes a SQLite database as a whole: the main file AND its two
// sidecars, each length-prefixed so no rearrangement of bytes between them can
// collide.
//
// Hashing the main file alone is not enough, and that gap is not theoretical. A
// writer working under WAL journal mode leaves its pages in the -wal sidecar
// until a checkpoint runs, so a migration of hundreds of kilobytes can be
// complete and durable while the main file is untouched. Any later reader —
// including an older pasture build — replays that sidecar on open and sees the
// change. A main-file digest therefore reports "nothing was written" for a
// database that was, in fact, already rewritten.
//
// TAKE IT WHILE NO HANDLE IS OPEN. An open connection under WAL keeps a -shm
// sidecar alive, and this digest counts the sidecars, so a digest taken with a
// reader still attached differs from one taken after it closed. Production
// leaves no sidecar behind, because a refusal closes the handle it opened.
//
// A missing sidecar counts as empty, which is the normal state once the last
// handle on the file closes.
func DatabaseDigest(t *testing.T, path string) string {
	t.Helper()
	h := sha256.New()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		part := path + suffix
		data, err := os.ReadFile(part)
		if errors.Is(err, os.ErrNotExist) {
			data = nil
		} else if err != nil {
			t.Fatalf("read %s: %v", part, err)
		}
		fmt.Fprintf(h, "%s:%d:", suffix, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReadDurableSchemaVersion reports the layout version a database records, or 0
// when it records none.
func ReadDurableSchemaVersion(t *testing.T, path string) int64 {
	t.Helper()
	db := openReadOnlyProbe(t, path)
	var version int64
	err := db.QueryRowContext(t.Context(),
		"SELECT version FROM "+DurableMigrationTable+" LIMIT 1").Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read the recorded layout version from %s: %v", path, err)
	}
	return version
}

// DurableRuntimeTables reports which of the durable runtime's own tables are
// present. The gate must leave every one of them absent: each is created by the
// first layout steps the runtime applies, so any one of them proves the runtime
// ran against the file.
func DurableRuntimeTables(t *testing.T, path string) []string {
	t.Helper()
	present := []string{}
	all := AllTables(t, path)
	for _, name := range []string{"workflow_status", "operation_outputs", "notifications", "workflow_queue"} {
		for _, have := range all {
			if have == name {
				present = append(present, name)
				break
			}
		}
	}
	return present
}

// AllTables lists every table in a database, for a failure message that names
// what a writer created.
func AllTables(t *testing.T, path string) []string {
	t.Helper()
	db := openReadOnlyProbe(t, path)
	rows, err := db.QueryContext(t.Context(),
		"SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatalf("list the tables of %s: %v", path, err)
	}
	defer func() { _ = rows.Close() }()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan a table name of %s: %v", path, err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate the tables of %s: %v", path, err)
	}
	return names
}

// openReadOnlyProbe opens a READ-ONLY handle for the inspections above. Read
// only, because a probe must never be the writer a caller is hunting for: the
// production read-only connection string cannot create the file and cannot
// change a byte of it.
func openReadOnlyProbe(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := dbconn.OpenReadOnlyDB(path)
	if err != nil {
		t.Fatalf("open %s read-only: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
