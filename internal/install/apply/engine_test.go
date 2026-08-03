package apply_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/inventory"
	"github.com/dayvidpham/pasture/internal/install/selection"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func leafBundle(t *testing.T, rel, content string) artifact.Bundle {
	t.Helper()
	p, _ := artifact.NewPath(rel)
	mode, _ := artifact.NewMode(0o644)
	entry, err := artifact.NewFileEntry(p, mode, artifact.DigestBytes([]byte(content)))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := artifact.NewManifest(entry)
	if err != nil {
		t.Fatal(err)
	}
	src := fstest.MapFS{rel: &fstest.MapFile{Data: []byte(content), Mode: 0o644}}
	bundle, err := artifact.NewBundle(src, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

// opencodeContract wires all three OpenCode cells as direct-file activations
// under root.
func opencodeContract(t *testing.T, root string) activation.ActivationContract {
	t.Helper()
	mk := func(axis cell.Extension, rel, content string) activation.ComponentActivation {
		c, _ := cell.New(ir.HarnessOpenCode, axis)
		df, err := activation.NewDirectFile(leafBundle(t, rel, content), root)
		if err != nil {
			t.Fatal(err)
		}
		act, err := activation.NewComponentActivation(c, df)
		if err != nil {
			t.Fatal(err)
		}
		return act
	}
	skills := mk(cell.SkillsAxis(), "skills/pasture/SKILL.md", "# skill\n")
	agents := mk(cell.AgentsAxis(), "agent/pasture.md", "# agent\n")
	hooks := mk(cell.HooksAxis(), "plugin/pasture-hooks.ts", "export default {}\n")
	exhaustive, err := activation.NewExhaustiveComponentActivations(skills, agents, hooks)
	if err != nil {
		t.Fatal(err)
	}
	host, _ := runtime.ParseHostVersion("1.17.18")
	constraint, _ := runtime.NewExactVersion(host)
	probe, _ := activation.NewCommandSchema("opencode", "--version")
	id, _ := activation.NewActivationContractID("opencode/activation@1.17.18")
	contract, err := activation.NewActivationContract(id, ir.HarnessOpenCode, constraint, probe, exhaustive)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func opencodeAllOnSelection(t *testing.T) selection.Selection {
	t.Helper()
	states := map[cell.Cell]bool{}
	for _, c := range cell.CanonicalCells() {
		states[c] = c.Harness() == ir.HarnessOpenCode
	}
	sel, err := selection.New(states)
	if err != nil {
		t.Fatal(err)
	}
	return sel
}

func TestApplySelectionInstallerEnsuresDirectFileCells(t *testing.T) {
	root := t.TempDir()
	engine, err := apply.NewEngine(apply.NewDirectFileActivator(apply.InstallerSource()))
	if err != nil {
		t.Fatal(err)
	}
	contracts := map[ir.HarnessID]activation.ActivationContract{
		ir.HarnessOpenCode: opencodeContract(t, root),
	}
	inv := inventory.New()
	res, applyErr := engine.ApplySelection(opencodeAllOnSelection(t), apply.InstallerSource(), contracts, &inv)
	if applyErr != nil {
		t.Fatalf("pre-plan error: %v", applyErr)
	}
	if !res.OK() {
		t.Fatalf("result not ok: %+v", res.Rows())
	}
	rows := res.Rows()
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// canonical opencode order
	want := []string{"opencode.skills", "opencode.agents", "opencode.hooks"}
	for i, r := range rows {
		if r.Cell().String() != want[i] || r.Status() != apply.Completed() {
			t.Errorf("row %d = %s/%s", i, r.Cell(), r.Status())
		}
	}
	for _, rel := range []string{"skills/pasture/SKILL.md", "agent/pasture.md", "plugin/pasture-hooks.ts"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("leaf not materialized: %s", rel)
		}
	}
	if inv.Len() != 3 {
		t.Errorf("inventory len = %d, want 3", inv.Len())
	}
}

func TestApplySelectionIdempotent(t *testing.T) {
	root := t.TempDir()
	engine, _ := apply.NewEngine(apply.NewDirectFileActivator(apply.InstallerSource()))
	contracts := map[ir.HarnessID]activation.ActivationContract{ir.HarnessOpenCode: opencodeContract(t, root)}
	inv := inventory.New()
	sel := opencodeAllOnSelection(t)
	if _, err := engine.ApplySelection(sel, apply.InstallerSource(), contracts, &inv); err != nil {
		t.Fatal(err)
	}
	res, err := engine.ApplySelection(sel, apply.InstallerSource(), contracts, &inv)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("second apply not ok")
	}
}

func TestApplySelectionRemovesDeselectedManagedCell(t *testing.T) {
	root := t.TempDir()
	engine, _ := apply.NewEngine(apply.NewDirectFileActivator(apply.InstallerSource()))
	contracts := map[ir.HarnessID]activation.ActivationContract{ir.HarnessOpenCode: opencodeContract(t, root)}
	inv := inventory.New()
	if _, err := engine.ApplySelection(opencodeAllOnSelection(t), apply.InstallerSource(), contracts, &inv); err != nil {
		t.Fatal(err)
	}
	// Now deselect everything.
	off := map[cell.Cell]bool{}
	for _, c := range cell.CanonicalCells() {
		off[c] = false
	}
	offSel, _ := selection.New(off)
	res, err := engine.ApplySelection(offSel, apply.InstallerSource(), contracts, &inv)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || len(res.Rows()) != 3 {
		t.Fatalf("remove result: ok=%v rows=%d", res.OK(), len(res.Rows()))
	}
	if _, err := os.Stat(filepath.Join(root, "plugin", "pasture-hooks.ts")); !os.IsNotExist(err) {
		t.Errorf("hooks leaf not removed")
	}
	oc, _ := cell.New(ir.HarnessOpenCode, cell.HooksAxis())
	rec, ok := inv.Lookup(oc)
	if !ok || rec.Observation() != inventory.Absent() {
		t.Errorf("expected absent tombstone for opencode.hooks")
	}
}

func TestApplySelectionUninstallLeavesNoOrphanedDirs(t *testing.T) {
	root := t.TempDir()
	engine, _ := apply.NewEngine(apply.NewDirectFileActivator(apply.InstallerSource()))
	contracts := map[ir.HarnessID]activation.ActivationContract{ir.HarnessOpenCode: opencodeContract(t, root)}
	inv := inventory.New()
	// Full install materializes nested trees: skills/pasture, agent, plugin.
	if _, err := engine.ApplySelection(opencodeAllOnSelection(t), apply.InstallerSource(), contracts, &inv); err != nil {
		t.Fatal(err)
	}
	// The intermediate directory Pasture had to create must be recorded, not
	// just the deepest leaf's parent.
	oc, _ := cell.New(ir.HarnessOpenCode, cell.SkillsAxis())
	rec, ok := inv.Lookup(oc)
	if !ok {
		t.Fatal("opencode.skills record missing after install")
	}
	var recorded []string
	for _, d := range rec.CreatedDirs() {
		recorded = append(recorded, d.String())
	}
	if len(recorded) != 2 || recorded[0] != "skills" || recorded[1] != "skills/pasture" {
		t.Fatalf("recorded created dirs = %v, want [skills skills/pasture]", recorded)
	}

	// Deselect everything and uninstall.
	off := map[cell.Cell]bool{}
	for _, c := range cell.CanonicalCells() {
		off[c] = false
	}
	offSel, _ := selection.New(off)
	res, err := engine.ApplySelection(offSel, apply.InstallerSource(), contracts, &inv)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("uninstall not ok: %+v", res.Rows())
	}
	// Zero orphaned directories anywhere under root.
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Errorf("orphaned entries under root after full uninstall: %v", entries)
	}
}

func TestApplySelectionUninstallPreservesForeignOccupiedDir(t *testing.T) {
	root := t.TempDir()
	engine, _ := apply.NewEngine(apply.NewDirectFileActivator(apply.InstallerSource()))
	contracts := map[ir.HarnessID]activation.ActivationContract{ir.HarnessOpenCode: opencodeContract(t, root)}
	inv := inventory.New()
	if _, err := engine.ApplySelection(opencodeAllOnSelection(t), apply.InstallerSource(), contracts, &inv); err != nil {
		t.Fatal(err)
	}
	// A foreign file moves into a directory Pasture created.
	foreign := filepath.Join(root, "skills", "pasture", "user-notes.md")
	if err := os.WriteFile(foreign, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	off := map[cell.Cell]bool{}
	for _, c := range cell.CanonicalCells() {
		off[c] = false
	}
	offSel, _ := selection.New(off)
	res, err := engine.ApplySelection(offSel, apply.InstallerSource(), contracts, &inv)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("uninstall not ok: %+v", res.Rows())
	}
	// The foreign file survives.
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("foreign file removed during uninstall: %v", err)
	}
	// The occupied directory survives.
	if _, err := os.Stat(filepath.Join(root, "skills", "pasture")); err != nil {
		t.Errorf("foreign-occupied created dir was removed: %v", err)
	}
	// The skills row carries an actionable note naming the preserved dir.
	var skillsRow apply.ActionRow
	found := false
	for _, r := range res.Rows() {
		if r.Cell().String() == "opencode.skills" {
			skillsRow = r
			found = true
		}
	}
	if !found {
		t.Fatal("no opencode.skills row in uninstall result")
	}
	if !strings.Contains(skillsRow.Diagnostic(), "skills/pasture") {
		t.Errorf("skills row diagnostic missing preserved-dir note: %q", skillsRow.Diagnostic())
	}
}

func TestApplySelectionHomeManagerInspectsDirectFileDeclaratively(t *testing.T) {
	root := t.TempDir()
	engine, _ := apply.NewEngine(apply.NewDirectFileActivator(apply.HomeManagerSource()))
	contracts := map[ir.HarnessID]activation.ActivationContract{ir.HarnessOpenCode: opencodeContract(t, root)}
	inv := inventory.New()
	res, err := engine.ApplySelection(opencodeAllOnSelection(t), apply.HomeManagerSource(), contracts, &inv)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Rows() {
		if r.Status() != apply.ManagedDeclaratively() {
			t.Errorf("cell %s status = %s, want managed_declaratively", r.Cell(), r.Status())
		}
	}
	// Home Manager never mutates direct-file destinations or writes inventory.
	if _, err := os.Stat(filepath.Join(root, "plugin", "pasture-hooks.ts")); !os.IsNotExist(err) {
		t.Errorf("home-manager must not materialize a direct-file leaf")
	}
	if inv.Len() != 0 {
		t.Errorf("home-manager direct-file inspection must write no inventory")
	}
}

func TestApplySelectionMissingContractFailsBeforeMutation(t *testing.T) {
	root := t.TempDir()
	engine, _ := apply.NewEngine(apply.NewDirectFileActivator(apply.InstallerSource()))
	// No claude-code contract wired, but claude-code is desired.
	contracts := map[ir.HarnessID]activation.ActivationContract{ir.HarnessOpenCode: opencodeContract(t, root)}
	states := map[cell.Cell]bool{}
	for _, c := range cell.CanonicalCells() {
		states[c] = c.Harness() == ir.HarnessClaudeCode && c.Extension() == cell.SkillsAxis()
	}
	sel, _ := selection.New(states)
	inv := inventory.New()
	_, applyErr := engine.ApplySelection(sel, apply.InstallerSource(), contracts, &inv)
	if applyErr == nil {
		t.Fatal("missing contract for desired cell = nil error, want pre-plan ApplyError")
	}
	// Nothing was materialized.
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Errorf("mutation occurred despite pre-plan failure")
	}
	// Error serializes to apply-error/v1.
	var ae *apply.ApplyError
	if !isApplyError(applyErr, &ae) {
		t.Fatalf("error is not *apply.ApplyError: %T", applyErr)
	}
	data, _ := ae.MarshalJSON()
	if !strings.Contains(string(data), apply.ErrorSchemaID) {
		t.Errorf("apply-error missing schema: %s", data)
	}
}

func isApplyError(err error, target **apply.ApplyError) bool {
	ae, ok := err.(*apply.ApplyError)
	if ok {
		*target = ae
	}
	return ok
}

func TestResultMarshalsFrozenSchemaAndOrder(t *testing.T) {
	root := t.TempDir()
	engine, _ := apply.NewEngine(apply.NewDirectFileActivator(apply.InstallerSource()))
	contracts := map[ir.HarnessID]activation.ActivationContract{ir.HarnessOpenCode: opencodeContract(t, root)}
	inv := inventory.New()
	res, _ := engine.ApplySelection(opencodeAllOnSelection(t), apply.InstallerSource(), contracts, &inv)
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Schema string `json:"schema"`
		OK     bool   `json:"ok"`
		Cells  []struct {
			Cell   string `json:"cell"`
			Status string `json:"status"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != apply.ResultSchemaID {
		t.Errorf("schema = %s", decoded.Schema)
	}
	if len(decoded.Cells) != 3 || decoded.Cells[0].Cell != "opencode.skills" {
		t.Errorf("row order broken: %+v", decoded.Cells)
	}
}
