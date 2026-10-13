package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/gate"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

const (
	envelopeDefinitionCommand   = "pasture.lifecycle.definition.append/v2"
	envelopeDefinitionOperation = "pasture.lifecycle.definition.activation/v2."
	envelopeStateKind           = provenance.EvidenceKind("pasture.lifecycle.definition-state.v2")
)

func definitionOperationID(document definitions.Document) provenance.OperationID {
	sum := sha256.Sum256(append([]byte(envelopeDefinitionOperation+"\x00"), document.Coordinate()...))
	return provenance.OperationID(envelopeDefinitionOperation + hex.EncodeToString(sum[:]))
}

// DefinitionReader is the complete owner read capability. Evidence alone cannot
// authenticate an operation's actual committed result slots.
type DefinitionReader interface {
	QueryEvidence(provenance.EvidenceQuery) (provenance.EvidencePage, error)
	LookupCommitted(provenance.OperationID) (provenance.CommittedResult, error)
}

type journalDefinitionReader struct{ journal provenance.Journal }

func (r journalDefinitionReader) QueryEvidence(q provenance.EvidenceQuery) (provenance.EvidencePage, error) {
	return r.journal.Facts().QueryEvidence(q)
}
func (r journalDefinitionReader) LookupCommitted(op provenance.OperationID) (provenance.CommittedResult, error) {
	return r.journal.LookupCommitted(op)
}

func NewDefinitionReader(journal provenance.Journal) (DefinitionReader, error) {
	if journal == nil {
		return nil, definitionError("NewDefinitionReader", "No provenance journal was supplied.", nil)
	}
	return journalDefinitionReader{journal}, nil
}

// NewTransactionDefinitionReader uses the dependency's owner verifier on exactly
// the caller's transaction. It neither acquires another connection nor owns tx.
func NewTransactionDefinitionReader(ctx context.Context, tx *sql.Tx) (DefinitionReader, error) {
	r, err := provenance.NewTransactionEvidenceReader(ctx, tx)
	if err != nil {
		return nil, definitionError("NewTransactionDefinitionReader", "The transaction-bound owner reader could not be constructed.", err)
	}
	return r, nil
}

var _ DefinitionReader = (*provenance.TransactionEvidenceReader)(nil)

type envelopeStatePayload struct {
	Kind           model.DefinitionKind           `json:"kind"`
	ID             model.DefinitionID             `json:"id"`
	Version        uint32                         `json:"version"`
	Codec          model.DefinitionCodec          `json:"codec"`
	Content        model.ContentIdentity          `json:"content"`
	Event          model.DefinitionLifecycleEvent `json:"event"`
	DefinitionSlot provenance.ResultSlotID        `json:"definition_slot"`
}

func stateFor(document definitions.Document) envelopeStatePayload {
	return envelopeStatePayload{document.Kind(), document.ID(), document.Version(), document.Codec(), document.Content(), model.DefinitionActivated, definitionSlot}
}

func NewEnvelopeDefinitionActivation(document definitions.Document) (GatedWrite, error) {
	if !document.IsValid() {
		return GatedWrite{}, invalidCommit("The definition document is not constructed.", "Only a typed document owner can provide a complete normalized coordinate.", "Use the matching definitions constructor before activation.")
	}
	coordinate := document.Coordinate()
	commandDigest := sha256.Sum256(append([]byte(envelopeDefinitionCommand+"\x00"), coordinate...))
	state, err := json.Marshal(stateFor(document))
	if err != nil {
		return GatedWrite{}, err
	}
	state, err = definitions.NormalizePayload(envelopeStateKind, state)
	if err != nil {
		return GatedWrite{}, err
	}
	stateDigest := sha256.Sum256(state)
	content := document.Content()
	return GatedWrite{class: gate.WriteDefinitionActivation, command: envelopeDefinitionCommand,
		operationID: definitionOperationID(document), commandDigest: commandDigest[:],
		effects: []provenance.Effect{
			{Sort: provenance.EffectEvidence, ResultSlot: definitionSlot, EvidenceKind: definitions.EvidenceKind, ContentDigest: content[:], Payload: document.Body()},
			{Sort: provenance.EffectEvidence, ResultSlot: definitionStateSlot, EvidenceKind: envelopeStateKind, ContentDigest: stateDigest[:], Payload: state},
		}, constructed: true}, nil
}

func definitionError(step, why string, cause error) error {
	return structured(pasterrors.CategoryStorage, "The lifecycle definition could not be validated.", why,
		"internal/lifecycle/receipt/envelope_definition.go in receipt."+step+" during definition admission/read",
		"No validated definition was returned; existing committed rows were not repaired or replaced.",
		"Inspect the named operation, result slots and evidence; restore damaged journal rows from a verified backup before retrying.", cause)
}

func definitionSlots(result provenance.CommittedResult) (provenance.JournalID, provenance.JournalID, error) {
	var snapshot, state provenance.JournalID
	if result.Kind != provenance.CommittedExact {
		return 0, 0, definitionError("definitionSlots", "The producing operation is not committed exactly.", nil)
	}
	for _, slot := range result.ResultSlots {
		switch slot.Slot {
		case definitionSlot:
			if snapshot != 0 || slot.Kind != provenance.JournalKindEvidence {
				return 0, 0, definitionError("definitionSlots", "Duplicate or wrong-kind definition result slot.", nil)
			}
			snapshot = slot.ProducedJournalID
		case definitionStateSlot:
			if state != 0 || slot.Kind != provenance.JournalKindEvidence {
				return 0, 0, definitionError("definitionSlots", "Duplicate or wrong-kind definition-state result slot.", nil)
			}
			state = slot.ProducedJournalID
		default:
			return 0, 0, definitionError("definitionSlots", "Unexpected result slot in a definition activation.", nil)
		}
	}
	if snapshot <= 0 || state <= snapshot {
		return 0, 0, definitionError("definitionSlots", fmt.Sprintf("Missing or misordered definition/state slots (%d, %d).", snapshot, state), nil)
	}
	return snapshot, state, nil
}

func EnsureActiveDefinition(ctx context.Context, s Service, document definitions.Document) (model.DefinitionRef, error) {
	write, err := NewEnvelopeDefinitionActivation(document)
	if err != nil {
		return model.DefinitionRef{}, err
	}
	reader, err := NewDefinitionReader(s.Appender.Journal)
	if err != nil {
		return model.DefinitionRef{}, err
	}
	result, err := reader.LookupCommitted(write.OperationID())
	if err != nil {
		return model.DefinitionRef{}, definitionError("EnsureActiveDefinition", "The indexed activation lookup failed.", err)
	}
	switch result.Kind {
	case provenance.CommittedAbsent:
		if err := rejectDisplacedDefinition(ctx, reader, document); err != nil {
			return model.DefinitionRef{}, err
		}
		intent, refusal := gate.NewEnvelopeDefinitionActivationIntent(document.Kind(), document.Content())
		if refusal != nil {
			return model.DefinitionRef{}, refusal
		}
		warrant, refusal := gate.Legalize(intent)
		if refusal != nil {
			return model.DefinitionRef{}, refusal
		}
		if _, err := s.Commit(ctx, warrant, write); err != nil {
			return model.DefinitionRef{}, err
		}
		result, err = reader.LookupCommitted(write.OperationID())
		if err != nil {
			return model.DefinitionRef{}, definitionError("EnsureActiveDefinition", "Post-commit slot lookup failed; activation may be committed.", err)
		}
	case provenance.CommittedExact:
	default:
		return model.DefinitionRef{}, definitionError("EnsureActiveDefinition", "The activation identity conflicts with committed journal truth.", nil)
	}
	id, _, err := definitionSlots(result)
	if err != nil {
		return model.DefinitionRef{}, err
	}
	ref := model.DefinitionRef{Definition: model.DefinitionJournalID(id), Kind: document.Kind(), Content: document.Content()}
	snapshot, _, err := ResolveDefinition(reader, ref)
	if err != nil {
		return model.DefinitionRef{}, err
	}
	if !bytes.Equal(snapshot.Body(), document.Body()) {
		return model.DefinitionRef{}, definitionError("EnsureActiveDefinition", "The committed body does not match the requested owner document.", nil)
	}
	return ref, nil
}

// The indexed lookup cannot distinguish first activation from a renamed
// operation index. Before admitting an absent coordinate, inspect a bounded,
// snapshot-pinned evidence inventory. Exhaustion is a refusal, not absence.
// Identical concurrent writers may already have published the correct operation;
// their subsequent Apply remains the journal's atomic conflict/replay boundary.
const definitionAbsenceProbePages = 8

func rejectDisplacedDefinition(ctx context.Context, reader DefinitionReader, document definitions.Document) error {
	query := provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{definitions.EvidenceKind, envelopeStateKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
	for range definitionAbsenceProbePages {
		if err := ctx.Err(); err != nil {
			return definitionError("rejectDisplacedDefinition", "Activation was canceled before absence could be verified.", err)
		}
		page, err := reader.QueryEvidence(query)
		if err != nil {
			return definitionError("rejectDisplacedDefinition", "The bounded definition absence inventory could not be read.", err)
		}
		for _, row := range page.Rows {
			var coordinate struct {
				Kind    model.DefinitionKind  `json:"kind"`
				ID      model.DefinitionID    `json:"id"`
				Version uint32                `json:"version"`
				Codec   model.DefinitionCodec `json:"codec"`
				Content model.ContentIdentity `json:"content"`
			}
			if err := json.Unmarshal(row.Payload, &coordinate); err != nil {
				return definitionError("rejectDisplacedDefinition", fmt.Sprintf("Definition evidence row %d is unreadable; absence is not established.", row.JournalID), err)
			}
			sum := sha256.Sum256(row.Payload)
			content := document.Content()
			matches := bytes.Equal(row.ContentDigest, content[:]) || sum == [32]byte(content) || coordinate.Content == content || (coordinate.Kind == document.Kind() && coordinate.ID == document.ID() && coordinate.Version == document.Version() && coordinate.Codec == document.Codec())
			if matches && row.ProducingOperationID != definitionOperationID(document) {
				return definitionError("rejectDisplacedDefinition", fmt.Sprintf("Definition coordinate already has evidence at row %d under unexpected operation %q; the indexed absence is damaged journal truth, not first activation.", row.JournalID, row.ProducingOperationID), nil)
			}
		}
		if page.Next == nil {
			return nil
		}
		query.Page.SnapshotMaxJournalID = page.SnapshotMaxJournalID
		query.Page.AfterJournalID = page.Next.AfterJournalID
	}
	return definitionError("rejectDisplacedDefinition", "The bounded evidence inventory was exhausted before absence could be established; no activation was appended.", nil)
}

// ResolveDefinition first captures an enclosing evidence watermark. Exact-ID
// bounds locate the snapshot, but the later activation row is read under that
// enclosing watermark, never under the snapshot row's ID alone.
func ResolveDefinition(reader DefinitionReader, ref model.DefinitionRef) (model.DefinitionSnapshot, model.DefinitionStateRecord, error) {
	return ResolveDefinitionAt(reader, ref, 0)
}

// ResolveDefinitionAt lets replay share its already selected enclosing snapshot.
// Zero selects a fresh snapshot; a positive watermark is never widened.
func ResolveDefinitionAt(reader DefinitionReader, ref model.DefinitionRef, watermark provenance.JournalID) (model.DefinitionSnapshot, model.DefinitionStateRecord, error) {
	var empty model.DefinitionSnapshot
	var noState model.DefinitionStateRecord
	if reader == nil || ref.Definition <= 0 || !model.IsEnvelopeDefinitionKind(ref.Kind) || ref.Content == (model.ContentIdentity{}) || watermark < 0 {
		return empty, noState, definitionError("ResolveDefinitionAt", "Reader, positive journal coordinate, admitted kind or content identity is missing.", nil)
	}
	if watermark == 0 {
		page, err := reader.QueryEvidence(provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{definitions.EvidenceKind, envelopeStateKind}, Page: provenance.FactPageRequest{Limit: 1}})
		if err != nil {
			return empty, noState, definitionError("ResolveDefinitionAt", "Enclosing evidence snapshot could not be captured.", err)
		}
		watermark = page.SnapshotMaxJournalID
	}
	id := provenance.JournalID(ref.Definition)
	if watermark < id {
		return empty, noState, definitionError("ResolveDefinitionAt", "Definition lies beyond the enclosing snapshot watermark.", nil)
	}
	page, err := reader.QueryEvidence(provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{definitions.EvidenceKind}, Page: provenance.FactPageRequest{Limit: 1, SnapshotMaxJournalID: id, AfterJournalID: id - 1}})
	if err != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", fmt.Sprintf("Exact definition row %d could not be read.", id), err)
	}
	if len(page.Rows) != 1 || page.Rows[0].JournalID != id {
		return empty, noState, definitionError("ResolveDefinitionAt", fmt.Sprintf("Definition row %d is missing or has the wrong evidence kind.", id), nil)
	}
	row := page.Rows[0]
	document, err := definitions.Decode(row.Payload)
	if err != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", fmt.Sprintf("Definition row %d failed its typed owner codec.", id), err)
	}
	if document.Kind() != ref.Kind || document.Content() != ref.Content || !bytes.Equal(document.Body(), row.Payload) || !bytes.Equal(row.ContentDigest, ref.Content[:]) {
		return empty, noState, definitionError("ResolveDefinitionAt", fmt.Sprintf("Definition row %d kind, canonical bytes or content digest does not match the reference.", id), nil)
	}
	if row.ProducingOperationID != definitionOperationID(document) {
		return empty, noState, definitionError("ResolveDefinitionAt", "Definition producing operation does not match its owner coordinate recipe.", nil)
	}
	result, err := reader.LookupCommitted(row.ProducingOperationID)
	if err != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", "Producing operation result-slot reconstruction failed.", err)
	}
	definitionID, stateID, err := definitionSlots(result)
	if err != nil {
		return empty, noState, err
	}
	if definitionID != id || stateID > watermark || result.AnchorJournalID != row.ProducingOperationJournalID || result.AnchorJournalID <= 0 || result.AnchorJournalID >= id {
		return empty, noState, definitionError("ResolveDefinitionAt", "Producing operation anchor or ordered result slots disagree with the snapshot/reference.", nil)
	}
	states, err := reader.QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{OperationIDs: []provenance.OperationID{row.ProducingOperationID}}, Kinds: []provenance.EvidenceKind{envelopeStateKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize, SnapshotMaxJournalID: watermark}})
	if err != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", "Activation state evidence could not be read under the enclosing watermark.", err)
	}
	if len(states.Rows) != 1 || states.Next != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", "Activation operation must produce exactly one state evidence row.", nil)
	}
	stateRow := states.Rows[0]
	normalized, err := definitions.NormalizePayload(envelopeStateKind, stateRow.Payload)
	if err != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", "Activation state payload is malformed.", err)
	}
	var state envelopeStatePayload
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", "Activation state typed fields are invalid.", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return empty, noState, definitionError("ResolveDefinitionAt", "Activation state contains trailing JSON.", err)
	}
	stateDigest := sha256.Sum256(normalized)
	if state != stateFor(document) || stateRow.JournalID != stateID || stateRow.ProducingOperationID != row.ProducingOperationID || stateRow.ProducingOperationJournalID != row.ProducingOperationJournalID || !bytes.Equal(normalized, stateRow.Payload) || !bytes.Equal(stateDigest[:], stateRow.ContentDigest) {
		return empty, noState, definitionError("ResolveDefinitionAt", fmt.Sprintf("Activation row %d coordinate, content, canonical bytes or operation linkage is damaged.", stateID), nil)
	}
	snapshot, err := model.NewDefinitionSnapshot(ref, document.ID(), document.Version(), document.Codec(), row.Payload)
	if err != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", "Model owner refused resolved snapshot construction.", err)
	}
	stateRecord, err := model.NewDefinitionStateRecord(snapshot, model.DefinitionStateFact{StateID: model.DefinitionStateID(stateID), Definition: ref.Definition, Event: state.Event, RecordedAt: stateRow.RecordedAt}, watermark)
	if err != nil {
		return empty, noState, definitionError("ResolveDefinitionAt", "Model owner refused resolved activation state construction.", err)
	}
	return snapshot, stateRecord, nil
}
