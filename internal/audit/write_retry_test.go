package audit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"gopkg.in/yaml.v3"
)

const writerSchedulingTolerance = 75 * time.Millisecond

type writerFixtureName string

// testutil imports audit for golden databases, so in-package policy tests use
// the same strict decoding locally rather than creating an import cycle.
func loadWriterFixture(t *testing.T, name writerFixtureName, out any) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", string(name)+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := yaml.NewDecoder(f)
	d.KnownFields(true)
	if err := d.Decode(out); err != nil {
		t.Fatal(err)
	}
}

func writerTrail(t *testing.T, profile timeouts.Profile) (*SqliteAuditTrail, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.db")
	s, err := NewSqliteAuditTrailWithOptions(path, WithTimeoutProfile(profile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, path
}

func writerHolder(t *testing.T, path string) *sql.Tx {
	t.Helper()
	db, err := dbconn.OpenSharedDBContextWithProfile(context.Background(), path, timeouts.ProductionProfile())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(context.Background(), `UPDATE audit_schema_meta SET applied_at=applied_at`); err != nil {
		t.Fatal(err)
	}
	return tx // completed BEGIN IMMEDIATE and write are readiness evidence.
}

func writerEvent() protocol.AuditEvent {
	return protocol.AuditEvent{Role: "worker", EpochId: "writer-epoch", Timestamp: time.Now()}
}

func writerCount(t *testing.T, s *SqliteAuditTrail, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func writerCacheSize(s *SqliteAuditTrail) int {
	s.roleMu.Lock()
	defer s.roleMu.Unlock()
	return len(s.roleToAgentId)
}

func TestAuditWriterShortColdRoleBudget(t *testing.T) {
	t.Parallel()
	const cases writerFixtureName = "audit_writer_profiles"
	var fixtures []struct {
		Name       string `yaml:"name"`
		Production bool   `yaml:"production"`
	}
	loadWriterFixture(t, cases, &fixtures)
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			profile := timeouts.DeadlineTestProfile()
			if f.Production {
				profile = timeouts.ProductionProfile()
			}
			s, path := writerTrail(t, profile)
			holder := writerHolder(t, path)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			start := time.Now()
			id, err := s.RecordEventReturningId(ctx, writerEvent())
			elapsed := time.Since(start)
			if !errors.Is(err, context.DeadlineExceeded) || id != 0 {
				t.Fatalf("id=%d error=%v", id, err)
			}
			if elapsed > 25*time.Millisecond+writerSchedulingTolerance {
				t.Fatalf("elapsed %s, tolerance %s", elapsed, writerSchedulingTolerance)
			}
			t.Logf("caller budget 25ms, elapsed %s, scheduler allowance %s", elapsed, writerSchedulingTolerance)
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			if writerCacheSize(s) != 0 || writerCount(t, s, "agents") != 0 || writerCount(t, s, "audit_events") != 0 {
				t.Fatal("cancelled write published state")
			}
			var busy int64
			if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
				t.Fatal(err)
			}
			if busy != profile.SQLiteBusy().Milliseconds() {
				t.Fatalf("leaked busy tier %d", busy)
			}
		})
	}
}

func TestAuditWriterLongHolderRetries(t *testing.T) {
	t.Parallel()
	s, path := writerTrail(t, timeouts.ProductionProfile())
	holder := writerHolder(t, path)
	failed := make(chan time.Time, 1)
	s.writerPolicy = defaultAuditWriterPolicy()
	s.writerPolicy.afterAttempt = func(_ int, err error) {
		if err != nil {
			select {
			case failed <- time.Now():
			default:
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan struct{})
	var id int64
	var err error
	// Failure cleanup cancels, releases and joins before the temp store is removed.
	t.Cleanup(func() { cancel(); _ = holder.Rollback(); <-done })
	start := time.Now()
	go func() { defer close(done); id, err = s.RecordEventReturningId(ctx, writerEvent()) }()
	select {
	case failedAt := <-failed:
		// The 100ms cleanup reserve is inside the 500ms tier. Removing it
		// would exceed this separately named 75ms scheduler allowance.
		ceiling := s.busyTier - min(100*time.Millisecond, s.busyTier/5) + writerSchedulingTolerance
		if failedAt.Sub(start) > ceiling {
			t.Fatalf("first exhausted attempt took %s, maximum %s including scheduler allowance", failedAt.Sub(start), ceiling)
		}
	case <-done:
		t.Fatalf("completed before observed retry: %v", err)
	case <-ctx.Done():
		t.Fatal("no exhausted attempt")
	}
	if remaining := 750*time.Millisecond - time.Since(start); remaining > 0 {
		timer := time.NewTimer(remaining)
		<-timer.C
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	<-done
	if err != nil || id <= 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	for _, table := range []string{"agents", "agents_software", "audit_events", "context_edges"} {
		if writerCount(t, s, table) != 1 {
			t.Fatalf("incomplete atomic write: %s", table)
		}
	}
}

func TestAuditWriterPermanentFailureRollsBackRegistration(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	if _, err := s.db.Exec(`CREATE TRIGGER refuse_context BEFORE INSERT ON context_edges BEGIN SELECT RAISE(ABORT,'permanent failure'); END`); err != nil {
		t.Fatal(err)
	}
	id, err := s.RecordEventReturningId(context.Background(), writerEvent())
	if err == nil || id != 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	for _, table := range []string{"agents", "agents_software", "audit_events", "context_edges"} {
		if writerCount(t, s, table) != 0 {
			t.Fatalf("rollback leaked %s", table)
		}
	}
	if writerCacheSize(s) != 0 {
		t.Fatal("rollback published role cache")
	}
}

func TestAuditWriterLongHolderOtherModes(t *testing.T) {
	t.Parallel()
	const cases writerFixtureName = "audit_writer_modes"
	var fixtures []struct {
		Mode string `yaml:"mode"`
	}
	loadWriterFixture(t, cases, &fixtures)
	for _, f := range fixtures {
		t.Run(f.Mode, func(t *testing.T) {
			t.Parallel()
			s, path := writerTrail(t, timeouts.ProductionProfile())
			event := writerEvent()
			var canonical int64
			if f.Mode == "warm" || f.Mode == "dedup" {
				if f.Mode == "dedup" {
					event.DedupKey = "writer-dedup"
				}
				var err error
				canonical, err = s.RecordEventReturningId(context.Background(), event)
				if err != nil {
					t.Fatal(err)
				}
			}
			if f.Mode == "well-known" {
				event.Role = "pasture/automaton/writer-test"
				if _, err := s.db.Exec(`INSERT INTO agents(id,kind_id) VALUES('direct',2); INSERT INTO agents_software(agent_id,name,version,source) VALUES('direct',?, 'test','test')`, event.Role); err != nil {
					t.Fatal(err)
				}
			}
			tables := []string{"agents", "agents_software", "audit_events", "context_edges", "session_entries"}
			before := make(map[string]int)
			for _, table := range tables {
				before[table] = writerCount(t, s, table)
			}
			holder := writerHolder(t, path)
			failed := make(chan struct{}, 1)
			s.writerPolicy = defaultAuditWriterPolicy()
			s.writerPolicy.afterAttempt = func(_ int, err error) {
				if err != nil {
					select {
					case failed <- struct{}{}:
					default:
					}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			done := make(chan struct{})
			var id int64
			var err error
			t.Cleanup(func() { cancel(); _ = holder.Rollback(); <-done })
			start := time.Now()
			go func() {
				defer close(done)
				if f.Mode == "batch" {
					err = s.RecordSessionEntries(ctx, []protocol.SessionEntry{{SessionId: "batch", EntryIndex: 0}, {SessionId: "batch", EntryIndex: 1}})
				} else {
					id, err = s.RecordEventReturningId(ctx, event)
				}
			}()
			select {
			case <-failed:
			case <-done:
				t.Fatalf("no exhausted attempt: %v", err)
			case <-ctx.Done():
				t.Fatal("no attempt evidence")
			}
			if remaining := 750*time.Millisecond - time.Since(start); remaining > 0 {
				timer := time.NewTimer(remaining)
				<-timer.C
			}
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			<-done
			if err != nil {
				t.Fatal(err)
			}
			if time.Since(start) >= 3*time.Second {
				t.Fatal("write exceeded operation deadline")
			}
			if f.Mode == "dedup" && id != canonical {
				t.Fatalf("canonical %d, replay %d", canonical, id)
			}
			for _, table := range tables {
				want := before[table]
				if f.Mode == "batch" {
					if table == "session_entries" {
						want += 2
					}
				} else if f.Mode != "dedup" && (table == "audit_events" || table == "context_edges") {
					want++
				}
				if got := writerCount(t, s, table); got != want {
					t.Fatalf("%s count %d want %d", table, got, want)
				}
			}
		})
	}
}

func TestAuditWriterIndependentTrailsShareOneRole(t *testing.T) {
	t.Parallel()
	s, path := writerTrail(t, timeouts.ProductionProfile())
	peer, err := NewSqliteAuditTrail(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	t.Cleanup(func() { cancel(); wg.Wait() })
	for _, trail := range []*SqliteAuditTrail{s, peer} {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- trail.RecordEvent(ctx, writerEvent()) }()
	}
	close(start)
	wg.Wait()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if writerCount(t, s, "agents") != 1 || writerCount(t, s, "agents_software") != 1 || writerCount(t, s, "audit_events") != 2 || writerCount(t, s, "context_edges") != 2 {
		t.Fatal("independent trails duplicated or orphaned role registration")
	}
}

func TestAuditWriterPoolWaitAndSessionReadCancellation(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	check := func(operation func(context.Context) error) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := operation(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("pool cancellation cause: %v", err)
		}
		if time.Since(start) > 25*time.Millisecond+writerSchedulingTolerance {
			t.Fatal("pool wait ignored caller budget")
		}
	}
	check(func(ctx context.Context) error { return s.RecordEvent(ctx, writerEvent()) })
	check(func(ctx context.Context) error {
		return s.RecordSessionEntries(ctx, []protocol.SessionEntry{{SessionId: "pool"}})
	})
	check(func(ctx context.Context) error { _, err := s.QuerySessionEntries(ctx, "pool"); return err })
}

func TestAuditWriterBatchPermanentFailureIsAtomic(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	if _, err := s.db.Exec(`CREATE TRIGGER refuse_second BEFORE INSERT ON session_entries WHEN NEW.entry_index=1 BEGIN SELECT RAISE(ABORT,'permanent failure'); END`); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	s.writerPolicy = defaultAuditWriterPolicy()
	s.writerPolicy.afterAttempt = func(_ int, _ error) { attempts++ }
	err := s.RecordSessionEntries(context.Background(), []protocol.SessionEntry{{SessionId: "batch", EntryIndex: 0}, {SessionId: "batch", EntryIndex: 1}})
	if err == nil || attempts != 1 || writerCount(t, s, "session_entries") != 0 {
		t.Fatalf("error %v attempts %d", err, attempts)
	}
}

type writerCodeError int

func (e writerCodeError) Error() string { return fmt.Sprintf("SQLite code %d", e) }
func (e writerCodeError) Code() int     { return int(e) }

func TestAuditWriterClassifier(t *testing.T) {
	t.Parallel()
	const cases writerFixtureName = "audit_writer_codes"
	var fixtures []struct {
		Name  string `yaml:"name"`
		Code  int    `yaml:"code"`
		Retry bool   `yaml:"retry"`
	}
	loadWriterFixture(t, cases, &fixtures)
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			for _, err := range []error{writerCodeError(f.Code), fmt.Errorf("wrapped: %w", writerCodeError(f.Code))} {
				if got := isAuditWriteContention(err); got != f.Retry {
					t.Fatalf("%v classified %v want %v", err, got, f.Retry)
				}
				attempts := 0
				p := auditWriterPolicy{ceiling: time.Second, initialDelay: time.Millisecond, maxDelay: time.Millisecond}
				ctx, cancel := context.WithTimeout(context.Background(), p.ceiling)
				result := retryAuditWrite(ctx, "classification attempts", p, func(context.Context) auditAttemptResult {
					attempts++
					if attempts == 3 {
						return auditAttemptResult{}
					}
					return auditAttemptResult{err: err, retry: isAuditWriteContention(err), impact: "Test attempt never wrote."}
				})
				cancel()
				if f.Retry {
					if result != nil || attempts != 3 {
						t.Fatalf("retryable code %d: result %v attempts %d", f.Code, result, attempts)
					}
				} else if !errors.Is(result, err) || attempts != 1 {
					t.Fatalf("permanent code %d: result %v attempts %d", f.Code, result, attempts)
				}
			}
		})
	}
	for _, err := range []error{nil, errors.New("already exists"), errors.New("database is locked"), errors.New("UNIQUE constraint failed")} {
		if isAuditWriteContention(err) {
			t.Fatalf("retried text-only error %v", err)
		}
	}
}

func TestAuditWriterAmbiguousCommittedOutcomeIsNeverReplayed(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	attempts := 0
	cause := errors.New("lost commit acknowledgement")
	s.writerPolicy = defaultAuditWriterPolicy()
	s.writerPolicy.afterAttempt = func(_ int, _ error) { attempts++ }
	s.writerPolicy.commit = func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return cause
	}
	id, err := s.RecordEventReturningId(context.Background(), writerEvent())
	if !errors.Is(err, cause) || id != 0 || attempts != 1 {
		t.Fatalf("id %d err %v attempts %d", id, err, attempts)
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || !strings.Contains(structured.Impact, "could not be confirmed") {
		t.Fatalf("false commit impact: %v", err)
	}
	if writerCount(t, s, "audit_events") != 1 || writerCacheSize(s) != 0 {
		t.Fatal("ambiguous outcome was replayed or cached")
	}
}

func TestAuditWriterAlreadyCancelledAndExpired(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	attempts := 0
	s.writerPolicy = defaultAuditWriterPolicy()
	s.writerPolicy.afterAttempt = func(_ int, _ error) { attempts++ }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.RecordEvent(ctx, writerEvent()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if err := s.RecordSessionEntries(expired, []protocol.SessionEntry{{SessionId: "expired"}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if attempts != 0 || writerCount(t, s, "agents") != 0 {
		t.Fatal("attempt started after cancellation")
	}
}

func TestAuditWriterReplacementConnectionUsesThisBudget(t *testing.T) {
	t.Parallel()
	s, path := writerTrail(t, timeouts.ProductionProfile())
	// The constructor is done; force the writer's Conn to perform real first
	// use of a replacement connection while the independent holder stays locked.
	s.db.SetMaxIdleConns(0)
	holder := writerHolder(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.RecordEvent(ctx, writerEvent())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(start) > 25*time.Millisecond+writerSchedulingTolerance {
		t.Fatal("replacement connection lost current acquisition deadline")
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxIdleConns(1)
	if err := s.RecordEvent(context.Background(), writerEvent()); err != nil {
		t.Fatal(err)
	}
	var busy int64
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != timeouts.ProductionProfile().SQLiteBusy().Milliseconds() {
		t.Fatalf("replacement leaked busy tier %d", busy)
	}
}

func TestAuditWriterRestoreFailureDiscardsConnection(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var before any
	if err := conn.Raw(func(raw any) error { before = raw; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("restore refused")
	s.writerPolicy = defaultAuditWriterPolicy()
	s.writerPolicy.restore = func(ctx context.Context, _ *sql.Conn, _ int64) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 100*time.Millisecond {
			t.Error("cleanup has no finite shared budget")
		}
		return cause
	}
	err = s.RecordEvent(context.Background(), writerEvent())
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || !strings.Contains(structured.Impact, "committed") || !strings.Contains(structured.What, "write committed") {
		t.Fatalf("lost successful commit outcome: %v", err)
	}
	s.writerPolicy = defaultAuditWriterPolicy()
	conn, err = s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		if raw == before {
			t.Error("bad connection returned to pool")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var busy int64
	if err := conn.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != s.busyTier.Milliseconds() {
		t.Fatal("discard replacement not fully configured")
	}
}

func TestAuditWriterRealBusySnapshotClassification(t *testing.T) {
	t.Parallel()
	s, path := writerTrail(t, timeouts.ProductionProfile())
	peer, err := sql.Open("sqlite", dbconn.SharedDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	// Use explicit DEFERRED on a reserved connection to create the stale read
	// snapshot. The production writer starts IMMEDIATE, so it never upgrades
	// this snapshot; classification still must cover drivers returning it.
	conn, err := peer.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN DEFERRED"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()
	var n int
	if err := conn.QueryRowContext(context.Background(), "SELECT count(*) FROM audit_events").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEvent(context.Background(), writerEvent()); err != nil {
		t.Fatal(err)
	}
	_, err = conn.ExecContext(context.Background(), "INSERT INTO agents(id,kind_id) VALUES('snapshot',2)")
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code() != 517 || !isAuditWriteContention(fmt.Errorf("snapshot: %w", err)) {
		t.Fatalf("expected typed BUSY_SNAPSHOT, got %v", err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
}

func TestAuditWriterRetryCancellationPreventsNextAttempt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := defaultAuditWriterPolicy()
	p.afterAttempt = func(n int, _ error) {
		if n == 1 {
			cancel()
		}
	}
	attempts := 0
	busy := writerCodeError(517)
	err := retryAuditWrite(ctx, "test retry", p, func(context.Context) auditAttemptResult {
		attempts++
		return auditAttemptResult{err: busy, retry: true, impact: "Rolled back."}
	})
	if attempts != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, busy) {
		t.Fatalf("attempts %d err %v", attempts, err)
	}
}

func TestAuditWriterBudgetIncludesAllAttempts(t *testing.T) {
	t.Parallel()
	s, path := writerTrail(t, timeouts.ProductionProfile())
	holder := writerHolder(t, path)
	defer holder.Rollback()
	s.writerPolicy = auditWriterPolicy{ceiling: 40 * time.Millisecond, initialDelay: time.Millisecond, maxDelay: time.Millisecond}
	attempts := 0
	s.writerPolicy.afterAttempt = func(_ int, _ error) { attempts++ }
	start := time.Now()
	err := s.RecordEvent(context.Background(), writerEvent())
	if !errors.Is(err, context.DeadlineExceeded) || attempts == 0 {
		t.Fatalf("attempts %d err %v", attempts, err)
	}
	if time.Since(start) > 40*time.Millisecond+writerSchedulingTolerance {
		t.Fatal("per-attempt budget reset")
	}
	if defaultAuditWriterPolicy().ceiling != 30*time.Second || busyRetryCeiling != 30*time.Second {
		t.Fatal("global or writer ceilings changed")
	}
}

func TestAuditWriterCancellationDuringBatchSQLRollsBack(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.withAuditWrite(ctx, "mid-batch cancellation", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agents(id,kind_id) VALUES('cancel-mid-write',2)`); err != nil {
			return err
		}
		cancel()
		_, err := tx.ExecContext(ctx, `INSERT INTO agents(id,kind_id) VALUES('never-written',2)`)
		return err
	})
	if !errors.Is(err, context.Canceled) || writerCount(t, s, "agents") != 0 {
		t.Fatalf("mid-write cancellation lost atomicity: %v", err)
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || strings.Contains(structured.Impact, "could not be confirmed") {
		t.Fatalf("pre-commit cancellation reported ambiguous commit: %v", err)
	}
	if err := s.RecordEvent(context.Background(), writerEvent()); err != nil {
		t.Fatalf("trail not reusable after pre-commit cancellation: %v", err)
	}
}

func TestAuditWriterTypedBusyCommitFailureIsNeverReplayed(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	attempts := 0
	cause := writerCodeError(5)
	s.writerPolicy = defaultAuditWriterPolicy()
	s.writerPolicy.afterAttempt = func(_ int, _ error) { attempts++ }
	s.writerPolicy.commit = func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return cause
	}
	id, err := s.RecordEventReturningId(context.Background(), writerEvent())
	if !errors.Is(err, cause) || id != 0 || attempts != 1 || writerCount(t, s, "audit_events") != 1 {
		t.Fatalf("blind commit retry: id %d err %v attempts %d", id, err, attempts)
	}
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || !strings.Contains(structured.What, "Couldn't confirm") {
		t.Fatalf("false commit headline: %v", err)
	}
}

func TestAuditWriterReservedBusyClampAndStandingRestore(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	if err := s.withAuditWrite(context.Background(), "clamp inspection", func(ctx context.Context, tx *sql.Tx) error {
		var busy int64
		if err := tx.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy); err != nil {
			return err
		}
		want := (s.busyTier - min(100*time.Millisecond, s.busyTier/5)).Milliseconds()
		if busy != want {
			t.Errorf("reserved clamp %d, want %d", busy, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var busy int64
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != s.busyTier.Milliseconds() {
		t.Fatal("reduced tier escaped reservation")
	}
}

func TestAuditWriterColdDedupReplayPublishesCanonicalAgent(t *testing.T) {
	t.Parallel()
	s, path := writerTrail(t, timeouts.ProductionProfile())
	event := writerEvent()
	event.DedupKey = "cold-replay"
	canonical, err := s.RecordEventReturningId(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := NewSqliteAuditTrail(path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	id, err := peer.RecordEventReturningId(context.Background(), event)
	if err != nil || id != canonical || writerCacheSize(peer) != 1 {
		t.Fatalf("cold replay id %d canonical %d cache %d err %v", id, canonical, writerCacheSize(peer), err)
	}
	if writerCount(t, s, "agents") != 1 || writerCount(t, s, "audit_events") != 1 || writerCount(t, s, "context_edges") != 1 {
		t.Fatal("cold dedup replay duplicated registration/event/context")
	}
}

func TestAuditWriterDirectLookupMissFallsBackToLegacyRole(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	event := writerEvent()
	event.Role = "pasture/automaton/not-registered"
	if err := s.RecordEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := s.db.QueryRow(`SELECT name FROM agents_software`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != legacyRoleAgentNamePrefix+event.Role || writerCount(t, s, "agents") != 1 {
		t.Fatal("direct-lookup fallback changed")
	}
}

func TestAuditWriterBusyRestoreReadbackMismatch(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer func() {
		if err := restoreAuditBusyTier(context.Background(), conn, s.busyTier.Milliseconds()); err != nil {
			t.Error(err)
		}
	}()
	// SQLite's busy handler uses a signed 32-bit millisecond count. This real
	// out-of-range setting reads back as 0, exercising verification failure.
	err = restoreAuditBusyTier(context.Background(), conn, 1<<32)
	if err == nil || !strings.Contains(err.Error(), "read-back") {
		t.Fatalf("unverified busy setting: %v", err)
	}
}

func TestQuerySessionEntriesRefusesNilContext(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	_, err := s.QuerySessionEntries(nil, "nil-context")
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) || structured.Category != pasterrors.CategoryValidation {
		t.Fatalf("nil context result %v", err)
	}
}

func TestAuditWriterCancellationDuringBackoff(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	p := defaultAuditWriterPolicy()
	p.initialDelay = time.Second
	entered := make(chan struct{})
	done := make(chan struct{})
	p.onBackoff = func(time.Duration) { close(entered) }
	var err error
	attempts := 0
	t.Cleanup(func() { cancel(); <-done })
	go func() {
		defer close(done)
		err = retryAuditWrite(ctx, "backoff cancellation", p, func(context.Context) auditAttemptResult {
			attempts++
			return auditAttemptResult{err: writerCodeError(5), retry: true, impact: "No transaction started."}
		})
	}()
	select {
	case <-entered:
	case <-done:
		t.Fatal("did not enter backoff")
	case <-time.After(time.Second):
		t.Fatal("backoff readiness not signalled")
	}
	start := time.Now()
	cancel()
	<-done
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("err %v attempts %d", err, attempts)
	}
	if time.Since(start) > writerSchedulingTolerance {
		t.Fatal("cancelled backoff spent a busy tier")
	}
	if defaultAuditWriterPolicy().initialDelay != 25*time.Millisecond || defaultAuditWriterPolicy().maxDelay != time.Second {
		t.Fatal("production exponential backoff bounds changed")
	}
}
