package tasks

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
)

func TestEpochAssignmentServicePublishesCompleteCurrentSnapshot(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	bindTestGovernedAllocation(t, tracker)
	actor, err := tracker.RegisterHumanAgent("integration-supervisor", "Integration Supervisor", "integration@example.test")
	if err != nil {
		t.Fatalf("register actor: %v", err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	plan := createHumanTestTask(t, tracker, "plan")
	seedAssignmentEpisode(t, tracker, plan, "integration-supervisor", RoleGoverningSupervisor, actor.ID, "integration-plan-seed")
	service, err := tracker.NewEpochService(EpochServiceOptions{})
	if err != nil {
		t.Fatalf("construct epoch service: %v", err)
	}
	commit := provenance.GitOID("0123456789abcdef0123456789abcdef01234567")
	slice, err := service.CreateSlice(context.Background(), CreateSliceInput{Meta: CommandMeta{OperationID: "integration-slice"}, Epoch: EpochRootID(epoch.String()), Plan: plan, Assignment: "integration-supervisor"})
	if err != nil {
		t.Fatalf("create slice: %v", err)
	}
	seedAssignmentEpisode(t, tracker, slice.Slice, "integration-owner", RoleOwnerResponsibility, actor.ID, "integration-owner-seed")
	memberCandidate, err := service.SetSliceCandidate(context.Background(), SetSliceCandidateInput{Meta: CommandMeta{OperationID: "integration-member"}, Epoch: EpochRootID(epoch.String()), Slice: slice.Slice, Repository: RepositoryID("repo-a"), Commit: commit, Assignment: "integration-owner"})
	if err != nil {
		t.Fatalf("set slice candidate: %v", err)
	}
	memberReplay, err := service.SetSliceCandidate(context.Background(), SetSliceCandidateInput{Meta: CommandMeta{OperationID: "integration-member"}, Epoch: EpochRootID(epoch.String()), Slice: slice.Slice, Repository: RepositoryID("repo-a"), Commit: commit, Assignment: "integration-owner"})
	if err != nil || !memberReplay.Replayed || memberReplay.Candidate != memberCandidate.Candidate || memberReplay.ActivityID != memberCandidate.ActivityID {
		t.Fatalf("exact slice candidate replay = %+v, err=%v; want exact replay", memberReplay, err)
	}
	created, err := service.CreateIntegrationCandidate(context.Background(), CreateIntegrationCandidateInput{
		Meta:       CommandMeta{OperationID: "integration-create"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "integration-supervisor",
		Repositories: []RepositoryCandidate{{
			Repository: RepositoryID("repo-a"),
			Candidate:  memberCandidate.Candidate,
			Commit:     commit,
		}},
	})
	if err != nil {
		t.Fatalf("create integration candidate: %v", err)
	}
	integrationReplay, err := service.CreateIntegrationCandidate(context.Background(), CreateIntegrationCandidateInput{Meta: CommandMeta{OperationID: "integration-create"}, Epoch: EpochRootID(epoch.String()), Plan: plan, Assignment: "integration-supervisor", Repositories: []RepositoryCandidate{{Repository: RepositoryID("repo-a"), Candidate: memberCandidate.Candidate, Commit: commit}}})
	if err != nil || !integrationReplay.Replayed || integrationReplay.Candidate != created.Candidate || integrationReplay.ActivityID != created.ActivityID {
		t.Fatalf("exact integration candidate replay = %+v, err=%v; want exact replay", integrationReplay, err)
	}
	changedCommit := provenance.GitOID("1123456789abcdef0123456789abcdef01234567")
	if _, err := service.CreateIntegrationCandidate(context.Background(), CreateIntegrationCandidateInput{Meta: CommandMeta{OperationID: "integration-create"}, Epoch: EpochRootID(epoch.String()), Plan: plan, Assignment: "integration-supervisor", Repositories: []RepositoryCandidate{{Repository: RepositoryID("repo-a"), Candidate: memberCandidate.Candidate, Commit: changedCommit}}}); err == nil {
		t.Fatal("changed integration replay succeeded; want conflict with no writes")
	}
	candidate, err := provenance.ParseTaskID(string(created.Candidate))
	if err != nil {
		t.Fatalf("parse integration candidate: %v", err)
	}
	published, err := service.PublishRepository(context.Background(), PublishRepositoryInput{
		Meta:       CommandMeta{OperationID: "integration-publish"},
		Epoch:      EpochRootID(epoch.String()),
		Candidate:  created.Candidate,
		Repository: RepositoryID("repo-a"),
		Ref:        GitRef("refs/heads/main"),
		Commit:     commit,
		Assignment: "integration-create-candidate-owner",
	})
	if err != nil {
		t.Fatalf("publish repository: %v", err)
	}
	if published.Evidence <= 0 || published.ActivityID == (provenance.ActivityID{}) || len(published.EventIDs) == 0 {
		t.Fatalf("incomplete publication result: %+v", published)
	}
	set, err := service.(*epochService).EpochAssignmentService.(*epochAssignmentService).currentCandidatePublicationSet(epoch, candidate)
	if err != nil {
		t.Fatalf("read current publication set: %v", err)
	}
	if len(set.value.Publications) != 1 || set.value.Publications[0].Repository != "repo-a" || set.value.Publications[0].Commit != commit {
		t.Fatalf("publication snapshot = %+v", set.value)
	}
}
