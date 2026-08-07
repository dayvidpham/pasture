package receipt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/lineage"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

func linkFact(from, to int64) lineage.LinkFact {
	return lineage.LinkFact{
		Harness: ir.HarnessClaudeCode,
		Kind:    runtime.IdentitySession,
		Value:   "S1",
		From:    model.OccurrenceID(from),
		To:      model.OccurrenceID(to),
	}
}

func TestNewLineageLinksOverCapRefusesActionably(t *testing.T) {
	facts := make([]lineage.LinkFact, gate.MaxLinksPerOperation+1)
	for i := range facts {
		facts[i] = linkFact(int64(i+1), int64(i+2))
	}
	_, err := NewLineageLinks(facts)
	if err == nil {
		t.Fatal("expected an over-cap refusal, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "narrow the scope with --binding") {
		t.Fatalf("over-cap refusal must be actionable (narrow the scope with --binding): %v", err)
	}
	if !strings.Contains(msg, "cap") {
		t.Fatalf("over-cap refusal must name the per-operation cap: %v", err)
	}
}

func TestNewLineageLinksEmptyRefuses(t *testing.T) {
	if _, err := NewLineageLinks(nil); err == nil {
		t.Fatal("expected a refusal for an empty edge set, got nil")
	}
}

func TestNewLineageLinksMultiHarnessRefuses(t *testing.T) {
	claude := linkFact(1, 2)
	opencode := linkFact(3, 4)
	opencode.Harness = ir.HarnessOpenCode
	_, err := NewLineageLinks([]lineage.LinkFact{claude, opencode})
	if err == nil || !strings.Contains(err.Error(), "harness") {
		t.Fatalf("expected a single-harness refusal, got %v", err)
	}
}

func TestNewLineageLinksWellFormedAndDeterministic(t *testing.T) {
	facts := []lineage.LinkFact{linkFact(10, 20), linkFact(20, 30), linkFact(30, 40)}
	write, err := NewLineageLinks(facts)
	if err != nil {
		t.Fatalf("NewLineageLinks: %v", err)
	}
	if write.Class() != gate.WriteLineageLinks {
		t.Fatalf("class = %v, want WriteLineageLinks", write.Class())
	}
	if len(write.effects) != len(facts) {
		t.Fatalf("effects = %d, want %d", len(write.effects), len(facts))
	}
	slots := map[provenance.ResultSlotID]struct{}{}
	for _, effect := range write.effects {
		if effect.EvidenceKind != lineageLinkKind {
			t.Fatalf("effect kind = %q, want %q", effect.EvidenceKind, lineageLinkKind)
		}
		if effect.ResultSlot == "" {
			t.Fatal("each link effect must carry a non-empty unique result slot")
		}
		if _, dup := slots[effect.ResultSlot]; dup {
			t.Fatalf("duplicate result slot %q", effect.ResultSlot)
		}
		slots[effect.ResultSlot] = struct{}{}
	}

	// The operation identity is content-derived, so the SAME edge set in a
	// different order builds the SAME operation (concurrent duplicates collapse).
	shuffled := []lineage.LinkFact{facts[2], facts[0], facts[1]}
	other, err := NewLineageLinks(shuffled)
	if err != nil {
		t.Fatalf("NewLineageLinks(shuffled): %v", err)
	}
	if write.OperationID() != other.OperationID() {
		t.Fatalf("operation identity must be order-independent: %q vs %q", write.OperationID(), other.OperationID())
	}
	// A different edge set builds a different operation.
	different, err := NewLineageLinks([]lineage.LinkFact{linkFact(10, 20)})
	if err != nil {
		t.Fatalf("NewLineageLinks(different): %v", err)
	}
	if write.OperationID() == different.OperationID() {
		t.Fatal("distinct edge sets must build distinct operation identities")
	}
}

func TestDecodeLinkRoundTripAndRejectsTampering(t *testing.T) {
	fact := linkFact(10, 20)
	payload, err := canonicalLinkPayload(fact)
	if err != nil {
		t.Fatalf("canonicalLinkPayload: %v", err)
	}
	record, err := DecodeLink(model.LifecycleLinkID(99), payload)
	if err != nil {
		t.Fatalf("DecodeLink: %v", err)
	}
	if record.LinkID.JournalID() != 99 || record.Harness != fact.Harness || record.Kind != fact.Kind ||
		record.Value != fact.Value || record.From != fact.From || record.To != fact.To {
		t.Fatalf("decoded record does not round-trip the fact: %+v", record)
	}
	if !record.IsValid() {
		t.Fatal("round-tripped record must be valid")
	}

	// Non-canonical (whitespace-padded) payload is refused.
	if _, err := DecodeLink(model.LifecycleLinkID(1), append([]byte(" "), payload...)); err == nil {
		t.Fatal("expected noncanonical payload refusal")
	}
	// Unknown field is refused.
	if _, err := DecodeLink(model.LifecycleLinkID(1), []byte(`{"harness":"claude-code","kind":1,"value":"S1","from":10,"to":20,"extra":1}`)); err == nil {
		t.Fatal("expected unknown-field refusal")
	}
	// A self-edge is refused as not well-formed.
	self, _ := canonicalLinkPayload(linkFact(10, 10))
	if _, err := DecodeLink(model.LifecycleLinkID(1), self); err == nil {
		t.Fatal("expected self-edge refusal")
	}
}

func TestCommitLineageRefusesUnwarrantedBeforeIO(t *testing.T) {
	write, err := NewLineageLinks([]lineage.LinkFact{linkFact(10, 20)})
	if err != nil {
		t.Fatalf("NewLineageLinks: %v", err)
	}
	// A zero warrant is refused with a typed *gate.Refusal before any I/O, even
	// though the Service has no journal wired — the gate authorizes first.
	var svc Service
	_, commitErr := svc.CommitLineage(context.Background(), gate.Warrant{}, write)
	var refusal *gate.Refusal
	if !errors.As(commitErr, &refusal) {
		t.Fatalf("expected a typed *gate.Refusal for a zero warrant, got %v", commitErr)
	}

	// A warrant for a different class is refused as a class mismatch.
	intent, r := gate.NewDefinitionActivationIntent(model.ContentIdentity{1})
	if r != nil {
		t.Fatalf("build definition intent: %v", r)
	}
	definitionWarrant, r := gate.Legalize(intent)
	if r != nil {
		t.Fatalf("legalize definition intent: %v", r)
	}
	_, commitErr = svc.CommitLineage(context.Background(), definitionWarrant, write)
	if !errors.As(commitErr, &refusal) {
		t.Fatalf("expected a typed *gate.Refusal for a class-mismatched warrant, got %v", commitErr)
	}
}
