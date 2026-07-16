package ir_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/pkg/protocol/portable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResultValueRefMustNotResolveAnUncapturedInput is the BLOCKER fix test:
// a ResultValueRef built from a declared-but-not-yet-captured ResultSlot must
// never resolve to a value that was only ever bound as a context input under
// the same key/scope/type — that would let a mutation's declared result
// appear "already available" before the mutation runs.
func TestResultValueRefMustNotResolveAnUncapturedInput(t *testing.T) {
	t.Parallel()

	scope, err := ir.NewRootScope("assignment")
	require.NoError(t, err)
	key, err := ir.NewBindingKey[portable.TaskRef]("pasture.binding.shared-name")
	require.NoError(t, err)

	// Bind "pasture.binding.shared-name" as a context input.
	bindings, err := ir.BindRuntimeValue(ir.NewRuntimeBindings(), key, scope, mustTaskRef(t, "input-value"))
	require.NoError(t, err)

	// Declare a result slot under the exact same key/scope/type, and build a
	// ResultValueRef for it, without ever calling CaptureRuntimeResult.
	codec, err := ir.NewJSONCodec[portable.TaskRef]("pasture.test.shared-result/v1", nil)
	require.NoError(t, err)
	slot, err := ir.NewResultSlot(key, scope, codec)
	require.NoError(t, err)
	resultRef, err := ir.ResultValueRef(slot)
	require.NoError(t, err)

	_, err = ir.ResolveRuntimeValue(bindings, resultRef, scope)
	require.Error(t, err, "an uncaptured result must not resolve to the context input sharing its key")
	for _, field := range []string{"what:", "why:", "where:", "phase:", "impact:", "fix:"} {
		assert.Contains(t, err.Error(), field)
	}
}

// TestInputValueRefMustNotResolveACapturedResult is the converse negative
// case: once a result has been captured under a key, an InputValueRef for
// that same key/scope/type must not resolve it either.
func TestInputValueRefMustNotResolveACapturedResult(t *testing.T) {
	t.Parallel()

	scope, err := ir.NewRootScope("assignment")
	require.NoError(t, err)
	key, err := ir.NewBindingKey[portable.TaskRef]("pasture.binding.shared-name-2")
	require.NoError(t, err)
	codec, err := ir.NewJSONCodec[portable.TaskRef]("pasture.test.shared-result-2/v1", nil)
	require.NoError(t, err)
	slot, err := ir.NewResultSlot(key, scope, codec)
	require.NoError(t, err)

	bindings, err := ir.CaptureRuntimeResult(ir.NewRuntimeBindings(), slot, []byte(`"captured-value"`))
	require.NoError(t, err)

	inputRef, err := ir.InputValueRef(key, scope)
	require.NoError(t, err)
	_, err = ir.ResolveRuntimeValue(bindings, inputRef, scope)
	require.Error(t, err, "a captured result must not resolve as a context input")
}

// TestResultValueRefResolvesOnlyAfterCapture is the positive control: once
// CaptureRuntimeResult actually runs, the exact same ResultValueRef resolves
// correctly — proving the source check rejects only genuine cross-source
// aliasing, not the legitimate capture-then-resolve flow.
func TestResultValueRefResolvesOnlyAfterCapture(t *testing.T) {
	t.Parallel()

	scope, err := ir.NewRootScope("assignment")
	require.NoError(t, err)
	key, err := ir.NewBindingKey[portable.TaskRef]("pasture.binding.shared-name-3")
	require.NoError(t, err)
	codec, err := ir.NewJSONCodec[portable.TaskRef]("pasture.test.shared-result-3/v1", nil)
	require.NoError(t, err)
	slot, err := ir.NewResultSlot(key, scope, codec)
	require.NoError(t, err)
	resultRef, err := ir.ResultValueRef(slot)
	require.NoError(t, err)

	_, err = ir.ResolveRuntimeValue(ir.NewRuntimeBindings(), resultRef, scope)
	require.Error(t, err, "resolving before capture must fail because the binding is simply missing")

	bindings, err := ir.CaptureRuntimeResult(ir.NewRuntimeBindings(), slot, []byte(`"captured-after"`))
	require.NoError(t, err)
	resolved, err := ir.ResolveRuntimeValue(bindings, resultRef, scope)
	require.NoError(t, err)
	assert.Equal(t, "captured-after", resolved.String())
}
