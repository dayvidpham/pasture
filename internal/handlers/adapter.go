package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dayvidpham/provenance"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
)

const (
	// AdapterInvocationSchema is the sole accepted hidden-adapter envelope.
	AdapterInvocationSchema = "pasture.adapter-invocation/v1"
	// AdapterResultSchema is the storage-identity-free result envelope.
	AdapterResultSchema = "pasture.adapter-result/v1"
	// AdapterInvocationMaxBytes bounds one stdin invocation before decoding.
	AdapterInvocationMaxBytes = 1 << 20

	adapterNativeInvocationMaxBytes = 4096
	adapterCollectionMaxItems       = 1024
)

// AdapterOperation is the closed semantic operation set accepted by the hidden
// adapter. Rework operations are deliberately absent: their current service
// inputs require caller-supplied journal evidence identities, which this
// private invocation boundary does not expose.
type AdapterOperation string

const (
	AdapterOperationSetInteractionMode         AdapterOperation = "pasture.epoch.interaction-mode.set/v1"
	AdapterOperationShowInteractionMode        AdapterOperation = "pasture.epoch.interaction-mode.show/v1"
	AdapterOperationStartReview                AdapterOperation = "pasture.epoch.review.start/v1"
	AdapterOperationSubmitPlanReview           AdapterOperation = "pasture.epoch.review.submit-plan/v1"
	AdapterOperationSubmitImplementationReview AdapterOperation = "pasture.epoch.review.submit-implementation/v1"
	AdapterOperationFinalizeReview             AdapterOperation = "pasture.epoch.review.finalize/v1"
	AdapterOperationCreateSlice                AdapterOperation = "pasture.epoch.slice.create/v1"
	AdapterOperationSetSliceCandidate          AdapterOperation = "pasture.epoch.slice.candidate.set/v1"
	AdapterOperationCloseSlice                 AdapterOperation = "pasture.epoch.slice.close/v1"
	AdapterOperationCreateIntegrationCandidate AdapterOperation = "pasture.epoch.integration.candidate-set.create/v1"
	AdapterOperationPublishRepository          AdapterOperation = "pasture.epoch.integration.repository.publish/v1"
	AdapterOperationRecordPlanUAT              AdapterOperation = "pasture.epoch.plan.uat.record/v1"
	AdapterOperationRatifyPlan                 AdapterOperation = "pasture.epoch.plan.ratify/v1"
	AdapterOperationRecordImplementationUAT    AdapterOperation = "pasture.epoch.implementation.uat.record/v1"
	AdapterOperationLand                       AdapterOperation = "pasture.epoch.land/v1"
)

var adapterOperations = [...]AdapterOperation{
	AdapterOperationSetInteractionMode,
	AdapterOperationShowInteractionMode,
	AdapterOperationStartReview,
	AdapterOperationSubmitPlanReview,
	AdapterOperationSubmitImplementationReview,
	AdapterOperationFinalizeReview,
	AdapterOperationCreateSlice,
	AdapterOperationSetSliceCandidate,
	AdapterOperationCloseSlice,
	AdapterOperationCreateIntegrationCandidate,
	AdapterOperationPublishRepository,
	AdapterOperationRecordPlanUAT,
	AdapterOperationRatifyPlan,
	AdapterOperationRecordImplementationUAT,
	AdapterOperationLand,
}

// SupportedAdapterOperations returns the closed operation set in stable order.
func SupportedAdapterOperations() []AdapterOperation {
	return append([]AdapterOperation(nil), adapterOperations[:]...)
}

// AdapterReviewSubjectKind is the JSON discriminator for a review subject.
type AdapterReviewSubjectKind string

const (
	AdapterReviewSubjectDocumentRevision        AdapterReviewSubjectKind = "document-revision"
	AdapterReviewSubjectImplementationCandidate AdapterReviewSubjectKind = "implementation-candidate"
)

// AdapterReviewAxis is the JSON spelling of one review axis.
type AdapterReviewAxis string

const (
	AdapterReviewAxisCorrectness AdapterReviewAxis = "correctness"
	AdapterReviewAxisTestQuality AdapterReviewAxis = "test-quality"
	AdapterReviewAxisElegance    AdapterReviewAxis = "elegance"
)

// AdapterReviewVerdict is the binary review verdict on the wire.
type AdapterReviewVerdict string

const (
	AdapterReviewAccept AdapterReviewVerdict = "accept"
	AdapterReviewRevise AdapterReviewVerdict = "revise"
)

// AdapterFindingSeverity is the closed implementation-finding severity set.
type AdapterFindingSeverity string

const (
	AdapterSeverityBlocker   AdapterFindingSeverity = "blocker"
	AdapterSeverityImportant AdapterFindingSeverity = "important"
	AdapterSeverityMinor     AdapterFindingSeverity = "minor"
)

// AdapterPlanUATOutcome is the semantic Plan UAT outcome spelling.
type AdapterPlanUATOutcome string

const (
	AdapterPlanUATAccepted         AdapterPlanUATOutcome = "accepted"
	AdapterPlanUATChangesRequested AdapterPlanUATOutcome = "changes-requested"
	AdapterPlanUATDeferredByAFK    AdapterPlanUATOutcome = "deferred-by-afk"
)

// AdapterImplementationUATOutcome is the semantic Implementation UAT outcome.
type AdapterImplementationUATOutcome string

const (
	AdapterImplementationUATAccepted         AdapterImplementationUATOutcome = "accepted"
	AdapterImplementationUATChangesRequested AdapterImplementationUATOutcome = "changes-requested"
)

// AdapterResolutionKind is a carry-forward resolution spelling.
type AdapterResolutionKind string

const (
	AdapterResolutionConfirm AdapterResolutionKind = "confirm"
	AdapterResolutionDefer   AdapterResolutionKind = "defer"
	AdapterResolutionReplace AdapterResolutionKind = "replace"
)

// AdapterInvocationEnvelope is the strict private wire envelope. Input is
// decoded again through the operation-specific DTO selected by Operation.
type AdapterInvocationEnvelope struct {
	Schema           string               `json:"schema"`
	Harness          ir.HarnessID         `json:"harness"`
	HarnessVersion   string               `json:"harnessVersion"`
	HarnessContract  ir.RuntimeContractID `json:"harnessContract"`
	NativeInvocation string               `json:"nativeInvocation"`
	Operation        AdapterOperation     `json:"operation"`
	Input            json.RawMessage      `json:"input"`
}

// AdapterResultEnvelope is the public decode shape for adapter results. Result
// contains one operation-specific semantic result and no storage identities.
type AdapterResultEnvelope struct {
	Schema    string           `json:"schema"`
	Operation AdapterOperation `json:"operation"`
	Result    json.RawMessage  `json:"result"`
}

type AdapterSetInteractionModeInput struct {
	Epoch tasks.EpochRootID     `json:"epoch"`
	Mode  tasks.InteractionMode `json:"mode"`
	Actor string                `json:"actor"`
}

type AdapterShowInteractionModeInput struct {
	Epoch tasks.EpochRootID `json:"epoch"`
}

type AdapterStartReviewInput struct {
	Epoch       tasks.EpochRootID        `json:"epoch"`
	SubjectKind AdapterReviewSubjectKind `json:"subjectKind"`
	Subject     string                   `json:"subject"`
}

type AdapterPlanReviewFeedback struct {
	Body string `json:"body"`
}

type AdapterSubmitPlanReviewInput struct {
	Epoch      tasks.EpochRootID           `json:"epoch"`
	Round      tasks.ReviewRoundID         `json:"round"`
	Axis       AdapterReviewAxis           `json:"axis"`
	Assignment provenance.AssignmentID     `json:"assignment"`
	Verdict    AdapterReviewVerdict        `json:"verdict"`
	Feedback   []AdapterPlanReviewFeedback `json:"feedback"`
}

type AdapterReviewFinding struct {
	Task     string                 `json:"task"`
	Severity AdapterFindingSeverity `json:"severity"`
	Summary  string                 `json:"summary"`
}

type AdapterSubmitImplementationReviewInput struct {
	Epoch      tasks.EpochRootID       `json:"epoch"`
	Round      tasks.ReviewRoundID     `json:"round"`
	Axis       AdapterReviewAxis       `json:"axis"`
	Assignment provenance.AssignmentID `json:"assignment"`
	Verdict    AdapterReviewVerdict    `json:"verdict"`
	Findings   []AdapterReviewFinding  `json:"findings"`
}

type AdapterFinalizeReviewInput struct {
	Epoch      tasks.EpochRootID       `json:"epoch"`
	Round      tasks.ReviewRoundID     `json:"round"`
	Assignment provenance.AssignmentID `json:"assignment"`
}

type AdapterCreateSliceInput struct {
	Epoch      tasks.EpochRootID       `json:"epoch"`
	Plan       string                  `json:"plan"`
	Assignment provenance.AssignmentID `json:"assignment"`
}

type AdapterSetSliceCandidateInput struct {
	Epoch      tasks.EpochRootID       `json:"epoch"`
	Slice      string                  `json:"slice"`
	Repository tasks.RepositoryID      `json:"repository"`
	Commit     provenance.GitOID       `json:"commit"`
	Assignment provenance.AssignmentID `json:"assignment"`
}

type AdapterCloseSliceInput struct {
	Epoch       tasks.EpochRootID               `json:"epoch"`
	Slice       string                          `json:"slice"`
	Candidate   tasks.ImplementationCandidateID `json:"candidate"`
	ReviewRound tasks.ReviewRoundID             `json:"reviewRound"`
	Assignment  provenance.AssignmentID         `json:"assignment"`
}

type AdapterRepositoryCandidate struct {
	Repository tasks.RepositoryID              `json:"repository"`
	Candidate  tasks.ImplementationCandidateID `json:"candidate"`
	Commit     provenance.GitOID               `json:"commit"`
}

type AdapterCreateIntegrationCandidateInput struct {
	Epoch        tasks.EpochRootID            `json:"epoch"`
	Plan         string                       `json:"plan"`
	Assignment   provenance.AssignmentID      `json:"assignment"`
	Repositories []AdapterRepositoryCandidate `json:"repositories"`
}

type AdapterPublishRepositoryInput struct {
	Epoch      tasks.EpochRootID               `json:"epoch"`
	Candidate  tasks.IntegrationCandidateSetID `json:"candidate"`
	Repository tasks.RepositoryID              `json:"repository"`
	Ref        tasks.GitRef                    `json:"ref"`
	Commit     provenance.GitOID               `json:"commit"`
	Assignment provenance.AssignmentID         `json:"assignment"`
}

type AdapterRecordPlanUATInput struct {
	Epoch         tasks.EpochRootID       `json:"epoch"`
	Proposal      string                  `json:"proposal"`
	Outcome       AdapterPlanUATOutcome   `json:"outcome"`
	Actor         string                  `json:"actor"`
	Interactions  []tasks.UATInteraction  `json:"interactions"`
	Feedback      []tasks.UATFeedbackItem `json:"feedback"`
	HeldQuestions []tasks.HeldUATQuestion `json:"heldQuestions"`
}

type AdapterRatifyPlanInput struct {
	Epoch       tasks.EpochRootID           `json:"epoch"`
	Proposal    string                      `json:"proposal"`
	ReviewRound tasks.ReviewRoundID         `json:"reviewRound"`
	PlanUAT     tasks.DecisionLedgerEntryID `json:"planUAT"`
	Actor       string                      `json:"actor"`
}

type AdapterResolution struct {
	Target string                `json:"target"`
	Kind   AdapterResolutionKind `json:"kind"`
	Note   string                `json:"note"`
}

type AdapterRecordImplementationUATInput struct {
	Epoch           tasks.EpochRootID               `json:"epoch"`
	Candidate       tasks.IntegrationCandidateSetID `json:"candidate"`
	Outcome         AdapterImplementationUATOutcome `json:"outcome"`
	Actor           string                          `json:"actor"`
	Interactions    []tasks.UATInteraction          `json:"interactions"`
	Feedback        []tasks.UATFeedbackItem         `json:"feedback"`
	HeldAnswers     []AdapterResolution             `json:"heldAnswers"`
	PlanFeedback    []AdapterResolution             `json:"planFeedback"`
	LedgerDecisions []AdapterResolution             `json:"ledgerDecisions"`
}

type AdapterLandInput struct {
	Epoch             tasks.EpochRootID               `json:"epoch"`
	Candidate         tasks.IntegrationCandidateSetID `json:"candidate"`
	ImplementationUAT tasks.DecisionLedgerEntryID     `json:"implementationUAT"`
	Actor             string                          `json:"actor"`
}

// AdapterInvokeInput carries the hidden command's process boundaries.
type AdapterInvokeInput struct {
	DBPath string
	Input  io.Reader
	Output io.Writer
}

type adapterStore interface {
	tasks.EpochServiceFactory
	Close() error
}

type adapterStoreOpener func(string) (adapterStore, error)

type preparedAdapterInvocation struct {
	operation AdapterOperation
	invoke    func(context.Context, tasks.EpochService) (any, error)
}

type adapterCommandResult struct {
	Replayed bool   `json:"replayed"`
	Epoch    string `json:"epoch"`
}

type adapterDecisionResult struct {
	adapterCommandResult
	Decision string `json:"decision"`
	Actor    string `json:"actor"`
}

type adapterInteractionModeResult struct {
	Epoch string                `json:"epoch"`
	Mode  tasks.InteractionMode `json:"mode"`
}

type adapterReviewStartResult struct {
	adapterCommandResult
	Round       string                   `json:"round"`
	SubjectKind AdapterReviewSubjectKind `json:"subjectKind"`
	Subject     string                   `json:"subject"`
}

type adapterReviewSubmitResult struct {
	adapterCommandResult
	Round string            `json:"round"`
	Axis  AdapterReviewAxis `json:"axis"`
}

type adapterReviewFinalizeResult struct {
	adapterCommandResult
	Round string `json:"round"`
}

type adapterSliceResult struct {
	adapterCommandResult
	Slice string `json:"slice"`
}

type adapterCandidateResult struct {
	adapterCommandResult
	Slice     string `json:"slice"`
	Candidate string `json:"candidate"`
}

type adapterIntegrationCandidateResult struct {
	adapterCommandResult
	Candidate string `json:"candidate"`
}

type adapterPublicationResult struct {
	adapterCommandResult
	Candidate  string             `json:"candidate"`
	Repository tasks.RepositoryID `json:"repository"`
}

// AdapterInvoke validates and executes one hidden adapter invocation. Every
// byte and semantic field is validated before the tracker is opened.
func AdapterInvoke(ctx context.Context, in AdapterInvokeInput) error {
	return adapterInvoke(ctx, in, func(path string) (adapterStore, error) {
		tracker, err := tasks.OpenTaskTracker(path)
		if err != nil {
			return nil, err
		}
		store, ok := tracker.(adapterStore)
		if !ok {
			_ = tracker.Close()
			return nil, fmt.Errorf("opened tracker does not implement the Epoch service factory")
		}
		return store, nil
	})
}

func adapterInvoke(ctx context.Context, in AdapterInvokeInput, open adapterStoreOpener) error {
	if ctx == nil {
		return adapterValidationError("The adapter invocation has no execution context.", "The command boundary requires a cancellable context before it can validate or run a workflow operation", "invoke the adapter with the Cobra command context", nil)
	}
	if in.Input == nil {
		return adapterValidationError("The adapter invocation has no JSON input stream.", "The hidden command accepts exactly one JSON envelope on standard input", "provide one pasture.adapter-invocation/v1 envelope on stdin", nil)
	}
	if in.Output == nil {
		return adapterValidationError("The adapter invocation has no result output stream.", "A successful invocation must return one typed semantic result", "provide a writable stdout stream", nil)
	}
	if open == nil {
		return adapterValidationError("The adapter store opener is not configured.", "The production boundary cannot construct the Epoch service without its tracker opener", "wire AdapterInvoke through the production command constructor", nil)
	}
	if err := ctx.Err(); err != nil {
		return adapterWorkflowError("The adapter invocation was cancelled before validation completed.", "The supplied execution context was already cancelled", "retry the native invocation with a live context", err)
	}

	data, err := io.ReadAll(io.LimitReader(in.Input, AdapterInvocationMaxBytes+1))
	if err != nil {
		return adapterValidationError("The adapter JSON envelope could not be read.", "Standard input failed while the one bounded invocation envelope was being read", "retry with a readable stdin stream containing one complete JSON object", err)
	}
	if len(data) > AdapterInvocationMaxBytes {
		return adapterValidationError(
			fmt.Sprintf("The adapter JSON envelope exceeds the %d-byte limit.", AdapterInvocationMaxBytes),
			"Adapter input is bounded so a native hook cannot allocate unbounded memory before validation",
			"send only the semantic command fields and remove native payloads or transcripts", nil)
	}
	prepared, err := decodeAdapterInvocation(data)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return adapterWorkflowError("The adapter invocation was cancelled after validation.", "The execution context ended before the Pasture store could be opened", "retry the native invocation with a live context", err)
	}

	store, err := open(in.DBPath)
	if err != nil {
		return preserveOrWrapAdapterError(err, pasterrors.CategoryStorage,
			"The adapter could not open the Pasture store after validating its input.",
			"The Epoch service requires the configured unified store",
			"verify --db or PASTURE_DB_PATH is writable and retry the same native invocation")
	}
	service, serviceErr := store.NewEpochService(tasks.EpochServiceOptions{})
	if serviceErr != nil || service == nil {
		if serviceErr == nil {
			serviceErr = fmt.Errorf("Epoch service factory returned a nil service")
		}
		closeErr := store.Close()
		if closeErr != nil {
			serviceErr = fmt.Errorf("%w; closing the store also failed: %v", serviceErr, closeErr)
		}
		return preserveOrWrapAdapterError(serviceErr, pasterrors.CategoryWorkflow,
			"The adapter could not construct the Epoch service.",
			"The validated command must run through the same complete aggregate as the direct CLI",
			"repair the configured store or Epoch service wiring, then retry the same native invocation")
	}

	result, invokeErr := prepared.invoke(ctx, service)
	closeErr := store.Close()
	if invokeErr != nil {
		return preserveOrWrapAdapterError(invokeErr, pasterrors.CategoryWorkflow,
			fmt.Sprintf("The adapter operation %q failed.", prepared.operation),
			"The Epoch service rejected or could not commit the semantic command",
			"correct the reported authority or workflow prerequisite, then retry the same native invocation")
	}
	if closeErr != nil {
		return preserveOrWrapAdapterError(closeErr, pasterrors.CategoryStorage,
			"The adapter committed its operation but could not close the Pasture store cleanly.",
			"The database handle reported a close failure after the Epoch service returned",
			"inspect the database and retry the same native invocation; replay prevents duplicate effects")
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return adapterWorkflowError("The adapter result could not be encoded.", "The semantic result did not satisfy its fixed JSON result schema", "report the result-schema bug and retry the same native invocation after upgrading Pasture", err)
	}
	wire, err := json.Marshal(AdapterResultEnvelope{Schema: AdapterResultSchema, Operation: prepared.operation, Result: resultJSON})
	if err != nil {
		return adapterWorkflowError("The adapter result envelope could not be encoded.", "The validated operation result could not be wrapped in pasture.adapter-result/v1", "report the result-envelope bug and retry after upgrading Pasture", err)
	}
	wire = append(wire, '\n')
	for len(wire) > 0 {
		n, writeErr := in.Output.Write(wire)
		if writeErr != nil {
			return adapterWorkflowError("The adapter result could not be written to standard output.", "The output stream failed after the semantic operation completed", "repair the output pipe and retry the same native invocation; replay prevents duplicate effects", writeErr)
		}
		if n == 0 {
			return adapterWorkflowError("The adapter result could not be written to standard output.", "The output stream accepted zero bytes without reporting an error", "repair the output pipe and retry the same native invocation; replay prevents duplicate effects", io.ErrShortWrite)
		}
		wire = wire[n:]
	}
	return nil
}

func decodeAdapterInvocation(data []byte) (preparedAdapterInvocation, error) {
	if !utf8.Valid(data) {
		return preparedAdapterInvocation{}, adapterValidationError("The adapter envelope is not valid UTF-8 JSON.", "JSON decoding would otherwise replace malformed bytes and lose the native invocation's exact meaning", "encode the complete envelope as valid UTF-8", nil)
	}
	var envelope AdapterInvocationEnvelope
	required := []string{"schema", "harness", "harnessVersion", "harnessContract", "nativeInvocation", "operation", "input"}
	if err := ir.StrictJSONWithPresence(data, required, &envelope); err != nil {
		return preparedAdapterInvocation{}, adapterValidationError("The adapter envelope is not one strict pasture.adapter-invocation/v1 JSON object.", "The envelope is malformed, has a duplicate or unknown field, omits a required field, or contains trailing JSON", "send exactly schema, harness, harnessVersion, harnessContract, nativeInvocation, operation, and input", err)
	}
	if envelope.Schema != AdapterInvocationSchema {
		return preparedAdapterInvocation{}, adapterValidationError(fmt.Sprintf("The adapter envelope schema %q is unsupported.", envelope.Schema), "This binary decodes exactly pasture.adapter-invocation/v1 and does not guess compatibility", "regenerate the adapter for pasture.adapter-invocation/v1", nil)
	}
	if err := validateAdapterNativeInvocation(envelope.NativeInvocation); err != nil {
		return preparedAdapterInvocation{}, err
	}
	if err := validateAdapterHarness(envelope); err != nil {
		return preparedAdapterInvocation{}, err
	}
	meta := tasks.CommandMeta{OperationID: adapterOperationID(envelope.HarnessContract, envelope.NativeInvocation)}

	switch envelope.Operation {
	case AdapterOperationSetInteractionMode:
		wire, err := strictAdapterInput[AdapterSetInteractionModeInput](envelope.Input, "epoch", "mode", "actor")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		if wire.Mode != tasks.InteractionNormal && wire.Mode != tasks.InteractionAFK {
			return preparedAdapterInvocation{}, adapterFieldError("mode", string(wire.Mode), "use normal or afk", nil)
		}
		actor, err := parseAdapterActor(wire.Actor)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.SetInteractionModeInput{Meta: meta, Epoch: epoch, Mode: wire.Mode, Actor: tasks.AssertedHumanActor{ActorID: actor}}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.SetInteractionMode(ctx, input)
			return adapterDecisionFrom(result), err
		}}, nil

	case AdapterOperationShowInteractionMode:
		wire, err := strictAdapterInput[AdapterShowInteractionModeInput](envelope.Input, "epoch")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.ShowInteractionMode(ctx, epoch)
			return adapterInteractionModeResult{Epoch: string(epoch), Mode: result.Mode}, err
		}}, nil

	case AdapterOperationStartReview:
		wire, err := strictAdapterInput[AdapterStartReviewInput](envelope.Input, "epoch", "subjectKind", "subject")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		subject, err := parseAdapterTask(wire.Subject, "subject")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		kind, err := parseAdapterReviewSubjectKind(wire.SubjectKind)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.StartReviewInput{Meta: meta, Epoch: epoch, Subject: tasks.ReviewSubjectRef{Kind: kind, SnapshotID: subject.String()}}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.StartReview(ctx, input)
			return adapterReviewStartFrom(result), err
		}}, nil

	case AdapterOperationSubmitPlanReview:
		wire, err := strictAdapterInput[AdapterSubmitPlanReviewInput](envelope.Input, "epoch", "round", "axis", "assignment", "verdict", "feedback")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, round, axis, assignment, err := parseAdapterReviewCommand(wire.Epoch, wire.Round, wire.Axis, wire.Assignment)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		verdict, err := parseAdapterReviewVerdict(wire.Verdict)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		if wire.Feedback == nil || len(wire.Feedback) > adapterCollectionMaxItems {
			return preparedAdapterInvocation{}, adapterCollectionError("feedback", len(wire.Feedback), true)
		}
		feedback := make([]tasks.PlanReviewFeedback, len(wire.Feedback))
		for i, item := range wire.Feedback {
			if strings.TrimSpace(item.Body) == "" {
				return preparedAdapterInvocation{}, adapterFieldError(fmt.Sprintf("feedback[%d].body", i), item.Body, "supply actionable non-empty feedback or an empty feedback array for acceptance", nil)
			}
			feedback[i] = tasks.PlanReviewFeedback{Body: item.Body}
		}
		submission := tasks.PlanReviewSubmission{Verdict: verdict, Feedback: feedback}
		if err := submission.Validate(); err != nil {
			return preparedAdapterInvocation{}, preserveOrWrapAdapterError(err, pasterrors.CategoryValidation, "The plan-review submission is not semantically valid.", "Plan review acceptance and revision feedback must agree", "correct the verdict and feedback before retrying")
		}
		input := tasks.SubmitReviewInput{Meta: meta, Epoch: epoch, Round: round, Axis: axis, Assignment: assignment, Submission: submission}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.SubmitReview(ctx, input)
			return adapterReviewSubmitFrom(result), err
		}}, nil

	case AdapterOperationSubmitImplementationReview:
		wire, err := strictAdapterInput[AdapterSubmitImplementationReviewInput](envelope.Input, "epoch", "round", "axis", "assignment", "verdict", "findings")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, round, axis, assignment, err := parseAdapterReviewCommand(wire.Epoch, wire.Round, wire.Axis, wire.Assignment)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		verdict, err := parseAdapterReviewVerdict(wire.Verdict)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		if wire.Findings == nil || len(wire.Findings) > adapterCollectionMaxItems {
			return preparedAdapterInvocation{}, adapterCollectionError("findings", len(wire.Findings), true)
		}
		findings := make([]tasks.ReviewFinding, len(wire.Findings))
		for i, item := range wire.Findings {
			task, err := parseAdapterTask(item.Task, fmt.Sprintf("findings[%d].task", i))
			if err != nil {
				return preparedAdapterInvocation{}, err
			}
			severity, err := parseAdapterFindingSeverity(item.Severity)
			if err != nil {
				return preparedAdapterInvocation{}, err
			}
			if strings.TrimSpace(item.Summary) == "" {
				return preparedAdapterInvocation{}, adapterFieldError(fmt.Sprintf("findings[%d].summary", i), item.Summary, "supply an actionable non-empty finding summary", nil)
			}
			findings[i] = tasks.ReviewFinding{Task: task, Severity: severity, Summary: item.Summary}
		}
		submission := tasks.ImplementationReviewSubmission{Verdict: verdict, Findings: findings}
		if err := submission.Validate(); err != nil {
			return preparedAdapterInvocation{}, preserveOrWrapAdapterError(err, pasterrors.CategoryValidation, "The implementation-review submission is not semantically valid.", "The binary verdict must agree with the typed finding severities", "correct the verdict and findings before retrying")
		}
		input := tasks.SubmitReviewInput{Meta: meta, Epoch: epoch, Round: round, Axis: axis, Assignment: assignment, Submission: submission}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.SubmitReview(ctx, input)
			return adapterReviewSubmitFrom(result), err
		}}, nil

	case AdapterOperationFinalizeReview:
		wire, err := strictAdapterInput[AdapterFinalizeReviewInput](envelope.Input, "epoch", "round", "assignment")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		round, err := parseAdapterTaskWrapper(string(wire.Round), "round")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		assignment, err := parseAdapterAssignment(wire.Assignment)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.FinalizeReviewInput{Meta: meta, Epoch: epoch, Round: tasks.ReviewRoundID(round), Assignment: assignment}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.FinalizeReview(ctx, input)
			return adapterReviewFinalizeResult{adapterCommandResult: adapterCommandFrom(result.CommandResult), Round: string(result.Round)}, err
		}}, nil

	case AdapterOperationCreateSlice:
		wire, err := strictAdapterInput[AdapterCreateSliceInput](envelope.Input, "epoch", "plan", "assignment")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		plan, err := parseAdapterTask(wire.Plan, "plan")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		assignment, err := parseAdapterAssignment(wire.Assignment)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.CreateSliceInput{Meta: meta, Epoch: epoch, Plan: plan, Assignment: assignment}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.CreateSlice(ctx, input)
			return adapterSliceResult{adapterCommandResult: adapterCommandFrom(result.CommandResult), Slice: result.Slice.String()}, err
		}}, nil

	case AdapterOperationSetSliceCandidate:
		wire, err := strictAdapterInput[AdapterSetSliceCandidateInput](envelope.Input, "epoch", "slice", "repository", "commit", "assignment")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		slice, err := parseAdapterTask(wire.Slice, "slice")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		repository, err := parseAdapterRepository(wire.Repository)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		commit, err := parseAdapterGitOID(wire.Commit)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		assignment, err := parseAdapterAssignment(wire.Assignment)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.SetSliceCandidateInput{Meta: meta, Epoch: epoch, Slice: slice, Repository: repository, Commit: commit, Assignment: assignment}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.SetSliceCandidate(ctx, input)
			return adapterCandidateFrom(result), err
		}}, nil

	case AdapterOperationCloseSlice:
		wire, err := strictAdapterInput[AdapterCloseSliceInput](envelope.Input, "epoch", "slice", "candidate", "reviewRound", "assignment")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		slice, err := parseAdapterTask(wire.Slice, "slice")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		candidate, err := parseAdapterTaskWrapper(string(wire.Candidate), "candidate")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		round, err := parseAdapterTaskWrapper(string(wire.ReviewRound), "reviewRound")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		assignment, err := parseAdapterAssignment(wire.Assignment)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.CloseSliceInput{Meta: meta, Epoch: epoch, Slice: slice, Candidate: tasks.ImplementationCandidateID(candidate), ReviewRound: tasks.ReviewRoundID(round), Assignment: assignment}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.CloseSlice(ctx, input)
			return adapterCommandFrom(result), err
		}}, nil

	case AdapterOperationCreateIntegrationCandidate:
		wire, err := strictAdapterInput[AdapterCreateIntegrationCandidateInput](envelope.Input, "epoch", "plan", "assignment", "repositories")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		plan, err := parseAdapterTask(wire.Plan, "plan")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		assignment, err := parseAdapterAssignment(wire.Assignment)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		if len(wire.Repositories) == 0 || len(wire.Repositories) > adapterCollectionMaxItems {
			return preparedAdapterInvocation{}, adapterCollectionError("repositories", len(wire.Repositories), false)
		}
		repositories := make([]tasks.RepositoryCandidate, len(wire.Repositories))
		seen := make(map[tasks.RepositoryID]struct{}, len(wire.Repositories))
		for i, item := range wire.Repositories {
			repository, err := parseAdapterRepository(item.Repository)
			if err != nil {
				return preparedAdapterInvocation{}, err
			}
			if _, duplicate := seen[repository]; duplicate {
				return preparedAdapterInvocation{}, adapterFieldError(fmt.Sprintf("repositories[%d].repository", i), string(repository), "list each repository exactly once", nil)
			}
			seen[repository] = struct{}{}
			candidate, err := parseAdapterTaskWrapper(string(item.Candidate), fmt.Sprintf("repositories[%d].candidate", i))
			if err != nil {
				return preparedAdapterInvocation{}, err
			}
			commit, err := parseAdapterGitOID(item.Commit)
			if err != nil {
				return preparedAdapterInvocation{}, err
			}
			repositories[i] = tasks.RepositoryCandidate{Repository: repository, Candidate: tasks.ImplementationCandidateID(candidate), Commit: commit}
		}
		input := tasks.CreateIntegrationCandidateInput{Meta: meta, Epoch: epoch, Plan: plan, Assignment: assignment, Repositories: repositories}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.CreateIntegrationCandidate(ctx, input)
			return adapterIntegrationCandidateResult{adapterCommandResult: adapterCommandFrom(result.CommandResult), Candidate: string(result.Candidate)}, err
		}}, nil

	case AdapterOperationPublishRepository:
		wire, err := strictAdapterInput[AdapterPublishRepositoryInput](envelope.Input, "epoch", "candidate", "repository", "ref", "commit", "assignment")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		candidate, err := parseAdapterTaskWrapper(string(wire.Candidate), "candidate")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		repository, err := parseAdapterRepository(wire.Repository)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		ref, err := parseAdapterGitRef(wire.Ref)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		commit, err := parseAdapterGitOID(wire.Commit)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		assignment, err := parseAdapterAssignment(wire.Assignment)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.PublishRepositoryInput{Meta: meta, Epoch: epoch, Candidate: tasks.IntegrationCandidateSetID(candidate), Repository: repository, Ref: ref, Commit: commit, Assignment: assignment}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.PublishRepository(ctx, input)
			return adapterPublicationResult{adapterCommandResult: adapterCommandFrom(result.CommandResult), Candidate: string(result.Candidate), Repository: result.Repository}, err
		}}, nil

	case AdapterOperationRecordPlanUAT:
		wire, err := strictAdapterInput[AdapterRecordPlanUATInput](envelope.Input, "epoch", "proposal", "outcome", "actor", "interactions", "feedback", "heldQuestions")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		proposal, err := parseAdapterTask(wire.Proposal, "proposal")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		outcome, err := parseAdapterPlanUATOutcome(wire.Outcome)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		actor, err := parseAdapterActor(wire.Actor)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		if err := validateAdapterPlanUATPayload(wire); err != nil {
			return preparedAdapterInvocation{}, err
		}
		payload := &tasks.PlanUATPayload{Interactions: append([]tasks.UATInteraction(nil), wire.Interactions...), Feedback: append([]tasks.UATFeedbackItem(nil), wire.Feedback...), HeldQuestions: append([]tasks.HeldUATQuestion(nil), wire.HeldQuestions...)}
		input := tasks.PlanUATInput{Meta: meta, Epoch: epoch, Proposal: proposal, Outcome: outcome, Actor: tasks.AssertedHumanActor{ActorID: actor}, Payload: payload}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.RecordPlanUAT(ctx, input)
			return adapterDecisionFrom(result), err
		}}, nil

	case AdapterOperationRatifyPlan:
		wire, err := strictAdapterInput[AdapterRatifyPlanInput](envelope.Input, "epoch", "proposal", "reviewRound", "planUAT", "actor")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		proposal, err := parseAdapterTask(wire.Proposal, "proposal")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		round, err := parseAdapterTaskWrapper(string(wire.ReviewRound), "reviewRound")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		planUAT, err := parseAdapterScalar(string(wire.PlanUAT), "planUAT")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		actor, err := parseAdapterActor(wire.Actor)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.RatifyPlanInput{Meta: meta, Epoch: epoch, Proposal: proposal, ReviewRound: tasks.ReviewRoundID(round), PlanUAT: tasks.DecisionLedgerEntryID(planUAT), Actor: tasks.AssertedHumanActor{ActorID: actor}}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.RatifyPlan(ctx, input)
			return adapterDecisionFrom(result), err
		}}, nil

	case AdapterOperationRecordImplementationUAT:
		wire, err := strictAdapterInput[AdapterRecordImplementationUATInput](envelope.Input, "epoch", "candidate", "outcome", "actor", "interactions", "feedback", "heldAnswers", "planFeedback", "ledgerDecisions")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		candidate, err := parseAdapterTaskWrapper(string(wire.Candidate), "candidate")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		outcome, err := parseAdapterImplementationUATOutcome(wire.Outcome)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		actor, err := parseAdapterActor(wire.Actor)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		payload, err := convertAdapterImplementationUATPayload(wire)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.ImplementationUATInput{Meta: meta, Epoch: epoch, Candidate: tasks.IntegrationCandidateSetID(candidate), Outcome: outcome, Actor: tasks.AssertedHumanActor{ActorID: actor}, Payload: &payload}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.RecordImplementationUAT(ctx, input)
			return adapterDecisionFrom(result), err
		}}, nil

	case AdapterOperationLand:
		wire, err := strictAdapterInput[AdapterLandInput](envelope.Input, "epoch", "candidate", "implementationUAT", "actor")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		epoch, err := parseAdapterEpoch(wire.Epoch)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		candidate, err := parseAdapterTaskWrapper(string(wire.Candidate), "candidate")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		implementationUAT, err := parseAdapterScalar(string(wire.ImplementationUAT), "implementationUAT")
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		actor, err := parseAdapterActor(wire.Actor)
		if err != nil {
			return preparedAdapterInvocation{}, err
		}
		input := tasks.LandInput{Meta: meta, Epoch: epoch, Candidate: tasks.IntegrationCandidateSetID(candidate), ImplementationUAT: tasks.DecisionLedgerEntryID(implementationUAT), Actor: tasks.AssertedHumanActor{ActorID: actor}}
		return preparedAdapterInvocation{operation: envelope.Operation, invoke: func(ctx context.Context, service tasks.EpochService) (any, error) {
			result, err := service.Land(ctx, input)
			return adapterDecisionFrom(result), err
		}}, nil
	default:
		allowed := make([]string, len(adapterOperations))
		for i, operation := range adapterOperations {
			allowed[i] = string(operation)
		}
		return preparedAdapterInvocation{}, adapterValidationError(fmt.Sprintf("The adapter operation %q is unsupported.", envelope.Operation), "The hidden boundary accepts only statically declared EpochService operations that do not expose storage identities", "use one of: "+strings.Join(allowed, ", "), nil)
	}
}

func strictAdapterInput[T any](raw json.RawMessage, required ...string) (T, error) {
	var target T
	if !utf8.Valid(raw) {
		return target, adapterValidationError("The adapter operation input is not valid UTF-8 JSON.", "Semantic input must retain its exact bytes through strict decoding", "encode the command-specific input as valid UTF-8 JSON", nil)
	}
	if err := ir.StrictJSONWithPresence(raw, required, &target); err != nil {
		return target, adapterValidationError("The adapter operation input does not match its strict semantic schema.", "The command input is malformed, has a duplicate or unknown field, omits a required field, or contains trailing JSON", "send only the documented fields for the selected semantic operation", err)
	}
	return target, nil
}

func validateAdapterHarness(envelope AdapterInvocationEnvelope) error {
	if !envelope.Harness.IsValid() {
		return adapterFieldError("harness", string(envelope.Harness), "use claude-code, codex, or opencode", nil)
	}
	if !envelope.HarnessContract.IsValid() {
		return adapterFieldError("harnessContract", envelope.HarnessContract.String(), "use a constructor-validated pinned runtime contract", nil)
	}
	if envelope.HarnessContract.Harness() != envelope.Harness {
		return adapterValidationError(fmt.Sprintf("The harness %q does not match contract %q.", envelope.Harness, envelope.HarnessContract), "A pinned runtime contract is bound to exactly one native harness", "use the harness encoded by the selected harnessContract", nil)
	}
	version, err := runtime.ParseHostVersion(envelope.HarnessVersion)
	if err != nil {
		return adapterFieldError("harnessVersion", envelope.HarnessVersion, "supply the exact native host MAJOR.MINOR.PATCH version", err)
	}
	for _, contract := range runtime.PinnedContracts() {
		if contract.ID() != envelope.HarnessContract {
			continue
		}
		if !contract.Supports(version) {
			return adapterValidationError(fmt.Sprintf("Harness version %q is incompatible with contract %q.", envelope.HarnessVersion, envelope.HarnessContract), "Generated adapters are valid only for the version range pinned by their reviewed runtime contract", "regenerate the adapter for this host version or run the pinned host version", nil)
		}
		return nil
	}
	return adapterValidationError(fmt.Sprintf("Harness contract %q is not pinned by this Pasture binary.", envelope.HarnessContract), "The adapter cannot guess semantics for an unregistered runtime contract", "regenerate the adapter using one of this binary's pinned runtime contracts", nil)
}

func validateAdapterNativeInvocation(value string) error {
	if !utf8.ValidString(value) || value == "" || strings.TrimSpace(value) != value || len(value) > adapterNativeInvocationMaxBytes {
		return adapterValidationError("The private native invocation identity is empty, padded, malformed, or too long.", "A bounded exact identity is required to derive one stable replay key without exposing it as a public operation ID", fmt.Sprintf("supply a valid UTF-8 identity of at most %d bytes without surrounding whitespace", adapterNativeInvocationMaxBytes), nil)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return adapterValidationError("The private native invocation identity contains whitespace or control data.", "A native invocation needs one byte-stable spelling across process and store restarts", "remove whitespace and control characters from nativeInvocation", nil)
		}
	}
	return nil
}

func adapterOperationID(contract ir.RuntimeContractID, nativeInvocation string) provenance.OperationID {
	hash := sha256.New()
	_, _ = hash.Write([]byte(AdapterInvocationSchema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(contract.String()))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(nativeInvocation))
	return provenance.OperationID("pasture.adapter." + hex.EncodeToString(hash.Sum(nil)))
}

func parseAdapterEpoch(value tasks.EpochRootID) (tasks.EpochRootID, error) {
	canonical, err := parseAdapterTaskWrapper(string(value), "epoch")
	return tasks.EpochRootID(canonical), err
}

func parseAdapterTask(value, field string) (provenance.TaskID, error) {
	id, err := provenance.ParseTaskID(value)
	if err != nil || id.String() != value {
		return provenance.TaskID{}, adapterFieldError(field, value, "supply the canonical namespace--UUID task identity", err)
	}
	return id, nil
}

func parseAdapterTaskWrapper(value, field string) (string, error) {
	id, err := parseAdapterTask(value, field)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func parseAdapterActor(value string) (provenance.ActorID, error) {
	id, err := provenance.ParseActorID(value)
	if err != nil || id.String() != value {
		return provenance.ActorID{}, adapterFieldError("actor", value, "supply the canonical namespace--UUID registered actor identity", err)
	}
	return id, nil
}

func parseAdapterAssignment(value provenance.AssignmentID) (provenance.AssignmentID, error) {
	canonical, err := parseAdapterScalar(string(value), "assignment")
	return provenance.AssignmentID(canonical), err
}

func parseAdapterScalar(value, field string) (string, error) {
	if !utf8.ValidString(value) || value == "" || strings.TrimSpace(value) != value {
		return "", adapterFieldError(field, value, "supply a non-empty canonical UTF-8 identity without surrounding whitespace", nil)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", adapterFieldError(field, value, "remove whitespace and control characters", nil)
		}
	}
	return value, nil
}

func parseAdapterRepository(value tasks.RepositoryID) (tasks.RepositoryID, error) {
	canonical, err := parseAdapterScalar(string(value), "repository")
	return tasks.RepositoryID(canonical), err
}

func parseAdapterGitOID(value provenance.GitOID) (provenance.GitOID, error) {
	if !utf8.ValidString(string(value)) {
		return "", adapterFieldError("commit", string(value), "supply a lowercase 40- or 64-hex Git object identity", nil)
	}
	if _, err := provenance.GitContext(value); err != nil {
		return "", adapterFieldError("commit", string(value), "supply a lowercase 40- or 64-hex Git object identity", err)
	}
	return value, nil
}

func parseAdapterGitRef(ref tasks.GitRef) (tasks.GitRef, error) {
	value := string(ref)
	if !utf8.ValidString(value) || value == "@" || !strings.Contains(value, "/") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "//") {
		return "", adapterFieldError("ref", value, "supply a ref accepted by git-check-ref-format, such as refs/heads/main", nil)
	}
	for _, r := range value {
		if r <= 0x1f || r == 0x7f {
			return "", adapterFieldError("ref", value, "remove control characters and supply a ref accepted by git-check-ref-format", nil)
		}
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return "", adapterFieldError("ref", value, "supply a ref accepted by git-check-ref-format, such as refs/heads/main", nil)
		}
	}
	return ref, nil
}

func parseAdapterReviewSubjectKind(value AdapterReviewSubjectKind) (tasks.ReviewSubjectKind, error) {
	switch value {
	case AdapterReviewSubjectDocumentRevision:
		return tasks.ReviewSubjectDocumentRevision, nil
	case AdapterReviewSubjectImplementationCandidate:
		return tasks.ReviewSubjectImplementationCandidate, nil
	default:
		return 0, adapterFieldError("subjectKind", string(value), "use document-revision or implementation-candidate", nil)
	}
}

func parseAdapterReviewAxis(value AdapterReviewAxis) (tasks.ReviewAxis, error) {
	switch value {
	case AdapterReviewAxisCorrectness:
		return tasks.AxisCorrectness, nil
	case AdapterReviewAxisTestQuality:
		return tasks.AxisTestQuality, nil
	case AdapterReviewAxisElegance:
		return tasks.AxisElegance, nil
	default:
		return 0, adapterFieldError("axis", string(value), "use correctness, test-quality, or elegance", nil)
	}
}

func parseAdapterReviewVerdict(value AdapterReviewVerdict) (tasks.Verdict, error) {
	switch value {
	case AdapterReviewAccept:
		return tasks.VerdictAccept, nil
	case AdapterReviewRevise:
		return tasks.VerdictRevise, nil
	default:
		return 0, adapterFieldError("verdict", string(value), "use accept or revise", nil)
	}
}

func parseAdapterFindingSeverity(value AdapterFindingSeverity) (tasks.FindingSeverity, error) {
	switch value {
	case AdapterSeverityBlocker:
		return tasks.SeverityBlocker, nil
	case AdapterSeverityImportant:
		return tasks.SeverityImportant, nil
	case AdapterSeverityMinor:
		return tasks.SeverityMinor, nil
	default:
		return 0, adapterFieldError("severity", string(value), "use blocker, important, or minor", nil)
	}
}

func parseAdapterReviewCommand(epochValue tasks.EpochRootID, roundValue tasks.ReviewRoundID, axisValue AdapterReviewAxis, assignmentValue provenance.AssignmentID) (tasks.EpochRootID, tasks.ReviewRoundID, tasks.ReviewAxis, provenance.AssignmentID, error) {
	epoch, err := parseAdapterEpoch(epochValue)
	if err != nil {
		return "", "", 0, "", err
	}
	round, err := parseAdapterTaskWrapper(string(roundValue), "round")
	if err != nil {
		return "", "", 0, "", err
	}
	axis, err := parseAdapterReviewAxis(axisValue)
	if err != nil {
		return "", "", 0, "", err
	}
	assignment, err := parseAdapterAssignment(assignmentValue)
	if err != nil {
		return "", "", 0, "", err
	}
	return epoch, tasks.ReviewRoundID(round), axis, assignment, nil
}

func parseAdapterPlanUATOutcome(value AdapterPlanUATOutcome) (tasks.PlanUATVerdict, error) {
	switch value {
	case AdapterPlanUATAccepted:
		return tasks.PlanUATAccepted, nil
	case AdapterPlanUATChangesRequested:
		return tasks.PlanUATChangesRequested, nil
	case AdapterPlanUATDeferredByAFK:
		return tasks.PlanUATDeferredByAFK, nil
	default:
		return 0, adapterFieldError("outcome", string(value), "use accepted, changes-requested, or deferred-by-afk", nil)
	}
}

func parseAdapterImplementationUATOutcome(value AdapterImplementationUATOutcome) (tasks.ImplementationUATVerdict, error) {
	switch value {
	case AdapterImplementationUATAccepted:
		return tasks.ImplUATAccepted, nil
	case AdapterImplementationUATChangesRequested:
		return tasks.ImplUATChangesRequested, nil
	default:
		return 0, adapterFieldError("outcome", string(value), "use accepted or changes-requested", nil)
	}
}

func parseAdapterResolutionKind(value AdapterResolutionKind) (tasks.ResolutionKind, error) {
	switch value {
	case AdapterResolutionConfirm:
		return tasks.ResolutionConfirm, nil
	case AdapterResolutionDefer:
		return tasks.ResolutionDefer, nil
	case AdapterResolutionReplace:
		return tasks.ResolutionReplace, nil
	default:
		return 0, adapterFieldError("kind", string(value), "use confirm, defer, or replace", nil)
	}
}

func validateAdapterPlanUATPayload(wire AdapterRecordPlanUATInput) error {
	if wire.Interactions == nil || wire.Feedback == nil || wire.HeldQuestions == nil {
		return adapterValidationError("The Plan UAT collections must be JSON arrays.", "Null collections make command meaning ambiguous across adapters", "encode interactions, feedback, and heldQuestions as arrays, using [] when empty", nil)
	}
	if len(wire.Interactions) > adapterCollectionMaxItems || len(wire.Feedback) > adapterCollectionMaxItems || len(wire.HeldQuestions) > adapterCollectionMaxItems {
		return adapterCollectionError("Plan UAT", max(len(wire.Interactions), len(wire.Feedback), len(wire.HeldQuestions)), true)
	}
	if err := validateAdapterInteractions(wire.Interactions); err != nil {
		return err
	}
	fixNow, err := validateAdapterFeedback(wire.Feedback)
	if err != nil {
		return err
	}
	seen := make(map[tasks.HeldUATQuestionID]struct{}, len(wire.HeldQuestions))
	hasStable := false
	for i, question := range wire.HeldQuestions {
		if question.ID == "" || strings.TrimSpace(question.Question) == "" {
			return adapterFieldError(fmt.Sprintf("heldQuestions[%d]", i), string(question.ID), "supply a non-empty unique id and question", nil)
		}
		if _, duplicate := seen[question.ID]; duplicate {
			return adapterFieldError(fmt.Sprintf("heldQuestions[%d].id", i), string(question.ID), "use each held-question identity exactly once", nil)
		}
		seen[question.ID] = struct{}{}
		hasStable = hasStable || question.Stable
	}
	if wire.Outcome == AdapterPlanUATAccepted && fixNow {
		return adapterValidationError("An accepted Plan UAT carries FIX-NOW feedback.", "Blocking feedback requires changes rather than acceptance", "use changes-requested or mark the feedback non-blocking", nil)
	}
	if wire.Outcome == AdapterPlanUATDeferredByAFK && (!hasStable || fixNow) {
		return adapterValidationError("The deferred Plan UAT lacks a stable held question or carries FIX-NOW feedback.", "AFK deferral is reserved for stable open questions without blocking feedback", "add a stable held question and resolve all FIX-NOW feedback before deferring", nil)
	}
	return nil
}

func convertAdapterImplementationUATPayload(wire AdapterRecordImplementationUATInput) (tasks.ImplUATPayload, error) {
	if wire.Interactions == nil || wire.Feedback == nil || wire.HeldAnswers == nil || wire.PlanFeedback == nil || wire.LedgerDecisions == nil {
		return tasks.ImplUATPayload{}, adapterValidationError("The Implementation UAT collections must be JSON arrays.", "Null collections make carry-forward resolution ambiguous across adapters", "encode every collection as an array, using [] when empty", nil)
	}
	for name, length := range map[string]int{
		"interactions": len(wire.Interactions), "feedback": len(wire.Feedback), "heldAnswers": len(wire.HeldAnswers), "planFeedback": len(wire.PlanFeedback), "ledgerDecisions": len(wire.LedgerDecisions),
	} {
		if length > adapterCollectionMaxItems {
			return tasks.ImplUATPayload{}, adapterCollectionError(name, length, true)
		}
	}
	if err := validateAdapterInteractions(wire.Interactions); err != nil {
		return tasks.ImplUATPayload{}, err
	}
	fixNow, err := validateAdapterFeedback(wire.Feedback)
	if err != nil {
		return tasks.ImplUATPayload{}, err
	}
	held, heldReplace, err := convertAdapterHeldResolutions(wire.HeldAnswers)
	if err != nil {
		return tasks.ImplUATPayload{}, err
	}
	plan, planReplace, err := convertAdapterFeedbackResolutions(wire.PlanFeedback)
	if err != nil {
		return tasks.ImplUATPayload{}, err
	}
	ledger, ledgerReplace, err := convertAdapterLedgerResolutions(wire.LedgerDecisions)
	if err != nil {
		return tasks.ImplUATPayload{}, err
	}
	if wire.Outcome == AdapterImplementationUATAccepted && (fixNow || heldReplace || planReplace || ledgerReplace) {
		return tasks.ImplUATPayload{}, adapterValidationError("An accepted Implementation UAT carries FIX-NOW feedback or a replacement resolution.", "Blocking feedback and replacement work require changes rather than acceptance", "use changes-requested or remove the blocking/replacement item", nil)
	}
	return tasks.ImplUATPayload{
		Interactions:    append([]tasks.UATInteraction(nil), wire.Interactions...),
		Feedback:        append([]tasks.UATFeedbackItem(nil), wire.Feedback...),
		HeldAnswers:     held,
		PlanFeedback:    plan,
		LedgerDecisions: ledger,
	}, nil
}

func validateAdapterInteractions(items []tasks.UATInteraction) error {
	for i, interaction := range items {
		if interaction.Prompt == "" || interaction.Response == "" {
			return adapterFieldError(fmt.Sprintf("interactions[%d]", i), "", "supply both the exact non-empty prompt and response", nil)
		}
	}
	return nil
}

func validateAdapterFeedback(items []tasks.UATFeedbackItem) (bool, error) {
	seen := make(map[tasks.UATFeedbackID]struct{}, len(items))
	fixNow := false
	for i, item := range items {
		if item.ID == "" || strings.TrimSpace(item.Body) == "" {
			return false, adapterFieldError(fmt.Sprintf("feedback[%d]", i), string(item.ID), "supply a non-empty unique id and actionable body", nil)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return false, adapterFieldError(fmt.Sprintf("feedback[%d].id", i), string(item.ID), "use each feedback identity exactly once", nil)
		}
		seen[item.ID] = struct{}{}
		fixNow = fixNow || item.FixNow
	}
	return fixNow, nil
}

func convertAdapterHeldResolutions(items []AdapterResolution) ([]tasks.HeldQuestionResolution, bool, error) {
	result := make([]tasks.HeldQuestionResolution, len(items))
	seen := make(map[string]struct{}, len(items))
	replaced := false
	for i, item := range items {
		target, kind, err := parseAdapterResolution(item, i, seen)
		if err != nil {
			return nil, false, err
		}
		result[i] = tasks.HeldQuestionResolution{Target: tasks.HeldUATQuestionID(target), Kind: kind, Note: item.Note}
		replaced = replaced || kind == tasks.ResolutionReplace
	}
	return result, replaced, nil
}

func convertAdapterFeedbackResolutions(items []AdapterResolution) ([]tasks.DeferredFeedbackResolution, bool, error) {
	result := make([]tasks.DeferredFeedbackResolution, len(items))
	seen := make(map[string]struct{}, len(items))
	replaced := false
	for i, item := range items {
		target, kind, err := parseAdapterResolution(item, i, seen)
		if err != nil {
			return nil, false, err
		}
		result[i] = tasks.DeferredFeedbackResolution{Target: tasks.UATFeedbackID(target), Kind: kind, Note: item.Note}
		replaced = replaced || kind == tasks.ResolutionReplace
	}
	return result, replaced, nil
}

func convertAdapterLedgerResolutions(items []AdapterResolution) ([]tasks.LedgerDecisionResolution, bool, error) {
	result := make([]tasks.LedgerDecisionResolution, len(items))
	seen := make(map[string]struct{}, len(items))
	replaced := false
	for i, item := range items {
		target, kind, err := parseAdapterResolution(item, i, seen)
		if err != nil {
			return nil, false, err
		}
		result[i] = tasks.LedgerDecisionResolution{Target: tasks.DecisionLedgerEntryID(target), Kind: kind, Note: item.Note}
		replaced = replaced || kind == tasks.ResolutionReplace
	}
	return result, replaced, nil
}

func parseAdapterResolution(item AdapterResolution, index int, seen map[string]struct{}) (string, tasks.ResolutionKind, error) {
	target, err := parseAdapterScalar(item.Target, fmt.Sprintf("resolution[%d].target", index))
	if err != nil {
		return "", 0, err
	}
	if _, duplicate := seen[target]; duplicate {
		return "", 0, adapterFieldError(fmt.Sprintf("resolution[%d].target", index), target, "resolve each target exactly once in its collection", nil)
	}
	seen[target] = struct{}{}
	kind, err := parseAdapterResolutionKind(item.Kind)
	if err != nil {
		return "", 0, err
	}
	return target, kind, nil
}

func adapterCommandFrom(result tasks.CommandResult) adapterCommandResult {
	return adapterCommandResult{Replayed: result.Replayed, Epoch: string(result.Epoch)}
}

func adapterDecisionFrom(result tasks.DecisionResult) adapterDecisionResult {
	return adapterDecisionResult{adapterCommandResult: adapterCommandFrom(result.CommandResult), Decision: string(result.DecisionID), Actor: result.ActorID.String()}
}

func adapterReviewStartFrom(result tasks.ReviewStartResult) adapterReviewStartResult {
	return adapterReviewStartResult{
		adapterCommandResult: adapterCommandFrom(result.CommandResult),
		Round:                string(result.Round), SubjectKind: adapterReviewSubjectKind(result.Subject.Kind), Subject: result.Subject.SnapshotID,
	}
}

func adapterReviewSubmitFrom(result tasks.ReviewSubmitResult) adapterReviewSubmitResult {
	return adapterReviewSubmitResult{adapterCommandResult: adapterCommandFrom(result.CommandResult), Round: string(result.Round), Axis: adapterReviewAxis(result.Axis)}
}

func adapterCandidateFrom(result tasks.CandidateResult) adapterCandidateResult {
	return adapterCandidateResult{adapterCommandResult: adapterCommandFrom(result.CommandResult), Slice: result.Slice.String(), Candidate: string(result.Candidate)}
}

func adapterReviewSubjectKind(kind tasks.ReviewSubjectKind) AdapterReviewSubjectKind {
	if kind == tasks.ReviewSubjectDocumentRevision {
		return AdapterReviewSubjectDocumentRevision
	}
	return AdapterReviewSubjectImplementationCandidate
}

func adapterReviewAxis(axis tasks.ReviewAxis) AdapterReviewAxis {
	switch axis {
	case tasks.AxisCorrectness:
		return AdapterReviewAxisCorrectness
	case tasks.AxisTestQuality:
		return AdapterReviewAxisTestQuality
	case tasks.AxisElegance:
		return AdapterReviewAxisElegance
	default:
		return AdapterReviewAxis("")
	}
}

func adapterCollectionError(field string, length int, emptyAllowed bool) error {
	requirement := fmt.Sprintf("supply at most %d items", adapterCollectionMaxItems)
	if !emptyAllowed {
		requirement = fmt.Sprintf("supply between 1 and %d items", adapterCollectionMaxItems)
	}
	return adapterValidationError(fmt.Sprintf("The adapter %s collection has %d items and is outside its bound.", field, length), "Semantic collections are bounded before the store is opened", requirement, nil)
}

func adapterFieldError(field, value, fix string, cause error) error {
	return adapterValidationError(fmt.Sprintf("The adapter field %q has invalid value %q.", field, value), "The selected semantic operation requires a canonical typed value at this field", fix, cause)
}

func adapterValidationError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why + ".",
		Where:    "Decoding the hidden adapter invocation (internal/handlers/adapter.go).",
		Impact:   "The Pasture store was not opened and no workflow state was read or changed.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}

func adapterWorkflowError(what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     what,
		Why:      why + ".",
		Where:    "Executing the hidden adapter invocation (internal/handlers/adapter.go).",
		Impact:   "The caller did not receive a complete successful adapter result.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}

func preserveOrWrapAdapterError(err error, category pasterrors.Category, what, why, fix string) error {
	var structured *pasterrors.StructuredError
	if stderrors.As(err, &structured) {
		return err
	}
	return &pasterrors.StructuredError{
		Category: category,
		What:     what,
		Why:      why + ".",
		Where:    "Executing the hidden adapter invocation (internal/handlers/adapter.go).",
		Impact:   "The selected semantic operation did not return a successful adapter result.",
		Fix:      fix + ".",
		Cause:    err,
	}
}
