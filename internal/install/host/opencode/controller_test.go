package opencode_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
		ActivationContract string   `json:"activation_contract"`
		VersionProbe       []string `json:"version_probe"`
		SkillsRoot         string   `json:"skills_root"`
		AgentsRoot         string   `json:"agents_root"`
		HooksRoot          string   `json:"hooks_root"`
		HookFile           string   `json:"hook_file"`
		ConfigReadOrder    []string `json:"config_read_order"`
		NativeWriterOrder  []string `json:"native_writer_order"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, fixture.ActivationContract, controller.Contract().ID().String())
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
			coordinate, err := cell.New(artifact.HarnessOpenCode, extension)
			require.NoError(t, err)
			result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			root := destination(t, controller, extension)
			component, err := controller.Descriptor().Component(extension)
			require.NoError(t, err)
			assertBundleMaterialized(t, root, component.Bundle())
			for _, sibling := range cell.CanonicalExtensions() {
				if sibling != extension {
					_, statErr := os.Lstat(destination(t, controller, sibling))
					require.ErrorIs(t, statErr, os.ErrNotExist)
				}
			}

			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			unrelated := filepath.Join(root, "user-owned.txt")
			require.NoError(t, os.WriteFile(unrelated, []byte("preserve\n"), 0o600))
			result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
			require.NoError(t, err)
			require.True(t, result.OK())
			for _, entry := range component.Bundle().Manifest().Entries() {
				_, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Path().String())))
				require.ErrorIs(t, statErr, os.ErrNotExist)
			}
			preserved, err := os.ReadFile(unrelated)
			require.NoError(t, err)
			require.Equal(t, "preserve\n", string(preserved))
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
		})
	}
}

func TestExternalExactMatchRemainsUnrecordedAndUnremoved(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	controller, svc := productionService(t, base)
	coordinate, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	_, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(base, "state", "installations.yaml")))

	_, svc = productionServiceAt(t, base, controller)
	result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, err)
	require.Equal(t, apply.ManagementExternal, result.Rows()[0].Management())
	result, err = svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, err)
	require.Equal(t, apply.NoOp(), result.Rows()[0].Status())
	assertBundleMaterialized(t, destination(t, controller, artifact.ExtensionSkills), controller.Descriptor().Skills().Bundle())
}

func TestHomeManagerRowsAreDeclarativeReadOnlyFacts(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	controller, svc := productionService(t, base)
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
			_, statErr := os.Lstat(destination(t, controller, row.Cell().Extension()))
			require.ErrorIs(t, statErr, os.ErrNotExist)
		}
	}
	_, err = os.Lstat(filepath.Join(base, "state", "installations.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestManagedDriftIsRejectedWithoutOverwriteOrRemoval(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	controller, svc := productionService(t, base)
	coordinate, _ := cell.New(artifact.HarnessOpenCode, cell.HooksAxis())
	_, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, err)
	hook := filepath.Join(destination(t, controller, artifact.ExtensionHooks), host.HookFile)
	require.NoError(t, os.WriteFile(hook, []byte("user-modified\n"), 0o644))
	result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: false, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, err)
	require.False(t, result.OK())
	content, err := os.ReadFile(hook)
	require.NoError(t, err)
	require.Equal(t, "user-modified\n", string(content))
}

func TestSymlinkAndTraversalRootsRejectWithoutTouchingTargets(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	_, err := host.New(base + string(os.PathSeparator) + "opencode" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "escape")
	require.Error(t, err)

	controller, svc := productionService(t, base)
	target := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(target, 0o755))
	skillsRoot := destination(t, controller, artifact.ExtensionSkills)
	require.NoError(t, os.MkdirAll(filepath.Dir(skillsRoot), 0o755))
	require.NoError(t, os.Symlink(target, skillsRoot))
	coordinate, _ := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	result, err := svc.ApplyCell(context.Background(), service.CellRequest{Cell: coordinate, Enabled: true, Scope: apply.GlobalScope(), Source: apply.InstallerSource()})
	require.NoError(t, err)
	require.False(t, result.OK())
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	require.Empty(t, entries)
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
	svc, err := service.New(service.Config{Registry: registry, Contracts: contracts, Activators: []apply.Activator{controller.Activator()}})
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
		contract, err := activation.NewActivationContract(id, ir.HarnessID(harness), runtime.OpenCode1_18_10().Versions(), probe, exhaustive)
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
	}
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
