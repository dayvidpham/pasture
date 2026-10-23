// Package sessionapitest builds the session-cache fixture shared by the CLI and
// handler tests. It is test support only: production code never imports it.
package sessionapitest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// Fixture session identities. Tests should assert against these names rather
// than restating the strings, so a fixture change fails one constant.
const (
	HarnessClaudeCode = "claude-code"
	HarnessOpenCode   = "opencode"
	HarnessCodex      = "codex"

	ActiveSession  = "active-session"
	IdleSession    = "idle-session"
	EndedSession   = "ended-session"
	UnknownSession = "unknown-session"

	EpochOne = "file://epoch/one"
	EpochTwo = "file://epoch/two"
)

// SeedStore creates a fresh, migrated unified store and seeds the binding and
// claim rows the session cache is derived from:
//
//   - one active claude-code session with a closed prior interval and an open
//     current interval (historical provenance is visible in the fixture),
//   - one idle claude-code session with a single open interval,
//   - one ended opencode session with a closed prior interval and a closed
//     current interval,
//   - one codex claim with no binding (unknown state).
//
// The returned path is the only thing the caller needs; the command or handler
// under test runs the journal-aware rebuild that turns these rows into cache
// rows.
func SeedStore(tb testing.TB) string {
	tb.Helper()

	path := filepath.Join(tb.TempDir(), "pasture.db")
	tracker, err := tasks.OpenTaskTracker(path)
	if err != nil {
		tb.Fatalf("seed session store: open tracker: %v", err)
	}
	if err := tracker.Close(); err != nil {
		tb.Fatalf("seed session store: close tracker: %v", err)
	}

	db, err := dbconn.OpenSharedDBWithProfile(path, timeouts.TestProfile())
	if err != nil {
		tb.Fatalf("seed session store: open seeded handle: %v", err)
	}
	defer func() { _ = db.Close() }()

	now := time.Now().UnixNano()
	minute := int64(time.Minute)
	closed := func(minutesAgo int) *int64 {
		at := now - int64(minutesAgo)*minute
		return &at
	}
	reasonUnbind := "unbind"
	reasonSessionEnd := "session_end"

	bindings := []struct {
		id        string
		harness   string
		session   string
		epoch     string
		role      string
		boundAt   int64
		unboundAt *int64
		reason    *string
	}{
		{id: "binding-active-previous", harness: HarnessClaudeCode, session: ActiveSession, epoch: EpochTwo, role: "reviewer", boundAt: now - 90*minute, unboundAt: closed(10), reason: &reasonUnbind},
		{id: "binding-active", harness: HarnessClaudeCode, session: ActiveSession, epoch: EpochOne, role: "implementer", boundAt: now - minute},
		{id: "binding-idle", harness: HarnessClaudeCode, session: IdleSession, epoch: EpochTwo, role: "reviewer", boundAt: now - 20*minute},
		{id: "binding-ended-previous", harness: HarnessOpenCode, session: EndedSession, epoch: EpochOne, role: "reviewer", boundAt: now - 70*minute, unboundAt: closed(40), reason: &reasonUnbind},
		{id: "binding-ended", harness: HarnessOpenCode, session: EndedSession, epoch: EpochTwo, role: "worker", boundAt: now - 30*minute, unboundAt: closed(5), reason: &reasonSessionEnd},
	}
	ctx := context.Background()
	for _, b := range bindings {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO pasture_session_epoch_binding
				(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			b.id, b.harness, b.session, b.epoch, b.role, b.boundAt, b.unboundAt, b.reason); err != nil {
			tb.Fatalf("seed session store: insert binding %s: %v", b.id, err)
		}
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO pasture_session_claim (harness, session, actor, claimed_at)
		VALUES (?, ?, 'file://actor/session-fixture', ?)`,
		HarnessCodex, UnknownSession, now-2*minute); err != nil {
		tb.Fatalf("seed session store: insert claim: %v", err)
	}

	return path
}
