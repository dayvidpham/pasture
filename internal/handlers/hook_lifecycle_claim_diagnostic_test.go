package handlers

// This file proves the session-claim mismatch diagnostic: a claim that is NOT
// stored because the session is already held by another actor reaches the
// command's standard error, names BOTH actors, and never fails the hook. The
// writer's typed outcome is covered from outside the package in
// internal/tasks/session_claim_test.go; here the subject is the handler's use
// of that outcome and the channel it writes to.

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// TestSessionClaimDiagnosticOnlyOnAMismatch is the pure contract of the
// renderer: an already-claimed session or a lost race whose stored actor
// differs names both actors; a written claim, a skipped claim, and the same
// actor render nothing.
func TestSessionClaimDiagnosticOnlyOnAMismatch(t *testing.T) {
	t.Parallel()
	bindings := []model.NativeBinding{{Kind: model.BindingSession, Value: "session-x"}}
	mismatched := []struct {
		name      string
		state     tasks.SessionClaimState
		attempted string
		stored    string
	}{
		{"already-claimed", tasks.SessionClaimAlreadyClaimed, "attempted-actor", "stored-actor"},
		{"lost-race", tasks.SessionClaimLostRace, "attempted-actor", "winner-actor"},
	}
	for _, testCase := range mismatched {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			line := sessionClaimMismatchDiagnostic(
				HookLifecycleInput{Harness: ir.HarnessClaudeCode, ActorClaim: tasks.ActorClaim(testCase.attempted)},
				bindings,
				tasks.SessionClaimOutcome{State: testCase.state, StoredActor: testCase.stored},
			)
			require.Contains(t, line, `"`+testCase.attempted+`"`, "the attempted actor must be named")
			require.Contains(t, line, `"`+testCase.stored+`"`, "the stored actor must be named")
			require.Contains(t, line, "session-x", "the session must be named")
			require.Contains(t, line, testCase.state.String(), "the outcome state must be readable")
		})
	}
	noMismatch := []struct {
		name   string
		state  tasks.SessionClaimState
		actor  string
		stored string
	}{
		{"wrote", tasks.SessionClaimWrote, "actor", "actor"},
		{"already-claimed-same-actor", tasks.SessionClaimAlreadyClaimed, "actor", "actor"},
		{"not-attempted", tasks.SessionClaimNotAttempted, "actor", ""},
	}
	for _, testCase := range noMismatch {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			line := sessionClaimMismatchDiagnostic(
				HookLifecycleInput{Harness: ir.HarnessClaudeCode, ActorClaim: tasks.ActorClaim(testCase.actor)},
				bindings,
				tasks.SessionClaimOutcome{State: testCase.state, StoredActor: testCase.stored},
			)
			require.Empty(t, line, "an ordinary claim must not produce a diagnostic")
		})
	}
}

// TestSessionClaimMismatchReachesStandardErrorWithoutFailingTheHook drives the
// committing handler over a real store: a session already claimed by one actor,
// then invoked by another. The hook must succeed with the same committed
// outcome, and the mismatch must reach the diagnostic sink naming both actors.
//
// RED when: the mismatch fails the hook, writes a second journal row, or is
// discarded.
func TestSessionClaimMismatchReachesStandardErrorWithoutFailingTheHook(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeSessionStartFixture)
	session := GateSessionIdentity(t, raw, "session_id")
	dbPath := gateStore(t)

	seed, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	_, err = tasks.RecordLifecycleSessionClaim(context.Background(), seed, ir.HarnessClaudeCode,
		sessionStartEventKind(ir.HarnessClaudeCode),
		[]model.NativeBinding{{Kind: model.BindingSession, Value: session}}, "stored-actor", gateClock{})
	require.NoError(t, err)
	require.NoError(t, seed.Close())

	input := gateInput(t, dbPath, "SessionStart", raw)
	input.ActorClaim = tasks.ActorClaim("attempted-actor")
	var diagnostics bytes.Buffer
	input.Diagnostics = &diagnostics

	_, err = hookLifecycle(t.Context(), input, tasks.OpenTaskTracker, tasks.NewGateReader)
	require.NoError(t, err, "a claim mismatch must never fail the hook")
	require.Contains(t, diagnostics.String(), `"attempted-actor"`)
	require.Contains(t, diagnostics.String(), `"stored-actor"`)
	require.Contains(t, diagnostics.String(), session)
	require.Contains(t, diagnostics.String(), tasks.SessionClaimAlreadyClaimed.String())
}

// TestSessionClaimLostRaceReachesStandardErrorNamingTheWinner forces the race
// deterministically with a BEFORE INSERT trigger that installs the winner
// between the handler's read and its INSERT ... ON CONFLICT DO NOTHING. The
// hook must succeed and the diagnostic must name the winner.
func TestSessionClaimLostRaceReachesStandardErrorNamingTheWinner(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeSessionStartFixture)
	session := GateSessionIdentity(t, raw, "session_id")
	dbPath := gateStore(t)

	db, err := dbconn.OpenSharedDBWithProfile(dbPath, timeouts.TestProfile())
	require.NoError(t, err)
	// The WHEN clause keeps the trigger's own insert (actor 'winner-actor') from
	// firing the trigger again, so the race is forced whether or not recursive
	// triggers are enabled.
	_, err = db.ExecContext(t.Context(), `CREATE TRIGGER claim_lost_race_winner BEFORE INSERT ON pasture_session_claim
		WHEN NEW.actor <> 'winner-actor'
		BEGIN
			INSERT INTO pasture_session_claim (harness, session, actor, claimed_at)
			VALUES (NEW.harness, NEW.session, 'winner-actor', 7);
		END`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	input := gateInput(t, dbPath, "SessionStart", raw)
	input.ActorClaim = tasks.ActorClaim("loser-actor")
	var diagnostics bytes.Buffer
	input.Diagnostics = &diagnostics

	_, err = hookLifecycle(t.Context(), input, tasks.OpenTaskTracker, tasks.NewGateReader)
	require.NoError(t, err, "a lost race must never fail the hook")
	require.Contains(t, diagnostics.String(), `"loser-actor"`)
	require.Contains(t, diagnostics.String(), `"winner-actor"`)
	require.Contains(t, diagnostics.String(), session)
	require.Contains(t, diagnostics.String(), tasks.SessionClaimLostRace.String())
}

// TestSessionClaimNoDiagnosticForWroteOrSameActor drives the ordinary claims
// end to end: the first invocation writes the claim, and the second finds it
// already claimed by the SAME actor. Neither may emit a diagnostic.
func TestSessionClaimNoDiagnosticForWroteOrSameActor(t *testing.T) {
	t.Parallel()
	raw := gateFixture(t, gateClaudeSessionStartFixture)
	dbPath := gateStore(t)
	operations := &gateOperations{}

	first := gateInput(t, dbPath, "SessionStart", raw)
	first.Operations = operations
	first.ActorClaim = tasks.ActorClaim("same-actor")
	var firstDiagnostics bytes.Buffer
	first.Diagnostics = &firstDiagnostics
	_, err := hookLifecycle(t.Context(), first, tasks.OpenTaskTracker, tasks.NewGateReader)
	require.NoError(t, err)
	require.Empty(t, firstDiagnostics.String(), "a written claim is not a mismatch")

	second := gateInput(t, dbPath, "SessionStart", raw)
	second.Operations = operations
	second.ActorClaim = tasks.ActorClaim("same-actor")
	var secondDiagnostics bytes.Buffer
	second.Diagnostics = &secondDiagnostics
	_, err = hookLifecycle(t.Context(), second, tasks.OpenTaskTracker, tasks.NewGateReader)
	require.NoError(t, err)
	require.Empty(t, secondDiagnostics.String(), "the same actor is not a mismatch")
}
