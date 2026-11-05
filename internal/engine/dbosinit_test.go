package engine

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// fakeFactory records how many times it was called and replays a scripted
// sequence of results, so the retry loop is exercised without a second process,
// a real database, or any sleep-as-synchronisation.
type fakeFactory struct {
	errs     []error // errs[i] is returned on attempt i+1; nil means success
	attempts int
}

func (f *fakeFactory) new(context.Context, dbos.Config) (dbos.Context, error) {
	f.attempts++
	if f.attempts <= len(f.errs) {
		if err := f.errs[f.attempts-1]; err != nil {
			return nil, err
		}
	}
	// A nil durable context with a nil error is enough: newDurableContext only
	// passes the value through, it never dereferences it.
	return nil, nil
}

func reportContains(e *pasterrors.StructuredError, want string) bool {
	var buf bytes.Buffer
	e.Report(&buf)
	return strings.Contains(buf.String(), want)
}

// pinnedDBOSVersion is the durable-execution library version whose SQLite
// driver refusal message isMissingDBOSSQLiteDriver matches.
const pinnedDBOSVersion = "github.com/dbos-inc/dbos-transact-golang v1.8.0"

// TestDBOSVersionPinMatchesDriverPredicate fails when the durable-execution
// library is upgraded, because isMissingDBOSSQLiteDriver matches a MESSAGE TEXT
// the library owns. Re-read dbos/internal/sysdb/sqlite_driver.go and update
// dbosMissingSQLiteDriverMarker and unlinkedSQLiteDriverError together.
//
// The schema-bootstrap retry this file used to pin is gone: v1.8.0 made SQLite
// migrations safe when several processes open a new database at once, so the
// probe-then-create race no longer produces a message to match.
//
// A bump also has to re-verify the reserved queue's name and polling cadence,
// which the recovery pick-up ceiling is derived from. Those are pinned
// separately by TestReservedQueueFactsMatchThePinnedRuntime in
// internal/engine/subworkflows_test.go.
func TestDBOSVersionPinMatchesDriverPredicate(t *testing.T) {
	t.Parallel()
	gomod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod to verify the durable-execution version pin: %v", err)
	}
	if !strings.Contains(string(gomod), pinnedDBOSVersion) {
		t.Fatalf("go.mod no longer pins %q — the unlinked-driver predicate in "+
			"internal/engine/dbosinit.go matches that version's error text and must be "+
			"re-verified against the new one before this pin is updated", pinnedDBOSVersion)
	}
	if !isMissingDBOSSQLiteDriver(errors.New(unlinkedSQLiteDriverError)) {
		t.Fatal("isMissingDBOSSQLiteDriver no longer matches the unlinked-driver refusal of the pinned version")
	}
}

// TestEngineLinksDBOSSQLiteDriver pins the blank import that links the durable
// runtime's SQLite driver into every binary that builds a context in this
// package. Without it, context construction fails at run time and the runtime
// cannot tell a busy or locked database apart from a permanent failure. The
// import cannot be relied on transitively: another package's link is that
// package's business and may be dropped without notice.
func TestEngineLinksDBOSSQLiteDriver(t *testing.T) {
	t.Parallel()
	testutil.RequireBlankImport(t, "engine.go", dbosSQLiteDriverPackage)
}

// unlinkedSQLiteDriverMessage is the verbatim refusal the durable runtime
// produces when the binary never linked its SQLite driver. It was recorded from
// a real run of the pinned library version: a throwaway program that opened a
// database handle and called dbos.NewContext and dbos.NewClient WITHOUT the
// blank import produced exactly this message from both, inside a *dbos.Error
// whose code is Initialization.
//
// Keeping the real text here means a library upgrade that rewords the refusal
// fails TestDBOSVersionPinMatchesRacePredicate rather than silently turning the
// diagnosis off.
const unlinkedSQLiteDriverMessage = "Error initializing DBOS Transact: SQLite support is not linked into this binary: " +
	`add import _ "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite" to register the SQLite driver`

// unlinkedSQLiteDriverError is the full text of that failure as an operator
// sees it, i.e. the *dbos.Error rendering of unlinkedSQLiteDriverMessage.
const unlinkedSQLiteDriverError = "DBOS Error Initialization: " + unlinkedSQLiteDriverMessage

// newUnlinkedSQLiteDriverFailure builds the failure in the shape the runtime
// really returns: a *dbos.Error carrying the initialization code. The shape is
// asserted below, so a library change to either the code or the rendering is
// caught here too.
func newUnlinkedSQLiteDriverFailure() error {
	return &dbos.Error{Code: dbos.ErrorCodeInitialization, Message: unlinkedSQLiteDriverMessage}
}

// TestUnlinkedSQLiteDriverFailureShape pins the recorded failure to the shape
// the pinned library version produces, so the tests below run against the real
// error rather than a hand-written string that has drifted from it.
func TestUnlinkedSQLiteDriverFailureShape(t *testing.T) {
	t.Parallel()
	err := newUnlinkedSQLiteDriverFailure()
	if got := err.Error(); got != unlinkedSQLiteDriverError {
		t.Fatalf("rendered failure = %q, want %q", got, unlinkedSQLiteDriverError)
	}
	var dbosErr *dbos.Error
	if !errors.As(err, &dbosErr) {
		t.Fatalf("failure type = %T, want *dbos.Error", err)
	}
	if dbosErr.Code != dbos.ErrorCodeInitialization {
		t.Errorf("code = %v, want %v", dbosErr.Code, dbos.ErrorCodeInitialization)
	}
}

func TestNewDurableContext_UnlinkedSQLiteDriverFailsOnTheFirstAttempt(t *testing.T) {
	t.Parallel()
	unlinked := newUnlinkedSQLiteDriverFailure()
	f := &fakeFactory{errs: []error{unlinked, nil}}

	_, err := newDurableContext(context.Background(), f.new, dbos.Config{AppName: "pasture"})
	if err == nil {
		t.Fatal("newDurableContext with an unlinked SQLite driver: got success, want a failure on the first attempt")
	}
	if f.attempts != 1 {
		t.Errorf("attempts = %d, want 1 (an unlinked driver is permanent and must not be retried)", f.attempts)
	}
	if !errors.Is(err, unlinked) {
		t.Errorf("error = %v, want the runtime's own refusal kept as the cause", err)
	}
}

// TestNewDurableContext_UnlinkedSQLiteDriverIsActionable pins what the operator
// reads. The runtime's own text names the import, but nothing else: it does not
// say who failed, what it means for the caller, or that the repair is a source
// change rather than an operator action.
func TestNewDurableContext_UnlinkedSQLiteDriverIsActionable(t *testing.T) {
	t.Parallel()
	f := &fakeFactory{errs: []error{newUnlinkedSQLiteDriverFailure()}}

	_, err := newDurableContext(context.Background(), f.new, dbos.Config{AppName: "pasture"})
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("error type = %T, want *errors.StructuredError with actionable guidance", err)
	}
	if structured.Category != pasterrors.CategoryStorage {
		t.Errorf("category = %q, want %q", structured.Category, pasterrors.CategoryStorage)
	}
	if got := pasterrors.ExitCode(err); got != 5 {
		t.Errorf("exit code = %d, want 5 (storage)", got)
	}
	if structured.Where != engineConstructionSite {
		t.Errorf("where = %q, want %q", structured.Where, engineConstructionSite)
	}
	// One check per part an operator needs: what, why, where, impact, and a fix
	// that names the exact import to add.
	for _, want := range []string{
		"Problem:",
		"Reason:",
		"Impact:",
		"How to fix:",
		"SQLite support is missing",
		"internal/engine/engine.go",
		"blank import",
		dbosSQLiteDriverPackage,
		"https://github.com/dayvidpham/pasture/issues",
	} {
		if !reportContains(structured, want) {
			t.Errorf("unlinked-driver report is missing %q", want)
		}
	}
}

// TestDescribeDurableStartupFailure covers the shared classifier both durable
// entry points use: the engine (through newDurableContext) and the epoch
// controller (internal/handlers/controller.go).
func TestDescribeDurableStartupFailure(t *testing.T) {
	t.Parallel()
	const where = "a caller's location."

	t.Run("nil stays nil", func(t *testing.T) {
		t.Parallel()
		if err := DescribeDurableStartupFailure(where, nil); err != nil {
			t.Errorf("error = %v, want nil", err)
		}
	})

	t.Run("unlinked driver is described", func(t *testing.T) {
		t.Parallel()
		cause := newUnlinkedSQLiteDriverFailure()
		err := DescribeDurableStartupFailure(where, cause)
		var structured *pasterrors.StructuredError
		if !errors.As(err, &structured) {
			t.Fatalf("error type = %T, want *errors.StructuredError", err)
		}
		if structured.Where != where {
			t.Errorf("where = %q, want the caller's own location %q", structured.Where, where)
		}
		if !errors.Is(err, cause) {
			t.Error("the runtime's own refusal must stay reachable as the cause")
		}
	})

	t.Run("a wrapped unlinked driver is still described", func(t *testing.T) {
		t.Parallel()
		cause := fmt.Errorf("opening the controller: %w", newUnlinkedSQLiteDriverFailure())
		var structured *pasterrors.StructuredError
		if !errors.As(DescribeDurableStartupFailure(where, cause), &structured) {
			t.Error("a wrapped refusal must be recognised too")
		}
	})

	t.Run("an unnamed cause is passed through unchanged", func(t *testing.T) {
		t.Parallel()
		// The classifier must not dress up failures it cannot diagnose: the
		// caller's own wrapping is more accurate than a guess.
		cause := errors.New("Error initializing DBOS Transact: ApplicationVersion cannot be empty")
		if got := DescribeDurableStartupFailure(where, cause); got != cause {
			t.Errorf("error = %v, want the original failure returned unchanged", got)
		}
	})
}

// SCHEMA-GATE TESTS
//
// The gate itself lives in the provenance library, which owns the durable
// schema contract and proves the refusal against a recorded 176 KB database
// that the superseded runtime really wrote, plus the matching hazard: the same
// file handed straight to the runtime is migrated in place. Those two proofs
// are not repeated here, and the recorded database is not copied into this
// repository, because the library exports no way to obtain it.
//
// What the tests below prove is pasture's own wiring, which the library cannot:
// the gate runs before the durable context is built, its refusal reaches the
// caller as an actionable storage error that names the file, a refused database
// keeps the durable layout it arrived with, and a database this build created
// is never refused.
//
// The refused database is built here from the only two facts the gate reads:
// the durable runtime records its layout version in a single-row table named
// dbos_migrations, and the superseded runtime stopped at version 41. Evidence
// for both, in the library version pinned in go.mod:
// dbos/internal/sysdb/sqlite_migrations.go (RunSqliteMigrations creates
// `dbos_migrations (version INTEGER NOT NULL PRIMARY KEY)` and reads a single
// row from it) and BuildSqliteMigrations in the same file, whose SQLite list
// runs 1..41 and then continues at 42, so 41 is the last version the superseded
// runtime can leave behind.

// The three databases these tests judge — one an older build wrote, one fresh,
// one this build created — and the digest that proves a refusal wrote nothing
// are built by internal/testutil (durableschema.go). They live there because the
// epoch controller needs exactly the same three, and a second copy of these
// facts would drift from the first.

// openTestSQLite opens a WRITABLE handle through the production opener, because
// the gate takes the exact handle a caller is about to hand the durable runtime.
// The handle is closed on cleanup; a test that measures a digest closes it
// itself first, since an open connection keeps a sidecar alive.
func openTestSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := dbconn.OpenSharedDB(path)
	if err != nil {
		t.Fatalf("open the test database %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// renderReport returns the user-visible error block, for a failure message that
// shows exactly what an operator would read.
func renderReport(e *pasterrors.StructuredError) string {
	var buf bytes.Buffer
	e.Report(&buf)
	return buf.String()
}

// requireStructuredStorageError fails unless err is a pasture storage error,
// which is the exit code an operator scripts against.
func requireStructuredStorageError(t *testing.T, err error) *pasterrors.StructuredError {
	t.Helper()
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("error type = %T, want *errors.StructuredError: %v", err, err)
	}
	if structured.Category != pasterrors.CategoryStorage {
		t.Errorf("error category = %v, want %v", structured.Category, pasterrors.CategoryStorage)
	}
	return structured
}

// The gate refuses a database an older build wrote, reads only, and says what
// the operator must do about it.
func TestRequireSupportedDurableSchema_RefusesADatabaseAnOlderBuildWrote(t *testing.T) {
	t.Parallel()
	path, before := testutil.WriteSupersededDurableDatabase(t)
	db := openTestSQLite(t, path)

	err := RequireSupportedDurableSchema(t.Context(), engineConstructionSite, db, path, timeouts.DeadlineTestProfile())
	if err == nil {
		t.Fatal("the gate accepted a database whose durable layout an older build wrote")
	}
	structured := requireStructuredStorageError(t, err)
	if !errors.Is(err, provenance.ErrSupersededDBOSSystemSchema) {
		t.Errorf("the refusal no longer matches the library's own sentinel: %v", err)
	}
	for _, want := range []string{
		path,                           // the file the operator must act on
		path + "-wal",                  // and its two companions
		path + "-shm",                  // ...
		"rm ",                          // the exact command
		"--db <path>",                  // the alternative to deleting it
		"pgrep -fa 'pasture|pastured'", // stop the processes first
	} {
		if !reportContains(structured, want) {
			t.Errorf("the refusal does not mention %q: %s", want, renderReport(structured))
		}
	}
	// The wrapped library text carries the recorded version and the floor, so a
	// floor that moves fails here instead of passing silently.
	for _, want := range []string{
		fmt.Sprintf("version %d", testutil.SupersededDurableSchemaVersion),
		fmt.Sprintf("floor %d", testutil.FirstSupportedDurableSchemaVersion),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not report %q: %v", want, err)
		}
	}

	// Close the handle first, exactly as the production refusal path does: an
	// open WAL connection keeps a -shm sidecar alive, and the digest counts the
	// sidecars.
	if err := db.Close(); err != nil {
		t.Fatalf("close the handle on %s after the refusal: %v", path, err)
	}
	if after := testutil.DatabaseDigest(t, path); after != before {
		t.Errorf("the refused database changed on disk: digest %s, want %s; the gate must only read", after, before)
	}
}

// A database with no durable layout is fresh, and the runtime creates the
// layout on its first launch. The gate must not stand in the way of that.
func TestRequireSupportedDurableSchema_AcceptsAFreshDatabase(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh.db")
	db := openTestSQLite(t, path)
	if err := RequireSupportedDurableSchema(t.Context(), engineConstructionSite, db, path, timeouts.DeadlineTestProfile()); err != nil {
		t.Fatalf("the gate refused a fresh database: %v", err)
	}
}

// The production path: engine construction refuses such a database, and the
// durable runtime never touches it.
func TestEngineNew_RefusesADatabaseAnOlderBuildWrote(t *testing.T) {
	t.Parallel()
	path, before := testutil.WriteSupersededDurableDatabase(t)

	built, err := New(t.Context(), Config{
		DBPath:             path,
		ApplicationVersion: "test-app-refuses-superseded",
		ExecutorID:         "test-executor-refuses-superseded",
		// The gate watches a below-floor layout for one SQLiteBusy window
		// before it calls it stable; the deadline profile keeps that short.
		Timeouts: timeouts.DeadlineTestProfile(),
	})
	// Measure the database FIRST, before any assertion below opens a handle of
	// its own: an open connection under WAL journal mode keeps a -shm sidecar
	// alive, and the digest counts the sidecars. Production leaves none behind,
	// because the refusal closes the only handle it opened.
	afterRefusal := testutil.DatabaseDigest(t, path)

	if err == nil {
		built.Shutdown(5 * time.Second)
		t.Fatal("engine.New opened a database whose durable layout an older build wrote")
	}
	if built != nil {
		t.Fatalf("engine.New returned an engine together with error %v", err)
	}
	structured := requireStructuredStorageError(t, err)
	if !reportContains(structured, path) {
		t.Errorf("the refusal does not name the database %s: %s", path, renderReport(structured))
	}
	if !reportContains(structured, engineConstructionSite) {
		t.Errorf("the refusal does not name the engine as the refusing caller: %s", renderReport(structured))
	}

	// Nothing was migrated: the recorded layout version is untouched and no
	// table the durable runtime owns was created.
	if version := testutil.ReadDurableSchemaVersion(t, path); version != testutil.SupersededDurableSchemaVersion {
		t.Errorf("recorded layout version = %d after the refusal, want %d: the refusal migrated the database",
			version, testutil.SupersededDurableSchemaVersion)
	}
	if tables := testutil.DurableRuntimeTables(t, path); len(tables) != 0 {
		t.Errorf("the refused database gained durable-runtime tables %v: the runtime ran against it", tables)
	}

	// The whole database, not only the durable layout, and sidecars included.
	// The refusal promises that no pasture data was written and that an older
	// build can still read the history, so NOTHING in this process may write to
	// the database before the gate passes — not the durable runtime, not the
	// projection table, and not the forensic trail, whose own migration would
	// raise a schema version that the older build then refuses in turn. Under
	// WAL journal mode a writer's pages sit in the -wal sidecar until a
	// checkpoint, so a main-file digest would call such a write invisible;
	// databaseDigest hashes the sidecars too, which is what makes this one
	// assertion cover every writer at once, including one added later.
	if afterRefusal != before {
		t.Errorf("the refused database changed on disk: digest %s, want %s; "+
			"something in engine.New wrote to the file before the schema gate refused it, "+
			"or this digest was taken while a handle was still open; check the sidecars and "+
			"the table list below. "+
			"Tables now present: %v", afterRefusal, before, testutil.AllTables(t, path))
	}
}

// The other side of the gate: a database this build created carries a supported
// layout, and reopening it must not be refused.
func TestEngineNew_AcceptsADatabaseThisBuildCreated(t *testing.T) {
	t.Parallel()
	dbPath := testutil.GoldenUnifiedDBPath(t)
	cfg := Config{
		DBPath:                   dbPath,
		ApplicationVersion:       "test-app-accepts-supported",
		ExecutorID:               "test-executor-accepts-supported",
		SkipMigrations:           true,
		QueueBasePollingInterval: 100 * time.Millisecond,
	}

	first, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("engine.New on a fresh database: %v", err)
	}
	first.Shutdown(30 * time.Second)

	version := testutil.ReadDurableSchemaVersion(t, dbPath)
	if version < testutil.FirstSupportedDurableSchemaVersion {
		t.Fatalf("this build left the database at layout version %d, below its own floor %d: "+
			"the fixture below no longer represents a database this build created",
			version, testutil.FirstSupportedDurableSchemaVersion)
	}

	second, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("engine.New refused a database this build itself created at layout version %d: %v", version, err)
	}
	second.Shutdown(30 * time.Second)
}

// A layout the gate cannot read at all is the third outcome, beside a refusal
// and a pass. It happens when the file is damaged, unreadable, or simply not
// the database pasture owns: another program's table of the same name is enough.
// The operator must get an actionable error for it too, not a bare library
// message and not silence.
func TestRequireSupportedDurableSchema_ReportsALayoutItCannotRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "foreign.db")
	db := openTestSQLite(t, path)
	// A table of the durable runtime's name that another program owns: it has
	// no version column, so the layout version can be neither read nor judged.
	if _, err := db.ExecContext(t.Context(),
		"CREATE TABLE "+testutil.DurableMigrationTable+" (applied_at TEXT NOT NULL)"); err != nil {
		t.Fatalf("create a foreign %s table in %s: %v", testutil.DurableMigrationTable, path, err)
	}

	err := RequireSupportedDurableSchema(t.Context(), engineConstructionSite, db, path, timeouts.DeadlineTestProfile())
	if err == nil {
		t.Fatal("the gate accepted a database whose durable layout it could not read")
	}
	structured := requireStructuredStorageError(t, err)
	if errors.Is(err, provenance.ErrSupersededDBOSSystemSchema) {
		t.Errorf("an unreadable layout was reported as an older build's database: %v", err)
	}
	for _, want := range []string{
		path,              // the file the operator must act on
		"integrity_check", // how to tell a damaged file from a foreign one
		"--db <path>",     // what to do when the file belongs to another program
	} {
		if !reportContains(structured, want) {
			t.Errorf("the report does not mention %q: %s", want, renderReport(structured))
		}
	}
}
