package handlers

import (
	"context"
	"crypto/rand"
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
	"github.com/dayvidpham/pasture/internal/tasks"
)

const (
	epochCommandInputMaxBytes = 1 << 20
	epochCommandMaxItems      = 1024
)

// EpochCommandInvocation carries the process boundaries shared by direct epoch
// commands. Structured command bodies are accepted only through InputArgument
// set to "-", which keeps every operation-specific payload on standard input.
type EpochCommandInvocation struct {
	DBPath        string
	InputArgument string
	Input         io.Reader
	Output        io.Writer
}

type epochCommandStore interface {
	tasks.EpochServiceFactory
	Show(provenance.TaskID) (provenance.Task, error)
	Close() error
}

type epochCommandStoreOpener func(string) (epochCommandStore, error)

type preparedEpochCommand struct {
	command string
	invoke  func(context.Context, epochCommandStore, tasks.EpochService) (any, error)
}

// EpochSetInteractionMode records an explicit human interaction-mode decision.
func EpochSetInteractionMode(ctx context.Context, invocation EpochCommandInvocation, epoch, mode, actor string) error {
	epochID, err := parseEpochCommandEpoch("epoch interaction-mode set", epoch)
	if err != nil {
		return err
	}
	interactionMode, err := parseEpochCommandInteractionMode("epoch interaction-mode set", mode)
	if err != nil {
		return err
	}
	actorID, err := parseEpochCommandActor("epoch interaction-mode set", actor)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta("epoch interaction-mode set")
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: "epoch interaction-mode set",
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.SetInteractionMode(ctx, tasks.SetInteractionModeInput{
				Meta:  meta,
				Epoch: epochID,
				Mode:  interactionMode,
				Actor: tasks.AssertedHumanActor{ActorID: actorID},
			})
			return epochDecisionResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochShowInteractionMode reads the current derived interaction-mode cursor.
func EpochShowInteractionMode(ctx context.Context, invocation EpochCommandInvocation, epoch string) error {
	epochID, err := parseEpochCommandEpoch("epoch interaction-mode show", epoch)
	if err != nil {
		return err
	}
	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: "epoch interaction-mode show",
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.ShowInteractionMode(ctx, epochID)
			return epochInteractionModeResult{Epoch: string(epochID), Mode: result.Mode}, err
		},
	}, openEpochCommandStore)
}

// EpochStartReview begins a review for the typed snapshot represented by subject.
// The task's static workflow phase determines the closed subject variant after
// the already-validated task ID has been read from the store.
func EpochStartReview(ctx context.Context, invocation EpochCommandInvocation, epoch, subject string) error {
	epochID, err := parseEpochCommandEpoch("epoch review start", epoch)
	if err != nil {
		return err
	}
	subjectID, err := parseEpochCommandTask("epoch review start", "subject", subject)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta("epoch review start")
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: "epoch review start",
		invoke: func(ctx context.Context, store epochCommandStore, service tasks.EpochService) (any, error) {
			subjectRef, err := epochReviewSubject(store, subjectID)
			if err != nil {
				return nil, err
			}
			result, err := service.StartReview(ctx, tasks.StartReviewInput{Meta: meta, Epoch: epochID, Subject: subjectRef})
			return epochReviewStartResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochSubmitReview records either a plan-review submission or an
// implementation-review submission. The operation-specific strict JSON body
// distinguishes the two closed submission forms by feedback versus findings.
func EpochSubmitReview(ctx context.Context, invocation EpochCommandInvocation, epoch, round, axis, assignment string) error {
	const command = "epoch review submit"
	epochID, reviewRound, reviewAxis, assignmentID, err := parseEpochCommandReview(command, epoch, round, axis, assignment)
	if err != nil {
		return err
	}
	raw, err := readEpochCommandInput(command, invocation)
	if err != nil {
		return err
	}
	submission, err := parseEpochCommandReviewSubmission(command, raw)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.SubmitReview(ctx, tasks.SubmitReviewInput{
				Meta:       meta,
				Epoch:      epochID,
				Round:      reviewRound,
				Axis:       reviewAxis,
				Assignment: assignmentID,
				Submission: submission,
			})
			return epochReviewSubmitResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochFinalizeReview finalizes the three-axis review round.
func EpochFinalizeReview(ctx context.Context, invocation EpochCommandInvocation, epoch, round, assignment string) error {
	const command = "epoch review finalize"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	reviewRound, err := parseEpochCommandReviewRound(command, round)
	if err != nil {
		return err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.FinalizeReview(ctx, tasks.FinalizeReviewInput{
				Meta:       meta,
				Epoch:      epochID,
				Round:      reviewRound,
				Assignment: assignmentID,
			})
			return epochReviewFinalizeResult{epochCommandResult: epochCommandResultFrom(result.CommandResult), Round: string(result.Round)}, err
		},
	}, openEpochCommandStore)
}

// EpochCreateSlice creates one assignment-controlled implementation slice.
func EpochCreateSlice(ctx context.Context, invocation EpochCommandInvocation, epoch, plan, assignment string) error {
	const command = "epoch slice create"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	planID, err := parseEpochCommandTask(command, "plan", plan)
	if err != nil {
		return err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.CreateSlice(ctx, tasks.CreateSliceInput{Meta: meta, Epoch: epochID, Plan: planID, Assignment: assignmentID})
			return epochSliceResult{epochCommandResult: epochCommandResultFrom(result.CommandResult), Slice: result.Slice.String()}, err
		},
	}, openEpochCommandStore)
}

// EpochSetSliceCandidate records an immutable candidate for a slice.
func EpochSetSliceCandidate(ctx context.Context, invocation EpochCommandInvocation, epoch, slice, repository, commit, assignment string) error {
	const command = "epoch slice candidate set"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	sliceID, err := parseEpochCommandTask(command, "slice", slice)
	if err != nil {
		return err
	}
	repositoryID, err := parseEpochCommandRepository(command, repository)
	if err != nil {
		return err
	}
	commitID, err := parseEpochCommandGitOID(command, commit)
	if err != nil {
		return err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.SetSliceCandidate(ctx, tasks.SetSliceCandidateInput{
				Meta:       meta,
				Epoch:      epochID,
				Slice:      sliceID,
				Repository: repositoryID,
				Commit:     commitID,
				Assignment: assignmentID,
			})
			return epochCandidateResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochReworkSlice replaces a candidate after every review finding is resolved
// through the strict rework payload.
func EpochReworkSlice(ctx context.Context, invocation EpochCommandInvocation, epoch, slice, candidate, assignment string) error {
	const command = "epoch slice rework"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	sliceID, err := parseEpochCommandTask(command, "slice", slice)
	if err != nil {
		return err
	}
	candidateID, err := parseEpochCommandImplementationCandidate(command, candidate)
	if err != nil {
		return err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return err
	}
	raw, err := readEpochCommandInput(command, invocation)
	if err != nil {
		return err
	}
	replacement, rework, err := parseEpochCommandSliceRework(command, raw)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.ReworkSlice(ctx, tasks.ReworkSliceInput{
				Meta:        meta,
				Epoch:       epochID,
				Slice:       sliceID,
				Candidate:   candidateID,
				Assignment:  assignmentID,
				Replacement: replacement,
				Rework:      rework,
			})
			return epochCandidateResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochCloseSlice closes a slice against its exact finalized review round.
func EpochCloseSlice(ctx context.Context, invocation EpochCommandInvocation, epoch, slice, candidate, reviewRound, assignment string) error {
	const command = "epoch slice close"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	sliceID, err := parseEpochCommandTask(command, "slice", slice)
	if err != nil {
		return err
	}
	candidateID, err := parseEpochCommandImplementationCandidate(command, candidate)
	if err != nil {
		return err
	}
	roundID, err := parseEpochCommandReviewRound(command, reviewRound)
	if err != nil {
		return err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.CloseSlice(ctx, tasks.CloseSliceInput{
				Meta:        meta,
				Epoch:       epochID,
				Slice:       sliceID,
				Candidate:   candidateID,
				ReviewRound: roundID,
				Assignment:  assignmentID,
			})
			return epochCommandResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochCreateIntegrationCandidate creates a complete multi-repository candidate set.
func EpochCreateIntegrationCandidate(ctx context.Context, invocation EpochCommandInvocation, epoch, plan, assignment string) error {
	const command = "epoch integration candidate-set create"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	planID, err := parseEpochCommandTask(command, "plan", plan)
	if err != nil {
		return err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return err
	}
	raw, err := readEpochCommandInput(command, invocation)
	if err != nil {
		return err
	}
	repositories, err := parseEpochCommandRepositories(command, raw)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.CreateIntegrationCandidate(ctx, tasks.CreateIntegrationCandidateInput{
				Meta:         meta,
				Epoch:        epochID,
				Plan:         planID,
				Assignment:   assignmentID,
				Repositories: repositories,
			})
			return epochIntegrationCandidateResult{epochCommandResult: epochCommandResultFrom(result.CommandResult), Candidate: string(result.Candidate)}, err
		},
	}, openEpochCommandStore)
}

// EpochReworkIntegrationCandidate replaces a candidate set after resolving its findings.
func EpochReworkIntegrationCandidate(ctx context.Context, invocation EpochCommandInvocation, epoch, candidate, assignment string) error {
	const command = "epoch integration candidate-set rework"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	candidateID, err := parseEpochCommandIntegrationCandidate(command, candidate)
	if err != nil {
		return err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return err
	}
	raw, err := readEpochCommandInput(command, invocation)
	if err != nil {
		return err
	}
	repositories, rework, err := parseEpochCommandIntegrationRework(command, raw)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.ReworkIntegrationCandidate(ctx, tasks.ReworkIntegrationCandidateInput{
				Meta:       meta,
				Epoch:      epochID,
				Candidate:  candidateID,
				Assignment: assignmentID,
				Replacement: tasks.IntegrationCandidateReplacement{
					Repositories: repositories,
				},
				Rework: rework,
			})
			return epochIntegrationCandidateResult{epochCommandResult: epochCommandResultFrom(result.CommandResult), Candidate: string(result.Candidate)}, err
		},
	}, openEpochCommandStore)
}

// EpochPublishRepository records verified publication for one candidate member.
func EpochPublishRepository(ctx context.Context, invocation EpochCommandInvocation, epoch, candidate, repository, ref, commit, assignment string) error {
	const command = "epoch integration publish-repository"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	candidateID, err := parseEpochCommandIntegrationCandidate(command, candidate)
	if err != nil {
		return err
	}
	repositoryID, err := parseEpochCommandRepository(command, repository)
	if err != nil {
		return err
	}
	gitRef, err := parseEpochCommandGitRef(command, ref)
	if err != nil {
		return err
	}
	commitID, err := parseEpochCommandGitOID(command, commit)
	if err != nil {
		return err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.PublishRepository(ctx, tasks.PublishRepositoryInput{
				Meta:       meta,
				Epoch:      epochID,
				Candidate:  candidateID,
				Repository: repositoryID,
				Ref:        gitRef,
				Commit:     commitID,
				Assignment: assignmentID,
			})
			return epochPublicationResult{epochCommandResult: epochCommandResultFrom(result.CommandResult), Candidate: string(result.Candidate), Repository: result.Repository}, err
		},
	}, openEpochCommandStore)
}

// EpochAcceptPlanUAT records the accepted Plan UAT baseline without a structured payload.
func EpochAcceptPlanUAT(ctx context.Context, invocation EpochCommandInvocation, epoch, proposal, actor string) error {
	return epochRecordPlanUAT(ctx, invocation, "epoch plan uat accept", epoch, proposal, actor, tasks.PlanUATAccepted, nil)
}

// EpochRequestPlanChanges records typed Plan UAT feedback from strict JSON stdin.
func EpochRequestPlanChanges(ctx context.Context, invocation EpochCommandInvocation, epoch, proposal, actor string) error {
	payload, err := parseEpochCommandPlanUATPayload("epoch plan uat changes-request", invocation)
	if err != nil {
		return err
	}
	return epochRecordPlanUAT(ctx, invocation, "epoch plan uat changes-request", epoch, proposal, actor, tasks.PlanUATChangesRequested, payload)
}

// EpochDeferPlanUAT records an AFK Plan UAT deferral from strict JSON stdin.
func EpochDeferPlanUAT(ctx context.Context, invocation EpochCommandInvocation, epoch, proposal, actor string) error {
	payload, err := parseEpochCommandPlanUATPayload("epoch plan uat defer", invocation)
	if err != nil {
		return err
	}
	if err := validateEpochCommandPlanDeferral(payload); err != nil {
		return err
	}
	return epochRecordPlanUAT(ctx, invocation, "epoch plan uat defer", epoch, proposal, actor, tasks.PlanUATDeferredByAFK, payload)
}

func epochRecordPlanUAT(ctx context.Context, invocation EpochCommandInvocation, command, epoch, proposal, actor string, outcome tasks.PlanUATVerdict, payload *tasks.PlanUATPayload) error {
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	proposalID, err := parseEpochCommandTask(command, "proposal", proposal)
	if err != nil {
		return err
	}
	actorID, err := parseEpochCommandActor(command, actor)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.RecordPlanUAT(ctx, tasks.PlanUATInput{
				Meta:     meta,
				Epoch:    epochID,
				Proposal: proposalID,
				Outcome:  outcome,
				Actor:    tasks.AssertedHumanActor{ActorID: actorID},
				Payload:  payload,
			})
			return epochDecisionResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochRatifyPlan records ratification as a first-class human decision.
func EpochRatifyPlan(ctx context.Context, invocation EpochCommandInvocation, epoch, proposal, reviewRound, planUAT, actor string) error {
	const command = "epoch plan ratify"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	proposalID, err := parseEpochCommandTask(command, "proposal", proposal)
	if err != nil {
		return err
	}
	roundID, err := parseEpochCommandReviewRound(command, reviewRound)
	if err != nil {
		return err
	}
	planUATID, err := parseEpochCommandDecision(command, "plan-uat", planUAT)
	if err != nil {
		return err
	}
	actorID, err := parseEpochCommandActor(command, actor)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.RatifyPlan(ctx, tasks.RatifyPlanInput{
				Meta:        meta,
				Epoch:       epochID,
				Proposal:    proposalID,
				ReviewRound: roundID,
				PlanUAT:     planUATID,
				Actor:       tasks.AssertedHumanActor{ActorID: actorID},
			})
			return epochDecisionResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochAcceptImplementationUAT records an accepted implementation UAT without
// a structured payload.
func EpochAcceptImplementationUAT(ctx context.Context, invocation EpochCommandInvocation, epoch, candidate, actor string) error {
	return epochRecordImplementationUAT(ctx, invocation, "epoch implementation uat accept", epoch, candidate, actor, tasks.ImplUATAccepted, nil)
}

// EpochRequestImplementationChanges records typed implementation-UAT feedback.
func EpochRequestImplementationChanges(ctx context.Context, invocation EpochCommandInvocation, epoch, candidate, actor string) error {
	payload, err := parseEpochCommandImplementationUATPayload("epoch implementation uat changes-request", invocation)
	if err != nil {
		return err
	}
	return epochRecordImplementationUAT(ctx, invocation, "epoch implementation uat changes-request", epoch, candidate, actor, tasks.ImplUATChangesRequested, payload)
}

func epochRecordImplementationUAT(ctx context.Context, invocation EpochCommandInvocation, command, epoch, candidate, actor string, outcome tasks.ImplementationUATVerdict, payload *tasks.ImplUATPayload) error {
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	candidateID, err := parseEpochCommandIntegrationCandidate(command, candidate)
	if err != nil {
		return err
	}
	actorID, err := parseEpochCommandActor(command, actor)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.RecordImplementationUAT(ctx, tasks.ImplementationUATInput{
				Meta:      meta,
				Epoch:     epochID,
				Candidate: candidateID,
				Outcome:   outcome,
				Actor:     tasks.AssertedHumanActor{ActorID: actorID},
				Payload:   payload,
			})
			return epochDecisionResultFrom(result), err
		},
	}, openEpochCommandStore)
}

// EpochLand records landing against the current accepted implementation UAT.
func EpochLand(ctx context.Context, invocation EpochCommandInvocation, epoch, candidate, implementationUAT, actor string) error {
	const command = "epoch land"
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return err
	}
	candidateID, err := parseEpochCommandIntegrationCandidate(command, candidate)
	if err != nil {
		return err
	}
	implementationUATID, err := parseEpochCommandDecision(command, "implementation-uat", implementationUAT)
	if err != nil {
		return err
	}
	actorID, err := parseEpochCommandActor(command, actor)
	if err != nil {
		return err
	}
	meta, err := newEpochCommandMeta(command)
	if err != nil {
		return err
	}

	return executeEpochCommand(ctx, invocation, preparedEpochCommand{
		command: command,
		invoke: func(ctx context.Context, _ epochCommandStore, service tasks.EpochService) (any, error) {
			result, err := service.Land(ctx, tasks.LandInput{
				Meta:              meta,
				Epoch:             epochID,
				Candidate:         candidateID,
				ImplementationUAT: implementationUATID,
				Actor:             tasks.AssertedHumanActor{ActorID: actorID},
			})
			return epochDecisionResultFrom(result), err
		},
	}, openEpochCommandStore)
}

func executeEpochCommand(ctx context.Context, invocation EpochCommandInvocation, prepared preparedEpochCommand, open epochCommandStoreOpener) error {
	if ctx == nil {
		return epochCommandValidationError(prepared.command, "The command has no execution context.", "The Cobra boundary must provide a live context before an epoch operation can run", "invoke the command through the Pasture CLI", nil)
	}
	if invocation.Output == nil {
		return epochCommandValidationError(prepared.command, "The command has no result output stream.", "A successful epoch command must emit one semantic JSON result", "provide a writable standard-output stream", nil)
	}
	if open == nil {
		return epochCommandValidationError(prepared.command, "The Epoch service store opener is not configured.", "The command cannot construct its production service boundary", "wire the command through tasks.OpenTaskTracker", nil)
	}
	if err := ctx.Err(); err != nil {
		return epochCommandExecutionError(prepared.command, "The command was cancelled before the store opened.", "The supplied execution context ended during command execution", "retry with a live command context", err)
	}

	store, err := open(invocation.DBPath)
	if err != nil {
		return preserveOrWrapEpochCommandError(prepared.command, err, pasterrors.CategoryStorage,
			"The command could not open the Pasture store after validating its input.",
			"The Epoch service requires the configured unified store",
			"verify --db or PASTURE_DB_PATH is writable and retry the command")
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
		return preserveOrWrapEpochCommandError(prepared.command, serviceErr, pasterrors.CategoryWorkflow,
			"The command could not construct the Epoch service.",
			"The validated command must run through the complete production aggregate",
			"repair the configured store or Epoch service wiring, then retry")
	}

	result, invokeErr := prepared.invoke(ctx, store, service)
	closeErr := store.Close()
	if invokeErr != nil {
		return preserveOrWrapEpochCommandError(prepared.command, invokeErr, pasterrors.CategoryWorkflow,
			fmt.Sprintf("The %s operation failed.", prepared.command),
			"The Epoch service rejected or could not commit the semantic command",
			"correct the reported authority or workflow prerequisite, then retry")
	}
	if closeErr != nil {
		return preserveOrWrapEpochCommandError(prepared.command, closeErr, pasterrors.CategoryStorage,
			fmt.Sprintf("The %s operation completed but the Pasture store did not close cleanly.", prepared.command),
			"The database handle reported a close failure after the Epoch service returned",
			"inspect the database before issuing another command")
	}

	wire, err := json.Marshal(result)
	if err != nil {
		return epochCommandExecutionError(prepared.command, "The semantic result could not be encoded as JSON.", "The fixed result schema could not represent the completed command", "report the result-schema bug and retry after upgrading Pasture", err)
	}
	return writeEpochCommandResult(prepared.command, invocation.Output, append(wire, '\n'))
}

func openEpochCommandStore(path string) (epochCommandStore, error) {
	tracker, err := tasks.OpenTaskTracker(path)
	if err != nil {
		return nil, err
	}
	store, ok := tracker.(epochCommandStore)
	if !ok {
		_ = tracker.Close()
		return nil, fmt.Errorf("opened tracker does not implement the Epoch service factory")
	}
	return store, nil
}

func writeEpochCommandResult(command string, output io.Writer, wire []byte) error {
	for len(wire) > 0 {
		n, err := output.Write(wire)
		if err != nil {
			return epochCommandExecutionError(command, "The semantic result could not be written to standard output.", "The output stream failed after the Epoch service completed", "repair the output stream and inspect the committed command state before retrying", err)
		}
		if n == 0 {
			return epochCommandExecutionError(command, "The semantic result could not be written to standard output.", "The output stream accepted zero bytes without returning an error", "repair the output stream and inspect the committed command state before retrying", io.ErrShortWrite)
		}
		wire = wire[n:]
	}
	return nil
}

func readEpochCommandInput(command string, invocation EpochCommandInvocation) ([]byte, error) {
	if invocation.InputArgument != "-" {
		return nil, epochCommandValidationError(command, "The command requires --input -.", "Structured semantic input is accepted only from standard input so command schemas remain deterministic", "pass --input - and pipe one operation-specific JSON object to standard input", nil)
	}
	if invocation.Input == nil {
		return nil, epochCommandValidationError(command, "The command has no standard-input stream.", "--input - selects one JSON object from standard input", "pipe the documented operation-specific JSON object to the command", nil)
	}
	data, err := io.ReadAll(io.LimitReader(invocation.Input, epochCommandInputMaxBytes+1))
	if err != nil {
		return nil, epochCommandValidationError(command, "The command input could not be read.", "Reading the bounded JSON value from standard input failed", "retry with a readable standard-input stream containing one complete JSON object", err)
	}
	if len(data) > epochCommandInputMaxBytes {
		return nil, epochCommandValidationError(command, fmt.Sprintf("The command input exceeds the %d-byte limit.", epochCommandInputMaxBytes), "Operation input is bounded before the store opens", "send only the documented semantic fields and remove unrelated data", nil)
	}
	if !utf8.Valid(data) {
		return nil, epochCommandValidationError(command, "The command input is not valid UTF-8 JSON.", "Semantic input must preserve its exact bytes through strict decoding", "encode one operation-specific JSON object as valid UTF-8", nil)
	}
	return data, nil
}

func strictEpochCommandInput[T any](command string, data []byte, required ...string) (T, error) {
	var target T
	if err := ir.StrictJSONWithPresence(data, required, &target); err != nil {
		return target, epochCommandValidationError(command, "The JSON input does not match the command's strict schema.", "The input is malformed, has a duplicate or unknown field, omits a required field, or contains trailing JSON", "send only the documented fields for this command as one JSON object", err)
	}
	return target, nil
}

func newEpochCommandMeta(command string) (tasks.CommandMeta, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return tasks.CommandMeta{}, epochCommandValidationError(command, "Pasture could not mint an internal operation identity.", "The operating system random source failed before the command could be prepared", "restore the system random source and retry the command", err)
	}
	id := provenance.OperationID("pasture.cli." + hex.EncodeToString(random[:]))
	if err := provenance.ValidateOperationID(id); err != nil {
		return tasks.CommandMeta{}, epochCommandValidationError(command, "Pasture generated an invalid internal operation identity.", "The generated identifier did not satisfy the Provenance operation contract", "report this internal error and retry after upgrading Pasture", err)
	}
	return tasks.CommandMeta{OperationID: id}, nil
}

func parseEpochCommandEpoch(command, value string) (tasks.EpochRootID, error) {
	id, err := parseEpochCommandTask(command, "epoch", value)
	if err != nil {
		return "", err
	}
	return tasks.EpochRootID(id.String()), nil
}

func parseEpochCommandTask(command, field, value string) (provenance.TaskID, error) {
	id, err := provenance.ParseTaskID(value)
	if err != nil || id.String() != value {
		return provenance.TaskID{}, epochCommandFieldError(command, field, value, "supply the canonical namespace--UUID task identity", err)
	}
	return id, nil
}

func parseEpochCommandActor(command, value string) (provenance.ActorID, error) {
	id, err := provenance.ParseActorID(value)
	if err != nil || id.String() != value {
		return provenance.ActorID{}, epochCommandFieldError(command, "actor", value, "supply the canonical namespace--UUID registered actor identity", err)
	}
	return id, nil
}

func parseEpochCommandAssignment(command, value string) (provenance.AssignmentID, error) {
	canonical, err := parseEpochCommandScalar(command, "assignment", value)
	return provenance.AssignmentID(canonical), err
}

func parseEpochCommandReviewRound(command, value string) (tasks.ReviewRoundID, error) {
	canonical, err := parseEpochCommandTask(command, "round", value)
	return tasks.ReviewRoundID(canonical.String()), err
}

func parseEpochCommandImplementationCandidate(command, value string) (tasks.ImplementationCandidateID, error) {
	canonical, err := parseEpochCommandTask(command, "candidate", value)
	return tasks.ImplementationCandidateID(canonical.String()), err
}

func parseEpochCommandIntegrationCandidate(command, value string) (tasks.IntegrationCandidateSetID, error) {
	canonical, err := parseEpochCommandTask(command, "candidate", value)
	return tasks.IntegrationCandidateSetID(canonical.String()), err
}

func parseEpochCommandDecision(command, field, value string) (tasks.DecisionLedgerEntryID, error) {
	canonical, err := parseEpochCommandScalar(command, field, value)
	return tasks.DecisionLedgerEntryID(canonical), err
}

func parseEpochCommandRepository(command, value string) (tasks.RepositoryID, error) {
	canonical, err := parseEpochCommandScalar(command, "repository", value)
	return tasks.RepositoryID(canonical), err
}

func parseEpochCommandGitOID(command, value string) (provenance.GitOID, error) {
	if !utf8.ValidString(value) {
		return "", epochCommandFieldError(command, "commit", value, "supply a lowercase 40- or 64-hex Git object identity", nil)
	}
	oid := provenance.GitOID(value)
	if _, err := provenance.GitContext(oid); err != nil {
		return "", epochCommandFieldError(command, "commit", value, "supply a lowercase 40- or 64-hex Git object identity", err)
	}
	return oid, nil
}

func parseEpochCommandGitRef(command, value string) (tasks.GitRef, error) {
	if !utf8.ValidString(value) || value == "@" || !strings.Contains(value, "/") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "//") {
		return "", epochCommandFieldError(command, "ref", value, "supply a ref accepted by git-check-ref-format, such as refs/heads/main", nil)
	}
	for _, r := range value {
		if r <= 0x1f || r == 0x7f {
			return "", epochCommandFieldError(command, "ref", value, "remove control characters and supply a ref accepted by git-check-ref-format", nil)
		}
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return "", epochCommandFieldError(command, "ref", value, "supply a ref accepted by git-check-ref-format, such as refs/heads/main", nil)
		}
	}
	return tasks.GitRef(value), nil
}

func parseEpochCommandScalar(command, field, value string) (string, error) {
	if !utf8.ValidString(value) || value == "" || strings.TrimSpace(value) != value {
		return "", epochCommandFieldError(command, field, value, "supply a non-empty canonical UTF-8 value without surrounding whitespace", nil)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", epochCommandFieldError(command, field, value, "remove whitespace and control characters", nil)
		}
	}
	return value, nil
}

func parseEpochCommandInteractionMode(command, value string) (tasks.InteractionMode, error) {
	switch tasks.InteractionMode(value) {
	case tasks.InteractionNormal:
		return tasks.InteractionNormal, nil
	case tasks.InteractionAFK:
		return tasks.InteractionAFK, nil
	default:
		return "", epochCommandFieldError(command, "mode", value, "use normal or afk", nil)
	}
}

func parseEpochCommandReview(command, epoch, round, axis, assignment string) (tasks.EpochRootID, tasks.ReviewRoundID, tasks.ReviewAxis, provenance.AssignmentID, error) {
	epochID, err := parseEpochCommandEpoch(command, epoch)
	if err != nil {
		return "", "", 0, "", err
	}
	roundID, err := parseEpochCommandReviewRound(command, round)
	if err != nil {
		return "", "", 0, "", err
	}
	axisID, err := parseEpochCommandReviewAxis(command, axis)
	if err != nil {
		return "", "", 0, "", err
	}
	assignmentID, err := parseEpochCommandAssignment(command, assignment)
	if err != nil {
		return "", "", 0, "", err
	}
	return epochID, roundID, axisID, assignmentID, nil
}

func parseEpochCommandReviewAxis(command, value string) (tasks.ReviewAxis, error) {
	switch value {
	case "correctness":
		return tasks.AxisCorrectness, nil
	case "test-quality":
		return tasks.AxisTestQuality, nil
	case "elegance":
		return tasks.AxisElegance, nil
	default:
		return 0, epochCommandFieldError(command, "axis", value, "use correctness, test-quality, or elegance", nil)
	}
}

func parseEpochCommandVerdict(command, value string) (tasks.Verdict, error) {
	switch value {
	case "accept":
		return tasks.VerdictAccept, nil
	case "revise":
		return tasks.VerdictRevise, nil
	default:
		return 0, epochCommandFieldError(command, "verdict", value, "use accept or revise", nil)
	}
}

func parseEpochCommandSeverity(command, value string) (tasks.FindingSeverity, error) {
	switch value {
	case "blocker":
		return tasks.SeverityBlocker, nil
	case "important":
		return tasks.SeverityImportant, nil
	case "minor":
		return tasks.SeverityMinor, nil
	default:
		return 0, epochCommandFieldError(command, "severity", value, "use blocker, important, or minor", nil)
	}
}

func parseEpochCommandFindingOutcome(command, value string) (tasks.FindingDisposition, error) {
	switch value {
	case "fixed":
		return tasks.FindingFixed, nil
	case "deferred":
		return tasks.FindingDeferred, nil
	default:
		return 0, epochCommandFieldError(command, "outcome", value, "use fixed or deferred", nil)
	}
}

func parseEpochCommandResolutionKind(command, value string) (tasks.ResolutionKind, error) {
	switch value {
	case "confirm":
		return tasks.ResolutionConfirm, nil
	case "defer":
		return tasks.ResolutionDefer, nil
	case "replace":
		return tasks.ResolutionReplace, nil
	default:
		return 0, epochCommandFieldError(command, "kind", value, "use confirm, defer, or replace", nil)
	}
}

func epochReviewSubject(store epochCommandStore, subject provenance.TaskID) (tasks.ReviewSubjectRef, error) {
	task, err := store.Show(subject)
	if err != nil {
		return tasks.ReviewSubjectRef{}, epochCommandExecutionError("epoch review start", fmt.Sprintf("The review subject %q could not be read.", subject), "A review can only start for an existing workflow snapshot", "supply an existing proposal revision or implementation candidate", err)
	}
	switch task.Phase {
	case provenance.PhasePropose:
		return tasks.ReviewSubjectRef{Kind: tasks.ReviewSubjectDocumentRevision, SnapshotID: subject.String()}, nil
	case provenance.PhaseWorkerSlices, provenance.PhaseImplUAT:
		return tasks.ReviewSubjectRef{Kind: tasks.ReviewSubjectImplementationCandidate, SnapshotID: subject.String()}, nil
	default:
		return tasks.ReviewSubjectRef{}, epochCommandExecutionError("epoch review start", fmt.Sprintf("The subject %q is in phase %q and is not reviewable.", subject, task.Phase), "Review start accepts proposal revisions and implementation candidates only", "select a task in the propose, worker-slices, or implementation-UAT workflow phase", nil)
	}
}

type epochReviewPlanInput struct {
	Verdict  string                    `json:"verdict"`
	Feedback []epochPlanReviewFeedback `json:"feedback"`
}

type epochPlanReviewFeedback struct {
	Body string `json:"body"`
}

type epochReviewImplementationInput struct {
	Verdict  string               `json:"verdict"`
	Findings []epochReviewFinding `json:"findings"`
}

type epochReviewFinding struct {
	Task     string `json:"task"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

func parseEpochCommandReviewSubmission(command string, data []byte) (tasks.ReviewSubmission, error) {
	fields, err := strictEpochCommandInput[map[string]json.RawMessage](command, data)
	if err != nil {
		return nil, err
	}
	// Decode separately after determining the closed variant. The strict map
	// decode rejects duplicate members and trailing values; the selected concrete
	// type below rejects unknown fields.
	_, hasFeedback := fields["feedback"]
	_, hasFindings := fields["findings"]
	if hasFeedback == hasFindings {
		return nil, epochCommandValidationError(command, "The review JSON does not select one submission kind.", "A review submission must contain exactly one of feedback or findings", "send verdict with feedback for a plan review or verdict with findings for an implementation review", nil)
	}
	if hasFeedback {
		wire, err := strictEpochCommandInput[epochReviewPlanInput](command, data, "verdict", "feedback")
		if err != nil {
			return nil, err
		}
		if wire.Feedback == nil || len(wire.Feedback) > epochCommandMaxItems {
			return nil, epochCommandCollectionError(command, "feedback", len(wire.Feedback), true)
		}
		verdict, err := parseEpochCommandVerdict(command, wire.Verdict)
		if err != nil {
			return nil, err
		}
		feedback := make([]tasks.PlanReviewFeedback, len(wire.Feedback))
		for i, item := range wire.Feedback {
			if strings.TrimSpace(item.Body) == "" {
				return nil, epochCommandFieldError(command, fmt.Sprintf("feedback[%d].body", i), item.Body, "supply non-empty actionable feedback", nil)
			}
			feedback[i] = tasks.PlanReviewFeedback{Body: item.Body}
		}
		submission := tasks.PlanReviewSubmission{Verdict: verdict, Feedback: feedback}
		if err := submission.Validate(); err != nil {
			return nil, epochCommandValidationError(command, "The plan-review submission is not semantically valid.", "Plan review acceptance and revision feedback must agree", "correct the verdict and feedback before retrying", err)
		}
		return submission, nil
	}

	wire, err := strictEpochCommandInput[epochReviewImplementationInput](command, data, "verdict", "findings")
	if err != nil {
		return nil, err
	}
	if wire.Findings == nil || len(wire.Findings) > epochCommandMaxItems {
		return nil, epochCommandCollectionError(command, "findings", len(wire.Findings), true)
	}
	verdict, err := parseEpochCommandVerdict(command, wire.Verdict)
	if err != nil {
		return nil, err
	}
	findings := make([]tasks.ReviewFinding, len(wire.Findings))
	for i, item := range wire.Findings {
		findingTask, err := parseEpochCommandTask(command, fmt.Sprintf("findings[%d].task", i), item.Task)
		if err != nil {
			return nil, err
		}
		severity, err := parseEpochCommandSeverity(command, item.Severity)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(item.Summary) == "" {
			return nil, epochCommandFieldError(command, fmt.Sprintf("findings[%d].summary", i), item.Summary, "supply a non-empty actionable finding summary", nil)
		}
		findings[i] = tasks.ReviewFinding{Task: findingTask, Severity: severity, Summary: item.Summary}
	}
	submission := tasks.ImplementationReviewSubmission{Verdict: verdict, Findings: findings}
	if err := submission.Validate(); err != nil {
		return nil, epochCommandValidationError(command, "The implementation-review submission is not semantically valid.", "The binary verdict must agree with the typed finding severities", "correct the verdict and findings before retrying", err)
	}
	return submission, nil
}

type epochSliceReworkInput struct {
	Repository string                        `json:"repository"`
	Commit     string                        `json:"commit"`
	Findings   []epochFindingResolutionInput `json:"findings"`
}

type epochFindingResolutionInput struct {
	Finding  string                 `json:"finding"`
	Outcome  string                 `json:"outcome"`
	Evidence []provenance.JournalID `json:"evidence"`
}

func parseEpochCommandSliceRework(command string, data []byte) (tasks.SliceCandidateReplacement, tasks.ReworkSubmission, error) {
	wire, err := strictEpochCommandInput[epochSliceReworkInput](command, data, "repository", "commit", "findings")
	if err != nil {
		return tasks.SliceCandidateReplacement{}, tasks.ReworkSubmission{}, err
	}
	repository, err := parseEpochCommandRepository(command, wire.Repository)
	if err != nil {
		return tasks.SliceCandidateReplacement{}, tasks.ReworkSubmission{}, err
	}
	commit, err := parseEpochCommandGitOID(command, wire.Commit)
	if err != nil {
		return tasks.SliceCandidateReplacement{}, tasks.ReworkSubmission{}, err
	}
	rework, err := parseEpochCommandReworkSubmission(command, wire.Findings)
	if err != nil {
		return tasks.SliceCandidateReplacement{}, tasks.ReworkSubmission{}, err
	}
	return tasks.SliceCandidateReplacement{Repository: repository, Commit: commit}, rework, nil
}

type epochRepositoriesInput struct {
	Repositories []epochRepositoryCandidateInput `json:"repositories"`
}

type epochRepositoryCandidateInput struct {
	Repository string `json:"repository"`
	Candidate  string `json:"candidate"`
	Commit     string `json:"commit"`
}

func parseEpochCommandRepositories(command string, data []byte) ([]tasks.RepositoryCandidate, error) {
	wire, err := strictEpochCommandInput[epochRepositoriesInput](command, data, "repositories")
	if err != nil {
		return nil, err
	}
	return parseEpochCommandRepositoryCandidates(command, wire.Repositories)
}

type epochIntegrationReworkInput struct {
	Repositories []epochRepositoryCandidateInput `json:"repositories"`
	Findings     []epochFindingResolutionInput   `json:"findings"`
}

func parseEpochCommandIntegrationRework(command string, data []byte) ([]tasks.RepositoryCandidate, tasks.ReworkSubmission, error) {
	wire, err := strictEpochCommandInput[epochIntegrationReworkInput](command, data, "repositories", "findings")
	if err != nil {
		return nil, tasks.ReworkSubmission{}, err
	}
	repositories, err := parseEpochCommandRepositoryCandidates(command, wire.Repositories)
	if err != nil {
		return nil, tasks.ReworkSubmission{}, err
	}
	rework, err := parseEpochCommandReworkSubmission(command, wire.Findings)
	if err != nil {
		return nil, tasks.ReworkSubmission{}, err
	}
	return repositories, rework, nil
}

func parseEpochCommandRepositoryCandidates(command string, items []epochRepositoryCandidateInput) ([]tasks.RepositoryCandidate, error) {
	if len(items) == 0 || len(items) > epochCommandMaxItems {
		return nil, epochCommandCollectionError(command, "repositories", len(items), false)
	}
	result := make([]tasks.RepositoryCandidate, len(items))
	seen := make(map[tasks.RepositoryID]struct{}, len(items))
	for i, item := range items {
		repository, err := parseEpochCommandRepository(command, item.Repository)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[repository]; duplicate {
			return nil, epochCommandFieldError(command, fmt.Sprintf("repositories[%d].repository", i), item.Repository, "list each repository exactly once", nil)
		}
		seen[repository] = struct{}{}
		candidate, err := parseEpochCommandImplementationCandidate(command, item.Candidate)
		if err != nil {
			return nil, err
		}
		commit, err := parseEpochCommandGitOID(command, item.Commit)
		if err != nil {
			return nil, err
		}
		result[i] = tasks.RepositoryCandidate{Repository: repository, Candidate: candidate, Commit: commit}
	}
	return result, nil
}

func parseEpochCommandReworkSubmission(command string, items []epochFindingResolutionInput) (tasks.ReworkSubmission, error) {
	if len(items) == 0 || len(items) > epochCommandMaxItems {
		return tasks.ReworkSubmission{}, epochCommandCollectionError(command, "findings", len(items), false)
	}
	findings := make([]tasks.FindingResolution, len(items))
	seen := make(map[provenance.TaskID]struct{}, len(items))
	for i, item := range items {
		finding, err := parseEpochCommandTask(command, fmt.Sprintf("findings[%d].finding", i), item.Finding)
		if err != nil {
			return tasks.ReworkSubmission{}, err
		}
		if _, duplicate := seen[finding]; duplicate {
			return tasks.ReworkSubmission{}, epochCommandFieldError(command, fmt.Sprintf("findings[%d].finding", i), item.Finding, "resolve each finding exactly once", nil)
		}
		seen[finding] = struct{}{}
		outcome, err := parseEpochCommandFindingOutcome(command, item.Outcome)
		if err != nil {
			return tasks.ReworkSubmission{}, err
		}
		if item.Evidence == nil || len(item.Evidence) > epochCommandMaxItems {
			return tasks.ReworkSubmission{}, epochCommandCollectionError(command, fmt.Sprintf("findings[%d].evidence", i), len(item.Evidence), true)
		}
		if outcome == tasks.FindingFixed && len(item.Evidence) == 0 {
			return tasks.ReworkSubmission{}, epochCommandFieldError(command, fmt.Sprintf("findings[%d].evidence", i), "", "supply at least one positive committed evidence journal ID for a fixed finding", nil)
		}
		for _, evidence := range item.Evidence {
			if evidence <= 0 {
				return tasks.ReworkSubmission{}, epochCommandFieldError(command, fmt.Sprintf("findings[%d].evidence", i), fmt.Sprint(evidence), "supply only positive committed evidence journal IDs", nil)
			}
		}
		findings[i] = tasks.FindingResolution{Finding: finding, Outcome: outcome, Evidence: append([]provenance.JournalID(nil), item.Evidence...)}
	}
	return tasks.ReworkSubmission{Findings: findings}, nil
}

type epochPlanUATInput struct {
	Interactions  []tasks.UATInteraction  `json:"interactions"`
	Feedback      []tasks.UATFeedbackItem `json:"feedback"`
	HeldQuestions []tasks.HeldUATQuestion `json:"heldQuestions"`
}

func parseEpochCommandPlanUATPayload(command string, invocation EpochCommandInvocation) (*tasks.PlanUATPayload, error) {
	data, err := readEpochCommandInput(command, invocation)
	if err != nil {
		return nil, err
	}
	wire, err := strictEpochCommandInput[epochPlanUATInput](command, data, "interactions", "feedback", "heldQuestions")
	if err != nil {
		return nil, err
	}
	if wire.HeldQuestions == nil {
		return nil, epochCommandValidationError(command, "The Plan UAT heldQuestions collection must be a JSON array.", "Null collections make the recorded decision ambiguous", "encode heldQuestions as an array, using [] when empty", nil)
	}
	if err := validateEpochCommandUATCollections(command, wire.Interactions, wire.Feedback, wire.HeldQuestions); err != nil {
		return nil, err
	}
	return &tasks.PlanUATPayload{
		Interactions:  append([]tasks.UATInteraction(nil), wire.Interactions...),
		Feedback:      append([]tasks.UATFeedbackItem(nil), wire.Feedback...),
		HeldQuestions: append([]tasks.HeldUATQuestion(nil), wire.HeldQuestions...),
	}, nil
}

func validateEpochCommandPlanDeferral(payload *tasks.PlanUATPayload) error {
	if payload == nil {
		return epochCommandValidationError("epoch plan uat defer", "The deferred Plan UAT has no structured payload.", "AFK deferral requires the open questions it carries forward", "provide interactions, feedback, and heldQuestions through --input -", nil)
	}
	hasStableQuestion := false
	for _, question := range payload.HeldQuestions {
		hasStableQuestion = hasStableQuestion || question.Stable
	}
	if !hasStableQuestion {
		return epochCommandValidationError("epoch plan uat defer", "The deferred Plan UAT has no stable held question.", "AFK deferral is reserved for stable open questions", "include at least one heldQuestions item with stable set to true", nil)
	}
	for _, item := range payload.Feedback {
		if item.FixNow {
			return epochCommandValidationError("epoch plan uat defer", "The deferred Plan UAT carries FIX-NOW feedback.", "Blocking feedback must be recorded as a changes request instead of an AFK deferral", "use changes-request or remove the blocking feedback before deferring", nil)
		}
	}
	return nil
}

type epochImplementationUATInput struct {
	Interactions    []tasks.UATInteraction  `json:"interactions"`
	Feedback        []tasks.UATFeedbackItem `json:"feedback"`
	HeldAnswers     []epochResolutionInput  `json:"heldAnswers"`
	PlanFeedback    []epochResolutionInput  `json:"planFeedback"`
	LedgerDecisions []epochResolutionInput  `json:"ledgerDecisions"`
}

type epochResolutionInput struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Note   string `json:"note"`
}

func parseEpochCommandImplementationUATPayload(command string, invocation EpochCommandInvocation) (*tasks.ImplUATPayload, error) {
	data, err := readEpochCommandInput(command, invocation)
	if err != nil {
		return nil, err
	}
	wire, err := strictEpochCommandInput[epochImplementationUATInput](command, data, "interactions", "feedback", "heldAnswers", "planFeedback", "ledgerDecisions")
	if err != nil {
		return nil, err
	}
	if wire.HeldAnswers == nil || wire.PlanFeedback == nil || wire.LedgerDecisions == nil {
		return nil, epochCommandValidationError(command, "The Implementation UAT collections must be JSON arrays.", "Null collections make carry-forward resolution ambiguous", "encode every collection as an array, using [] when empty", nil)
	}
	if err := validateEpochCommandUATCollections(command, wire.Interactions, wire.Feedback, nil); err != nil {
		return nil, err
	}
	if len(wire.HeldAnswers) > epochCommandMaxItems || len(wire.PlanFeedback) > epochCommandMaxItems || len(wire.LedgerDecisions) > epochCommandMaxItems {
		return nil, epochCommandValidationError(command, "An Implementation UAT collection exceeds its item limit.", "Structured collection input is bounded before the store opens", fmt.Sprintf("supply at most %d items per collection", epochCommandMaxItems), nil)
	}
	held, err := parseEpochCommandHeldResolutions(command, wire.HeldAnswers)
	if err != nil {
		return nil, err
	}
	plan, err := parseEpochCommandPlanResolutions(command, wire.PlanFeedback)
	if err != nil {
		return nil, err
	}
	ledger, err := parseEpochCommandLedgerResolutions(command, wire.LedgerDecisions)
	if err != nil {
		return nil, err
	}
	return &tasks.ImplUATPayload{
		Interactions:    append([]tasks.UATInteraction(nil), wire.Interactions...),
		Feedback:        append([]tasks.UATFeedbackItem(nil), wire.Feedback...),
		HeldAnswers:     held,
		PlanFeedback:    plan,
		LedgerDecisions: ledger,
	}, nil
}

func validateEpochCommandUATCollections(command string, interactions []tasks.UATInteraction, feedback []tasks.UATFeedbackItem, heldQuestions []tasks.HeldUATQuestion) error {
	if interactions == nil || feedback == nil {
		return epochCommandValidationError(command, "The UAT interactions and feedback collections must be JSON arrays.", "Null collections make the recorded decision ambiguous", "encode interactions and feedback as arrays, using [] when empty", nil)
	}
	if len(interactions) > epochCommandMaxItems || len(feedback) > epochCommandMaxItems || (heldQuestions != nil && len(heldQuestions) > epochCommandMaxItems) {
		return epochCommandValidationError(command, "A UAT collection exceeds its item limit.", "Structured collection input is bounded before the store opens", fmt.Sprintf("supply at most %d items per collection", epochCommandMaxItems), nil)
	}
	for i, interaction := range interactions {
		if interaction.Prompt == "" || interaction.Response == "" {
			return epochCommandFieldError(command, fmt.Sprintf("interactions[%d]", i), "", "supply both the exact non-empty prompt and response", nil)
		}
	}
	seenFeedback := make(map[tasks.UATFeedbackID]struct{}, len(feedback))
	for i, item := range feedback {
		if item.ID == "" || strings.TrimSpace(item.Body) == "" {
			return epochCommandFieldError(command, fmt.Sprintf("feedback[%d]", i), string(item.ID), "supply a non-empty unique ID and actionable body", nil)
		}
		if _, duplicate := seenFeedback[item.ID]; duplicate {
			return epochCommandFieldError(command, fmt.Sprintf("feedback[%d].id", i), string(item.ID), "use each feedback identity exactly once", nil)
		}
		seenFeedback[item.ID] = struct{}{}
	}
	if heldQuestions == nil {
		return nil
	}
	seenHeld := make(map[tasks.HeldUATQuestionID]struct{}, len(heldQuestions))
	for i, item := range heldQuestions {
		if item.ID == "" || strings.TrimSpace(item.Question) == "" {
			return epochCommandFieldError(command, fmt.Sprintf("heldQuestions[%d]", i), string(item.ID), "supply a non-empty unique ID and question", nil)
		}
		if _, duplicate := seenHeld[item.ID]; duplicate {
			return epochCommandFieldError(command, fmt.Sprintf("heldQuestions[%d].id", i), string(item.ID), "use each held-question identity exactly once", nil)
		}
		seenHeld[item.ID] = struct{}{}
	}
	return nil
}

func parseEpochCommandHeldResolutions(command string, items []epochResolutionInput) ([]tasks.HeldQuestionResolution, error) {
	result := make([]tasks.HeldQuestionResolution, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		target, kind, err := parseEpochCommandResolution(command, "heldAnswers", i, item, seen)
		if err != nil {
			return nil, err
		}
		result[i] = tasks.HeldQuestionResolution{Target: tasks.HeldUATQuestionID(target), Kind: kind, Note: item.Note}
	}
	return result, nil
}

func parseEpochCommandPlanResolutions(command string, items []epochResolutionInput) ([]tasks.DeferredFeedbackResolution, error) {
	result := make([]tasks.DeferredFeedbackResolution, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		target, kind, err := parseEpochCommandResolution(command, "planFeedback", i, item, seen)
		if err != nil {
			return nil, err
		}
		result[i] = tasks.DeferredFeedbackResolution{Target: tasks.UATFeedbackID(target), Kind: kind, Note: item.Note}
	}
	return result, nil
}

func parseEpochCommandLedgerResolutions(command string, items []epochResolutionInput) ([]tasks.LedgerDecisionResolution, error) {
	result := make([]tasks.LedgerDecisionResolution, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		target, kind, err := parseEpochCommandResolution(command, "ledgerDecisions", i, item, seen)
		if err != nil {
			return nil, err
		}
		result[i] = tasks.LedgerDecisionResolution{Target: tasks.DecisionLedgerEntryID(target), Kind: kind, Note: item.Note}
	}
	return result, nil
}

func parseEpochCommandResolution(command, field string, index int, item epochResolutionInput, seen map[string]struct{}) (string, tasks.ResolutionKind, error) {
	target, err := parseEpochCommandScalar(command, fmt.Sprintf("%s[%d].target", field, index), item.Target)
	if err != nil {
		return "", 0, err
	}
	if _, duplicate := seen[target]; duplicate {
		return "", 0, epochCommandFieldError(command, fmt.Sprintf("%s[%d].target", field, index), target, "resolve each target exactly once", nil)
	}
	seen[target] = struct{}{}
	kind, err := parseEpochCommandResolutionKind(command, item.Kind)
	if err != nil {
		return "", 0, err
	}
	return target, kind, nil
}

type epochCommandResult struct {
	Replayed bool   `json:"replayed"`
	Epoch    string `json:"epoch"`
}

type epochDecisionResult struct {
	epochCommandResult
	Decision string `json:"decision"`
	Actor    string `json:"actor"`
}

type epochInteractionModeResult struct {
	Epoch string                `json:"epoch"`
	Mode  tasks.InteractionMode `json:"mode"`
}

type epochReviewStartResult struct {
	epochCommandResult
	Round       string `json:"round"`
	SubjectKind string `json:"subjectKind"`
	Subject     string `json:"subject"`
}

type epochReviewSubmitResult struct {
	epochCommandResult
	Round string `json:"round"`
	Axis  string `json:"axis"`
}

type epochReviewFinalizeResult struct {
	epochCommandResult
	Round string `json:"round"`
}

type epochSliceResult struct {
	epochCommandResult
	Slice string `json:"slice"`
}

type epochCandidateResult struct {
	epochCommandResult
	Slice     string `json:"slice"`
	Candidate string `json:"candidate"`
}

type epochIntegrationCandidateResult struct {
	epochCommandResult
	Candidate string `json:"candidate"`
}

type epochPublicationResult struct {
	epochCommandResult
	Candidate  string             `json:"candidate"`
	Repository tasks.RepositoryID `json:"repository"`
}

func epochCommandResultFrom(result tasks.CommandResult) epochCommandResult {
	return epochCommandResult{Replayed: result.Replayed, Epoch: string(result.Epoch)}
}

func epochDecisionResultFrom(result tasks.DecisionResult) epochDecisionResult {
	return epochDecisionResult{epochCommandResult: epochCommandResultFrom(result.CommandResult), Decision: string(result.DecisionID), Actor: result.ActorID.String()}
}

func epochReviewStartResultFrom(result tasks.ReviewStartResult) epochReviewStartResult {
	return epochReviewStartResult{
		epochCommandResult: epochCommandResultFrom(result.CommandResult),
		Round:              string(result.Round),
		SubjectKind:        result.Subject.Kind.String(),
		Subject:            result.Subject.SnapshotID,
	}
}

func epochReviewSubmitResultFrom(result tasks.ReviewSubmitResult) epochReviewSubmitResult {
	return epochReviewSubmitResult{epochCommandResult: epochCommandResultFrom(result.CommandResult), Round: string(result.Round), Axis: result.Axis.String()}
}

func epochCandidateResultFrom(result tasks.CandidateResult) epochCandidateResult {
	return epochCandidateResult{epochCommandResult: epochCommandResultFrom(result.CommandResult), Slice: result.Slice.String(), Candidate: string(result.Candidate)}
}

func epochCommandCollectionError(command, field string, length int, emptyAllowed bool) error {
	requirement := fmt.Sprintf("supply at most %d items", epochCommandMaxItems)
	if !emptyAllowed {
		requirement = fmt.Sprintf("supply between 1 and %d items", epochCommandMaxItems)
	}
	return epochCommandValidationError(command, fmt.Sprintf("The %s collection has %d items and is outside its allowed bound.", field, length), "Structured semantic collections are bounded before the Pasture store opens", requirement, nil)
}

func epochCommandFieldError(command, field, value, fix string, cause error) error {
	return epochCommandValidationError(command, fmt.Sprintf("The %q field has invalid value %q.", field, value), "The selected command requires a canonical typed value at this field", fix, cause)
}

func epochCommandValidationError(command, what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why + ".",
		Where:    fmt.Sprintf("Validating %s (internal/handlers/epoch_command.go).", command),
		Impact:   "The Pasture store was not opened and no workflow state was read or changed.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}

func epochCommandExecutionError(command, what, why, fix string, cause error) error {
	return &pasterrors.StructuredError{
		Category: pasterrors.CategoryWorkflow,
		What:     what,
		Why:      why + ".",
		Where:    fmt.Sprintf("Executing %s (internal/handlers/epoch_command.go).", command),
		Impact:   "The caller did not receive a complete successful semantic result.",
		Fix:      fix + ".",
		Cause:    cause,
	}
}

func preserveOrWrapEpochCommandError(command string, err error, category pasterrors.Category, what, why, fix string) error {
	var structured *pasterrors.StructuredError
	if stderrors.As(err, &structured) {
		return err
	}
	return &pasterrors.StructuredError{
		Category: category,
		What:     what,
		Why:      why + ".",
		Where:    fmt.Sprintf("Executing %s (internal/handlers/epoch_command.go).", command),
		Impact:   "The requested semantic operation did not return a successful result.",
		Fix:      fix + ".",
		Cause:    err,
	}
}
