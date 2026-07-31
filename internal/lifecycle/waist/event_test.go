package waist

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestNewEventDerivesEverySemanticArm(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	tests := []struct {
		name       string
		event      runtime.ClaudeLifecycleEvent
		identities []identitySpec
		want       runtime.EventSemantic
	}{
		{
			name:  "observation",
			event: runtime.ClaudeEventSessionStart,
			identities: []identitySpec{
				{runtime.IdentitySession, "session_id", "session-1"},
			},
			want: runtime.SemanticObservation,
		},
		{
			name:  "gate consultation",
			event: runtime.ClaudeEventPreToolUse,
			identities: []identitySpec{
				{runtime.IdentitySession, "session_id", "session-1"},
				{runtime.IdentityToolCall, "tool_use_id", "tool-1"},
			},
			want: runtime.SemanticGateConsultation,
		},
		{
			name:  "explicit human response",
			event: runtime.ClaudeEventElicitationResult,
			identities: []identitySpec{
				{runtime.IdentitySession, "session_id", "session-1"},
				{runtime.IdentityRequest, "request_id", "request-1"},
			},
			want: runtime.SemanticExplicitHumanResponse,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			binding, err := BindEvent(contract, test.event)
			if err != nil {
				t.Fatalf("BindEvent() error = %v", err)
			}
			identities := buildIdentities(t, test.identities)
			event, err := binding.NewEvent(identities)
			if err != nil {
				t.Fatalf("NewEvent() error = %v", err)
			}
			if !event.IsValid() || !event.Semantics().IsValid() || !event.Origin().IsValid() {
				t.Fatal("NewEvent() returned an invalid constructor-owned value")
			}
			if got := event.Semantics().Semantic(); got != test.want {
				t.Fatalf("Semantic() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestVerifyIdentitiesRejectsUndeclaredNativeName(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventSessionStart)
	identity := mustIdentity(t, runtime.IdentitySession, "unknown_id", "value")
	assertInvalidEvent(t, binding, []Identity{identity}, "not declared")
}

func TestVerifyIdentitiesRejectsDeclaredNameWithWrongKind(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventSessionStart)
	identity := mustIdentity(t, runtime.IdentityRequest, "session_id", "value")
	assertInvalidEvent(t, binding, []Identity{identity}, "declared as session")
}

func TestVerifyIdentitiesRejectsDuplicatePair(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventSessionStart)
	identity := mustIdentity(t, runtime.IdentitySession, "session_id", "value")
	assertInvalidEvent(t, binding, []Identity{identity, identity}, "more than once")
}

func TestVerifyIdentitiesRejectsAbsentRequiredIdentity(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventSessionStart)
	assertInvalidEvent(t, binding, nil, "missing required")
}

func TestVerifyIdentitiesRevalidatesOversizedConstructorState(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventSessionStart)
	identity := Identity{
		kind:        runtime.IdentitySession,
		nativeName:  "session_id",
		value:       strings.Repeat("x", identityValueMaxBytes+1),
		constructed: true,
	}
	assertInvalidEvent(t, binding, []Identity{identity}, "over the 512-byte limit")
}

func TestVerifyIdentitiesRejectsIdentityNotBuiltByConstructor(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventSessionStart)
	assertInvalidEvent(t, binding, []Identity{{}}, "not built by NewIdentity")
}

func TestInvalidConstructionCannotProduceValidL2(t *testing.T) {
	t.Parallel()
	if event, err := (EventBinding{}).NewEvent(nil); err == nil || event.IsValid() {
		t.Fatalf("zero binding NewEvent() = (%#v, %v), want invalid event and error", event, err)
	}
	if (L2{}).IsValid() || (L2{}).Semantics().IsValid() || (L2{}).Origin().IsValid() {
		t.Fatal("zero L2 or its nested values reported valid")
	}
}

func TestNewEventDerivesPostToolBatchUnresolvedFact(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventPostToolBatch)
	session := mustIdentity(t, runtime.IdentitySession, "session_id", "session-1")

	event, err := binding.NewEvent([]Identity{session})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	identities := event.Semantics().Identities()
	if len(identities) != 1 || identities[0].Kind != runtime.IdentitySession {
		t.Fatalf("Identities() = %#v, want exactly one session identity", identities)
	}
	facts := event.Semantics().UnresolvedFacts()
	if len(facts) != 1 || facts[0].Reason != UnresolvedToolCall || !facts[0].IsValid() {
		t.Fatalf("UnresolvedFacts() = %#v, want exactly tool-call-unresolved", facts)
	}
}

func TestNewEventOrdinaryMappingHasNoUnresolvedFacts(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventSessionStart)
	session := mustIdentity(t, runtime.IdentitySession, "session_id", "session-1")
	event, err := binding.NewEvent([]Identity{session})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if facts := event.Semantics().UnresolvedFacts(); len(facts) != 0 {
		t.Fatalf("UnresolvedFacts() = %#v, want none", facts)
	}
}

type identitySpec struct {
	kind       runtime.NativeIdentityKind
	nativeName string
	value      string
}

func buildIdentities(t *testing.T, specs []identitySpec) []Identity {
	t.Helper()
	identities := make([]Identity, 0, len(specs))
	for _, spec := range specs {
		identities = append(identities, mustIdentity(t, spec.kind, spec.nativeName, spec.value))
	}
	return identities
}

func mustBinding(t *testing.T, event runtime.ClaudeLifecycleEvent) EventBinding {
	t.Helper()
	binding, err := BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), event)
	if err != nil {
		t.Fatalf("BindEvent() error = %v", err)
	}
	return binding
}

func mustIdentity(t *testing.T, kind runtime.NativeIdentityKind, nativeName, value string) Identity {
	t.Helper()
	identity, err := NewIdentity(kind, nativeName, value)
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}
	return identity
}

func assertInvalidEvent(t *testing.T, binding EventBinding, identities []Identity, want string) {
	t.Helper()
	event, err := binding.NewEvent(identities)
	if err == nil {
		t.Fatal("NewEvent() error = nil, want validation error")
	}
	if event.IsValid() {
		t.Fatal("NewEvent() returned a valid event after validation failed")
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("NewEvent() error = %q, want substring %q", err, want)
	}
}
