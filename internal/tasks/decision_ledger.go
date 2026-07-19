// Package tasks — decision-ledger BASE types (#43).
//
// decision_ledger.go holds ONLY the generic, declarative, serializable decision-ledger
// base types #43 owns: the compile-time typed descriptor/draft extension-construction
// primitives, the one immutable DecisionCatalog, the stored encoding/entry/query/UAT
// reference types, and the CoverageDigest. It contains NO policy: no concrete decision
// kinds, no AFK/UAT/ratify/land rules, no session or permission logic. #49 (the ordered
// same-package issue) adds the production PolicySet and the concrete descriptors on top
// of exactly these primitives.
//
// Fixed encoding identity. A decision's codec identity is its DecisionCodecID plus its
// DecisionSchemaDigest — never a function pointer. Two descriptors that agree on
// kind/codec/schema are the same stored shape even with different function values, so a
// deliberate encoding change forces a schema-digest (version) change, which
// golden-fixture tests pin. Descriptor construction mints an opaque token; only a draft
// carrying a catalog-registered token can be appended, and DecodeDecision additionally
// requires the exact registered descriptor token. Type-erased catalog validation decodes
// a copied payload, revalidates the typed value, re-encodes it, and requires
// byte-identical canonical output.
//
// Delivered-surface note (S3.2-style). The issue's Go sketch spells the actor-kind type
// `provenance.ActorKind`; the released Provenance surface exports it as
// `provenance.AgentKind` (a distinct identity domain was never introduced — AgentKind is
// canonical). DecisionAttribution/DecisionQuery therefore use provenance.AgentKind,
// mapping the issue's ActorKind onto the delivered name exactly like the AssignmentSlotID
// mapping.

package tasks

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// DecisionKindID is a decision's stable kind identity (the ledger row's kind column).
type DecisionKindID string

// DecisionCodecID names the canonical codec a decision's payload is encoded with.
type DecisionCodecID string

// DecisionSchemaDigest pins the exact schema a codec targets; changing the encoding
// forces a new digest, which golden fixtures verify.
type DecisionSchemaDigest [32]byte

// CanonicalDecisionPayload is the opaque, canonical encoded bytes of one decision value.
type CanonicalDecisionPayload []byte

// descriptorToken is the opaque per-descriptor identity. Its address (a distinct pointer
// per NewDecisionDescriptor call) is the runtime append token. It carries one unused byte
// so it is NOT zero-size: pointers to zero-size types may share the runtime.zerobase
// address and compare equal, which would defeat per-descriptor token identity.
type descriptorToken struct{ _ byte }

// DecisionLedgerEntryID identifies one immutable ledger entry.
type DecisionLedgerEntryID string

// EpochRootID identifies the protected epoch-root/REQUEST the ledger belongs to.
type EpochRootID string

// DocumentRevisionID identifies one document revision (proposal/ledger revision). It is
// deliberately named DocumentRevisionID, not TaskRevisionID, to avoid confusion with a
// Provenance CAS token.
type DocumentRevisionID string

// PlanUATDecisionID identifies one Plan-UAT snapshot reference.
type PlanUATDecisionID string

// DecisionDescriptor is a compile-time typed, validated codec for one decision value
// type T. It is constructed by NewDecisionDescriptor, which mints its opaque token; only
// a successfully constructed descriptor can produce a draft. Its function fields are
// construction-time behavior, never serialized and never codec identity.
type DecisionDescriptor[T any] struct {
	kind     DecisionKindID
	codec    DecisionCodecID
	schema   DecisionSchemaDigest
	token    *descriptorToken
	validate func(T) error
	encode   func(T) (CanonicalDecisionPayload, error)
	decode   func(CanonicalDecisionPayload) (T, error)
}

// NewDecisionDescriptor validates a decision codec's own kind/codec/schema/functions and
// mints an opaque token. It rejects an empty kind or codec, a zero schema digest, and any
// nil function; it does NOT pretend an isolated constructor can detect another
// descriptor's kind (catalog construction owns cross-descriptor conflict detection).
func NewDecisionDescriptor[T any](
	kind DecisionKindID,
	codec DecisionCodecID,
	schema DecisionSchemaDigest,
	validate func(T) error,
	encode func(T) (CanonicalDecisionPayload, error),
	decode func(CanonicalDecisionPayload) (T, error),
) (DecisionDescriptor[T], error) {
	if kind == "" {
		return DecisionDescriptor[T]{}, decisionErr("DecisionDescriptor", "the decision kind is empty",
			"a descriptor's kind is its stored ledger identity and cannot be empty", "supply a non-empty DecisionKindID")
	}
	if codec == "" {
		return DecisionDescriptor[T]{}, decisionErr("DecisionDescriptor", "the codec id is empty",
			"a descriptor's codec names its canonical encoding and cannot be empty", "supply a non-empty DecisionCodecID")
	}
	if schema == (DecisionSchemaDigest{}) {
		return DecisionDescriptor[T]{}, decisionErr("DecisionDescriptor", "the schema digest is zero",
			"a zero schema digest cannot pin a codec's version, so encoding changes could go undetected",
			"supply the non-zero schema digest of the codec's target schema")
	}
	if validate == nil || encode == nil || decode == nil {
		return DecisionDescriptor[T]{}, decisionErr("DecisionDescriptor", "a validate/encode/decode function is nil",
			"a descriptor must be able to validate, canonically encode, and decode its value type",
			"supply non-nil validate, encode, and decode functions")
	}
	return DecisionDescriptor[T]{
		kind:     kind,
		codec:    codec,
		schema:   schema,
		token:    &descriptorToken{},
		validate: validate,
		encode:   encode,
		decode:   decode,
	}, nil
}

// Kind, Codec, Schema expose the descriptor's stored identity for manifest/inspection.
func (d DecisionDescriptor[T]) Kind() DecisionKindID         { return d.kind }
func (d DecisionDescriptor[T]) Codec() DecisionCodecID       { return d.codec }
func (d DecisionDescriptor[T]) Schema() DecisionSchemaDigest { return d.schema }
func (d DecisionDescriptor[T]) constructed() bool            { return d.token != nil }

// Draft validates a typed value and canonically encodes it, retaining the descriptor's
// token so only a catalog-registered descriptor's draft can later be appended. It copies
// the encoded payload defensively so the returned draft owns its bytes.
func (d DecisionDescriptor[T]) Draft(value T) (DecisionDraft, error) {
	if !d.constructed() {
		return DecisionDraft{}, decisionErr("Draft", "the descriptor is a zero value",
			"only a descriptor built by NewDecisionDescriptor can produce a draft", "construct the descriptor with NewDecisionDescriptor")
	}
	if err := d.validate(value); err != nil {
		return DecisionDraft{}, fmt.Errorf("decision draft for kind %q: value validation failed: %w", d.kind, err)
	}
	payload, err := d.encode(value)
	if err != nil {
		return DecisionDraft{}, fmt.Errorf("decision draft for kind %q: canonical encode failed: %w", d.kind, err)
	}
	return DecisionDraft{
		kind:    d.kind,
		codec:   d.codec,
		schema:  d.schema,
		token:   d.token,
		payload: copyPayload(payload),
	}, nil
}

// DecisionDraft is an opaque, catalog-issued pending decision. Callers never construct a
// DecisionEncoding directly; a draft is the only carrier a ledger mutation (#49) accepts.
type DecisionDraft struct {
	kind    DecisionKindID
	codec   DecisionCodecID
	schema  DecisionSchemaDigest
	token   *descriptorToken
	payload CanonicalDecisionPayload
}

// encoding returns the stored DecisionEncoding for this draft. It is package-private: the
// append path (#49) persists it, but callers outside the package cannot fabricate one.
func (d DecisionDraft) encoding() DecisionEncoding {
	return DecisionEncoding{
		Kind:    d.kind,
		Codec:   d.codec,
		Schema:  d.schema,
		Payload: copyPayload(d.payload),
	}
}

// DecisionBinding is a type-erased catalog binding produced by BindDecision. Its concrete
// type is private so a caller cannot fabricate one.
type DecisionBinding interface {
	decisionBinding()
	entry() DecisionCatalogEntry
	token() *descriptorToken
	// validateStored decodes a copied payload, revalidates the typed value, re-encodes
	// it, and requires byte-identical canonical output.
	validateStored(enc DecisionEncoding) error
}

type typedBinding[T any] struct {
	d DecisionDescriptor[T]
}

func (typedBinding[T]) decisionBinding() {}

func (b typedBinding[T]) entry() DecisionCatalogEntry {
	return DecisionCatalogEntry{Kind: b.d.kind, Codec: b.d.codec, Schema: b.d.schema}
}

func (b typedBinding[T]) token() *descriptorToken { return b.d.token }

func (b typedBinding[T]) validateStored(enc DecisionEncoding) error {
	if enc.Kind != b.d.kind || enc.Codec != b.d.codec || enc.Schema != b.d.schema {
		return decisionErr("ValidateStored",
			fmt.Sprintf("stored encoding identity (%q/%q) does not match the registered descriptor (%q/%q)", enc.Kind, enc.Codec, b.d.kind, b.d.codec),
			"a stored decision must carry the exact registered kind/codec/schema",
			"store only encodings produced by a catalog-registered descriptor draft")
	}
	value, err := b.d.decode(copyPayload(enc.Payload))
	if err != nil {
		return fmt.Errorf("validate stored decision %q: decode failed: %w", enc.Kind, err)
	}
	if err := b.d.validate(value); err != nil {
		return fmt.Errorf("validate stored decision %q: decoded value invalid: %w", enc.Kind, err)
	}
	reencoded, err := b.d.encode(value)
	if err != nil {
		return fmt.Errorf("validate stored decision %q: re-encode failed: %w", enc.Kind, err)
	}
	if !bytes.Equal(reencoded, enc.Payload) {
		return decisionErr("ValidateStored", fmt.Sprintf("stored payload for kind %q is not canonical", enc.Kind),
			"the stored bytes differ from the descriptor's re-encoding of the decoded value, so the payload is non-canonical",
			"store only the descriptor's canonical encoding")
	}
	return nil
}

// BindDecision type-erases a descriptor into a catalog binding.
func BindDecision[T any](d DecisionDescriptor[T]) DecisionBinding {
	return typedBinding[T]{d: d}
}

// DecisionCatalogEntry is the public, serializable manifest row for one registered kind.
type DecisionCatalogEntry struct {
	Kind   DecisionKindID       `json:"kind"`
	Codec  DecisionCodecID      `json:"codec"`
	Schema DecisionSchemaDigest `json:"schema"`
}

// DecisionCatalog is an immutable set of registered decision bindings keyed by kind. It
// is constructed once by NewDecisionCatalog and never mutated.
type DecisionCatalog struct {
	byKind   map[DecisionKindID]DecisionBinding
	manifest []DecisionCatalogEntry
}

// NewDecisionCatalog validates and freezes a set of bindings. It rejects an empty or
// duplicate kind and any codec/schema conflict independent of registration order. The
// concrete reserved-kind policy is #49's; #43's base construction rejects only empty and
// duplicate kinds.
func NewDecisionCatalog(bindings ...DecisionBinding) (DecisionCatalog, error) {
	byKind := make(map[DecisionKindID]DecisionBinding, len(bindings))
	manifest := make([]DecisionCatalogEntry, 0, len(bindings))
	for i, b := range bindings {
		if b == nil {
			return DecisionCatalog{}, decisionErr("DecisionCatalog", fmt.Sprintf("binding %d is nil", i),
				"a catalog binding must be produced by BindDecision", "remove the nil binding")
		}
		e := b.entry()
		if e.Kind == "" {
			return DecisionCatalog{}, decisionErr("DecisionCatalog", fmt.Sprintf("binding %d has an empty kind", i),
				"every registered decision kind must be non-empty", "register only descriptors with a non-empty kind")
		}
		if _, dup := byKind[e.Kind]; dup {
			return DecisionCatalog{}, decisionErr("DecisionCatalog", fmt.Sprintf("kind %q is registered more than once", e.Kind),
				"a catalog maps each kind to exactly one codec/schema, so a duplicate kind is a conflict regardless of order",
				"register each decision kind exactly once")
		}
		byKind[e.Kind] = b
		manifest = append(manifest, e)
	}
	return DecisionCatalog{byKind: byKind, manifest: manifest}, nil
}

// Manifest returns the registered entries in registration order. The returned slice never
// aliases the catalog's own storage.
func (c DecisionCatalog) Manifest() []DecisionCatalogEntry {
	out := make([]DecisionCatalogEntry, len(c.manifest))
	copy(out, c.manifest)
	return out
}

// ValidateDraft accepts a draft only if the catalog registers a binding whose token is
// the draft's exact descriptor token and whose kind/codec/schema match. This is the
// runtime-append gate: a draft from an unregistered descriptor is rejected.
func (c DecisionCatalog) ValidateDraft(draft DecisionDraft) error {
	b, ok := c.byKind[draft.kind]
	if !ok {
		return decisionErr("ValidateDraft", fmt.Sprintf("kind %q is not registered", draft.kind),
			"only a draft of a catalog-registered descriptor may be appended", "register the descriptor before drafting")
	}
	if b.token() != draft.token {
		return decisionErr("ValidateDraft", fmt.Sprintf("draft for kind %q carries an unregistered descriptor token", draft.kind),
			"the draft was produced by a descriptor that is not the one registered under this kind",
			"draft from the exact descriptor registered in the catalog")
	}
	e := b.entry()
	if draft.codec != e.Codec || draft.schema != e.Schema {
		return decisionErr("ValidateDraft", fmt.Sprintf("draft codec/schema for kind %q does not match the registered descriptor", draft.kind),
			"the draft's codec/schema disagree with the registered descriptor's", "draft from the registered descriptor")
	}
	return nil
}

// ValidateStored validates a persisted encoding against its registered descriptor: the
// kind must be registered, and the type-erased binding decodes/revalidates/re-encodes and
// requires byte-identical canonical output.
func (c DecisionCatalog) ValidateStored(encoding DecisionEncoding) error {
	b, ok := c.byKind[encoding.Kind]
	if !ok {
		return decisionErr("ValidateStored", fmt.Sprintf("kind %q is not registered", encoding.Kind),
			"a stored decision must belong to a registered kind", "register the descriptor for this kind")
	}
	return b.validateStored(encoding)
}

// DecodeDecision decodes a stored encoding to its typed value using the supplied
// descriptor, but only after confirming the catalog registers that exact descriptor token
// for the encoding's kind and that the identities agree; it then decodes, validates, and
// requires the re-encoding to be byte-identical.
func DecodeDecision[T any](
	catalog DecisionCatalog,
	descriptor DecisionDescriptor[T],
	encoding DecisionEncoding,
) (T, error) {
	var zero T
	if !descriptor.constructed() {
		return zero, decisionErr("DecodeDecision", "the descriptor is a zero value",
			"decoding requires a descriptor built by NewDecisionDescriptor", "construct the descriptor with NewDecisionDescriptor")
	}
	b, ok := catalog.byKind[encoding.Kind]
	if !ok {
		return zero, decisionErr("DecodeDecision", fmt.Sprintf("kind %q is not registered", encoding.Kind),
			"only a registered kind can be decoded through the catalog", "register the descriptor for this kind")
	}
	if b.token() != descriptor.token {
		return zero, decisionErr("DecodeDecision", fmt.Sprintf("descriptor for kind %q is not the registered one", encoding.Kind),
			"DecodeDecision requires the exact descriptor token registered in the catalog",
			"decode with the descriptor registered in the catalog")
	}
	if encoding.Codec != descriptor.codec || encoding.Schema != descriptor.schema || encoding.Kind != descriptor.kind {
		return zero, decisionErr("DecodeDecision", fmt.Sprintf("encoding identity does not match descriptor for kind %q", encoding.Kind),
			"the stored kind/codec/schema must match the descriptor's", "decode with the descriptor that produced the encoding")
	}
	value, err := descriptor.decode(copyPayload(encoding.Payload))
	if err != nil {
		return zero, fmt.Errorf("decode decision %q: %w", encoding.Kind, err)
	}
	if err := descriptor.validate(value); err != nil {
		return zero, fmt.Errorf("decode decision %q: decoded value invalid: %w", encoding.Kind, err)
	}
	reencoded, err := descriptor.encode(value)
	if err != nil {
		return zero, fmt.Errorf("decode decision %q: re-encode failed: %w", encoding.Kind, err)
	}
	if !bytes.Equal(reencoded, encoding.Payload) {
		return zero, decisionErr("DecodeDecision", fmt.Sprintf("stored payload for kind %q is not canonical", encoding.Kind),
			"the decoded value re-encodes to different bytes, so the stored payload is non-canonical",
			"store only the descriptor's canonical encoding")
	}
	return value, nil
}

// DecisionEncoding is the persisted, serializable form of one decision: its identity plus
// canonical payload bytes. Callers never construct it directly (a draft or the append path
// does); it is public so the ledger entry can carry it.
type DecisionEncoding struct {
	Kind    DecisionKindID           `json:"kind"`
	Codec   DecisionCodecID          `json:"codec"`
	Schema  DecisionSchemaDigest     `json:"schema"`
	Payload CanonicalDecisionPayload `json:"payload"`
}

// DecisionAttribution is the decider/recorder attribution stored on a ledger entry. The
// Decider is the epoch root's registered user; the Recorder is the appending assignment's
// actor. Kinds use provenance.AgentKind (the delivered spelling of the issue's ActorKind).
type DecisionAttribution struct {
	Decider      provenance.ActorID   `json:"decider"`
	DeciderKind  provenance.AgentKind `json:"deciderKind"`
	Recorder     provenance.ActorID   `json:"recorder"`
	RecorderKind provenance.AgentKind `json:"recorderKind"`
}

// DecisionLedgerEntry is one immutable ledger entry. Decision is the sole purpose-specific
// stored payload, so question/options/verbatim/rationale/resolution fields cannot drift
// beside it. SourceRequest and Context are optional.
type DecisionLedgerEntry struct {
	ID            DecisionLedgerEntryID     `json:"id"`
	Epoch         EpochRootID               `json:"epoch"`
	Actor         DecisionAttribution       `json:"actor"`
	SourceRequest *ir.UserDecisionRequestID `json:"sourceRequest,omitempty"`
	Context       *DecisionEncoding         `json:"context,omitempty"`
	Decision      DecisionEncoding          `json:"decision"`
}

// DecisionQuery is a read-only projection filter over ledger entries by decider/recorder
// actor, actor-kind, and decision kind. Empty slices match everything on that axis.
type DecisionQuery struct {
	Deciders      []provenance.ActorID   `json:"deciders,omitempty"`
	Recorders     []provenance.ActorID   `json:"recorders,omitempty"`
	DeciderKinds  []provenance.AgentKind `json:"deciderKinds,omitempty"`
	RecorderKinds []provenance.AgentKind `json:"recorderKinds,omitempty"`
	Kinds         []DecisionKindID       `json:"kinds,omitempty"`
}

// PlanUATSnapshot is an immutable reference tying a Plan-UAT decision entry to its subject
// proposal revision and the input/output ledger revisions land compares.
type PlanUATSnapshot struct {
	ID            PlanUATDecisionID     `json:"id"`
	UATTaskID     provenance.TaskID     `json:"uatTaskId"`
	Proposal      DocumentRevisionID    `json:"proposal"`
	DecisionEntry DecisionLedgerEntryID `json:"decisionEntry"`
	InputLedger   DocumentRevisionID    `json:"inputLedger"`
	OutputLedger  DocumentRevisionID    `json:"outputLedger"`
}

// CoverageDigest is the accepted coverage digest a UAT records and land recomputes/
// compares. Its text form is "sha256:<hex>".
type CoverageDigest [32]byte

const coverageDigestPrefix = "sha256:"

// MarshalText renders the digest as "sha256:<hex>".
func (d CoverageDigest) MarshalText() ([]byte, error) {
	out := make([]byte, 0, len(coverageDigestPrefix)+2*len(d))
	out = append(out, coverageDigestPrefix...)
	out = append(out, []byte(hex.EncodeToString(d[:]))...)
	return out, nil
}

// UnmarshalText parses a "sha256:<hex>" digest so CoverageDigest round-trips through JSON.
func (d *CoverageDigest) UnmarshalText(text []byte) error {
	parsed, err := ParseCoverageDigest(text)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// ParseCoverageDigest parses a "sha256:<hex>" coverage digest, rejecting a missing prefix
// or a wrong-length hex body.
func ParseCoverageDigest(text []byte) (CoverageDigest, error) {
	s := string(text)
	if len(s) < len(coverageDigestPrefix) || s[:len(coverageDigestPrefix)] != coverageDigestPrefix {
		return CoverageDigest{}, decisionErr("ParseCoverageDigest", fmt.Sprintf("%q has no %q prefix", s, coverageDigestPrefix),
			"a coverage digest's text form is sha256:<64 hex chars>", "supply a sha256:<hex> digest")
	}
	body := s[len(coverageDigestPrefix):]
	raw, err := hex.DecodeString(body)
	if err != nil {
		return CoverageDigest{}, decisionErr("ParseCoverageDigest", fmt.Sprintf("body %q is not valid hex", body),
			"the digest body must be lower-case hex", "supply a hex-encoded 32-byte digest")
	}
	if len(raw) != 32 {
		return CoverageDigest{}, decisionErr("ParseCoverageDigest", fmt.Sprintf("body decodes to %d bytes, want 32", len(raw)),
			"a coverage digest is exactly 32 bytes", "supply a 32-byte (64 hex char) digest")
	}
	var out CoverageDigest
	copy(out[:], raw)
	return out, nil
}

// copyPayload returns an independently-owned copy of a payload so callers cannot alias a
// descriptor's or draft's buffers.
func copyPayload(in CanonicalDecisionPayload) CanonicalDecisionPayload {
	if in == nil {
		return nil
	}
	out := make(CanonicalDecisionPayload, len(in))
	copy(out, in)
	return out
}

func decisionErr(where, what, why, fix string) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     fmt.Sprintf("Pasture rejected a decision-ledger operation: %s.", what),
		Why:      why + ".",
		Where:    fmt.Sprintf("Decision-ledger base types (internal/tasks/decision_ledger.go, %s).", where),
		Impact:   "The decision-ledger value was not constructed or accepted; nothing was persisted.",
		Fix:      fix + ".",
	}
}
