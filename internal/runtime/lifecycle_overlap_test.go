package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
)

// TestOpenCodeFailurePolicyOverlapIsVersionBlind pins the lookup order the
// version-blind failure-policy query depends on: the three coordinates both
// OpenCode contracts share declare identical failure policies, so resolving
// 1.18.29 first is unobservable there and 2.0.20-only coordinates resolve to
// their declared rows instead of the observe-only fallback. A profile edit
// that moves one shared coordinate breaks this test rather than silently
// version-skewing the fault policy.
func TestOpenCodeFailurePolicyOverlapIsVersionBlind(t *testing.T) {
	t.Parallel()
	shared := []string{"session.created", "tool.execute.before", "tool.execute.after"}
	for _, name := range shared {
		v1, ok := lookupIn(t, pastureruntime.OpenCode1_18_29Lifecycle(), pastureruntime.OpenCodeLifecycleEvents(), name)
		require.True(t, ok, name)
		v2, ok := lookupIn(t, pastureruntime.OpenCode2_0_20Lifecycle(), pastureruntime.OpenCode2LifecycleEvents(), name)
		require.True(t, ok, name)
		require.Equal(t, v1, v2, "shared coordinate %q must declare one failure policy", name)
	}
	resolved, ok := pastureruntime.LookupLifecycleFailure(ir.HarnessOpenCode, "session.created")
	require.True(t, ok)
	require.Equal(t, pastureruntime.SemanticObservation, resolved.Semantic)
	for _, name := range []string{"session.prompt", "permission.evaluate", "shell.create.before"} {
		resolved, ok := pastureruntime.LookupLifecycleFailure(ir.HarnessOpenCode, name)
		require.True(t, ok, "2.0.20-only coordinate %q must resolve to its declared row", name)
		require.True(t, resolved.Declared(), name)
	}
	_, ok = pastureruntime.LookupLifecycleFailure(ir.HarnessOpenCode, "no.such.coordinate")
	require.False(t, ok, "an undeclared coordinate still falls through to the caller")
}

func lookupIn[E comparable](t *testing.T, contract pastureruntime.LifecycleContract[E], events []E, name string) (pastureruntime.LifecycleFailurePolicy, bool) {
	t.Helper()
	for _, event := range events {
		mapping, err := contract.Mapping(event)
		require.NoError(t, err)
		if mapping.NativeName() == name {
			return pastureruntime.LifecycleFailurePolicy{
				Mode:         mapping.Failure(),
				DeclaredMode: mapping.DeclaredFailure(),
				Evidence:     mapping.Evidence(),
				Semantic:     mapping.Semantic(),
				Blocking:     mapping.Blocking(),
				Response:     mapping.Response(),
			}, true
		}
	}
	return pastureruntime.LifecycleFailurePolicy{}, false
}
