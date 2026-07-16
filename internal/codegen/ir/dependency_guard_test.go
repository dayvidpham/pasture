package ir_test

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forbiddenModule is one dependency boundary TestPackageIsProvenanceFree
// enforces, matched either by exact import path or by module-subtree prefix.
type forbiddenModule struct {
	path        string
	prefixMatch bool
	why         string
}

// forbiddenModules is the provenance-free guard's rule set. The two entries
// deliberately use different matching:
//
//   - "github.com/dayvidpham/provenance" is prefix-matched (path itself, or
//     path+"/..."): provenance is a multi-package module (pkg/ptypes,
//     pkg/namespace, internal/sqlite, internal/graph, internal/helpers, …),
//     and a dependency on any one subpackage — not just the module root —
//     would just as surely pull in the durable-store client this guard
//     exists to keep out. `go list -deps` of internal/tasks, which
//     legitimately imports provenance, lists all of those subpaths
//     individually, confirming they are real, reachable import paths, not a
//     hypothetical shape.
//   - "github.com/dayvidpham/pasture/pkg/protocol" is matched EXACTLY, not by
//     prefix: its child pkg/protocol/portable is precisely the dependency-
//     free package the round-1 fix moved the portable ref types into (see
//     portable_refs.go), and internal/codegen/ir legitimately depends on it
//     today. Prefix-matching this root would make that real, intentional
//     dependency indistinguishable from the pkg/protocol facade this guard
//     forbids, breaking the guard against the package's own correct import.
//
// This asymmetry must survive future edits: do not prefix-match pkg/protocol
// without first re-checking pkg/protocol/portable's own dependency graph.
var forbiddenModules = []forbiddenModule{
	{
		path:        "github.com/dayvidpham/provenance",
		prefixMatch: true,
		why: "this package must compile documents entirely in memory with no durable-store " +
			"client, so it stays importable by any future in-memory tooling without dragging in " +
			"provenance's SQLite-backed task tracker (root package or any subpackage — " +
			"internal/sqlite in particular is exactly the durable-store client this guard exists " +
			"to keep out)",
	},
	{
		path:        "github.com/dayvidpham/pasture/pkg/protocol",
		prefixMatch: false,
		why: "pkg/protocol's TaskTracker facade imports provenance; this package must use only " +
			"the dependency-free pkg/protocol/portable child, which this exact-path (not prefix) " +
			"match deliberately still allows",
	},
}

// matchesForbiddenModule reports whether dep violates module, either by
// exact match or, when module.prefixMatch is set, by naming a subpackage one
// path segment or deeper under module.path.
func matchesForbiddenModule(dep string, module forbiddenModule) bool {
	if dep == module.path {
		return true
	}
	return module.prefixMatch && strings.HasPrefix(dep, module.path+"/")
}

// TestPackageIsProvenanceFree enforces the #38 package boundary: internal/
// codegen/ir compiles a Document entirely in memory and must never pull in a
// durable-store client (github.com/dayvidpham/provenance, root or any
// subpackage) transitively. A prior revision imported pkg/protocol's
// TaskTracker facade for five small identity types; that facade itself
// imports provenance, silently violating the boundary. The identity types
// now live in the dependency-free pkg/protocol/portable package (see
// pkg/protocol/portable/portable_refs.go); this test fails loudly if the
// provenance edge ever comes back, under any import path — including a
// provenance subpackage reached without the bare root package, and
// including indirectly through pkg/protocol itself.
func TestPackageIsProvenanceFree(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "list", "-deps", "github.com/dayvidpham/pasture/internal/codegen/ir")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "go list -deps must resolve internal/codegen/ir's complete dependency graph:\n%s", output)

	deps := strings.Split(strings.TrimSpace(string(output)), "\n")
	require.NotEmpty(t, deps, "go list -deps returned no dependencies")

	for _, dep := range deps {
		for _, module := range forbiddenModules {
			assert.False(t, matchesForbiddenModule(dep, module),
				"what: internal/codegen/ir (production code) transitively depends on %q, which "+
					"violates the forbidden module boundary %q; "+
					"why: %s; "+
					"where: go list -deps github.com/dayvidpham/pasture/internal/codegen/ir "+
					"(TestPackageIsProvenanceFree); "+
					"phase: dependency boundary guard; "+
					"impact: the #38 provenance-free package boundary is violated; "+
					"fix: remove the import that pulled in %q — portable cross-boundary identities "+
					"belong in pkg/protocol/portable, not pkg/protocol or provenance",
				dep, module.path, module.why, dep)
		}
	}
}

// TestMatchesForbiddenModuleHandlesRealProvenanceSubpackages is a table-driven
// regression proving the prefix-matching helper itself catches a real
// provenance subpackage path (not just a hypothetical shape) while still
// allowing pkg/protocol/portable — i.e. the exact asymmetry
// TestPackageIsProvenanceFree depends on. The five provenance subpackage
// paths below are the ones `go list -deps
// github.com/dayvidpham/pasture/internal/tasks` (a package that legitimately
// imports provenance elsewhere in this module) actually lists.
func TestMatchesForbiddenModuleHandlesRealProvenanceSubpackages(t *testing.T) {
	t.Parallel()

	provenance := forbiddenModules[0]
	require.Equal(t, "github.com/dayvidpham/provenance", provenance.path)
	require.True(t, provenance.prefixMatch)

	protocol := forbiddenModules[1]
	require.Equal(t, "github.com/dayvidpham/pasture/pkg/protocol", protocol.path)
	require.False(t, protocol.prefixMatch)

	caught := []string{
		"github.com/dayvidpham/provenance",
		"github.com/dayvidpham/provenance/pkg/ptypes",
		"github.com/dayvidpham/provenance/pkg/namespace",
		"github.com/dayvidpham/provenance/internal/sqlite",
		"github.com/dayvidpham/provenance/internal/graph",
		"github.com/dayvidpham/provenance/internal/helpers",
	}
	for _, dep := range caught {
		assert.True(t, matchesForbiddenModule(dep, provenance), "%q must be caught as a provenance subpackage", dep)
	}
	assert.False(t, matchesForbiddenModule("github.com/dayvidpham/provenance-other-module", provenance),
		"a module whose name merely starts with the forbidden path (no '/' boundary) must not be caught")

	assert.True(t, matchesForbiddenModule("github.com/dayvidpham/pasture/pkg/protocol", protocol))
	assert.False(t, matchesForbiddenModule("github.com/dayvidpham/pasture/pkg/protocol/portable", protocol),
		"pkg/protocol/portable is the deliberately allowed dependency-free child and must not be caught")
}

// TestPackageHasNoFilesystemOrPublisherImport enforces the companion half of
// the #38 boundary at the source level: Compile renders entirely in memory
// (see document.go's Compile doc comment) and this package's own non-test
// Go files must never directly import a filesystem-writing or network-
// publishing package. This is a source-level (not transitive) check because
// the standard library's crypto packages transitively touch os/io/fs for
// entropy sourcing; that is not a publisher dependency of this package.
func TestPackageHasNoFilesystemOrPublisherImport(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRoot(t), "internal", "codegen", "ir")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	// This enumeration is a curated allowlist-complement, not a semantic
	// "does I/O" check (source-level, not transitive — see the doc comment
	// above): every entry here is a stdlib package that can perform
	// filesystem or network side effects on its own, without importing any
	// other listed entry. "net" is included alongside "net/http" because raw
	// TCP/UDP dialing is a publisher without importing net/http at all;
	// "syscall" and "os/user" are included for the same reason as "os".
	forbidden := map[string]bool{
		"os":            true,
		"os/exec":       true,
		"os/user":       true,
		"io/fs":         true,
		"io/ioutil":     true,
		"path/filepath": true,
		"net":           true,
		"net/http":      true,
		"syscall":       true,
		"database/sql":  true,
	}

	fileSet := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		require.NoError(t, err, "parse imports of %s", path)
		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			require.NoError(t, unquoteErr)
			assert.False(t, forbidden[importPath],
				"what: %s directly imports %q; "+
					"why: internal/codegen/ir is an in-memory typed IR and compiler with no filesystem "+
					"publisher or network dependency — Compile documents its own \"no publisher dependency\" "+
					"contract; "+
					"where: %s (TestPackageHasNoFilesystemOrPublisherImport); "+
					"phase: dependency boundary guard; "+
					"impact: a caller could no longer trust that constructing or compiling a Document is "+
					"side-effect free; "+
					"fix: move filesystem or network I/O into a separate publisher package that consumes "+
					"the RenderedTree this package already returns",
				name, importPath, path,
			)
		}
	}
	require.Positive(t, checked, "no non-test .go files were found under %s", dir)
}

// repoRoot walks up from the test's working directory (the ir package
// directory) to the module root, identified by go.mod.
func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the ir package directory")
		}
		dir = parent
	}
}
