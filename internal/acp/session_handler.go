// Package acp — IndexingSessionHandler implements SessionHandler by flushing
// each ACP session update to the audit trail immediately (per-update mode).
// Hook events are fired at session start and end via the injected hooks.Manager.
package acp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/hooks"
)

// IndexingSessionHandler implements SessionHandler by indexing and persisting
// each update immediately on HandleUpdate (per-update flush mode).
//
// All methods are safe for concurrent invocation (multiple sessions may run in
// parallel within a single Temporal activity).
//
// Lifecycle per session:
//  1. First HandleUpdate call → fire HookSessionStarted, index update, persist to trail.
//  2. Subsequent HandleUpdate calls → index update, persist to trail (no extra hooks).
//  3. HandleSessionEnd → fire HookSessionEnded → clean up session tracking state.
type IndexingSessionHandler struct {
	indexer  *SharedIndexer
	trail    audit.Trail
	hooksMgr *hooks.Manager
	epochId  string

	mu             sync.Mutex
	sessions       map[string]bool // sessionId → started (true once first update received)
	entriesWritten int             // total entries persisted across all sessions
	lastSessionId  string          // last session ID seen via HandleSessionEnd
	lastStopReason StopReason      // stop reason from the last HandleSessionEnd call
}

// NewIndexingSessionHandler constructs a ready-to-use IndexingSessionHandler.
//
//   - indexer must not be nil.
//   - trail must not be nil.
//   - hooksMgr may be nil (hooks are optional; nil manager is a no-op).
//   - epochId is embedded in every hook payload so consumers can correlate events.
func NewIndexingSessionHandler(
	indexer *SharedIndexer,
	trail audit.Trail,
	hooksMgr *hooks.Manager,
	epochId string,
) *IndexingSessionHandler {
	if indexer == nil {
		panic("acp.NewIndexingSessionHandler: indexer must not be nil")
	}
	if trail == nil {
		panic("acp.NewIndexingSessionHandler: trail must not be nil")
	}
	return &IndexingSessionHandler{
		indexer:  indexer,
		trail:    trail,
		hooksMgr: hooksMgr,
		epochId:  epochId,
		sessions: make(map[string]bool),
	}
}

// HandleUpdate indexes the update immediately and persists it to the audit trail.
//
// On the first update for a session, HookSessionStarted is dispatched via the
// Manager (if non-nil). Hook errors are treated as best-effort: logged via
// slog.Warn but do not cause HandleUpdate to return an error.
//
// Returns an error only if the context is already cancelled, indexing fails,
// or the trail write fails.
func (h *IndexingSessionHandler) HandleUpdate(ctx context.Context, update SessionUpdate) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Determine whether this is the first update for this session (under lock).
	h.mu.Lock()
	isFirst := !h.sessions[update.SessionId]
	if isFirst {
		h.sessions[update.SessionId] = true
	}
	h.mu.Unlock()

	// Fire HookSessionStarted on the first update for this session (best-effort).
	if isFirst {
		h.dispatchHook(ctx, hooks.HookSessionStarted, update.SessionId, nil)
	}

	// Index the single update → []protocol.SessionEntry.
	entries, err := h.indexer.Index([]SessionUpdate{update})
	if err != nil {
		return fmt.Errorf("acp.IndexingSessionHandler.HandleUpdate: indexing failed (sessionId=%q): %w",
			update.SessionId, err)
	}

	// Persist to audit trail immediately.
	if len(entries) > 0 {
		if err := h.trail.RecordSessionEntries(ctx, entries); err != nil {
			return fmt.Errorf("acp.IndexingSessionHandler.HandleUpdate: trail write failed (sessionId=%q, entries=%d): %w",
				update.SessionId, len(entries), err)
		}
		h.mu.Lock()
		h.entriesWritten += len(entries)
		h.mu.Unlock()
	}

	return nil
}

// HandleSessionEnd finalises the session identified by sessionId.
//
// Steps (in order):
//  1. Dispatch HookSessionEnded (best-effort).
//  2. Record last session ID and stop reason.
//  3. Remove the session from the tracking map.
//
// No bulk indexing occurs here — entries are already persisted by HandleUpdate.
// Returns nil unless the context is cancelled.
func (h *IndexingSessionHandler) HandleSessionEnd(ctx context.Context, sessionId string, reason StopReason) error {
	// Read current entries count for the hook payload (under lock).
	h.mu.Lock()
	currentEntries := h.entriesWritten
	h.lastSessionId = sessionId
	h.lastStopReason = reason
	delete(h.sessions, sessionId)
	h.mu.Unlock()

	// Fire HookSessionEnded (best-effort; do not return hook errors).
	h.dispatchHook(ctx, hooks.HookSessionEnded, sessionId, map[string]any{
		"stopReason":      string(reason),
		"entriesRecorded": currentEntries,
	})

	return nil
}

// EntriesRecorded returns the total number of SessionEntry rows persisted by
// this handler across all sessions so far.
// Safe for concurrent use.
func (h *IndexingSessionHandler) EntriesRecorded() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.entriesWritten
}

// LastSessionId returns the session ID from the most recent HandleSessionEnd
// call. Returns an empty string if no session has ended yet.
// Safe for concurrent use.
func (h *IndexingSessionHandler) LastSessionId() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastSessionId
}

// LastStopReason returns the StopReason from the most recent HandleSessionEnd
// call. Returns an empty StopReason if no session has ended yet.
// Safe for concurrent use.
func (h *IndexingSessionHandler) LastStopReason() StopReason {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastStopReason
}

// dispatchHook fires an event to the Manager if it is non-nil.
// Hook dispatch errors are logged via slog.Warn and intentionally not returned —
// hook dispatch is best-effort and must not disrupt the session lifecycle.
func (h *IndexingSessionHandler) dispatchHook(
	ctx context.Context,
	event hooks.HookEvent,
	sessionId string,
	extra map[string]any,
) {
	if h.hooksMgr == nil {
		return
	}

	data := map[string]any{
		"sessionId": sessionId,
	}
	for k, v := range extra {
		data[k] = v
	}

	payload := hooks.HookPayload{
		Event:   event,
		EpochId: h.epochId,
		Data:    data,
	}
	if _, err := h.hooksMgr.Dispatch(ctx, payload); err != nil {
		slog.Warn("hook dispatch failed",
			"what", fmt.Sprintf("hook dispatch failed for event %s", payload.Event),
			"why", err.Error(),
			"impact", "hook handlers for this event did not execute",
			"fix", "check handler registration and handler implementation",
			"event", string(event),
			"sessionId", sessionId,
			"epochId", h.epochId,
		)
	}
}
