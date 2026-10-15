package definitions

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/lifecycle/waist"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// InterpreterDocumentVersion is the explicit format/content revision of the
// interpreter document. A body change for the stable interpreter ID requires
// a bump, enforced by the committed version ledger fixture.
const InterpreterDocumentVersion uint32 = 1

// InterpreterDocumentID is the stable definition identity of the interpreter
// document. It does not change across versions and never selects whatever
// interpreter happens to be marked active in a store.
const InterpreterDocumentID model.DefinitionID = "pasture.lifecycle.interpreter"

type interpreterDocument struct {
	Kind          model.DefinitionKind     `json:"kind"`
	ID            model.DefinitionID       `json:"id"`
	Version       uint32                   `json:"version"`
	Codec         model.DefinitionCodec    `json:"codec"`
	Specification InterpreterSpecification `json:"specification"`
}

// InterpreterStage is one stage of the verified interpretation contract: the
// owning package, its entry symbol, and the value it produces. The stages run
// in order; no stage reads the journal and no stage invents journal refs.
type InterpreterStage struct {
	Stage  string `json:"stage"`
	Owner  string `json:"owner"`
	Entry  string `json:"entry"`
	Input  string `json:"input"`
	Output string `json:"output"`
}

// InterpreterSpecification is the shared pure descriptor of the compiled
// interpretation contract: the verified-L2/transform/record pipeline, the
// closed semantic enums with their identity bounds, and the explicit source
// inventory the producing entry is compiled from. It is not the metamodel
// vocabulary: the vocabulary is an input coordinate the pipeline stamps, and
// its owner tracks it separately.
type InterpreterSpecification struct {
	// Pipeline is the ordered interpretation contract from verified event to
	// durable record.
	Pipeline []InterpreterStage `json:"pipeline"`
	// Semantics are the closed event meanings the transform discriminates.
	Semantics []string `json:"semantics"`
	// Terminals maps each non-observation semantic to the legalization
	// terminal the transform produces. Observation needs no terminal.
	Terminals map[string]string `json:"terminals"`
	// UnresolvedReasons are the closed correlation-gap reasons.
	UnresolvedReasons []string `json:"unresolved_reasons"`
	// IdentityKinds are the native correlation kinds interpretation may
	// carry, in canonical order.
	IdentityKinds []string `json:"identity_kinds"`
	// IdentityBounds bounds interpretation identity values.
	IdentityBounds ByteBounds `json:"identity_bounds"`
	// DecisionKinds are the consultation decision vocabulary shared by the
	// evaluator, the durable consultation, and the backend.
	DecisionKinds []string `json:"decision_kinds"`
	// RecordKinds are the durable interpreted evidence kinds: v1 is read-only
	// legacy without a coordinate, v2 carries the producing manifest, and v3
	// carries the complete five-reference semantic envelope.
	RecordKinds []string `json:"record_kinds"`
	// VocabularyRule states how the interpretation vocabulary travels: every
	// produced record stamps the manifest coordinate it was interpreted
	// against; readers resolve meaning to the journaled vocabulary, never to
	// the current binary's belief.
	VocabularyRule string `json:"vocabulary_rule"`
	// Sources is the compiled interpretation source manifest: every
	// production interpretation source with its generation-time content hash,
	// sorted by path.
	Sources []SourceEntry `json:"sources"`
}

// InterpreterSpecificationForBinary is the pure owner descriptor used by both
// the document generator and the interpretation agreement checks. The source
// manifest comes from the committed generated inventory, never from a runtime
// filesystem read.
func InterpreterSpecificationForBinary() (InterpreterSpecification, error) {
	sources, err := InterpreterSourceInventory()
	if err != nil {
		return InterpreterSpecification{}, err
	}
	return AssembleInterpreterSpecification(sources)
}

// AssembleInterpreterSpecification builds the interpreter specification around
// one source manifest. Explicit generation supplies the freshly hashed
// manifest; the binary variant supplies the committed embedded one.
func AssembleInterpreterSpecification(sources []SourceEntry) (InterpreterSpecification, error) {
	return InterpreterSpecification{
		Pipeline: []InterpreterStage{
			{Stage: "verify", Owner: "internal/lifecycle/waist", Entry: "EventBinding.NewEvent", Input: "L1 native identities with bound mapping", Output: "verified L2 Event (Semantics plus Origin)"},
			{Stage: "transform", Owner: "internal/lifecycle/legalize", Entry: "Event", Input: "verified L2 Event", Output: "semantic terminal (observation passthrough, gate-consultation Legalized, explicit-human-response NoAuthority)"},
			{Stage: "derive", Owner: "internal/lifecycle/middleend", Entry: "Derive", Input: "verified L2 Event with producing manifest", Output: "canonical interpreted effect with optional consultation and host response"},
			{Stage: "record", Owner: "internal/lifecycle/receipt", Entry: "NewInterpreted", Input: "verified L2 Event with contract and manifest", Output: "constructor-owned interpreted.v2 record effect"},
			{Stage: "type", Owner: "internal/lifecycle/model", Entry: "NewInterpretedRecord", Input: "interpretation identity with semantic arms", Output: "immutable interpreted record (WithMetamodel for v2)"},
		},
		Semantics:         []string{"observation", "gate-consultation", "explicit-human-response"},
		Terminals:         map[string]string{"gate-consultation": "Legalized", "explicit-human-response": "NoAuthority"},
		UnresolvedReasons: []string{"tool-call-unresolved"},
		IdentityKinds:     []string{"session", "turn", "request", "tool-call", "agent", "message"},
		IdentityBounds:    ByteBounds{Min: 1, Max: 512},
		DecisionKinds:     []string{"proceed", "deny", "require-human"},
		RecordKinds:       []string{"pasture.lifecycle.interpreted.v1", "pasture.lifecycle.interpreted.v2", "pasture.lifecycle.interpreted.v3"},
		VocabularyRule:    "every produced record stamps its producing manifest; readers resolve to the journaled vocabulary",
		Sources:           sources,
	}, nil
}

// GenerateInterpreterBody is the production owner codec used by explicit
// generation. It never reads source files or a store at runtime.
func GenerateInterpreterBody() ([]byte, error) {
	spec, err := InterpreterSpecificationForBinary()
	if err != nil {
		return nil, err
	}
	return AssembleInterpreterBody(spec)
}

// AssembleInterpreterBody encodes one interpreter specification as the
// canonical owner body. The generator and the round-trip checks share this
// assembler.
func AssembleInterpreterBody(spec InterpreterSpecification) ([]byte, error) {
	raw, err := marshalDocument(interpreterDocument{model.DefinitionInterpreter, InterpreterDocumentID, InterpreterDocumentVersion, model.DefinitionCanonicalJSON, spec})
	if err != nil {
		return nil, failure("AssembleInterpreterBody", "The typed interpreter specification could not be encoded.", err)
	}
	return NormalizePayload(EvidenceKind, raw)
}

// NewInterpreter returns the compiled interpreter owner document from its
// generated body. Historical bodies remain decodable; only this constructor
// provides a document the activation writer accepts.
func NewInterpreter() (Document, error) {
	body, err := generatedBody("generated/interpreter.json")
	if err != nil {
		return Document{}, err
	}
	document, err := decodeInterpreter(body)
	if err != nil {
		return Document{}, err
	}
	if document.ID() != InterpreterDocumentID {
		return Document{}, failure("NewInterpreter", "The embedded document does not identify the interpreter; regenerate the interpreter document.", nil)
	}
	document.ownerBuilt = true
	return document, nil
}

// ParseInterpreterSpecification strictly decodes one schema owner body and
// returns its shared specification. Readers and agreement checks consume the
// specification through this parser, never by reimplementing the codec.
func ParseInterpreterSpecification(body []byte) (InterpreterSpecification, error) {
	var wire interpreterDocument
	if err := strictDecode(body, &wire); err != nil {
		return InterpreterSpecification{}, failure("ParseInterpreterSpecification", "The typed interpreter document contains malformed, duplicate or unknown fields.", err)
	}
	doc, err := decodeInterpreter(body)
	if err != nil {
		return InterpreterSpecification{}, err
	}
	if doc.ID() != InterpreterDocumentID {
		return InterpreterSpecification{}, failure("ParseInterpreterSpecification", "The document does not identify the interpreter owner.", nil)
	}
	return wire.Specification, nil
}

func decodeInterpreter(payload []byte) (Document, error) {
	var wire interpreterDocument
	if err := strictDecode(payload, &wire); err != nil {
		return Document{}, failure("decodeInterpreter", "The typed interpreter document contains malformed, duplicate or unknown fields.", err)
	}
	// Version 1 is a historical codec arm, independent of the version emitted
	// by today's generator. Add a new arm rather than replacing this decoder.
	switch wire.Version {
	case 1:
	default:
		return Document{}, failure("decodeInterpreter", "The interpreter document version has no supported historical codec.", nil)
	}
	if wire.Kind != model.DefinitionInterpreter || wire.Version != 1 || wire.Codec != model.DefinitionCanonicalJSON || wire.ID != InterpreterDocumentID {
		return Document{}, failure("decodeInterpreter", "Interpreter document kind, owner identity, codec or version is invalid.", fmt.Errorf("kind=%d version=%d codec=%d id=%q", wire.Kind, wire.Version, wire.Codec, wire.ID))
	}
	if err := validateInterpreterSpecification(wire.Specification); err != nil {
		return Document{}, failure("decodeInterpreter", "The interpreter descriptor violates its structural contract.", err)
	}
	normalized, err := NormalizePayload(EvidenceKind, payload)
	if err != nil {
		return Document{}, err
	}
	second, err := NormalizePayload(EvidenceKind, normalized)
	if err != nil || !bytes.Equal(normalized, second) {
		return Document{}, failure("decodeInterpreter", "Journal normalization is not idempotent.", err)
	}
	return Document{kind: wire.Kind, id: wire.ID, version: wire.Version, codec: wire.Codec, body: normalized, content: model.ContentIdentity(sha256.Sum256(normalized))}, nil
}

func validateInterpreterSpecification(spec InterpreterSpecification) error {
	if len(spec.Pipeline) != 5 {
		return fmt.Errorf("interpreter descriptor pipeline is not the verified five-stage contract")
	}
	wantStages := []string{"verify", "transform", "derive", "record", "type"}
	for index, stage := range spec.Pipeline {
		if stage.Stage != wantStages[index] || stage.Owner == "" || stage.Entry == "" || stage.Input == "" || stage.Output == "" {
			return fmt.Errorf("interpreter descriptor pipeline stage %d is not the reviewed contract", index)
		}
	}
	// Every semantic must be a live runtime meaning, and together they must be
	// the complete closed set; a renamed semantic fails against the runtime
	// owner rather than a copied literal.
	var live []string
	for semantic := runtime.SemanticObservation; semantic.IsValid(); semantic++ {
		live = append(live, semantic.String())
		if semantic == 255 {
			break
		}
	}
	if !slices.Equal(spec.Semantics, live) {
		return fmt.Errorf("interpreter descriptor semantics are not the closed runtime set")
	}
	for semantic, terminal := range spec.Terminals {
		if !slices.Contains(spec.Semantics, semantic) || terminal == "" {
			return fmt.Errorf("interpreter descriptor terminal for %q is not a described semantic", semantic)
		}
	}
	// The unresolved vocabulary is closed to the single waist reason; the
	// waist owns the set.
	if !slices.Equal(spec.UnresolvedReasons, []string{waist.UnresolvedToolCall.String()}) {
		return fmt.Errorf("interpreter descriptor unresolved reasons are not the closed waist set")
	}
	var liveIdentities []string
	for kind := runtime.IdentitySession; kind.IsValid(); kind++ {
		liveIdentities = append(liveIdentities, kind.String())
	}
	if !slices.Equal(spec.IdentityKinds, liveIdentities) {
		return fmt.Errorf("interpreter descriptor identity kinds are not the closed runtime set")
	}
	if spec.IdentityBounds.Min != 1 || spec.IdentityBounds.Max != 512 {
		return fmt.Errorf("interpreter descriptor identity bounds are not the enforced window")
	}
	var liveDecisions []string
	for decision := waist.DecisionProceed; decision.IsValid(); decision++ {
		liveDecisions = append(liveDecisions, decision.String())
	}
	if !slices.Equal(spec.DecisionKinds, liveDecisions) {
		return fmt.Errorf("interpreter descriptor decision kinds are not the closed waist set")
	}
	if len(spec.RecordKinds) == 0 || spec.VocabularyRule == "" {
		return fmt.Errorf("interpreter descriptor record kinds or vocabulary rule is empty")
	}
	return validateSources("interpreter", spec.Sources)
}

// InterpreterSourceInventory returns the committed generation-time
// interpretation source manifest. It reads the checked-in inventory, never
// the filesystem.
func InterpreterSourceInventory() ([]SourceEntry, error) {
	return readSourceInventory("generated/interpreter-sources.json")
}
