package audit_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

func newTestSqliteTrail(t *testing.T) (*audit.SqliteAuditTrail, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_audit.db")
	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = trail.Close() })
	return trail, dbPath
}

func TestSqliteAuditTrail_Suite(t *testing.T) {
	trail, _ := newTestSqliteTrail(t)
	runTrailSuite(t, trail)
}

// TestSqliteAuditTrail_Durability verifies that events survive a close/reopen
// cycle — the core "survives kill-restart" acceptance criterion.
func TestSqliteAuditTrail_Durability(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "durable.db")
	ctx := context.Background()

	// Phase 1: write events and close.
	trail1, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail: %v", err)
	}

	ev1 := makeEvent("dur-epoch", protocol.P1_Request, "supervisor", protocol.EventPhaseTransition)
	ev2 := makeEvent("dur-epoch", protocol.P2_Elicit, "worker", protocol.EventVoteRecorded)

	if err := trail1.RecordEvent(ctx, ev1); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if err := trail1.RecordEvent(ctx, ev2); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if err := trail1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Phase 2: reopen and verify events are present.
	trail2, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail (reopen): %v", err)
	}
	defer trail2.Close()

	got, err := trail2.QueryEvents(ctx, "dur-epoch", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events after reopen, got %d", len(got))
	}
	if got[0].Phase != protocol.P1_Request {
		t.Errorf("event[0]: want phase %q, got %q", protocol.P1_Request, got[0].Phase)
	}
	if got[1].Phase != protocol.P2_Elicit {
		t.Errorf("event[1]: want phase %q, got %q", protocol.P2_Elicit, got[1].Phase)
	}
}

// TestSqliteAuditTrail_ParentDirCreation verifies that NewSqliteAuditTrail
// creates intermediate directories when the parent does not exist.
func TestSqliteAuditTrail_ParentDirCreation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "sub", "audit.db")

	trail, err := audit.NewSqliteAuditTrail(dbPath)
	if err != nil {
		t.Fatalf("NewSqliteAuditTrail with missing parent: %v", err)
	}
	defer trail.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database file not created at %q: %v", dbPath, err)
	}
}

// TestSqliteAuditTrail_ConcurrentAccess verifies thread-safety under parallel
// writes. SQLite with WAL mode serialises writers without data loss.
func TestSqliteAuditTrail_ConcurrentAccess(t *testing.T) {
	trail, _ := newTestSqliteTrail(t)
	ctx := context.Background()

	const goroutines = 10
	const eventsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			for j := range eventsPerGoroutine {
				ev := protocol.AuditEvent{
					EpochID:   "concurrent-sqlite",
					Phase:     protocol.P9_Slice,
					Role:      "worker",
					EventType: protocol.EventSliceStarted,
					Payload:   map[string]any{"goroutine": idx, "seq": j},
					Timestamp: time.Now().UTC(),
				}
				if err := trail.RecordEvent(ctx, ev); err != nil {
					t.Errorf("goroutine %d: RecordEvent: %v", idx, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	got, err := trail.QueryEvents(ctx, "concurrent-sqlite", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	want := goroutines * eventsPerGoroutine
	if len(got) != want {
		t.Errorf("want %d events, got %d", want, len(got))
	}
}

// TestSqliteAuditTrail_PreservesChronologicalOrder verifies that rows come back
// in insertion order (ascending id), not arbitrary order.
func TestSqliteAuditTrail_PreservesChronologicalOrder(t *testing.T) {
	trail, _ := newTestSqliteTrail(t)
	ctx := context.Background()

	phases := []protocol.PhaseId{protocol.P1_Request, protocol.P2_Elicit, protocol.P3_Propose}
	for _, ph := range phases {
		ev := makeEvent("order-sqlite", ph, "supervisor", protocol.EventPhaseTransition)
		if err := trail.RecordEvent(ctx, ev); err != nil {
			t.Fatalf("RecordEvent: %v", err)
		}
	}

	got, err := trail.QueryEvents(ctx, "order-sqlite", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	for i, ph := range phases {
		if got[i].Phase != ph {
			t.Errorf("event[%d]: want %q, got %q", i, ph, got[i].Phase)
		}
	}
}
