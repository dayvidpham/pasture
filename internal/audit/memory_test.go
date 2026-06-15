package audit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

func TestInMemoryAuditTrail_Suite(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	runTrailSuite(t, trail)
}

func TestInMemoryAuditTrail_SessionEntrySuite(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	runSessionEntrySuite(t, trail)
}

func TestInMemoryAuditTrail_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	ctx := context.Background()

	const goroutines = 20
	const eventsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			for j := range eventsPerGoroutine {
				ev := protocol.AuditEvent{
					EpochId:   "concurrent-epoch",
					Phase:     protocol.PhaseWorkerSlices,
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

	// All events should be recorded.
	got, err := trail.QueryEvents(ctx, "concurrent-epoch", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	want := goroutines * eventsPerGoroutine
	if len(got) != want {
		t.Errorf("want %d events, got %d", want, len(got))
	}
}

// TestInMemoryAuditTrail_RecordEventReturningId_Suite runs the shared
// RecordEventReturningId contract suite against the in-memory trail. The
// in-memory trail uses a synthetic monotonic counter; the suite asserts
// (positive id, distinct ids per call, queryable round-trip).
func TestInMemoryAuditTrail_RecordEventReturningId_Suite(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	runRecordEventReturningIdSuite(t, trail)
}

// TestInMemoryAuditTrail_RecordEventReturningId_ConcurrentUnique verifies
// that under N concurrent goroutines all calling RecordEventReturningId
// against the same in-memory trail, EVERY returned id is distinct. The
// in-memory trail's counter is incremented under m.mu so this is the
// straightforward "no two callers see the same value" property.
func TestInMemoryAuditTrail_RecordEventReturningId_ConcurrentUnique(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	ctx := context.Background()

	const N = 64
	var wg sync.WaitGroup
	idCh := make(chan int64, N)
	errCh := make(chan error, N)

	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ev := protocol.AuditEvent{
				EpochId:   "concurrent-unique-mem",
				Phase:     protocol.PhaseWorkerSlices,
				Role:      "worker",
				EventType: protocol.EventSliceStarted,
				Payload:   map[string]any{"goroutine": idx},
				Timestamp: time.Now().UTC(),
			}
			id, err := trail.RecordEventReturningId(ctx, ev)
			if err != nil {
				errCh <- err
				return
			}
			idCh <- id
		}(i)
	}
	wg.Wait()
	close(idCh)
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent in-memory RecordEventReturningId: %v", err)
	}

	seen := make(map[int64]int)
	for id := range idCh {
		if id <= 0 {
			t.Errorf("in-memory RecordEventReturningId returned non-positive id %d", id)
			continue
		}
		seen[id]++
	}
	if len(seen) != N {
		t.Errorf("expected %d distinct ids from %d goroutines, got %d", N, N, len(seen))
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("in-memory id %d returned to %d goroutines (must be unique)", id, count)
		}
	}
}

func TestInMemoryAuditTrail_PreservesInsertionOrder(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	ctx := context.Background()

	phases := []protocol.PhaseId{protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose}
	for _, ph := range phases {
		ev := makeEvent("order-epoch", ph, "supervisor", protocol.EventPhaseTransition)
		if err := trail.RecordEvent(ctx, ev); err != nil {
			t.Fatalf("RecordEvent: %v", err)
		}
	}

	got, err := trail.QueryEvents(ctx, "order-epoch", nil, nil)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
	for i, ph := range phases {
		if got[i].Phase != ph {
			t.Errorf("event[%d]: want phase %q, got %q", i, ph, got[i].Phase)
		}
	}
}
