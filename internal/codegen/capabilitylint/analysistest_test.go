package capabilitylint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/capabilitylint"
	"golang.org/x/tools/go/analysis/analysistest"
)

// analyzerRepoRoot returns the pasture module root: analysistest.Run's
// module-mode branch requires its dir argument to itself contain (or be) the
// module root, and every fixture package under testdata/ imports the real
// github.com/dayvidpham/pasture/internal/codegen/ir package, so the fixture
// corpus is loaded as ordinary packages of this module (via their full
// import path, not GOPATH-style stand-ins) rather than a separate synthetic
// module — this is what lets the fixtures import ir directly, unchanged.
func analyzerRepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
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

const testdataImportPrefix = "github.com/dayvidpham/pasture/internal/codegen/capabilitylint/testdata/"

// TestAnalyzer_FixtureCorpus runs capabilitylint.Analyzer, via
// analysistest.Run, over every fixture package in testdata/: analysistest
// itself both loads and type-checks each package (a stronger compile check
// than the old TestFixturesCompileDespiteBeingLinted's `go build`) and
// verifies the analyzer's reported diagnostics against each fixture's
// `// want "regexp"` comments, failing on any unexpected diagnostic or any
// expected diagnostic that did not fire. This is the single source of truth
// for the fixture corpus: every deny case (one `// want` line each) and
// every allow case (zero `// want` lines, so any diagnostic at all is a
// failure) from the pre-analyzer test suite is represented below.
func TestAnalyzer_FixtureCorpus(t *testing.T) {
	t.Parallel()

	root := analyzerRepoRoot(t)

	denyCases := []string{
		"concatenated_literal",
		"converted_literal_id",
		"converted_string_parameter",
		"function_call_result",
		"literal_id",
		"local_typed_var",
		"paren_wrapped_callee",
		"paren_wrapped_literal",
		"reassigned_parameter",
		"reassigned_parameter_via_closure",
		"reassigned_parameter_via_pointer",
		"reassigned_parameter_via_range",
		"shadowed_import_selector",
		"shadowed_ir_import_selector",
		"shadowed_local_const",
		"struct_field_selector",
	}
	allowCases := []string{
		"typed_const_id",
		"qualified_const_id",
		"typed_parameter_forwarding",
		"range_defined_loop_variable",
		"verbatim_closure_forwarding",
		"unrelated_address_of",
	}

	var patterns []string
	for _, name := range append(append([]string{}, denyCases...), allowCases...) {
		patterns = append(patterns, testdataImportPrefix+name)
	}

	analysistest.Run(t, root, capabilitylint.Analyzer, patterns...)
}
