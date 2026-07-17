package effects_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustArgs(t testing.TB, values ...string) []effects.Argument {
	t.Helper()
	args := make([]effects.Argument, 0, len(values))
	for _, value := range values {
		arg, err := effects.NewArgument(value)
		require.NoError(t, err)
		args = append(args, arg)
	}
	return args
}

func mustExecutable(t testing.TB, name string) effects.ExecutableRef {
	t.Helper()
	ref, err := effects.NewExecutableRef(name)
	require.NoError(t, err)
	return ref
}

func mustPathDir(t testing.TB, path string) effects.WorkingDirectoryRef {
	t.Helper()
	dir, err := effects.NewPathWorkingDirectory(mustOwnedPath(t, path))
	require.NoError(t, err)
	return dir
}

// TestRunProcessQuotingAndDefaults proves argv quoting is preserved verbatim and
// that unset streams/exit take safe defaults.
func TestRunProcessQuotingAndDefaults(t *testing.T) {
	t.Parallel()

	process, err := effects.NewRunProcess(effects.RunProcessSpec{
		Executable: mustExecutable(t, "git"),
		Arguments:  mustArgs(t, "commit", "-m", "fix: a b | c && d"),
		Directory:  mustPathDir(t, "repo"),
	})
	require.NoError(t, err)
	assert.True(t, process.IsValid())
	assert.Equal(t, effects.RuntimeClassNative, process.Classify())

	args := process.Arguments()
	require.Len(t, args, 3)
	assert.Equal(t, "fix: a b | c && d", args[2].String(), "argv element is verbatim, never shell-split")

	assert.Equal(t, effects.InputNone, process.Stdin().Kind())
	assert.Equal(t, effects.OutputDiscard, process.Stdout().Kind())
	assert.True(t, process.Exit().Accepts(0))
}

// TestRunProcessTypedStreamsAndEnv proves literal/file/captured streams, typed
// env, working directory, and exit sets round-trip.
func TestRunProcessTypedStreamsAndEnv(t *testing.T) {
	t.Parallel()

	captured, err := effects.NewCaptureID("out")
	require.NoError(t, err)
	stdout, err := effects.NewCapturedOutput(captured)
	require.NoError(t, err)
	env, err := effects.NewEnvBinding("GIT_AUTHOR_NAME", "Pasture")
	require.NoError(t, err)
	exit, err := effects.NewExitExpectation(0, 1)
	require.NoError(t, err)

	process, err := effects.NewRunProcess(effects.RunProcessSpec{
		Executable:  mustExecutable(t, "grep"),
		Arguments:   mustArgs(t, "pattern"),
		Directory:   mustPathDir(t, "repo"),
		Environment: []effects.EnvBinding{env},
		Stdin:       effects.NewLiteralInput([]byte("haystack")),
		Stdout:      stdout,
		Exit:        exit,
	})
	require.NoError(t, err)

	assert.Equal(t, effects.InputLiteral, process.Stdin().Kind())
	capture, ok := process.Stdout().Capture()
	require.True(t, ok)
	assert.Equal(t, "out", capture.String())
	require.Len(t, process.Environment(), 1)
	assert.Equal(t, "GIT_AUTHOR_NAME", process.Environment()[0].Name())
	assert.True(t, process.Exit().Accepts(1))
}

// TestRunProcessNegatives covers missing operands and duplicate env.
func TestRunProcessNegatives(t *testing.T) {
	t.Parallel()

	_, err := effects.NewRunProcess(effects.RunProcessSpec{
		Executable: effects.ExecutableRef{},
		Directory:  mustPathDir(t, "repo"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "executable")

	_, err = effects.NewRunProcess(effects.RunProcessSpec{
		Executable: mustExecutable(t, "git"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "working directory")

	dup1, _ := effects.NewEnvBinding("X", "1")
	dup2, _ := effects.NewEnvBinding("X", "2")
	_, err = effects.NewRunProcess(effects.RunProcessSpec{
		Executable:  mustExecutable(t, "git"),
		Directory:   mustPathDir(t, "repo"),
		Environment: []effects.EnvBinding{dup1, dup2},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than once")
}

// TestPipelineCommandSubstitutionDataflow proves command substitution is modeled
// as an explicit output-to-input edge: a later step consumes an earlier step's
// captured output.
func TestPipelineCommandSubstitutionDataflow(t *testing.T) {
	t.Parallel()

	capture, err := effects.NewCaptureID("head-sha")
	require.NoError(t, err)
	produceOut, err := effects.NewCapturedOutput(capture)
	require.NoError(t, err)
	producer, err := effects.NewRunProcess(effects.RunProcessSpec{
		Executable: mustExecutable(t, "git"),
		Arguments:  mustArgs(t, "rev-parse", "HEAD"),
		Directory:  mustPathDir(t, "repo"),
		Stdout:     produceOut,
	})
	require.NoError(t, err)
	producerStep, err := effects.NewProcessStep(producer)
	require.NoError(t, err)

	consumeIn, err := effects.NewPreviousOutputInput(capture)
	require.NoError(t, err)
	consumer, err := effects.NewRunProcess(effects.RunProcessSpec{
		Executable: mustExecutable(t, "git"),
		Arguments:  mustArgs(t, "tag", "-a", "-F", "-"),
		Directory:  mustPathDir(t, "repo"),
		Stdin:      consumeIn,
	})
	require.NoError(t, err)
	consumerStep, err := effects.NewProcessStep(consumer)
	require.NoError(t, err)

	pipeline, err := effects.NewPipeline(producerStep, consumerStep)
	require.NoError(t, err)
	require.Len(t, pipeline.Steps(), 2)
}

// TestPipelineRejectsBrokenDataflow proves ordering and dataflow errors: an edge
// with no earlier producer, a self/forward reference, and a duplicate capture.
func TestPipelineRejectsBrokenDataflow(t *testing.T) {
	t.Parallel()

	capture, err := effects.NewCaptureID("cap")
	require.NoError(t, err)
	consumeIn, err := effects.NewPreviousOutputInput(capture)
	require.NoError(t, err)
	orphan, err := effects.NewRunProcess(effects.RunProcessSpec{
		Executable: mustExecutable(t, "cat"),
		Directory:  mustPathDir(t, "repo"),
		Stdin:      consumeIn,
	})
	require.NoError(t, err)
	orphanStep, err := effects.NewProcessStep(orphan)
	require.NoError(t, err)

	_, err = effects.NewPipeline(orphanStep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before any step produces it")

	_, err = effects.NewPipeline()
	require.Error(t, err)

	// Duplicate capture across two steps.
	out1, err := effects.NewCapturedOutput(capture)
	require.NoError(t, err)
	out2, err := effects.NewCapturedOutput(capture)
	require.NoError(t, err)
	p1, err := effects.NewRunProcess(effects.RunProcessSpec{Executable: mustExecutable(t, "a"), Directory: mustPathDir(t, "repo"), Stdout: out1})
	require.NoError(t, err)
	p2, err := effects.NewRunProcess(effects.RunProcessSpec{Executable: mustExecutable(t, "b"), Directory: mustPathDir(t, "repo"), Stdout: out2})
	require.NoError(t, err)
	s1, err := effects.NewProcessStep(p1)
	require.NoError(t, err)
	s2, err := effects.NewProcessStep(p2)
	require.NoError(t, err)
	_, err = effects.NewPipeline(s1, s2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "produced more than once")
}

// TestClassifyShellConstructRejectsUnsupported proves unsupported shell
// constructs fail actionably and are never rendered through sh -c.
func TestClassifyShellConstructRejectsUnsupported(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fragment  string
		construct effects.ShellConstruct
	}{
		// Compound multi-character idioms.
		{"echo $(date)", effects.ShellExpansion},
		{"echo ${VAR}", effects.ShellExpansion},
		{"a && b", effects.ShellControlOperator},
		{"cmd > out.txt", effects.ShellRedirection},
		{"cmd 2>&1", effects.ShellRedirection},
		{"cmd >> out.txt", effects.ShellRedirection},
		// Bare single-metacharacter tokens (the shellMetaCharacters-derived
		// fallback scan), each previously untested.
		{"a ; b", effects.ShellControlOperator},
		{"a || b", effects.ShellControlOperator},
		{"cmd &", effects.ShellControlOperator},
		{"! cmd", effects.ShellControlOperator},
		{"echo a\necho b", effects.ShellControlOperator},
		{"printf 'a\tb'", effects.ShellControlOperator},
		{"a | b", effects.ShellPipeline},
		{"ls *.go", effects.ShellGlobbing},
		{"ls file?.go", effects.ShellGlobbing},
		{"ls [abc].go", effects.ShellGlobbing},
		{"cmd < in.txt", effects.ShellRedirection},
		{"echo `date`", effects.ShellExpansion},
		{"echo $VAR", effects.ShellExpansion},
		{"~/foo", effects.ShellExpansion},
		{"(echo hi)", effects.ShellGrouping},
		{"echo hi)", effects.ShellGrouping},
		{"{ echo hi; }", effects.ShellGrouping},
		{"echo hi}", effects.ShellGrouping},
		{`echo "hi"`, effects.ShellQuoting},
		{"echo 'hi'", effects.ShellQuoting},
		{`echo a\ b`, effects.ShellQuoting},
		{"echo hi # comment", effects.ShellComment},
	}
	for _, testCase := range cases {
		construct, err := effects.ClassifyShellConstruct(testCase.fragment)
		require.Error(t, err, testCase.fragment)
		assert.Equal(t, testCase.construct, construct, testCase.fragment)
		assert.NotContains(t, err.Error(), "sh -c ", "must not suggest smuggling through sh -c")
		assert.Contains(t, err.Error(), "dedicated semantic operation", testCase.fragment)
	}

	// A plain command with no unsupported construct classifies clean.
	construct, err := effects.ClassifyShellConstruct("git status")
	require.NoError(t, err)
	assert.Equal(t, effects.ShellConstruct(""), construct)
}

// TestRunProcessEffectsRoundTrip proves the declared portable EffectSet is
// carried through.
func TestRunProcessEffectsRoundTrip(t *testing.T) {
	t.Parallel()

	effectID, err := ir.NewEffectID("pasture.effect.filesystem-write/v1")
	require.NoError(t, err)
	set, err := ir.NewEffectSet(effectID)
	require.NoError(t, err)
	process, err := effects.NewRunProcess(effects.RunProcessSpec{
		Executable: mustExecutable(t, "git"),
		Directory:  mustPathDir(t, "repo"),
		Effects:    set,
	})
	require.NoError(t, err)
	assert.True(t, process.Effects().Equal(set))
}
