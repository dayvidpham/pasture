package receipt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/pasture/internal/writelane"
	"github.com/dayvidpham/provenance"
)

// prepareEnvelopes is the batch-path envelope preparation behind
// Service.PrepareReceipt: one memoized document build; one indexed lookup
// and commit-if-absent per owner document with full slot/body/digest/state
// resolution for fresh activations (shared across the lane turn, standalone
// on the direct path). Its behavior matches EnsureOccurrenceEnvelope plus
// the prepared finalizer — same absence scan, same fixed-class gate, same
// deterministic commits, same supplied-ref and pairing refusals — with no
// duplicate lookups and no admission-time re-resolution of evidence the
// ensures just resolved.
//
// A zero extra set is the record-only path: the complete occurrence alone,
// never a fabricated semantic record. Prepared effects supplied for a
// non-valid capture are refused outright. Every failure is raised before the
// payload blob write and the journal append.
func (s Service) prepareEnvelopes(ctx context.Context, delivery Delivery, observed model.OccurrenceEnvelopeRef, extra []provenance.Effect) (model.OccurrenceEnvelopeRef, []provenance.Effect, error) {
	var empty model.OccurrenceEnvelopeRef
	const where = "Preparing complete lifecycle envelopes (internal/lifecycle/receipt/envelope_batch.go in receipt.Service.prepareEnvelopes)."
	documents, runtimeContract, err := memoizedActiveDocuments(delivery.Contract)
	if err != nil {
		return empty, nil, err
	}
	if s.Appender.Journal == nil {
		return empty, nil, envelopeError(where, "The lifecycle receipt service cannot ensure definition references.", "Envelope refs are journaled owner facts, so the provenance journal is required to ensure them.", "No occurrence was committed.", "Construct the service through tasks.NewLifecycleReceiptService.", nil)
	}
	reader, err := NewDefinitionReader(s.Appender.Journal)
	if err != nil {
		return empty, nil, err
	}
	// Phase 1 (commit): ensure every needed document is durably
	// activated. The interpreter activates only when a prepared
	// interpretation is present; record-only captures never activate
	// semantic documents.
	//
	// Within one lane turn the first record to need a document performs the
	// full lookup, commit-if-absent and resolution and shares its reference;
	// later same-turn records reuse it instead of repeating the identical
	// reads against the same rows. Journal truth is monotonic and definition
	// rows are immutable, so the reuse sees the same rows the first
	// resolution proved. Errors are never shared: every record retries a
	// failed ensure independently. Outside a turn (direct path, nil scope)
	// every call ensures standalone.
	//
	// A hit resolves nothing further: the reference is built from the
	// journal's committed slots for the content-derived operation identity
	// (conflict enforcement binds coordinate to result), and kind and content
	// come from the in-hand owner document. Body, digest and state integrity
	// are proved for fresh activations here and for every ref on the
	// read/replay boundaries, which re-resolve before exposing anything.
	// Supplied-ref forgery and pairing are still refused here, purely, before
	// any write.
	shared := writelane.TurnScopeFrom(ctx)
	ensure := func(document definitions.Document) (model.DefinitionRef, error) {
		key := turnDocumentKey(document)
		if shared != nil {
			if cached, ok := shared.Get(key); ok {
				if ref, ok := cached.(model.DefinitionRef); ok {
					return ref, nil
				}
			}
		}
		ref, result, fresh, err := ensureCommittedDefinition(ctx, s, document, reader)
		if err != nil {
			return model.DefinitionRef{}, err
		}
		if fresh {
			if _, _, _, err := resolveCommittedDefinition(reader, ref, document, result, 0); err != nil {
				return model.DefinitionRef{}, err
			}
		}
		if shared != nil {
			shared.Set(key, ref)
		}
		return ref, nil
	}
	runtimeRef, err := ensure(documents.Runtime)
	if err != nil {
		return empty, nil, err
	}
	schemaRef, err := ensure(documents.Schema)
	if err != nil {
		return empty, nil, err
	}
	implementationRef, err := ensure(documents.EpochImplementation)
	if err != nil {
		return empty, nil, err
	}
	retentionRef, err := ensure(documents.Retention)
	if err != nil {
		return empty, nil, err
	}
	var interpreterRef model.DefinitionRef
	if len(extra) > 0 {
		interpreterRef, err = ensure(documents.Interpreter)
		if err != nil {
			return empty, nil, err
		}
	}
	complete := model.OccurrenceEnvelopeRef{
		Runtime:        model.RuntimeContractDefinitionRef{Definition: runtimeRef, Contract: delivery.Contract},
		Schema:         model.LifecycleSchemaDefinitionRef{Definition: schemaRef},
		Implementation: model.EpochImplementationRef{Definition: implementationRef},
		Retention:      model.RetentionPolicyDefinitionRef{Definition: retentionRef},
	}
	complete.HostVersion = observed.HostVersion
	complete.HostVersionSource = observed.HostVersionSource
	complete.Origin = observed.Origin
	if observed.Runtime.Contract.IsValid() && observed.Runtime.Contract != delivery.Contract {
		return empty, nil, envelopeError(where, "The supplied runtime contract does not match the delivery contract.", "The envelope runtime ref resolves to the runtime body whose contract must equal the delivery contract.", "No occurrence was committed; nothing was replaced.", "Supply the delivery contract unchanged as the envelope runtime contract.", nil)
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
			return empty, nil, envelopeError(where, "The supplied occurrence envelope "+slot.name+" reference does not match the owner document for this write.", "A wrong, dangling or nonmatching supplied value is refused rather than silently replaced; unset slots are populated by the owner.", "No occurrence was committed; nothing was replaced.", "Omit the envelope refs and let the owner populate them, or supply the exact owner refs for this contract.", nil)
		}
	}
	if len(extra) == 0 {
		return complete, nil, nil
	}
	// Only a valid capture carries a verified prepared interpretation.
	// Malformed and unsupported captures commit record-only complete
	// occurrences; prepared effects supplied for one are refused outright
	// rather than committed or silently dropped, so no semantic record can
	// ever attach to a capture that has no interpretation.
	if delivery.Capture != model.CaptureValid {
		return empty, nil, preparedRefusal(where, "A non-valid capture supplied prepared interpretation.", "Only a valid capture carries a verified prepared interpretation; malformed and unsupported captures commit record-only occurrences without fabricated semantic records.", "Omit prepared effects for non-valid captures.", nil)
	}
	final, err := finalizePreparedBatch(ctx, reader, runtimeContract, interpreterRef, delivery, complete, extra, shared)
	if err != nil {
		return empty, nil, err
	}
	return complete, final, nil
}

// finalizePreparedBatch binds verified prepared interpretation material to
// its owner-resolved complete envelopes. The bridge and interpreter
// resolutions share the turn scope like every other owner document; admission
// is proved by pure checks over resolved evidence — shared-ref equality,
// producer contract and manifest agreement, and structural completeness.
func finalizePreparedBatch(ctx context.Context, reader DefinitionReader, runtimeContract ir.RuntimeContractID, interpreterRef model.DefinitionRef, delivery Delivery, complete model.OccurrenceEnvelopeRef, extra []provenance.Effect, shared *writelane.TurnScope) ([]provenance.Effect, error) {
	const where = "Finalizing prepared lifecycle effects (internal/lifecycle/receipt/envelope_batch.go in receipt.Service.finalizePreparedBatch)."
	preparedEffect := extra[0]
	prepared, err := DecodeInterpretedV2(model.InterpretationID(1), model.OccurrenceID(1), preparedEffect.Payload)
	if err != nil {
		return nil, preparedRefusal(where, "The prepared interpretation is malformed.", err.Error(), "Use receipt.Record.Effect without modifying or reconstructing its payload.", err)
	}
	if prepared.Contract() != runtimeContract {
		return nil, preparedRefusal(where, "The prepared interpretation contract does not match the delivery contract.", "Durable interpretation must identify the exact runtime profile compiled for the delivery wire.", "Pass the delivery contract unchanged through L2 derivation.", nil)
	}
	manifest, ok := prepared.Metamodel()
	if !ok || manifest != metamodel.Active() {
		return nil, preparedRefusal(where, "The prepared interpretation cites an unexpected metamodel producer.", "The semantic envelope bridges the exact active producer coordinate, never a caller-supplied label or an unrelated manifest.", "Derive the prepared value against the active metamodel without replacing its coordinate.", nil)
	}
	// The legacy metamodel activation is resolved, never re-issued, through
	// the owner-validated normalized bridge, shared across the turn like
	// every other owner document. DeliveryCommit ensures the activation
	// before Receive on the valid-capture path; a store without it fails
	// closed here rather than finalizing against an unjournaled vocabulary.
	metamodelRef, err := turnSharedBridge(ctx, reader, shared)
	if err != nil {
		return nil, err
	}
	envelope := model.SemanticEnvelopeRef{
		Runtime:        complete.Runtime,
		Schema:         complete.Schema,
		Metamodel:      metamodelRef,
		Interpreter:    model.InterpreterDefinitionRef{Definition: interpreterRef},
		Implementation: complete.Implementation,
	}
	// Admission from in-hand evidence: the ensures above resolved every ref
	// through the owners at the shared watermark, so equality, agreement and
	// completeness are proved purely here. The read boundaries re-resolve
	// independently before exposing anything.
	if err := model.ValidateSemanticEnvelope(envelope); err != nil {
		return nil, preparedRefusal(where, "The semantic envelope is incomplete.", err.Error(), "Resolve all five owner refs before finalizing.", err)
	}
	if !envelope.SharedRefsEqual(complete) {
		return nil, preparedRefusal(where, "The paired occurrence and semantic envelopes disagree on their shared references.", "The runtime, schema and implementation refs must be exactly equal across the pair.", "Finalize the pair from one ensured envelope.", nil)
	}
	final, err := finalizeInterpretedV3(recordFromModel(prepared), envelope)
	if err != nil {
		return nil, err
	}
	if len(extra) == 1 {
		return []provenance.Effect{final}, nil
	}
	// The incoming consultation is validated against the PREPARED bytes first,
	// so a tampered pre-finalization input fails rather than being concealed
	// by the re-emission. Only its evaluated verdict and legalized value
	// survive: the reference is finalized against the v3 bytes, never patched.
	incoming, err := DecodeConsultation(extra[1], preparedEffect)
	if err != nil {
		return nil, preparedRefusal(where, "The prepared consultation does not reference its prepared interpretation.", err.Error(), "Construct both prepared records together and preserve interpreted-then-consultation order.", err)
	}
	decision, ok := incoming.Decision()
	if !ok {
		return nil, preparedRefusal(where, "The prepared consultation carries no evaluated decision.", "Only an evaluated gate consultation finalizes against v3; observations finalize without one.", "Derive the consultation from an evaluated gate before finalizing.", nil)
	}
	var wire consultationWireV2
	if err := json.Unmarshal(extra[1].Payload, &wire); err != nil {
		return nil, preparedRefusal(where, "The prepared consultation payload is unreadable.", "The validated consultation must expose its legalized value for reference finalization.", "Construct the consultation through its owner without modifying its payload.", err)
	}
	payload, err := marshalConsultationV2(wire.Legalized, decision, final.Payload)
	if err != nil {
		return nil, preparedRefusal(where, "The finalized consultation reference could not be encoded.", "The evaluated verdict, legalized value and final interpreted bytes failed canonical encoding.", "Report the incompatible prepared shape.", err)
	}
	sum := sha256.Sum256(payload)
	consultation := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: consultationSlot, EvidenceKind: consultationKind, ContentDigest: append([]byte(nil), sum[:]...), Payload: append(json.RawMessage(nil), payload...)}
	if _, err := DecodeConsultation(consultation, final); err != nil {
		return nil, preparedRefusal(where, "The finalized consultation does not reference its finalized interpretation.", err.Error(), "Report the finalization defect without committing the pair.", err)
	}
	return []provenance.Effect{final, consultation}, nil
}

// recordFromModel projects a validated model interpretation back to the
// constructor-owned prepared record the v3 finalizer consumes. Both carry the
// same verified semantic arms, producer contract and manifest; neither carries
// journal refs.
func recordFromModel(record model.InterpretedRecord) Record {
	manifest, _ := record.Metamodel()
	return Record{semantic: record.Semantic(), identities: record.Identities(), unresolved: record.UnresolvedFacts(), contract: record.Contract(), metamodel: manifest, constructed: true}
}

func preparedRefusal(where, what, why, fix string, cause error) error {
	return structured(pasterrors.CategoryValidation, what, why, where, "No occurrence was committed; already activated reusable definitions may exist, but never a partial occurrence/interpretation pair.", fix, cause)
}

// turnDocumentKey identifies one owner document within a turn by its
// deterministic activation operation: the content-derived identity the
// journal folds, so equal documents share one entry whatever contract
// selected them.
func turnDocumentKey(document definitions.Document) string {
	return "lifecycle-definition:" + string(definitionOperationID(document))
}

// turnSharedBridge resolves the active metamodel producer through the
// normalized bridge, sharing the turn scope like every other owner document.
// A failure is never shared: every record retries the bridge independently.
func turnSharedBridge(ctx context.Context, reader DefinitionReader, shared *writelane.TurnScope) (model.LifecycleMetamodelRef, error) {
	active := metamodel.Active()
	key := "lifecycle-metamodel-bridge:" + hex.EncodeToString(active.Content[:])
	if shared != nil {
		if cached, ok := shared.Get(key); ok {
			if ref, ok := cached.(model.LifecycleMetamodelRef); ok {
				return ref, nil
			}
		}
	}
	ref, _, _, err := BridgeActiveMetamodel(ctx, reader)
	if err != nil {
		return model.LifecycleMetamodelRef{}, err
	}
	if shared != nil {
		shared.Set(key, ref)
	}
	return ref, nil
}
