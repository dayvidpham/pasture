package capabilitylint_test

import (
	"go/token"
	"os"
	"os/exec"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/capabilitylint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustCheckTestdata(t testing.TB, path string) []capabilitylint.Finding {
	t.Helper()
	fset := token.NewFileSet()
	findings, err := capabilitylint.CheckFile(fset, path, nil)
	require.NoError(t, err)
	return findings
}

// TestCheck_RejectsRawStringLiteralIdentity is the acceptance criterion's
// headline case: a raw string literal at a canonical MustDefineCapability
// call site is flagged, even though — see
// TestLiteralIdentityFixtureCompilesDespiteBeingLinted below — it compiles
// successfully on its own.
func TestCheck_RejectsRawStringLiteralIdentity(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/literal_id/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "raw string literal")
	assert.Contains(t, findings[0].Message, "MustDefineCapability")
	assert.Contains(t, findings[0].Message, "what:")
	assert.Contains(t, findings[0].Message, "fix:")
}

// TestCheck_RejectsInlineConversion proves an inline CapabilityID(...)
// conversion of a literal is rejected exactly like the bare literal, even
// though it too compiles successfully.
func TestCheck_RejectsInlineConversion(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/converted_literal_id/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "inline")
	assert.Contains(t, findings[0].Message, "CapabilityID(...)")
	assert.Contains(t, findings[0].Message, "DefineCapability")
}

// TestCheck_RejectsFunctionLocalTypedVariable proves a correctly typed
// (ir.CapabilityID) but function-local variable is still rejected: the
// canonical declaration site must be package scope, not merely
// type-correct.
func TestCheck_RejectsFunctionLocalTypedVariable(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/local_typed_var/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "localID")
	assert.Contains(t, findings[0].Message, "does not resolve to a package-level")
}

// TestCheck_AcceptsCanonicalTypedConstDeclaration proves the accepted
// contract's own required-API pattern produces zero findings.
func TestCheck_AcceptsCanonicalTypedConstDeclaration(t *testing.T) {
	t.Parallel()

	assert.Empty(t, mustCheckTestdata(t, "testdata/typed_const_id/case.go"))
}

// TestCheck_AllowsQualifiedCrossPackageConstReference proves a
// package-qualified reference to a canonical const declared elsewhere is not
// a false positive.
func TestCheck_AllowsQualifiedCrossPackageConstReference(t *testing.T) {
	t.Parallel()

	assert.Empty(t, mustCheckTestdata(t, "testdata/qualified_const_id/case.go"))
}

// TestCheck_RejectsParenWrappedLiteral proves a parenthesized literal — a
// shape gofmt does not normalize away — is rejected identically to the bare
// literal.
func TestCheck_RejectsParenWrappedLiteral(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/paren_wrapped_literal/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "raw string literal")
	assert.Contains(t, findings[0].Message, "MustDefineCapability")
}

// TestCheck_RejectsConcatenatedLiteral proves string concatenation is
// rejected: both operands are untyped string constants, and Go's own
// assignability rule lets their concatenation compile as a CapabilityID
// value just as readily as a bare literal.
func TestCheck_RejectsConcatenatedLiteral(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/concatenated_literal/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "binary expression")
	assert.Contains(t, findings[0].Message, "concatenation")
}

// TestCheck_RejectsFunctionCallResult proves the result of an arbitrary
// function call is rejected: a call result cannot be statically verified as
// a stable, canonical identity, distinct from the recognized
// CapabilityID(...) inline conversion shape.
func TestCheck_RejectsFunctionCallResult(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/function_call_result/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "function/method call")
	assert.Contains(t, findings[0].Message, "MustDefineCapability")
}

// TestCheck_RejectsStructFieldSelector proves a struct-field selector
// (cfg.ID) is rejected — it must not be confused with the conservative
// pkg.SomeCapabilityID cross-package allowance, since cfg is not an
// imported package name.
func TestCheck_RejectsStructFieldSelector(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/struct_field_selector/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "cfg.ID")
	assert.Contains(t, findings[0].Message, "not a reference to one of this file's imported packages")
}

// TestCheck_RejectsShadowedLocalConst proves the scope-aware fix: a
// function-local variable that merely shares its name with a legitimate
// package-level CapabilityID constant is still rejected, because it
// resolves to a different *ast.Object than the constant it shadows.
func TestCheck_RejectsShadowedLocalConst(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/shadowed_local_const/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "CapabilityRenderDiagram")
	assert.Contains(t, findings[0].Message, "shadowing")
}

// TestCheck_AllowsTypedParameterForwarding proves the third recognized-safe
// shape: an ir.CapabilityID-typed parameter of the enclosing function (or
// function literal), forwarded verbatim, produces zero findings — the exact
// pattern ir.MustDefineCapability's own required implementation uses when
// it forwards its own id parameter to ir.DefineCapability.
func TestCheck_AllowsTypedParameterForwarding(t *testing.T) {
	t.Parallel()

	assert.Empty(t, mustCheckTestdata(t, "testdata/typed_parameter_forwarding/case.go"))
}

// TestCheck_RejectsConvertedStringParameter proves the typed-parameter
// allowance is not a loophole: a plain string parameter converted inline to
// CapabilityID at the call site — rather than a parameter already typed
// CapabilityID — is still rejected by the existing inline-conversion rule.
func TestCheck_RejectsConvertedStringParameter(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/converted_string_parameter/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "inline")
	assert.Contains(t, findings[0].Message, "CapabilityID(...)")
}

// TestCheck_RejectsReassignedParameter proves the parameter-forwarding
// allowance requires verbatim forwarding: a CapabilityID-typed parameter
// reassigned to a raw literal before being forwarded is rejected, even
// though the identifier still resolves to the parameter's own *ast.Object.
func TestCheck_RejectsReassignedParameter(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/reassigned_parameter/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "id")
	assert.Contains(t, findings[0].Message, "DefineCapability")
}

// TestCheck_RejectsReassignedParameterViaClosure proves the verbatim-
// forwarding check follows a captured parameter into a nested function
// literal: reassignment happens only inside the closure, not in the
// enclosing function's own body, and must still be rejected.
func TestCheck_RejectsReassignedParameterViaClosure(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/reassigned_parameter_via_closure/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "id")
	assert.Contains(t, findings[0].Message, "MustDefineCapability")
}

// TestCheck_AllowsVerbatimClosureForwarding proves the counterpart to the
// two reassignment cases above: a closure that captures an outer
// CapabilityID-typed parameter and forwards it WITHOUT ever reassigning it
// produces zero findings — reassignment is what is rejected, not closure
// capture in general.
func TestCheck_AllowsVerbatimClosureForwarding(t *testing.T) {
	t.Parallel()

	assert.Empty(t, mustCheckTestdata(t, "testdata/verbatim_closure_forwarding/case.go"))
}

// TestCheck_RejectsShadowedImportSelector proves rule 3's import-qualifier
// allowance is scope-aware like rule 1: a local variable shadowing a real
// file import's name ("fmt") cannot launder an arbitrary field selector on
// itself as if it were that import.
func TestCheck_RejectsShadowedImportSelector(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/shadowed_import_selector/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "fmt.Value")
	assert.Contains(t, findings[0].Message, "not a reference to one of this file's imported packages")
}

// TestCheck_RejectsShadowedIRImportSelector is the "ir-shadow" variant of
// TestCheck_RejectsShadowedImportSelector: the shadowed import's local name
// is "ir" itself, proving the scope-aware check is uniform and not
// accidentally special-casing the target package's own conventional import
// name.
func TestCheck_RejectsShadowedIRImportSelector(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/shadowed_ir_import_selector/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "ir.Value")
	assert.Contains(t, findings[0].Message, "not a reference to one of this file's imported packages")
}

// TestCheck_RejectsParenWrappedCallee proves a parenthesized constructor
// callee — (ir.MustDefineCapability[In, Out])(...) — is still recognized as
// a lint target and its raw-literal identity argument is flagged; before
// this fix the call site was invisible to Check entirely.
func TestCheck_RejectsParenWrappedCallee(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/paren_wrapped_callee/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "raw string literal")
	assert.Contains(t, findings[0].Message, "MustDefineCapability")
}

// TestCheck_RejectsRangeReassignedParameter proves the verbatim-forwarding
// check follows range-clause reassignment: `for _, id = range ids` (using
// `=`) rebinds the parameter's own *ast.Object on every iteration — an
// *ast.RangeStmt, a distinct AST shape from *ast.AssignStmt — and must be
// rejected identically to a direct reassignment.
func TestCheck_RejectsRangeReassignedParameter(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/reassigned_parameter_via_range/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "id")
	assert.Contains(t, findings[0].Message, "DefineCapability")
}

// TestCheck_AllowsRangeDefinedLoopVariable proves a `:=` range clause does
// not falsely disqualify a same-named parameter: the loop-local `id` it
// introduces is a new *ast.Object distinct from the outer parameter's, so
// forwarding the outer parameter after the loop stays allowed.
func TestCheck_AllowsRangeDefinedLoopVariable(t *testing.T) {
	t.Parallel()

	assert.Empty(t, mustCheckTestdata(t, "testdata/range_defined_loop_variable/case.go"))
}

// TestCheck_RejectsPointerReassignedParameter proves address-of
// disqualification: taking a candidate parameter's address (`&id`) at all
// — regardless of whether the resulting pointer is used to mutate it —
// disqualifies it, since this syntactic checker cannot track what a
// pointer's writes ultimately target.
func TestCheck_RejectsPointerReassignedParameter(t *testing.T) {
	t.Parallel()

	findings := mustCheckTestdata(t, "testdata/reassigned_parameter_via_pointer/case.go")
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "id")
	assert.Contains(t, findings[0].Message, "MustDefineCapability")
}

// TestCheck_AllowsUnrelatedAddressOf proves address-of disqualification is
// scoped to the exact identifier whose address is taken: taking the address
// of a completely unrelated local variable must not disqualify the
// CapabilityID-typed parameter forwarded elsewhere in the same function.
func TestCheck_AllowsUnrelatedAddressOf(t *testing.T) {
	t.Parallel()

	assert.Empty(t, mustCheckTestdata(t, "testdata/unrelated_address_of/case.go"))
}

// TestFixturesCompileDespiteBeingLinted documents Go's actual literal rule,
// as the acceptance criterion requires: every rejected fixture above is
// nonetheless valid, compiling Go — proving the type system alone cannot
// reject a raw literal (or an inline conversion, a parenthesized literal, a
// concatenation, a function-call result, a struct-field selector, a
// function-local typed variable, or a shadowing local) as a CapabilityID
// value, which is exactly why this static rule, not the compiler, is
// required.
func TestFixturesCompileDespiteBeingLinted(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{
		"./testdata/literal_id",
		"./testdata/converted_literal_id",
		"./testdata/local_typed_var",
		"./testdata/typed_const_id",
		"./testdata/qualified_const_id",
		"./testdata/paren_wrapped_literal",
		"./testdata/concatenated_literal",
		"./testdata/function_call_result",
		"./testdata/struct_field_selector",
		"./testdata/shadowed_local_const",
		"./testdata/typed_parameter_forwarding",
		"./testdata/converted_string_parameter",
		"./testdata/reassigned_parameter",
		"./testdata/reassigned_parameter_via_closure",
		"./testdata/verbatim_closure_forwarding",
		"./testdata/shadowed_import_selector",
		"./testdata/shadowed_ir_import_selector",
		"./testdata/paren_wrapped_callee",
		"./testdata/reassigned_parameter_via_range",
		"./testdata/range_defined_loop_variable",
		"./testdata/reassigned_parameter_via_pointer",
		"./testdata/unrelated_address_of",
	} {
		dir := dir
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("go", "build", dir)
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "fixture %s must be valid, compiling Go regardless of its lint findings:\n%s", dir, output)
		})
	}
}
