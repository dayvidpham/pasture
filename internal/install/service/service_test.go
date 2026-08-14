package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/selection"
	"github.com/dayvidpham/pasture/internal/install/service"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func bundle(t *testing.T, rel, content string) artifact.Bundle {
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
	b, err := artifact.NewBundle(fstest.MapFS{rel: &fstest.MapFile{Data: []byte(content), Mode: 0o644}}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func contracts(t *testing.T, root string) map[ir.HarnessID]activation.ActivationContract {
	t.Helper()
	out := map[ir.HarnessID]activation.ActivationContract{}
	for _, harness := range cell.CanonicalHarnesses() {
		mk := func(axis cell.Extension) activation.ComponentActivation {
			c, _ := cell.New(harness, axis)
			rel := filepath.ToSlash(filepath.Join(string(harness), axis.String(), "component.txt"))
			df, err := activation.NewDirectFile(bundle(t, rel, c.String()+"\n"), root)
			if err != nil {
				t.Fatal(err)
			}
			a, err := activation.NewComponentActivation(c, df)
			if err != nil {
				t.Fatal(err)
			}
			return a
		}
		exhaustive, err := activation.NewExhaustiveComponentActivations(mk(cell.SkillsAxis()), mk(cell.AgentsAxis()), mk(cell.HooksAxis()))
		if err != nil {
			t.Fatal(err)
		}
		version, _ := runtime.ParseHostVersion("1.0.0")
		versions, _ := runtime.NewExactVersion(version)
		probe, _ := activation.NewCommandSchema("host", "--version")
		id, _ := activation.NewActivationContractID(string(harness) + "/activation@1")
		contract, err := activation.NewActivationContract(id, harness, versions, probe, exhaustive)
		if err != nil {
			t.Fatal(err)
		}
		out[harness] = contract
	}
	return out
}

func all(t *testing.T, enabled func(cell.Cell) bool) selection.Selection {
	t.Helper()
	states := map[cell.Cell]bool{}
	for _, c := range cell.CanonicalCells() {
		states[c] = enabled(c)
	}
	sel, err := selection.New(states)
	if err != nil {
		t.Fatal(err)
	}
	return sel
}

func newService(t *testing.T, root, state string) *service.Service {
	t.Helper()
	repo, err := service.NewFileRegistry(state)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Config{Registry: repo, Contracts: contracts(t, root), Activators: []apply.Activator{apply.NewDirectFileActivator()}})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestServiceApplySelectionPersistsCanonicalFacts(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state", "installations.yaml")
	svc := newService(t, root, state)
	res, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || len(res.Rows()) != 9 {
		t.Fatalf("result ok=%v rows=%d", res.OK(), len(res.Rows()))
	}
	for i, row := range res.Rows() {
		if row.Cell().Index() != i || row.Status() != apply.Completed() {
			t.Fatalf("row %d = %s/%s", i, row.Cell(), row.Status())
		}
	}
	store, err := registry.Load(state)
	if err != nil {
		t.Fatal(err)
	}
	if store.Len() != 9 {
		t.Fatalf("persisted records=%d, want 9", store.Len())
	}
}

func TestServiceStopsAtFirstFailureAndKeepsEarlierFact(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(root, "claude-code", "agents")
	if err := os.MkdirAll(conflict, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflict, "component.txt"), []byte("user-owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state", "installations.yaml")
	svc := newService(t, root, state)
	res, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("conflicting selection unexpectedly succeeded")
	}
	rows := res.Rows()
	if rows[0].Status() != apply.Completed() || rows[1].Status() != apply.Failed() || rows[2].Status() != apply.Unattempted() {
		t.Fatalf("first rows = %s/%s/%s", rows[0].Status(), rows[1].Status(), rows[2].Status())
	}
	store, loadErr := registry.Load(state)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if store.Len() != 1 {
		t.Fatalf("persisted records=%d, want only first confirmed fact", store.Len())
	}
	if got, _ := os.ReadFile(filepath.Join(conflict, "component.txt")); string(got) != "user-owned\n" {
		t.Fatal("external conflict was modified")
	}
}

func TestServicePreservesExactExternalMatchWhenDisabled(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.MkdirAll(filepath.Join(root, "opencode", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(root, "opencode", "skills", "component.txt")
	if err := os.WriteFile(leaf, []byte("opencode.skills\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state", "installations.yaml")
	svc := newService(t, root, state)
	c, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	first, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil || !first.OK() {
		t.Fatalf("ensure external: %v %+v", err, first.Rows())
	}
	store, _ := registry.Load(state)
	key, _ := registry.GlobalKey(c)
	record, ok := store.Lookup(key)
	if !ok || record.Managed() {
		t.Fatal("exact external match was adopted")
	}
	second, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil || !second.OK() || second.Rows()[0].Status() != apply.NoOp() {
		t.Fatalf("disable external: %v %+v", err, second.Rows())
	}
	if _, err := os.Stat(leaf); err != nil {
		t.Fatalf("external match removed: %v", err)
	}
}

func TestServiceHomeManagerInspectsWithoutOwnershipOrMutation(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state", "installations.yaml")
	svc := newService(t, root, state)
	res, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return false }), Scope: apply.GlobalScope(), Source: apply.HomeManagerSource()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || len(res.Rows()) != 9 {
		t.Fatalf("result ok=%v rows=%d", res.OK(), len(res.Rows()))
	}
	for _, row := range res.Rows() {
		if row.Status() != apply.ManagedDeclaratively() || row.Observation() != registry.ObservationAbsent {
			t.Fatalf("row %s = %s/%s", row.Cell(), row.Status(), row.Observation())
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("declarative inspection persisted ownership: %v", err)
	}
}

func TestServiceProjectScopeUsesDistinctRegistryTable(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	project := filepath.Join(base, "project")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	projectRoot, err := registry.CanonicalProjectRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := apply.ProjectScope(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state", "installations.yaml")
	svc := newService(t, root, state)
	c, _ := cell.New(artifact.HarnessCodex, cell.AgentsAxis())
	res, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: scope, Source: apply.InstallerSource()})
	if err != nil || !res.OK() {
		t.Fatalf("project apply: %v %+v", err, res.Rows())
	}
	store, _ := registry.Load(state)
	projectKey, _ := registry.ProjectKey(projectRoot, c)
	if _, ok := store.Lookup(projectKey); !ok {
		t.Fatal("project fact missing")
	}
	globalKey, _ := registry.GlobalKey(c)
	if _, ok := store.Lookup(globalKey); ok {
		t.Fatal("project fact leaked into global table")
	}
}
