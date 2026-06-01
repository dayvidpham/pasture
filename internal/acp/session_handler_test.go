package acp_test

import (
	"context"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/acp"
	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/hooks"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func makeUpdate(sessionId, role string, stopReason acp.StopReason) acp.SessionUpdate {
	return acp.SessionUpdate{
		SessionId:  sessionId,
		Role:       role,
		StopReason: stopReason,
		Timestamp:  time.Now().UnixMilli(),
		Content: []acp.ContentBlock{
			{Type: "text", Content: "hello from " + role},
		},
	}
}

// captureHandler records which HookEvents it received, for assertion.
type captureHandler struct {
	received []hooks.HookPayload
}

func (c *captureHandler) Handle(_ context.Context, payload hooks.HookPayload) error {
	c.received = append(c.received, payload)
	return nil
}

func (c *captureHandler) Events() []hooks.HookEvent {
	return []hooks.HookEvent{hooks.HookSessionStarted, hooks.HookSessionEnded}
}

func newManager(cap *captureHandler) *hooks.Manager {
	mgr := hooks.NewManager()
	mgr.Register(cap)
	return mgr
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestHandleUpdateAccumulatesPerSession verifies that updates for different
// sessions are stored independently and both trigger HookSessionStarted.
func TestHandleUpdateAccumulatesPerSession(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	cap := &captureHandler{}
	mgr := newManager(cap)
	h := acp.NewIndexingSessionHandler(indexer, trail, mgr, "epoch-1")
	ctx := context.Background()

	u1 := makeUpdate("session-A", "user", "")
	u2 := makeUpdate("session-B", "assistant", "")
	u3 := makeUpdate("session-A", "assistant", "")

	if err := h.HandleUpdate(ctx, u1); err != nil {
		t.Fatalf("HandleUpdate session-A/1: %v", err)
	}
	if err := h.HandleUpdate(ctx, u2); err != nil {
		t.Fatalf("HandleUpdate session-B/1: %v", err)
	}
	if err := h.HandleUpdate(ctx, u3); err != nil {
		t.Fatalf("HandleUpdate session-A/2: %v", err)
	}

	// HookSessionStarted should have fired twice (once per session).
	started := 0
	for _, p := range cap.received {
		if p.Event == hooks.HookSessionStarted {
			started++
		}
	}
	if started != 2 {
		t.Errorf("expected 2 HookSessionStarted events, got %d", started)
	}
}

// TestFirstUpdateFiresSessionStarted verifies that HookSessionStarted fires
// exactly once per session (on the very first update) and not on subsequent ones.
func TestFirstUpdateFiresSessionStarted(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	cap := &captureHandler{}
	mgr := newManager(cap)
	h := acp.NewIndexingSessionHandler(indexer, trail, mgr, "epoch-2")
	ctx := context.Background()

	for i := range 3 {
		u := makeUpdate("session-X", "user", "")
		if err := h.HandleUpdate(ctx, u); err != nil {
			t.Fatalf("HandleUpdate #%d: %v", i, err)
		}
	}

	started := 0
	for _, p := range cap.received {
		if p.Event == hooks.HookSessionStarted {
			started++
		}
	}
	if started != 1 {
		t.Errorf("expected exactly 1 HookSessionStarted, got %d", started)
	}
}

// TestHandleUpdatePersistsImmediately verifies that each update is indexed and
// persisted to the audit trail on HandleUpdate — before HandleSessionEnd.
func TestHandleUpdatePersistsImmediately(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	cap := &captureHandler{}
	mgr := newManager(cap)
	h := acp.NewIndexingSessionHandler(indexer, trail, mgr, "epoch-persist")
	ctx := context.Background()

	const sessionId = "session-imm"

	// Send two updates but do NOT call HandleSessionEnd yet.
	if err := h.HandleUpdate(ctx, makeUpdate(sessionId, "user", "")); err != nil {
		t.Fatalf("HandleUpdate/1: %v", err)
	}
	if err := h.HandleUpdate(ctx, makeUpdate(sessionId, "assistant", "")); err != nil {
		t.Fatalf("HandleUpdate/2: %v", err)
	}

	// Entries should already be in the trail without calling HandleSessionEnd.
	entries, err := trail.QuerySessionEntries(ctx, sessionId)
	if err != nil {
		t.Fatalf("QuerySessionEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries in trail after HandleUpdate (per-update flush mode), got 0 before HandleSessionEnd")
	}

	// EntriesRecorded should reflect what has been written so far.
	if h.EntriesRecorded() != len(entries) {
		t.Errorf("EntriesRecorded() = %d, want %d", h.EntriesRecorded(), len(entries))
	}
}

// TestHandleSessionEndIndexesAndPersists verifies the full end-of-session path:
// updates are persisted per-update, and HookSessionEnded is dispatched on end.
func TestHandleSessionEndIndexesAndPersists(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	cap := &captureHandler{}
	mgr := newManager(cap)
	h := acp.NewIndexingSessionHandler(indexer, trail, mgr, "epoch-3")
	ctx := context.Background()

	const sessionId = "session-Y"

	// Send two updates, then end the session.
	if err := h.HandleUpdate(ctx, makeUpdate(sessionId, "user", "")); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	if err := h.HandleUpdate(ctx, makeUpdate(sessionId, "assistant", "")); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	if err := h.HandleSessionEnd(ctx, sessionId, acp.StopReasonEndTurn); err != nil {
		t.Fatalf("HandleSessionEnd: %v", err)
	}

	// Verify entries were written to the audit trail.
	entries, err := trail.QuerySessionEntries(ctx, sessionId)
	if err != nil {
		t.Fatalf("QuerySessionEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 session entry after HandleSessionEnd, got 0")
	}
	for _, e := range entries {
		if e.SessionId != sessionId {
			t.Errorf("entry has unexpected sessionId %q", e.SessionId)
		}
	}

	// Verify HookSessionEnded was fired.
	ended := 0
	for _, p := range cap.received {
		if p.Event == hooks.HookSessionEnded {
			ended++
		}
	}
	if ended != 1 {
		t.Errorf("expected 1 HookSessionEnded event, got %d", ended)
	}
}

// TestMultipleSessionsAreIndependent verifies that updates for multiple sessions
// are each persisted immediately and independently.
func TestMultipleSessionsAreIndependent(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	cap := &captureHandler{}
	mgr := newManager(cap)
	h := acp.NewIndexingSessionHandler(indexer, trail, mgr, "epoch-4")
	ctx := context.Background()

	// Interleave updates for two sessions.
	if err := h.HandleUpdate(ctx, makeUpdate("sess-1", "user", "")); err != nil {
		t.Fatalf("HandleUpdate sess-1: %v", err)
	}
	if err := h.HandleUpdate(ctx, makeUpdate("sess-2", "user", "")); err != nil {
		t.Fatalf("HandleUpdate sess-2: %v", err)
	}
	if err := h.HandleUpdate(ctx, makeUpdate("sess-1", "assistant", "")); err != nil {
		t.Fatalf("HandleUpdate sess-1: %v", err)
	}

	// In per-update flush mode, BOTH sessions should have entries already
	// without needing to call HandleSessionEnd first.
	e1, err := trail.QuerySessionEntries(ctx, "sess-1")
	if err != nil {
		t.Fatalf("QuerySessionEntries sess-1: %v", err)
	}
	if len(e1) == 0 {
		t.Error("expected entries for sess-1 after updates (per-update flush mode)")
	}

	e2, err := trail.QuerySessionEntries(ctx, "sess-2")
	if err != nil {
		t.Fatalf("QuerySessionEntries sess-2: %v", err)
	}
	if len(e2) == 0 {
		t.Error("expected entries for sess-2 after updates (per-update flush mode)")
	}

	// End sess-1 only — does not affect sess-2 state.
	if err := h.HandleSessionEnd(ctx, "sess-1", acp.StopReasonEndTurn); err != nil {
		t.Fatalf("HandleSessionEnd sess-1: %v", err)
	}

	// sess-2 entries should remain available after sess-1 ends.
	e2After, err := trail.QuerySessionEntries(ctx, "sess-2")
	if err != nil {
		t.Fatalf("QuerySessionEntries sess-2 (after sess-1 end): %v", err)
	}
	if len(e2After) != len(e2) {
		t.Errorf("sess-2 entries changed after sess-1 ended: was %d, now %d", len(e2), len(e2After))
	}
}

// TestHandleSessionEndEmptyUpdates verifies that ending a session with no prior
// updates is a no-op (no panic, no error, no entries written).
func TestHandleSessionEndEmptyUpdates(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	cap := &captureHandler{}
	mgr := newManager(cap)
	h := acp.NewIndexingSessionHandler(indexer, trail, mgr, "epoch-5")
	ctx := context.Background()

	// End a session that was never started.
	if err := h.HandleSessionEnd(ctx, "sess-ghost", acp.StopReasonCancelled); err != nil {
		t.Fatalf("HandleSessionEnd on empty session: %v", err)
	}

	// No entries should have been written.
	entries, err := trail.QuerySessionEntries(ctx, "sess-ghost")
	if err != nil {
		t.Fatalf("QuerySessionEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for ghost session, got %d", len(entries))
	}

	// HookSessionEnded should still have fired (best-effort).
	ended := 0
	for _, p := range cap.received {
		if p.Event == hooks.HookSessionEnded {
			ended++
		}
	}
	if ended != 1 {
		t.Errorf("expected 1 HookSessionEnded even for empty session, got %d", ended)
	}
}

// TestNilHooksMgrIsNoOp verifies that a nil hooksMgr skips dispatch without panic.
func TestNilHooksMgrIsNoOp(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	// Explicitly pass nil hooksMgr.
	h := acp.NewIndexingSessionHandler(indexer, trail, nil, "epoch-6")
	ctx := context.Background()

	if err := h.HandleUpdate(ctx, makeUpdate("sess-nil", "user", "")); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	if err := h.HandleSessionEnd(ctx, "sess-nil", acp.StopReasonEndTurn); err != nil {
		t.Fatalf("HandleSessionEnd: %v", err)
	}

	// Entries should still be written despite no hooks manager.
	entries, err := trail.QuerySessionEntries(ctx, "sess-nil")
	if err != nil {
		t.Fatalf("QuerySessionEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least 1 entry even with nil hooksMgr")
	}
}

// TestHandlerImplementsInterface verifies compile-time that IndexingSessionHandler
// implements the acp.SessionHandler interface.
func TestHandlerImplementsInterface(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	var _ acp.SessionHandler = acp.NewIndexingSessionHandler(indexer, trail, nil, "epoch-7")
}

// TestHandlerEpochIDInHookPayload verifies the epochId is propagated to hook payloads.
func TestHandlerEpochIDInHookPayload(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	cap := &captureHandler{}
	mgr := newManager(cap)
	const epochId = "epoch-abc-123"
	h := acp.NewIndexingSessionHandler(indexer, trail, mgr, epochId)
	ctx := context.Background()

	if err := h.HandleUpdate(ctx, makeUpdate("sess-epoch", "user", "")); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}

	for _, p := range cap.received {
		if p.EpochId != epochId {
			t.Errorf("hook payload has epochId=%q, want %q", p.EpochId, epochId)
		}
	}
}

// TestTrailQueryAfterSessionEnd verifies that trail entries have the correct provider.
func TestTrailQueryAfterSessionEnd(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	h := acp.NewIndexingSessionHandler(indexer, trail, nil, "epoch-8")
	ctx := context.Background()

	const sessionId = "sess-prov"
	if err := h.HandleUpdate(ctx, makeUpdate(sessionId, "assistant", "")); err != nil {
		t.Fatalf("HandleUpdate: %v", err)
	}
	if err := h.HandleSessionEnd(ctx, sessionId, acp.StopReasonEndTurn); err != nil {
		t.Fatalf("HandleSessionEnd: %v", err)
	}

	entries, err := trail.QuerySessionEntries(ctx, sessionId)
	if err != nil {
		t.Fatalf("QuerySessionEntries: %v", err)
	}

	for _, e := range entries {
		if e.Provider != "acp" {
			t.Errorf("entry.Provider = %q, want %q", e.Provider, "acp")
		}
	}
}

// TestEntriesRecordedCountsAcrossUpdates verifies EntriesRecorded accumulates
// correctly as each update is flushed per-update.
func TestEntriesRecordedCountsAcrossUpdates(t *testing.T) {
	t.Parallel()
	trail := audit.NewInMemoryAuditTrail()
	indexer := acp.NewSharedIndexer()
	h := acp.NewIndexingSessionHandler(indexer, trail, nil, "epoch-count")
	ctx := context.Background()

	const sessionId = "sess-count"

	// Initial count should be 0.
	if h.EntriesRecorded() != 0 {
		t.Errorf("initial EntriesRecorded() = %d, want 0", h.EntriesRecorded())
	}

	// Each update should increment the count.
	for i := 1; i <= 3; i++ {
		if err := h.HandleUpdate(ctx, makeUpdate(sessionId, "user", "")); err != nil {
			t.Fatalf("HandleUpdate #%d: %v", i, err)
		}
		if h.EntriesRecorded() < i {
			// EntriesRecorded should be at least i (indexer may produce >1 entry per update).
			t.Errorf("after update #%d: EntriesRecorded() = %d, want >= %d",
				i, h.EntriesRecorded(), i)
		}
	}
}

// ─── Ensure protocol.SessionEntry is importable (no test-only dual-export) ───

// TestSessionEntryType is a trivial check that protocol.SessionEntry is the
// same type used in both the production trail and the test assertions.
func TestSessionEntryType(t *testing.T) {
	t.Parallel()
	var _ []protocol.SessionEntry // type must resolve without import cycle
}
