package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		{ir.HarnessClaudeCode, hostcontract.ClaudeCode2_1_261()},
		{ir.HarnessOpenCode, hostcontract.OpenCode1_18_29()},
		{ir.HarnessCodex, hostcontract.Codex0_153_0()},
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

// TestGeneratedManifestsCarryRuntimeFailureModesVerbatim renders the two
// provider registration manifests with the SAME renderer main() uses and
// asserts two things at once.
//
// First, the rendered text names arms of internal/runtime.FailureMode. The
// registration package no longer declares a failure vocabulary of its own, so
// an arm that is not in the runtime enum cannot be emitted.
//
// Second, the OpenCode rows keep the behavior the runtime profile declares: 15
// named callbacks stay throw-fail-fast and 32 catch-all/SSE observations stay
// observe-only. The removed openCodeFailure helper used to fold those six arms
// into two, which relabelled every named callback as an exit-2 block. A host
// that reads that label would block a session on a plugin that only throws.
func TestGeneratedManifestsCarryRuntimeFailureModesVerbatim(t *testing.T) {
	openCode := string(renderProviderManifest(
		hostcontract.OpenCode1_18_29(), "OpenCode1_18_29", "ir.HarnessOpenCode"))
	codex := string(renderProviderManifest(
		hostcontract.Codex0_153_0(), "Codex0_153_0", "ir.HarnessCodex"))

	for _, rendered := range []string{openCode, codex} {
		if !strings.Contains(rendered, `pastureruntime "github.com/dayvidpham/pasture/internal/runtime"`) {
			t.Fatalf("rendered manifest does not import internal/runtime, so it cannot name a runtime failure arm:\n%s", rendered)
		}
		for _, retired := range []string{"Failure:FailureReportAndContinue", "Failure:FailureExitTwoBlocks"} {
			if strings.Contains(rendered, retired) {
				t.Errorf(
					"rendered manifest still writes the retired registration-local arm %q; every row must name an arm of internal/runtime.FailureMode",
					retired)
			}
		}
	}

	counts := map[string]int{}
	for _, line := range strings.Split(openCode, "\n") {
		index := strings.Index(line, "Failure:")
		if index < 0 {
			continue
		}
		rest := line[index+len("Failure:"):]
		end := strings.IndexAny(rest, ",}")
		if end < 0 {
			t.Fatalf("cannot read the failure arm out of rendered row %q", line)
		}
		counts[rest[:end]]++
	}
	want := map[string]int{
		"pastureruntime.FailureThrowFailFast": 15,
		"pastureruntime.FailureObserveOnly":   32,
	}
	if len(counts) != len(want) {
		t.Fatalf("OpenCode rows use failure arms %v, want exactly %v", counts, want)
	}
	for arm, wantCount := range want {
		if counts[arm] != wantCount {
			t.Errorf(
				"OpenCode rows carry %d %s rows, want %d; a lossy collapse of the runtime failure vocabulary would change this count",
				counts[arm], arm, wantCount)
		}
	}
}

// TestGeneratedNamesFollowTheContractVersion pins the moved-ceiling rule as a
// mechanism: the generated registration and payload files, and the manifest
// functions they declare, are named from Contract.Version. The expectation is
// derived from the contracts, so moving a version moves the expected names with
// it; a generated file that kept an old name, or a hand-named one, turns RED.
func TestGeneratedNamesFollowTheContractVersion(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		contract     hostcontract.Contract
		registration string
		payload      string
		function     string
	}{
		{hostcontract.ClaudeCode2_1_261(), "internal/lifecycle/registration/claude_TOKEN.gen.go", "internal/lifecycle/ingress/claude/payload_TOKEN.gen.go", "func ClaudeCodeTOKEN() Manifest"},
		{hostcontract.OpenCode1_18_29(), "internal/lifecycle/registration/opencode_TOKEN.gen.go", "internal/lifecycle/ingress/opencode/payload_TOKEN.gen.go", "func OpenCodeTOKEN() Manifest"},
		{hostcontract.Codex0_153_0(), "internal/lifecycle/registration/codex_TOKEN.gen.go", "internal/lifecycle/ingress/codex/payload_TOKEN.gen.go", "func CodexTOKEN() Manifest"},
	} {
		token := versionToken(tc.contract)
		if token == "" || token != strings.ReplaceAll(tc.contract.Version, ".", "_") {
			t.Fatalf("versionToken(%q) = %q", tc.contract.Version, token)
		}
		registration := strings.ReplaceAll(tc.registration, "TOKEN", token)
		payload := strings.ReplaceAll(tc.payload, "TOKEN", token)
		for _, rel := range []string{registration, payload} {
			if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
				t.Errorf("the generated file named from Contract.Version %q is not committed: %v", tc.contract.Version, err)
			}
		}
		body, err := os.ReadFile(filepath.Join(root, registration))
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.ReplaceAll(tc.function, "TOKEN", token); !strings.Contains(string(body), want) {
			t.Errorf("the generated registration for version %q does not declare %q; the function name did not follow the version", tc.contract.Version, want)
		}
	}
}
