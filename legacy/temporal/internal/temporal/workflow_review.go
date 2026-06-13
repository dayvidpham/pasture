package temporal

import (
	"github.com/dayvidpham/pasture/pkg/protocol"
	"go.temporal.io/sdk/workflow"
)

// ReviewPhaseWorkflow is the child workflow for P4_Review or P10_CodeReview.
//
// Receives ReviewVoteSignal signals from reviewer agents via SubmitVote().
// Waits using workflow.Await() until all 3 ReviewAxis members (Correctness,
// TestQuality, Elegance) have cast their vote, then returns a ReviewResult
// with the complete vote mapping.
//
// Signal routing: EpochWorkflow can forward ReviewVoteSignal to this child via
// the external workflow handle if desired. Alternatively, reviewer agents send
// signals directly to the ReviewPhaseWorkflow instance.
//
// Port of Python ReviewPhaseWorkflow in scripts/aura_protocol/workflow.py.
type ReviewPhaseWorkflow struct {
	votes map[protocol.ReviewAxis]protocol.VoteType
}

// SubmitVote is the signal handler for reviewer votes.
// Idempotent: if the same axis votes again, the later vote overwrites.
func (rw *ReviewPhaseWorkflow) SubmitVote(_ workflow.Context, sig protocol.ReviewVoteSignal) {
	rw.votes[sig.Axis] = sig.Vote
}

// Run waits for all 3 ReviewAxis members to vote, then returns results.
//
// Blocks via workflow.Await() until all 3 axes (Correctness, TestQuality,
// Elegance) have submitted votes. Returns a ReviewResult with the full
// vote mapping.
func (rw *ReviewPhaseWorkflow) Run(ctx workflow.Context, input ReviewInput) (*ReviewResult, error) {
	rw.votes = make(map[protocol.ReviewAxis]protocol.VoteType)

	// Register signal handler via goroutine-per-channel pattern.
	workflow.Go(ctx, func(ctx workflow.Context) {
		ch := workflow.GetSignalChannel(ctx, string(protocol.SignalSubmitVote))
		for {
			var sig protocol.ReviewVoteSignal
			ch.Receive(ctx, &sig)
			rw.SubmitVote(ctx, sig)
		}
	})

	// Wait until all 3 ReviewAxis members have voted.
	_ = workflow.Await(ctx, func() bool {
		for _, ax := range protocol.AllReviewAxes {
			if _, ok := rw.votes[ax]; !ok {
				return false
			}
		}
		return true
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Defensive copy of votes.
	result := make(map[protocol.ReviewAxis]protocol.VoteType, len(rw.votes))
	for k, v := range rw.votes {
		result[k] = v
	}

	return &ReviewResult{
		PhaseId:    input.PhaseId,
		Success:    true,
		VoteResult: result,
	}, nil
}

// ReviewWorkflowFn is the top-level function registered with the Temporal worker.
// Exported for test registration via TestWorkflowEnvironment.RegisterWorkflow.
func ReviewWorkflowFn(ctx workflow.Context, input ReviewInput) (*ReviewResult, error) {
	rw := &ReviewPhaseWorkflow{}
	return rw.Run(ctx, input)
}

// reviewWorkflowFn is an alias for RegisterWorkflows backward compatibility.
var reviewWorkflowFn = ReviewWorkflowFn
