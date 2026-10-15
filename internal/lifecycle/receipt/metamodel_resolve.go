package receipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/dayvidpham/pasture/internal/lifecycle/metamodel"
	"github.com/dayvidpham/pasture/internal/lifecycle/model"
	"github.com/dayvidpham/provenance"
)

// ResolveMetamodelRef validates one semantic-envelope metamodel reference
// against its legacy v1 snapshot and activation state. It is the explicit
// legacy-codec branch of semantic resolution: the v1 snapshot bytes are
// validated with the versioned metamodel owner codec, the persisted evidence
// digest must equal the ref content, and the activation state and producing
// operation linkage must agree. The raw producer hash is never equated with
// the normalized persisted digest; the bridge owns that proof.
//
// For the active producer the resolution additionally proves
// manifest-to-snapshot agreement: owner canonicalization of the exact
// producer-coordinate body yields the referenced snapshot bytes. Historical
// producers resolve through the persisted normalized evidence, the
// manifest-bound state payload, and the operation recipe alone.
func ResolveMetamodelRef(reader DefinitionReader, ref model.DefinitionRef, watermark provenance.JournalID) (model.DefinitionSnapshot, model.DefinitionStateRecord, error) {
	var empty model.DefinitionSnapshot
	var noState model.DefinitionStateRecord
	if reader == nil || ref.Definition <= 0 || ref.Kind != model.DefinitionMetamodel || ref.Content == (model.ContentIdentity{}) || watermark < 0 {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Reader, positive journal coordinate, metamodel kind or content identity is missing.", nil)
	}
	if watermark == 0 {
		page, err := reader.QueryEvidence(provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{definitionKind, definitionStateKind}, Page: provenance.FactPageRequest{Limit: 1}})
		if err != nil {
			return empty, noState, bridgeError("ResolveMetamodelRef", "Enclosing evidence snapshot could not be captured.", err)
		}
		watermark = page.SnapshotMaxJournalID
	}
	id := provenance.JournalID(ref.Definition)
	if watermark < id {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Metamodel snapshot lies beyond the enclosing snapshot watermark.", nil)
	}
	page, err := reader.QueryEvidence(provenance.EvidenceQuery{Kinds: []provenance.EvidenceKind{definitionKind}, Page: provenance.FactPageRequest{Limit: 1, SnapshotMaxJournalID: id, AfterJournalID: id - 1}})
	if err != nil {
		return empty, noState, bridgeError("ResolveMetamodelRef", fmt.Sprintf("Exact metamodel snapshot row %d could not be read.", id), err)
	}
	if len(page.Rows) != 1 || page.Rows[0].JournalID != id {
		return empty, noState, bridgeError("ResolveMetamodelRef", fmt.Sprintf("Metamodel snapshot row %d is missing or has the wrong evidence kind.", id), nil)
	}
	row := page.Rows[0]
	normalized, err := definitions.NormalizePayload(definitionKind, row.Payload)
	if err != nil {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Persisted metamodel body is malformed.", err)
	}
	if !bytes.Equal(normalized, row.Payload) {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Persisted metamodel body is not journal-canonical.", nil)
	}
	decoded, err := metamodel.DecodeBody(normalized)
	if err != nil {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Persisted metamodel body failed its owner codec.", err)
	}
	digest := sha256.Sum256(normalized)
	if model.ContentIdentity(digest) != ref.Content || !bytes.Equal(row.ContentDigest, digest[:]) {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Persisted metamodel evidence digest does not match the referenced content.", nil)
	}
	result, err := reader.LookupCommitted(row.ProducingOperationID)
	if err != nil {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Producing operation result-slot reconstruction failed.", err)
	}
	snapshotID, stateID, err := definitionSlots(result)
	if err != nil {
		return empty, noState, err
	}
	if snapshotID != id || stateID > watermark || result.AnchorJournalID != row.ProducingOperationJournalID || result.AnchorJournalID <= 0 || result.AnchorJournalID >= id {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Producing operation anchor or ordered result slots disagree with the snapshot/reference.", nil)
	}
	// The v1 operation recipe is content-derived from the RAW producer
	// coordinate, which a normalized-only ref cannot reconstruct, so no
	// recipe equality is asserted here. The ordered result slots, the anchor,
	// and the state row's same-operation linkage below bind this snapshot to
	// exactly the activation that produced it.
	states, err := reader.QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{OperationIDs: []provenance.OperationID{row.ProducingOperationID}}, Kinds: []provenance.EvidenceKind{definitionStateKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize, SnapshotMaxJournalID: watermark}})
	if err != nil {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Activation state evidence could not be read under the enclosing watermark.", err)
	}
	if len(states.Rows) != 1 || states.Next != nil {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Activation operation must produce exactly one state evidence row.", nil)
	}
	stateRow := states.Rows[0]
	if stateRow.JournalID != provenance.JournalID(stateID) || stateRow.ProducingOperationID != row.ProducingOperationID || stateRow.ProducingOperationJournalID != row.ProducingOperationJournalID {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Activation state operation linkage is damaged.", nil)
	}
	if err := validateResolvedBridgeState(stateRow); err != nil {
		return empty, noState, err
	}
	if manifest, ok := activeProducerManifest(); ok && decoded.ID == manifest.ID && decoded.Version == manifest.Version {
		if err := producerAgreement(normalized); err != nil {
			return empty, noState, err
		}
	}
	snapshot, err := model.NewDefinitionSnapshot(ref, decoded.ID, decoded.Version, model.DefinitionCanonicalJSON, normalized)
	if err != nil {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Model owner refused resolved snapshot construction.", err)
	}
	record, err := model.NewDefinitionStateRecord(snapshot, model.DefinitionStateFact{StateID: model.DefinitionStateID(stateID), Definition: model.DefinitionJournalID(snapshotID), Event: model.DefinitionActivated, RecordedAt: stateRow.RecordedAt}, watermark)
	if err != nil {
		return empty, noState, bridgeError("ResolveMetamodelRef", "Model owner refused resolved activation state construction.", err)
	}
	return snapshot, record, nil
}

// validateResolvedBridgeState verifies a v1 activation state row is canonical,
// digest-authenticated, and shaped as a producer activation.
func validateResolvedBridgeState(stateRow provenance.EvidenceRow) error {
	normalized, err := definitions.NormalizePayload(definitionStateKind, stateRow.Payload)
	if err != nil || !bytes.Equal(normalized, stateRow.Payload) {
		return bridgeError("validateResolvedBridgeState", "Persisted activation state is malformed or not journal-canonical.", err)
	}
	sum := sha256.Sum256(normalized)
	if !bytes.Equal(sum[:], stateRow.ContentDigest) {
		return bridgeError("validateResolvedBridgeState", "Activation state digest does not match its bytes.", nil)
	}
	var wire struct {
		Definition string `json:"definition"`
		Event      string `json:"event"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stateRow.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return bridgeError("validateResolvedBridgeState", "Activation state typed fields are invalid.", err)
	}
	if len(wire.Definition) != 2*sha256.Size || wire.Event != "activated" {
		return bridgeError("validateResolvedBridgeState", "Activation state coordinate or event is not a producer activation.", nil)
	}
	return nil
}

// activeProducerManifest reports the compile-time active metamodel producer
// coordinate for the agreement proof.
func activeProducerManifest() (model.LifecycleMetamodelManifest, bool) {
	manifest := metamodel.Active()
	if !manifest.IsValid() {
		return model.LifecycleMetamodelManifest{}, false
	}
	return manifest, true
}
