package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/stretchr/testify/require"
)

// Compare the raw snapshot, not just installer bundles: target-level manifests
// and verbatim documents must participate in the same complete inventory.
func TestEmbeddedAssetsMatchCodexHarness(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	require.NoError(t, err)
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	live, err := codegen.EmitHarness(codegen.RepoRoots(root), codegen.CodexTarget, codegen.GenerateOptions{Write: false})
	require.NoError(t, err)
	want := make(map[string]string, len(live))
	for _, file := range live {
		rel, err := filepath.Rel(root, file.Path)
		require.NoError(t, err)
		name := filepath.ToSlash(rel)
		_, duplicate := want[name]
		require.False(t, duplicate, "canonical emitter duplicated %s", name)
		want[name] = file.Content
	}
	files, err := decodeSnapshot(generatedSnapshot)
	require.NoError(t, err)
	got := make(map[string]string, len(files))
	for _, file := range files {
		_, duplicate := got[file.Path]
		require.False(t, duplicate, "embedded snapshot duplicated %s; run make generate", file.Path)
		got[file.Path] = file.Content
	}
	require.NotEmpty(t, want)
	for name, content := range want {
		actual, exists := got[name]
		require.True(t, exists, "Codex embedded snapshot is missing %s; run make generate", name)
		require.True(t, content == actual, "Codex embedded bytes drifted for %s; run make generate", name)
	}
	for name := range got {
		_, exists := want[name]
		require.True(t, exists, "Codex embedded snapshot retains stale %s; run make generate", name)
	}
}
