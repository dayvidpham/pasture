package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	"github.com/google/uuid"
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
	replayed, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil || !replayed.Replayed || replayed.DecisionID != mode.DecisionID || replayed.ActivityID != mode.ActivityID {
		t.Fatalf("exact mode replay = %+v, %v; want original bindings", replayed, err)
	}
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
	reopenedReplay, err := service.SetInteractionMode(ctx, SetInteractionModeInput{Meta: CommandMeta{OperationID: "human-mode-1"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil || !reopenedReplay.Replayed || reopenedReplay.DecisionID != mode.DecisionID || reopenedReplay.ActivityID != mode.ActivityID {
		t.Fatalf("reopened direct Apply replay = %+v, %v; want original bindings", reopenedReplay, err)
	}

	planUAT, err := service.RecordPlanUAT(ctx, PlanUATInput{Meta: CommandMeta{OperationID: "human-plan-uat-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("record Plan UAT: %v", err)
	}
	if replay, err := service.RecordPlanUAT(ctx, PlanUATInput{Meta: CommandMeta{OperationID: "human-plan-uat-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil || !replay.Replayed || replay.DecisionID != planUAT.DecisionID {
		t.Fatalf("Plan UAT direct Apply replay = %+v, %v", replay, err)
	}
	seedAcceptedReview(t, tracker, epoch, proposal, "review-round-1")
	ratified, err := service.RatifyPlan(ctx, RatifyPlanInput{Meta: CommandMeta{OperationID: "human-ratify-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "review-round-1", PlanUAT: planUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("ratify plan: %v", err)
	}
	if ratified.DecisionID == planUAT.DecisionID || len(ratified.EventIDs) != 2 {
		t.Fatalf("ratification was not a distinct decision plus lifecycle: %+v", ratified)
	}
	if replay, err := service.RatifyPlan(ctx, RatifyPlanInput{Meta: CommandMeta{OperationID: "human-ratify-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "review-round-1", PlanUAT: planUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil || !replay.Replayed || replay.DecisionID != ratified.DecisionID {
		t.Fatalf("ratify direct Apply replay = %+v, %v", replay, err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close before implementation UAT: %v", err)
	}
	tracker = openHumanTestTracker(t, db)
	service = newHumanTestService(t, tracker)
	if replay, err := service.RatifyPlan(ctx, RatifyPlanInput{Meta: CommandMeta{OperationID: "human-ratify-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "review-round-1", PlanUAT: planUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil || !replay.Replayed || replay.DecisionID != ratified.DecisionID || replay.ActivityID != ratified.ActivityID || len(replay.EventIDs) != len(ratified.EventIDs) {
		t.Fatalf("ratify replay after reopen = %+v, %v; want original bindings", replay, err)
	}

	implUAT, err := service.RecordImplementationUAT(ctx, ImplementationUATInput{Meta: CommandMeta{OperationID: "human-impl-uat-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("record Implementation UAT: %v", err)
	}
	if replay, err := service.RecordImplementationUAT(ctx, ImplementationUATInput{Meta: CommandMeta{OperationID: "human-impl-uat-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil || !replay.Replayed || replay.DecisionID != implUAT.DecisionID {
		t.Fatalf("Implementation UAT direct Apply replay = %+v, %v", replay, err)
	}
	landed, err := service.Land(ctx, LandInput{Meta: CommandMeta{OperationID: "human-land-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: implUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if landed.DecisionID == implUAT.DecisionID || len(landed.EventIDs) != 2 {
		t.Fatalf("landing was not a distinct decision plus lifecycle: %+v", landed)
	}
	if replay, err := service.Land(ctx, LandInput{Meta: CommandMeta{OperationID: "human-land-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: implUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil || !replay.Replayed || replay.DecisionID != landed.DecisionID {
		t.Fatalf("land direct Apply replay = %+v, %v", replay, err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close after landing: %v", err)
	}
	tracker = openHumanTestTracker(t, db)
	service = newHumanTestService(t, tracker)
	defer tracker.Close()
	if replay, err := service.Land(ctx, LandInput{Meta: CommandMeta{OperationID: "human-land-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: implUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil || !replay.Replayed || replay.DecisionID != landed.DecisionID || replay.ActivityID != landed.ActivityID || len(replay.EventIDs) != len(landed.EventIDs) {
		t.Fatalf("land replay after reopen = %+v, %v; want original bindings", replay, err)
	}
	attributions, err := tracker.Journal().TaskAttributions(epoch)
	if err != nil {
		t.Fatalf("read epoch attributions: %v", err)
	}
	if !containsActor(attributions, human.ID) {
		t.Fatalf("epoch attributions do not contain selected human %s: %+v", human.ID, attributions)
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
			before := humanDecisionFootprint(t, tracker, epoch)
			_, err := service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: provenance.OperationID("rejected-" + tc.name)}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: tc.actor}})
			if err == nil {
				t.Fatalf("%s actor was accepted", tc.name)
			}
			if after := humanDecisionFootprint(t, tracker, epoch); after != before {
				t.Fatalf("%s actor changed persisted human-decision state: before=%+v after=%+v", tc.name, before, after)
			}
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
	before, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{epoch}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "barrier-mode"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err == nil || barrier.calls != 1 {
		t.Fatalf("barrier result err=%v calls=%d, want rejection after one call", err, barrier.calls)
	}
	after, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{epoch}})
	if err != nil || len(after.Events) != len(before.Events) {
		t.Fatalf("barrier rejection wrote task events: before=%d after=%d err=%v", len(before.Events), len(after.Events), err)
	}
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
	_, err = loser.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "mode-loser"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}})
	if !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
		t.Fatalf("stale mode result err=%v calls=%d, want typed condition failure after one barrier call", err, barrier.calls)
	}
	page, queryErr := tracker.Journal().Facts().QueryDecisions(provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: epoch}}, Kinds: []provenance.DecisionKind{journalDecisionKind(DecisionInteractionModeChanged)}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}})
	if queryErr != nil || len(page.Rows) != 1 || page.Rows[0].ProducingOperationID != "mode-winner" {
		t.Fatalf("stale mode left an unexpected canonical decision set: rows=%+v err=%v", page.Rows, queryErr)
	}
}

func TestEpochHumanServiceOldAcceptedPlanUATCannotRatifyAfterLaterNonAccept(t *testing.T) {
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
	if _, err := service.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "state-changes"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATChangesRequested, Payload: &PlanUATPayload{Feedback: []UATFeedbackItem{{ID: "revise", Body: "revise"}}}, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
		t.Fatalf("changes-requested Plan UAT: %v", err)
	}
	before := subjectFootprint(t, tracker, proposal)
	if _, err := service.RatifyPlan(context.Background(), RatifyPlanInput{Meta: CommandMeta{OperationID: "state-ratify-old"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "state-round", PlanUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}); err == nil {
		t.Fatal("old accepted Plan UAT was ratified after changes_requested")
	}
	if after := subjectFootprint(t, tracker, proposal); after != before {
		t.Fatalf("stale ratify wrote partial state: before=%+v after=%+v", before, after)
	}

	if _, err := service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "state-afk"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
		t.Fatalf("set AFK: %v", err)
	}
	if _, err := service.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "state-deferred"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATDeferredByAFK, Payload: &PlanUATPayload{HeldQuestions: []HeldUATQuestion{{ID: "held", Question: "open", Stable: true}}}, Actor: AssertedHumanActor{ActorID: human.ID}}); err != nil {
		t.Fatalf("deferred Plan UAT: %v", err)
	}
	before = subjectFootprint(t, tracker, proposal)
	if _, err := service.RatifyPlan(context.Background(), RatifyPlanInput{Meta: CommandMeta{OperationID: "state-ratify-old-again"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "state-round", PlanUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}); err == nil {
		t.Fatal("old accepted Plan UAT was ratified after deferred_by_afk")
	}
	if after := subjectFootprint(t, tracker, proposal); after != before {
		t.Fatalf("stale deferred ratify wrote partial state: before=%+v after=%+v", before, after)
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
		barrier.after = func() error {
			_, err := winner.RatifyPlan(context.Background(), RatifyPlanInput{Meta: CommandMeta{OperationID: "race-ratify-winner"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "race-round", PlanUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
			return err
		}
		loser, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
		if err != nil {
			t.Fatal(err)
		}
		before := subjectFootprint(t, tracker, proposal)
		_, err = loser.RecordPlanUAT(context.Background(), PlanUATInput{Meta: CommandMeta{OperationID: "race-plan-loser"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATChangesRequested, Payload: &PlanUATPayload{Feedback: []UATFeedbackItem{{ID: "fix", Body: "fix"}}}, Actor: AssertedHumanActor{ActorID: human.ID}})
		if !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
			t.Fatalf("Plan UAT loser = %v, barrier calls=%d; want condition failure after one barrier", err, barrier.calls)
		}
		if _, found, err := loser.(*epochHumanService).committedOperationDecision("race-plan-loser", planUATDecisionKinds()); err != nil || found {
			t.Fatalf("losing Plan UAT operation persisted: found=%t err=%v", found, err)
		}
		if after := subjectFootprint(t, tracker, proposal); after.decisions != before.decisions+1 || after.activities != before.activities+1 {
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
		barrier.after = func() error {
			_, err := winner.Land(context.Background(), LandInput{Meta: CommandMeta{OperationID: "race-land-winner"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: accepted.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
			return err
		}
		loser, err := tracker.NewEpochHumanService(EpochServiceOptions{Synchronization: EpochServiceSynchronization{RaceBarrier: barrier}})
		if err != nil {
			t.Fatal(err)
		}
		before := subjectFootprint(t, tracker, candidate)
		_, err = loser.RecordImplementationUAT(context.Background(), ImplementationUATInput{Meta: CommandMeta{OperationID: "race-impl-loser"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATChangesRequested, Actor: AssertedHumanActor{ActorID: human.ID}})
		if !errors.Is(err, provenance.ErrConditionFailed) || barrier.calls != 1 {
			t.Fatalf("Implementation UAT loser = %v, barrier calls=%d; want condition failure after one barrier", err, barrier.calls)
		}
		if _, found, err := loser.(*epochHumanService).committedOperationDecision("race-impl-loser", []DecisionKindID{DecisionImplementationUAT}); err != nil || found {
			t.Fatalf("losing Implementation UAT operation persisted: found=%t err=%v", found, err)
		}
		if after := subjectFootprint(t, tracker, candidate); after.decisions != before.decisions || after.evidence != before.evidence+1 || after.activities != before.activities+1 {
			t.Fatalf("Implementation UAT barrier footprint = before=%+v after=%+v; want one land state and no loser candidate rows", before, after)
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
	beforeProposal := subjectFootprint(t, tracker, proposalA)
	if _, err := service.RatifyPlan(context.Background(), RatifyPlanInput{Meta: CommandMeta{OperationID: "binding-ratify"}, Epoch: EpochRootID(epoch.String()), Proposal: proposalA, ReviewRound: "binding-round", PlanUAT: planB.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}); err == nil {
		t.Fatal("ratify accepted a Plan UAT from another proposal")
	}
	if after := subjectFootprint(t, tracker, proposalA); after != beforeProposal {
		t.Fatalf("wrong ratify binding wrote partial state: before=%+v after=%+v", beforeProposal, after)
	}

	implA, err := service.RecordImplementationUAT(context.Background(), ImplementationUATInput{Meta: CommandMeta{OperationID: "binding-impl-a"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidateA.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatal(err)
	}
	implB, err := service.RecordImplementationUAT(context.Background(), ImplementationUATInput{Meta: CommandMeta{OperationID: "binding-impl-b"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidateB.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatal(err)
	}
	beforeCandidate := subjectFootprint(t, tracker, candidateA)
	if _, err := service.Land(context.Background(), LandInput{Meta: CommandMeta{OperationID: "binding-land"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidateA.String()), ImplementationUAT: implB.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}}); err == nil {
		t.Fatal("land accepted an Implementation UAT from another candidate")
	}
	if after := subjectFootprint(t, tracker, candidateA); after != beforeCandidate {
		t.Fatalf("wrong land binding wrote partial state: before=%+v after=%+v", beforeCandidate, after)
	}
	if implA.DecisionID == implB.DecisionID {
		t.Fatal("test setup did not produce distinct candidate decisions")
	}
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

func containsActor(values []provenance.TaskAttribution, actor provenance.ActorID) bool {
	for _, value := range values {
		if value.ActorID == actor {
			return true
		}
	}
	return false
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

type decisionFootprint struct {
	decisions  int
	evidence   int
	events     int
	activities int
}

type subjectStateFootprint struct {
	decisions  int
	evidence   int
	events     int
	activities int
}

func subjectFootprint(t *testing.T, tracker *trackerImpl, subject provenance.TaskID) subjectStateFootprint {
	t.Helper()
	decisionKinds := make([]provenance.DecisionKind, 0, 7)
	for _, kind := range []DecisionKindID{DecisionInteractionModeChanged, DecisionPlanUATAccepted, DecisionPlanUATChangesRequested, DecisionPlanUATDeferredByAFK, DecisionImplementationUAT, DecisionPlanRatified, DecisionLanded} {
		decisionKinds = append(decisionKinds, journalDecisionKind(kind))
	}
	decisions, err := tracker.Journal().Facts().QueryDecisions(provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, Kinds: decisionKinds, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}})
	if err != nil {
		t.Fatalf("read subject decisions: %v", err)
	}
	evidence, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: subject}}, Kinds: []provenance.EvidenceKind{planSubjectEvidenceKind, candidateEvidenceKind, preconditionEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}})
	if err != nil {
		t.Fatalf("read subject evidence: %v", err)
	}
	events, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{subject}, Limit: provenance.MaxFactPageSize})
	if err != nil {
		t.Fatalf("read subject events: %v", err)
	}
	activities, err := tracker.Activities(nil)
	if err != nil {
		t.Fatalf("read activities: %v", err)
	}
	return subjectStateFootprint{decisions: len(decisions.Rows), evidence: len(evidence.Rows), events: len(events.Events), activities: len(activities)}
}

func humanDecisionFootprint(t *testing.T, tracker *trackerImpl, epoch provenance.TaskID) decisionFootprint {
	t.Helper()
	decisions, err := tracker.Journal().Facts().QueryDecisions(provenance.DecisionQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: epoch}}, Kinds: []provenance.DecisionKind{journalDecisionKind(DecisionInteractionModeChanged)}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}})
	if err != nil {
		t.Fatalf("read decision footprint: %v", err)
	}
	evidence, err := tracker.Journal().Facts().QueryEvidence(provenance.EvidenceQuery{Filter: provenance.FactFilter{TaskScope: provenance.FactTaskScope{Kind: provenance.FactTaskExact, TaskID: epoch}}, Kinds: []provenance.EvidenceKind{"pasture.epoch.subject.v1", preconditionEvidenceKind}, Page: provenance.FactPageRequest{Limit: provenance.MaxFactPageSize}})
	if err != nil {
		t.Fatalf("read evidence footprint: %v", err)
	}
	events, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{epoch}, Limit: provenance.MaxFactPageSize})
	if err != nil {
		t.Fatalf("read event footprint: %v", err)
	}
	activities, err := tracker.Activities(nil)
	if err != nil {
		t.Fatalf("read activity footprint: %v", err)
	}
	return decisionFootprint{decisions: len(decisions.Rows), evidence: len(evidence.Rows), events: len(events.Events), activities: len(activities)}
}
