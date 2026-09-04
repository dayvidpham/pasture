package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dayvidpham/provenance"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

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
// bounded schema-bootstrap retry in dbosinit.go, and it does not: the gate runs
// and returns before newDurableContext is ever called.
//
// A BELOW-FLOOR VERSION IS NOT ALWAYS PERMANENT, AND THE GATE WAITS TO FIND OUT.
// The runtime writes its layout one migration per transaction, so a database
// that another process is creating at this moment records a version below the
// floor until that process has finished. Read once, such a file is
// indistinguishable from one an older build wrote. Two daemons started against
// one file therefore used to refuse one of them, at random, with a sentence
// that blamed an older build. The gate now tells the two apart by PROGRESS: it
// re-reads the recorded version, and treats a version that moves, or a writer
// that holds the file's write lock, as a migration in flight. It gives up in
// two ways, each with its own sentence:
//
//   - The version did not move for one SQLiteBusy window, no writer held the
//     lock in that window, and no progress was seen before. The layout is
//     standing still below the floor: an older build wrote it, or a first
//     start was interrupted. The refusal says so, and says what to do with
//     the file. Once progress HAS been seen, a quiet window does not end the
//     wait: the gate waits to the bound and reports the next case.
//   - Progress was observed, and the WorkflowResult bound ran out before the
//     layout reached the floor. Another process was migrating the file and did
//     not finish in time. The refusal says that, and never blames an older
//     build, because the gate saw the file being written.
//
// The two windows come from the injected timeout profile and nowhere else.
// SQLiteBusy is the poll interval and the stability window: it is one SQLite
// lock wait, and it is the window SQLite itself uses to decide that a lock is
// not going to clear. No tier is defined for waiting out another process's
// whole layout bootstrap; WorkflowResult is the closest, because it is the
// outermost whole-operation wait, and its production value equals the two 30 s
// retry ceilings that bound the same situation one layer later
// (busyRetryCeiling in internal/audit/migrate.go and dbosRaceRetryCeiling in
// dbosinit.go).
//
// KNOWN LIMIT — A LIVE MIGRATOR THAT COMMITS NOTHING FOR THE FIRST WINDOW LOOKS
// STABLE. The runtime's migrations are small and back to back, so a live
// process that neither commits nor holds the lock for the first SQLiteBusy
// window after the gate's first look (500 ms in production) has stalled, not
// paused. If that happens the gate refuses with the older-build sentence,
// exactly as every read of the file did before the wait existed. A stall that
// comes AFTER the gate saw progress is not refused that way: the gate waits to
// the bound and reports the migration as unfinished. The wait narrows the first
// outcome; it does not remove it.

// KNOWN LIMIT — THE GATE IS A FLOOR, NOT A RANGE. It refuses a layout version
// BELOW the supported floor and accepts every version at or above it, so a
// database written by a FUTURE build (a newer durable runtime, a higher layout
// version) is accepted here and then handed to this build's runtime. That is
// the library's contract, not a pasture choice: the check it exposes compares
// against a floor only. The consequence is bounded but real — a downgraded
// binary opens a database it does not understand, instead of refusing it with
// the message above. An upper bound has to come from the library, which owns
// the layout contract and the version it was built against; it is recorded on
// the follow-up for that repository. Re-read this note when that gate gains a
// ceiling, and stop passing the accepted case straight through.

// durableMigrationTable and durableVersionColumn name the runtime's layout
// bookkeeping. Pasture reads them for ONE purpose: to tell a layout that is
// being written from one that is standing still. The decision whether a version
// is supported stays with the library, which does not expose the number it
// read. The names are coupled to the runtime version pinned in go.mod, and
// TestTheProgressReaderReadsTheVersionTheLibraryReports in
// internal/engine/schema_gate_test.go fails when they stop matching what the
// library reports.
const (
	durableMigrationTable = "dbos_migrations"
	durableVersionColumn  = "version"
)

// durableLayoutState is what one look at the layout found.
type durableLayoutState uint8

const (
	// durableLayoutUsable: the library accepts the file — it is fresh, or its
	// recorded version is at or above the floor.
	durableLayoutUsable durableLayoutState = iota + 1
	// durableLayoutSuperseded: the recorded version is below the floor.
	durableLayoutSuperseded
	// durableLayoutWriterHeld: another connection held the file's write lock
	// for a whole busy window, so a migration transaction is in flight.
	durableLayoutWriterHeld
)

// durableLayoutObservation is one look at the layout. version and refusal are
// meaningful for durableLayoutSuperseded only.
type durableLayoutObservation struct {
	state   durableLayoutState
	version int64
	refusal error
}

// durableLayoutProbe takes one look at the layout. Production supplies
// probeDurableLayout; tests supply a scripted sequence, so the wait is driven
// without a second process.
type durableLayoutProbe func(ctx context.Context) (durableLayoutObservation, error)

// schemaGateClock is the seam through which the wait reads time and pauses.
// Production reads the wall clock and pauses on a timer; tests advance a fake
// clock on every pause, so the deadline path is asserted without a clock.
type schemaGateClock struct {
	now   func() time.Time
	pause func(ctx context.Context, d time.Duration) error
}

func realSchemaGateClock() schemaGateClock {
	return schemaGateClock{
		now: time.Now,
		pause: func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

// RequireSupportedDurableSchema refuses a pasture database whose durable
// schema a superseded runtime wrote, waits out one that another process is
// writing at this moment, and reports every other preflight failure in the
// same actionable shape.
//
// Call it on the exact *sql.DB that is about to become the durable runtime's
// system handle, before dbos.NewContext or dbos.NewClient. dbPath names the
// file for the operator. where is the caller's own location, because the engine
// and the epoch controller open the same file and an operator needs to know
// which of them refused to start. profile supplies the two windows the wait
// uses: SQLiteBusy between looks, WorkflowResult in total.
//
// The gate writes nothing. Each look may take the file's write lock for the
// time of one read and release it with a rollback; no page is written and no
// row is changed. On refusal nothing was opened, created, or migrated, and the
// records in the file are as they were.
func RequireSupportedDurableSchema(ctx context.Context, where string, db *sql.DB, dbPath string, profile timeouts.Profile) error {
	return awaitSupportedDurableSchema(ctx, where, dbPath, probeDurableLayout(db, dbPath), profile, realSchemaGateClock())
}

// awaitSupportedDurableSchema is the wait behind RequireSupportedDurableSchema,
// with its three dependencies injected: the look, the windows, and the clock.
func awaitSupportedDurableSchema(
	ctx context.Context,
	where, dbPath string,
	probe durableLayoutProbe,
	profile timeouts.Profile,
	clock schemaGateClock,
) error {
	if err := profile.Validate(); err != nil {
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConfig,
			What:     "The durable schema gate was given an unusable timeout profile.",
			Why:      err.Error(),
			Where:    where,
			Impact:   "The database was not checked and nothing was opened.",
			Fix:      "Pass the profile the caller opened its database handle with; every caller has one.",
			Cause:    err,
		}
	}
	interval := profile.SQLiteBusy()
	bound := profile.WorkflowResult()
	deadline := clock.now().Add(bound)

	first, err := probe(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return durableSchemaWaitCancelledError(where, dbPath, err, durableLayoutObservation{})
		}
		return unreadableDurableSchemaError(where, dbPath, err)
	}
	if first.state == durableLayoutUsable {
		return nil
	}

	// From here the layout is below the floor, or a writer holds the file.
	// progressed records whether the gate ever saw the file being written; it
	// decides which of the two refusals is TRUE when the wait ends.
	progressed := first.state == durableLayoutWriterHeld
	last := first
	quietSince := clock.now()
	for {
		if !clock.now().Before(deadline) {
			if progressed {
				return unfinishedDurableMigrationError(where, dbPath, bound, first, last)
			}
			return supersededDurableSchemaError(where, dbPath, last.refusal)
		}
		if err := clock.pause(ctx, interval); err != nil {
			return durableSchemaWaitCancelledError(where, dbPath, err, last)
		}
		observed, err := probe(ctx)
		if err != nil {
			// A look that failed because the caller's context ended is a
			// cancelled wait, not an unreadable file: the statement's own
			// context check is one more place the cancel can land.
			if ctx.Err() != nil {
				return durableSchemaWaitCancelledError(where, dbPath, err, last)
			}
			return unreadableDurableSchemaError(where, dbPath, err)
		}
		switch observed.state {
		case durableLayoutUsable:
			return nil
		case durableLayoutWriterHeld:
			progressed = true
			quietSince = clock.now()
		case durableLayoutSuperseded:
			moved := last.state != durableLayoutSuperseded || observed.version != last.version
			last = observed
			if moved {
				progressed = true
				quietSince = clock.now()
				continue
			}
			if !progressed && clock.now().Sub(quietSince) >= interval {
				return supersededDurableSchemaError(where, dbPath, observed.refusal)
			}
		}
	}
}

// probeDurableLayout is the production look at the layout.
//
// It asks the library first, on the pooled handle, so the decision whether the
// version is supported is the library's. Only when the library refuses does it
// take one BEGIN IMMEDIATE on the same handle: the connection string makes
// every transaction immediate, so that statement blocks on the driver's
// busy_timeout while a migration transaction holds the write lock, and a lock
// that did not clear within that window is reported as a writer in flight.
// When the lock is free, the recorded version is read under it and the
// transaction is rolled back. That is the mechanism the wait rests on, and it
// was chosen over a bare re-read because a re-read cannot see a transaction
// that is open but has not yet committed.
func probeDurableLayout(db *sql.DB, dbPath string) durableLayoutProbe {
	return func(ctx context.Context) (durableLayoutObservation, error) {
		refusal := provenance.RequireSupportedDBOSSystemSchema(ctx, db, dbPath)
		if refusal == nil {
			return durableLayoutObservation{state: durableLayoutUsable}, nil
		}
		if !errors.Is(refusal, provenance.ErrSupersededDBOSSystemSchema) {
			// The library's own read can meet a lock too: the first statement
			// on this handle opens its first connection, and on a file that
			// another process is switching to write-ahead logging at that
			// moment the switch fails with a SQLITE_BUSY no busy handler
			// retries. That is a writer in flight, not an unreadable file.
			if isSQLiteLockContention(refusal) {
				return durableLayoutObservation{state: durableLayoutWriterHeld}, nil
			}
			return durableLayoutObservation{}, refusal
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			if isSQLiteLockContention(err) {
				return durableLayoutObservation{state: durableLayoutWriterHeld}, nil
			}
			return durableLayoutObservation{}, fmt.Errorf(
				"take the write lock to read the durable layout version of %s: %w", dbPath, err)
		}
		defer func() { _ = tx.Rollback() }()
		var version int64
		if err := tx.QueryRowContext(ctx,
			"SELECT "+durableVersionColumn+" FROM "+durableMigrationTable+" LIMIT 1").Scan(&version); err != nil {
			return durableLayoutObservation{}, fmt.Errorf(
				"read the durable layout version of %s from %s after the library refused it: %w",
				dbPath, durableMigrationTable, err)
		}
		return durableLayoutObservation{state: durableLayoutSuperseded, version: version, refusal: refusal}, nil
	}
}

// isSQLiteLockContention reports whether err is SQLite saying that a lock did
// not clear: SQLITE_BUSY or SQLITE_LOCKED, with any extended code. The driver
// error is checked first; the text forms are the same ones
// internal/audit/migrate.go recognises, for an error that arrives flattened.
func isSQLiteLockContention(err error) bool {
	if err == nil {
		return false
	}
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		switch driverErr.Code() & 0xff {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "SQLITE_LOCKED") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

// supersededDurableSchemaError explains a database whose durable layout this
// build will not open, because the layout stood still below the floor for the
// whole stability window.
//
// TWO CONDITIONS PRODUCE IT, and the text names both, because the repair is the
// same but what the operator loses is not:
//  1. An older pasture build wrote the file. Its epochs and their history are
//     in there and are lost with the file.
//  2. A first start of THIS build was interrupted. The runtime applies its
//     layout steps one transaction at a time, so a start that was killed
//     part-way leaves a below-floor version behind. Nothing of value is in such
//     a file: it never finished being created.
//
// A first start that is still running is NOT one of them: the gate waits that
// case out and reports it with unfinishedDurableMigrationError when it does
// not finish. This sentence is only ever produced for a layout the gate watched
// stand still.
//
// The category is storage: what failed for the caller is the database, and the
// repair is an operator action on that file. The wrapped library error is kept
// as the cause, so errors.Is against the library sentinel still succeeds for a
// caller that wants to tell this refusal from any other storage failure.
func supersededDurableSchemaError(where, dbPath string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What: fmt.Sprintf(
			"This pasture build can't open the database %s: an older build, or an interrupted first\n"+
				"start, left it in a layout this build doesn't read.", dbPath),
		Why: "The durable-execution state in that file records an older layout than this build uses,\n" +
			"and it did not change while this process watched it. Either an older pasture build wrote\n" +
			"the file, or a first start of this build was stopped part-way and never finished writing\n" +
			"the new layout. This build supports no upgrade of the older one: the change was a clean\n" +
			"cut, so there is no path that carries old records forward.",
		Where: where,
		Impact: "Nothing was opened or started, and no pasture data was written: no epoch, no history,\n" +
			"and no layout step. Opening the file does normalise its journal mode, so its header can\n" +
			"differ; the records inside it do not. No epoch can run or be inspected from this build\n" +
			"until the database is replaced.",
		Fix: fmt.Sprintf(
			"The file can't be converted, so it has to be replaced. Step 2 tells you what you lose.\n"+
				"1. Stop every pasture and pastured process that uses this file:\n"+
				"     pgrep -fa 'pasture|pastured'\n"+
				"2. Decide which case this is.\n"+
				"   - An older build wrote it: its epochs and their history go with the file. Read them\n"+
				"     with that older build first if you still need them, then come back here.\n"+
				"   - A first start of this build was interrupted (a crash, a power loss, or a stopped\n"+
				"     container the first time this database was created): there is nothing to keep,\n"+
				"     because the file never finished being created.\n"+
				"   - A first start is running in another process but stalled: this process waited for\n"+
				"     it and saw no progress. If that process is alive, let it finish and run the\n"+
				"     command again; delete nothing.\n"+
				"3. Delete the file and its two companions:\n"+
				"     rm %s %s-wal %s-shm\n"+
				"4. Run the command again. This build creates a fresh database on its next start.\n"+
				"   To keep the old file instead, point pasture at another path with --db <path>.",
			dbPath, dbPath, dbPath),
		Cause: cause,
	}
}

// unfinishedDurableMigrationError explains a database that another process was
// migrating for the whole wait: the gate saw the recorded version move, or a
// writer hold the lock, and the bound ran out before the layout reached the
// floor.
//
// It never says "older build": the gate watched the file being written, and a
// sentence that blamed an older build would send the operator to delete a file
// another process is creating. Its cause is the last observation, not the
// library sentinel, because this is not a refusal of the file — it is a wait
// that ran out — and a caller that matches the sentinel must not mistake it
// for one.
func unfinishedDurableMigrationError(where, dbPath string, bound time.Duration, first, last durableLayoutObservation) error {
	seen := "a writer held the file's write lock"
	if first.state == durableLayoutSuperseded && last.state == durableLayoutSuperseded {
		seen = fmt.Sprintf("its recorded layout version advanced from %d to %d", first.version, last.version)
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What: fmt.Sprintf(
			"Couldn't open the database %s: another process was migrating it and did not finish\n"+
				"within %s.", dbPath, bound),
		Why: fmt.Sprintf(
			"The durable-execution layout in that file was still being written while this process\n"+
				"waited: %s. Another pasture or pastured process is creating\n"+
				"or upgrading that layout, and it had not finished when this process's wait ran out.",
			seen),
		Where: where,
		Impact: "Nothing was opened or started, and no pasture data was written. The other process's\n" +
			"work is untouched, and this process can't run an epoch until it opens the database.",
		Fix: "1. Find the process that is migrating the file:\n" +
			"     pgrep -fa 'pasture|pastured'\n" +
			"2. Let it finish, then run this command again. A first start writes the layout one step\n" +
			"   at a time and normally takes well under a second.\n" +
			"3. If that process is stuck or gone, stop it and run this command again. A layout that is\n" +
			"   no longer advancing is reported on its own, with what to do about the file.",
		Cause: fmt.Errorf("last observation of %s: %s", dbPath, describeDurableLayout(last)),
	}
}

// durableSchemaWaitCancelledError explains a wait that the caller cancelled
// while another process was still writing the layout, or while the gate was
// still deciding whether it was.
func durableSchemaWaitCancelledError(where, dbPath string, cancelCause error, last durableLayoutObservation) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What: fmt.Sprintf(
			"Opening the database %s was cancelled while its durable-execution layout was below the\n"+
				"floor this build reads.", dbPath),
		Why: fmt.Sprintf(
			"This process was waiting to see whether another process was still writing that layout\n"+
				"when its caller cancelled start-up (last observation: %s).", describeDurableLayout(last)),
		Where: where,
		Impact: "Nothing was opened or started, and no pasture data was written. The file is as the\n" +
			"other process left it.",
		Fix: "1. Identify what cancelled start-up (an interrupted command, a shutdown signal, a caller\n" +
			"   timeout) and let it clear.\n" +
			"2. Run the command again once any other pasture process has finished with the file:\n" +
			"     pgrep -fa 'pasture|pastured'",
		Cause: cancelCause,
	}
}

func describeDurableLayout(observed durableLayoutObservation) string {
	switch observed.state {
	case durableLayoutUsable:
		return "the layout is usable"
	case durableLayoutWriterHeld:
		return "a writer held the file's write lock"
	case durableLayoutSuperseded:
		return fmt.Sprintf("recorded layout version %d, below the floor", observed.version)
	default:
		return "no look at the layout completed"
	}
}

// unreadableDurableSchemaError explains a preflight that could not decide
// whether the database is usable, which is every failure of the gate other than
// the refusals above: an unreadable file, a damaged one, or a table of the same
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
		Impact: "Nothing was opened or started, and no pasture data was written. The engine did not\n" +
			"start, so no epoch can run from this process.",
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
