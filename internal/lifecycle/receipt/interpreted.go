package receipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/provenance"
)

// interpretedKindV2 is the durable evidence kind for interpreted records that
// carry a codebook coordinate (D2). It is a SEPARATE kind from interpreted.v1:
// v1's strict decode never sees it, so committed v1 records decode unchanged
// with no in-place migration. Nothing writes v1 after M5.
const interpretedKindV2 = provenance.EvidenceKind("pasture.lifecycle.interpreted.v2")

// Record is the constructor-owned interpreted lifecycle evidence.
//
// The record deliberately keeps the waist's semantic vocabulary rather than
// the occurrence model's native-binding vocabulary.  That preserves the
// boundary between interpretation and raw occurrence ingestion.
//
// Since M5 a record ALWAYS carries the codebook coordinate it was interpreted
// against, and its durable form is interpreted.v2. Interpreted.v1 is a
// read-only legacy kind: committed v1 records decode unchanged through
// DecodeInterpreted, but nothing produces v1 anymore.
type Record struct {
	semantic    runtime.EventSemantic
	identities  []waist.SemanticIdentity
	unresolved  []waist.UnresolvedFact
	contract    ir.RuntimeContractID
	metamodel   model.LifecycleMetamodelManifest
	constructed bool
}

// DecodeInterpreted strictly decodes canonical interpreted.v1 evidence. It is
// the read path for committed pre-M5 records; the returned model record has no
// codebook coordinate (Codebook() reports false).
func DecodeInterpreted(id model.InterpretationID, occurrence model.OccurrenceID, payload []byte) (model.InterpretedRecord, error) {
	if err := rejectDuplicateJSONMembers(payload); err != nil {
		return model.InterpretedRecord{}, fmt.Errorf("decode interpreted lifecycle evidence: %w", err)
	}
	var wire interpretedPayload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return model.InterpretedRecord{}, fmt.Errorf("decode interpreted lifecycle evidence: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return model.InterpretedRecord{}, err
	}
	identities, unresolved, err := decodeInterpretedArms(wire.Identities, wire.UnresolvedFacts)
	if err != nil {
		return model.InterpretedRecord{}, err
	}
	record, err := model.NewInterpretedRecord(id, occurrence, wire.Semantic, identities, unresolved, wire.Contract)
	if err != nil {
		return model.InterpretedRecord{}, err
	}
	canonical, err := canonicalV1Payload(wire.Semantic, identities, unresolved, wire.Contract)
	if err := requireCanonicalMatch(canonical, err, payload, interpretedKind); err != nil {
		return model.InterpretedRecord{}, err
	}
	return record, nil
}

// DecodeInterpretedV2 strictly decodes canonical interpreted.v2 evidence,
// returning a model record that carries the cited codebook coordinate.
func DecodeInterpretedV2(id model.InterpretationID, occurrence model.OccurrenceID, payload []byte) (model.InterpretedRecord, error) {
	if err := rejectDuplicateJSONMembers(payload); err != nil {
		return model.InterpretedRecord{}, fmt.Errorf("decode interpreted.v2 lifecycle evidence: %w", err)
	}
	var wire interpretedPayloadV2
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return model.InterpretedRecord{}, fmt.Errorf("decode interpreted.v2 lifecycle evidence: %w", err)
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
	record, err := model.NewInterpretedRecordWithMetamodel(id, occurrence, wire.Semantic, identities, unresolved, wire.Contract, book)
	if err != nil {
		return model.InterpretedRecord{}, err
	}
	canonical, err := canonicalV2Payload(Record{semantic: wire.Semantic, identities: identities, unresolved: unresolved, contract: wire.Contract, metamodel: book, constructed: true})
	if err := requireCanonicalMatch(canonical, err, payload, interpretedKindV2); err != nil {
		return model.InterpretedRecord{}, err
	}
	return record, nil
}

func decodeInterpretedArms(wireIdentities []interpretedIdentity, wireUnresolved []interpretedUnresolved) ([]waist.SemanticIdentity, []waist.UnresolvedFact, error) {
	identities := make([]waist.SemanticIdentity, len(wireIdentities))
	for i, identity := range wireIdentities {
		if !identity.Kind.IsValid() || identity.Value == "" || len(identity.Value) > 512 {
			return nil, nil, fmt.Errorf("decode interpreted lifecycle evidence: invalid identity at index %d", i)
		}
		identities[i] = waist.SemanticIdentity{Kind: identity.Kind, Value: identity.Value}
	}
	unresolved := make([]waist.UnresolvedFact, len(wireUnresolved))
	for i, fact := range wireUnresolved {
		if !fact.Reason.IsValid() {
			return nil, nil, fmt.Errorf("decode interpreted lifecycle evidence: invalid unresolved fact at index %d", i)
		}
		unresolved[i] = waist.UnresolvedFact{Reason: fact.Reason}
	}
	return identities, unresolved, nil
}

func decodeMetamodelManifest(wire interpretedMetamodel) (model.LifecycleMetamodelManifest, error) {
	if len(wire.Content) != 2*sha256.Size {
		return model.LifecycleMetamodelManifest{}, fmt.Errorf("decode interpreted.v2 lifecycle evidence: codebook content is not a sha256 hex digest")
	}
	raw, err := hex.DecodeString(wire.Content)
	if err != nil {
		return model.LifecycleMetamodelManifest{}, fmt.Errorf("decode interpreted.v2 lifecycle evidence: codebook content is not hex: %w", err)
	}
	var content model.ContentIdentity
	copy(content[:], raw)
	manifest := model.LifecycleMetamodelManifest{ID: model.DefinitionID(wire.ID), Version: wire.Version, Content: content}
	if !manifest.IsValid() {
		return model.LifecycleMetamodelManifest{}, fmt.Errorf("decode interpreted.v2 lifecycle evidence: codebook coordinate is invalid")
	}
	return manifest, nil
}

// requireCanonicalMatch verifies that a decoded payload equals its canonical
// re-encoding (allowing the journal's normalized form). It preserves the strict
// canonical-equality discipline shared by both interpreted kinds.
func requireCanonicalMatch(canonical []byte, encodeErr error, payload []byte, kind provenance.EvidenceKind) error {
	normalized := canonical
	if mutation, normErr := provenance.Canonicalize(provenance.OperationInput{Effects: []provenance.Effect{{Sort: provenance.EffectEvidence, ResultSlot: interpretedSlot, EvidenceKind: kind, ContentDigest: make([]byte, sha256.Size), Payload: canonical}}}); normErr == nil {
		normalized = mutation.NormalizedEffects()[0].Payload
	}
	if encodeErr != nil || (!bytes.Equal(canonical, payload) && !bytes.Equal(normalized, payload)) {
		return fmt.Errorf("decode interpreted lifecycle evidence: payload is not canonical compact JSON")
	}
	return nil
}

// validateInterpretedPayload validates a durable interpreted.v2 extra effect
// payload as it flows through receipt.Service.Receive.
func validateInterpretedPayload(payload []byte) error {
	_, err := DecodeInterpretedV2(model.InterpretationID(1), model.OccurrenceID(1), payload)
	return err
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decode interpreted lifecycle evidence: trailing JSON value")
	}
	return nil
}

func rejectDuplicateJSONMembers(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key := keyToken.(string)
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate JSON member %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	if err := walk(); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

// NewInterpreted constructs an interpreted.v2 record from a verified L2 value
// and the codebook coordinate it was interpreted against. Since M5 the
// coordinate is required: the record binds interpretation to a versioned,
// journaled codebook, and an invalid coordinate is refused before any evidence
// is constructed.
func NewInterpreted(l2 waist.L2, contract ir.RuntimeContractID, manifest model.LifecycleMetamodelManifest) (Record, error) {
	const where = "Constructing an interpreted lifecycle record (internal/lifecycle/receipt/interpreted.go in receipt.NewInterpreted)."
	if !l2.IsValid() || !l2.Semantics().IsValid() || !l2.Origin().IsValid() {
		return Record{}, structured(
			pasterrors.CategoryValidation,
			"The interpreted lifecycle record source is invalid.",
			"Only a constructor-built waist L2 carries a verified semantic arm, origin, and bounded correlation values.",
			where,
			"No interpreted evidence was constructed.",
			"Build L2 through waist.BindEvent followed by EventBinding.NewEvent before creating the interpreted record.",
			nil,
		)
	}
	if !contract.IsValid() {
		return Record{}, structured(
			pasterrors.CategoryValidation,
			"The interpreted lifecycle record has an invalid runtime contract.",
			"Durable interpretation must identify the version-bounded runtime contract that produced the L2 value.",
			where,
			"No interpreted evidence was constructed.",
			"Pass the constructor-produced contract ID from the same pinned lifecycle profile as the L2 value.",
			nil,
		)
	}
	originContract := l2.Origin().Contract()
	if originContract != contract {
		return Record{}, structured(
			pasterrors.CategoryValidation,
			fmt.Sprintf("The interpreted lifecycle record contract %q does not match the L2 origin contract %q.", contract, originContract),
			"The redundant contract argument must agree with the contract pinned into L2; otherwise evidence could be attributed to a different runtime profile.",
			where,
			"No interpreted evidence was constructed.",
			"Pass l2.Origin().Contract() unchanged as the record contract.",
			nil,
		)
	}
	if !manifest.IsValid() {
		return Record{}, structured(
			pasterrors.CategoryValidation,
			"The interpreted lifecycle record has an invalid codebook coordinate.",
			"Since M5 every interpretation cites the versioned, content-addressed codebook it was produced against, so the coordinate cannot be zero.",
			where,
			"No interpreted evidence was constructed.",
			"Pass metamodel.Active() as the interpretation coordinate.",
			nil,
		)
	}

	semantics := l2.Semantics()
	return Record{
		semantic:    semantics.Semantic(),
		identities:  semantics.Identities(),
		unresolved:  semantics.UnresolvedFacts(),
		contract:    contract,
		metamodel:   manifest,
		constructed: true,
	}, nil
}

// Semantic returns the interpreted lifecycle arm.
func (r Record) Semantic() runtime.EventSemantic { return r.semantic }

// Identities returns a defensive copy of the interpreted waist identities.
func (r Record) Identities() []waist.SemanticIdentity {
	return append([]waist.SemanticIdentity(nil), r.identities...)
}

// UnresolvedFacts returns a defensive copy of the known correlation gaps.
func (r Record) UnresolvedFacts() []waist.UnresolvedFact {
	return append([]waist.UnresolvedFact(nil), r.unresolved...)
}

// Contract returns the pinned runtime contract that produced the record.
func (r Record) Contract() ir.RuntimeContractID { return r.contract }

// Metamodel returns the codebook coordinate this interpretation cites.
func (r Record) Metamodel() model.LifecycleMetamodelManifest { return r.metamodel }

// IsValid reports whether the record was constructed by NewInterpreted.
func (r Record) IsValid() bool { return r.constructed }

// Effect converts the interpreted record into its durable interpreted.v2
// evidence effect.
func (r Record) Effect() provenance.Effect {
	if !r.IsValid() {
		return provenance.Effect{}
	}
	payload, err := canonicalV2Payload(r)
	if err != nil {
		return provenance.Effect{}
	}
	digest := sha256.Sum256(payload)
	return provenance.Effect{
		Sort:          provenance.EffectEvidence,
		ResultSlot:    interpretedSlot,
		EvidenceKind:  interpretedKindV2,
		ContentDigest: append([]byte(nil), digest[:]...),
		Payload:       append(json.RawMessage(nil), payload...),
	}
}

type interpretedPayload struct {
	Semantic        runtime.EventSemantic   `json:"semantic"`
	Identities      []interpretedIdentity   `json:"identities"`
	UnresolvedFacts []interpretedUnresolved `json:"unresolved_facts"`
	Contract        ir.RuntimeContractID    `json:"contract"`
}

type interpretedPayloadV2 struct {
	Semantic        runtime.EventSemantic   `json:"semantic"`
	Identities      []interpretedIdentity   `json:"identities"`
	UnresolvedFacts []interpretedUnresolved `json:"unresolved_facts"`
	Contract        ir.RuntimeContractID    `json:"contract"`
	Metamodel       interpretedMetamodel    `json:"codebook"`
}

type interpretedMetamodel struct {
	ID      string `json:"id"`
	Version uint32 `json:"version"`
	Content string `json:"content"`
}

type interpretedIdentity struct {
	Kind  runtime.NativeIdentityKind `json:"kind"`
	Value string                     `json:"value"`
}

type interpretedUnresolved struct {
	Reason waist.UnresolvedReason `json:"reason"`
}

func interpretedArms(r Record) ([]interpretedIdentity, []interpretedUnresolved) {
	identities := make([]interpretedIdentity, len(r.identities))
	for index, identity := range r.identities {
		identities[index] = interpretedIdentity{Kind: identity.Kind, Value: identity.Value}
	}
	unresolved := make([]interpretedUnresolved, len(r.unresolved))
	for index, fact := range r.unresolved {
		unresolved[index] = interpretedUnresolved{Reason: fact.Reason}
	}
	return identities, unresolved
}

func canonicalV1Payload(semantic runtime.EventSemantic, semanticIdentities []waist.SemanticIdentity, semanticUnresolved []waist.UnresolvedFact, contract ir.RuntimeContractID) ([]byte, error) {
	identities, unresolved := interpretedArms(Record{identities: semanticIdentities, unresolved: semanticUnresolved})
	return encodeCanonical(interpretedPayload{
		Semantic:        semantic,
		Identities:      identities,
		UnresolvedFacts: unresolved,
		Contract:        contract,
	})
}

func canonicalV2Payload(r Record) ([]byte, error) {
	identities, unresolved := interpretedArms(r)
	return encodeCanonical(interpretedPayloadV2{
		Semantic:        r.semantic,
		Identities:      identities,
		UnresolvedFacts: unresolved,
		Contract:        r.contract,
		Metamodel: interpretedMetamodel{
			ID:      string(r.metamodel.ID),
			Version: r.metamodel.Version,
			Content: hex.EncodeToString(r.metamodel.Content[:]),
		},
	})
}

func encodeCanonical(payload any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, fmt.Errorf("encode interpreted lifecycle evidence: %w", err)
	}
	return append([]byte(nil), bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})...), nil
}
