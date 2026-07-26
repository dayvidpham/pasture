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
	actor, err := tracker.RegisterHumanAgent("integration-supervisor", "Integration Supervisor", "integration@example.test")
	if err != nil {
		t.Fatalf("register actor: %v", err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	plan := createHumanTestTask(t, tracker, "plan")
	member := createHumanTestTask(t, tracker, "member")
	seedAssignmentEpisode(t, tracker, plan, "integration-supervisor", RoleGoverningSupervisor, actor.ID, "integration-plan-seed")
	service, err := tracker.NewEpochService(EpochServiceOptions{})
	if err != nil {
		t.Fatalf("construct epoch service: %v", err)
	}
	commit := provenance.GitOID("0123456789abcdef0123456789abcdef01234567")
	created, err := service.CreateIntegrationCandidate(context.Background(), CreateIntegrationCandidateInput{
		Meta:       CommandMeta{OperationID: "integration-create"},
		Epoch:      EpochRootID(epoch.String()),
		Plan:       plan,
		Assignment: "integration-supervisor",
		Repositories: []RepositoryCandidate{{
			Repository: RepositoryID("repo-a"),
			Candidate:  ImplementationCandidateID(member.String()),
			Commit:     commit,
		}},
	})
	if err != nil {
		t.Fatalf("create integration candidate: %v", err)
	}
	candidate, err := provenance.ParseTaskID(string(created.Candidate))
	if err != nil {
		t.Fatalf("parse integration candidate: %v", err)
	}
	seedAssignmentEpisode(t, tracker, candidate, "integration-publisher", RoleGoverningSupervisor, actor.ID, "integration-candidate-seed")
	published, err := service.PublishRepository(context.Background(), PublishRepositoryInput{
		Meta:       CommandMeta{OperationID: "integration-publish"},
		Epoch:      EpochRootID(epoch.String()),
		Candidate:  created.Candidate,
		Repository: RepositoryID("repo-a"),
		Ref:        GitRef("refs/heads/main"),
		Commit:     commit,
		Assignment: "integration-publisher",
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
