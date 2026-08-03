package effects_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutableRefRejectsShellMetacharacters proves an executable name cannot
// smuggle shell operators: those must become dedicated semantic operations.
func TestExecutableRefRejectsShellMetacharacters(t *testing.T) {
	t.Parallel()

	valid := []string{"git", "/usr/bin/git", "./scripts/build.sh", "go"}
	for _, name := range valid {
		ref, err := effects.NewExecutableRef(name)
		require.NoError(t, err, name)
		assert.Equal(t, name, ref.String())
		assert.True(t, ref.IsValid())
	}

	invalid := []struct {
		name string
		hint string
	}{
		{"", "empty"},
		{" git", "empty or padded"},
		{"git|grep", "metacharacter"},
		{"echo $HOME", "metacharacter"},
		{"a && b", "metacharacter"},
		{"cat < f", "metacharacter"},
		{"ls *.go", "metacharacter"},
		{"sh -c \"x\"", "metacharacter"},
	}
	for _, testCase := range invalid {
		_, err := effects.NewExecutableRef(testCase.name)
		require.Error(t, err, testCase.name)
		assert.Contains(t, err.Error(), testCase.hint, testCase.name)
	}

	assert.False(t, effects.ExecutableRef{}.IsValid())
}

// TestArgumentIsLiteralNotShellParsed proves an argument is a verbatim argv
// element: shell-special characters are legal content, only NUL and invalid
// UTF-8 are rejected.
func TestArgumentIsLiteralNotShellParsed(t *testing.T) {
	t.Parallel()

	literals := []string{"--message=fix: bug", "$HOME is literal here", "a | b", "*", "'quoted'"}
	for _, value := range literals {
		arg, err := effects.NewArgument(value)
		require.NoError(t, err, value)
		assert.Equal(t, value, arg.String())
	}

	_, err := effects.NewArgument("has\x00nul")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NUL")
	assert.False(t, effects.Argument{}.IsValid())
}

// TestEnvBindingValueIsLiteral proves a binding value is never expanded and its
// name must be a portable identifier.
func TestEnvBindingValueIsLiteral(t *testing.T) {
	t.Parallel()

	binding, err := effects.NewEnvBinding("HOME", "$OTHER/not/expanded")
	require.NoError(t, err)
	assert.Equal(t, "HOME", binding.Name())
	assert.Equal(t, "$OTHER/not/expanded", binding.Value())

	badNames := []string{"", "1BAD", "has space", "has=eq", "lower-dash"}
	for _, name := range badNames {
		_, err := effects.NewEnvBinding(name, "v")
		require.Error(t, err, name)
	}
	assert.False(t, effects.EnvBinding{}.IsValid())
}

// TestInputRefSum covers each stdin source and the previous-output dataflow edge.
func TestInputRefSum(t *testing.T) {
	t.Parallel()

	assert.Equal(t, effects.InputNone, effects.NoInput().Kind())

	literal := effects.NewLiteralInput([]byte("payload"))
	assert.Equal(t, effects.InputLiteral, literal.Kind())
	got, ok := literal.Literal()
	require.True(t, ok)
	assert.Equal(t, []byte("payload"), got)

	fileInput, err := effects.NewFileInput(mustOwnedPath(t, "in.txt"))
	require.NoError(t, err)
	assert.Equal(t, effects.InputFile, fileInput.Kind())

	capture, err := effects.NewCaptureID("step-1-out")
	require.NoError(t, err)
	previous, err := effects.NewPreviousOutputInput(capture)
	require.NoError(t, err)
	consumed, ok := previous.PreviousOutput()
	require.True(t, ok)
	assert.Equal(t, "step-1-out", consumed.String())

	_, err = effects.NewPreviousOutputInput(effects.CaptureID{})
	require.Error(t, err)
	_, err = effects.NewFileInput(effects.OwnedPath{})
	require.Error(t, err)
}

// TestOutputRefSum covers each stdout/stderr sink.
func TestOutputRefSum(t *testing.T) {
	t.Parallel()

	assert.Equal(t, effects.OutputDiscard, effects.DiscardOutput().Kind())

	capture, err := effects.NewCaptureID("cap")
	require.NoError(t, err)
	captured, err := effects.NewCapturedOutput(capture)
	require.NoError(t, err)
	got, ok := captured.Capture()
	require.True(t, ok)
	assert.Equal(t, "cap", got.String())

	fileOut, err := effects.NewFileOutput(mustOwnedPath(t, "out.txt"))
	require.NoError(t, err)
	path, ok := fileOut.File()
	require.True(t, ok)
	assert.Equal(t, "out.txt", path.String())

	_, err = effects.NewCapturedOutput(effects.CaptureID{})
	require.Error(t, err)
}

// TestExitExpectation covers the success set, custom sets, dedup/sort, and range.
func TestExitExpectation(t *testing.T) {
	t.Parallel()

	success := effects.ExpectSuccess()
	assert.True(t, success.Accepts(0))
	assert.False(t, success.Accepts(1))

	custom, err := effects.NewExitExpectation(3, 0, 3, 1)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1, 3}, custom.Codes())
	assert.True(t, custom.Accepts(3))

	_, err = effects.NewExitExpectation()
	require.Error(t, err)
	_, err = effects.NewExitExpectation(256)
	require.Error(t, err)
	_, err = effects.NewExitExpectation(-1)
	require.Error(t, err)
	assert.False(t, effects.ExitExpectation{}.IsValid())
}

// TestWorkingDirectoryRefSum covers the path and worktree variants.
func TestWorkingDirectoryRefSum(t *testing.T) {
	t.Parallel()

	pathDir, err := effects.NewPathWorkingDirectory(mustOwnedPath(t, "sub/dir"))
	require.NoError(t, err)
	assert.Equal(t, effects.WorkingDirectoryPath, pathDir.Kind())
	_, ok := pathDir.Path()
	assert.True(t, ok)

	worktree, err := ir.NewWorktreeRef("assignment-42")
	require.NoError(t, err)
	worktreeDir, err := effects.NewWorktreeWorkingDirectory(worktree)
	require.NoError(t, err)
	assert.Equal(t, effects.WorkingDirectoryWorktree, worktreeDir.Kind())
	_, ok = worktreeDir.Worktree()
	assert.True(t, ok)

	_, err = effects.NewPathWorkingDirectory(effects.OwnedPath{})
	require.Error(t, err)
	_, err = effects.NewWorktreeWorkingDirectory(ir.WorktreeRef{})
	require.Error(t, err)
}
