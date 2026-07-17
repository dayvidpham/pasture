package effects

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ExecutableResolver resolves a program name to an executable path, exactly like
// exec.LookPath. It is injected so process and git effects never assume a fixed
// binary location and can be exercised against a stub in tests.
type ExecutableResolver func(name string) (string, error)

// CommandRunner runs a resolved command in a working directory and returns its
// combined output. It is injected so git effects can be driven without touching
// a real repository in tests.
type CommandRunner func(dir, executable string, args ...string) (string, error)

// GitRepositoryPusher is the production RepositoryPusher backed by a git
// executable. It carries no guarded-push policy — verify/push/re-read primitives
// only; the verify-push-reread-then-prove algorithm lives in
// GuardedPushExactCommit. RepositoryID names the repository working directory,
// and remoteName is the configured git remote the RemoteRef is pushed to.
type GitRepositoryPusher struct {
	resolve    ExecutableResolver
	run        CommandRunner
	remoteName string
}

// NewGitRepositoryPusher wires a git-backed pusher. resolve locates the git
// binary (pass exec.LookPath in production); run executes it (pass
// DefaultCommandRunner in production). remoteName is the git remote the guarded
// push targets (for example "origin").
func NewGitRepositoryPusher(resolve ExecutableResolver, run CommandRunner, remoteName string) (GitRepositoryPusher, error) {
	if resolve == nil || run == nil {
		return GitRepositoryPusher{}, effectError(
			"git repository pusher is missing an executable resolver or command runner",
			"the git binary must be resolved and executed through injected seams",
			"NewGitRepositoryPusher", "git pusher wiring",
			"the pusher cannot dispatch git",
			"pass a non-nil resolver (exec.LookPath) and runner (DefaultCommandRunner)", nil,
		)
	}
	if strings.TrimSpace(remoteName) == "" {
		return GitRepositoryPusher{}, effectError(
			"git remote name is empty",
			"a guarded push targets a configured git remote",
			"NewGitRepositoryPusher", "git pusher wiring",
			"the pusher has no remote to push to",
			"supply the git remote name, such as \"origin\"", nil,
		)
	}
	return GitRepositoryPusher{resolve: resolve, run: run, remoteName: remoteName}, nil
}

// DefaultCommandRunner runs git through os/exec, returning trimmed combined
// output or an actionable error.
func DefaultCommandRunner(dir, executable string, args ...string) (string, error) {
	cmd := exec.Command(executable, args...) //nolint:gosec // executable resolved via injected LookPath
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"command %s %s failed in %s: %w — output: %s",
			executable, strings.Join(args, " "), dir, err, strings.TrimSpace(out.String()),
		)
	}
	return strings.TrimSpace(out.String()), nil
}

func (p GitRepositoryPusher) git(repository RepositoryID, args ...string) (string, error) {
	path, err := p.resolve("git")
	if err != nil {
		return "", effectError(
			"git executable could not be resolved",
			"git effects dispatch a resolved git binary, never a shell",
			"GitRepositoryPusher", "git dispatch",
			"the git primitive cannot run",
			"ensure git is installed and on PATH", err,
		)
	}
	return p.run(repository.String(), path, args...)
}

// VerifyLocalObject confirms the local repository holds the exact commit and
// that the commit's tree matches the expected tree digest.
func (p GitRepositoryPusher) VerifyLocalObject(repository RepositoryID, commit CommitOID, tree TreeDigest) error {
	kind, err := p.git(repository, "cat-file", "-t", commit.String())
	if err != nil {
		return err
	}
	if strings.TrimSpace(kind) != "commit" {
		return effectError(
			fmt.Sprintf("local object %s is a %q, not a commit", commit, strings.TrimSpace(kind)),
			"a guarded push lands an exact commit object",
			"GitRepositoryPusher.VerifyLocalObject", "git local verification",
			"the landing target is not a commit",
			"supply the object id of a commit", nil,
		)
	}
	actualTree, err := p.git(repository, "rev-parse", commit.String()+"^{tree}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(actualTree) != tree.String() {
		return effectError(
			fmt.Sprintf("commit %s carries tree %s, not the expected %s", commit, strings.TrimSpace(actualTree), tree),
			"the exact tree the commit must carry is verified before any push",
			"GitRepositoryPusher.VerifyLocalObject", "git local verification",
			"the local object does not match the expected tree",
			"land the commit whose tree matches the expected digest", nil,
		)
	}
	return nil
}

// PushExact performs only the commit:remoteRef update under a force-with-lease
// guard derived from the expected-old state. An error is not by itself failure:
// GuardedPushExactCommit re-reads the remote to decide.
func (p GitRepositoryPusher) PushExact(repository RepositoryID, commit CommitOID, remoteRef RemoteRef, expectedOld ExpectedOldOID) error {
	lease := remoteRef.String() + ":"
	if oldCommit, present := expectedOld.Commit(); present {
		lease = remoteRef.String() + ":" + oldCommit.String()
	}
	refspec := commit.String() + ":" + remoteRef.String()
	_, err := p.git(repository, "push", "--force-with-lease="+lease, p.remoteName, refspec)
	return err
}

// ReadRemote re-reads the current commit of remoteRef on the configured remote.
func (p GitRepositoryPusher) ReadRemote(repository RepositoryID, remoteRef RemoteRef) (RemoteState, error) {
	out, err := p.git(repository, "ls-remote", p.remoteName, remoteRef.String())
	if err != nil {
		return RemoteState{}, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return AbsentRemoteState(), nil
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return AbsentRemoteState(), nil
	}
	commit, err := NewCommitOID(strings.ToLower(fields[0]))
	if err != nil {
		return RemoteState{}, effectError(
			fmt.Sprintf("remote ref %s reported an unparseable commit id %q", remoteRef, fields[0]),
			"the re-read must yield an exact commit id to verify the landing",
			"GitRepositoryPusher.ReadRemote", "git remote verification",
			"the landing cannot be verified",
			"ensure the remote reports a full commit id for the ref", err,
		)
	}
	return PresentRemoteState(commit), nil
}
