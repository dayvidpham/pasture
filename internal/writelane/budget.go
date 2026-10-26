// Package writelane owns the daemon's single serialized lifecycle write lane:
// the ONE budget splitter (WriteLaneBudget + ExceedsWriteBatch) and the ONE
// goroutine that commits admitted records.
//
// The lane exists because the contended resource is SQLite's single writer
// slot, not throughput: every hook process opened pasture.db and asked for a
// write lock at least twice per event, so a convoy of short-lived writers
// starved one another and each hook reported a fault it could not interpret.
// Putting the lane in front of the store serializes those writes by
// construction and bounds how long any one turn holds the writer, so the
// engine's and the CLI's own writers are never starved past their busy window.
//
// ONE SPLITTER. Every place that groups records into a batch asks
// ExceedsWriteBatch; no site re-derives a count or byte bound. The sibling
// repository's ingest lane (internal/ingest/pipeline.go ExceedsWriteBudget) is
// the pattern this package adapts, and the reason for a single splitter is the
// same one stated there: two sites spelling out the same terms drift.
package writelane

import (
	"fmt"
	"time"

	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
)

// maxDerivedHoldTarget is the ceiling BudgetFromProfile places on the lane's
// hold target, whatever the busy tier is. The production profile is SQLiteBusy
// 1.5s; four fifths of that is 1.2s, and this constant caps the hold at 1s.
// 1s remains below the busy tier the other writers wait on, and below the 2s
// ingress and start-slice windows. A turn that outruns the cap is reported as
// HoldExceeded; the lane still commits every admitted record.
const maxDerivedHoldTarget = time.Second

// derivedQueueRecords is the number of admitted-but-uncommitted records the
// lane may stage. It is a fixed staging depth, deeper than one turn, not a
// time derivation.
const derivedQueueRecords = 1024

// productionFlushInterval is the production profile's idle fallback. It is
// 500ms, strictly below the 1s hold cap. Non-production profiles do not use
// it: their holds are shorter than 500ms, and Validate requires FlushInterval
// to be strictly below HoldTarget.
const productionFlushInterval = 500 * time.Millisecond

// ingressPayloadFloor is the floor Validate places on BatchBytes: the one item
// a hook may send is bounded by the ingress payload cap, and a batch byte
// budget below one such item would make the byte term meaningless for the
// largest admissible record. The oversize term in ExceedsWriteBatch admits a
// record larger than the whole batch budget ALONE regardless, so this floor is
// a sanity bound on the knob, not a correctness condition.
const ingressPayloadFloor int64 = 1 << 20

// derivedBatchBytes is one lane turn's staged-payload bound.
//
// It is 64 MiB, sixty-four times the 1 MiB ingress payload cap, unchanged
// from the byte-budget raise. It is not sized from the record count: the
// record cap is Provenance's batch cap (256), and 256 maximum-size records
// are 256 MiB, which is above this bound. A turn of maximum-size records is
// therefore split by bytes before the count cap. The byte term still keeps
// one turn from staging an unbounded aggregate. QueueBytes stays four times
// this value (256 MiB). HoldTarget, BatchRecords, FlushInterval, and
// QueueRecords are not derived from this constant.
const derivedBatchBytes int64 = 64 << 20

// WriteLaneBudget is every bound the lane enforces, in one value. It is
// constructor-validated: the daemon resolves it once from the timeout profile
// (BudgetFromProfile) and an operator may override named knobs, but the value
// the lane runs on always passes Validate, so an inverted combination is
// refused with a named error rather than clamped.
type WriteLaneBudget struct {
	// QueueRecords bounds the number of admitted-but-uncommitted records. When
	// the queue is at this bound a new request BLOCKS in the lane until a
	// commit frees room; the connection handler is what blocks, so a further
	// request is not read from the socket (natural backpressure). It is a
	// bound on staged memory and an admission brake, never a refusal path: an
	// invocation waiting here is abandoned by its own hook-invocation deadline
	// exactly as a slow direct write is today.
	QueueRecords int
	// QueueBytes bounds the staged payload bytes across the whole queue, the
	// byte half of the same brake.
	QueueBytes int64
	// BatchRecords bounds ONE lane turn's record count. One turn is one
	// Provenance batch, so the production value is Provenance's batch cap and
	// Validate refuses anything above it. The count is not a per-record time
	// budget: a full turn is not claimed to finish inside HoldTarget.
	// HoldExceeded reports a turn that outran the hold; the lane still commits
	// every admitted record and never drops one.
	BatchRecords int
	// BatchBytes bounds ONE lane turn's staged payload bytes on the same terms.
	BatchBytes int64
	// FlushInterval is the IDLE FALLBACK only: the longest a non-empty partial
	// batch waits for a companion record before it is committed anyway. It must
	// stay well below the hook-invocation tier, because every record in the
	// batch is still waiting for its acknowledgement.
	FlushInterval time.Duration
	// HoldTarget is the observed bound on how long ONE lane turn may hold the
	// SQLite writer, so the engine's own writes and every CLI writer keep
	// their measured wait. It is derived below the other writers' busy tier.
	// Exceeding it increments HoldExceeded. It is not enforced by dropping an
	// admitted record.
	HoldTarget time.Duration
}

// ExceedsWriteBatch reports whether the pending batch must be flushed BEFORE
// the next record joins it. It is the ONE splitter: the single home of the
// count and byte terms.
//
// A record larger than the whole batch budget is still admitted, alone: the
// batch it would join is flushed first and the empty-batch term then admits
// it. Refusing it instead would lose an event that happened.
func ExceedsWriteBatch(b WriteLaneBudget, pendingRecords int, pendingBytes, nextBytes int64) bool {
	if pendingRecords >= b.BatchRecords {
		return true
	}
	return pendingRecords > 0 && pendingBytes+nextBytes > b.BatchBytes
}

// Validate refuses an unusable budget with an error naming the knob and the
// relationship that failed, so an operator who set one via the environment is
// told which one and what it collides with. It never clamps.
func (b WriteLaneBudget) Validate() error {
	switch {
	case b.QueueRecords <= 0:
		return fmt.Errorf("write lane budget QueueRecords is %d; it must be positive, because it is the number of admitted-but-uncommitted records the lane may stage before it stops reading new requests", b.QueueRecords)
	case b.QueueBytes <= 0:
		return fmt.Errorf("write lane budget QueueBytes is %d; it must be positive, because it is the staged payload memory the lane may hold before it stops reading new requests", b.QueueBytes)
	case b.BatchRecords <= 0:
		return fmt.Errorf("write lane budget BatchRecords is %d; it must be positive, because it is the record count of one lane turn", b.BatchRecords)
	case b.BatchRecords > b.QueueRecords:
		return fmt.Errorf("write lane budget BatchRecords is %d and QueueRecords is %d; one lane turn cannot take more records than the queue can stage, so BatchRecords must not exceed QueueRecords", b.BatchRecords, b.QueueRecords)
	case b.BatchRecords > provenance.MaxBatchRecords:
		return fmt.Errorf("write lane budget BatchRecords is %d, above the Provenance batch cap of %d; one lane turn commits its whole batch in ONE transaction, and Provenance refuses a batch larger than %d, so a turn that could exceed it would fail every record in it: lower BatchRecords to at most %d", b.BatchRecords, provenance.MaxBatchRecords, provenance.MaxBatchRecords, provenance.MaxBatchRecords)
	case b.BatchBytes <= 0:
		return fmt.Errorf("write lane budget BatchBytes is %d; it must be positive, because it is the staged payload bytes of one lane turn", b.BatchBytes)
	case b.BatchBytes > b.QueueBytes:
		return fmt.Errorf("write lane budget BatchBytes is %d and QueueBytes is %d; one lane turn cannot hold more bytes than the queue can stage, so BatchBytes must not exceed QueueBytes", b.BatchBytes, b.QueueBytes)
	case b.BatchBytes < ingressPayloadFloor:
		return fmt.Errorf("write lane budget BatchBytes is %d, below the %d-byte ingress payload cap; a batch smaller than one admissible record makes the byte term meaningless for the largest record a hook may send", b.BatchBytes, ingressPayloadFloor)
	case b.FlushInterval <= 0:
		return fmt.Errorf("write lane budget FlushInterval is %s; it must be positive, because it is the idle wait after which a partial batch is committed", b.FlushInterval)
	case b.HoldTarget <= 0:
		return fmt.Errorf("write lane budget HoldTarget is %s; it must be positive, because it bounds how long one lane turn may hold the SQLite writer", b.HoldTarget)
	case b.FlushInterval >= b.HoldTarget:
		return fmt.Errorf("write lane budget is inverted: FlushInterval %s must be strictly below HoldTarget %s, because a partial batch that waited out its hold bound would already have starved the other writers the bound exists to protect", b.FlushInterval, b.HoldTarget)
	}
	return nil
}

// BudgetFromProfile derives the lane budget from one validated timeout profile:
// the profile is the single home of the tiers, so the lane joins it rather than
// carrying a second, independent set of durations.
//
// The derivation, and why each term is what it is:
//
//   - HoldTarget is four fifths of the profile's SQLite busy tier, capped by
//     maxDerivedHoldTarget (1s). The busy tier is the window every OTHER writer
//     waits for the lock, so a lane turn that stays inside four fifths of it
//     leaves scheduling headroom instead of becoming the holder that starves
//     them. Four fifths of the production 1.5s busy tier is 1.2s, so the cap
//     holds production at 1s. 1s remains below that busy tier and below the 2s
//     ingress and start-slice windows. A turn that outruns HoldTarget is
//     reported as HoldExceeded; the lane still commits every admitted record
//     and never drops one to meet the bound.
//   - BatchRecords is provenance.MaxBatchRecords (256), a fixed cap. It is not
//     derived from a per-record commit cost, and a full turn is not claimed to
//     finish inside HoldTarget. The real bounds on one turn are this cap, the
//     queue count, and the byte caps.
//   - BatchBytes is derivedBatchBytes (64 MiB). QueueBytes is four times that
//     (256 MiB). A turn of maximum-size records is split by the byte cap before
//     the record cap, because 256 such records are 256 MiB.
//   - QueueRecords is derivedQueueRecords (1024): a fixed staging depth, deeper
//     than one turn, still bounded.
//   - FlushInterval on the production profile is productionFlushInterval
//     (500ms), strictly below the 1s hold. It is the ONLY wait a lone record
//     can add to the hook path. Non-production profiles keep the scaled
//     fallback — 25ms, never more than a quarter of HoldTarget, never below
//     1ms — because a fixed 500ms flush would not be strictly below those
//     shorter holds, and Validate would refuse the budget.
func BudgetFromProfile(profile timeouts.Profile) (WriteLaneBudget, error) {
	if err := profile.Validate(); err != nil {
		return WriteLaneBudget{}, fmt.Errorf("derive the write lane budget: the timeout profile is invalid, so no lane bound can be derived from it: %w", err)
	}
	hold := profile.SQLiteBusy() * 4 / 5
	if hold > maxDerivedHoldTarget {
		hold = maxDerivedHoldTarget
	}
	// Production is the only profile whose hold (1s) can contain a 500ms idle
	// fallback. TestProfile's hold is 400ms and DeadlineTestProfile's is 20ms;
	// applying the production flush there would invert FlushInterval and
	// HoldTarget. Those profiles keep the scaled fallback.
	flush := 25 * time.Millisecond
	if profile.Kind() == timeouts.Production {
		flush = productionFlushInterval
	} else {
		if quarter := hold / 4; quarter < flush {
			flush = quarter
		}
		if flush < time.Millisecond {
			flush = time.Millisecond
		}
	}
	budget := WriteLaneBudget{
		QueueRecords:  derivedQueueRecords,
		QueueBytes:    derivedBatchBytes * 4,
		BatchRecords:  provenance.MaxBatchRecords,
		BatchBytes:    derivedBatchBytes,
		FlushInterval: flush,
		HoldTarget:    hold,
	}
	if err := budget.Validate(); err != nil {
		return WriteLaneBudget{}, fmt.Errorf("derive the write lane budget from the timeout profile: %w", err)
	}
	return budget, nil
}
