package handlers

import (
	"context"
	"fmt"

	"github.com/dayvidpham/pasture/internal/config"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/temporal"
	"github.com/dayvidpham/pasture/internal/types"
)

// SignalVote sends a ReviewVoteSignal to the EpochWorkflow.
//
// Validates axis and vote values before connecting to Temporal. The reviewerID
// identifies the reviewer agent; it is optional but recommended for audit trail.
//
// Exit codes: 0=success, 1=validation error, 2=connection error, 3=workflow error.
func SignalVote(
	ctx context.Context,
	conn config.ConnectionConfig,
	epochID string,
	axis types.ReviewAxis,
	vote types.VoteType,
	reviewerID string,
	format types.OutputFormat,
	factory TemporalClientFactory,
) (int, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}

	if epochID == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "An epoch ID is required to record a vote.",
			Why:      "The --epoch-id flag was not provided.",
			Impact:   "Without an epoch ID, the vote can't be associated with any review.",
			Fix: "1. Pass the epoch's ID:\n" +
				"     pasture-msg vote --epoch-id <id> --axis <axis> --vote <vote>\n" +
				"2. If you don't know the epoch ID, list active epochs:\n" +
				"     pasture-msg epoch list",
		}
		return pasterrors.ExitCode(err), err
	}
	if !axis.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a recognised review axis.", axis),
			Why:      "Reviews are scored along three named axes: correctness, test_quality, and elegance.",
			Impact:   "The vote can't be recorded against an unknown axis.",
			Fix: "1. Pick one of the three review axes and retry:\n" +
				"     pasture-msg vote --axis correctness  --epoch-id <id> --vote <vote>\n" +
				"     pasture-msg vote --axis test_quality --epoch-id <id> --vote <vote>\n" +
				"     pasture-msg vote --axis elegance     --epoch-id <id> --vote <vote>",
		}
		return pasterrors.ExitCode(err), err
	}
	if !vote.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a recognised vote value.", vote),
			Why:      "A vote must be either ACCEPT or REVISE.",
			Impact:   "The vote can't be recorded with an unknown value.",
			Fix: "1. Use one of the two recognised vote values:\n" +
				"     pasture-msg vote --vote ACCEPT --epoch-id <id> --axis <axis>\n" +
				"     pasture-msg vote --vote REVISE --epoch-id <id> --axis <axis>",
		}
		return pasterrors.ExitCode(err), err
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	payload := types.ReviewVoteSignal{
		Axis:       axis,
		Vote:       vote,
		ReviewerID: reviewerID,
	}

	if err := c.SignalWorkflow(ctx, epochID, "", temporal.SignalSubmitVote, payload); err != nil {
		return pasterrors.ExitCode(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow}), &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't record the vote for epoch %q.", epochID),
			Why:      fmt.Sprintf("The Temporal server rejected the vote signal: %s", err),
			Impact:   "The vote was not recorded against this review.",
			Fix: fmt.Sprintf("1. Confirm the epoch is currently running:\n"+
				"     pasture-msg epoch status --epoch-id %q\n"+
				"2. If the epoch isn't found, list active epochs to find the right ID:\n"+
				"     pasture-msg epoch list\n"+
				"3. Retry the vote once the epoch is healthy.",
				epochID),
		}
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// SignalComplete sends a SliceProgressSignal to the EpochWorkflow marking a
// slice as completed.
//
// output and errMsg are mutually exclusive: set output for success, errMsg for
// failure. Both may be nil (treated as successful completion with no output).
//
// Exit codes: 0=success, 1=validation error, 2=connection error, 3=workflow error.
func SignalComplete(
	ctx context.Context,
	conn config.ConnectionConfig,
	epochID, sliceID string,
	output, errMsg *string,
	format types.OutputFormat,
	factory TemporalClientFactory,
) (int, error) {
	if factory == nil {
		factory = DefaultClientFactory
	}

	if epochID == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "An epoch ID is required to mark a slice complete.",
			Why:      "The --epoch-id flag was not provided.",
			Impact:   "Without an epoch ID, the completion can't be linked to a workflow.",
			Fix: "1. Pass the epoch's ID:\n" +
				"     pasture-msg complete --epoch-id <id> --slice-id <id>\n" +
				"2. If you don't know the epoch ID, list active epochs:\n" +
				"     pasture-msg epoch list",
		}
		return pasterrors.ExitCode(err), err
	}
	if sliceID == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A slice ID is required to mark a slice complete.",
			Why:      "The --slice-id flag was not provided.",
			Impact:   "Without a slice ID, there's nothing to mark complete.",
			Fix: "1. Pass the slice's ID:\n" +
				"     pasture-msg complete --slice-id <id> --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if output != nil && errMsg != nil {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Pass either --output or --error, not both.",
			Why:      "A slice completion is either a success (with --output) or a failure (with --error). It can't be both at once.",
			Impact:   "The completion can't be recorded because the result is ambiguous.",
			Fix: "1. Pick one and retry. For success:\n" +
				"     pasture-msg complete --output \"<result>\" --slice-id <id> --epoch-id <id>\n" +
				"2. For failure:\n" +
				"     pasture-msg complete --error \"<reason>\" --slice-id <id> --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	c, err := factory(ctx, conn)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer c.Close()

	// Build payload: Completed=true for success path, false when errMsg set.
	completed := errMsg == nil
	stageName := "complete"
	if !completed {
		stageName = "error"
	}

	payload := types.SliceProgressSignal{
		SliceID:    sliceID,
		LeafTaskID: sliceID, // use sliceID as the leaf task identifier for top-level completion
		StageName:  stageName,
		Completed:  completed,
	}

	if err := c.SignalWorkflow(ctx, epochID, "", temporal.SignalSliceProgress, payload); err != nil {
		return pasterrors.ExitCode(&pasterrors.StructuredError{Category: pasterrors.CategoryWorkflow}), &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't mark slice %q complete in epoch %q.", sliceID, epochID),
			Why:      fmt.Sprintf("The Temporal server rejected the completion signal: %s", err),
			Impact:   "The slice's completion isn't recorded, so the workflow can't move past it.",
			Fix: fmt.Sprintf("1. Confirm the epoch is currently running:\n"+
				"     pasture-msg epoch status --epoch-id %q\n"+
				"2. If the epoch isn't found, list active epochs to find the right ID:\n"+
				"     pasture-msg epoch list\n"+
				"3. Retry the completion once the epoch is healthy.",
				epochID),
		}
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}
