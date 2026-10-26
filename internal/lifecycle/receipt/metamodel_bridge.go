package receipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// metamodelOperationID is the deterministic legacy operation identity for one
// metamodel producer coordinate: the v1 domain plus the leading content bytes.
// The activation writer and the normalized-snapshot bridge share this recipe,
// so the bridge resolves exactly the operation the writer committed and no
// second activation is ever issued under a new label.
func metamodelOperationID(manifest model.LifecycleMetamodelManifest) provenance.OperationID {
	return provenance.OperationID(definitionOperationPrefix + hex.EncodeToString(manifest.Content[:16]))
}

// BridgeActiveMetamodel resolves the compile-time active metamodel producer to
// its owner-validated normalized snapshot and state. It is the current-writer
// entry to the raw-producer to normalized-snapshot bridge; historical producer
// coordinates resolve through BridgeMetamodelProducer directly.
func BridgeActiveMetamodel(ctx context.Context, reader DefinitionReader) (model.LifecycleMetamodelRef, model.DefinitionSnapshot, model.DefinitionStateRecord, error) {
	if err := ctx.Err(); err != nil {
		return model.LifecycleMetamodelRef{}, model.DefinitionSnapshot{}, model.DefinitionStateRecord{}, bridgeError("BridgeActiveMetamodel", "Metamodel bridging was canceled before resolution.", err)
	}
	return bridgeMetamodelProducerAt(ctx, reader, metamodel.Active(), 0)
}

// BridgeMetamodelProducer resolves one legacy metamodel activation — addressed
// by its raw producer coordinate — to the owner-validated normalized snapshot
// and activation state the journal actually persists.
//
// The legacy writer journals the raw producer body while the commit boundary
// persists journal-normalized bytes, so the producer content identity and the
// persisted evidence digest are generally NOT equal. The bridge therefore never
// equates the two hashes: it locates the activation by the producer recipe,
// validates the persisted bytes with the versioned metamodel owner codec,
// verifies the persisted digest and the state/operation linkage, and returns a
// reference carrying the normalized persisted digest. Constructing this typed
// reference from existing owner evidence is a representation bridge, not a
// relabelled kind: the returned kind stays the metamodel kind, and no legacy
// row is rewritten.
//
// For the active producer the bridge additionally proves manifest-to-snapshot
// agreement: owner canonicalization of the exact producer body yields the
// referenced snapshot bytes. Historical producers cannot reconstruct their raw
// bytes, so they resolve through the persisted normalized evidence, the
// manifest-bound state payload, and the operation recipe alone.
func BridgeMetamodelProducer(reader DefinitionReader, manifest model.LifecycleMetamodelManifest) (model.LifecycleMetamodelRef, model.DefinitionSnapshot, model.DefinitionStateRecord, error) {
	return bridgeMetamodelProducerAt(context.Background(), reader, manifest, 0)
}

// bridgeMetamodelProducerAt shares the batch watermark across the bridge and
// the definition resolutions: one enclosing snapshot for the whole envelope.
// A zero watermark captures a fresh one; a positive watermark is never
// widened.
func bridgeMetamodelProducerAt(ctx context.Context, reader DefinitionReader, manifest model.LifecycleMetamodelManifest, watermark provenance.JournalID) (model.LifecycleMetamodelRef, model.DefinitionSnapshot, model.DefinitionStateRecord, error) {
	var empty model.DefinitionSnapshot
	var noState model.DefinitionStateRecord
	if reader == nil || !manifest.IsValid() {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Reader or nonzero producer coordinate is missing.", nil)
	}
	operation := metamodelOperationID(manifest)
	result, err := reader.LookupCommitted(operation)
	if err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "The producing operation lookup failed.", err)
	}
	if result.Kind != provenance.CommittedExact {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "The producer coordinate names no committed metamodel activation; ensure the metamodel before bridging.", nil)
	}
	snapshotID, stateID, err := definitionSlots(result)
	if err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, err
	}
	// The enclosing evidence watermark pins one snapshot for the snapshot and
	// state reads below. A batch shares its already captured watermark. A
	// direct reader already knows the activation state ID from the committed
	// result slots, so it uses that exact bound instead of scanning the
	// definition kinds for a fresh watermark. The journal-only fallback keeps
	// the old capture for compatibility.
	if watermark == 0 {
		if _, direct := reader.(DirectEvidenceReader); direct {
			watermark = provenance.JournalID(stateID)
		} else {
			watermarkPage, err := reader.QueryEvidence(provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{definitionKind, definitionStateKind}, Page: provenance.FactPageRequest{Limit: 1}})
			if err != nil {
				return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("bridgeMetamodelProducerAt", "Enclosing evidence snapshot could not be captured.", err)
			}
			watermark = watermarkPage.SnapshotMaxJournalID
		}
	}
	id := provenance.JournalID(snapshotID)
	if watermark < provenance.JournalID(stateID) {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Activation lies beyond the enclosing snapshot watermark.", nil)
	}
	row, err := readExactEvidence(ctx, reader, definitionKind, id, watermark)
	if err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Exact metamodel snapshot row could not be read.", err)
	}
	if row.JournalID != id {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Metamodel snapshot row is missing or has the wrong evidence kind.", nil)
	}
	if row.ProducingOperationID != operation {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Metamodel snapshot producing operation does not match the producer coordinate recipe.", nil)
	}
	if result.AnchorJournalID != row.ProducingOperationJournalID || result.AnchorJournalID <= 0 || result.AnchorJournalID >= id {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Producing operation anchor disagrees with the snapshot row.", nil)
	}
	// The persisted bytes must already be journal-canonical: the commit
	// boundary normalizes every evidence payload, so non-canonical persisted
	// bytes are damage, not a second encoding.
	normalized, err := definitions.NormalizePayload(definitionKind, row.Payload)
	if err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Persisted metamodel body is malformed.", err)
	}
	if !bytes.Equal(normalized, row.Payload) {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Persisted metamodel body is not journal-canonical.", nil)
	}
	decoded, err := metamodel.DecodeBody(normalized)
	if err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Persisted metamodel body failed its owner codec.", err)
	}
	if decoded.ID != manifest.ID || decoded.Version != manifest.Version {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Persisted metamodel identity or version does not match the producer coordinate.", nil)
	}
	digest := sha256.Sum256(normalized)
	if !bytes.Equal(row.ContentDigest, digest[:]) {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Persisted metamodel evidence digest does not match its bytes.", nil)
	}
	if manifest.Content == metamodel.Active().Content {
		if err := producerAgreement(normalized); err != nil {
			return model.LifecycleMetamodelRef{}, empty, noState, err
		}
	}
	stateRow, err := readEvidenceByOperation(ctx, reader, definitionStateKind, operation, watermark)
	if err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Activation state evidence could not be read.", err)
	}
	if err := validateBridgeState(manifest, stateRow, stateID, operation, row.ProducingOperationJournalID); err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, err
	}
	ref := model.LifecycleMetamodelRef{Definition: model.DefinitionRef{
		Definition: model.DefinitionJournalID(snapshotID), Kind: model.DefinitionMetamodel, Content: model.ContentIdentity(digest),
	}}
	snapshot, err := model.NewDefinitionSnapshot(ref.Definition, decoded.ID, decoded.Version, model.DefinitionCanonicalJSON, normalized)
	if err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Model owner refused resolved snapshot construction.", err)
	}
	record, err := model.NewDefinitionStateRecord(snapshot, model.DefinitionStateFact{StateID: model.DefinitionStateID(stateID), Definition: model.DefinitionJournalID(snapshotID), Event: model.DefinitionActivated, RecordedAt: stateRow.RecordedAt}, watermark)
	if err != nil {
		return model.LifecycleMetamodelRef{}, empty, noState, bridgeError("BridgeMetamodelProducer", "Model owner refused resolved activation state construction.", err)
	}
	return ref, snapshot, record, nil
}

// producerAgreement proves manifest-to-snapshot agreement for the active
// producer: owner canonicalization of the exact producer-coordinate body
// yields the referenced snapshot bytes, and the coordinate content is the raw
// body digest. Historical producers skip this step; their raw bytes are not
// reconstructible.
//
// The normalization is memoized: the producer body and coordinate are
// compiled into the binary, so the normalized bytes are a pure function of
// the binary, not of the store. Journal truth (the snapshot row the result is
// compared against) is never cached — every call still reads and verifies
// the persisted row, digest, state and linkage.
var producerAgreementMemo = sync.OnceValues(func() ([]byte, error) {
	body := metamodel.Body()
	manifest := metamodel.Active()
	rawDigest := sha256.Sum256(body)
	if model.ContentIdentity(rawDigest) != manifest.Content {
		return nil, bridgeError("producerAgreement", "Active producer coordinate does not identify the active body.", nil)
	}
	normalized, err := definitions.NormalizePayload(definitionKind, body)
	if err != nil {
		return nil, bridgeError("producerAgreement", "Active producer body is malformed.", err)
	}
	return normalized, nil
})

func producerAgreement(snapshot []byte) error {
	normalized, err := producerAgreementMemo()
	if err != nil {
		return err
	}
	if !bytes.Equal(normalized, snapshot) {
		return bridgeError("producerAgreement", "Owner canonicalization of the producer body does not yield the snapshot bytes.", nil)
	}
	return nil
}

// validateBridgeState verifies the activation state row against the producer
// coordinate: the state payload recomputed from the manifest must equal the
// persisted canonical bytes, and the row must link the same operation, anchor,
// and ordered state slot.
func validateBridgeState(manifest model.LifecycleMetamodelManifest, stateRow provenance.EvidenceRow, stateID provenance.JournalID, operation provenance.OperationID, anchor provenance.JournalID) error {
	expected, err := canonicalDefinitionStatePayload(manifest)
	if err != nil {
		return bridgeError("validateBridgeState", "Producer state payload could not be recomputed.", err)
	}
	normalized, err := definitions.NormalizePayload(definitionStateKind, expected)
	if err != nil {
		return bridgeError("validateBridgeState", "Producer state payload is malformed.", err)
	}
	stateNormalized, err := definitions.NormalizePayload(definitionStateKind, stateRow.Payload)
	if err != nil || !bytes.Equal(stateNormalized, stateRow.Payload) {
		return bridgeError("validateBridgeState", "Persisted activation state is malformed or not journal-canonical.", err)
	}
	if !bytes.Equal(normalized, stateRow.Payload) {
		return bridgeError("validateBridgeState", "Activation state does not carry the producer coordinate.", nil)
	}
	stateDigest := sha256.Sum256(normalized)
	if !bytes.Equal(stateDigest[:], stateRow.ContentDigest) {
		return bridgeError("validateBridgeState", "Activation state digest does not match its bytes.", nil)
	}
	var wire struct {
		Definition string `json:"definition"`
		Event      string `json:"event"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stateRow.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return bridgeError("validateBridgeState", "Activation state typed fields are invalid.", err)
	}
	if wire.Definition != hex.EncodeToString(manifest.Content[:]) || wire.Event != "activated" {
		return bridgeError("validateBridgeState", "Activation state coordinate or event is not the producer activation.", nil)
	}
	if stateRow.JournalID != provenance.JournalID(stateID) || stateRow.ProducingOperationID != operation || stateRow.ProducingOperationJournalID != anchor {
		return bridgeError("validateBridgeState", "Activation state operation linkage is damaged.", nil)
	}
	return nil
}

// readExactEvidence reads one known evidence row. The direct primary-key door
// is the hot path; the journal-backed page remains the fallback for hand-built
// services and transaction-bound readers.
func readExactEvidence(ctx context.Context, reader DefinitionReader, kind provenance.EvidenceKind, id provenance.JournalID, watermark provenance.JournalID) (provenance.EvidenceRow, error) {
	if direct, ok := reader.(DirectEvidenceReader); ok {
		row, found, err := direct.EvidenceAt(ctx, kind, id)
		if err != nil {
			return provenance.EvidenceRow{}, err
		}
		if !found {
			return provenance.EvidenceRow{}, fmt.Errorf("evidence row %d of kind %q is absent", id, kind)
		}
		if row.JournalID != id || row.EvidenceKind != kind {
			return provenance.EvidenceRow{}, fmt.Errorf("evidence row %d does not match kind %q", id, kind)
		}
		return row, nil
	}
	page, err := reader.QueryEvidence(provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{kind}, Page: provenance.FactPageRequest{Limit: 1, SnapshotMaxJournalID: id, AfterJournalID: id - 1}})
	if err != nil {
		return provenance.EvidenceRow{}, err
	}
	if len(page.Rows) != 1 || page.Rows[0].JournalID != id {
		return provenance.EvidenceRow{}, fmt.Errorf("evidence row %d of kind %q is absent", id, kind)
	}
	if watermark > 0 && provenance.JournalID(id) > watermark {
		return provenance.EvidenceRow{}, fmt.Errorf("evidence row %d lies beyond watermark %d", id, watermark)
	}
	return page.Rows[0], nil
}

// readEvidenceByOperation reads the state row produced by one activation
// operation. The direct door looks up the operation's journal anchor and then
// its evidence row; the journal-backed fallback keeps the original filtered
// page semantics.
func readEvidenceByOperation(ctx context.Context, reader DefinitionReader, kind provenance.EvidenceKind, operation provenance.OperationID, watermark provenance.JournalID) (provenance.EvidenceRow, error) {
	if direct, ok := reader.(DirectEvidenceReader); ok {
		row, found, err := direct.EvidenceByOperation(ctx, kind, operation)
		if err != nil {
			return provenance.EvidenceRow{}, err
		}
		if !found {
			return provenance.EvidenceRow{}, fmt.Errorf("evidence of kind %q for operation %q is absent", kind, operation)
		}
		if row.EvidenceKind != kind || row.ProducingOperationID != operation {
			return provenance.EvidenceRow{}, fmt.Errorf("evidence for operation %q does not match kind %q", operation, kind)
		}
		if watermark > 0 && row.JournalID > watermark {
			return provenance.EvidenceRow{}, fmt.Errorf("evidence for operation %q lies beyond watermark %d", operation, watermark)
		}
		return row, nil
	}
	states, err := reader.QueryEvidence(provenance.EvidenceQuery{
		Filter: provenance.FactFilter{OperationIDs: []provenance.OperationID{operation}},
		Kinds:  []provenance.EvidenceKind{kind},
		Page:   provenance.FactPageRequest{Limit: provenance.MaxFactPageSize, SnapshotMaxJournalID: watermark},
	})
	if err != nil {
		return provenance.EvidenceRow{}, err
	}
	if len(states.Rows) != 1 || states.Next != nil {
		return provenance.EvidenceRow{}, fmt.Errorf("operation %q did not produce exactly one state evidence row", operation)
	}
	return states.Rows[0], nil
}

func bridgeError(step, why string, cause error) error {
	return structured(pasterrors.CategoryStorage, "The legacy metamodel activation could not be bridged to its normalized snapshot.", why,
		"internal/lifecycle/receipt/metamodel_bridge.go in receipt."+step+" during metamodel bridge resolution",
		"No bridged metamodel reference was returned; existing committed rows were not repaired or replaced.",
		"Inspect the named producer coordinate, snapshot/state rows and operation; restore damaged journal rows from a verified backup before retrying.", cause)
}
