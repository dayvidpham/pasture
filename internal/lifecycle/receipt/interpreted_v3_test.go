package receipt

// Unit pins for the interpreted.v3 codec and the model envelope constructor.
// Pure: no store, no journal. The finalized bytes here are built by the sole
// v3 emitter (finalizeInterpretedV3) from a constructor-owned prepared record
// and a structurally complete envelope with test-shaped refs.

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

func testSemanticEnvelope() model.SemanticEnvelopeRef {
	content := func(seed byte) model.ContentIdentity {
		var out model.ContentIdentity
		for i := range out {
			out[i] = seed + byte(i)
		}
		return out
	}
	return model.SemanticEnvelopeRef{
		Runtime:        model.RuntimeContractDefinitionRef{Definition: model.DefinitionRef{Definition: 11, Kind: model.DefinitionRuntimeContract, Content: content(1)}},
		Schema:         model.LifecycleSchemaDefinitionRef{Definition: model.DefinitionRef{Definition: 12, Kind: model.DefinitionLifecycleSchema, Content: content(2)}},
		Metamodel:      model.LifecycleMetamodelRef{Definition: model.DefinitionRef{Definition: 13, Kind: model.DefinitionMetamodel, Content: content(3)}},
		Interpreter:    model.InterpreterDefinitionRef{Definition: model.DefinitionRef{Definition: 14, Kind: model.DefinitionInterpreter, Content: content(4)}},
		Implementation: model.EpochImplementationRef{Definition: model.DefinitionRef{Definition: 15, Kind: model.DefinitionEpochImplementation, Content: content(5)}},
	}
}

func testOccurrenceEnvelope(t *testing.T) model.OccurrenceEnvelopeRef {
	t.Helper()
	semantic := testSemanticEnvelope()
	return model.OccurrenceEnvelopeRef{
		Runtime:        model.RuntimeContractDefinitionRef{Definition: semantic.Runtime.Definition, Contract: mustClaudeLifecycleContract(t)},
		Schema:         semantic.Schema,
		Implementation: semantic.Implementation,
		Retention:      model.RetentionPolicyDefinitionRef{Definition: model.DefinitionRef{Definition: 16, Kind: model.DefinitionRetentionPolicy, Content: semantic.Runtime.Definition.Content}},
	}
}

func TestFinalizeInterpretedV3RoundTripsThroughStrictDecode(t *testing.T) {
	t.Parallel()
	prepared, err := NewInterpreted(mustPostToolBatchL2(t, "v3-codec"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	envelope := testSemanticEnvelope()
	effect, err := finalizeInterpretedV3(prepared, envelope)
	if err != nil {
		t.Fatalf("finalizeInterpretedV3: %v", err)
	}
	if effect.Sort != provenance.EffectEvidence || effect.ResultSlot != interpretedSlot || effect.EvidenceKind != interpretedKindV3 {
		t.Fatalf("finalized effect = %#v, want v3 evidence in the interpreted slot", effect)
	}
	sum := sha256.Sum256(effect.Payload)
	if !bytes.Equal(sum[:], effect.ContentDigest) {
		t.Fatal("finalized content digest does not authenticate the payload")
	}
	decoded, err := DecodeInterpretedV3(model.InterpretationID(9), model.OccurrenceID(8), effect.Payload)
	if err != nil {
		t.Fatalf("DecodeInterpretedV3: %v", err)
	}
	got, ok := decoded.SemanticEnvelope()
	if !ok {
		t.Fatal("decoded v3 reports no semantic envelope")
	}
	if got != envelope {
		t.Fatalf("decoded envelope = %+v, want %+v", got, envelope)
	}
	if decoded.Semantic() != prepared.Semantic() || decoded.Contract() != prepared.Contract() {
		t.Fatal("decoded semantic arms or contract differ from the prepared record")
	}
	manifest, ok := decoded.Metamodel()
	if !ok || manifest != prepared.Metamodel() {
		t.Fatal("decoded manifest differs from the prepared producer coordinate")
	}
	// The legacy decoders never see v3 bytes, and v3 never sees theirs: kind
	// isolation is structural, not a version check callers can skip.
	if _, err := DecodeInterpreted(9, 8, effect.Payload); err == nil {
		t.Fatal("interpreted.v1 decoder accepted a v3 payload")
	}
	if _, err := DecodeInterpretedV2(9, 8, effect.Payload); err == nil {
		t.Fatal("interpreted.v2 decoder accepted a v3 payload")
	}
	preparedEffect := prepared.Effect()
	if _, err := DecodeInterpretedV3(9, 8, preparedEffect.Payload); err == nil {
		t.Fatal("interpreted.v3 decoder accepted a v2 payload without an envelope")
	}
}

func TestFinalizeInterpretedV3RefusesIncompleteEnvelopes(t *testing.T) {
	t.Parallel()
	prepared, err := NewInterpreted(mustPostToolBatchL2(t, "v3-incomplete"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	complete := testSemanticEnvelope()
	cases := []struct {
		name     string
		envelope model.SemanticEnvelopeRef
	}{
		{"zero runtime", withSemanticSlot(complete, "runtime", model.DefinitionRef{})},
		{"zero schema", withSemanticSlot(complete, "schema", model.DefinitionRef{})},
		{"zero metamodel", withSemanticSlot(complete, "metamodel", model.DefinitionRef{})},
		{"zero interpreter", withSemanticSlot(complete, "interpreter", model.DefinitionRef{})},
		{"zero implementation", withSemanticSlot(complete, "implementation", model.DefinitionRef{})},
		{"wrong-kind metamodel", withSemanticKind(complete, "metamodel", model.DefinitionInterpreter)},
		{"zero-content interpreter", withSemanticContent(complete, "interpreter", model.ContentIdentity{})},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := finalizeInterpretedV3(prepared, tc.envelope); err == nil {
				t.Fatalf("finalizeInterpretedV3 accepted an incomplete envelope (%s)", tc.name)
			}
			if _, err := model.NewInterpretedRecordWithEnvelope(1, 1, prepared.Semantic(), prepared.Identities(), prepared.UnresolvedFacts(), prepared.Contract(), prepared.Metamodel(), tc.envelope); err == nil {
				t.Fatalf("model constructor accepted an incomplete envelope (%s)", tc.name)
			}
		})
	}
}

func TestDecodeInterpretedV3RejectsNoncanonicalPayloads(t *testing.T) {
	t.Parallel()
	prepared, err := NewInterpreted(mustPostToolBatchL2(t, "v3-canon"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	effect, err := finalizeInterpretedV3(prepared, testSemanticEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Replace(effect.Payload, []byte(`"semantic"`), []byte(`"semantic":1,"semantic"`), 1)
	trailing := append(append([]byte(nil), effect.Payload...), []byte(` {}`)...)
	unknown := bytes.Replace(effect.Payload, []byte(`"envelope"`), []byte(`"envelope2":1,"envelope"`), 1)
	for name, payload := range map[string][]byte{"duplicate member": duplicate, "trailing value": trailing, "unknown field": unknown} {
		if _, err := DecodeInterpretedV3(9, 8, payload); err == nil {
			t.Fatalf("DecodeInterpretedV3 accepted %s", name)
		}
	}
	if !strings.Contains(string(effect.Payload), `"envelope"`) {
		t.Fatal("finalized v3 payload carries no envelope member")
	}
}

func TestSharedRefsEqualRequiresExactTriple(t *testing.T) {
	t.Parallel()
	semantic := testSemanticEnvelope()
	occurrence := testOccurrenceEnvelope(t)
	if !semantic.SharedRefsEqual(occurrence) {
		t.Fatal("matching envelopes report unequal shared refs")
	}
	moved := occurrence
	moved.Schema.Definition.Definition++
	if semantic.SharedRefsEqual(moved) {
		t.Fatal("different schema refs report equal")
	}
	moved = occurrence
	moved.Runtime.Definition.Content[0] ^= 1
	if semantic.SharedRefsEqual(moved) {
		t.Fatal("content-mismatched runtime refs report equal")
	}
}

func withSemanticSlot(envelope model.SemanticEnvelopeRef, slot string, ref model.DefinitionRef) model.SemanticEnvelopeRef {
	switch slot {
	case "runtime":
		envelope.Runtime.Definition = ref
	case "schema":
		envelope.Schema.Definition = ref
	case "metamodel":
		envelope.Metamodel.Definition = ref
	case "interpreter":
		envelope.Interpreter.Definition = ref
	case "implementation":
		envelope.Implementation.Definition = ref
	}
	return envelope
}

func withSemanticKind(envelope model.SemanticEnvelopeRef, slot string, kind model.DefinitionKind) model.SemanticEnvelopeRef {
	ref := envelopeForSlot(envelope, slot)
	ref.Kind = kind
	return withSemanticSlot(envelope, slot, ref)
}

func withSemanticContent(envelope model.SemanticEnvelopeRef, slot string, content model.ContentIdentity) model.SemanticEnvelopeRef {
	ref := envelopeForSlot(envelope, slot)
	ref.Content = content
	return withSemanticSlot(envelope, slot, ref)
}

func envelopeForSlot(envelope model.SemanticEnvelopeRef, slot string) model.DefinitionRef {
	switch slot {
	case "runtime":
		return envelope.Runtime.Definition
	case "schema":
		return envelope.Schema.Definition
	case "metamodel":
		return envelope.Metamodel.Definition
	case "interpreter":
		return envelope.Interpreter.Definition
	case "implementation":
		return envelope.Implementation.Definition
	}
	return model.DefinitionRef{}
}

// TestPreparedMaterialCarriesNoJournalCoordinates pins the L2 content
// boundary on the prepared side: a prepared interpreted effect and its
// consultation carry verified semantics, producer contract and manifest, but
// no journal, store, payload or authority coordinates and no envelope. Only
// the receipt finalizer assigns refs, from the owners, at commit time.
func TestPreparedMaterialCarriesNoJournalCoordinates(t *testing.T) {
	t.Parallel()
	prepared, err := NewInterpreted(mustPostToolBatchL2(t, "prepared-pure"), mustClaudeLifecycleContract(t), metamodel.Active())
	if err != nil {
		t.Fatal(err)
	}
	effect := prepared.Effect()
	for _, member := range []string{"definition", "envelope", "journal", "JournalID", "payload", "authority", "store"} {
		if bytes.Contains(effect.Payload, []byte(`"`+member+`"`)) {
			t.Fatalf("prepared interpreted payload carries journal member %q:\n%s", member, effect.Payload)
		}
	}
	consultation, err := NewConsultation(prepared, jsonPort{raw: []byte(`{"rule":"allow"}`), valid: true}, jsonPort{raw: []byte(`{"decision":"proceed"}`), valid: true})
	if err != nil {
		t.Fatal(err)
	}
	consultationEffect := consultation.Effect()
	for _, member := range []string{"definition", "envelope", "journal", "JournalID", "authority", "store"} {
		if bytes.Contains(consultationEffect.Payload, []byte(`"`+member+`"`)) {
			t.Fatalf("prepared consultation payload carries journal member %q:\n%s", member, consultationEffect.Payload)
		}
	}
}
