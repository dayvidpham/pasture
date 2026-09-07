package tasks

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/internal/lifecycle/gateauthority"
	"github.com/dayvidpham/provenance"
)

// preparedAssignmentCatchUp is the recovery seam consumed by the Reader. It is
// not a Snapshot or an Authority. The Reader still owns current governance,
// phase checks, its final torn probe, and the final local linearization check.
// In particular, HistoricalPredicates must be subtracted from its shared budget.
type preparedAssignmentCatchUp struct {
	State                assignmentRecoveryState
	Page                 assignmentIndexPage
	HistoricalPredicates int
}

func (t *trackerImpl) prepareAssignmentCatchUp(ctx context.Context, snapshot provenance.JournalID, pinned bool) (preparedAssignmentCatchUp, error) {
	var result preparedAssignmentCatchUp
	state, err := readAssignmentRecoveryState(ctx, t.auditDB)
	if err != nil {
		return result, recoveryFault("gate state", err)
	}
	if state.Status != assignmentCoverageValid || state.Destructive != state.CertifiedDestructive || !state.Complete {
		return result, recoveryFault("gate state", fmt.Errorf("index is dirty, rebuilding or has an incomplete operator scan"))
	}
	api, ok := t.Journal().(provenance.AssignmentStartQueryAPI)
	if !ok {
		return result, recoveryFault("gate assignment capability", fmt.Errorf("public assignment-start query unavailable"))
	}
	if err := ctx.Err(); err != nil {
		return result, recoveryFault("gate cancellation", err)
	}
	page, err := api.QueryAssignmentStarts(provenance.AssignmentStartQuery{Page: provenance.AssignmentStartPageRequest{
		Limit: gateauthority.CatchUpPageSize, SnapshotMaxJournalID: snapshot, SnapshotPinned: pinned, AfterJournalID: state.Through,
	}})
	if err != nil {
		return result, recoveryFault("gate assignment page", err)
	}
	if page.Next != nil {
		return result, recoveryFault("gate assignment page", fmt.Errorf("assignment tail exceeds one page; operator rebuild required"))
	}
	var taskIDs []provenance.TaskID
	var operationIDs []provenance.OperationID
	tasks := map[provenance.TaskID]bool{}
	operations := map[provenance.OperationID]bool{}
	for _, row := range page.Rows {
		if !tasks[row.TaskID] {
			tasks[row.TaskID] = true
			taskIDs = append(taskIDs, row.TaskID)
		}
	}
	var material []provenance.TaskEventRow
	var evidence []provenance.EvidenceRow
	// Empty filters mean unfiltered history in these public APIs, not an empty
	// population. Keep the guards adjacent to the only two auxiliary query sites.
	if len(taskIDs) > 0 {
		if err := ctx.Err(); err != nil {
			return result, recoveryFault("gate material cancellation", err)
		}
		facts, err := t.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: taskIDs,
			EventKinds: []provenance.EventKind{FamilyAssignmentStarted.EventKind()}, Limit: gateauthority.CatchUpPageSize, SnapshotMaxJournalID: page.SnapshotMaxJournalID})
		if err != nil {
			return result, recoveryFault("gate material page", err)
		}
		if facts.Next != nil {
			return result, recoveryFault("gate material page", fmt.Errorf("material exactness requires an operator drain"))
		}
		material = facts.Events
	}
	decoded, err := decodeRecoveryMaterials(material, page.SnapshotMaxJournalID)
	if err != nil {
		return result, recoveryFault("gate material decoding", err)
	}
	for _, row := range page.Rows {
		if row.PredecessorAssignmentID != nil {
			continue
		}
		m, present := decoded[recoveryMaterialKey{row.TaskID, row.AssignmentID}]
		if present && *m.Row.ProducedByOperationJournalID == row.ProducingOperationJournalID {
			continue
		}
		op := provenance.GovernedAllocationSupplementOperationID(row.ProducingOperationID)
		if !operations[op] {
			operations[op] = true
			operationIDs = append(operationIDs, op)
		}
	}
	if len(operationIDs) > 0 {
		if err := ctx.Err(); err != nil {
			return result, recoveryFault("gate evidence cancellation", err)
		}
		facts, err := t.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{
			Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskAny}, OperationIDs: operationIDs},
			Kinds:  []provenance.EvidenceKind{assignmentCommandEvidenceKind, reviewRoundAuthorityEvidenceKind},
			Page:   provenance.FactPageRequest{Limit: gateauthority.CatchUpPageSize, SnapshotMaxJournalID: page.SnapshotMaxJournalID},
		})
		if err != nil {
			return result, recoveryFault("gate evidence page", err)
		}
		if facts.Next != nil {
			return result, recoveryFault("gate evidence page", fmt.Errorf("evidence exactness requires an operator drain"))
		}
		evidence = facts.Rows
	}
	previous, err := recoveryPredecessors(ctx, t.auditDB, state, page.Rows)
	if err != nil {
		return result, recoveryFault("gate predecessor read", err)
	}
	proof, err := authenticateRecoveryRows(ctx, t.Journal(), page, material, evidence, previous, state.Through)
	if err != nil {
		return result, err
	}
	seen := map[provenance.AssignmentID]startedEpisode{}
	for _, row := range proof.Rows {
		seen[row.Assignment] = row
	}
	for _, member := range proof.Members {
		row, ok := seen[member.Assignment]
		if !ok || row.Task != member.Task || row.Actor != member.Actor || row.Role != RoleAxisReviewer {
			return result, recoveryFault("gate review completeness", fmt.Errorf("bounded page leaves pending review members"))
		}
	}
	result.State = state
	result.Page = assignmentIndexPage{
		From:     state.Through,
		Through:  page.SnapshotMaxJournalID,
		Rows:     proof.Rows,
		Expected: &result.State,
		Members:  proof.Members,
	}
	result.HistoricalPredicates = proof.HistoricalPredicates
	return result, nil
}

// persistAssignmentCatchUp makes exactly one persistence attempt. Its caller
// places this AFTER the final journal/torn probe; it cannot retry within a gate.
func (t *trackerImpl) persistAssignmentCatchUp(ctx context.Context, prepared preparedAssignmentCatchUp) error {
	return persistAssignmentIndex(ctx, t.auditDB, t.timeoutProfile, prepared.Page)
}
