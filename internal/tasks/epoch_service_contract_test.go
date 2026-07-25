package tasks_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/provenance"
)

type fieldSpec struct {
	name      string
	typ       reflect.Type
	anonymous bool
}

func typeOf[T any]() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

func fields(specs ...fieldSpec) []fieldSpec { return specs }

func field[T any](name string) fieldSpec {
	return fieldSpec{name: name, typ: typeOf[T]()}
}

func embedded[T any]() fieldSpec {
	typ := typeOf[T]()
	return fieldSpec{name: typ.Name(), typ: typ, anonymous: true}
}

func assertStructShape(t *testing.T, typ reflect.Type, want []fieldSpec) {
	t.Helper()
	if typ.Kind() != reflect.Struct {
		t.Fatalf("%s is %s, want struct", typ, typ.Kind())
	}
	if typ.NumField() != len(want) {
		t.Fatalf("%s has %d fields, want %d: %v", typ, typ.NumField(), len(want), want)
	}
	for i, expected := range want {
		got := typ.Field(i)
		if got.Name != expected.name || got.Type != expected.typ || got.Anonymous != expected.anonymous {
			t.Errorf("%s field %d = {%s %s anonymous=%t}, want {%s %s anonymous=%t}",
				typ, i, got.Name, got.Type, got.Anonymous, expected.name, expected.typ, expected.anonymous)
		}
	}
}

func TestEpochServiceCompleteMethodSet(t *testing.T) {
	t.Parallel()

	want := []string{
		"CloseSlice", "CreateIntegrationCandidate", "CreateSlice", "FinalizeReview",
		"Land", "PublishRepository", "RatifyPlan", "RecordImplementationUAT",
		"RecordPlanUAT", "ReworkIntegrationCandidate", "ReworkSlice",
		"SetInteractionMode", "SetSliceCandidate", "ShowInteractionMode",
		"StartReview", "SubmitReview",
	}
	typ := typeOf[tasks.EpochService]()
	if typ.NumMethod() != len(want) {
		t.Fatalf("EpochService has %d methods, want %d", typ.NumMethod(), len(want))
	}
	for i, name := range want {
		if got := typ.Method(i).Name; got != name {
			t.Errorf("EpochService method %d = %q, want %q", i, got, name)
		}
	}
}

func TestEpochServiceMethodSignaturesCompile(t *testing.T) {
	t.Parallel()

	var _ func(tasks.EpochService, context.Context, tasks.SetInteractionModeInput) (tasks.DecisionResult, error) = tasks.EpochService.SetInteractionMode
	var _ func(tasks.EpochService, context.Context, tasks.EpochRootID) (tasks.InteractionModeCursor, error) = tasks.EpochService.ShowInteractionMode
	var _ func(tasks.EpochService, context.Context, tasks.StartReviewInput) (tasks.ReviewStartResult, error) = tasks.EpochService.StartReview
	var _ func(tasks.EpochService, context.Context, tasks.SubmitReviewInput) (tasks.ReviewSubmitResult, error) = tasks.EpochService.SubmitReview
	var _ func(tasks.EpochService, context.Context, tasks.FinalizeReviewInput) (tasks.ReviewFinalizeResult, error) = tasks.EpochService.FinalizeReview
	var _ func(tasks.EpochService, context.Context, tasks.CreateSliceInput) (tasks.SliceResult, error) = tasks.EpochService.CreateSlice
	var _ func(tasks.EpochService, context.Context, tasks.SetSliceCandidateInput) (tasks.CandidateResult, error) = tasks.EpochService.SetSliceCandidate
	var _ func(tasks.EpochService, context.Context, tasks.ReworkSliceInput) (tasks.CandidateResult, error) = tasks.EpochService.ReworkSlice
	var _ func(tasks.EpochService, context.Context, tasks.CloseSliceInput) (tasks.CommandResult, error) = tasks.EpochService.CloseSlice
	var _ func(tasks.EpochService, context.Context, tasks.CreateIntegrationCandidateInput) (tasks.IntegrationCandidateResult, error) = tasks.EpochService.CreateIntegrationCandidate
	var _ func(tasks.EpochService, context.Context, tasks.ReworkIntegrationCandidateInput) (tasks.IntegrationCandidateResult, error) = tasks.EpochService.ReworkIntegrationCandidate
	var _ func(tasks.EpochService, context.Context, tasks.PublishRepositoryInput) (tasks.PublicationResult, error) = tasks.EpochService.PublishRepository
	var _ func(tasks.EpochService, context.Context, tasks.PlanUATInput) (tasks.DecisionResult, error) = tasks.EpochService.RecordPlanUAT
	var _ func(tasks.EpochService, context.Context, tasks.RatifyPlanInput) (tasks.DecisionResult, error) = tasks.EpochService.RatifyPlan
	var _ func(tasks.EpochService, context.Context, tasks.ImplementationUATInput) (tasks.DecisionResult, error) = tasks.EpochService.RecordImplementationUAT
	var _ func(tasks.EpochService, context.Context, tasks.LandInput) (tasks.DecisionResult, error) = tasks.EpochService.Land
}

// These exact shapes freeze asserted-human authority at the API boundary. Persisting the
// selected alternate human is intentionally a later implementation/integration obligation;
// a contract-only leaf cannot truthfully prove store behavior.
func TestExplicitHumanInputShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		typ  reflect.Type
		want []fieldSpec
	}{
		{typeOf[tasks.SetInteractionModeInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[tasks.InteractionMode]("Mode"), field[tasks.AssertedHumanActor]("Actor"))},
		{typeOf[tasks.PlanUATInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[provenance.TaskID]("Proposal"), field[tasks.PlanUATVerdict]("Outcome"), field[tasks.AssertedHumanActor]("Actor"), field[*tasks.PlanUATPayload]("Payload"))},
		{typeOf[tasks.RatifyPlanInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[provenance.TaskID]("Proposal"), field[tasks.ReviewRoundID]("ReviewRound"), field[tasks.DecisionLedgerEntryID]("PlanUAT"), field[tasks.AssertedHumanActor]("Actor"))},
		{typeOf[tasks.ImplementationUATInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[tasks.IntegrationCandidateSetID]("Candidate"), field[tasks.ImplementationUATVerdict]("Outcome"), field[tasks.AssertedHumanActor]("Actor"), field[*tasks.ImplUATPayload]("Payload"))},
		{typeOf[tasks.LandInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[tasks.IntegrationCandidateSetID]("Candidate"), field[tasks.DecisionLedgerEntryID]("ImplementationUAT"), field[tasks.AssertedHumanActor]("Actor"))},
	}
	for _, tc := range cases {
		t.Run(tc.typ.Name(), func(t *testing.T) { assertStructShape(t, tc.typ, tc.want) })
	}
	assertStructShape(t, typeOf[tasks.AssertedHumanActor](), fields(field[provenance.ActorID]("ActorID")))
	assertStructShape(t, typeOf[tasks.CommandMeta](), fields(field[provenance.OperationID]("OperationID")))
}

// Outcome is the sole Implementation UAT verdict. ImplUATPayload contains only structured
// content and therefore cannot disagree with the input's authoritative outcome.
func TestUATPayloadShapesHaveOneVerdict(t *testing.T) {
	t.Parallel()

	assertStructShape(t, typeOf[tasks.PlanUATPayload](), fields(
		field[[]tasks.UATInteraction]("Interactions"),
		field[[]tasks.UATFeedbackItem]("Feedback"),
		field[[]tasks.HeldUATQuestion]("HeldQuestions"),
	))
	assertStructShape(t, typeOf[tasks.ImplUATPayload](), fields(
		field[[]tasks.UATInteraction]("Interactions"),
		field[[]tasks.UATFeedbackItem]("Feedback"),
		field[[]tasks.HeldQuestionResolution]("HeldAnswers"),
		field[[]tasks.DeferredFeedbackResolution]("PlanFeedback"),
		field[[]tasks.LedgerDecisionResolution]("LedgerDecisions"),
	))
}

// Assignment is explicit for every assignment-controlled command except review start,
// whose approved command resolves one unique governing assignment from the epoch graph.
func TestAssignmentControlledInputShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		typ  reflect.Type
		want []fieldSpec
	}{
		{typeOf[tasks.StartReviewInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[tasks.ReviewSubjectRef]("Subject"))},
		{typeOf[tasks.SubmitReviewInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[tasks.ReviewRoundID]("Round"), field[tasks.ReviewAxis]("Axis"), field[provenance.AssignmentID]("Assignment"), field[tasks.ReviewSubmission]("Submission"))},
		{typeOf[tasks.FinalizeReviewInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[tasks.ReviewRoundID]("Round"), field[provenance.AssignmentID]("Assignment"))},
		{typeOf[tasks.CreateSliceInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[provenance.TaskID]("Plan"), field[provenance.AssignmentID]("Assignment"))},
		{typeOf[tasks.SetSliceCandidateInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[provenance.TaskID]("Slice"), field[string]("Repository"), field[provenance.GitOID]("Commit"), field[provenance.AssignmentID]("Assignment"))},
		{typeOf[tasks.ReworkSliceInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[provenance.TaskID]("Slice"), field[tasks.ImplementationCandidateID]("Candidate"), field[provenance.AssignmentID]("Assignment"), field[tasks.SliceCandidateReplacement]("Replacement"), field[tasks.ReworkSubmission]("Rework"))},
		{typeOf[tasks.CloseSliceInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[provenance.TaskID]("Slice"), field[tasks.ImplementationCandidateID]("Candidate"), field[tasks.ReviewRoundID]("ReviewRound"), field[provenance.AssignmentID]("Assignment"))},
		{typeOf[tasks.CreateIntegrationCandidateInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[provenance.TaskID]("Plan"), field[provenance.AssignmentID]("Assignment"), field[[]tasks.RepositoryCandidate]("Repositories"))},
		{typeOf[tasks.ReworkIntegrationCandidateInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[tasks.IntegrationCandidateSetID]("Candidate"), field[provenance.AssignmentID]("Assignment"), field[tasks.IntegrationCandidateReplacement]("Replacement"), field[tasks.ReworkSubmission]("Rework"))},
		{typeOf[tasks.PublishRepositoryInput](), fields(field[tasks.CommandMeta]("Meta"), field[tasks.EpochRootID]("Epoch"), field[tasks.IntegrationCandidateSetID]("Candidate"), field[string]("Repository"), field[string]("Ref"), field[provenance.GitOID]("Commit"), field[provenance.AssignmentID]("Assignment"))},
	}
	for _, tc := range cases {
		t.Run(tc.typ.Name(), func(t *testing.T) { assertStructShape(t, tc.typ, tc.want) })
	}
}

func TestReviewAndReplacementPayloadShapes(t *testing.T) {
	t.Parallel()

	assertStructShape(t, typeOf[tasks.PlanReviewFeedback](), fields(field[string]("Body")))
	assertStructShape(t, typeOf[tasks.ReviewFinding](), fields(field[provenance.TaskID]("Task"), field[tasks.FindingSeverity]("Severity"), field[string]("Summary")))
	assertStructShape(t, typeOf[tasks.PlanReviewSubmission](), fields(field[tasks.Verdict]("Verdict"), field[[]tasks.PlanReviewFeedback]("Feedback")))
	assertStructShape(t, typeOf[tasks.ImplementationReviewSubmission](), fields(field[tasks.Verdict]("Verdict"), field[[]tasks.ReviewFinding]("Findings")))
	var _ tasks.ReviewSubmission = tasks.PlanReviewSubmission{}
	var _ tasks.ReviewSubmission = tasks.ImplementationReviewSubmission{}
	assertStructShape(t, typeOf[tasks.FindingResolution](), fields(field[provenance.TaskID]("Finding"), field[tasks.FindingDisposition]("Outcome"), field[[]provenance.JournalID]("Evidence")))
	assertStructShape(t, typeOf[tasks.ReworkSubmission](), fields(field[[]tasks.FindingResolution]("Findings")))
	assertStructShape(t, typeOf[tasks.SliceCandidateReplacement](), fields(field[string]("Repository"), field[provenance.GitOID]("Commit")))
	assertStructShape(t, typeOf[tasks.RepositoryCandidate](), fields(field[string]("Repository"), field[tasks.ImplementationCandidateID]("Candidate"), field[provenance.GitOID]("Commit")))
	assertStructShape(t, typeOf[tasks.IntegrationCandidateReplacement](), fields(field[[]tasks.RepositoryCandidate]("Repositories")))
}

func TestReviewSubmissionVariantConstraints(t *testing.T) {
	t.Parallel()

	valid := []tasks.ReviewSubmission{
		tasks.PlanReviewSubmission{Verdict: tasks.VerdictAccept},
		tasks.PlanReviewSubmission{Verdict: tasks.VerdictRevise, Feedback: []tasks.PlanReviewFeedback{{Body: "Specify the missing acceptance case"}}},
		tasks.ImplementationReviewSubmission{Verdict: tasks.VerdictAccept},
		tasks.ImplementationReviewSubmission{Verdict: tasks.VerdictAccept, Findings: []tasks.ReviewFinding{
			{Task: provenance.TaskID{Namespace: "important"}, Severity: tasks.SeverityImportant, Summary: "Track validation hardening"},
			{Task: provenance.TaskID{Namespace: "minor"}, Severity: tasks.SeverityMinor, Summary: "Track naming cleanup"},
		}},
		tasks.ImplementationReviewSubmission{Verdict: tasks.VerdictRevise, Findings: []tasks.ReviewFinding{{Task: provenance.TaskID{Namespace: "finding"}, Severity: tasks.SeverityBlocker, Summary: "Production path is incomplete"}}},
	}
	for i, submission := range valid {
		if err := submission.Validate(); err != nil {
			t.Errorf("valid submission %d rejected: %v", i, err)
		}
	}

	invalid := []tasks.ReviewSubmission{
		tasks.PlanReviewSubmission{Verdict: tasks.VerdictRevise},
		tasks.PlanReviewSubmission{Verdict: tasks.VerdictAccept, Feedback: []tasks.PlanReviewFeedback{{Body: "revision"}}},
		tasks.PlanReviewSubmission{Verdict: tasks.VerdictRevise, Feedback: []tasks.PlanReviewFeedback{{}}},
		tasks.ImplementationReviewSubmission{Verdict: tasks.VerdictRevise},
		tasks.ImplementationReviewSubmission{Verdict: tasks.VerdictAccept, Findings: []tasks.ReviewFinding{{Task: provenance.TaskID{Namespace: "finding"}, Severity: tasks.SeverityBlocker, Summary: "blocks acceptance"}}},
		tasks.ImplementationReviewSubmission{Verdict: tasks.VerdictRevise, Findings: []tasks.ReviewFinding{{Task: provenance.TaskID{Namespace: "finding"}, Severity: tasks.SeverityImportant, Summary: "follow-up only"}}},
		tasks.ImplementationReviewSubmission{Verdict: tasks.VerdictRevise, Findings: []tasks.ReviewFinding{{}}},
		tasks.ImplementationReviewSubmission{Verdict: tasks.VerdictRevise, Findings: []tasks.ReviewFinding{
			{Task: provenance.TaskID{Namespace: "blocker"}, Severity: tasks.SeverityBlocker, Summary: "real blocker"},
			{},
		}},
	}
	for i, submission := range invalid {
		if err := submission.Validate(); err == nil {
			t.Errorf("invalid submission %d accepted", i)
		}
	}
}

func TestCommandResultShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		typ  reflect.Type
		want []fieldSpec
	}{
		{typeOf[tasks.InteractionModeCursor](), fields(field[*tasks.DecisionLedgerEntryID]("Entry"), field[tasks.InteractionMode]("Mode"))},
		{typeOf[tasks.CommandResult](), fields(field[provenance.OperationID]("OperationID"), field[bool]("Replayed"), field[tasks.EpochRootID]("Epoch"), field[provenance.ActivityID]("ActivityID"), field[[]provenance.JournalID]("EventIDs"))},
		{typeOf[tasks.DecisionResult](), fields(embedded[tasks.CommandResult](), field[tasks.DecisionLedgerEntryID]("DecisionID"), field[provenance.ActorID]("ActorID"))},
		{typeOf[tasks.ReviewStartResult](), fields(embedded[tasks.CommandResult](), field[tasks.ReviewRoundID]("Round"), field[tasks.ReviewSubjectRef]("Subject"))},
		{typeOf[tasks.ReviewSubmitResult](), fields(embedded[tasks.CommandResult](), field[tasks.ReviewRoundID]("Round"), field[tasks.ReviewAxis]("Axis"), field[provenance.JournalID]("Event"))},
		{typeOf[tasks.ReviewFinalizeResult](), fields(embedded[tasks.CommandResult](), field[tasks.ReviewRoundID]("Round"), field[[3]provenance.JournalID]("ReviewEvents"))},
		{typeOf[tasks.SliceResult](), fields(embedded[tasks.CommandResult](), field[provenance.TaskID]("Slice"))},
		{typeOf[tasks.CandidateResult](), fields(embedded[tasks.CommandResult](), field[provenance.TaskID]("Slice"), field[tasks.ImplementationCandidateID]("Candidate"))},
		{typeOf[tasks.IntegrationCandidateResult](), fields(embedded[tasks.CommandResult](), field[tasks.IntegrationCandidateSetID]("Candidate"))},
		{typeOf[tasks.PublicationResult](), fields(embedded[tasks.CommandResult](), field[tasks.IntegrationCandidateSetID]("Candidate"), field[string]("Repository"), field[provenance.JournalID]("Evidence"))},
	}
	for _, tc := range cases {
		t.Run(tc.typ.Name(), func(t *testing.T) { assertStructShape(t, tc.typ, tc.want) })
	}
}

func TestCommandContractsExcludeLegacyStorageAndFallbackAuthority(t *testing.T) {
	t.Parallel()

	contracts := append(commandInputTypes(), commandResultTypes()...)
	contracts = append(contracts,
		typeOf[tasks.PlanUATPayload](),
		typeOf[tasks.ImplUATPayload](),
		typeOf[tasks.ReworkSubmission](),
	)
	forbidden := []string{
		"RaceBarrier", "Synchronization",
		"ExpectedLedger", "ExpectedRevision", "Revision", "Codec", "PayloadCodec",
		"Report", "ReportFile", "RawReport", "ReportedVerdict",
		"Decider", "Recorder", "Trust", "UserActorID", "Owner",
	}
	for _, typ := range contracts {
		for _, name := range forbidden {
			if _, found := typ.FieldByName(name); found {
				t.Errorf("%s leaks forbidden field %s", typ.Name(), name)
			}
		}
	}

	for _, typ := range commandInputTypes() {
		if _, found := typ.FieldByName("ReviewEvents"); found {
			t.Errorf("%s accepts raw review event IDs; finalized evidence must be derived", typ.Name())
		}
	}
	for _, typ := range assignmentControlledInputTypes() {
		if _, found := typ.FieldByName("Actor"); found {
			t.Errorf("%s accepts a fallback actor instead of assignment authority", typ.Name())
		}
	}
}

// The factory shape makes synchronization reachable by a future concrete production
// service. Calling the barrier after preflight and proving synchronized winner/loser
// behavior remain obligations of the implementation and acceptance leaves.
func TestProductionConstructionCarriesSynchronizationOutsideCommands(t *testing.T) {
	t.Parallel()

	assertStructShape(t, typeOf[tasks.EpochServiceSynchronization](), fields(field[tasks.EpochRaceBarrier]("RaceBarrier")))
	assertStructShape(t, typeOf[tasks.EpochServiceOptions](), fields(field[tasks.EpochServiceSynchronization]("Synchronization"), field[func() time.Time]("now")))
	var _ func(tasks.EpochServiceFactory, tasks.EpochServiceOptions) (tasks.EpochService, error) = tasks.EpochServiceFactory.NewEpochService
	var _ func(tasks.EpochHumanServiceFactory, tasks.EpochServiceOptions) (tasks.EpochHumanService, error) = tasks.EpochHumanServiceFactory.NewEpochHumanService
}

func commandInputTypes() []reflect.Type {
	return []reflect.Type{
		typeOf[tasks.SetInteractionModeInput](), typeOf[tasks.PlanUATInput](),
		typeOf[tasks.RatifyPlanInput](), typeOf[tasks.ImplementationUATInput](),
		typeOf[tasks.LandInput](), typeOf[tasks.StartReviewInput](),
		typeOf[tasks.SubmitReviewInput](), typeOf[tasks.FinalizeReviewInput](),
		typeOf[tasks.CreateSliceInput](), typeOf[tasks.SetSliceCandidateInput](),
		typeOf[tasks.ReworkSliceInput](), typeOf[tasks.CloseSliceInput](),
		typeOf[tasks.CreateIntegrationCandidateInput](),
		typeOf[tasks.ReworkIntegrationCandidateInput](),
		typeOf[tasks.PublishRepositoryInput](),
	}
}

func assignmentControlledInputTypes() []reflect.Type {
	return []reflect.Type{
		typeOf[tasks.StartReviewInput](), typeOf[tasks.SubmitReviewInput](),
		typeOf[tasks.FinalizeReviewInput](), typeOf[tasks.CreateSliceInput](),
		typeOf[tasks.SetSliceCandidateInput](), typeOf[tasks.ReworkSliceInput](),
		typeOf[tasks.CloseSliceInput](), typeOf[tasks.CreateIntegrationCandidateInput](),
		typeOf[tasks.ReworkIntegrationCandidateInput](), typeOf[tasks.PublishRepositoryInput](),
	}
}

func commandResultTypes() []reflect.Type {
	return []reflect.Type{
		typeOf[tasks.InteractionModeCursor](), typeOf[tasks.CommandResult](),
		typeOf[tasks.DecisionResult](),
		typeOf[tasks.ReviewStartResult](), typeOf[tasks.ReviewSubmitResult](),
		typeOf[tasks.ReviewFinalizeResult](), typeOf[tasks.SliceResult](),
		typeOf[tasks.CandidateResult](), typeOf[tasks.IntegrationCandidateResult](),
		typeOf[tasks.PublicationResult](),
	}
}
