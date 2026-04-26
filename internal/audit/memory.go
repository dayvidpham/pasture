package audit

import (
	"context"
	"sync"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// InMemoryAuditTrail is a Trail implementation backed by an in-memory slice.
//
// Intended for testing and local development. Events are not persisted across
// process restarts. All methods are safe for concurrent use.
//
// # Synthetic event ids
//
// RecordEventReturningID returns a synthetic monotonic counter value
// (lastEventID, starting at 1 on the first call). The counter is per-trail
// and increments under m.mu, so two concurrent callers always observe two
// distinct ids — matching the SQLite-backed trail's per-statement-LastInsertId
// guarantee. The id is NOT a real audit_events row id and MUST NOT be
// persisted across processes; it exists so test code that exercises
// AttachContext-style flows against the in-memory trail can rely on
// per-call uniqueness.
type InMemoryAuditTrail struct {
	mu             sync.RWMutex
	events         []protocol.AuditEvent
	sessionEntries []protocol.SessionEntry
	// lastEventID is the synthetic monotonic counter handed out by
	// RecordEventReturningID. Guarded by mu (held in WRITE mode whenever the
	// counter is read-and-incremented atomically).
	lastEventID int64
}

// NewInMemoryAuditTrail returns an empty, ready-to-use InMemoryAuditTrail.
func NewInMemoryAuditTrail() *InMemoryAuditTrail {
	return &InMemoryAuditTrail{}
}

// RecordEvent appends event to the in-memory list, discarding the synthetic
// row id. It is safe for concurrent use. See RecordEventReturningID for the
// id-returning variant; callers that need the id MUST use that.
func (m *InMemoryAuditTrail) RecordEvent(ctx context.Context, event protocol.AuditEvent) error {
	_, err := m.RecordEventReturningID(ctx, event)
	return err
}

// RecordEventReturningID appends event and returns a synthetic monotonic
// id (per-trail, starting at 1, incremented on every successful call). The
// id-and-append are performed atomically under m.mu so concurrent callers
// always observe distinct ids — matching the SQLite-backed trail's
// per-statement-LastInsertId race-safety guarantee.
//
// The returned id is NOT a real audit_events row id and is only meaningful
// for the lifetime of this in-memory trail; do not persist it.
func (m *InMemoryAuditTrail) RecordEventReturningID(_ context.Context, event protocol.AuditEvent) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastEventID++
	m.events = append(m.events, event)
	return m.lastEventID, nil
}

// QueryEvents returns all events matching the filters in insertion order.
//
// epochID is required. phase and role are optional; nil means "no filter".
func (m *InMemoryAuditTrail) QueryEvents(_ context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []protocol.AuditEvent
	for _, ev := range m.events {
		if ev.EpochID != epochID {
			continue
		}
		if phase != nil && ev.Phase != *phase {
			continue
		}
		if role != nil && ev.Role != *role {
			continue
		}
		result = append(result, ev)
	}
	return result, nil
}

// Events returns a defensive copy of all recorded events.
//
// Intended for use in tests and assertions — callers receive a snapshot that
// is safe to inspect without holding the internal lock.
func (m *InMemoryAuditTrail) Events() []protocol.AuditEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]protocol.AuditEvent, len(m.events))
	copy(cp, m.events)
	return cp
}

// RecordSessionEntries appends the given entries to the in-memory session entry
// list. Nil or empty slices are accepted as no-ops. Safe for concurrent use.
func (m *InMemoryAuditTrail) RecordSessionEntries(_ context.Context, entries []protocol.SessionEntry) error {
	if len(entries) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionEntries = append(m.sessionEntries, entries...)
	return nil
}

// QuerySessionEntries returns all session entries for the given sessionID in
// insertion order. Returns an empty (non-nil) slice when no entries exist.
// Safe for concurrent use.
func (m *InMemoryAuditTrail) QuerySessionEntries(_ context.Context, sessionID string) ([]protocol.SessionEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]protocol.SessionEntry, 0)
	for _, e := range m.sessionEntries {
		if e.SessionID == sessionID {
			result = append(result, e)
		}
	}
	return result, nil
}
