package release_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/release"
)

// initGitRepo creates a minimal git repo in dir.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@test.com")
	mustGit(t, dir, "config", "user.name", "Test")
	return dir
}

// commitFile writes a file and commits it in dir.
func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", name)
	mustGit(t, dir, "commit", "-m", msg)
}

// ─── GitStatus ────────────────────────────────────────────────────────────────

func TestGitStatus_Clean(t *testing.T) {
	dir := initGitRepo(t)
	commitFile(t, dir, "README.md", "hello", "init: readme")

	status, err := release.GitStatus(dir)
	if err != nil {
		t.Fatalf("GitStatus error: %v", err)
	}
	if status != "" {
		t.Errorf("clean repo should have empty status, got: %q", status)
	}
}

func TestGitStatus_Dirty(t *testing.T) {
	dir := initGitRepo(t)
	commitFile(t, dir, "README.md", "hello", "init: readme")
	// Modify the file without committing.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := release.GitStatus(dir)
	if err != nil {
		t.Fatalf("GitStatus error: %v", err)
	}
	if status == "" {
		t.Error("dirty repo should have non-empty status")
	}
}

// ─── GitTag ───────────────────────────────────────────────────────────────────

func TestGitTag(t *testing.T) {
	dir := initGitRepo(t)
	commitFile(t, dir, "f.txt", "content", "feat: something")

	if err := release.GitTag(dir, "v1.0.0", "Release 1.0.0"); err != nil {
		t.Fatalf("GitTag error: %v", err)
	}
	// Tag should exist.
	tag, err := release.GitLatestVersionTag(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.0.0" {
		t.Errorf("tag = %q, want %q", tag, "v1.0.0")
	}
}

// ─── GitCommit ────────────────────────────────────────────────────────────────

func TestGitCommit(t *testing.T) {
	dir := initGitRepo(t)
	// Create initial commit so we have a HEAD.
	commitFile(t, dir, "init.txt", "init", "chore: init")

	// Write a new file to stage and commit.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := release.GitCommit(dir, []string{"new.txt"}, "feat: new file"); err != nil {
		t.Fatalf("GitCommit error: %v", err)
	}
	// Working tree should be clean again.
	status, _ := release.GitStatus(dir)
	if status != "" {
		t.Errorf("after commit, status should be empty, got: %q", status)
	}
}

func TestGitCommit_NoFiles(t *testing.T) {
	dir := initGitRepo(t)
	err := release.GitCommit(dir, nil, "empty commit")
	if err == nil {
		t.Error("expected error for empty file list")
	}
}

// ─── GitRollback ─────────────────────────────────────────────────────────────

func TestGitRollback(t *testing.T) {
	dir := initGitRepo(t)
	commitFile(t, dir, "v.txt", "1.0.0", "chore: init")

	// Simulate an in-progress write.
	if err := os.WriteFile(filepath.Join(dir, "v.txt"), []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "tag", "-a", "v1.0.0", "-m", "test tag")

	if err := release.GitRollback(dir, "v1.0.0"); err != nil {
		t.Fatalf("GitRollback error: %v", err)
	}
	// File should be restored.
	data, _ := os.ReadFile(filepath.Join(dir, "v.txt"))
	if string(data) != "1.0.0" {
		t.Errorf("after rollback, file = %q, want %q", data, "1.0.0")
	}
	// Tag should be deleted.
	tag, _ := release.GitLatestVersionTag(dir)
	if tag != "" {
		t.Errorf("tag should be deleted after rollback, got %q", tag)
	}
}

// ─── GitLatestVersionTag ─────────────────────────────────────────────────────

func TestGitLatestVersionTag_None(t *testing.T) {
	dir := initGitRepo(t)
	commitFile(t, dir, "f.txt", "init", "init")

	tag, err := release.GitLatestVersionTag(dir)
	if err != nil {
		t.Fatalf("GitLatestVersionTag error: %v", err)
	}
	if tag != "" {
		t.Errorf("expected empty tag, got %q", tag)
	}
}

func TestGitLatestVersionTag_Multiple(t *testing.T) {
	dir := initGitRepo(t)
	commitFile(t, dir, "f.txt", "v1", "chore: v1")
	mustGit(t, dir, "tag", "-a", "v0.9.0", "-m", "old")
	commitFile(t, dir, "f.txt", "v2", "chore: v2")
	mustGit(t, dir, "tag", "-a", "v1.0.0", "-m", "new")

	tag, err := release.GitLatestVersionTag(dir)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tag != "v1.0.0" {
		t.Errorf("latest tag = %q, want %q", tag, "v1.0.0")
	}
}

// ─── GitCommitsSince / GitAllCommits ─────────────────────────────────────────

func TestGitCommitsSince(t *testing.T) {
	dir := initGitRepo(t)
	commitFile(t, dir, "a.txt", "a", "feat: first")
	mustGit(t, dir, "tag", "-a", "v0.1.0", "-m", "base")
	commitFile(t, dir, "b.txt", "b", "fix: second")
	commitFile(t, dir, "c.txt", "c", "docs: third")

	subjects, err := release.GitCommitsSince(dir, "v0.1.0")
	if err != nil {
		t.Fatalf("GitCommitsSince error: %v", err)
	}
	if len(subjects) != 2 {
		t.Fatalf("expected 2 commits since tag, got %d: %v", len(subjects), subjects)
	}
}

func TestGitAllCommits(t *testing.T) {
	dir := initGitRepo(t)
	commitFile(t, dir, "a.txt", "a", "feat: alpha")
	commitFile(t, dir, "b.txt", "b", "fix: beta")

	subjects, err := release.GitAllCommits(dir)
	if err != nil {
		t.Fatalf("GitAllCommits error: %v", err)
	}
	if len(subjects) < 2 {
		t.Errorf("expected at least 2 commits, got %d: %v", len(subjects), subjects)
	}
}
