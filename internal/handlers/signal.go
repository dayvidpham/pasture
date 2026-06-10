package handlers

import (
	"context"
	"fmt"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SignalVote delivers a review-vote signal to the epoch's control workflow.
//
// Validates axis and vote before sending. reviewerId identifies the reviewer
// agent; it is optional but recommended for the audit trail.
//
// Exit codes: 0=success, 1=validation error, 3=workflow error.
func SignalVote(
	ctrl EpochController,
	epochId string,
	axis protocol.ReviewAxis,
	vote protocol.VoteType,
	reviewerId string,
	format types.OutputFormat,
) (int, error) {
	if err := requireEpochID(epochId, "record a vote",
		"Recording a review vote (internal/handlers/signal.go in handlers.SignalVote).",
		"pasture signal vote --epoch-id <id> --axis <axis> --vote <vote>"); err != nil {
		return pasterrors.ExitCode(err), err
	}
	if !axis.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a recognised review axis.", axis),
			Why:      "Reviews are scored along three named axes: correctness, test_quality, and elegance.",
			Where:    "Recording a review vote (internal/handlers/signal.go in handlers.SignalVote).",
			Impact:   "The vote can't be recorded against an unknown axis.",
			Fix: "1. Pick one of the three review axes and retry:\n" +
				"     pasture signal vote --axis correctness  --epoch-id <id> --vote <vote>\n" +
				"     pasture signal vote --axis test_quality --epoch-id <id> --vote <vote>\n" +
				"     pasture signal vote --axis elegance     --epoch-id <id> --vote <vote>",
		}
		return pasterrors.ExitCode(err), err
	}
	if !vote.IsValid() {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("%q is not a recognised vote value.", vote),
			Why:      "A vote must be either ACCEPT or REVISE.",
			Where:    "Recording a review vote (internal/handlers/signal.go in handlers.SignalVote).",
			Impact:   "The vote can't be recorded with an unknown value.",
			Fix: "1. Use one of the two recognised vote values:\n" +
				"     pasture signal vote --vote ACCEPT --epoch-id <id> --axis <axis>\n" +
				"     pasture signal vote --vote REVISE --epoch-id <id> --axis <axis>",
		}
		return pasterrors.ExitCode(err), err
	}

	sig := protocol.ReviewVoteSignal{Axis: axis, Vote: vote, ReviewerId: reviewerId}
	if err := ctrl.SubmitVote(context.Background(), epochId, sig); err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}

// SignalComplete delivers a slice-progress signal marking a slice complete.
//
// output and errMsg are mutually exclusive: set output for success, errMsg for
// failure. Both nil is treated as a successful completion with no output.
//
// Exit codes: 0=success, 1=validation error, 3=workflow error.
func SignalComplete(
	ctrl EpochController,
	epochId, sliceId string,
	output, errMsg *string,
	format types.OutputFormat,
) (int, error) {
	if err := requireEpochID(epochId, "mark a slice complete",
		"Marking a slice complete (internal/handlers/signal.go in handlers.SignalComplete).",
		"pasture signal complete --epoch-id <id> --slice-id <id>"); err != nil {
		return pasterrors.ExitCode(err), err
	}
	if sliceId == "" {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A slice ID is required to mark a slice complete.",
			Why:      "The --slice-id flag was not provided.",
			Where:    "Marking a slice complete (internal/handlers/signal.go in handlers.SignalComplete).",
			Impact:   "Without a slice ID, there's nothing to mark complete.",
			Fix: "1. Pass the slice's ID:\n" +
				"     pasture signal complete --slice-id <id> --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}
	if output != nil && errMsg != nil {
		err := &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Pass either --output or --error, not both.",
			Why:      "A slice completion is either a success (with --output) or a failure (with --error). It can't be both at once.",
			Where:    "Marking a slice complete (internal/handlers/signal.go in handlers.SignalComplete).",
			Impact:   "The completion can't be recorded because the result is ambiguous.",
			Fix: "1. Pick one and retry. For success:\n" +
				"     pasture signal complete --output \"<result>\" --slice-id <id> --epoch-id <id>\n" +
				"2. For failure:\n" +
				"     pasture signal complete --error \"<reason>\" --slice-id <id> --epoch-id <id>",
		}
		return pasterrors.ExitCode(err), err
	}

	completed := errMsg == nil
	stageName := "complete"
	if !completed {
		stageName = "error"
	}
	sig := protocol.SliceProgressSignal{
		SliceId:    sliceId,
		LeafTaskId: sliceId,
		StageName:  stageName,
		Completed:  completed,
	}
	if err := ctrl.ReportSliceProgress(context.Background(), epochId, sig); err != nil {
		return pasterrors.ExitCode(err), err
	}

	out, fmtErr := formatters.FormatSignalResult(true, format)
	if fmtErr != nil {
		return pasterrors.ExitCode(fmtErr), fmtErr
	}
	fmt.Println(out)
	return 0, nil
}
