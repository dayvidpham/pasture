package release_test

import (
	"encoding/json"
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

// ─── --plugin cross-repo marketplace sync (L3 integration) ────────────────────

// writeParentMarketplace writes a populated parent marketplace.json (with a
// plugins[] array) to a temp dir OUTSIDE the release repo and returns its path.
func writeParentMarketplace(t *testing.T, metaVersion, pluginName, pluginVersion string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "marketplace.json")
	content := `{
  "metadata": {"version": "` + metaVersion + `"},
  "plugins": [
    {"name": "other", "source": "./other", "version": "5.5.5"},
    {"name": "` + pluginName + `", "source": "./` + pluginName + `", "version": "` + pluginVersion + `"}
  ]
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeReleaseRegistry writes a registry JSON that maps pluginName →
// marketplacePath (and the plugin's own repo dir) to a temp file and returns
// its path.
func writeReleaseRegistry(t *testing.T, marketplacePath, pluginName, pluginDir string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{
				Path: marketplacePath,
				Plugins: []release.PluginEntry{
					{Name: pluginName, Path: pluginDir, Remote: "https://example.com/" + pluginName},
				},
			},
		},
	}
	if err := r.Save(path, false); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return path
}

func readParentPluginVersion(t *testing.T, marketplacePath, pluginName string) string {
	t.Helper()
	data, err := os.ReadFile(marketplacePath)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	plugins, _ := obj["plugins"].([]interface{})
	for _, p := range plugins {
		entry, _ := p.(map[string]interface{})
		if n, _ := entry["name"].(string); n == pluginName {
			v, _ := entry["version"].(string)
			return v
		}
	}
	t.Fatalf("plugin %q not found in %s", pluginName, marketplacePath)
	return ""
}

func readParentMetaVersion(t *testing.T, marketplacePath string) string {
	t.Helper()
	data, err := os.ReadFile(marketplacePath)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	meta, _ := obj["metadata"].(map[string]interface{})
	v, _ := meta["version"].(string)
	return v
}

// TestRunRelease_Plugin_SyncsCrossRepoMarketplace bumps a plugin's repo and
// asserts the registered PARENT marketplace's plugins[name].version is synced
// while its metadata.version is preserved. The marketplace lives OUTSIDE the
// release repo (cross-repo), resolved purely through the registry path.
func TestRunRelease_Plugin_SyncsCrossRepoMarketplace(t *testing.T) {
	repo := setupRepoDir(t, "0.0.1")
	// Parent marketplace at metadata.version 7.0.0; plugin "pasture" at 0.0.1.
	marketplace := writeParentMarketplace(t, "7.0.0", "pasture", "0.0.1")
	registry := writeReleaseRegistry(t, marketplace, "pasture", repo)

	opts := release.ReleaseOptions{
		BumpKind:     types.BumpPatch,
		DryRun:       false,
		NoChangelog:  true,
		NoCommit:     true,
		NoTag:        true,
		RepoRoot:     repo,
		Plugin:       "pasture",
		RegistryPath: registry,
	}
	if err := release.RunRelease(opts); err != nil {
		t.Fatalf("RunRelease(--plugin) error: %v", err)
	}

	// The in-repo file was bumped 0.0.1 -> 0.0.2.
	vf := release.NewJsonVersionFile("package.json", filepath.Join(repo, "package.json"))
	if v, _ := vf.Read(); v != "0.0.2" {
		t.Errorf("in-repo package.json = %q, want %q", v, "0.0.2")
	}
	// The PARENT marketplace plugins[pasture].version was synced to 0.0.2.
	if v := readParentPluginVersion(t, marketplace, "pasture"); v != "0.0.2" {
		t.Errorf("parent plugins[pasture].version = %q, want %q", v, "0.0.2")
	}
	// metadata.version PRESERVED — the core cross-repo invariant.
	if v := readParentMetaVersion(t, marketplace); v != "7.0.0" {
		t.Errorf("parent metadata.version = %q, want %q (must be preserved)", v, "7.0.0")
	}
	// The OTHER plugin in the parent marketplace is untouched.
	if v := readParentPluginVersion(t, marketplace, "other"); v != "5.5.5" {
		t.Errorf("parent plugins[other].version = %q, want %q (untouched)", v, "5.5.5")
	}
}

// TestRunRelease_Plugin_DryRun_WritesNothing verifies --plugin honors --dry-run.
func TestRunRelease_Plugin_DryRun_WritesNothing(t *testing.T) {
	repo := setupRepoDir(t, "0.0.1")
	marketplace := writeParentMarketplace(t, "7.0.0", "pasture", "0.0.1")
	registry := writeReleaseRegistry(t, marketplace, "pasture", repo)

	opts := release.ReleaseOptions{
		BumpKind:     types.BumpPatch,
		DryRun:       true,
		NoChangelog:  true,
		NoCommit:     true,
		NoTag:        true,
		RepoRoot:     repo,
		Plugin:       "pasture",
		RegistryPath: registry,
	}
	if err := release.RunRelease(opts); err != nil {
		t.Fatalf("RunRelease(--plugin dry-run) error: %v", err)
	}
	if v := readParentPluginVersion(t, marketplace, "pasture"); v != "0.0.1" {
		t.Errorf("dry-run modified parent plugins[pasture].version to %q, want %q", v, "0.0.1")
	}
}

// TestRunRelease_Plugin_DoubleBumpGuard registers a marketplace whose path is
// the SAME .claude-plugin/marketplace.json that lives inside the release repo
// and is therefore discovered + bumped by the normal flow. The post-commit
// per-plugin write must be SKIPPED (double-bump guard) so the file is not
// rewritten with a plugins[].version that fights the in-repo metadata bump.
func TestRunRelease_Plugin_DoubleBumpGuard(t *testing.T) {
	repo := t.TempDir()
	// In-repo marketplace.json discovered by the normal flow (metadata.version).
	pluginDir := filepath.Join(repo, ".claude-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inRepoMarketplace := filepath.Join(pluginDir, "marketplace.json")
	content := `{
  "metadata": {"version": "1.0.0"},
  "plugins": [
    {"name": "pasture", "source": "./pasture", "version": "1.0.0"}
  ]
}
`
	if err := os.WriteFile(inRepoMarketplace, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-b", "main")
	mustGit(t, repo, "config", "user.email", "test@test.com")
	mustGit(t, repo, "config", "user.name", "Test")
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-m", "chore: init")

	// Registry points the plugin at the SAME in-repo marketplace.json.
	registry := writeReleaseRegistry(t, inRepoMarketplace, "pasture", repo)

	opts := release.ReleaseOptions{
		BumpKind:     types.BumpPatch,
		DryRun:       false,
		NoChangelog:  true,
		NoCommit:     true,
		NoTag:        true,
		RepoRoot:     repo,
		Plugin:       "pasture",
		RegistryPath: registry,
	}
	if err := release.RunRelease(opts); err != nil {
		t.Fatalf("RunRelease(double-bump) error: %v", err)
	}

	// The normal flow bumps metadata.version 1.0.0 -> 1.0.1.
	if v := readParentMetaVersion(t, inRepoMarketplace); v != "1.0.1" {
		t.Errorf("in-repo metadata.version = %q, want %q (bumped by normal flow)", v, "1.0.1")
	}
	// The guard SKIPPED the per-plugin write, so plugins[pasture].version is
	// still its original 1.0.0 (the metadata bump is the authoritative change).
	if v := readParentPluginVersion(t, inRepoMarketplace, "pasture"); v != "1.0.0" {
		t.Errorf("plugins[pasture].version = %q, want %q (double-bump guard should skip per-plugin write)", v, "1.0.0")
	}
}

// TestRunRelease_Plugin_UnknownPlugin_ActionableError asserts a missing
// registry entry produces an actionable error and does not touch the parent.
func TestRunRelease_Plugin_UnknownPlugin_ActionableError(t *testing.T) {
	repo := setupRepoDir(t, "0.0.1")
	marketplace := writeParentMarketplace(t, "7.0.0", "pasture", "0.0.1")
	// Registry knows "pasture" but we ask for "ghost".
	registry := writeReleaseRegistry(t, marketplace, "pasture", repo)

	opts := release.ReleaseOptions{
		BumpKind:     types.BumpPatch,
		DryRun:       false,
		NoChangelog:  true,
		NoCommit:     true,
		NoTag:        true,
		RepoRoot:     repo,
		Plugin:       "ghost",
		RegistryPath: registry,
	}
	err := release.RunRelease(opts)
	if err == nil {
		t.Fatal("expected error for unknown --plugin, got nil")
	}
	for _, want := range []string{"ghost", "registry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q — not actionable: %v", want, err)
		}
	}
}

// ─── detached-HEAD pre-flight guard (L3 parity) ───────────────────────────────

// TestRunRelease_DetachedHead_Blocks asserts the pre-flight guard refuses to
// release from a detached HEAD, even in dry-run, with an actionable message.
func TestRunRelease_DetachedHead_Blocks(t *testing.T) {
	dir := setupRepoDir(t, "1.0.0")
	// Detach HEAD onto the current commit.
	mustGit(t, dir, "checkout", "--detach", "HEAD")

	opts := release.ReleaseOptions{
		BumpKind:    types.BumpPatch,
		DryRun:      true, // even dry-run must surface the blocker
		NoChangelog: true,
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	err := release.RunRelease(opts)
	if err == nil {
		t.Fatal("expected detached-HEAD error, got nil")
	}
	for _, want := range []string{"detached HEAD", "branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q — not actionable: %v", want, err)
		}
	}
}

// TestRunRelease_OnBranch_NotBlocked is the negative control: a normal branch
// repo passes the detached-HEAD guard (the existing dry-run bump tests already
// exercise this implicitly, but this makes the guard's pass-path explicit).
func TestRunRelease_OnBranch_NotBlocked(t *testing.T) {
	dir := setupRepoDir(t, "1.0.0")
	detached, err := release.GitIsDetachedHead(dir)
	if err != nil {
		t.Fatalf("GitIsDetachedHead error: %v", err)
	}
	if detached {
		t.Error("expected on-branch repo to NOT be detached")
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
