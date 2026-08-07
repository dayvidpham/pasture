package receipt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

func validPlan() model.DisclosurePlanFact {
	var scope, projection model.ContentIdentity
	scope[0] = 0xA1
	projection[0] = 0xB2
	return model.DisclosurePlanFact{Scope: scope, Projection: projection, Policy: "m5-static-disclosure"}
}

func validAttempt() model.DisclosureAttemptFact {
	return model.DisclosureAttemptFact{RecordedAt: time.Unix(0, 1234).UTC()}
}

func validResult() model.DisclosureResultFact {
	return model.DisclosureResultFact{Disposition: model.DisclosureReleased}
}

func TestNewDisclosureWellFormed(t *testing.T) {
	write, err := NewDisclosure(validPlan(), validAttempt(), validResult())
	if err != nil {
		t.Fatalf("NewDisclosure: %v", err)
	}
	if write.Class() != gate.WriteDisclosure {
		t.Fatalf("class = %v, want WriteDisclosure", write.Class())
	}
	if len(write.effects) != 3 {
		t.Fatalf("a disclosure write must carry exactly 3 effects (plan, attempt, result), got %d", len(write.effects))
	}
	wantSlots := map[provenance.ResultSlotID]provenance.EvidenceKind{
		disclosurePlanSlot:    disclosurePlanKind,
		disclosureAttemptSlot: disclosureAttemptKind,
		disclosureResultSlot:  disclosureResultKind,
	}
	seen := map[provenance.ResultSlotID]struct{}{}
	for _, effect := range write.effects {
		kind, ok := wantSlots[effect.ResultSlot]
		if !ok {
			t.Fatalf("unexpected disclosure result slot %q", effect.ResultSlot)
		}
		if effect.EvidenceKind != kind {
			t.Fatalf("slot %q has kind %q, want %q", effect.ResultSlot, effect.EvidenceKind, kind)
		}
		if _, dup := seen[effect.ResultSlot]; dup {
			t.Fatalf("duplicate disclosure result slot %q", effect.ResultSlot)
		}
		seen[effect.ResultSlot] = struct{}{}
	}
	if len(seen) != 3 {
		t.Fatalf("expected the three distinct disclosure slots, got %d", len(seen))
	}
	// A disclosure write takes a fresh random operation identity at commit, so
	// the constructor leaves the deterministic operation identity empty.
	if write.OperationID() != "" {
		t.Fatalf("a disclosure write must not carry a deterministic operation identity, got %q", write.OperationID())
	}
}

func TestNewDisclosureRefusesInvalidFacts(t *testing.T) {
	cases := []struct {
		name    string
		plan    model.DisclosurePlanFact
		attempt model.DisclosureAttemptFact
		result  model.DisclosureResultFact
	}{
		{"zero plan", model.DisclosurePlanFact{}, validAttempt(), validResult()},
		{"zero attempt", validPlan(), model.DisclosureAttemptFact{}, validResult()},
		{"zero result", validPlan(), validAttempt(), model.DisclosureResultFact{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewDisclosure(tc.plan, tc.attempt, tc.result); err == nil {
				t.Fatalf("expected a refusal for %s, got nil", tc.name)
			}
		})
	}
}

// TestCommitDisclosureRefusesUnwarrantedBeforeIO: a zero or class-mismatched
// warrant is refused with a typed *gate.Refusal before any I/O, even though the
// Service has no journal wired — the gate authorizes FIRST.
func TestCommitDisclosureRefusesUnwarrantedBeforeIO(t *testing.T) {
	write, err := NewDisclosure(validPlan(), validAttempt(), validResult())
	if err != nil {
		t.Fatalf("NewDisclosure: %v", err)
	}
	var svc Service
	_, commitErr := svc.CommitDisclosure(context.Background(), gate.Warrant{}, write)
	var refusal *gate.Refusal
	if !errors.As(commitErr, &refusal) {
		t.Fatalf("expected a typed *gate.Refusal for a zero warrant, got %v", commitErr)
	}

	// A warrant for a different class is refused as a class mismatch.
	intent, r := gate.NewLineageIntent("claude-code", 1)
	if r != nil {
		t.Fatalf("build lineage intent: %v", r)
	}
	lineageWarrant, r := gate.Legalize(intent)
	if r != nil {
		t.Fatalf("legalize lineage intent: %v", r)
	}
	_, commitErr = svc.CommitDisclosure(context.Background(), lineageWarrant, write)
	if !errors.As(commitErr, &refusal) {
		t.Fatalf("expected a typed *gate.Refusal for a class-mismatched warrant, got %v", commitErr)
	}
	if !strings.Contains(refusal.Error(), "disclosure") {
		t.Fatalf("class-mismatch refusal must name the disclosure write class: %v", refusal)
	}
}

// TestCommitDisclosureRefusesNonDisclosureWrite: a warrant of the right class but
// a write built for another class is refused before any I/O.
func TestCommitDisclosureRefusesNonDisclosureWrite(t *testing.T) {
	intent, r := gate.NewDisclosureIntent(validPlan().Projection)
	if r != nil {
		t.Fatalf("build disclosure intent: %v", r)
	}
	warrant, r := gate.Legalize(intent)
	if r != nil {
		t.Fatalf("legalize disclosure intent: %v", r)
	}
	var svc Service
	_, err := svc.CommitDisclosure(context.Background(), warrant, GatedWrite{})
	if err == nil {
		t.Fatal("committing an unconstructed/wrong-class write must be refused")
	}
}
