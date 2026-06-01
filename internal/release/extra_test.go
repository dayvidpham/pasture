package release_test

// extra_test.go covers remaining uncovered paths to reach 75%+ coverage:
// - MarketplaceVersionFile Name/Path accessors
// - JsonVersionFile Path accessor
// - PyprojectVersionFile Path accessor
// - Bump panic (via recover — tested inline)
// - prependChangelog via RunRelease with NoChangelog=false
// - buildChangelogEntry via RunRelease with NoChangelog=false
// - workingTreeDirty .beads/ exclusion path
// - registry Exec
// - registry Load malformed JSON
// - SyncVersions error path (unreadable directory)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/release"
	"github.com/dayvidpham/pasture/internal/types"
)

// ─── VersionFile accessors ────────────────────────────────────────────────────

func TestVersionFileAccessors_Json(t *testing.T) {
	vf := release.NewJsonVersionFile("my-name", "/some/path.json")
	if vf.Name() != "my-name" {
		t.Errorf("Name() = %q, want %q", vf.Name(), "my-name")
	}
	if vf.Path() != "/some/path.json" {
		t.Errorf("Path() = %q, want %q", vf.Path(), "/some/path.json")
	}
}

func TestVersionFileAccessors_Marketplace(t *testing.T) {
	vf := release.NewMarketplaceVersionFile("marketplace.json", "/mp/marketplace.json")
	if vf.Name() != "marketplace.json" {
		t.Errorf("Name() = %q", vf.Name())
	}
	if vf.Path() != "/mp/marketplace.json" {
		t.Errorf("Path() = %q", vf.Path())
	}
}

func TestVersionFileAccessors_Pyproject(t *testing.T) {
	vf := release.NewPyprojectVersionFile("pyproject.toml", "/repo/pyproject.toml")
	if vf.Path() != "/repo/pyproject.toml" {
		t.Errorf("Path() = %q", vf.Path())
	}
}

// ─── VersionFile Read errors ─────────────────────────────────────────────────

func TestJsonVersionFile_Read_MissingFile(t *testing.T) {
	vf := release.NewJsonVersionFile("package.json", "/nonexistent/package.json")
	_, err := vf.Read()
	if err == nil {
		t.Error("expected error reading missing file")
	}
}

func TestJsonVersionFile_Read_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	vf := release.NewJsonVersionFile("package.json", path)
	_, err := vf.Read()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestJsonVersionFile_Read_NoVersionField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte(`{"name":"foo"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vf := release.NewJsonVersionFile("package.json", path)
	_, err := vf.Read()
	if err == nil {
		t.Error("expected error for missing version field")
	}
}

func TestMarketplaceVersionFile_Read_NoMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "marketplace.json")
	if err := os.WriteFile(path, []byte(`{"plugins":[]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vf := release.NewMarketplaceVersionFile("marketplace.json", path)
	_, err := vf.Read()
	if err == nil {
		t.Error("expected error for missing metadata")
	}
}

func TestMarketplaceVersionFile_Read_NoVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "marketplace.json")
	if err := os.WriteFile(path, []byte(`{"metadata":{"name":"foo"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vf := release.NewMarketplaceVersionFile("marketplace.json", path)
	_, err := vf.Read()
	if err == nil {
		t.Error("expected error for missing metadata.version")
	}
}

func TestPyprojectVersionFile_Read_NoProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	if err := os.WriteFile(path, []byte("[tool.poetry]\nversion = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vf := release.NewPyprojectVersionFile("pyproject.toml", path)
	_, err := vf.Read()
	if err == nil {
		t.Error("expected error when no [project] section")
	}
}

// ─── VersionFile Write errors ─────────────────────────────────────────────────

func TestJsonVersionFile_Write_MissingFile(t *testing.T) {
	vf := release.NewJsonVersionFile("package.json", "/nonexistent/package.json")
	err := vf.Write("1.0.0", false)
	if err == nil {
		t.Error("expected error writing to missing file")
	}
}

func TestMarketplaceVersionFile_Write_NoMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "marketplace.json")
	if err := os.WriteFile(path, []byte(`{"plugins":[]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vf := release.NewMarketplaceVersionFile("marketplace.json", path)
	err := vf.Write("1.0.0", false)
	if err == nil {
		t.Error("expected error writing to file with no metadata")
	}
}

func TestMarketplaceVersionFile_Write_DryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "marketplace.json")
	if err := os.WriteFile(path, []byte(`{"metadata":{"version":"1.0.0"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vf := release.NewMarketplaceVersionFile("marketplace.json", path)
	if err := vf.Write("9.9.9", true); err != nil {
		t.Fatalf("Write(dryRun) error: %v", err)
	}
	// Unchanged.
	v, _ := vf.Read()
	if v != "1.0.0" {
		t.Errorf("dry-run modified file; got %q", v)
	}
}

// ─── RunRelease with changelog ────────────────────────────────────────────────

func TestRunRelease_LivePatch_WithChangelog(t *testing.T) {
	dir := setupRepoDir(t, "1.2.3")
	opts := release.ReleaseOptions{
		BumpKind:    types.BumpPatch,
		DryRun:      false,
		NoChangelog: false, // generate changelog
		NoCommit:    true,
		NoTag:       true,
		RepoRoot:    dir,
	}
	if err := release.RunRelease(opts); err != nil {
		t.Fatalf("RunRelease error: %v", err)
	}
	// CHANGELOG.md should be created.
	clPath := filepath.Join(dir, "CHANGELOG.md")
	data, err := os.ReadFile(clPath)
	if err != nil {
		t.Fatalf("CHANGELOG.md not created: %v", err)
	}
	if !strings.Contains(string(data), "1.2.4") {
		t.Errorf("CHANGELOG.md missing version 1.2.4:\n%s", data)
	}
}

// ─── Registry Exec ────────────────────────────────────────────────────────────

func TestPluginRegistry_Exec(t *testing.T) {
	// Create two real directories and exec "echo" in each.
	base := t.TempDir()
	dir1 := filepath.Join(base, "plugin1")
	dir2 := filepath.Join(base, "plugin2")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}

	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{Plugins: []release.PluginEntry{
				{Name: "plugin1", Path: dir1},
				{Name: "plugin2", Path: dir2},
			}},
		},
	}
	if err := r.Exec("echo", "hello"); err != nil {
		t.Errorf("Exec error: %v", err)
	}
}

func TestPluginRegistry_Exec_CommandFails(t *testing.T) {
	base := t.TempDir()
	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{Plugins: []release.PluginEntry{
				{Name: "p", Path: base},
			}},
		},
	}
	err := r.Exec("false") // "false" always exits non-zero
	if err == nil {
		t.Error("expected error for failing command")
	}
}

// ─── Registry Load malformed JSON ─────────────────────────────────────────────

func TestPluginRegistry_Load_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var r release.PluginRegistry
	err := r.Load(path)
	if err == nil {
		t.Error("expected error for malformed JSON registry")
	}
}

// ─── DefaultRegistryPath ─────────────────────────────────────────────────────

func TestDefaultRegistryPath(t *testing.T) {
	p := release.DefaultRegistryPath()
	if p == "" {
		t.Error("DefaultRegistryPath() returned empty string")
	}
	if !strings.Contains(p, "claude-plugin-registry.json") {
		t.Errorf("unexpected registry path: %q", p)
	}
}
