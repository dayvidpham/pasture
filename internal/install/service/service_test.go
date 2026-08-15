package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		config.Activators = []apply.Activator{apply.NewDirectFileActivator()}
	}
	svc, err := service.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return svc
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
			again, err := svc.ApplyCell(context.Background(), request)
			if err != nil || !again.OK() || again.Rows()[0].Status() != apply.NoOp() {
				t.Fatalf("idempotent remove: %v %+v", err, again.Rows())
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
	if got.Observation() != registry.ObservationInstalled || got.LastOutcome() != registry.OutcomeFailed {
		t.Fatalf("persisted observation=%s outcome=%s", got.Observation(), got.LastOutcome())
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
			if err != nil || result.OK() || !strings.Contains(result.Rows()[0].Diagnostic(), "unsafe mode") {
				t.Fatalf("result=%+v err=%v", result.Rows(), err)
			}
			after, _ := os.ReadDir(target)
			if len(before) != len(after) {
				t.Fatalf("symlink target changed: before=%v after=%v", before, after)
			}
		})
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
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := newService(t, root, filepath.Join(base, "state", "installations.yaml"))
	c, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	_, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: c, Enabled: true, Scope: apply.GlobalScope(), Source: apply.HomeManagerSource()})
	var applyErr *apply.ApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("error=%v", err)
	}
	encoded, _ := json.Marshal(applyErr)
	if !strings.Contains(string(encoded), `"remediation":"rerun_home_manager"`) || !strings.Contains(string(encoded), "did not inspect") {
		t.Fatalf("error json=%s", encoded)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("direct-file request mutated root: %v", entries)
	}
}

type probeGroup struct {
	selectionCalls atomic.Int32
	cellCalls      atomic.Int32
	rejectCell     bool
}

func (g *probeGroup) Harness() ir.HarnessID { return artifact.HarnessClaudeCode }
func (g *probeGroup) ReconcileSelection(_ context.Context, request service.GroupSelection) (service.GroupResult, error) {
	g.selectionCalls.Add(1)
	actions := make([]service.GroupAction, 0, 3)
	for _, extension := range cell.CanonicalExtensions() {
		c, _ := cell.New(artifact.HarnessClaudeCode, extension)
		actions = append(actions, service.GroupAction{Row: apply.NewActionRow(c, apply.Inspect(), apply.Completed(), apply.ManagementExternal, registry.ObservationAbsent, "legacy group probe found no managed state")})
	}
	return service.GroupResult{Handled: true, Actions: actions}, nil
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
	svc := newServiceConfig(t, root, filepath.Join(base, "state", "installations.yaml"), service.Config{Groups: []service.GroupReconciler{group}})
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
	svc := newServiceConfig(t, root, filepath.Join(base, "state", "installations.yaml"), service.Config{Contracts: contractSet, Activators: []apply.Activator{spy, apply.NewDirectFileActivator()}})
	result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: all(t, func(cell.Cell) bool { return true }), Scope: apply.GlobalScope(), Source: apply.HomeManagerSource()})
	if err != nil || result.OK() || spy.calls.Load() != 0 {
		t.Fatalf("result=%+v err=%v native calls=%d", result.Rows(), err, spy.calls.Load())
	}
	if result.Rows()[0].Cell() != nativeCell || result.Rows()[0].Status() != apply.Unattempted() {
		t.Fatalf("native row=%+v", result.Rows()[0])
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
