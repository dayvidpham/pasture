// Package lifecycle is the canonical middle of Pasture's harness integration:
// the narrow waist between per-harness native lifecycle events and Pasture's
// durable effects.
//
// The pipeline it sits in:
//
//	native occurrence
//	  -> generated registration + trivial trampoline   (foreign side, no semantics)
//	  -> per-harness frontend, written in Go           (source language -> IR)
//	  -> THIS PACKAGE: target-agnostic lifecycle IR    (the narrow waist)
//	  -> lowering pass                                 (IR -> durable effects)
//	  -> engine + Provenance                           (effects -> stored facts)
//
// Three rules give the waist its value, and all three are enforced here rather
// than documented and hoped for:
//
//  1. The waist is SEMANTIC and NARROW. [Semantics] carries only what is true
//     of an occurrence regardless of which host produced it: what it means, and
//     whether the host is waiting on a result. Every other property a host has
//     — its wire surface, what it lets a handler mutate, how it schedules and
//     reconciles concurrent handlers, how it reports failure — describes how to
//     speak back to one specific host. That is target detail, and it is reached
//     only through [BackendView].
//
//  2. The waist is VERIFIED, and the verifier is [EventBinding.NewEvent]. A
//     frontend does not state what an event means; it resolves a typed event
//     against a pinned contract and supplies only what that contract cannot
//     know — the payload digest and the extracted correlation values. Meaning
//     is then read out of the pinned table. A frontend therefore has no way to
//     invent semantics, and a reviewer has one verifier to trust instead of one
//     frontend per host.
//
//  3. The waist is OPAQUE. Every type here is constructor-owned with unexported
//     fields, and every accessor that returns a slice returns a fresh copy. An
//     exported slice field would be aliasable, and sorting it in place would
//     mutate the caller's own data.
//
// What is deliberately unrepresentable: Pasture actors, assignments, journal
// identifiers, document revisions, and review or publication evidence. There is
// no field on [Event], [Semantics] or [Origin] capable of holding any of them.
// A native occurrence can therefore never manufacture Pasture authority,
// because there is nowhere to put it.
//
// This package knows no host by name. It has no host-specific branch, no host
// payload format, and no import of any frontend. If that stops being true, it
// has stopped being a waist.
//
// This is NOT internal/codegen/ir.SemanticOperation. That is the authoring IR
// for generated documents (a different level, a different purpose). This
// package is the runtime lifecycle IR.
package lifecycle

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/runtime"
)

// identityValueMaxBytes bounds one native correlation value before it is
// admitted into the IR. Correlation identifiers are short by construction
// (session, turn, request, tool-call, agent, and message identifiers); the
// bound stops a hostile or malfunctioning host payload from carrying a
// transcript-sized value through the waist under an identity field name.
const identityValueMaxBytes = 512

// NativeEventName is one exact native lifecycle event spelling, as it appears
// in a host's own payload and in its own hook registration.
//
// It is a source-language token, not a Pasture semantic identity. Only a
// frontend resolves one, and it does so against that host's closed typed event
// enum before any waist value exists — there is deliberately no string lookup
// on runtime.LifecycleContract. Once an [Event] exists, its name is an echo of
// the pinned contract's own spelling, never a caller's: the constructor takes
// no name argument, so there is no spelling for a frontend to get wrong.
type NativeEventName string

// Digest is a SHA-256 over the exact bytes read at the process boundary.
//
// It is computed BEFORE parsing, so it is well-defined even for a payload that
// fails to parse, and it is what makes replay detection independent of every
// later stage: two invocations carrying identical bytes produce an identical
// digest no matter how the parse goes, and two invocations carrying different
// bytes cannot collapse into one record.
//
// The zero value is invalid and is rejected by [EventBinding.NewEvent]. A
// digest over empty input is not the zero value, so "the host sent nothing" and
// "nobody computed a digest" stay distinguishable.
type Digest [32]byte

// NewDigest computes the digest of the exact bytes read at the process
// boundary. Call it on the raw payload, before any parsing or normalisation.
func NewDigest(raw []byte) Digest { return sha256.Sum256(raw) }

// IsZero reports whether this is the zero digest, i.e. one that was never
// computed. It is not the digest of empty input.
func (d Digest) IsZero() bool { return d == Digest{} }

// String returns the lowercase hexadecimal encoding of the digest.
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// Identity is one native correlation value a frontend extracted from a host
// payload: a typed kind, the exact native field it came from, and its exact
// bytes.
//
// It is the constructor's INPUT type, not a waist type. The native field name
// exists so [EventBinding.NewEvent] can check the pair against the pinned
// mapping; once checked, the name is target detail and is dropped. What
// survives into the waist is [SemanticIdentity].
//
// The kind vocabulary is runtime.NativeIdentityKind, which spans session, turn,
// request, tool-call, agent, and message only — so an Identity cannot carry a
// Pasture actor, assignment, journal identifier, revision, or evidence
// reference no matter what a frontend does.
type Identity struct {
	kind        runtime.NativeIdentityKind
	nativeName  string
	value       string
	constructed bool
}

// NewIdentity validates and constructs one native correlation identity.
//
// The value is kept byte-exact: correlation is only useful if the same native
// occurrence yields the same bytes across processes and store restarts, so
// padded, empty, oversized, or control-bearing values are rejected rather than
// normalised.
//
// Whether this identity is ADMISSIBLE for a given event is a separate question,
// answered by [EventBinding.NewEvent] against the pinned contract's declared
// field set. This constructor only guarantees the value is well-formed.
func NewIdentity(kind runtime.NativeIdentityKind, nativeName, value string) (Identity, error) {
	const where = "Constructing a native correlation identity (internal/lifecycle/event.go in lifecycle.NewIdentity)."
	if !kind.IsValid() {
		return Identity{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The native correlation kind %d is not one this build recognises.", uint8(kind)),
			Why:      "Native correlation is limited to a closed set of kinds (session, turn, request, tool-call, agent, message) so a native occurrence can never carry Pasture authority.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      "Use one of the identity kinds declared by the pinned lifecycle contract for this event.",
		}
	}
	if nativeName == "" || strings.TrimSpace(nativeName) != nativeName {
		return Identity{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The native correlation field name %q is empty or padded with whitespace.", nativeName),
			Why:      "A correlation field is matched against the pinned contract by its exact native spelling; a padded name would match nothing and silently drop the correlation.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      "Use the exact field name the pinned lifecycle contract declares for this event.",
		}
	}
	if err := validateIdentityValue(nativeName, value, where); err != nil {
		return Identity{}, err
	}
	return Identity{kind: kind, nativeName: nativeName, value: value, constructed: true}, nil
}

func validateIdentityValue(nativeName, value, where string) error {
	switch {
	case value == "":
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The native correlation field %q has an empty value.", nativeName),
			Why:      "An empty correlation value cannot identify the occurrence it came from, so the recorded fact could never be found again.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      fmt.Sprintf("Send the host's own %q value unchanged, or omit the field if the contract marks it optional.", nativeName),
		}
	case !utf8.ValidString(value):
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The native correlation field %q is not valid UTF-8.", nativeName),
			Why:      "Correlation values are stored and compared byte-for-byte; accepting malformed bytes would let two different occurrences compare equal after replacement characters were substituted.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      "Send the payload as valid UTF-8.",
		}
	case len(value) > identityValueMaxBytes:
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The native correlation field %q carries %d bytes, over the %d-byte limit.", nativeName, len(value), identityValueMaxBytes),
			Why:      "Correlation identifiers are short by construction; an oversized value indicates payload content is being smuggled through an identity field.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      fmt.Sprintf("Send only the host's own identifier in %q, not surrounding payload content.", nativeName),
		}
	case strings.TrimSpace(value) != value:
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The native correlation field %q has leading or trailing whitespace.", nativeName),
			Why:      "Correlation must be byte-exact so the same occurrence always derives the same identity; trimming silently would make two spellings of one value diverge.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      fmt.Sprintf("Send the host's own %q value without surrounding whitespace.", nativeName),
		}
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("The native correlation field %q contains a control character.", nativeName),
				Why:      "Control characters are unsafe in identities that are logged, compared, and echoed back to operators.",
				Where:    where,
				Impact:   "The lifecycle event was not built and nothing was recorded.",
				Fix:      fmt.Sprintf("Remove control characters from the %q value.", nativeName),
			}
		}
	}
	return nil
}

// Kind returns the typed native correlation kind.
func (i Identity) Kind() runtime.NativeIdentityKind { return i.kind }

// NativeName returns the exact native field this value came from. It is
// checked against the pinned mapping and then dropped; it does not reach the
// waist.
func (i Identity) NativeName() string { return i.nativeName }

// Value returns the exact native correlation bytes.
func (i Identity) Value() string { return i.value }

// IsValid reports whether this identity came from [NewIdentity].
func (i Identity) IsValid() bool { return i.constructed }

// SemanticIdentity is one correlation value as it exists inside the waist:
// a typed kind and exact bytes, with the native field name stripped.
//
// The name is gone on purpose. Two hosts that both correlate by session use
// different field spellings for it; keeping either spelling in the waist would
// make a target-agnostic value carry target detail, and would make two
// equivalent occurrences compare unequal for a reason that has nothing to do
// with what they mean.
type SemanticIdentity struct {
	Kind  runtime.NativeIdentityKind
	Value string
}

// Semantics is THE WAIST: everything about one occurrence that is true
// regardless of which host produced it.
//
// It carries exactly three things — what the occurrence means, whether the host
// is awaiting a result, and the native correlation it came with. Nothing else
// is host-independent, and nothing else belongs here.
//
// It is opaque and constructor-owned. [Semantics.Identities] returns a fresh
// copy on every call, because an exported or shared slice would let a consumer
// sort or overwrite another consumer's view.
type Semantics struct {
	semantic    runtime.EventSemantic
	blocking    runtime.BlockingMode
	identities  []SemanticIdentity
	constructed bool
}

// Semantic returns the only Pasture meaning this occurrence may have.
func (s Semantics) Semantic() runtime.EventSemantic { return s.semantic }

// Blocking reports whether the host waits for, and can act on, a result.
func (s Semantics) Blocking() runtime.BlockingMode { return s.blocking }

// Identities returns the correlation values in a deterministic order, sorted by
// (Kind, Value). The returned slice is a fresh copy.
//
// Sorting at construction rather than at read is what makes two frontends that
// extracted the same values in different orders produce byte-equal keys.
// Several values of one kind are retained, in sorted order, rather than
// collapsed — dropping one would silently lose correlation.
func (s Semantics) Identities() []SemanticIdentity {
	return append([]SemanticIdentity(nil), s.identities...)
}

// EquivalentTo reports whether two occurrences have the same target-agnostic
// SHAPE: the same meaning, the same blocking mode, and the same multiset of
// correlation kinds.
//
// It deliberately does NOT compare correlation VALUES. The relation exists to
// answer "do these two native events mean the same thing", and two hosts
// observing one logical occurrence issue their own identifiers for it, so a
// value comparison would report every genuine equivalence as a difference.
//
// It is therefore coarse, and it is not on its own evidence that a frontend
// read the right event: different events of one host can reduce to the same
// shape. Pair it with assertions on [Origin.NativeEventName] and on the exact
// values from [Semantics.Identities].
//
// Note the asymmetry with [Semantics.CanonicalKey], which DOES encode values:
// equal canonical keys imply EquivalentTo, but not the reverse.
//
// A zero Semantics is equivalent to nothing, including another zero Semantics.
func (s Semantics) EquivalentTo(other Semantics) bool {
	if !s.constructed || !other.constructed {
		return false
	}
	if s.semantic != other.semantic || s.blocking != other.blocking {
		return false
	}
	if len(s.identities) != len(other.identities) {
		return false
	}
	for index := range s.identities {
		if s.identities[index].Kind != other.identities[index].Kind {
			return false
		}
	}
	return true
}

// IsValid reports whether these semantics came from [EventBinding.NewEvent].
func (s Semantics) IsValid() bool { return s.constructed }

// Origin is where an occurrence came from: the pinned contract that describes
// it, the contract's own spelling of the event, and the digest of the exact
// bytes the host sent.
//
// It also retains the immutable target behaviour table resolved at bind time,
// but there is no accessor for it here. That is reached only through
// [BackendView], which is the single greppable capability boundary keeping the
// lowering pass target-agnostic.
type Origin struct {
	contract    ir.RuntimeContractID
	nativeName  NativeEventName
	digest      Digest
	behaviour   runtime.LifecycleEventMapping
	constructed bool
}

// Contract returns the pinned, version-bounded runtime contract that describes
// this occurrence.
func (o Origin) Contract() ir.RuntimeContractID { return o.contract }

// Harness returns the host family this occurrence came from. It is derived from
// [Origin.Contract] rather than stored, so it cannot disagree with it.
func (o Origin) Harness() ir.HarnessID { return o.contract.Harness() }

// NativeEventName returns the pinned contract's exact spelling of the event.
func (o Origin) NativeEventName() NativeEventName { return o.nativeName }

// PayloadDigest returns the digest of the exact bytes read at the process
// boundary.
func (o Origin) PayloadDigest() Digest { return o.digest }

// IsValid reports whether this origin came from [EventBinding.NewEvent].
func (o Origin) IsValid() bool { return o.constructed }

// EventBinding is the capability a frontend must hold before it may build an
// [Event]: one pinned runtime contract together with the immutable mapping for
// exactly one native event inside it.
//
// It is opaque and can only be produced by [BindEvent], which resolves a TYPED
// event value against a pinned runtime.LifecycleContract. That is what makes
// the verifier meaningful: a frontend cannot hand the constructor a table it
// wrote itself, so "agrees with the pinned contract" is a real check and not a
// tautology.
type EventBinding struct {
	contract    ir.RuntimeContractID
	behaviour   runtime.LifecycleEventMapping
	nativeName  NativeEventName
	constructed bool
}

// BindEvent resolves one typed native event against a pinned lifecycle contract
// and returns the capability needed to construct an [Event] for it.
//
// This is the type erasure point, and the only admission point into the waist:
// generic in, non-generic out. E is the host's own closed event enum, so there
// is no way to bind an arbitrary native event string; past this call nothing
// downstream is parameterised by host, which is what lets one lowering pass
// serve every frontend.
//
// A zero or unpinned contract, or an event value the contract does not carry,
// fails here — before any payload is read.
func BindEvent[E comparable](contract runtime.LifecycleContract[E], event E) (EventBinding, error) {
	const where = "Binding a native lifecycle event (internal/lifecycle/event.go in lifecycle.BindEvent)."
	if !contract.IsValid() {
		return EventBinding{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A lifecycle event was bound against an empty runtime contract.",
			Why:      "Only the reviewed, version-pinned lifecycle profiles describe what a native event means; an empty contract describes nothing.",
			Where:    where,
			Impact:   "No lifecycle event could be built and nothing was recorded.",
			Fix:      "Bind the event against the pinned lifecycle profile for the host you are parsing.",
		}
	}
	behaviour, err := contract.Mapping(event)
	if err != nil {
		return EventBinding{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The pinned %s lifecycle contract does not describe the requested native event.", contract.Harness()),
			Why:      "Each pinned profile covers a closed catalogue of native events for one exact host version; the requested event is not in it.",
			Where:    where,
			Impact:   "No lifecycle event could be built and nothing was recorded.",
			Fix:      "Use an event from this host's own event catalogue, or pin a contract for a host version that declares it.",
			Cause:    err,
		}
	}
	return EventBinding{
		contract:    contract.ID(),
		behaviour:   behaviour,
		nativeName:  NativeEventName(behaviour.NativeName()),
		constructed: true,
	}, nil
}

// DeclaredIdentities returns the native correlation fields the pinned contract
// declares for the bound event, in the contract's own deterministic order.
//
// A frontend uses this to know what to extract and which fields are required.
// The returned slice is a fresh copy.
func (b EventBinding) DeclaredIdentities() []runtime.NativeIdentityField {
	return b.behaviour.Identities()
}

// IsValid reports whether this binding came from [BindEvent].
func (b EventBinding) IsValid() bool { return b.constructed }

// Event is the waist value: one native lifecycle occurrence expressed entirely
// in target-agnostic terms, plus the coordinates saying where it came from.
//
// It is opaque and constructor-verified. Every value in hand has been checked
// against the pinned runtime contract for its host and event, and carries
// native correlation only.
type Event struct {
	semantics   Semantics
	origin      Origin
	constructed bool
}

// Semantics returns the target-agnostic waist value.
func (e Event) Semantics() Semantics { return e.semantics }

// Origin returns the coordinates of the occurrence.
func (e Event) Origin() Origin { return e.origin }

// IsValid reports whether this event came from [EventBinding.NewEvent].
func (e Event) IsValid() bool { return e.constructed }

// NewEvent is THE VERIFIER. It admits one parse result into the waist, or
// rejects it with an actionable error.
//
// It takes only what the pinned contract cannot know: the digest of the exact
// bytes the host sent, and the correlation values a frontend extracted.
// Everything else — the event's meaning, its blocking mode, its native spelling
// — is read out of the binding. A frontend therefore states no semantics and
// cannot get any wrong.
//
// It rejects:
//
//  1. a zero or unbound binding, i.e. one that did not come from [BindEvent];
//  2. the zero digest, i.e. one nobody computed;
//  3. an identity whose (Kind, NativeName) PAIR is not declared by the pinned
//     mapping. Checking the name alone is not enough: a session field supplied
//     under a request kind would pass a name-only check, and because the waist
//     compares identity KINDS, that produces a semantically wrong correlation
//     inside IR the verifier has already blessed;
//  4. a declared identity marked required that is absent;
//  5. the same (kind, native name) pair supplied more than once;
//  6. an identity value that is empty, oversized, padded, or control-bearing —
//     re-checked here rather than trusted from [NewIdentity], because a
//     verifier that trusts its input is not a verifier.
//
// On success the native field names are dropped, the surviving values become
// [SemanticIdentity] sorted by (Kind, Value), and every slice is stored as a
// defensive copy so a caller mutating its own argument cannot alter the IR.
func (b EventBinding) NewEvent(digest Digest, identities []Identity) (Event, error) {
	const where = "Verifying a parsed lifecycle event (internal/lifecycle/event.go in lifecycle.EventBinding.NewEvent)."
	if !b.IsValid() {
		return Event{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A lifecycle event was built from a binding that did not come from the event binder.",
			Why:      "The binding is what supplies the event's meaning from the reviewed pinned contract; without one there is nothing to derive semantics from, so the event could only carry invented ones.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      "Resolve the typed native event against the pinned lifecycle profile with the event binder before building the event.",
		}
	}
	if digest.IsZero() {
		return Event{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("No payload digest was computed for native event %q.", b.nativeName),
			Why:      "Replay detection compares the digest of the exact bytes the host sent; with no digest, two distinct occurrences would be indistinguishable and a repeated delivery would be recorded twice.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      "Compute the digest over the exact bytes read at the process boundary, before parsing, and pass it here.",
		}
	}
	semanticIdentities, err := b.verifyIdentities(identities, where)
	if err != nil {
		return Event{}, err
	}
	return Event{
		semantics: Semantics{
			semantic:    b.behaviour.Semantic(),
			blocking:    b.behaviour.Blocking(),
			identities:  semanticIdentities,
			constructed: true,
		},
		origin: Origin{
			contract:    b.contract,
			nativeName:  b.nativeName,
			digest:      digest,
			behaviour:   b.behaviour,
			constructed: true,
		},
		constructed: true,
	}, nil
}

// suppliedKey is the duplicate-detection key required by the verifier: the
// (kind, native name) PAIR, not either component alone.
type suppliedKey struct {
	kind       runtime.NativeIdentityKind
	nativeName string
}

// verifyIdentities checks every supplied identity against the fields the pinned
// contract declares for this event, then checks that nothing required is
// missing, and returns the surviving values as sorted waist identities.
//
// Restricting to declared fields is what keeps the waist narrow: a frontend
// cannot widen the IR by attaching correlation the contract never described,
// and it cannot omit correlation the contract requires.
func (b EventBinding) verifyIdentities(supplied []Identity, where string) ([]SemanticIdentity, error) {
	declared := b.DeclaredIdentities()
	seen := make(map[suppliedKey]struct{}, len(supplied))
	present := make(map[string]struct{}, len(supplied))
	semantic := make([]SemanticIdentity, 0, len(supplied))

	for index, identity := range supplied {
		if !identity.IsValid() {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("Correlation identity %d for native event %q was not built by the identity constructor.", index, b.nativeName),
				Why:      "Only constructor-built identities have been checked for exact, bounded, control-free native values; an unchecked one could carry arbitrary payload content.",
				Where:    where,
				Impact:   "The lifecycle event was not built and nothing was recorded.",
				Fix:      "Build every correlation identity with the identity constructor before verifying the event.",
			}
		}
		if err := validateIdentityValue(identity.nativeName, identity.value, where); err != nil {
			return nil, err
		}

		field, found := findDeclaredField(declared, identity.nativeName)
		if !found {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("Correlation field %q is not declared by the pinned contract for native event %q.", identity.nativeName, b.nativeName),
				Why:      "Correlation is restricted to the fields the reviewed contract declares, so a native payload cannot introduce new correlation — or Pasture authority disguised as correlation — through the waist.",
				Where:    where,
				Impact:   "The lifecycle event was not built and nothing was recorded.",
				Fix:      fmt.Sprintf("Extract only the declared correlation fields for %q: %s.", b.nativeName, describeDeclaredFields(declared)),
			}
		}
		if field.Kind() != identity.kind {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("Correlation field %q was supplied as a %s identity, but the pinned contract declares it as a %s identity.", identity.nativeName, identity.kind, field.Kind()),
				Why:      "The waist compares correlation by KIND, not by native field name, so a mis-kinded field would correlate this occurrence with unrelated facts — inside IR the verifier had already accepted.",
				Where:    where,
				Impact:   "The lifecycle event was not built and nothing was recorded.",
				Fix:      fmt.Sprintf("Extract %q with the %s kind the pinned contract declares for it.", identity.nativeName, field.Kind()),
			}
		}

		key := suppliedKey{kind: identity.kind, nativeName: identity.nativeName}
		if _, duplicate := seen[key]; duplicate {
			return nil, &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("Correlation field %q was supplied more than once for native event %q.", identity.nativeName, b.nativeName),
				Why:      "One native field carries one value; two would make the derived correlation depend on which copy happened to win.",
				Where:    where,
				Impact:   "The lifecycle event was not built and nothing was recorded.",
				Fix:      fmt.Sprintf("Supply %q exactly once.", identity.nativeName),
			}
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
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("Native event %q is missing its required correlation field %q.", b.nativeName, field.NativeName()),
			Why:      "The pinned contract marks this field required because the recorded fact cannot be correlated back to the occurrence without it.",
			Where:    where,
			Impact:   "The lifecycle event was not built and nothing was recorded.",
			Fix:      fmt.Sprintf("Include %q in the %s payload for %q.", field.NativeName(), b.contract.Harness(), b.nativeName),
		}
	}

	slices.SortFunc(semantic, compareSemanticIdentities)
	return semantic, nil
}

// compareSemanticIdentities orders waist identities by (Kind, Value).
//
// The Value tiebreak is not decoration. Two identities of one kind must not be
// collapsed — dropping either would silently lose correlation — so the order
// between them has to come from somewhere, and the only stable source is the
// values themselves. Without the tiebreak, two frontends that extracted the
// same pair in different orders would produce different canonical keys for one
// occurrence, and it would be recorded twice.
//
// No pinned profile currently declares two identity fields of one kind, so this
// branch is unreachable through any contract that exists today. It is a forward
// invariant, and it is exercised directly by the package-internal tests rather
// than left to be discovered by the first profile that needs it.
func compareSemanticIdentities(left, right SemanticIdentity) int {
	if order := cmp.Compare(left.Kind, right.Kind); order != 0 {
		return order
	}
	return cmp.Compare(left.Value, right.Value)
}

func findDeclaredField(declared []runtime.NativeIdentityField, nativeName string) (runtime.NativeIdentityField, bool) {
	for _, field := range declared {
		if field.NativeName() == nativeName {
			return field, true
		}
	}
	return runtime.NativeIdentityField{}, false
}

// describeDeclaredFields renders the declared correlation fields for an error
// message, marking which are required so the operator knows what to add.
func describeDeclaredFields(declared []runtime.NativeIdentityField) string {
	if len(declared) == 0 {
		return "(this event declares no correlation fields)"
	}
	parts := make([]string, 0, len(declared))
	for _, field := range declared {
		suffix := " (optional)"
		if field.Required() {
			suffix = " (required)"
		}
		parts = append(parts, field.NativeName()+suffix)
	}
	return strings.Join(parts, ", ")
}
