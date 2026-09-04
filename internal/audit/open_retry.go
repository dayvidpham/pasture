package audit

// open_retry.go — the bounded retry the audit trail's open runs around the
// statements that sit OUTSIDE the migrator's own transaction.
//
// Two pasture processes that open the same file at the same moment contend
// before the migrator ever runs. The clearest case is a file still in
// rollback-journal mode, which is what a pasture older than the unified
// database leaves behind: the first connection's open-time pragmas switch it to
// write-ahead logging, and that switch reads the header under a read
// transaction and then upgrades to a write lock. SQLite does not invoke the
// busy handler on a read-to-write upgrade, so the process that loses the
// upgrade gets a plain SQLITE_BUSY at its first statement, and busy_timeout
// never sees it. The same first statement can also meet a lock the other
// process holds for longer than one busy window. In both cases nothing about
// the file is wrong, and a fresh attempt a moment later succeeds: the file is
// in WAL mode by then, or the lock has cleared.
//
// The loop below is the migrator's own busy retry (beginImmediateWithRetry)
// applied to those statements. It shares the migrator's backoff and its
// ceiling, so an operator reads one budget for "another process holds the
// audit database": busyRetryCeiling, 30 s.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// busyRetryPolicy bounds one retry loop over a statement another process can
// hold off: the first pause, the pause cap, and when the last attempt may
// start. The ceiling bounds the START of the last attempt, not total wall
// clock; an attempt already under way is never abandoned.
type busyRetryPolicy struct {
	ceiling      time.Duration
	initialDelay time.Duration
	maxDelay     time.Duration
}

// defaultBusyRetryPolicy is the migrator's budget, applied to the open.
func defaultBusyRetryPolicy() busyRetryPolicy {
	return busyRetryPolicy{
		ceiling:      busyRetryCeiling,
		initialDelay: busyRetryInitialDelay,
		maxDelay:     busyRetryMaxDelay,
	}
}

// retryOnBusy runs op and, while it fails with SQLITE_BUSY or SQLITE_LOCKED,
// runs it again on the policy's backoff until it succeeds, fails another way,
// the caller's context ends, or the ceiling is spent. Any error that is not
// lock contention is returned unchanged from the first attempt. doing names
// the operation, in the infinitive, for the report a spent ceiling produces.
func retryOnBusy(ctx context.Context, policy busyRetryPolicy, dbPath, doing string, op func() error) error {
	deadline := time.Now().Add(policy.ceiling)
	delay := policy.initialDelay
	attempts := 0
	for {
		attempts++
		err := op()
		if err == nil {
			return nil
		}
		if !isBusyError(err) {
			return err
		}
		if time.Now().After(deadline) {
			return openHeldByAnotherProcessError(dbPath, doing, policy.ceiling, attempts, err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return openCancelledError(dbPath, doing, attempts, ctx.Err(), err)
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

// execWithBusyRetry runs one statement through retryOnBusy.
func execWithBusyRetry(ctx context.Context, policy busyRetryPolicy, db *sql.DB, dbPath, doing, stmt string) error {
	return retryOnBusy(ctx, policy, dbPath, doing, func() error {
		_, err := db.ExecContext(ctx, stmt)
		return err
	})
}

// isStructuredError reports whether err carries a *pasterrors.StructuredError,
// which callers return unwrapped so the exit code follows its category.
func isStructuredError(err error) bool {
	var structured *pasterrors.StructuredError
	return errors.As(err, &structured)
}

// openHeldByAnotherProcessError explains an open that met a locked file on
// every attempt for the whole ceiling. It names the contention, so a process
// that loses a concurrent open exits with a sentence that is true for what
// happened to it.
func openHeldByAnotherProcessError(dbPath, doing string, ceiling time.Duration, attempts int, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Another pasture process held the audit database while this one was opening it.",
		Why: fmt.Sprintf(
			"This process tried %d time(s) over more than %s to %s on %s, and SQLite reported the\n"+
				"file locked every time. Another pasture or pastured process was writing it — normally\n"+
				"switching it to write-ahead logging or upgrading its schema — and did not let go within\n"+
				"that budget.",
			attempts, ceiling, doing, dbPath),
		Where: "Opening the audit database (internal/audit/sqlite.go in audit.NewSqliteAuditTrail).",
		Impact: "This process didn't open the audit database, so it can't record or read events.\n" +
			"No data was changed by this attempt.",
		Fix: "1. Check which pasture process holds the file:\n" +
			"     pgrep -fa 'pasture|pastured'\n" +
			"2. Let it finish, then run this command again.\n" +
			"3. If that process is stuck, stop it and run this command again.",
		Cause: cause,
	}
}

// openCancelledError explains an open that its caller cancelled while another
// process still held the file.
func openCancelledError(dbPath, doing string, attempts int, cancelCause, lastErr error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     "Opening the audit database was cancelled while another pasture process held it.",
		Why: fmt.Sprintf(
			"After %d attempt(s) to %s on %s, this process was still waiting for the file lock to clear\n"+
				"when its caller cancelled start-up (last error: %v).",
			attempts, doing, dbPath, lastErr),
		Where:  "Opening the audit database (internal/audit/sqlite.go in audit.NewSqliteAuditTrail).",
		Impact: "This process didn't open the audit database. No data was changed by this attempt.",
		Fix: "1. Identify what cancelled start-up (an interrupted command, a shutdown signal, a caller\n" +
			"   timeout) and let it clear.\n" +
			"2. Run the command again once the other process has finished with the file:\n" +
			"     pgrep -fa 'pasture|pastured'",
		Cause: cancelCause,
	}
}
