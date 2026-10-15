package definitions

import (
	"bytes"
	"crypto/sha256"
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// RetentionDocumentVersion is the explicit format/content revision of the
// retention policy document. A body change for the stable store-all ID
// requires a bump, enforced by the committed version ledger fixture.
const RetentionDocumentVersion uint32 = 1

// RetentionDocumentID is the stable definition identity of the store-all
// retention policy document. It does not change across versions, and no
// alternate retention mode exists.
const RetentionDocumentID model.DefinitionID = "pasture.lifecycle.retention.store-all"

type retentionDocument struct {
	Kind          model.DefinitionKind   `json:"kind"`
	ID            model.DefinitionID     `json:"id"`
	Version       uint32                 `json:"version"`
	Codec         model.DefinitionCodec  `json:"codec"`
	Specification RetentionSpecification `json:"specification"`
}

// RetentionSpecification is the shared pure descriptor of the store-all SQLite
// retention policy. It is consumed by the document generator and the owner
// agreement checks; the injected writer window is described by its timeout-tier
// rule, never by a guessed wall-clock value.
type RetentionSpecification struct {
	// Policy names the single retention mode. Committed named bodies are
	// retained; there is no age- or size-bounded dropping of journal rows.
	Policy string `json:"policy"`
	// CommittedNamedBodies states the fate of payload blobs a recorded
	// occurrence names: retained, never reclaimed.
	CommittedNamedBodies string `json:"committed_named_bodies"`
	// OrphanEligibility states when an unnamed payload blob may be reclaimed:
	// unreferenced by any recorded occurrence, strictly older than the snapshot
	// instant minus the writer window, and of known age.
	OrphanEligibility string `json:"orphan_eligibility"`
	// UnknownAge states the fate of a blob with an unknown written-at stamp:
	// never eligible, whatever the snapshot instant.
	UnknownAge string `json:"unknown_age"`
	// ReadCap bounds how many orphan blobs one projection rebuild reclaims. It
	// sizes a read command's added latency, not a host budget.
	ReadCap int `json:"read_cap"`
	// PruneBatchCap bounds how many orphan blobs one operator prune batch
	// deletes. Each batch is its own short write transaction.
	PruneBatchCap int `json:"prune_batch_cap"`
	// WindowRule names the writer window the reclaim ages against: the
	// WorkflowResult tier of the injected timeout profile, or the operator's
	// retention age when larger. The window is a floor and never a ceiling.
	WindowRule string `json:"window_rule"`
	// WriteOrder states the deliberate blob/journal order: the payload blob is
	// written before the journal row that names it, so an orphan blob is
	// reclaimable while a journal row naming an absent blob is corruption.
	WriteOrder string `json:"write_order"`
	// Digest names the content-address algorithm for payload blobs.
	Digest string `json:"digest"`
	// NewestRow states the operator prune's one exclusion: the row at the
	// greatest written-at is never deleted.
	NewestRow string `json:"newest_row"`
}

// RetentionSpecificationForBinary is the pure owner descriptor used by both the
// document generator and the reclaim-policy agreement checks.
func RetentionSpecificationForBinary() (RetentionSpecification, error) {
	return RetentionSpecification{
		Policy:               "store-all",
		CommittedNamedBodies: "retained",
		OrphanEligibility:    "unreferenced and older than snapshot-minus-window with known age",
		UnknownAge:           "never eligible",
		ReadCap:              1024,
		PruneBatchCap:        4096,
		WindowRule:           "max(operator retention age, WorkflowResult tier of the injected timeout profile)",
		WriteOrder:           "payload blob Put before journal append",
		Digest:               "sha256",
		NewestRow:            "excluded from operator prune",
	}, nil
}

// GenerateRetentionBody is the production owner codec used by explicit
// generation. It never reads a store at runtime.
func GenerateRetentionBody() ([]byte, error) {
	spec, err := RetentionSpecificationForBinary()
	if err != nil {
		return nil, err
	}
	return AssembleRetentionBody(spec)
}

// AssembleRetentionBody encodes one retention specification as the canonical
// owner body. The generator and the round-trip checks share this assembler.
func AssembleRetentionBody(spec RetentionSpecification) ([]byte, error) {
	raw, err := marshalDocument(retentionDocument{model.DefinitionRetentionPolicy, RetentionDocumentID, RetentionDocumentVersion, model.DefinitionCanonicalJSON, spec})
	if err != nil {
		return nil, failure("AssembleRetentionBody", "The typed retention specification could not be encoded.", err)
	}
	return NormalizePayload(EvidenceKind, raw)
}

// NewRetentionPolicy returns the compiled retention owner document from its
// generated body. Historical bodies remain decodable; only this constructor
// provides a document the activation writer accepts.
func NewRetentionPolicy() (Document, error) {
	body, err := generatedBody("generated/retention.json")
	if err != nil {
		return Document{}, err
	}
	document, err := decodeRetention(body)
	if err != nil {
		return Document{}, err
	}
	if document.ID() != RetentionDocumentID {
		return Document{}, failure("NewRetentionPolicy", "The embedded document does not identify the retention policy; regenerate the retention document.", nil)
	}
	document.ownerBuilt = true
	return document, nil
}

// ParseRetentionSpecification strictly decodes one schema owner body and
// returns its shared specification. Readers and agreement checks consume the
// specification through this parser, never by reimplementing the codec.
func ParseRetentionSpecification(body []byte) (RetentionSpecification, error) {
	var wire retentionDocument
	if err := strictDecode(body, &wire); err != nil {
		return RetentionSpecification{}, failure("ParseRetentionSpecification", "The typed retention document contains malformed, duplicate or unknown fields.", err)
	}
	doc, err := decodeRetention(body)
	if err != nil {
		return RetentionSpecification{}, err
	}
	if doc.ID() != RetentionDocumentID {
		return RetentionSpecification{}, failure("ParseRetentionSpecification", "The document does not identify the retention owner.", nil)
	}
	return wire.Specification, nil
}

func decodeRetention(payload []byte) (Document, error) {
	var wire retentionDocument
	if err := strictDecode(payload, &wire); err != nil {
		return Document{}, failure("decodeRetention", "The typed retention document contains malformed, duplicate or unknown fields.", err)
	}
	// Version 1 is a historical codec arm, independent of the version emitted
	// by today's generator. Add a new arm rather than replacing this decoder.
	switch wire.Version {
	case 1:
	default:
		return Document{}, failure("decodeRetention", "The retention document version has no supported historical codec.", nil)
	}
	if wire.Kind != model.DefinitionRetentionPolicy || wire.Version != 1 || wire.Codec != model.DefinitionCanonicalJSON || wire.ID != RetentionDocumentID {
		return Document{}, failure("decodeRetention", "Retention document kind, owner identity, codec or version is invalid.", fmt.Errorf("kind=%d version=%d codec=%d id=%q", wire.Kind, wire.Version, wire.Codec, wire.ID))
	}
	if err := validateRetentionSpecification(wire.Specification); err != nil {
		return Document{}, failure("decodeRetention", "The retention descriptor violates its structural contract.", err)
	}
	normalized, err := NormalizePayload(EvidenceKind, payload)
	if err != nil {
		return Document{}, err
	}
	second, err := NormalizePayload(EvidenceKind, normalized)
	if err != nil || !bytes.Equal(normalized, second) {
		return Document{}, failure("decodeRetention", "Journal normalization is not idempotent.", err)
	}
	return Document{kind: wire.Kind, id: wire.ID, version: wire.Version, codec: wire.Codec, body: normalized, content: model.ContentIdentity(sha256.Sum256(normalized))}, nil
}

func validateRetentionSpecification(spec RetentionSpecification) error {
	if spec.Policy != "store-all" || spec.CommittedNamedBodies != "retained" || spec.UnknownAge != "never eligible" {
		return fmt.Errorf("retention descriptor does not describe the store-all policy")
	}
	if spec.ReadCap != 1024 || spec.PruneBatchCap != 4096 {
		return fmt.Errorf("retention descriptor caps are not the enforced reclaim budgets")
	}
	if spec.WindowRule == "" || spec.WriteOrder == "" || spec.OrphanEligibility == "" || spec.Digest == "" || spec.NewestRow == "" {
		return fmt.Errorf("retention descriptor window, order, eligibility, digest or newest-row rule is empty")
	}
	return nil
}
