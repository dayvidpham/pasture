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

func (f *fakeFactory) new(context.Context, dbos.Config) (dbos.DBOSContext, error) {
	f.attempts++
	if f.attempts <= len(f.errs) {
		if err := f.errs[f.attempts-1]; err != nil {
			return nil, err
		}
	}
	// A nil DBOSContext with a nil error is enough: newDurableContext only
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
const pinnedDBOSVersion = "github.com/dbos-inc/dbos-transact-golang v0.20.0"

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
	// The predicate must still recognise the failure this pin was chosen for.
	if !isDBOSBootstrapRace(errors.New(ciLostRaceError)) {
		t.Fatal("isDBOSBootstrapRace no longer matches the observed lost-race failure of the pinned version")
	}
}
