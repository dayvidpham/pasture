package codegen_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/codegen/scan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateRunsStrictGateBeforeWritingOutput is pasture#42's end-to-end
// "no partial output" proof for the production pipeline: it copies the real
// canonical skills/ and agents/ trees into a temp module root, injects one
// unclassified harness-syntax candidate, then runs codegen.Generate against
// that root. Generation must fail on the strict gate and write no schema.xml
// — the gate runs before any output is produced.
func TestGenerateRunsStrictGateBeforeWritingOutput(t *testing.T) {
	t.Parallel()

	realRoot, err := scan.ModuleRoot()
	require.NoError(t, err)

	tmpRoot := t.TempDir()
	for _, dir := range []string{"skills", "agents"} {
		require.NoError(t, os.CopyFS(filepath.Join(tmpRoot, dir), os.DirFS(filepath.Join(realRoot, dir))),
			"copying canonical root %q into the temp module root", dir)
	}

	// Inject one unclassified candidate: append a new section containing a
	// TeamCreate( call to a real, active skill owner. It matches no
	// classification-manifest entry, so the strict gate must reject it.
	injected := filepath.Join(tmpRoot, "skills", "worker", "SKILL.md")
	original, err := os.ReadFile(injected)
	require.NoError(t, err)
	appended := string(original) + "\n\n## Injected Unclassified Candidate\n\nSpawn via TeamCreate({ team_name: \"never-classified\" }) here.\n"
	require.NoError(t, os.WriteFile(injected, []byte(appended), 0o644))

	targets, err := codegen.ResolveHarness([]string{string(codegen.HarnessClaudeCode)})
	require.NoError(t, err)

	result, errs := codegen.Generate(tmpRoot, targets, codegen.DefaultOptions)

	require.NotEmpty(t, errs, "an unclassified candidate must abort generation")
	assert.Contains(t, errs[0].Error(), "no partial output")
	assert.Empty(t, result.SchemaPath, "the gate runs before schema generation")
	assert.Empty(t, result.Files, "the gate runs before any harness output")

	_, statErr := os.Stat(filepath.Join(tmpRoot, "schema.xml"))
	assert.True(t, os.IsNotExist(statErr), "generation must not write schema.xml when the strict gate fails")
}

// TestGenerateGateFailureIsTheSoleError proves that when the strict gate
// fails, Generate returns exactly the gate error and does not additionally
// run (and report) the schema, harness, or global-id steps.
func TestGenerateGateFailureIsTheSoleError(t *testing.T) {
	t.Parallel()

	// A module root with neither skills/ nor agents/ fails the gate at the
	// scan's root-discovery stage.
	tmpRoot := t.TempDir()

	targets, err := codegen.ResolveHarness([]string{string(codegen.HarnessClaudeCode)})
	require.NoError(t, err)

	result, errs := codegen.Generate(tmpRoot, targets, codegen.DefaultOptions)

	require.Len(t, errs, 1, "the gate failure must be the sole error; no downstream step ran")
	assert.Contains(t, errs[0].Error(), "pasture#42 strict source-migration gate")
	assert.Empty(t, result.SchemaPath)
	assert.Empty(t, result.Files)
}

// TestStrictLifecycleRowGateAcceptsTheShippedProfiles proves the second strict
// gate is wired and green on the tree that ships. A blocking exit code is a
// real refusal of a user's prompt or tool call, so no shipped row may claim one
// without citing where the host's blocking behavior was read.
func TestStrictLifecycleRowGateAcceptsTheShippedProfiles(t *testing.T) {
	t.Parallel()

	require.NoError(t, codegen.RequireEvidencedLifecycleRows(),
		"every shipped lifecycle row that claims a blocking exit code must cite its evidence")
}

// TestStrictLifecycleRowGateRunsBeforeAnyWrite pins the ORDER of the pipeline.
// The gate is only worth having if it aborts before generation writes anything:
// a partially written tree with an unevidenced blocking row is worse than no
// output at all. The shipped profiles pass the gate, so the ordering cannot be
// proven by triggering it; this reads the production pipeline instead.
//
// WHAT IT VISITS: every call expression in the body of codegen.Generate, in
// source order, read through the parser. The gate call is located; every call
// BEFORE it must be the source-migration gate, which is the one call the doc of
// Generate places ahead of it and which writes nothing; and the three writes
// the pipeline makes today must all stand AFTER it.
// WHAT IT DOES NOT READ: whether a call writes. A call ahead of the gate is
// refused whatever it does, so a fourth write cannot be added above the gate
// under a new name — the first version looked for three write names and a
// write under any other name passed above the gate. A write hidden inside the
// source-migration gate itself, or reached through a callee of a call that
// stands after the gate, is outside its reach.
//
// MUTATION: call any function — a new one, or a write moved up — before the
// lifecycle-row gate in Generate. This test turns RED naming the call.
func TestStrictLifecycleRowGateRunsBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	root, err := scan.ModuleRoot()
	require.NoError(t, err)
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, filepath.Join(root, "internal/codegen/generate.go"), nil, 0)
	require.NoError(t, err, "the production pipeline must be readable beside its test")

	var generate *ast.FuncDecl
	for _, node := range parsed.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Recv == nil && function.Name.Name == "Generate" {
			generate = function
			break
		}
	}
	require.NotNil(t, generate, "codegen.Generate must exist to be the production pipeline")

	type call struct {
		Name string
		Pos  token.Pos
	}
	calls := []call{}
	ast.Inspect(generate.Body, func(node ast.Node) bool {
		expression, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		switch function := expression.Fun.(type) {
		case *ast.Ident:
			calls = append(calls, call{Name: function.Name, Pos: expression.Pos()})
		case *ast.SelectorExpr:
			calls = append(calls, call{Name: function.Sel.Name, Pos: expression.Pos()})
		}
		return true
	})
	sort.Slice(calls, func(i, j int) bool { return calls[i].Pos < calls[j].Pos })

	gate := -1
	for index, candidate := range calls {
		if candidate.Name == "RequireEvidencedLifecycleRows" {
			gate = index
			break
		}
	}
	require.NotEqual(t, -1, gate, "Generate no longer calls the strict lifecycle-row gate")

	before := []string{}
	for _, candidate := range calls[:gate] {
		before = append(before, fmt.Sprintf("%s (generate.go:%d)", candidate.Name, fileSet.Position(candidate.Pos).Line))
	}
	sourceGate := fmt.Sprintf("RequireClassifiedSource (generate.go:%d)", fileSet.Position(calls[0].Pos).Line)
	assert.Equal(t, []string{sourceGate}, before,
		"the only call Generate may make before the strict lifecycle-row gate is the strict "+
			"source-migration gate, which writes nothing. Every other call stands after it, "+
			"whatever it does, because a refused row must leave NO partial output and this "+
			"reader does not know which calls write")

	after := map[string]bool{}
	for _, candidate := range calls[gate+1:] {
		after[candidate.Name] = true
	}
	for _, write := range []string{"GenerateSchemaToFile", "EmitHarness", "emitInventoryRows"} {
		assert.True(t, after[write],
			"Generate no longer calls %s after the strict lifecycle-row gate; the pipeline's three "+
				"writes are named here so that one moved above the gate, or removed, fails by name", write)
	}
}
