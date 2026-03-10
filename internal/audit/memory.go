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
type InMemoryAuditTrail struct {
	mu     sync.RWMutex
	events []protocol.AuditEvent
}

// NewInMemoryAuditTrail returns an empty, ready-to-use InMemoryAuditTrail.
func NewInMemoryAuditTrail() *InMemoryAuditTrail {
	return &InMemoryAuditTrail{}
}

// RecordEvent appends event to the in-memory list. It is safe for concurrent use.
func (m *InMemoryAuditTrail) RecordEvent(_ context.Context, event protocol.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
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
