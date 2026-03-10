package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// TestRecordAuditEvent_UninitializedTrail verifies that the activity returns an
// actionable error when InitTrail has not been called.
func TestRecordAuditEvent_UninitializedTrail(t *testing.T) {
	// Reset to nil explicitly.
	audit.InitTrail(nil)

	ev := makeEvent("ep-x", protocol.PhaseRequest, "supervisor", protocol.EventPhaseTransition)
	err := audit.RecordAuditEvent(context.Background(), ev)
	if err == nil {
		t.Fatal("expected error when trail not initialized, got nil")
	}
	// Error should describe the problem clearly.
	if len(err.Error()) < 20 {
		t.Errorf("error message too short to be actionable: %q", err.Error())
	}
}

// TestQueryAuditEvents_UninitializedTrail verifies that query also fails clearly.
func TestQueryAuditEvents_UninitializedTrail(t *testing.T) {
	audit.InitTrail(nil)

	_, err := audit.QueryAuditEvents(context.Background(), "ep-x", nil, nil)
	if err == nil {
		t.Fatal("expected error when trail not initialized, got nil")
	}
}

// TestActivityRoundtrip_WithInMemory verifies the full activity roundtrip using
// the InMemoryAuditTrail. This is the integration path: InitTrail → activity call
// → QueryEvents.
func TestActivityRoundtrip_WithInMemory(t *testing.T) {
	trail := audit.NewInMemoryAuditTrail()
	audit.InitTrail(trail)
	t.Cleanup(func() { audit.InitTrail(nil) })

	ctx := context.Background()
	ev := makeEvent("act-epoch", protocol.PhaseWorkerSlices, "worker", protocol.EventSliceStarted)

	if err := audit.RecordAuditEvent(ctx, ev); err != nil {
		t.Fatalf("RecordAuditEvent: %v", err)
	}

	got, err := audit.QueryAuditEvents(ctx, "act-epoch", nil, nil)
	if err != nil {
		t.Fatalf("QueryAuditEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].EpochID != "act-epoch" {
		t.Errorf("wrong epoch: got %q", got[0].EpochID)
	}
}

// TestQueryAuditEvents_PhaseFilter verifies that phase filtering works through
// the activity wrapper.
func TestQueryAuditEvents_PhaseFilter(t *testing.T) {
	trail := audit.NewInMemoryAuditTrail()
	audit.InitTrail(trail)
	t.Cleanup(func() { audit.InitTrail(nil) })

	ctx := context.Background()

	ev1 := makeEvent("filter-epoch", protocol.PhaseRequest, "supervisor", protocol.EventPhaseTransition)
	ev2 := makeEvent("filter-epoch", protocol.PhaseElicit, "supervisor", protocol.EventPhaseAdvance)

	_ = audit.RecordAuditEvent(ctx, ev1)
	_ = audit.RecordAuditEvent(ctx, ev2)

	ph := protocol.PhaseElicit
	got, err := audit.QueryAuditEvents(ctx, "filter-epoch", &ph, nil)
	if err != nil {
		t.Fatalf("QueryAuditEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].Phase != protocol.PhaseElicit {
		t.Errorf("wrong phase: %q", got[0].Phase)
	}
}

// errorTrail is a stub that always returns errors, used to verify activity
// error propagation.
type errorTrail struct{}

func (e *errorTrail) RecordEvent(_ context.Context, _ protocol.AuditEvent) error {
	return errors.New("stub: record failed")
}

func (e *errorTrail) QueryEvents(_ context.Context, _ string, _ *protocol.PhaseId, _ *string) ([]protocol.AuditEvent, error) {
	return nil, errors.New("stub: query failed")
}

func TestActivityRoundtrip_PropagatesTrailError(t *testing.T) {
	audit.InitTrail(&errorTrail{})
	t.Cleanup(func() { audit.InitTrail(nil) })

	ctx := context.Background()
	ev := makeEvent("err-epoch", protocol.PhaseRequest, "supervisor", protocol.EventPhaseTransition)

	if err := audit.RecordAuditEvent(ctx, ev); err == nil {
		t.Fatal("expected error from errorTrail, got nil")
	}

	if _, err := audit.QueryAuditEvents(ctx, "err-epoch", nil, nil); err == nil {
		t.Fatal("expected error from errorTrail, got nil")
	}
}
