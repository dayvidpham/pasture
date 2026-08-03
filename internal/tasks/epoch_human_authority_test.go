package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
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

	t.Run("changed existing epoch reaches Apply conflict without writes", func(t *testing.T) {
		tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
		defer tracker.Close()
		human, err := tracker.RegisterHumanAgent("authority", "Authority Human", "")
		if err != nil {
			t.Fatal(err)
		}
		epochA := createHumanTestTask(t, tracker, "epoch-a")
		epochB := createHumanTestTask(t, tracker, "epoch-b")
		candidate := createHumanTestTask(t, tracker, "candidate")
		seedCurrentCandidateLifecycle(t, tracker, epochA, candidate, "uat-conflict-state")
		seedCleanImplementationReview(t, tracker, epochA, candidate, "uat-conflict-round", "uat-conflict-review")
		service := newHumanTestService(t, tracker)
		input := authorityImplementationUATInput(epochA, candidate, human.ID, "uat-changed-epoch")
		if _, err := service.RecordImplementationUAT(context.Background(), input); err != nil {
			t.Fatalf("record original Implementation UAT: %v", err)
		}
		scope := humanStoreScopeFor([]provenance.TaskID{epochA, epochB, candidate}, []provenance.OperationID{input.Meta.OperationID})
		before := humanDecisionStoreFootprint(t, tracker, scope)
		changed := input
		changed.Epoch = EpochRootID(epochB.String())
		if _, err := service.RecordImplementationUAT(context.Background(), changed); !errors.Is(err, provenance.ErrOperationConflict) {
			t.Fatalf("changed existing epoch Implementation UAT = %v; want provenance operation conflict", err)
		}
		after := humanDecisionStoreFootprint(t, tracker, scope)
		assertHumanDecisionStoreFootprintEqual(t, before, after)
		assertCommittedOperationKind(t, after, input.Meta.OperationID, provenance.CommittedExact)
	})
}

func TestEpochHumanServiceAuthorityRatify(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	human, err := tracker.RegisterHumanAgent("authority", "Authority Human", "")
	if err != nil {
		t.Fatal(err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	proposal := createHumanTestTask(t, tracker, "proposal")
	winner := newHumanTestService(t, tracker)
	accepted, err := winner.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "ratify-current-accepted"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("record accepted Plan UAT: %v", err)
	}
	seedAcceptedReview(t, tracker, epoch, proposal, "ratify-current-review")
	later := PlanUATInput{Meta: CommandMeta{OperationID: "ratify-current-later"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATChangesRequested, Payload: &PlanUATPayload{Feedback: []UATFeedbackItem{{ID: "revise", Body: "revise"}}}, Actor: AssertedHumanActor{ActorID: human.ID}}
	barrier := &callbackEpochBarrier{}
	barrier.after = func() error {
		_, err := winner.RecordPlanUAT(context.Background(), later)
		return err
	}
	loser, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
	if err != nil {
		t.Fatal(err)
	}
	loserOperation := provenance.OperationID("ratify-current-loser")
	scope := humanStoreScopeFor([]provenance.TaskID{epoch, proposal}, []provenance.OperationID{accepted.OperationID, later.Meta.OperationID, loserOperation})
	before := humanDecisionStoreFootprint(t, tracker, scope)
	_, err = loser.RatifyPlan(context.Background(), RatifyPlanInput{Meta: CommandMeta{OperationID: loserOperation}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "ratify-current-review", PlanUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
	if !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
		t.Fatalf("Ratify after a later Plan UAT = %v, barrier calls=%d; want condition failure after one barrier", err, barrier.calls)
	}
	after := humanDecisionStoreFootprint(t, tracker, scope)
	assertCommittedOperationKind(t, after, later.Meta.OperationID, provenance.CommittedExact)
	assertCommittedAbsent(t, after, loserOperation)
	assertNoOperationPartials(t, after, loserOperation)
	if after.Statuses[proposal.String()] != provenance.StatusOpen || !reflect.DeepEqual(after.Statuses, before.Statuses) {
		t.Fatalf("Ratify loser changed proposal status: before=%+v after=%+v", before.Statuses, after.Statuses)
	}
	if len(after.Decisions) != len(before.Decisions)+1 || len(after.Evidence) != len(before.Evidence)+2 || len(after.Events) != len(before.Events)+1 || len(after.Activities) != len(before.Activities)+1 {
		t.Fatalf("post-barrier writes = decisions %d->%d evidence %d->%d events %d->%d activities %d->%d; want only the later Plan UAT", len(before.Decisions), len(after.Decisions), len(before.Evidence), len(after.Evidence), len(before.Events), len(after.Events), len(before.Activities), len(after.Activities))
	}
	for _, decision := range after.Decisions[len(before.Decisions):] {
		if decision.OperationID != later.Meta.OperationID {
			t.Fatalf("unexpected post-barrier decision: %+v", decision)
		}
	}
	for _, evidence := range after.Evidence[len(before.Evidence):] {
		if evidence.OperationID != later.Meta.OperationID {
			t.Fatalf("unexpected post-barrier evidence: %+v", evidence)
		}
	}
	for _, event := range after.Events[len(before.Events):] {
		if event.OperationID != later.Meta.OperationID {
			t.Fatalf("unexpected post-barrier event: %+v", event)
		}
	}
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
		scope := humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{"uat-land-complete", input.Meta.OperationID})
		footprint := humanDecisionStoreFootprint(t, tracker, scope)
		state := findEvidenceByOperationAndKind(t, footprint, "uat-land-complete", candidateEvidenceKind)
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
		wantBinding := landAuthorityBinding{
			Epoch:                              EpochRootID(epoch.String()),
			Candidate:                          IntegrationCandidateSetID(candidate.String()),
			LandOperation:                      input.Meta.OperationID,
			ImplementationUAT:                  accepted.DecisionID,
			ImplementationUATOperation:         "uat-land-complete",
			ImplementationUATDecisionFact:      findDecisionByOperation(t, footprint, "uat-land-complete").JournalID,
			ImplementationUATStateFact:         state.JournalID,
			ImplementationUATReviewBindingFact: findEvidenceByOperationAndKind(t, footprint, "uat-land-complete", implementationUATReviewBindingEvidenceKind).JournalID,
			ReviewFact:                         authority.reviewFact,
			ReviewRound:                        "round-land-complete",
			ReviewAxes: [3]reviewAxisAuthority{
				{Axis: AxisCorrectness, Event: 101, Verdict: VerdictAccept},
				{Axis: AxisTestQuality, Event: 102, Verdict: VerdictAccept},
				{Axis: AxisElegance, Event: 103, Verdict: VerdictAccept},
			},
			ManifestFact:       authority.manifestFact,
			Members:            authority.members,
			PublicationSetFact: authority.publicationFact,
			Publications:       publications,
		}
		binding := assertLandAuthorityBindingEvidence(t, footprint, input.Meta.OperationID, wantBinding)
		immediate, err := service.Land(context.Background(), input)
		if err != nil || !immediate.Replayed || !sameDecisionResultBindings(immediate, result) {
			t.Fatalf("immediate Land replay = %+v, %v; want original binding", immediate, err)
		}
		afterImmediate := humanDecisionStoreFootprint(t, tracker, scope)
		assertHumanDecisionStoreFootprintEqual(t, footprint, afterImmediate)
		if got := assertLandAuthorityBindingEvidence(t, afterImmediate, input.Meta.OperationID, wantBinding); !reflect.DeepEqual(got, binding) {
			t.Fatalf("immediate Land binding changed: got %+v; want %+v", got, binding)
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
		originalScope := humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{"land-replay-uat", input.Meta.OperationID})
		originalFootprint := humanDecisionStoreFootprint(t, tracker, originalScope)
		wantBinding := landAuthorityBinding{
			Epoch:                              EpochRootID(epoch.String()),
			Candidate:                          IntegrationCandidateSetID(candidate.String()),
			LandOperation:                      input.Meta.OperationID,
			ImplementationUAT:                  accepted.DecisionID,
			ImplementationUATOperation:         "land-replay-uat",
			ImplementationUATDecisionFact:      findDecisionByOperation(t, originalFootprint, "land-replay-uat").JournalID,
			ImplementationUATStateFact:         findEvidenceByOperationAndKind(t, originalFootprint, "land-replay-uat", candidateEvidenceKind).JournalID,
			ImplementationUATReviewBindingFact: findEvidenceByOperationAndKind(t, originalFootprint, "land-replay-uat", implementationUATReviewBindingEvidenceKind).JournalID,
			ReviewFact:                         authority.reviewFact,
			ReviewRound:                        "land-replay-round",
			ReviewAxes: [3]reviewAxisAuthority{
				{Axis: AxisCorrectness, Event: 101, Verdict: VerdictAccept},
				{Axis: AxisTestQuality, Event: 102, Verdict: VerdictAccept},
				{Axis: AxisElegance, Event: 103, Verdict: VerdictAccept},
			},
			ManifestFact:       authority.manifestFact,
			Members:            authority.members,
			PublicationSetFact: authority.publicationFact,
			Publications:       publications,
		}
		originalBinding := assertLandAuthorityBindingEvidence(t, originalFootprint, input.Meta.OperationID, wantBinding)
		other, err := tracker.RegisterHumanAgent("authority-retry", "Changed Retry Human", "")
		if err != nil {
			t.Fatal(err)
		}
		changed := input
		changed.Actor = AssertedHumanActor{ActorID: other.ID}
		beforeConflict := humanDecisionStoreFootprint(t, tracker, originalScope)
		if _, err := service.Land(context.Background(), changed); !errors.Is(err, provenance.ErrOperationConflict) {
			t.Fatalf("changed Land retry = %v; want provenance operation conflict", err)
		}
		assertHumanDecisionStoreFootprintEqual(t, beforeConflict, humanDecisionStoreFootprint(t, tracker, originalScope))
		originalConditions := authorityPreconditionsForOperation(t, tracker, epoch, input.Meta.OperationID)
		appendStartedReviewAuthority(t, tracker, epoch, candidate, "land-replay-newer-review", "land-replay-newer-review-op")
		seedCandidatePublicationSet(t, tracker, epoch, candidate, authority.manifestFact, publications, authority.publicationFact, "land-replay-newer-publication")
		if err := tracker.Close(); err != nil {
			t.Fatal(err)
		}
		tracker = openHumanTestTracker(t, db)
		defer tracker.Close()
		service = newHumanTestService(t, tracker)
		beforeReplay := humanDecisionStoreFootprint(t, tracker, originalScope)
		replayed, err := service.Land(context.Background(), input)
		if err != nil || !replayed.Replayed || !sameDecisionResultBindings(replayed, original) {
			t.Fatalf("reopened Land replay = %+v, %v; want original binding", replayed, err)
		}
		afterReplay := humanDecisionStoreFootprint(t, tracker, originalScope)
		assertHumanDecisionStoreFootprintEqual(t, beforeReplay, afterReplay)
		if got := assertLandAuthorityBindingEvidence(t, afterReplay, input.Meta.OperationID, wantBinding); !reflect.DeepEqual(got, originalBinding) {
			t.Fatalf("reopened Land binding changed: got %+v; want %+v", got, originalBinding)
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

func assertLandAuthorityBindingEvidence(t *testing.T, footprint normalizedHumanStoreFootprint, operation provenance.OperationID, want landAuthorityBinding) normalizedEvidenceFact {
	t.Helper()
	got := findEvidenceByOperationAndKind(t, footprint, operation, landAuthorityBindingEvidenceKind)
	decoded, err := decodeLandAuthorityBinding(got.Payload)
	if err != nil {
		t.Fatalf("decode Land authority binding for %q: %v", operation, err)
	}
	canonical, err := newLandAuthorityBinding(want)
	if err != nil {
		t.Fatalf("construct expected Land authority binding for %q: %v", operation, err)
	}
	if !reflect.DeepEqual(decoded, canonical) {
		t.Fatalf("Land authority binding for %q = %+v; want %+v", operation, decoded, canonical)
	}
	wantPayload, err := canonicalJSON(canonical)
	if err != nil {
		t.Fatalf("encode expected Land authority binding for %q: %v", operation, err)
	}
	digest := sha256.Sum256(wantPayload)
	if !bytes.Equal(got.Payload, wantPayload) || !bytes.Equal(got.Digest, digest[:]) {
		t.Fatalf("Land authority binding bytes/digest for %q = %q/%x; want %q/%x", operation, got.Payload, got.Digest, wantPayload, digest)
	}
	expected, ok := footprint.ExpectedOperations[string(operation)]
	if !ok || expected.Kind != provenance.CommittedExact || expected.AnchorJournalID == 0 || !got.HasProducingOperationID || got.ProducingOperationID != expected.AnchorJournalID {
		t.Fatalf("Land authority binding producer for %q = %+v; want independently captured nonzero anchor %+v", operation, got, expected)
	}
	committed, ok := footprint.Operations[string(operation)]
	if !ok || !reflect.DeepEqual(committed, expected) {
		t.Fatalf("Land committed operation for %q = %+v; want independently captured %+v", operation, committed, expected)
	}
	count := 0
	for _, slot := range committed.ResultSlots {
		if slot.Slot != "evidence-2" {
			continue
		}
		count++
		if slot.Kind != provenance.JournalKindEvidence || slot.ProducedJournalID != got.JournalID || slot.HasTaskID || slot.HasActivityID {
			t.Fatalf("Land evidence-2 result slot = %+v; want binding journal %d", slot, got.JournalID)
		}
	}
	if count != 1 {
		t.Fatalf("Land operation %q has %d evidence-2 result slots; want one", operation, count)
	}
	return got
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
