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

// runRecordEventReturningIdSuite exercises the RecordEventReturningId contract
// against any Trail implementation. The contract:
//
//  1. A successful RecordEventReturningId returns (positiveId, nil).
//  2. Two sequential calls return strictly-increasing ids (no collisions).
//  3. The recorded events round-trip through QueryEvents in insertion order.
//
// The concurrent-uniqueness assertion lives in implementation-specific tests
// (sqlite_test.go's TestSqliteAuditTrail_RecordEventReturningId_ConcurrentUnique
// and memory_test.go's TestInMemoryAuditTrail_RecordEventReturningId_ConcurrentUnique)
// because the implementations differ in the contention model (SQLite has a real
// transaction commit; in-memory has a mutex-guarded counter).
func runRecordEventReturningIdSuite(t *testing.T, trail audit.Trail) {
	t.Helper()
	ctx := context.Background()

	const epoch = "id-suite-epoch"

	t.Run("ReturnsPositiveId", func(t *testing.T) {
		ev := makeEvent(epoch, protocol.PhaseRequest, "supervisor", protocol.EventPhaseTransition)
		id, err := trail.RecordEventReturningId(ctx, ev)
		if err != nil {
			t.Fatalf("RecordEventReturningId: unexpected error: %v", err)
		}
		if id <= 0 {
			t.Errorf("RecordEventReturningId returned non-positive id %d, want > 0", id)
		}
	})

	t.Run("SequentialCallsReturnDistinctIDs", func(t *testing.T) {
		ev1 := makeEvent(epoch, protocol.PhaseElicit, "supervisor", protocol.EventPhaseTransition)
		ev2 := makeEvent(epoch, protocol.PhasePropose, "supervisor", protocol.EventPhaseTransition)
		id1, err := trail.RecordEventReturningId(ctx, ev1)
		if err != nil {
			t.Fatalf("RecordEventReturningId(ev1): %v", err)
		}
		id2, err := trail.RecordEventReturningId(ctx, ev2)
		if err != nil {
			t.Fatalf("RecordEventReturningId(ev2): %v", err)
		}
		if id1 == id2 {
			t.Errorf("RecordEventReturningId returned duplicate ids: id1=%d id2=%d (must be distinct)", id1, id2)
		}
		if id2 <= id1 {
			t.Errorf("RecordEventReturningId ids not monotonically increasing: id1=%d id2=%d", id1, id2)
		}
	})

	t.Run("RecordedEventsAreQueryable", func(t *testing.T) {
		// Both events from the previous subtest are present, plus the one
		// from ReturnsPositiveId. Verify the count by querying the epoch.
		got, err := trail.QueryEvents(ctx, epoch, nil, nil)
		if err != nil {
			t.Fatalf("QueryEvents(%q): %v", epoch, err)
		}
		if len(got) != 3 {
			t.Errorf("want 3 events recorded via RecordEventReturningId, got %d", len(got))
		}
	})
}

// ─── Session Entry Helpers ────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func int64Ptr(i int64) *int64 { return &i }

// makeSessionEntry constructs a minimal SessionEntry for use in tests.
func makeSessionEntry(sessionId string, idx int, role string) protocol.SessionEntry {
	return protocol.SessionEntry{
		SessionId:      sessionId,
		EntryIndex:     idx,
		Provider:       "anthropic",
		EntryType:      "message",
		Role:           role,
		TimestampMs:    int64Ptr(time.Now().UnixMilli()),
		ContentPreview: strPtr("test content"),
		TokensIn:       intPtr(10),
		TokensOut:      intPtr(20),
	}
}

// runSessionEntrySuite runs a standard suite of session entry behaviour tests
// against any Trail implementation.
func runSessionEntrySuite(t *testing.T, trail audit.Trail) {
	t.Helper()
	ctx := context.Background()

	sessionA := "session-aaa"
	sessionB := "session-bbb"

	// ── Record multiple entries for session A ──────────────────────────────
	t.Run("RecordMultipleEntries", func(t *testing.T) {
		entries := []protocol.SessionEntry{
			makeSessionEntry(sessionA, 0, "user"),
			makeSessionEntry(sessionA, 1, "assistant"),
			makeSessionEntry(sessionA, 2, "user"),
		}
		if err := trail.RecordSessionEntries(ctx, entries); err != nil {
			t.Fatalf("RecordSessionEntries: unexpected error: %v", err)
		}
	})

	// ── Query by sessionId returns correct entries ─────────────────────────
	t.Run("QueryBySessionId", func(t *testing.T) {
		got, err := trail.QuerySessionEntries(ctx, sessionA)
		if err != nil {
			t.Fatalf("QuerySessionEntries(%q): %v", sessionA, err)
		}
		if len(got) != 3 {
			t.Fatalf("want 3 entries, got %d", len(got))
		}
		for i, e := range got {
			if e.SessionId != sessionA {
				t.Errorf("entry[%d]: want sessionId %q, got %q", i, sessionA, e.SessionId)
			}
		}
	})

	// ── Query preserves entry order ────────────────────────────────────────
	t.Run("QueryPreservesOrder", func(t *testing.T) {
		got, err := trail.QuerySessionEntries(ctx, sessionA)
		if err != nil {
			t.Fatalf("QuerySessionEntries: %v", err)
		}
		if len(got) < 3 {
			t.Fatalf("want at least 3 entries, got %d", len(got))
		}
		for i := range 3 {
			if got[i].EntryIndex != i {
				t.Errorf("entry[%d]: want EntryIndex %d, got %d", i, i, got[i].EntryIndex)
			}
		}
	})

	// ── Empty query returns empty slice (not nil) ──────────────────────────
	t.Run("QueryMissingSessionReturnsEmpty", func(t *testing.T) {
		got, err := trail.QuerySessionEntries(ctx, "session-does-not-exist")
		if err != nil {
			t.Fatalf("QuerySessionEntries(missing): unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("want empty slice, got nil")
		}
		if len(got) != 0 {
			t.Fatalf("want 0 entries, got %d", len(got))
		}
	})

	// ── Multiple sessions do not cross-contaminate ─────────────────────────
	t.Run("MultipleSessionsNoContamination", func(t *testing.T) {
		entriesB := []protocol.SessionEntry{
			makeSessionEntry(sessionB, 0, "user"),
			makeSessionEntry(sessionB, 1, "assistant"),
		}
		if err := trail.RecordSessionEntries(ctx, entriesB); err != nil {
			t.Fatalf("RecordSessionEntries(sessionB): %v", err)
		}

		gotA, err := trail.QuerySessionEntries(ctx, sessionA)
		if err != nil {
			t.Fatalf("QuerySessionEntries(sessionA): %v", err)
		}
		gotB, err := trail.QuerySessionEntries(ctx, sessionB)
		if err != nil {
			t.Fatalf("QuerySessionEntries(sessionB): %v", err)
		}

		if len(gotA) != 3 {
			t.Errorf("sessionA: want 3 entries, got %d", len(gotA))
		}
		if len(gotB) != 2 {
			t.Errorf("sessionB: want 2 entries, got %d", len(gotB))
		}
		for _, e := range gotA {
			if e.SessionId != sessionA {
				t.Errorf("sessionA result contains entry with sessionId=%q", e.SessionId)
			}
		}
		for _, e := range gotB {
			if e.SessionId != sessionB {
				t.Errorf("sessionB result contains entry with sessionId=%q", e.SessionId)
			}
		}
	})

	// ── Recording empty batch is a no-op ──────────────────────────────────
	t.Run("RecordEmptyBatchNoOp", func(t *testing.T) {
		if err := trail.RecordSessionEntries(ctx, nil); err != nil {
			t.Fatalf("RecordSessionEntries(nil): unexpected error: %v", err)
		}
		if err := trail.RecordSessionEntries(ctx, []protocol.SessionEntry{}); err != nil {
			t.Fatalf("RecordSessionEntries(empty): unexpected error: %v", err)
		}
	})
}

// ─── Shared Helpers ───────────────────────────────────────────────────────────

// makeEvent constructs a minimal AuditEvent for use in tests.
func makeEvent(epochId string, phase protocol.PhaseId, role string, eventType protocol.EventType) protocol.AuditEvent {
	return protocol.AuditEvent{
		EpochId:   epochId,
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

	ev1 := makeEvent(ep1, protocol.PhaseRequest, roleA, protocol.EventPhaseTransition)
	ev2 := makeEvent(ep1, protocol.PhaseElicit, roleB, protocol.EventVoteRecorded)
	ev3 := makeEvent(ep2, protocol.PhaseRequest, roleA, protocol.EventPhaseTransition)

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
	t.Run("QueryByEpochId", func(t *testing.T) {
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
		ph := protocol.PhaseRequest
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
		ph := protocol.PhaseElicit
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
