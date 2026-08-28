package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

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
