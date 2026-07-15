package artifact_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionImportsStandardLibraryOnly(t *testing.T) {
	t.Parallel()

	directoryEntries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir artifact package: %v", err)
	}
	fileSet := token.NewFileSet()
	for _, directoryEntry := range directoryEntries {
		name := directoryEntry.Name()
		if directoryEntry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", name, err)
		}
		for _, importPath := range parsedImportPaths(t, parsed) {
			pkg, err := build.Default.Import(importPath, ".", build.FindOnly)
			if err != nil {
				t.Errorf("production file %s imports %q, which cannot be resolved as standard library: %v", name, importPath, err)
				continue
			}
			if !pkg.Goroot {
				t.Errorf("production file %s imports non-standard package %q", name, importPath)
			}
		}
	}
}

func parsedImportPaths(t *testing.T, file *ast.File) []string {
	t.Helper()
	paths := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("Unquote import %s: %v", spec.Path.Value, err)
		}
		paths = append(paths, value)
	}
	return paths
}
