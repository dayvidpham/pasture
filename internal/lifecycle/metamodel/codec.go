package metamodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// DecodedBody is the owner-validated view of one persisted metamodel body: the
// definition identity and document version its bytes claim. The bridge resolves
// snapshots through this codec, never by trusting a bare journal coordinate.
type DecodedBody struct {
	ID      model.DefinitionID
	Version uint32
}

// DecodeBody validates one persisted metamodel body with the versioned owner
// codec. It requires the known metamodel document shape, the stable definition
// identity, and a supported historical version; it never equates the body with
// whatever the current binary generates, so historical producer bodies remain
// resolvable after the profiles move on.
func DecodeBody(payload []byte) (DecodedBody, error) {
	if err := uniqueMembers(payload); err != nil {
		return DecodedBody{}, codecError("DecodeBody", "Metamodel body JSON is malformed or contains duplicate fields.", err)
	}
	var wire metamodelDoc
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return DecodedBody{}, codecError("DecodeBody", "Metamodel body does not match the known owner shape.", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return DecodedBody{}, codecError("DecodeBody", "Metamodel body contains trailing JSON.", err)
	}
	if wire.ID != string(LifecycleMetamodelID) {
		return DecodedBody{}, codecError("DecodeBody", fmt.Sprintf("Metamodel body identity %q is not the metamodel document.", wire.ID), nil)
	}
	// Version 1 is a historical codec arm. Add a new arm rather than replacing
	// this decoder when the canonical body content changes.
	switch wire.Version {
	case 1:
	default:
		return DecodedBody{}, codecError("DecodeBody", "The metamodel body version has no supported historical codec.", nil)
	}
	if err := validateCatalog(wire.Harnesses); err != nil {
		return DecodedBody{}, codecError("DecodeBody", "The metamodel event catalog violates its structural contract.", err)
	}
	return DecodedBody{ID: LifecycleMetamodelID, Version: wire.Version}, nil
}

func uniqueMembers(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				seen := make(map[string]bool)
				for decoder.More() {
					key, err := decoder.Token()
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
				for decoder.More() {
					if err := walk(); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
			}
			_, err = decoder.Token()
		}
		return err
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data: %v", err)
	}
	return nil
}

// validateCatalog enforces the structural contract every metamodel body honors
// regardless of version: named harnesses with unique native events whose
// declared meanings parse against the closed runtime vocabulary.
func validateCatalog(harnesses []metamodelHarness) error {
	if len(harnesses) == 0 {
		return fmt.Errorf("metamodel body names no harness")
	}
	seen := make(map[string]bool, len(harnesses))
	for _, harness := range harnesses {
		if harness.Harness == "" || seen[harness.Harness] {
			return fmt.Errorf("metamodel harness %q is empty or duplicated", harness.Harness)
		}
		seen[harness.Harness] = true
		if len(harness.Events) == 0 {
			return fmt.Errorf("metamodel harness %q declares no events", harness.Harness)
		}
		events := make(map[string]bool, len(harness.Events))
		for _, event := range harness.Events {
			if event.NativeName == "" || events[event.NativeName] {
				return fmt.Errorf("metamodel event %q is empty or duplicated", event.NativeName)
			}
			events[event.NativeName] = true
			if err := validateEventVocabulary(event); err != nil {
				return fmt.Errorf("metamodel event %q: %w", event.NativeName, err)
			}
		}
	}
	return nil
}

func validateEventVocabulary(event metamodelEvent) error {
	if !parseSemantic(event.Semantic) || event.Blocking == "" || event.Mutation == "" || event.Failure == "" || event.StopLoop == "" {
		return fmt.Errorf("event meaning, blocking, mutation, failure or stop-loop is not declared")
	}
	for _, identity := range event.Identities {
		if identity == "" {
			return fmt.Errorf("identity entry is empty")
		}
	}
	for _, unresolved := range event.Unresolved {
		if unresolved == "" {
			return fmt.Errorf("unresolved entry is empty")
		}
	}
	return nil
}

// parseSemantic reports whether the token names a live runtime event meaning.
// The runtime owns the closed set; the codec never copies it.
func parseSemantic(token string) bool {
	for semantic := runtime.SemanticObservation; semantic.IsValid(); semantic++ {
		if semantic.String() == token {
			return true
		}
	}
	return false
}

func codecError(owner, reason string, cause error) error {
	return &pasterrors.StructuredError{Category: pasterrors.CategoryValidation,
		What:   "The persisted metamodel body could not be validated.",
		Why:    reason,
		Where:  "internal/lifecycle/metamodel/codec.go in metamodel." + owner + " during owner body validation",
		Impact: "No metamodel snapshot was resolved; the journal was not changed.",
		Fix:    "Resolve the coordinate through the metamodel owner; restore damaged evidence from a verified backup.",
		Cause:  cause}
}
