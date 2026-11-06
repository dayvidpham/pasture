package tasks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProductionSessionReplayEntriesNeverSupplyTheObserver(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	calls := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, ok := call.Fun.(*ast.Ident)
				if !ok || name.Name != "refreshSessionState" {
					return true
				}
				calls++
				require.Len(t, call.Args, 5, path)
				observer, ok := call.Args[4].(*ast.Ident)
				require.True(t, ok, path)
				require.Equal(t, "nil", observer.Name, path)
				full, ok := call.Args[2].(*ast.Ident)
				require.True(t, ok, path)
				bounded, ok := call.Args[3].(*ast.Ident)
				require.True(t, ok, path)
				switch fn.Name.Name {
				case "EnsureSessionState":
					require.Equal(t, "false", full.Name)
					require.Equal(t, "true", bounded.Name, "a read must stay inside the workflow-result wait")
				case "RefreshSessionState":
					require.Equal(t, "false", full.Name)
					require.Equal(t, "false", bounded.Name, "an explicit maintenance refresh must finish a large suffix")
				case "RebuildSessionState":
					require.Equal(t, "true", full.Name)
					require.Equal(t, "false", bounded.Name)
				default:
					t.Fatalf("unexpected production replay entry %s in %s", fn.Name.Name, path)
				}
				return true
			})
		}
	}
	require.Equal(t, 3, calls, "every real public entry must delegate to the one replay core")
}

func TestProductionSessionPageLeaseIsReadOnlyAndContextBound(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "session_state_rebuild.go", nil, 0)
	require.NoError(t, err)
	leases, readers := 0, 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "readSessionOccurrencePage" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "BeginTx":
				leases++
				require.Len(t, call.Args, 2)
				ctx, ok := call.Args[0].(*ast.Ident)
				require.True(t, ok)
				require.Equal(t, "ctx", ctx.Name)
				address, ok := call.Args[1].(*ast.UnaryExpr)
				require.True(t, ok)
				options, ok := address.X.(*ast.CompositeLit)
				require.True(t, ok)
				require.Len(t, options.Elts, 1)
				readOnly := options.Elts[0].(*ast.KeyValueExpr)
				require.Equal(t, "ReadOnly", readOnly.Key.(*ast.Ident).Name)
				require.Equal(t, "true", readOnly.Value.(*ast.Ident).Name)
			case "NewTransactionEvidenceReader":
				readers++
				require.Len(t, call.Args, 2)
				require.Equal(t, "ctx", call.Args[0].(*ast.Ident).Name)
				require.Equal(t, "tx", call.Args[1].(*ast.Ident).Name)
			}
			return true
		})
	}
	require.Equal(t, 1, leases)
	require.Equal(t, 1, readers)
}
