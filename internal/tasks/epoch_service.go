package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/dayvidpham/provenance"
)

// AssertedHumanActor is the registered human explicitly selected for a user gate.
// It is asserted attribution; authentication may be attached as separate evidence.
type AssertedHumanActor struct {
	ActorID provenance.ActorID
}

// CommandMeta carries the internal idempotency identity shared by direct and adapter
// invocations. It is not part of the ordinary CLI grammar.
type CommandMeta struct {
	OperationID provenance.OperationID
}

// RepositoryID identifies one repository participating in an integration
// candidate or publication. It is intentionally distinct from GitOID.
type RepositoryID string

// GitRef identifies the remote ref verified for one published repository
// member. Git object identity remains provenance.GitOID.
type GitRef string

// SetInteractionModeInput records an explicit human choice of epoch interaction mode.
type SetInteractionModeInput struct {
	Meta  CommandMeta
	Epoch EpochRootID
	Mode  InteractionMode
	Actor AssertedHumanActor
}

// PlanUATPayload is the structured content required by non-trivial Plan UAT outcomes.
// The proposal and outcome remain first-class fields of PlanUATInput.
type PlanUATPayload struct {
	Interactions  []UATInteraction
	Feedback      []UATFeedbackItem
	HeldQuestions []HeldUATQuestion
}

// PlanUATInput records one explicit human Plan UAT decision.
type PlanUATInput struct {
	Meta     CommandMeta
	Epoch    EpochRootID
	Proposal provenance.TaskID
	Outcome  PlanUATVerdict
	Actor    AssertedHumanActor
	Payload  *PlanUATPayload
}

// RatifyPlanInput records ratification as a first-class human decision over the
// accepted review round and Plan UAT decision.
type RatifyPlanInput struct {
	Meta        CommandMeta
	Epoch       EpochRootID
	Proposal    provenance.TaskID
	ReviewRound ReviewRoundID
	PlanUAT     DecisionLedgerEntryID
	Actor       AssertedHumanActor
}

// ImplementationUATInput records one explicit human decision over an integration
// candidate. Payload carries only structured feedback and carry-forward resolution.
type ImplementationUATInput struct {
	Meta      CommandMeta
	Epoch     EpochRootID
	Candidate IntegrationCandidateSetID
	Outcome   ImplementationUATVerdict
	Actor     AssertedHumanActor
	Payload   *ImplUATPayload
}

// LandInput records landing as a first-class human decision over an accepted
// implementation UAT decision and its exact integration candidate.
type LandInput struct {
	Meta              CommandMeta
	Epoch             EpochRootID
	Candidate         IntegrationCandidateSetID
	ImplementationUAT DecisionLedgerEntryID
	Actor             AssertedHumanActor
}

// CommandResult identifies one committed aggregate operation and its material records.
// Replayed distinguishes an exact idempotent replay without changing any identifiers.
type CommandResult struct {
	OperationID provenance.OperationID
	Replayed    bool
	Epoch       EpochRootID
	ActivityID  provenance.ActivityID
	EventIDs    []provenance.JournalID
}

// DecisionResult is the common result of all five explicit-human decisions.
type DecisionResult struct {
	CommandResult
	DecisionID DecisionLedgerEntryID
	ActorID    provenance.ActorID
}

// PlanReviewFeedback is one severity-free actionable revision requested by a plan review.
type PlanReviewFeedback struct {
	Body string
}

// ReviewFinding is one typed finding submitted only for an implementation review.
type ReviewFinding struct {
	Task     provenance.TaskID
	Severity FindingSeverity
	Summary  string
}

// ReviewSubmission is the closed sum of plan and implementation review submissions.
// Its package-private marker prevents a caller from adding an unvalidated variant.
type ReviewSubmission interface {
	Validate() error
	reviewSubmission()
}

// PlanReviewSubmission carries a binary plan verdict and severity-free feedback.
type PlanReviewSubmission struct {
	Verdict  Verdict
	Feedback []PlanReviewFeedback
}

func (PlanReviewSubmission) reviewSubmission() {}

// Validate requires actionable feedback exactly for a revising plan review.
func (s PlanReviewSubmission) Validate() error {
	if !s.Verdict.valid() {
		return reviewErr("PlanReviewSubmission.Validate", "the verdict is invalid", "a plan review verdict is accept or revise", "supply VerdictAccept or VerdictRevise")
	}
	if s.Verdict == VerdictAccept && len(s.Feedback) != 0 {
		return reviewErr("PlanReviewSubmission.Validate", "an accepting plan review carries revision feedback", "plan feedback requests revision and is incompatible with acceptance", "remove the feedback or use VerdictRevise")
	}
	if s.Verdict == VerdictRevise && len(s.Feedback) == 0 {
		return reviewErr("PlanReviewSubmission.Validate", "a revising plan review has no actionable feedback", "a revision must tell the plan author what to change", "supply at least one non-empty feedback item")
	}
	for i, feedback := range s.Feedback {
		if feedback.Body == "" {
			return reviewErr("PlanReviewSubmission.Validate", "plan feedback is empty", "empty feedback is not actionable", fmt.Sprintf("supply a non-empty body for feedback item %d", i))
		}
	}
	return nil
}

// ImplementationReviewSubmission carries a binary implementation verdict and severity-
// classified findings. It cannot carry plan feedback by construction.
type ImplementationReviewSubmission struct {
	Verdict  Verdict
	Findings []ReviewFinding
}

func (ImplementationReviewSubmission) reviewSubmission() {}

// Validate applies the code-review threshold: ACCEPT permits non-blocking follow-up
// findings, while REVISE requires at least one actionable BLOCKER.
func (s ImplementationReviewSubmission) Validate() error {
	if !s.Verdict.valid() {
		return reviewErr("ImplementationReviewSubmission.Validate", "the verdict is invalid", "an implementation review verdict is accept or revise", "supply VerdictAccept or VerdictRevise")
	}
	if s.Verdict == VerdictRevise && len(s.Findings) == 0 {
		return reviewErr("ImplementationReviewSubmission.Validate", "a revising implementation review has no findings", "a revision must identify at least one concrete finding", "supply at least one finding")
	}
	hasBlocker := false
	for i, finding := range s.Findings {
		if finding.Task == (provenance.TaskID{}) || !finding.Severity.valid() || finding.Summary == "" {
			return reviewErr("ImplementationReviewSubmission.Validate", "an implementation finding is incomplete", "each finding requires a task, known severity, and actionable summary", fmt.Sprintf("complete finding %d", i))
		}
		if finding.Severity == SeverityBlocker {
			hasBlocker = true
		}
	}
	if s.Verdict == VerdictAccept && hasBlocker {
		return reviewErr("ImplementationReviewSubmission.Validate", "an accepting implementation review carries a blocker", "a blocker requires revision before the implementation can be accepted", "resolve the blocker or use VerdictRevise")
	}
	if s.Verdict == VerdictRevise && !hasBlocker {
		return reviewErr("ImplementationReviewSubmission.Validate", "a revising implementation review has no blocker", "important and minor findings are non-blocking follow-up work under the review threshold", "use VerdictAccept or include the actionable blocker that requires revision")
	}
	return nil
}

// FindingDisposition is the closed resolution of a finding during candidate rework.
type FindingDisposition int

const (
	findingDispositionInvalid FindingDisposition = iota
	FindingFixed
	FindingDeferred
)

// FindingResolution ties a finding to its disposition and journaled fix evidence.
type FindingResolution struct {
	Finding  provenance.TaskID
	Outcome  FindingDisposition
	Evidence []provenance.JournalID
}

// ReworkSubmission is the complete typed finding disposition set for one rework.
type ReworkSubmission struct {
	Findings []FindingResolution
}

// StartReviewInput starts a round after resolving the unique active governing
// assignment from the epoch graph. The caller cannot substitute a human actor.
type StartReviewInput struct {
	Meta    CommandMeta
	Epoch   EpochRootID
	Subject ReviewSubjectRef
}

// SubmitReviewInput is authorized by the exact active axis-reviewer assignment.
type SubmitReviewInput struct {
	Meta       CommandMeta
	Epoch      EpochRootID
	Round      ReviewRoundID
	Axis       ReviewAxis
	Assignment provenance.AssignmentID
	Submission ReviewSubmission
}

// FinalizeReviewInput is authorized by the exact active governing assignment.
type FinalizeReviewInput struct {
	Meta       CommandMeta
	Epoch      EpochRootID
	Round      ReviewRoundID
	Assignment provenance.AssignmentID
}

// ReviewStartResult identifies the created review round and its reviewed subject.
type ReviewStartResult struct {
	CommandResult
	Round   ReviewRoundID
	Subject ReviewSubjectRef
}

// ReviewSubmitResult identifies the immutable event for one axis submission.
type ReviewSubmitResult struct {
	CommandResult
	Round ReviewRoundID
	Axis  ReviewAxis
	Event provenance.JournalID
}

// ReviewFinalizeResult identifies the finalized round and its three axis events.
type ReviewFinalizeResult struct {
	CommandResult
	Round        ReviewRoundID
	ReviewEvents [3]provenance.JournalID
}

// CreateSliceInput creates a slice under the exact active governing assignment.
type CreateSliceInput struct {
	Meta       CommandMeta
	Epoch      EpochRootID
	Plan       provenance.TaskID
	Assignment provenance.AssignmentID
}

// SetSliceCandidateInput records an immutable repository candidate for one slice.
type SetSliceCandidateInput struct {
	Meta       CommandMeta
	Epoch      EpochRootID
	Slice      provenance.TaskID
	Repository RepositoryID
	Commit     provenance.GitOID
	Assignment provenance.AssignmentID
}

// ReworkSliceInput replaces a slice candidate after complete finding disposition.
type ReworkSliceInput struct {
	Meta        CommandMeta
	Epoch       EpochRootID
	Slice       provenance.TaskID
	Candidate   ImplementationCandidateID
	Assignment  provenance.AssignmentID
	Replacement SliceCandidateReplacement
	Rework      ReworkSubmission
}

// SliceCandidateReplacement is the complete immutable replacement value for one slice.
// The aggregate allocates its ImplementationCandidateID when committing this value.
type SliceCandidateReplacement struct {
	Repository RepositoryID
	Commit     provenance.GitOID
}

// CloseSliceInput closes a slice under the exact governing assignment and finalized
// review round. The aggregate derives the round's canonical review events atomically.
type CloseSliceInput struct {
	Meta        CommandMeta
	Epoch       EpochRootID
	Slice       provenance.TaskID
	Candidate   ImplementationCandidateID
	ReviewRound ReviewRoundID
	Assignment  provenance.AssignmentID
}

// SliceResult identifies a created slice task.
type SliceResult struct {
	CommandResult
	Slice provenance.TaskID
}

// CandidateResult identifies the immutable candidate selected for a slice.
type CandidateResult struct {
	CommandResult
	Slice     provenance.TaskID
	Candidate ImplementationCandidateID
}

// RepositoryCandidate is one repository commit included in an integration candidate.
type RepositoryCandidate struct {
	Repository RepositoryID
	Candidate  ImplementationCandidateID
	Commit     provenance.GitOID
}

// CreateIntegrationCandidateInput creates one exact multi-repository candidate set.
type CreateIntegrationCandidateInput struct {
	Meta         CommandMeta
	Epoch        EpochRootID
	Plan         provenance.TaskID
	Assignment   provenance.AssignmentID
	Repositories []RepositoryCandidate
}

// ReworkIntegrationCandidateInput replaces an integration candidate after findings.
type ReworkIntegrationCandidateInput struct {
	Meta        CommandMeta
	Epoch       EpochRootID
	Candidate   IntegrationCandidateSetID
	Assignment  provenance.AssignmentID
	Replacement IntegrationCandidateReplacement
	Rework      ReworkSubmission
}

// IntegrationCandidateReplacement is the complete repository set that atomically
// replaces an existing integration candidate.
type IntegrationCandidateReplacement struct {
	Repositories []RepositoryCandidate
}

// PublishRepositoryInput records verified remote publication evidence for one member.
type PublishRepositoryInput struct {
	Meta       CommandMeta
	Epoch      EpochRootID
	Candidate  IntegrationCandidateSetID
	Repository RepositoryID
	Ref        GitRef
	Commit     provenance.GitOID
	Assignment provenance.AssignmentID
}

// IntegrationCandidateResult identifies the immutable integration candidate set.
type IntegrationCandidateResult struct {
	CommandResult
	Candidate IntegrationCandidateSetID
}

// PublicationResult identifies the exact publication-evidence event.
type PublicationResult struct {
	CommandResult
	Candidate  IntegrationCandidateSetID
	Repository RepositoryID
	Evidence   provenance.JournalID
}

// EpochMutationKind is the closed mutation identity exposed to synchronization seams.
type EpochMutationKind int

const (
	epochMutationInvalid EpochMutationKind = iota
	MutationSetInteractionMode
	MutationStartReview
	MutationSubmitReview
	MutationFinalizeReview
	MutationCreateSlice
	MutationSetSliceCandidate
	MutationReworkSlice
	MutationCloseSlice
	MutationCreateIntegrationCandidate
	MutationReworkIntegrationCandidate
	MutationPublishRepository
	MutationRecordPlanUAT
	MutationRatifyPlan
	MutationRecordImplementationUAT
	MutationLand
)

// EpochRaceBarrier is an injected deterministic synchronization seam. Implementations
// call AfterPreflight after authoritative reads and immediately before atomic Apply.
// Production supplies an inert barrier; synchronized tests may hold contenders here.
type EpochRaceBarrier interface {
	AfterPreflight(context.Context, EpochMutationKind) error
}

// EpochServiceSynchronization groups synchronization dependencies separately from all
// ordinary command inputs and results.
type EpochServiceSynchronization struct {
	RaceBarrier EpochRaceBarrier
}

// EpochServiceOptions carries optional aggregate behavior dependencies. Its zero value is
// production-safe: a nil race barrier means no synchronization pause. CLI commands never
// receive these options.
type EpochServiceOptions struct {
	Synchronization EpochServiceSynchronization
	now             func() time.Time
}

// EpochServiceFactory is the production construction boundary for the composed service.
// The assignment implementation composes its service with EpochHumanService.
type EpochServiceFactory interface {
	NewEpochService(EpochServiceOptions) (EpochService, error)
}

// EpochHumanService is the explicit-human portion of the workflow aggregate.
type EpochHumanService interface {
	SetInteractionMode(context.Context, SetInteractionModeInput) (DecisionResult, error)
	ShowInteractionMode(context.Context, EpochRootID) (InteractionModeCursor, error)
	RecordPlanUAT(context.Context, PlanUATInput) (DecisionResult, error)
	RatifyPlan(context.Context, RatifyPlanInput) (DecisionResult, error)
	RecordImplementationUAT(context.Context, ImplementationUATInput) (DecisionResult, error)
	Land(context.Context, LandInput) (DecisionResult, error)
}

// EpochHumanServiceFactory constructs the production human-decision aggregate without
// requiring placeholder implementations of assignment-controlled operations.
type EpochHumanServiceFactory interface {
	NewEpochHumanService(EpochServiceOptions) (EpochHumanService, error)
}

// EpochAssignmentService is the assignment-authorized portion of the aggregate.
type EpochAssignmentService interface {
	StartReview(context.Context, StartReviewInput) (ReviewStartResult, error)
	SubmitReview(context.Context, SubmitReviewInput) (ReviewSubmitResult, error)
	FinalizeReview(context.Context, FinalizeReviewInput) (ReviewFinalizeResult, error)

	CreateSlice(context.Context, CreateSliceInput) (SliceResult, error)
	SetSliceCandidate(context.Context, SetSliceCandidateInput) (CandidateResult, error)
	ReworkSlice(context.Context, ReworkSliceInput) (CandidateResult, error)
	CloseSlice(context.Context, CloseSliceInput) (CommandResult, error)

	CreateIntegrationCandidate(context.Context, CreateIntegrationCandidateInput) (IntegrationCandidateResult, error)
	ReworkIntegrationCandidate(context.Context, ReworkIntegrationCandidateInput) (IntegrationCandidateResult, error)
	PublishRepository(context.Context, PublishRepositoryInput) (PublicationResult, error)
}

// EpochService is the one public workflow contract composed from its two authority
// families. Production composition does not permit either family to fake the other.
type EpochService interface {
	EpochHumanService
	EpochAssignmentService
}
