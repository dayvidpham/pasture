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
			What:     "epoch-id is required",
			Why:      "--epoch-id flag was not provided",
			Impact:   "vote signal cannot be sent without an epoch ID",
			Fix:      "provide --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if !axis.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a valid review axis", axis),
			Why:      "ReviewAxis must be one of: correctness, test_quality, elegance",
			Impact:   "vote cannot be recorded with an unknown axis",
			Fix:      "pass --axis correctness|test_quality|elegance",
		}
		return pasterrors.ExitCode(err), err
	}
	if !vote.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a valid vote", vote),
			Why:      "VoteType must be one of: ACCEPT, REVISE",
			Impact:   "vote cannot be recorded with an unknown value",
			Fix:      "pass --vote ACCEPT|REVISE",
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
			What:     fmt.Sprintf("vote signal failed for epoch %q", epochID),
			Why:      err.Error(),
			Impact:   "vote was not recorded",
			Fix:      fmt.Sprintf("verify that epoch %q exists and is running", epochID),
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
			What:     "epoch-id is required",
			Why:      "--epoch-id flag was not provided",
			Impact:   "completion signal cannot be sent without an epoch ID",
			Fix:      "provide --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if sliceID == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "slice-id is required",
			Why:      "--slice-id flag was not provided",
			Impact:   "completion signal cannot be sent without a slice ID",
			Fix:      "provide --slice-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if output != nil && errMsg != nil {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "--output and --error are mutually exclusive",
			Why:      "a slice completion is either successful (--output) or failed (--error), not both",
			Impact:   "completion signal cannot be sent with ambiguous result",
			Fix:      "provide either --output <text> or --error <text>, not both",
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
			What:     fmt.Sprintf("complete signal failed for slice %q in epoch %q", sliceID, epochID),
			Why:      err.Error(),
			Impact:   "slice completion was not signaled",
			Fix:      fmt.Sprintf("verify that epoch %q exists and is running", epochID),
		}
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}
