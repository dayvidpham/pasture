package inventory_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/inventory"
	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
)

// registrationManifests is the pinned lifecycle-event truth: the three
// generated registration manifests. The inventory lifecycle-event rows are
// emitted by hostcontractgen in the SAME contract walk that renders these
// manifests, so agreement here is a belt-and-suspenders check on top of the
// by-construction emission — any divergence means a generated projection
// drifted from the pinned contract.
func registrationManifests() []registration.Manifest {
	return []registration.Manifest{
		registration.ClaudeCode2_1_210(),
		registration.OpenCode1_18_10(),
		registration.Codex0_146_0(),
	}
}

// TestLifecycleEventRowsAgreeWithRegistration proves exhaustive, exact
// agreement between the inventory lifecycle-event rows and the pinned
// registration manifests: every registration event has a row
// (harness, lifecycle-event, NativeName), and no lifecycle-event row exists
// without a backing registration event.
//
// EXPECTED TO FAIL until L3 generates lifecycle_events.gen.go — at L1/L2 the
// inventory table has no lifecycle-event rows, so every expected row is
// reported missing.
func TestLifecycleEventRowsAgreeWithRegistration(t *testing.T) {
	table, err := inventory.Table()
	if err != nil {
		t.Fatalf("inventory.Table(): %v", err)
	}

	// Collect lifecycle-event rows present in the table.
	have := make(map[inventory.Key]bool)
	for _, r := range table {
		if r.Key.Kind == inventory.KindLifecycleEvent {
			have[r.Key] = true
		}
	}

	// Every registration event must have its row (exhaustiveness / CI-reject).
	for _, m := range registrationManifests() {
		for _, e := range m.Entries() {
			k := inventory.Key{Harness: m.Harness, Kind: inventory.KindLifecycleEvent, ID: e.NativeName}
			if !have[k] {
				t.Errorf(
					"missing lifecycle-event row: harness=%q native=%q — registration authored it but the inventory table has no matching row; "+
						"regenerate lifecycle_events.gen.go (make generate) so the same-walk emission covers every registration event",
					m.Harness, e.NativeName)
			}
			delete(have, k)
		}
	}

	// No lifecycle-event row may exist without a backing registration event.
	for k := range have {
		t.Errorf(
			"extra lifecycle-event row not backed by any registration event: harness=%q native=%q — a generated projection fabricated a lifecycle event; regenerate from the pinned contract",
			k.Harness, k.ID)
	}
}

// TestLifecycleEventHarnessCoverage guards that all three enabled harnesses
// contribute lifecycle-event rows (a generator silently dropping a harness must
// fail). EXPECTED TO FAIL until L3.
func TestLifecycleEventHarnessCoverage(t *testing.T) {
	table, err := inventory.Table()
	if err != nil {
		t.Fatalf("inventory.Table(): %v", err)
	}
	seen := make(map[ir.HarnessID]int)
	for _, r := range table {
		if r.Key.Kind == inventory.KindLifecycleEvent {
			seen[r.Key.Harness]++
		}
	}
	for _, h := range ir.EnabledHarnessIDs() {
		if seen[h] == 0 {
			t.Errorf("no lifecycle-event rows for enabled harness %q — the same-walk emission dropped a harness", h)
		}
	}
}
