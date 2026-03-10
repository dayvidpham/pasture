// Package audit provides the pluggable audit persistence interface and
// concrete implementations for the Pasture epoch workflow audit trail.
//
// The Trail interface is the core abstraction; all persistence is routed
// through it. Two implementations are provided:
//
//   - InMemoryAuditTrail — for testing and local development (non-durable).
//   - SqliteAuditTrail  — for production use (durable, CGO-free via modernc.org/sqlite).
//
// Temporal activity wrappers (RecordAuditEvent, QueryAuditEvents) delegate to
// a module-level Trail singleton that must be initialized via InitTrail before
// the worker starts.
package audit

import (
	"context"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// Trail is the pluggable audit persistence interface.
//
// All implementations must be safe for concurrent use (multiple goroutines may
// call RecordEvent and QueryEvents simultaneously inside a Temporal worker).
type Trail interface {
	// RecordEvent persists a single audit event.
	//
	// Returns an error if the underlying store is unavailable or the write
	// fails. The caller (Temporal activity) is responsible for retry policy.
	RecordEvent(ctx context.Context, event protocol.AuditEvent) error

	// QueryEvents returns all audit events matching the given filters.
	//
	// epochID is required and filters by exact match. phase and role are
	// optional; a nil pointer means "no filter on this field". Results are
	// returned in chronological order (insertion order for in-memory,
	// ascending id order for SQLite).
	QueryEvents(ctx context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error)
}
