package codegen

import (
	"sort"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/inventory"
)

// These tests live in package codegen (white-box) because the command and
// native-function derivation sources — commandSkillDirs, codexNativeFunctions,
// deriveOpenCodeNativeToolNames — are deliberately unexported. They re-derive
// each axis from its pinned source and assert exact agreement with the
// committed inventory rows, giving the CI-reject semantics the ratified plan
// requires: a missing row, an extra row, or a row that contradicts the
// contract-derived truth turns the build red.
//
// The exhaustiveness assertions EXPECT TO FAIL until L3 emits commands.gen.go
// and native_functions.gen.go; at L1/L2 the inventory table has no command or
// native rows, so every expected row is reported missing.

// TestInventoryCommandRowsFanOutPerHarness proves the command axis: every
// authored command (commandSkillDirs key) has a row for every enabled harness
// (uniform per-harness fan-out, UAT resolution 4), and no command row exists
// outside that authored fan-out.
func TestInventoryCommandRowsFanOutPerHarness(t *testing.T) {
	table, err := inventory.Table()
	if err != nil {
		t.Fatalf("inventory.Table(): %v", err)
	}

	have := make(map[inventory.Key]bool)
	for _, r := range table {
		if r.Key.Kind == inventory.KindCommand {
			have[r.Key] = true
		}
	}

	for _, h := range ir.EnabledHarnessIDs() {
		for commandID := range commandSkillDirs {
			k := inventory.Key{Harness: h, Kind: inventory.KindCommand, ID: commandID}
			if !have[k] {
				t.Errorf(
					"missing command row: harness=%q id=%q — commandSkillDirs authors this command but the inventory table has no per-harness row; "+
						"regenerate commands.gen.go (make generate) so every command fans out across every enabled harness",
					h, commandID)
			}
			delete(have, k)
		}
	}

	for k := range have {
		t.Errorf(
			"extra command row not backed by commandSkillDirs × enabled harness: harness=%q id=%q — a fabricated command entry; regenerate from the authoring surface",
			k.Harness, k.ID)
	}
}

// TestInventoryNativeFunctionRowsAgreeWithPinnedDerivation proves the
// native-function axis: the committed rows equal, per harness, the
// contract-derived native call names (codex via codexNativeFunctions, opencode
// via deriveOpenCodeNativeToolNames; Claude derives none). Set equality makes
// missing rows, extra rows, AND contradicting rows all fail — the table can
// never override contract-derived native truth.
//
// EXPECTED TO FAIL until L3 emits native_functions.gen.go.
func TestInventoryNativeFunctionRowsAgreeWithPinnedDerivation(t *testing.T) {
	openCodeNative, err := deriveOpenCodeNativeToolNames()
	if err != nil {
		t.Fatalf("deriveOpenCodeNativeToolNames(): %v", err)
	}
	expected := map[ir.HarnessID][]string{
		ir.HarnessCodex:    codexNativeFunctions(),
		ir.HarnessOpenCode: openCodeNative,
	}

	table, err := inventory.Table()
	if err != nil {
		t.Fatalf("inventory.Table(): %v", err)
	}
	got := make(map[ir.HarnessID][]string)
	for _, r := range table {
		if r.Key.Kind == inventory.KindNativeFunction {
			got[r.Key.Harness] = append(got[r.Key.Harness], r.Key.ID)
		}
	}

	for h, want := range expected {
		missing, extra := diffStringSets(want, got[h])
		for _, id := range missing {
			t.Errorf(
				"missing native-function row: harness=%q id=%q — the pinned contract classifies it native but the table omits it; regenerate native_functions.gen.go",
				h, id)
		}
		for _, id := range extra {
			t.Errorf(
				"contradicting native-function row: harness=%q id=%q — committed as native but the pinned contract does NOT classify it native; the table may not override contract-derived truth (CI-reject)",
				h, id)
		}
		delete(got, h)
	}
	for h, ids := range got {
		t.Errorf(
			"native-function rows for harness %q have no pinned derivation source (only codex and opencode derive native calls; Claude derives none): %v",
			h, ids)
	}
}

// TestInventoryNativeContradictionIsDetected pins the CI-reject mechanism
// itself: given the pinned-contract re-derivation, a fabricated native-function
// row that the contract does not classify native is reported as an extra
// (contradiction). This proves the comparison used above turns a contradicting
// committed row red, independent of the committed generated data.
func TestInventoryNativeContradictionIsDetected(t *testing.T) {
	derived := codexNativeFunctions()
	if len(derived) == 0 {
		t.Fatal("codexNativeFunctions() returned no native calls; the pinned Codex contract must classify at least request-input")
	}
	fabricated := append([]string{"fabricated-native-call"}, derived...)

	missing, extra := diffStringSets(derived, fabricated)
	if len(missing) != 0 {
		t.Fatalf("re-derivation lost a genuine native call: %v", missing)
	}
	if len(extra) != 1 || extra[0] != "fabricated-native-call" {
		t.Fatalf("contradiction detection failed: expected the fabricated row flagged as extra, got extra=%v", extra)
	}
}

// diffStringSets returns the elements present in want but absent from got
// (missing) and present in got but absent from want (extra), treating each as a
// set. It is the primitive behind the native-function agreement/contradiction
// checks.
func diffStringSets(want, got []string) (missing, extra []string) {
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
