package release

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ─── Git helpers ─────────────────────────────────────────────────────────────

// gitRun runs a git command in dir and returns its combined stdout.
// Returns a descriptive error on non-zero exit.
func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"workflow error: git %s failed in %s — %w — stderr: %s — "+
				"ensure git is installed and the directory is a git repository",
			strings.Join(args, " "), dir, err, errOut.String(),
		)
	}
	return strings.TrimSpace(out.String()), nil
}

// GitStatus returns the short status output for the repository at dir.
func GitStatus(dir string) (string, error) {
	return gitRun(dir, "status", "--porcelain")
}

// GitIsDetachedHead reports whether the repository at dir has a detached HEAD
// (i.e. HEAD points at a commit rather than a branch). It mirrors
// aura-release's is_detached_head, which runs `git symbolic-ref --quiet HEAD`.
//
// The probe distinguishes three exit states, because not every non-zero exit
// means "detached":
//   - exit 0   → HEAD resolves to a branch ref            → (false, nil)
//   - exit 1   → HEAD is valid but resolves to no branch  → (true, nil) detached
//   - exit 128 → not a git repository (or git unusable)   → (false, nil)
//
// Exit 128 is deliberately treated as "not detached" rather than an error: the
// caller's subsequent working-tree validation (GitStatus) surfaces genuine
// "not a git repo" problems for real releases, while dry-run flows that operate
// on a non-repo directory must not be blocked by this pre-flight guard. Only a
// genuine detached HEAD (exit 1) should block a release.
func GitIsDetachedHead(dir string) (bool, error) {
	cmd := exec.Command("git", "symbolic-ref", "--quiet", "HEAD") //nolint:gosec
	cmd.Dir = dir
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err == nil {
		// HEAD resolves to a branch ref → not detached.
		return false, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		// Exit 1 = valid repo, HEAD not a branch → genuinely detached.
		// Any other non-zero (notably 128 = not a git repo) is NOT a detached
		// HEAD; let the working-tree check handle non-repo errors instead.
		if exitErr.ExitCode() == 1 {
			return true, nil
		}
		return false, nil
	}
	// git could not be executed at all (missing binary, etc.).
	return false, fmt.Errorf(
		"workflow error: cannot determine HEAD state in %s — %w — stderr: %s — "+
			"ensure git is installed and on PATH",
		dir, err, errOut.String(),
	)
}

// GitTag creates an annotated git tag at HEAD in dir.
func GitTag(dir, tag, message string) error {
	_, err := gitRun(dir, "tag", "-a", tag, "-m", message)
	if err != nil {
		return fmt.Errorf(
			"workflow error: cannot create git tag %s — %w — "+
				"delete an existing tag with 'git tag -d %s' if it already exists",
			tag, err, tag,
		)
	}
	return nil
}

// GitCommit stages files and creates a commit with message in dir.
func GitCommit(dir string, files []string, message string) error {
	if len(files) == 0 {
		return fmt.Errorf(
			"validation error: GitCommit called with no files to stage — "+
				"provide at least one file path relative to %s",
			dir,
		)
	}
	addArgs := append([]string{"add", "--"}, files...)
	if _, err := gitRun(dir, addArgs...); err != nil {
		return err
	}
	if _, err := gitRun(dir, "commit", "-m", message); err != nil {
		return fmt.Errorf(
			"workflow error: git commit failed in %s — %w — "+
				"check that the staged files have changes",
			dir, err,
		)
	}
	return nil
}

// GitRollback partially undoes a release on error: it deletes the created tag
// and restores TRACKED files to HEAD (discarding uncommitted writes to files git
// already tracks). "git checkout -- ." touches tracked files only, so a file the
// release newly CREATED but did not commit (e.g. a first-ever CHANGELOG.md
// written by prependChangelog) is not removed by the rollback — whether it was
// left untracked or already git-added/staged when a later step failed, it
// survives. It intentionally does NOT undo an already-made release commit —
// matching aura-release, whose rollback likewise leaves the commit in place. A
// caller that needs the commit gone must reset it separately.
func GitRollback(dir, tag string) error {
	// Delete the tag if it was created.
	_, _ = gitRun(dir, "tag", "-d", tag) // ignore error if tag didn't exist
	// Restore tracked files to HEAD (undo writes to tracked files; a file the
	// release newly created is not touched and remains, whether left untracked
	// or already staged).
	if _, err := gitRun(dir, "checkout", "--", "."); err != nil {
		return fmt.Errorf(
			"workflow error: git rollback failed in %s — %w — "+
				"manually restore modified files with 'git checkout -- .'",
			dir, err,
		)
	}
	return nil
}

// GitLatestVersionTag returns the most recent vX.Y.Z tag reachable from HEAD,
// or ("", nil) if none exists.
func GitLatestVersionTag(dir string) (string, error) {
	out, err := gitRun(dir, "tag", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "v") {
			// Validate it looks like vX.Y.Z
			rest := strings.TrimPrefix(line, "v")
			if _, parseErr := ParseSemVer(rest); parseErr == nil {
				return line, nil
			}
		}
	}
	return "", nil
}

// GitCommitsSince returns the subject lines of commits since ref (exclusive).
func GitCommitsSince(dir, ref string) ([]string, error) {
	out, err := gitRun(dir, "log", ref+"..HEAD", "--format=%s")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// GitAllCommits returns the subject lines of all commits reachable from HEAD.
func GitAllCommits(dir string) ([]string, error) {
	out, err := gitRun(dir, "log", "--format=%s")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
