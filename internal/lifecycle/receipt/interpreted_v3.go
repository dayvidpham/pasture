package receipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// interpretedKindV3 is the durable evidence kind for interpreted records that
// carry the complete five-reference semantic envelope. It is a SEPARATE kind
// from interpreted.v1/v2: their strict decoders never see it, so committed
// legacy records decode unchanged with no in-place migration and no inferred
// envelope. Nothing but the common receipt boundary finalizes v3.
const interpretedKindV3 = provenance.EvidenceKind("pasture.lifecycle.interpreted.v3")

// CurrentInterpretedEvidenceKind identifies newly finalized interpretations.
// Readers admit the historical kinds alongside it.
func CurrentInterpretedEvidenceKind() provenance.EvidenceKind { return interpretedKindV3 }

// definitionRefWire is the canonical durable encoding of one envelope owner
// reference: the positive journal coordinate, the exact owner kind, and the
// hex content identity the snapshot bytes must hash to.
type definitionRefWire struct {
	Definition provenance.JournalID `json:"definition"`
	Kind       model.DefinitionKind `json:"kind"`
	Content    string               `json:"content"`
}

func encodeDefinitionRef(ref model.DefinitionRef) definitionRefWire {
	return definitionRefWire{Definition: provenance.JournalID(ref.Definition), Kind: ref.Kind, Content: hex.EncodeToString(ref.Content[:])}
}

func decodeDefinitionRef(wire definitionRefWire, slot string, want model.DefinitionKind) (model.DefinitionRef, error) {
	if wire.Definition <= 0 || wire.Kind != want || len(wire.Content) != 2*sha256.Size {
		return model.DefinitionRef{}, fmt.Errorf("decode interpreted.v3 lifecycle evidence: envelope slot %q is not a complete nonzero owner reference", slot)
	}
	raw, err := hex.DecodeString(wire.Content)
	if err != nil {
		return model.DefinitionRef{}, fmt.Errorf("decode interpreted.v3 lifecycle evidence: envelope slot %q content is not hex: %w", slot, err)
	}
	var content model.ContentIdentity
	copy(content[:], raw)
	if content == (model.ContentIdentity{}) {
		return model.DefinitionRef{}, fmt.Errorf("decode interpreted.v3 lifecycle evidence: envelope slot %q content is zero", slot)
	}
	return model.DefinitionRef{Definition: model.DefinitionJournalID(wire.Definition), Kind: wire.Kind, Content: content}, nil
}

type interpretedEnvelopeWire struct {
	Runtime        definitionRefWire `json:"runtime"`
	Schema         definitionRefWire `json:"schema"`
	Metamodel      definitionRefWire `json:"metamodel"`
	Interpreter    definitionRefWire `json:"interpreter"`
	Implementation definitionRefWire `json:"implementation"`
}

func encodeSemanticEnvelope(envelope model.SemanticEnvelopeRef) interpretedEnvelopeWire {
	return interpretedEnvelopeWire{
		Runtime:        encodeDefinitionRef(envelope.Runtime.Definition),
		Schema:         encodeDefinitionRef(envelope.Schema.Definition),
		Metamodel:      encodeDefinitionRef(envelope.Metamodel.Definition),
		Interpreter:    encodeDefinitionRef(envelope.Interpreter.Definition),
		Implementation: encodeDefinitionRef(envelope.Implementation.Definition),
	}
}

func decodeSemanticEnvelope(wire interpretedEnvelopeWire) (model.SemanticEnvelopeRef, error) {
	runtimeRef, err := decodeDefinitionRef(wire.Runtime, "runtime", model.DefinitionRuntimeContract)
	if err != nil {
		return model.SemanticEnvelopeRef{}, err
	}
	schemaRef, err := decodeDefinitionRef(wire.Schema, "schema", model.DefinitionLifecycleSchema)
	if err != nil {
		return model.SemanticEnvelopeRef{}, err
	}
	metamodelRef, err := decodeDefinitionRef(wire.Metamodel, "metamodel", model.DefinitionMetamodel)
	if err != nil {
		return model.SemanticEnvelopeRef{}, err
	}
	interpreterRef, err := decodeDefinitionRef(wire.Interpreter, "interpreter", model.DefinitionInterpreter)
	if err != nil {
		return model.SemanticEnvelopeRef{}, err
	}
	implementationRef, err := decodeDefinitionRef(wire.Implementation, "implementation", model.DefinitionEpochImplementation)
	if err != nil {
		return model.SemanticEnvelopeRef{}, err
	}
	return model.SemanticEnvelopeRef{
		Runtime:        model.RuntimeContractDefinitionRef{Definition: runtimeRef},
		Schema:         model.LifecycleSchemaDefinitionRef{Definition: schemaRef},
		Metamodel:      model.LifecycleMetamodelRef{Definition: metamodelRef},
		Interpreter:    model.InterpreterDefinitionRef{Definition: interpreterRef},
		Implementation: model.EpochImplementationRef{Definition: implementationRef},
	}, nil
}

type interpretedPayloadV3 struct {
	Semantic        runtime.EventSemantic   `json:"semantic"`
	Identities      []interpretedIdentity   `json:"identities"`
	UnresolvedFacts []interpretedUnresolved `json:"unresolved_facts"`
	Contract        ir.RuntimeContractID    `json:"contract"`
	Metamodel       interpretedMetamodel    `json:"manifest"`
	Envelope        interpretedEnvelopeWire `json:"envelope"`
}

// DecodeInterpretedV3 strictly decodes canonical interpreted.v3 evidence,
// returning a model record that carries the cited complete semantic envelope.
// Historical v1/v2 decoders never see this kind, and this decoder never sees
// theirs: a row whose kind is v3 but whose envelope is incomplete is corrupt,
// not legacy.
func DecodeInterpretedV3(id model.InterpretationID, occurrence model.OccurrenceID, payload []byte) (model.InterpretedRecord, error) {
	if err := rejectDuplicateJSONMembers(payload); err != nil {
		return model.InterpretedRecord{}, fmt.Errorf("decode interpreted.v3 lifecycle evidence: %w", err)
	}
	var wire interpretedPayloadV3
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return model.InterpretedRecord{}, fmt.Errorf("decode interpreted.v3 lifecycle evidence: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return model.InterpretedRecord{}, err
	}
	identities, unresolved, err := decodeInterpretedArms(wire.Identities, wire.UnresolvedFacts)
	if err != nil {
		return model.InterpretedRecord{}, err
	}
	book, err := decodeMetamodelManifest(wire.Metamodel)
	if err != nil {
		return model.InterpretedRecord{}, err
	}
	envelope, err := decodeSemanticEnvelope(wire.Envelope)
	if err != nil {
		return model.InterpretedRecord{}, err
	}
	record, err := model.NewInterpretedRecordWithEnvelope(id, occurrence, wire.Semantic, identities, unresolved, wire.Contract, book, envelope)
	if err != nil {
		return model.InterpretedRecord{}, err
	}
	canonical, err := canonicalV3Payload(Record{semantic: wire.Semantic, identities: identities, unresolved: unresolved, contract: wire.Contract, metamodel: book, constructed: true}, envelope)
	if err := requireCanonicalMatch(canonical, err, payload, interpretedKindV3); err != nil {
		return model.InterpretedRecord{}, err
	}
	return record, nil
}

// validateInterpretedV3Payload validates a finalized interpreted.v3 effect
// payload as it flows through the receipt commit boundary.
func validateInterpretedV3Payload(payload []byte) error {
	_, err := DecodeInterpretedV3(model.InterpretationID(1), model.OccurrenceID(1), payload)
	return err
}

func canonicalV3Payload(r Record, envelope model.SemanticEnvelopeRef) ([]byte, error) {
	identities, unresolved := interpretedArms(r)
	return encodeCanonical(interpretedPayloadV3{
		Semantic:        r.semantic,
		Identities:      identities,
		UnresolvedFacts: unresolved,
		Contract:        r.contract,
		Metamodel: interpretedMetamodel{
			ID:      string(r.metamodel.ID),
			Version: r.metamodel.Version,
			Content: hex.EncodeToString(r.metamodel.Content[:]),
		},
		Envelope: encodeSemanticEnvelope(envelope),
	})
}

// finalizeInterpretedV3 is the SOLE interpreted.v3 emitter. It binds one
// constructor-owned prepared interpretation (verified L2 semantic arms, the
// producer contract and manifest, no journal refs) to its owner-resolved
// complete semantic envelope. The prepared record never carries refs and the
// envelope never carries semantics: fabrication in either direction is
// unrepresentable. Callers must have ensured and validated every ref through
// the definition owners before finalizing.
func finalizeInterpretedV3(prepared Record, envelope model.SemanticEnvelopeRef) (provenance.Effect, error) {
	const where = "Finalizing an interpreted.v3 record (internal/lifecycle/receipt/interpreted_v3.go in receipt.finalizeInterpretedV3)."
	if !prepared.IsValid() {
		return provenance.Effect{}, finalizedError(where, "The prepared interpretation is invalid.", "Only a constructor-built record carries verified semantic arms and its producer coordinate.", "No interpreted evidence was finalized.", "Build the prepared value through receipt.NewInterpreted over a verified L2.", nil)
	}
	if err := model.ValidateSemanticEnvelope(envelope); err != nil {
		return provenance.Effect{}, finalizedError(where, "The semantic envelope is incomplete.", err.Error(), "No interpreted evidence was finalized.", "Resolve all five owner refs and prove the three shared refs equal before finalizing.", nil)
	}
	payload, err := canonicalV3Payload(prepared, envelope)
	if err != nil {
		return provenance.Effect{}, finalizedError(where, "The finalized interpreted.v3 payload could not be encoded.", "The verified arms, contract, manifest and envelope failed canonical JSON encoding, which indicates an internal contract defect.", "No interpreted evidence was finalized.", "Report the incompatible prepared shape and retry only after correcting it.", err)
	}
	sum := sha256.Sum256(payload)
	return provenance.Effect{
		Sort:          provenance.EffectEvidence,
		ResultSlot:    interpretedSlot,
		EvidenceKind:  interpretedKindV3,
		ContentDigest: append([]byte(nil), sum[:]...),
		Payload:       append(json.RawMessage(nil), payload...),
	}, nil
}

func finalizedError(where, what, why, impact, fix string, cause error) error {
	return structured(pasterrors.CategoryValidation, what, why, where, impact, fix, cause)
}
