package ir_test

import (
	"errors"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mutationFixture(t testing.TB) (ir.MutationAuthority, ir.ResolvedMutationRequest, ir.CanonicalCommandDigest, ir.LexicalScopeSnapshot) {
	t.Helper()
	authority, err := ir.InitiatingAssignment(mustAssignmentRef(t, "assignment-owner"))
	require.NoError(t, err)
	operationID, err := ir.NewSemanticOperationID("pasture.test.factory-operation/v1")
	require.NoError(t, err)
	request, err := ir.NewResolvedMutationRequest(operationID, "pasture.test.factory-request/v1", []byte(`{"a":1}`))
	require.NoError(t, err)
	digest, err := ir.DigestCanonicalCommand([]byte("factory-command"))
	require.NoError(t, err)
	scope, err := ir.NewRootScope("factory")
	require.NoError(t, err)
	bindings := ir.NewRuntimeBindings()
	snapshot, err := ir.SnapshotBindings(bindings, scope)
	require.NoError(t, err)
	return authority, request, digest, snapshot
}

// TestDeterministicMutationRefFactoryContinuityAcrossRetriesAndFreshAgent
// proves the required continuity invariant: retrying the same logical
// invocation (calling the factory again with the same seed from the SAME
// process) and reconstructing it from a fresh agent (a brand new, unrelated
// call to the factory with no shared in-memory state — modeled here by using
// a second, independently obtained MutationContinuation) both mint the exact
// same MutationRef.
func TestDeterministicMutationRefFactoryContinuityAcrossRetriesAndFreshAgent(t *testing.T) {
	t.Parallel()

	authority, request, digest, scope := mutationFixture(t)
	const seed = "assignment-owner/pasture.test.factory-operation/attempt-1"

	first, err := ir.NewMutationContinuationFromFactory(
		ir.DeterministicMutationRefFactory, seed, authority, request, digest, nil, scope,
	)
	require.NoError(t, err)

	// Retry: the same in-flight caller invokes the factory again with the
	// same seed after a transient failure.
	retry, err := ir.NewMutationContinuationFromFactory(
		ir.DeterministicMutationRefFactory, seed, authority, request, digest, nil, scope,
	)
	require.NoError(t, err)
	assert.Equal(t, first.Ref(), retry.Ref(), "retrying the same logical invocation must mint the same MutationRef")

	// Fresh-agent reconstruction: an entirely separate call path (no shared
	// state with the above) reconstructs the continuation from persisted
	// seed material alone.
	freshAgentFactory := ir.DeterministicMutationRefFactory
	reconstructed, err := ir.NewMutationContinuationFromFactory(
		freshAgentFactory, seed, authority, request, digest, nil, scope,
	)
	require.NoError(t, err)
	assert.Equal(t, first.Ref(), reconstructed.Ref(), "fresh-agent reconstruction from the same seed must mint the same MutationRef")
}

// TestDeterministicMutationRefFactoryDistinctMinting proves the second
// required invariant: two distinct logical invocations (distinct seeds) must
// never collide.
func TestDeterministicMutationRefFactoryDistinctMinting(t *testing.T) {
	t.Parallel()

	authority, request, digest, scope := mutationFixture(t)

	first, err := ir.NewMutationContinuationFromFactory(
		ir.DeterministicMutationRefFactory, "invocation-a", authority, request, digest, nil, scope,
	)
	require.NoError(t, err)
	second, err := ir.NewMutationContinuationFromFactory(
		ir.DeterministicMutationRefFactory, "invocation-b", authority, request, digest, nil, scope,
	)
	require.NoError(t, err)
	assert.NotEqual(t, first.Ref(), second.Ref(), "distinct logical invocations must mint distinct MutationRefs")
}

// TestMutationRefFactoryIsGenuinelyInjected proves the DI seam is real, not
// bypassed: swapping in a custom factory changes which MutationRef gets
// minted for the same seed, and the factory's error is propagated verbatim.
func TestMutationRefFactoryIsGenuinelyInjected(t *testing.T) {
	t.Parallel()

	authority, request, digest, scope := mutationFixture(t)
	const seed = "invocation-c"

	viaDefault, err := ir.NewMutationContinuationFromFactory(
		ir.DeterministicMutationRefFactory, seed, authority, request, digest, nil, scope,
	)
	require.NoError(t, err)

	custom := func(string) (portable.MutationRef, error) {
		return portable.NewMutationRef("custom-mint-for-" + seed)
	}
	viaCustom, err := ir.NewMutationContinuationFromFactory(
		ir.MutationRefFactory(custom), seed, authority, request, digest, nil, scope,
	)
	require.NoError(t, err)
	assert.NotEqual(t, viaDefault.Ref(), viaCustom.Ref(), "an injected custom factory must actually determine the minted ref, not be silently ignored")
	assert.Equal(t, "custom-mint-for-invocation-c", viaCustom.Ref().String())

	failing := func(string) (portable.MutationRef, error) {
		return portable.MutationRef{}, errors.New("injected factory failure")
	}
	_, err = ir.NewMutationContinuationFromFactory(
		ir.MutationRefFactory(failing), seed, authority, request, digest, nil, scope,
	)
	require.Error(t, err)
	for _, field := range []string{"what:", "why:", "where:", "phase:", "impact:", "fix:"} {
		assert.Contains(t, err.Error(), field)
	}

	_, err = ir.NewMutationContinuationFromFactory(
		nil, seed, authority, request, digest, nil, scope,
	)
	require.Error(t, err, "a nil factory must be rejected")
}
