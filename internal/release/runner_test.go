package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/release"
	"github.com/dayvidpham/pasture/internal/types"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// setupRepoDir creates a minimal git repo with a package.json at version.
func setupRepoDir(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()

	pkg := `{"name":"test","version":"` + version + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@test.com")
	mustGit(t, dir, "config", "user.name", "Test")
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "chore: init")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// ─── RunRelease — dry-run ─────────────────────────────────────────────────────

func TestRunRelease_DryRun_NoFiles(t *testing.T) {
	dir := t.TempDir()
	opts := release.ReleaseOptions{
		BumpKind:    types.BumpPatch,
		DryRun:      true,
		NoChangelog: true,
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	err := release.RunRelease(opts)
	if err == nil {
		t.Error("expected error for no version files, got nil")
	}
	if !strings.Contains(err.Error(), "no version files") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunRelease_DryRun_PatchBump(t *testing.T) {
	dir := setupRepoDir(t, "1.0.0")
	opts := release.ReleaseOptions{
		BumpKind:    types.BumpPatch,
		DryRun:      true,
		NoChangelog: true,
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	if err := release.RunRelease(opts); err != nil {
		t.Errorf("RunRelease(dry-run patch) error: %v", err)
	}
	// File must be unchanged in dry-run.
	vf := release.NewJsonVersionFile("package.json", filepath.Join(dir, "package.json"))
	v, _ := vf.Read()
	if v != "1.0.0" {
		t.Errorf("dry-run modified version to %q", v)
	}
}

func TestRunRelease_DryRun_MinorBump(t *testing.T) {
	dir := setupRepoDir(t, "2.3.4")
	opts := release.ReleaseOptions{
		BumpKind:    types.BumpMinor,
		DryRun:      true,
		NoChangelog: true,
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	if err := release.RunRelease(opts); err != nil {
		t.Errorf("RunRelease(dry-run minor) error: %v", err)
	}
}

func TestRunRelease_InvalidBumpKind(t *testing.T) {
	dir := setupRepoDir(t, "1.0.0")
	opts := release.ReleaseOptions{
		BumpKind:    types.BumpKind("invalid"),
		DryRun:      true,
		NoChangelog: true,
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	err := release.RunRelease(opts)
	if err == nil {
		t.Error("expected error for invalid bump kind")
	}
	if !strings.Contains(err.Error(), "bump kind") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunRelease_VersionDrift_NoSync(t *testing.T) {
	dir := t.TempDir()
	pyproj := "[project]\nversion = \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproj), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := `{"version":"2.0.0"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := release.ReleaseOptions{
		BumpKind:    types.BumpPatch,
		DryRun:      true,
		NoChangelog: true,
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	err := release.RunRelease(opts)
	if err == nil {
		t.Error("expected drift error, got nil")
	}
	if !strings.Contains(err.Error(), "drift") {
		t.Errorf("expected drift error, got: %v", err)
	}
}

func TestRunRelease_VersionDrift_WithSync(t *testing.T) {
	dir := t.TempDir()
	pyproj := "[project]\nversion = \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproj), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := `{"version":"2.0.0"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := release.ReleaseOptions{
		BumpKind:    types.BumpPatch,
		DryRun:      true,
		Sync:        true,
		NoChangelog: true,
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	if err := release.RunRelease(opts); err != nil {
		t.Errorf("RunRelease(sync dry-run) error: %v", err)
	}
}

// TestRunRelease_LivePatch performs a real patch bump in a temp git repo.
func TestRunRelease_LivePatch(t *testing.T) {
	dir := setupRepoDir(t, "0.1.0")
	opts := release.ReleaseOptions{
		BumpKind:    types.BumpPatch,
		DryRun:      false,
		NoChangelog: true,
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	if err := release.RunRelease(opts); err != nil {
		t.Fatalf("RunRelease(live patch) error: %v", err)
	}
	vf := release.NewJsonVersionFile("package.json", filepath.Join(dir, "package.json"))
	v, err := vf.Read()
	if err != nil {
		t.Fatal(err)
	}
	if v != "0.1.1" {
		t.Errorf("after patch bump, version = %q, want %q", v, "0.1.1")
	}
}
