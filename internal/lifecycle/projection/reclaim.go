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

// RebuildOptions carries what the rebuild's reclaim needs beyond the store:
// the clock that supplies the snapshot instant, and the longest window any
// writer may hold between its blob write and its journal append (the
// WorkflowResult tier of the injected profile). Both are required and both
// come from the store that owns the rebuild, never from a caller above it.
// There is no silent default for a bound: a zero window would reclaim
// in-flight blobs. The rebuild takes NO output stream; it RETURNS what the
// reclaim did, and the layer that owns a diagnostic stream prints the one
// failure line from that outcome.
type RebuildOptions struct {
	Clock  receipt.Clock
	Window time.Duration
}

func (o RebuildOptions) validate() error {
	switch {
	case o.Clock == nil:
		return projectionError("The reclaiming projection rebuild has no clock.", "The reclaim ages orphan payload blobs against the instant the journal was read, and that instant must come from the injected clock.", "No projection rows were changed and nothing was reclaimed.", "Pass the process clock in RebuildOptions.Clock.", nil)
	case o.Window <= 0:
		return projectionError("The reclaiming projection rebuild has no writer window.", "An orphan payload blob is reclaimed only when it is older than the longest window a writer may hold between its blob write and its journal append; a zero window would reclaim a blob whose append is still in flight.", "No projection rows were changed and nothing was reclaimed.", "Pass the WorkflowResult tier of the injected timeout profile in RebuildOptions.Window.", nil)
	}
	return nil
}

// ReclaimOutcome is what the reclaim inside one rebuild did: the digests it
// deleted, and the failure it reported if it could not run. A failure never
// fails the rebuild; the rebuild returns it here and the caller that owns a
// diagnostic stream prints ReclaimFailureLine once.
type ReclaimOutcome struct {
	Reclaimed []digest.Digest
	Failure   error
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
// It runs under a savepoint so that a failure of the delete rolls back the
// reclaim alone and the rebuild still commits. The second result reports that
// failure; the third is true only when the savepoint machinery itself failed,
// after which the transaction's state is not known and the caller must not
// commit.
func reclaimOrphanPayloads(ctx context.Context, tx *sql.Tx, snapshot time.Time, window time.Duration) ([]digest.Digest, error, bool) {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT reclaim_orphans`); err != nil {
		return nil, fmt.Errorf("%w: begin: %w", errReclaimSavepoint, err), true
	}
	before := snapshot.Add(-window).UnixNano()
	deleted, err := receipt.PayloadReclaimer{Tx: tx}.ReclaimOrphansWrittenBefore(ctx, before, ReclaimCap)
	if err != nil {
		if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT reclaim_orphans`); rollbackErr != nil {
			return nil, fmt.Errorf("%w: roll back after %v: %w", errReclaimSavepoint, err, rollbackErr), true
		}
		if _, releaseErr := tx.ExecContext(ctx, `RELEASE SAVEPOINT reclaim_orphans`); releaseErr != nil {
			return nil, fmt.Errorf("%w: release after roll back: %w", errReclaimSavepoint, releaseErr), true
		}
		return nil, err, false
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT reclaim_orphans`); err != nil {
		return nil, fmt.Errorf("%w: release: %w", errReclaimSavepoint, err), true
	}
	return deleted, nil, false
}
