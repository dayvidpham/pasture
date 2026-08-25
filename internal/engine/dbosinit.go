package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// DBOS Transact bootstraps its own system-database schema the first time a
// process opens a SQLite file: it probes sqlite_master for the migrations
// table, creates it when absent, then applies each numbered migration in its
// own transaction. That probe-then-create is not atomic, and the migration
// bodies are not written with IF NOT EXISTS, so two pasture processes opening
// the same fresh pasture.db can both decide the schema is missing. One wins;
// the loser's CREATE fails against the schema the winner has just committed.
//
// The library retries its own migration run only for SQLITE_BUSY / SQLITE_LOCKED
// (dbos/internal/sysdb/dialect.go SqliteDialect.IsRetryable), and even that is
// defeated for migration failures because dbos/internal/sysdb/sqlite_migrations.go
// formats every error with %v and so severs the driver error from the chain.
// A lost race therefore surfaces immediately as a plain SQLITE_ERROR (code 1)
// "already exists" string and kills process start-up.
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
// dbos.NewDBOSContext; tests inject a stub to drive the retry loop
// deterministically, without a second process and without any sleep.
type dbosContextFactory func(context.Context, dbos.Config) (dbos.DBOSContext, error)

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
// Any error that is not the race signature is returned on the first attempt,
// unchanged, so a genuine configuration or corruption failure still fails fast.
func newDurableContext(
	ctx context.Context,
	newCtx dbosContextFactory,
	cfg dbos.Config,
	policy dbosRetryPolicy,
) (dbos.DBOSContext, error) {
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
			return nil, err
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
// schema-bootstrap failure (dbos/internal/sysdb/sqlite_pool.go). Requiring it
// keeps the retry predicate scoped to schema bootstrap: an "already exists"
// from anywhere else in start-up is still fatal on the first attempt.
const dbosMigrationFailurePrefix = "failed to run sqlite migrations"

// dbosBootstrapRaceMarkers are the ways a lost schema-bootstrap race shows up,
// in the order the library can produce them: the migrations-table create loses
// the probe-then-create window; a migration body re-creates a table or index,
// or re-adds a column, the winner already committed; the version bookkeeping
// row is inserted twice; or the loser is simply still queued behind the
// winner's write lock. Every one of them is repaired by re-running the
// bootstrap against the winner's committed schema.
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
// sqlite error to inspect. The match is coupled to
// github.com/dbos-inc/dbos-transact-golang v0.20.0; a version bump must
// re-check dbos/internal/sysdb/sqlite_migrations.go and sqlite_pool.go. A
// signature that stops matching costs an actionable start-up failure under
// concurrency, not silent corruption.
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
