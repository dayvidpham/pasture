package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/dbos-inc/dbos-transact-golang/dbos"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// DBOS Transact bootstraps its own system-database schema the first time a
// process opens a SQLite file: it probes sqlite_master for the migrations
// table, creates it when absent, then applies each numbered migration in its
// own transaction. That probe-then-create is not atomic, and only 14 of the 47
// SQLite migration files are written with IF NOT EXISTS, so two pasture
// processes opening the same fresh pasture.db can both decide the schema is
// missing. One wins; the loser's CREATE fails against the schema the winner has
// just committed — the other 33 migrations make that collision certain rather
// than merely possible.
//
// The library retries its own migration run only for SQLITE_BUSY / SQLITE_LOCKED
// (dbos/internal/sysdb/dialect.go SqliteDialect.IsRetryable), and even that is
// defeated for migration failures because dbos/internal/sysdb/sqlite_migrations.go
// formats every error with %v and so severs the driver error from the chain.
// A lost race therefore surfaces immediately as a plain SQLITE_ERROR (code 1)
// "already exists" string and kills process start-up.
//
// EVIDENCE, read from the library version pinned in go.mod (see
// dbosBootstrapRaceMarkers for the per-message locations):
//   - probe-then-create, outside any transaction:
//     dbos/internal/sysdb/sqlite_migrations.go:227-240 (RunSqliteMigrations).
//   - one transaction per migration, with %v formatting on every failure:
//     dbos/internal/sysdb/sqlite_migrations.go:261-296 (applySqliteMigration).
//   - the retry that cannot see the driver error:
//     dbos/internal/sysdb/dialect.go:399-412 (SqliteDialect.IsRetryable, which
//     reads the driver's error code) and dbos/internal/sysdb/system_database.go:6664-6675
//     (Retry, whose condition chain is the two dialects' IsRetryable).
//   - the migration bodies: dbos/internal/sysdb/migrations/sqlite holds 47
//     files; 14 of them use IF NOT EXISTS and the other 33 collide.
//
// KNOWN LIMIT, same version: dbos/internal/sysdb/system_database.go:873-884
// (startupError) REPLACES the message above when the start-up context has
// already exceeded dbos.Config.SystemDBStartupTimeout. A race lost that late
// therefore reads as "system database startup timed out after ... while
// initializing the SQLite system database" and this predicate does not match
// it. That is deliberate: a start-up that ran out of its own budget is not
// repaired by an immediate re-attempt, and the timeout text is actionable on
// its own. Pasture leaves dbos.Config.SystemDBStartupTimeout unset, so the
// library default of 2 minutes applies (dbos/dbos.go:63 and :131-132) — far
// more than a bootstrap needs. Re-check this if that budget ever becomes short
// enough that a normal bootstrap can hit it.
//
// Re-running the whole bootstrap is the correct repair, and it converges: the
// second run re-probes sqlite_master, re-reads the committed version row, and
// skips everything the winner already applied. No pasture state is written by a
// failed attempt, so the retry is not a partial-write hazard — it is a fresh
// read of the file the winner has finished with.

// dbosRaceRetryCeiling bounds re-attempting DBOS initialisation after a lost
// schema-bootstrap race. It exists for the case where the "already exists"
// verdict is NOT a race (e.g. a foreign table of the same name in the file) and
// therefore never converges.
//
// Precisely: it bounds when the LAST attempt may START, not how long the whole
// operation takes. Real elapsed time can exceed it by one attempt plus one
// backoff, because an attempt already under way is never abandoned mid-flight.
// Callers that need a hard wall-clock bound must cancel the context instead.
const dbosRaceRetryCeiling = 30 * time.Second

// dbosRaceRetryInitialDelay is the first pause between attempts; the delay
// doubles up to dbosRaceRetryMaxDelay. The first re-attempt is deliberately
// quick because the winner has, by construction, already committed the
// statement we collided with.
const dbosRaceRetryInitialDelay = 25 * time.Millisecond

// dbosRaceRetryMaxDelay caps the per-attempt backoff.
const dbosRaceRetryMaxDelay = 1 * time.Second

// dbosContextFactory constructs a durable-execution context. Production passes
// dbos.NewContext; tests inject a stub to drive the retry loop
// deterministically, without a second process and without any sleep.
type dbosContextFactory func(context.Context, dbos.Config) (dbos.Context, error)

// dbosRetryPolicy is the bounded-retry budget for newDurableContext. The zero
// value is not usable; defaultDBOSRetryPolicy returns the production budget.
type dbosRetryPolicy struct {
	ceiling      time.Duration
	initialDelay time.Duration
	maxDelay     time.Duration
}

func defaultDBOSRetryPolicy() dbosRetryPolicy {
	return dbosRetryPolicy{
		ceiling:      dbosRaceRetryCeiling,
		initialDelay: dbosRaceRetryInitialDelay,
		maxDelay:     dbosRaceRetryMaxDelay,
	}
}

// newDurableContext calls newCtx and, when the failure is the benign
// lost-bootstrap-race signature described above, re-attempts it until it
// succeeds, the error becomes something else, the caller's context is
// cancelled, or the policy ceiling is spent.
//
// Any error that is not the race signature ends the loop on the first attempt,
// so a genuine configuration or corruption failure still fails fast. Such an
// error goes through DescribeDurableStartupFailure, which replaces the causes
// pasture can name with actionable text and passes every other one through
// unchanged.
func newDurableContext(
	ctx context.Context,
	newCtx dbosContextFactory,
	cfg dbos.Config,
	policy dbosRetryPolicy,
) (dbos.Context, error) {
	deadline := time.Now().Add(policy.ceiling)
	delay := policy.initialDelay
	attempts := 0
	for {
		attempts++
		dbosCtx, err := newCtx(ctx, cfg)
		if err == nil {
			return dbosCtx, nil
		}
		if !isDBOSBootstrapRace(err) {
			return nil, DescribeDurableStartupFailure(engineConstructionSite, err)
		}
		if time.Now().After(deadline) {
			return nil, dbosBootstrapRaceCeilingError(cfg.AppName, attempts, policy.ceiling, err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, dbosBootstrapRaceCancelledError(attempts, ctx.Err(), err)
		case <-timer.C:
		}
		if delay < policy.maxDelay {
			delay *= 2
			if delay > policy.maxDelay {
				delay = policy.maxDelay
			}
		}
	}
}

// dbosMigrationFailurePrefix is the wrapper DBOS puts on every system-database
// schema-bootstrap failure. Requiring it keeps the retry predicate scoped to
// schema bootstrap: an "already exists" from anywhere else in start-up is still
// fatal on the first attempt.
//
// Evidence in the pinned library version: dbos/internal/sysdb/sqlite_pool.go:172
// (newSqliteSystemDatabase wraps the migration run).
const dbosMigrationFailurePrefix = "failed to run sqlite migrations"

// dbosBootstrapRaceMarkers are the ways a lost schema-bootstrap race shows up,
// in the order the library can produce them: the migrations-table create loses
// the probe-then-create window; a migration body re-creates a table or index,
// or re-adds a column, the winner already committed; the version bookkeeping
// row is inserted twice; or the loser is simply still queued behind the
// winner's write lock. Every one of them is repaired by re-running the
// bootstrap against the winner's committed schema.
//
// Evidence in the pinned library version, message by message:
//   - "already exists" reaches us from two producers:
//     dbos/internal/sysdb/sqlite_migrations.go:238 ("failed to create migrations
//     table: %v", the lost probe-then-create window) and
//     dbos/internal/sysdb/sqlite_migrations.go:281 ("failed to execute migration
//     %d: %v", a migration body re-creating a table or index). SQLite supplies
//     the "already exists" text itself.
//   - "duplicate column name" reaches us from the same line :281, for a
//     migration body that re-adds a column.
//   - "UNIQUE constraint failed: dbos_migrations" reaches us from
//     dbos/internal/sysdb/sqlite_migrations.go:288 ("failed to insert migration
//     version %d: %v"). That insert runs only on a first-ever bootstrap
//     (lastApplied == 0); a database that already carries a version row is
//     UPDATEd instead at :293, and a lost race there shows up as a lock message
//     rather than a constraint one.
//   - "database is locked" and "database table is locked" are SQLite's own
//     busy/locked text, and can wrap any statement above.
var dbosBootstrapRaceMarkers = []string{
	"already exists",
	"duplicate column name",
	"UNIQUE constraint failed: dbos_migrations",
	"database is locked",
	"database table is locked",
}

// isDBOSBootstrapRace reports whether err is a lost DBOS schema-bootstrap race.
//
// It matches on text, not on error identity, because the library flattens the
// driver error with %v at two levels before it reaches us — there is no wrapped
// sqlite error to inspect. The match is coupled to the
// github.com/dbos-inc/dbos-transact-golang version pinned in go.mod; a version
// bump must re-check dbos/internal/sysdb/sqlite_migrations.go and
// sqlite_pool.go. A signature that stops matching costs an actionable start-up
// failure under concurrency, not silent corruption.
//
// The version this predicate is read against is pinned by
// TestDBOSVersionPinMatchesRacePredicate in internal/engine/dbosinit_test.go,
// which fails on any bump so the messages get re-read before the pin moves.
func isDBOSBootstrapRace(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.Contains(msg, dbosMigrationFailurePrefix) {
		return false
	}
	for _, marker := range dbosBootstrapRaceMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func dbosBootstrapRaceCeilingError(appName string, attempts int, ceiling time.Duration, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Couldn't set up the durable-execution schema in the pasture database.",
		Why: fmt.Sprintf(
			"Durable-execution start-up kept failing with a schema-already-exists conflict for more\n"+
				"than %s (%d attempt(s)). That conflict normally means another pasture process was\n"+
				"creating the same schema at the same moment, and a re-attempt then succeeds. It did\n"+
				"not converge here, so one of three things is true: a process is stuck mid-setup; the\n"+
				"database file already contains tables with durable-execution names that pasture did not\n"+
				"create; or the recorded schema version went backwards, so every start-up re-applies setup\n"+
				"steps that are already in place. Step 2 below tells the three apart.",
			ceiling, attempts,
		),
		Where: "Constructing the engine (internal/engine/dbosinit.go in engine.newDurableContext).",
		Impact: "This process can't start its durable engine, so no epoch can run from it. Nothing was\n" +
			"written by the failed attempts — the database is exactly as the other process left it.",
		Fix: "1. Check whether another pasture or pastured process is running against the same database:\n" +
			"     pgrep -fa 'pasture|pastured'\n" +
			"   If one is stuck, stop it and retry.\n" +
			"2. Confirm the database file is the one pasture owns and is healthy:\n" +
			"     sqlite3 <path-to-pasture.db> 'PRAGMA integrity_check; SELECT version FROM dbos_migrations;'\n" +
			"3. If the file holds unrelated tables under durable-execution names, point pasture at a\n" +
			"   different database with --db <path>.\n" +
			fmt.Sprintf("   (durable-execution application name: %s)", appName),
		Cause: cause,
	}
}

func dbosBootstrapRaceCancelledError(attempts int, cancelCause, lastErr error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Durable-execution start-up was cancelled while waiting for another process to finish creating the database schema.",
		Why: fmt.Sprintf(
			"After %d attempt(s) this process was still waiting out a concurrent schema setup when its\n"+
				"caller cancelled start-up (last setup error: %v).",
			attempts, lastErr,
		),
		Where: "Constructing the engine (internal/engine/dbosinit.go in engine.newDurableContext).",
		Impact: "The engine did not start. Nothing was written by the cancelled attempts, so the database\n" +
			"is unchanged and a later start can proceed normally.",
		Fix: "1. Identify what cancelled start-up (an interrupted command, a shutdown signal, a caller\n" +
			"   timeout) and let it clear.\n" +
			"2. Start pasture again once the other process has finished:\n" +
			"     pgrep -fa 'pasture|pastured'",
		Cause: cancelCause,
	}
}

// engineConstructionSite names the place newDurableContext runs, for the Where
// line of the errors it produces.
const engineConstructionSite = "Constructing the engine (internal/engine/engine.go in engine.New)."

// dbosMissingSQLiteDriverMarker is how the durable runtime refuses a binary
// that never linked its SQLite driver. The runtime keeps the driver in a
// registry that only the driver package's init populates, so a binary that
// drops the blank import compiles, links, and then fails at the first attempt
// to open a system database.
//
// Evidence in the pinned library version:
// dbos/internal/sysdb/sqlite_driver.go:47-53 (registeredSQLiteDriver) writes
// this message; dbos/internal/sysdb/sqlite_pool.go:72 and :141 are the two
// callers, and :141 runs even when the caller supplies its own handle, which is
// pasture's case. dbos/dbos.go:636-638 then re-wraps it as an initialisation
// error, so the text below arrives inside a *dbos.Error.
//
// Matching text rather than an error value is forced by the library: the
// message is built with fmt.Errorf and then flattened into the *dbos.Error
// message, and the library exports no sentinel for it. The match is coupled to
// the version pinned in go.mod and is re-checked by
// TestDBOSVersionPinMatchesRacePredicate.
const dbosMissingSQLiteDriverMarker = "SQLite support is not linked into this binary"

// dbosSQLiteDriverPackage is the package whose blank import registers the
// SQLite driver. It is named in the fix text below, and pinned in every binary
// that opens a system database by the blank-import guards in
// internal/engine/dbosinit_test.go and internal/handlers/controller_test.go.
const dbosSQLiteDriverPackage = "github.com/dbos-inc/dbos-transact-golang/dbos/driver/sqlite"

// isMissingDBOSSQLiteDriver reports whether err is the durable runtime's
// refusal of a binary that did not link the SQLite driver.
//
// This failure is permanent for the life of the process: no other process can
// repair it, and re-attempting it only repeats the same registry lookup. It
// must therefore never enter the lost-race retry above, and
// isDBOSBootstrapRace does not match it — the two predicates require different
// text and this one carries no migration wrapper.
func isMissingDBOSSQLiteDriver(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), dbosMissingSQLiteDriverMarker)
}

// DescribeDurableStartupFailure returns an actionable replacement for a
// durable-runtime start-up failure whose cause pasture can name, and returns
// err unchanged when it cannot name one. It returns nil for a nil error.
//
// where is the caller's own location, used for the Where line: the engine and
// the epoch controller both build a durable runtime over the same shared
// handle, and an operator needs to know which of them refused to start.
//
// Callers keep their own wrapping for the errors this function passes through.
func DescribeDurableStartupFailure(where string, err error) error {
	if err == nil {
		return nil
	}
	if isMissingDBOSSQLiteDriver(err) {
		return missingDBOSSQLiteDriverError(where, err)
	}
	return err
}

// missingDBOSSQLiteDriverError explains a binary that did not link the durable
// runtime's SQLite driver.
//
// The category is storage because that is what failed for the caller: pasture
// could not open its database. The repair, however, is a source change in this
// repository, not an operator action on the machine — the Fix line says so
// plainly, because an operator who reads "storage" alone would go looking at
// the file.
func missingDBOSSQLiteDriverError(where string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "This pasture build can't open a pasture database: its SQLite support is missing.",
		Why: "The durable-execution runtime finds its SQLite driver through a registry that only the\n" +
			"driver package fills in, and this binary never linked that package. Nothing in the code\n" +
			"names the package, so the compiler and the linker can't notice it is gone: the build\n" +
			"succeeds and the failure appears here, at the first attempt to open the database.",
		Where:  where,
		Impact: "No epoch can start or be inspected from this binary. No data was read or written.",
		Fix: "This is a defect in the build, not in the database or the machine — an operator can't\n" +
			"work around it.\n" +
			"1. Report it, with the version of pasture you are running:\n" +
			"     https://github.com/dayvidpham/pasture/issues\n" +
			"2. To repair it in a source checkout, add this blank import to the file that opens the\n" +
			"   database (internal/engine/engine.go for the engine, internal/handlers/controller.go\n" +
			fmt.Sprintf("   for lifecycle commands):\n     _ %q\n", dbosSQLiteDriverPackage) +
			"3. Rebuild, then run the tests in those two packages: they check that the import is\n" +
			"   present in each file that needs it.",
		Cause: cause,
	}
}

// SCHEMA GATE
//
// The durable runtime migrates a system database in place while it builds a
// context or a client. A database that the superseded runtime wrote is
// therefore upgraded silently, and after that moment no refusal is possible any
// more. This build supports no such in-place upgrade, so the gate below must
// run on the exact handle that becomes the runtime's system database, BEFORE
// that construction.
//
// The check itself belongs to the provenance library, which owns the durable
// schema contract and pins the floor against a recorded database written by the
// superseded runtime. Pasture calls that public gate and translates its refusal
// into the error shape pasture's own commands report. It never re-implements
// the check, so the floor cannot drift between the two repositories.
//
// The refusal is permanent: deleting the file is the only repair, and no
// re-attempt can change the recorded version. It must therefore never enter the
// bounded schema-bootstrap retry above, and it does not: the gate runs and
// returns before newDurableContext is ever called.

// RequireSupportedDurableSchema refuses a pasture database whose durable
// schema a superseded runtime wrote, and reports every other preflight failure
// in the same actionable shape.
//
// Call it on the exact *sql.DB that is about to become the durable runtime's
// system handle, before dbos.NewContext or dbos.NewClient. dbPath names the
// file for the operator. where is the caller's own location, because the engine
// and the epoch controller open the same file and an operator needs to know
// which of them refused to start.
//
// The gate only reads. On refusal nothing was opened, created, or migrated, and
// the file is byte-for-byte as it was.
func RequireSupportedDurableSchema(ctx context.Context, where string, db *sql.DB, dbPath string) error {
	if err := provenance.RequireSupportedDBOSSystemSchema(ctx, db, dbPath); err != nil {
		if errors.Is(err, provenance.ErrSupersededDBOSSystemSchema) {
			return supersededDurableSchemaError(where, dbPath, err)
		}
		return unreadableDurableSchemaError(where, dbPath, err)
	}
	return nil
}

// supersededDurableSchemaError explains a database that an older pasture build
// wrote and that this build will not open.
//
// The category is storage: what failed for the caller is the database, and the
// repair is an operator action on that file. The wrapped library error is kept
// as the cause, so errors.Is against the library sentinel still succeeds for a
// caller that wants to tell this refusal from any other storage failure.
func supersededDurableSchemaError(where, dbPath string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What: fmt.Sprintf(
			"This pasture build can't open the database %s: an older pasture build wrote it.", dbPath),
		Why: "The durable-execution state in that file has a layout that an older pasture build\n" +
			"created. This build reads a newer layout and supports no upgrade of the old one: the\n" +
			"change was a clean cut, so there is no path that carries the old records forward.",
		Where: where,
		Impact: "Nothing was opened, started, or changed, and the file is exactly as it was. No epoch\n" +
			"can run or be inspected from this build until the database is replaced.",
		Fix: fmt.Sprintf(
			"The old file can't be converted, so it has to be replaced. Its epochs and their history\n"+
				"are lost with it — read them with the older build first if you still need them.\n"+
				"1. Stop every pasture and pastured process that uses this file:\n"+
				"     pgrep -fa 'pasture|pastured'\n"+
				"2. Confirm no other process is opening it right now. A first-ever start-up is still\n"+
				"   writing the new layout while it runs, and reads as an old file until it finishes.\n"+
				"3. Delete the file and its two companions:\n"+
				"     rm %s %s-wal %s-shm\n"+
				"4. Run the command again. This build creates a fresh database on its next start.\n"+
				"   To keep the old file instead, point pasture at another path with --db <path>.",
			dbPath, dbPath, dbPath),
		Cause: cause,
	}
}

// unreadableDurableSchemaError explains a preflight that could not decide
// whether the database is usable, which is every failure of the gate other than
// the refusal above: an unreadable file, a damaged one, or a table of the same
// name that pasture did not write.
func unreadableDurableSchemaError(where, dbPath string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What: fmt.Sprintf(
			"Couldn't check the durable-execution layout of the database %s.", dbPath),
		Why: "Before it opens a database, pasture reads the recorded layout version to be sure this\n" +
			"build can use it. That read failed, so the layout is unknown. The file is normally\n" +
			"unreadable, damaged, or not the database pasture owns.",
		Where: where,
		Impact: "Nothing was opened, started, or changed. The engine did not start, so no epoch can run\n" +
			"from this process.",
		Fix: fmt.Sprintf(
			"1. Confirm the path exists and this user may read and write it:\n"+
				"     ls -l %s\n"+
				"2. Confirm the file is a healthy SQLite database:\n"+
				"     sqlite3 %s 'PRAGMA integrity_check;'\n"+
				"3. If the file belongs to another program, point pasture at its own database with\n"+
				"   --db <path>.", dbPath, dbPath),
		Cause: cause,
	}
}
