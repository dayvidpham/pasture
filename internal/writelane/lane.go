package writelane

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dayvidpham/pasture/internal/writeprofile"
	"github.com/dayvidpham/provenance"
)

// ErrLaneClosed is returned by Submit once the lane has been closed: no record
// was admitted, so the caller may honestly report that nothing was committed
// for it.
var ErrLaneClosed = errors.New("the lifecycle write lane is closed")

// ErrNotAdmitted marks a Submit that staged NOTHING: the lane was closed, the
// caller's own context ended while the queue was at its bound, or the request
// itself was malformed. It is the evidence for a provably not-recorded claim,
// exactly as ErrLifecycleBeforeDurableWrite is on the direct path, and the
// served handler converts it into that sentinel. An error from the COMMIT is
// never wrapped in it.
var ErrNotAdmitted = errors.New("the lifecycle write lane did not admit the record")

// Record is one admitted record's durable work, expressed in two phases so the
// lane can commit a whole flush in ONE provenance transaction:
//
//   - Prepare runs everything that must happen BEFORE the shared write
//     transaction — the payload blob write, identity resolution, and
//     canonicalization — and returns the record's canonical journal operation.
//     A Prepare error is this record's own: it contributes no operation to the
//     batch and the lane reports that error to its caller. Anything durable it
//     left behind is a reclaimable payload blob, never a journal row.
//   - Settle reports this record's own outcome to its caller and runs any work
//     that must follow the shared commit. It is called exactly once for every
//     record whose Prepare succeeded, with that record's own BatchRecordResult,
//     whatever the batch did.
//
// The context both phases receive is the LANE's, not the submitter's: a record
// a hook abandoned while it waited in the queue is still committed (evidence
// over silence), so neither phase may be cancelled by the hook's own deadline.
type Record interface {
	Prepare(ctx context.Context) (provenance.OperationInput, error)
	Settle(ctx context.Context, outcome provenance.BatchRecordResult) error
}

// BatchApply commits a flush's prepared operations in ONE transaction and
// returns one outcome per input, aligned by index. It is the lane's whole reason
// for phase 2: the per-record lane ran one transaction per record, and the
// pinned Provenance BatchJournal folds the batch through the same per-operation
// reducer step in a single BEGIN IMMEDIATE.
type BatchApply func(ctx context.Context, inputs []provenance.OperationInput) (provenance.BatchResult, error)

// item is one admitted-but-uncommitted record. Its identity is deliberately
// opaque to the lane: the lane moves bytes and calls a Record, and never knows
// what a record means.
type item struct {
	queueDone              func()
	admissionToPrepareDone func()
	batchPrepareWaitDone   func()
	bytes                  int64
	record                 Record
	result                 chan error
}

// Stats is a read-only snapshot of what the lane has done. It exists so an
// operator, a test and the startup log line can read the same numbers without
// reaching into the lane's state.
type Stats struct {
	Records         int64
	Batches         int64
	MaxBatchRecords int64
	MaxHold         time.Duration
	QueueDepth      int64
	HoldExceeded    int64
}

// Lane is the ONE serialized writer for lifecycle records: a bounded staging
// queue of admitted records, one goroutine that commits them in bounded
// batches, and per-record completion so a caller learns whether ITS record
// committed.
//
// THE CALLER LEARNS NOTHING BEFORE THE COMMIT. Submit returns only after the
// record's Settle has run and reported its result, so there is no path on which
// a hook is told "recorded" for a record that has not committed. That is the
// property the whole daemon-fronted lane exists to preserve, and it is a
// property of Submit's shape rather than of any discipline at the call site.
type Lane struct {
	budget WriteLaneBudget
	apply  BatchApply
	ctx    context.Context

	mu           sync.Mutex
	cond         *sync.Cond
	pending      []*item
	pendingBytes int64
	closed       bool
	// arrived is the accumulation signal: a channel that is CLOSED AND
	// REPLACED under the mutex by every Submit that stages a record. A commit
	// loop captures the current channel under the same lock it takes its first
	// record in, so a signal can never be attributed to a record that was
	// already taken, and no stale signal can flush a batch early.
	arrived chan struct{}

	done chan struct{}

	records                atomic.Int64
	batches                atomic.Int64
	maxBatchRecords        atomic.Int64
	holdExceeded           atomic.Int64
	maxHoldNanos           atomic.Int64
	experimentQueueRecords atomic.Int64
	experimentQueueBytes   atomic.Int64
	experimentMetricErrors atomic.Int64
}

// New constructs the lane and starts its commit goroutine. The budget is
// validated here, so a lane that exists is a lane whose bounds hold; the batch
// applier is required, because a lane with no way to commit a flush would admit
// records it could never make durable.
func New(budget WriteLaneBudget, apply BatchApply) (*Lane, error) {
	if err := budget.Validate(); err != nil {
		return nil, fmt.Errorf("construct the lifecycle write lane: %w", err)
	}
	if apply == nil {
		return nil, fmt.Errorf("construct the lifecycle write lane: no batch applier was supplied, so an admitted record could never be committed")
	}
	lane := &Lane{
		budget:  budget,
		apply:   apply,
		ctx:     context.Background(),
		arrived: make(chan struct{}),
		done:    make(chan struct{}),
	}
	lane.cond = sync.NewCond(&lane.mu)
	go lane.run()
	return lane, nil
}

// Budget returns the validated budget this lane runs on.
func (l *Lane) Budget() WriteLaneBudget { return l.budget }

// Submit admits one record and blocks until it has committed or failed.
//
// ADMISSION BLOCKS RATHER THAN REFUSING. When the queue is at its record or
// byte bound the caller waits for room inside its own context; the connection
// handler is what waits, so no further request is read from the socket and the
// pressure is passed back to the hook, which abandons at its own
// hook-invocation deadline exactly as it does against a slow direct write. No
// new refusal path is introduced.
//
// A record larger than the whole batch byte budget is admitted when the queue
// is empty and committed ALONE; refusing it would lose an event that happened.
func (l *Lane) Submit(ctx context.Context, bytes int64, record Record) error {
	defer writeprofile.Region("lane.admission.through.response")()
	if l == nil {
		return fmt.Errorf("%w: the lane is nil, so no record could be admitted and nothing was committed", ErrNotAdmitted)
	}
	if record == nil {
		return fmt.Errorf("%w: no record work was supplied, so nothing was committed", ErrNotAdmitted)
	}
	if bytes < 0 {
		return fmt.Errorf("%w: the record states %d bytes, and a negative size cannot be staged", ErrNotAdmitted, bytes)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	me := &item{bytes: bytes, record: record, result: make(chan error, 1)}

	// Unblock a waiter promptly when its context ends. Without this the
	// admission wait would only be revisited when a commit freed room, so an
	// abandoned hook would sit here until the next record committed.
	stop := context.AfterFunc(ctx, func() {
		l.mu.Lock()
		l.cond.Broadcast()
		l.mu.Unlock()
	})
	defer stop()

	l.mu.Lock()
	for {
		if l.closed {
			l.mu.Unlock()
			return fmt.Errorf("%w: %w", ErrNotAdmitted, ErrLaneClosed)
		}
		if l.roomForLocked(bytes) {
			break
		}
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return fmt.Errorf("%w: the queue stayed at its bound (%d records / %d bytes staged) until this invocation's own context ended, so the record was never staged and nothing was committed for it: %w", ErrNotAdmitted, l.budget.QueueRecords, l.budget.QueueBytes, err)
		}
		l.cond.Wait()
	}
	me.queueDone = writeprofile.Timer("lane.queue.accumulation.wait")
	me.admissionToPrepareDone = writeprofile.Timer("lane.admission.to_own_prepare")
	l.pending = append(l.pending, me)
	l.pendingBytes += bytes
	l.observeExperimentQueue()
	close(l.arrived)
	l.arrived = make(chan struct{})
	l.cond.Broadcast()
	l.mu.Unlock()

	return <-me.result
}

// roomForLocked reports whether one more record of size bytes may be staged
// without exceeding the queue bounds. An oversize record is staged when the
// queue is empty, so it always has a path through.
func (l *Lane) roomForLocked(bytes int64) bool {
	if len(l.pending) >= l.budget.QueueRecords {
		return false
	}
	if len(l.pending) > 0 && l.pendingBytes+bytes > l.budget.QueueBytes {
		return false
	}
	return true
}

// Close stops admission and drains what is already staged inside the ctx
// budget, then stops the commit goroutine. A drain that does not finish in
// time returns the ctx error; the records still staged are lost, which is why
// the daemon reports every hook that was still waiting as record-unknown
// rather than recorded.
func (l *Lane) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	l.closed = true
	close(l.arrived)
	l.arrived = make(chan struct{})
	l.cond.Broadcast()
	l.mu.Unlock()

	select {
	case <-l.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("the lifecycle write lane did not drain its staged records inside %s; the connection server is stopping with records still uncommitted, and each hook they belong to is reported record-unknown rather than recorded: %w", l.budget.HoldTarget, ctx.Err())
	}
}

// Stats reports a snapshot of the lane's counters.
func (l *Lane) Stats() Stats {
	if l == nil {
		return Stats{}
	}
	l.mu.Lock()
	depth := int64(len(l.pending))
	l.mu.Unlock()
	return Stats{
		Records:         l.records.Load(),
		Batches:         l.batches.Load(),
		MaxBatchRecords: l.maxBatchRecords.Load(),
		MaxHold:         time.Duration(l.maxHoldNanos.Load()),
		QueueDepth:      depth,
		HoldExceeded:    l.holdExceeded.Load(),
	}
}

// run is the single commit goroutine.
//
// THE FLUSH POLICY HAS EXACTLY THREE TRIGGERS, and they are the design's:
//
//   - the batch is FULL (ExceedsWriteBatch), or
//   - the queue DRAINS (an absorption pass left nothing staged), or
//   - the interval elapses on a partial batch.
//
// The first two commit at once; the third is the IDLE FALLBACK that bounds how
// long a partial batch — including a lone record, which has no companion to
// wait for — may hold the store before it is committed anyway. A turn's records
// share ONE transaction and one sequential prepare/apply/settle pass, so each
// result channel is answered as soon as that record's own report is produced.
func (l *Lane) run() {
	defer close(l.done)
	for {
		batch, arrival, staged, ok := l.takeFirst()
		if !ok {
			return
		}
		bytes := int64(0)
		for _, it := range batch {
			bytes += it.bytes
		}

		if staged {
			var drained, stopped bool
			drained, stopped, arrival = l.absorb(&batch, &bytes)
			if drained || stopped || ExceedsWriteBatch(l.budget, len(batch), bytes, 0) {
				reason := "full"
				if stopped {
					reason = "stopped"
				} else if drained {
					reason = "drained"
				}
				l.flushBatch(batch, reason)
				continue
			}
		}

		reason := "timer"
		timer := time.NewTimer(l.budget.FlushInterval)
	waiting:
		for {
			select {
			case <-arrival:
				var drained, stopped bool
				drained, stopped, arrival = l.absorb(&batch, &bytes)
				if drained || stopped {
					if stopped {
						reason = "stopped"
					} else {
						reason = "drained"
					}
					break waiting
				}
				// The record that arrived does not fit this batch's budget. Keep
				// waiting for the idle fallback; it is carried by the next batch.
			case <-timer.C:
				break waiting
			}
			if ExceedsWriteBatch(l.budget, len(batch), bytes, 0) {
				reason = "full"
				break waiting
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		l.flushBatch(batch, reason)
	}
}

// takeFirst blocks until at least one record is staged, then removes exactly
// one record, reporting whether MORE records were staged at that instant and
// the arrival signal CURRENT at that moment.
//
// The signal has to be captured under the same lock that removes the record:
// capturing it later would attribute this record's own admission to the
// accumulation wait, flushing every lone record immediately and making the
// interval fallback unreachable.
//
// staged=true means an absorption pass should run at once, so a burst that
// arrived while the previous batch committed is coalesced rather than committed
// one record per turn.
//
// ok=false means the lane is closed with nothing staged, which ends the commit
// goroutine. A closed lane with records still staged keeps committing them, so
// a shutdown inside the drain budget leaves no admitted record uncommitted.
func (l *Lane) takeFirst() (first []*item, arrival <-chan struct{}, staged, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for len(l.pending) == 0 && !l.closed {
		l.cond.Wait()
	}
	if len(l.pending) == 0 {
		return nil, nil, false, false
	}
	one := l.pending[0]
	l.pending = l.pending[1:]
	l.pendingBytes -= one.bytes
	arrival = l.arrived
	staged = len(l.pending) > 0
	l.cond.Broadcast()
	return []*item{one}, arrival, staged, true
}

// absorb takes every staged record the batch budget still admits, up to the
// first record that would exceed it. It reports whether the queue is now empty
// (drained), whether the lane has been closed (stopped), and the arrival signal
// as of the end of the pass, read under the same lock, so a record staged
// after the pass still wakes the waiter.
func (l *Lane) absorb(batch *[]*item, bytes *int64) (drained, stopped bool, arrival <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for len(l.pending) > 0 {
		next := l.pending[0]
		if ExceedsWriteBatch(l.budget, len(*batch), *bytes, next.bytes) {
			break
		}
		l.pending = l.pending[1:]
		l.pendingBytes -= next.bytes
		*batch = append(*batch, next)
		*bytes += next.bytes
	}
	l.cond.Broadcast()
	return len(l.pending) == 0, l.closed, l.arrived
}

// commitBatch runs the batch's durable work and answers each record's result
// channel with its own report. Its legacy Hold statistic records whole-turn
// elapsed time, not SQLite writer-lock ownership. Exceeding HoldTarget is reported, never enforced by
// truncating the batch, because a record already admitted must either commit or
// fail honestly, never be dropped.
//
// THE PER-RECORD COUNTER IS PUBLISHED BEFORE THE ACKNOWLEDGEMENT. A caller that
// has received its result can then already observe that its record committed;
// an observable that lags the ack is a lie about what the caller knows, and a
// reader that polled Stats for "did my record commit" would race the very
// guarantee the lane exists to give. The turn counters follow the batch, since
// they describe the turn and not one record.
func (l *Lane) commitBatch(batch []*item) {
	// Queue residence ends for EVERY record when this batch starts, not when
	// that record's preparation starts after its predecessors' preparation.
	for _, it := range batch {
		if it.queueDone != nil {
			it.queueDone()
		}
		it.batchPrepareWaitDone = writeprofile.Timer("lane.within_batch.prepare_wait")
	}
	defer writeprofile.Region("lane.turn.prepare.apply.settle")()
	started := time.Now()
	reports := l.commitRecords(batch)
	for i, it := range batch {
		l.records.Add(1)
		it.result <- reports[i]
	}
	held := time.Since(started)

	l.batches.Add(1)
	raiseMax(&l.maxBatchRecords, int64(len(batch)))
	raiseMax(&l.maxHoldNanos, held.Nanoseconds())
	if held > l.budget.HoldTarget {
		l.holdExceeded.Add(1)
	}
}

// commitRecords commits one lane turn. It PREPARES every record (the per-record
// blob write and canonicalization, which must precede the shared transaction),
// commits every prepared operation in ONE batch apply, then SETTLES every
// record against its own outcome. It returns one report per input, aligned by
// index, so commitBatch can answer each caller.
//
// A record whose Prepare fails contributes no operation to the transaction: its
// own error is its report, and the other records still commit. That keeps a
// single record's pre-transaction fault from failing a whole flush, and it is
// honest because the batch transaction contains only the operations that were
// actually prepared.
func (l *Lane) commitRecords(batch []*item) []error {
	reports := make([]error, len(batch))
	type staged struct {
		index int
		item  *item
	}
	prepared := make([]staged, 0, len(batch))
	inputs := make([]provenance.OperationInput, 0, len(batch))
	// One share per turn: every record prepared in this loop resolves
	// turn-constant owner facts once and reuses them, instead of repeating
	// the identical indexed lookups against the same journal rows. The scope
	// dies with the turn, so nothing learned here outlives the batch apply.
	turn := ContextWithTurnScope(l.ctx, NewTurnScope())
	for i, it := range batch {
		if it.batchPrepareWaitDone != nil {
			it.batchPrepareWaitDone()
		}
		if it.admissionToPrepareDone != nil {
			it.admissionToPrepareDone()
		}
		prepareDone := writeprofile.Region("lane.prepare.blob.identity.definitions.encode")
		input, err := it.record.Prepare(turn)
		prepareDone()
		if err != nil {
			reports[i] = err
			continue
		}
		prepared = append(prepared, staged{index: i, item: it})
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return reports
	}
	applyDone := writeprofile.Region("lane.apply.pool.lock.fold.integrity.commit")
	result, applyErr := l.apply(l.ctx, inputs)
	applyDone()
	for k, p := range prepared {
		settleDone := writeprofile.Region("lane.settle.response")
		reports[p.index] = p.item.record.Settle(l.ctx, batchOutcome(result, k, applyErr))
		settleDone()
	}
	return reports
}

// batchOutcome returns one prepared record's own outcome from a batch apply. It
// is the honesty seam between Provenance's closed outcome taxonomy and the
// lane's per-request reporting:
//
//   - A committed or short-circuited record is durably recorded; the lane hands
//     the record that exact outcome, and Report decides what its caller learns.
//     A short-circuit is NOT a failure: its durability comes from an earlier
//     transaction, so it stays durable whatever this batch did.
//   - A conflict, a failed fold, or a record a rolled-back batch never durably
//     folded is NOT recorded by this batch. The lane fills in the batch-level
//     error when the record carries none of its own, so Report always has a
//     typed reason to report.
//   - A missing outcome (the batch apply refused before it built any, e.g. an
//     oversized or deadline-expired batch) is the honest not-recorded with the
//     batch error attached.
func batchOutcome(result provenance.BatchResult, index int, applyErr error) provenance.BatchRecordResult {
	if index >= 0 && index < len(result.Records) {
		outcome := result.Records[index]
		if outcome.Err == nil && !OutcomeIsDurable(outcome.Outcome) {
			outcome.Err = applyErr
		}
		return outcome
	}
	return provenance.BatchRecordResult{Outcome: provenance.BatchRecordNotRecorded, Err: applyErr}
}

// OutcomeIsDurable reports whether a batch outcome means the record is durably
// recorded after the call: BatchRecordCommitted (this batch's transaction wrote
// it) or BatchRecordShortCircuited (an earlier transaction did). Every other
// outcome means nothing this batch folded is durable for the record.
func OutcomeIsDurable(outcome provenance.BatchRecordOutcome) bool {
	return outcome == provenance.BatchRecordCommitted || outcome == provenance.BatchRecordShortCircuited
}

// Report maps one record's batch outcome to the error the lane reports for that
// record. It is the ONE home of the mapping, so the lane and the served path
// cannot drift about what an outcome means.
//
//   - nil means the record is durably recorded: committed by this batch, or
//     short-circuited because an EARLIER transaction already committed it. A
//     caller that gets nil may claim durability; a short-circuit is NOT a
//     failure.
//   - Every other outcome (conflict, failed, not-recorded) returns a typed,
//     actionable error. A record the batch rolled back is never reported as a
//     commit, and the whole-batch failure is never reported as a partial commit.
func Report(outcome provenance.BatchRecordResult) error {
	if OutcomeIsDurable(outcome.Outcome) {
		return nil
	}
	if outcome.Err != nil {
		return outcome.Err
	}
	return fmt.Errorf("the lifecycle write lane did not commit this record: the batch reported outcome %q for it with no per-record error, so nothing this batch folded is durable for it", outcome.Outcome)
}

// raiseMax swaps a larger value into a peak counter without a lock.
func raiseMax(counter *atomic.Int64, value int64) {
	for {
		current := counter.Load()
		if value <= current || counter.CompareAndSwap(current, value) {
			return
		}
	}
}
