package definitions

import (
	"reflect"

	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// CompatibleSourceInventoryUpdate reports whether incoming can supersede
// existing at the same definition coordinate when the only difference is the
// generated source inventory.
//
// The source manifest describes the build that produced a document, not the
// protocol shape the document names. A rebuilt binary must not need a new
// document version just because an engine or interpreter source file changed.
// The check is deliberately narrow: kind, id, version and codec must match,
// the document must be one of the two kinds that carry a source manifest, and
// every non-source field must be identical. Semantic changes still require a
// new version.
func CompatibleSourceInventoryUpdate(existing []byte, incoming Document) (bool, error) {
	if !incoming.IsValid() {
		return false, nil
	}
	committed, err := Decode(existing)
	if err != nil {
		return false, err
	}
	if committed.Kind() != incoming.Kind() || committed.ID() != incoming.ID() ||
		committed.Version() != incoming.Version() || committed.Codec() != incoming.Codec() {
		return false, nil
	}
	switch incoming.Kind() {
	case model.DefinitionEpochImplementation:
		before, err := ParseEpochSpecification(existing)
		if err != nil {
			return false, err
		}
		after, err := ParseEpochSpecification(incoming.Body())
		if err != nil {
			return false, err
		}
		before.Sources = nil
		after.Sources = nil
		return reflect.DeepEqual(before, after), nil
	case model.DefinitionInterpreter:
		before, err := ParseInterpreterSpecification(existing)
		if err != nil {
			return false, err
		}
		after, err := ParseInterpreterSpecification(incoming.Body())
		if err != nil {
			return false, err
		}
		before.Sources = nil
		after.Sources = nil
		return reflect.DeepEqual(before, after), nil
	default:
		return false, nil
	}
}
