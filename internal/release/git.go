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

// GitRollback restores the named tag and resets modified files to HEAD.
// Used to undo a partial release on error.
func GitRollback(dir, tag string) error {
	// Delete the tag if it was created.
	_, _ = gitRun(dir, "tag", "-d", tag) // ignore error if tag didn't exist
	// Reset working tree to HEAD (undo file writes).
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
