package tasks

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"
)

func TestEpochHumanServiceAuthorityImplementationUAT(t *testing.T) {
	t.Run("missing review rejects without writes", func(t *testing.T) {
		tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
		defer tracker.Close()
		human, err := tracker.RegisterHumanAgent("authority", "Authority Human", "")
		if err != nil {
			t.Fatal(err)
		}
		epoch := createHumanTestTask(t, tracker, "epoch")
		candidate := createHumanTestTask(t, tracker, "candidate")
		seedCurrentCandidateLifecycle(t, tracker, epoch, candidate, "uat-no-review-state")
		service := newHumanTestService(t, tracker)
		input := authorityImplementationUATInput(epoch, candidate, human.ID, "uat-no-review")
		scope := humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{input.Meta.OperationID})
		before := humanDecisionStoreFootprint(t, tracker, scope)
		if _, err := service.RecordImplementationUAT(context.Background(), input); err == nil {
			t.Fatal("Implementation UAT without a clean review succeeded")
		}
		after := humanDecisionStoreFootprint(t, tracker, scope)
		assertHumanDecisionStoreFootprintEqual(t, before, after)
		assertCommittedAbsent(t, after, input.Meta.OperationID)
	})

	t.Run("clean review binds and stale review has zero loser partials", func(t *testing.T) {
		tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
		defer tracker.Close()
		human, err := tracker.RegisterHumanAgent("authority", "Authority Human", "")
		if err != nil {
			t.Fatal(err)
		}
		epoch := createHumanTestTask(t, tracker, "epoch")
		candidate := createHumanTestTask(t, tracker, "candidate")
		stateFact := seedCurrentCandidateLifecycle(t, tracker, epoch, candidate, "uat-clean-state")
		reviewFact := seedCleanImplementationReview(t, tracker, epoch, candidate, "uat-clean-round", "uat-clean-review")
		service := newHumanTestService(t, tracker)
		input := authorityImplementationUATInput(epoch, candidate, human.ID, "uat-clean")
		result, err := service.RecordImplementationUAT(context.Background(), input)
		if err != nil {
			t.Fatalf("clean-review Implementation UAT: %v", err)
		}
		binding := authorityBindingForOperation(t, tracker, candidate, input.Meta.OperationID)
		if binding.ReviewFact != reviewFact || binding.ReviewRound != "uat-clean-round" || binding.Operation != input.Meta.OperationID {
			t.Fatalf("Implementation UAT review binding = %+v; want exact clean review %d", binding, reviewFact)
		}
		conditions := authorityPreconditionsForOperation(t, tracker, candidate, input.Meta.OperationID)
		wantConditions := []conditionSnapshot{
			oracleEvidenceCondition(candidate, candidateEvidenceKind, stateFact, provenance.ConditionCurrentFact),
			oracleEvidenceCondition(candidate, implementationReviewAuthorityEvidenceKind, reviewFact, provenance.ConditionCurrentFact),
		}
		if !reflect.DeepEqual(conditions, wantConditions) {
			t.Fatalf("Implementation UAT conditions = %+v; want %+v", conditions, wantConditions)
		}
		if result.Replayed {
			t.Fatal("fresh clean-review Implementation UAT was marked replayed")
		}

		barrier := &callbackEpochBarrier{}
		barrier.after = func() error {
			appendStartedReviewAuthority(t, tracker, epoch, candidate, "uat-later-round", "uat-later-review")
			return nil
		}
		loser, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
		if err != nil {
			t.Fatal(err)
		}
		loserInput := authorityImplementationUATInput(epoch, candidate, human.ID, "uat-stale-review")
		before := humanDecisionStoreFootprint(t, tracker, humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{loserInput.Meta.OperationID}))
		_, err = loser.RecordImplementationUAT(context.Background(), loserInput)
		if !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
			t.Fatalf("stale review Implementation UAT = %v, barrier calls=%d; want condition failure after one barrier", err, barrier.calls)
		}
		after := humanDecisionStoreFootprint(t, tracker, humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{loserInput.Meta.OperationID}))
		assertCommittedAbsent(t, after, loserInput.Meta.OperationID)
		assertNoOperationPartials(t, after, loserInput.Meta.OperationID)
		if len(after.Evidence) != len(before.Evidence)+1 {
			t.Fatalf("stale review loser wrote an unexpected partial: before=%d evidence after=%d", len(before.Evidence), len(after.Evidence))
		}
	})

	t.Run("reopen replay preserves original review identity", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "pasture.db")
		tracker := openHumanTestTracker(t, db)
		human, err := tracker.RegisterHumanAgent("authority", "Authority Human", "")
		if err != nil {
			t.Fatal(err)
		}
		epoch := createHumanTestTask(t, tracker, "epoch")
		candidate := createHumanTestTask(t, tracker, "candidate")
		stateFact := seedCurrentCandidateLifecycle(t, tracker, epoch, candidate, "uat-replay-state")
		reviewFact := seedCleanImplementationReview(t, tracker, epoch, candidate, "uat-replay-round", "uat-replay-review")
		service := newHumanTestService(t, tracker)
		input := authorityImplementationUATInput(epoch, candidate, human.ID, "uat-replay")
		original, err := service.RecordImplementationUAT(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		originalConditions := authorityPreconditionsForOperation(t, tracker, candidate, input.Meta.OperationID)
		appendStartedReviewAuthority(t, tracker, epoch, candidate, "uat-replay-newer-round", "uat-replay-newer-review")
		if err := tracker.Close(); err != nil {
			t.Fatal(err)
		}
		tracker = openHumanTestTracker(t, db)
		defer tracker.Close()
		service = newHumanTestService(t, tracker)
		replayed, err := service.RecordImplementationUAT(context.Background(), input)
		if err != nil || !replayed.Replayed || !sameDecisionResultBindings(replayed, original) {
			t.Fatalf("reopened Implementation UAT replay = %+v, %v; want original binding", replayed, err)
		}
		if binding := authorityBindingForOperation(t, tracker, candidate, input.Meta.OperationID); binding.ReviewFact != reviewFact {
			t.Fatalf("replayed binding review fact = %d; want original %d", binding.ReviewFact, reviewFact)
		}
		if got := authorityPreconditionsForOperation(t, tracker, candidate, input.Meta.OperationID); !reflect.DeepEqual(got, originalConditions) || !reflect.DeepEqual(got, []conditionSnapshot{oracleEvidenceCondition(candidate, candidateEvidenceKind, stateFact, provenance.ConditionCurrentFact), oracleEvidenceCondition(candidate, implementationReviewAuthorityEvidenceKind, reviewFact, provenance.ConditionCurrentFact)}) {
			t.Fatalf("replayed Implementation UAT conditions = %+v; want original candidate/review identities", got)
		}
	})
}

func TestEpochHumanServiceAuthorityLand(t *testing.T) {
	for _, tc := range []struct {
		name         string
		publications func([]candidateMember) []repositoryPublication
	}{
		{name: "no publication set", publications: func([]candidateMember) []repositoryPublication { return nil }},
		{name: "missing member", publications: func(members []candidateMember) []repositoryPublication { return publicationsForMembers(members[:1]) }},
		{name: "extra member", publications: func(members []candidateMember) []repositoryPublication {
			return append(publicationsForMembers(members), repositoryPublication{Repository: "repo-extra", Candidate: "human-service--018f0000-0000-7000-8000-000000000099", Ref: "refs/heads/extra", Commit: members[0].Commit, VerificationOperation: "verify-extra"})
		}},
		{name: "commit mismatch", publications: func(members []candidateMember) []repositoryPublication {
			publications := publicationsForMembers(members)
			publications[0].Commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			return publications
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker, human, epoch, candidate, accepted, authority := seedAcceptedImplementationUAT(t, "land-"+tc.name)
			defer tracker.Close()
			authority.members = humanCandidateMembers(t, tracker, "land")
			authority.manifestFact = seedCandidateManifest(t, tracker, epoch, candidate, authority.members, "land-manifest")
			publications := tc.publications(authority.members)
			if len(publications) != 0 {
				authority.publicationFact = seedCandidatePublicationSet(t, tracker, epoch, candidate, authority.manifestFact, publications, 0, "land-publications")
			}
			service := newHumanTestService(t, tracker)
			input := LandInput{Meta: CommandMeta{OperationID: provenance.OperationID("land-reject-" + strings.ReplaceAll(tc.name, " ", "-"))}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}
			scope := humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{input.Meta.OperationID})
			before := humanDecisionStoreFootprint(t, tracker, scope)
			if _, err := service.Land(context.Background(), input); err == nil {
				t.Fatalf("Land accepted %s publication authority", tc.name)
			}
			after := humanDecisionStoreFootprint(t, tracker, scope)
			assertHumanDecisionStoreFootprintEqual(t, before, after)
			assertCommittedAbsent(t, after, input.Meta.OperationID)
		})
	}

	t.Run("complete ordered publications commit and stale publication loses", func(t *testing.T) {
		tracker, human, epoch, candidate, accepted, authority := seedAcceptedImplementationUAT(t, "land-complete")
		defer tracker.Close()
		authority.members = humanCandidateMembers(t, tracker, "land-complete")
		authority.manifestFact = seedCandidateManifest(t, tracker, epoch, candidate, authority.members, "land-complete-manifest")
		publications := publicationsForMembers(authority.members)
		authority.publicationFact = seedCandidatePublicationSet(t, tracker, epoch, candidate, authority.manifestFact, []repositoryPublication{publications[1], publications[0]}, 0, "land-complete-publications")
		publication := authorityPublicationForOperation(t, tracker, candidate, "land-complete-publications")
		if !reflect.DeepEqual(publication.Publications, publications) {
			t.Fatalf("publication set ordering = %+v; want canonical repository order %+v", publication.Publications, publications)
		}
		service := newHumanTestService(t, tracker)
		input := LandInput{Meta: CommandMeta{OperationID: "land-complete"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}
		result, err := service.Land(context.Background(), input)
		if err != nil {
			t.Fatalf("Land with complete publications: %v", err)
		}
		state := findEvidenceByOperationAndKind(t, humanDecisionStoreFootprint(t, tracker, humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{input.Meta.OperationID})), "uat-land-complete", candidateEvidenceKind)
		wantConditions := []conditionSnapshot{
			oracleEvidenceCondition(candidate, candidateEvidenceKind, state.JournalID, provenance.ConditionCurrentFact),
			oracleEvidenceCondition(candidate, implementationReviewAuthorityEvidenceKind, authority.reviewFact, provenance.ConditionCurrentFact),
			oracleEvidenceCondition(candidate, candidateManifestEvidenceKind, authority.manifestFact, provenance.ConditionExactFact),
			oracleEvidenceCondition(candidate, candidatePublicationSetEvidenceKind, authority.publicationFact, provenance.ConditionCurrentFact),
		}
		if got := authorityPreconditionsForOperation(t, tracker, epoch, input.Meta.OperationID); !reflect.DeepEqual(got, wantConditions) {
			t.Fatalf("Land conditions = %+v; want %+v", got, wantConditions)
		}
		if result.Replayed {
			t.Fatal("fresh Land result was marked replayed")
		}
	})

	t.Run("stale publication condition rejects after the barrier", func(t *testing.T) {
		tracker, human, epoch, candidate, accepted, authority := seedAcceptedImplementationUAT(t, "land-stale")
		defer tracker.Close()
		authority.members = humanCandidateMembers(t, tracker, "land-stale")
		authority.manifestFact = seedCandidateManifest(t, tracker, epoch, candidate, authority.members, "land-stale-manifest")
		publications := publicationsForMembers(authority.members)
		authority.publicationFact = seedCandidatePublicationSet(t, tracker, epoch, candidate, authority.manifestFact, publications, 0, "land-stale-publications")
		barrier := &callbackEpochBarrier{}
		barrier.after = func() error {
			seedCandidatePublicationSet(t, tracker, epoch, candidate, authority.manifestFact, publications, authority.publicationFact, "land-stale-publication-replaced")
			return nil
		}
		loser, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
		if err != nil {
			t.Fatal(err)
		}
		loserInput := LandInput{Meta: CommandMeta{OperationID: "land-stale-publication"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}
		_, err = loser.Land(context.Background(), loserInput)
		if err == nil || !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
			t.Fatalf("stale publication Land = %v, barrier calls=%d; want condition failure", err, barrier.calls)
		}
		after := humanDecisionStoreFootprint(t, tracker, humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{loserInput.Meta.OperationID}))
		assertCommittedAbsent(t, after, loserInput.Meta.OperationID)
		assertNoOperationPartials(t, after, loserInput.Meta.OperationID)
	})

	t.Run("reopen replay preserves original authority identities", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "pasture.db")
		tracker := openHumanTestTracker(t, db)
		human, err := tracker.RegisterHumanAgent("authority", "Authority Human", "")
		if err != nil {
			t.Fatal(err)
		}
		epoch := createHumanTestTask(t, tracker, "epoch")
		candidate := createHumanTestTask(t, tracker, "candidate")
		authority := humanCandidateAuthority{}
		authority.stateFact = seedCurrentCandidateLifecycle(t, tracker, epoch, candidate, "land-replay-state")
		authority.reviewFact = seedCleanImplementationReview(t, tracker, epoch, candidate, "land-replay-round", "land-replay-review")
		service := newHumanTestService(t, tracker)
		accepted, err := service.RecordImplementationUAT(context.Background(), authorityImplementationUATInput(epoch, candidate, human.ID, "land-replay-uat"))
		if err != nil {
			t.Fatal(err)
		}
		authority.members = humanCandidateMembers(t, tracker, "land-replay")
		authority.manifestFact = seedCandidateManifest(t, tracker, epoch, candidate, authority.members, "land-replay-manifest")
		publications := publicationsForMembers(authority.members)
		authority.publicationFact = seedCandidatePublicationSet(t, tracker, epoch, candidate, authority.manifestFact, publications, 0, "land-replay-publications")
		input := LandInput{Meta: CommandMeta{OperationID: "land-replay"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}
		original, err := service.Land(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		originalConditions := authorityPreconditionsForOperation(t, tracker, epoch, input.Meta.OperationID)
		appendStartedReviewAuthority(t, tracker, epoch, candidate, "land-replay-newer-review", "land-replay-newer-review-op")
		seedCandidatePublicationSet(t, tracker, epoch, candidate, authority.manifestFact, publications, authority.publicationFact, "land-replay-newer-publication")
		if err := tracker.Close(); err != nil {
			t.Fatal(err)
		}
		tracker = openHumanTestTracker(t, db)
		defer tracker.Close()
		service = newHumanTestService(t, tracker)
		replayed, err := service.Land(context.Background(), input)
		if err != nil || !replayed.Replayed || !sameDecisionResultBindings(replayed, original) {
			t.Fatalf("reopened Land replay = %+v, %v; want original binding", replayed, err)
		}
		if got := authorityPreconditionsForOperation(t, tracker, epoch, input.Meta.OperationID); !reflect.DeepEqual(got, originalConditions) {
			t.Fatalf("replayed Land conditions = %+v; want original %+v", got, originalConditions)
		}
	})
}

func authorityImplementationUATInput(epoch, candidate provenance.TaskID, actor provenance.ActorID, operation provenance.OperationID) ImplementationUATInput {
	return ImplementationUATInput{Meta: CommandMeta{OperationID: operation}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: actor}}
}

func seedAcceptedImplementationUAT(t *testing.T, prefix string) (*trackerImpl, provenance.HumanAgent, provenance.TaskID, provenance.TaskID, DecisionResult, humanCandidateAuthority) {
	t.Helper()
	prefix = strings.ReplaceAll(prefix, " ", "-")
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	human, err := tracker.RegisterHumanAgent("authority", "Authority Human", "")
	if err != nil {
		tracker.Close()
		t.Fatalf("register human: %v", err)
	}
	epoch := createHumanTestTask(t, tracker, prefix+"-epoch")
	candidate := createHumanTestTask(t, tracker, prefix+"-candidate")
	authority := humanCandidateAuthority{}
	authority.stateFact = seedCurrentCandidateLifecycle(t, tracker, epoch, candidate, provenance.OperationID(prefix+"-state"))
	authority.reviewFact = seedCleanImplementationReview(t, tracker, epoch, candidate, "round-"+ReviewRoundID(prefix), provenance.OperationID(prefix+"-review"))
	accepted, err := newHumanTestService(t, tracker).RecordImplementationUAT(context.Background(), authorityImplementationUATInput(epoch, candidate, human.ID, provenance.OperationID("uat-"+prefix)))
	if err != nil {
		tracker.Close()
		t.Fatalf("record accepted Implementation UAT: %v", err)
	}
	return tracker, human, epoch, candidate, accepted, authority
}

func appendStartedReviewAuthority(t *testing.T, tracker *trackerImpl, epoch, candidate provenance.TaskID, round ReviewRoundID, operation provenance.OperationID) {
	t.Helper()
	started, err := newReviewStartedAuthority(EpochRootID(epoch.String()), IntegrationCandidateSetID(candidate.String()), round, operation)
	if err != nil {
		t.Fatalf("construct later review authority: %v", err)
	}
	effect, err := newReviewAuthorityEvidenceEffect(candidate, started, "review-authority")
	if err != nil {
		t.Fatalf("construct later review effect: %v", err)
	}
	applyAuthorityTestEffect(t, tracker, operation, nil, effect)
}

func authorityBindingForOperation(t *testing.T, tracker *trackerImpl, candidate provenance.TaskID, operation provenance.OperationID) implementationUATReviewBinding {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}, OperationIDs: []provenance.OperationID{operation}}, Kinds: []provenance.EvidenceKind{implementationUATReviewBindingEvidenceKind}, Page: provenance.FactPageRequest{Limit: 2}})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("query UAT review binding for %q = %+v, %v", operation, page.Rows, err)
	}
	binding, err := decodeImplementationUATReviewBinding(page.Rows[0].Payload)
	if err != nil {
		t.Fatalf("decode UAT review binding: %v", err)
	}
	return binding
}

func authorityPublicationForOperation(t *testing.T, tracker *trackerImpl, candidate provenance.TaskID, operation provenance.OperationID) candidatePublicationSet {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: candidate}, OperationIDs: []provenance.OperationID{operation}}, Kinds: []provenance.EvidenceKind{candidatePublicationSetEvidenceKind}, Page: provenance.FactPageRequest{Limit: 2}})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("query publication set for %q = %+v, %v", operation, page.Rows, err)
	}
	publication, err := decodeCandidatePublicationSet(page.Rows[0].Payload)
	if err != nil {
		t.Fatalf("decode publication set: %v", err)
	}
	return publication
}

func authorityPreconditionsForOperation(t *testing.T, tracker *trackerImpl, subject provenance.TaskID, operation provenance.OperationID) []conditionSnapshot {
	t.Helper()
	page, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}, OperationIDs: []provenance.OperationID{operation}}, Kinds: []provenance.EvidenceKind{preconditionEvidenceKind}, Page: provenance.FactPageRequest{Limit: 2}})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("query precondition evidence for %q = %+v, %v", operation, page.Rows, err)
	}
	var value conditionEvidence
	if err := decodeAuthorityJSON(page.Rows[0].Payload, &value); err != nil {
		t.Fatalf("decode precondition evidence for %q: %v", operation, err)
	}
	return value.Conditions
}

func assertNoOperationPartials(t *testing.T, footprint normalizedHumanStoreFootprint, operation provenance.OperationID) {
	t.Helper()
	for _, decision := range footprint.Decisions {
		if decision.OperationID == operation {
			t.Fatalf("operation %q left decision partial: %+v", operation, decision)
		}
	}
	for _, evidence := range footprint.Evidence {
		if evidence.OperationID == operation {
			t.Fatalf("operation %q left evidence partial: %+v", operation, evidence)
		}
	}
	for _, event := range footprint.Events {
		if event.OperationID == operation {
			t.Fatalf("operation %q left task-event partial: %+v", operation, event)
		}
	}
	for _, activity := range footprint.Activities {
		if activity.ID == oracleActivityID(operation) {
			t.Fatalf("operation %q left activity partial: %+v", operation, activity)
		}
	}
}
