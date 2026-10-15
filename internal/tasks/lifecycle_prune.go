package tasks

import (
	"context"
	"fmt"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/lifecycle/projection"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// PruneBatchBound bounds how many batches one prune run may execute. Each batch
// is ONE projection rebuild and its bounded delete, in its own write
// transaction, so the bound is also a bound on how long one operator command
// may keep taking the store's write lock. At the batch cap the run may stop
// with eligible blobs still present; it says so with PruneOutcome.Truncated
// rather than pretending the job is done.
const PruneBatchBound = 256

// PruneOptions is what the operator's prune brings to the store.
type PruneOptions struct {
	// Retain is the retention age: an orphan blob older than max(Retain,
	// writer window) is eligible. Zero keeps no orphan beyond the writer-window
	// safety floor, which is the read path's behavior, so the operator drops
	// every blob the store can safely part with.
	Retain time.Duration
	// MaxBytes, when positive, stops the run once this many bytes of orphan
	// blobs have been dropped, so one command cannot reclaim more than the
	// operator asked for. Zero removes the size bound.
	MaxBytes int64
	// Vacuum runs VACUUM after the reclaim, returning the freed pages to the
	// filesystem and shrinking the file. VACUUM rewrites the whole database and
	// takes an exclusive lock, so it is opt-in.
	Vacuum bool
}

// PruneOutcome is what one prune run did, in the terms an operator reads.
type PruneOutcome struct {
	// Batches is how many projection rebuilds the run executed.
	Batches int
	// Reclaimed is how many orphan blobs the run deleted.
	Reclaimed int64
	// ReclaimedBytes is how many bytes of payload those blobs held, summed
	// from the deleted rows themselves.
	ReclaimedBytes int64
	// Truncated is true when the run stopped at the batch bound or the byte
	// budget with eligible blobs still present, so the operator knows a second
	// run would find more.
	Truncated bool
	// Failure is the reclaim failure of the first batch that could not delete,
	// if any. It never fails the run: the batch transaction rolled back, the
	// projection still committed, and the batches before it stand.
	Failure error
	// Vacuumed is true only when VACUUM ran and succeeded.
	Vacuumed bool
	// VacuumFailure carries a VACUUM that could not run. It is not fatal: the
	// reclaim has already committed and freed the pages for reuse; only the
	// file shrink is deferred.
	VacuumFailure error
}

// PruneLifecyclePayloads reclaims orphan payload blobs in bounded batches. It
// is the ONLY operator-driven delete of payload blob rows, and every invariant
// the read path's reclaim holds holds here:
//
//   - a blob an occurrence names is never deleted, because each batch rebuilds
//     the occurrence projection from the journal in the same transaction the
//     delete runs in;
//   - a blob with an unknown age (written_at 0) is never deleted;
//   - a blob younger than the writer window is never deleted, because its
//     writer may still be between its blob write and its journal append;
//   - and the operator's one extra refusal, the blob at the greatest
//     written_at, is never deleted.
//
// Each batch is its own write transaction, so a store that is being written
// while the prune runs keeps moving between batches. A blob that becomes named
// concurrently cannot be deleted out from under its writer: any occurrence
// committed after a batch's rebuild names a blob whose Put stamped it inside
// the writer window, and the age bound keeps it.
func PruneLifecyclePayloads(ctx context.Context, tracker protocol.TaskTracker, options PruneOptions) (PruneOutcome, error) {
	if options.Retain < 0 || options.MaxBytes < 0 {
		return PruneOutcome{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The payload blob prune was given a negative bound.",
			Why:      fmt.Sprintf("A retention age and a byte budget are durations and counts; this call gave %s and %d.", options.Retain, options.MaxBytes),
			Where:    "Pruning lifecycle payload blobs (internal/tasks/lifecycle_prune.go in tasks.PruneLifecyclePayloads).",
			Impact:   "Nothing was opened and nothing was deleted.",
			Fix:      "Pass a retention age of zero or more and a byte budget of zero (no budget) or more.",
		}
	}
	store, ok := tracker.(lifecycleReceiptStore)
	if !ok || store.auditDBHandle() == nil {
		return PruneOutcome{}, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "The supplied tracker cannot prune lifecycle payload blobs.",
			Why:      "Pruning needs the unified journal and database handle the tracker holds.",
			Where:    "Pruning lifecycle payload blobs (internal/tasks/lifecycle_prune.go in tasks.PruneLifecyclePayloads).",
			Impact:   "No payload blob was inspected or changed.",
			Fix:      "Use the tracker returned by tasks.OpenTaskTracker.",
		}
	}
	policy := projection.ReclaimPolicy{MaxAge: options.Retain, Cap: projection.PruneCap, KeepNewest: true}
	outcome := PruneOutcome{}
	drained := false
	for outcome.Batches < PruneBatchBound {
		reclaim, err := projection.RebuildOccurrences(ctx, store.Journal(), store.auditDBHandle(), projection.RebuildOptions{
			Clock:   store.lifecycleStoreClock(),
			Window:  store.lifecycleTimeoutProfile().WorkflowResult(),
			Reclaim: policy,
		})
		if err != nil {
			return PruneOutcome{}, err
		}
		outcome.Batches++
		outcome.Reclaimed += int64(len(reclaim.Reclaimed))
		outcome.ReclaimedBytes += reclaim.ReclaimedBytes
		if reclaim.Failure != nil {
			fmt.Fprintln(store.lifecycleDiagnosticSink(), projection.ReclaimFailureLine(reclaim.Failure))
			outcome.Failure = reclaim.Failure
			break
		}
		if len(reclaim.Reclaimed) < policy.Cap {
			drained = true
			break
		}
		if options.MaxBytes > 0 && outcome.ReclaimedBytes >= options.MaxBytes {
			break
		}
	}
	// The run stopped early only when it neither drained the backlog nor hit a
	// failure it reports on its own.
	outcome.Truncated = !drained && outcome.Failure == nil
	if options.Vacuum {
		if _, err := store.auditDBHandle().ExecContext(ctx, `VACUUM`); err != nil {
			outcome.VacuumFailure = err
		} else {
			outcome.Vacuumed = true
		}
	}
	return outcome, nil
}
