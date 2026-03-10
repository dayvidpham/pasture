package audit

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Module-Level Trail Singleton ─────────────────────────────────────────────

// trail is the module-level singleton injected before the Temporal worker starts.
// Activities delegate all persistence calls to this value. Access to this
// variable is not protected by a mutex because it is written exactly once at
// worker startup (InitTrail) and read-only thereafter — the same pattern used
// in the Python aura-protocol reference implementation.
var trail Trail

// uninitializedMsg is the error text shown when an activity runs before
// InitTrail has been called. It is actionable: it describes what failed, why,
// where, and how to fix it.
const uninitializedMsg = "audit trail not initialized" +
	" — what: RecordAuditEvent/QueryAuditEvents called before InitTrail" +
	" — why: the Temporal worker must inject a Trail implementation at startup" +
	" — where: audit.InitTrail() in your worker main() or test setup" +
	" — fix: call audit.InitTrail(impl) before starting the Temporal worker"

// InitTrail injects the Trail implementation used by the activity wrappers.
//
// Must be called once before the Temporal worker starts. Passing nil resets
// the singleton (useful in tests to isolate state between test cases).
//
// This function is not concurrency-safe with activity execution; call it
// during worker startup, before any activities can run.
func InitTrail(t Trail) {
	trail = t
}

// ─── Temporal Activity Wrappers ───────────────────────────────────────────────

// RecordAuditEvent is a Temporal activity wrapper that persists an AuditEvent
// via the injected Trail.
//
// The caller is responsible for setting an appropriate start_to_close_timeout
// and retry policy on the Temporal activity options. This function returns an
// error (not an application error with non_retryable) so Temporal can apply
// its standard retry logic; the injected Trail is expected to be idempotent or
// the caller should set MaxAttempts=1 if strict once-only semantics are needed.
//
// Returns an error if:
//   - InitTrail was not called before this activity ran.
//   - The underlying Trail.RecordEvent fails (e.g. I/O error).
func RecordAuditEvent(ctx context.Context, event protocol.AuditEvent) error {
	if trail == nil {
		return fmt.Errorf(uninitializedMsg)
	}
	return trail.RecordEvent(ctx, event)
}

// QueryAuditEvents is a Temporal activity wrapper that queries audit events
// via the injected Trail.
//
// epochID is required. phase and role are optional filters; pass nil to skip.
//
// Returns (nil, error) if:
//   - InitTrail was not called before this activity ran.
//   - The underlying Trail.QueryEvents fails.
func QueryAuditEvents(ctx context.Context, epochID string, phase *protocol.PhaseId, role *string) ([]protocol.AuditEvent, error) {
	if trail == nil {
		return nil, fmt.Errorf(uninitializedMsg)
	}
	return trail.QueryEvents(ctx, epochID, phase, role)
}

// RecordSessionEntries is a Temporal activity wrapper that persists a batch of
// SessionEntry records via the injected Trail.
//
// Nil or empty slices are accepted as no-ops. All entries in the batch are
// written atomically where the backend supports transactions (SQLite).
//
// Returns an error if:
//   - InitTrail was not called before this activity ran.
//   - The underlying Trail.RecordSessionEntries fails (e.g. I/O error).
func RecordSessionEntries(ctx context.Context, entries []protocol.SessionEntry) error {
	if trail == nil {
		return fmt.Errorf(uninitializedMsg)
	}
	return trail.RecordSessionEntries(ctx, entries)
}

// QuerySessionEntries is a Temporal activity wrapper that retrieves all session
// entries for the given sessionID via the injected Trail.
//
// Returns an empty (non-nil) slice when no entries exist for sessionID.
// Returns (nil, error) if:
//   - InitTrail was not called before this activity ran.
//   - The underlying Trail.QuerySessionEntries fails.
func QuerySessionEntries(ctx context.Context, sessionID string) ([]protocol.SessionEntry, error) {
	if trail == nil {
		return nil, fmt.Errorf(uninitializedMsg)
	}
	return trail.QuerySessionEntries(ctx, sessionID)
}
