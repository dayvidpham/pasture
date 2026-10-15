package receipt

import (
	"slices"
	"testing"

	digest "github.com/opencontainers/go-digest"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
)

// The schema descriptor repeats the receipt codec's wire identities so the
// committed document names the enforced values. This white-box check proves
// the repetition against the unexported constants no black-box test can see;
// the descriptor literals must move with the codec, never the reverse.
func TestSchemaDescriptorMatchesReceiptWire(t *testing.T) {
	t.Parallel()
	document, err := definitions.NewLifecycleSchema()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := definitions.ParseSchemaSpecification(document.Body())
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.OccurrenceKinds) != 1 || spec.OccurrenceKinds[0] != string(receiptKind) {
		t.Fatalf("descriptor occurrence kinds %q do not match the receipt writer", spec.OccurrenceKinds)
	}
	if !slices.Equal(spec.InterpretedKinds, []string{string(interpretedKind), string(interpretedKindV2)}) {
		t.Fatalf("descriptor interpreted kinds %q do not match the receipt codecs", spec.InterpretedKinds)
	}
	if !slices.Equal(spec.DefinitionKinds, []string{string(definitionKind), string(definitions.EvidenceKind)}) {
		t.Fatalf("descriptor definition kinds %q do not match the definition writers", spec.DefinitionKinds)
	}
	if !slices.Equal(spec.DefinitionStateKinds, []string{string(definitionStateKind), string(envelopeStateKind)}) {
		t.Fatalf("descriptor state kinds %q do not match the definition writers", spec.DefinitionStateKinds)
	}
	if !slices.Equal(spec.DefinitionSlots, []string{string(definitionSlot), string(definitionStateSlot)}) {
		t.Fatalf("descriptor slots %q do not match the activation result slots", spec.DefinitionSlots)
	}
	// The payload blob digest the receipt computes names its algorithm; the
	// descriptor must name the same one.
	if digest.FromBytes([]byte("descriptor agreement")).Algorithm() != digest.SHA256 || spec.PayloadDigest != "sha256" {
		t.Fatalf("descriptor digest %q does not match the receipt payload addressing", spec.PayloadDigest)
	}
	// Every gate class survives its own spelling round-trip, so the epoch
	// descriptor's class list parses against the live gate set.
	for _, class := range []gate.WriteClass{gate.WriteDeliveryReceipt, gate.WriteDefinitionActivation, gate.WriteLineageLinks, gate.WriteDisclosure} {
		parsed, ok := gate.ParseWriteClass(class.String())
		if !ok || parsed != class {
			t.Fatalf("gate class %q does not survive its spelling round-trip", class.String())
		}
	}
	if _, ok := gate.ParseWriteClass("bogus-class"); ok {
		t.Fatal("gate parses a class its String never emits")
	}
}
