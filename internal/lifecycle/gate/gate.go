// Package gate is the Pasture-side normative write gate: the closed set of
// durable lifecycle write classes, a constructor-owned Warrant, and Legalize as
// the sole warrant issuer.
//
// The gate replaces the M1 static no-write proof (a package that "neither
// exercises authority nor performs durable writes") with a NORMATIVE gated
// path: every durable lifecycle write must present a Warrant, and a Warrant can
// only be produced by Legalize. Threading a required Warrant through the sole
// durable writer (receipt.Service.Receive) and the non-delivery commit surface
// makes an ungated write uncompilable rather than merely absent.
//
// The gate is PURE. It imports only model and ir; it performs no clock, store,
// or I/O access and holds no runtime, env, or config policy input. At M5 policy
// is static: every well-formed intent of an enumerated class is legal, and
// everything else is refused with a typed *Refusal.
//
// Origin blindness (Stage-4 M4 seam). WriteIntent carries NOTHING but its write
// class: it deliberately has no capture-origin, trust, or actor field, and no
// constructor accepts one. The gate therefore cannot observe where a write came
// from, so origin discrimination is UNREPRESENTABLE by construction. The seam a
// future raw-ingestion milestone (M4) needs is preserved without being built:
// M4 can add its own origin marking upstream, and this gate will still be
// unable to discriminate on it. The writeIntentShape compile-time assertion
// below locks that guarantee — adding any field to WriteIntent fails to
// compile until the shape lock and this seam are consciously reconsidered.
package gate

import (
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
)

// MaxLinksPerOperation bounds the number of committed occurrence links a single
// lineage-links operation may carry. It is the closed cap the lineage write
// class validates against; a derivation producing more edges must narrow its
// scope rather than commit an unbounded operation.
const MaxLinksPerOperation = 64

// WriteClass is the closed set of Pasture-side durable lifecycle write classes.
//
// Adding a class is a reviewed enum extension — the deliberate chokepoint that
// replaced the M1 static no-write proof with a normative gated path. Every
// durable lifecycle write is exactly one of these classes.
type WriteClass uint8

const (
	// WriteDeliveryReceipt is the existing host-evidence delivery write: one
	// occurrence receipt (plus its interpreted and optional consultation
	// evidence) per host delivery, produced by receipt.Service.Receive.
	WriteDeliveryReceipt WriteClass = iota + 1
	// WriteDefinitionActivation is the lazy, idempotent codebook definition
	// journal write (definition snapshot + activation state).
	WriteDefinitionActivation
	// WriteLineageLinks is the read-side materialization of committed
	// occurrence predecessor links (1..MaxLinksPerOperation edges).
	WriteLineageLinks
	// WriteDisclosure is the context-disclosure write (plan + attempt + result
	// facts) committed as one operation before the projection is printed.
	WriteDisclosure
)

// IsValid reports whether c is one of the enumerated write classes. The zero
// value is deliberately invalid so an unset class can never be legalized.
func (c WriteClass) IsValid() bool {
	switch c {
	case WriteDeliveryReceipt, WriteDefinitionActivation, WriteLineageLinks, WriteDisclosure:
		return true
	default:
		return false
	}
}

// String returns the stable lowercase spelling of the write class, used in
// actionable refusal messages. Unknown classes render their numeric form so a
// diagnostic never silently drops an out-of-range value.
func (c WriteClass) String() string {
	switch c {
	case WriteDeliveryReceipt:
		return "delivery-receipt"
	case WriteDefinitionActivation:
		return "definition-activation"
	case WriteLineageLinks:
		return "lineage-links"
	case WriteDisclosure:
		return "disclosure"
	default:
		return "unknown-write-class"
	}
}

// WriteIntent is the constructor-validated description of a pending durable
// lifecycle write. It carries ONLY its write class: each constructor enforces
// per-class well-formedness at construction and then discards the coordinates,
// so the intent that reaches Legalize is an opaque class token that retains
// nothing capable of encoding capture origin (see the package doc's M4-seam
// note and the writeIntentShape lock below).
//
// The zero value has an invalid class and cannot be legalized.
type WriteIntent struct {
	class WriteClass
}

// writeIntentShape is a compile-time lock on WriteIntent's exact field set.
// Struct conversion succeeds only when both types have identical fields, so if
// a field is ever added to WriteIntent this assignment fails to compile —
// forcing a reviewer to reopen the origin-blindness seam before any new intent
// data (which could smuggle in capture origin) can land.
type writeIntentShape struct {
	class WriteClass
}

var (
	_ = WriteIntent(writeIntentShape{})
	_ = writeIntentShape(WriteIntent{})
)

// Class returns the write class this intent describes.
func (i WriteIntent) Class() WriteClass { return i.class }

// NewDeliveryIntent constructs a delivery-receipt write intent. It validates
// the host coordinates (a real runtime contract and a nonzero event kind) and
// then retains only the write class — the gate never sees, and cannot act on,
// which host or delivery produced the write.
//
// L1 STUB: sets the class without validating coordinates. Real per-class
// validation lands in L3 (aura-plugins-a4bbb); the L2 constructor tests fail
// against this stub until then.
func NewDeliveryIntent(contract ir.RuntimeContractID, event model.ContractEventKind) (WriteIntent, *Refusal) {
	return WriteIntent{class: WriteDeliveryReceipt}, nil
}

// NewDefinitionActivationIntent constructs a definition-activation write intent.
// It takes the codebook's content identity — the value that identifies WHAT is
// being journaled — rather than the full model.CodebookCoordinate, because the
// gate only needs a well-formed content identity to judge legality and because
// model.CodebookCoordinate is introduced by a later wave; the coordinate's
// version and id travel with the definition write itself, not the gate.
//
// L1 STUB: sets the class without validating the content identity (real
// validation lands in L3).
func NewDefinitionActivationIntent(content model.ContentIdentity) (WriteIntent, *Refusal) {
	return WriteIntent{class: WriteDefinitionActivation}, nil
}

// NewLineageIntent constructs a lineage-links write intent. It validates the
// host and the bounded edge count (1..MaxLinksPerOperation); an over-cap
// derivation is refused so the operator narrows the scope rather than committing
// an unbounded operation.
//
// L1 STUB: sets the class without validating the host or edge count (real
// validation lands in L3).
func NewLineageIntent(harness ir.HarnessID, links int) (WriteIntent, *Refusal) {
	return WriteIntent{class: WriteLineageLinks}, nil
}

// NewDisclosureIntent constructs a disclosure write intent. It validates the
// projection scope digest that fingerprints the disclosed content; the plan,
// attempt, and result facts travel with the disclosure write itself.
//
// L1 STUB: sets the class without validating the scope digest (real validation
// lands in L3).
func NewDisclosureIntent(scope model.ContentIdentity) (WriteIntent, *Refusal) {
	return WriteIntent{class: WriteDisclosure}, nil
}

// Warrant is the constructor-owned proof that a durable lifecycle write of a
// given class has been legalized. Only Legalize produces a valid Warrant; the
// zero value is invalid and every commit surface refuses it, so a write cannot
// present a warrant it did not obtain from the gate.
type Warrant struct {
	class  WriteClass
	issued bool
}

// IsValid reports whether the warrant was issued by Legalize for an enumerated
// class. The zero Warrant is never valid.
func (w Warrant) IsValid() bool { return w.issued && w.class.IsValid() }

// Class returns the write class this warrant admits.
func (w Warrant) Class() WriteClass { return w.class }

// Legalize is the SOLE issuer of Warrants. At M5 policy is pure and static:
// every well-formed intent of an enumerated class is legal, so Legalize refuses
// only the zero-value / unenumerated intent with RefusalUnknownClass. There is
// no env, config, or runtime policy input — a gate with a second policy source
// would be a different, unratified design.
//
// L1 STUB: issues a warrant for any intent without refusing the zero /
// unenumerated intent. The real static policy (and the RefusalUnknownClass
// refusal) lands in L3; the L2 Legalize-refusal tests fail against this stub.
func Legalize(intent WriteIntent) (Warrant, *Refusal) {
	return Warrant{class: intent.class, issued: true}, nil
}

// Authorize is the commit-surface check every durable lifecycle write performs
// before touching the store. It returns nil when warrant admits a write of the
// expected class; otherwise a typed *Refusal — RefusalInvalidIntent for a
// zero/unissued warrant (an ungated write) and RefusalClassMismatch for a
// warrant issued for a different class (a misrouted write). Because it runs
// before any I/O, a refused write never writes a blob or appends an operation.
//
// L1 STUB: authorizes every warrant. The real refusal policy lands in L3; the
// L2 Authorize-refusal and read-back-empty tests fail against this stub.
func Authorize(warrant Warrant, expected WriteClass) *Refusal {
	return nil
}
