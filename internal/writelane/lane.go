package writelane

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
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

// Commit is one admitted record's serialized durable work. The lane runs it on
// its single goroutine and returns its error to the submitting caller.
//
// The context it receives is the LANE's, not the submitter's: a record that a
// hook abandoned while it waited in the queue is still committed (evidence
// over silence), so the commit must not be cancelled by the hook's own
// deadline. The work's own writers bound themselves with the injected profile.
type Commit func(ctx context.Context) error

// item is one admitted-but-uncommitted record. Its identity is deliberately
// opaque to the lane: the lane moves bytes and calls a closure, and never
// knows what a record means.
type item struct {
	bytes  int64
	commit Commit
	result chan error
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
// commit closure has run and reported its error, so there is no path on which
// a hook is told "recorded" for a record that has not committed. That is the
// property the whole daemon-fronted lane exists to preserve, and it is a
// property of Submit's shape rather than of any discipline at the call site.
type Lane struct {
	budget WriteLaneBudget
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

	records         atomic.Int64
	batches         atomic.Int64
	maxBatchRecords atomic.Int64
	holdExceeded    atomic.Int64
	maxHoldNanos    atomic.Int64
}

// New constructs the lane and starts its commit goroutine. The budget is
// validated here, so a lane that exists is a lane whose bounds hold.
func New(budget WriteLaneBudget) (*Lane, error) {
	if err := budget.Validate(); err != nil {
		return nil, fmt.Errorf("construct the lifecycle write lane: %w", err)
	}
	lane := &Lane{
		budget:  budget,
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
func (l *Lane) Submit(ctx context.Context, bytes int64, commit Commit) error {
	if l == nil {
		return fmt.Errorf("%w: the lane is nil, so no record could be admitted and nothing was committed", ErrNotAdmitted)
	}
	if commit == nil {
		return fmt.Errorf("%w: no commit work was supplied, so nothing was committed", ErrNotAdmitted)
	}
	if bytes < 0 {
		return fmt.Errorf("%w: the record states %d bytes, and a negative size cannot be staged", ErrNotAdmitted, bytes)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	me := &item{bytes: bytes, commit: commit, result: make(chan error, 1)}

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
	l.pending = append(l.pending, me)
	l.pendingBytes += bytes
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
// wait for — may hold the store before it is committed anyway. Each record's own
// result channel is answered as soon as its commit returns, so a slow record in
// a batch delays only its own acknowledgement, never another record's.
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
				l.commitBatch(batch)
				continue
			}
		}

		timer := time.NewTimer(l.budget.FlushInterval)
	waiting:
		for {
			select {
			case <-arrival:
				var drained, stopped bool
				drained, stopped, arrival = l.absorb(&batch, &bytes)
				if drained || stopped {
					break waiting
				}
				// The record that arrived does not fit this batch's budget. Keep
				// waiting for the idle fallback; it is carried by the next batch.
			case <-timer.C:
				break waiting
			}
			if ExceedsWriteBatch(l.budget, len(batch), bytes, 0) {
				break waiting
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		l.commitBatch(batch)
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

// commitBatch runs each record's commit in order and answers each record's
// result channel with that commit's error. It records how long the turn held
// the writer against HoldTarget; exceeding the target is reported, never
// enforced by truncating the batch, because a record already admitted must
// either commit or fail honestly, never be dropped.
func (l *Lane) commitBatch(batch []*item) {
	started := time.Now()
	for _, it := range batch {
		err := it.commit(l.ctx)
		it.result <- err
	}
	held := time.Since(started)

	l.batches.Add(1)
	l.records.Add(int64(len(batch)))
	recordCount := int64(len(batch))
	raiseMax(&l.maxBatchRecords, recordCount)
	raiseMax(&l.maxHoldNanos, held.Nanoseconds())
	if held > l.budget.HoldTarget {
		l.holdExceeded.Add(1)
	}
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
