package legalize_test

import (
	"bytes"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/legalize"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestEventSemanticTerminals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		event            runtime.ClaudeLifecycleEvent
		expectedBlocking runtime.BlockingMode
		semantic         runtime.EventSemantic
		legalized        bool
		noAuthority      bool
	}{
		{name: "SessionStart observation", event: runtime.ClaudeEventSessionStart, expectedBlocking: runtime.NonBlocking, semantic: runtime.SemanticObservation},
		{name: "PreToolUse ordinal 8", event: runtime.ClaudeEventPreToolUse, expectedBlocking: runtime.Blocking, semantic: runtime.SemanticGateConsultation, legalized: true},
		{name: "PostToolBatch ordinal 13", event: runtime.ClaudeEventPostToolBatch, expectedBlocking: runtime.Blocking, semantic: runtime.SemanticGateConsultation, legalized: true},
		{name: "PreCompact ordinal 25", event: runtime.ClaudeEventPreCompact, expectedBlocking: runtime.Blocking, semantic: runtime.SemanticGateConsultation, legalized: true},
		{name: "Elicitation ordinal 29", event: runtime.ClaudeEventElicitation, expectedBlocking: runtime.Blocking, semantic: runtime.SemanticGateConsultation, legalized: true},
		{name: "ConfigChange out of M1 conditionally blocking", event: runtime.ClaudeEventConfigChange, expectedBlocking: runtime.ConditionallyBlocking, semantic: runtime.SemanticGateConsultation, legalized: true},
		{name: "ElicitationResult blocking human response", event: runtime.ClaudeEventElicitationResult, expectedBlocking: runtime.Blocking, semantic: runtime.SemanticExplicitHumanResponse, noAuthority: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			l2 := realL2(t, test.event)
			if got := l2.Semantics().Blocking(); got != test.expectedBlocking {
				t.Fatalf("L2 blocking mode = %v, want %v", got, test.expectedBlocking)
			}
			result, err := legalize.Event(l2)
			if err != nil {
				t.Fatalf("Event() error = %v", err)
			}
			if !result.IsValid() || result.Semantic() != test.semantic {
				t.Fatalf("Event() result = %#v semantic %v, want valid %v", result, result.Semantic(), test.semantic)
			}
			legalized, hasLegalized := result.Legalized()
			if hasLegalized != test.legalized {
				t.Fatalf("Legalized present = %t, want %t", hasLegalized, test.legalized)
			}
			if hasLegalized {
				if !legalized.IsValid() || legalized.AuthorityExercised() {
					t.Fatalf("Legalized = %#v, want valid non-authoritative value", legalized)
				}
				raw, marshalErr := legalized.MarshalJSON()
				if marshalErr != nil || !bytes.Equal(raw, []byte(`{"authority":"not-exercised"}`)) {
					t.Fatalf("Legalized JSON = %s, %v", raw, marshalErr)
				}
			}
			noAuthority, hasNoAuthority := result.NoAuthority()
			if hasNoAuthority != test.noAuthority || (hasNoAuthority && !noAuthority.IsValid()) {
				t.Fatalf("NoAuthority = %#v, %t, want present %t", noAuthority, hasNoAuthority, test.noAuthority)
			}
		})
	}
}

func TestEventRejectsInvalidL2(t *testing.T) {
	t.Parallel()
	if result, err := legalize.Event(waist.L2{}); err == nil || result.IsValid() {
		t.Fatalf("Event(zero) = %#v, %v, want invalid result and actionable error", result, err)
	}
}

func TestZeroValuesAreDefensive(t *testing.T) {
	t.Parallel()
	var result legalize.Result
	if result.IsValid() || result.Semantic() != 0 {
		t.Fatalf("zero Result = %#v, want invalid", result)
	}
	if value, ok := result.Legalized(); ok || value.IsValid() {
		t.Fatalf("zero Result Legalized = %#v, %t", value, ok)
	}
	if value, ok := result.NoAuthority(); ok || value.IsValid() {
		t.Fatalf("zero Result NoAuthority = %#v, %t", value, ok)
	}
	var legalized legalize.Legalized
	if legalized.IsValid() || legalized.AuthorityExercised() {
		t.Fatalf("zero Legalized = %#v, want invalid and non-authoritative", legalized)
	}
	if raw, err := legalized.MarshalJSON(); err == nil || raw != nil {
		t.Fatalf("zero Legalized MarshalJSON = %q, %v, want defensive error", raw, err)
	}
	if (legalize.NoAuthority{}).IsValid() {
		t.Fatal("zero NoAuthority is valid")
	}
}

func realL2(t *testing.T, event runtime.ClaudeLifecycleEvent) waist.L2 {
	t.Helper()
	contract := runtime.ClaudeCode2_1_210Lifecycle()
	binding, err := waist.BindEvent(contract, event)
	if err != nil {
		t.Fatalf("BindEvent(%v): %v", event, err)
	}
	declared := binding.DeclaredIdentities()
	identities := make([]waist.Identity, 0, len(declared))
	for index, field := range declared {
		identity, identityErr := waist.NewIdentity(field.Kind(), field.NativeName(), field.NativeName()+"-value-"+string(rune('a'+index)))
		if identityErr != nil {
			t.Fatalf("NewIdentity(%s): %v", field.NativeName(), identityErr)
		}
		identities = append(identities, identity)
	}
	l2, err := binding.NewEvent(identities)
	if err != nil {
		t.Fatalf("NewEvent(%v): %v", event, err)
	}
	return l2
}
