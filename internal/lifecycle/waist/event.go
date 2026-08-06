// Package waist defines the target-independent lifecycle values between native
// harness frontends and Pasture's protocol stages.
package waist

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/runtime"
)

const identityValueMaxBytes = 512

// NativeEventName is an exact native lifecycle event spelling.
type NativeEventName string

// Identity is one constructor-validated native correlation value.
type Identity struct {
	kind        runtime.NativeIdentityKind
	nativeName  string
	value       string
	constructed bool
}

// NewIdentity validates and constructs one native correlation identity.
func NewIdentity(kind runtime.NativeIdentityKind, nativeName, value string) (Identity, error) {
	const where = "Constructing a native correlation identity (internal/lifecycle/waist/event.go in waist.NewIdentity)."
	if !kind.IsValid() {
		return Identity{}, validationError(
			fmt.Sprintf("The native correlation kind %d is not recognised.", uint8(kind)),
			"Native correlation uses a closed set of kinds so a native occurrence cannot carry unrelated authority.",
			where,
			"The lifecycle event was not built.",
			"Use the identity kind declared by the pinned lifecycle contract.",
			nil,
		)
	}
	if nativeName == "" || strings.TrimSpace(nativeName) != nativeName {
		return Identity{}, validationError(
			fmt.Sprintf("The native correlation field name %q is empty or padded.", nativeName),
			"Correlation fields are matched by their exact native spelling.",
			where,
			"The lifecycle event was not built.",
			"Use the exact field name declared by the pinned lifecycle contract.",
			nil,
		)
	}
	if err := validateIdentityValue(nativeName, value, where); err != nil {
		return Identity{}, err
	}
	return Identity{kind: kind, nativeName: nativeName, value: value, constructed: true}, nil
}

func validateIdentityValue(nativeName, value, where string) error {
	var what, why, fix string
	switch {
	case value == "":
		what = fmt.Sprintf("The native correlation field %q has an empty value.", nativeName)
		why = "An empty value cannot identify the native occurrence."
		fix = fmt.Sprintf("Send the host's %q value unchanged, or omit an optional field.", nativeName)
	case !utf8.ValidString(value):
		what = fmt.Sprintf("The native correlation field %q is not valid UTF-8.", nativeName)
		why = "Correlation values must remain byte-exact across processes and storage."
		fix = "Send the identity as valid UTF-8."
	case len(value) > identityValueMaxBytes:
		what = fmt.Sprintf("The native correlation field %q carries %d bytes, over the %d-byte limit.", nativeName, len(value), identityValueMaxBytes)
		why = "Native identifiers are short; an oversized value indicates payload content was supplied as an identity."
		fix = fmt.Sprintf("Send only the host's identifier in %q.", nativeName)
	case strings.TrimSpace(value) != value:
		what = fmt.Sprintf("The native correlation field %q has surrounding whitespace.", nativeName)
		why = "Correlation values are compared byte-for-byte and are not normalised."
		fix = fmt.Sprintf("Send the host's %q value without surrounding whitespace.", nativeName)
	default:
		for _, r := range value {
			if unicode.IsControl(r) {
				what = fmt.Sprintf("The native correlation field %q contains a control character.", nativeName)
				why = "Control characters are unsafe in identities that are compared, logged, and displayed."
				fix = fmt.Sprintf("Remove control characters from the %q value.", nativeName)
				break
			}
		}
	}
	if what == "" {
		return nil
	}
	return validationError(what, why, where, "The lifecycle event was not built.", fix, nil)
}

func (i Identity) Kind() runtime.NativeIdentityKind { return i.kind }
func (i Identity) NativeName() string               { return i.nativeName }
func (i Identity) Value() string                    { return i.value }
func (i Identity) IsValid() bool                    { return i.constructed }

// SemanticIdentity is target-independent native correlation.
type SemanticIdentity struct {
	Kind  runtime.NativeIdentityKind
	Value string
}

// UnresolvedReason is the closed set of reasons why native correlation could
// not be represented completely.
type UnresolvedReason uint8

const (
	// UnresolvedToolCall means the native event does not expose a tool-call
	// identity even though the occurrence concerns a tool-call batch.
	UnresolvedToolCall UnresolvedReason = iota + 1
)

func (r UnresolvedReason) IsValid() bool { return r == UnresolvedToolCall }

func (r UnresolvedReason) String() string {
	if r == UnresolvedToolCall {
		return "tool-call-unresolved"
	}
	return ""
}

// UnresolvedFact records a typed, non-authoritative correlation gap.
type UnresolvedFact struct {
	Reason UnresolvedReason
}

func (f UnresolvedFact) IsValid() bool { return f.Reason.IsValid() }

// Semantics is the target-independent lifecycle meaning and correlation.
type Semantics struct {
	semantic    runtime.EventSemantic
	blocking    runtime.BlockingMode
	identities  []SemanticIdentity
	unresolved  []UnresolvedFact
	constructed bool
}

func (s Semantics) Semantic() runtime.EventSemantic { return s.semantic }
func (s Semantics) Blocking() runtime.BlockingMode  { return s.blocking }
func (s Semantics) Identities() []SemanticIdentity {
	return append([]SemanticIdentity(nil), s.identities...)
}
func (s Semantics) UnresolvedFacts() []UnresolvedFact {
	return append([]UnresolvedFact(nil), s.unresolved...)
}
func (s Semantics) IsValid() bool { return s.constructed }

// Origin identifies the pinned contract and native event that produced an L2.
type Origin struct {
	contract    ir.RuntimeContractID
	nativeName  NativeEventName
	constructed bool
}

func (o Origin) Contract() ir.RuntimeContractID   { return o.contract }
func (o Origin) Harness() ir.HarnessID            { return o.contract.Harness() }
func (o Origin) NativeEventName() NativeEventName { return o.nativeName }
func (o Origin) IsValid() bool                    { return o.constructed }

// EventBinding is an L1 native event bound to one pinned lifecycle mapping.
type EventBinding struct {
	contract    ir.RuntimeContractID
	mapping     runtime.LifecycleEventMapping
	nativeName  NativeEventName
	constructed bool
}

// L1 is the bound native event consumed by the lifecycle transform.
type L1 = EventBinding

// BindEvent resolves a typed native event against its pinned lifecycle contract.
func BindEvent[E comparable](contract runtime.LifecycleContract[E], event E) (EventBinding, error) {
	const where = "Binding a native lifecycle event (internal/lifecycle/waist/event.go in waist.BindEvent)."
	if !contract.IsValid() {
		return EventBinding{}, validationError(
			"A lifecycle event was bound against an empty runtime contract.",
			"Only a reviewed, version-pinned lifecycle profile defines native event semantics.",
			where,
			"No lifecycle event could be built.",
			"Use the pinned lifecycle profile for the native harness.",
			nil,
		)
	}
	mapping, err := contract.Mapping(event)
	if err != nil {
		return EventBinding{}, validationError(
			fmt.Sprintf("The pinned %s lifecycle contract does not describe the requested native event.", contract.Harness()),
			"A pinned lifecycle profile contains only its closed typed event catalogue.",
			where,
			"No lifecycle event could be built.",
			"Use an event from the matching harness event enumeration.",
			err,
		)
	}
	return EventBinding{
		contract:    contract.ID(),
		mapping:     mapping,
		nativeName:  NativeEventName(mapping.NativeName()),
		constructed: true,
	}, nil
}

func (b EventBinding) DeclaredIdentities() []runtime.NativeIdentityField {
	return b.mapping.Identities()
}
func (b EventBinding) IsValid() bool { return b.constructed }

// Event is the verified L2 lifecycle value.
type Event struct {
	semantics   Semantics
	origin      Origin
	constructed bool
}

// L2 is the verified semantic lifecycle value returned by NewEvent.
type L2 = Event

func (e Event) Semantics() Semantics { return e.semantics }
func (e Event) Origin() Origin       { return e.origin }
func (e Event) IsValid() bool        { return e.constructed }

// NewEvent is the sole L1-to-L2 transform. It verifies constructor-built
// identities against the bound mapping and derives semantics from that mapping.
func (b EventBinding) NewEvent(identities []Identity) (L2, error) {
	const where = "Verifying a parsed lifecycle event (internal/lifecycle/waist/event.go in waist.EventBinding.NewEvent)."
	if !b.IsValid() {
		return Event{}, validationError(
			"A lifecycle event was built from a binding that did not come from BindEvent.",
			"The binding supplies semantics from the pinned lifecycle contract.",
			where,
			"The lifecycle event was not built.",
			"Bind the typed native event before constructing its semantic event.",
			nil,
		)
	}
	semanticIdentities, err := b.verifyIdentities(identities, where)
	if err != nil {
		return Event{}, err
	}
	unresolved, err := unresolvedFacts(b.mapping.UnresolvedIdentities(), where)
	if err != nil {
		return Event{}, err
	}
	return Event{
		semantics: Semantics{
			semantic:    b.mapping.Semantic(),
			blocking:    b.mapping.Blocking(),
			identities:  semanticIdentities,
			unresolved:  unresolved,
			constructed: true,
		},
		origin: Origin{
			contract:    b.contract,
			nativeName:  b.nativeName,
			constructed: true,
		},
		constructed: true,
	}, nil
}

func unresolvedFacts(kinds []runtime.NativeIdentityKind, where string) ([]UnresolvedFact, error) {
	facts := make([]UnresolvedFact, 0, len(kinds))
	for _, kind := range kinds {
		var reason UnresolvedReason
		switch kind {
		case runtime.IdentityToolCall:
			reason = UnresolvedToolCall
		default:
			return nil, validationError(
				fmt.Sprintf("The lifecycle mapping contains unsupported unresolved identity kind %q.", kind),
				"Every contract-level correlation gap must have an explicit closed waist reason.",
				where,
				"The lifecycle event was not built.",
				"Add a reviewed unresolved reason before declaring this identity kind unresolved.",
				nil,
			)
		}
		facts = append(facts, UnresolvedFact{Reason: reason})
	}
	return facts, nil
}

type suppliedKey struct {
	kind       runtime.NativeIdentityKind
	nativeName string
}

func (b EventBinding) verifyIdentities(supplied []Identity, where string) ([]SemanticIdentity, error) {
	declared := b.DeclaredIdentities()
	seen := make(map[suppliedKey]struct{}, len(supplied))
	present := make(map[string]struct{}, len(supplied))
	semantic := make([]SemanticIdentity, 0, len(supplied))

	for index, identity := range supplied {
		if !identity.IsValid() {
			return nil, validationError(
				fmt.Sprintf("Correlation identity %d for native event %q was not built by NewIdentity.", index, b.nativeName),
				"Only constructor-built identities have bounded, exact native values.",
				where,
				"The lifecycle event was not built.",
				"Build every correlation identity with NewIdentity before verifying the event.",
				nil,
			)
		}
		if err := validateIdentityValue(identity.nativeName, identity.value, where); err != nil {
			return nil, err
		}
		field, found := b.mapping.DeclaredField(identity.nativeName)
		if !found {
			return nil, validationError(
				fmt.Sprintf("Correlation field %q is not declared for native event %q.", identity.nativeName, b.nativeName),
				"Only correlation declared by the pinned contract may enter the lifecycle waist.",
				where,
				"The lifecycle event was not built.",
				fmt.Sprintf("Extract only these declared fields: %s.", describeDeclaredFields(declared)),
				nil,
			)
		}
		if field.Kind() != identity.kind {
			return nil, validationError(
				fmt.Sprintf("Correlation field %q was supplied as %s but is declared as %s.", identity.nativeName, identity.kind, field.Kind()),
				"Semantic correlation is classified by identity kind, not native field spelling.",
				where,
				"The lifecycle event was not built.",
				fmt.Sprintf("Use the %s kind declared for %q.", field.Kind(), identity.nativeName),
				nil,
			)
		}
		key := suppliedKey{kind: identity.kind, nativeName: identity.nativeName}
		if _, duplicate := seen[key]; duplicate {
			return nil, validationError(
				fmt.Sprintf("Correlation field %q was supplied more than once for native event %q.", identity.nativeName, b.nativeName),
				"One native correlation field must have exactly one value.",
				where,
				"The lifecycle event was not built.",
				fmt.Sprintf("Supply %q exactly once.", identity.nativeName),
				nil,
			)
		}
		seen[key] = struct{}{}
		present[identity.nativeName] = struct{}{}
		semantic = append(semantic, SemanticIdentity{Kind: identity.kind, Value: identity.value})
	}

	for _, field := range declared {
		if !field.Required() {
			continue
		}
		if _, found := present[field.NativeName()]; found {
			continue
		}
		return nil, validationError(
			fmt.Sprintf("Native event %q is missing required correlation field %q.", b.nativeName, field.NativeName()),
			"The pinned contract requires this field to correlate the occurrence.",
			where,
			"The lifecycle event was not built.",
			fmt.Sprintf("Include %q in the %s payload.", field.NativeName(), b.contract.Harness()),
			nil,
		)
	}

	slices.SortFunc(semantic, compareSemanticIdentities)
	return semantic, nil
}

func compareSemanticIdentities(left, right SemanticIdentity) int {
	if order := cmp.Compare(left.Kind, right.Kind); order != 0 {
		return order
	}
	return cmp.Compare(left.Value, right.Value)
}

func describeDeclaredFields(declared []runtime.NativeIdentityField) string {
	if len(declared) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(declared))
	for _, field := range declared {
		requirement := "optional"
		if field.Required() {
			requirement = "required"
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", field.NativeName(), requirement))
	}
	return strings.Join(parts, ", ")
}

func validationError(what, why, where, impact, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why,
		Where:    where,
		Impact:   impact,
		Fix:      fix,
		Cause:    cause,
	}
}
