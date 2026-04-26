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
	//
	// Callers that need the inserted row id (so they can attach context_edges
	// rows in the same logical step) MUST use RecordEventReturningID instead.
	// RecordEvent is retained for callers that record-and-forget; it is a
	// thin wrapper over RecordEventReturningID that discards the id.
	RecordEvent(ctx context.Context, event protocol.AuditEvent) error

	// RecordEventReturningID persists a single audit event and returns the
	// audit_events.id of the newly-inserted row.
	//
	// Implementations MUST recover the id from the same INSERT statement
	// that wrote the row (e.g. via sql.Result.LastInsertId for SQLite-backed
	// trails) so the returned id is race-safe under concurrent writes.
	// A separate post-write SELECT MAX(id) workaround is forbidden — that
	// pattern races with a concurrent writer's INSERT and can return a row
	// id that belongs to a DIFFERENT goroutine.
	//
	// On the SQLite backend this is the canonical write path; RecordEvent
	// delegates here and discards the id. On the in-memory backend the id
	// is a synthetic monotonic counter scoped to the trail's lifetime — it
	// is NOT a real audit_events row id and MUST NOT be persisted across
	// processes; the counter exists so test code that asserts unique-id-
	// per-call behaviour works against the in-memory trail too.
	//
	// Returns the new id and a nil error on success; on failure returns 0
	// and an error (typically *pasterrors.StructuredError on the SQLite
	// backend so callers can errors.As() it for category-based handling).
	RecordEventReturningID(ctx context.Context, event protocol.AuditEvent) (int64, error)

	// QueryEvents returns all audit events matching the given filters.
	//
	// epochID is required and filters by exact match. phase and role are
	// optional; a nil pointer means "no filter on this field". Results are
	// returned in chronological order (insertion order for in-memory,
	// ascending id order for SQLite).
	QueryEvents(ctx context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error)

	// RecordSessionEntries persists a batch of SessionEntry records.
	//
	// Entries are appended; existing entries for the same session are not
	// replaced. The caller should pass all entries for a turn in a single call
	// for efficient batch INSERT in the SQLite backend.
	//
	// Returns an error if the underlying store is unavailable or the write
	// fails. The caller (Temporal activity) is responsible for retry policy.
	RecordSessionEntries(ctx context.Context, entries []protocol.SessionEntry) error

	// QuerySessionEntries returns all session entries for the given sessionID
	// in insertion order (ascending entry_index for SQLite, insertion order
	// for in-memory).
	//
	// Returns an empty (non-nil) slice when no entries exist for sessionID.
	// Returns an error if the underlying store is unavailable.
	QuerySessionEntries(ctx context.Context, sessionID string) ([]protocol.SessionEntry, error)
}
