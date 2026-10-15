package receipt

import (
	"bytes"
	"crypto/sha256"
	"sort"
	"time"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// StoredOccurrence is the journal-truth identity one transaction replay
// verifies semantic evidence against: the occurrence row, its contract, and
// its envelope for the shared-ref pairing rule.
type StoredOccurrence struct {
	JournalID provenance.JournalID
	Contract  ir.RuntimeContractID
	Envelope  model.OccurrenceEnvelopeRef
}

// semanticInterpretedKinds is the closed set of interpreted evidence kinds
// transaction verification admits: both read-only legacy kinds and the
// finalized v3. The at-most-one-interpretation invariant holds ACROSS all
// three: an occurrence may carry at most one interpretation whether it is
// v1, v2 or v3.
var semanticInterpretedKinds = []provenance.EvidenceKind{interpretedKind, interpretedKindV2, interpretedKindV3}

// semanticConsultationKinds is the closed set of consultation evidence kinds
// transaction verification admits.
var semanticConsultationKinds = []provenance.EvidenceKind{consultationKindV1, consultationKind}

// VerifyTransactionSemanticEvidence verifies interpreted and consultation
// integrity on the caller's transaction view before a migration marker is
// published. Every v3 record resolves its complete five-reference envelope
// through the same owner resolver admission uses, proves the three shared
// refs exactly equal to its associated occurrence, and every consultation
// proves its exact-payload link to its immediately preceding interpreted
// record. Legacy v1/v2 records decode strictly with no inferred envelope.
// A duplicate interpretation across kinds, a cross-operation association, a
// digest mismatch, or any resolution failure refuses with no partial
// validated result, and the caller rolls the whole transaction back.
func VerifyTransactionSemanticEvidence(reader DefinitionReader, occurrences map[provenance.OperationID]StoredOccurrence, watermark provenance.JournalID) error {
	const where = "Verifying transaction semantic evidence (internal/lifecycle/receipt/semantic_verify.go in receipt.VerifyTransactionSemanticEvidence)."
	if reader == nil || watermark < 0 {
		return envelopeError(where, "The transaction semantic verification is incompletely wired.", "The transaction-bound evidence reader and a non-negative watermark are both required.", "No semantic evidence was verified.", "Pass the active migration transaction reader and its snapshot watermark.", nil)
	}
	operations := make([]provenance.OperationID, 0, len(occurrences))
	for operation := range occurrences {
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i] < operations[j] })
	interpretedByOperation := make(map[provenance.OperationID][]provenance.EvidenceRow, len(occurrences))
	consultationByOperation := make(map[provenance.OperationID][]provenance.EvidenceRow, len(occurrences))
	for start := 0; start < len(operations); start += provenance.MaxFactFilterValues {
		end := start + provenance.MaxFactFilterValues
		if end > len(operations) {
			end = len(operations)
		}
		if err := collectSemanticRows(reader, operations[start:end], watermark, interpretedByOperation, consultationByOperation); err != nil {
			return err
		}
	}
	for _, operation := range operations {
		stored := occurrences[operation]
		interpreted := interpretedByOperation[operation]
		if len(interpreted) > 1 {
			return envelopeError(where, "A lifecycle occurrence has duplicate interpretations.", "Its operation produced more than one interpreted fact across the admitted kinds.", "No semantic evidence was verified.", "Repair the corrupt operation.", nil)
		}
		var interpretedEffect provenance.Effect
		if len(interpreted) == 1 {
			row := interpreted[0]
			sum := sha256.Sum256(row.Payload)
			if !bytes.Equal(sum[:], row.ContentDigest) {
				return envelopeError(where, "Interpreted lifecycle evidence failed digest validation.", "Stored digest differs from the exact payload.", "No semantic evidence was verified.", "Restore canonical evidence.", nil)
			}
			interpretedEffect = provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: interpretedSlot, EvidenceKind: row.EvidenceKind, ContentDigest: append([]byte(nil), row.ContentDigest...), Payload: append([]byte(nil), row.Payload...)}
			record, err := decodeSemanticInterpreted(row.EvidenceKind, model.InterpretationID(row.JournalID), model.OccurrenceID(stored.JournalID), row.Payload)
			if err != nil {
				return envelopeError(where, "Interpreted lifecycle evidence is malformed or noncanonical.", err.Error(), "No semantic evidence was verified.", "Repair the evidence producer and row.", err)
			}
			if row.EvidenceKind == interpretedKindV3 {
				occurrence := model.NewOccurrenceRecord(model.OccurrenceID(stored.JournalID), 0, stored.Contract, stored.Envelope, time.Time{}, provenance.ActorID{}, nil, 0, model.EvidencePayloadRef{})
				if err := ResolveSemanticEnvelope(reader, occurrence, record, watermark); err != nil {
					return err
				}
			}
		}
		for _, row := range consultationByOperation[operation] {
			if len(interpreted) != 1 {
				return envelopeError(where, "Consultation evidence has no interpreted record in its operation.", "A consultation must immediately follow exactly one interpreted record in the same operation.", "No semantic evidence was verified.", "Repair the corrupt operation.", nil)
			}
			sum := sha256.Sum256(row.Payload)
			if !bytes.Equal(sum[:], row.ContentDigest) {
				return envelopeError(where, "Consultation lifecycle evidence failed digest validation.", "Stored digest differs from the exact payload.", "No semantic evidence was verified.", "Restore canonical evidence.", nil)
			}
			effect := provenance.Effect{Sort: provenance.EffectEvidence, ResultSlot: consultationSlot, EvidenceKind: row.EvidenceKind, ContentDigest: append([]byte(nil), row.ContentDigest...), Payload: append([]byte(nil), row.Payload...)}
			if _, err := DecodeConsultation(effect, interpretedEffect); err != nil {
				return envelopeError(where, "Consultation lifecycle evidence does not reference its interpreted record.", err.Error(), "No semantic evidence was verified.", "Repair the corrupt operation.", err)
			}
		}
	}
	return nil
}

func collectSemanticRows(reader DefinitionReader, operations []provenance.OperationID, watermark provenance.JournalID, interpreted, consultation map[provenance.OperationID][]provenance.EvidenceRow) error {
	const where = "Verifying transaction semantic evidence (internal/lifecycle/receipt/semantic_verify.go in receipt.collectSemanticRows)."
	query := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: operations}, Kinds: append(append([]provenance.EvidenceKind(nil), semanticInterpretedKinds...), semanticConsultationKinds...), Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize, SnapshotMaxJournalID: watermark}}
	for {
		page, err := reader.QueryEvidence(query)
		if err != nil {
			return envelopeError(where, "Semantic lifecycle evidence could not be read.", "The operation-filtered evidence query failed.", "No semantic evidence was verified.", "Repair journal evidence and retry.", err)
		}
		for _, row := range page.Rows {
			switch row.EvidenceKind {
			case interpretedKind, interpretedKindV2, interpretedKindV3:
				interpreted[row.ProducingOperationID] = append(interpreted[row.ProducingOperationID], row)
			case consultationKind, consultationKindV1:
				consultation[row.ProducingOperationID] = append(consultation[row.ProducingOperationID], row)
			default:
				return envelopeError(where, "Semantic evidence has an unsupported kind.", "Only the admitted interpreted and consultation kinds verify.", "No semantic evidence was verified.", "Repair the corrupt journal association.", nil)
			}
		}
		if page.Next == nil {
			break
		}
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	return nil
}

func decodeSemanticInterpreted(kind provenance.EvidenceKind, id model.InterpretationID, occurrence model.OccurrenceID, payload []byte) (model.InterpretedRecord, error) {
	switch kind {
	case interpretedKind:
		return DecodeInterpreted(id, occurrence, payload)
	case interpretedKindV2:
		return DecodeInterpretedV2(id, occurrence, payload)
	case interpretedKindV3:
		return DecodeInterpretedV3(id, occurrence, payload)
	default:
		return model.InterpretedRecord{}, envelopeError("Decoding transaction semantic evidence (internal/lifecycle/receipt/semantic_verify.go in receipt.decodeSemanticInterpreted).", "Interpreted evidence kind is not admitted.", "Only the three interpreted wire kinds decode.", "No record was decoded.", "Repair the corrupt query result.", nil)
	}
}
