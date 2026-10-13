package audit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// auditWriterPolicy governs a whole atomic write, including pool acquisition.
// Seams are per-trail, configured by in-package tests before concurrent use.
type auditWriterPolicy struct {
	ceiling, initialDelay, maxDelay time.Duration
	afterAttempt                    func(int, error)
	onBackoff                       func(time.Duration)
	commit                          func(*sql.Tx) error
	restore                         func(context.Context, *sql.Conn, int64) error
}

func defaultAuditWriterPolicy() auditWriterPolicy {
	return auditWriterPolicy{ceiling: 30 * time.Second, initialDelay: 25 * time.Millisecond, maxDelay: time.Second}
}

// Direct modernc statements retain code-bearing error identity (including
// extended codes). There is no evidenced stripped-code producer here, so no
// text fallback for lock messages, bootstrap collisions or SQL errors. Unlike
// the migration compatibility predicate isBusyError in migrate.go, forensic
// INSERT failures have no producer that flattens their code-bearing cause.
func isAuditWriteContention(err error) bool {
	var coded interface {
		error
		Code() int
	}
	if !errors.As(err, &coded) {
		return false
	}
	primary := coded.Code() & 255
	return primary == 5 || primary == 6
}

type auditAttemptResult struct {
	err    error
	retry  bool
	impact string
	what   string
}

const auditCommitStage = "commit atomic write"

func (s *SqliteAuditTrail) withAuditWrite(ctx context.Context, operation string, op func(context.Context, *sql.Tx) error) error {
	if ctx == nil {
		return contextValidationError(operation)
	}
	p := s.writerPolicy
	if p.ceiling == 0 {
		defaults := defaultAuditWriterPolicy()
		p.ceiling, p.initialDelay, p.maxDelay = defaults.ceiling, defaults.initialDelay, defaults.maxDelay
	}
	budget, cancel := context.WithTimeout(ctx, p.ceiling)
	defer cancel()
	return retryAuditWrite(budget, operation, p, func(ctx context.Context) auditAttemptResult {
		return s.auditWriteAttempt(ctx, p, op)
	})
}

func retryAuditWrite(ctx context.Context, operation string, p auditWriterPolicy, attempt func(context.Context) auditAttemptResult) error {
	delay := p.initialDelay
	var last error
	attempts := 0
	for {
		if err := ctx.Err(); err != nil {
			return auditWriteError(operation, attempts, p.ceiling, errors.Join(err, last), "No transaction committed in this call; failed attempts were rolled back or never started.", "")
		}
		attempts++
		result := attempt(ctx)
		if p.afterAttempt != nil {
			p.afterAttempt(attempts, result.err)
		}
		if result.err == nil {
			return nil
		}
		last = result.err
		if ctx.Err() != nil {
			return auditWriteError(operation, attempts, p.ceiling, errors.Join(ctx.Err(), last), result.impact, result.what)
		}
		if !result.retry {
			return auditWriteError(operation, attempts, p.ceiling, last, result.impact, result.what)
		}
		deadline, _ := ctx.Deadline()
		wait := delay
		if !deadline.IsZero() {
			wait = min(wait, max(time.Until(deadline), 0))
		}
		timer := time.NewTimer(wait)
		if p.onBackoff != nil {
			p.onBackoff(wait)
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return auditWriteError(operation, attempts, p.ceiling, errors.Join(ctx.Err(), last), result.impact, result.what)
		case <-timer.C:
		}
		delay = min(delay*2, p.maxDelay)
	}
}

func (s *SqliteAuditTrail) auditWriteAttempt(ctx context.Context, p auditWriterPolicy, op func(context.Context, *sql.Tx) error) auditAttemptResult {
	const unchanged = "No transaction committed in this call; failed attempts were rolled back or never started."
	if err := ctx.Err(); err != nil {
		return auditAttemptResult{err: err, impact: unchanged}
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return auditAttemptResult{err: fmt.Errorf("acquire configured connection: %w", err), retry: isAuditWriteContention(err), impact: unchanged}
	}
	// Reserve cleanup headroom inside the configured tier, not an independent
	// busy wait after cancellation. Deadline clamping floors milliseconds to 0.
	tier := s.busyTier
	window := tier
	if deadline, ok := ctx.Deadline(); ok {
		window = min(window, max(time.Until(deadline), 0))
	}
	cleanupBudget := min(100*time.Millisecond, window/5)
	cap := window - cleanupBudget
	var tx *sql.Tx
	committed, uncertain := false, false
	discard := false
	stage := "set connection-local busy timeout"
	_, err = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", cap.Milliseconds()))
	if err == nil {
		stage = "begin immediate write transaction"
		if err = ctx.Err(); err == nil {
			tx, err = conn.BeginTx(ctx, nil)
		}
	}
	if err == nil {
		stage = "execute atomic write"
		err = op(ctx, tx)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		stage = auditCommitStage
		commit := p.commit
		if commit == nil {
			commit = (*sql.Tx).Commit
		}
		err = commit(tx)
		committed = err == nil
		// database/sql marks Tx done before driver commit. A subsequent
		// Rollback cannot confirm non-commit; never blindly replay that failure.
		uncertain = err != nil
	}
	if tx != nil && !committed {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !(stage != auditCommitStage && ctx.Err() != nil && errors.Is(rollbackErr, sql.ErrTxDone)) {
			// Before commit, ErrTxDone on cancellation means database/sql owns
			// rollback. Conn.Close joins its transaction release. A genuine
			// rollback failure requires discard, but cannot imply a commit that
			// was never attempted. Commit failures remain uncertain regardless.
			discard = true
			err = errors.Join(err, fmt.Errorf("rollback could not confirm non-commit: %w", rollbackErr))
		}
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	restore := p.restore
	if restore == nil {
		restore = restoreAuditBusyTier
	}
	var cleanupErr error
	if !uncertain && !discard {
		cleanupErr = restore(cleanupCtx, conn, tier.Milliseconds())
	}
	if uncertain || discard || cleanupErr != nil {
		// ErrBadConn from Raw synchronously closes/removes the driver connection.
		// No reduced busy tier or uncertain transaction is returned to the pool.
		discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
		if !errors.Is(discardErr, driver.ErrBadConn) && !errors.Is(discardErr, sql.ErrConnDone) {
			cleanupErr = errors.Join(cleanupErr, discardErr)
		}
	}
	cancel()
	closeErr := conn.Close()
	if errors.Is(closeErr, sql.ErrConnDone) {
		closeErr = nil
	}
	cleanupErr = errors.Join(cleanupErr, closeErr)
	impact := unchanged
	what := ""
	if discard && !uncertain {
		impact = "The transaction never reached commit; its connection was discarded because rollback could not verify reusability. The failed write was not committed."
	}
	if committed {
		impact = "The complete atomic write committed. Do not blindly resend an ordinary event or batch; inspect the recorded rows first."
		what = "The atomic audit write committed, but releasing its database connection failed."
	}
	if uncertain {
		impact = "The transaction outcome could not be confirmed; its connection was discarded. Inspect the recorded event or batch before any retry to avoid duplicates."
		what = "Couldn't confirm whether the atomic audit write committed."
	}
	if err == nil && cleanupErr == nil {
		return auditAttemptResult{}
	}
	return auditAttemptResult{
		err:    errors.Join(wrapAuditStage(stage, err), wrapAuditStage("restore or discard reserved connection", cleanupErr)),
		retry:  !committed && !uncertain && !discard && cleanupErr == nil && isAuditWriteContention(err),
		impact: impact,
		what:   what,
	}
}

func restoreAuditBusyTier(ctx context.Context, conn *sql.Conn, ms int64) error {
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", ms)); err != nil {
		return err
	}
	var got int64
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&got); err != nil {
		return err
	}
	if got != ms {
		return fmt.Errorf("busy timeout read-back %d, expected %d", got, ms)
	}
	return nil
}

func wrapAuditStage(stage string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", stage, err)
}

func auditWriteError(operation string, attempts int, ceiling time.Duration, cause error, impact, what string) error {
	if what == "" {
		what = fmt.Sprintf("Couldn't complete %s in the audit database.", operation)
	}
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     what,
		Why:      fmt.Sprintf("The operation failed after %d attempt(s) within one %s budget (or the earlier caller deadline): %v", attempts, ceiling, cause),
		Where:    "Atomic audit writer (internal/audit/write_retry.go in audit.SqliteAuditTrail.withAuditWrite).",
		Impact:   impact,
		Fix:      "Check SQLite lock holders, storage permissions and database integrity. Retry with a live context after contention clears only when non-commit is confirmed; inspect rows first for an uncertain or committed outcome.",
		Cause:    cause,
	}
}
