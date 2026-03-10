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
	trail := audit.NewInMemoryAuditTrail()
	runTrailSuite(t, trail)
}

func TestInMemoryAuditTrail_ConcurrentAccess(t *testing.T) {
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
					EpochID:   "concurrent-epoch",
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

func TestInMemoryAuditTrail_PreservesInsertionOrder(t *testing.T) {
	trail := audit.NewInMemoryAuditTrail()
	ctx := context.Background()

	phases := []protocol.PhaseId{protocol.P1_Request, protocol.P2_Elicit, protocol.P3_Propose}
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
