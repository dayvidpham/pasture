package definitions_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/definitions"
	"github.com/stretchr/testify/require"
)

// The source guards prove the committed generation-time inventories still
// match the filesystem: a changed dependency fails here with the regeneration
// and version-bump repair, so a silent source drift can never ride an old
// document identity.

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	absolute, err := filepath.Abs(root)
	require.NoError(t, err)
	absolute = filepath.Clean(absolute)
	_, err = os.Stat(filepath.Join(absolute, "go.mod"))
	require.NoError(t, err, "source guard must resolve the module root")
	return absolute
}

func hashFile(t *testing.T, root, path string) string {
	t.Helper()
	require.False(t, filepath.IsAbs(path), "inventory path must be repository-relative")
	content, err := os.ReadFile(filepath.Join(root, path))
	require.NoError(t, err, "inventory source must exist")
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestEpochSourceInventoryMatchesFilesystem(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	inventory, err := definitions.EpochSourceInventory()
	require.NoError(t, err)
	require.NotEmpty(t, inventory)
	previous := ""
	seen := make(map[string]bool, len(inventory))
	for _, entry := range inventory {
		require.False(t, seen[entry.Path], "inventory path duplicated")
		seen[entry.Path] = true
		require.Greater(t, entry.Path, previous, "inventory must stay sorted")
		previous = entry.Path
		require.Equal(t, hashFile(t, root, entry.Path), entry.SHA256, "source changed: regenerate and bump the document version")
	}
	// Coverage: every production engine source is inventoried. A file the
	// engine adds later fails here until it is listed or explicitly
	// classified, so no execution source escapes the manifest.
	var production []string
	entries, err := os.ReadDir(filepath.Join(root, "internal", "engine"))
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "export_test.go" {
			continue // test-only accessor, never epoch execution
		}
		production = append(production, "internal/engine/"+name)
	}
	slices.Sort(production)
	var inventoried []string
	for _, entry := range inventory {
		inventoried = append(inventoried, entry.Path)
	}
	require.Equal(t, production, inventoried, "engine execution source without manifest entry")
}

func TestInterpreterSourceInventoryMatchesFilesystem(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	inventory, err := definitions.InterpreterSourceInventory()
	require.NoError(t, err)
	expected := []string{
		"internal/lifecycle/backend/backend.go",
		"internal/lifecycle/legalize/legalize.go",
		"internal/lifecycle/middleend/evaluate.go",
		"internal/lifecycle/model/interpreted.go",
		"internal/lifecycle/receipt/interpreted.go",
		"internal/lifecycle/waist/consultation.go",
		"internal/lifecycle/waist/decision.go",
		"internal/lifecycle/waist/event.go",
		"internal/runtime/lifecycle.go",
	}
	var inventoried []string
	for _, entry := range inventory {
		inventoried = append(inventoried, entry.Path)
		require.Equal(t, hashFile(t, root, entry.Path), entry.SHA256, "source changed: regenerate and bump the document version")
	}
	require.Equal(t, expected, inventoried, "interpretation pipeline source without manifest entry")
	// The legalize stage is a single-file owner: every production source in
	// its directory must be inventoried, so the transform cannot gain an
	// unlisted file.
	entries, err := os.ReadDir(filepath.Join(root, "internal", "lifecycle", "legalize"))
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		require.Contains(t, inventoried, "internal/lifecycle/legalize/"+name)
	}
}
