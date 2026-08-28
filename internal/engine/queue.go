// Package engine — queue.go defines the DBOS queue for concurrency-limited
// slice and review sub-workflow dispatch.
//
// Sub-workflows that drive individual implementation slices and review cycles are
// dispatched through a shared DBOS queue with a configurable per-executor
// concurrency limit K. Bounded concurrency is the primary control point for the
// single-writer WAL bottleneck: 30+ unbounded sub-workflows would thrash the
// shared SQLite connection, so K is tuned to the write throughput of the
// pasture.db file.
//
// Queue lifecycle:
//   - newSliceQueue registers a database-backed queue row and may be called
//     before or after dbos.Launch. Engine.New calls it during construction, so
//     the queue exists before the first enqueue.
//   - Sub-workflows are enqueued via Engine.EnqueueSlice / Engine.EnqueueReview,
//     each of which calls dbos.RunWorkflow with dbos.WithQueue on the registered
//     slice queue.
//   - DBOS dequeues and starts sub-workflows up to K at a time; excess are
//     held in the queues table until a running sub-workflow completes and frees
//     a slot. Multi-process crash recovery and automatic retry across restarts
//     are tracked as a separate follow-up item.
package engine

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SliceQueueName is the canonical DBOS queue name for slice and review
// sub-workflow dispatch. A single shared queue keeps the concurrency budget
// unified across both sub-workflow kinds — slices and reviews compete for the
// same K slots, so the total in-flight count across both is bounded by K.
const SliceQueueName = "pasture-slice-queue"

// ControlQueueName is the canonical DBOS queue name for epoch control
// workflows. CLI lifecycle commands enqueue onto this queue through a DBOS
// client; pastured hosts the registered workflow and dequeues it.
const ControlQueueName = "pasture-control-queue"

// EpochControlWorkflowName is the stable DBOS workflow name used by clients
// that enqueue epoch control work without linking to the engine implementation.
const EpochControlWorkflowName = "pasture.epoch_control.v1"

// DefaultSliceQueueConcurrency is the default per-executor concurrency limit
// for the slice queue. The value balances SQLite WAL write throughput against
// parallel-agent utilisation:
//
//   - SQLite WAL serialises writers: only one write transaction commits at a
//     time. Under 30+ unbounded writers the commit queue grows faster than it
//     drains and busy_timeout errors accumulate.
//   - K=8 allows up to 8 sub-workflows to hold a write transaction concurrently.
//     This is a conservative default chosen to stay within the WAL commit
//     throughput of a typical single-disk host; the right value depends on
//     your storage — lower K (e.g. 4) on HDD or network-attached storage,
//     higher K (e.g. 16) on NVMe-backed hosts with idle I/O headroom.
//   - A benchmark validating a specific K for your setup is the authoritative
//     guide; measure with your actual storage before changing this default.
//
// Override via --slice-concurrency / PASTURE_SLICE_CONCURRENCY or the engine
// Config.SliceConcurrency field.
const DefaultSliceQueueConcurrency = 8

// SliceConcurrencyEnv is the environment variable that overrides the
// per-executor concurrency limit for the slice queue. When set, its integer
// value is used instead of DefaultSliceQueueConcurrency.
const SliceConcurrencyEnv = "PASTURE_SLICE_CONCURRENCY"

// ResolveSliceConcurrency resolves the effective per-executor concurrency limit K
// from the three override sources, highest-priority first:
//
//  1. flagVal > 0: the caller-supplied CLI flag value (--slice-concurrency).
//  2. $PASTURE_SLICE_CONCURRENCY env var (non-empty, parses as a positive int).
//  3. DefaultSliceQueueConcurrency (8).
//
// If the env var is set but not a valid positive integer, the function returns
// an actionable validation error (the caller should surface it and exit 1).
// A zero or negative flagVal is treated as "not set" (fall through to env/default).
//
// This function is the single resolution rule shared by pastured and any other
// process that constructs an Engine; call it once at startup and pass the
// result to engine.Config.SliceConcurrency.
func ResolveSliceConcurrency(flagVal int) (int, error) {
	if flagVal > 0 {
		return flagVal, nil
	}
	envStr := os.Getenv(SliceConcurrencyEnv)
	if envStr != "" {
		v, err := strconv.Atoi(envStr)
		if err != nil || v <= 0 {
			return 0, &pasterrors.StructuredError{
				Category: pasterrors.CategoryValidation,
				What:     fmt.Sprintf("$%s=%q is not a valid concurrency limit.", SliceConcurrencyEnv, envStr),
				Why:      "The environment variable must be a positive integer.",
				Where:    "Resolving the slice-queue concurrency limit (internal/engine/queue.go in engine.ResolveSliceConcurrency).",
				Impact:   "The daemon cannot start without a valid concurrency limit for the slice queue.",
				Fix: fmt.Sprintf(
					"Set $%s to a positive integer (default is %d):\n"+
						"  export %s=8\n"+
						"Or unset it to use the default:\n"+
						"  unset %s",
					SliceConcurrencyEnv, DefaultSliceQueueConcurrency, SliceConcurrencyEnv, SliceConcurrencyEnv,
				),
			}
		}
		return v, nil
	}
	return DefaultSliceQueueConcurrency, nil
}

// queueConflictPolicy is the registration conflict policy both pasture queues
// use. A queue configuration is a row in the system database now, shared by
// every process that registers the same name, so a policy must be chosen.
//
// What changed: the configuration used to be private to the process that
// created it, so each process always polled its own settings. It is shared
// state today, and the queue runner reloads the row on every poll iteration, so
// a second pasture process that registers the same queue reconfigures the
// running one live.
//
// AlwaysUpdate says the process now starting governs the queue it is about to
// poll. For the only case pasture supports — peers of the same application
// version — it agrees with the runtime default (update only when this
// application version is the latest registered one). It differs only across
// versions, and a mixed-version deployment is out of scope: an upgrade replaces
// the database rather than sharing it. AlwaysUpdate does, however, maximise the
// live-reconfiguration effect described above.
//
// Whether a running queue should be reconfigurable by a starting peer at all —
// together with concurrency hot-reload and the recovery re-enqueue semantics —
// is a design decision for the queue work tracked in
// https://github.com/dayvidpham/pasture/issues/104.
const queueConflictPolicy = dbos.QueueConflictAlwaysUpdate

// newSliceQueue registers the pasture-slice-queue with the given DBOS context
// and returns the queue. concurrency must be > 0; Engine.New enforces this by
// clamping any <= 0 value to DefaultSliceQueueConcurrency before calling here,
// so an invalid concurrency is a programming error, not a user-facing case.
//
// Registration writes the queue configuration to the system database and can
// fail (a storage error, or an invalid configuration); the error is returned to
// the caller rather than swallowed. Engine.New calls this during construction,
// so callers that use Engine.New do not need to call it directly. (Note: the
// function is intentionally unexported; see Engine.SliceQueue for the public
// accessor.)
func newSliceQueue(ctx dbos.Context, concurrency int, basePollingInterval time.Duration) (dbos.Queue, error) {
	opts := queueOptions(basePollingInterval, dbos.WithWorkerConcurrency(concurrency))
	return dbos.RegisterQueue(ctx, SliceQueueName, opts...)
}

func newControlQueue(ctx dbos.Context, basePollingInterval time.Duration) (dbos.Queue, error) {
	opts := queueOptions(basePollingInterval, dbos.WithWorkerConcurrency(1))
	return dbos.RegisterQueue(ctx, ControlQueueName, opts...)
}

func queueOptions(basePollingInterval time.Duration, opts ...dbos.QueueOption) []dbos.QueueOption {
	opts = append(opts, dbos.WithQueueOnConflict(queueConflictPolicy))
	if basePollingInterval > 0 {
		opts = append(opts, dbos.WithQueueBasePollingInterval(basePollingInterval))
	}
	return opts
}

// EnqueueSlice dispatches a SliceSubWorkflow via the slice queue, giving it the
// supplied workflow id (sliceId) so start_slice / complete_slice signals can
// address it. The caller supplies the epoch context needed for hook dispatch and
// parent progress signalling. The returned handle is live; callers may call
// GetResult to wait for the slice to complete.
func (e *Engine) EnqueueSlice(in SliceInput) (dbos.WorkflowHandle[SliceResult], error) {
	if in.SliceId == "" {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A non-empty slice ID is required to enqueue a slice sub-workflow.",
			Why:      "SliceInput.SliceId was empty; the workflow id and the start_slice/complete_slice signal address are both derived from it.",
			Where:    "Enqueuing a slice sub-workflow (internal/engine/queue.go in engine.EnqueueSlice).",
			Impact:   "Without a slice ID the sub-workflow cannot be addressed by signals, so the slice cannot be started or completed.",
			Fix:      "Set SliceInput.SliceId to the unique identifier for this slice before calling EnqueueSlice.",
		}
	}
	if in.EpochId == "" {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A non-empty epoch ID is required to enqueue a slice sub-workflow.",
			Why:      "SliceInput.EpochId was empty; the epoch ID is needed for hook dispatch and the parent progress signal.",
			Where:    "Enqueuing a slice sub-workflow (internal/engine/queue.go in engine.EnqueueSlice).",
			Impact:   "Without an epoch ID, hook events and progress signals cannot be correlated with the parent epoch.",
			Fix:      "Set SliceInput.EpochId to the ID of the epoch that owns this slice.",
		}
	}
	h, err := dbos.RunWorkflow(e.dbosCtx, e.SliceSubWorkflow, in,
		dbos.WithWorkflowID(in.SliceId),
		dbos.WithQueue(e.sliceQueue),
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

// EnqueueReview dispatches a ReviewSubWorkflow via the slice queue. The
// workflow id is derived from the epoch id, phase id, and review round so
// that each round of a review cycle runs a fresh sub-workflow rather than
// returning the memoized result of a prior round.
//
// The caller should retain the returned handle to send submit_vote signals
// and to wait for the result. To compute the same workflow id for vote
// delivery, call protocol.ReviewWorkflowID(epochId, phaseId, round).
func (e *Engine) EnqueueReview(in ReviewInput) (dbos.WorkflowHandle[ReviewResult], error) {
	if in.EpochId == "" {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A non-empty epoch ID is required to enqueue a review sub-workflow.",
			Why:      "ReviewInput.EpochId was empty; the epoch ID is part of the deterministic review workflow id.",
			Where:    "Enqueuing a review sub-workflow (internal/engine/queue.go in engine.EnqueueReview).",
			Impact:   "An empty epoch ID produces a degenerate workflow id that cannot be addressed by vote signals.",
			Fix:      "Set ReviewInput.EpochId to the ID of the epoch that owns this review phase.",
		}
	}
	if in.PhaseId == "" {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "A non-empty phase ID is required to enqueue a review sub-workflow.",
			Why:      "ReviewInput.PhaseId was empty; the phase ID is part of the deterministic review workflow id.",
			Where:    "Enqueuing a review sub-workflow (internal/engine/queue.go in engine.EnqueueReview).",
			Impact:   "An empty phase ID produces a degenerate workflow id ('-review-') that cannot be uniquely addressed.",
			Fix:      "Set ReviewInput.PhaseId to the review phase identifier (e.g. \"review\" or \"code-review\").",
		}
	}
	round := in.Round
	if round <= 0 {
		round = 1
	}
	wfID := protocol.ReviewWorkflowID(in.EpochId, in.PhaseId, round)
	h, err := dbos.RunWorkflow(e.dbosCtx, e.ReviewSubWorkflow, in,
		dbos.WithWorkflowID(wfID),
		dbos.WithQueue(e.sliceQueue),
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
