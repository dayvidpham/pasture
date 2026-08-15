package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	return contractsWith(t, root, func(c cell.Cell) activation.ActivationStrategy {
		rel := filepath.ToSlash(filepath.Join(string(c.Harness()), c.Extension().String(), "component.txt"))
		df, err := activation.NewDirectFile(bundle(t, rel, c.String()+"\n"), root)
		if err != nil {
			t.Fatal(err)
		}
		return df
	})
}

func contractsWith(t *testing.T, root string, strategy func(cell.Cell) activation.ActivationStrategy) map[ir.HarnessID]activation.ActivationContract {
	t.Helper()
	out := map[ir.HarnessID]activation.ActivationContract{}
	for _, harness := range cell.CanonicalHarnesses() {
		mk := func(axis cell.Extension) activation.ComponentActivation {
			c, _ := cell.New(harness, axis)
			a, err := activation.NewComponentActivation(c, strategy(c))
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

func newServiceConfig(t *testing.T, root, state string, config service.Config) *service.Service {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if config.Registry == nil {
		repo, err := service.NewFileRegistry(state)
		if err != nil {
			t.Fatal(err)
		}
		config.Registry = repo
	}
	if config.Contracts == nil {
		config.Contracts = contracts(t, root)
	}
	if config.Activators == nil {
		config.Activators = []apply.Activator{directFileActivator(t, config.Contracts)}
	}
	svc, err := service.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func directFileActivator(t *testing.T, contracts map[ir.HarnessID]activation.ActivationContract) *apply.DirectFileActivator {
	t.Helper()
	policies := make([]apply.DirectFilePolicy, 0, 9)
	for _, c := range cell.CanonicalCells() {
		contract, ok := contracts[c.Harness()]
		if !ok {
			continue
		}
		descriptor, _ := activation.NewComponentDescriptor(c)
		binding, err := activation.LookupComponentActivation(contract, descriptor)
		if err != nil || binding.Strategy().Kind() != activation.DirectFileKindValue() {
			continue
		}
		policy, err := apply.PassThroughDirectFile(c)
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, policy)
	}
	activator, err := apply.NewDirectFileActivator(policies...)
	if err != nil {
		t.Fatal(err)
	}
	return activator
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
	return newServiceConfig(t, root, state, service.Config{})
}

func TestServiceConstructionRequiresExactDirectFilePoliciesAndOneActivator(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	state := filepath.Join(base, "state", "installations.yaml")
	repo, _ := service.NewFileRegistry(state)
	contractSet := contracts(t, root)
	policies := make([]apply.DirectFilePolicy, 0, 9)
	for _, c := range cell.CanonicalCells() {
		policy, _ := apply.PassThroughDirectFile(c)
		policies = append(policies, policy)
	}
	missing, _ := apply.NewDirectFileActivator(policies[:8]...)
	if _, err := service.New(service.Config{Registry: repo, Contracts: contractSet, Activators: []apply.Activator{missing}}); err == nil {
		t.Fatal("service accepted missing DirectFile policy")
	}
	complete, _ := apply.NewDirectFileActivator(policies...)
	if _, err := service.New(service.Config{Registry: repo, Contracts: contractSet, Activators: []apply.Activator{complete, complete}}); err == nil {
		t.Fatal("service accepted duplicate strategy activators")
	}
	nativeCell, _ := cell.New(artifact.HarnessCodex, cell.HooksAxis())
	mixed := contractsWith(t, root, func(c cell.Cell) activation.ActivationStrategy {
		if c == nativeCell {
			return nativeStrategy(t, c)
		}
		rel := filepath.ToSlash(filepath.Join(string(c.Harness()), c.Extension().String(), "component.txt"))
		direct, err := activation.NewDirectFile(bundle(t, rel, c.String()+"\n"), root)
		if err != nil {
			t.Fatal(err)
		}
		return direct
	})
	if _, err := service.New(service.Config{Registry: repo, Contracts: mixed, Activators: []apply.Activator{complete, &nativeSpy{}}}); err == nil {
		t.Fatal("service accepted a policy for a non-DirectFile cell")
	}
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
	for _, row := range rows[2:] {
		if row.Status() != apply.Unattempted() {
			t.Fatalf("later row %s status=%s", row.Cell(), row.Status())
		}
		later := filepath.Join(root, string(row.Cell().Harness()), row.Cell().Extension().String(), "component.txt")
		if _, err := os.Lstat(later); !os.IsNotExist(err) {
			t.Fatalf("later path %s was attempted: %v", later, err)
		}
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

func TestServiceAllFalsePreservesRequestScopeAndExactJSON(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		scope apply.Scope
		want  string
	}{
		{name: "global", scope: apply.GlobalScope(), want: "global"},
		{name: "project", scope: projectScope(t), want: "project"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, "targets")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			svc := newService(t, root, filepath.Join(base, "state", "installations.yaml"))
			result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return false }), Scope: tc.scope, Source: apply.InstallerSource()})
			if err != nil || !result.OK() || result.Scope().String() != tc.want || len(result.Rows()) != 0 {
				t.Fatalf("result=%+v rows=%v err=%v", result, result.Rows(), err)
			}
			encoded, err := result.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			want := "{\n  \"schema\": \"pasture.install.apply-result/v1\",\n  \"source\": \"installer\",\n  \"scope\": \"" + tc.want + "\",\n  \"ok\": true,\n  \"cells\": null\n}"
			if string(encoded) != want {
				t.Fatalf("json mismatch\n got: %s\nwant: %s", encoded, want)
			}
		})
	}
}

func TestServiceApplySelectionAndApplyCellAreEquivalent(t *testing.T) {
	t.Parallel()
	c, _ := cell.New(artifact.HarnessOpenCode, cell.AgentsAxis())
	applyOne := func(t *testing.T, selectionMode bool) (apply.ActionRow, []byte, registry.Record) {
		t.Helper()
		base := t.TempDir()
		root := filepath.Join(base, "targets")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		state := filepath.Join(base, "state", "installations.yaml")
		svc := newService(t, root, state)
		var result apply.Result
		var err error
		if selectionMode {
			result, err = svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(candidate cell.Cell) bool { return candidate == c }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
		} else {
			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
		}
		if err != nil || !result.OK() || len(result.Rows()) != 1 {
			t.Fatalf("apply failed: %v %+v", err, result.Rows())
		}
		live, err := os.ReadFile(filepath.Join(root, "opencode", "agents", "component.txt"))
		if err != nil {
			t.Fatal(err)
		}
		store, err := registry.Load(state)
		if err != nil {
			t.Fatal(err)
		}
		key, _ := registry.GlobalKey(c)
		record, ok := store.Lookup(key)
		if !ok {
			t.Fatal("record missing")
		}
		return result.Rows()[0], live, record
	}
	selectionRow, selectionBytes, selectionRecord := applyOne(t, true)
	cellRow, cellBytes, cellRecord := applyOne(t, false)
	if selectionRow.Status() != cellRow.Status() || selectionRow.Operation() != cellRow.Operation() || selectionRow.Management() != cellRow.Management() || selectionRow.Observation() != cellRow.Observation() || selectionRow.Diagnostic() != cellRow.Diagnostic() || !bytes.Equal(selectionBytes, cellBytes) {
		t.Fatalf("selection row=%+v cell row=%+v bytes=%q/%q", selectionRow, cellRow, selectionBytes, cellBytes)
	}
	if selectionRecord.Source() != cellRecord.Source() || selectionRecord.Managed() != cellRecord.Managed() || selectionRecord.Observation() != cellRecord.Observation() || selectionRecord.ArtifactID() != cellRecord.ArtifactID() {
		t.Fatalf("records differ: %+v %+v", selectionRecord, cellRecord)
	}
}

func TestServiceApplySelectionAndApplyCellCompleteStateMatrix(t *testing.T) {
	t.Parallel()
	type scenario struct {
		name    string
		enabled bool
		prepare func(*testing.T, *service.Service, string, string, apply.Scope, cell.Cell)
	}
	scenarios := []scenario{
		{name: "absent-install", enabled: true},
		{name: "managed-current-idempotent", enabled: true, prepare: installManagedCell},
		{name: "managed-installed-remove", enabled: false, prepare: installManagedCell},
		{name: "managed-unknown-remove", enabled: false, prepare: func(t *testing.T, svc *service.Service, root, state string, scope apply.Scope, c cell.Cell) {
			installManagedCell(t, svc, root, state, scope, c)
			store, err := registry.Load(state)
			if err != nil {
				t.Fatal(err)
			}
			key, _ := scope.Key(c)
			current, ok := store.Lookup(key)
			if !ok {
				t.Fatal("managed fixture missing")
			}
			unknown, err := registry.NewRecord(registry.RecordInput{Key: current.Key(), Source: current.Source(), Strategy: current.Strategy(), Managed: current.Managed(), ArtifactID: current.ArtifactID(), Version: current.Version(), Selector: current.Selector(), Leaves: current.Leaves(), CreatedDirs: current.CreatedDirs(), SharedConfig: current.SharedConfig(), Observation: registry.ObservationUnknown, Trust: current.Trust(), LastOperation: registry.OperationRemove, LastOutcome: registry.OutcomeFailed, Diagnostic: "interrupted prior removal"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Upsert(unknown); err != nil {
				t.Fatal(err)
			}
			if err := registry.Save(state, store); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, scopeName := range []string{"global", "project"} {
		scopeName := scopeName
		for _, scenario := range scenarios {
			scenario := scenario
			t.Run(scopeName+"/"+scenario.name, func(t *testing.T) {
				t.Parallel()
				type observed struct {
					result       apply.Result
					tree, record string
				}
				run := func(t *testing.T, selectionMode bool) observed {
					t.Helper()
					base := t.TempDir()
					root := filepath.Join(base, "targets")
					state := filepath.Join(base, "state", "installations.yaml")
					if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
						t.Fatal(err)
					}
					var scope apply.Scope
					if scopeName == "project" {
						project := filepath.Join(base, "project")
						if err := os.Mkdir(project, 0o755); err != nil {
							t.Fatal(err)
						}
						projectRoot, err := registry.CanonicalProjectRoot(project)
						if err != nil {
							t.Fatal(err)
						}
						scope, err = apply.ProjectScope(projectRoot)
						if err != nil {
							t.Fatal(err)
						}
					} else {
						scope = apply.GlobalScope()
					}
					svc := newService(t, root, state)
					c, _ := cell.New(artifact.HarnessOpenCode, cell.AgentsAxis())
					if scenario.prepare != nil {
						scenario.prepare(t, svc, root, state, scope, c)
					}
					var result apply.Result
					var err error
					if selectionMode {
						result, err = svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(candidate cell.Cell) bool { return candidate == c && scenario.enabled }), Scope: scope, Source: apply.InstallerSource()})
					} else {
						result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: scenario.enabled, Scope: scope, Source: apply.InstallerSource()})
					}
					if err != nil || !result.OK() || len(result.Rows()) != 1 {
						t.Fatalf("result=%+v err=%v", result.Rows(), err)
					}
					store, err := registry.Load(state)
					if err != nil {
						t.Fatal(err)
					}
					key, _ := scope.Key(c)
					record, ok := store.Lookup(key)
					if !ok {
						t.Fatal("result record missing")
					}
					return observed{result: result, tree: snapshotTree(t, root), record: recordSignature(record)}
				}
				selectionResult := run(t, true)
				cellResult := run(t, false)
				selectionJSON, _ := selectionResult.result.MarshalJSON()
				cellJSON, _ := cellResult.result.MarshalJSON()
				if !bytes.Equal(selectionJSON, cellJSON) || selectionResult.tree != cellResult.tree || selectionResult.record != cellResult.record {
					t.Fatalf("entry points diverged\nselection result=%s\ncell result=%s\nselection tree=%s\ncell tree=%s\nselection record=%s\ncell record=%s", selectionJSON, cellJSON, selectionResult.tree, cellResult.tree, selectionResult.record, cellResult.record)
				}
			})
		}
	}
}

func TestServiceExternalDesiredFalseParityPreservesFilesAndAuthority(t *testing.T) {
	t.Parallel()
	for _, scopeName := range []string{"global", "project"} {
		scopeName := scopeName
		for _, selectionMode := range []bool{true, false} {
			selectionMode := selectionMode
			t.Run(scopeName+"/selection="+fmtBool(selectionMode), func(t *testing.T) {
				t.Parallel()
				base := t.TempDir()
				root := filepath.Join(base, "targets")
				state := filepath.Join(base, "state", "installations.yaml")
				leafDir := filepath.Join(root, "opencode", "agents")
				if err := os.MkdirAll(leafDir, 0o755); err != nil {
					t.Fatal(err)
				}
				leaf := filepath.Join(leafDir, "component.txt")
				if err := os.WriteFile(leaf, []byte("opencode.agents\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				var scope apply.Scope
				if scopeName == "project" {
					project := filepath.Join(base, "project")
					if err := os.Mkdir(project, 0o755); err != nil {
						t.Fatal(err)
					}
					projectRoot, _ := registry.CanonicalProjectRoot(project)
					scope, _ = apply.ProjectScope(projectRoot)
				} else {
					scope = apply.GlobalScope()
				}
				svc := newService(t, root, state)
				before := snapshotTree(t, base)
				c, _ := cell.New(artifact.HarnessOpenCode, cell.AgentsAxis())
				var result apply.Result
				var err error
				if selectionMode {
					result, err = svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return false }), Scope: scope, Source: apply.InstallerSource()})
				} else {
					result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: false, Scope: scope, Source: apply.InstallerSource()})
				}
				if err != nil || !result.OK() {
					t.Fatalf("result=%+v err=%v", result.Rows(), err)
				}
				if selectionMode && len(result.Rows()) != 0 {
					t.Fatalf("selection rows=%+v", result.Rows())
				}
				if !selectionMode && (len(result.Rows()) != 1 || result.Rows()[0].Status() != apply.NoOp()) {
					t.Fatalf("cell rows=%+v", result.Rows())
				}
				if after := snapshotTree(t, base); after != before {
					t.Fatalf("external state changed\nbefore=%s\nafter=%s", before, after)
				}
				if _, err := os.Stat(state); !os.IsNotExist(err) {
					t.Fatalf("external preservation persisted authority: %v", err)
				}
			})
		}
	}
}

func TestServiceSingleCellResultJSONIsCanonical(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := newService(t, root, filepath.Join(base, "state", "installations.yaml"))
	c, _ := cell.New(artifact.HarnessOpenCode, cell.AgentsAxis())
	result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"schema\": \"pasture.install.apply-result/v1\",\n  \"source\": \"installer\",\n  \"scope\": \"global\",\n  \"ok\": true,\n  \"cells\": [\n    {\n      \"index\": 4,\n      \"cell\": \"opencode.agents\",\n      \"harness\": \"opencode\",\n      \"extension\": \"agents\",\n      \"operation\": \"ensure\",\n      \"status\": \"completed\",\n      \"management\": \"pasture_managed\",\n      \"observation\": \"installed\"\n    }\n  ]\n}"
	if string(encoded) != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", encoded, want)
	}
}

func TestServiceManagedRemovalIsScopedIdempotentAndPreservesForeignContent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		scope apply.Scope
	}{
		{name: "global", scope: apply.GlobalScope()},
		{name: "project", scope: projectScope(t)},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, "targets")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(base, "state", "installations.yaml")
			svc := newService(t, root, state)
			c, _ := cell.New(artifact.HarnessCodex, cell.AgentsAxis())
			request := service.CellRequest{Cell: c, Enabled: true, Scope: tc.scope, Source: apply.InstallerSource()}
			first, err := svc.ApplyCell(context.Background(), request)
			if err != nil || !first.OK() {
				t.Fatalf("install: %v %+v", err, first.Rows())
			}
			second, err := svc.ApplyCell(context.Background(), request)
			if err != nil || !second.OK() || second.Rows()[0].Status() != apply.Completed() {
				t.Fatalf("idempotent install: %v %+v", err, second.Rows())
			}
			foreign := filepath.Join(root, "codex", "agents", "foreign.txt")
			if err := os.WriteFile(foreign, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			request.Enabled = false
			removed, err := svc.ApplyCell(context.Background(), request)
			if err != nil || !removed.OK() || removed.Rows()[0].Observation() != registry.ObservationAbsent {
				t.Fatalf("remove: %v %+v", err, removed.Rows())
			}
			if got, err := os.ReadFile(foreign); err != nil || string(got) != "preserve" {
				t.Fatalf("foreign content changed: %q %v", got, err)
			}
			reloaded, err := registry.Load(state)
			if err != nil {
				t.Fatal(err)
			}
			key, err := tc.scope.Key(c)
			if err != nil {
				t.Fatal(err)
			}
			tombstone, ok := reloaded.Lookup(key)
			if !ok || reloaded.Len() != 1 || tombstone.Key() != key || tombstone.Source() != registry.SourceInstaller || tombstone.Strategy() != activation.DirectFileKindValue() || !tombstone.Managed() || tombstone.ArtifactID().String() == "" || len(tombstone.Leaves()) != 0 || len(tombstone.CreatedDirs()) != 0 || len(tombstone.SharedConfig()) != 0 || tombstone.Observation() != registry.ObservationAbsent || tombstone.Trust() != registry.TrustNotApplicable || tombstone.LastOperation() != registry.OperationRemove || tombstone.LastOutcome() != registry.OutcomeCompleted || tombstone.Diagnostic() == "" {
				t.Fatalf("incomplete scoped tombstone: %+v", tombstone)
			}
			again, err := svc.ApplyCell(context.Background(), request)
			if err != nil || !again.OK() || again.Rows()[0].Status() != apply.NoOp() {
				t.Fatalf("idempotent remove: %v %+v", err, again.Rows())
			}
		})
	}
}

func TestServiceRemovalPreservesSiblingAndOppositeScopeRecords(t *testing.T) {
	t.Parallel()
	for _, requestedScope := range []string{"global", "project"} {
		requestedScope := requestedScope
		t.Run(requestedScope, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, "targets")
			state := filepath.Join(base, "state", "installations.yaml")
			project := filepath.Join(base, "project")
			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}
			projectRoot, _ := registry.CanonicalProjectRoot(project)
			projectScope, _ := apply.ProjectScope(projectRoot)
			globalScope := apply.GlobalScope()
			scope := globalScope
			opposite := projectScope
			if requestedScope == "project" {
				scope, opposite = projectScope, globalScope
			}
			svc := newService(t, root, state)
			c, _ := cell.New(artifact.HarnessCodex, cell.AgentsAxis())
			installManagedCell(t, svc, root, state, scope, c)
			store, err := registry.Load(state)
			if err != nil {
				t.Fatal(err)
			}
			sibling, _ := cell.New(artifact.HarnessOpenCode, cell.HooksAxis())
			siblingKey, _ := scope.Key(sibling)
			oppositeKey, _ := opposite.Key(c)
			for _, key := range []registry.Key{siblingKey, oppositeKey} {
				record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: false, Observation: registry.ObservationAbsent, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationInspect, LastOutcome: registry.OutcomeCompleted, Diagnostic: "preserved sentinel"})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Upsert(record); err != nil {
					t.Fatal(err)
				}
			}
			if err := registry.Save(state, store); err != nil {
				t.Fatal(err)
			}
			beforeSibling, _ := store.Lookup(siblingKey)
			beforeOpposite, _ := store.Lookup(oppositeKey)
			result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: false, Scope: scope, Source: apply.InstallerSource()})
			if err != nil || !result.OK() {
				t.Fatalf("remove=%+v err=%v", result.Rows(), err)
			}
			after, err := registry.Load(state)
			if err != nil {
				t.Fatal(err)
			}
			afterSibling, okSibling := after.Lookup(siblingKey)
			afterOpposite, okOpposite := after.Lookup(oppositeKey)
			if !okSibling || !okOpposite || after.Len() != 3 || recordSignature(afterSibling) != recordSignature(beforeSibling) || recordSignature(afterOpposite) != recordSignature(beforeOpposite) {
				t.Fatalf("unrelated records changed\nsibling before=%s after=%s\nopposite before=%s after=%s", recordSignature(beforeSibling), recordSignature(afterSibling), recordSignature(beforeOpposite), recordSignature(afterOpposite))
			}
		})
	}
}

func TestServiceManagedUnknownRemovalDoesNotClaimAbsentWhileExternalBundleRemains(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	leafDir := filepath.Join(root, "opencode", "skills")
	if err := os.MkdirAll(leafDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leafDir, "component.txt"), []byte("opencode.skills\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state", "installations.yaml")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	c, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	key, _ := registry.GlobalKey(c)
	record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true, Observation: registry.ObservationUnknown, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationRemove, LastOutcome: registry.OutcomeFailed, Diagnostic: "prior removal uncertain"})
	if err != nil {
		t.Fatal(err)
	}
	store := registry.New()
	_ = store.Upsert(record)
	if err := registry.Save(state, store); err != nil {
		t.Fatal(err)
	}
	svc := newService(t, root, state)
	result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil || result.OK() || result.Rows()[0].Observation() != registry.ObservationInstalled || result.Rows()[0].Status() != apply.Failed() {
		t.Fatalf("result=%+v err=%v", result.Rows(), err)
	}
	reloaded, err := registry.Load(state)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reloaded.Lookup(key)
	if got.Observation() != registry.ObservationInstalled || got.LastOutcome() != registry.OutcomeFailed || got.Managed() || len(got.Leaves()) != 1 {
		t.Fatalf("persisted observation=%s outcome=%s", got.Observation(), got.LastOutcome())
	}
	retry, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil || !retry.OK() || retry.Rows()[0].Status() != apply.NoOp() {
		t.Fatalf("external retry=%+v err=%v", retry.Rows(), err)
	}
	if content, err := os.ReadFile(filepath.Join(leafDir, "component.txt")); err != nil || string(content) != "opencode.skills\n" {
		t.Fatalf("external leaf changed on retry: %q err=%v", content, err)
	}
}

func TestApplyErrorTypedAccessorsMatchFrozenJSON(t *testing.T) {
	t.Parallel()
	err := apply.NewApplyError(apply.HomeManagerSource(), "contract", "missing activation", "service.bind", "nothing changed", "wire all cells", apply.RemediationRerunHomeManager)
	if err.Source() != apply.HomeManagerSource() || err.Stage() != "contract" || err.Reason() != "missing activation" || err.Location() != "service.bind" || err.Impact() != "nothing changed" || err.Fix() != "wire all cells" || err.Remediation() != apply.RemediationRerunHomeManager {
		t.Fatalf("typed accessors lost data: %#v", err)
	}
	encoded, marshalErr := err.MarshalJSON()
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	want := "{\n  \"schema\": \"pasture.install.apply-error/v1\",\n  \"source\": \"home-manager\",\n  \"stage\": \"contract\",\n  \"reason\": \"missing activation\",\n  \"where\": \"service.bind\",\n  \"impact\": \"nothing changed\",\n  \"fix\": \"wire all cells\",\n  \"remediation\": \"rerun_home_manager\"\n}"
	if string(encoded) != want {
		t.Fatalf("error JSON mismatch\n got: %s\nwant: %s", encoded, want)
	}
}

func TestServiceRejectsSymlinkedDestinationRootForEnsureAndRemove(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{true, false} {
		enabled := enabled
		t.Run(fmtBool(enabled), func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			target := filepath.Join(base, "target")
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "root-link")
			if err := os.Symlink(target, root); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(base, "state", "installations.yaml")
			c, _ := cell.New(artifact.HarnessOpenCode, cell.HooksAxis())
			if !enabled {
				key, _ := registry.GlobalKey(c)
				record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true, Observation: registry.ObservationUnknown, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationRemove, LastOutcome: registry.OutcomeFailed})
				if err != nil {
					t.Fatal(err)
				}
				store := registry.New()
				_ = store.Upsert(record)
				if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := registry.Save(state, store); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadDir(target)
			svc := newService(t, root, state)
			result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: enabled, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			if err != nil || result.OK() || !strings.Contains(result.Rows()[0].Diagnostic(), "no-follow") {
				t.Fatalf("result=%+v err=%v", result.Rows(), err)
			}
			after, _ := os.ReadDir(target)
			if len(before) != len(after) {
				t.Fatalf("symlink target changed: before=%v after=%v", before, after)
			}
		})
	}
}

func TestServiceDirectFileFirstInstallCreatesMissingRootAndReclaimsOwnedDescendants(t *testing.T) {
	t.Parallel()
	for _, scope := range []struct {
		name  string
		value func(*testing.T) apply.Scope
	}{
		{name: "global", value: func(*testing.T) apply.Scope { return apply.GlobalScope() }},
		{name: "project", value: func(t *testing.T) apply.Scope { return projectScope(t) }},
	} {
		scope := scope
		t.Run(scope.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, "missing", "targets")
			state := filepath.Join(base, "state", "installations.yaml")
			svc := newService(t, root, state)
			c, _ := cell.New(artifact.HarnessOpenCode, cell.AgentsAxis())
			request := service.CellRequest{Cell: c, Enabled: true, Scope: scope.value(t), Source: apply.InstallerSource()}
			installed, err := svc.ApplyCell(context.Background(), request)
			if err != nil || !installed.OK() || installed.Rows()[0].Status() != apply.Completed() {
				t.Fatalf("install=%+v err=%v", installed.Rows(), err)
			}
			leaf := filepath.Join(root, "opencode", "agents", "component.txt")
			if content, err := os.ReadFile(leaf); err != nil || string(content) != "opencode.agents\n" {
				t.Fatalf("leaf=%q err=%v", content, err)
			}
			request.Enabled = false
			removed, err := svc.ApplyCell(context.Background(), request)
			if err != nil || !removed.OK() || removed.Rows()[0].Observation() != registry.ObservationAbsent {
				t.Fatalf("remove=%+v err=%v", removed.Rows(), err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("owned descendants remain: %v err=%v", entries, err)
			}
		})
	}
}

func TestServiceDirectFileRejectsRootIntermediateAndLeafSymlinksWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"root", "intermediate", "leaf"} {
		boundary := boundary
		for _, enabled := range []bool{true, false} {
			enabled := enabled
			for _, scopeName := range []string{"global", "project"} {
				scopeName := scopeName
				t.Run(boundary+"/"+fmtBool(enabled)+"/"+scopeName, func(t *testing.T) {
					t.Parallel()
					base := t.TempDir()
					outside := filepath.Join(base, "outside")
					if err := os.Mkdir(outside, 0o750); err != nil {
						t.Fatal(err)
					}
					outsideLeaf := filepath.Join(outside, "occupied")
					if err := os.WriteFile(outsideLeaf, []byte("external"), 0o640); err != nil {
						t.Fatal(err)
					}
					root := filepath.Join(base, "targets")
					switch boundary {
					case "root":
						if err := os.Symlink(outside, root); err != nil {
							t.Fatal(err)
						}
					case "intermediate":
						if err := os.Mkdir(root, 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(outside, filepath.Join(root, "opencode")); err != nil {
							t.Fatal(err)
						}
					case "leaf":
						if err := os.MkdirAll(filepath.Join(root, "opencode", "agents"), 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(outsideLeaf, filepath.Join(root, "opencode", "agents", "component.txt")); err != nil {
							t.Fatal(err)
						}
					}
					state := filepath.Join(base, "state", "installations.yaml")
					if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
						t.Fatal(err)
					}
					var scope apply.Scope
					if scopeName == "project" {
						project := filepath.Join(base, "project")
						if err := os.Mkdir(project, 0o755); err != nil {
							t.Fatal(err)
						}
						projectRoot, err := registry.CanonicalProjectRoot(project)
						if err != nil {
							t.Fatal(err)
						}
						scope, err = apply.ProjectScope(projectRoot)
						if err != nil {
							t.Fatal(err)
						}
					} else {
						scope = apply.GlobalScope()
					}
					c, _ := cell.New(artifact.HarnessOpenCode, cell.AgentsAxis())
					if !enabled {
						key, _ := scope.Key(c)
						record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true, Observation: registry.ObservationUnknown, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationRemove, LastOutcome: registry.OutcomeFailed})
						if err != nil {
							t.Fatal(err)
						}
						store := registry.New()
						if err := store.Upsert(record); err != nil {
							t.Fatal(err)
						}
						if err := registry.Save(state, store); err != nil {
							t.Fatal(err)
						}
					}
					before := snapshotTree(t, base)
					svc := newService(t, root, state)
					result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: enabled, Scope: scope, Source: apply.InstallerSource()})
					if err != nil || result.OK() || len(result.Rows()) != 1 || result.Rows()[0].Status() != apply.Failed() || result.Rows()[0].Cell() != c {
						t.Fatalf("result=%+v err=%v", result.Rows(), err)
					}
					if after := snapshotTree(t, base); after != before {
						t.Fatalf("symlink rejection mutated state\nbefore=%s\nafter=%s", before, after)
					}
				})
			}
		}
	}
}

func TestFileRegistryDoesNotCreateThroughIntermediateSymlink(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	safe := filepath.Join(base, "safe")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(safe, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(safe, "link")); err != nil {
		t.Fatal(err)
	}
	repo, err := service.NewFileRegistry(filepath.Join(safe, "link", "missing", "installations.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	err = repo.Save(context.Background(), registry.New())
	if err == nil || !strings.Contains(err.Error(), "pre-created") {
		t.Fatalf("save error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "missing")); !os.IsNotExist(err) {
		t.Fatalf("outside descendant created: %v", err)
	}
}

func TestServiceHomeManagerApplyCellRejectsDirectFile(t *testing.T) {
	t.Parallel()
	for _, scopeName := range []string{"global", "project"} {
		scopeName := scopeName
		t.Run(scopeName, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, "targets")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			var scope apply.Scope
			if scopeName == "project" {
				project := filepath.Join(base, "project")
				if err := os.Mkdir(project, 0o755); err != nil {
					t.Fatal(err)
				}
				projectRoot, _ := registry.CanonicalProjectRoot(project)
				scope, _ = apply.ProjectScope(projectRoot)
			} else {
				scope = apply.GlobalScope()
			}
			svc := newService(t, root, filepath.Join(base, "state", "installations.yaml"))
			before := snapshotTree(t, base)
			c, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
			_, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: scope, Source: apply.HomeManagerSource()})
			var applyErr *apply.ApplyError
			if !errors.As(err, &applyErr) {
				t.Fatalf("error=%v", err)
			}
			encoded, _ := applyErr.MarshalJSON()
			want := "{\n  \"schema\": \"pasture.install.apply-error/v1\",\n  \"source\": \"home-manager\",\n  \"stage\": \"declarative ownership validation\",\n  \"reason\": \"Home Manager owns DirectFile cell opencode.skills declaratively\",\n  \"where\": \"internal/install/service.engine.applyCell\",\n  \"impact\": \"Pasture did not inspect, write, or remove the declarative destination\",\n  \"fix\": \"rerun Home Manager activation so Nix realizes the link and invokes apply-selection for native cells\",\n  \"remediation\": \"rerun_home_manager\"\n}"
			if string(encoded) != want || applyErr.Source() != apply.HomeManagerSource() || applyErr.Stage() != "declarative ownership validation" || applyErr.Remediation() != apply.RemediationRerunHomeManager {
				t.Fatalf("error json=%s", encoded)
			}
			if after := snapshotTree(t, base); after != before {
				t.Fatalf("direct-file rejection mutated tree\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

type probeGroup struct {
	selectionCalls atomic.Int32
	cellCalls      atomic.Int32
	rejectCell     bool
}

type sequentialGroup struct {
	mu       sync.Mutex
	executed []cell.Cell
	wrongKey bool
	closed   int
	stage    service.GroupTerminalStage
}

func (*sequentialGroup) Harness() ir.HarnessID { return artifact.HarnessClaudeCode }
func (g *sequentialGroup) PlanSelection(_ context.Context, request service.GroupSelection) (service.GroupPlan, error) {
	results := make([]service.GroupResultCell, 0, 3)
	steps := make([]service.GroupStep, 0, 3)
	for _, extension := range cell.CanonicalExtensions() {
		c, _ := cell.New(artifact.HarnessClaudeCode, extension)
		key, _ := request.Scope.Key(c)
		if g.wrongKey {
			key, _ = registry.GlobalKey(c)
		}
		result, err := service.NewGroupResultCell(c, key, apply.Inspect())
		if err != nil {
			return service.GroupPlan{}, err
		}
		step, err := service.NewGroupStep(service.InspectGroupAction(), c)
		if err != nil {
			return service.GroupPlan{}, err
		}
		results = append(results, result)
		steps = append(steps, step)
	}
	return service.NewGroupPlan(results, steps)
}
func (g *sequentialGroup) ExecuteAction(_ context.Context, _ service.GroupSelection, _ service.GroupPlan, step service.GroupStep) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.executed = append(g.executed, step.ControlCell())
	return nil
}
func (*sequentialGroup) InspectAction(_ context.Context, _ service.GroupSelection, plan service.GroupPlan, _ service.GroupStep, _ error) (service.GroupFacts, error) {
	actions := make([]service.GroupAction, 0, 3)
	for _, result := range plan.ResultCells() {
		record, err := registry.NewRecord(registry.RecordInput{Key: result.Key(), Source: registry.SourceInstaller, Strategy: activation.NativePluginKindValue(), Managed: true, Observation: registry.ObservationAbsent, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationInspect, LastOutcome: registry.OutcomeCompleted})
		if err != nil {
			return service.GroupFacts{}, err
		}
		actions = append(actions, service.GroupAction{Row: apply.NewActionRow(result.Cell(), result.Operation(), apply.Completed(), apply.ManagementPasture, registry.ObservationAbsent, "confirmed after group action"), Record: &record})
	}
	return service.NewGroupFacts(actions...)
}

func (g *sequentialGroup) ClosePlan(_ context.Context, _ service.GroupSelection, _ service.GroupPlan, stage service.GroupTerminalStage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed++
	g.stage = stage
	return nil
}
func (*sequentialGroup) PreflightCell(context.Context, service.GroupCell) error { return nil }
func (g *sequentialGroup) calls() []cell.Cell {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]cell.Cell(nil), g.executed...)
}
func (g *sequentialGroup) closure() (int, service.GroupTerminalStage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed, g.stage
}

func (g *probeGroup) Harness() ir.HarnessID { return artifact.HarnessClaudeCode }
func (g *probeGroup) PlanSelection(_ context.Context, request service.GroupSelection) (service.GroupPlan, error) {
	g.selectionCalls.Add(1)
	results := make([]service.GroupResultCell, 0, 3)
	steps := make([]service.GroupStep, 0, 3)
	for _, extension := range cell.CanonicalExtensions() {
		c, _ := cell.New(artifact.HarnessClaudeCode, extension)
		key, _ := request.Scope.Key(c)
		result, err := service.NewGroupResultCell(c, key, apply.Inspect())
		if err != nil {
			return service.GroupPlan{}, err
		}
		step, err := service.NewGroupStep(service.InspectGroupAction(), c)
		if err != nil {
			return service.GroupPlan{}, err
		}
		results = append(results, result)
		steps = append(steps, step)
	}
	return service.NewGroupPlan(results, steps)
}
func (g *probeGroup) ExecuteAction(context.Context, service.GroupSelection, service.GroupPlan, service.GroupStep) error {
	return nil
}
func (g *probeGroup) InspectAction(_ context.Context, _ service.GroupSelection, plan service.GroupPlan, _ service.GroupStep, _ error) (service.GroupFacts, error) {
	actions := make([]service.GroupAction, 0, 3)
	for _, result := range plan.ResultCells() {
		actions = append(actions, service.GroupAction{Row: apply.NewActionRow(result.Cell(), result.Operation(), apply.Completed(), apply.ManagementExternal, registry.ObservationAbsent, "legacy group probe found no managed state")})
	}
	return service.NewGroupFacts(actions...)
}
func (*probeGroup) ClosePlan(context.Context, service.GroupSelection, service.GroupPlan, service.GroupTerminalStage) error {
	return nil
}
func (g *probeGroup) PreflightCell(_ context.Context, request service.GroupCell) error {
	g.cellCalls.Add(1)
	if g.rejectCell {
		return apply.NewApplyError(request.Source, "selection-wide reconciliation", "a legacy Claude transition requires all sibling choices", "claude group preflight", "the cell request performed no mutation", "rerun apply-selection with all three Claude choices", apply.RemediationApplySelection)
	}
	return nil
}

func TestServiceCanonicalGroupSeamProbesAllFalseAndRejectsApplyCell(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	group := &probeGroup{rejectCell: true}
	svc := newServiceConfig(t, root, filepath.Join(base, "state", "installations.yaml"), service.Config{Group: group})
	result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return false }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil || !result.OK() || result.Scope() != registry.ScopeGlobal || len(result.Rows()) != 3 || group.selectionCalls.Load() != 1 {
		t.Fatalf("result=%+v rows=%v calls=%d err=%v", result, result.Rows(), group.selectionCalls.Load(), err)
	}
	c, _ := cell.New(artifact.HarnessClaudeCode, cell.SkillsAxis())
	_, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	var applyErr *apply.ApplyError
	if !errors.As(err, &applyErr) || group.cellCalls.Load() != 1 {
		t.Fatalf("error=%v calls=%d", err, group.cellCalls.Load())
	}
	encoded, _ := json.Marshal(applyErr)
	if !strings.Contains(string(encoded), `"remediation":"apply_selection"`) {
		t.Fatalf("error json=%s", encoded)
	}
}

func TestServiceGroupExecutesInspectsAndPersistsOneActionBeforeAdvancing(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	state := filepath.Join(base, "state", "installations.yaml")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	file, _ := service.NewFileRegistry(state)
	fault := &faultRegistry{base: file, saveErr: errors.New("injected group save failure")}
	group := &sequentialGroup{}
	svc := newServiceConfig(t, root, state, service.Config{Registry: fault, Group: group})
	result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return false }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil || result.OK() || len(result.Rows()) != 3 {
		t.Fatalf("result=%+v err=%v", result.Rows(), err)
	}
	if calls := group.calls(); len(calls) != 1 || calls[0] != result.Rows()[0].Cell() {
		t.Fatalf("executed group actions=%v; later action ran ahead of failed persistence", calls)
	}
	if result.Rows()[0].Status() != apply.Failed() || result.Rows()[1].Status() != apply.Completed() || result.Rows()[2].Status() != apply.Completed() {
		t.Fatalf("statuses=%s/%s/%s", result.Rows()[0].Status(), result.Rows()[1].Status(), result.Rows()[2].Status())
	}
	if count, stage := group.closure(); count != 1 || stage != service.GroupTerminalSaveFailed {
		t.Fatalf("close count=%d stage=%v", count, stage)
	}
}

func TestServiceRejectsGroupPlanWithWrongProjectKeyBeforeExecution(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	group := &sequentialGroup{wrongKey: true}
	svc := newServiceConfig(t, root, filepath.Join(base, "state", "installations.yaml"), service.Config{Group: group})
	_, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return false }), Scope: projectScope(t), Source: apply.InstallerSource()})
	var applyErr *apply.ApplyError
	if !errors.As(err, &applyErr) || len(group.calls()) != 0 || applyErr.Stage() != "pre-plan validation" {
		t.Fatalf("error=%v calls=%v", err, group.calls())
	}
	if count, stage := group.closure(); count != 1 || stage != service.GroupTerminalPlanInvalid {
		t.Fatalf("close count=%d stage=%v", count, stage)
	}
}

type nativeSpy struct{ calls atomic.Int32 }

func (*nativeSpy) StrategyKind() activation.StrategyKind { return activation.NativePluginKindValue() }
func (s *nativeSpy) Inspect(context.Context, apply.Source, registry.Key, activation.ComponentActivation, *registry.Record) (apply.Outcome, error) {
	s.calls.Add(1)
	return apply.Outcome{Observation: registry.ObservationAbsent}, nil
}
func (s *nativeSpy) Ensure(context.Context, apply.Source, registry.Key, activation.ComponentActivation, *registry.Record) (apply.Outcome, error) {
	s.calls.Add(1)
	return apply.Outcome{Observation: registry.ObservationUnknown}, errors.New("native spy must not mutate during preflight tests")
}
func (s *nativeSpy) Remove(context.Context, apply.Source, registry.Key, activation.ComponentActivation, registry.Record) (apply.Outcome, error) {
	s.calls.Add(1)
	return apply.Outcome{Observation: registry.ObservationUnknown}, errors.New("native spy must not mutate during preflight tests")
}

func nativeStrategy(t *testing.T, c cell.Cell) activation.ActivationStrategy {
	t.Helper()
	command, err := activation.NewCommandSchema("host", "plugin", c.String())
	if err != nil {
		t.Fatal(err)
	}
	strategy, err := activation.NewNativePlugin(c.String(), command)
	if err != nil {
		t.Fatal(err)
	}
	return strategy
}

func TestServiceRejectsMixedNativeControllersBeforeAnyInspection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		recorded  registry.Source
		requested apply.Source
	}{
		{name: "installer-over-home-manager", recorded: registry.SourceHomeManager, requested: apply.InstallerSource()},
		{name: "home-manager-over-installer", recorded: registry.SourceInstaller, requested: apply.HomeManagerSource()},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, "targets")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(base, "state", "installations.yaml")
			if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
				t.Fatal(err)
			}
			conflictCell, _ := cell.New(artifact.HarnessCodex, cell.HooksAxis())
			key, _ := registry.GlobalKey(conflictCell)
			record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: tc.recorded, Strategy: activation.NativePluginKindValue(), Managed: true, Observation: registry.ObservationInstalled, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted})
			if err != nil {
				t.Fatal(err)
			}
			store := registry.New()
			_ = store.Upsert(record)
			if err := registry.Save(state, store); err != nil {
				t.Fatal(err)
			}
			spy := &nativeSpy{}
			svc := newServiceConfig(t, root, state, service.Config{Contracts: contractsWith(t, root, func(c cell.Cell) activation.ActivationStrategy { return nativeStrategy(t, c) }), Activators: []apply.Activator{spy}})
			before := snapshotTree(t, base)
			_, err = svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: tc.requested})
			var applyErr *apply.ApplyError
			if !errors.As(err, &applyErr) || spy.calls.Load() != 0 {
				t.Fatalf("error=%v native calls=%d", err, spy.calls.Load())
			}
			encoded, _ := json.Marshal(applyErr)
			if !strings.Contains(string(encoded), tc.recorded.String()) || !strings.Contains(string(encoded), tc.requested.String()) || !strings.Contains(string(encoded), conflictCell.String()) {
				t.Fatalf("error json=%s", encoded)
			}
			_, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: conflictCell, Enabled: true, Scope: apply.GlobalScope(), Source: tc.requested})
			if !errors.As(err, &applyErr) || spy.calls.Load() != 0 {
				t.Fatalf("cell error=%v native calls=%d", err, spy.calls.Load())
			}
			if after := snapshotTree(t, base); after != before {
				t.Fatalf("controller rejection mutated state\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

func TestServiceHomeManagerPreinspectsAllDeclarativeCellsBeforeNativeMutation(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.MkdirAll(filepath.Join(root, "opencode", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "opencode", "agents", "component.txt"), []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	nativeCell, _ := cell.New(artifact.HarnessClaudeCode, cell.SkillsAxis())
	spy := &nativeSpy{}
	contractSet := contractsWith(t, root, func(c cell.Cell) activation.ActivationStrategy {
		if c == nativeCell {
			return nativeStrategy(t, c)
		}
		rel := filepath.ToSlash(filepath.Join(string(c.Harness()), c.Extension().String(), "component.txt"))
		direct, err := activation.NewDirectFile(bundle(t, rel, c.String()+"\n"), root)
		if err != nil {
			t.Fatal(err)
		}
		return direct
	})
	svc := newServiceConfig(t, root, filepath.Join(base, "state", "installations.yaml"), service.Config{Contracts: contractSet, Activators: []apply.Activator{spy, directFileActivator(t, contractSet)}})
	before := snapshotTree(t, base)
	result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: apply.HomeManagerSource()})
	if err != nil || result.OK() || spy.calls.Load() != 0 {
		t.Fatalf("result=%+v err=%v native calls=%d", result.Rows(), err, spy.calls.Load())
	}
	if result.Rows()[0].Cell() != nativeCell || result.Rows()[0].Status() != apply.Unattempted() {
		t.Fatalf("native row=%+v", result.Rows()[0])
	}
	for i, row := range result.Rows() {
		if row.Cell().Index() != i {
			t.Fatalf("row %d is %s", i, row.Cell())
		}
	}
	if after := snapshotTree(t, base); after != before {
		t.Fatalf("declarative preflight mutated state\nbefore=%s\nafter=%s", before, after)
	}
}

type faultRegistry struct {
	base     service.FileRegistry
	loadErr  error
	saveErr  error
	loadCall atomic.Int32
	saveCall atomic.Int32
}

func (r *faultRegistry) Load(ctx context.Context) (registry.Store, error) {
	r.loadCall.Add(1)
	if r.loadErr != nil {
		return registry.Store{}, r.loadErr
	}
	return r.base.Load(ctx)
}
func (r *faultRegistry) Save(ctx context.Context, store registry.Store) error {
	r.saveCall.Add(1)
	if r.saveErr != nil {
		return r.saveErr
	}
	return r.base.Save(ctx, store)
}

func TestServiceRegistryLoadFailureIsTypedAndPerformsNoMutation(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state", "installations.yaml")
	file, _ := service.NewFileRegistry(state)
	fault := &faultRegistry{base: file, loadErr: errors.New("injected unreadable registry")}
	svc := newServiceConfig(t, root, state, service.Config{Registry: fault})
	_, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	var applyErr *apply.ApplyError
	if !errors.As(err, &applyErr) || fault.loadCall.Load() != 1 || fault.saveCall.Load() != 0 {
		t.Fatalf("error=%v loads=%d saves=%d", err, fault.loadCall.Load(), fault.saveCall.Load())
	}
	encoded, _ := applyErr.MarshalJSON()
	wantFields := []string{`"schema": "pasture.install.apply-error/v1"`, `"source": "installer"`, `"stage": "registry-load"`, `"where": "internal/install/service.Service.load"`, `"impact":`, `"fix":`, `"remediation": "manual_repair"`}
	for _, field := range wantFields {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("error JSON missing %s: %s", field, encoded)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("load failure mutated root: %v", entries)
	}
}

func TestServiceRealFileRegistryRejectsCorruptAndUnreadableAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		seed func(*testing.T, string)
	}{
		{name: "corrupt", seed: func(t *testing.T, state string) {
			t.Helper()
			if err := os.WriteFile(state, []byte("schema: ["), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wrong-type", seed: func(t *testing.T, state string) {
			t.Helper()
			if err := os.Mkdir(state, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, "targets")
			state := filepath.Join(base, "state", "installations.yaml")
			if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
				t.Fatal(err)
			}
			tc.seed(t, state)
			before := snapshotTree(t, base)
			svc := newService(t, root, state)
			_, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			var applyErr *apply.ApplyError
			if !errors.As(err, &applyErr) || applyErr.Stage() != "registry-load" || applyErr.Remediation() != apply.RemediationManualRepair {
				t.Fatalf("error=%v", err)
			}
			if after := snapshotTree(t, base); after != before {
				t.Fatalf("load rejection mutated tree\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

func TestServiceRejectsIncompleteContractAndInvalidRequestsWithoutMutation(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	state := filepath.Join(base, "state", "installations.yaml")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	file, _ := service.NewFileRegistry(state)
	valid := contracts(t, root)
	delete(valid, artifact.HarnessCodex)
	incompleteService := newServiceConfig(t, root, state, service.Config{Registry: file, Contracts: valid})
	validService := newServiceConfig(t, root, state, service.Config{Registry: file})
	invalidScope := apply.Scope{}
	requests := []struct {
		stage  string
		invoke func() error
	}{
		{stage: "activation contract validation", invoke: func() error {
			_, err := incompleteService.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			return err
		}},
		{stage: "cell validation", invoke: func() error {
			_, err := validService.ApplyCell(context.Background(), service.CellRequest{Cell: cell.Cell{}, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			return err
		}},
		{stage: "pre-plan validation", invoke: func() error {
			c, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
			_, err := validService.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: invalidScope, Source: apply.InstallerSource()})
			return err
		}},
		{stage: "source validation", invoke: func() error {
			c, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
			_, err := validService.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: apply.GlobalScope(), Source: apply.SourceInvalid})
			return err
		}},
	}
	before := snapshotTree(t, base)
	for i, request := range requests {
		var applyErr *apply.ApplyError
		if err := request.invoke(); !errors.As(err, &applyErr) || applyErr.Stage() != request.stage {
			t.Fatalf("request %d error=%v", i, err)
		}
		if after := snapshotTree(t, base); after != before {
			t.Fatalf("request %d mutated tree\nbefore=%s\nafter=%s", i, before, after)
		}
	}
}

func TestServiceRegistrySaveFailureRetainsPriorBytesAndStopsLaterCells(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "state", "installations.yaml")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	prior := registry.New()
	if err := registry.Save(state, prior); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := service.NewFileRegistry(state)
	fault := &faultRegistry{base: file, saveErr: errors.New("injected atomic replacement failure")}
	svc := newServiceConfig(t, root, state, service.Config{Registry: fault})
	result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	if err != nil || result.OK() || len(result.Rows()) != 9 || result.Rows()[0].Status() != apply.Failed() {
		t.Fatalf("result=%+v err=%v", result.Rows(), err)
	}
	for i, row := range result.Rows()[1:] {
		if row.Status() != apply.Unattempted() {
			t.Fatalf("later row %d status=%s", i+1, row.Status())
		}
	}
	if !strings.Contains(result.Rows()[0].Diagnostic(), "what") && !strings.Contains(result.Rows()[0].Diagnostic(), "could not be saved atomically") {
		t.Fatalf("non-actionable diagnostic: %s", result.Rows()[0].Diagnostic())
	}
	after, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("authoritative bytes changed\nbefore=%s\nafter=%s", before, after)
	}
	first := filepath.Join(root, "claude-code", "skills", "component.txt")
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first live action did not occur: %v", err)
	}
	second := filepath.Join(root, "claude-code", "agents", "component.txt")
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("later live action occurred: %v", err)
	}
	encoded, err := result.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "schema": "pasture.install.apply-result/v1",
  "source": "installer",
  "scope": "global",
  "ok": false,
  "cells": [
    {
      "index": 0,
      "cell": "claude-code.skills",
      "harness": "claude-code",
      "extension": "skills",
      "operation": "ensure",
      "status": "failed",
      "management": "pasture_managed",
      "observation": "installed",
      "diagnostic": "confirmed registry fact could not be saved atomically: injected atomic replacement failure; where: claude-code.skills; when: registry-save; impact: the live component may have changed but the previous registry remains authoritative; later cells were not attempted; fix: inspect status, repair the registry path or permissions, and rerun the same apply operation"
    },
    {
      "index": 1,
      "cell": "claude-code.agents",
      "harness": "claude-code",
      "extension": "agents",
      "operation": "ensure",
      "status": "unattempted",
      "management": "unknown",
      "diagnostic": "an earlier canonical cell failed; this cell was not attempted"
    },
    {
      "index": 2,
      "cell": "claude-code.hooks",
      "harness": "claude-code",
      "extension": "hooks",
      "operation": "ensure",
      "status": "unattempted",
      "management": "unknown",
      "diagnostic": "an earlier canonical cell failed; this cell was not attempted"
    },
    {
      "index": 3,
      "cell": "opencode.skills",
      "harness": "opencode",
      "extension": "skills",
      "operation": "ensure",
      "status": "unattempted",
      "management": "unknown",
      "diagnostic": "an earlier canonical cell failed; this cell was not attempted"
    },
    {
      "index": 4,
      "cell": "opencode.agents",
      "harness": "opencode",
      "extension": "agents",
      "operation": "ensure",
      "status": "unattempted",
      "management": "unknown",
      "diagnostic": "an earlier canonical cell failed; this cell was not attempted"
    },
    {
      "index": 5,
      "cell": "opencode.hooks",
      "harness": "opencode",
      "extension": "hooks",
      "operation": "ensure",
      "status": "unattempted",
      "management": "unknown",
      "diagnostic": "an earlier canonical cell failed; this cell was not attempted"
    },
    {
      "index": 6,
      "cell": "codex.skills",
      "harness": "codex",
      "extension": "skills",
      "operation": "ensure",
      "status": "unattempted",
      "management": "unknown",
      "diagnostic": "an earlier canonical cell failed; this cell was not attempted"
    },
    {
      "index": 7,
      "cell": "codex.agents",
      "harness": "codex",
      "extension": "agents",
      "operation": "ensure",
      "status": "unattempted",
      "management": "unknown",
      "diagnostic": "an earlier canonical cell failed; this cell was not attempted"
    },
    {
      "index": 8,
      "cell": "codex.hooks",
      "harness": "codex",
      "extension": "hooks",
      "operation": "ensure",
      "status": "unattempted",
      "management": "unknown",
      "diagnostic": "an earlier canonical cell failed; this cell was not attempted"
    }
  ]
}`
	if string(encoded) != want {
		t.Fatalf("failed result JSON mismatch\n got: %s\nwant: %s", encoded, want)
	}
}

func projectScope(t *testing.T) apply.Scope {
	t.Helper()
	project := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := registry.CanonicalProjectRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := apply.ProjectScope(root)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func fmtBool(value bool) string {
	if value {
		return "ensure"
	}
	return "remove"
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var rows []string
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		row := fmt.Sprintf("%s|%s|%04o", filepath.ToSlash(rel), info.Mode().Type(), info.Mode().Perm())
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			row += "|" + string(content)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			if err != nil {
				return err
			}
			row += "|->" + target
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}

func installManagedCell(t *testing.T, svc *service.Service, root, state string, scope apply.Scope, c cell.Cell) {
	t.Helper()
	result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: scope, Source: apply.InstallerSource()})
	if err != nil || !result.OK() {
		t.Fatalf("fixture install=%+v err=%v root=%s state=%s", result.Rows(), err, root, state)
	}
}

func recordSignature(record registry.Record) string {
	var leaves []string
	for _, leaf := range record.Leaves() {
		leaves = append(leaves, fmt.Sprintf("%s:%s:%s:%s", leaf.Path(), leaf.Type(), leaf.Mode(), leaf.Digest()))
	}
	var dirs []string
	for _, dir := range record.CreatedDirs() {
		dirs = append(dirs, dir.String())
	}
	var shared []string
	for _, item := range record.SharedConfig() {
		shared = append(shared, fmt.Sprintf("%s:%s:%s", item.Path(), item.Identity(), item.Digest()))
	}
	return fmt.Sprintf("scope=%s cell=%s source=%s strategy=%s managed=%t artifact=%s version=%s selector=%s leaves=%v dirs=%v shared=%v observation=%s trust=%s operation=%s outcome=%s diagnostic=%q", record.Key().Scope(), record.Cell(), record.Source(), record.Strategy(), record.Managed(), record.ArtifactID(), record.Version(), record.Selector(), leaves, dirs, shared, record.Observation(), record.Trust(), record.LastOperation(), record.LastOutcome(), record.Diagnostic())
}
