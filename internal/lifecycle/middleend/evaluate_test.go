package middleend_test

import (
	"bytes"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/backend"
	"github.com/dayvidpham/pasture/internal/lifecycle/codebook"
	"github.com/dayvidpham/pasture/internal/lifecycle/middleend"
	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestDeriveObservationAndExplicitHumanResponse(t *testing.T) {
	t.Parallel()
	for _, event := range []runtime.ClaudeLifecycleEvent{
		runtime.ClaudeEventSessionStart,
		runtime.ClaudeEventElicitationResult,
	} {
		event := event
		t.Run(event.NativeName(), func(t *testing.T) {
			t.Parallel()
			derivation, err := middleend.Derive(realL2(t, event), codebook.Active())
			if err != nil {
				t.Fatalf("Derive() error = %v", err)
			}
			if !derivation.IsValid() {
				t.Fatal("Derive() returned an invalid derivation")
			}
			if effects := derivation.Effects(); len(effects) != 1 || effects[0].ResultSlot != "interpreted" {
				t.Fatalf("Effects() = %#v, want one interpreted effect", effects)
			}
			if derivation.Response().IsValid() {
				t.Fatal("Response() is valid for a non-gate event")
			}
		})
	}
}

func TestDeriveGateUsesCanonicalOrderAndProceed(t *testing.T) {
	t.Parallel()
	derivation, err := middleend.Derive(realL2(t, runtime.ClaudeEventPreToolUse), codebook.Active())
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	effects := derivation.Effects()
	if len(effects) != 2 || effects[0].ResultSlot != "interpreted" || effects[1].ResultSlot != "consultation" {
		t.Fatalf("Effects() = %#v, want interpreted then consultation", effects)
	}
	response := derivation.Response()
	if !derivation.IsValid() || !response.IsValid() || response.Decision() != backend.DecisionProceed {
		t.Fatalf("Derive() = %#v with response %#v, want valid Proceed", derivation, response)
	}
}

func TestEffectsReturnsDeepDefensiveCopy(t *testing.T) {
	t.Parallel()
	derivation, err := middleend.Derive(realL2(t, runtime.ClaudeEventPreToolUse), codebook.Active())
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	first := derivation.Effects()
	want := derivation.Effects()
	first[0].ResultSlot = "rewritten"
	first[0].Payload[0] ^= 0xff
	first[0].ContentDigest[0] ^= 0xff
	got := derivation.Effects()
	if got[0].ResultSlot != want[0].ResultSlot || !bytes.Equal(got[0].Payload, want[0].Payload) || !bytes.Equal(got[0].ContentDigest, want[0].ContentDigest) {
		t.Fatalf("Effects() changed after caller mutation: got %#v, want %#v", got[0], want[0])
	}
}

func TestClaudeGateDerivationRegressionIsNonLive(t *testing.T) {
	t.Parallel()
	event := realL2(t, runtime.ClaudeEventPreToolUse)
	derivation, err := middleend.Derive(event, codebook.Active())
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	interpreted, err := receipt.NewInterpreted(event, event.Origin().Contract(), codebook.Active())
	if err != nil {
		t.Fatalf("NewInterpreted() error = %v", err)
	}
	effects := derivation.Effects()
	if len(effects) != 2 || effects[0].ResultSlot != interpreted.Effect().ResultSlot || derivation.Response().Decision() != backend.DecisionProceed {
		t.Fatalf("non-live Claude regression derivation = %#v, %#v", effects, derivation.Response())
	}
}

func TestZeroDerivationIsInvalid(t *testing.T) {
	t.Parallel()
	var derivation middleend.Derivation
	if derivation.IsValid() || derivation.Response().IsValid() || derivation.Effects() != nil {
		t.Fatalf("zero Derivation = %#v, want invalid empty value", derivation)
	}
}

func realL2(t *testing.T, event runtime.ClaudeLifecycleEvent) waist.L2 {
	t.Helper()
	binding, err := waist.BindEvent(runtime.ClaudeCode2_1_210Lifecycle(), event)
	if err != nil {
		t.Fatalf("BindEvent() error = %v", err)
	}
	identities := make([]waist.Identity, 0, len(binding.DeclaredIdentities()))
	for index, field := range binding.DeclaredIdentities() {
		identity, err := waist.NewIdentity(field.Kind(), field.NativeName(), field.NativeName()+"-regression-"+string(rune('a'+index)))
		if err != nil {
			t.Fatalf("NewIdentity() error = %v", err)
		}
		identities = append(identities, identity)
	}
	eventValue, err := binding.NewEvent(identities)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return eventValue
}
