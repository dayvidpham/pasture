package effects_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGitObjectIDValidation proves commit and tree ids require full lowercase
// hex.
func TestGitObjectIDValidation(t *testing.T) {
	t.Parallel()

	_, err := effects.NewCommitOID(oidA)
	require.NoError(t, err)
	sha256Len := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err = effects.NewCommitOID(sha256Len)
	require.NoError(t, err)

	bad := []string{"", "abc", "ABCD1111111111111111111111111111111111ff", "zz11111111111111111111111111111111111111"}
	for _, value := range bad {
		_, err := effects.NewCommitOID(value)
		require.Error(t, err, value)
		_, err = effects.NewTreeDigest(value)
		require.Error(t, err, value)
	}
	assert.False(t, effects.CommitOID{}.IsValid())
}

// TestRemoteRefValidation proves refs reject whitespace, control, and globs.
func TestRemoteRefValidation(t *testing.T) {
	t.Parallel()

	ref, err := effects.NewRemoteRef("refs/heads/main")
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/main", ref.String())

	bad := []string{"", "refs/heads/ma in", "refs/heads/*", "refs/heads/ma\tin"}
	for _, value := range bad {
		_, err := effects.NewRemoteRef(value)
		require.Error(t, err, value)
	}
}

// TestExpectedOldOIDForms proves the absent and exact-commit forms and that the
// zero value is invalid.
func TestExpectedOldOIDForms(t *testing.T) {
	t.Parallel()

	absent := effects.ExpectAbsentRemote()
	assert.True(t, absent.IsValid())
	assert.True(t, absent.Absent())
	_, ok := absent.Commit()
	assert.False(t, ok)

	at, err := effects.ExpectRemoteAt(mustCommit(t, oidA))
	require.NoError(t, err)
	assert.False(t, at.Absent())
	commit, ok := at.Commit()
	require.True(t, ok)
	assert.True(t, commit.Equal(mustCommit(t, oidA)))

	_, err = effects.ExpectRemoteAt(effects.CommitOID{})
	require.Error(t, err)
	assert.False(t, effects.ExpectedOldOID{}.IsValid())
}

// TestGitEffectsDistinguishEvidenceFromStateChange proves read-evidence effects
// are read-only while stage/commit/fetch/rebase change state, and that commit
// requires an explicit policy operand.
func TestGitEffectsDistinguishEvidenceFromStateChange(t *testing.T) {
	t.Parallel()

	repo := mustRepository(t, "/repo")

	for _, kind := range []effects.GitEffectKind{effects.GitStatusEvidence, effects.GitCommitEvidence, effects.GitDiffEvidence} {
		evidence, err := effects.NewGitReadEvidence(repo, kind)
		require.NoError(t, err, kind)
		assert.False(t, evidence.StateChanging(), kind)
		assert.Equal(t, effects.RuntimeClassNative, evidence.Classify())
	}

	stage, err := effects.NewGitStage(repo, mustOwnedPath(t, "a.md"))
	require.NoError(t, err)
	assert.True(t, stage.StateChanging())
	paths, ok := stage.Paths()
	require.True(t, ok)
	require.Len(t, paths, 1)

	commit, err := effects.NewGitCommit(repo, effects.CommitPolicyAgentCommit)
	require.NoError(t, err)
	assert.True(t, commit.StateChanging())
	policy, ok := commit.Policy()
	require.True(t, ok)
	assert.Equal(t, effects.CommitPolicyAgentCommit, policy)
	assert.Equal(t, effects.RuntimeClassSemanticInstruction, commit.Classify())

	fetch, err := effects.NewGitFetch(repo, mustRemoteRef(t, "refs/heads/main"))
	require.NoError(t, err)
	assert.True(t, fetch.StateChanging())
	rebase, err := effects.NewGitRebase(repo, mustRemoteRef(t, "refs/heads/main"))
	require.NoError(t, err)
	assert.True(t, rebase.StateChanging())
}

// TestGitCommitPolicyIsExplicit proves the commit policy is a required operand,
// not a renderer default: an invalid policy is rejected.
func TestGitCommitPolicyIsExplicit(t *testing.T) {
	t.Parallel()

	repo := mustRepository(t, "/repo")
	_, err := effects.NewGitCommit(repo, effects.CommitPolicy("guessed"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent-commit")

	assert.True(t, effects.CommitPolicyAgentCommit.IsValid())
	assert.True(t, effects.CommitPolicyPlainCommit.IsValid())
	assert.False(t, effects.CommitPolicy("").IsValid())
}

// TestGitEffectNegatives covers zero repository and empty stage.
func TestGitEffectNegatives(t *testing.T) {
	t.Parallel()

	_, err := effects.NewGitReadEvidence(effects.RepositoryID{}, effects.GitStatusEvidence)
	require.Error(t, err)
	_, err = effects.NewGitReadEvidence(mustRepository(t, "/repo"), effects.GitStage)
	require.Error(t, err)
	_, err = effects.NewGitStage(mustRepository(t, "/repo"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no paths")
	assert.False(t, effects.GitEffect{}.IsValid())
}
