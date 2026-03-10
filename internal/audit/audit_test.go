// Package audit_test provides shared test helpers and interface compliance
// checks for the audit.Trail interface.
package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Interface Compliance ─────────────────────────────────────────────────────

// TestTrailInterface verifies that both implementations satisfy the Trail interface
// at compile time. If either assignment fails to compile, the interface is broken.
func TestTrailInterface(t *testing.T) {
	t.Helper()
	var _ audit.Trail = &audit.InMemoryAuditTrail{}
	var _ audit.Trail = (*audit.SqliteAuditTrail)(nil)
}

// ─── Shared Helpers ───────────────────────────────────────────────────────────

// makeEvent constructs a minimal AuditEvent for use in tests.
func makeEvent(epochID string, phase protocol.PhaseId, role string, eventType protocol.EventType) protocol.AuditEvent {
	return protocol.AuditEvent{
		EpochID:   epochID,
		Phase:     phase,
		Role:      role,
		EventType: eventType,
		Payload:   map[string]any{"test": true},
		Timestamp: time.Now().UTC(),
	}
}

// runTrailSuite runs a standard suite of Trail behaviour tests against any
// Trail implementation. This allows memory and SQLite to share the same
// correctness assertions.
func runTrailSuite(t *testing.T, trail audit.Trail) {
	t.Helper()
	ctx := context.Background()

	ep1 := "epoch-aaa"
	ep2 := "epoch-bbb"
	roleA := "supervisor"
	roleB := "worker"

	ev1 := makeEvent(ep1, protocol.P1_Request, roleA, protocol.EventPhaseTransition)
	ev2 := makeEvent(ep1, protocol.P2_Elicit, roleB, protocol.EventVoteRecorded)
	ev3 := makeEvent(ep2, protocol.P1_Request, roleA, protocol.EventPhaseTransition)

	// Record three events.
	if err := trail.RecordEvent(ctx, ev1); err != nil {
		t.Fatalf("RecordEvent(ev1): unexpected error: %v", err)
	}
	if err := trail.RecordEvent(ctx, ev2); err != nil {
		t.Fatalf("RecordEvent(ev2): unexpected error: %v", err)
	}
	if err := trail.RecordEvent(ctx, ev3); err != nil {
		t.Fatalf("RecordEvent(ev3): unexpected error: %v", err)
	}

	// Query all events for ep1 — expect ev1, ev2.
	t.Run("QueryByEpochID", func(t *testing.T) {
		got, err := trail.QueryEvents(ctx, ep1, nil, nil)
		if err != nil {
			t.Fatalf("QueryEvents(%q): %v", ep1, err)
		}
		if len(got) != 2 {
			t.Fatalf("QueryEvents(%q): want 2 events, got %d", ep1, len(got))
		}
	})

	// Query ep1 filtered by phase p1 — expect ev1 only.
	t.Run("QueryByEpochAndPhase", func(t *testing.T) {
		ph := protocol.P1_Request
		got, err := trail.QueryEvents(ctx, ep1, &ph, nil)
		if err != nil {
			t.Fatalf("QueryEvents: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1 event, got %d", len(got))
		}
		if got[0].EventType != protocol.EventPhaseTransition {
			t.Errorf("wrong event type: got %q", got[0].EventType)
		}
	})

	// Query ep1 filtered by role roleB — expect ev2 only.
	t.Run("QueryByEpochAndRole", func(t *testing.T) {
		got, err := trail.QueryEvents(ctx, ep1, nil, &roleB)
		if err != nil {
			t.Fatalf("QueryEvents: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1 event, got %d", len(got))
		}
		if got[0].Role != roleB {
			t.Errorf("wrong role: got %q", got[0].Role)
		}
	})

	// Query ep1 filtered by both phase p2 and role roleB — expect ev2 only.
	t.Run("QueryByEpochPhaseAndRole", func(t *testing.T) {
		ph := protocol.P2_Elicit
		got, err := trail.QueryEvents(ctx, ep1, &ph, &roleB)
		if err != nil {
			t.Fatalf("QueryEvents: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1, got %d", len(got))
		}
	})

	// Query ep2 — expect ev3 only.
	t.Run("QuerySeparateEpoch", func(t *testing.T) {
		got, err := trail.QueryEvents(ctx, ep2, nil, nil)
		if err != nil {
			t.Fatalf("QueryEvents: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1, got %d", len(got))
		}
	})

	// Query non-existent epoch — expect empty, no error.
	t.Run("QueryMissingEpoch", func(t *testing.T) {
		got, err := trail.QueryEvents(ctx, "epoch-missing", nil, nil)
		if err != nil {
			t.Fatalf("QueryEvents: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("want 0, got %d", len(got))
		}
	})
}
