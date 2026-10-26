package writelane

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/provenance"
)

// testBudget is the smallest budget that satisfies Validate, used as the base
// for the table rows that change exactly one knob.
func testBudget() WriteLaneBudget {
	return WriteLaneBudget{
		QueueRecords:  8,
		QueueBytes:    8 << 20,
		BatchRecords:  4,
		BatchBytes:    2 << 20,
		FlushInterval: 2 * time.Millisecond,
		HoldTarget:    100 * time.Millisecond,
	}
}

func TestExceedsWriteBatchSplitsOnCountAndBytes(t *testing.T) {
	budget := testBudget()
	budget.BatchRecords = 4
	budget.BatchBytes = 1024

	cases := []struct {
		name    string
		pending int
		bytes   int64
		next    int64
		want    bool
	}{
		{name: "empty batch admits the first record however large", pending: 0, bytes: 0, next: 4096, want: false},
		{name: "count cap reached flushes before the next joins", pending: 4, bytes: 10, next: 1, want: true},
		{name: "count one below the cap admits", pending: 3, bytes: 10, next: 1, want: false},
		{name: "byte cap exceeded flushes before the next joins", pending: 1, bytes: 1000, next: 25, want: true},
		{name: "byte cap exactly reached admits", pending: 1, bytes: 1000, next: 24, want: false},
		{name: "empty batch is not split by the byte term", pending: 0, bytes: 0, next: 1 << 20, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExceedsWriteBatch(budget, tc.pending, tc.bytes, tc.next); got != tc.want {
				t.Fatalf("ExceedsWriteBatch(pending=%d, bytes=%d, next=%d) = %v, want %v", tc.pending, tc.bytes, tc.next, got, tc.want)
			}
		})
	}
}

func TestValidateRefusesEachInvertedKnobAndNamesIt(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*WriteLaneBudget)
		want string
	}{
		{name: "queue records", mut: func(b *WriteLaneBudget) { b.QueueRecords = 0 }, want: "QueueRecords"},
		{name: "queue bytes", mut: func(b *WriteLaneBudget) { b.QueueBytes = 0 }, want: "QueueBytes"},
		{name: "batch records", mut: func(b *WriteLaneBudget) { b.BatchRecords = 0 }, want: "BatchRecords"},
		{name: "batch records above queue records", mut: func(b *WriteLaneBudget) { b.BatchRecords = b.QueueRecords + 1 }, want: "BatchRecords"},
		{name: "batch records above the provenance batch cap", mut: func(b *WriteLaneBudget) {
			b.QueueRecords = provenance.MaxBatchRecords + 1
			b.BatchRecords = provenance.MaxBatchRecords + 1
		}, want: "Provenance batch cap"},
		{name: "batch bytes", mut: func(b *WriteLaneBudget) { b.BatchBytes = 0 }, want: "BatchBytes"},
		{name: "batch bytes above queue bytes", mut: func(b *WriteLaneBudget) { b.BatchBytes = b.QueueBytes + 1 }, want: "BatchBytes"},
		{name: "batch bytes below the ingress floor", mut: func(b *WriteLaneBudget) { b.BatchBytes = 1 }, want: "ingress payload cap"},
		{name: "flush interval", mut: func(b *WriteLaneBudget) { b.FlushInterval = 0 }, want: "FlushInterval"},
		{name: "hold target", mut: func(b *WriteLaneBudget) { b.HoldTarget = 0 }, want: "HoldTarget"},
		{name: "flush interval not below the hold target", mut: func(b *WriteLaneBudget) { b.FlushInterval = b.HoldTarget }, want: "inverted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := testBudget()
			tc.mut(&budget)
			err := budget.Validate()
			if err == nil {
				t.Fatalf("Validate accepted a budget whose %s is out of bounds: %+v", tc.name, budget)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error for %s does not name %q: %v", tc.name, tc.want, err)
			}
		})
	}
	if err := testBudget().Validate(); err != nil {
		t.Fatalf("Validate refused the budget every other row is derived from: %v", err)
	}
}

func TestBudgetFromProfileStaysInsideTheProfileTiers(t *testing.T) {
	for _, profile := range timeouts.KnownProfiles() {
		t.Run(fmt.Sprintf("kind-%d", profile.Kind()), func(t *testing.T) {
			budget, err := BudgetFromProfile(profile)
			if err != nil {
				t.Fatalf("BudgetFromProfile(%v) = %v", profile.Kind(), err)
			}
			if err := budget.Validate(); err != nil {
				t.Fatalf("BudgetFromProfile produced a budget Validate refuses: %v", err)
			}
			// BatchBytes is a fixed derivation, not a profile knob. The literals
			// pin the raised budget: 64 MiB is above a count-legal turn of
			// 1 MiB records, and QueueBytes stays four times that (256 MiB).
			if budget.BatchBytes != 64<<20 {
				t.Fatalf("BatchBytes = %d, want 64 MiB (%d)", budget.BatchBytes, int64(64<<20))
			}
			if budget.QueueBytes != 256<<20 {
				t.Fatalf("QueueBytes = %d, want 256 MiB (%d)", budget.QueueBytes, int64(256<<20))
			}
			if budget.QueueBytes != budget.BatchBytes*4 {
				t.Fatalf("QueueBytes = %d, want 4 x BatchBytes (%d)", budget.QueueBytes, budget.BatchBytes*4)
			}
			if budget.QueueRecords != 1024 {
				t.Fatalf("QueueRecords = %d, want 1024", budget.QueueRecords)
			}
			if budget.BatchRecords != 256 {
				t.Fatalf("BatchRecords = %d, want 256", budget.BatchRecords)
			}
			if budget.BatchRecords != provenance.MaxBatchRecords {
				t.Fatalf("BatchRecords = %d, want provenance.MaxBatchRecords (%d)", budget.BatchRecords, provenance.MaxBatchRecords)
			}
			if budget.HoldTarget > profile.SQLiteBusy() {
				t.Fatalf("HoldTarget %s exceeds the SQLite busy tier %s; the lane would hold the writer past the window the other writers wait", budget.HoldTarget, profile.SQLiteBusy())
			}
			if budget.HoldTarget > maxDerivedHoldTarget {
				t.Fatalf("HoldTarget %s exceeds the %s ceiling", budget.HoldTarget, maxDerivedHoldTarget)
			}
			if budget.FlushInterval >= budget.HoldTarget {
				t.Fatalf("FlushInterval %s is not strictly below HoldTarget %s", budget.FlushInterval, budget.HoldTarget)
			}
			if budget.FlushInterval >= profile.HookInvocation() {
				t.Fatalf("FlushInterval %s is not below the hook-invocation tier %s", budget.FlushInterval, profile.HookInvocation())
			}
			switch profile.Kind() {
			case timeouts.Production:
				if budget.HoldTarget != time.Second || budget.FlushInterval != 50*time.Millisecond {
					t.Fatalf("production hold/flush = %s/%s, want 1s/50ms", budget.HoldTarget, budget.FlushInterval)
				}
			case timeouts.Test:
				// 500ms busy derives a 400ms hold. The production 50ms flush
				// would fit under that hold, but the scaled 25ms fallback
				// must remain.
				if budget.HoldTarget != 400*time.Millisecond || budget.FlushInterval != 25*time.Millisecond {
					t.Fatalf("TestProfile hold/flush = %s/%s, want 400ms/25ms", budget.HoldTarget, budget.FlushInterval)
				}
			case timeouts.DeadlineTest:
				// 25ms busy derives a 20ms hold, and the scaled fallback is
				// hold/4 (5ms). The production 50ms flush would not be
				// strictly below that hold.
				if budget.HoldTarget != 20*time.Millisecond || budget.FlushInterval != 5*time.Millisecond {
					t.Fatalf("DeadlineTestProfile hold/flush = %s/%s, want 20ms/5ms", budget.HoldTarget, budget.FlushInterval)
				}
			}
		})
	}
}

func TestProductionHoldTargetStaysAtTheCapWhenTheBusyTierGrows(t *testing.T) {
	if maxDerivedHoldTarget != time.Second {
		t.Fatalf("maxDerivedHoldTarget = %s, want 1s", maxDerivedHoldTarget)
	}
	profile := timeouts.ProductionProfile()
	uncapped := profile.SQLiteBusy() * 4 / 5
	if uncapped != 1200*time.Millisecond {
		t.Fatalf("4/5 of production SQLiteBusy (%s) is %s, want 1.2s", profile.SQLiteBusy(), uncapped)
	}
	if uncapped <= maxDerivedHoldTarget {
		t.Fatalf("4/5 of production SQLiteBusy (%s) is %s, which does not exceed the %s cap; this test no longer proves the cap holds the lane hold down", profile.SQLiteBusy(), uncapped, maxDerivedHoldTarget)
	}
	budget, err := BudgetFromProfile(profile)
	if err != nil {
		t.Fatalf("BudgetFromProfile(production) = %v", err)
	}
	if budget.HoldTarget != time.Second {
		t.Fatalf("production HoldTarget = %s, want 1s; do not raise maxDerivedHoldTarget past 1s with the busy tier", budget.HoldTarget)
	}
	if budget.HoldTarget != maxDerivedHoldTarget {
		t.Fatalf("production HoldTarget = %s, want the cap %s", budget.HoldTarget, maxDerivedHoldTarget)
	}
	if budget.FlushInterval != 50*time.Millisecond {
		t.Fatalf("production FlushInterval = %s, want 50ms", budget.FlushInterval)
	}
	if budget.BatchRecords != 256 {
		t.Fatalf("production BatchRecords = %d, want 256", budget.BatchRecords)
	}
	if budget.BatchRecords != provenance.MaxBatchRecords {
		t.Fatalf("production BatchRecords = %d, want provenance.MaxBatchRecords (%d)", budget.BatchRecords, provenance.MaxBatchRecords)
	}
	if budget.QueueRecords != 1024 {
		t.Fatalf("production QueueRecords = %d, want 1024", budget.QueueRecords)
	}
}

func TestBatchRecordsAtTheProvenanceCapValidatesAndOneAboveIsRefused(t *testing.T) {
	if provenance.MaxBatchRecords != 256 {
		t.Fatalf("provenance.MaxBatchRecords = %d, want 256; the production batch cap is that constant", provenance.MaxBatchRecords)
	}
	budget, err := BudgetFromProfile(timeouts.ProductionProfile())
	if err != nil {
		t.Fatalf("BudgetFromProfile(production) = %v", err)
	}
	if budget.BatchRecords != provenance.MaxBatchRecords {
		t.Fatalf("production BatchRecords = %d, want provenance.MaxBatchRecords (%d)", budget.BatchRecords, provenance.MaxBatchRecords)
	}
	if err := budget.Validate(); err != nil {
		t.Fatalf("Validate refused BatchRecords == provenance.MaxBatchRecords (%d): %v", provenance.MaxBatchRecords, err)
	}

	// QueueRecords must be at least the batch, or Validate fails on the queue
	// relationship before it can name the Provenance cap.
	over := budget
	over.QueueRecords = provenance.MaxBatchRecords + 1
	over.BatchRecords = provenance.MaxBatchRecords + 1
	err = over.Validate()
	if err == nil {
		t.Fatal("Validate accepted a batch one above the Provenance cap")
	}
	if !strings.Contains(err.Error(), "Provenance batch cap") {
		t.Fatalf("Validate error does not name the Provenance cap: %v", err)
	}

	if ExceedsWriteBatch(budget, provenance.MaxBatchRecords-1, 0, 1) {
		t.Fatal("the splitter refused the record that fills a Provenance-cap turn")
	}
	if !ExceedsWriteBatch(budget, provenance.MaxBatchRecords, 0, 1) {
		t.Fatal("the splitter admitted a record past the Provenance cap into the same turn")
	}

	lane := &Lane{budget: budget, apply: testApply, ctx: context.Background()}
	batch := make([]*item, provenance.MaxBatchRecords)
	for i := range batch {
		batch[i] = &item{
			bytes:  1,
			record: commitFunc(func(context.Context) error { return nil }),
			result: make(chan error, 1),
		}
	}
	lane.commitBatch(batch)
	if got := lane.Stats().Records; got != int64(provenance.MaxBatchRecords) {
		t.Fatalf("committed records = %d, want %d; a cap-sized turn must commit every admitted record", got, provenance.MaxBatchRecords)
	}
	for i, it := range batch {
		select {
		case commitErr := <-it.result:
			if commitErr != nil {
				t.Fatalf("record %d failed: %v", i, commitErr)
			}
		default:
			t.Fatalf("record %d was not answered; the lane dropped an admitted record", i)
		}
	}
}

func TestBudgetFromProfileRefusesAnInvalidProfile(t *testing.T) {
	if _, err := BudgetFromProfile(timeouts.Profile{}); err == nil {
		t.Fatal("BudgetFromProfile accepted the zero profile")
	}
}
