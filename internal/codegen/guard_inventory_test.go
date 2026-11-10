package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardInventory names the guards this epoch added, each with the requirement it
// proves. It is written by hand on purpose: a history-derived set answers "what
// changed", not "what must be proved", and drags in every integration and sample
// test the branch touches.
//
// A new guard is added here in the same change that adds it. That convention is
// the completeness check: there is no scan, so there is nothing to churn.
var guardInventory = []struct {
	Guard       string
	Requirement string
	// NoPureCore records why a guard cannot expose a pure core. Empty means it
	// has one and must have two samples.
	NoPureCore string
}{
	{Guard: "TestRenderedMarkdownLinksResolve", Requirement: "R27 links resolve"},
	{Guard: "TestRenderedBacktickedCitationsResolve", Requirement: "R27 citations resolve"},
	{Guard: "TestSurvivingProtocolDocumentsCarryTheirRegistryStructure", Requirement: "R27 documents keep their structure"},
	{Guard: "TestHandoffBindingsMatchThePinnedSet", Requirement: "R12 handoff bindings"},
	{Guard: "TestHandoffCreateStepsBindEveryReference", Requirement: "R12 handoff create steps"},
	{Guard: "TestMergeRequestFieldsAreRegistered", Requirement: "R31 landing fields"},
	{Guard: "TestStubSuccessorResolves", Requirement: "R14 stub successor"},
	{Guard: "TestRetiredMechanismNames", Requirement: "R32 retired mechanisms"},
	{Guard: "TestQuestionStimulusFields", Requirement: "R23 question fields"},
	{Guard: "TestDispositionEnumIncludesWontDo", Requirement: "R24 dispositions"},
	{Guard: "TestInternalTaxonomyTokens", Requirement: "R27 taxonomy"},
	{Guard: "TestUserGateList", Requirement: "R28 user gates"},
	{Guard: "TestValidationCasesStateEveryStep", Requirement: "R25 validation steps"},
	{Guard: "TestQuestionRecordShapeIsStable", Requirement: "R26 question record"},
	{Guard: "TestSignalRecordShapeIsStable", Requirement: "R26 signal record"},
	{Guard: "TestHandoffExportStep", Requirement: "R31 handoff export"},
	{Guard: "TestProtocolGuardRejectsEachStub", Requirement: "R27 per-branch samples"},
	{Guard: "TestProtocolGuardRejectsAnEmptyIndex", Requirement: "R27 empty index"},
	{Guard: "TestCitationGuardAllowsOnlyRuntimeArtefacts", Requirement: "R27 citation scope"},
}

var assertionCalls = map[string]struct{}{
	"Error": {}, "Errorf": {}, "Fatal": {}, "Fatalf": {}, "Fail": {}, "FailNow": {},
}

// guardFindings reports a guard that cannot prove anything: absent, without an
// assertion, skipped, or returning before it asserts.
func guardFindings(guard string, files map[string]*ast.File, fset *token.FileSet) []string {
	fn := findTestFunc(guard, files)
	if fn == nil {
		return []string{"guard " + guard + " is named in the inventory but no test function declares it — why: the inventory would claim a proof that does not exist; where: internal/codegen/guard_inventory_test.go; fix: add the test, or remove the inventory entry"}
	}
	var findings []string
	asserted := false
	skipped := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			switch callee := node.Fun.(type) {
			case *ast.SelectorExpr:
				if _, ok := assertionCalls[callee.Sel.Name]; ok {
					asserted = true
				}
				// testify: assert.* and require.* are assertions whatever the method.
				if pkg, ok := callee.X.(*ast.Ident); ok && (pkg.Name == "assert" || pkg.Name == "require") {
					asserted = true
				}
				if callee.Sel.Name == "Skip" || callee.Sel.Name == "Skipf" {
					skipped = true
				}
			case *ast.Ident:
				if _, ok := assertionCalls[callee.Name]; ok {
					asserted = true
				}
			}
		}
		return true
	})
	if skipped {
		findings = append(findings, "guard "+guard+" calls t.Skip — why: a skipped guard proves nothing and the suite stays green; where: the guard body; fix: make the guard run, or remove it from the inventory")
	}
	if !asserted {
		findings = append(findings, "guard "+guard+" contains no assertion call — why: a body that compares and returns cannot fail; where: the guard body; fix: assert the finding, do not just compute it")
	}
	return findings
}

func findTestFunc(name string, files map[string]*ast.File) *ast.FuncDecl {
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || fn.Name.Name != name {
				continue
			}
			return fn
		}
	}
	return nil
}

func parseTestPackage(t *testing.T) (map[string]*ast.File, *token.FileSet) {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoError(t, err)
		files[name] = file
	}
	return files, fset
}

// TestGuardInventory proves every named guard exists and asserts something. The
// inventory is the list above; adding a guard without adding it here is caught by
// review, and adding it here without a body is caught below.
func TestGuardInventory(t *testing.T) {
	require.NotEmpty(t, guardInventory, "the inventory must name the guards this epoch added")
	files, fset := parseTestPackage(t)
	for _, entry := range guardInventory {
		if entry.Requirement == "" {
			t.Errorf("guard %s records no requirement — fix: say what it proves", entry.Guard)
		}
		if entry.NoPureCore != "" && len(strings.Fields(entry.NoPureCore)) < 4 {
			t.Errorf("guard %s records NoPureCore %q — why: the field exists to explain a missing pure core, not to wave a sample away; fix: state the reason in a sentence", entry.Guard, entry.NoPureCore)
		}
		for _, finding := range guardFindings(entry.Guard, files, fset) {
			t.Error(finding)
		}
	}
}

// TestGuardInventoryRejectsAnEmptyBody is the meta-guard's own sample: an
// inventory entry whose function has no assertion must be reported.
func TestGuardInventoryRejectsAnEmptyBody(t *testing.T) {
	empty, err := parser.ParseFile(token.NewFileSet(), "empty_test.go", "package codegen\n\nimport \"testing\"\n\nfunc TestEmptyGuard(t *testing.T) {\n\t_ = t\n}\n", 0)
	require.NoError(t, err)
	files := map[string]*ast.File{"empty_test.go": empty}
	got := guardFindings("TestEmptyGuard", files, token.NewFileSet())
	if len(got) == 0 {
		t.Fatal("a guard with an empty body was accepted")
	}
	assert.Contains(t, strings.Join(got, "\n"), "no assertion")

	if found := guardFindings("TestAbsentGuard", files, token.NewFileSet()); len(found) == 0 {
		t.Fatal("an inventory entry with no function was accepted")
	}
}
