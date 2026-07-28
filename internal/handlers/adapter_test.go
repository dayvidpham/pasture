package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
)

const (
	adapterTestEpoch      = tasks.EpochRootID("adapter--01960000-0000-7000-8000-000000000001")
	adapterTestPlan       = "adapter--01960000-0000-7000-8000-000000000002"
	adapterTestSlice      = "adapter--01960000-0000-7000-8000-000000000003"
	adapterTestCandidate  = tasks.ImplementationCandidateID("adapter--01960000-0000-7000-8000-000000000004")
	adapterTestRound      = tasks.ReviewRoundID("adapter--01960000-0000-7000-8000-000000000005")
	adapterTestActor      = "adapter--01960000-0000-7000-8000-000000000006"
	adapterTestFinding    = "adapter--01960000-0000-7000-8000-000000000007"
	adapterTestCommit     = provenance.GitOID("0123456789abcdef0123456789abcdef01234567")
	adapterTestAssignment = provenance.AssignmentID("assignment/adapter/reviewer")
)

type recordingAdapterService struct {
	tasks.EpochService
	called     string
	operations []provenance.OperationID
}

func (s *recordingAdapterService) record(name string, meta tasks.CommandMeta) {
	s.called = name
	s.operations = append(s.operations, meta.OperationID)
}

func (s *recordingAdapterService) SetInteractionMode(_ context.Context, in tasks.SetInteractionModeInput) (tasks.DecisionResult, error) {
	s.record("set-interaction-mode", in.Meta)
	return adapterTestDecision(in.Meta, in.Epoch, in.Actor.ActorID), nil
}

func (s *recordingAdapterService) ShowInteractionMode(_ context.Context, _ tasks.EpochRootID) (tasks.InteractionModeCursor, error) {
	s.called = "show-interaction-mode"
	return tasks.InteractionModeCursor{Mode: tasks.InteractionAFK}, nil
}

func (s *recordingAdapterService) StartReview(_ context.Context, in tasks.StartReviewInput) (tasks.ReviewStartResult, error) {
	s.record("start-review", in.Meta)
	return tasks.ReviewStartResult{CommandResult: adapterTestCommand(in.Meta, in.Epoch), Round: adapterTestRound, Subject: in.Subject}, nil
}

func (s *recordingAdapterService) SubmitReview(_ context.Context, in tasks.SubmitReviewInput) (tasks.ReviewSubmitResult, error) {
	name := "submit-implementation-review"
	if _, ok := in.Submission.(tasks.PlanReviewSubmission); ok {
		name = "submit-plan-review"
	}
	s.record(name, in.Meta)
	return tasks.ReviewSubmitResult{CommandResult: adapterTestCommand(in.Meta, in.Epoch), Round: in.Round, Axis: in.Axis, Event: 17}, nil
}

func (s *recordingAdapterService) FinalizeReview(_ context.Context, in tasks.FinalizeReviewInput) (tasks.ReviewFinalizeResult, error) {
	s.record("finalize-review", in.Meta)
	return tasks.ReviewFinalizeResult{CommandResult: adapterTestCommand(in.Meta, in.Epoch), Round: in.Round, ReviewEvents: [3]provenance.JournalID{1, 2, 3}}, nil
}

func (s *recordingAdapterService) CreateSlice(_ context.Context, in tasks.CreateSliceInput) (tasks.SliceResult, error) {
	s.record("create-slice", in.Meta)
	return tasks.SliceResult{CommandResult: adapterTestCommand(in.Meta, in.Epoch), Slice: mustAdapterTestTask(adapterTestSlice)}, nil
}

func (s *recordingAdapterService) SetSliceCandidate(_ context.Context, in tasks.SetSliceCandidateInput) (tasks.CandidateResult, error) {
	s.record("set-slice-candidate", in.Meta)
	return tasks.CandidateResult{CommandResult: adapterTestCommand(in.Meta, in.Epoch), Slice: in.Slice, Candidate: adapterTestCandidate}, nil
}

func (s *recordingAdapterService) CloseSlice(_ context.Context, in tasks.CloseSliceInput) (tasks.CommandResult, error) {
	s.record("close-slice", in.Meta)
	return adapterTestCommand(in.Meta, in.Epoch), nil
}

func (s *recordingAdapterService) CreateIntegrationCandidate(_ context.Context, in tasks.CreateIntegrationCandidateInput) (tasks.IntegrationCandidateResult, error) {
	s.record("create-integration-candidate", in.Meta)
	return tasks.IntegrationCandidateResult{CommandResult: adapterTestCommand(in.Meta, in.Epoch), Candidate: tasks.IntegrationCandidateSetID(adapterTestCandidate)}, nil
}

func (s *recordingAdapterService) PublishRepository(_ context.Context, in tasks.PublishRepositoryInput) (tasks.PublicationResult, error) {
	s.record("publish-repository", in.Meta)
	return tasks.PublicationResult{CommandResult: adapterTestCommand(in.Meta, in.Epoch), Candidate: in.Candidate, Repository: in.Repository, Evidence: 91}, nil
}

func (s *recordingAdapterService) RecordPlanUAT(_ context.Context, in tasks.PlanUATInput) (tasks.DecisionResult, error) {
	s.record("record-plan-uat", in.Meta)
	return adapterTestDecision(in.Meta, in.Epoch, in.Actor.ActorID), nil
}

func (s *recordingAdapterService) RatifyPlan(_ context.Context, in tasks.RatifyPlanInput) (tasks.DecisionResult, error) {
	s.record("ratify-plan", in.Meta)
	return adapterTestDecision(in.Meta, in.Epoch, in.Actor.ActorID), nil
}

func (s *recordingAdapterService) RecordImplementationUAT(_ context.Context, in tasks.ImplementationUATInput) (tasks.DecisionResult, error) {
	s.record("record-implementation-uat", in.Meta)
	return adapterTestDecision(in.Meta, in.Epoch, in.Actor.ActorID), nil
}

func (s *recordingAdapterService) Land(_ context.Context, in tasks.LandInput) (tasks.DecisionResult, error) {
	s.record("land", in.Meta)
	return adapterTestDecision(in.Meta, in.Epoch, in.Actor.ActorID), nil
}

type fakeAdapterStore struct {
	service     tasks.EpochService
	constructed int
	closed      int
}

func (s *fakeAdapterStore) NewEpochService(tasks.EpochServiceOptions) (tasks.EpochService, error) {
	s.constructed++
	return s.service, nil
}

func (s *fakeAdapterStore) Close() error {
	s.closed++
	return nil
}

func TestAdapterInvokeRoutesEverySupportedOperation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		operation AdapterOperation
		input     any
		called    string
	}{
		{"set interaction mode", AdapterOperationSetInteractionMode, AdapterSetInteractionModeInput{Epoch: adapterTestEpoch, Mode: tasks.InteractionAFK, Actor: adapterTestActor}, "set-interaction-mode"},
		{"show interaction mode", AdapterOperationShowInteractionMode, AdapterShowInteractionModeInput{Epoch: adapterTestEpoch}, "show-interaction-mode"},
		{"start review", AdapterOperationStartReview, AdapterStartReviewInput{Epoch: adapterTestEpoch, SubjectKind: AdapterReviewSubjectDocumentRevision, Subject: adapterTestPlan}, "start-review"},
		{"submit plan review", AdapterOperationSubmitPlanReview, AdapterSubmitPlanReviewInput{Epoch: adapterTestEpoch, Round: adapterTestRound, Axis: AdapterReviewAxisCorrectness, Assignment: adapterTestAssignment, Verdict: AdapterReviewAccept, Feedback: []AdapterPlanReviewFeedback{}}, "submit-plan-review"},
		{"submit implementation review", AdapterOperationSubmitImplementationReview, AdapterSubmitImplementationReviewInput{Epoch: adapterTestEpoch, Round: adapterTestRound, Axis: AdapterReviewAxisTestQuality, Assignment: adapterTestAssignment, Verdict: AdapterReviewAccept, Findings: []AdapterReviewFinding{}}, "submit-implementation-review"},
		{"finalize review", AdapterOperationFinalizeReview, AdapterFinalizeReviewInput{Epoch: adapterTestEpoch, Round: adapterTestRound, Assignment: adapterTestAssignment}, "finalize-review"},
		{"create slice", AdapterOperationCreateSlice, AdapterCreateSliceInput{Epoch: adapterTestEpoch, Plan: adapterTestPlan, Assignment: adapterTestAssignment}, "create-slice"},
		{"set slice candidate", AdapterOperationSetSliceCandidate, AdapterSetSliceCandidateInput{Epoch: adapterTestEpoch, Slice: adapterTestSlice, Repository: "repo-a", Commit: adapterTestCommit, Assignment: adapterTestAssignment}, "set-slice-candidate"},
		{"close slice", AdapterOperationCloseSlice, AdapterCloseSliceInput{Epoch: adapterTestEpoch, Slice: adapterTestSlice, Candidate: adapterTestCandidate, ReviewRound: adapterTestRound, Assignment: adapterTestAssignment}, "close-slice"},
		{"create integration candidate", AdapterOperationCreateIntegrationCandidate, AdapterCreateIntegrationCandidateInput{Epoch: adapterTestEpoch, Plan: adapterTestPlan, Assignment: adapterTestAssignment, Repositories: []AdapterRepositoryCandidate{{Repository: "repo-a", Candidate: adapterTestCandidate, Commit: adapterTestCommit}}}, "create-integration-candidate"},
		{"publish repository", AdapterOperationPublishRepository, AdapterPublishRepositoryInput{Epoch: adapterTestEpoch, Candidate: tasks.IntegrationCandidateSetID(adapterTestCandidate), Repository: "repo-a", Ref: "refs/heads/main", Commit: adapterTestCommit, Assignment: adapterTestAssignment}, "publish-repository"},
		{"record plan UAT", AdapterOperationRecordPlanUAT, AdapterRecordPlanUATInput{Epoch: adapterTestEpoch, Proposal: adapterTestPlan, Outcome: AdapterPlanUATAccepted, Actor: adapterTestActor, Interactions: []tasks.UATInteraction{}, Feedback: []tasks.UATFeedbackItem{}, HeldQuestions: []tasks.HeldUATQuestion{}}, "record-plan-uat"},
		{"ratify plan", AdapterOperationRatifyPlan, AdapterRatifyPlanInput{Epoch: adapterTestEpoch, Proposal: adapterTestPlan, ReviewRound: adapterTestRound, PlanUAT: "decision-plan-uat", Actor: adapterTestActor}, "ratify-plan"},
		{"record implementation UAT", AdapterOperationRecordImplementationUAT, AdapterRecordImplementationUATInput{Epoch: adapterTestEpoch, Candidate: tasks.IntegrationCandidateSetID(adapterTestCandidate), Outcome: AdapterImplementationUATAccepted, Actor: adapterTestActor, Interactions: []tasks.UATInteraction{}, Feedback: []tasks.UATFeedbackItem{}, HeldAnswers: []AdapterResolution{}, PlanFeedback: []AdapterResolution{}, LedgerDecisions: []AdapterResolution{}}, "record-implementation-uat"},
		{"land", AdapterOperationLand, AdapterLandInput{Epoch: adapterTestEpoch, Candidate: tasks.IntegrationCandidateSetID(adapterTestCandidate), ImplementationUAT: "decision-implementation-uat", Actor: adapterTestActor}, "land"},
	}

	if got := SupportedAdapterOperations(); len(got) != len(cases) {
		t.Fatalf("supported operation count = %d, routed cases = %d", len(got), len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service := &recordingAdapterService{}
			store := &fakeAdapterStore{service: service}
			var output bytes.Buffer
			err := adapterInvoke(context.Background(), AdapterInvokeInput{
				Input:  adapterTestEnvelope(t, tc.operation, tc.input, "native-"+strings.ReplaceAll(tc.name, " ", "-")),
				Output: &output,
			}, func(string) (adapterStore, error) { return store, nil })
			if err != nil {
				t.Fatalf("adapterInvoke: %v", err)
			}
			if service.called != tc.called {
				t.Fatalf("service call = %q, want %q", service.called, tc.called)
			}
			if store.constructed != 1 || store.closed != 1 {
				t.Fatalf("store constructed/closed = %d/%d, want 1/1", store.constructed, store.closed)
			}
			var result AdapterResultEnvelope
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatalf("decode result: %v; output=%s", err, output.Bytes())
			}
			if result.Schema != AdapterResultSchema || result.Operation != tc.operation || len(result.Result) == 0 {
				t.Fatalf("result envelope = %+v", result)
			}
			lower := strings.ToLower(output.String())
			for _, forbidden := range []string{"operationid", "activityid", "eventid", "evidenceid", "native-"} {
				if strings.Contains(lower, forbidden) {
					t.Errorf("result leaked %q: %s", forbidden, output.String())
				}
			}
		})
	}
}

func TestAdapterInvocationIdentityIsStablePrivateAndCommandIndependent(t *testing.T) {
	t.Parallel()

	service := &recordingAdapterService{}
	invoke := func(operation AdapterOperation, input any) string {
		t.Helper()
		store := &fakeAdapterStore{service: service}
		var output bytes.Buffer
		if err := adapterInvoke(context.Background(), AdapterInvokeInput{
			Input:  adapterTestEnvelope(t, operation, input, "private-native-request-77"),
			Output: &output,
		}, func(string) (adapterStore, error) { return store, nil }); err != nil {
			t.Fatalf("adapterInvoke(%s): %v", operation, err)
		}
		return output.String()
	}

	set := AdapterSetInteractionModeInput{Epoch: adapterTestEpoch, Mode: tasks.InteractionAFK, Actor: adapterTestActor}
	first := invoke(AdapterOperationSetInteractionMode, set)
	second := invoke(AdapterOperationSetInteractionMode, set)
	plan := AdapterRecordPlanUATInput{Epoch: adapterTestEpoch, Proposal: adapterTestPlan, Outcome: AdapterPlanUATAccepted, Actor: adapterTestActor, Interactions: []tasks.UATInteraction{}, Feedback: []tasks.UATFeedbackItem{}, HeldQuestions: []tasks.HeldUATQuestion{}}
	third := invoke(AdapterOperationRecordPlanUAT, plan)

	if len(service.operations) != 3 || service.operations[0] == "" || service.operations[0] != service.operations[1] || service.operations[0] != service.operations[2] {
		t.Fatalf("derived operation identities = %v; want one stable identity independent of semantic command", service.operations)
	}
	for index, output := range []string{first, second, third} {
		if strings.Contains(output, "private-native-request-77") || strings.Contains(output, string(service.operations[0])) {
			t.Errorf("output %d leaked private invocation or derived operation identity: %s", index, output)
		}
	}
}

func TestAdapterRejectsMalformedForbiddenAndInvalidSemanticInputBeforeStoreOpen(t *testing.T) {
	t.Parallel()

	base := string(adapterTestEnvelopeBytes(t, AdapterOperationSetSliceCandidate, AdapterSetSliceCandidateInput{
		Epoch: adapterTestEpoch, Slice: adapterTestSlice, Repository: "repo-a", Commit: adapterTestCommit, Assignment: adapterTestAssignment,
	}, "native-rejection-test"))
	publish := string(adapterTestEnvelopeBytes(t, AdapterOperationPublishRepository, AdapterPublishRepositoryInput{
		Epoch: adapterTestEpoch, Candidate: tasks.IntegrationCandidateSetID(adapterTestCandidate), Repository: "repo-a", Ref: "refs/heads/main", Commit: adapterTestCommit, Assignment: adapterTestAssignment,
	}, "native-ref-test"))

	cases := []struct {
		name string
		data string
	}{
		{"unknown envelope field", strings.Replace(base, `"input":`, `"unexpected":true,"input":`, 1)},
		{"duplicate envelope field", strings.Replace(base, `"schema":`, `"schema":"pasture.adapter-invocation/v1","schema":`, 1)},
		{"trailing JSON", base + `{}`},
		{"public operation id", strings.Replace(base, `"input":`, `"operationId":"public-operation","input":`, 1)},
		{"public revision", strings.Replace(base, `"input":`, `"revision":"storage-revision","input":`, 1)},
		{"public evidence id", strings.Replace(base, `"input":`, `"evidenceId":7,"input":`, 1)},
		{"raw native payload", strings.Replace(base, `"input":`, `"nativePayload":{},"input":`, 1)},
		{"wrong schema", strings.Replace(base, AdapterInvocationSchema, "pasture.adapter-invocation/v2", 1)},
		{"harness contract mismatch", strings.Replace(base, `"harness":"claude-code"`, `"harness":"codex"`, 1)},
		{"incompatible harness version", strings.Replace(base, `"harnessVersion":"2.1.210"`, `"harnessVersion":"2.1.211"`, 1)},
		{"unpinned harness contract", strings.Replace(base, "claude-code/claude-code@2.1.210", "claude-code/claude-code@9.9.9", 1)},
		{"unknown operation", strings.Replace(base, string(AdapterOperationSetSliceCandidate), "pasture.epoch.unknown/v1", 1)},
		{"raw payload inside semantic input", strings.Replace(base, `"epoch":`, `"nativePayload":{},"epoch":`, 1)},
		{"storage identity inside semantic input", strings.Replace(base, `"epoch":`, `"journalId":91,"epoch":`, 1)},
		{"invalid repository", strings.Replace(base, `"repository":"repo-a"`, `"repository":"repo a"`, 1)},
		{"invalid Git OID", strings.Replace(base, string(adapterTestCommit), strings.ToUpper(string(adapterTestCommit)), 1)},
		{"invalid Git ref", strings.Replace(publish, `"ref":"refs/heads/main"`, `"ref":"refs/heads/../main"`, 1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opened := 0
			var output bytes.Buffer
			err := adapterInvoke(context.Background(), AdapterInvokeInput{Input: strings.NewReader(tc.data), Output: &output}, func(string) (adapterStore, error) {
				opened++
				return &fakeAdapterStore{service: &recordingAdapterService{}}, nil
			})
			if err == nil {
				t.Fatal("invalid adapter invocation succeeded")
			}
			if opened != 0 {
				t.Fatalf("store opened %d times for rejected input", opened)
			}
			var structured interface{ Unwrap() error }
			if !errors.As(err, &structured) {
				t.Fatalf("error is not structured/actionable: %T %v", err, err)
			}
			if output.Len() != 0 {
				t.Fatalf("rejected invocation wrote output: %s", output.String())
			}
		})
	}
}

func TestAdapterClosesStoreWhenEpochServiceRejectsOperation(t *testing.T) {
	t.Parallel()

	want := errors.New("service rejected test operation")
	store := &fakeAdapterStore{service: errorEpochService{err: want}}
	var output bytes.Buffer
	err := adapterInvoke(context.Background(), AdapterInvokeInput{
		Input:  adapterTestEnvelope(t, AdapterOperationSetInteractionMode, AdapterSetInteractionModeInput{Epoch: adapterTestEpoch, Mode: tasks.InteractionAFK, Actor: adapterTestActor}, "native-service-error"),
		Output: &output,
	}, func(string) (adapterStore, error) { return store, nil })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped service error", err)
	}
	if store.closed != 1 {
		t.Fatalf("store close count = %d, want 1", store.closed)
	}
	if output.Len() != 0 {
		t.Fatalf("failed operation wrote output: %s", output.String())
	}
}

type errorEpochService struct {
	tasks.EpochService
	err error
}

func (s errorEpochService) SetInteractionMode(context.Context, tasks.SetInteractionModeInput) (tasks.DecisionResult, error) {
	return tasks.DecisionResult{}, s.err
}

func adapterTestEnvelope(t *testing.T, operation AdapterOperation, input any, nativeInvocation string) *bytes.Reader {
	t.Helper()
	return bytes.NewReader(adapterTestEnvelopeBytes(t, operation, input, nativeInvocation))
}

func adapterTestEnvelopeBytes(t *testing.T, operation AdapterOperation, input any, nativeInvocation string) []byte {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	contract := runtime.ClaudeCode2_1_210()
	wire, err := json.Marshal(AdapterInvocationEnvelope{
		Schema: AdapterInvocationSchema, Harness: contract.Harness(), HarnessVersion: "2.1.210",
		HarnessContract: contract.ID(), NativeInvocation: nativeInvocation, Operation: operation, Input: payload,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return wire
}

func adapterTestCommand(meta tasks.CommandMeta, epoch tasks.EpochRootID) tasks.CommandResult {
	return tasks.CommandResult{
		OperationID: meta.OperationID,
		Epoch:       epoch,
		ActivityID:  provenance.ActivityID{Namespace: "adapter", UUID: mustAdapterTestTask("adapter--01960000-0000-7000-8000-000000000009").UUID},
		EventIDs:    []provenance.JournalID{11, 12},
	}
}

func adapterTestDecision(meta tasks.CommandMeta, epoch tasks.EpochRootID, actor provenance.ActorID) tasks.DecisionResult {
	return tasks.DecisionResult{CommandResult: adapterTestCommand(meta, epoch), DecisionID: "decision-test", ActorID: actor}
}

func mustAdapterTestTask(value string) provenance.TaskID {
	id, err := provenance.ParseTaskID(value)
	if err != nil {
		panic(fmt.Sprintf("parse adapter test task %q: %v", value, err))
	}
	return id
}
