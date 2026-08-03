package effects_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEffectsImportsNoTaskPackage proves the one-way package edge from the
// effects side: internal/effects must never import an internal/task package.
// The import direction is task -> effects; effects passes a verified proof to
// task's protected commit, but effects never depends on task. The reverse edge
// (task imports effects) is proven by the compilepass consumer fixture and, when
// the task package lands, by that package compiling against this one.
func TestEffectsImportsNoTaskPackage(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	inspected := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		// Only production files define the package's own import surface; test
		// files may legitimately import anything and are excluded.
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		inspected++
		file, err := parser.ParseFile(fset, filepath.Join(".", entry.Name()), nil, parser.ImportsOnly)
		require.NoError(t, err, entry.Name())
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			assert.NotContains(t, path, "internal/task",
				"%s imports %q: effects must never import the task package (the edge is task -> effects)", entry.Name(), path)
		}
	}
	require.Positive(t, inspected, "expected to inspect at least one production file")
}
