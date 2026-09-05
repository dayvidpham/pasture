package opencode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	host "github.com/dayvidpham/pasture/internal/install/host/opencode"
	"github.com/dayvidpham/pasture/internal/install/registry"
	"github.com/dayvidpham/pasture/internal/install/selection"
	"github.com/dayvidpham/pasture/internal/install/service"
	"github.com/dayvidpham/pasture/internal/runtime"
)

func TestControllerMatchesDocumentedGlobalLayout(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "opencode")
	controller, err := host.New(root)
	require.NoError(t, err)

	fixturePath := filepath.Join(moduleRoot(t), "testdata", "install", "global", "opencode", "layout.json")
	raw, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	var fixture struct {
		VersionProbe      []string `json:"version_probe"`
		SkillsRoot        string   `json:"skills_root"`
		AgentsRoot        string   `json:"agents_root"`
		HooksRoot         string   `json:"hooks_root"`
		HookFile          string   `json:"hook_file"`
		ConfigReadOrder   []string `json:"config_read_order"`
		NativeWriterOrder []string `json:"native_writer_order"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	// The id and the admission are read from the OpenCode runtime contract, the
	// one root, never restated in the fixture.
	require.Equal(t, "opencode/activation@"+runtime.OpenCode1_18_29().Versions().Min().String(), controller.Contract().ID().String())
	require.False(t, controller.Contract().HostVersions().HasUpperBound(), "installer admission is the runtime contract's floor")
	require.Equal(t, runtime.OpenCode1_18_29().Versions().Min().String(), controller.Contract().HostVersions().Min().String())
	require.Equal(t, fixture.VersionProbe[0], controller.Contract().VersionProbe().Program())
	require.Equal(t, fixture.VersionProbe[1:], controller.Contract().VersionProbe().Args())
	require.Equal(t, filepath.Join(root, fixture.SkillsRoot), destination(t, controller, artifact.ExtensionSkills))
	require.Equal(t, filepath.Join(root, fixture.AgentsRoot), destination(t, controller, artifact.ExtensionAgents))
	require.Equal(t, filepath.Join(root, fixture.HooksRoot), destination(t, controller, artifact.ExtensionHooks))
	require.Equal(t, fixture.HookFile, controller.Descriptor().Hooks().Bundle().Manifest().Entries()[0].Path().String())
	facts := host.DirectDiscoveryFacts()
	require.Equal(t, fixture.ConfigReadOrder, configStrings(facts.ReadOrder()))
	require.Equal(t, fixture.NativeWriterOrder, configStrings(facts.NativeWriterOrder()))
}

func TestEachCellInstallsStatusesAndRemovesIndependently(t *testing.T) {
	for _, extension := range cell.CanonicalExtensions() {
		extension := extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			controller, svc := productionService(t, base)
			statePath := filepath.Join(base, "state", "installations.yaml")
			coordinate, err := cell.New(artifact.HarnessOpenCode, extension)
			require.NoError(t, err)
			result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			require.Equal(t, apply.Completed(), result.Rows()[0].Status())
			root := destination(t, controller, extension)
			component, err := controller.Descriptor().Component(extension)
			require.NoError(t, err)
			assertBundleMaterialized(t, root, component.Bundle())
			if extension == artifact.ExtensionHooks {
				assertCanonicalHookLayout(t, controller, true)
			}
			for _, sibling := range cell.CanonicalExtensions() {
				if sibling != extension {
					_, statErr := os.Lstat(destination(t, controller, sibling))
					require.ErrorIs(t, statErr, os.ErrNotExist)
				}
			}

			stableTree := snapshotPath(t, controller.ConfigRoot())
			stableRegistry := snapshotPath(t, statePath)
			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			require.Equal(t, apply.Completed(), result.Rows()[0].Status())
			require.Equal(t, stableTree, snapshotPath(t, controller.ConfigRoot()))
			require.Equal(t, stableRegistry, snapshotPath(t, statePath))
			unrelated := filepath.Join(root, "user-owned.txt")
			require.NoError(t, os.WriteFile(unrelated, []byte("preserve\n"), 0o600))
			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			require.Equal(t, apply.Completed(), result.Rows()[0].Status())
			require.Equal(t, registry.ObservationAbsent, result.Rows()[0].Observation())
			for _, entry := range component.Bundle().Manifest().Entries() {
				_, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Path().String())))
				require.ErrorIs(t, statErr, os.ErrNotExist)
			}
			if extension == artifact.ExtensionHooks {
				assertCanonicalHookLayout(t, controller, false)
			}
			preserved, err := os.ReadFile(unrelated)
			require.NoError(t, err)
			require.Equal(t, "preserve\n", string(preserved))
			removedTree := snapshotPath(t, controller.ConfigRoot())
			removedRegistry := snapshotPath(t, statePath)
			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			require.Equal(t, apply.NoOp(), result.Rows()[0].Status())
			require.Equal(t, removedTree, snapshotPath(t, controller.ConfigRoot()))
			require.Equal(t, removedRegistry, snapshotPath(t, statePath))
		})
	}
}

func TestSelectionStopsAfterAgentConflictAndKeepsVerifiedSkills(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	controller, svc := productionService(t, base)
	agentRoot := destination(t, controller, artifact.ExtensionAgents)
	require.NoError(t, os.MkdirAll(agentRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agentRoot, "worker.md"), []byte("external\n"), 0o644))

	selection, err := selectionWithOpenCodeEnabled()
	require.NoError(t, err)
	result, applyErr := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: selection, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, applyErr)
	require.False(t, result.OK())
	require.Len(t, result.Rows(), 3)
	require.Equal(t, apply.Completed(), result.Rows()[0].Status())
	require.Equal(t, apply.Failed(), result.Rows()[1].Status())
	require.Equal(t, apply.Unattempted(), result.Rows()[2].Status())
	assertBundleMaterialized(t, destination(t, controller, artifact.ExtensionSkills), controller.Descriptor().Skills().Bundle())
	_, err = os.Lstat(filepath.Join(destination(t, controller, artifact.ExtensionHooks), host.HookFile))
	require.ErrorIs(t, err, os.ErrNotExist)
	content, err := os.ReadFile(filepath.Join(agentRoot, "worker.md"))
	require.NoError(t, err)
	require.Equal(t, "external\n", string(content))

	require.NoError(t, os.Remove(filepath.Join(agentRoot, "worker.md")))
	result, applyErr = svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: selection, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, applyErr)
	require.True(t, result.OK())
	require.Len(t, result.Rows(), 3)
	for index, extension := range cell.CanonicalExtensions() {
		require.Equal(t, apply.Completed(), result.Rows()[index].Status())
		component, componentErr := controller.Descriptor().Component(extension)
		require.NoError(t, componentErr)
		assertBundleMaterialized(t, destination(t, controller, extension), component.Bundle())
	}
}

func TestEachCellUpdatesFromAnExactPriorManagedBundle(t *testing.T) {
	for _, extension := range cell.CanonicalExtensions() {
		extension := extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			controller, svc := productionService(t, base)
			coordinate, _ := cell.New(artifact.HarnessOpenCode, extension)
			_, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)

			component, err := controller.Descriptor().Component(extension)
			require.NoError(t, err)
			root := destination(t, controller, extension)
			oldBundle := modifiedBundle(t, component.Bundle())
			materializeBundle(t, root, oldBundle)
			leaves := make([]registry.Leaf, 0, oldBundle.Manifest().Len())
			for _, entry := range oldBundle.Manifest().Entries() {
				leaf, leafErr := registry.NewLeaf(entry.Path(), artifact.RegularFileType(), entry.Mode(), entry.Digest())
				require.NoError(t, leafErr)
				leaves = append(leaves, leaf)
			}
			key, err := registry.GlobalKey(coordinate)
			require.NoError(t, err)
			record, err := registry.NewRecord(registry.RecordInput{Key: key, Source: registry.SourceInstaller, Strategy: activation.DirectFileKindValue(), Managed: true, ArtifactID: oldBundle.ID(), Leaves: leaves, Observation: registry.ObservationInstalled, Trust: registry.TrustNotApplicable, LastOperation: registry.OperationEnsure, LastOutcome: registry.OutcomeCompleted})
			require.NoError(t, err)
			statePath := filepath.Join(base, "state", "installations.yaml")
			store, err := registry.Load(statePath)
			require.NoError(t, err)
			require.NoError(t, store.Upsert(record))
			require.NoError(t, registry.Save(statePath, store))

			result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			assertBundleMaterialized(t, root, component.Bundle())
			stableTree := snapshotPath(t, controller.ConfigRoot())
			stableRegistry := snapshotPath(t, statePath)
			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.Equal(t, apply.Completed(), result.Rows()[0].Status())
			require.Equal(t, stableTree, snapshotPath(t, controller.ConfigRoot()))
			require.Equal(t, stableRegistry, snapshotPath(t, statePath))
		})
	}
}

func TestEachExactExternalCellRemainsUnmanagedAndUnremoved(t *testing.T) {
	for _, extension := range cell.CanonicalExtensions() {
		extension := extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			controller, svc := productionService(t, base)
			component, err := controller.Descriptor().Component(extension)
			require.NoError(t, err)
			root := destination(t, controller, extension)
			materializeBundle(t, root, component.Bundle())
			before := snapshotPath(t, controller.ConfigRoot())
			coordinate, err := cell.New(artifact.HarnessOpenCode, extension)
			require.NoError(t, err)
			result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.Equal(t, apply.Completed(), result.Rows()[0].Status())
			require.Equal(t, apply.ManagementExternal, result.Rows()[0].Management())
			require.Equal(t, before, snapshotPath(t, controller.ConfigRoot()))

			statePath := filepath.Join(base, "state", "installations.yaml")
			store, err := registry.Load(statePath)
			require.NoError(t, err)
			key, err := registry.GlobalKey(coordinate)
			require.NoError(t, err)
			record, ok := store.Lookup(key)
			require.True(t, ok)
			require.False(t, record.Managed())
			require.Equal(t, registry.ObservationInstalled, record.Observation())
			registryBeforeDisable := snapshotPath(t, statePath)
			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.Equal(t, apply.NoOp(), result.Rows()[0].Status())
			require.Equal(t, before, snapshotPath(t, controller.ConfigRoot()))
			require.Equal(t, registryBeforeDisable, snapshotPath(t, statePath))
			for _, sibling := range cell.CanonicalExtensions() {
				if sibling == extension {
					continue
				}
				_, statErr := os.Lstat(destination(t, controller, sibling))
				require.ErrorIs(t, statErr, os.ErrNotExist)
			}
		})
	}
}

func TestHomeManagerMaterializedCellsAreByteIdenticalReadOnlyFacts(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	controller, svc := productionService(t, base)
	for _, extension := range cell.CanonicalExtensions() {
		component, err := controller.Descriptor().Component(extension)
		require.NoError(t, err)
		materializeBundle(t, destination(t, controller, extension), component.Bundle())
	}
	before := snapshotPath(t, controller.ConfigRoot())
	desired, err := selectionWithOpenCodeEnabled()
	require.NoError(t, err)
	result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: desired, Scope: apply.GlobalScope(), Source: apply.HomeManagerSource()})
	require.NoError(t, err)
	require.True(t, result.OK())
	require.Len(t, result.Rows(), 9)
	for _, row := range result.Rows() {
		require.Equal(t, apply.ManagedDeclaratively(), row.Status())
		require.Equal(t, apply.ManagementDeclarative, row.Management())
		if row.Cell().Harness() == artifact.HarnessOpenCode {
			require.Equal(t, registry.ObservationInstalled, row.Observation())
		}
	}
	require.Equal(t, before, snapshotPath(t, controller.ConfigRoot()))
	_, err = os.Lstat(filepath.Join(base, "state", "installations.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestHomeManagerMismatchedCellsDiagnoseWithoutMutation(t *testing.T) {
	for _, extension := range cell.CanonicalExtensions() {
		extension := extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			controller, svc := productionService(t, base)
			for _, candidate := range cell.CanonicalExtensions() {
				component, err := controller.Descriptor().Component(candidate)
				require.NoError(t, err)
				materializeBundle(t, destination(t, controller, candidate), component.Bundle())
			}
			component, err := controller.Descriptor().Component(extension)
			require.NoError(t, err)
			leaf := component.Bundle().Manifest().Entries()[0]
			leafPath := filepath.Join(destination(t, controller, extension), filepath.FromSlash(leaf.Path().String()))
			require.NoError(t, os.WriteFile(leafPath, []byte("declarative-user-drift\n"), os.FileMode(leaf.Mode().Bits())))
			before := snapshotPath(t, base)
			desired, err := selectionWithOpenCodeEnabled()
			require.NoError(t, err)
			result, err := svc.ApplySelection(context.Background(), service.SelectionRequest{Selection: desired, Scope: apply.GlobalScope(), Source: apply.HomeManagerSource()})
			require.NoError(t, err)
			require.False(t, result.OK())
			row := rowFor(t, result, extension)
			require.Equal(t, apply.Failed(), row.Status())
			require.Equal(t, apply.ManagementDeclarative, row.Management())
			require.Contains(t, row.Diagnostic(), "declarative cell inspection failed")
			require.Contains(t, row.Diagnostic(), "live files match neither the desired bundle nor the recorded ownership token")
			require.Contains(t, row.Diagnostic(), "impact: no native action was attempted")
			require.Contains(t, row.Diagnostic(), "fix: repair the destination and rerun Home Manager")
			require.Equal(t, before, snapshotPath(t, base))
			_, statErr := os.Lstat(filepath.Join(base, "state", "installations.yaml"))
			require.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

func TestEachManagedCellRejectsContentAndModeDriftExactly(t *testing.T) {
	for _, extension := range cell.CanonicalExtensions() {
		for _, drift := range []string{"content", "mode"} {
			extension, drift := extension, drift
			t.Run(extension.String()+"/"+drift, func(t *testing.T) {
				t.Parallel()
				base := t.TempDir()
				controller, svc := productionService(t, base)
				coordinate, err := cell.New(artifact.HarnessOpenCode, extension)
				require.NoError(t, err)
				_, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
				require.NoError(t, err)
				component, err := controller.Descriptor().Component(extension)
				require.NoError(t, err)
				leaf := component.Bundle().Manifest().Entries()[0]
				leafPath := filepath.Join(destination(t, controller, extension), filepath.FromSlash(leaf.Path().String()))
				if drift == "content" {
					require.NoError(t, os.WriteFile(leafPath, []byte("managed-user-drift\n"), os.FileMode(leaf.Mode().Bits())))
				} else {
					require.NoError(t, os.Chmod(leafPath, 0o600))
				}
				before := snapshotPath(t, base)
				for _, enabled := range []bool{true, false} {
					result, applyErr := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: enabled, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
					require.NoError(t, applyErr)
					require.False(t, result.OK())
					require.Equal(t, apply.Failed(), result.Rows()[0].Status())
					require.Equal(t, before, snapshotPath(t, base))
				}
			})
		}
	}
}

func TestEachCellRejectsSymlinkAndWrongTypeBoundariesWithoutMutation(t *testing.T) {
	for _, extension := range cell.CanonicalExtensions() {
		for _, boundary := range []string{"root-symlink", "intermediate-symlink", "leaf-symlink", "root-wrong-type", "intermediate-wrong-type", "leaf-wrong-type"} {
			extension, boundary := extension, boundary
			t.Run(extension.String()+"/"+boundary, func(t *testing.T) {
				t.Parallel()
				base := t.TempDir()
				configRoot := filepath.Join(base, "config", "opencode")
				controller, err := host.New(configRoot)
				require.NoError(t, err)
				component, err := controller.Descriptor().Component(extension)
				require.NoError(t, err)
				root := destination(t, controller, extension)
				leaf := component.Bundle().Manifest().Entries()[0]
				leafPath := filepath.Join(root, filepath.FromSlash(leaf.Path().String()))
				outside := filepath.Join(base, "outside")
				require.NoError(t, os.MkdirAll(outside, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(outside, "sentinel"), []byte("outside\n"), 0o600))
				switch boundary {
				case "root-symlink":
					require.NoError(t, os.MkdirAll(filepath.Dir(root), 0o755))
					require.NoError(t, os.Symlink(outside, root))
				case "intermediate-symlink":
					require.NoError(t, os.MkdirAll(filepath.Dir(configRoot), 0o755))
					require.NoError(t, os.Symlink(outside, configRoot))
				case "leaf-symlink":
					require.NoError(t, os.MkdirAll(filepath.Dir(leafPath), 0o755))
					require.NoError(t, os.Symlink(filepath.Join(outside, "sentinel"), leafPath))
				case "root-wrong-type":
					require.NoError(t, os.MkdirAll(filepath.Dir(root), 0o755))
					require.NoError(t, os.WriteFile(root, []byte("wrong root\n"), 0o600))
				case "intermediate-wrong-type":
					require.NoError(t, os.MkdirAll(filepath.Dir(configRoot), 0o755))
					require.NoError(t, os.WriteFile(configRoot, []byte("wrong parent\n"), 0o600))
				case "leaf-wrong-type":
					require.NoError(t, os.MkdirAll(leafPath, 0o700))
				}
				_, svc := productionServiceAt(t, base, controller)
				before := snapshotPath(t, base)
				coordinate, err := cell.New(artifact.HarnessOpenCode, extension)
				require.NoError(t, err)
				result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
				require.NoError(t, err)
				require.False(t, result.OK())
				require.Equal(t, apply.Failed(), result.Rows()[0].Status())
				require.Equal(t, before, snapshotPath(t, base))
			})
		}
	}
}

func TestEachCellConflictRepairAndRerunConverges(t *testing.T) {
	for _, extension := range cell.CanonicalExtensions() {
		extension := extension
		t.Run(extension.String(), func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			controller, svc := productionService(t, base)
			component, err := controller.Descriptor().Component(extension)
			require.NoError(t, err)
			leaf := component.Bundle().Manifest().Entries()[0]
			leafPath := filepath.Join(destination(t, controller, extension), filepath.FromSlash(leaf.Path().String()))
			require.NoError(t, os.MkdirAll(filepath.Dir(leafPath), 0o755))
			require.NoError(t, os.WriteFile(leafPath, []byte("external conflict\n"), 0o600))
			before := snapshotPath(t, base)
			coordinate, err := cell.New(artifact.HarnessOpenCode, extension)
			require.NoError(t, err)
			result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.False(t, result.OK())
			require.Equal(t, before, snapshotPath(t, base))
			require.NoError(t, os.Remove(leafPath))
			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			require.Equal(t, apply.Completed(), result.Rows()[0].Status())
			assertBundleMaterialized(t, destination(t, controller, extension), component.Bundle())
		})
	}
}

func TestHookLifecyclePreservesEveryConfigSentinelByteAndMode(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	controller, svc := productionService(t, base)
	require.NoError(t, os.MkdirAll(controller.ConfigRoot(), 0o755))
	for index, name := range []host.ConfigFile{host.LegacyConfigJSON, host.OpenCodeJSON, host.OpenCodeJSONC} {
		path := filepath.Join(controller.ConfigRoot(), string(name))
		require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf("sentinel-%d\n", index)), os.FileMode(0o600+index)))
	}
	sentinels := snapshotConfigFiles(t, controller.ConfigRoot())
	coordinate, err := cell.New(artifact.HarnessOpenCode, cell.HooksAxis())
	require.NoError(t, err)
	for _, enabled := range []bool{true, true, false, false} {
		result, applyErr := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: enabled, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
		require.NoError(t, applyErr)
		require.True(t, result.OK())
		require.Equal(t, sentinels, snapshotConfigFiles(t, controller.ConfigRoot()))
		assertCanonicalHookLayout(t, controller, enabled)
	}
}

func assertCanonicalHookLayout(t *testing.T, controller host.Controller, installed bool) {
	t.Helper()
	require.Equal(t, filepath.Join(controller.ConfigRoot(), "plugins"), destination(t, controller, artifact.ExtensionHooks))
	hookPath := filepath.Join(controller.ConfigRoot(), "plugins", host.HookFile)
	info, err := os.Lstat(hookPath)
	if installed {
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular())
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	_, err = os.Lstat(filepath.Join(controller.ConfigRoot(), "plugin"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestTraversalRootIsRejected(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	_, err := host.New(base + string(os.PathSeparator) + "opencode" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "escape")
	require.Error(t, err)
}

func productionService(t *testing.T, base string) (host.Controller, *service.Service) {
	t.Helper()
	controller, err := host.New(filepath.Join(base, "config", "opencode"))
	require.NoError(t, err)
	return productionServiceAt(t, base, controller)
}

func productionServiceAt(t *testing.T, base string, controller host.Controller) (host.Controller, *service.Service) {
	t.Helper()
	stateDir := filepath.Join(base, "state")
	require.NoError(t, os.MkdirAll(stateDir, 0o700))
	registry, err := service.NewFileRegistry(filepath.Join(stateDir, "installations.yaml"))
	require.NoError(t, err)
	contracts := fakeSiblingContracts(t, base)
	contracts[ir.HarnessOpenCode] = controller.Contract()
	// Frontends own activator composition. One shared DirectFile activator serves
	// every harness contract rather than each host controller constructing one.
	policies := make([]apply.DirectFilePolicy, 0, len(cell.CanonicalCells()))
	for _, coordinate := range cell.CanonicalCells() {
		policy, policyErr := apply.PassThroughDirectFile(coordinate)
		require.NoError(t, policyErr)
		policies = append(policies, policy)
	}
	directFile, err := apply.NewDirectFileActivator(policies...)
	require.NoError(t, err)
	svc, err := service.New(service.Config{Registry: registry, Contracts: contracts, Activators: []apply.Activator{directFile}})
	require.NoError(t, err)
	return controller, svc
}

func fakeSiblingContracts(t *testing.T, base string) map[ir.HarnessID]activation.ActivationContract {
	t.Helper()
	out := map[ir.HarnessID]activation.ActivationContract{}
	for _, harness := range []artifact.Harness{artifact.HarnessClaudeCode, artifact.HarnessCodex} {
		var activations []activation.ComponentActivation
		for _, extension := range cell.CanonicalExtensions() {
			coordinate, _ := cell.New(harness, extension)
			strategy, err := activation.NewDirectFile(tinyBundle(t), filepath.Join(base, "unused", string(harness), extension.String()))
			require.NoError(t, err)
			bound, err := activation.NewComponentActivation(coordinate, strategy)
			require.NoError(t, err)
			activations = append(activations, bound)
		}
		exhaustive, err := activation.NewExhaustiveComponentActivations(activations[0], activations[1], activations[2])
		require.NoError(t, err)
		id, _ := activation.NewActivationContractID(string(harness) + "/test@1")
		probe, _ := activation.NewCommandSchema("unused", "--version")
		contract, err := activation.NewActivationContract(id, ir.HarnessID(harness), runtime.OpenCode1_18_29().Versions(), probe, exhaustive)
		require.NoError(t, err)
		out[ir.HarnessID(harness)] = contract
	}
	return out
}

func tinyBundle(t *testing.T) artifact.Bundle {
	t.Helper()
	path, _ := artifact.NewPath("unused")
	mode, _ := artifact.NewMode(0o644)
	content := []byte("unused\n")
	entry, _ := artifact.NewFileEntry(path, mode, artifact.DigestBytes(content))
	manifest, _ := artifact.NewManifest(entry)
	bundle, err := artifact.NewBundle(fstest.MapFS{"unused": &fstest.MapFile{Data: content, Mode: 0o644}}, manifest)
	require.NoError(t, err)
	return bundle
}

func modifiedBundle(t *testing.T, current artifact.Bundle) artifact.Bundle {
	t.Helper()
	source := fstest.MapFS{}
	entries := make([]artifact.Entry, 0, current.Manifest().Len())
	for index, entry := range current.Manifest().Entries() {
		file, err := current.Open(entry.Path().String())
		require.NoError(t, err)
		content, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		if index == 0 {
			content = append([]byte("prior-release\n"), content...)
		}
		source[entry.Path().String()] = &fstest.MapFile{Data: content, Mode: os.FileMode(entry.Mode().Bits())}
		oldEntry, err := artifact.NewFileEntry(entry.Path(), entry.Mode(), artifact.DigestBytes(content))
		require.NoError(t, err)
		entries = append(entries, oldEntry)
	}
	manifest, err := artifact.NewManifest(entries...)
	require.NoError(t, err)
	bundle, err := artifact.NewBundle(source, manifest)
	require.NoError(t, err)
	return bundle
}

func materializeBundle(t *testing.T, root string, bundle artifact.Bundle) {
	t.Helper()
	for _, entry := range bundle.Manifest().Entries() {
		file, err := bundle.Open(entry.Path().String())
		require.NoError(t, err)
		content, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		path := filepath.Join(root, filepath.FromSlash(entry.Path().String()))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, content, os.FileMode(entry.Mode().Bits())))
	}
}

func selectionWithOpenCodeEnabled() (selection.Selection, error) {
	states := make(map[cell.Cell]bool, 9)
	for _, coordinate := range cell.CanonicalCells() {
		states[coordinate] = coordinate.Harness() == artifact.HarnessOpenCode
	}
	return selection.New(states)
}

func assertBundleMaterialized(t *testing.T, root string, bundle artifact.Bundle) {
	t.Helper()
	for _, entry := range bundle.Manifest().Entries() {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path().String())))
		require.NoError(t, err)
		file, err := bundle.Open(entry.Path().String())
		require.NoError(t, err)
		want, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.Equal(t, want, got)
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(entry.Path().String())))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(entry.Mode().Bits()), info.Mode().Perm())
	}
}

type pathSnapshot struct {
	Path    string
	Mode    os.FileMode
	Content string
}

func snapshotPath(t *testing.T, root string) []pathSnapshot {
	t.Helper()
	if _, err := os.Lstat(root); errorsIsNotExist(err) {
		return []pathSnapshot{{Path: ".", Mode: 0, Content: "<absent>"}}
	} else {
		require.NoError(t, err)
	}
	var snapshots []pathSnapshot
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content := ""
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content = string(raw)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			content = target
		}
		snapshots = append(snapshots, pathSnapshot{Path: filepath.ToSlash(rel), Mode: info.Mode(), Content: content})
		return nil
	}))
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Path < snapshots[j].Path })
	return snapshots
}

func errorsIsNotExist(err error) bool { return err != nil && os.IsNotExist(err) }

func rowFor(t *testing.T, result apply.Result, extension artifact.Extension) apply.ActionRow {
	t.Helper()
	for _, row := range result.Rows() {
		if row.Cell().Harness() == artifact.HarnessOpenCode && row.Cell().Extension() == extension {
			return row
		}
	}
	require.FailNow(t, "OpenCode result row missing", extension.String())
	return apply.ActionRow{}
}

func snapshotConfigFiles(t *testing.T, root string) []pathSnapshot {
	t.Helper()
	var out []pathSnapshot
	for _, name := range []host.ConfigFile{host.LegacyConfigJSON, host.OpenCodeJSON, host.OpenCodeJSONC} {
		path := filepath.Join(root, string(name))
		info, err := os.Stat(path)
		require.NoError(t, err)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		out = append(out, pathSnapshot{Path: string(name), Mode: info.Mode(), Content: string(bytes.Clone(raw))})
	}
	return out
}

func destination(t *testing.T, controller host.Controller, extension artifact.Extension) string {
	t.Helper()
	value, err := controller.Destination(extension)
	require.NoError(t, err)
	return value
}

func configStrings(values []host.ConfigFile) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for current := wd; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		require.NotEqual(t, current, filepath.Dir(current), "go.mod not found above %s", wd)
	}
}
