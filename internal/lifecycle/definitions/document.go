// Package definitions owns immutable versioned lifecycle documents. Documents
// are constructed by their typed owners, never by accepting arbitrary JSON.
package definitions

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

const EvidenceKind = provenance.EvidenceKind("pasture.lifecycle.definition.v2")

type Document struct {
	kind       model.DefinitionKind
	id         model.DefinitionID
	version    uint32
	codec      model.DefinitionCodec
	body       []byte
	content    model.ContentIdentity
	ownerBuilt bool
}

func (d Document) Kind() model.DefinitionKind     { return d.kind }
func (d Document) ID() model.DefinitionID         { return d.id }
func (d Document) Version() uint32                { return d.version }
func (d Document) Codec() model.DefinitionCodec   { return d.codec }
func (d Document) Body() []byte                   { return bytes.Clone(d.body) }
func (d Document) Content() model.ContentIdentity { return d.content }
func (d Document) IsValid() bool {
	return d.ownerBuilt && model.IsEnvelopeDefinitionKind(d.kind) && d.id != "" && d.version > 0 && d.codec == model.DefinitionCanonicalJSON && len(d.body) > 0 && d.content == model.ContentIdentity(sha256.Sum256(d.body))
}

// Coordinate is the shared owner encoding used by both operation and command
// domains. Body content is already normalized; it is not re-hashed differently.
func (d Document) Coordinate() []byte {
	out, _ := json.Marshal(struct {
		Kind    model.DefinitionKind  `json:"kind"`
		ID      model.DefinitionID    `json:"id"`
		Version uint32                `json:"version"`
		Codec   model.DefinitionCodec `json:"codec"`
		Content model.ContentIdentity `json:"content"`
	}{d.kind, d.id, d.version, d.codec, d.content})
	return out
}

func failure(where, reason string, cause error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation,
		What: "The lifecycle definition document could not be constructed or decoded.", Why: reason,
		Where:  "internal/lifecycle/definitions/" + where + " during document codec validation",
		Impact: "No validated document was returned and no journal write was performed.",
		Fix:    "Use the typed document owner and supported version/codec; restore damaged stored documents from a verified backup.", Cause: cause}
}

// NormalizePayload uses exactly the public journal codec used at commit. It
// performs no journal I/O, invents no result IDs and has no authority input.
func NormalizePayload(kind provenance.EvidenceKind, payload []byte) ([]byte, error) {
	if err := uniqueMembers(payload); err != nil {
		return nil, failure("NormalizePayload", "JSON must contain one value and no duplicate object members.", err)
	}
	sum := sha256.Sum256(payload)
	canonical, err := provenance.Canonicalize(provenance.OperationInput{Effects: []provenance.Effect{{
		Sort: provenance.EffectEvidence, EvidenceKind: kind, ContentDigest: sum[:], Payload: json.RawMessage(payload),
	}}})
	if err != nil {
		return nil, failure("NormalizePayload", "The public journal codec refused the document payload.", err)
	}
	out := canonical.NormalizedEffects()[0].Payload
	return bytes.Clone(out), nil
}

func uniqueMembers(payload []byte) error {
	d := json.NewDecoder(bytes.NewReader(payload))
	d.UseNumber()
	var walk func() error
	walk = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				seen := make(map[string]bool)
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return fmt.Errorf("duplicate or invalid JSON member %v", key)
					}
					seen[name] = true
					if err := walk(); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
			}
			_, err = d.Token()
		}
		return err
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data: %v", err)
	}
	return nil
}

func strictDecode(payload []byte, target any) error {
	if err := uniqueMembers(payload); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

// Decode validates historical documents using their versioned owner codec, not
// the currently generated content. Other body owners extend this closed dispatch.
func Decode(payload []byte) (Document, error) {
	var header struct {
		Kind model.DefinitionKind `json:"kind"`
	}
	if err := uniqueMembers(payload); err != nil {
		return Document{}, failure("Decode", "Document JSON is malformed or contains duplicate fields.", err)
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return Document{}, failure("Decode", "Document header cannot be read.", err)
	}
	switch header.Kind {
	case model.DefinitionRuntimeContract:
		return decodeRuntime(payload)
	case model.DefinitionLifecycleSchema:
		return decodeSchema(payload)
	case model.DefinitionInterpreter:
		return decodeInterpreter(payload)
	case model.DefinitionEpochImplementation:
		return decodeEpoch(payload)
	case model.DefinitionRetentionPolicy:
		return decodeRetention(payload)
	default:
		return Document{}, failure("Decode", fmt.Sprintf("No supported document owner codec for kind %d.", header.Kind), nil)
	}
}
