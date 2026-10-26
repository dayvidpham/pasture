package writelane

import (
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
			if budget.QueueRecords != 256 {
				t.Fatalf("QueueRecords = %d, want 256; the byte-budget change must not move the record staging bound", budget.QueueRecords)
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
			if budget.BatchRecords < 1 {
				t.Fatalf("BatchRecords is %d; a batch must admit at least one record", budget.BatchRecords)
			}
			if profile.Kind() == timeouts.Production {
				if budget.HoldTarget != 400*time.Millisecond || budget.BatchRecords != 20 || budget.FlushInterval != 25*time.Millisecond {
					t.Fatalf("production time knobs moved with the byte budget: HoldTarget=%s BatchRecords=%d FlushInterval=%s; want 400ms, 20, 25ms", budget.HoldTarget, budget.BatchRecords, budget.FlushInterval)
				}
			}
		})
	}
}

func TestProductionHoldTargetStaysAtTheCapWhenTheBusyTierGrows(t *testing.T) {
	profile := timeouts.ProductionProfile()
	uncapped := profile.SQLiteBusy() * 4 / 5
	if uncapped <= maxDerivedHoldTarget {
		t.Fatalf("4/5 of production SQLiteBusy (%s) is %s, which does not exceed the %s cap; this test no longer proves the cap holds the lane hold down", profile.SQLiteBusy(), uncapped, maxDerivedHoldTarget)
	}
	budget, err := BudgetFromProfile(profile)
	if err != nil {
		t.Fatalf("BudgetFromProfile(production) = %v", err)
	}
	if budget.HoldTarget != maxDerivedHoldTarget {
		t.Fatalf("production HoldTarget = %s, want the %s cap; do not raise maxDerivedHoldTarget with the busy tier", budget.HoldTarget, maxDerivedHoldTarget)
	}
	if budget.FlushInterval != 25*time.Millisecond {
		t.Fatalf("production FlushInterval = %s, want 25ms; the busy-tier change must not move the idle flush", budget.FlushInterval)
	}
	if want := int(maxDerivedHoldTarget / assumedPerRecordCommitCost); budget.BatchRecords != want {
		t.Fatalf("production BatchRecords = %d, want %d derived from the capped hold; the busy-tier change must not change BatchRecords", budget.BatchRecords, want)
	}
}

func TestBudgetFromProfileRefusesAnInvalidProfile(t *testing.T) {
	if _, err := BudgetFromProfile(timeouts.Profile{}); err == nil {
		t.Fatal("BudgetFromProfile accepted the zero profile")
	}
}
