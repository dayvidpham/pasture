package effects_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/effects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeB is a second fixed valid tree digest, distinct from treeA, reused
// across GitRepositoryPusher tree-mismatch scenarios.
const treeB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// commandCall records one CommandRunner invocation for assertion.
type commandCall struct {
	dir        string
	executable string
	args       []string
}

type stubResponse struct {
	output string
	err    error
}

// stubCommandRunner is a deterministic, in-memory CommandRunner: each call
// pops the next canned (output, err) pair off responses and records the exact
// dir/executable/args it was invoked with, so tests can pin GitRepositoryPusher's
// command construction without a real git binary.
type stubCommandRunner struct {
	calls     []commandCall
	responses []stubResponse
}

func (s *stubCommandRunner) run(dir, executable string, args ...string) (string, error) {
	s.calls = append(s.calls, commandCall{dir: dir, executable: executable, args: append([]string(nil), args...)})
	if len(s.responses) == 0 {
		return "", fmt.Errorf("stubCommandRunner: no canned response configured for call %d", len(s.calls))
	}
	next := s.responses[0]
	s.responses = s.responses[1:]
	return next.output, next.err
}

func stubResolver(path string, resolveErr error) effects.ExecutableResolver {
	return func(string) (string, error) {
		if resolveErr != nil {
			return "", resolveErr
		}
		return path, nil
	}
}

func mustPusher(t testing.TB, runner *stubCommandRunner, remoteName string) effects.GitRepositoryPusher {
	t.Helper()
	pusher, err := effects.NewGitRepositoryPusher(stubResolver("/usr/bin/git", nil), runner.run, remoteName)
	require.NoError(t, err)
	return pusher
}

// --- NewGitRepositoryPusher validation ---

func TestNewGitRepositoryPusherRejectsNilResolver(t *testing.T) {
	t.Parallel()
	_, err := effects.NewGitRepositoryPusher(nil, (&stubCommandRunner{}).run, "origin")
	require.Error(t, err)
}

func TestNewGitRepositoryPusherRejectsNilRunner(t *testing.T) {
	t.Parallel()
	_, err := effects.NewGitRepositoryPusher(stubResolver("/usr/bin/git", nil), nil, "origin")
	require.Error(t, err)
}

func TestNewGitRepositoryPusherRejectsEmptyRemoteName(t *testing.T) {
	t.Parallel()
	_, err := effects.NewGitRepositoryPusher(stubResolver("/usr/bin/git", nil), (&stubCommandRunner{}).run, "   ")
	require.Error(t, err)
}

// --- VerifyLocalObject: command construction + kind/tree verification ---

func TestGitRepositoryPusherVerifyLocalObjectAcceptsMatchingCommitAndTree(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{
		{output: "commit"},
		{output: treeA},
	}}
	pusher := mustPusher(t, runner, "origin")

	err := pusher.VerifyLocalObject(mustRepository(t, "/repo"), mustCommit(t, oidA), mustTree(t, treeA))
	require.NoError(t, err)

	require.Len(t, runner.calls, 2)
	assert.Equal(t, "/repo", runner.calls[0].dir)
	assert.Equal(t, "/usr/bin/git", runner.calls[0].executable)
	assert.Equal(t, []string{"cat-file", "-t", oidA}, runner.calls[0].args)
	assert.Equal(t, "/repo", runner.calls[1].dir)
	assert.Equal(t, []string{"rev-parse", oidA + "^{tree}"}, runner.calls[1].args)
}

func TestGitRepositoryPusherVerifyLocalObjectRejectsNonCommitKind(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{output: "blob"}}}
	pusher := mustPusher(t, runner, "origin")

	err := pusher.VerifyLocalObject(mustRepository(t, "/repo"), mustCommit(t, oidA), mustTree(t, treeA))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a commit")
	assert.Len(t, runner.calls, 1, "must short-circuit before checking the tree")
}

func TestGitRepositoryPusherVerifyLocalObjectRejectsTreeMismatch(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{
		{output: "commit"},
		{output: treeB},
	}}
	pusher := mustPusher(t, runner, "origin")

	err := pusher.VerifyLocalObject(mustRepository(t, "/repo"), mustCommit(t, oidA), mustTree(t, treeA))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the expected")
	require.Len(t, runner.calls, 2)
}

func TestGitRepositoryPusherVerifyLocalObjectSurfacesCatFileFailure(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{err: errors.New("fatal: not a valid object")}}}
	pusher := mustPusher(t, runner, "origin")

	err := pusher.VerifyLocalObject(mustRepository(t, "/repo"), mustCommit(t, oidA), mustTree(t, treeA))
	require.Error(t, err)
	require.Len(t, runner.calls, 1, "must not attempt rev-parse when cat-file fails")
}

// --- PushExact: force-with-lease expect-string + refspec construction ---

func TestGitRepositoryPusherPushExactForceWithLeaseExpectAbsentRemote(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{output: ""}}}
	pusher := mustPusher(t, runner, "origin")
	remoteRef := mustRemoteRef(t, "refs/heads/main")

	err := pusher.PushExact(mustRepository(t, "/repo"), mustCommit(t, oidA), remoteRef, effects.ExpectAbsentRemote())
	require.NoError(t, err)

	require.Len(t, runner.calls, 1)
	call := runner.calls[0]
	assert.Equal(t, "/repo", call.dir)
	assert.Equal(t, []string{
		"push",
		"--force-with-lease=refs/heads/main:",
		"origin",
		oidA + ":refs/heads/main",
	}, call.args, "an absent expectation must lease with an empty expect-string, requiring the ref not already exist")
}

func TestGitRepositoryPusherPushExactForceWithLeaseExpectRemoteAt(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{output: ""}}}
	pusher := mustPusher(t, runner, "origin")
	remoteRef := mustRemoteRef(t, "refs/heads/main")
	old, err := effects.ExpectRemoteAt(mustCommit(t, oidB))
	require.NoError(t, err)

	err = pusher.PushExact(mustRepository(t, "/repo"), mustCommit(t, oidA), remoteRef, old)
	require.NoError(t, err)

	require.Len(t, runner.calls, 1)
	call := runner.calls[0]
	assert.Equal(t, []string{
		"push",
		"--force-with-lease=refs/heads/main:" + oidB,
		"origin",
		oidA + ":refs/heads/main",
	}, call.args, "a present expectation must lease with the exact expected-old commit id")
}

func TestGitRepositoryPusherPushExactSurfacesFailure(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{err: errors.New("stale info")}}}
	pusher := mustPusher(t, runner, "origin")
	remoteRef := mustRemoteRef(t, "refs/heads/main")

	err := pusher.PushExact(mustRepository(t, "/repo"), mustCommit(t, oidA), remoteRef, effects.ExpectAbsentRemote())
	require.Error(t, err)
}

// --- ReadRemote: presence, absence, malformed and multi-line output ---

func TestGitRepositoryPusherReadRemotePresent(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{output: oidA + "\trefs/heads/main"}}}
	pusher := mustPusher(t, runner, "origin")
	remoteRef := mustRemoteRef(t, "refs/heads/main")

	state, err := pusher.ReadRemote(mustRepository(t, "/repo"), remoteRef)
	require.NoError(t, err)
	assert.True(t, state.Present())
	commit, ok := state.Commit()
	require.True(t, ok)
	assert.Equal(t, oidA, commit.String())

	require.Len(t, runner.calls, 1)
	assert.Equal(t, []string{"ls-remote", "origin", "refs/heads/main"}, runner.calls[0].args)
}

func TestGitRepositoryPusherReadRemoteEmptyOutputMeansAbsent(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{output: "   "}}}
	pusher := mustPusher(t, runner, "origin")

	state, err := pusher.ReadRemote(mustRepository(t, "/repo"), mustRemoteRef(t, "refs/heads/main"))
	require.NoError(t, err)
	assert.False(t, state.Present())
	_, ok := state.Commit()
	assert.False(t, ok)
}

func TestGitRepositoryPusherReadRemoteLowersUppercaseCommitID(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{output: strings.ToUpper(oidA) + "\trefs/heads/main"}}}
	pusher := mustPusher(t, runner, "origin")

	state, err := pusher.ReadRemote(mustRepository(t, "/repo"), mustRemoteRef(t, "refs/heads/main"))
	require.NoError(t, err)
	commit, ok := state.Commit()
	require.True(t, ok)
	assert.Equal(t, oidA, commit.String(), "the parsed commit id is canonicalized to lowercase")
}

func TestGitRepositoryPusherReadRemoteMalformedCommitID(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{output: "not-a-valid-oid\trefs/heads/main"}}}
	pusher := mustPusher(t, runner, "origin")

	_, err := pusher.ReadRemote(mustRepository(t, "/repo"), mustRemoteRef(t, "refs/heads/main"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unparseable")
}

func TestGitRepositoryPusherReadRemoteMultiLineOutputUsesFirstToken(t *testing.T) {
	t.Parallel()
	// ls-remote can report multiple lines (for example a ref and its peeled
	// annotated-tag target); ReadRemote must parse only the first field.
	multiLine := oidA + "\trefs/heads/main\n" + oidB + "\trefs/heads/main^{}"
	runner := &stubCommandRunner{responses: []stubResponse{{output: multiLine}}}
	pusher := mustPusher(t, runner, "origin")

	state, err := pusher.ReadRemote(mustRepository(t, "/repo"), mustRemoteRef(t, "refs/heads/main"))
	require.NoError(t, err)
	commit, ok := state.Commit()
	require.True(t, ok)
	assert.Equal(t, oidA, commit.String())
}

func TestGitRepositoryPusherReadRemoteSurfacesLsRemoteFailure(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{responses: []stubResponse{{err: errors.New("could not resolve host")}}}
	pusher := mustPusher(t, runner, "origin")

	_, err := pusher.ReadRemote(mustRepository(t, "/repo"), mustRemoteRef(t, "refs/heads/main"))
	require.Error(t, err)
}

// --- Executable resolution ---

func TestGitRepositoryPusherSurfacesResolverFailure(t *testing.T) {
	t.Parallel()
	runner := &stubCommandRunner{}
	pusher, err := effects.NewGitRepositoryPusher(stubResolver("", errors.New("exec: \"git\": executable file not found in $PATH")), runner.run, "origin")
	require.NoError(t, err)

	_, err = pusher.ReadRemote(mustRepository(t, "/repo"), mustRemoteRef(t, "refs/heads/main"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be resolved")
	assert.Empty(t, runner.calls, "the command runner must never be invoked when resolution fails")
}
