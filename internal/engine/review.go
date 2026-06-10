package engine

import (
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ReviewInput is the input to a review sub-workflow.
type ReviewInput struct {
	// EpochId is the parent epoch this review belongs to.
	EpochId string `json:"epochId"`
	// PhaseId identifies which review phase this is (e.g. "review" or "code-review").
	PhaseId string `json:"phaseId"`
}

// ReviewResult is the output of a review sub-workflow.
type ReviewResult struct {
	// PhaseId echoes the input for correlation on the parent side.
	PhaseId string `json:"phaseId"`
	// Success is true when all review axes received an ACCEPT vote.
	Success bool `json:"success"`
	// VoteResult is the per-axis vote map collected by the sub-workflow.
	VoteResult map[protocol.ReviewAxis]protocol.VoteType `json:"voteResult"`
}

// reviewVotePollInterval is the Recv timeout for each vote-wait iteration.
// Short enough to check cancellation frequently; long enough not to spin.
const reviewVotePollInterval = 5 * time.Second

// ReviewSubWorkflow is the DBOS sub-workflow for a single P4/P10 review phase.
//
// Lifecycle:
//  1. Dispatched via Engine.EnqueueReview to the slice queue; starts when a
//     queue slot is free (bounded by K).
//  2. Receives submit_vote signals (ReviewVoteSignal) via a polling Recv loop
//     until all three ReviewAxis members have voted.
//  3. Returns a ReviewResult with the collected per-axis vote map.
//
// The submit_vote signals are addressed to this sub-workflow by the id assigned
// by Engine.EnqueueReview (protocol.ReviewWorkflowID(epochId, phaseId)).
//
// Idempotency: if the same axis votes twice, the later vote overwrites the
// earlier one (last-writer-wins per ReviewAxis key).
func (e *Engine) ReviewSubWorkflow(ctx dbos.DBOSContext, in ReviewInput) (ReviewResult, error) {
	votes := make(map[protocol.ReviewAxis]protocol.VoteType)

	// Poll for submit_vote signals until all axes have voted or the workflow
	// is cancelled. Each Recv call blocks up to reviewVotePollInterval and
	// returns a timeout error when the queue is empty for that window.
	for {
		// Check if all axes have voted before blocking again.
		if allAxesVoted(votes) {
			break
		}

		sig, err := dbos.Recv[protocol.ReviewVoteSignal](ctx,
			protocol.SignalSubmitVote.String(), reviewVotePollInterval)
		if err != nil {
			if isRecvTimeout(err) {
				// No vote in this window — loop and check again (or wait for
				// a cancellation event on the next Recv).
				continue
			}
			// Genuine error (cancellation, storage failure, etc.) — abort.
			return ReviewResult{}, fmt.Errorf("review %q phase %q: error waiting for vote: %w",
				in.EpochId, in.PhaseId, err)
		}

		// Record the vote; later votes for the same axis overwrite earlier ones.
		if sig.Axis != "" {
			votes[sig.Axis] = sig.Vote
		}
	}

	// Determine overall success: all-ACCEPT means the review passes.
	success := true
	for _, v := range votes {
		if v != protocol.VoteAccept {
			success = false
			break
		}
	}

	// Defensive copy.
	result := make(map[protocol.ReviewAxis]protocol.VoteType, len(votes))
	for k, v := range votes {
		result[k] = v
	}

	return ReviewResult{
		PhaseId:    in.PhaseId,
		Success:    success,
		VoteResult: result,
	}, nil
}

// allAxesVoted returns true when every member of protocol.AllReviewAxes has an
// entry in the votes map.
func allAxesVoted(votes map[protocol.ReviewAxis]protocol.VoteType) bool {
	for _, ax := range protocol.AllReviewAxes {
		if _, ok := votes[ax]; !ok {
			return false
		}
	}
	return true
}
