package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
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
	require.Equal(t, hostexit.Outcome{}, native, "an unsupported harness produces no usable Outcome")
}

// TestDispatchLifecycleForRoutesOpenCodeByObservedVersion pins the contract
// selection for the one harness that serves two versions: an observed host at
// or above the 2.0.20 floor takes the 2.0.20 row (manifest, activation,
// parser, binder and mapping), and anything else — an older host or a version
// that never parses — stays on the pinned 1.18.29 row. Every other harness
// ignores the version. The 2.0.20 activation withholds every row, so routing
// there never evaluates without proofs; it only selects the admission table.
func TestDispatchLifecycleForRoutesOpenCodeByObservedVersion(t *testing.T) {
	t.Parallel()

	v2, err := dispatchLifecycleFor(ir.HarnessOpenCode, "2.0.20")
	require.NoError(t, err)
	require.Equal(t, "2.0.20", v2.manifest.Version)
	entries, err := v2.activations()
	require.NoError(t, err)
	require.Len(t, entries, 17)

	v1, err := dispatchLifecycleFor(ir.HarnessOpenCode, "1.18.29")
	require.NoError(t, err)
	require.Equal(t, "1.18.29", v1.manifest.Version)

	for _, version := range []string{"", "  ", "host-local", "1.19.0", "1.22.0+runtime", "2.0.19"} {
		row, err := dispatchLifecycleFor(ir.HarnessOpenCode, version)
		require.NoError(t, err, version)
		require.Equal(t, "1.18.29", row.manifest.Version, "version %q must stay on the pinned row", version)
	}
	for _, version := range []string{"2.0.20", "2.1.0", "2.0.20+build"} {
		row, err := dispatchLifecycleFor(ir.HarnessOpenCode, version)
		require.NoError(t, err, version)
		require.Equal(t, "2.0.20", row.manifest.Version, "version %q must take the 2.0.20 row", version)
	}

	claude, err := dispatchLifecycleFor(ir.HarnessClaudeCode, "2.0.20")
	require.NoError(t, err)
	plain, err := dispatchLifecycle(ir.HarnessClaudeCode)
	require.NoError(t, err)
	require.Equal(t, plain.manifest.Version, claude.manifest.Version, "other harnesses ignore the version")

	_, err = dispatchLifecycleFor(ir.HarnessID("grok-build"), "2.0.20")
	require.Error(t, err, "an unsupported harness is refused at any version")
}
