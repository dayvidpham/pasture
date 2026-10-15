package definitions_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The retention descriptor describes the reclaim writer window by its
// timeout-tier rule, never by a guessed wall-clock value. This guard proves
// every production projection rebuild honors that rule: each RebuildOptions
// literal must draw its window from a WorkflowResult tier call, so a future
// call site cannot silently pass a literal duration or a narrower tier.
func TestRebuildOptionsWindowsUseWorkflowResultTier(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	var literals int
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "worktree" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		set, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		ast.Inspect(set, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isRebuildOptions(literal.Type) {
				return true
			}
			literals++
			window, ok := rebuildWindow(literal)
			require.True(t, ok, "production RebuildOptions literal in %s names no window", path)
			require.True(t, callsWorkflowResult(window), "production RebuildOptions window in %s is not the WorkflowResult tier", path)
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, literals, 0, "guard must observe at least one production RebuildOptions literal")
}

func isRebuildOptions(expr ast.Expr) bool {
	switch resolved := expr.(type) {
	case *ast.SelectorExpr:
		return resolved.Sel.Name == "RebuildOptions"
	case *ast.Ident:
		return resolved.Name == "RebuildOptions"
	default:
		return false
	}
}

func rebuildWindow(literal *ast.CompositeLit) (ast.Expr, bool) {
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "Window" {
			return pair.Value, true
		}
	}
	return nil, false
}

func callsWorkflowResult(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if selected, ok := node.(*ast.SelectorExpr); ok && selected.Sel.Name == "WorkflowResult" {
			found = true
			return false
		}
		return true
	})
	return found
}
