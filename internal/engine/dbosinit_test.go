package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
)

// ciLostRaceError is the verbatim failure a losing process produced in CI when
// two daemons opened the same fresh database at once. It is the exact string
// the retry predicate must recognise; keeping the real text here means a
// library upgrade that reshapes the message fails this test instead of silently
// turning the retry off.
const ciLostRaceError = "Error initializing DBOS Transact: failed to run sqlite migrations: " +
	"failed to create migrations table: SQL logic error: table dbos_migrations already exists (1)"

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

func fastPolicy() dbosRetryPolicy {
	return dbosRetryPolicy{
		ceiling:      30 * time.Second,
		initialDelay: time.Millisecond,
		maxDelay:     2 * time.Millisecond,
	}
}

func TestNewDurableContext_RetriesLostSchemaBootstrapRace(t *testing.T) {
	t.Parallel()
	f := &fakeFactory{errs: []error{errors.New(ciLostRaceError)}}

	if _, err := newDurableContext(context.Background(), f.new, dbos.Config{AppName: "pasture"}, fastPolicy()); err != nil {
		t.Fatalf("newDurableContext after one lost race: got error %v, want success on the retry", err)
	}
	if f.attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one lost race, one converging retry)", f.attempts)
	}
}

func TestNewDurableContext_ReturnsNonRaceFailureImmediately(t *testing.T) {
	t.Parallel()
	fatal := errors.New("Error initializing DBOS Transact: ApplicationVersion cannot be empty")
	f := &fakeFactory{errs: []error{fatal, nil}}

	_, err := newDurableContext(context.Background(), f.new, dbos.Config{AppName: "pasture"}, fastPolicy())
	if !errors.Is(err, fatal) {
		t.Fatalf("error = %v, want the original failure returned unchanged", err)
	}
	if f.attempts != 1 {
		t.Errorf("attempts = %d, want 1 (a non-race failure must not be retried)", f.attempts)
	}
}

func TestNewDurableContext_CeilingIsBoundedAndActionable(t *testing.T) {
	t.Parallel()
	// A race signature that never converges (e.g. a foreign table under a
	// durable-execution name) must not spin forever. Every attempt fails, so
	// the loop can only end at the ceiling.
	permanent := errors.New(ciLostRaceError)
	f := &fakeFactory{}
	for range 1000 {
		f.errs = append(f.errs, permanent)
	}
	policy := dbosRetryPolicy{ceiling: 20 * time.Millisecond, initialDelay: time.Millisecond, maxDelay: 2 * time.Millisecond}

	_, err := newDurableContext(context.Background(), f.new, dbos.Config{AppName: "pasture"}, policy)
	if err == nil {
		t.Fatal("newDurableContext with a never-converging race: got success, want the ceiling error")
	}
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
	if !errors.Is(err, permanent) {
		t.Error("ceiling error must wrap the last underlying failure so operators can see it")
	}
	for _, want := range []string{"Problem:", "Reason:", "Impact:", "How to fix:", "pgrep"} {
		if !reportContains(structured, want) {
			t.Errorf("ceiling error report is missing %q", want)
		}
	}
	// The exact attempt count is timing-dependent; the contract is that the
	// loop terminated at the ceiling having actually tried.
	if f.attempts < 1 {
		t.Errorf("attempts = %d, want at least 1", f.attempts)
	}
}

func TestNewDurableContext_CancelledWaitIsActionable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeFactory{errs: []error{errors.New(ciLostRaceError)}}
	// A long delay makes the cancelled-context arm of the select the only
	// ready case, so this test is deterministic and still instant.
	policy := dbosRetryPolicy{ceiling: time.Hour, initialDelay: time.Hour, maxDelay: time.Hour}

	_, err := newDurableContext(ctx, f.new, dbos.Config{AppName: "pasture"}, policy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want a cancellation error wrapping context.Canceled", err)
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("error type = %T, want *errors.StructuredError", err)
	}
	if f.attempts != 1 {
		t.Errorf("attempts = %d, want 1", f.attempts)
	}
}

func TestIsDBOSBootstrapRace(t *testing.T) {
	t.Parallel()
	const prefix = "Error initializing DBOS Transact: failed to run sqlite migrations: "
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"observed migrations-table race", errors.New(ciLostRaceError), true},
		{"migration body re-creates a table", errors.New(prefix + "failed to execute migration 1: SQL logic error: table workflow_status already exists (1)"), true},
		{"migration body re-adds a column", errors.New(prefix + "failed to execute migration 33: SQL logic error: duplicate column name: rate_limited (1)"), true},
		{"version bookkeeping inserted twice", errors.New(prefix + "failed to insert migration version 1: constraint failed: UNIQUE constraint failed: dbos_migrations.version (2067)"), true},
		{"loser still queued behind the winner's write lock", errors.New(prefix + "failed to execute migration 1: database is locked (5)"), true},
		{"wrapped race", fmt.Errorf("engine.New: %w", errors.New(ciLostRaceError)), true},
		{"already-exists outside schema bootstrap is fatal", errors.New("engine.New: failed to open the forensic audit trail: table audit_events already exists"), false},
		{"unrelated bootstrap failure is fatal", errors.New(prefix + "disk I/O error (10)"), false},
		{"configuration failure is fatal", errors.New("Error initializing DBOS Transact: ApplicationVersion cannot be empty"), false},
		{"unlinked sqlite driver is not a race", errors.New(unlinkedSQLiteDriverError), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isDBOSBootstrapRace(tc.err); got != tc.want {
				t.Errorf("isDBOSBootstrapRace(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// reportContains renders the user-visible error block and reports whether it
// contains want, so the assertions above check what an operator actually sees.
func reportContains(e *pasterrors.StructuredError, want string) bool {
	var buf bytes.Buffer
	e.Report(&buf)
	return strings.Contains(buf.String(), want)
}

// pinnedDBOSVersion is the durable-execution library version whose schema
// bootstrap the retry predicate was read against and whose error strings
// isDBOSBootstrapRace matches.
const pinnedDBOSVersion = "github.com/dbos-inc/dbos-transact-golang v1.2.0"

// TestDBOSVersionPinMatchesRacePredicate fails when the durable-execution
// library is upgraded, because the retry predicate in dbosinit.go recognises a
// lost schema-bootstrap race by MESSAGE TEXT — the library flattens the driver
// error with %v before it reaches us, so there is no typed error to match on.
//
// That coupling cannot be checked by any behavioural test: if a new version
// rewords "failed to create migrations table: ... already exists", the
// predicate silently stops matching, the retry never fires, a losing process
// dies at start-up again, and every existing test still passes because they all
// assert against the old strings.
//
// WHEN THIS TEST FAILS, DO NOT JUST BUMP THE CONSTANT. Re-read the new
// version's dbos/internal/sysdb/sqlite_migrations.go (the migrations-table
// bootstrap and applySqliteMigration) and sqlite_pool.go (the wrapper prefix
// and the retry that wraps them), then update BOTH this constant AND
// dbosMigrationFailurePrefix / dbosBootstrapRaceMarkers / ciLostRaceError to
// the messages that version actually produces. If the new version made the
// bootstrap atomic or retry-safe itself, delete the predicate instead.
//
// The same applies to the second text-matched message: re-read
// dbos/internal/sysdb/sqlite_driver.go (registeredSQLiteDriver) and update
// dbosMissingSQLiteDriverMarker and unlinkedSQLiteDriverError together.
func TestDBOSVersionPinMatchesRacePredicate(t *testing.T) {
	t.Parallel()
	gomod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod to verify the durable-execution version pin: %v", err)
	}
	if !strings.Contains(string(gomod), pinnedDBOSVersion) {
		t.Fatalf("go.mod no longer pins %q — the lost-schema-bootstrap-race retry in "+
			"internal/engine/dbosinit.go matches that version's error text and must be "+
			"re-verified against the new one before this pin is updated", pinnedDBOSVersion)
	}
	// The predicates must still recognise the failures this pin was chosen for.
	if !isDBOSBootstrapRace(errors.New(ciLostRaceError)) {
		t.Fatal("isDBOSBootstrapRace no longer matches the observed lost-race failure of the pinned version")
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

// TestNewDurableContext_UnlinkedSQLiteDriverIsNotRetried pins that a binary
// which did not link the SQLite driver fails at once.
//
// The refusal is permanent for the life of the process: no peer can repair it
// and every re-attempt repeats the same registry lookup. Retrying it would turn
// an instant, explainable failure into a long silent wait that ends in the
// wrong diagnosis (a schema-bootstrap race).
func TestNewDurableContext_UnlinkedSQLiteDriverIsNotRetried(t *testing.T) {
	t.Parallel()
	unlinked := newUnlinkedSQLiteDriverFailure()
	// The second scripted result is a success: if the loop retried, the test
	// would pass with two attempts and no error, which the assertions reject.
	f := &fakeFactory{errs: []error{unlinked, nil}}

	_, err := newDurableContext(context.Background(), f.new, dbos.Config{AppName: "pasture"}, fastPolicy())
	if err == nil {
		t.Fatal("newDurableContext with an unlinked SQLite driver: got success, want a failure on the first attempt")
	}
	if f.attempts != 1 {
		t.Errorf("attempts = %d, want 1 (an unlinked driver is permanent and must not be retried)", f.attempts)
	}
	if isDBOSBootstrapRace(unlinked) {
		t.Error("the lost-race predicate matches the unlinked-driver refusal; the two failures must stay distinct")
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

	_, err := newDurableContext(context.Background(), f.new, dbos.Config{AppName: "pasture"}, fastPolicy())
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

// supersededDurableSchemaVersion is the layout version the superseded runtime
// stopped at, and therefore the version the refused database below records.
const supersededDurableSchemaVersion = 41

// firstSupportedDurableSchemaVersion is the floor the gate enforces: the first
// layout version this build's durable runtime introduces. The refusal names it,
// so a floor that ever moves in the library fails these tests instead of
// passing silently.
const firstSupportedDurableSchemaVersion = 42

// durableMigrationTable is the single-row table the durable runtime keeps its
// layout version in.
const durableMigrationTable = "dbos_migrations"

// writeSupersededDurableDatabase writes a private database whose durable layout
// is the one the superseded runtime left behind, and returns its path with the
// digest of its bytes. The digest lets a caller prove that a refusal wrote
// nothing.
func writeSupersededDurableDatabase(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pasture.db")
	// The production opener, so the file arrives in the exact shape a real
	// pasture database has: WAL journal mode and the same pragmas. A fixture
	// written on plainer settings would be converted to WAL by the first
	// production open, and that conversion alone rewrites the file header —
	// which would look, to the digest assertions below, like a writer the gate
	// failed to hold back.
	//
	// That header rewrite is the ONE change the digest cannot cover by
	// construction: the shared handle's connection string sets the journal mode,
	// and that pragma runs on the gate's own first query, before any refusal is
	// possible. A database arriving on a rollback journal is therefore
	// normalised to WAL even when it is refused. It carries no pasture data, and
	// the refusal says so in those words rather than claiming the file is
	// untouched. Writing the fixture through the production opener keeps that
	// single unavoidable change out of the way of every other assertion.
	db, err := dbconn.OpenSharedDB(path)
	if err != nil {
		t.Fatalf("open %s with the production opener: %v", path, err)
	}
	if _, err := db.ExecContext(t.Context(),
		"CREATE TABLE "+durableMigrationTable+" (version INTEGER NOT NULL PRIMARY KEY)"); err != nil {
		_ = db.Close()
		t.Fatalf("create the %s table in %s: %v", durableMigrationTable, path, err)
	}
	if _, err := db.ExecContext(t.Context(),
		"INSERT INTO "+durableMigrationTable+" (version) VALUES (?)", supersededDurableSchemaVersion); err != nil {
		_ = db.Close()
		t.Fatalf("record layout version %d in %s: %v", supersededDurableSchemaVersion, path, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close %s after writing the superseded layout: %v", path, err)
	}
	return path, databaseDigest(t, path)
}

// openTestSQLite opens a private handle on the same driver production uses. The
// handle is closed on cleanup.
func openTestSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open the test database %s: %v", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping the test database %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// databaseDigest hashes a SQLite database as a whole: the main file AND its two
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
// A missing sidecar counts as empty, which is the normal state after the last
// handle on the file closes.
func databaseDigest(t *testing.T, path string) string {
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

// readDurableSchemaVersion reports the layout version a database records, or 0
// when it records none.
func readDurableSchemaVersion(t *testing.T, path string) int64 {
	t.Helper()
	db := openTestSQLite(t, path)
	var version int64
	err := db.QueryRowContext(t.Context(),
		"SELECT version FROM "+durableMigrationTable+" LIMIT 1").Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read the recorded layout version from %s: %v", path, err)
	}
	return version
}

// durableRuntimeTables reports which tables the durable runtime owns are
// present in the database. The gate must leave every one of them absent.
func durableRuntimeTables(t *testing.T, path string) []string {
	t.Helper()
	db := openTestSQLite(t, path)
	present := []string{}
	// These four tables are created by the very first layout steps the runtime
	// applies, so any one of them proves the runtime ran against the file.
	for _, name := range []string{"workflow_status", "operation_outputs", "notifications", "workflow_queue"} {
		var found int
		err := db.QueryRowContext(t.Context(),
			"SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Fatalf("probe %s for the table %s: %v", path, name, err)
		}
		present = append(present, name)
	}
	return present
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
	path, before := writeSupersededDurableDatabase(t)
	db := openTestSQLite(t, path)

	err := RequireSupportedDurableSchema(t.Context(), engineConstructionSite, db, path)
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
		fmt.Sprintf("version %d", supersededDurableSchemaVersion),
		fmt.Sprintf("floor %d", firstSupportedDurableSchemaVersion),
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
	if after := databaseDigest(t, path); after != before {
		t.Errorf("the refused database changed on disk: digest %s, want %s; the gate must only read", after, before)
	}
}

// A database with no durable layout is fresh, and the runtime creates the
// layout on its first launch. The gate must not stand in the way of that.
func TestRequireSupportedDurableSchema_AcceptsAFreshDatabase(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh.db")
	db := openTestSQLite(t, path)
	if err := RequireSupportedDurableSchema(t.Context(), engineConstructionSite, db, path); err != nil {
		t.Fatalf("the gate refused a fresh database: %v", err)
	}
}

// The production path: engine construction refuses such a database, and the
// durable runtime never touches it.
func TestEngineNew_RefusesADatabaseAnOlderBuildWrote(t *testing.T) {
	t.Parallel()
	path, before := writeSupersededDurableDatabase(t)

	built, err := New(t.Context(), Config{
		DBPath:             path,
		ApplicationVersion: "test-app-refuses-superseded",
		ExecutorID:         "test-executor-refuses-superseded",
	})
	// Measure the database FIRST, before any assertion below opens a handle of
	// its own: an open connection under WAL journal mode keeps a -shm sidecar
	// alive, and the digest counts the sidecars. Production leaves none behind,
	// because the refusal closes the only handle it opened.
	afterRefusal := databaseDigest(t, path)

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
	if version := readDurableSchemaVersion(t, path); version != supersededDurableSchemaVersion {
		t.Errorf("recorded layout version = %d after the refusal, want %d: the refusal migrated the database",
			version, supersededDurableSchemaVersion)
	}
	if tables := durableRuntimeTables(t, path); len(tables) != 0 {
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
			"something in engine.New wrote to the file before the schema gate refused it. "+
			"Tables now present: %v", afterRefusal, before, allTables(t, path))
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

	version := readDurableSchemaVersion(t, dbPath)
	if version < firstSupportedDurableSchemaVersion {
		t.Fatalf("this build left the database at layout version %d, below its own floor %d: "+
			"the fixture below no longer represents a database this build created",
			version, firstSupportedDurableSchemaVersion)
	}

	second, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("engine.New refused a database this build itself created at layout version %d: %v", version, err)
	}
	second.Shutdown(30 * time.Second)
}

// allTables lists every table in a database, for a failure message that names
// what a writer created.
func allTables(t *testing.T, path string) []string {
	t.Helper()
	db := openTestSQLite(t, path)
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
		"CREATE TABLE "+durableMigrationTable+" (applied_at TEXT NOT NULL)"); err != nil {
		t.Fatalf("create a foreign %s table in %s: %v", durableMigrationTable, path, err)
	}

	err := RequireSupportedDurableSchema(t.Context(), engineConstructionSite, db, path)
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
