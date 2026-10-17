package audit

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/timeouts"
)

func cleanupDeadline(ctx context.Context, conn *sql.Conn, tier int64) error {
	<-ctx.Done()
	return restoreAuditBusyTier(ctx, conn, tier)
}

func cleanupTrail(t *testing.T, logger *slog.Logger) *SqliteAuditTrail {
	t.Helper()
	s, err := NewSqliteAuditTrailWithOptions(filepath.Join(t.TempDir(), "audit.db"), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Actual COMMIT, actual expired cleanup context and actual driver retirement.
// Cancellation after COMMIT cannot undo the row or cancel detached diagnostics.
func TestCommittedCleanupDeadlineReturnsSuccessWithWarning(t *testing.T) {
	for _, cancelledCaller := range []bool{false, true} {
		name := "live caller"
		if cancelledCaller {
			name = "caller cancelled after commit"
		}
		t.Run(name, func(t *testing.T) {
			var diagnostics bytes.Buffer
			s := cleanupTrail(t, slog.New(slog.NewJSONHandler(&diagnostics, nil)))
			conn, err := s.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var original any
			if err := conn.Raw(func(raw any) error { original = raw; return nil }); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			committed, attempts := false, 0
			var reserved *sql.Conn
			s.writerPolicy = defaultAuditWriterPolicy()
			s.writerPolicy.commit = func(tx *sql.Tx) error {
				var clamped int64
				if err := tx.QueryRow("PRAGMA busy_timeout").Scan(&clamped); err != nil {
					return err
				}
				if clamped >= s.busyTier.Milliseconds() {
					t.Error("probe did not actually lower the reserved busy tier")
				}
				err := tx.Commit()
				committed = err == nil
				if committed && cancelledCaller {
					cancel()
				}
				return err
			}
			s.writerPolicy.afterAttempt = func(_ int, _ error) { attempts++ }
			s.writerPolicy.restore = func(ctx context.Context, c *sql.Conn, tier int64) error {
				reserved = c
				if !committed || errors.Is(ctx.Err(), context.Canceled) {
					t.Error("cleanup must follow COMMIT with its own non-caller cancellation scope")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 100*time.Millisecond {
					t.Error("cleanup lost its unchanged finite budget")
				}
				return cleanupDeadline(ctx, c, tier)
			}
			event := writerEvent()
			event.Payload = map[string]any{"private": "not-for-cleanup-diagnostics"}
			id, err := s.RecordEventReturningId(caller, event)
			if err != nil || id <= 0 || !committed || attempts != 1 {
				t.Fatalf("id=%d committed=%t attempts=%d err=%v", id, committed, attempts, err)
			}
			if stats := s.db.Stats(); stats.OpenConnections != 0 || stats.InUse != 0 || stats.Idle != 0 {
				t.Fatalf("retired connection remains pooled: %+v", stats)
			}
			if err := reserved.Raw(func(any) error { return nil }); !errors.Is(err, sql.ErrConnDone) {
				t.Fatalf("reserved connection remains usable: %v", err)
			}
			if writerCount(t, s, "audit_events") != 1 || writerCacheSize(s) != 1 {
				t.Fatal("known commit was lost, retried, or failed to publish its committed role")
			}
			var storedID int64
			if err := s.db.QueryRow("SELECT id FROM audit_events").Scan(&storedID); err != nil || storedID != id {
				t.Fatalf("returned id=%d stored=%d err=%v", id, storedID, err)
			}
			var warning map[string]any
			if err := json.Unmarshal(diagnostics.Bytes(), &warning); err != nil {
				t.Fatalf("want exactly one structured warning: %q: %v", diagnostics.String(), err)
			}
			for key, want := range map[string]string{"level": "WARN", "event": "audit_cleanup_retired", "operation": "RecordEvent", "durability": "committed", "cleanup": "deadline_exceeded", "connection": "retired_from_pool"} {
				if warning[key] != want {
					t.Errorf("warning[%s]=%v, want %s", key, warning[key], want)
				}
			}
			action, ok := warning["action"].(string)
			if !ok || !strings.Contains(action, "Do not retry") || strings.Contains(diagnostics.String(), "not-for-cleanup-diagnostics") {
				t.Fatal("warning lost the operator action or exposed payload content")
			}
			replacement, err := s.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := replacement.Raw(func(raw any) error {
				if raw == original {
					t.Error("retired driver returned to pool")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var busy int64
			if err := replacement.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busy); err != nil || busy != timeouts.ProductionProfile().SQLiteBusy().Milliseconds() {
				t.Fatalf("replacement standing tier=%d err=%v", busy, err)
			}
			if err := replacement.Close(); err != nil {
				t.Fatal(err)
			}
			s.writerPolicy = defaultAuditWriterPolicy()
			if err := s.RecordEvent(t.Context(), writerEvent()); err != nil {
				t.Fatal(err)
			}
			if writerCount(t, s, "audit_events") != 2 || bytes.Count(diagnostics.Bytes(), []byte("\n")) != 1 {
				t.Fatal("next write duplicated work or healthy cleanup emitted a warning")
			}
		})
	}
}

type failingCleanupHandler struct{ err error }

func (h failingCleanupHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (h failingCleanupHandler) Handle(context.Context, slog.Record) error { return h.err }
func (h failingCleanupHandler) WithAttrs([]slog.Attr) slog.Handler        { return h }
func (h failingCleanupHandler) WithGroup(string) slog.Handler             { return h }

func TestCleanupDegradedSuccessRefusesUnconfirmedCases(t *testing.T) {
	for _, mode := range []string{"unknown-cleanup", "joined-policy-error", "retirement-unconfirmed", "warning-disabled", "warning-failed", "unknown-commit", "foreign-key-before-commit"} {
		t.Run(mode, func(t *testing.T) {
			var diagnostics bytes.Buffer
			s := cleanupTrail(t, slog.New(slog.NewJSONHandler(&diagnostics, nil)))
			s.writerPolicy = defaultAuditWriterPolicy()
			s.writerPolicy.restore = cleanupDeadline
			cause := errors.New("unconfirmed cleanup control")
			switch mode {
			case "unknown-cleanup":
				s.writerPolicy.restore = func(context.Context, *sql.Conn, int64) error { return cause }
			case "joined-policy-error":
				s.writerPolicy.restore = func(ctx context.Context, conn *sql.Conn, tier int64) error {
					return errors.Join(cleanupDeadline(ctx, conn, tier), cause)
				}
			case "retirement-unconfirmed":
				s.writerPolicy.retire = func(*sql.Conn) error { return cause }
			case "warning-disabled":
				s.logger = slog.New(slog.NewJSONHandler(&diagnostics, &slog.HandlerOptions{Level: slog.LevelError}))
			case "warning-failed":
				s.logger = slog.New(failingCleanupHandler{cause})
			case "unknown-commit":
				s.writerPolicy.commit = func(tx *sql.Tx) error {
					if err := tx.Commit(); err != nil {
						return err
					}
					return cause
				}
			}
			attempts := 0
			s.writerPolicy.afterAttempt = func(_ int, _ error) { attempts++ }
			var id int64
			var err error
			if mode == "foreign-key-before-commit" {
				err = s.withAuditWrite(t.Context(), "foreign key control", func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, "INSERT INTO context_edges(event_id,context_kind,context_id) VALUES(-1,'EpochContext','missing')")
					return err
				})
			} else {
				id, err = s.RecordEventReturningId(t.Context(), writerEvent())
			}
			if err == nil || id != 0 || attempts != 1 || diagnostics.Len() != 0 {
				t.Fatalf("mode=%s id=%d attempts=%d warning=%q err=%v", mode, id, attempts, diagnostics.String(), err)
			}
			switch mode {
			case "unknown-cleanup", "joined-policy-error", "retirement-unconfirmed", "warning-failed", "unknown-commit":
				if !errors.Is(err, cause) {
					t.Fatalf("lost original failure identity: %v", err)
				}
			case "foreign-key-before-commit":
				if !strings.Contains(err.Error(), "FOREIGN KEY") {
					t.Fatalf("lost actual foreign-key refusal: %v", err)
				}
			}
			if stats := s.db.Stats(); stats.OpenConnections != 0 {
				t.Fatalf("unconfirmed adapter left reduced-policy connection pooled: %+v", stats)
			}
			wantRows := 1
			if mode == "foreign-key-before-commit" {
				wantRows = 0
			}
			if got := writerCount(t, s, "audit_events"); got != wantRows {
				t.Fatalf("rows=%d want=%d", got, wantRows)
			}
		})
	}
}

func TestClosedConnIsNotRetirementConfirmation(t *testing.T) {
	s, _ := writerTrail(t, timeouts.ProductionProfile())
	conn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := retireAuditConnection(conn); !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("an already-returned connection is not proof of retirement: %v", err)
	}
}

func TestCleanupLoggerConstructionAndUnifiedOwnerWiring(t *testing.T) {
	if _, err := NewSqliteAuditTrailWithOptions(filepath.Join(t.TempDir(), "must-not-open.db"), WithLogger(nil)); err == nil {
		t.Fatal("nil diagnostics must be refused before database construction")
	}
	var sink bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&sink, nil))
	path := filepath.Join(t.TempDir(), "audit.db")
	first, err := NewSqliteAuditTrailWithOptions(path, WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	if first.logger != logger {
		t.Fatal("normal constructor dropped owning logger")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewSqliteAuditTrailWithOptions(path, WithLogger(logger), WithSkipMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.logger != logger {
		t.Fatal("skip-migration constructor dropped owning logger")
	}

	// The unified store supplies the daemon/CLI diagnostic owner on BOTH
	// normal construction branches and when it reuses an auto-cutover trail.
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "../tasks/open_unified.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var loggers, cutover int
	text := func(node ast.Node) string { var b bytes.Buffer; _ = printer.Fprint(&b, set, node); return b.String() }
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && text(call.Fun) == "audit.NewSqliteAuditTrailContextWithOptions" {
			for _, arg := range call.Args {
				if text(arg) == "audit.WithLogger(slog.New(slog.NewJSONHandler(cfg.diagnostics, nil)))" {
					loggers++
				}
			}
		}
		literal, ok := node.(*ast.CompositeLit)
		if ok && text(literal.Type) == "idmigrate.CatchUpOptions" {
			for _, elt := range literal.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if ok && text(kv.Key) == "Logger" && text(kv.Value) == "slog.New(slog.NewJSONHandler(cfg.diagnostics, nil))" {
					cutover++
				}
			}
		}
		return true
	})
	if loggers != 2 || cutover != 1 {
		t.Fatalf("owning diagnostic routes normal=%d cutover=%d", loggers, cutover)
	}
	file, err = parser.ParseFile(set, "../idmigrate/pipeline.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	forwarded := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && text(call) == "audit.WithLogger(opts.Logger)" {
			forwarded++
		}
		return true
	})
	if forwarded != 1 {
		t.Fatal("cutover dropped the logger before returning its reusable trail")
	}
}
