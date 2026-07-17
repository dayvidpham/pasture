package scan_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPackageIsProvenanceAndTaskFree enforces this package's own dependency
// boundary, mirroring internal/codegen/ir's TestPackageIsProvenanceFree (see
// ir/document_ast_test.go): the scanner independently walks and parses
// Markdown in memory and reports a classified inventory; it must never pull
// in Provenance's durable-store client or the (not-yet-built) internal/task
// package transitively, so any future in-memory tooling can depend on it
// without dragging in a database or task-backend dependency.
func TestPackageIsProvenanceAndTaskFree(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "list", "-deps", "github.com/dayvidpham/pasture/internal/codegen/scan")
	cmd.Dir = repoRootForGuard(t)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "go list -deps must resolve internal/codegen/scan's complete dependency graph:\n%s", output)

	deps := strings.Split(strings.TrimSpace(string(output)), "\n")
	require.NotEmpty(t, deps, "go list -deps returned no dependencies")

	forbidden := []struct {
		path        string
		prefixMatch bool
	}{
		{path: "github.com/dayvidpham/provenance", prefixMatch: true},
		{path: "github.com/dayvidpham/pasture/internal/tasks", prefixMatch: true},
	}

	for _, dep := range deps {
		for _, module := range forbidden {
			matches := dep == module.path || (module.prefixMatch && strings.HasPrefix(dep, module.path+"/"))
			assert.False(t, matches,
				"what: internal/codegen/scan (production code) transitively depends on %q, which violates its dependency-free boundary; "+
					"why: the scanner must stay usable as in-memory tooling with no durable-store client or task-backend dependency; "+
					"where: go list -deps github.com/dayvidpham/pasture/internal/codegen/scan (TestPackageIsProvenanceAndTaskFree); "+
					"phase: dependency boundary guard; "+
					"impact: a future caller could not depend on this package without also pulling in %q; "+
					"fix: remove the import that pulled in %q",
				dep, module.path, module.path)
		}
	}
}

func repoRootForGuard(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the scan package directory")
		}
		dir = parent
	}
}
