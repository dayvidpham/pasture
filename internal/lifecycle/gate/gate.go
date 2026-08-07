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
func NewDeliveryIntent(contract ir.RuntimeContractID, event model.ContractEventKind) (WriteIntent, *Refusal) {
	if !contract.IsValid() {
		return WriteIntent{}, refuseInvalidIntent(WriteDeliveryReceipt,
			"a delivery-receipt write intent has no runtime contract",
			"a delivery receipt must name the exact enabled host contract that produced it",
			"supply the generated runtime contract coordinate from the dispatched delivery")
	}
	if event == 0 {
		return WriteIntent{}, refuseInvalidIntent(WriteDeliveryReceipt,
			"a delivery-receipt write intent has no event kind",
			"a receipt with an unknown event kind cannot be interpreted or queried safely",
			"supply the generated typed event kind from the dispatched delivery")
	}
	return WriteIntent{class: WriteDeliveryReceipt}, nil
}

// NewDefinitionActivationIntent constructs a definition-activation write intent.
// It takes the codebook's content identity — the value that identifies WHAT is
// being journaled — rather than the full model.CodebookCoordinate, because the
// gate only needs a well-formed content identity to judge legality and because
// model.CodebookCoordinate is introduced by a later wave; the coordinate's
// version and id travel with the definition write itself, not the gate.
func NewDefinitionActivationIntent(content model.ContentIdentity) (WriteIntent, *Refusal) {
	if content == (model.ContentIdentity{}) {
		return WriteIntent{}, refuseInvalidIntent(WriteDefinitionActivation,
			"a definition-activation write intent has no content identity",
			"a journaled codebook definition must be addressed by the sha256 of its canonical body",
			"supply the active codebook coordinate's content identity")
	}
	return WriteIntent{class: WriteDefinitionActivation}, nil
}

// NewLineageIntent constructs a lineage-links write intent. It validates the
// host and the bounded edge count (1..MaxLinksPerOperation); an over-cap
// derivation is refused so the operator narrows the scope rather than committing
// an unbounded operation.
func NewLineageIntent(harness ir.HarnessID, links int) (WriteIntent, *Refusal) {
	if !harness.IsValid() {
		return WriteIntent{}, refuseInvalidIntent(WriteLineageLinks,
			"a lineage-links write intent has no host",
			"occurrence chains are reconstructed per host, so a lineage write must name one enabled harness",
			"supply an enabled harness identity for the chain being materialized")
	}
	if links < 1 || links > MaxLinksPerOperation {
		return WriteIntent{}, refuseInvalidIntent(WriteLineageLinks,
			"a lineage-links write intent is empty or over the per-operation cap",
			"a lineage operation commits between one and the bounded maximum of predecessor edges so one write cannot grow unbounded",
			"narrow the scope with --binding so the derivation yields at most the per-operation cap of edges")
	}
	return WriteIntent{class: WriteLineageLinks}, nil
}

// NewDisclosureIntent constructs a disclosure write intent. It validates the
// projection scope digest that fingerprints the disclosed content; the plan,
// attempt, and result facts travel with the disclosure write itself.
func NewDisclosureIntent(scope model.ContentIdentity) (WriteIntent, *Refusal) {
	if scope == (model.ContentIdentity{}) {
		return WriteIntent{}, refuseInvalidIntent(WriteDisclosure,
			"a disclosure write intent has no projection scope",
			"a disclosure records the sha256 fingerprint of the exact projection it releases",
			"supply the canonical projection content digest as the disclosure scope")
	}
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
func Legalize(intent WriteIntent) (Warrant, *Refusal) {
	if !intent.class.IsValid() {
		return Warrant{}, newRefusal(intent.class, RefusalUnknownClass,
			"a durable lifecycle write was refused: its write class is not one of the enumerated Pasture-side classes",
			"Legalize issues warrants only for the closed WriteClass set, so a zero-value or unenumerated intent cannot be legalized",
			"legalizing a lifecycle write intent (internal/lifecycle/gate in gate.Legalize)",
			"no warrant was issued and no durable write can proceed",
			"construct the intent with one of the gate.NewXxxIntent constructors for an enumerated write class before calling Legalize")
	}
	return Warrant{class: intent.class, issued: true}, nil
}

// Authorize is the commit-surface check every durable lifecycle write performs
// before touching the store. It returns nil when warrant admits a write of the
// expected class; otherwise a typed *Refusal — RefusalInvalidIntent for a
// zero/unissued warrant (an ungated write) and RefusalClassMismatch for a
// warrant issued for a different class (a misrouted write). Because it runs
// before any I/O, a refused write never writes a blob or appends an operation.
func Authorize(warrant Warrant, expected WriteClass) *Refusal {
	if !expected.IsValid() {
		return newRefusal(expected, RefusalUnknownClass,
			"a durable lifecycle write was refused: the commit surface named an unenumerated expected class",
			"a commit surface must authorize against one enumerated WriteClass",
			"authorizing a lifecycle write (internal/lifecycle/gate in gate.Authorize)",
			"nothing was written",
			"pass one of the enumerated gate.WriteClass constants as the expected class")
	}
	if !warrant.IsValid() {
		return newRefusal(expected, RefusalInvalidIntent,
			"a durable "+expected.String()+" write was refused: it presented no gate warrant",
			"every durable lifecycle write must present a Warrant issued by gate.Legalize; a zero-value warrant means the write was never legalized",
			"authorizing a lifecycle write (internal/lifecycle/gate in gate.Authorize)",
			"nothing was written",
			"obtain a warrant with gate.Legalize for the write's class and present it at the commit surface")
	}
	if warrant.Class() != expected {
		return newRefusal(warrant.Class(), RefusalClassMismatch,
			"a durable "+expected.String()+" write was refused: it presented a "+warrant.Class().String()+" warrant",
			"a warrant only admits the exact class it was legalized for, so a warrant for another class cannot authorize this write",
			"authorizing a lifecycle write (internal/lifecycle/gate in gate.Authorize)",
			"nothing was written",
			"legalize an intent of the "+expected.String()+" class and present that warrant at this commit surface")
	}
	return nil
}
