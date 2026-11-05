package apply_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/artifact"
	"github.com/dayvidpham/pasture/internal/install/activation"
	"github.com/dayvidpham/pasture/internal/install/apply"
	"github.com/dayvidpham/pasture/internal/install/cell"
	"github.com/dayvidpham/pasture/internal/install/registry"
)

// upgradeBundle publishes one direct-file bundle whose manifest is derived from
// the exact bytes it serves, so presence and digest comparisons stay honest.
func upgradeBundle(t *testing.T, files map[string]string) artifact.Bundle {
	t.Helper()
	source := fstest.MapFS{}
	entries := make([]artifact.Entry, 0, len(files))
	for name, body := range files {
		source[name] = &fstest.MapFile{Data: []byte(body), Mode: 0o644}
		path, err := artifact.NewPath(name)
		require.NoError(t, err)
		mode, err := artifact.NewMode(0o644)
		require.NoError(t, err)
		entry, err := artifact.NewFileEntry(path, mode, artifact.DigestBytes([]byte(body)))
		require.NoError(t, err)
		entries = append(entries, entry)
	}
	manifest, err := artifact.NewManifest(entries...)
	require.NoError(t, err)
	bundle, err := artifact.NewBundle(source, manifest)
	require.NoError(t, err)
	return bundle
}

func upgradeBinding(t *testing.T, coordinate cell.Cell, bundle artifact.Bundle, root string) (registry.Key, activation.ComponentActivation) {
	t.Helper()
	strategy, err := activation.NewDirectFile(bundle, root)
	require.NoError(t, err)
	binding, err := activation.NewComponentActivation(coordinate, strategy)
	require.NoError(t, err)
	key, err := registry.GlobalKey(coordinate)
	require.NoError(t, err)
	return key, binding
}

func upgradeActivator(t *testing.T, coordinate cell.Cell) *apply.DirectFileActivator {
	t.Helper()
	policy, err := apply.PassThroughDirectFile(coordinate)
	require.NoError(t, err)
	activator, err := apply.NewDirectFileActivator(policy)
	require.NoError(t, err)
	return activator
}

// A bundle that publishes a skill the recorded install never owned is an
// upgrade, not a damaged tree: inspection must classify it as installed and
// Ensure must materialize the new leaf.
func TestInspectAttributesUnrecordedAbsentLeafToNewerBundleVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coordinate, err := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	require.NoError(t, err)
	activator := upgradeActivator(t, coordinate)
	root := filepath.Join(t.TempDir(), "skills")

	installed := upgradeBundle(t, map[string]string{"existing.txt": "existing\n"})
	key, installedBinding := upgradeBinding(t, coordinate, installed, root)
	seeded, err := activator.Ensure(ctx, apply.InstallerSource(), key, installedBinding, nil)
	require.NoError(t, err)
	require.Equal(t, registry.ObservationInstalled, seeded.Observation)
	require.NotNil(t, seeded.Record)
	require.True(t, seeded.Record.Managed())
	require.Len(t, seeded.Record.Leaves(), 1)

	upgraded := upgradeBundle(t, map[string]string{
		"existing.txt": "existing\n",
		"added.txt":    "added\n",
	})
	_, upgradedBinding := upgradeBinding(t, coordinate, upgraded, root)

	out, err := activator.Inspect(ctx, apply.InstallerSource(), key, upgradedBinding, seeded.Record)
	require.NoError(t, err, "a newly published leaf must not read as a partial install")
	require.Equal(t, registry.ObservationInstalled, out.Observation)

	ensured, err := activator.Ensure(ctx, apply.InstallerSource(), key, upgradedBinding, seeded.Record)
	require.NoError(t, err)
	require.Equal(t, registry.ObservationInstalled, ensured.Observation)
	require.NotNil(t, ensured.Record)
	require.Len(t, ensured.Record.Leaves(), 2)

	body, err := os.ReadFile(filepath.Join(root, "added.txt"))
	require.NoError(t, err)
	require.Equal(t, "added\n", string(body))
}

// A leaf the recorded install owned disappearing from disk stays a conflict,
// even when the inspected bundle also publishes brand-new leaves.
func TestInspectStillFaultsWhenRecordedLeafDisappears(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coordinate, err := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	require.NoError(t, err)
	activator := upgradeActivator(t, coordinate)
	root := filepath.Join(t.TempDir(), "skills")

	installed := upgradeBundle(t, map[string]string{
		"existing.txt": "existing\n",
		"removed.txt":  "removed\n",
	})
	key, installedBinding := upgradeBinding(t, coordinate, installed, root)
	seeded, err := activator.Ensure(ctx, apply.InstallerSource(), key, installedBinding, nil)
	require.NoError(t, err)
	require.NotNil(t, seeded.Record)

	require.NoError(t, os.Remove(filepath.Join(root, "removed.txt")))

	upgraded := upgradeBundle(t, map[string]string{
		"existing.txt": "existing\n",
		"removed.txt":  "removed\n",
		"added.txt":    "added\n",
	})
	_, upgradedBinding := upgradeBinding(t, coordinate, upgraded, root)

	_, err = activator.Inspect(ctx, apply.InstallerSource(), key, upgradedBinding, seeded.Record)
	require.Error(t, err)
	require.Contains(t, err.Error(), "only part of the bundle is present")
}

// Without a recorded install there is no version to compare against, so a
// partially materialized external tree must still be refused.
func TestInspectRejectsPartialTreeWithoutRecordedIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coordinate, err := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	require.NoError(t, err)
	activator := upgradeActivator(t, coordinate)
	root := filepath.Join(t.TempDir(), "skills")

	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "existing.txt"), []byte("existing\n"), 0o644))

	bundle := upgradeBundle(t, map[string]string{
		"existing.txt": "existing\n",
		"added.txt":    "added\n",
	})
	key, binding := upgradeBinding(t, coordinate, bundle, root)

	_, err = activator.Inspect(ctx, apply.InstallerSource(), key, binding, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "only part of the bundle is present"), "err=%v", err)
}

// A bundle that stops publishing a skill retires the managed leaf and the
// Pasture-created directory that held it.
func TestEnsureRetiresLeavesDroppedByNewerBundle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coordinate, err := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	require.NoError(t, err)
	activator := upgradeActivator(t, coordinate)
	root := filepath.Join(t.TempDir(), "skills")

	installed := upgradeBundle(t, map[string]string{
		"keep.txt":           "keep\n",
		"skill-old/SKILL.md": "old\n",
	})
	key, installedBinding := upgradeBinding(t, coordinate, installed, root)
	seeded, err := activator.Ensure(ctx, apply.InstallerSource(), key, installedBinding, nil)
	require.NoError(t, err)
	require.NotNil(t, seeded.Record)
	require.Len(t, seeded.Record.Leaves(), 2)
	require.True(t, containsPath(seeded.Record.CreatedDirs(), "skill-old"), "createdDirs=%v", seeded.Record.CreatedDirs())

	upgraded := upgradeBundle(t, map[string]string{"keep.txt": "keep\n"})
	_, upgradedBinding := upgradeBinding(t, coordinate, upgraded, root)

	out, err := activator.Inspect(ctx, apply.InstallerSource(), key, upgradedBinding, seeded.Record)
	require.NoError(t, err)
	require.Equal(t, registry.ObservationInstalled, out.Observation)

	ensured, err := activator.Ensure(ctx, apply.InstallerSource(), key, upgradedBinding, seeded.Record)
	require.NoError(t, err)
	require.NotNil(t, ensured.Record)
	require.Len(t, ensured.Record.Leaves(), 1)
	require.False(t, containsPath(ensured.Record.CreatedDirs(), "skill-old"), "createdDirs=%v", ensured.Record.CreatedDirs())

	_, statErr := os.Stat(filepath.Join(root, "skill-old"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	_, statErr = os.Stat(filepath.Join(root, "skill-old", "SKILL.md"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	body, readErr := os.ReadFile(filepath.Join(root, "keep.txt"))
	require.NoError(t, readErr)
	require.Equal(t, "keep\n", string(body))
}

// A leaf the user changed is never deleted by an upgrade.
func TestEnsureRefusesToRetireDriftedLeaf(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coordinate, err := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	require.NoError(t, err)
	activator := upgradeActivator(t, coordinate)
	root := filepath.Join(t.TempDir(), "skills")

	installed := upgradeBundle(t, map[string]string{
		"keep.txt":  "keep\n",
		"stale.txt": "stale\n",
	})
	key, installedBinding := upgradeBinding(t, coordinate, installed, root)
	seeded, err := activator.Ensure(ctx, apply.InstallerSource(), key, installedBinding, nil)
	require.NoError(t, err)
	require.NotNil(t, seeded.Record)

	require.NoError(t, os.WriteFile(filepath.Join(root, "stale.txt"), []byte("user edit\n"), 0o644))

	upgraded := upgradeBundle(t, map[string]string{"keep.txt": "keep\n"})
	_, upgradedBinding := upgradeBinding(t, coordinate, upgraded, root)

	_, err = activator.Ensure(ctx, apply.InstallerSource(), key, upgradedBinding, seeded.Record)
	require.Error(t, err)
	require.Contains(t, err.Error(), "drifted from its ownership token")

	body, readErr := os.ReadFile(filepath.Join(root, "stale.txt"))
	require.NoError(t, readErr)
	require.Equal(t, "user edit\n", string(body))
}

// An externally owned tree is inspected but never pruned.
func TestEnsureNeverRetiresLeavesFromExternalTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	coordinate, err := cell.New(artifact.HarnessOpenCode, cell.SkillsAxis())
	require.NoError(t, err)
	activator := upgradeActivator(t, coordinate)
	root := filepath.Join(t.TempDir(), "skills")

	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "external.txt"), []byte("external\n"), 0o644))

	externalPath, err := artifact.NewPath("external.txt")
	require.NoError(t, err)
	externalMode, err := artifact.NewMode(0o644)
	require.NoError(t, err)
	externalLeaf, err := registry.NewLeaf(externalPath, artifact.RegularFileType(), externalMode, artifact.DigestBytes([]byte("external\n")))
	require.NoError(t, err)
	key, err := registry.GlobalKey(coordinate)
	require.NoError(t, err)
	prior, err := registry.NewRecord(registry.RecordInput{
		Key:           key,
		Source:        registry.SourceInstaller,
		Strategy:      activation.DirectFileKindValue(),
		Managed:       false,
		Leaves:        []registry.Leaf{externalLeaf},
		Observation:   registry.ObservationInstalled,
		Trust:         registry.TrustNotApplicable,
		LastOperation: registry.OperationInspect,
		LastOutcome:   registry.OutcomeCompleted,
	})
	require.NoError(t, err)

	bundle := upgradeBundle(t, map[string]string{"keep.txt": "keep\n"})
	_, binding := upgradeBinding(t, coordinate, bundle, root)

	out, err := activator.Ensure(ctx, apply.InstallerSource(), key, binding, &prior)
	require.NoError(t, err)
	require.Equal(t, registry.ObservationInstalled, out.Observation)

	body, readErr := os.ReadFile(filepath.Join(root, "external.txt"))
	require.NoError(t, readErr)
	require.Equal(t, "external\n", string(body))
}

func containsPath(paths []artifact.Path, want string) bool {
	for _, path := range paths {
		if path.String() == want {
			return true
		}
	}
	return false
}
