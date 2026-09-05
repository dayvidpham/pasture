package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestEveryLifecycleWriterCommandBoundsItsWindow is the DERIVED guard over
// production writers of the lifecycle store. The receipt service accepts a
// context with no deadline (a cancellation-bounded context is bounded and
// reports none), so the claim that NO PRODUCTION WRITER HOLDS AN UNBOUNDED
// WINDOW between its payload write and its journal append rests on the
// commands that make production contexts. A pair pinned by name would be a
// hand list that a later writer command escapes; this guard derives the
// population instead, so a new writer is covered the day it is registered.
//
// POPULATION: the live cobra tree under `hook lifecycle`: the parent and every
// registered leaf that has a RunE. A command is a WRITER when the closure of
// its RunE over the non-test sources of cmd/pasture calls an exported function
// of internal/handlers whose own closure, over that package's non-test
// sources, calls a Receive method; that second set is derived the same way.
// NON-VACUITY: the derived writer set has at least two members (today the
// native hook and the raw import); an empty or single set is RED.
// ASSERTION: every writer's closure builds its context through the deadline
// constructor with a profile tier, in one of the two production spellings:
// the call context.WithTimeout(_, timeouts.ProductionProfile().<Tier>()), or
// context.WithTimeout passed as a value in a call that also passes
// timeouts.ProductionProfile() (the native hook's outcome seam).
// FORM: go/ast, chosen over the behavioural form because the raw import's
// tier is 30 s in the production profile the built binary runs under and a
// held-lock run would cost that in wall time per run; the native path already
// has its held-lock behavioural proof in this package.
// WHAT IT VISITS: the cobra tree under hookLifecycleCmd; every non-test .go
// file of cmd/pasture and of internal/handlers. WHAT IT DOES NOT READ: test
// files; packages below handlers; the duration a tier evaluates to; whether
// the store honours the bounded context (the held-lock proof does that).
// MUTATION: replace the raw import's context.WithTimeout with WithCancel and
// this test is RED naming `pasture hook lifecycle raw`.
func TestEveryLifecycleWriterCommandBoundsItsWindow(t *testing.T) {
	// Serial on purpose: it reads the live command tree, which the whole
	// process shares, and the package's serial sweep classifies every test
	// that reaches that tree.
	root := writerGuardModuleRoot(t)
	handlersGraph := writerGuardParse(t, filepath.Join(root, "internal", "handlers"))
	receiveReachers := handlersGraph.functionsReaching(func(call *ast.CallExpr) bool {
		selector, ok := call.Fun.(*ast.SelectorExpr)
		return ok && selector.Sel.Name == "Receive"
	})
	require.NotEmpty(t, receiveReachers, "no handler reaches a Receive call; the derivation is broken, not the tree")
	cmdGraph := writerGuardParse(t, filepath.Join(root, "cmd", "pasture"))

	commands := []*cobra.Command{hookLifecycleCmd}
	commands = append(commands, hookLifecycleCmd.Commands()...)
	writers := []string{}
	for _, command := range commands {
		if command.RunE == nil {
			continue
		}
		body := cmdGraph.bodyOfFunc(t, command.RunE)
		closure := cmdGraph.closure(body)
		if !callsHandlersWriter(closure, receiveReachers) {
			continue
		}
		writers = append(writers, command.CommandPath())
		require.True(t, boundsItsWindow(closure),
			"%s reaches the receipt store but builds no bounded context: every lifecycle writer must derive its context through context.WithTimeout from a tier of timeouts.ProductionProfile(), or the window between its payload write and its journal append is unbounded and the orphan reclaim's age bound is a false claim", command.CommandPath())
	}
	require.GreaterOrEqual(t, len(writers), 2, "non-vacuity: the derived writer set must hold at least the native hook and the raw import; found %v", writers)
}

func writerGuardModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, err, "module root must hold go.mod")
	return root
}

// writerGuardGraph is the parsed non-test source of one package: every
// top-level function by name, plus the file set so a RunE closure can be
// located from its runtime position.
type writerGuardGraph struct {
	fset  *token.FileSet
	files map[string]*ast.File
	funcs map[string]*ast.FuncDecl
}

func writerGuardParse(t *testing.T, dir string) *writerGuardGraph {
	t.Helper()
	graph := &writerGuardGraph{fset: token.NewFileSet(), files: map[string]*ast.File{}, funcs: map[string]*ast.FuncDecl{}}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, parseErr := parser.ParseFile(graph.fset, path, nil, 0)
		require.NoError(t, parseErr)
		graph.files[path] = file
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Body != nil {
				graph.funcs[function.Name.Name] = function
			}
		}
	}
	require.NotEmpty(t, graph.funcs, "no functions parsed under %s", dir)
	return graph
}

// bodyOfFunc finds the source body of a live function value: the runtime
// gives its file and first line, and the parsed file gives the function
// literal or declaration that starts there.
func (g *writerGuardGraph) bodyOfFunc(t *testing.T, fn any) *ast.BlockStmt {
	t.Helper()
	runtimeFunc := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	require.NotNil(t, runtimeFunc)
	file, line := runtimeFunc.FileLine(runtimeFunc.Entry())
	var found *ast.BlockStmt
	for path, parsed := range g.files {
		if filepath.Base(path) != filepath.Base(file) {
			continue
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if found != nil {
				return false
			}
			switch typed := node.(type) {
			case *ast.FuncLit:
				if g.fset.Position(typed.Pos()).Line == line {
					found = typed.Body
				}
			case *ast.FuncDecl:
				if typed.Body != nil && g.fset.Position(typed.Pos()).Line == line {
					found = typed.Body
				}
			}
			return true
		})
	}
	require.NotNil(t, found, "no function starts at %s:%d; the RunE must be a literal or a declaration in this package", file, line)
	return found
}

// closure is the body plus every same-package function it calls by name,
// transitively.
func (g *writerGuardGraph) closure(body *ast.BlockStmt) []*ast.BlockStmt {
	seen := map[string]bool{}
	bodies := []*ast.BlockStmt{body}
	for index := 0; index < len(bodies); index++ {
		ast.Inspect(bodies[index], func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, isIdent := call.Fun.(*ast.Ident); isIdent && !seen[ident.Name] {
				if function, known := g.funcs[ident.Name]; known {
					seen[ident.Name] = true
					bodies = append(bodies, function.Body)
				}
			}
			return true
		})
	}
	return bodies
}

// functionsReaching returns the names of every function whose closure holds a
// call the predicate accepts.
func (g *writerGuardGraph) functionsReaching(predicate func(*ast.CallExpr) bool) map[string]bool {
	reaching := map[string]bool{}
	for name, function := range g.funcs {
		for _, body := range g.closure(function.Body) {
			hit := false
			ast.Inspect(body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && predicate(call) {
					hit = true
				}
				return !hit
			})
			if hit {
				reaching[name] = true
				break
			}
		}
	}
	return reaching
}

func callsHandlersWriter(closure []*ast.BlockStmt, writers map[string]bool) bool {
	for _, body := range closure {
		hit := false
		ast.Inspect(body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
				if pkg, isIdent := selector.X.(*ast.Ident); isIdent && pkg.Name == "handlers" && writers[selector.Sel.Name] {
					hit = true
				}
			}
			return !hit
		})
		if hit {
			return true
		}
	}
	return false
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg && selector.Sel.Name == name
}

// isProductionProfileCall matches timeouts.ProductionProfile().
func isProductionProfileCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && len(call.Args) == 0 && isSelector(call.Fun, "timeouts", "ProductionProfile")
}

// isProfileTier matches timeouts.ProductionProfile().<Tier>().
func isProfileTier(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && isProductionProfileCall(selector.X)
}

func boundsItsWindow(closure []*ast.BlockStmt) bool {
	for _, body := range closure {
		hit := false
		ast.Inspect(body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Spelling 1: context.WithTimeout(_, timeouts.ProductionProfile().<Tier>()).
			if isSelector(call.Fun, "context", "WithTimeout") && len(call.Args) == 2 && isProfileTier(call.Args[1]) {
				hit = true
			}
			// Spelling 2: context.WithTimeout as a value beside timeouts.ProductionProfile() in one call.
			passesFactory, passesProfile := false, false
			for _, argument := range call.Args {
				if isSelector(argument, "context", "WithTimeout") {
					passesFactory = true
				}
				if isProductionProfileCall(argument) {
					passesProfile = true
				}
			}
			if passesFactory && passesProfile {
				hit = true
			}
			return !hit
		})
		if hit {
			return true
		}
	}
	return false
}
