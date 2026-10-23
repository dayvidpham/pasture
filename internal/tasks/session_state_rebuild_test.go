package tasks_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

const rebuildClaudeSession = "c02859c0-10ab-49c3-9b93-29280bd45fbb"

func TestRebuildSessionStateUsesBindingAndOccurrence(t *testing.T) {
	t.Parallel()
	tracker, db, path := claimStore(t)
	actor, err := tracker.RegisterHumanAgent("rebuild-holder", "Rebuild Holder", "rebuild-holder@example.test")
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	outcome, err := handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:         path,
		Harness:        "claude-code",
		Event:          "SessionStart",
		HostVersion:    "2.1.261",
		Input:          bytes.NewReader(raw),
		Clock:          claimClock{},
		Operations:     claimOperation("rebuild.first"),
		ActorClaim:     tasks.ActorClaim(actor.ID.String()),
		TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)
	require.Equal(t, hostexit.ExitContinue, outcome.Exit)

	// An explicit binding adds a later activity instant than the receipt. The
	// rebuild must preserve both edges, not just the occurrence.
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO pasture_session_epoch_binding
			(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES ('binding-rebuild', 'claude-code', ?, 'file://epoch-rebuild', 'implementer', ?, NULL, NULL)`,
		rebuildClaudeSession, time.Unix(1700000005, 0).UnixNano())
	require.NoError(t, err)
	// A binding-only session with no receipt and no claim must still appear.
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO pasture_session_epoch_binding
			(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES ('binding-only', 'claude-code', 'binding-only-session', 'file://epoch-rebuild', 'reviewer', ?, NULL, NULL)`,
		time.Unix(1700000010, 0).UnixNano())
	require.NoError(t, err)

	rebuild, err := tasks.RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.Equal(t, 2, rebuild.Sessions)
	require.Equal(t, int64(2), rebuild.Revision)
	require.Greater(t, rebuild.CompletedThroughJournalID, int64(0))

	var firstSeen, lastSeen, journalID int64
	var lastEvent string
	require.NoError(t, db.QueryRow(`
		SELECT first_seen, last_seen, last_event, last_journal_id
		FROM pasture_session_state WHERE harness = 'claude-code' AND session = ?`,
		rebuildClaudeSession).Scan(&firstSeen, &lastSeen, &lastEvent, &journalID))
	require.Equal(t, int64(1700000000_000000000), firstSeen)
	require.Equal(t, int64(1700000005_000000000), lastSeen)
	require.Equal(t, "SessionStart", lastEvent)
	require.Greater(t, journalID, int64(0))

	require.NoError(t, db.QueryRow(`
		SELECT first_seen, last_seen, last_event, last_journal_id
		FROM pasture_session_state WHERE harness = 'claude-code' AND session = 'binding-only-session'`).
		Scan(&firstSeen, &lastSeen, &lastEvent, &journalID))
	require.Equal(t, int64(1700000010_000000000), firstSeen)
	require.Equal(t, int64(1700000010_000000000), lastSeen)
	require.Empty(t, lastEvent)
	require.Equal(t, int64(0), journalID)

	var dirty, completed, revision int64
	require.NoError(t, db.QueryRow(`
		SELECT dirty, completed_through_jid, revision
		FROM pasture_session_api_state WHERE singleton_id = 0`).Scan(&dirty, &completed, &revision))
	require.Equal(t, int64(0), dirty)
	require.Equal(t, rebuild.CompletedThroughJournalID, completed)
	require.Equal(t, int64(2), revision)
}

func TestEnsureSessionStateRebuildsOnlyWhenNeeded(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)

	// The singleton exists with revision 0, so the first ensure must rebuild.
	rebuild, rebuilt, err := tasks.EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.True(t, rebuilt)
	require.Equal(t, int64(1), rebuild.Revision)

	// A clean marker and no later occurrence must not replay.
	_, rebuilt, err = tasks.EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.False(t, rebuilt)

	// A dirty marker is the raw-import and state-failure signal.
	require.NoError(t, tasks.MarkSessionStateDirty(t.Context(), tracker))
	rebuild, rebuilt, err = tasks.EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.True(t, rebuilt)
	require.Equal(t, int64(2), rebuild.Revision)

	var dirty int64
	require.NoError(t, db.QueryRow(`SELECT dirty FROM pasture_session_api_state WHERE singleton_id = 0`).Scan(&dirty))
	require.Equal(t, int64(0), dirty)
}

func readRebuiltState(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}, harness, session string) (first, last, journal, updated int64, event string) {
	t.Helper()
	err := db.QueryRow(`
		SELECT first_seen, last_seen, last_journal_id, updated_at, last_event
		FROM pasture_session_state WHERE harness = ? AND session = ?`, harness, session).
		Scan(&first, &last, &journal, &updated, &event)
	require.NoError(t, err)
	return first, last, journal, updated, event
}

func TestRebuildSessionStateMatchesLiveTail(t *testing.T) {
	t.Parallel()
	tracker, db, path := claimStore(t)
	actor, err := tracker.RegisterHumanAgent("equivalence-holder", "Equivalence Holder", "equivalence-holder@example.test")
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:         path,
		Harness:        "claude-code",
		Event:          "SessionStart",
		HostVersion:    "2.1.261",
		Input:          bytes.NewReader(raw),
		Clock:          claimClock{},
		Operations:     claimOperation("equivalence.first"),
		ActorClaim:     tasks.ActorClaim(actor.ID.String()),
		TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)

	first, last, journal, updated, event := readRebuiltState(t, db, "claude-code", rebuildClaudeSession)
	_, err = tasks.RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	firstAfter, lastAfter, journalAfter, updatedAfter, eventAfter := readRebuiltState(t, db, "claude-code", rebuildClaudeSession)
	require.Equal(t, first, firstAfter)
	require.Equal(t, last, lastAfter)
	require.Equal(t, journal, journalAfter)
	require.Equal(t, updated, updatedAfter)
	require.Equal(t, event, eventAfter)
}

func TestRebuildSessionStateRepairsASessionEndBinding(t *testing.T) {
	t.Parallel()
	tracker, db, path := claimStore(t)
	actor, err := tracker.RegisterHumanAgent("end-holder", "End Holder", "end-holder@example.test")
	require.NoError(t, err)
	start, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:         path,
		Harness:        "claude-code",
		Event:          "SessionStart",
		HostVersion:    "2.1.261",
		Input:          bytes.NewReader(start),
		Clock:          claimClock{},
		Operations:     claimOperation("repair.start"),
		ActorClaim:     tasks.ActorClaim(actor.ID.String()),
		TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO pasture_session_epoch_binding
			(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason)
		VALUES ('binding-end', 'claude-code', ?, 'file://epoch-rebuild', 'implementer', ?, NULL, NULL)`,
		rebuildClaudeSession, time.Unix(1700000001, 0).UnixNano())
	require.NoError(t, err)

	end, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_end_2_1_261.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:         path,
		Harness:        "claude-code",
		Event:          "SessionEnd",
		HostVersion:    "2.1.261",
		Input:          bytes.NewReader(end),
		Clock:          claimClock{},
		Operations:     claimOperation("repair.end"),
		ActorClaim:     tasks.ActorClaim(actor.ID.String()),
		TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)

	var reason string
	var unbound sql.NullInt64
	require.NoError(t, db.QueryRow(`
		SELECT unbound_reason, unbound_at FROM pasture_session_epoch_binding WHERE binding_id = 'binding-end'`).
		Scan(&reason, &unbound))
	require.Equal(t, "session_end", reason)
	require.True(t, unbound.Valid)

	// Simulate a tail that committed the receipt but failed to close the
	// binding. The rebuild is the repair path and must close it from the
	// latest SessionEnd occurrence.
	_, err = db.ExecContext(t.Context(), `
		UPDATE pasture_session_epoch_binding SET unbound_at = NULL, unbound_reason = NULL WHERE binding_id = 'binding-end'`)
	require.NoError(t, err)
	_, err = tasks.RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`
		SELECT unbound_reason, unbound_at FROM pasture_session_epoch_binding WHERE binding_id = 'binding-end'`).
		Scan(&reason, &unbound))
	require.Equal(t, "session_end", reason)
	require.True(t, unbound.Valid)
}

func TestEnsureSessionStateDetectsCommittedOccurrence(t *testing.T) {
	t.Parallel()
	tracker, db, path := claimStore(t)
	actor, err := tracker.RegisterHumanAgent("ensure-holder", "Ensure Holder", "ensure-holder@example.test")
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:         path,
		Harness:        "claude-code",
		Event:          "SessionStart",
		HostVersion:    "2.1.261",
		Input:          bytes.NewReader(raw),
		Clock:          claimClock{},
		Operations:     claimOperation("ensure.first"),
		ActorClaim:     tasks.ActorClaim(actor.ID.String()),
		TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)

	// Simulate a crash after the receipt committed and before the tail
	// advanced the marker.
	_, err = db.ExecContext(t.Context(), `UPDATE pasture_session_api_state SET completed_through_jid = 0 WHERE singleton_id = 0`)
	require.NoError(t, err)
	outcome, rebuilt, err := tasks.EnsureSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.True(t, rebuilt)
	require.Greater(t, outcome.CompletedThroughJournalID, int64(0))
}
