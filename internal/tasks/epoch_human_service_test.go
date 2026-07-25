package tasks

import (
	"context"
	"encoding/json"
	"errors"
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
	service := newHumanTestService(t, tracker)

	mode, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("set interaction mode: %v", err)
	}
	if mode.Replayed || mode.ActorID != human.ID || mode.DecisionID == "" || mode.ActivityID == (provenance.ActivityID{}) || len(mode.EventIDs) != 1 {
		t.Fatalf("mode result missing exact material bindings: %+v", mode)
	}
	assertHumanDecisionActivity(t, tracker, mode.ActivityID, human.ID, provenance.PhaseRequest)
	assertCanonicalModeDecision(t, tracker, epoch, mode, InteractionAFK)
	modeScope := humanStoreScopeFor([]provenance.TaskID{epoch}, []provenance.OperationID{"human-mode-1"})
	modeBeforeRetry := humanDecisionStoreFootprint(t, tracker, modeScope)
	replayed, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil || !replayed.Replayed || !sameDecisionResultBindings(replayed, mode) {
		t.Fatalf("exact mode replay = %+v, %v; want original bindings", replayed, err)
	}
	assertHumanDecisionStoreFootprintEqual(t, modeBeforeRetry, humanDecisionStoreFootprint(t, tracker, modeScope))
	if _, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionNormal, Actor: AssertedHumanActor{ActorID: human.ID}}); !errors.Is(err, provenance.ErrOperationConflict) {
		t.Fatalf("changed mode under same operation id error = %v, want operation conflict", err)
	}

	if err := tracker.Close(); err != nil {
		t.Fatalf("close before reopen: %v", err)
	}
	tracker = openHumanTestTracker(t, db)
	defer tracker.Close()
	service = newHumanTestService(t, tracker)
	cursor, err := service.ShowInteractionMode(ctx, EpochRootID(epoch.String()))
	if err != nil || cursor.Mode != InteractionAFK || cursor.Entry == nil || *cursor.Entry != mode.DecisionID {
		t.Fatalf("reopened mode cursor = %+v, %v", cursor, err)
	}
	modeBeforeReopenReplay := humanDecisionStoreFootprint(t, tracker, modeScope)
	reopenedReplay, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil || !reopenedReplay.Replayed || !sameDecisionResultBindings(reopenedReplay, mode) {
		t.Fatalf("reopened direct Apply replay = %+v, %v; want original bindings", reopenedReplay, err)
	}
	assertHumanDecisionStoreFootprintEqual(t, modeBeforeReopenReplay, humanDecisionStoreFootprint(t, tracker, modeScope))

	planInput := PlanUATInput{Meta: CommandMeta{OperationID: "human-plan-uat-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}}
	planDraft, err := service.(*epochHumanService).policy.DraftPlanUAT(PlanUATDecision{Snapshot: servicePlanSnapshot(planInput, epoch), ReportedVerdict: PlanUATAccepted})
	if err != nil {
		t.Fatalf("draft Plan UAT oracle: %v", err)
	}
	planUAT, err := service.RecordPlanUAT(ctx, planInput)
	if err != nil {
		t.Fatalf("record Plan UAT: %v", err)
	}
	seedAcceptedReview(t, tracker, epoch, proposal, "review-round-1")

	ratifyInput := RatifyPlanInput{Meta: CommandMeta{OperationID: "human-ratify-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "review-round-1", PlanUAT: planUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}
	ratifyDraft, err := service.(*epochHumanService).policy.DraftPlanRatified(PlanRatified{Proposal: proposal.String(), ReviewRound: ratifyInput.ReviewRound, PlanUAT: planUAT.DecisionID})
	if err != nil {
		t.Fatalf("draft ratification oracle: %v", err)
	}
	ratified, err := service.RatifyPlan(ctx, ratifyInput)
	if err != nil {
		t.Fatalf("ratify plan: %v", err)
	}

	implInput := ImplementationUATInput{Meta: CommandMeta{OperationID: "human-impl-uat-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}}
	implDraft, err := service.(*epochHumanService).policy.DraftImplementationUAT(ImplUATAccepted, ImplUATPayload{})
	if err != nil {
		t.Fatalf("draft Implementation UAT oracle: %v", err)
	}
	implUAT, err := service.RecordImplementationUAT(ctx, implInput)
	if err != nil {
		t.Fatalf("record Implementation UAT: %v", err)
	}

	landInput := LandInput{Meta: CommandMeta{OperationID: "human-land-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: implUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}
	landDraft, err := service.(*epochHumanService).policy.DraftLanded(EpochLanded{Candidate: landInput.Candidate, ImplementationUAT: implUAT.DecisionID})
	if err != nil {
		t.Fatalf("draft landing oracle: %v", err)
	}
	landed, err := service.Land(ctx, landInput)
	if err != nil {
		t.Fatalf("land: %v", err)
	}

	allScope := humanStoreScopeFor(
		[]provenance.TaskID{epoch, proposal, candidate},
		[]provenance.OperationID{"human-mode-1", "human-plan-uat-1", "human-ratify-1", "human-impl-uat-1", "human-land-1"},
	)
	nonModeCases := []struct {
		name       string
		operation  provenance.OperationID
		subject    provenance.TaskID
		phase      provenance.Phase
		kind       DecisionKindID
		draft      DecisionDraft
		status     map[string]provenance.Status
		run        func() (DecisionResult, error)
		retry      func() (DecisionResult, error)
		wantResult DecisionResult
	}{
		{
			name: "plan-uat", operation: planInput.Meta.OperationID, subject: proposal, phase: provenance.PhasePlanUAT,
			kind: DecisionPlanUATAccepted, draft: planDraft, run: func() (DecisionResult, error) { return planUAT, nil },
			retry:  func() (DecisionResult, error) { return service.RecordPlanUAT(ctx, planInput) },
			status: map[string]provenance.Status{epoch.String(): provenance.StatusClosed, proposal.String(): provenance.StatusClosed, candidate.String(): provenance.StatusOpen},
		},
		{
			name: "ratify", operation: ratifyInput.Meta.OperationID, subject: proposal, phase: provenance.PhaseRatify,
			kind: DecisionPlanRatified, draft: ratifyDraft, run: func() (DecisionResult, error) { return ratified, nil },
			retry:  func() (DecisionResult, error) { return service.RatifyPlan(ctx, ratifyInput) },
			status: map[string]provenance.Status{epoch.String(): provenance.StatusClosed, proposal.String(): provenance.StatusClosed, candidate.String(): provenance.StatusOpen},
		},
		{
			name: "implementation-uat", operation: implInput.Meta.OperationID, subject: candidate, phase: provenance.PhaseImplUAT,
			kind: DecisionImplementationUAT, draft: implDraft, run: func() (DecisionResult, error) { return implUAT, nil },
			retry:  func() (DecisionResult, error) { return service.RecordImplementationUAT(ctx, implInput) },
			status: map[string]provenance.Status{epoch.String(): provenance.StatusClosed, proposal.String(): provenance.StatusClosed, candidate.String(): provenance.StatusOpen},
		},
		{
			name: "land", operation: landInput.Meta.OperationID, subject: epoch, phase: provenance.PhaseLanding,
			kind: DecisionLanded, draft: landDraft, run: func() (DecisionResult, error) { return landed, nil },
			retry:  func() (DecisionResult, error) { return service.Land(ctx, landInput) },
			status: map[string]provenance.Status{epoch.String(): provenance.StatusClosed, proposal.String(): provenance.StatusClosed, candidate.String(): provenance.StatusOpen},
		},
	}
	for _, tc := range nonModeCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.run()
			if err != nil {
				t.Fatal(err)
			}
			before := humanDecisionStoreFootprint(t, tracker, allScope)
			retry, err := tc.retry()
			if err != nil || !retry.Replayed || !sameDecisionResultBindings(retry, result) {
				t.Fatalf("exact %s retry = %+v, %v; want identical bindings", tc.name, retry, err)
			}
			after := humanDecisionStoreFootprint(t, tracker, allScope)
			assertHumanDecisionStoreFootprintEqual(t, before, after)
			assertHumanDecisionOracle(t, tracker, after, tc.operation, result, epoch, tc.subject, human.ID, tc.phase, tc.kind, tc.draft.encoding(), tc.status)
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
			assertHumanDecisionStoreFootprintEqual(t, beforeReopen, humanDecisionStoreFootprint(t, tracker, allScope))
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
	barrier.after = func() error {
		_, err := winner.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "mode-winner"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
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
	assertCompleteOperation(t, after, "mode-winner", provenance.TaskID(epoch))
	assertCommittedAbsent(t, after, "mode-loser")
	if reflect.DeepEqual(before, after) {
		t.Fatal("stale mode barrier did not retain the complete winner footprint")
	}
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
		assertCompleteOperation(t, after, "race-ratify-winner", proposal)
		assertCommittedAbsent(t, after, "race-plan-loser")
		if winnerResult.DecisionID == "" || reflect.DeepEqual(before, after) {
			t.Fatalf("Plan UAT barrier footprint = before=%+v after=%+v; want one complete winner", before, after)
		}
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
		assertCompleteOperation(t, after, "race-land-winner", epoch)
		assertCommittedAbsent(t, after, "race-impl-loser")
		if winnerResult.DecisionID == "" || reflect.DeepEqual(before, after) {
			t.Fatalf("Implementation UAT barrier footprint = before=%+v after=%+v; want one complete winner", before, after)
		}
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

func assertHumanDecisionActivity(t *testing.T, tracker *trackerImpl, id provenance.ActivityID, actor provenance.ActorID, phase provenance.Phase) {
	t.Helper()
	activities, err := tracker.Activities(&actor)
	if err != nil {
		t.Fatalf("read persisted activities: %v", err)
	}
	for _, activity := range activities {
		if activity.ID == id {
			if activity.AgentID != actor || activity.Phase != phase || activity.Stage != provenance.StageComplete {
				t.Fatalf("persisted activity = %+v, want actor=%s phase=%s stage=%s", activity, actor, phase, provenance.StageComplete)
			}
			return
		}
	}
	t.Fatalf("real activity %q was not persisted for actor %q: %+v", id, actor, activities)
}

func assertCanonicalModeDecision(t *testing.T, tracker *trackerImpl, epoch provenance.TaskID, result DecisionResult, want InteractionMode) {
	t.Helper()
	facts, err := tracker.Journal().Facts().QueryDecisions(provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: epoch}}, Kinds: []provenance.DecisionKind{journalDecisionKind(DecisionInteractionModeChanged)}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}})
	if err != nil || len(facts.Rows) != 1 {
		t.Fatalf("query canonical mode decision: rows=%+v err=%v", facts.Rows, err)
	}
	var decision persistedDecision
	if err := json.Unmarshal(facts.Rows[0].Payload, &decision); err != nil {
		t.Fatalf("decode canonical mode decision: %v", err)
	}
	var changed InteractionModeChanged
	if err := json.Unmarshal(decision.Decision.Payload, &changed); err != nil || decision.ID != result.DecisionID || decision.Actor != result.ActorID.String() || changed.To != want {
		t.Fatalf("canonical mode decision = %+v changed=%+v err=%v", decision, changed, err)
	}
	events, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{epoch}, EventKinds: []provenance.EventKind{FamilyEpochDecisionRecorded.EventKind()}, Limit: provenance.MaxFactPageSize})
	if err != nil || len(events.Events) != 1 {
		t.Fatalf("query lifecycle references: events=%+v err=%v", events.Events, err)
	}
	var event map[string]json.RawMessage
	if err := json.Unmarshal(events.Events[0].Payload, &event); err != nil || event["detail"] != nil {
		t.Fatalf("lifecycle event duplicated decision authority: payload=%s err=%v", events.Events[0].Payload, err)
	}
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
	JournalID            provenance.JournalID
	TaskID               provenance.TaskID
	Kind                 provenance.DecisionKind
	ActorID              provenance.ActorID
	OperationID          provenance.OperationID
	RecordedAt           time.Time
	Payload              []byte
	ProducingOperationID provenance.JournalID
}

type normalizedEvidenceFact struct {
	JournalID            provenance.JournalID
	TaskID               provenance.TaskID
	Kind                 provenance.EvidenceKind
	ActorID              provenance.ActorID
	OperationID          provenance.OperationID
	Digest               []byte
	Payload              []byte
	RecordedAt           time.Time
	ProducingOperationID provenance.JournalID
	Conditions           []conditionSnapshot
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
	JournalID          provenance.JournalID
	TaskID             provenance.TaskID
	Kind               provenance.EventKind
	ActorID            provenance.ActorID
	OperationID        provenance.OperationID
	OperationJournalID provenance.JournalID
	RecordedAt         time.Time
	Payload            []byte
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
	Decisions  []normalizedDecisionFact
	Evidence   []normalizedEvidenceFact
	Activities []normalizedActivity
	Events     []normalizedTaskEvent
	Statuses   map[string]provenance.Status
	Operations map[string]normalizedCommittedOperation
}

func humanDecisionStoreFootprint(t *testing.T, tracker *trackerImpl, scope humanStoreScope) normalizedHumanStoreFootprint {
	t.Helper()
	footprint := normalizedHumanStoreFootprint{Statuses: make(map[string]provenance.Status), Operations: make(map[string]normalizedCommittedOperation)}
	decisionKinds := make([]provenance.DecisionKind, 0, 7)
	for _, kind := range []DecisionKindID{DecisionInteractionModeChanged, DecisionPlanUATAccepted, DecisionPlanUATChangesRequested, DecisionPlanUATDeferredByAFK, DecisionImplementationUAT, DecisionPlanRatified, DecisionLanded} {
		decisionKinds = append(decisionKinds, journalDecisionKind(kind))
	}
	evidenceKinds := []provenance.EvidenceKind{"pasture.epoch.subject.v1", "pasture.review.round.v1", "pasture.plan-uat.decision.v1", "pasture.implementation-uat.decision.v1", planSubjectEvidenceKind, candidateEvidenceKind, preconditionEvidenceKind}
	for _, task := range scope.tasks {
		decisionQuery := provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: task}}, Kinds: decisionKinds, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
		for {
			page, err := tracker.Journal().Facts().QueryDecisions(decisionQuery)
			if err != nil {
				t.Fatalf("read complete decision footprint for %s: %v", task, err)
			}
			for _, row := range page.Rows {
				footprint.Decisions = append(footprint.Decisions, normalizedDecisionFact{
					JournalID: row.JournalID, TaskID: valueTaskID(row.TaskID), Kind: row.DecisionKind,
					ActorID: row.EffectiveActorID, OperationID: row.ProducingOperationID,
					RecordedAt: row.RecordedAt, Payload: append([]byte(nil), row.Payload...),
					ProducingOperationID: row.ProducingOperationJournalID,
				})
			}
			if page.Next == nil {
				break
			}
			decisionQuery.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
			decisionQuery.Page.AfterJournalID = page.Next.AfterJournalID
		}
		evidenceQuery := provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: task}}, Kinds: evidenceKinds, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}}
		for {
			page, err := tracker.Journal().Facts().QueryEvidence(evidenceQuery)
			if err != nil {
				t.Fatalf("read complete evidence footprint for %s: %v", task, err)
			}
			for _, row := range page.Rows {
				normalized := normalizedEvidenceFact{
					JournalID: row.JournalID, TaskID: valueTaskID(row.TaskID), Kind: row.EvidenceKind,
					ActorID: row.EffectiveActorID, OperationID: row.ProducingOperationID,
					Digest: append([]byte(nil), row.ContentDigest...), Payload: append([]byte(nil), row.Payload...),
					RecordedAt: row.RecordedAt, ProducingOperationID: row.ProducingOperationJournalID,
				}
				if row.EvidenceKind == preconditionEvidenceKind {
					var conditions conditionEvidence
					if err := json.Unmarshal(row.Payload, &conditions); err != nil {
						t.Fatalf("decode complete precondition footprint %d: %v", row.JournalID, err)
					}
					normalized.Conditions = append([]conditionSnapshot(nil), conditions.Conditions...)
				}
				footprint.Evidence = append(footprint.Evidence, normalized)
			}
			if page.Next == nil {
				break
			}
			evidenceQuery.Page.SnapshotMaxJournalID = page.Next.SnapshotMaxJournalID
			evidenceQuery.Page.AfterJournalID = page.Next.AfterJournalID
		}
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
		footprint.Operations[string(operation)] = normalized
	}

	query := provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: append([]provenance.TaskID(nil), scope.tasks...), Limit: provenance.MaxFactPageSize}
	operationByEvent := make(map[provenance.JournalID]provenance.OperationID)
	for operation, result := range footprint.Operations {
		for _, event := range result.EmittedEvents {
			operationByEvent[event] = provenance.OperationID(operation)
		}
	}
	for {
		page, err := tracker.Journal().QueryTaskEvents(query)
		if err != nil {
			t.Fatalf("read complete event footprint: %v", err)
		}
		for _, row := range page.Events {
			operation := operationByEvent[row.JournalID]
			operationJournalID := provenance.JournalID(0)
			if row.ProducedByOperationJournalID != nil {
				operationJournalID = *row.ProducedByOperationJournalID
			}
			footprint.Events = append(footprint.Events, normalizedTaskEvent{
				JournalID: row.JournalID, TaskID: row.TaskID, Kind: row.EventKind, ActorID: eventActor(row),
				OperationID: operation, OperationJournalID: operationJournalID, RecordedAt: row.RecordedAt,
				Payload: append([]byte(nil), row.Payload...),
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

func valueTaskID(id *provenance.TaskID) provenance.TaskID {
	if id == nil {
		return provenance.TaskID{}
	}
	return *id
}

func eventActor(row provenance.TaskEventRow) provenance.ActorID {
	for _, context := range row.Contexts {
		if context.Kind() != provenance.EventContextKindActor {
			continue
		}
		encoded, err := json.Marshal(context)
		if err != nil {
			continue
		}
		var value struct {
			Identity string `json:"identity"`
		}
		if json.Unmarshal(encoded, &value) == nil {
			if actor, err := provenance.ParseActorID(value.Identity); err == nil {
				return actor
			}
		}
	}
	return row.ActorID
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
	if !ok || result.Present || result.Kind != provenance.CommittedAbsent || len(result.EmittedEvents) != 0 || len(result.ResultSlots) != 0 {
		t.Fatalf("operation %q is not absent in complete footprint: %+v", operation, result)
	}
}

func assertCompleteOperation(t *testing.T, footprint normalizedHumanStoreFootprint, operation provenance.OperationID, subject provenance.TaskID) {
	t.Helper()
	assertCommittedOperationKind(t, footprint, operation, provenance.CommittedExact)
	decision := findDecisionByOperation(t, footprint, operation)
	if decision.TaskID != subject || len(decision.Payload) == 0 {
		t.Fatalf("operation %q decision = %+v; want subject %s and canonical payload", operation, decision, subject)
	}
	result := footprint.Operations[string(operation)]
	if len(result.EmittedEvents) == 0 || len(result.ResultSlots) < 4 {
		t.Fatalf("operation %q committed result = %+v; want all event, evidence, and decision/activity slots", operation, result)
	}
	var activityID provenance.ActivityID
	var eventID provenance.JournalID
	for _, slot := range result.ResultSlots {
		switch slot.Slot {
		case activityResultSlot:
			if !slot.HasActivityID {
				t.Fatalf("operation %q activity result slot has no activity binding", operation)
			}
			activityID = slot.ActivityID
		case eventResultSlot:
			eventID = slot.ProducedJournalID
		}
	}
	if activityID == (provenance.ActivityID{}) || eventID == 0 {
		t.Fatalf("operation %q result slots = %+v; want real activity and event bindings", operation, result.ResultSlots)
	}
	activityFound := false
	for _, activity := range footprint.Activities {
		if activity.ID == activityID {
			activityFound = true
			break
		}
	}
	if !activityFound {
		t.Fatalf("operation %q activity %q is absent from complete activity footprint", operation, activityID)
	}
	eventFound := false
	for _, event := range footprint.Events {
		if event.JournalID == eventID && event.TaskID == subject {
			eventFound = true
			break
		}
	}
	if !eventFound {
		t.Fatalf("operation %q reference event %d is absent from complete event footprint", operation, eventID)
	}
}

func assertHumanDecisionOracle(t *testing.T, tracker *trackerImpl, footprint normalizedHumanStoreFootprint, operation provenance.OperationID, result DecisionResult, epoch, subject provenance.TaskID, actor provenance.ActorID, phase provenance.Phase, kind DecisionKindID, expected DecisionEncoding, statuses map[string]provenance.Status) {
	t.Helper()
	if result.OperationID != operation || result.Epoch != EpochRootID(epoch.String()) || result.ActorID != actor || result.DecisionID != decisionIDForOperation(operation) || result.ActivityID == (provenance.ActivityID{}) || len(result.EventIDs) == 0 {
		t.Fatalf("returned human decision result = %+v; want operation/epoch/decision/actor and complete bindings", result)
	}
	decision := findDecisionByOperation(t, footprint, operation)
	if decision.TaskID != subject || decision.Kind != journalDecisionKind(kind) || decision.ActorID != actor || decision.OperationID != operation {
		t.Fatalf("canonical decision row = %+v; want subject=%s kind=%s actor=%s operation=%s", decision, subject, kind, actor, operation)
	}
	var envelope persistedDecision
	if err := json.Unmarshal(decision.Payload, &envelope); err != nil {
		t.Fatalf("decode canonical decision envelope: %v", err)
	}
	if envelope.ID != result.DecisionID || envelope.Epoch != epoch.String() || envelope.Subject != subject.String() || envelope.Actor != actor.String() || !sameDecisionEncoding(envelope.Decision, expected) {
		t.Fatalf("canonical decision envelope = %+v; want epoch=%s subject=%s actor=%s decision=%+v", envelope, epoch, subject, actor, expected)
	}

	activityFound := false
	for _, activity := range footprint.Activities {
		if activity.ID == result.ActivityID {
			activityFound = true
			if activity.AgentID != actor || activity.Phase != phase || activity.Stage != provenance.StageComplete {
				t.Fatalf("canonical activity = %+v; want actor=%s phase=%s complete", activity, actor, phase)
			}
		}
	}
	if !activityFound {
		t.Fatalf("returned activity %q is absent from complete activity footprint", result.ActivityID)
	}

	committed, ok := footprint.Operations[string(operation)]
	if !ok || committed.Kind != provenance.CommittedExact || !reflect.DeepEqual(committed.EmittedEvents, result.EventIDs) {
		t.Fatalf("committed operation = %+v; want exact result and EventIDs=%v", committed, result.EventIDs)
	}
	if len(committed.ResultSlots) < 4 {
		t.Fatalf("committed result slots = %+v; want decision/activity/event and evidence slots", committed.ResultSlots)
	}
	var referenceEventID provenance.JournalID
	var decisionSlotFound, activitySlotFound, eventSlotFound bool
	for _, slot := range committed.ResultSlots {
		switch slot.Slot {
		case decisionResultSlot:
			if slot.Kind != provenance.JournalKindDecision || slot.ProducedJournalID != decision.JournalID || slot.HasTaskID || slot.HasActivityID {
				t.Fatalf("decision result slot = %+v; want canonical decision row %d", slot, decision.JournalID)
			}
			decisionSlotFound = true
		case activityResultSlot:
			if slot.Kind != provenance.JournalKindActivity || !slot.HasActivityID || slot.ActivityID != result.ActivityID || slot.HasTaskID {
				t.Fatalf("activity result slot = %+v; want returned activity %s", slot, result.ActivityID)
			}
			activitySlotFound = true
		case eventResultSlot:
			if slot.Kind != provenance.JournalKindTaskEvent || !slot.HasTaskID || slot.TaskID != subject || slot.HasActivityID {
				t.Fatalf("event result slot = %+v; want subject %s", slot, subject)
			}
			referenceEventID = slot.ProducedJournalID
			eventSlotFound = true
		default:
			if slot.Kind != provenance.JournalKindEvidence || slot.ProducedJournalID == 0 || slot.HasTaskID || slot.HasActivityID {
				t.Fatalf("unexpected persisted evidence result slot = %+v", slot)
			}
		}
	}
	if !decisionSlotFound || !activitySlotFound || !eventSlotFound || referenceEventID == 0 {
		t.Fatalf("committed result omitted a required decision/activity/reference-event binding: %+v", committed.ResultSlots)
	}

	for _, eventID := range result.EventIDs {
		found := false
		for _, event := range footprint.Events {
			if event.JournalID == eventID {
				found = true
				if event.TaskID != subject || event.OperationID != operation {
					t.Fatalf("returned event row = %+v; want subject=%s operation=%s", event, subject, operation)
				}
				break
			}
		}
		if !found {
			t.Fatalf("returned EventID %d is absent from complete event footprint", eventID)
		}
	}
	var reference normalizedTaskEvent
	foundReference := false
	for _, event := range footprint.Events {
		if event.JournalID == referenceEventID {
			reference, foundReference = event, true
			break
		}
	}
	if !foundReference || reference.Kind != FamilyEpochDecisionRecorded.EventKind() {
		t.Fatalf("reference event = %+v; want %s", reference, FamilyEpochDecisionRecorded.EventKind())
	}
	var referencePayload struct {
		Epoch    string `json:"epoch"`
		Activity string `json:"activity"`
		Actor    string `json:"actor"`
		Decision string `json:"decision"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(reference.Payload, &referencePayload); err != nil || referencePayload.Epoch != epoch.String() || referencePayload.Activity != result.ActivityID.String() || referencePayload.Actor != actor.String() || referencePayload.Decision != string(result.DecisionID) || referencePayload.Kind != string(kind) {
		t.Fatalf("reference-only lifecycle event = %+v payload=%s err=%v; want exact bindings", referencePayload, reference.Payload, err)
	}
	var rawReference map[string]json.RawMessage
	if err := json.Unmarshal(reference.Payload, &rawReference); err != nil || rawReference["detail"] != nil {
		t.Fatalf("reference-only lifecycle event duplicated decision detail: payload=%s err=%v", reference.Payload, err)
	}

	preconditionCount := 0
	nonPreconditionCount := 0
	for _, evidence := range footprint.Evidence {
		if evidence.OperationID != operation {
			continue
		}
		if len(evidence.Digest) == 0 || len(evidence.Payload) == 0 || evidence.TaskID == (provenance.TaskID{}) || evidence.ActorID != actor {
			t.Fatalf("operation %q has an incomplete canonical evidence row %+v", operation, evidence)
		}
		if evidence.Kind == preconditionEvidenceKind {
			preconditionCount++
			if evidence.TaskID != subject {
				t.Fatalf("operation %q precondition evidence task = %s; want %s", operation, evidence.TaskID, subject)
			}
			if len(evidence.Conditions) == 0 {
				t.Fatalf("operation %q has an empty precondition condition snapshot", operation)
			}
			for _, condition := range evidence.Conditions {
				if condition.Task == "" || (condition.DecisionKind == "" && condition.EvidenceKind == "") || (condition.DecisionKind != "" && condition.EvidenceKind != "") || condition.AssertedJournalID < 0 {
					t.Fatalf("operation %q has malformed canonical condition snapshot %+v", operation, condition)
				}
			}
			continue
		}
		nonPreconditionCount++
		if evidence.Kind == planSubjectEvidenceKind || evidence.Kind == candidateEvidenceKind {
			var state subjectStateEvidence
			if err := json.Unmarshal(evidence.Payload, &state); err != nil || state.Epoch != epoch.String() || state.Subject != evidence.TaskID.String() || state.Decision == "" || state.DecisionKind == "" || state.Operation != operation {
				t.Fatalf("operation %q current-state evidence = %+v payload=%s err=%v; want exact epoch/subject/decision/operation", operation, state, evidence.Payload, err)
			}
		} else {
			var reference struct {
				Epoch     string `json:"epoch"`
				Subject   string `json:"subject"`
				Decision  string `json:"decision"`
				Reference string `json:"reference"`
			}
			if err := json.Unmarshal(evidence.Payload, &reference); err != nil || reference.Epoch != epoch.String() || reference.Subject != subject.String() || reference.Decision == "" || reference.Reference == "" {
				t.Fatalf("operation %q prerequisite evidence = %+v payload=%s err=%v; want exact epoch/subject/reference", operation, reference, evidence.Payload, err)
			}
		}
	}
	wantEvidence := map[DecisionKindID]int{DecisionPlanUATAccepted: 1, DecisionPlanRatified: 3, DecisionImplementationUAT: 1, DecisionLanded: 2}[kind]
	if preconditionCount != 1 || nonPreconditionCount != wantEvidence {
		t.Fatalf("operation %q evidence footprint has %d prerequisite/state and %d precondition rows; want %d and 1", operation, nonPreconditionCount, preconditionCount, wantEvidence)
	}

	for task, status := range statuses {
		if got, ok := footprint.Statuses[task]; !ok || got != status {
			t.Fatalf("task %s status = %v (present=%t); want %v", task, got, ok, status)
		}
	}
}
