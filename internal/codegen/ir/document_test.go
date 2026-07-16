package ir_test

import (
	"errors"
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoldmarkDocumentCompilesEntirelyInMemory(t *testing.T) {
	t.Parallel()

	markdownSource := []byte("# Worker\n\nKeep native-looking `TeamCreate()` examples unchanged.\n")
	markdown, err := ir.Markdown(markdownSource, mustLocation(t, "intro", len(markdownSource)))
	require.NoError(t, err)
	ranges, err := ir.MarkdownSourceRanges(markdown)
	require.NoError(t, err)
	assert.NotEmpty(t, ranges)
	markdownSource[0] = '!'

	scope, err := ir.NewRootScope("document")
	require.NoError(t, err)
	skill, err := ir.NewSkillID("pasture.skill.worker/v1")
	require.NoError(t, err)
	invoke, err := ir.NewInvokeSkill(skill, nil, scope)
	require.NoError(t, err)
	operation, err := ir.Operation(invoke, mustLocation(t, "invoke", 0))
	require.NoError(t, err)
	verbatimSource := []byte("\nPortable exact prose.\n")
	verbatim, err := ir.Verbatim(verbatimSource, mustLocation(t, "verbatim", 0))
	require.NoError(t, err)
	verbatimSource[1] = '!'

	claudeContract := mustContract(t, ir.HarnessClaudeCode, "2.1.210")
	openCodeContract := mustContract(t, ir.HarnessOpenCode, "1.17.18")
	codexContract := mustContract(t, ir.HarnessCodex, "0.144.1")
	claudeLiteral, err := ir.LiteralForHarness(ir.HarnessClaudeCode, claudeContract, []byte("\nClaude contract literal.\n"), "pinned schema has no portable equivalent")
	require.NoError(t, err)
	openCodeUnsupported, err := ir.LiteralUnsupported(ir.HarnessOpenCode, "the pinned host has no equivalent")
	require.NoError(t, err)
	codexLiteral, err := ir.LiteralForHarness(ir.HarnessCodex, codexContract, []byte("\nCodex contract literal.\n"), "pinned native representation")
	require.NoError(t, err)
	literal, err := ir.TargetLiteral(
		[]ir.TargetCase{claudeLiteral, openCodeUnsupported, codexLiteral},
		mustLocation(t, "literal", 0),
	)
	require.NoError(t, err)
	document, err := ir.NewDocument(markdown, operation, verbatim, literal)
	require.NoError(t, err)
	assert.Equal(t, 4, document.Len())

	validatorCalled := false
	target, err := ir.NewTarget(
		ir.HarnessClaudeCode, claudeContract, "skills/worker/SKILL.md",
		ir.OperationLowererFunc(func(operation ir.SemanticOperation, at ir.Location) ([]byte, error) {
			kind, err := ir.SemanticOperationKind(operation)
			if err != nil {
				return nil, err
			}
			return []byte("\n<!-- semantic:" + string(kind) + " -->\n"), nil
		}),
		ir.NativeValidatorFunc(func(tree ir.RenderedTree) error {
			validatorCalled = true
			file, ok := tree.File("skills/worker/SKILL.md")
			if !ok || len(file.Content()) == 0 {
				return errors.New("complete expected file is absent")
			}
			return nil
		}),
	)
	require.NoError(t, err)
	tree, err := ir.Compile(document, target)
	require.NoError(t, err)
	assert.True(t, validatorCalled)
	assert.Equal(t, []string{"skills/worker/SKILL.md"}, tree.Paths())
	file, ok := tree.File("skills/worker/SKILL.md")
	require.True(t, ok)
	assert.Equal(t,
		"# Worker\n\nKeep native-looking `TeamCreate()` examples unchanged.\n"+
			"\n<!-- semantic:invoke_skill -->\n"+
			"\nPortable exact prose.\n"+
			"\nClaude contract literal.\n",
		string(file.Content()),
	)
	content := file.Content()
	content[0] = '!'
	fileAgain, ok := tree.File(file.Path())
	require.True(t, ok)
	assert.Equal(t, byte('#'), fileAgain.Content()[0])
	assert.Equal(t, openCodeContract, mustContract(t, ir.HarnessOpenCode, "1.17.18"))
}

// TestCompileFailureMatrixReturnsZeroTreeAndShortCircuits is the per-stage
// failure matrix replacing a prior revision's single all-or-nothing
// filesystem test. That test wrote a file to a temp directory and asserted
// it was untouched after every failure — but Compile never touches a
// filesystem at all (see TestPackageHasNoFilesystemOrPublisherImport, the
// real forbidden-publisher-dependency guard), so the assertion could never
// have caught a regression; it always vacuously passed. This matrix instead
// proves, for every reachable failure stage: (1) Compile always returns a
// zero-length RenderedTree, and (2) a failing stage prevents every later
// stage's side-effecting call from ever running, using call-counting spies.
func TestCompileFailureMatrixReturnsZeroTreeAndShortCircuits(t *testing.T) {
	t.Parallel()

	source := []byte("# Safe\n")
	markdown, err := ir.Markdown(source, mustLocation(t, "safe", len(source)))
	require.NoError(t, err)
	scope, err := ir.NewRootScope("failure")
	require.NoError(t, err)
	skill, err := ir.NewSkillID("pasture.skill.failure/v1")
	require.NoError(t, err)
	invoke, err := ir.NewInvokeSkill(skill, nil, scope)
	require.NoError(t, err)
	operation, err := ir.Operation(invoke, mustLocation(t, "failure-operation", 0))
	require.NoError(t, err)
	document, err := ir.NewDocument(markdown, operation)
	require.NoError(t, err)
	contract := mustContract(t, ir.HarnessClaudeCode, "2.1.210")

	t.Run("empty document short-circuits before any target use", func(t *testing.T) {
		t.Parallel()
		lowererCalls, validatorCalls := 0, 0
		target := mustTargetSpy(t, contract, &lowererCalls, &validatorCalls, nil)
		tree, err := ir.Compile(ir.Document{}, target)
		require.Error(t, err)
		assert.Zero(t, tree.Len())
		assert.Equal(t, 0, lowererCalls, "an empty document must never reach the lowerer")
		assert.Equal(t, 0, validatorCalls, "an empty document must never reach native validation")
	})

	t.Run("zero target short-circuits before any part rendering", func(t *testing.T) {
		t.Parallel()
		tree, err := ir.Compile(document, ir.Target{})
		require.Error(t, err)
		assert.Zero(t, tree.Len())
	})

	t.Run("nil lowerer short-circuits before native validation", func(t *testing.T) {
		t.Parallel()
		validatorCalls := 0
		target, targetErr := ir.NewTarget(
			ir.HarnessClaudeCode, contract, "SKILL.md", nil,
			ir.NativeValidatorFunc(func(ir.RenderedTree) error { validatorCalls++; return nil }),
		)
		require.NoError(t, targetErr)
		tree, err := ir.Compile(document, target)
		require.Error(t, err)
		assert.Zero(t, tree.Len())
		assert.Equal(t, 0, validatorCalls, "a missing lowerer must never reach native validation")
	})

	t.Run("lowering failure short-circuits before native validation", func(t *testing.T) {
		t.Parallel()
		validatorCalls := 0
		target := mustTargetSpy(t, contract, nil, &validatorCalls, errors.New("injected lowerer failure"))
		tree, err := ir.Compile(document, target)
		require.Error(t, err)
		assert.Zero(t, tree.Len())
		assert.Equal(t, 0, validatorCalls, "a failing lowerer must never reach native validation")
	})

	t.Run("empty lowering output short-circuits before native validation", func(t *testing.T) {
		t.Parallel()
		validatorCalls := 0
		lowererCalls := 0
		target, targetErr := ir.NewTarget(
			ir.HarnessClaudeCode, contract, "SKILL.md",
			ir.OperationLowererFunc(func(ir.SemanticOperation, ir.Location) ([]byte, error) {
				lowererCalls++
				return nil, nil
			}),
			ir.NativeValidatorFunc(func(ir.RenderedTree) error { validatorCalls++; return nil }),
		)
		require.NoError(t, targetErr)
		tree, err := ir.Compile(document, target)
		require.Error(t, err)
		assert.Zero(t, tree.Len())
		assert.Equal(t, 1, lowererCalls)
		assert.Equal(t, 0, validatorCalls, "empty lowering output must never reach native validation")
	})

	t.Run("first native validator failure short-circuits remaining validators", func(t *testing.T) {
		t.Parallel()
		firstCalls, secondCalls := 0, 0
		target, targetErr := ir.NewTarget(
			ir.HarnessClaudeCode, contract, "SKILL.md",
			ir.OperationLowererFunc(func(ir.SemanticOperation, ir.Location) ([]byte, error) { return []byte("semantic"), nil }),
			ir.NativeValidatorFunc(func(ir.RenderedTree) error {
				firstCalls++
				return errors.New("injected first loader failure")
			}),
			ir.NativeValidatorFunc(func(ir.RenderedTree) error {
				secondCalls++
				return nil
			}),
		)
		require.NoError(t, targetErr)
		tree, err := ir.Compile(document, target)
		require.Error(t, err)
		assert.Zero(t, tree.Len())
		assert.Equal(t, 1, firstCalls, "the failing validator must run exactly once")
		assert.Equal(t, 0, secondCalls, "a later validator must never run after an earlier one fails")
	})

	t.Run("all validators pass and every stage runs exactly once", func(t *testing.T) {
		t.Parallel()
		lowererCalls, firstCalls, secondCalls := 0, 0, 0
		target, targetErr := ir.NewTarget(
			ir.HarnessClaudeCode, contract, "SKILL.md",
			ir.OperationLowererFunc(func(ir.SemanticOperation, ir.Location) ([]byte, error) {
				lowererCalls++
				return []byte("semantic"), nil
			}),
			ir.NativeValidatorFunc(func(ir.RenderedTree) error { firstCalls++; return nil }),
			ir.NativeValidatorFunc(func(ir.RenderedTree) error { secondCalls++; return nil }),
		)
		require.NoError(t, targetErr)
		tree, err := ir.Compile(document, target)
		require.NoError(t, err)
		assert.Equal(t, 1, tree.Len())
		assert.Equal(t, 1, lowererCalls)
		assert.Equal(t, 1, firstCalls)
		assert.Equal(t, 1, secondCalls)
	})

	t.Run("target-literal unsupported case for the selected harness", func(t *testing.T) {
		t.Parallel()
		claude, err := ir.LiteralUnsupported(ir.HarnessClaudeCode, "no reviewed native syntax")
		require.NoError(t, err)
		openCode, err := ir.LiteralUnsupported(ir.HarnessOpenCode, "unsupported")
		require.NoError(t, err)
		codex, err := ir.LiteralUnsupported(ir.HarnessCodex, "unsupported")
		require.NoError(t, err)
		literal, err := ir.TargetLiteral([]ir.TargetCase{claude, openCode, codex}, mustLocation(t, "unsupported-literal", 0))
		require.NoError(t, err)
		literalDocument, err := ir.NewDocument(literal)
		require.NoError(t, err)
		tree, err := ir.Compile(literalDocument, mustTarget(t, contract, nil))
		require.Error(t, err)
		assert.Zero(t, tree.Len())
	})

	t.Run("target-literal contract mismatch", func(t *testing.T) {
		t.Parallel()
		olderContract := mustContract(t, ir.HarnessClaudeCode, "2.1.209")
		claude, err := ir.LiteralForHarness(ir.HarnessClaudeCode, olderContract, []byte("old literal"), "reviewed for an older contract")
		require.NoError(t, err)
		openCode, err := ir.LiteralUnsupported(ir.HarnessOpenCode, "unsupported")
		require.NoError(t, err)
		codex, err := ir.LiteralUnsupported(ir.HarnessCodex, "unsupported")
		require.NoError(t, err)
		literal, err := ir.TargetLiteral([]ir.TargetCase{claude, openCode, codex}, mustLocation(t, "mismatched-literal", 0))
		require.NoError(t, err)
		literalDocument, err := ir.NewDocument(literal)
		require.NoError(t, err)
		tree, err := ir.Compile(literalDocument, mustTarget(t, contract, nil))
		require.Error(t, err)
		assert.Zero(t, tree.Len())
	})
}

func mustTargetSpy(t testing.TB, contract ir.RuntimeContractID, lowererCalls, validatorCalls *int, lowererErr error) ir.Target {
	t.Helper()
	lowerer := ir.OperationLowererFunc(func(ir.SemanticOperation, ir.Location) ([]byte, error) {
		if lowererCalls != nil {
			*lowererCalls++
		}
		if lowererErr != nil {
			return nil, lowererErr
		}
		return []byte("semantic"), nil
	})
	validator := ir.NativeValidatorFunc(func(ir.RenderedTree) error {
		if validatorCalls != nil {
			*validatorCalls++
		}
		return nil
	})
	target, err := ir.NewTarget(ir.HarnessClaudeCode, contract, "SKILL.md", lowerer, validator)
	require.NoError(t, err)
	return target
}

func TestTargetLiteralRequiresExhaustiveUniqueCasesAndExactContract(t *testing.T) {
	t.Parallel()

	location := mustLocation(t, "literal-validation", 0)
	claudeContract := mustContract(t, ir.HarnessClaudeCode, "2.1.210")
	claude, err := ir.LiteralForHarness(ir.HarnessClaudeCode, claudeContract, []byte("literal"), "reviewed")
	require.NoError(t, err)
	openCode, err := ir.LiteralUnsupported(ir.HarnessOpenCode, "unsupported")
	require.NoError(t, err)
	_, err = ir.TargetLiteral([]ir.TargetCase{claude, openCode}, location)
	assert.Error(t, err)
	codex, err := ir.LiteralUnsupported(ir.HarnessCodex, "unsupported")
	require.NoError(t, err)
	_, err = ir.TargetLiteral([]ir.TargetCase{claude, claude, openCode, codex}, location)
	assert.Error(t, err)

	part, err := ir.TargetLiteral([]ir.TargetCase{claude, openCode, codex}, location)
	require.NoError(t, err)
	document, err := ir.NewDocument(part)
	require.NoError(t, err)
	differentContract := mustContract(t, ir.HarnessClaudeCode, "2.1.211")
	tree, err := ir.Compile(document, mustTarget(t, differentContract, nil))
	require.Error(t, err)
	assert.Zero(t, tree.Len())

	openCodeTarget := mustTargetForHarness(t, ir.HarnessOpenCode, mustContract(t, ir.HarnessOpenCode, "1.17.18"), nil)
	tree, err = ir.Compile(document, openCodeTarget)
	require.Error(t, err)
	assert.Zero(t, tree.Len())

	for _, unsafePath := range []string{"/absolute", "../escape", "..", "a/../escape", `windows\path`} {
		_, err := ir.NewTarget(ir.HarnessClaudeCode, claudeContract, unsafePath, nil)
		assert.Error(t, err, unsafePath)
	}
}

func mustTarget(t testing.TB, contract ir.RuntimeContractID, lowerer ir.OperationLowerer) ir.Target {
	t.Helper()
	return mustTargetForHarness(t, ir.HarnessClaudeCode, contract, lowerer)
}

func mustTargetForHarness(t testing.TB, harness ir.HarnessID, contract ir.RuntimeContractID, lowerer ir.OperationLowerer) ir.Target {
	t.Helper()
	target, err := ir.NewTarget(harness, contract, "SKILL.md", lowerer)
	require.NoError(t, err)
	return target
}

func FuzzMarkdownPart(f *testing.F) {
	f.Add([]byte("# Heading\n\ntext\n"))
	f.Add([]byte("````\nTeamCreate -> task((\n````\n"))
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, source []byte) {
		location, err := ir.NewLocation("fuzz", "fuzz/SKILL.md", "body", ir.SourceRange{})
		require.NoError(t, err)
		part, err := ir.Markdown(source, location)
		if err != nil {
			return
		}
		document, err := ir.NewDocument(part)
		require.NoError(t, err)
		contract := mustContract(t, ir.HarnessCodex, "0.144.1")
		target := mustTargetForHarness(t, ir.HarnessCodex, contract, nil)
		tree, err := ir.Compile(document, target)
		require.NoError(t, err)
		file, ok := tree.File("SKILL.md")
		require.True(t, ok)
		assert.Equal(t, source, file.Content())
	})
}
