package tasks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

type rejectingEpochBarrier struct{ calls int }

func (b *rejectingEpochBarrier) AfterPreflight(context.Context, EpochMutationKind) error {
	b.calls++
	return errors.New("held by test barrier")
}

type callbackEpochBarrier struct {
	calls int
	after func() error
}

func (b *callbackEpochBarrier) AfterPreflight(context.Context, EpochMutationKind) error {
	b.calls++
	if b.calls != 1 || b.after == nil {
		return nil
	}
	return b.after()
}

func TestEpochHumanServiceProductionFlowAndReopen(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "pasture.db")
	tracker := openHumanTestTracker(t, db)
	defer tracker.Close()
	human, err := tracker.RegisterHumanAgent("test", "Selected Human", "human@example.test")
	if err != nil {
		t.Fatalf("register human: %v", err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	proposal := createHumanTestTask(t, tracker, "proposal")
	candidate := createHumanTestTask(t, tracker, "candidate")
	candidateAuthority := humanCandidateAuthority{}
	candidateAuthority.stateFact = seedCurrentCandidateLifecycle(t, tracker, epoch, candidate, "flow-candidate-state")
	candidateAuthority.reviewFact = seedCleanImplementationReview(t, tracker, epoch, candidate, "candidate-review-1", "flow-review-finalized")
	candidateAuthority.members = humanCandidateMembers(t, tracker, "flow")
	candidateAuthority.manifestFact = seedCandidateManifest(t, tracker, epoch, candidate, candidateAuthority.members, "flow-candidate-manifest")
	candidateAuthority.publicationFact = seedCandidatePublicationSet(t, tracker, epoch, candidate, candidateAuthority.manifestFact, publicationsForMembers(candidateAuthority.members), 0, "flow-publication-set")
	service := newHumanTestService(t, tracker)

	mode, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("set interaction mode: %v", err)
	}
	if mode.Replayed || mode.ActorID != human.ID || mode.DecisionID == "" || mode.ActivityID == (provenance.ActivityID{}) || len(mode.EventIDs) != 1 {
		t.Fatalf("mode result missing exact material bindings: %+v", mode)
	}
	modeScope := humanStoreScopeFor([]provenance.TaskID{epoch}, []provenance.OperationID{"human-mode-1"})
	modeBeforeRetry := humanDecisionStoreFootprint(t, tracker, modeScope)
	modeExpectation := humanDecisionExpectation{
		operation: mode.OperationID, actor: human.ID, epoch: epoch, subject: epoch, phase: provenance.PhaseRequest,
		kind: DecisionInteractionModeChanged, note: "explicit interaction-mode decision",
		branch:     oracleModeBranch{From: InteractionNormal, To: InteractionAFK},
		conditions: []conditionSnapshot{oracleDecisionCondition(epoch, DecisionInteractionModeChanged, 0)},
		evidence:   []oracleEvidenceExpectation{{kind: "pasture.epoch.subject.v1", task: epoch, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: epoch.String(), Decision: string(oracleDecisionID(mode.OperationID)), Reference: epoch.String()}}},
		statuses:   map[string]provenance.Status{epoch.String(): provenance.StatusOpen},
	}
	assertHumanDecisionOracleExact(t, tracker, modeBeforeRetry, modeExpectation, mode, false)
	replayed, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil || !replayed.Replayed || !sameDecisionResultBindings(replayed, mode) {
		t.Fatalf("exact mode replay = %+v, %v; want original bindings", replayed, err)
	}
	modeAfterRetry := humanDecisionStoreFootprint(t, tracker, modeScope)
	assertHumanDecisionStoreFootprintEqual(t, modeBeforeRetry, modeAfterRetry)
	assertHumanDecisionOracleExact(t, tracker, modeAfterRetry, modeExpectation, replayed, true)
	modeBeforeChangedConflict := humanDecisionStoreFootprint(t, tracker, modeScope)
	if _, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionNormal, Actor: AssertedHumanActor{ActorID: human.ID}}); !errors.Is(err, provenance.ErrOperationConflict) {
		t.Fatalf("changed mode under same operation id error = %v, want operation conflict", err)
	}
	assertHumanDecisionStoreFootprintEqual(t, modeBeforeChangedConflict, humanDecisionStoreFootprint(t, tracker, modeScope))

	mode1Decision := findDecisionByOperation(t, modeBeforeRetry, mode.OperationID)
	mode2Input := SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-2"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionNormal, Actor: AssertedHumanActor{ActorID: human.ID}}
	mode2, err := service.SetInteractionMode(ctx, mode2Input)
	if err != nil {
		t.Fatalf("set interaction mode back to normal: %v", err)
	}
	mode2Scope := humanStoreScopeFor([]provenance.TaskID{epoch}, []provenance.OperationID{"human-mode-1", "human-mode-2"})
	mode2BeforeRetry := humanDecisionStoreFootprint(t, tracker, mode2Scope)
	mode2Expectation := humanDecisionExpectation{
		operation: mode2.OperationID, actor: human.ID, epoch: epoch, subject: epoch, phase: provenance.PhaseRequest,
		kind: DecisionInteractionModeChanged, note: "explicit interaction-mode decision",
		branch:     oracleModeBranch{From: InteractionAFK, To: InteractionNormal},
		conditions: []conditionSnapshot{oracleDecisionCondition(epoch, DecisionInteractionModeChanged, mode1Decision.JournalID)},
		evidence:   []oracleEvidenceExpectation{{kind: "pasture.epoch.subject.v1", task: epoch, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: epoch.String(), Decision: string(oracleDecisionID(mode2.OperationID)), Reference: epoch.String()}}},
		statuses:   map[string]provenance.Status{epoch.String(): provenance.StatusOpen},
	}
	assertHumanDecisionOracleExact(t, tracker, mode2BeforeRetry, mode2Expectation, mode2, false)
	mode2ImmediateReplay, err := service.SetInteractionMode(ctx, mode2Input)
	if err != nil || !mode2ImmediateReplay.Replayed || !sameDecisionResultBindings(mode2ImmediateReplay, mode2) {
		t.Fatalf("immediate AFK-to-normal replay = %+v, %v; want original bindings", mode2ImmediateReplay, err)
	}
	mode2AfterImmediateReplay := humanDecisionStoreFootprint(t, tracker, mode2Scope)
	assertHumanDecisionStoreFootprintEqual(t, mode2BeforeRetry, mode2AfterImmediateReplay)
	assertHumanDecisionOracleExact(t, tracker, mode2AfterImmediateReplay, mode2Expectation, mode2ImmediateReplay, true)

	if err := tracker.Close(); err != nil {
		t.Fatalf("close before reopen: %v", err)
	}
	tracker = openHumanTestTracker(t, db)
	defer tracker.Close()
	service = newHumanTestService(t, tracker)
	cursor, err := service.ShowInteractionMode(ctx, EpochRootID(epoch.String()))
	if err != nil || cursor.Mode != InteractionNormal || cursor.Entry == nil || *cursor.Entry != mode2.DecisionID {
		t.Fatalf("reopened mode cursor = %+v, %v", cursor, err)
	}
	modeBeforeReopenReplay := humanDecisionStoreFootprint(t, tracker, mode2Scope)
	reopenedReplay, err := service.SetInteractionMode(ctx, mode2Input)
	if err != nil || !reopenedReplay.Replayed || !sameDecisionResultBindings(reopenedReplay, mode2) {
		t.Fatalf("reopened AFK-to-normal direct Apply replay = %+v, %v; want original bindings", reopenedReplay, err)
	}
	modeAfterReopenReplay := humanDecisionStoreFootprint(t, tracker, mode2Scope)
	assertHumanDecisionStoreFootprintEqual(t, modeBeforeReopenReplay, modeAfterReopenReplay)
	assertHumanDecisionOracleExact(t, tracker, modeAfterReopenReplay, mode2Expectation, reopenedReplay, true)

	planPayload := &PlanUATPayload{
		Interactions: []UATInteraction{{Prompt: "Does the proposal preserve the audit trail?", Response: "Yes, the decision and its evidence remain replayable."}},
		Feedback:     []UATFeedbackItem{{ID: "plan-feedback-1", Body: "Retain the exact selected actor in the audit view.", FixNow: false}},
	}
	planInput := PlanUATInput{Meta: CommandMeta{OperationID: "human-plan-uat-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}, Payload: planPayload}
	planUAT, err := service.RecordPlanUAT(ctx, planInput)
	if err != nil {
		t.Fatalf("record Plan UAT: %v", err)
	}
	seedAcceptedReview(t, tracker, epoch, proposal, "review-round-1")

	ratifyInput := RatifyPlanInput{Meta: CommandMeta{OperationID: "human-ratify-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "review-round-1", PlanUAT: planUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}
	ratified, err := service.RatifyPlan(ctx, ratifyInput)
	if err != nil {
		t.Fatalf("ratify plan: %v", err)
	}

	implPayload := &ImplUATPayload{
		Interactions: []UATInteraction{{Prompt: "Does the implementation replay without a delta?", Response: "Yes, immediate and reopened retries return the same bindings."}},
		Feedback:     []UATFeedbackItem{{ID: "impl-feedback-1", Body: "Keep the raw producer anchor visible to the oracle.", FixNow: false}},
		PlanFeedback: []DeferredFeedbackResolution{{Target: "plan-feedback-1", Kind: ResolutionConfirm, Note: "Validated and carried forward unchanged."}},
	}
	implInput := ImplementationUATInput{Meta: CommandMeta{OperationID: "human-impl-uat-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}, Payload: implPayload}
	implUAT, err := service.RecordImplementationUAT(ctx, implInput)
	if err != nil {
		t.Fatalf("record Implementation UAT: %v", err)
	}

	landInput := LandInput{Meta: CommandMeta{OperationID: "human-land-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: implUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}
	landed, err := service.Land(ctx, landInput)
	if err != nil {
		t.Fatalf("land: %v", err)
	}

	allScope := humanStoreScopeFor(
		[]provenance.TaskID{epoch, proposal, candidate},
		[]provenance.OperationID{"human-mode-1", "human-plan-uat-1", "human-ratify-1", "human-impl-uat-1", "human-land-1"},
	)
	planState := findEvidenceByOperationAndKind(t, humanDecisionStoreFootprint(t, tracker, allScope), planInput.Meta.OperationID, planSubjectEvidenceKind)
	implState := findEvidenceByOperationAndKind(t, humanDecisionStoreFootprint(t, tracker, allScope), implInput.Meta.OperationID, candidateEvidenceKind)
	finalStatuses := map[string]provenance.Status{epoch.String(): provenance.StatusClosed, proposal.String(): provenance.StatusClosed, candidate.String(): provenance.StatusOpen}
	nonModeCases := []struct {
		name        string
		expectation humanDecisionExpectation
		run         func() (DecisionResult, error)
		retry       func() (DecisionResult, error)
	}{
		{
			name: "plan-uat", expectation: humanDecisionExpectation{
				operation: planInput.Meta.OperationID, actor: human.ID, epoch: epoch, subject: proposal, phase: provenance.PhasePlanUAT,
				kind: DecisionPlanUATAccepted, note: "explicit Plan UAT decision", branch: oraclePlanAcceptedBranchFromInput(planInput, epoch),
				conditions: []conditionSnapshot{oracleEvidenceCondition(proposal, planSubjectEvidenceKind, 0, provenance.ConditionCurrentFact)},
				evidence:   []oracleEvidenceExpectation{{kind: planSubjectEvidenceKind, task: proposal, state: &oracleStateEvidence{Epoch: epoch.String(), Subject: proposal.String(), State: string(subjectStatePlanAccepted), Decision: oracleDecisionID(planInput.Meta.OperationID), DecisionKind: DecisionPlanUATAccepted, Operation: planInput.Meta.OperationID}}},
				statuses:   finalStatuses,
			}, run: func() (DecisionResult, error) { return planUAT, nil },
			retry: func() (DecisionResult, error) { return service.RecordPlanUAT(ctx, planInput) },
		},
		{
			name: "ratify", expectation: humanDecisionExpectation{
				operation: ratifyInput.Meta.OperationID, actor: human.ID, epoch: epoch, subject: proposal, phase: provenance.PhaseRatify,
				kind: DecisionPlanRatified, note: "explicit plan-ratification decision", branch: oraclePlanRatifiedBranch{Proposal: proposal.String(), ReviewRound: ratifyInput.ReviewRound, PlanUAT: planUAT.DecisionID},
				conditions: []conditionSnapshot{oracleEvidenceCondition(proposal, planSubjectEvidenceKind, planState.JournalID, provenance.ConditionExactFact)},
				evidence: []oracleEvidenceExpectation{
					{kind: planSubjectEvidenceKind, task: proposal, state: &oracleStateEvidence{Epoch: epoch.String(), Subject: proposal.String(), State: string(subjectStatePlanRatified), Decision: oracleDecisionID(ratifyInput.Meta.OperationID), DecisionKind: DecisionPlanRatified, Operation: ratifyInput.Meta.OperationID}},
					{kind: "pasture.review.round.v1", task: proposal, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: proposal.String(), Decision: string(oracleDecisionID(ratifyInput.Meta.OperationID)), Reference: string(ratifyInput.ReviewRound)}},
					{kind: "pasture.plan-uat.decision.v1", task: proposal, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: proposal.String(), Decision: string(oracleDecisionID(ratifyInput.Meta.OperationID)), Reference: string(ratifyInput.PlanUAT)}},
				},
				closeTask: proposal, statuses: finalStatuses,
			}, run: func() (DecisionResult, error) { return ratified, nil },
			retry: func() (DecisionResult, error) { return service.RatifyPlan(ctx, ratifyInput) },
		},
		{
			name: "implementation-uat", expectation: humanDecisionExpectation{
				operation: implInput.Meta.OperationID, actor: human.ID, epoch: epoch, subject: candidate, phase: provenance.PhaseImplUAT,
				kind: DecisionImplementationUAT, note: "explicit Implementation UAT decision", branch: oracleImplementationUATBranch{Outcome: ImplUATAccepted, Payload: *implPayload},
				conditions: []conditionSnapshot{oracleEvidenceCondition(candidate, candidateEvidenceKind, candidateAuthority.stateFact, provenance.ConditionCurrentFact), oracleEvidenceCondition(candidate, implementationReviewAuthorityEvidenceKind, candidateAuthority.reviewFact, provenance.ConditionCurrentFact)},
				evidence: []oracleEvidenceExpectation{
					{kind: candidateEvidenceKind, task: candidate, state: &oracleStateEvidence{Epoch: epoch.String(), Subject: candidate.String(), State: string(subjectStateImplementationAccepted), Decision: oracleDecisionID(implInput.Meta.OperationID), DecisionKind: DecisionImplementationUAT, Operation: implInput.Meta.OperationID}},
					{kind: implementationUATReviewBindingEvidenceKind, task: candidate, value: implementationUATReviewBinding{Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ReviewRound: "candidate-review-1", ReviewFact: candidateAuthority.reviewFact, Operation: implInput.Meta.OperationID}},
				},
				statuses: finalStatuses,
			}, run: func() (DecisionResult, error) { return implUAT, nil },
			retry: func() (DecisionResult, error) { return service.RecordImplementationUAT(ctx, implInput) },
		},
		{
			name: "land", expectation: humanDecisionExpectation{
				operation: landInput.Meta.OperationID, actor: human.ID, epoch: epoch, subject: epoch, phase: provenance.PhaseLanding,
				kind: DecisionLanded, note: "explicit landing decision", branch: oracleLandedBranch{Candidate: landInput.Candidate, ImplementationUAT: implUAT.DecisionID},
				conditions: []conditionSnapshot{oracleEvidenceCondition(candidate, candidateEvidenceKind, implState.JournalID, provenance.ConditionCurrentFact), oracleEvidenceCondition(candidate, implementationReviewAuthorityEvidenceKind, candidateAuthority.reviewFact, provenance.ConditionCurrentFact), oracleEvidenceCondition(candidate, candidateManifestEvidenceKind, candidateAuthority.manifestFact, provenance.ConditionExactFact), oracleEvidenceCondition(candidate, candidatePublicationSetEvidenceKind, candidateAuthority.publicationFact, provenance.ConditionCurrentFact)},
				evidence: []oracleEvidenceExpectation{
					{kind: candidateEvidenceKind, task: candidate, state: &oracleStateEvidence{Epoch: epoch.String(), Subject: candidate.String(), State: string(subjectStateImplementationLanded), Decision: oracleDecisionID(landInput.Meta.OperationID), DecisionKind: DecisionLanded, Operation: landInput.Meta.OperationID}},
					{kind: "pasture.implementation-uat.decision.v1", task: epoch, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: epoch.String(), Decision: string(oracleDecisionID(landInput.Meta.OperationID)), Reference: string(landInput.ImplementationUAT)}},
				},
				closeTask: epoch, statuses: finalStatuses,
			}, run: func() (DecisionResult, error) { return landed, nil },
			retry: func() (DecisionResult, error) { return service.Land(ctx, landInput) },
		},
	}
	for _, tc := range nonModeCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.run()
			if err != nil {
				t.Fatal(err)
			}
			before := humanDecisionStoreFootprint(t, tracker, allScope)
			assertHumanDecisionOracleExact(t, tracker, before, tc.expectation, result, false)
			retry, err := tc.retry()
			if err != nil || !retry.Replayed || !sameDecisionResultBindings(retry, result) {
				t.Fatalf("exact %s retry = %+v, %v; want identical bindings", tc.name, retry, err)
			}
			after := humanDecisionStoreFootprint(t, tracker, allScope)
			assertHumanDecisionStoreFootprintEqual(t, before, after)
			assertHumanDecisionOracleExact(t, tracker, after, tc.expectation, retry, true)
		})
	}

	beforeReopen := humanDecisionStoreFootprint(t, tracker, allScope)
	if err := tracker.Close(); err != nil {
		t.Fatalf("close after landing: %v", err)
	}
	tracker = openHumanTestTracker(t, db)
	service = newHumanTestService(t, tracker)
	afterOpen := humanDecisionStoreFootprint(t, tracker, allScope)
	assertHumanDecisionStoreFootprintEqual(t, beforeReopen, afterOpen)
	for _, tc := range []struct {
		name  string
		want  DecisionResult
		retry func() (DecisionResult, error)
	}{
		{name: "ratify", want: ratified, retry: func() (DecisionResult, error) { return service.RatifyPlan(ctx, ratifyInput) }},
		{name: "land", want: landed, retry: func() (DecisionResult, error) { return service.Land(ctx, landInput) }},
	} {
		t.Run("reopen-"+tc.name, func(t *testing.T) {
			replayed, err := tc.retry()
			if err != nil || !replayed.Replayed || !sameDecisionResultBindings(replayed, tc.want) {
				t.Fatalf("%s replay after reopen = %+v, %v; want original bindings", tc.name, replayed, err)
			}
			afterReplay := humanDecisionStoreFootprint(t, tracker, allScope)
			assertHumanDecisionStoreFootprintEqual(t, beforeReopen, afterReplay)
			var expectation humanDecisionExpectation
			for _, nonMode := range nonModeCases {
				if nonMode.name == tc.name {
					expectation = nonMode.expectation
					break
				}
			}
			assertHumanDecisionOracleExact(t, tracker, afterReplay, expectation, replayed, true)
		})
	}
}

func TestEpochHumanServiceRejectsNonHumanOrMissingActorBeforeWrite(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	epoch := createHumanTestTask(t, tracker, "epoch")
	service := newHumanTestService(t, tracker)
	models := provenance.DefaultModelRegistry().Models()
	if len(models) == 0 {
		t.Fatal("default model registry is empty")
	}
	ml, err := tracker.RegisterMLAgent("human-service-ml", provenance.RoleWorker, models[0].Provider, models[0].Name)
	if err != nil {
		t.Fatalf("register ML actor: %v", err)
	}
	software, err := tracker.RegisterSoftwareAgent("human-service-software", "test-software", "1", "test")
	if err != nil {
		t.Fatalf("register software actor: %v", err)
	}
	system, _, found, err := readSystemIdentity(tracker.auditDB)
	if err != nil || !found {
		t.Fatalf("read reserved system actor: found=%t err=%v", found, err)
	}
	unknown := provenance.ActorID{Namespace: "human-service-unknown", UUID: uuid.Must(uuid.NewV7())}
	cases := []struct {
		name  string
		actor provenance.ActorID
	}{
		{name: "missing"},
		{name: "unknown", actor: unknown},
		{name: "machine-learning", actor: ml.ID},
		{name: "software", actor: software.ID},
		{name: "reserved-system", actor: system},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operation := provenance.OperationID("rejected-" + tc.name)
			before := humanDecisionStoreFootprint(t, tracker, humanStoreScopeFor([]provenance.TaskID{epoch}, []provenance.OperationID{operation}))
			_, err := service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: provenance.OperationID("rejected-" + tc.name)}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: tc.actor}})
			if err == nil {
				t.Fatalf("%s actor was accepted", tc.name)
			}
			after := humanDecisionStoreFootprint(t, tracker, humanStoreScopeFor([]provenance.TaskID{epoch}, []provenance.OperationID{operation}))
			assertHumanDecisionStoreFootprintEqual(t, before, after)
		})
	}
}

func TestEpochHumanServiceBarrierRunsAfterPreflightBeforeApply(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	human, err := tracker.RegisterHumanAgent("test", "Barrier Human", "")
	if err != nil {
		t.Fatal(err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	barrier := &rejectingEpochBarrier{}
	service, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
	if err != nil {
		t.Fatal(err)
	}
	scope := humanStoreScopeFor([]provenance.TaskID{epoch}, []provenance.OperationID{"barrier-mode"})
	before := humanDecisionStoreFootprint(t, tracker, scope)
	_, err = service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "barrier-mode"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err == nil || barrier.calls != 1 {
		t.Fatalf("barrier result err=%v calls=%d, want rejection after one call", err, barrier.calls)
	}
	assertHumanDecisionStoreFootprintEqual(t, before, humanDecisionStoreFootprint(t, tracker, scope))
}

func TestEpochHumanServiceRejectsStaleModeConditionAfterBarrier(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	human, err := tracker.RegisterHumanAgent("test", "Race Human", "")
	if err != nil {
		t.Fatal(err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	winner := newHumanTestService(t, tracker)
	barrier := &callbackEpochBarrier{}
	var winnerResult DecisionResult
	barrier.after = func() error {
		var err error
		winnerResult, err = winner.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "mode-winner"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
		return err
	}
	loser, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
	if err != nil {
		t.Fatal(err)
	}
	scope := humanStoreScopeFor([]provenance.TaskID{epoch}, []provenance.OperationID{"mode-winner", "mode-loser"})
	before := humanDecisionStoreFootprint(t, tracker, scope)
	_, err = loser.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "mode-loser"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
		t.Fatalf("stale mode result err=%v calls=%d, want typed condition failure after one barrier call", err, barrier.calls)
	}
	after := humanDecisionStoreFootprint(t, tracker, scope)
	assertBarrierWinnerDelta(t, before, after, humanDecisionExpectation{
		operation: "mode-winner", actor: human.ID, epoch: epoch, subject: epoch, phase: provenance.PhaseRequest,
		kind: DecisionInteractionModeChanged, note: "explicit interaction-mode decision",
		branch:     oracleModeBranch{From: InteractionNormal, To: InteractionAFK},
		conditions: []conditionSnapshot{oracleDecisionCondition(epoch, DecisionInteractionModeChanged, 0)},
		evidence:   []oracleEvidenceExpectation{{kind: "pasture.epoch.subject.v1", task: epoch, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: epoch.String(), Decision: string(oracleDecisionID("mode-winner")), Reference: epoch.String()}}},
		statuses:   map[string]provenance.Status{epoch.String(): provenance.StatusOpen},
	}, winnerResult, "mode-loser")
}

func TestEpochHumanServiceOldAcceptedPlanUATCannotRatifyAfterLaterNonAccept(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome PlanUATVerdict
		payload *PlanUATPayload
	}{
		{name: "changes-requested", outcome: PlanUATChangesRequested, payload: &PlanUATPayload{Feedback: []UATFeedbackItem{{ID: "revise", Body: "revise"}}}},
		{name: "deferred-by-afk", outcome: PlanUATDeferredByAFK, payload: &PlanUATPayload{HeldQuestions: []HeldUATQuestion{{ID: "held", Question: "open", Stable: true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
			defer tracker.Close()
			human, err := tracker.RegisterHumanAgent("test", "State Human", "")
			if err != nil {
				t.Fatal(err)
			}
			epoch := createHumanTestTask(t, tracker, "epoch")
			proposal := createHumanTestTask(t, tracker, "proposal")
			service := newHumanTestService(t, tracker)
			accepted, err := service.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "state-accepted"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
			if err != nil {
				t.Fatalf("accepted Plan UAT: %v", err)
			}
			seedAcceptedReview(t, tracker, epoch, proposal, "state-round")
			if tc.outcome == PlanUATDeferredByAFK {
				if _, err := service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "state-afk"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
					t.Fatalf("set AFK: %v", err)
				}
			}
			if _, err := service.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "state-later"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: tc.outcome, Payload: tc.payload, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
				t.Fatalf("later %s Plan UAT: %v", tc.outcome, err)
			}
			scope := humanStoreScopeFor([]provenance.TaskID{epoch, proposal}, []provenance.OperationID{"state-accepted", "state-later", "state-afk", "state-ratify-old"})
			before := humanDecisionStoreFootprint(t, tracker, scope)
			_, err = service.RatifyPlan(context.Background(), RatifyPlanInput{Meta: CommandMeta{OperationID: "state-ratify-old"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "state-round", PlanUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
			var structured *pasterrors.StructuredError
			if err == nil || !errors.As(err, &structured) || structured.Category != pasterrors.CategoryValidation || !strings.Contains(err.Error(), "current accepted state") {
				t.Fatalf("stale %s ratify error = %v; want actionable validation rejection", tc.outcome, err)
			}
			assertHumanDecisionStoreFootprintEqual(t, before, humanDecisionStoreFootprint(t, tracker, scope))
			assertCommittedAbsent(t, before, "state-ratify-old")
		})
	}
}

func TestEpochHumanServiceUATTerminalBarrierRows(t *testing.T) {
	t.Run("plan-uat-versus-ratify", func(t *testing.T) {
		tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
		defer tracker.Close()
		human, err := tracker.RegisterHumanAgent("test", "Plan Race Human", "")
		if err != nil {
			t.Fatal(err)
		}
		epoch := createHumanTestTask(t, tracker, "epoch")
		proposal := createHumanTestTask(t, tracker, "proposal")
		winner := newHumanTestService(t, tracker)
		accepted, err := winner.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "race-plan-accepted"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
		if err != nil {
			t.Fatal(err)
		}
		seedAcceptedReview(t, tracker, epoch, proposal, "race-round")
		barrier := &callbackEpochBarrier{}
		var winnerResult DecisionResult
		barrier.after = func() error {
			var err error
			winnerResult, err = winner.RatifyPlan(context.Background(), RatifyPlanInput{Meta: CommandMeta{OperationID: "race-ratify-winner"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "race-round", PlanUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
			return err
		}
		loser, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
		if err != nil {
			t.Fatal(err)
		}
		scope := humanStoreScopeFor([]provenance.TaskID{epoch, proposal}, []provenance.OperationID{"race-plan-accepted", "race-ratify-winner", "race-plan-loser"})
		before := humanDecisionStoreFootprint(t, tracker, scope)
		_, err = loser.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "race-plan-loser"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATChangesRequested, Payload: &PlanUATPayload{Feedback: []UATFeedbackItem{{ID: "fix", Body: "fix"}}}, Actor: AssertedHumanActor{ActorID: human.ID}})
		if !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
			t.Fatalf("Plan UAT loser = %v, barrier calls=%d; want condition failure after one barrier", err, barrier.calls)
		}
		after := humanDecisionStoreFootprint(t, tracker, scope)
		acceptedState := findEvidenceByOperationAndKind(t, before, "race-plan-accepted", planSubjectEvidenceKind)
		assertBarrierWinnerDelta(t, before, after, humanDecisionExpectation{
			operation: "race-ratify-winner", actor: human.ID, epoch: epoch, subject: proposal, phase: provenance.PhaseRatify,
			kind: DecisionPlanRatified, note: "explicit plan-ratification decision",
			branch:     oraclePlanRatifiedBranch{Proposal: proposal.String(), ReviewRound: "race-round", PlanUAT: accepted.DecisionID},
			conditions: []conditionSnapshot{oracleEvidenceCondition(proposal, planSubjectEvidenceKind, acceptedState.JournalID, provenance.ConditionExactFact)},
			evidence: []oracleEvidenceExpectation{
				{kind: planSubjectEvidenceKind, task: proposal, state: &oracleStateEvidence{Epoch: epoch.String(), Subject: proposal.String(), State: string(subjectStatePlanRatified), Decision: oracleDecisionID("race-ratify-winner"), DecisionKind: DecisionPlanRatified, Operation: "race-ratify-winner"}},
				{kind: "pasture.review.round.v1", task: proposal, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: proposal.String(), Decision: string(oracleDecisionID("race-ratify-winner")), Reference: "race-round"}},
				{kind: "pasture.plan-uat.decision.v1", task: proposal, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: proposal.String(), Decision: string(oracleDecisionID("race-ratify-winner")), Reference: string(accepted.DecisionID)}},
			},
			closeTask: proposal,
			statuses:  map[string]provenance.Status{epoch.String(): provenance.StatusOpen, proposal.String(): provenance.StatusClosed},
		}, winnerResult, "race-plan-loser")
	})

	t.Run("implementation-uat-versus-land", func(t *testing.T) {
		tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
		defer tracker.Close()
		human, err := tracker.RegisterHumanAgent("test", "Implementation Race Human", "")
		if err != nil {
			t.Fatal(err)
		}
		epoch := createHumanTestTask(t, tracker, "epoch")
		candidate := createHumanTestTask(t, tracker, "candidate")
		authority := humanCandidateAuthority{}
		authority.stateFact = seedCurrentCandidateLifecycle(t, tracker, epoch, candidate, "race-candidate-state")
		authority.reviewFact = seedCleanImplementationReview(t, tracker, epoch, candidate, "race-review", "race-review-finalized")
		authority.members = humanCandidateMembers(t, tracker, "race")
		authority.manifestFact = seedCandidateManifest(t, tracker, epoch, candidate, authority.members, "race-candidate-manifest")
		authority.publicationFact = seedCandidatePublicationSet(t, tracker, epoch, candidate, authority.manifestFact, publicationsForMembers(authority.members), 0, "race-publication-set")
		winner := newHumanTestService(t, tracker)
		accepted, err := winner.RecordImplementationUAT(context.Background(), ImplementationUATInput{Meta: CommandMeta{OperationID: "race-impl-accepted"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
		if err != nil {
			t.Fatal(err)
		}
		barrier := &callbackEpochBarrier{}
		var winnerResult DecisionResult
		barrier.after = func() error {
			var err error
			winnerResult, err = winner.Land(context.Background(), LandInput{Meta: CommandMeta{OperationID: "race-land-winner"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
			return err
		}
		loser, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
		if err != nil {
			t.Fatal(err)
		}
		scope := humanStoreScopeFor([]provenance.TaskID{epoch, candidate}, []provenance.OperationID{"race-impl-accepted", "race-land-winner", "race-impl-loser"})
		before := humanDecisionStoreFootprint(t, tracker, scope)
		_, err = loser.RecordImplementationUAT(context.Background(), ImplementationUATInput{Meta: CommandMeta{OperationID: "race-impl-loser"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATChangesRequested, Actor: AssertedHumanActor{ActorID: human.ID}})
		if !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
			t.Fatalf("Implementation UAT loser = %v, barrier calls=%d; want condition failure after one barrier", err, barrier.calls)
		}
		after := humanDecisionStoreFootprint(t, tracker, scope)
		acceptedState := findEvidenceByOperationAndKind(t, before, "race-impl-accepted", candidateEvidenceKind)
		assertBarrierWinnerDelta(t, before, after, humanDecisionExpectation{
			operation: "race-land-winner", actor: human.ID, epoch: epoch, subject: epoch, phase: provenance.PhaseLanding,
			kind: DecisionLanded, note: "explicit landing decision",
			branch:     oracleLandedBranch{Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: accepted.DecisionID},
			conditions: []conditionSnapshot{oracleEvidenceCondition(candidate, candidateEvidenceKind, acceptedState.JournalID, provenance.ConditionCurrentFact), oracleEvidenceCondition(candidate, implementationReviewAuthorityEvidenceKind, authority.reviewFact, provenance.ConditionCurrentFact), oracleEvidenceCondition(candidate, candidateManifestEvidenceKind, authority.manifestFact, provenance.ConditionExactFact), oracleEvidenceCondition(candidate, candidatePublicationSetEvidenceKind, authority.publicationFact, provenance.ConditionCurrentFact)},
			evidence: []oracleEvidenceExpectation{
				{kind: candidateEvidenceKind, task: candidate, state: &oracleStateEvidence{Epoch: epoch.String(), Subject: candidate.String(), State: string(subjectStateImplementationLanded), Decision: oracleDecisionID("race-land-winner"), DecisionKind: DecisionLanded, Operation: "race-land-winner"}},
				{kind: "pasture.implementation-uat.decision.v1", task: epoch, reference: &oracleReferenceEvidence{Epoch: epoch.String(), Subject: epoch.String(), Decision: string(oracleDecisionID("race-land-winner")), Reference: string(accepted.DecisionID)}},
			},
			closeTask: epoch,
			statuses:  map[string]provenance.Status{epoch.String(): provenance.StatusClosed, candidate.String(): provenance.StatusOpen},
		}, winnerResult, "race-impl-loser")
	})
}

func TestEpochHumanServiceRejectsWrongRatifyAndLandBindings(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	human, err := tracker.RegisterHumanAgent("test", "Binding Human", "")
	if err != nil {
		t.Fatal(err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	proposalA := createHumanTestTask(t, tracker, "proposal-a")
	proposalB := createHumanTestTask(t, tracker, "proposal-b")
	candidateA := createHumanTestTask(t, tracker, "candidate-a")
	candidateB := createHumanTestTask(t, tracker, "candidate-b")
	seedCurrentCandidateLifecycle(t, tracker, epoch, candidateA, "binding-candidate-a-state")
	seedCleanImplementationReview(t, tracker, epoch, candidateA, "binding-review-a", "binding-review-a-finalized")
	seedCurrentCandidateLifecycle(t, tracker, epoch, candidateB, "binding-candidate-b-state")
	seedCleanImplementationReview(t, tracker, epoch, candidateB, "binding-review-b", "binding-review-b-finalized")
	membersA := humanCandidateMembers(t, tracker, "binding-a")
	manifestA := seedCandidateManifest(t, tracker, epoch, candidateA, membersA, "binding-candidate-a-manifest")
	seedCandidatePublicationSet(t, tracker, epoch, candidateA, manifestA, publicationsForMembers(membersA), 0, "binding-candidate-a-publication")
	service := newHumanTestService(t, tracker)
	if _, err := service.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "binding-plan-a"}, Epoch: EpochRootID(epoch.String()), Proposal: proposalA, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
		t.Fatal(err)
	}
	planB, err := service.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "binding-plan-b"}, Epoch: EpochRootID(epoch.String()), Proposal: proposalB, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatal(err)
	}
	seedAcceptedReview(t, tracker, epoch, proposalA, "binding-round")
	implA, err := service.RecordImplementationUAT(context.Background(), ImplementationUATInput{Meta: CommandMeta{OperationID: "binding-impl-a"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidateA.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatal(err)
	}
	implB, err := service.RecordImplementationUAT(context.Background(), ImplementationUATInput{Meta: CommandMeta{OperationID: "binding-impl-b"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidateB.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if implA.DecisionID == implB.DecisionID {
		t.Fatal("test setup did not produce distinct candidate decisions")
	}
	cases := []struct {
		name        string
		scope       humanStoreScope
		operation   provenance.OperationID
		bindingText string
		invoke      func() error
	}{
		{
			name: "ratify proposal binding", scope: humanStoreScopeFor([]provenance.TaskID{epoch, proposalA, proposalB}, []provenance.OperationID{"binding-plan-a", "binding-plan-b", "binding-ratify"}), operation: "binding-ratify", bindingText: "Plan UAT decision",
			invoke: func() error {
				_, err := service.RatifyPlan(context.Background(), RatifyPlanInput{Meta: CommandMeta{OperationID: "binding-ratify"}, Epoch: EpochRootID(epoch.String()), Proposal: proposalA, ReviewRound: "binding-round", PlanUAT: planB.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
				return err
			},
		},
		{
			name: "land candidate binding", scope: humanStoreScopeFor([]provenance.TaskID{epoch, candidateA, candidateB}, []provenance.OperationID{"binding-impl-a", "binding-impl-b", "binding-land"}), operation: "binding-land", bindingText: "Implementation UAT decision",
			invoke: func() error {
				_, err := service.Land(context.Background(), LandInput{Meta: CommandMeta{OperationID: "binding-land"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidateA.String()), ImplementationUAT: implB.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := humanDecisionStoreFootprint(t, tracker, tc.scope)
			err := tc.invoke()
			var structured *pasterrors.StructuredError
			if err == nil || !errors.As(err, &structured) || structured.Category != pasterrors.CategoryValidation || !strings.Contains(err.Error(), tc.bindingText) {
				t.Fatalf("wrong binding returned an unhelpful error: %v", err)
			}
			assertHumanDecisionStoreFootprintEqual(t, before, humanDecisionStoreFootprint(t, tracker, tc.scope))
			assertCommittedAbsent(t, before, tc.operation)
		})
	}
}

func TestEpochHumanServiceValidAFKReplayReachesApplyConflict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome PlanUATVerdict
		payload *PlanUATPayload
	}{
		{name: "accepted", outcome: PlanUATAccepted},
		{name: "changes-requested", outcome: PlanUATChangesRequested, payload: &PlanUATPayload{Feedback: []UATFeedbackItem{{ID: "revise", Body: "revise"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
			defer tracker.Close()
			human, err := tracker.RegisterHumanAgent("test", "AFK Replay Human", "")
			if err != nil {
				t.Fatal(err)
			}
			epoch := createHumanTestTask(t, tracker, "epoch")
			proposal := createHumanTestTask(t, tracker, "proposal")
			service := newHumanTestService(t, tracker)
			operation := provenance.OperationID("afk-replay-" + tc.name)
			original := PlanUATInput{Meta: CommandMeta{OperationID: operation}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: tc.outcome, Payload: tc.payload, Actor: AssertedHumanActor{ActorID: human.ID}}
			if _, err := service.RecordPlanUAT(context.Background(), original); err != nil {
				t.Fatalf("record original %s Plan UAT: %v", tc.outcome, err)
			}
			modeOperation := provenance.OperationID("afk-replay-mode-" + tc.name)
			if _, err := service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: modeOperation}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
				t.Fatalf("set live AFK mode: %v", err)
			}
			deferred := PlanUATInput{Meta: original.Meta, Epoch: original.Epoch, Proposal: proposal, Outcome: PlanUATDeferredByAFK, Payload: &PlanUATPayload{HeldQuestions: []HeldUATQuestion{{ID: "held", Question: "open", Stable: true}}}, Actor: original.Actor}
			scope := humanStoreScopeFor([]provenance.TaskID{epoch, proposal}, []provenance.OperationID{operation, modeOperation})
			before := humanDecisionStoreFootprint(t, tracker, scope)
			_, err = service.RecordPlanUAT(context.Background(), deferred)
			if !errors.Is(err, provenance.ErrOperationConflict) {
				t.Fatalf("valid %s to deferred AFK retry = %v; want authoritative Apply conflict", tc.outcome, err)
			}
			assertHumanDecisionStoreFootprintEqual(t, before, humanDecisionStoreFootprint(t, tracker, scope))
			assertCommittedOperationKind(t, before, operation, provenance.CommittedExact)
		})
	}
}

func TestEpochHumanServiceDeferredHeldQuestionsPersistAndConflict(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	human, err := tracker.RegisterHumanAgent("test", "Held Question Human", "")
	if err != nil {
		t.Fatal(err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	proposal := createHumanTestTask(t, tracker, "proposal")
	service := newHumanTestService(t, tracker)
	if _, err := service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "held-mode"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
		t.Fatal(err)
	}
	payload := &PlanUATPayload{
		Interactions:  []UATInteraction{{Prompt: "approve?", Response: "later"}},
		Feedback:      []UATFeedbackItem{{ID: "feedback", Body: "follow up"}},
		HeldQuestions: []HeldUATQuestion{{ID: "held-1", Question: "Which binding?", Stable: true}, {ID: "held-2", Question: "Which actor?", Stable: false}},
	}
	input := PlanUATInput{Meta: CommandMeta{OperationID: "held-deferred"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATDeferredByAFK, Payload: payload, Actor: AssertedHumanActor{ActorID: human.ID}}
	if _, err := service.RecordPlanUAT(context.Background(), input); err != nil {
		t.Fatalf("record deferred Plan UAT: %v", err)
	}
	scope := humanStoreScopeFor([]provenance.TaskID{epoch, proposal}, []provenance.OperationID{"held-mode", "held-deferred"})
	before := humanDecisionStoreFootprint(t, tracker, scope)
	decision := findDecisionByOperation(t, before, input.Meta.OperationID)
	var envelope persistedDecision
	if err := json.Unmarshal(decision.Payload, &envelope); err != nil {
		t.Fatalf("decode canonical deferred decision envelope: %v", err)
	}
	var deferred PlanDeferredByAFK
	if err := json.Unmarshal(envelope.Decision.Payload, &deferred); err != nil {
		t.Fatalf("decode canonical deferred decision: %v", err)
	}
	if !reflect.DeepEqual(deferred.Interactions, payload.Interactions) || !reflect.DeepEqual(deferred.Feedback, payload.Feedback) || !reflect.DeepEqual(deferred.HeldQuestions, payload.HeldQuestions) || deferred.ModeEntry == "" {
		t.Fatalf("canonical deferred payload = %+v; want complete payload %+v and mode entry", deferred, payload)
	}
	changed := *payload
	changed.HeldQuestions = append([]HeldUATQuestion(nil), payload.HeldQuestions...)
	changed.HeldQuestions[1].Question = "Which registered actor?"
	input.Payload = &changed
	_, err = service.RecordPlanUAT(context.Background(), input)
	if !errors.Is(err, provenance.ErrOperationConflict) {
		t.Fatalf("changed HeldQuestions retry = %v; want provenance operation conflict", err)
	}
	assertHumanDecisionStoreFootprintEqual(t, before, humanDecisionStoreFootprint(t, tracker, scope))
	assertCommittedOperationKind(t, before, input.Meta.OperationID, provenance.CommittedExact)
}

func TestEpochHumanServiceUsesInjectedClock(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	human, err := tracker.RegisterHumanAgent("test", "Clock Human", "")
	if err != nil {
		t.Fatal(err)
	}
	epoch := createHumanTestTask(t, tracker, "epoch")
	fixed := time.Date(2026, time.July, 25, 22, 0, 0, 123456789, time.UTC)
	service, err := tracker.NewEpochHumanService(EpochServiceOptions{now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "fixed-clock"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
		t.Fatal(err)
	}
	page, err := tracker.Journal().Facts().QueryDecisions(provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: epoch}}, Kinds: []provenance.DecisionKind{journalDecisionKind(DecisionInteractionModeChanged)}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].RecordedAt.UnixNano() != fixed.UnixNano() {
		t.Fatalf("RecordedAt = %v, err=%v; want injected UTC instant %v", page.Rows, err, fixed)
	}
}

func openHumanTestTracker(t *testing.T, path string) *trackerImpl {
	t.Helper()
	opened, err := OpenTaskTracker(path)
	if err != nil {
		t.Fatalf("OpenTaskTracker: %v", err)
	}
	tracker, ok := opened.(*trackerImpl)
	if !ok {
		t.Fatalf("OpenTaskTracker returned %T, want *trackerImpl", opened)
	}
	return tracker
}

func newHumanTestService(t *testing.T, tracker *trackerImpl) EpochHumanService {
	t.Helper()
	service, err := tracker.NewEpochHumanService(EpochServiceOptions{})
	if err != nil {
		t.Fatalf("NewEpochHumanService: %v", err)
	}
	return service
}

func createHumanTestTask(t *testing.T, tracker *trackerImpl, title string) provenance.TaskID {
	t.Helper()
	task, err := tracker.Create("human-service", title, "", provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped)
	if err != nil {
		t.Fatalf("create %s task: %v", title, err)
	}
	return task.ID
}

func seedAcceptedReview(t *testing.T, tracker *trackerImpl, epoch, proposal provenance.TaskID, round ReviewRoundID) {
	t.Helper()
	effect, err := MapMaterialEvent(ReviewRoundFinalizedEvent{Subject: proposal, Epoch: epoch, Round: round, Verdict: VerdictAccept})
	if err != nil {
		t.Fatalf("map accepted review: %v", err)
	}
	session, err := tracker.systemSession()
	if err != nil {
		t.Fatalf("system session: %v", err)
	}
	if _, err := session.Atomic(func(op *provenance.Operation) { op.Add(effect) }); err != nil {
		t.Fatalf("seed accepted review: %v", err)
	}
}

type humanCandidateAuthority struct {
	stateFact       provenance.JournalID
	reviewFact      provenance.JournalID
	manifestFact    provenance.JournalID
	publicationFact provenance.JournalID
	members         []candidateMember
}

func seedCurrentCandidateLifecycle(t *testing.T, tracker *trackerImpl, epoch, candidate provenance.TaskID, operation provenance.OperationID) provenance.JournalID {
	t.Helper()
	state, err := newAssignmentSubjectStateEvidence(epoch, candidate, subjectStateCandidateCurrent, operation)
	if err != nil {
		t.Fatalf("construct candidate lifecycle authority: %v", err)
	}
	effect, err := newSubjectStateEvidenceEffect(candidate, state, "candidate-state")
	if err != nil {
		t.Fatalf("construct candidate lifecycle effect: %v", err)
	}
	applyAuthorityTestEffect(t, tracker, operation, nil, effect)
	return factJournalID(t, tracker, candidate, candidateEvidenceKind, operation)
}

func seedCleanImplementationReview(t *testing.T, tracker *trackerImpl, epoch, candidate provenance.TaskID, round ReviewRoundID, operation provenance.OperationID) provenance.JournalID {
	t.Helper()
	review, err := newFinalizedReviewAuthority(EpochRootID(epoch.String()), IntegrationCandidateSetID(candidate.String()), round, [3]reviewAxisAuthority{
		{Axis: AxisCorrectness, Event: 101, Verdict: VerdictAccept},
		{Axis: AxisTestQuality, Event: 102, Verdict: VerdictAccept},
		{Axis: AxisElegance, Event: 103, Verdict: VerdictAccept},
	}, operation)
	if err != nil {
		t.Fatalf("construct clean implementation review authority: %v", err)
	}
	effect, err := newReviewAuthorityEvidenceEffect(candidate, review, "review-authority")
	if err != nil {
		t.Fatalf("construct clean implementation review effect: %v", err)
	}
	applyAuthorityTestEffect(t, tracker, operation, nil, effect)
	return factJournalID(t, tracker, candidate, implementationReviewAuthorityEvidenceKind, operation)
}

func seedCandidateManifest(t *testing.T, tracker *trackerImpl, epoch, candidate provenance.TaskID, members []candidateMember, operation provenance.OperationID) provenance.JournalID {
	t.Helper()
	manifest, err := newIntegrationCandidateManifest(EpochRootID(epoch.String()), IntegrationCandidateSetID(candidate.String()), members, operation)
	if err != nil {
		t.Fatalf("construct candidate manifest authority: %v", err)
	}
	effect, err := newCandidateManifestEvidenceEffect(candidate, manifest, "candidate-manifest")
	if err != nil {
		t.Fatalf("construct candidate manifest effect: %v", err)
	}
	applyAuthorityTestEffect(t, tracker, operation, nil, effect)
	return factJournalID(t, tracker, candidate, candidateManifestEvidenceKind, operation)
}

func seedCandidatePublicationSet(t *testing.T, tracker *trackerImpl, epoch, candidate provenance.TaskID, manifestFact provenance.JournalID, publications []repositoryPublication, previousFact provenance.JournalID, operation provenance.OperationID) provenance.JournalID {
	t.Helper()
	publicationSet, err := newCandidatePublicationSet(EpochRootID(epoch.String()), IntegrationCandidateSetID(candidate.String()), publications, operation)
	if err != nil {
		t.Fatalf("construct candidate publication authority: %v", err)
	}
	effect, err := newCandidatePublicationSetEvidenceEffect(candidate, publicationSet, "publication-set")
	if err != nil {
		t.Fatalf("construct candidate publication effect: %v", err)
	}
	conditions := []provenance.Condition{candidateManifestExactCondition(candidate, manifestFact)}
	if previousFact != 0 {
		conditions = append(conditions, candidatePublicationSetCurrentCondition(candidate, previousFact))
	}
	applyAuthorityTestEffect(t, tracker, operation, conditions, effect)
	return factJournalID(t, tracker, candidate, candidatePublicationSetEvidenceKind, operation)
}

func humanCandidateMembers(t *testing.T, tracker *trackerImpl, prefix string) []candidateMember {
	t.Helper()
	memberA := createHumanTestTask(t, tracker, prefix+"-member-a")
	memberB := createHumanTestTask(t, tracker, prefix+"-member-b")
	return []candidateMember{
		{Repository: "repo-a", Candidate: ImplementationCandidateID(memberA.String()), Commit: authorityTestCommitA},
		{Repository: "repo-b", Candidate: ImplementationCandidateID(memberB.String()), Commit: authorityTestCommitB},
	}
}

func publicationsForMembers(members []candidateMember) []repositoryPublication {
	publications := make([]repositoryPublication, len(members))
	for i, member := range members {
		publications[i] = repositoryPublication{Repository: member.Repository, Candidate: member.Candidate, Ref: "refs/heads/main", Commit: member.Commit, VerificationOperation: provenance.OperationID("verify-" + string(member.Repository))}
	}
	return publications
}

type humanStoreScope struct {
	tasks      []provenance.TaskID
	operations []provenance.OperationID
}

func humanStoreScopeFor(tasks []provenance.TaskID, operations []provenance.OperationID) humanStoreScope {
	copyTasks := append([]provenance.TaskID(nil), tasks...)
	copyOperations := append([]provenance.OperationID(nil), operations...)
	sort.Slice(copyTasks, func(i, j int) bool { return copyTasks[i].String() < copyTasks[j].String() })
	sort.Slice(copyOperations, func(i, j int) bool { return string(copyOperations[i]) < string(copyOperations[j]) })
	return humanStoreScope{tasks: copyTasks, operations: copyOperations}
}

type normalizedDecisionFact struct {
	JournalID               provenance.JournalID
	TaskID                  provenance.TaskID
	Kind                    provenance.DecisionKind
	RawActorID              string
	ActorID                 provenance.ActorID
	OperationID             provenance.OperationID
	RecordedAt              time.Time
	Payload                 []byte
	ProducingOperationID    provenance.JournalID
	HasProducingOperationID bool
	Contexts                []normalizedEventContext
}

type normalizedEvidenceFact struct {
	JournalID               provenance.JournalID
	TaskID                  provenance.TaskID
	Kind                    provenance.EvidenceKind
	RawActorID              string
	ActorID                 provenance.ActorID
	OperationID             provenance.OperationID
	Digest                  []byte
	Payload                 []byte
	RecordedAt              time.Time
	ProducingOperationID    provenance.JournalID
	HasProducingOperationID bool
	Conditions              []conditionSnapshot
	Contexts                []normalizedEventContext
}

type normalizedEventContext struct {
	Kind     provenance.EventContextKind `json:"kind"`
	Identity string                      `json:"identity"`
}

type normalizedActivity struct {
	ID        provenance.ActivityID
	AgentID   provenance.ActorID
	Phase     provenance.Phase
	Stage     provenance.Stage
	StartedAt time.Time
	EndedAt   *time.Time
	Notes     string
}

type normalizedTaskEvent struct {
	JournalID             provenance.JournalID
	TaskID                provenance.TaskID
	Kind                  provenance.EventKind
	RawActorID            string
	ActorID               provenance.ActorID
	Contexts              []normalizedEventContext
	ActorContextIDs       []string
	OperationID           provenance.OperationID
	OperationJournalID    provenance.JournalID
	HasOperationJournalID bool
	RecordedAt            time.Time
	Payload               []byte
}

type normalizedResultSlot struct {
	Slot              provenance.ResultSlotID
	ProducedJournalID provenance.JournalID
	Kind              provenance.JournalKind
	TaskID            provenance.TaskID
	HasTaskID         bool
	ActivityID        provenance.ActivityID
	HasActivityID     bool
}

type normalizedCommittedOperation struct {
	Present         bool
	Kind            provenance.CommittedResultKind
	AnchorJournalID provenance.JournalID
	EmittedEvents   []provenance.JournalID
	ResultSlots     []normalizedResultSlot
}

type normalizedHumanStoreFootprint struct {
	Decisions          []normalizedDecisionFact
	Evidence           []normalizedEvidenceFact
	Activities         []normalizedActivity
	Events             []normalizedTaskEvent
	Statuses           map[string]provenance.Status
	Operations         map[string]normalizedCommittedOperation
	ExpectedOperations map[string]normalizedCommittedOperation
}

const oracleCanonicalJSONCodec DecisionCodecID = "pasture.canonical-json/v1"

type oracleModeBranch struct {
	From InteractionMode `json:"from"`
	To   InteractionMode `json:"to"`
}

type oraclePlanSnapshot struct {
	ID            PlanUATDecisionID     `json:"id"`
	UATTaskID     provenance.TaskID     `json:"uatTaskId"`
	Proposal      DocumentRevisionID    `json:"proposal"`
	DecisionEntry DecisionLedgerEntryID `json:"decisionEntry"`
	InputLedger   DocumentRevisionID    `json:"inputLedger"`
	OutputLedger  DocumentRevisionID    `json:"outputLedger"`
}

type oraclePlanAcceptedBranch struct {
	Snapshot     oraclePlanSnapshot `json:"snapshot"`
	Interactions []UATInteraction   `json:"interactions"`
	Feedback     []UATFeedbackItem  `json:"feedback"`
}

type oraclePlanRatifiedBranch struct {
	Proposal    string                `json:"proposal"`
	ReviewRound ReviewRoundID         `json:"reviewRound"`
	PlanUAT     DecisionLedgerEntryID `json:"planUat"`
}

type oracleImplementationUATBranch struct {
	Outcome ImplementationUATVerdict `json:"outcome"`
	Payload ImplUATPayload           `json:"payload"`
}

type oracleLandedBranch struct {
	Candidate         IntegrationCandidateSetID `json:"candidate"`
	ImplementationUAT DecisionLedgerEntryID     `json:"implementationUat"`
}

type oracleStateEvidence struct {
	Epoch        string                 `json:"epoch"`
	Subject      string                 `json:"subject"`
	State        string                 `json:"state"`
	Decision     DecisionLedgerEntryID  `json:"decision"`
	DecisionKind DecisionKindID         `json:"decisionKind"`
	Operation    provenance.OperationID `json:"operation"`
}

type oracleReferenceEvidence struct {
	Epoch     string
	Subject   string
	Decision  string
	Reference string
}

type oracleConditionEvidence struct {
	Conditions []conditionSnapshot `json:"conditions"`
}

type oracleEvidenceExpectation struct {
	kind       provenance.EvidenceKind
	task       provenance.TaskID
	state      *oracleStateEvidence
	reference  *oracleReferenceEvidence
	conditions []conditionSnapshot
	value      any
}

type humanDecisionExpectation struct {
	operation  provenance.OperationID
	actor      provenance.ActorID
	epoch      provenance.TaskID
	subject    provenance.TaskID
	phase      provenance.Phase
	kind       DecisionKindID
	note       string
	branch     any
	conditions []conditionSnapshot
	evidence   []oracleEvidenceExpectation
	closeTask  provenance.TaskID
	statuses   map[string]provenance.Status
}

func readRawDecisionFacts(t *testing.T, tracker *trackerImpl, task provenance.TaskID) []normalizedDecisionFact {
	t.Helper()
	rows, err := tracker.auditDB.QueryContext(context.Background(), `
		SELECT d.journal_id, j.recorded_at, d.task_id, d.decision_kind, d.payload,
		       ja.effective_actor_id, o.operation_id, j.produced_by_operation_journal_id
		FROM journal_decisions d
		JOIN journal j ON j.journal_id = d.journal_id
		JOIN journal_attributed ja ON ja.journal_id = d.journal_id
		LEFT JOIN journal_operations o ON o.journal_id = j.produced_by_operation_journal_id
		WHERE d.task_id = ?
		ORDER BY d.journal_id`, task.String())
	if err != nil {
		t.Fatalf("read all decision kinds for %s: %v", task, err)
	}
	defer rows.Close()
	var facts []normalizedDecisionFact
	for rows.Next() {
		var journalID, recordedAt int64
		var rawTask, rawKind, rawActor, rawOperation string
		var payload []byte
		var anchor sql.NullInt64
		if err := rows.Scan(&journalID, &recordedAt, &rawTask, &rawKind, &payload, &rawActor, &rawOperation, &anchor); err != nil {
			t.Fatalf("scan all decision kinds for %s: %v", task, err)
		}
		factTask, err := provenance.ParseTaskID(rawTask)
		if err != nil {
			t.Fatalf("parse observed decision task %q: %v", rawTask, err)
		}
		actor, _ := provenance.ParseActorID(rawActor)
		fact := normalizedDecisionFact{
			JournalID: provenance.JournalID(journalID), TaskID: factTask, Kind: provenance.DecisionKind(rawKind),
			RawActorID: rawActor, ActorID: actor, OperationID: provenance.OperationID(rawOperation),
			RecordedAt: time.Unix(0, recordedAt).UTC(), Payload: append([]byte(nil), payload...),
		}
		if anchor.Valid {
			fact.ProducingOperationID = provenance.JournalID(anchor.Int64)
			fact.HasProducingOperationID = true
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read all decision kinds for %s: %v", task, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close all decision kinds for %s: %v", task, err)
	}
	for i := range facts {
		facts[i].Contexts = rawFactContexts(t, tracker, "journal_decision_contexts", "decision_journal_id", facts[i].JournalID)
	}
	return facts
}

func readRawEvidenceFacts(t *testing.T, tracker *trackerImpl, task provenance.TaskID) []normalizedEvidenceFact {
	t.Helper()
	rows, err := tracker.auditDB.QueryContext(context.Background(), `
		SELECT e.journal_id, j.recorded_at, e.task_id, e.evidence_kind, e.content_digest, e.payload,
		       ja.effective_actor_id, o.operation_id, j.produced_by_operation_journal_id
		FROM journal_evidence e
		JOIN journal j ON j.journal_id = e.journal_id
		JOIN journal_attributed ja ON ja.journal_id = e.journal_id
		LEFT JOIN journal_operations o ON o.journal_id = j.produced_by_operation_journal_id
		WHERE e.task_id = ?
		ORDER BY e.journal_id`, task.String())
	if err != nil {
		t.Fatalf("read all evidence kinds for %s: %v", task, err)
	}
	defer rows.Close()
	var facts []normalizedEvidenceFact
	for rows.Next() {
		var journalID, recordedAt int64
		var rawTask, rawKind, rawActor, rawOperation string
		var digest, payload []byte
		var anchor sql.NullInt64
		if err := rows.Scan(&journalID, &recordedAt, &rawTask, &rawKind, &digest, &payload, &rawActor, &rawOperation, &anchor); err != nil {
			t.Fatalf("scan all evidence kinds for %s: %v", task, err)
		}
		factTask, err := provenance.ParseTaskID(rawTask)
		if err != nil {
			t.Fatalf("parse observed evidence task %q: %v", rawTask, err)
		}
		actor, _ := provenance.ParseActorID(rawActor)
		fact := normalizedEvidenceFact{
			JournalID: provenance.JournalID(journalID), TaskID: factTask, Kind: provenance.EvidenceKind(rawKind),
			RawActorID: rawActor, ActorID: actor, OperationID: provenance.OperationID(rawOperation),
			Digest: append([]byte(nil), digest...), Payload: append([]byte(nil), payload...),
			RecordedAt: time.Unix(0, recordedAt).UTC(),
		}
		if anchor.Valid {
			fact.ProducingOperationID = provenance.JournalID(anchor.Int64)
			fact.HasProducingOperationID = true
		}
		if fact.Kind == preconditionEvidenceKind {
			var conditions conditionEvidence
			if err := json.Unmarshal(fact.Payload, &conditions); err != nil {
				t.Fatalf("decode complete precondition footprint %d: %v", fact.JournalID, err)
			}
			fact.Conditions = append([]conditionSnapshot(nil), conditions.Conditions...)
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read all evidence kinds for %s: %v", task, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close all evidence kinds for %s: %v", task, err)
	}
	for i := range facts {
		facts[i].Contexts = rawFactContexts(t, tracker, "journal_evidence_contexts", "evidence_journal_id", facts[i].JournalID)
	}
	return facts
}

func rawFactContexts(t *testing.T, tracker *trackerImpl, table, column string, journalID provenance.JournalID) []normalizedEventContext {
	t.Helper()
	rows, err := tracker.auditDB.QueryContext(context.Background(), fmt.Sprintf("SELECT context_kind, context_identity FROM %s WHERE %s = ? ORDER BY context_kind, context_identity", table, column), int64(journalID))
	if err != nil {
		t.Fatalf("read raw fact contexts for journal %d: %v", journalID, err)
	}
	defer rows.Close()
	var contexts []normalizedEventContext
	for rows.Next() {
		var context normalizedEventContext
		if err := rows.Scan(&context.Kind, &context.Identity); err != nil {
			t.Fatalf("scan raw fact context for journal %d: %v", journalID, err)
		}
		contexts = append(contexts, context)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read raw fact contexts for journal %d: %v", journalID, err)
	}
	return contexts
}

func humanDecisionStoreFootprint(t *testing.T, tracker *trackerImpl, scope humanStoreScope) normalizedHumanStoreFootprint {
	t.Helper()
	footprint := normalizedHumanStoreFootprint{Statuses: make(map[string]provenance.Status), Operations: make(map[string]normalizedCommittedOperation), ExpectedOperations: make(map[string]normalizedCommittedOperation)}
	// LookupCommitted is captured by the independently supplied operation ID
	// before any raw decision, evidence, or event row is inspected. This record
	// is the oracle's expected producer identity.
	for _, operation := range scope.operations {
		result, err := tracker.Journal().LookupCommitted(operation)
		if err != nil {
			t.Fatalf("capture committed operation %q before raw rows: %v", operation, err)
		}
		footprint.ExpectedOperations[string(operation)] = normalizeCommittedOperation(result)
	}
	for _, task := range scope.tasks {
		footprint.Decisions = append(footprint.Decisions, readRawDecisionFacts(t, tracker, task)...)
		footprint.Evidence = append(footprint.Evidence, readRawEvidenceFacts(t, tracker, task)...)
		taskValue, err := tracker.Show(task)
		if err != nil {
			t.Fatalf("read complete status footprint for %s: %v", task, err)
		}
		footprint.Statuses[task.String()] = taskValue.Status
	}

	for _, operation := range scope.operations {
		result, err := tracker.Journal().LookupCommitted(operation)
		if err != nil {
			t.Fatalf("lookup complete operation footprint %q: %v", operation, err)
		}
		footprint.Operations[string(operation)] = normalizeCommittedOperation(result)
	}

	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: append([]provenance.TaskID(nil), scope.tasks...), Limit: provenance.MaxFactPageSize}
	for {
		page, err := tracker.Journal().QueryTaskEvents(query)
		if err != nil {
			t.Fatalf("read complete event footprint: %v", err)
		}
		for _, row := range page.Events {
			operation, operationJournalID, hasOperationJournalID := rawTaskEventProducer(t, tracker, row.JournalID)
			contexts := normalizeEventContexts(t, row.Contexts)
			footprint.Events = append(footprint.Events, normalizedTaskEvent{
				JournalID: row.JournalID, TaskID: row.TaskID, Kind: row.EventKind,
				RawActorID: row.ActorID.String(), ActorID: row.ActorID, Contexts: contexts,
				ActorContextIDs: actorContextIDs(contexts), OperationID: operation,
				OperationJournalID: operationJournalID, HasOperationJournalID: hasOperationJournalID,
				RecordedAt: row.RecordedAt, Payload: append([]byte(nil), row.Payload...),
			})
		}
		if page.Next == nil {
			break
		}
		query.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
		query.AfterJournalID = page.Next.AfterJournalID
	}

	activities, err := tracker.Activities(nil)
	if err != nil {
		t.Fatalf("read complete activity footprint: %v", err)
	}
	for _, activity := range activities {
		var endedAt *time.Time
		if activity.EndedAt != nil {
			ended := *activity.EndedAt
			endedAt = &ended
		}
		footprint.Activities = append(footprint.Activities, normalizedActivity{ID: activity.ID, AgentID: activity.AgentID, Phase: activity.Phase, Stage: activity.Stage, StartedAt: activity.StartedAt, EndedAt: endedAt, Notes: activity.Notes})
	}
	sort.Slice(footprint.Decisions, func(i, j int) bool { return footprint.Decisions[i].JournalID < footprint.Decisions[j].JournalID })
	sort.Slice(footprint.Evidence, func(i, j int) bool { return footprint.Evidence[i].JournalID < footprint.Evidence[j].JournalID })
	sort.Slice(footprint.Events, func(i, j int) bool { return footprint.Events[i].JournalID < footprint.Events[j].JournalID })
	sort.Slice(footprint.Activities, func(i, j int) bool { return footprint.Activities[i].ID.String() < footprint.Activities[j].ID.String() })
	return footprint
}

func normalizeCommittedOperation(result provenance.CommittedResult) normalizedCommittedOperation {
	normalized := normalizedCommittedOperation{Present: result.Kind != provenance.CommittedAbsent, Kind: result.Kind, AnchorJournalID: result.AnchorJournalID, EmittedEvents: append([]provenance.JournalID(nil), result.EmittedEvents...)}
	for _, slot := range result.ResultSlots {
		value := normalizedResultSlot{Slot: slot.Slot, ProducedJournalID: slot.ProducedJournalID, Kind: slot.Kind}
		if slot.TaskID != nil {
			value.TaskID, value.HasTaskID = *slot.TaskID, true
		}
		if slot.ActivityID != nil {
			value.ActivityID, value.HasActivityID = *slot.ActivityID, true
		}
		normalized.ResultSlots = append(normalized.ResultSlots, value)
	}
	return normalized
}

func valueTaskID(id *provenance.TaskID) provenance.TaskID {
	if id == nil {
		return provenance.TaskID{}
	}
	return *id
}

func normalizeEventContexts(t *testing.T, contexts []provenance.EventContext) []normalizedEventContext {
	t.Helper()
	if len(contexts) == 0 {
		return nil
	}
	normalized := make([]normalizedEventContext, 0, len(contexts))
	for _, context := range contexts {
		encoded, err := json.Marshal(context)
		if err != nil {
			t.Fatalf("encode observed event context: %v", err)
		}
		var value normalizedEventContext
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatalf("decode observed event context: %v", err)
		}
		normalized = append(normalized, value)
	}
	return normalized
}

func actorContextIDs(contexts []normalizedEventContext) []string {
	var actors []string
	for _, context := range contexts {
		if context.Kind == provenance.EventContextKindActor {
			actors = append(actors, context.Identity)
		}
	}
	return actors
}

func rawTaskEventProducer(t *testing.T, tracker *trackerImpl, journalID provenance.JournalID) (provenance.OperationID, provenance.JournalID, bool) {
	t.Helper()
	var anchor sql.NullInt64
	var operation sql.NullString
	row := tracker.auditDB.QueryRowContext(context.Background(), `
		SELECT j.produced_by_operation_journal_id, o.operation_id
		FROM journal j
		LEFT JOIN journal_operations o ON o.journal_id = j.produced_by_operation_journal_id
		WHERE j.journal_id = ?`, int64(journalID))
	if err := row.Scan(&anchor, &operation); err != nil {
		t.Fatalf("read raw task-event producer for journal %d: %v", journalID, err)
	}
	if !anchor.Valid {
		return "", 0, false
	}
	return provenance.OperationID(operation.String), provenance.JournalID(anchor.Int64), true
}

func assertHumanDecisionStoreFootprintEqual(t *testing.T, want, got normalizedHumanStoreFootprint) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("complete persisted footprint changed:\nwant: %+v\n got: %+v", want, got)
	}
}

func sameDecisionResultBindings(left, right DecisionResult) bool {
	return left.OperationID == right.OperationID && left.Epoch == right.Epoch && left.ActivityID == right.ActivityID && left.DecisionID == right.DecisionID && left.ActorID == right.ActorID && reflect.DeepEqual(left.EventIDs, right.EventIDs)
}

func findDecisionByOperation(t *testing.T, footprint normalizedHumanStoreFootprint, operation provenance.OperationID) normalizedDecisionFact {
	t.Helper()
	var found normalizedDecisionFact
	count := 0
	for _, decision := range footprint.Decisions {
		if decision.OperationID == operation {
			found = decision
			count++
		}
	}
	if count != 1 {
		t.Fatalf("operation %q has %d canonical decision rows in complete footprint", operation, count)
	}
	return found
}

func assertCommittedOperationKind(t *testing.T, footprint normalizedHumanStoreFootprint, operation provenance.OperationID, kind provenance.CommittedResultKind) {
	t.Helper()
	result, ok := footprint.Operations[string(operation)]
	if !ok || result.Kind != kind {
		t.Fatalf("operation %q normalized result = %+v, want kind %s", operation, result, kind)
	}
}

func assertCommittedAbsent(t *testing.T, footprint normalizedHumanStoreFootprint, operation provenance.OperationID) {
	t.Helper()
	result, ok := footprint.Operations[string(operation)]
	if !ok || result.Present || result.Kind != provenance.CommittedAbsent || result.AnchorJournalID != 0 || len(result.EmittedEvents) != 0 || len(result.ResultSlots) != 0 {
		t.Fatalf("operation %q is not absent in complete footprint: %+v", operation, result)
	}
}

func oracleDecisionID(operation provenance.OperationID) DecisionLedgerEntryID {
	return DecisionLedgerEntryID("decision:" + string(operation))
}

func oracleActivityID(operation provenance.OperationID) provenance.ActivityID {
	return provenance.ActivityID{Namespace: "pasture", UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("activity:"+string(operation)))}
}

func oracleEventContexts(want humanDecisionExpectation, result DecisionResult) []normalizedEventContext {
	contexts := []normalizedEventContext{
		{Kind: provenance.EventContextKindActivity, Identity: result.ActivityID.String()},
		{Kind: provenance.EventContextKindActor, Identity: want.actor.String()},
		{Kind: provenance.EventContextKindTask, Identity: want.subject.String()},
		{Kind: provenance.EventContextKindTask, Identity: want.epoch.String()},
	}
	sort.Slice(contexts, func(i, j int) bool {
		if contexts[i].Kind != contexts[j].Kind {
			return contexts[i].Kind < contexts[j].Kind
		}
		return contexts[i].Identity < contexts[j].Identity
	})
	unique := contexts[:0]
	for _, context := range contexts {
		if len(unique) == 0 || unique[len(unique)-1] != context {
			unique = append(unique, context)
		}
	}
	return unique
}

func oracleDecisionCondition(task provenance.TaskID, kind DecisionKindID, asserted provenance.JournalID) conditionSnapshot {
	return conditionSnapshot{Kind: provenance.ConditionCurrentFact, FactKind: provenance.FactDecision, Task: task.String(), DecisionKind: journalDecisionKind(kind), AssertedJournalID: asserted}
}

func oracleEvidenceCondition(task provenance.TaskID, kind provenance.EvidenceKind, asserted provenance.JournalID, conditionKind provenance.ConditionKind) conditionSnapshot {
	return conditionSnapshot{Kind: conditionKind, FactKind: provenance.FactEvidence, Task: task.String(), EvidenceKind: kind, AssertedJournalID: asserted}
}

func oraclePlanAcceptedBranchFromInput(in PlanUATInput, epoch provenance.TaskID) oraclePlanAcceptedBranch {
	var payload PlanUATPayload
	if in.Payload != nil {
		payload = *in.Payload
	}
	operation := oracleDecisionID(in.Meta.OperationID)
	return oraclePlanAcceptedBranch{
		Snapshot: oraclePlanSnapshot{
			ID: PlanUATDecisionID(operation), UATTaskID: in.Proposal,
			Proposal: DocumentRevisionID(in.Proposal.String()), DecisionEntry: operation,
			InputLedger: DocumentRevisionID(epoch.String()), OutputLedger: DocumentRevisionID(string(in.Meta.OperationID)),
		},
		Interactions: payload.Interactions,
		Feedback:     payload.Feedback,
	}
}

func oracleSchemaDigest(t *testing.T, kind DecisionKindID) DecisionSchemaDigest {
	t.Helper()
	hexByKind := map[DecisionKindID]string{
		DecisionInteractionModeChanged: "7e02eb66a7c3119a89445c23ab8abd13106720c4f7d74997ec3ccf1a8077980b",
		DecisionPlanUATAccepted:        "e91e64ac26c52cd07be3968065af7b88f6f555ea8144c71ccb6002b518ee09c1",
		DecisionImplementationUAT:      "541809202fb1823057f695ea4cb42bd2583b3ed8c0eff9f66a2b148b730a237b",
		DecisionPlanRatified:           "88aa092c97f74e7358a3246931b7aa5b0c9ed3a3df0a45c8a191eaac55dbe40e",
		DecisionLanded:                 "df1f49fb065d6dbf1395731d69f82b11be6a55f5c956696002afa541dcacef05",
	}
	raw, ok := hexByKind[kind]
	if !ok {
		t.Fatalf("oracle has no literal schema digest for decision kind %q", kind)
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("decode literal schema digest for %q: %v", kind, err)
	}
	var schema DecisionSchemaDigest
	copy(schema[:], decoded)
	return schema
}

func decodeOracleJSON(t *testing.T, payload []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode exact oracle JSON: %v; payload=%s", err, payload)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("exact oracle JSON has trailing data: %v; payload=%s", err, payload)
	}
}

func findEvidenceByOperationAndKind(t *testing.T, footprint normalizedHumanStoreFootprint, operation provenance.OperationID, kind provenance.EvidenceKind) normalizedEvidenceFact {
	t.Helper()
	var found normalizedEvidenceFact
	count := 0
	for _, evidence := range footprint.Evidence {
		if evidence.OperationID == operation && evidence.Kind == kind {
			found = evidence
			count++
		}
	}
	if count != 1 {
		t.Fatalf("operation %q has %d evidence rows of kind %q; want exactly one", operation, count, kind)
	}
	return found
}

func oracleEvidencePayload(t *testing.T, want oracleEvidenceExpectation) []byte {
	t.Helper()
	var value any
	switch {
	case want.state != nil && want.reference == nil && want.conditions == nil && want.value == nil:
		value = want.state
	case want.state == nil && want.reference != nil && want.conditions == nil && want.value == nil:
		value = want.reference
	case want.state == nil && want.reference == nil && want.conditions != nil && want.value == nil:
		value = oracleConditionEvidence{Conditions: want.conditions}
	case want.state == nil && want.reference == nil && want.conditions == nil && want.value != nil:
		value = want.value
	default:
		t.Fatalf("invalid oracle evidence expectation: %+v", want)
	}
	payload, err := canonicalJSON(value)
	if err != nil {
		t.Fatalf("encode exact oracle evidence expectation: %v", err)
	}
	return payload
}

func oracleBranch(t *testing.T, encoding DecisionEncoding) any {
	t.Helper()
	switch encoding.Kind {
	case DecisionInteractionModeChanged:
		var branch oracleModeBranch
		decodeOracleJSON(t, encoding.Payload, &branch)
		return branch
	case DecisionPlanUATAccepted:
		var branch oraclePlanAcceptedBranch
		decodeOracleJSON(t, encoding.Payload, &branch)
		return branch
	case DecisionImplementationUAT:
		var branch oracleImplementationUATBranch
		decodeOracleJSON(t, encoding.Payload, &branch)
		return branch
	case DecisionPlanRatified:
		var branch oraclePlanRatifiedBranch
		decodeOracleJSON(t, encoding.Payload, &branch)
		return branch
	case DecisionLanded:
		var branch oracleLandedBranch
		decodeOracleJSON(t, encoding.Payload, &branch)
		return branch
	default:
		t.Fatalf("oracle has no branch decoder for decision kind %q", encoding.Kind)
		return nil
	}
}

func assertHumanDecisionOracleExact(t *testing.T, _ *trackerImpl, footprint normalizedHumanStoreFootprint, want humanDecisionExpectation, result DecisionResult, replayed bool) {
	t.Helper()
	if result.OperationID != want.operation || result.Replayed != replayed || result.Epoch != EpochRootID(want.epoch.String()) || result.DecisionID != oracleDecisionID(want.operation) || result.ActorID != want.actor || result.ActivityID != oracleActivityID(want.operation) {
		t.Fatalf("returned human decision result = %+v; want operation=%s actor=%s replayed=%t epoch=%s decision=%s activity=%s", result, want.operation, want.actor, replayed, want.epoch, oracleDecisionID(want.operation), oracleActivityID(want.operation))
	}
	if len(result.EventIDs) != 1+boolToInt(want.closeTask != (provenance.TaskID{})) {
		t.Fatalf("returned event ids = %v; want reference plus lifecycle-close event count", result.EventIDs)
	}

	decision := findDecisionByOperation(t, footprint, want.operation)
	if decision.TaskID != want.subject || decision.Kind != journalDecisionKind(want.kind) || decision.RawActorID != want.actor.String() || decision.ActorID != want.actor || decision.OperationID != want.operation || !decision.HasProducingOperationID || decision.ProducingOperationID == 0 || len(decision.Contexts) != 0 {
		t.Fatalf("canonical decision row = %+v; want subject=%s kind=%s actor=%s operation=%s, no contexts, and nonzero anchor", decision, want.subject, want.kind, want.actor, want.operation)
	}
	var envelope persistedDecision
	decodeOracleJSON(t, decision.Payload, &envelope)
	if envelope.ID != result.DecisionID || envelope.Epoch != want.epoch.String() || envelope.Subject != want.subject.String() || envelope.Actor != want.actor.String() {
		t.Fatalf("canonical decision envelope = %+v; want epoch=%s id=%s subject=%s actor=%s", envelope, want.epoch, result.DecisionID, want.subject, want.actor)
	}
	encoding := envelope.Decision
	if encoding.Kind != want.kind || encoding.Codec != oracleCanonicalJSONCodec || encoding.Schema != oracleSchemaDigest(t, want.kind) {
		t.Fatalf("canonical decision encoding = %+v; want literal kind=%s codec=%s schema=%x", encoding, want.kind, oracleCanonicalJSONCodec, oracleSchemaDigest(t, want.kind))
	}
	if gotBranch := oracleBranch(t, encoding); !reflect.DeepEqual(gotBranch, want.branch) {
		t.Fatalf("canonical decision branch = %#v; want independent literal branch %#v", gotBranch, want.branch)
	}

	activityCount := 0
	for _, activity := range footprint.Activities {
		if activity.ID != result.ActivityID {
			continue
		}
		activityCount++
		if activity.AgentID != want.actor || activity.Phase != want.phase || activity.Stage != provenance.StageComplete || activity.Notes != want.note {
			t.Fatalf("canonical activity = %+v; want actor=%s phase=%s stage=%s notes=%q", activity, want.actor, want.phase, provenance.StageComplete, want.note)
		}
	}
	if activityCount != 1 {
		t.Fatalf("activity %q appears %d times in complete footprint; want exactly once", result.ActivityID, activityCount)
	}

	expectedCommitted, ok := footprint.ExpectedOperations[string(want.operation)]
	if !ok || expectedCommitted.Kind != provenance.CommittedExact || expectedCommitted.AnchorJournalID == 0 {
		t.Fatalf("independently captured operation %q = %+v; want an exact committed operation with a nonzero anchor", want.operation, expectedCommitted)
	}
	anchor := expectedCommitted.AnchorJournalID
	committed, ok := footprint.Operations[string(want.operation)]
	if !ok || !reflect.DeepEqual(committed, expectedCommitted) || !reflect.DeepEqual(committed.EmittedEvents, result.EventIDs) {
		t.Fatalf("committed operation = %+v; want independently captured complete operation %+v and returned EventIDs=%v", committed, expectedCommitted, result.EventIDs)
	}
	if decision.ProducingOperationID != anchor {
		t.Fatalf("decision producer anchor = %d; want independently captured anchor %d", decision.ProducingOperationID, anchor)
	}
	wantEvidence := append([]oracleEvidenceExpectation(nil), want.evidence...)
	wantEvidence = append(wantEvidence, oracleEvidenceExpectation{kind: preconditionEvidenceKind, task: want.subject, conditions: want.conditions})
	evidenceRows := make([]normalizedEvidenceFact, 0, len(wantEvidence))
	for _, evidence := range footprint.Evidence {
		if evidence.OperationID == want.operation {
			evidenceRows = append(evidenceRows, evidence)
		}
	}
	if len(evidenceRows) != len(wantEvidence) {
		t.Fatalf("operation %q has %d evidence rows; want exact %d", want.operation, len(evidenceRows), len(wantEvidence))
	}
	for i, expected := range wantEvidence {
		got := evidenceRows[i]
		if got.Kind != expected.kind || got.TaskID != expected.task || got.RawActorID != want.actor.String() || got.ActorID != want.actor || got.OperationID != want.operation || !got.HasProducingOperationID || got.ProducingOperationID != anchor || len(got.Payload) == 0 || len(got.Digest) == 0 || len(got.Contexts) != 0 {
			t.Fatalf("evidence row %d = %+v; want exact kind=%s task=%s actor=%s operation=%s anchor=%d and no contexts", i, got, expected.kind, expected.task, want.actor, want.operation, anchor)
		}
		switch {
		case expected.state != nil:
			var state oracleStateEvidence
			decodeOracleJSON(t, got.Payload, &state)
			if !reflect.DeepEqual(state, *expected.state) {
				t.Fatalf("evidence row %d state = %+v; want exact %+v", i, state, *expected.state)
			}
		case expected.reference != nil:
			var reference oracleReferenceEvidence
			decodeOracleJSON(t, got.Payload, &reference)
			if !reflect.DeepEqual(reference, *expected.reference) {
				t.Fatalf("evidence row %d reference = %+v; want exact %+v", i, reference, *expected.reference)
			}
		case expected.conditions != nil:
			var conditions oracleConditionEvidence
			decodeOracleJSON(t, got.Payload, &conditions)
			if !reflect.DeepEqual(conditions.Conditions, expected.conditions) {
				t.Fatalf("evidence row %d conditions = %+v; want exact %+v", i, conditions.Conditions, expected.conditions)
			}
		}
		expectedPayload := oracleEvidencePayload(t, expected)
		digest := sha256.Sum256(expectedPayload)
		if !bytes.Equal(got.Digest, digest[:]) {
			t.Fatalf("evidence row %d digest = %x; want sha256(independent payload)=%x persisted payload=%q", i, got.Digest, digest, got.Payload)
		}
	}

	operationEvents := make([]normalizedTaskEvent, 0, len(result.EventIDs))
	for _, event := range footprint.Events {
		if event.OperationID == want.operation {
			operationEvents = append(operationEvents, event)
		}
	}
	if len(operationEvents) != len(result.EventIDs) {
		t.Fatalf("operation %q has %d normalized emitted events; want exactly %d", want.operation, len(operationEvents), len(result.EventIDs))
	}
	for i, eventID := range result.EventIDs {
		var event normalizedTaskEvent
		found := false
		for _, candidate := range operationEvents {
			if candidate.JournalID == eventID {
				event, found = candidate, true
				break
			}
		}
		if !found || event.RawActorID != want.actor.String() || event.ActorID != want.actor || event.OperationID != want.operation || !event.HasOperationJournalID || event.OperationJournalID != anchor {
			t.Fatalf("returned event %d = %+v; want exact raw/effective actor=%s, operation=%s, anchor=%d", eventID, event, want.actor, want.operation, anchor)
		}
		if i == 0 {
			if event.Kind != FamilyEpochDecisionRecorded.EventKind() || event.TaskID != want.subject {
				t.Fatalf("reference event = %+v; want task=%s kind=%s", event, want.subject, FamilyEpochDecisionRecorded.EventKind())
			}
			if !reflect.DeepEqual(event.ActorContextIDs, []string{want.actor.String()}) || !reflect.DeepEqual(event.Contexts, oracleEventContexts(want, result)) {
				t.Fatalf("reference event contexts = %+v/%+v; want exact actor attribution and independent contexts %+v", event.ActorContextIDs, event.Contexts, oracleEventContexts(want, result))
			}
			var gotPayload struct {
				Epoch    string `json:"epoch"`
				Activity string `json:"activity"`
				Actor    string `json:"actor"`
				Decision string `json:"decision"`
				Kind     string `json:"kind"`
			}
			decodeOracleJSON(t, event.Payload, &gotPayload)
			wantPayload := struct {
				Epoch    string `json:"epoch"`
				Activity string `json:"activity"`
				Actor    string `json:"actor"`
				Decision string `json:"decision"`
				Kind     string `json:"kind"`
			}{want.epoch.String(), result.ActivityID.String(), want.actor.String(), string(result.DecisionID), string(want.kind)}
			if !reflect.DeepEqual(gotPayload, wantPayload) {
				t.Fatalf("reference event payload = %+v; want exact %+v", gotPayload, wantPayload)
			}
		} else {
			if event.Kind != provenance.EventKindTaskClosed || event.TaskID != want.closeTask || !bytes.Equal(event.Payload, []byte("{}")) || len(event.Contexts) != 0 || len(event.ActorContextIDs) != 0 {
				t.Fatalf("lifecycle-close event = %+v; want task=%s kind=%s and exact empty-object payload", event, want.closeTask, provenance.EventKindTaskClosed)
			}
		}
	}

	if len(committed.ResultSlots) != len(want.evidence)+3 {
		t.Fatalf("committed result slots = %+v; want exactly %d ordered slots", committed.ResultSlots, len(want.evidence)+3)
	}
	activitySlot := committed.ResultSlots[0]
	if activitySlot.Slot != activityResultSlot || activitySlot.Kind != provenance.JournalKindActivity || activitySlot.ProducedJournalID == 0 || !activitySlot.HasActivityID || activitySlot.ActivityID != result.ActivityID || activitySlot.HasTaskID {
		t.Fatalf("activity result slot = %+v; want exact activity binding %s", activitySlot, result.ActivityID)
	}
	decisionSlot := committed.ResultSlots[1]
	if decisionSlot.Slot != decisionResultSlot || decisionSlot.Kind != provenance.JournalKindDecision || decisionSlot.ProducedJournalID != decision.JournalID || decisionSlot.HasTaskID || decisionSlot.HasActivityID {
		t.Fatalf("decision result slot = %+v; want exact decision row %d", decisionSlot, decision.JournalID)
	}
	for i, expected := range want.evidence {
		slot := committed.ResultSlots[3+i]
		row := evidenceRows[i]
		if slot.Slot != provenance.ResultSlotID(fmt.Sprintf("evidence-%d", i)) || slot.Kind != provenance.JournalKindEvidence || slot.ProducedJournalID != row.JournalID || slot.HasTaskID || slot.HasActivityID {
			t.Fatalf("evidence result slot %d = %+v; want exact row %d for %s", i, slot, row.JournalID, expected.kind)
		}
	}
	eventSlot := committed.ResultSlots[2]
	if eventSlot.Slot != eventResultSlot || eventSlot.Kind != provenance.JournalKindTaskEvent || eventSlot.ProducedJournalID != result.EventIDs[0] || !eventSlot.HasTaskID || eventSlot.TaskID != want.subject || eventSlot.HasActivityID {
		t.Fatalf("event result slot = %+v; want exact reference event %d for task %s", eventSlot, result.EventIDs[0], want.subject)
	}

	for task, status := range want.statuses {
		if got, ok := footprint.Statuses[task]; !ok || got != status {
			t.Fatalf("task %s status = %v (present=%t); want exact %v", task, got, ok, status)
		}
	}
	if len(footprint.Statuses) != len(want.statuses) {
		t.Fatalf("status footprint = %+v; want exact task set %+v", footprint.Statuses, want.statuses)
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func assertBarrierWinnerDelta(t *testing.T, before, after normalizedHumanStoreFootprint, want humanDecisionExpectation, winner DecisionResult, loser provenance.OperationID) {
	t.Helper()
	assertHumanDecisionOracleExact(t, nil, after, want, winner, false)
	assertCommittedAbsent(t, after, loser)
	for _, decision := range after.Decisions {
		if decision.OperationID == loser {
			t.Fatalf("loser operation %q left decision partial: %+v", loser, decision)
		}
	}
	for _, evidence := range after.Evidence {
		if evidence.OperationID == loser {
			t.Fatalf("loser operation %q left evidence partial: %+v", loser, evidence)
		}
	}
	for _, event := range after.Events {
		if event.OperationID == loser {
			t.Fatalf("loser operation %q left task-event partial: %+v", loser, event)
		}
	}
	loserActivity := oracleActivityID(loser)
	for _, activity := range after.Activities {
		if activity.ID == loserActivity {
			t.Fatalf("loser operation %q left deterministic Activity partial: %+v", loser, activity)
		}
	}
	if beforeResult, ok := before.Operations[string(loser)]; ok && beforeResult.Present {
		t.Fatalf("barrier loser %q was already present before the race: %+v", loser, beforeResult)
	}
	residual := subtractValidatedWinner(t, before, after, validatedWinnerDeltaFromOracle(t, before, after, want, winner))
	assertHumanDecisionStoreFootprintEqual(t, before, residual)
	for task, beforeStatus := range before.Statuses {
		wantStatus, ok := want.statuses[task]
		if !ok {
			t.Fatalf("winner expectation omits pre-existing task %s status", task)
		}
		if wantStatus != beforeStatus && task != want.closeTask.String() {
			t.Fatalf("status task %s changed from %s to %s outside winner close transition", task, beforeStatus, wantStatus)
		}
	}
}

type validatedWinnerDelta struct {
	decision       normalizedDecisionFact
	evidence       []normalizedEvidenceFact
	events         []normalizedTaskEvent
	activity       normalizedActivity
	operation      provenance.OperationID
	operationValue normalizedCommittedOperation
	status         *validatedStatusTransition
}

type validatedStatusTransition struct {
	task          string
	before        provenance.Status
	beforePresent bool
	after         provenance.Status
}

// validatedWinnerDeltaFromOracle is intentionally called only after
// assertHumanDecisionOracleExact has accepted every winner binding. It records
// full normalized values, not an operation-wide predicate, for once-only
// subtraction from a barrier footprint.
func validatedWinnerDeltaFromOracle(t *testing.T, before, after normalizedHumanStoreFootprint, want humanDecisionExpectation, winner DecisionResult) validatedWinnerDelta {
	t.Helper()
	delta := validatedWinnerDelta{decision: findDecisionByOperation(t, after, want.operation), operation: want.operation}
	for _, evidence := range after.Evidence {
		if evidence.OperationID == want.operation {
			delta.evidence = append(delta.evidence, evidence)
		}
	}
	if len(delta.evidence) != len(want.evidence)+1 {
		t.Fatalf("validated winner %q has %d evidence rows; want exactly %d including preconditions", want.operation, len(delta.evidence), len(want.evidence)+1)
	}
	for _, eventID := range winner.EventIDs {
		count := 0
		for _, event := range after.Events {
			if event.JournalID == eventID {
				delta.events = append(delta.events, event)
				count++
			}
		}
		if count != 1 {
			t.Fatalf("validated winner %q event %d appears %d times", want.operation, eventID, count)
		}
	}
	for _, activity := range after.Activities {
		if activity.ID == winner.ActivityID {
			if delta.activity.ID != (provenance.ActivityID{}) {
				t.Fatalf("validated winner %q has duplicate activity %s", want.operation, winner.ActivityID)
			}
			delta.activity = activity
		}
	}
	if delta.activity.ID == (provenance.ActivityID{}) {
		t.Fatalf("validated winner %q is missing activity %s", want.operation, winner.ActivityID)
	}
	operation, ok := after.Operations[string(want.operation)]
	if !ok {
		t.Fatalf("validated winner %q is missing its committed operation", want.operation)
	}
	delta.operationValue = operation
	if want.closeTask != (provenance.TaskID{}) {
		task := want.closeTask.String()
		beforeStatus, beforePresent := before.Statuses[task]
		afterStatus, afterPresent := after.Statuses[task]
		wantStatus, wanted := want.statuses[task]
		if !afterPresent || !wanted || afterStatus != wantStatus {
			t.Fatalf("validated winner %q close status %q = %s (present=%t); want %s", want.operation, task, afterStatus, afterPresent, wantStatus)
		}
		delta.status = &validatedStatusTransition{task: task, before: beforeStatus, beforePresent: beforePresent, after: afterStatus}
	}
	return delta
}

func subtractValidatedWinner(t *testing.T, before, after normalizedHumanStoreFootprint, delta validatedWinnerDelta) normalizedHumanStoreFootprint {
	t.Helper()
	residual := cloneHumanStoreFootprint(after)
	residual.Decisions = subtractExactOne(t, residual.Decisions, delta.decision, "decision")
	for _, evidence := range delta.evidence {
		residual.Evidence = subtractExactOne(t, residual.Evidence, evidence, "evidence")
	}
	for _, event := range delta.events {
		residual.Events = subtractExactOne(t, residual.Events, event, "task event")
	}
	residual.Activities = subtractExactOne(t, residual.Activities, delta.activity, "activity")

	storedOperation, found := residual.Operations[string(delta.operation)]
	if !found || !reflect.DeepEqual(storedOperation, delta.operationValue) {
		t.Fatalf("validated winner %q committed operation is missing or changed: %+v", delta.operation, storedOperation)
	}
	if beforeOperation, found := before.Operations[string(delta.operation)]; found {
		residual.Operations[string(delta.operation)] = beforeOperation
	} else {
		delete(residual.Operations, string(delta.operation))
	}
	if beforeExpected, found := before.ExpectedOperations[string(delta.operation)]; found {
		residual.ExpectedOperations[string(delta.operation)] = beforeExpected
	} else {
		delete(residual.ExpectedOperations, string(delta.operation))
	}
	if delta.status != nil {
		if got, found := residual.Statuses[delta.status.task]; !found || got != delta.status.after {
			t.Fatalf("validated winner status transition for %q is missing or changed: %s (present=%t), want %s", delta.status.task, got, found, delta.status.after)
		}
		if delta.status.beforePresent {
			residual.Statuses[delta.status.task] = delta.status.before
		} else {
			delete(residual.Statuses, delta.status.task)
		}
	}
	return residual
}

func subtractExactOne[T any](t *testing.T, values []T, expected T, label string) []T {
	t.Helper()
	index := -1
	for i, value := range values {
		if reflect.DeepEqual(value, expected) {
			if index >= 0 {
				t.Fatalf("validated winner %s appears more than once", label)
			}
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("validated winner %s is missing from the after footprint", label)
	}
	if len(values) == 1 {
		return nil
	}
	return append(values[:index:index], values[index+1:]...)
}

func cloneHumanStoreFootprint(footprint normalizedHumanStoreFootprint) normalizedHumanStoreFootprint {
	clone := footprint
	clone.Decisions = append([]normalizedDecisionFact(nil), footprint.Decisions...)
	clone.Evidence = append([]normalizedEvidenceFact(nil), footprint.Evidence...)
	clone.Events = append([]normalizedTaskEvent(nil), footprint.Events...)
	clone.Activities = append([]normalizedActivity(nil), footprint.Activities...)
	clone.Statuses = make(map[string]provenance.Status, len(footprint.Statuses))
	for key, value := range footprint.Statuses {
		clone.Statuses[key] = value
	}
	clone.Operations = make(map[string]normalizedCommittedOperation, len(footprint.Operations))
	for key, value := range footprint.Operations {
		clone.Operations[key] = value
	}
	clone.ExpectedOperations = make(map[string]normalizedCommittedOperation, len(footprint.ExpectedOperations))
	for key, value := range footprint.ExpectedOperations {
		clone.ExpectedOperations[key] = value
	}
	return clone
}

func TestEpochHumanServiceExactWinnerSubtractionRetainsUnexpectedRows(t *testing.T) {
	winnerOperation := provenance.OperationID("winner")
	winnerDecision := normalizedDecisionFact{JournalID: 1, OperationID: winnerOperation, ProducingOperationID: 10, HasProducingOperationID: true}
	winnerEvidence := normalizedEvidenceFact{JournalID: 2, OperationID: winnerOperation, Kind: "pasture.expected.v1", ProducingOperationID: 10, HasProducingOperationID: true}
	winnerPrecondition := normalizedEvidenceFact{JournalID: 3, OperationID: winnerOperation, Kind: preconditionEvidenceKind, ProducingOperationID: 10, HasProducingOperationID: true}
	winnerEvent := normalizedTaskEvent{JournalID: 4, OperationID: winnerOperation, OperationJournalID: 10, HasOperationJournalID: true}
	winnerActivity := normalizedActivity{ID: oracleActivityID(winnerOperation)}
	before := normalizedHumanStoreFootprint{
		Statuses:           map[string]provenance.Status{"winner-task": provenance.StatusOpen},
		Operations:         map[string]normalizedCommittedOperation{},
		ExpectedOperations: map[string]normalizedCommittedOperation{},
	}
	operation := normalizedCommittedOperation{Present: true, Kind: provenance.CommittedExact, AnchorJournalID: 10}
	after := cloneHumanStoreFootprint(before)
	after.Decisions = []normalizedDecisionFact{winnerDecision, {JournalID: 5, OperationID: winnerOperation, Kind: "pasture.unknown.decision.v1", ProducingOperationID: 10, HasProducingOperationID: true}}
	after.Evidence = []normalizedEvidenceFact{winnerEvidence, winnerPrecondition}
	after.Events = []normalizedTaskEvent{winnerEvent, {JournalID: 6, OperationID: winnerOperation, OperationJournalID: 99, HasOperationJournalID: true, Contexts: []normalizedEventContext{{Kind: provenance.EventContextKindTask, Identity: "unexpected-context"}}}}
	after.Activities = []normalizedActivity{winnerActivity}
	after.Statuses["winner-task"] = provenance.StatusClosed
	after.Operations[string(winnerOperation)] = operation
	after.ExpectedOperations[string(winnerOperation)] = operation
	residual := subtractValidatedWinner(t, before, after, validatedWinnerDelta{
		decision: winnerDecision, evidence: []normalizedEvidenceFact{winnerEvidence, winnerPrecondition}, events: []normalizedTaskEvent{winnerEvent}, activity: winnerActivity,
		operation: winnerOperation, operationValue: operation,
		status: &validatedStatusTransition{task: "winner-task", before: provenance.StatusOpen, beforePresent: true, after: provenance.StatusClosed},
	})
	if len(residual.Decisions) != 1 || residual.Decisions[0].Kind != "pasture.unknown.decision.v1" || len(residual.Events) != 1 || residual.Events[0].OperationJournalID != 99 {
		t.Fatalf("exact subtraction erased unexpected rows: %+v", residual)
	}
	if reflect.DeepEqual(before, residual) {
		t.Fatal("exact subtraction made a footprint with unknown/wrong-anchor rows equal to its before snapshot")
	}
}
