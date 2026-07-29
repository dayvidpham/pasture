package lifecycle_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackendViewExposesTheBindTimeTargetTable proves the backend side is real:
// the target detail deliberately absent from Semantics is not lost, it is
// retained from bind time and reachable through exactly one symbol.
//
// It is retained rather than re-derived because there is no lookup to
// re-derive it with — internal/runtime offers no native-name lookup by design,
// so "ask the contract again later" is not an option that exists.
func TestBackendViewExposesTheBindTimeTargetTable(t *testing.T) {
	t.Parallel()

	binding := gateBinding(t)
	event, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
	})
	require.NoError(t, err)

	behaviour := lifecycle.BackendView(event).TargetBehaviour()

	assert.Equal(t, string(event.Origin().NativeEventName()), behaviour.NativeName())
	assert.True(t, behaviour.Surface().IsValid(), "the wire surface is target detail and lives here")
	assert.True(t, behaviour.Mutation().IsValid(), "what a handler may mutate is target detail")
	assert.True(t, behaviour.Order().IsValid(), "handler scheduling is target detail")
	assert.True(t, behaviour.Reconciliation().IsValid(), "reconciliation of concurrent handlers is target detail")
	assert.True(t, behaviour.Failure().IsValid(), "failure behaviour is target detail")
	assert.True(t, behaviour.StopLoop().IsValid(), "stop-loop policy is target detail")

	assert.Equal(t, event.Semantics().Semantic(), behaviour.Semantic(),
		"the two views describe one occurrence and must not disagree")
	assert.Equal(t, event.Semantics().Blocking(), behaviour.Blocking())
}

func TestBackendViewOfAZeroEventIsInvalid(t *testing.T) {
	t.Parallel()

	view := lifecycle.BackendView(lifecycle.Event{})
	assert.False(t, view.IsValid(), "a view of nothing must announce itself rather than return a plausible zero table")
	assert.Empty(t, view.TargetBehaviour().NativeName())
	assert.False(t, lifecycle.Backend{}.IsValid())
}

// TestTargetBehaviourIsADefensiveCopyOfItsIdentities guards the one slice the
// pinned table exposes.
func TestTargetBehaviourIsADefensiveCopyOfItsIdentities(t *testing.T) {
	t.Parallel()

	binding := gateBinding(t)
	event, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
	})
	require.NoError(t, err)

	behaviour := lifecycle.BackendView(event).TargetBehaviour()
	first := behaviour.Identities()
	require.NotEmpty(t, first)
	first[0] = runtime.NativeIdentityField{}

	assert.NotEqual(t, runtime.NativeIdentityField{},
		lifecycle.BackendView(event).TargetBehaviour().Identities()[0],
		"the pinned table is immutable; a consumer must not be able to blank it for the next one")
}

// TestBackendViewIsTheOnlyRouteToTargetDetail states the boundary the lowering
// pass is checked against. Semantics and Origin expose meaning, blocking mode,
// correlation and coordinates — and nothing that says how to speak back to one
// specific host. The lowering pass may therefore read them freely; if it needs
// this symbol, it has grown a host-specific branch.
func TestBackendViewIsTheOnlyRouteToTargetDetail(t *testing.T) {
	t.Parallel()

	binding := observationBinding(t)
	event, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	require.NoError(t, err)

	origin := event.Origin()
	// The full method set a target-agnostic consumer can reach without the
	// backend view. Adding a target-detail accessor to either type reopens the
	// side door this boundary exists to close.
	assert.Equal(t, 6, methodCount(origin), "Origin: Contract, Harness, NativeEventName, PayloadDigest, ReplayKey, IsValid")
	assert.Equal(t, 6, methodCount(event.Semantics()), "Semantics: Semantic, Blocking, Identities, EquivalentTo, CanonicalKey, IsValid")
	assert.Equal(t, 3, methodCount(event), "Event: Semantics, Origin, IsValid")
}
