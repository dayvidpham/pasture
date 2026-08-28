package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// RequireBlankImport fails the test unless goFile contains a blank import
// (`_ "<importPath>"`) of importPath.
//
// It exists for links that the compiler cannot check. A package registered
// only through another package's init() has no referenced identifier, so
// deleting the blank import still compiles and still passes every test that
// does not reach the run-time registry lookup. The failure then appears at
// run time, in a binary, on a user's machine. A source scan is the cheapest
// check that keeps the link present.
//
// goFile is a path relative to the calling test's working directory, which is
// the directory of the package under test.
func RequireBlankImport(t *testing.T, goFile, importPath string) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, goFile, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf(
			"cannot read %s to check for the required blank import %q: %v — "+
				"the file must exist and parse; if it was renamed, update this check to name the new file",
			goFile, importPath, err,
		)
	}

	for _, imp := range file.Imports {
		path, unquoteErr := strconv.Unquote(imp.Path.Value)
		if unquoteErr != nil || path != importPath {
			continue
		}
		if imp.Name != nil && imp.Name.Name == "_" {
			return
		}
		t.Fatalf(
			"%s imports %q under the name %q, not as a blank import — "+
				"restore `_ %q` so the package's init() still runs",
			goFile, importPath, importName(imp), importPath,
		)
	}

	t.Fatalf(
		"%s is missing the required blank import `_ %q` — "+
			"nothing references it by name, so removing it still compiles and still passes the tests, "+
			"and the failure only appears at run time; restore the import",
		goFile, importPath,
	)
}

func importName(imp *ast.ImportSpec) string {
	if imp.Name == nil {
		return "(default)"
	}
	return imp.Name.Name
}
