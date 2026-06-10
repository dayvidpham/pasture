// Package engine — queue.go defines the DBOS WorkflowQueue for concurrency-limited
// slice and review sub-workflow dispatch.
//
// Sub-workflows that drive individual implementation slices and review cycles are
// dispatched through a shared DBOS WorkflowQueue with a configurable per-executor
// concurrency limit K. Bounded concurrency is the primary control point for the
// single-writer WAL bottleneck: 30+ unbounded sub-workflows would thrash the
// shared SQLite connection, so K is tuned to the write throughput of the
// pasture.db file.
//
// Queue lifecycle:
//   - NewSliceQueue must be called BEFORE dbos.Launch (NewWorkflowQueue panics
//     after Launch). Engine.New calls it as part of construction.
//   - Sub-workflows are enqueued via Engine.EnqueueSlice / Engine.EnqueueReview,
//     each of which calls dbos.RunWorkflow with dbos.WithQueue(SliceQueueName).
//   - DBOS dequeues and starts sub-workflows up to K at a time; excess are
//     durably backlogged in the queues table and retried automatically.
package engine

import (
	"fmt"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SliceQueueName is the canonical DBOS queue name for slice and review
// sub-workflow dispatch. A single shared queue keeps the concurrency budget
// unified across both sub-workflow kinds — slices and reviews compete for the
// same K slots, so the total in-flight count across both is bounded by K.
const SliceQueueName = "pasture-slice-queue"

// DefaultSliceQueueConcurrency is the default per-executor concurrency limit
// for the slice queue. The value balances SQLite WAL write throughput against
// parallel-agent utilisation:
//
//   - SQLite WAL serialises writers: only one write transaction commits at a
//     time. Under 30+ unbounded writers the commit queue grows faster than it
//     drains and busy_timeout errors accumulate.
//   - K=8 allows up to 8 sub-workflows to hold a write transaction concurrently;
//     empirically, 8 provides near-linear throughput on a single-disk host with
//     a 5 s busy_timeout, while 16+ shows diminishing returns and higher latency.
//   - Lower K (e.g. 4) is safer on slower storage (HDD, networked FS) or with
//     a stricter latency SLA; higher K (e.g. 16) is appropriate for an SSD-backed
//     host with idle I/O headroom.
//
// Override via --slice-concurrency / PASTURE_SLICE_CONCURRENCY or the engine
// Config.SliceConcurrency field.
const DefaultSliceQueueConcurrency = 8

// SliceConcurrencyEnv is the environment variable that overrides the
// per-executor concurrency limit for the slice queue. When set, its integer
// value is used instead of DefaultSliceQueueConcurrency.
const SliceConcurrencyEnv = "PASTURE_SLICE_CONCURRENCY"

// newSliceQueue registers the pasture-slice-queue with the given DBOS context
// and returns the WorkflowQueue. concurrency must be > 0; it bounds the number
// of slice/review sub-workflows the local executor runs concurrently.
//
// This must be called before dbos.Launch. Engine.New calls it as part of
// construction, so callers that use Engine.New do not need to call it directly.
func newSliceQueue(ctx dbos.DBOSContext, concurrency int) (dbos.WorkflowQueue, error) {
	if concurrency <= 0 {
		return dbos.WorkflowQueue{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("The slice queue concurrency limit must be a positive integer, got %d.", concurrency),
			Why:      "A concurrency of 0 or below would prevent any sub-workflows from running.",
			Where:    "Creating the slice queue (internal/engine/queue.go in engine.newSliceQueue).",
			Impact:   "No slice or review sub-workflows would ever execute.",
			Fix: "1. Set a positive value (default is 8):\n" +
				"     pastured --slice-concurrency 8\n" +
				"   Or set the environment variable:\n" +
				"     PASTURE_SLICE_CONCURRENCY=8 pastured",
		}
	}
	q := dbos.NewWorkflowQueue(ctx, SliceQueueName,
		dbos.WithWorkerConcurrency(concurrency),
	)
	return q, nil
}

// EnqueueSlice dispatches a SliceSubWorkflow via the slice queue, giving it the
// supplied workflow id (sliceId) so start_slice / complete_slice signals can
// address it. The caller supplies the epoch context needed for hook dispatch and
// parent progress signalling. The returned handle is live; callers may call
// GetResult to wait for the slice to complete.
func (e *Engine) EnqueueSlice(in SliceInput) (dbos.WorkflowHandle[SliceResult], error) {
	h, err := dbos.RunWorkflow(e.dbosCtx, e.SliceSubWorkflow, in,
		dbos.WithWorkflowID(in.SliceId),
		dbos.WithQueue(SliceQueueName),
	)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't enqueue the slice sub-workflow for slice %q.", in.SliceId),
			Why:      "The DBOS engine rejected the enqueue request — likely a storage failure or duplicate workflow id.",
			Where:    "Enqueuing a slice sub-workflow (internal/engine/queue.go in engine.EnqueueSlice).",
			Impact:   "The slice will not run until successfully enqueued.",
			Fix: "1. Confirm the database is healthy:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"2. Confirm the slice id is not already in use by a running workflow:\n" +
				fmt.Sprintf("     pasture status --epoch-id %s\n", in.EpochId) +
				"3. Retry once the issue is resolved.",
			Cause: err,
		}
	}
	return h, nil
}

// EnqueueReview dispatches a ReviewSubWorkflow via the slice queue, giving it a
// workflow id formed from "<epochId>-review-<phaseId>". The caller should retain
// the returned handle to send submit_vote signals and to wait for the result.
func (e *Engine) EnqueueReview(in ReviewInput) (dbos.WorkflowHandle[ReviewResult], error) {
	wfID := protocol.ReviewWorkflowID(in.EpochId, in.PhaseId)
	h, err := dbos.RunWorkflow(e.dbosCtx, e.ReviewSubWorkflow, in,
		dbos.WithWorkflowID(wfID),
		dbos.WithQueue(SliceQueueName),
	)
	if err != nil {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryWorkflow,
			What:     fmt.Sprintf("Couldn't enqueue the review sub-workflow for epoch %q phase %s.", in.EpochId, in.PhaseId),
			Why:      "The DBOS engine rejected the enqueue request — likely a storage failure or duplicate workflow id.",
			Where:    "Enqueuing a review sub-workflow (internal/engine/queue.go in engine.EnqueueReview).",
			Impact:   "The review phase will not proceed until the sub-workflow is enqueued.",
			Fix: "1. Confirm the database is healthy:\n" +
				"     ls -l ~/.local/share/pasture/pasture.db\n" +
				"2. Confirm the review is not already running for this epoch and phase.\n" +
				"3. Retry once the issue is resolved.",
			Cause: err,
		}
	}
	return h, nil
}
