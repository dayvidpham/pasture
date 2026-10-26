package receipt

import (
	"bytes"
	"context"
	"slices"
	"sync"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// occurrenceKindV2 is the durable evidence kind for occurrences that carry
// the complete four-reference envelope. It is a SEPARATE kind from
// occurrence.v1: the v1 read path never sees it, so committed legacy zero-ref
// records decode unchanged and are never proof of completeness. Nothing but
// the common receipt boundary emits v2.
const occurrenceKindV2 = provenance.EvidenceKind("pasture.lifecycle.occurrence.v2")

// CurrentOccurrenceEvidenceKind identifies newly committed occurrences.
// Readers admit the historical kind alongside it.
func CurrentOccurrenceEvidenceKind() provenance.EvidenceKind { return occurrenceKindV2 }

// EnsureOccurrenceEnvelope resolves the complete four-reference occurrence
// envelope for one delivery contract: the contract's own runtime document
// plus the shared schema, epoch implementation and retention documents. Every
// ref is ensured through the real definition owners, so a returned ref always
// names a durably committed, owner-resolvable definition.
//
// A caller-supplied nonzero ref is validated and must equal the owner
// document selected for this write; a wrong, dangling or nonmatching supplied
// value is refused rather than silently replaced. Unset slots are populated
// by the owner, never accepted as a committed envelope. Provenance carriers
// (host version, source, origin) pass through untouched: the envelope refs
// are admission, the carriers are observation.
func EnsureOccurrenceEnvelope(ctx context.Context, service Service, contract ir.RuntimeContractID, observed model.OccurrenceEnvelopeRef) (model.OccurrenceEnvelopeRef, error) {
	const where = "Ensuring the complete occurrence envelope (internal/lifecycle/receipt/occurrence_v2.go in receipt.EnsureOccurrenceEnvelope)."
	documents, _, err := memoizedActiveDocuments(contract)
	if err != nil {
		return model.OccurrenceEnvelopeRef{}, err
	}
	if service.Appender.Journal == nil {
		return model.OccurrenceEnvelopeRef{}, envelopeError(where, "The lifecycle receipt service cannot ensure definition references.", "Envelope refs are journaled owner facts, so the provenance journal is required to ensure them.", "No occurrence was committed.", "Construct the service through tasks.NewLifecycleReceiptService.", nil)
	}
	reader, err := service.definitionReader()
	if err != nil {
		return model.OccurrenceEnvelopeRef{}, err
	}
	// Each document resolves under a freshly captured post-commit watermark:
	// this unit is explicit pre-activation, not the batch hot path, so
	// clarity beats sharing one capture here.
	runtimeRef, _, _, err := ensureActiveDefinitionAt(ctx, service, documents.Runtime, reader, 0)
	if err != nil {
		return model.OccurrenceEnvelopeRef{}, err
	}
	schemaRef, _, _, err := ensureActiveDefinitionAt(ctx, service, documents.Schema, reader, 0)
	if err != nil {
		return model.OccurrenceEnvelopeRef{}, err
	}
	implementationRef, _, _, err := ensureActiveDefinitionAt(ctx, service, documents.EpochImplementation, reader, 0)
	if err != nil {
		return model.OccurrenceEnvelopeRef{}, err
	}
	retentionRef, _, _, err := ensureActiveDefinitionAt(ctx, service, documents.Retention, reader, 0)
	if err != nil {
		return model.OccurrenceEnvelopeRef{}, err
	}
	complete := model.OccurrenceEnvelopeRef{
		Runtime:        model.RuntimeContractDefinitionRef{Definition: runtimeRef, Contract: contract},
		Schema:         model.LifecycleSchemaDefinitionRef{Definition: schemaRef},
		Implementation: model.EpochImplementationRef{Definition: implementationRef},
		Retention:      model.RetentionPolicyDefinitionRef{Definition: retentionRef},
	}
	complete.HostVersion = observed.HostVersion
	complete.HostVersionSource = observed.HostVersionSource
	complete.Origin = observed.Origin
	if observed.Runtime.Contract.IsValid() && observed.Runtime.Contract != contract {
		return model.OccurrenceEnvelopeRef{}, envelopeError(where, "The supplied runtime contract does not match the delivery contract.", "The envelope runtime ref resolves to the runtime body whose contract must equal the delivery contract.", "No occurrence was committed; nothing was replaced.", "Supply the delivery contract unchanged as the envelope runtime contract.", nil)
	}
	supplied := []struct {
		name     string
		have     model.DefinitionRef
		want     model.DefinitionRef
		supplied bool
	}{
		{"runtime", observed.Runtime.Definition, complete.Runtime.Definition, isSuppliedRef(observed.Runtime.Definition)},
		{"schema", observed.Schema.Definition, complete.Schema.Definition, isSuppliedRef(observed.Schema.Definition)},
		{"implementation", observed.Implementation.Definition, complete.Implementation.Definition, isSuppliedRef(observed.Implementation.Definition)},
		{"retention", observed.Retention.Definition, complete.Retention.Definition, isSuppliedRef(observed.Retention.Definition)},
	}
	for _, slot := range supplied {
		if slot.supplied && slot.have != slot.want {
			return model.OccurrenceEnvelopeRef{}, envelopeError(where, "The supplied occurrence envelope "+slot.name+" reference does not match the owner document for this write.", "A wrong, dangling or nonmatching supplied value is refused rather than silently replaced; unset slots are populated by the owner.", "No occurrence was committed; nothing was replaced.", "Omit the envelope refs and let the owner populate them, or supply the exact owner refs for this contract.", nil)
		}
	}
	return complete, nil
}

// activeDocumentsMemoEntry is the memoized pure result of resolving one
// delivery contract to its owner documents. The documents are compiled into
// the binary (embedded generated bodies, deterministic construction), so the
// entry is a pure function of (binary, contract) with no store input and no
// staleness: the same binary always builds the same documents. This memoizes
// CPU only — journal truth (LookupCommitted, evidence reads) is never cached,
// and every ref is still ensured and resolved through the owners per write.
type activeDocumentsMemoEntry struct {
	documents definitions.ActiveDocuments
	runtime   ir.RuntimeContractID
	err       error
}

var activeDocumentsMemo sync.Map // ir.RuntimeContractID -> activeDocumentsMemoEntry

func memoizedActiveDocuments(delivery ir.RuntimeContractID) (definitions.ActiveDocuments, ir.RuntimeContractID, error) {
	if cached, ok := activeDocumentsMemo.Load(delivery); ok {
		entry := cached.(activeDocumentsMemoEntry)
		return entry.documents, entry.runtime, entry.err
	}
	documents, runtimeContract, err := buildActiveDocuments(delivery)
	activeDocumentsMemo.Store(delivery, activeDocumentsMemoEntry{documents: documents, runtime: runtimeContract, err: err})
	return documents, runtimeContract, err
}

// buildActiveDocuments resolves one delivery contract to its complete owner
// document set, proving the runtime document was built for exactly this
// contract's wire and the shared schema describes the wire the current codec
// emits. It is pure and memoized by memoizedActiveDocuments; see that memo
// for the caching contract.
func buildActiveDocuments(contract ir.RuntimeContractID) (definitions.ActiveDocuments, ir.RuntimeContractID, error) {
	const where = "Ensuring the complete occurrence envelope (internal/lifecycle/receipt/occurrence_v2.go in receipt.buildActiveDocuments)."
	// The delivery names the wire profile; the runtime document names the
	// versioned execution profile compiled for that wire. The owner proves
	// they are the same profile; an unregistered delivery contract is refused,
	// not guessed.
	runtimeContract, err := definitions.RuntimeContractForDelivery(contract)
	if err != nil {
		return definitions.ActiveDocuments{}, ir.RuntimeContractID{}, envelopeError(where, "The delivery contract names no compiled runtime profile.", "The wire contract must resolve to exactly one versioned execution profile compiled for it.", "No occurrence was committed.", "Supply a registered wire contract from the generated support report.", err)
	}
	documents, err := definitions.ActiveForContract(runtimeContract)
	if err != nil {
		return definitions.ActiveDocuments{}, ir.RuntimeContractID{}, envelopeError(where, "The delivery contract has no complete owner document set.", "The runtime document is selected by contract and the remaining four are the shared compiled documents; without all five no complete envelope can be admitted.", "No occurrence was committed.", "Supply a registered runtime contract with a compiled owner document.", err)
	}
	// The runtime document must be the one built for THIS contract, not an
	// arbitrary historical document admitted under the current codec.
	expectedRuntime, err := definitions.NewRuntimeContract(runtimeContract)
	if err != nil {
		return definitions.ActiveDocuments{}, ir.RuntimeContractID{}, envelopeError(where, "The delivery contract has no compiled runtime document.", "The contract names no generated runtime body in this binary.", "No occurrence was committed.", "Supply a registered runtime contract from the generated support report.", err)
	}
	if documents.Runtime.Content() != expectedRuntime.Content() || !bytes.Equal(documents.Runtime.Body(), expectedRuntime.Body()) {
		return definitions.ActiveDocuments{}, ir.RuntimeContractID{}, envelopeError(where, "The runtime document does not match the delivery contract.", "The owner document set must be built for the exact contract that produced the delivery; a caller cannot substitute another contract's document.", "No occurrence was committed.", "Use the owner document set for the delivery contract without replacing its runtime document.", nil)
	}
	// The runtime body must pair with the delivery wire: its stamped
	// registration coordinate equals the delivery contract. This is the
	// runtime side of the codec pairing the occurrence envelope carries.
	runtimeSpecification, err := definitions.ParseRuntimeSpecification(documents.Runtime.Body())
	if err != nil {
		return definitions.ActiveDocuments{}, ir.RuntimeContractID{}, envelopeError(where, "The runtime document is unreadable.", "The runtime owner body failed its typed codec.", "No occurrence was committed.", "Regenerate the runtime document and retry the delivery.", err)
	}
	if runtimeSpecification.Registration.Contract != contract {
		return definitions.ActiveDocuments{}, ir.RuntimeContractID{}, envelopeError(where, "The runtime document is not compiled for the delivery wire.", "The runtime body's registration coordinate must equal the delivery contract; a profile compiled for another wire cannot admit this delivery.", "No occurrence was committed.", "Use the owner document set for the delivery contract.", nil)
	}
	// The shared schema must describe the wire the current codec emits:
	// occurrence.v2 and interpreted.v3 with the paired four/five refs. A
	// schema that predates either envelope cannot admit this write.
	specification, err := definitions.ParseSchemaSpecification(documents.Schema.Body())
	if err != nil {
		return definitions.ActiveDocuments{}, ir.RuntimeContractID{}, envelopeError(where, "The shared schema document is unreadable.", "The schema owner body failed its typed codec.", "No occurrence was committed.", "Regenerate the schema document and retry the delivery.", err)
	}
	if !slices.Contains(specification.OccurrenceKinds, string(occurrenceKindV2)) || !slices.Contains(specification.InterpretedKinds, string(interpretedKindV3)) {
		return definitions.ActiveDocuments{}, ir.RuntimeContractID{}, envelopeError(where, "The shared schema does not support the complete envelopes.", "The schema descriptor must name the occurrence.v2 and interpreted.v3 wire this codec emits; pairing against an older schema would admit refs the readers cannot validate.", "No occurrence was committed.", "Regenerate the dual-envelope schema document before admitting complete envelopes.", nil)
	}
	return documents, runtimeContract, nil
}

// isSuppliedRef reports whether an envelope slot carries caller-supplied
// content. The typed runtime-contract carrier alone does not count: legacy
// envelopes name their contract with zero definition refs.
func isSuppliedRef(ref model.DefinitionRef) bool {
	return ref.Definition > 0 || ref.Kind != 0 || ref.Content != (model.ContentIdentity{})
}

// resolveOccurrenceRefs proves every ref of a structurally complete envelope
// owner-resolvable at the watermark: kind, canonical bytes, content digest,
// producing operation and activation state. The envelope runtime contract must
// equal the delivery contract the occurrence was committed under. It returns
// no snapshot: the refs themselves are the durable facts, already ensured
// through the same owners.
func resolveOccurrenceRefs(reader DefinitionReader, contract ir.RuntimeContractID, envelope model.OccurrenceEnvelopeRef, watermark provenance.JournalID) error {
	const where = "Resolving the complete occurrence envelope (internal/lifecycle/receipt/occurrence_v2.go in receipt.resolveOccurrenceRefs)."
	if err := model.ValidateOccurrenceEnvelope(envelope); err != nil {
		return envelopeError(where, "The occurrence envelope is incomplete.", err.Error(), "No validated envelope was returned.", "Admit envelopes only through receipt.EnsureOccurrenceEnvelope.", nil)
	}
	if !envelope.Runtime.Contract.IsValid() || envelope.Runtime.Contract != contract {
		return envelopeError(where, "The occurrence envelope runtime contract does not match the delivery contract.", "The envelope runtime ref resolves to the runtime body whose contract must equal the delivery contract and the occurrence codec's contract.", "No validated envelope was returned.", "Admit envelopes only through receipt.EnsureOccurrenceEnvelope.", nil)
	}
	refs := []model.DefinitionRef{envelope.Runtime.Definition, envelope.Schema.Definition, envelope.Implementation.Definition, envelope.Retention.Definition}
	var runtimeSnapshot model.DefinitionSnapshot
	for index, ref := range refs {
		snapshot, _, err := ResolveDefinitionAt(reader, ref, watermark)
		if err != nil {
			return err
		}
		if index == 0 {
			runtimeSnapshot = snapshot
		}
	}
	// The resolved runtime body must pair with the delivery wire: its stamped
	// registration coordinate equals the occurrence contract. A runtime
	// document compiled for another wire cannot validate this occurrence,
	// even when every ref resolves separately.
	runtimeSpecification, err := definitions.ParseRuntimeSpecification(runtimeSnapshot.Body())
	if err != nil {
		return envelopeError(where, "The resolved runtime document is unreadable.", "The runtime owner body failed its typed codec.", "No validated envelope was returned.", "Restore the damaged definition from a verified backup.", err)
	}
	if runtimeSpecification.Registration.Contract != contract {
		return envelopeError(where, "The resolved runtime document is not compiled for the occurrence wire.", "The runtime body's registration coordinate must equal the occurrence contract.", "No validated envelope was returned.", "Restore the damaged envelope from a verified backup.", nil)
	}
	return nil
}

// ValidateStoredOccurrenceEnvelope enforces the mechanical admission boundary
// for journal truth: v2 requires all four nonzero, correctly typed,
// resolvable refs; a v1 record with any nonzero reference must validate every
// supplied ref, so the old zero encoding is never a loophole for dangling or
// wrong-kind refs. All-zero v1 remains faithful historical read data.
func ValidateStoredOccurrenceEnvelope(reader DefinitionReader, kind provenance.EvidenceKind, contract ir.RuntimeContractID, envelope model.OccurrenceEnvelopeRef, watermark provenance.JournalID) error {
	const where = "Validating stored occurrence evidence (internal/lifecycle/receipt/occurrence_v2.go in receipt.validateStoredOccurrenceEnvelope)."
	switch kind {
	case occurrenceKindV2:
		return resolveOccurrenceRefs(reader, contract, envelope, watermark)
	case receiptKind:
		refs := []model.DefinitionRef{envelope.Runtime.Definition, envelope.Schema.Definition, envelope.Implementation.Definition, envelope.Retention.Definition}
		supplied := false
		for _, ref := range refs {
			if isSuppliedRef(ref) {
				supplied = true
			}
		}
		if !supplied {
			return nil
		}
		for _, ref := range refs {
			if !isSuppliedRef(ref) {
				return envelopeError(where, "A legacy occurrence carries a partial envelope.", "A v1 record with any nonzero reference must validate all supplied refs; a partial envelope is neither legacy zero nor complete v2.", "No validated envelope was returned.", "Restore the damaged journal row from a verified backup.", nil)
			}
			if _, _, err := ResolveDefinitionAt(reader, ref, watermark); err != nil {
				return err
			}
		}
		return nil
	default:
		return envelopeError(where, "Stored occurrence evidence has an unsupported kind.", "Only the legacy and complete occurrence wire kinds are admitted.", "No validated envelope was returned.", "Repair the journal row or reader kind dispatch.", nil)
	}
}

func envelopeError(where, what, why, impact, fix string, cause error) error {
	return structured(pasterrors.CategoryValidation, what, why, where, impact, fix, cause)
}
