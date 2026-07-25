package tasks

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/provenance"
)

type rejectingEpochBarrier struct{ calls int }

func (b *rejectingEpochBarrier) AfterPreflight(context.Context, EpochMutationKind) error {
	b.calls++
	return errors.New("held by test barrier")
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
	if mode.Replayed || mode.ActorID != human.ID || mode.DecisionID == "" || mode.ActivityID == (provenance.ActivityID{}) || len(mode.EventIDs) != 2 {
		t.Fatalf("mode result missing exact material bindings: %+v", mode)
	}
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

	planUAT, err := service.RecordPlanUAT(ctx, PlanUATInput{Meta: CommandMeta{OperationID: "human-plan-uat-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, Outcome: PlanUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("record Plan UAT: %v", err)
	}
	seedAcceptedReview(t, tracker, epoch, proposal, "review-round-1")
	ratified, err := service.RatifyPlan(ctx, RatifyPlanInput{Meta: CommandMeta{OperationID: "human-ratify-1"}, Epoch: EpochRootID(epoch.String()), Proposal: proposal, ReviewRound: "review-round-1", PlanUAT: planUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("ratify plan: %v", err)
	}
	if ratified.DecisionID == planUAT.DecisionID || len(ratified.EventIDs) != 3 {
		t.Fatalf("ratification was not a distinct decision plus lifecycle: %+v", ratified)
	}

	implUAT, err := service.RecordImplementationUAT(ctx, ImplementationUATInput{Meta: CommandMeta{OperationID: "human-impl-uat-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), Outcome: ImplUATAccepted, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("record Implementation UAT: %v", err)
	}
	landed, err := service.Land(ctx, LandInput{Meta: CommandMeta{OperationID: "human-land-1"}, Epoch: EpochRootID(epoch.String()), Candidate: IntegrationCandidateSetID(candidate.String()), ImplementationUAT: implUAT.DecisionID, Actor: AssertedHumanActor{ActorID: human.ID}})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if landed.DecisionID == implUAT.DecisionID || len(landed.EventIDs) != 3 {
		t.Fatalf("landing was not a distinct decision plus lifecycle: %+v", landed)
	}
	attributions, err := tracker.Journal().TaskAttributions(epoch)
	if err != nil {
		t.Fatalf("read epoch attributions: %v", err)
	}
	if !containsActor(attributions, human.ID) {
		t.Fatalf("epoch attributions do not contain selected human %s: %+v", human.ID, attributions)
	}
}

func TestEpochHumanServiceRejectsUnknownActorBeforeWrite(t *testing.T) {
	tracker := openHumanTestTracker(t, filepath.Join(t.TempDir(), "pasture.db"))
	defer tracker.Close()
	epoch := createHumanTestTask(t, tracker, "epoch")
	service := newHumanTestService(t, tracker)
	before, err := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{epoch}})
	if err != nil {
		t.Fatal(err)
	}
	unknown := provenance.ActorID{Namespace: "test", UUID: provenance.TaskID{Namespace: "x"}.UUID}
	_, err = service.SetInteractionMode(context.Background(), SetInteractionModeInput{Meta: CommandMeta{OperationID: "unknown-actor"}, Epoch: EpochRootID(epoch.String()), Mode: InteractionAFK, Actor: AssertedHumanActor{ActorID: unknown}})
	if err == nil {
		t.Fatal("unknown actor was accepted")
	}
	after, qerr := tracker.Journal().QueryTaskEvents(provenance.JournalQueryV1{OrderBy: provenance.OrderByJournalID, TaskIDs: []provenance.TaskID{epoch}})
	if qerr != nil || len(after.Events) != len(before.Events) {
		t.Fatalf("unknown actor changed epoch journal: before=%d after=%d err=%v", len(before.Events), len(after.Events), qerr)
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
