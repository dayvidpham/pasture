package audit

// open_retry_test.go — the bounded retry the audit trail's open runs around
// the statements outside the migrator's transaction (open_retry.go).
//
// The loop is driven with an injected operation, so each ending is reached in
// milliseconds with no second process. One test then holds a REAL lock on a
// real rollback-journal file, which is the contention the loop exists for, and
// proves the production constructor retries it and reports it truthfully.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// quickBusyRetry spends its ceiling in about twenty milliseconds.
func quickBusyRetry() busyRetryPolicy {
	return busyRetryPolicy{ceiling: 20 * time.Millisecond, initialDelay: time.Millisecond, maxDelay: 2 * time.Millisecond}
}

// withBusyRetry is the in-package option that shortens the open's retry budget.
func withBusyRetry(policy busyRetryPolicy) SqliteAuditTrailOption {
	return func(o *sqliteAuditTrailOptions) { o.busyRetry = policy }
}

var errBusy = errors.New("database is locked (5) (SQLITE_BUSY)")

func TestRetryOnBusy_RetriesALockedStatementUntilItSucceeds(t *testing.T) {
	t.Parallel()
	attempts := 0
	err := retryOnBusy(context.Background(), quickBusyRetry(), "/tmp/pasture.db", "do the thing", func() error {
		attempts++
		if attempts < 3 {
			return errBusy
		}
		return nil
	})
	if err != nil {
		t.Fatalf("a lock that cleared on the third attempt was reported as a failure: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (two locked attempts, one that succeeded)", attempts)
	}
}

func TestRetryOnBusy_ReturnsAnyOtherFailureFromTheFirstAttempt(t *testing.T) {
	t.Parallel()
	other := errors.New("no such table: audit_events")
	attempts := 0
	err := retryOnBusy(context.Background(), quickBusyRetry(), "/tmp/pasture.db", "do the thing", func() error {
		attempts++
		return other
	})
	if !errors.Is(err, other) {
		t.Fatalf("a failure that is not a lock was not returned as itself: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (only lock contention is retried)", attempts)
	}
}

func TestRetryOnBusy_SpentCeilingIsActionableAndNamesTheContention(t *testing.T) {
	t.Parallel()
	attempts := 0
	err := retryOnBusy(context.Background(), quickBusyRetry(), "/tmp/pasture.db", "create the audit tables", func() error {
		attempts++
		return errBusy
	})
	if err == nil {
		t.Fatal("a lock that never cleared was reported as success")
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("error type = %T, want *errors.StructuredError: %v", err, err)
	}
	if structured.Category != pasterrors.CategoryStorage || pasterrors.ExitCode(err) != 5 {
		t.Errorf("category = %v exit = %d, want storage / 5", structured.Category, pasterrors.ExitCode(err))
	}
	if want := "Another pasture process held the audit database while this one was opening it."; structured.What != want {
		t.Errorf("What = %q, want %q", structured.What, want)
	}
	if !strings.Contains(structured.Why, "create the audit tables") {
		t.Errorf("the report does not name the operation that was locked out: %s", structured.Why)
	}
	if !errors.Is(err, errBusy) {
		t.Errorf("the last lock error is not kept as the cause: %v", err)
	}
	if attempts < 2 {
		t.Errorf("attempts = %d; a spent ceiling must have retried at least once", attempts)
	}
}

func TestRetryOnBusy_CancelledWaitIsActionable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	slow := busyRetryPolicy{ceiling: time.Hour, initialDelay: time.Hour, maxDelay: time.Hour}
	err := retryOnBusy(ctx, slow, "/tmp/pasture.db", "do the thing", func() error {
		attempts++
		cancel()
		return errBusy
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancellation is not wrapped as the cause: %v", err)
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || !strings.Contains(structured.What, "was cancelled") {
		t.Errorf("a cancelled wait is not reported as one: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

// The contention the loop exists for, with a real lock: a rollback-journal
// file that another connection holds a write lock on while this process opens
// it. The open-time switch to write-ahead logging needs a write lock of its
// own, SQLite refuses the upgrade without invoking the busy handler, and
// before the retry the constructor gave up on that first statement with a raw
// "database is locked". The constructor must retry it, report a spent ceiling
// with the sentence that names the other process, and succeed once the lock
// is released.
//
// MUTATION: run the first statement without retryOnBusy. The first open then
// fails with the raw text and the What assertion fails.
func TestOpeningARollbackJournalFileAnotherConnectionHoldsIsRetriedAndReportedTruthfully(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.db")

	// A plain handle, no pragmas: the file stays in rollback-journal mode, as
	// a pasture older than the unified database left it.
	plain, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open a plain handle on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	if _, err := plain.ExecContext(t.Context(), `CREATE TABLE legacy (x INTEGER)`); err != nil {
		t.Fatalf("create a table in rollback-journal mode: %v", err)
	}
	holder, err := plain.Conn(t.Context())
	if err != nil {
		t.Fatalf("take a dedicated connection: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	if _, err := holder.ExecContext(t.Context(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("hold the write lock: %v", err)
	}

	// While the lock is held, the open must retry and then report the spent
	// ceiling truthfully — not the raw lock text of its first attempt.
	_, err = NewSqliteAuditTrailWithOptions(path, withBusyRetry(quickBusyRetry()),
		WithTimeoutProfile(timeouts.DeadlineTestProfile()))
	if err == nil {
		t.Fatal("the open succeeded while another connection held the write lock on a rollback-journal file")
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("the open failed with the raw lock error instead of the actionable report: %v", err)
	}
	if want := "Another pasture process held the audit database while this one was opening it."; structured.What != want {
		t.Errorf("What = %q, want %q\nfull error: %v", structured.What, want, err)
	}
	if pasterrors.ExitCode(err) != 5 {
		t.Errorf("exit code = %d, want 5 (storage)", pasterrors.ExitCode(err))
	}

	// Once the lock is released the same open succeeds, and the file is now in
	// write-ahead logging mode.
	if _, err := holder.ExecContext(t.Context(), `ROLLBACK`); err != nil {
		t.Fatalf("release the write lock: %v", err)
	}
	trail, err := NewSqliteAuditTrailWithOptions(path, withBusyRetry(quickBusyRetry()),
		WithTimeoutProfile(timeouts.DeadlineTestProfile()))
	if err != nil {
		t.Fatalf("the open failed after the lock was released: %v", err)
	}
	t.Cleanup(func() { _ = trail.Close() })
	var mode string
	if err := trail.db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read the journal mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q after the open, want %q", mode, "wal")
	}
}
