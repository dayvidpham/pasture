package tasks_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/sessionapi"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/pkg/protocol"
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

// seedInferenceOwnedEpoch builds the inference fixture shape through this
// black-box package: one task, one epoch projection row, and one
// owner-responsibility episode held by actor. Everything goes through public
// store APIs except the projection row, which the engine owns and no task
// command writes.
func seedInferenceOwnedEpoch(t *testing.T, tracker protocol.TaskTracker, db *sql.DB, actor provenance.ActorID, title, operation string) provenance.TaskID {
	t.Helper()
	epoch, err := tracker.Create("file://inference-test", title, "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `
		CREATE TABLE IF NOT EXISTS epoch_state_projection (
			epoch_id      TEXT PRIMARY KEY,
			current_phase TEXT NOT NULL,
			state_json    TEXT NOT NULL,
			updated_at    INTEGER NOT NULL
		)`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO epoch_state_projection (epoch_id, current_phase, state_json, updated_at)
		VALUES (?, 'p3-plan', '{}', 1)`, epoch.ID.String())
	require.NoError(t, err)

	var committer string
	var authorityJournalID int64
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT committer_actor_id, genesis_authority_journal_id
		FROM pasture_system_identity WHERE singleton_id = 0`).Scan(&committer, &authorityJournalID))
	authority := provenance.JournalID(authorityJournalID)
	_, err = tracker.Journal().Apply(provenance.OperationInput{
		OperationID:        provenance.OperationID(operation),
		ActorID:            actor,
		AuthorityJournalID: &authority,
		CommandDigest:      []byte(operation),
		Effects: []provenance.Effect{{
			Sort:         provenance.EffectAssignmentStart,
			ResultSlot:   "authority",
			TaskID:       epoch.ID,
			AssignmentID: provenance.AssignmentID(operation + "-assignment"),
			SlotID:       provenance.SlotOwnerResponsibility,
			Occupant:     actor,
		}},
	})
	require.NoError(t, err)
	return epoch.ID
}

func countInferredBindings(t *testing.T, db *sql.DB, session string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM pasture_session_inferred_binding
		WHERE harness = 'claude-code' AND session = ?`, session).Scan(&count))
	return count
}

// TestSessionEndClearsInferredBinding is the end-to-end cleanup contract: a
// rebuild derives an inferred binding from the claim's actor, the host's
// session-end event clears it in the same transaction that closes the explicit
// binding, and a later rebuild does not resurrect it.
func TestSessionEndClearsInferredBinding(t *testing.T) {
	t.Parallel()
	tracker, db, path := claimStore(t)
	actor, err := tracker.RegisterHumanAgent("infer-end-holder", "Inference End Holder", "infer-end-holder@example.test")
	require.NoError(t, err)
	epoch := seedInferenceOwnedEpoch(t, tracker, db, actor.ID, "Inference end epoch", "infer-end-owner")

	start, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:         path,
		Harness:        "claude-code",
		Event:          "SessionStart",
		HostVersion:    "2.1.261",
		Input:          bytes.NewReader(start),
		Clock:          claimClock{},
		Operations:     claimOperation("infer-end.start"),
		ActorClaim:     tasks.ActorClaim(actor.ID.String()),
		TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)

	_, err = tasks.RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.Equal(t, 1, countInferredBindings(t, db, rebuildClaudeSession), "the unique owned epoch is inferred")
	var inferredEpoch string
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT epoch_id FROM pasture_session_inferred_binding
		WHERE harness = 'claude-code' AND session = ?`, rebuildClaudeSession).Scan(&inferredEpoch))
	require.Equal(t, epoch.String(), inferredEpoch)

	// The read model is the consumer of this table, so the derivation is
	// asserted through it: with no explicit binding, the open inferred interval
	// is the primary view (epoch, binding, source, confidence, state) and the
	// interval history carries the same labels.
	service := &sessionapi.Service{
		DB:           db,
		Now:          func() time.Time { return claimClock{}.Now() },
		ActiveWindow: 15 * time.Minute,
	}
	detail, err := service.Get(t.Context(), "claude-code", rebuildClaudeSession)
	require.NoError(t, err)
	require.Len(t, detail.Session.Bindings, 1)
	require.Equal(t, sessionapi.BindingSourceInferred, detail.Session.Bindings[0].Source)
	require.Equal(t, sessionapi.ConfidenceObserved, detail.Session.Bindings[0].Confidence)
	require.NotNil(t, detail.Session.Bindings[0].Epoch)
	require.Equal(t, epoch.String(), detail.Session.Bindings[0].Epoch.ID)
	require.NotNil(t, detail.Session.Binding, "an inferred interval fills the primary binding when no explicit binding exists")
	require.Equal(t, sessionapi.BindingSourceInferred, detail.Session.Binding.Source)
	require.Equal(t, sessionapi.ConfidenceObserved, detail.Session.Binding.Confidence)
	require.Equal(t, sessionapi.StateActive, detail.Session.Activity.State)
	require.NotNil(t, detail.Session.Epoch)
	require.Equal(t, epoch.String(), detail.Session.Epoch.ID)
	var confidence string
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT confidence FROM pasture_session_inferred_binding
		WHERE harness = 'claude-code' AND session = ?`, rebuildClaudeSession).Scan(&confidence))
	require.Equal(t, "observed", confidence, "the claim-ownership rule is stored as observed confidence")

	end, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_end_2_1_261.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath:         path,
		Harness:        "claude-code",
		Event:          "SessionEnd",
		HostVersion:    "2.1.261",
		Input:          bytes.NewReader(end),
		Clock:          claimClock{},
		Operations:     claimOperation("infer-end.end"),
		ActorClaim:     tasks.ActorClaim(actor.ID.String()),
		TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)
	require.Zero(t, countInferredBindings(t, db, rebuildClaudeSession),
		"session end clears the inferred derivation in the same transaction that closes the explicit binding")

	_, err = tasks.RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	require.Zero(t, countInferredBindings(t, db, rebuildClaudeSession),
		"a later rebuild must not resurrect an inferred binding for an ended session")
	detail, err = service.Get(t.Context(), "claude-code", rebuildClaudeSession)
	require.NoError(t, err)
	require.Empty(t, detail.Session.Bindings, "an ended session keeps no inferred interval")
	require.Nil(t, detail.Session.Binding, "an ended session has no current binding")
	require.Nil(t, detail.Session.Epoch)
	require.Equal(t, sessionapi.StateUnknown, detail.Session.Activity.State,
		"a session with no explicit OR inferred binding is unknown")
}

// TestEndedSessionKeepsNoInferredRowAcrossAFailedInferenceWrite pins the
// interaction between the two failure paths: a session-end clear must survive a
// rollback of a failed inference write. A stale open row is seeded after the
// tail cleared the session, a second live session forces the inference INSERT
// to fail, and the rebuild must still commit with no open inferred row for the
// ended session.
func TestEndedSessionKeepsNoInferredRowAcrossAFailedInferenceWrite(t *testing.T) {
	t.Parallel()
	tracker, db, path := claimStore(t)
	actor, err := tracker.RegisterHumanAgent("infer-end-fault", "Inference End Fault", "infer-end-fault@example.test")
	require.NoError(t, err)
	seedInferenceOwnedEpoch(t, tracker, db, actor.ID, "Inference end fault epoch", "infer-end-fault-owner")
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO pasture_session_claim (harness, session, actor, claimed_at)
		VALUES ('claude-code', 'session-live-peer', ?, ?)`, actor.ID.String(), claimClock{}.Now().UnixNano())
	require.NoError(t, err)

	start, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath: path, Harness: "claude-code", Event: "SessionStart", HostVersion: "2.1.261",
		Input: bytes.NewReader(start), Clock: claimClock{}, Operations: claimOperation("infer-end-fault.start"),
		ActorClaim: tasks.ActorClaim(actor.ID.String()), TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)
	_, err = tasks.RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err)
	var inferred int
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM pasture_session_inferred_binding`).Scan(&inferred))
	require.Equal(t, 2, inferred, "both claimed sessions infer from the same unique owned epoch")

	// Break the inference cache so the next rebuild's INSERT fails while its
	// DELETE still works.
	_, err = db.ExecContext(t.Context(), `DROP TABLE pasture_session_inferred_binding`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `
		CREATE TABLE pasture_session_inferred_binding (
			binding_id TEXT PRIMARY KEY, harness TEXT NOT NULL, session TEXT NOT NULL,
			epoch_id TEXT NOT NULL, role TEXT NOT NULL, bound_at INTEGER NOT NULL,
			unbound_at INTEGER, unbound_reason TEXT, inferred_at INTEGER NOT NULL, reason TEXT NOT NULL)`)
	require.NoError(t, err)

	end, err := os.ReadFile(filepath.Join("..", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_end_2_1_261.json"))
	require.NoError(t, err)
	_, err = handlers.HookLifecycleNative(context.Background(), handlers.HookLifecycleInput{
		DBPath: path, Harness: "claude-code", Event: "SessionEnd", HostVersion: "2.1.261",
		Input: bytes.NewReader(end), Clock: claimClock{}, Operations: claimOperation("infer-end-fault.end"),
		ActorClaim: tasks.ActorClaim(actor.ID.String()), TimeoutProfile: timeouts.TestProfile(),
	})
	require.NoError(t, err)

	// A stale open row for the ended session, as if an earlier writer or a crash
	// left it behind. The rebuild must not leave it open.
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO pasture_session_inferred_binding
			(binding_id, harness, session, epoch_id, role, bound_at, unbound_at, unbound_reason, inferred_at, reason)
		VALUES ('stale-ended', 'claude-code', ?, 'file://stale', 'owner-responsibility', ?, NULL, NULL, ?, 'claim-ownership')`,
		rebuildClaudeSession, claimClock{}.Now().UnixNano(), claimClock{}.Now().UnixNano())
	require.NoError(t, err)

	_, err = tasks.RebuildSessionState(t.Context(), tracker)
	require.NoError(t, err, "a failed inference write must not fail the rebuild")

	var openEnded int
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM pasture_session_inferred_binding
		WHERE harness = 'claude-code' AND session = ? AND unbound_at IS NULL`,
		rebuildClaudeSession).Scan(&openEnded))
	require.Zero(t, openEnded, "an ended session can never retain an open inferred interval, even after a rolled-back inference write")
	var sessions int
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM pasture_session_state WHERE harness = 'claude-code' AND session = ?`,
		rebuildClaudeSession).Scan(&sessions))
	require.Equal(t, 1, sessions, "the activity cache still commits")
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
