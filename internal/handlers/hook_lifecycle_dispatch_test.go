package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
)

// TestDispatchLifecycleRejectsUnsupportedHarness is the relocated home of the
// unknown-harness negative coverage formerly asserted in nativeresponse_test.go
// (TestEncodeRejectsUnsupportedHarness). Deleting the nativeresponse.Encode
// harness switch — the per-host encoders are total over their input and take no
// harness argument — moved the rejection decision to the registry lookup, so
// the negative case is asserted here, at the surface that now owns it:
//
//   - dispatchLifecycle returns the unchanged actionable unsupported-harness
//     error naming the harness, and the zero registry row; and
//   - HookLifecycleNative returns that same rejection with NIL native bytes, so
//     nothing is written to stdout.
//
// Coverage is provably not dropped: the same rejected input yields the same
// error text, asserted at the surface that now decides it.
//
// FAILS until the L3 HookLifecycleNative body lands.
func TestDispatchLifecycleRejectsUnsupportedHarness(t *testing.T) {
	t.Parallel()

	dispatch, err := dispatchLifecycle(ir.HarnessID("grok-build"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "grok-build",
		"the unsupported-harness error must name the offending harness")
	require.Empty(t, dispatch.name, "an unsupported harness yields the zero registry row")
	require.Nil(t, dispatch.activations, "an unsupported harness yields no activations constructor")
	require.Nil(t, dispatch.encode, "an unsupported harness yields no native encoder")

	native, err := HookLifecycleNative(context.Background(), HookLifecycleInput{Harness: ir.HarnessID("grok-build")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "grok-build",
		"HookLifecycleNative must surface the unsupported-harness error naming the harness")
	require.Nil(t, native, "an unsupported harness produces no native stdout bytes")
}
