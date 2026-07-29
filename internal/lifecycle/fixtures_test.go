package lifecycle_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/require"
)

// The waist has no host-specific knowledge, but its tests need a real pinned
// contract to verify against — that is the whole point of the verifier, and a
// contract the test wrote itself would make every check a tautology.
//
// Every reference to a concrete pinned profile in this package is confined to
// this file, and each binding below is named for the SHAPE the tests need from
// it rather than for the host it happens to come from. A test therefore states
// "I need an event that declares an optional identity", not "I need this host".
// If a pinned profile is re-pinned to a newer host version, only this file
// moves.

// Native correlation field spellings the pinned profiles declare. They exist as
// constants so a test that supplies a deliberately wrong field or kind is
// obviously doing so.
const (
	// sessionField is declared with the session kind on every binding below.
	sessionField = "session_id"
	// requestField is declared with the request kind on gateWithRequestBinding.
	requestField = "request_id"
	// toolCallField is declared with the tool-call kind on gateBinding.
	toolCallField = "tool_use_id"
	// undeclaredField is declared by no binding used in these tests.
	undeclaredField = "definitely_not_a_declared_correlation_field"
)

// gateBindingNativeName is the pinned contract's own spelling of gateBinding's
// event. A test asserts the constructor echoes this rather than any caller
// input, so the expected value has to be stated somewhere; it is stated here
// with every other host-specific fact.
const gateBindingNativeName lifecycle.NativeEventName = "PreToolUse"

// observationBinding is a non-blocking observation declaring exactly one
// required session identity.
func observationBinding(t *testing.T) lifecycle.EventBinding {
	t.Helper()
	binding, err := lifecycle.BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeEventSessionStart)
	require.NoError(t, err)
	return binding
}

// gateBinding is a blocking gate consultation declaring two required
// identities: one session, one tool-call.
func gateBinding(t *testing.T) lifecycle.EventBinding {
	t.Helper()
	binding, err := lifecycle.BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeEventPreToolUse)
	require.NoError(t, err)
	return binding
}

// gateWithRequestBinding is a blocking gate consultation declaring a required
// session identity AND a required request identity.
//
// The second identity is what makes the mis-kinded-identity test meaningful:
// because a request-kinded field is genuinely declared here, supplying the
// session FIELD under the request KIND is rejected only by a check on the pair.
// A check on the name alone would accept it.
func gateWithRequestBinding(t *testing.T) lifecycle.EventBinding {
	t.Helper()
	binding, err := lifecycle.BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeEventPermissionRequest)
	require.NoError(t, err)
	return binding
}

// optionalIdentityBinding is a blocking gate consultation whose declared
// identities are all OPTIONAL, so omitting them is legal.
func optionalIdentityBinding(t *testing.T) lifecycle.EventBinding {
	t.Helper()
	binding, err := lifecycle.BindEvent(runtime.OpenCode1_17_18Lifecycle(), runtime.OpenCodeEventShellEnv)
	require.NoError(t, err)
	return binding
}

// foreignGateBinding is a blocking gate consultation from a DIFFERENT pinned
// host than gateBinding, declaring the same two identity kinds (session,
// tool-call) under that host's own field spellings.
//
// It is the second half of the equivalence tests: two hosts, two contracts, two
// native spellings, one shape.
func foreignGateBinding(t *testing.T) lifecycle.EventBinding {
	t.Helper()
	binding, err := lifecycle.BindEvent(runtime.OpenCode1_17_18Lifecycle(), runtime.OpenCodeEventToolExecuteBefore)
	require.NoError(t, err)
	return binding
}

// bindZeroContract attempts a binding against a contract nobody pinned.
func bindZeroContract() (lifecycle.EventBinding, error) {
	return lifecycle.BindEvent(runtime.LifecycleContract[runtime.ClaudeLifecycleEvent]{}, runtime.ClaudeEventSessionStart)
}

// bindEventOutsideTheCatalogue attempts a binding for a typed event value that
// no pinned profile carries.
func bindEventOutsideTheCatalogue() (lifecycle.EventBinding, error) {
	return lifecycle.BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), runtime.ClaudeLifecycleEvent(0))
}

// declaredField returns the native field a binding declares for one kind, so a
// test can supply correlation for a host whose spellings it does not hardcode.
func declaredField(t *testing.T, binding lifecycle.EventBinding, kind runtime.NativeIdentityKind) string {
	t.Helper()
	for _, field := range binding.DeclaredIdentities() {
		if field.Kind() == kind {
			return field.NativeName()
		}
	}
	require.Failf(t, "binding declares no identity of the requested kind",
		"kind %s is not declared; declared fields are %v", kind, binding.DeclaredIdentities())
	return ""
}

// identity builds one correlation value, failing the test if it is malformed.
func identity(t *testing.T, kind runtime.NativeIdentityKind, nativeName, value string) lifecycle.Identity {
	t.Helper()
	built, err := lifecycle.NewIdentity(kind, nativeName, value)
	require.NoError(t, err)
	return built
}

// methodCount returns how many EXPORTED methods a value's type has. It is how
// the tests pin a capability boundary: a boundary that is only documented is a
// boundary the next accessor quietly crosses.
func methodCount(value any) int {
	return reflect.TypeOf(value).NumMethod()
}

// requireRejected asserts the verifier refused the input and that the refusal
// says enough for an operator to act on: it must name the offending thing.
func requireRejected(t *testing.T, err error, mustMention ...string) {
	t.Helper()
	require.Error(t, err)
	for _, fragment := range mustMention {
		require.Truef(t, strings.Contains(err.Error(), fragment),
			"the rejection must mention %q so it is actionable; got:\n%s", fragment, err.Error())
	}
}
