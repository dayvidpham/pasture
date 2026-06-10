package engine

import (
	"fmt"
	"log/slog"
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
	// Round is the review-cycle counter for this (epochId, phaseId) pair. It
	// starts at 1 and increments each time a review returns REVISE and the
	// protocol re-enters the review phase. Supplying the round ensures each
	// re-review runs a fresh DBOS sub-workflow (a different id) rather than
	// returning the memoized result of a prior round.
	//
	// The round value MUST come from a deterministic, replay-stable counter
	// tracked in workflow state, NOT from wall-clock time or a random value.
	// Default 0 is treated as round 1 by EnqueueReview for backwards
	// compatibility (existing callers that don't set Round still get the
	// correct first-round workflow id).
	Round int `json:"round,omitempty"`
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
//
// Human review phases wait hours-to-days for votes. Each Recv iteration
// consumes durable step storage regardless of whether a vote arrived (the
// DBOS SDK records a row per iteration on timeout). At 5 s, a 24-hour
// idle review accumulates ~17,280 timeout-operation rows, adding constant
// write-transaction pressure to the shared SQLite WAL — exactly the
// bottleneck the slice-queue concurrency limit exists to protect.
//
// The long interval is safe because dbos.Send wakes the Recv goroutine
// immediately via the notification condvar — response latency to an
// arriving vote is independent of this timeout. The timeout only bounds
// how often we re-check ctx.Done() for cancellation, which the SDK's
// recv select already handles internally.
//
// 1 hour → ~24 iterations/day per idle review, vs ~17,280 at 5 s.
const reviewVotePollInterval = 1 * time.Hour

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

		// Validate the axis and vote before recording. Signals arrive via raw
		// dbos.Send payloads and any non-empty string is accepted at the transport
		// layer. A junk axis ("corectness") stored in the votes map would be
		// iterated by the success computation, potentially flipping Success=false
		// even when all canonical axes voted ACCEPT. Drop invalid signals and log
		// them so the sender can diagnose the issue.
		if !sig.Axis.IsValid() || !sig.Vote.IsValid() {
			slog.Default().Warn("review sub-workflow dropped invalid vote signal",
				"epochId", in.EpochId,
				"phaseId", in.PhaseId,
				"axis", string(sig.Axis),
				"vote", string(sig.Vote),
				"reason", "axis or vote value is not in the canonical set; check the ReviewAxis and VoteType constants",
			)
			continue
		}
		// Record the vote; later votes for the same axis overwrite earlier ones
		// (last-writer-wins per ReviewAxis key). This makes round-2 re-votes
		// after a REVISE deterministic.
		votes[sig.Axis] = sig.Vote
	}

	// Determine overall success: all canonical axes must have an ACCEPT vote.
	// We iterate AllReviewAxes (not the votes map) so that a junk axis that
	// somehow survived the validation guard above cannot flip the verdict.
	success := allAxesVoted(votes)
	if success {
		for _, ax := range protocol.AllReviewAxes {
			if votes[ax] != protocol.VoteAccept {
				success = false
				break
			}
		}
	}

	return ReviewResult{
		PhaseId:    in.PhaseId,
		Success:    success,
		VoteResult: votes,
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
