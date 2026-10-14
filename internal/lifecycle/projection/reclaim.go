package projection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	digest "github.com/opencontainers/go-digest"

	"github.com/dayvidpham/pasture/internal/lifecycle/receipt"
)

// ReclaimCap bounds how many orphan payload blobs one projection rebuild
// reclaims. It is 1024 because the rebuild runs on a READ command and not
// under the hook-invocation tier: the bound sizes a read command's added
// latency, not a host budget. At 1024 the 14,490 legacy orphans measured on a
// live store drain in about fifteen read commands. It is a constant and not
// configuration on purpose: the reclaim is a safety valve for a state the
// write order creates deliberately, not a retention policy.
const ReclaimCap = 1024

// PruneCap bounds how many orphan payload blobs ONE BATCH of the operator
// prune deletes. The prune verb runs batches so that each batch is its own
// short write transaction: a store that is being written while the prune runs
// keeps its hook writers moving between batches. The number is a row count and
// not a latency claim, because the latency of a delete depends on the store's
// page shape; it is sized well under the read path's fifteen-command drain of
// the measured legacy backlog so one batch stays a small hold on the write
// lock, and the verb loops until the backlog is gone or the operator's budget
// is met.
const PruneCap = 4096

// ReclaimPolicy bounds the orphan payload blobs one rebuild may delete. Its
// zero value is the READ-COMMAND policy: delete up to ReclaimCap orphans older
// than the writer window, and nothing else. The operator prune verb is the one
// caller that sets it, and each field is a deliberate widening of the read
// path's bound:
//
//   - MaxAge is the operator's RETENTION age. An orphan is eligible only when
//     it is older than max(Window, MaxAge), so a value below the writer window
//     cannot shorten the in-flight safety floor; zero leaves the floor as the
//     only age bound, which is the read path's behavior.
//   - Cap bounds the rows this rebuild deletes. Zero uses ReclaimCap.
//   - KeepNewest refuses to delete the row at the greatest written_at. The
//     read path does not need it: a read command is bounded small, runs
//     unattended, and its reclaim is a safety valve. The operator verb asks
//     for a bulk delete, so it states the one exclusion an operator would
//     assume.
type ReclaimPolicy struct {
	MaxAge     time.Duration
	Cap        int
	KeepNewest bool
}

func (p ReclaimPolicy) validate() error {
	switch {
	case p.MaxAge < 0:
		return projectionError("The reclaim retention age is negative.", "A retention age is a duration forward from the snapshot instant; a negative one would read as a bound in the future and select every orphan.", "No projection rows were changed and nothing was reclaimed.", "Pass a retention age of zero or more.", nil)
	case p.Cap < 0:
		return projectionError("The reclaim row budget is negative.", "A row budget is a count; a negative one has no meaning and would select nothing.", "No projection rows were changed and nothing was reclaimed.", "Pass a row budget of zero (the read-command cap) or more.", nil)
	}
	return nil
}

// RebuildOptions carries what the rebuild's reclaim needs beyond the store:
// the clock that supplies the snapshot instant, the longest window any writer
// may hold between its blob write and its journal append (the WorkflowResult
// tier of the injected profile), and the retention policy that widens the
// reclaim for the operator prune verb. All are required and all come from the
// store that owns the rebuild, never from a caller above it. There is no
// silent default for a bound: a zero window would reclaim in-flight blobs. The
// rebuild takes NO output stream; it RETURNS what the reclaim did, and the
// layer that owns a diagnostic stream prints the one failure line from that
// outcome.
type RebuildOptions struct {
	Clock   receipt.Clock
	Window  time.Duration
	Reclaim ReclaimPolicy
}

func (o RebuildOptions) validate() error {
	switch {
	case o.Clock == nil:
		return projectionError("The reclaiming projection rebuild has no clock.", "The reclaim ages orphan payload blobs against the instant the journal was read, and that instant must come from the injected clock.", "No projection rows were changed and nothing was reclaimed.", "Pass the process clock in RebuildOptions.Clock.", nil)
	case o.Window <= 0:
		return projectionError("The reclaiming projection rebuild has no writer window.", "An orphan payload blob is reclaimed only when it is older than the longest window a writer may hold between its blob write and its journal append; a zero window would reclaim a blob whose append is still in flight.", "No projection rows were changed and nothing was reclaimed.", "Pass the WorkflowResult tier of the injected timeout profile in RebuildOptions.Window.", nil)
	case o.Reclaim.validate() != nil:
		return o.Reclaim.validate()
	}
	return nil
}

// reclaimAge is the age bound one reclaim reads: the writer window, or the
// operator's retention age when it is larger. The writer window is a FLOOR and
// never a ceiling, so a retention age below it cannot shorten the in-flight
// safety margin.
func (p ReclaimPolicy) reclaimAge(window time.Duration) time.Duration {
	if p.MaxAge > window {
		return p.MaxAge
	}
	return window
}

// reclaimCap is the row budget one reclaim reads: the caller's cap, or the
// read path's ReclaimCap when none is given.
func (p ReclaimPolicy) reclaimCap() int {
	if p.Cap > 0 {
		return p.Cap
	}
	return ReclaimCap
}

// ReclaimOutcome is what the reclaim inside one rebuild did: the digests it
// deleted, how many bytes those rows held, and the failure it reported if it
// could not run. A failure never fails the rebuild; the rebuild returns it here
// and the caller that owns a diagnostic stream prints ReclaimFailureLine once.
type ReclaimOutcome struct {
	Reclaimed      []digest.Digest
	ReclaimedBytes int64
	Failure        error
}

// Count is how many payload blobs this rebuild reclaimed.
func (o ReclaimOutcome) Count() int { return len(o.Reclaimed) }

// reclaimFailurePrefix and reclaimFailureSuffix frame the ONE line a failed
// reclaim writes. The suffix is operator text: it says where it happened, that
// the rebuild still completed, that nothing was deleted, and what happens next.
const (
	reclaimFailurePrefix = "pasture: the orphan payload reclaim inside the projection rebuild failed: "
	reclaimFailureSuffix = "; this happened in the projection rebuild (internal/lifecycle/projection/reclaim.go) after the projection rows were rebuilt; " +
		"the rebuild still completed and nothing was deleted; the orphan payload blobs stay where they were, and the next read command retries the reclaim"
)

// ReclaimFailureLine renders the ONE diagnostic line a failed reclaim earns.
// The rebuild does not write it; the store that ran the rebuild does, on the
// diagnostic sink it was constructed with, so a test can pin the phrases
// against the text the operator reads.
func ReclaimFailureLine(failure error) string {
	return reclaimFailurePrefix + failure.Error() + reclaimFailureSuffix
}

// errReclaimSavepoint marks a failure of the savepoint machinery itself, after
// which the transaction's state is not known and the rebuild must not commit.
var errReclaimSavepoint = errors.New("the orphan reclaim savepoint could not be managed")

// reclaimOrphanPayloads is the ONE reclaim of orphan payload blobs, and it is
// called exactly once, from the rebuild, inside the rebuild's write
// transaction AFTER the projection rows have been re-inserted. That placement
// is the whole safety argument: the projection the eligibility reads is the
// one just rebuilt from the journal in this same transaction, so "no
// occurrence names this digest" is true of the journal as of the snapshot.
//
// The age reference is the SNAPSHOT INSTANT, captured before the journal was
// read, never the delete instant. A journal row committed after the snapshot
// is not in the projection, but its blob was written no earlier than that
// commit minus the writer's bounded window, so relative to the snapshot it is
// younger than the window and cannot be selected; a row committed at or before
// the snapshot is in the projection and its blob is named. Both halves are
// needed, and both are pinned by tests.
//
// A blob whose written_at is 0 has an UNKNOWN age and is never selected,
// whatever the snapshot instant: the migration that added the column stamps
// every pre-existing row with the migration instant, so a 0 after the upgrade
// can only come from a build that predates the column writing to an upgraded
// store, and an unknown age must never satisfy an age bound. That rule lives
// in the one statement (receipt.PayloadReclaimer.ReclaimOrphansWrittenBefore)
// and the trade it makes is stated there.
//
// TWO CLOCKS. The age this reclaim computes reads the INJECTED clocks only:
// the blob store's clock at the Put (the stamp) and the rebuild's clock at the
// snapshot (RebuildOptions.Clock). The writer-window refusal in
// receipt.Service.Receive reads the WALL clock, because it judges a context
// deadline. In production all of these are the wall clock, so the window the
// refusal enforces is the window this reclaim ages against; a test that
// scripts one injected clock must script the other.
//
// It runs under a savepoint so that a failure of the delete rolls back the
// reclaim alone and the rebuild still commits. The second result reports that
// failure; the third is true only when the savepoint machinery itself failed,
// after which the transaction's state is not known and the caller must not
// commit.
// The policy widens the age bound and the row budget and may add the
// newest-row exclusion; the zero policy is the read path's behavior exactly,
// and the statement it selects is the read path's statement unchanged.
func reclaimOrphanPayloads(ctx context.Context, tx *sql.Tx, snapshot time.Time, window time.Duration, policy ReclaimPolicy) (reclaimResult, error, bool) {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT reclaim_orphans`); err != nil {
		return reclaimResult{}, fmt.Errorf("%w: begin: %w", errReclaimSavepoint, err), true
	}
	before := snapshot.Add(-policy.reclaimAge(window)).UnixNano()
	reclaimer := receipt.PayloadReclaimer{Tx: tx}
	var deleted []digest.Digest
	var bytes int64
	var err error
	if policy.KeepNewest {
		deleted, bytes, err = reclaimer.PruneOrphansWrittenBefore(ctx, before, policy.reclaimCap())
	} else {
		deleted, bytes, err = reclaimer.ReclaimOrphansWrittenBefore(ctx, before, policy.reclaimCap())
	}
	if err != nil {
		if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT reclaim_orphans`); rollbackErr != nil {
			return reclaimResult{}, fmt.Errorf("%w: roll back after %v: %w", errReclaimSavepoint, err, rollbackErr), true
		}
		if _, releaseErr := tx.ExecContext(ctx, `RELEASE SAVEPOINT reclaim_orphans`); releaseErr != nil {
			return reclaimResult{}, fmt.Errorf("%w: release after roll back: %w", errReclaimSavepoint, releaseErr), true
		}
		return reclaimResult{}, err, false
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT reclaim_orphans`); err != nil {
		return reclaimResult{}, fmt.Errorf("%w: release: %w", errReclaimSavepoint, err), true
	}
	return reclaimResult{deleted: deleted, bytes: bytes}, nil, false
}

// reclaimResult is what one reclaim call deleted: the digests and the bytes
// their bodies held. It exists so the byte total travels with the row list from
// the delete, where it is summed from the RETURNING clause, rather than being
// reconstructed from a before/after table total a concurrent writer could
// distort.
type reclaimResult struct {
	deleted []digest.Digest
	bytes   int64
}
