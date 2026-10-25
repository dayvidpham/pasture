package tasks_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
)

func tailInput(bindings []model.NativeBinding, occurrence model.OccurrenceID) tasks.SessionTailInput {
	return tasks.SessionTailInput{
		Harness:      ir.HarnessClaudeCode,
		Contract:     registration.ClaudeCode__2_1_261().Contract,
		EventKind:    registration.EventSessionStart,
		Bindings:     bindings,
		OccurrenceID: occurrence,
		RecordedAt:   time.Unix(1700000000, 0).UTC(),
		ActorClaim:   "tail-actor",
		Clock:        claimClock{},
	}
}

func TestRecordLifecycleSessionTailWritesClaimStateAndRevision(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	bindings := []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "tail-session"}}
	var diagnostics bytes.Buffer
	in := tailInput(bindings, model.OccurrenceID(42))
	in.Diagnostics = &diagnostics

	out, err := tasks.RecordLifecycleSessionTail(t.Context(), tracker, in)
	require.NoError(t, err)
	require.Equal(t, tasks.SessionClaimWrote, out.Claim.State)
	require.Equal(t, "tail-actor", out.Claim.StoredActor)
	require.True(t, out.State.Written)
	require.False(t, out.State.Dirty)
	require.Empty(t, diagnostics.String())

	var actor string
	require.NoError(t, db.QueryRow(
		`SELECT actor FROM pasture_session_claim WHERE harness = ? AND session = ?`,
		string(ir.HarnessClaudeCode), "tail-session").Scan(&actor))
	require.Equal(t, "tail-actor", actor)

	var firstSeen, lastSeen, journalID int64
	var lastEvent string
	require.NoError(t, db.QueryRow(
		`SELECT first_seen, last_seen, last_event, last_journal_id FROM pasture_session_state WHERE harness = ? AND session = ?`,
		string(ir.HarnessClaudeCode), "tail-session").Scan(&firstSeen, &lastSeen, &lastEvent, &journalID))
	require.Equal(t, int64(1700000000_000000000), firstSeen)
	require.Equal(t, int64(1700000000_000000000), lastSeen)
	require.Equal(t, "SessionStart", lastEvent)
	require.Equal(t, int64(42), journalID)

	var revision, completed int64
	require.NoError(t, db.QueryRow(
		`SELECT revision, completed_through_jid FROM pasture_session_api_state WHERE singleton_id = 0`).Scan(&revision, &completed))
	require.Equal(t, int64(1), revision)
	require.Equal(t, int64(42), completed)
}

type failingSessionStateWriter struct{}

func (failingSessionStateWriter) UpsertSessionState(context.Context, *sql.Tx, tasks.SessionTailInput, string) error {
	return errors.New("injected state failure")
}

func TestRecordLifecycleSessionTailPreservesClaimWhenStateFails(t *testing.T) {
	t.Parallel()
	tracker, db, _ := claimStore(t)
	bindings := []model.NativeBinding{{Kind: model.BindingSession, NativeName: "session_id", Value: "dirty-session"}}
	var diagnostics bytes.Buffer
	in := tailInput(bindings, model.OccurrenceID(43))
	in.StateWriter = failingSessionStateWriter{}
	in.Diagnostics = &diagnostics

	out, err := tasks.RecordLifecycleSessionTail(t.Context(), tracker, in)
	require.NoError(t, err, "a state-only failure must not fail the host continuation")
	require.Equal(t, tasks.SessionClaimWrote, out.Claim.State)
	require.False(t, out.State.Written)
	require.True(t, out.State.Dirty)
	require.Contains(t, diagnostics.String(), "session state cache write failed")

	var actor string
	require.NoError(t, db.QueryRow(
		`SELECT actor FROM pasture_session_claim WHERE harness = ? AND session = ?`,
		string(ir.HarnessClaudeCode), "dirty-session").Scan(&actor))
	require.Equal(t, "tail-actor", actor, "the claim must survive a state-write failure")

	var revision, completed, dirty int64
	require.NoError(t, db.QueryRow(
		`SELECT revision, completed_through_jid, dirty FROM pasture_session_api_state WHERE singleton_id = 0`).Scan(&revision, &completed, &dirty))
	require.Equal(t, int64(0), revision, "a failed state write must not bump the revision")
	require.Equal(t, int64(0), completed, "a failed state write must not advance the watermark")
	require.Equal(t, int64(1), dirty, "the cache must be marked dirty for the next rebuild")
}
