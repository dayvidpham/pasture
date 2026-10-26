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

// assumedPerRecordCommitCost is the per-record commit cost the batch-record
// count is derived from.
//
// IT IS A CONSERVATIVE DESIGN CONSTANT, NOT A MEASUREMENT OF THIS TREE. Rule 1
// of the sibling repository's design-performance skill forbids presenting a
// guess as a measurement, so this is named for what it is. Phase 2 kept it as
// the splitter's per-record allowance rather than replacing it: a lane turn is
// now ONE ApplyBatchContext transaction, so what the count must still bound is
// the turn's hold on the writer. The pinned Provenance v0.9.0 release records a
// batched per-record fold cost of 11.64 ms (its own sandbox, serial p50), so 20
// records per turn is about 233 ms, inside HoldTarget (400 ms); the phase-2
// measurement in docs/audits/lifecycle-write-lane.md is the check that the
// bound is not the bottleneck. The constant stays at 20 ms so the derived count
// errors SMALL.
const assumedPerRecordCommitCost = 20 * time.Millisecond

// maxDerivedHoldTarget is the ceiling BudgetFromProfile places on the lane's
// hold target, whatever the busy tier is. The production SQLite busy tier is
// 500 ms and the derivation takes four fifths of it (400 ms); this constant
// keeps a profile with a LARGER busy tier from letting the lane hold the
// writer longer than the 400 ms the design sized against the other writers'
// wait. See PROPOSAL-19 section 3 (Bounds, splitter, backpressure, oversize).
const maxDerivedHoldTarget = 400 * time.Millisecond

// ingressPayloadFloor is the floor Validate places on BatchBytes: the one item
// a hook may send is bounded by the ingress payload cap, and a batch byte
// budget below one such item would make the byte term meaningless for the
// largest admissible record. The oversize term in ExceedsWriteBatch admits a
// record larger than the whole batch budget ALONE regardless, so this floor is
// a sanity bound on the knob, not a correctness condition.
const ingressPayloadFloor int64 = 1 << 20

// derivedBatchBytes is one lane turn's staged-payload bound.
//
// It is 64 MiB, sixty-four times the 1 MiB ingress payload cap. The previous
// 8 MiB start split a turn of maximum-size records after eight of them. The
// hold-derived BatchRecords count is at most 20 (HoldTarget is capped at
// 400 ms and each record is allowed 20 ms), so twenty maximum-size records
// are 20 MiB. 64 MiB sits above that, so a count-legal turn of admissible
// records is split by the hold-derived count, not by bytes. The byte term
// still keeps one turn from staging an unbounded aggregate. QueueBytes stays
// four times this value (256 MiB). HoldTarget, BatchRecords, FlushInterval,
// and QueueRecords are not derived from this constant.
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
	// BatchRecords bounds ONE lane turn's record count. The turn is what holds
	// the store writer, so this count times the per-record commit cost must
	// stay inside HoldTarget.
	BatchRecords int
	// BatchBytes bounds ONE lane turn's staged payload bytes on the same terms.
	BatchBytes int64
	// FlushInterval is the IDLE FALLBACK only: the longest a non-empty partial
	// batch waits for a companion record before it is committed anyway. It must
	// stay well below the hook-invocation tier, because every record in the
	// batch is still waiting for its acknowledgement.
	FlushInterval time.Duration
	// HoldTarget bounds how long ONE lane turn may hold the SQLite writer, so
	// the engine's own writes and every CLI writer keep their measured wait.
	// It is derived below the other writers' busy tier.
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
//     maxDerivedHoldTarget. The busy tier is the window every OTHER writer
//     waits for the lock, so a lane turn that stays inside four fifths of it
//     leaves scheduling headroom instead of becoming the holder that starves
//     them.
//   - BatchRecords is HoldTarget divided by assumedPerRecordCommitCost, floored
//     at one, so the turn's record count is sized against the hold bound rather
//     than picked.
//   - BatchBytes is derivedBatchBytes (64 MiB): sixty-four times the 1 MiB
//     ingress payload cap, above a count-legal turn of maximum-size records,
//     so the byte term does not split that turn before the hold-derived count.
//   - QueueRecords/QueueBytes are the staging bounds: 256 records and four
//     times BatchBytes (256 MiB), so the queue is deeper than one turn but
//     still bounded.
//   - FlushInterval is 25 ms, the design's stated start, and never more than a
//     quarter of HoldTarget. It is the ONLY wait a lone record can add to the
//     hook path, so it is bounded hard: it stays well below the hook-invocation
//     tier, and it is below a hold bound that is itself below the busy tier.
func BudgetFromProfile(profile timeouts.Profile) (WriteLaneBudget, error) {
	if err := profile.Validate(); err != nil {
		return WriteLaneBudget{}, fmt.Errorf("derive the write lane budget: the timeout profile is invalid, so no lane bound can be derived from it: %w", err)
	}
	hold := profile.SQLiteBusy() * 4 / 5
	if hold > maxDerivedHoldTarget {
		hold = maxDerivedHoldTarget
	}
	flush := 25 * time.Millisecond
	if quarter := hold / 4; quarter < flush {
		flush = quarter
	}
	if flush < time.Millisecond {
		flush = time.Millisecond
	}
	batchRecords := int(hold / assumedPerRecordCommitCost)
	if batchRecords < 1 {
		batchRecords = 1
	}
	budget := WriteLaneBudget{
		QueueRecords:  256,
		QueueBytes:    derivedBatchBytes * 4,
		BatchRecords:  batchRecords,
		BatchBytes:    derivedBatchBytes,
		FlushInterval: flush,
		HoldTarget:    hold,
	}
	if err := budget.Validate(); err != nil {
		return WriteLaneBudget{}, fmt.Errorf("derive the write lane budget from the timeout profile: %w", err)
	}
	return budget, nil
}
