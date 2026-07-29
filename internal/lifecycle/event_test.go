package lifecycle_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Digest
// ---------------------------------------------------------------------------

func TestDigestIsStableAndDistinguishesEmptyFromAbsent(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"any":"bytes"}`)
	require.Equal(t, lifecycle.NewDigest(payload), lifecycle.NewDigest(payload),
		"identical bytes must digest identically or replay detection cannot work")
	require.NotEqual(t, lifecycle.NewDigest(payload), lifecycle.NewDigest(append(payload, ' ')),
		"a one-byte difference must change the digest or two occurrences could collapse")

	assert.True(t, lifecycle.Digest{}.IsZero(), "the zero digest is the one nobody computed")
	assert.False(t, lifecycle.NewDigest(nil).IsZero(),
		"a digest over empty input is a real digest; 'the host sent nothing' and 'nobody computed one' must stay distinguishable")
	assert.Len(t, lifecycle.NewDigest(payload).String(), 64, "the digest renders as lowercase hex")
	assert.Equal(t, strings.ToLower(lifecycle.NewDigest(payload).String()), lifecycle.NewDigest(payload).String())
}

// ---------------------------------------------------------------------------
// Identity construction
// ---------------------------------------------------------------------------

func TestNewIdentityRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		kind        runtime.NativeIdentityKind
		nativeName  string
		value       string
		mustMention string
	}{
		{"unrecognised kind", runtime.NativeIdentityKind(0), sessionField, "s-1", "0"},
		{"kind above the closed set", runtime.NativeIdentityKind(200), sessionField, "s-1", "200"},
		{"empty field name", runtime.IdentitySession, "", "s-1", "empty or padded"},
		{"padded field name", runtime.IdentitySession, " " + sessionField, "s-1", "empty or padded"},
		{"empty value", runtime.IdentitySession, sessionField, "", "empty value"},
		{"padded value", runtime.IdentitySession, sessionField, " s-1 ", "whitespace"},
		{"control character", runtime.IdentitySession, sessionField, "s-\x00-1", "control character"},
		{"invalid utf-8", runtime.IdentitySession, sessionField, string([]byte{0xff, 0xfe}), "UTF-8"},
		{"oversized value", runtime.IdentitySession, sessionField, strings.Repeat("x", 513), "513"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := lifecycle.NewIdentity(testCase.kind, testCase.nativeName, testCase.value)
			requireRejected(t, err, testCase.mustMention)
		})
	}
}

func TestNewIdentityAcceptsValueAtTheBound(t *testing.T) {
	t.Parallel()

	atBound := strings.Repeat("x", 512)
	built, err := lifecycle.NewIdentity(runtime.IdentitySession, sessionField, atBound)
	require.NoError(t, err, "the bound is inclusive; only values OVER it are smuggled payload")
	assert.Equal(t, atBound, built.Value(), "the value must survive byte-exact")
	assert.True(t, built.IsValid())
	assert.False(t, lifecycle.Identity{}.IsValid(), "the zero identity was never checked")
}

// ---------------------------------------------------------------------------
// BindEvent — the single admission point
// ---------------------------------------------------------------------------

func TestBindEventRejectsZeroContract(t *testing.T) {
	t.Parallel()

	_, err := bindZeroContract()
	requireRejected(t, err, "empty runtime contract")
}

func TestBindEventRejectsEventOutsideThePinnedCatalogue(t *testing.T) {
	t.Parallel()

	_, err := bindEventOutsideTheCatalogue()
	requireRejected(t, err, "does not describe the requested native event")

	assert.False(t, lifecycle.EventBinding{}.IsValid(), "the zero binding came from no contract")
}

// ---------------------------------------------------------------------------
// NewEvent — the verifier
// ---------------------------------------------------------------------------

func TestNewEventRejectsUnboundBinding(t *testing.T) {
	t.Parallel()

	_, err := lifecycle.EventBinding{}.NewEvent(lifecycle.NewDigest([]byte("payload")), nil)
	requireRejected(t, err, "did not come from the event binder")
}

func TestNewEventRejectsZeroDigest(t *testing.T) {
	t.Parallel()

	binding := observationBinding(t)
	_, err := binding.NewEvent(lifecycle.Digest{}, []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	requireRejected(t, err, "No payload digest was computed")
}

// TestNewEventRejectsIdentityWhoseKindDoesNotMatchTheDeclaredPair is the
// regression this verifier exists for.
//
// The binding declares BOTH a session field and a request field. Supplying the
// session field under the request kind therefore passes any check on the field
// NAME alone — the name is declared. Only a check on the (kind, name) PAIR
// rejects it. Accepting it would put a session identifier into the waist
// labelled as a request identifier, and because the waist correlates by KIND,
// this occurrence would then be matched against unrelated facts — inside IR the
// verifier had already blessed.
func TestNewEventRejectsIdentityWhoseKindDoesNotMatchTheDeclaredPair(t *testing.T) {
	t.Parallel()

	binding := gateWithRequestBinding(t)
	declared := binding.DeclaredIdentities()
	require.Contains(t, fieldNames(declared), sessionField)
	require.Contains(t, fieldNames(declared), requestField,
		"this test is only meaningful when a request-kinded field is genuinely declared")

	_, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		// The session FIELD, tagged with the request KIND.
		identity(t, runtime.IdentityRequest, sessionField, "s-1"),
		identity(t, runtime.IdentityRequest, requestField, "r-1"),
	})
	requireRejected(t, err, sessionField, "request", "session")
}

func TestNewEventRejectsUndeclaredCorrelationField(t *testing.T) {
	t.Parallel()

	binding := observationBinding(t)
	_, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentitySession, undeclaredField, "smuggled"),
	})
	requireRejected(t, err, undeclaredField, "not declared by the pinned contract")
}

func TestNewEventRejectsMissingRequiredCorrelationField(t *testing.T) {
	t.Parallel()

	binding := gateBinding(t)
	_, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	requireRejected(t, err, toolCallField, "required")
}

func TestNewEventRejectsDuplicateCorrelationField(t *testing.T) {
	t.Parallel()

	binding := observationBinding(t)
	_, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentitySession, sessionField, "s-2"),
	})
	requireRejected(t, err, sessionField, "more than once")
}

func TestNewEventRejectsIdentityThatSkippedItsConstructor(t *testing.T) {
	t.Parallel()

	binding := observationBinding(t)
	_, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{{}})
	requireRejected(t, err, "not built by the identity constructor")
}

func TestNewEventAcceptsAbsentOptionalCorrelationFields(t *testing.T) {
	t.Parallel()

	binding := optionalIdentityBinding(t)
	for _, field := range binding.DeclaredIdentities() {
		require.Falsef(t, field.Required(),
			"this test needs an event whose declared fields are all optional; %q is required", field.NativeName())
	}

	event, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), nil)
	require.NoError(t, err, "omitting an optional field is legal, not a verification failure")
	assert.Empty(t, event.Semantics().Identities())
	assert.True(t, event.IsValid())
}

func TestNewEventDerivesMeaningFromTheContractNotTheCaller(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"whatever":"the host sent"}`)
	digest := lifecycle.NewDigest(payload)

	binding := gateBinding(t)
	event, err := binding.NewEvent(digest, []lifecycle.Identity{
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	require.NoError(t, err)

	// Semantics come out of the pinned table. The caller supplied neither.
	assert.Equal(t, runtime.SemanticGateConsultation, event.Semantics().Semantic())
	assert.Equal(t, runtime.Blocking, event.Semantics().Blocking())

	// The native spelling is the contract's, not a caller's: NewEvent takes no
	// name argument, so there is nothing for a frontend to get wrong.
	assert.Equal(t, gateBindingNativeName, event.Origin().NativeEventName())
	assert.Equal(t, digest, event.Origin().PayloadDigest())
	assert.True(t, event.Origin().Contract().IsValid())
	assert.Equal(t, event.Origin().Contract().Harness(), event.Origin().Harness(),
		"the host family is derived from the contract, never stored separately")
}

// ---------------------------------------------------------------------------
// Semantics — ordering, copying, and the dropped native names
// ---------------------------------------------------------------------------

func TestSemanticIdentitiesAreSortedAndStripOfNativeFieldNames(t *testing.T) {
	t.Parallel()

	binding := gateBinding(t)
	// Supplied deliberately in the reverse of the sorted order.
	event, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	require.NoError(t, err)

	assert.Equal(t, []lifecycle.SemanticIdentity{
		{Kind: runtime.IdentitySession, Value: "s-1"},
		{Kind: runtime.IdentityToolCall, Value: "call-1"},
	}, event.Semantics().Identities(),
		"identities sort by (Kind, Value) at construction, so two frontends that extracted them in different orders agree")
}

func TestSemanticIdentitiesAreADefensiveCopy(t *testing.T) {
	t.Parallel()

	binding := gateBinding(t)
	event, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
	})
	require.NoError(t, err)

	first := event.Semantics().Identities()
	first[0] = lifecycle.SemanticIdentity{Kind: runtime.IdentityAgent, Value: "forged"}

	assert.Equal(t, lifecycle.SemanticIdentity{Kind: runtime.IdentitySession, Value: "s-1"},
		event.Semantics().Identities()[0],
		"an accessor that shared its slice would let one consumer rewrite another's view of the IR")
}

func TestCallerMutatingItsOwnArgumentCannotAlterTheIR(t *testing.T) {
	t.Parallel()

	binding := gateBinding(t)
	supplied := []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
	}
	event, err := binding.NewEvent(lifecycle.NewDigest([]byte("payload")), supplied)
	require.NoError(t, err)

	supplied[0] = identity(t, runtime.IdentitySession, sessionField, "s-forged")

	assert.Equal(t, "s-1", event.Semantics().Identities()[0].Value)
}

// ---------------------------------------------------------------------------
// EquivalentTo — the coarse cross-host shape relation
// ---------------------------------------------------------------------------

func TestEquivalentToMatchesShapeAcrossHostsAndIgnoresValues(t *testing.T) {
	t.Parallel()

	local := gateBinding(t)
	foreign := foreignGateBinding(t)

	localEvent, err := local.NewEvent(lifecycle.NewDigest([]byte("local payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
	})
	require.NoError(t, err)

	// The foreign host spells its correlation fields differently; the waist
	// does not know or care, because the field names never reach it.
	foreignEvent, err := foreign.NewEvent(lifecycle.NewDigest([]byte("foreign payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, declaredField(t, foreign, runtime.IdentitySession), "totally-different-session"),
		identity(t, runtime.IdentityToolCall, declaredField(t, foreign, runtime.IdentityToolCall), "totally-different-call"),
	})
	require.NoError(t, err)

	require.NotEqual(t, localEvent.Origin().Harness(), foreignEvent.Origin().Harness(),
		"this test is only meaningful across two different hosts")
	assert.True(t, localEvent.Semantics().EquivalentTo(foreignEvent.Semantics()),
		"one logical occurrence reported by two hosts has one shape; correlation VALUES differ between hosts and must not be compared")
	assert.True(t, foreignEvent.Semantics().EquivalentTo(localEvent.Semantics()), "the relation is symmetric")

	assert.NotEqual(t, localEvent.Semantics().CanonicalKey(), foreignEvent.Semantics().CanonicalKey(),
		"the canonical key DOES encode values, so equivalence must not imply an equal key")
}

func TestEquivalentToRejectsDifferentShapesAndZeroValues(t *testing.T) {
	t.Parallel()

	gate, err := gateBinding(t).NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
		identity(t, runtime.IdentityToolCall, toolCallField, "call-1"),
	})
	require.NoError(t, err)

	observation, err := observationBinding(t).NewEvent(lifecycle.NewDigest([]byte("payload")), []lifecycle.Identity{
		identity(t, runtime.IdentitySession, sessionField, "s-1"),
	})
	require.NoError(t, err)

	assert.False(t, gate.Semantics().EquivalentTo(observation.Semantics()),
		"a blocking gate and a non-blocking observation are not the same occurrence")

	var zero lifecycle.Semantics
	assert.False(t, zero.EquivalentTo(zero), "a zero Semantics is equivalent to nothing, including itself")
	assert.False(t, gate.Semantics().EquivalentTo(zero))
	assert.False(t, zero.EquivalentTo(gate.Semantics()))
	assert.False(t, zero.IsValid())
}

// ---------------------------------------------------------------------------
// The narrowness invariant, checked structurally
// ---------------------------------------------------------------------------

// TestWaistTypesHaveNoFieldForPastureAuthority pins the field sets of every
// waist type.
//
// "An actor, assignment, journal identifier, revision or evidence reference has
// no field to occupy" is the property that stops a native occurrence from
// manufacturing Pasture authority. Prose cannot enforce it and a behavioural
// test cannot observe it — a field that exists but is never populated today is
// exactly the field somebody populates next year. So the field set itself is
// the assertion: widening any of these types fails here and forces the change
// to be argued for rather than merged.
func TestWaistTypesHaveNoFieldForPastureAuthority(t *testing.T) {
	t.Parallel()

	cases := []struct {
		typ   reflect.Type
		want  []string
		notes string
	}{
		{
			typ:   reflect.TypeOf(lifecycle.Event{}),
			want:  []string{"semantics", "origin", "constructed"},
			notes: "an Event is a waist value plus its coordinates, and nothing else",
		},
		{
			typ:   reflect.TypeOf(lifecycle.Semantics{}),
			want:  []string{"semantic", "blocking", "identities", "constructed"},
			notes: "only meaning, blocking mode and correlation are host-independent; everything else is target detail",
		},
		{
			typ:   reflect.TypeOf(lifecycle.Origin{}),
			want:  []string{"contract", "nativeName", "digest", "behaviour", "constructed"},
			notes: "coordinates plus the bind-time target table, which is reachable only through the backend view",
		},
		{
			typ:   reflect.TypeOf(lifecycle.SemanticIdentity{}),
			want:  []string{"Kind", "Value"},
			notes: "the native field name is dropped on the way into the waist",
		},
		{
			typ:   reflect.TypeOf(lifecycle.Identity{}),
			want:  []string{"kind", "nativeName", "value", "constructed"},
			notes: "the constructor INPUT type still carries the native name, so the pair can be verified",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.typ.Name(), func(t *testing.T) {
			t.Parallel()
			got := make([]string, 0, testCase.typ.NumField())
			for index := 0; index < testCase.typ.NumField(); index++ {
				got = append(got, testCase.typ.Field(index).Name)
			}
			assert.Equal(t, testCase.want, got, testCase.notes)
		})
	}
}

// TestSemanticsCarriesNoTargetDetailTypes proves the split is real: none of the
// enums that describe how to speak back to one specific host may appear on the
// waist type.
func TestSemanticsCarriesNoTargetDetailTypes(t *testing.T) {
	t.Parallel()

	targetDetail := []reflect.Type{
		reflect.TypeOf(runtime.HookSurface(0)),
		reflect.TypeOf(runtime.MutationMode(0)),
		reflect.TypeOf(runtime.HandlerOrder(0)),
		reflect.TypeOf(runtime.ReconciliationMode(0)),
		reflect.TypeOf(runtime.FailureMode(0)),
		reflect.TypeOf(runtime.StopLoopPolicy(0)),
	}

	semantics := reflect.TypeOf(lifecycle.Semantics{})
	for index := 0; index < semantics.NumField(); index++ {
		field := semantics.Field(index)
		for _, forbidden := range targetDetail {
			assert.NotEqualf(t, forbidden, field.Type,
				"field %q carries %s, which describes one specific host; reach it through the backend view instead",
				field.Name, forbidden.Name())
		}
	}
}

func fieldNames(declared []runtime.NativeIdentityField) []string {
	names := make([]string, 0, len(declared))
	for _, field := range declared {
		names = append(names, field.NativeName())
	}
	return names
}

func TestDeclaredIdentitiesIsADefensiveCopy(t *testing.T) {
	t.Parallel()

	binding := gateBinding(t)
	first := binding.DeclaredIdentities()
	require.NotEmpty(t, first)
	first[0] = runtime.NativeIdentityField{}

	assert.NotEqual(t, runtime.NativeIdentityField{}, binding.DeclaredIdentities()[0],
		"a frontend must not be able to blank the declarations it is being checked against")
	assert.NotContains(t, strings.Join(fieldNames(binding.DeclaredIdentities()), ","), undeclaredField)
}
