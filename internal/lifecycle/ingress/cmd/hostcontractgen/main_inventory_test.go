package main

import (
	"sort"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/inventory"
	"github.com/dayvidpham/pasture/internal/lifecycle/ingress/internal/hostcontract"
)

// TestInventoryLifecycleRowsAgreeWithPinnedContracts re-derives the expected
// lifecycle-event row set FRESH from the pinned hostcontract contracts — the
// exact source the generator walk consumes — and asserts set-equality against
// the COMMITTED inventory table's lifecycle-event rows.
//
// This test only lives here, in the generator's own package main, because the
// unexported internal/lifecycle/ingress/internal/hostcontract package is
// importable from within the ingress tree. The sibling registration-agreement
// test (internal/inventory) can only compare two GENERATED artifacts (the
// committed inventory table vs the committed registration manifests); a
// coordinated hand-edit to BOTH .gen.go files would slip past it. Re-deriving
// straight from the raw hostcontract.Contract.Events here — the single upstream
// truth both generated projections descend from — closes that gap: a doctored
// coordinated edit of both generated files now goes red against the contracts.
//
// Mirrors the native-axis agreement test's diffStringSets / set-equality style.
func TestInventoryLifecycleRowsAgreeWithPinnedContracts(t *testing.T) {
	// Re-derive expected rows from the pinned contracts, keyed exactly as the
	// generator walk keys them (harness selector × contract event Name). This
	// is the same (harness, contract) pairing main() uses for the inventory
	// lifecycle-event emission.
	type harnessContract struct {
		harness  ir.HarnessID
		contract hostcontract.Contract
	}
	pinned := []harnessContract{
		{ir.HarnessClaudeCode, hostcontract.ClaudeCode2_1_210()},
		{ir.HarnessOpenCode, hostcontract.OpenCode1_18_10()},
		{ir.HarnessCodex, hostcontract.Codex0_146_0()},
	}
	var derived []string
	for _, hc := range pinned {
		for _, ev := range hc.contract.Events {
			derived = append(derived, lifecycleRowKey(hc.harness, ev.Name))
		}
	}

	// Collect the committed inventory table's lifecycle-event rows.
	table, err := inventory.Table()
	if err != nil {
		t.Fatalf("inventory.Table(): %v", err)
	}
	var committed []string
	for _, r := range table {
		if r.Key.Kind == inventory.KindLifecycleEvent {
			committed = append(committed, lifecycleRowKey(r.Key.Harness, r.Key.ID))
		}
	}

	missing, extra := diffLifecycleRowSets(derived, committed)
	for _, k := range missing {
		t.Errorf(
			"missing lifecycle-event row %q — the pinned hostcontract contract declares this event but the committed inventory table omits it; regenerate lifecycle_events.gen.go (make generate)",
			k)
	}
	for _, k := range extra {
		t.Errorf(
			"contradicting lifecycle-event row %q — committed in the inventory table but NO pinned hostcontract contract declares it; the generated table may not override contract-derived truth even via a coordinated edit of both .gen.go files (CI-reject)",
			k)
	}
}

// lifecycleRowKey joins a harness and native event name into a single set
// element. The NUL separator can never appear in either component, so distinct
// (harness, name) pairs never collide.
func lifecycleRowKey(harness ir.HarnessID, name string) string {
	return string(harness) + "\x00" + name
}

// diffLifecycleRowSets returns the elements present in want but absent from got
// (missing) and present in got but absent from want (extra), treating each as a
// set. Mirrors the native-axis diffStringSets primitive.
func diffLifecycleRowSets(want, got []string) (missing, extra []string) {
	wantSet := make(map[string]bool, len(want))
	for _, s := range want {
		wantSet[s] = true
	}
	gotSet := make(map[string]bool, len(got))
	for _, s := range got {
		gotSet[s] = true
	}
	for s := range wantSet {
		if !gotSet[s] {
			missing = append(missing, s)
		}
	}
	for s := range gotSet {
		if !wantSet[s] {
			extra = append(extra, s)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
