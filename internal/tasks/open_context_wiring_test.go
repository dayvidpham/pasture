package tasks

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenContextWrappersAndProductionActivationWiring(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "open_unified.go", nil, 0)
	require.NoError(t, err)
	functions := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			functions[fn.Name.Name] = fn
		}
	}
	source := func(node ast.Node) string {
		var out bytes.Buffer
		require.NoError(t, format.Node(&out, token.NewFileSet(), node))
		return out.String()
	}
	for name, want := range map[string]string{
		"OpenTaskTracker":            "return OpenTaskTrackerContext(context.Background(), dbPath)",
		"openTaskTrackerImpl":        "return OpenTaskTrackerContext(context.Background(), dbPath)",
		"OpenTaskTrackerWithOptions": "return OpenTaskTrackerContextWithOptions(context.Background(), dbPath, opts...)",
	} {
		fn := functions[name]
		require.NotNil(t, fn)
		require.Len(t, fn.Body.List, 1)
		require.Equal(t, want, source(fn.Body.List[0]))
	}
	defaults := functions["productionOpenDefaults"]
	require.NotNil(t, defaults)
	ast.Inspect(defaults.Body, func(node ast.Node) bool {
		if field, ok := node.(*ast.KeyValueExpr); ok {
			require.NotEqual(t, "beforeActivation", source(field.Key), "production barrier must remain nil/pass-through")
			require.NotEqual(t, "activationObserver", source(field.Key), "production must never observe test activation phases")
		}
		return true
	})
	activationCalls := 0
	ast.Inspect(functions["openTaskTrackerWithOptions"].Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && source(call.Fun) == "provenance.OpenBorrowedSQLiteContext" {
			activationCalls++
			require.Equal(t, "ctx", source(call.Args[0]))
			require.Equal(t, "auditDB", source(call.Args[1]))
			require.Equal(t, "activationOptions", source(call.Args[2]))
		}
		return true
	})
	require.Equal(t, 1, activationCalls)
	options, err := parser.ParseFile(token.NewFileSet(), "activation_options.go", nil, 0)
	require.NoError(t, err)
	busyCalls := 0
	ast.Inspect(options, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && source(call.Fun) == "provenance.WithBorrowedActivationBusyTimeout" {
			busyCalls++
			require.Equal(t, "cfg.timeouts.SQLiteBusy()", source(call.Args[0]))
		}
		return true
	})
	require.Equal(t, 1, busyCalls)
}
