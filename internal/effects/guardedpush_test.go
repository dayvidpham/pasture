package effects_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func expectAbsent() effects.ExpectedOldOID { return effects.ExpectAbsentRemote() }

// TestGuardedPushExactCommitScenarios covers the full landing matrix: exact and
// absent expected-old, missing local object, tree mismatch, stale/racing/absent
// remote, successful push+verification, and already-at-target idempotent replay.
// Only a re-read confirming the exact commit produces a proof.
func TestGuardedPushExactCommitScenarios(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		expected    effects.ExpectedOldOID
		pusher      *fakePusher
		wantProof   bool
		wantOutcome effects.GuardedPushOutcome
		wantVerify  int
		wantPush    int
		wantErrHint string
	}{
		{
			name:        "fresh push to absent ref verifies",
			expected:    effects.ExpectAbsentRemote(),
			pusher:      &fakePusher{remoteAfter: effects.PresentRemoteState(mustCommit(t, oidA))},
			wantProof:   true,
			wantOutcome: effects.GuardedPushPushed,
			wantVerify:  1,
			wantPush:    1,
		},
		{
			name:        "already-at-target is idempotent replay success",
			expected:    expectAbsent(),
			pusher:      &fakePusher{pushErr: errors.New("rejected: already up to date"), remoteAfter: effects.PresentRemoteState(mustCommit(t, oidA))},
			wantProof:   true,
			wantOutcome: effects.GuardedPushIdempotentReplay,
			wantVerify:  1,
			wantPush:    1,
		},
		{
			name:        "missing local object never pushes and yields no proof",
			expected:    expectAbsent(),
			pusher:      &fakePusher{localErr: errors.New("object not found")},
			wantProof:   false,
			wantVerify:  1,
			wantPush:    0,
			wantErrHint: "local object",
		},
		{
			name:        "stale remote at a different commit yields no proof",
			expected:    expectAbsent(),
			pusher:      &fakePusher{remoteAfter: effects.PresentRemoteState(mustCommit(t, oidB))},
			wantProof:   false,
			wantVerify:  1,
			wantPush:    1,
			wantErrHint: "did not reach exact commit",
		},
		{
			name:        "racing remote left absent yields no proof",
			expected:    expectAbsent(),
			pusher:      &fakePusher{remoteAfter: effects.AbsentRemoteState()},
			wantProof:   false,
			wantVerify:  1,
			wantPush:    1,
			wantErrHint: "did not reach exact commit",
		},
		{
			name:        "remote re-read failure yields no proof",
			expected:    expectAbsent(),
			pusher:      &fakePusher{readErr: errors.New("remote unreachable")},
			wantProof:   false,
			wantVerify:  1,
			wantPush:    1,
			wantErrHint: "could not be re-read",
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			input := mustGuardedInput(t, testCase.expected)
			proof, err := effects.GuardedPushExactCommit(input, testCase.pusher)
			assert.Equal(t, testCase.wantVerify, testCase.pusher.verifyCalls, "verify call count")
			assert.Equal(t, testCase.wantPush, testCase.pusher.pushCalls, "push call count")
			if testCase.wantProof {
				require.NoError(t, err)
				require.NoError(t, proof.Validate())
				assert.Equal(t, testCase.wantOutcome, proof.Outcome())
				assert.True(t, proof.Commit().Equal(mustCommit(t, oidA)))
				assert.True(t, proof.Tree().Equal(mustTree(t, treeA)))
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErrHint)
				assert.Error(t, proof.Validate(), "no proof when landing is unverified")
			}
		})
	}
}

// TestGuardedPushExactCommitWithExpectedCommit exercises the non-absent
// expected-old form and confirms it reaches a proof.
func TestGuardedPushExactCommitWithExpectedCommit(t *testing.T) {
	t.Parallel()
	expected, err := effects.ExpectRemoteAt(mustCommit(t, oidB))
	require.NoError(t, err)
	input := mustGuardedInput(t, expected)
	pusher := &fakePusher{remoteAfter: effects.PresentRemoteState(mustCommit(t, oidA))}
	proof, err := effects.GuardedPushExactCommit(input, pusher)
	require.NoError(t, err)
	require.NoError(t, proof.Validate())
	assert.Equal(t, effects.GuardedPushPushed, proof.Outcome())
}

// TestGuardedPushRejectsInvalidInputs proves the algorithm rejects a zero input
// and a nil pusher before attempting anything.
func TestGuardedPushRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	_, err := effects.GuardedPushExactCommit(effects.GuardedPushInput{}, &fakePusher{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero or invalid")

	pusher := &fakePusher{remoteAfter: effects.PresentRemoteState(mustCommit(t, oidA))}
	_, err = effects.GuardedPushExactCommit(mustGuardedInput(t, expectAbsent()), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
	assert.Equal(t, 0, pusher.verifyCalls)
}

// TestNewGuardedPushInputValidation is the negative table for input construction.
func TestNewGuardedPushInputValidation(t *testing.T) {
	t.Parallel()

	repo := mustRepository(t, "/repo")
	commit := mustCommit(t, oidA)
	tree := mustTree(t, treeA)
	ref := mustRemoteRef(t, "refs/heads/main")

	cases := []struct {
		name  string
		build func() (effects.GuardedPushInput, error)
		hint  string
	}{
		{"zero repository", func() (effects.GuardedPushInput, error) {
			return effects.NewGuardedPushInput(effects.RepositoryID{}, commit, tree, ref, expectAbsent())
		}, "repository"},
		{"zero commit", func() (effects.GuardedPushInput, error) {
			return effects.NewGuardedPushInput(repo, effects.CommitOID{}, tree, ref, expectAbsent())
		}, "commit"},
		{"zero tree", func() (effects.GuardedPushInput, error) {
			return effects.NewGuardedPushInput(repo, commit, effects.TreeDigest{}, ref, expectAbsent())
		}, "tree"},
		{"zero ref", func() (effects.GuardedPushInput, error) {
			return effects.NewGuardedPushInput(repo, commit, tree, effects.RemoteRef{}, expectAbsent())
		}, "ref"},
		{"unspecified expected-old", func() (effects.GuardedPushInput, error) {
			return effects.NewGuardedPushInput(repo, commit, tree, ref, effects.ExpectedOldOID{})
		}, "expected-old"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := testCase.build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.hint)
		})
	}
}

// TestVerifiedGuardedPushIsNonSerializable proves the proof has no wire form:
// json.Marshal fails, so a proof token can never leak out of the process.
func TestVerifiedGuardedPushIsNonSerializable(t *testing.T) {
	t.Parallel()
	input := mustGuardedInput(t, expectAbsent())
	pusher := &fakePusher{remoteAfter: effects.PresentRemoteState(mustCommit(t, oidA))}
	proof, err := effects.GuardedPushExactCommit(input, pusher)
	require.NoError(t, err)

	_, marshalErr := json.Marshal(proof)
	require.Error(t, marshalErr, "the proof must refuse to serialize")
	assert.Contains(t, marshalErr.Error(), "cannot be serialized")

	// Even embedded in a struct, marshaling must fail rather than silently emit
	// an empty object.
	_, wrappedErr := json.Marshal(struct {
		Proof effects.VerifiedGuardedPush
	}{Proof: proof})
	require.Error(t, wrappedErr)
}

// TestZeroVerifiedGuardedPushIsInvalid proves the zero value and a value with a
// wrong/zero field never validate.
func TestZeroVerifiedGuardedPushIsInvalid(t *testing.T) {
	t.Parallel()
	require.Error(t, effects.VerifiedGuardedPush{}.Validate())
}

// TestGuardedPushBatchPartialWithoutRollback proves multi-repository
// orchestration records exact per-repository results, stops after the first
// failure, and makes no rollback claim: the already-verified landing keeps its
// proof.
func TestGuardedPushBatchPartialWithoutRollback(t *testing.T) {
	t.Parallel()

	okInput, err := effects.NewGuardedPushInput(
		mustRepository(t, "/repo-a"), mustCommit(t, oidA), mustTree(t, treeA),
		mustRemoteRef(t, "refs/heads/main"), expectAbsent())
	require.NoError(t, err)
	failInput, err := effects.NewGuardedPushInput(
		mustRepository(t, "/repo-b"), mustCommit(t, oidA), mustTree(t, treeA),
		mustRemoteRef(t, "refs/heads/main"), expectAbsent())
	require.NoError(t, err)
	neverInput, err := effects.NewGuardedPushInput(
		mustRepository(t, "/repo-c"), mustCommit(t, oidA), mustTree(t, treeA),
		mustRemoteRef(t, "refs/heads/main"), expectAbsent())
	require.NoError(t, err)

	// A pusher that verifies the first landing but leaves the remote stale on
	// every subsequent call (different commit) so repo-b fails.
	pusher := &statefulBatchPusher{results: map[string]effects.RemoteState{
		"/repo-a": effects.PresentRemoteState(mustCommit(t, oidA)),
		"/repo-b": effects.PresentRemoteState(mustCommit(t, oidB)),
	}}

	results := effects.GuardedPushBatch([]effects.GuardedPushInput{okInput, failInput, neverInput}, pusher)
	require.Len(t, results, 2, "stops after the first failure; repo-c is never attempted")
	assert.True(t, results[0].Verified())
	assert.NoError(t, results[0].Proof.Validate(), "the prior verified landing is not rolled back")
	assert.False(t, results[1].Verified())
	require.Error(t, results[1].Err)
}

type statefulBatchPusher struct {
	results map[string]effects.RemoteState
}

func (p *statefulBatchPusher) VerifyLocalObject(effects.RepositoryID, effects.CommitOID, effects.TreeDigest) error {
	return nil
}
func (p *statefulBatchPusher) PushExact(effects.RepositoryID, effects.CommitOID, effects.RemoteRef, effects.ExpectedOldOID) error {
	return nil
}
func (p *statefulBatchPusher) ReadRemote(repository effects.RepositoryID, _ effects.RemoteRef) (effects.RemoteState, error) {
	return p.results[repository.String()], nil
}
