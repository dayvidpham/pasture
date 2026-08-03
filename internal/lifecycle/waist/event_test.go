package waist

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/runtime"
)

const (
	testIdentityValueMaxBytes = 512
	testIdentityValueTooLong  = 513
)

func TestNewEventDerivesEverySemanticArm(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	tests := []struct {
		name           string
		event          runtime.ClaudeLifecycleEvent
		identities     []identitySpec
		wantSemantic   runtime.EventSemantic
		wantBlocking   runtime.BlockingMode
		wantIdentities []SemanticIdentity
		wantUnresolved []UnresolvedFact
	}{
		{
			name:         "observation",
			event:        runtime.ClaudeEventSessionStart,
			wantSemantic: runtime.SemanticObservation,
			wantBlocking: runtime.NonBlocking,
			identities: []identitySpec{
				{runtime.IdentitySession, "session_id", "session-1"},
			},
			wantIdentities: []SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "session-1"},
			},
		},
		{
			name:         "gate consultation",
			event:        runtime.ClaudeEventPreToolUse,
			wantSemantic: runtime.SemanticGateConsultation,
			wantBlocking: runtime.Blocking,
			identities: []identitySpec{
				{runtime.IdentitySession, "session_id", "session-1"},
				{runtime.IdentityToolCall, "tool_use_id", "tool-1"},
			},
			wantIdentities: []SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "session-1"},
				{Kind: runtime.IdentityToolCall, Value: "tool-1"},
			},
		},
		{
			name:         "explicit human response",
			event:        runtime.ClaudeEventElicitationResult,
			wantSemantic: runtime.SemanticExplicitHumanResponse,
			wantBlocking: runtime.Blocking,
			identities: []identitySpec{
				{runtime.IdentitySession, "session_id", "session-1"},
				{runtime.IdentityRequest, "request_id", "request-1"},
			},
			wantIdentities: []SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "session-1"},
				{Kind: runtime.IdentityRequest, Value: "request-1"},
			},
		},
		{
			name:         "post tool batch",
			event:        runtime.ClaudeEventPostToolBatch,
			wantSemantic: runtime.SemanticGateConsultation,
			wantBlocking: runtime.Blocking,
			identities: []identitySpec{
				{runtime.IdentitySession, "session_id", "session-1"},
			},
			wantIdentities: []SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "session-1"},
			},
			wantUnresolved: []UnresolvedFact{
				{Reason: UnresolvedToolCall},
			},
		},
		{
			name:         "ordinary tool observation",
			event:        runtime.ClaudeEventPostToolUse,
			wantSemantic: runtime.SemanticObservation,
			wantBlocking: runtime.NonBlocking,
			identities: []identitySpec{
				{runtime.IdentitySession, "session_id", "session-1"},
				{runtime.IdentityToolCall, "tool_use_id", "tool-1"},
			},
			wantIdentities: []SemanticIdentity{
				{Kind: runtime.IdentitySession, Value: "session-1"},
				{Kind: runtime.IdentityToolCall, Value: "tool-1"},
			},
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
			semantics := event.Semantics()
			if got := semantics.Semantic(); got != test.wantSemantic {
				t.Fatalf("Semantic() = %v, want %v", got, test.wantSemantic)
			}
			if got := semantics.Blocking(); got != test.wantBlocking {
				t.Fatalf("Blocking() = %v, want %v", got, test.wantBlocking)
			}
			if got := semantics.Identities(); !slices.Equal(got, test.wantIdentities) {
				t.Fatalf("Identities() = %#v, want %#v", got, test.wantIdentities)
			}
			if got := semantics.UnresolvedFacts(); !slices.Equal(got, test.wantUnresolved) {
				t.Fatalf("UnresolvedFacts() = %#v, want %#v", got, test.wantUnresolved)
			}
			origin := event.Origin()
			if got := origin.Contract(); got != contract.ID() {
				t.Fatalf("Origin().Contract() = %q, want %q", got, contract.ID())
			}
			if got := origin.NativeEventName(); got != NativeEventName(test.event.NativeName()) {
				t.Fatalf("Origin().NativeEventName() = %q, want %q", got, test.event.NativeName())
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
	first := mustIdentity(t, runtime.IdentitySession, "session_id", "value-1")
	second := mustIdentity(t, runtime.IdentitySession, "session_id", "value-2")
	assertInvalidEvent(t, binding, []Identity{first, second}, "more than once")
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
		value:       strings.Repeat("x", testIdentityValueTooLong),
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

func TestNewIdentityAcceptsValidBoundaryValue(t *testing.T) {
	t.Parallel()
	value := strings.Repeat("x", testIdentityValueMaxBytes)
	identity, err := NewIdentity(runtime.IdentitySession, "session_id", value)
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}
	if !identity.IsValid() {
		t.Fatal("NewIdentity() returned an invalid identity")
	}
	if identity.Kind() != runtime.IdentitySession || identity.NativeName() != "session_id" || identity.Value() != value {
		t.Fatalf("NewIdentity() = %#v, want session/session_id/%d-byte value", identity, testIdentityValueMaxBytes)
	}
}

func TestNewIdentityRejectsInvalidBoundaryValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		kind       runtime.NativeIdentityKind
		nativeName string
		value      string
		wantWhat   string
	}{
		{
			name:       "invalid kind",
			kind:       runtime.NativeIdentityKind(0),
			nativeName: "session_id",
			value:      "session-1",
			wantWhat:   "not recognised",
		},
		{
			name:       "empty native name",
			kind:       runtime.IdentitySession,
			nativeName: "",
			value:      "session-1",
			wantWhat:   "empty or padded",
		},
		{
			name:       "padded native name",
			kind:       runtime.IdentitySession,
			nativeName: " session_id",
			value:      "session-1",
			wantWhat:   "empty or padded",
		},
		{
			name:       "empty value",
			kind:       runtime.IdentitySession,
			nativeName: "session_id",
			value:      "",
			wantWhat:   "empty value",
		},
		{
			name:       "invalid UTF-8 value",
			kind:       runtime.IdentitySession,
			nativeName: "session_id",
			value:      string([]byte{0xff}),
			wantWhat:   "not valid UTF-8",
		},
		{
			name:       "oversized value",
			kind:       runtime.IdentitySession,
			nativeName: "session_id",
			value:      strings.Repeat("x", testIdentityValueTooLong),
			wantWhat:   "over the 512-byte limit",
		},
		{
			name:       "padded value",
			kind:       runtime.IdentitySession,
			nativeName: "session_id",
			value:      " session-1",
			wantWhat:   "surrounding whitespace",
		},
		{
			name:       "control-bearing value",
			kind:       runtime.IdentitySession,
			nativeName: "session_id",
			value:      "session\x00-1",
			wantWhat:   "control character",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			identity, err := NewIdentity(test.kind, test.nativeName, test.value)
			if err == nil {
				t.Fatal("NewIdentity() error = nil, want validation error")
			}
			if identity != (Identity{}) {
				t.Fatalf("NewIdentity() identity = %#v, want zero invalid identity", identity)
			}
			assertStructuredValidationError(t, err, test.wantWhat)
		})
	}
}

func TestWaistAccessorsDefensivelyCopySlices(t *testing.T) {
	t.Parallel()
	binding := mustBinding(t, runtime.ClaudeEventPostToolBatch)
	session := mustIdentity(t, runtime.IdentitySession, "session_id", "session-1")
	batch, err := binding.NewEvent([]Identity{session})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	toolBinding := mustBinding(t, runtime.ClaudeEventPreToolUse)
	toolEvent, err := toolBinding.NewEvent([]Identity{
		session,
		mustIdentity(t, runtime.IdentityToolCall, "tool_use_id", "tool-1"),
	})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}

	identities := toolEvent.Semantics().Identities()
	identities[0] = SemanticIdentity{Kind: runtime.IdentityRequest, Value: "rewritten"}
	if got := toolEvent.Semantics().Identities(); !slices.Equal(got, []SemanticIdentity{
		{Kind: runtime.IdentitySession, Value: "session-1"},
		{Kind: runtime.IdentityToolCall, Value: "tool-1"},
	}) {
		t.Fatalf("Identities() changed after caller mutation: %#v", got)
	}

	facts := batch.Semantics().UnresolvedFacts()
	facts[0] = UnresolvedFact{}
	if got := batch.Semantics().UnresolvedFacts(); !slices.Equal(got, []UnresolvedFact{{Reason: UnresolvedToolCall}}) {
		t.Fatalf("UnresolvedFacts() changed after caller mutation: %#v", got)
	}

	declared := toolBinding.DeclaredIdentities()
	declared[0] = runtime.NativeIdentityField{}
	declaredAgain := toolBinding.DeclaredIdentities()
	if len(declaredAgain) != 2 || declaredAgain[0].Kind() != runtime.IdentitySession || declaredAgain[0].NativeName() != "session_id" || !declaredAgain[0].Required() {
		t.Fatalf("DeclaredIdentities() changed after caller mutation: %#v", declaredAgain)
	}
}

func TestNewEventClaudeCatalogueHasOnlyPostToolBatchUnresolvedFact(t *testing.T) {
	t.Parallel()
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	for _, eventKind := range runtime.ClaudeLifecycleEvents() {
		eventKind := eventKind
		t.Run(eventKind.NativeName(), func(t *testing.T) {
			t.Parallel()
			mapping, err := contract.Mapping(eventKind)
			if err != nil {
				t.Fatalf("Mapping(%q) error = %v", eventKind.NativeName(), err)
			}
			identities := make([]Identity, 0, len(mapping.Identities()))
			for index, field := range mapping.Identities() {
				identity, err := NewIdentity(
					field.Kind(),
					field.NativeName(),
					fmt.Sprintf("catalogue-%d-%s", index, field.Kind()),
				)
				if err != nil {
					t.Fatalf("NewIdentity(%q) error = %v", field.NativeName(), err)
				}
				identities = append(identities, identity)
			}
			binding, err := BindEvent(contract, eventKind)
			if err != nil {
				t.Fatalf("BindEvent(%q) error = %v", eventKind.NativeName(), err)
			}
			event, err := binding.NewEvent(identities)
			if err != nil {
				t.Fatalf("NewEvent(%q) error = %v", eventKind.NativeName(), err)
			}
			if !event.IsValid() {
				t.Fatalf("NewEvent(%q) returned an invalid event", eventKind.NativeName())
			}
			if !event.Semantics().IsValid() {
				t.Fatalf("NewEvent(%q) returned invalid semantics", eventKind.NativeName())
			}
			if !event.Origin().IsValid() {
				t.Fatalf("NewEvent(%q) returned an invalid origin", eventKind.NativeName())
			}
			want := []UnresolvedFact{}
			if eventKind == runtime.ClaudeEventPostToolBatch {
				want = []UnresolvedFact{{Reason: UnresolvedToolCall}}
			}
			if got := event.Semantics().UnresolvedFacts(); !slices.Equal(got, want) {
				t.Fatalf("UnresolvedFacts() = %#v, want %#v", got, want)
			}
		})
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
	assertStructuredValidationError(t, err, want)
}

func assertStructuredValidationError(t testing.TB, err error, wantWhat string) {
	t.Helper()
	var structured *pasterrors.StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("error type = %T, want *pasterrors.StructuredError", err)
	}
	if structured.Category != pasterrors.CategoryValidation {
		t.Fatalf("error category = %q, want %q", structured.Category, pasterrors.CategoryValidation)
	}
	if !strings.Contains(structured.What, wantWhat) {
		t.Fatalf("error What = %q, want substring %q", structured.What, wantWhat)
	}
	for field, value := range map[string]string{
		"Why":    structured.Why,
		"Where":  structured.Where,
		"Impact": structured.Impact,
		"Fix":    structured.Fix,
	} {
		if value == "" {
			t.Fatalf("error %s is empty; want actionable structured error: %#v", field, structured)
		}
	}
}
