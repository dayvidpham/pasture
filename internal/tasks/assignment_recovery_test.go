package tasks

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
	"github.com/stretchr/testify/require"
)

func TestAssignmentRecoveryFactoryCertifiesOnlyAnEmptyPrefix(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	opened, err := OpenTaskTracker(path)
	require.NoError(t, err)
	store := opened.(*trackerImpl)
	defer store.Close()
	var status string
	var generation, revision, destructive, certified, complete int64
	require.NoError(t, store.auditDB.QueryRow(`SELECT coverage_status, generation, state_revision,
		destructive_revision, certified_destructive_revision, recovery_complete
		FROM pasture_actor_assignment_state WHERE singleton_id=0`).Scan(
		&status, &generation, &revision, &destructive, &certified, &complete))
	require.Equal(t, "valid", status)
	require.Zero(t, generation)
	require.Positive(t, revision)
	require.Equal(t, destructive, certified)
	require.EqualValues(t, 1, complete)
}

func TestAssignmentRecoveryOrdinaryConflictCommitsDirtyWithoutOverwrite(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	actor := feasibilityActor(t, store, "ordinary-conflict")
	task := createHumanTestTask(t, store, "ordinary-conflict")
	state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	row := startedEpisode{Assignment: "conflict", Actor: actor, Task: task,
		Role: RoleOwnerResponsibility, Authority: state.Through + 1}
	require.NoError(t, recordAssignmentStart(t.Context(), store.auditDB, row))
	require.NoError(t, recordAssignmentStart(t.Context(), store.auditDB, row))
	row.Authority++
	err = recordAssignmentStart(t.Context(), store.auditDB, row)
	var stale *IndexStaleError
	require.ErrorAs(t, err, &stale)
	actual, err := readAssignmentRecoveryState(context.Background(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageDirty, actual.Status)
	require.Greater(t, actual.Destructive, actual.CertifiedDestructive)
	var authority provenance.JournalID
	require.NoError(t, store.auditDB.QueryRow(`SELECT authority_journal_id FROM pasture_actor_assignment WHERE assignment_id='conflict'`).Scan(&authority))
	require.Equal(t, row.Authority-1, authority)
	require.ErrorAs(t, recordAssignmentStart(t.Context(), store.auditDB, row), &stale)
}

func TestAssignmentRecoveryResetAndValidTailResume(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pasture.db")
	store := openHumanTestTracker(t, path)
	actor := feasibilityActor(t, store, "resume")
	for i := 0; i < 3; i++ {
		task := createHumanTestTask(t, store, fmt.Sprintf("resume-%d", i))
		seedFeasibilityEpisode(t, store, task, provenance.AssignmentID(fmt.Sprintf("resume-%d", i)), actor, provenance.OperationID(fmt.Sprintf("start-%d", i)))
	}
	scan, err := beginAssignmentRecoveryScan(t.Context(), store.auditDB, store.Journal(), 2)
	require.NoError(t, err)
	require.NotNil(t, scan.Page.Next)
	var proof assignmentRecoveryProof
	for _, row := range scan.Page.Rows {
		proof.Rows = append(proof.Rows, startedEpisode{Assignment: row.AssignmentID, Actor: row.Occupant, Task: row.TaskID, Role: RoleOwnerResponsibility, Authority: row.AuthorityJournalID})
	}
	state, err := publishAssignmentRecoveryPage(t.Context(), store.auditDB, scan, proof)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageValid, state.Status)
	require.False(t, state.Complete)
	require.NoError(t, store.Close())
	store = openHumanTestTracker(t, path)
	defer store.Close()
	task := createHumanTestTask(t, store, "after-snapshot")
	seedFeasibilityEpisode(t, store, task, "later", actor, "later-start")
	resumed, err := beginAssignmentRecoveryScan(t.Context(), store.auditDB, store.Journal(), 2)
	require.NoError(t, err)
	require.Equal(t, state.Snapshot, resumed.Page.SnapshotMaxJournalID)
	require.Equal(t, state.After, resumed.State.After)
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{PageSize: 2}))
	completed, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, state.Snapshot, completed.Through)
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{PageSize: 2}))
	later, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Greater(t, later.Through, completed.Through)
	_, err = store.auditDB.Exec(`DELETE FROM pasture_actor_assignment WHERE assignment_id='later'`)
	require.NoError(t, err)
	dirty, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, assignmentCoverageDirty, dirty.Status)
	require.Error(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{}))
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}))
	reset, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.Equal(t, dirty.Generation+1, reset.Generation)
	require.Equal(t, assignmentCoverageValid, reset.Status)
	_, err = resetAssignmentRecovery(t.Context(), store.auditDB, dirty)
	require.Error(t, err)
}

func TestAssignmentRecoveryRealComposedCommandsAndSplitReview(t *testing.T) {
	t.Parallel()
	store := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer store.Close()
	bindTestGovernedAllocation(t, store)
	actor := feasibilityActor(t, store, "composed-recovery")
	epoch := createHumanTestTask(t, store, "epoch")
	plan := createHumanTestTask(t, store, "plan")
	seedAssignmentEpisode(t, store, plan, "supervisor", RoleGoverningSupervisor, actor, "supervisor-start")
	service, err := store.NewEpochService(EpochServiceOptions{})
	require.NoError(t, err)
	slice, err := service.CreateSlice(t.Context(), CreateSliceInput{Meta: CommandMeta{OperationID: "recover-slice"}, Epoch: EpochRootID(epoch.String()), Plan: plan, Assignment: "supervisor"})
	require.NoError(t, err)
	prepared, err := store.prepareAssignmentCatchUp(t.Context(), 0, false)
	require.NoError(t, err)
	require.NoError(t, store.persistAssignmentCatchUp(t.Context(), prepared))
	commit := provenance.GitOID("0123456789abcdef0123456789abcdef01234567")
	member, err := service.SetSliceCandidate(t.Context(), SetSliceCandidateInput{Meta: CommandMeta{OperationID: "recover-member"}, Epoch: EpochRootID(epoch.String()), Slice: slice.Slice, Repository: "repo-z", Commit: commit, Assignment: "recover-slice-slice-owner"})
	require.NoError(t, err)
	integration, err := service.CreateIntegrationCandidate(t.Context(), CreateIntegrationCandidateInput{Meta: CommandMeta{OperationID: "recover-integration"}, Epoch: EpochRootID(epoch.String()), Plan: plan, Assignment: "supervisor", Repositories: []RepositoryCandidate{{Repository: "repo-z", Candidate: member.Candidate, Commit: commit}}})
	require.NoError(t, err)
	_, err = service.StartReview(t.Context(), StartReviewInput{Meta: CommandMeta{OperationID: "recover-review"}, Epoch: EpochRootID(epoch.String()), Subject: ReviewSubjectRef{Kind: ReviewSubjectImplementationCandidate, SnapshotID: string(integration.Candidate)}})
	require.NoError(t, err)
	require.NoError(t, RebuildAssignmentIndex(t.Context(), store, AssignmentIndexRebuildOptions{Reset: true, PageSize: 2}))
	var count int
	require.NoError(t, store.auditDB.QueryRow(`SELECT COUNT(*) FROM pasture_actor_assignment WHERE assignment_id LIKE 'recover-review-%'`).Scan(&count))
	require.Equal(t, 13, count)
	state, err := readAssignmentRecoveryState(t.Context(), store.auditDB)
	require.NoError(t, err)
	require.True(t, state.Complete)
	require.Equal(t, assignmentCoverageValid, state.Status)
}
