package tasks

// session_claim_reader_test.go covers the read half of the claim seam: the one
// helper both halves share, and the one SELECT the gate reader issues.
//
// The write half is covered from outside the package in session_claim_test.go.
// These subjects are white-box because the read is the gate's own SQL, and a
// subject that reached it any other way would not be testing it.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/timeouts"
)

// TestLifecycleSessionExtractsTheSessionIdentity holds the shared helper. The
// write and the read must never disagree about which binding is the session, so
// this is the single place that decision is asserted.
//
// RED when: a second session binding is accepted, or a non-session binding is
// read as the session.
func TestLifecycleSessionExtractsTheSessionIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		bindings []model.NativeBinding
		want     string
		wantErr  bool
	}{
		{name: "none", bindings: nil, want: ""},
		{name: "no-session-binding", bindings: []model.NativeBinding{{Kind: model.BindingTurn, NativeName: "turn", Value: "t"}}, want: ""},
		{
			name:     "one",
			bindings: []model.NativeBinding{{Kind: model.BindingTurn, NativeName: "turn", Value: "t"}, {Kind: model.BindingSession, NativeName: "session_id", Value: "s"}},
			want:     "s",
		},
		{
			name: "two",
			bindings: []model.NativeBinding{
				{Kind: model.BindingSession, NativeName: "session_id", Value: "s"},
				{Kind: model.BindingSession, NativeName: "session_id_alt", Value: "other"},
			},
			wantErr: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			session, err := lifecycleSession(testCase.bindings)
			if testCase.wantErr {
				require.ErrorContains(t, err, "more than one session identity")
				return
			}
			require.NoError(t, err)
			require.Equal(t, testCase.want, session)
		})
	}
}

// TestReadLifecycleSessionClaimAnswersOneRow is the read's whole contract: one
// row answers its actor, no row answers unbound, and a repeated read answers the
// same thing because the read consumes nothing.
//
// RED when: a missing row becomes an error, or a second read answers differently.
func TestReadLifecycleSessionClaimAnswersOneRow(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := feasibilityActor(t, store, "claim-reader")
	gateClaim(t, store, actor, "read-session")

	claimed, present, err := store.readLifecycleSessionClaim(t.Context(), gateHarness, "read-session")
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, actor.String(), claimed)

	_, present, err = store.readLifecycleSessionClaim(t.Context(), gateHarness, "absent-session")
	require.NoError(t, err, "an absent claim is unbound, not a fault")
	require.False(t, present)

	again, present, err := store.readLifecycleSessionClaim(t.Context(), gateHarness, "read-session")
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, claimed, again)
}

// TestReadLifecycleSessionClaimAnswersTheBoundSessionOnly proves the read is
// keyed by session. A claim written for one session must not answer another.
func TestReadLifecycleSessionClaimAnswersTheBoundSessionOnly(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	actor := feasibilityActor(t, store, "claim-keyed")
	gateClaim(t, store, actor, "claimed-session")
	_, present, err := store.readLifecycleSessionClaim(t.Context(), gateHarness, "unclaimed-session")
	require.NoError(t, err)
	require.False(t, present, "a claim is keyed by its session and answers no other")
}

// TestReadLifecycleSessionClaimFailsWhileTheStoreIsBusy proves the read faults
// rather than reporting an unbound session when it cannot see the file. An
// unbound answer here would silently open the gate for a bound session.
func TestReadLifecycleSessionClaimFailsWhileTheStoreIsBusy(t *testing.T) {
	store, _ := openGateStore(t, WithTimeoutProfile(timeouts.DeadlineTestProfile()))
	gateClaim(t, store, feasibilityActor(t, store, "claim-busy"), "busy-claim")
	held, err := store.auditDB.Conn(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	_, present, err := store.readLifecycleSessionClaim(t.Context(), gateHarness, "busy-claim")
	require.Error(t, err)
	require.False(t, present)
}

// TestReadLifecycleSessionClaimReleasesItsConnection is the lease proof. The
// store pools one connection and Provenance borrows the same pool, so a read
// that kept its connection would block the very call that follows it.
func TestReadLifecycleSessionClaimReleasesItsConnection(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	require.Equal(t, 1, store.auditDB.Stats().MaxOpenConnections, "the Pasture handle must pool exactly one connection")
	gateClaim(t, store, feasibilityActor(t, store, "claim-release"), "release-session")
	bounded, cancel := context.WithTimeout(t.Context(), timeouts.TestProfile().WorkflowResult())
	defer cancel()
	_, present, err := store.readLifecycleSessionClaim(bounded, gateHarness, "release-session")
	require.NoError(t, err)
	require.True(t, present)
	// The claim read is complete, so the one pooled connection is free for the
	// ownership read that follows it in the same snapshot.
	require.Zero(t, store.auditDB.Stats().InUse, "a completed claim read must hold no pooled connection")
}

// TestReadLifecycleSessionClaimHonoursACancelledContext proves a context that
// already ended surfaces as the context cause, so the caller can tell a caller
// cancellation from a store fault.
func TestReadLifecycleSessionClaimHonoursACancelledContext(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	gateClaim(t, store, feasibilityActor(t, store, "claim-cancelled"), "cancelled-session")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, present, err := store.readLifecycleSessionClaim(cancelled, gateHarness, "cancelled-session")
	require.Error(t, err)
	require.False(t, present)
	require.True(t, errors.Is(err, context.Canceled), "the context cause must stay in the chain, got %v", err)
}

// TestReadLifecycleSessionClaimStampsNothing proves the read is a read. The claim
// table must carry the same rows before and after.
func TestReadLifecycleSessionClaimStampsNothing(t *testing.T) {
	t.Parallel()
	store, _ := openGateStore(t)
	gateClaim(t, store, feasibilityActor(t, store, "claim-read-only"), "read-only-session")
	before := gateClaimRows(t, store)
	_, _, err := store.readLifecycleSessionClaim(t.Context(), gateHarness, "read-only-session")
	require.NoError(t, err)
	require.Equal(t, before, gateClaimRows(t, store), "a claim read must not change the claim table")
}

func gateClaimRows(t *testing.T, store *trackerImpl) string {
	t.Helper()
	var rows string
	err := store.auditDB.QueryRow(
		`SELECT COALESCE(GROUP_CONCAT(harness || '|' || session || '|' || actor || '|' || claimed_at, ';' ORDER BY harness, session), '') FROM pasture_session_claim`,
	).Scan(&rows)
	require.NoError(t, err)
	return rows
}
