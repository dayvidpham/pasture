package receipt

import (
	"slices"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// ResolveSemanticEnvelope proves one finalized interpreted.v3 record
// owner-valid at the watermark: all five refs resolve to their actual
// body/state rows, the three shared refs are exactly equal to the associated
// occurrence envelope, the interpretation contract equals the occurrence
// contract, and the resolved schema supports the v3 wire. A paired mismatch,
// a dangling or wrong-kind ref, a content mismatch, or a schema that
// predates v3 fails closed with no partial validated result.
//
// Admission (Receive) and validated reads (projection Records, transaction
// replay) share this resolver, so tampering fails identically on every path.
func ResolveSemanticEnvelope(reader DefinitionReader, occurrence model.OccurrenceRecord, record model.InterpretedRecord, watermark provenance.JournalID) error {
	const where = "Resolving the complete semantic envelope (internal/lifecycle/receipt/semantic_envelope.go in receipt.ResolveSemanticEnvelope)."
	envelope, ok := record.SemanticEnvelope()
	if !ok {
		return envelopeError(where, "The interpretation carries no semantic envelope.", "Only a finalized interpreted.v3 record carries the complete five-reference envelope; legacy records expose absence, never an inferred envelope.", "No validated interpretation was returned.", "Finalize interpretations only through the common receipt boundary.", nil)
	}
	if err := model.ValidateSemanticEnvelope(envelope); err != nil {
		return envelopeError(where, "The semantic envelope is incomplete.", err.Error(), "No validated interpretation was returned.", "Finalize interpretations only through the common receipt boundary.", nil)
	}
	if !envelope.SharedRefsEqual(occurrence.Envelope) {
		return envelopeError(where, "The paired occurrence and semantic envelopes disagree on their shared references.", "The runtime, schema and implementation refs must be exactly equal across the pair; individually valid but different refs fail the pair.", "No validated interpretation was returned.", "Restore the damaged envelope from a verified backup; refs are never repaired from current artifacts.", nil)
	}
	// The interpretation identifies the versioned execution profile; the
	// occurrence names the delivery wire. The owner proves they are the same
	// profile rather than comparing spellings.
	runtimeContract, err := definitions.RuntimeContractForDelivery(occurrence.RuntimeContract)
	if err != nil {
		return envelopeError(where, "The occurrence contract names no compiled runtime profile.", "The wire contract must resolve to exactly one versioned execution profile compiled for it.", "No validated interpretation was returned.", "Restore the damaged record from a verified backup.", err)
	}
	if !record.Contract().IsValid() || record.Contract() != runtimeContract {
		return envelopeError(where, "The interpretation contract does not match the occurrence contract.", "Durable interpretation must identify the exact runtime profile compiled for the occurrence wire.", "No validated interpretation was returned.", "Restore the damaged record from a verified backup.", nil)
	}
	if !occurrence.Envelope.Runtime.Contract.IsValid() || occurrence.Envelope.Runtime.Contract != occurrence.RuntimeContract {
		return envelopeError(where, "The occurrence envelope runtime contract does not match the occurrence contract.", "The envelope runtime ref resolves to the runtime body whose contract must equal the delivery contract.", "No validated interpretation was returned.", "Restore the damaged envelope from a verified backup.", nil)
	}
	refs := []model.DefinitionRef{envelope.Runtime.Definition, envelope.Schema.Definition, envelope.Interpreter.Definition, envelope.Implementation.Definition}
	for _, ref := range refs {
		if _, _, err := ResolveDefinitionAt(reader, ref, watermark); err != nil {
			return err
		}
	}
	if _, _, err := ResolveMetamodelRef(reader, envelope.Metamodel.Definition, watermark); err != nil {
		return err
	}
	schemaSnapshot, _, err := ResolveDefinitionAt(reader, envelope.Schema.Definition, watermark)
	if err != nil {
		return err
	}
	specification, err := definitions.ParseSchemaSpecification(schemaSnapshot.Body())
	if err != nil {
		return envelopeError(where, "The resolved schema document is unreadable.", "The schema owner body failed its typed codec.", "No validated interpretation was returned.", "Restore the damaged definition from a verified backup.", err)
	}
	if !slices.Contains(specification.InterpretedKinds, string(interpretedKindV3)) {
		return envelopeError(where, "The resolved schema does not support interpreted.v3.", "A semantic envelope validated against a schema that predates its wire is a pairing violation.", "No validated interpretation was returned.", "Resolve against the dual-envelope schema document.", nil)
	}
	return nil
}
