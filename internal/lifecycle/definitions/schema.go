package definitions

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// SchemaDocumentVersion is the explicit format/content revision of the lifecycle
// schema document. It is not the host version, the release number, or any store
// version. A body change for the stable schema ID requires a bump, enforced by
// the committed version ledger fixture.
const SchemaDocumentVersion uint32 = 1

// SchemaDocumentID is the stable definition identity of the lifecycle schema
// document. It does not change across versions.
const SchemaDocumentID model.DefinitionID = "pasture.lifecycle.occurrence-schema"

type schemaDocument struct {
	Kind          model.DefinitionKind  `json:"kind"`
	ID            model.DefinitionID    `json:"id"`
	Version       uint32                `json:"version"`
	Codec         model.DefinitionCodec `json:"codec"`
	Specification SchemaSpecification   `json:"specification"`
}

// SchemaSpecification is the shared pure descriptor of the durable lifecycle
// codec contract. It is consumed by the document generator, the owner agreement
// checks, and the dual-envelope readers; no caller invokes generation at
// runtime to obtain it.
//
// The contract covers the wire evidence kinds the journal actually emits, the
// two envelope reference sets with their pairing rule, and the value bounds the
// codecs enforce. Host payload field catalogs remain runtime-contract data;
// this is not a host JSON schema and not the interpretation vocabulary.
type SchemaSpecification struct {
	// OccurrenceKinds are the durable occurrence evidence kinds the receipt
	// writer emits. v1 is read-only legacy without refs; v2 carries the
	// complete four-reference envelope and is the only kind new writes emit.
	OccurrenceKinds []string `json:"occurrence_kinds"`
	// InterpretedKinds are the durable interpreted evidence kinds. v1 is
	// read-only legacy without a metamodel coordinate; v2 carries one; v3
	// carries the complete five-reference semantic envelope.
	InterpretedKinds []string `json:"interpreted_kinds"`
	// DefinitionKinds are the durable definition snapshot evidence kinds for
	// the legacy metamodel writer (v1) and the five-kind writer (v2).
	DefinitionKinds []string `json:"definition_kinds"`
	// DefinitionStateKinds are the durable activation-state evidence kinds
	// paired with the snapshot kinds above.
	DefinitionStateKinds []string `json:"definition_state_kinds"`
	// DefinitionSlots are the ordered result slots every definition activation
	// commits, in commit order.
	DefinitionSlots []string `json:"definition_slots"`
	// OccurrenceEnvelope is the complete four-reference occurrence envelope:
	// definition kind per slot name.
	OccurrenceEnvelope map[string]model.DefinitionKind `json:"occurrence_envelope"`
	// SemanticEnvelope is the complete five-reference semantic envelope:
	// definition kind per slot name. The runtime, schema and implementation
	// slots are shared with the occurrence envelope and must be exactly equal
	// across the pair; no ref may be zero, dangling, or content-mismatched.
	SemanticEnvelope map[string]model.DefinitionKind `json:"semantic_envelope"`
	// SharedEnvelopeSlots names the slots the two envelopes share. A paired
	// occurrence/interpretation is admitted only when these refs are exactly
	// equal; refs that each resolve separately but differ fail the pair.
	SharedEnvelopeSlots []string `json:"shared_envelope_slots"`
	// BindingKinds are the native correlation binding kinds an occurrence may
	// carry, in canonical order.
	BindingKinds []string `json:"binding_kinds"`
	// BindingValueBytes bounds occurrence binding text: 1 through 512 bytes of
	// valid UTF-8 with no surrounding padding and no control characters.
	BindingValueBytes ByteBounds `json:"binding_value_bytes"`
	// IdentityValueBytes bounds interpretation identity values: at most 512
	// bytes with the same text discipline as bindings.
	IdentityValueBytes ByteBounds `json:"identity_value_bytes"`
	// PayloadDigest names the content-address algorithm for evidence payload
	// blobs. The blob write precedes the journal append, so an orphan blob is
	// reclaimable while a journal row naming an absent blob is corruption.
	PayloadDigest string `json:"payload_digest"`
	// CaptureDispositions are the numeric capture dispositions a receipt may
	// record: valid plus every non-valid path, all of which still journal a
	// complete occurrence envelope and never fabricate an interpretation.
	CaptureDispositions []uint8 `json:"capture_dispositions"`
	// CaptureOrigins are the provenance origins an envelope may record. Origin
	// is provenance-only; the write gate never observes it.
	CaptureOrigins []string `json:"capture_origins"`
	// HostVersionSources are the closed provenance sources for the observed
	// host version, which is provenance rather than an admission check.
	HostVersionSources []string `json:"host_version_sources"`
	// RuntimeContractRule states the contract pairing the codecs enforce: the
	// envelope runtime ref resolves to the runtime body whose contract equals
	// the delivery contract and the occurrence codec's contract.
	RuntimeContractRule string `json:"runtime_contract_rule"`
}

// ByteBounds is an inclusive byte-length bound for codec-validated text.
type ByteBounds struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// SchemaSpecificationForBinary is the pure owner descriptor used by both the
// document generator and the compiled-codec agreement checks. Every literal
// below names a value the production codecs enforce; the agreement checks
// prove each one against its live owner, so drift fails loudly instead of
// rotting.
func SchemaSpecificationForBinary() (SchemaSpecification, error) {
	return SchemaSpecification{
		OccurrenceKinds:      []string{"pasture.lifecycle.occurrence.v1", "pasture.lifecycle.occurrence.v2"},
		InterpretedKinds:     []string{"pasture.lifecycle.interpreted.v1", "pasture.lifecycle.interpreted.v2", "pasture.lifecycle.interpreted.v3"},
		DefinitionKinds:      []string{"pasture.lifecycle.definition.v1", "pasture.lifecycle.definition.v2"},
		DefinitionStateKinds: []string{"pasture.lifecycle.definition-state.v1", "pasture.lifecycle.definition-state.v2"},
		DefinitionSlots:      []string{"definition", "definition-state"},
		OccurrenceEnvelope: map[string]model.DefinitionKind{
			"runtime": model.DefinitionRuntimeContract, "schema": model.DefinitionLifecycleSchema,
			"implementation": model.DefinitionEpochImplementation, "retention": model.DefinitionRetentionPolicy,
		},
		SemanticEnvelope: map[string]model.DefinitionKind{
			"runtime": model.DefinitionRuntimeContract, "schema": model.DefinitionLifecycleSchema,
			"metamodel": model.DefinitionMetamodel, "interpreter": model.DefinitionInterpreter,
			"implementation": model.DefinitionEpochImplementation,
		},
		SharedEnvelopeSlots: []string{"runtime", "schema", "implementation"},
		BindingKinds:        []string{"session", "turn", "request", "tool-call", "agent", "message", "task", "worktree"},
		BindingValueBytes:   ByteBounds{Min: 1, Max: 512},
		IdentityValueBytes:  ByteBounds{Min: 1, Max: 512},
		PayloadDigest:       "sha256",
		CaptureDispositions: []uint8{1, 2, 3, 4, 5, 6, 7, 8},
		CaptureOrigins:      []string{"authentic-capture", "pinned-contract", "review-finding", "authored", "raw"},
		HostVersionSources:  []string{"caller-supplied", "executable-query"},
		RuntimeContractRule: "envelope runtime ref resolves to the runtime body whose contract equals the delivery contract",
	}, nil
}

// GenerateSchemaBody is the production owner codec used by explicit
// generation. It never reads a store at runtime.
func GenerateSchemaBody() ([]byte, error) {
	spec, err := SchemaSpecificationForBinary()
	if err != nil {
		return nil, err
	}
	return AssembleSchemaBody(spec)
}

// AssembleSchemaBody encodes one schema specification as the canonical owner
// body. The generator and the round-trip checks share this assembler so the
// committed bytes are always exactly what the descriptor describes.
func AssembleSchemaBody(spec SchemaSpecification) ([]byte, error) {
	raw, err := marshalDocument(schemaDocument{model.DefinitionLifecycleSchema, SchemaDocumentID, SchemaDocumentVersion, model.DefinitionCanonicalJSON, spec})
	if err != nil {
		return nil, failure("AssembleSchemaBody", "The typed schema specification could not be encoded.", err)
	}
	return NormalizePayload(EvidenceKind, raw)
}

// NewLifecycleSchema returns the compiled schema owner document from its
// generated body. Historical bodies remain decodable; only this constructor
// provides a document the activation writer accepts.
func NewLifecycleSchema() (Document, error) {
	body, err := generatedBody("generated/schema.json")
	if err != nil {
		return Document{}, err
	}
	document, err := decodeSchema(body)
	if err != nil {
		return Document{}, err
	}
	if document.ID() != SchemaDocumentID {
		return Document{}, failure("NewLifecycleSchema", "The embedded document does not identify the lifecycle schema; regenerate the schema document.", nil)
	}
	document.ownerBuilt = true
	return document, nil
}

// ParseSchemaSpecification strictly decodes one schema owner body and
// returns its shared specification. Readers and agreement checks consume the
// specification through this parser, never by reimplementing the codec.
func ParseSchemaSpecification(body []byte) (SchemaSpecification, error) {
	var wire schemaDocument
	if err := strictDecode(body, &wire); err != nil {
		return SchemaSpecification{}, failure("ParseSchemaSpecification", "The typed schema document contains malformed, duplicate or unknown fields.", err)
	}
	doc, err := decodeSchema(body)
	if err != nil {
		return SchemaSpecification{}, err
	}
	if doc.ID() != SchemaDocumentID {
		return SchemaSpecification{}, failure("ParseSchemaSpecification", "The document does not identify the schema owner.", nil)
	}
	return wire.Specification, nil
}

func decodeSchema(payload []byte) (Document, error) {
	var wire schemaDocument
	if err := strictDecode(payload, &wire); err != nil {
		return Document{}, failure("decodeSchema", "The typed schema document contains malformed, duplicate or unknown fields.", err)
	}
	// Version 1 is a historical codec arm, independent of the version emitted
	// by today's generator. Add a new arm rather than replacing this decoder.
	switch wire.Version {
	case 1:
	default:
		return Document{}, failure("decodeSchema", "The schema document version has no supported historical codec.", nil)
	}
	if wire.Kind != model.DefinitionLifecycleSchema || wire.Version != 1 || wire.Codec != model.DefinitionCanonicalJSON || wire.ID != SchemaDocumentID {
		return Document{}, failure("decodeSchema", "Schema document kind, owner identity, codec or version is invalid.", fmt.Errorf("kind=%d version=%d codec=%d id=%q", wire.Kind, wire.Version, wire.Codec, wire.ID))
	}
	if err := validateSchemaSpecification(wire.Specification); err != nil {
		return Document{}, failure("decodeSchema", "The schema descriptor violates its structural contract.", err)
	}
	normalized, err := NormalizePayload(EvidenceKind, payload)
	if err != nil {
		return Document{}, err
	}
	second, err := NormalizePayload(EvidenceKind, normalized)
	if err != nil || !bytes.Equal(normalized, second) {
		return Document{}, failure("decodeSchema", "Journal normalization is not idempotent.", err)
	}
	return Document{kind: wire.Kind, id: wire.ID, version: wire.Version, codec: wire.Codec, body: normalized, content: model.ContentIdentity(sha256.Sum256(normalized))}, nil
}

func validateSchemaSpecification(spec SchemaSpecification) error {
	if len(spec.OccurrenceKinds) == 0 || len(spec.InterpretedKinds) == 0 || len(spec.DefinitionKinds) == 0 || len(spec.DefinitionStateKinds) == 0 {
		return fmt.Errorf("schema descriptor names no wire evidence kinds")
	}
	if !slices.Equal(spec.DefinitionSlots, []string{"definition", "definition-state"}) {
		return fmt.Errorf("schema descriptor definition slots are not the ordered activation pair")
	}
	// The envelope slot sets are closed: exactly the occurrence four and the
	// semantic five, with the shared three present in both.
	wantOccurrence := map[string]model.DefinitionKind{
		"runtime": model.DefinitionRuntimeContract, "schema": model.DefinitionLifecycleSchema,
		"implementation": model.DefinitionEpochImplementation, "retention": model.DefinitionRetentionPolicy,
	}
	wantSemantic := map[string]model.DefinitionKind{
		"runtime": model.DefinitionRuntimeContract, "schema": model.DefinitionLifecycleSchema,
		"metamodel": model.DefinitionMetamodel, "interpreter": model.DefinitionInterpreter,
		"implementation": model.DefinitionEpochImplementation,
	}
	if len(spec.OccurrenceEnvelope) != len(wantOccurrence) || len(spec.SemanticEnvelope) != len(wantSemantic) {
		return fmt.Errorf("schema descriptor envelope sets are not the complete four/five refs")
	}
	for slot, kind := range wantOccurrence {
		if spec.OccurrenceEnvelope[slot] != kind {
			return fmt.Errorf("occurrence envelope slot %q is not its owner kind", slot)
		}
	}
	for slot, kind := range wantSemantic {
		if spec.SemanticEnvelope[slot] != kind {
			return fmt.Errorf("semantic envelope slot %q is not its owner kind", slot)
		}
	}
	if !slices.Equal(spec.SharedEnvelopeSlots, []string{"runtime", "schema", "implementation"}) {
		return fmt.Errorf("schema descriptor shared slots are not exactly the paired three")
	}
	for _, kind := range spec.BindingKinds {
		if _, err := model.ParseNativeBindingKind(kind); err != nil {
			return fmt.Errorf("schema descriptor binding kind %q is not declared: %w", kind, err)
		}
	}
	if spec.BindingValueBytes.Min != 1 || spec.BindingValueBytes.Max != 512 || spec.IdentityValueBytes.Max != 512 {
		return fmt.Errorf("schema descriptor text bounds are not the enforced 1..512 window")
	}
	for _, source := range spec.HostVersionSources {
		if err := model.ValidateHostVersionSource(model.HostVersionSource(source)); err != nil {
			return fmt.Errorf("schema descriptor host version source %q is not admitted: %w", source, err)
		}
	}
	if len(spec.CaptureDispositions) == 0 || len(spec.CaptureOrigins) == 0 || spec.PayloadDigest == "" || spec.RuntimeContractRule == "" {
		return fmt.Errorf("schema descriptor capture, origin, digest or contract rule is empty")
	}
	return nil
}
