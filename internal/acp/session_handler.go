// Package acp — IndexingSessionHandler implements SessionHandler by accumulating
// ACP session updates, indexing them via SharedIndexer on session end, and
// persisting the resulting entries to the audit trail. Hook events are fired at
// session start and end via the injected hooks.Manager.
package acp

import (
	"context"
	"sync"

	"github.com/dayvidpham/pasture/internal/audit"
	"github.com/dayvidpham/pasture/internal/hooks"
)

// IndexingSessionHandler implements SessionHandler by accumulating updates per
// session and flushing them as audit SessionEntry records when the session ends.
//
// All methods are safe for concurrent invocation (multiple sessions may run in
// parallel within a single Temporal activity).
//
// Lifecycle per session:
//  1. First HandleUpdate call → fire HookSessionStarted, accumulate update.
//  2. Subsequent HandleUpdate calls → accumulate updates (no extra hooks).
//  3. HandleSessionEnd → Index updates → RecordSessionEntries → fire HookSessionEnded → clean up.
type IndexingSessionHandler struct {
	indexer  *SharedIndexer
	trail    audit.Trail
	hooksMgr *hooks.Manager
	epochID  string

	mu              sync.Mutex
	updates         map[string][]SessionUpdate // sessionID → accumulated updates
	entriesWritten  int                        // total entries persisted across all sessions
	lastSessionID   string                     // last session ID seen via HandleSessionEnd
	lastStopReason  StopReason                 // stop reason from the last HandleSessionEnd call
}

// NewIndexingSessionHandler constructs a ready-to-use IndexingSessionHandler.
//
//   - indexer must not be nil.
//   - trail must not be nil.
//   - hooksMgr may be nil (hooks are optional; nil manager is a no-op).
//   - epochID is embedded in every hook payload so consumers can correlate events.
func NewIndexingSessionHandler(
	indexer *SharedIndexer,
	trail audit.Trail,
	hooksMgr *hooks.Manager,
	epochID string,
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
		epochID:  epochID,
		updates:  make(map[string][]SessionUpdate),
	}
}

// HandleUpdate accumulates update into the per-session queue.
//
// On the first update for a session, HookSessionStarted is dispatched via the
// Manager (if non-nil). Hook errors are treated as best-effort and logged but
// do not cause HandleUpdate to return an error.
//
// Returns an error only if the context is already cancelled.
func (h *IndexingSessionHandler) HandleUpdate(ctx context.Context, update SessionUpdate) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	h.mu.Lock()
	existing := h.updates[update.SessionID]
	isFirst := len(existing) == 0
	h.updates[update.SessionID] = append(existing, update)
	h.mu.Unlock()

	if isFirst {
		h.dispatchHook(ctx, hooks.HookSessionStarted, update.SessionID, nil)
	}

	return nil
}

// HandleSessionEnd finalises the session identified by sessionID.
//
// Steps (in order):
//  1. Retrieve accumulated updates for the session (under lock; then release).
//  2. Call indexer.Index to convert updates → []protocol.SessionEntry.
//  3. Persist entries via trail.RecordSessionEntries.
//  4. Dispatch HookSessionEnded (best-effort).
//  5. Remove the session from the accumulation map.
//
// Returns the first fatal error encountered in steps 2–3. Step 4 errors are
// best-effort and do not cause HandleSessionEnd to return an error.
func (h *IndexingSessionHandler) HandleSessionEnd(ctx context.Context, sessionID string, reason StopReason) error {
	h.mu.Lock()
	accumulated := h.updates[sessionID]
	// Copy to avoid holding the lock during indexing and I/O.
	snapshot := make([]SessionUpdate, len(accumulated))
	copy(snapshot, accumulated)
	// Delete immediately under the same lock to prevent a concurrent
	// HandleSessionEnd from re-processing the same session (double-end race).
	delete(h.updates, sessionID)
	h.mu.Unlock()

	// Index updates → SessionEntry rows.
	entries, err := h.indexer.Index(snapshot)
	if err != nil {
		return err
	}

	// Persist to audit trail.
	if len(entries) > 0 {
		if err := h.trail.RecordSessionEntries(ctx, entries); err != nil {
			return err
		}
	}

	// Record session outcome and update counters under a single lock acquisition.
	h.mu.Lock()
	h.entriesWritten += len(entries)
	h.lastSessionID = sessionID
	h.lastStopReason = reason
	h.mu.Unlock()

	// Fire HookSessionEnded (best-effort; do not return hook errors).
	h.dispatchHook(ctx, hooks.HookSessionEnded, sessionID, map[string]any{
		"stopReason":      string(reason),
		"entriesRecorded": len(entries),
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

// LastSessionID returns the session ID from the most recent HandleSessionEnd
// call. Returns an empty string if no session has ended yet.
// Safe for concurrent use.
func (h *IndexingSessionHandler) LastSessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastSessionID
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
// Errors are intentionally swallowed — hook dispatch is best-effort.
func (h *IndexingSessionHandler) dispatchHook(
	ctx context.Context,
	event hooks.HookEvent,
	sessionID string,
	extra map[string]any,
) {
	if h.hooksMgr == nil {
		return
	}

	data := map[string]any{
		"sessionId": sessionID,
	}
	for k, v := range extra {
		data[k] = v
	}

	payload := hooks.HookPayload{
		Event:   event,
		EpochID: h.epochID,
		Data:    data,
	}
	//nolint:errcheck // hook dispatch is best-effort; errors are intentionally ignored
	_ = h.hooksMgr.Dispatch(ctx, payload)
}
