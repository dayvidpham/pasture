package inventory_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/inventory"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

func TestViewMutatesCallerOwnedStoreAndPreservesProjects(t *testing.T) {
	store := registry.New()
	c, _ := cell.New(ir.HarnessClaudeCode, cell.SkillsAxis())
	rootDir := t.TempDir()
	root, _ := registry.CanonicalProjectRoot(rootDir)
	projectKey, _ := registry.ProjectKey(root, c)
	project, err := registry.NewRecord(registry.RecordInput{Key: projectKey, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: false, Observation: registry.ObservationAbsent, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationRemove, LastOutcome: registry.OutcomeCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(project); err != nil {
		t.Fatal(err)
	}

	bundleID, _ := artifact.ParseBundleID("artifact.bundle.v1:sha256:" + strings.Repeat("a", 64))
	version, _ := inventory.NewVersion("claude-code@2.1.261")
	selector, _ := inventory.NewSelector("pasture-skills@user")
	global, err := inventory.NewRecord(inventory.RecordInput{Cell: c, Source: inventory.InstallerSource(), Strategy: activation.NativePluginKindValue(), Managed: true, ArtifactID: bundleID, Version: version, Selector: selector, Observation: inventory.Installed(), Trust: inventory.TrustNotApplicable(), LastOperation: inventory.OperationEnsure, LastOutcome: inventory.OutcomeCompleted})
	if err != nil {
		t.Fatal(err)
	}
	view := inventory.View(&store)
	if err := view.Upsert(global); err != nil {
		t.Fatal(err)
	}
	if view.Len() != 1 {
		t.Fatalf("global view Len=%d, want 1", view.Len())
	}
	if store.Len() != 2 {
		t.Fatalf("shared Store Len=%d, want global+project", store.Len())
	}
	if got := inventory.UnifiedStatus(store); len(got) != 2 || got[0].Scope != registry.ScopeGlobal || got[1].Scope != registry.ScopeProject {
		t.Fatalf("UnifiedStatus=%v", got)
	}
	if got := inventory.Projects(store); len(got) != 1 || got[0].ProjectRoot != root {
		t.Fatalf("Projects=%v", got)
	}
}

func TestTypedInventoryInputRejectsInvalidOwnership(t *testing.T) {
	_, err := inventory.NewVersion(" padded ")
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("invalid version error=%v", err)
	}
}
