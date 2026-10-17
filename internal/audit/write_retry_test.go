package audit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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

// Tests with wall-clock upper bounds run before this package releases its
// parallel database fixtures. They retain their real holders, clocks, and
// original bounds. Transaction/content checks remain parallel and concurrency
// probes retain simultaneous writers; this is not package-wide serialization.

func TestAuditWriterWallClockProbesExcludeParallelFixtures(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "write_retry_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name.Name {
		case "TestAuditWriterShortColdRoleBudget", "TestAuditWriterLongHolderRetries",
			"TestAuditWriterLongHolderOtherModes", "TestAuditWriterIndependentTrailsShareOneRole",
			"TestAuditWriterPoolWaitAndSessionReadCancellation", "TestAuditWriterReplacementConnectionUsesThisBudget",
			"TestAuditWriterBudgetIncludesAllAttempts", "TestAuditWriterCancellationDuringBackoff",
			"TestAuditWriterConnectorSetupHonorsCurrentAcquisitionDeadline":
		default:
			continue
		}
		checked++
		// Include nested subtests: a serial parent alone does not isolate a
		// latency probe if its own cases release each other with t.Parallel.
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Parallel" {
				t.Errorf("%s releases a wall-clock/native-budget probe alongside competing fixtures", fn.Name.Name)
			}
			return true
		})
	}
	if checked != 9 {
		t.Fatalf("checked %d wall-clock/native-budget probes, want all nine", checked)
	}
}

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
	const cases writerFixtureName = "audit_writer_profiles"
	var fixtures []struct {
		Name       string `yaml:"name"`
		Production bool   `yaml:"production"`
	}
	loadWriterFixture(t, cases, &fixtures)
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
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
		t.Logf("real exhausted audit attempt: elapsed=%s ceiling=%s", failedAt.Sub(start), ceiling)
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
	const cases writerFixtureName = "audit_writer_modes"
	var fixtures []struct {
		Mode string `yaml:"mode"`
	}
	loadWriterFixture(t, cases, &fixtures)
	for _, f := range fixtures {
		t.Run(f.Mode, func(t *testing.T) {
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
			elapsed := time.Since(start)
			if elapsed >= 3*time.Second {
				t.Fatalf("write exceeded 3s operation deadline: elapsed=%s", elapsed)
			}
			t.Logf("real %s audit write: elapsed=%s, holder released after 750ms", f.Mode, elapsed)
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
	// The two writer goroutines below still overlap on the same store. Keep
	// unrelated package fixtures out of their explicit 3s caller budget.
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
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	check := func(name string, operation func(context.Context) error) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := operation(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("pool cancellation cause: %v", err)
		}
		elapsed := time.Since(start)
		if elapsed > 25*time.Millisecond+writerSchedulingTolerance {
			t.Fatalf("%s pool wait exceeded 25ms caller budget plus %s scheduler allowance: elapsed=%s", name, writerSchedulingTolerance, elapsed)
		}
		t.Logf("real %s pool wait: elapsed=%s, reserved connection retained", name, elapsed)
	}
	check("event", func(ctx context.Context) error { return s.RecordEvent(ctx, writerEvent()) })
	check("batch", func(ctx context.Context) error {
		return s.RecordSessionEntries(ctx, []protocol.SessionEntry{{SessionId: "pool"}})
	})
	check("read", func(ctx context.Context) error { _, err := s.QuerySessionEntries(ctx, "pool"); return err })
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
	s, path := writerTrail(t, timeouts.ProductionProfile())
	// The constructor is done; force the writer's Conn to perform real first
	// use of a replacement connection while the independent holder stays locked.
	// This store is already WAL: contention is at the writer's BEGIN, not
	// necessarily at connector setup. The separate DELETE-journal probe below
	// isolates the connector's WAL conversion and caller-deadline clamp.
	s.db.SetMaxIdleConns(0)
	holder := writerHolder(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.RecordEvent(ctx, writerEvent())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed > 25*time.Millisecond+writerSchedulingTolerance {
		t.Fatalf("replacement connection exceeded 25ms acquisition budget plus %s scheduler allowance: elapsed=%s", writerSchedulingTolerance, elapsed)
	}
	t.Logf("real replacement connection: elapsed=%s, holder retained until return", elapsed)
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxIdleConns(1)
	// MaxIdleConns(0) closed the failed connection. This subsequent write and
	// read-back prove a NEW retained connection's configuration, not reuse of
	// the failed connection.
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

func TestAuditWriterConnectorSetupHonorsCurrentAcquisitionDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connector-setup.db")
	holder, err := dbconn.OpenDefaultDB(path) // no WAL pragmas; retain DELETE mode.
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec("CREATE TABLE held (v INTEGER); INSERT INTO held VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	var journal string
	if err := holder.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
		t.Fatalf("setup journal=%q error=%v, want DELETE before conversion", journal, err)
	}
	if _, err := holder.Exec("BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = holder.Exec("ROLLBACK") }()
	var count int
	if err := holder.QueryRow("SELECT count(*) FROM held").Scan(&count); err != nil || count != 1 {
		t.Fatalf("establish held read transaction: count=%d error=%v", count, err)
	}
	// The held DELETE read transaction prevents WAL conversion. Unlike the
	// already-WAL writer probe, no audit BEGIN or writer clamp is involved:
	// this is the actual shared connector's first-use setup SQL and retry.
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	db, err := dbconn.OpenSharedDBContextWithProfile(ctx, path, timeouts.ProductionProfile())
	elapsed := time.Since(start)
	if db != nil {
		_ = db.Close()
		t.Fatal("connector published a pool while DELETE-to-WAL conversion was blocked")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended connector setup error=%v, want caller DeadlineExceeded", err)
	}
	if elapsed > 25*time.Millisecond+writerSchedulingTolerance {
		t.Fatalf("contended connector setup exceeded 25ms plus %s scheduler allowance: elapsed=%s", writerSchedulingTolerance, elapsed)
	}
	t.Logf("real DELETE-to-WAL connector setup: elapsed=%s, read transaction retained", elapsed)
	if err := holder.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
		t.Fatalf("blocked connector changed journal=%q error=%v", journal, err)
	}
	if _, err := holder.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	db, err = dbconn.OpenSharedDBContextWithProfile(t.Context(), path, timeouts.ProductionProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var busy int64
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 500 {
		t.Fatalf("new configured connector busy timeout=%d error=%v, want standing 500ms", busy, err)
	}
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("new configured connector journal=%q error=%v, want WAL after holder release", journal, err)
	}
}

type auditAcquisitionConnector struct {
	base    driver.Driver
	connect func(context.Context) (driver.Conn, error)
}

func (c auditAcquisitionConnector) Driver() driver.Driver { return c.base }
func (c auditAcquisitionConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.connect(ctx)
}

// The real replacement probe above exercises SQLite setup and lock contention.
// This separate dependency probe pins the context actually passed by the writer
// to database/sql acquisition, and delivers cancellation at Connect itself. It
// does not infer context propagation from how quickly a timer was scheduled.
func TestAuditWriterAcquisitionReceivesCurrentOperationBudget(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	realDB := s.db
	type callerKey struct{}
	marker := &struct{ value int }{value: 1}
	caller, cancel := context.WithCancel(context.WithValue(t.Context(), callerKey{}, marker))
	defer cancel()
	policy := defaultAuditWriterPolicy()
	s.writerPolicy = policy
	var earliest time.Time
	calls := 0
	probeDB := sql.OpenDB(auditAcquisitionConnector{base: realDB.Driver(), connect: func(ctx context.Context) (driver.Conn, error) {
		calls++
		if ctx.Value(callerKey{}) != marker {
			t.Error("connection acquisition lost the current caller's context value")
		}
		deadline, ok := ctx.Deadline()
		latest := time.Now().Add(policy.ceiling)
		if !ok || deadline.Before(earliest) || deadline.After(latest) {
			t.Errorf("connection acquisition deadline=%v present=%v, want the operation ceiling between %v and %v", deadline, ok, earliest, latest)
		}
		cancel() // synchronously cancel WHILE acquiring, before any SQL can run.
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Errorf("connection acquisition ignored synchronous caller cancellation: %v", ctx.Err())
			return nil, errors.New("connection acquisition context did not receive caller cancellation")
		}
		return nil, ctx.Err()
	}})
	defer probeDB.Close()
	s.db = probeDB
	defer func() { s.db = realDB }() // writerTrail cleanup still owns the real pool.
	opCalled := false
	earliest = time.Now().Add(policy.ceiling)
	err := s.withAuditWrite(caller, "acquisition context inspection", func(context.Context, *sql.Tx) error {
		opCalled = true
		return errors.New("SQL must not run after cancelled connection acquisition")
	})
	if !errors.Is(err, context.Canceled) || calls != 1 || opCalled {
		t.Fatalf("acquisitions=%d SQL-called=%v error=%v, want one cancelled acquisition and no SQL", calls, opCalled, err)
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
	// Keep the real wall-clock probe, but do not charge this package's parallel
	// migration/SQLite load to its 40ms operation budget. The independent retry
	// test below checks deadline identity across multiple attempts, without
	// inferring a reset merely from elapsed scheduler time.
	s, path := writerTrail(t, timeouts.ProductionProfile())
	holder := writerHolder(t, path)
	defer holder.Rollback()
	s.writerPolicy = auditWriterPolicy{ceiling: 40 * time.Millisecond, initialDelay: time.Millisecond, maxDelay: time.Millisecond}
	attempts := 0
	s.writerPolicy.afterAttempt = func(_ int, _ error) { attempts++ }
	start := time.Now()
	err := s.RecordEvent(context.Background(), writerEvent())
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) || attempts == 0 {
		t.Fatalf("attempts %d err %v", attempts, err)
	}
	if elapsed > 40*time.Millisecond+writerSchedulingTolerance {
		t.Fatalf("whole write took %s, above the 40ms total budget plus %s scheduler allowance (%d attempts)", elapsed, writerSchedulingTolerance, attempts)
	}
	t.Logf("real locked audit writer: total budget=40ms elapsed=%s attempts=%d, holder retained until return", elapsed, attempts)
	if defaultAuditWriterPolicy().ceiling != 30*time.Second || busyRetryCeiling != 30*time.Second {
		t.Fatal("global or writer ceilings changed")
	}
}

func TestAuditWriterRetriesShareOneOperationDeadline(t *testing.T) {
	t.Parallel()
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	s.writerPolicy = defaultAuditWriterPolicy()
	// Retries are driven by returned typed SQL failures, not by waiting for a
	// timer or releasing a competing writer at just the right instant. Keep
	// the production whole-operation ceiling; no elapsed assertion needs it
	// to expire. The third attempt cancels the caller synchronously instead.
	s.writerPolicy.initialDelay = time.Nanosecond
	s.writerPolicy.maxDelay = time.Nanosecond
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var deadline time.Time
	attempts := 0
	err := s.withAuditWrite(ctx, "retry deadline inspection", func(ctx context.Context, tx *sql.Tx) error {
		attempts++
		got, ok := ctx.Deadline()
		if !ok {
			return errors.New("audit write attempt has no whole-operation deadline")
		}
		if attempts == 1 {
			deadline = got
		} else if !got.Equal(deadline) {
			t.Errorf("attempt %d reset the operation deadline from %s to %s", attempts, deadline, got)
		}
		// Each failed attempt must really roll back before the next can insert
		// the same primary key. The retry engine, transaction, and cleanup are
		// production code; only the transient SQL failure is injected.
		if _, err := tx.ExecContext(ctx, `INSERT INTO agents(id,kind_id) VALUES('budget-attempt',2)`); err != nil {
			return err
		}
		if attempts < 3 {
			return writerCodeError(5)
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || attempts != 3 {
		t.Fatalf("attempts=%d error=%v, want three attempts followed by caller cancellation", attempts, err)
	}
	if writerCount(t, s, "agents") != 0 {
		t.Fatal("an uncommitted retry published an agent row")
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
	elapsed := time.Since(start)
	if elapsed > writerSchedulingTolerance {
		t.Fatalf("cancelled backoff exceeded %s scheduler allowance: elapsed=%s", writerSchedulingTolerance, elapsed)
	}
	t.Logf("real backoff cancellation: elapsed=%s", elapsed)
	if defaultAuditWriterPolicy().initialDelay != 25*time.Millisecond || defaultAuditWriterPolicy().maxDelay != time.Second {
		t.Fatal("production exponential backoff bounds changed")
	}
}
