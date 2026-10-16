package writelane

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
)

// commitFunc is a lane test's Record: Prepare succeeds with a synthetic
// operation and Settle runs the closure the test supplied. It exists so the
// lane's transport behaviour (admission, batching, flush policy, hold) can be
// tested without a store, exactly as the phase-1 tests did with a bare closure.
type commitFunc func(ctx context.Context) error

func (f commitFunc) Prepare(context.Context) (provenance.OperationInput, error) {
	return provenance.OperationInput{OperationID: provenance.OperationID("lane-test")}, nil
}

func (f commitFunc) Settle(ctx context.Context, _ provenance.BatchRecordResult) error { return f(ctx) }

// testApply is a BatchApply that commits every prepared record, so a lane test
// that does not care about the batch seam sees each record commit exactly as it
// did before phase 2.
func testApply(_ context.Context, inputs []provenance.OperationInput) (provenance.BatchResult, error) {
	records := make([]provenance.BatchRecordResult, len(inputs))
	for i := range records {
		records[i] = provenance.BatchRecordResult{Outcome: provenance.BatchRecordCommitted}
	}
	return provenance.BatchResult{Committed: true, Records: records}, nil
}

// newTestLane constructs a lane on the given budget with the default committing
// applier.
func newTestLane(t *testing.T, budget WriteLaneBudget) *Lane {
	t.Helper()
	lane, err := New(budget, testApply)
	if err != nil {
		t.Fatal(err)
	}
	return lane
}

// waitForCommitted runs Submit on its own goroutine and returns a channel that
// receives Submit's result, so a test can assert on what has NOT happened yet.
func waitForCommitted(lane *Lane, ctx context.Context, bytes int64, record Record) <-chan error {
	done := make(chan error, 1)
	go func() { done <- lane.Submit(ctx, bytes, record) }()
	return done
}

func TestNewRefusesANilBatchApplier(t *testing.T) {
	if _, err := New(testBudget(), nil); err == nil {
		t.Fatal("New accepted a lane with no way to commit a flush")
	}
}

func TestSubmitDoesNotReturnBeforeTheCommit(t *testing.T) {
	lane := newTestLane(t, testBudget())
	defer lane.Close(context.Background())

	entered := make(chan struct{})
	release := make(chan struct{})
	done := waitForCommitted(lane, context.Background(), 16, commitFunc(func(context.Context) error {
		close(entered)
		<-release
		return nil
	}))

	<-entered
	select {
	case err := <-done:
		t.Fatalf("Submit returned (%v) while its commit was still running; the caller would have been told recorded for an uncommitted record", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Submit returned %v after the commit succeeded", err)
	}
}

func TestSubmitReturnsTheCommitError(t *testing.T) {
	lane := newTestLane(t, testBudget())
	defer lane.Close(context.Background())

	want := errors.New("journal refused the append")
	if got := lane.Submit(context.Background(), 8, commitFunc(func(context.Context) error { return want })); !errors.Is(got, want) {
		t.Fatalf("Submit returned %v, want the commit's own error %v", got, want)
	}
}

func TestFlushWhenFullGroupsRecordsIntoBoundedBatches(t *testing.T) {
	budget := testBudget()
	budget.BatchRecords = 4
	budget.QueueRecords = 32
	budget.FlushInterval = 50 * time.Millisecond
	lane := newTestLane(t, budget)
	defer lane.Close(context.Background())

	var mu sync.Mutex
	seen := 0
	hold := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lane.Submit(context.Background(), 8, commitFunc(func(context.Context) error {
				mu.Lock()
				seen++
				mu.Unlock()
				<-hold
				return nil
			}))
		}()
	}
	// Hold every batch open so the queue fills to its bound and the splitter is
	// what decides the batch boundaries, not the timing of the commits.
	time.Sleep(20 * time.Millisecond)
	close(hold)
	wg.Wait()

	stats := lane.Stats()
	if stats.Records != 12 {
		t.Fatalf("committed %d records, want 12", stats.Records)
	}
	if stats.MaxBatchRecords > int64(budget.BatchRecords) {
		t.Fatalf("a batch held %d records, above BatchRecords %d; the splitter did not bound the turn", stats.MaxBatchRecords, budget.BatchRecords)
	}
	if stats.Batches >= stats.Records {
		t.Fatalf("12 records produced %d batches; the splitter never grouped anything", stats.Batches)
	}
}

func TestDrainFlushCommitsAsSoonAsACompanionDrainsTheQueue(t *testing.T) {
	budget := testBudget()
	budget.HoldTarget = time.Hour
	budget.FlushInterval = time.Second // the drain trigger must fire long before the interval
	lane := newTestLane(t, budget)
	defer lane.Close(context.Background())

	first := waitForCommitted(lane, context.Background(), 8, commitFunc(func(context.Context) error { return nil }))
	// Let the commit goroutine take the first record and park on its idle wait,
	// so the second record's arrival is what drains the queue.
	time.Sleep(10 * time.Millisecond)

	started := time.Now()
	second := waitForCommitted(lane, context.Background(), 8, commitFunc(func(context.Context) error { return nil }))
	for _, done := range []<-chan error{first, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(budget.FlushInterval / 2):
			t.Fatalf("the batch did not flush when the arriving record drained the queue; it waited for the %s interval instead of committing at once", budget.FlushInterval)
		}
	}
	if elapsed := time.Since(started); elapsed > budget.FlushInterval/2 {
		t.Fatalf("the drain flush took %s, past the %s half-interval bound", elapsed, budget.FlushInterval)
	}
	if stats := lane.Stats(); stats.Records != 2 || stats.MaxBatchRecords != 2 {
		t.Fatalf("stats = %+v, want both records in one batch", stats)
	}
}

func TestIntervalFallbackCommitsAPartialBatch(t *testing.T) {
	budget := testBudget()
	budget.BatchBytes = 1 << 20
	budget.QueueBytes = 8 << 20
	budget.FlushInterval = 200 * time.Millisecond
	budget.HoldTarget = time.Second
	lane := newTestLane(t, budget)
	defer lane.Close(context.Background())

	// Two records that cannot share a batch under the byte budget: the one that
	// arrives while the other is staged does not fit, so nothing drains the
	// queue and only the interval can bound the partial batch's wait.
	const halfMiB = 600 << 10
	started := time.Now()
	first := waitForCommitted(lane, context.Background(), halfMiB, commitFunc(func(context.Context) error { return nil }))
	second := waitForCommitted(lane, context.Background(), halfMiB, commitFunc(func(context.Context) error { return nil }))
	for _, done := range []<-chan error{first, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * budget.FlushInterval):
			t.Fatalf("the partial batch was still uncommitted %s after the interval %s", 10*budget.FlushInterval, budget.FlushInterval)
		}
	}
	if elapsed := time.Since(started); elapsed < budget.FlushInterval/2 {
		t.Fatalf("the partial batch flushed after %s, before the %s interval that is supposed to bound it", elapsed, budget.FlushInterval)
	}
	if stats := lane.Stats(); stats.Records != 2 || stats.Batches != 2 {
		t.Fatalf("stats = %+v, want each oversized-for-the-batch record committed in its own batch", stats)
	}
}

func TestOversizeRecordIsAdmittedAndCommittedAlone(t *testing.T) {
	budget := testBudget()
	budget.BatchBytes = 1 << 20
	budget.QueueBytes = 8 << 20
	lane := newTestLane(t, budget)
	defer lane.Close(context.Background())

	const oversize = 2 << 20
	if err := lane.Submit(context.Background(), oversize, commitFunc(func(context.Context) error { return nil })); err != nil {
		t.Fatalf("an oversize record was refused rather than committed alone: %v", err)
	}
	// The record counter precedes its ACK; whole-turn counters deliberately
	// follow it. Join the real lane before inspecting completed-turn statistics,
	// while retaining the immediate per-record publication guarantee.
	if stats := lane.Stats(); stats.Records != 1 {
		t.Fatalf("stats = %+v, want the committed record visible before its ACK", stats)
	}
	if err := lane.Close(context.Background()); err != nil {
		t.Fatalf("join the completed oversize turn before reading turn statistics: %v", err)
	}
	if stats := lane.Stats(); stats.Records != 1 || stats.MaxBatchRecords != 1 || stats.Batches != 1 {
		t.Fatalf("stats = %+v, want the oversize record committed alone", stats)
	}
}

func TestQueueBoundAppliesBackpressureInsteadOfRefusing(t *testing.T) {
	budget := testBudget()
	budget.QueueRecords = 1
	budget.BatchRecords = 1
	budget.FlushInterval = 10 * time.Millisecond
	budget.HoldTarget = time.Second
	lane := newTestLane(t, budget)
	defer lane.Close(context.Background())

	entered := make(chan struct{})
	release := make(chan struct{})
	held := waitForCommitted(lane, context.Background(), 8, commitFunc(func(context.Context) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return nil
	}))
	<-entered // the commit goroutine now holds the lane

	// The first record has left the queue and is being committed, so one more
	// may be staged; the next one must wait.
	staged := waitForCommitted(lane, context.Background(), 8, commitFunc(func(context.Context) error { return nil }))
	deadline := time.Now().Add(2 * time.Second)
	for lane.Stats().QueueDepth == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	blocked := lane.Submit(ctx, 8, commitFunc(func(context.Context) error { return nil }))
	if elapsed := time.Since(started); elapsed < 25*time.Millisecond {
		t.Fatalf("the record beyond the queue bound returned after %s while the bound was held; it should have waited for its own context", elapsed)
	}
	if blocked == nil {
		t.Fatal("a record that could not be admitted reported success")
	}
	if !errors.Is(blocked, context.DeadlineExceeded) {
		t.Fatalf("the admission refusal is %v, want it to carry the caller's own deadline", blocked)
	}
	close(release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	if err := <-staged; err != nil {
		t.Fatalf("the staged record that was inside the bound failed: %v", err)
	}
}

func TestCloseDrainsStagedRecords(t *testing.T) {
	budget := testBudget()
	budget.FlushInterval = 20 * time.Millisecond
	lane := newTestLane(t, budget)

	var mu sync.Mutex
	committed := 0
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := lane.Submit(context.Background(), 8, commitFunc(func(context.Context) error {
				mu.Lock()
				committed++
				mu.Unlock()
				return nil
			})); err != nil {
				t.Errorf("Submit %d returned %v", n, err)
			}
		}(i)
	}
	time.Sleep(10 * time.Millisecond)
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lane.Close(closeCtx); err != nil {
		t.Fatalf("Close returned %v", err)
	}
	wg.Wait()
	if committed != 4 {
		t.Fatalf("Close committed %d of 4 staged records", committed)
	}
	if err := lane.Submit(context.Background(), 8, commitFunc(func(context.Context) error { return nil })); !errors.Is(err, ErrLaneClosed) {
		t.Fatalf("Submit after Close returned %v, want ErrLaneClosed", err)
	}
}

func TestStatsReportATurnThatOutranItsHoldTarget(t *testing.T) {
	budget := testBudget()
	budget.HoldTarget = 5 * time.Millisecond
	budget.FlushInterval = time.Millisecond
	lane := newTestLane(t, budget)
	defer lane.Close(context.Background())

	if err := lane.Submit(context.Background(), 8, commitFunc(func(context.Context) error {
		time.Sleep(15 * time.Millisecond)
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	stats := lane.Stats()
	if stats.HoldExceeded == 0 {
		t.Fatalf("stats = %+v, want the turn that outran its %s hold target to be reported", stats, budget.HoldTarget)
	}
	if stats.MaxHold < 15*time.Millisecond {
		t.Fatalf("MaxHold %s is below the 15ms the commit took", stats.MaxHold)
	}
}
