package capabilitylint_test

import (
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/capabilitylint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModuleSourceHasNoCapabilityIdentityLintFindings is capabilitylint's
// standing enforcement gate: it walks every real (non-test, non-testdata,
// non-nested-module) .go file in this module and requires zero
// capabilitylint findings, so `go test ./...` — this repository's mandatory
// quality gate (AGENTS.md: "The -race flag is mandatory for all test runs",
// run against `go test ./...`) — actually protects every real
// DefineCapability/MustDefineCapability call site, not just this package's
// own fixtures.
//
// Test files (_test.go) are deliberately excluded: DefineCapability's own
// error-returning form exists precisely for dynamic/user-supplied inputs
// (see ir.DefineCapability's doc comment), and this package's and ir's own
// tests legitimately exercise that form with table-driven, non-canonical
// identities to test validation itself — those are not canonical
// declaration sites and are out of this rule's scope. testdata/ directories
// (this package's own bypass/acceptance fixtures, and #38's ir
// compile-fail/compile-pass fixtures) are excluded for the same reason: they
// are intentionally either violating or exercising the rule already, not
// production declaration sites. legacy/temporal is a separate nested Go
// module (its own go.mod) and is out of "this module" by definition.
func TestModuleSourceHasNoCapabilityIdentityLintFindings(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	var findings []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "legacy", "testdata", "node_modules", "bin":
				return filepath.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		fileFindings, checkErr := capabilitylint.CheckFile(fset, path, nil)
		require.NoError(t, checkErr, "capabilitylint must be able to parse every real module source file: %s", path)
		for _, finding := range fileFindings {
			position := fset.Position(finding.Pos)
			relative, relErr := filepath.Rel(root, path)
			require.NoError(t, relErr)
			findings = append(findings, relative+":"+position.String()+": "+finding.Message)
		}
		return nil
	})
	require.NoError(t, err, "walking the module source tree from %s", root)

	assert.Empty(t, findings, "every real DefineCapability/MustDefineCapability call site in this module must resolve its identity argument to a canonical package-level const; found:\n%s", strings.Join(findings, "\n\n"))
}

// repoRoot walks up from the test's working directory (this package's
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
			t.Fatal("could not locate go.mod above the capabilitylint package directory")
		}
		dir = parent
	}
}
