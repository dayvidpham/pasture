package effects_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryEffectClassifiesToAKnownRuntimeClass proves each Effect variant
// answers Classify with a valid RuntimeClass — the exhaustive requirement that a
// runtime contract has an explicit plan for every modeled effect.
func TestEveryEffectClassifiesToAKnownRuntimeClass(t *testing.T) {
	t.Parallel()

	repo := mustRepository(t, "/repo")

	process, err := effects.NewRunProcess(effects.RunProcessSpec{
		Executable: mustExecutable(t, "git"),
		Directory:  mustPathDir(t, "repo"),
	})
	require.NoError(t, err)

	fsEffect, err := effects.NewWriteReplaceFile(mustOwnedPath(t, "a.md"), []byte("x"))
	require.NoError(t, err)

	gitEvidence, err := effects.NewGitReadEvidence(repo, effects.GitStatusEvidence)
	require.NoError(t, err)
	gitCommit, err := effects.NewGitCommit(repo, effects.CommitPolicyAgentCommit)
	require.NoError(t, err)

	guarded := mustGuardedInput(t, effects.ExpectAbsentRemote())

	variants := []effects.Effect{process, fsEffect, gitEvidence, gitCommit, guarded}
	for _, effect := range variants {
		assert.True(t, effect.Classify().IsValid(), "%T classifies to a known runtime class", effect)
	}

	// The specific classes are load-bearing.
	assert.Equal(t, effects.RuntimeClassNative, process.Classify())
	assert.Equal(t, effects.RuntimeClassNative, fsEffect.Classify())
	assert.Equal(t, effects.RuntimeClassNative, gitEvidence.Classify())
	assert.Equal(t, effects.RuntimeClassSemanticInstruction, gitCommit.Classify())
	assert.Equal(t, effects.RuntimeClassParentMediated, guarded.Classify())

	assert.False(t, effects.RuntimeClass("").IsValid())
	assert.True(t, effects.RuntimeClassUnsupported.IsValid())
}
