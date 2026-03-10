package release_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/release"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// writeRegistry writes a PluginRegistry JSON to path, creating parent dirs.
func writeRegistry(t *testing.T, path string, r *release.PluginRegistry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makePluginDir creates a directory with a package.json at the given version.
func makePluginDir(t *testing.T, parent, name, version string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := map[string]interface{}{"name": name, "version": version}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ─── Load / Save ─────────────────────────────────────────────────────────────

func TestPluginRegistry_LoadSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")

	original := &release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{
				Path: "/some/marketplace.json",
				Plugins: []release.PluginEntry{
					{Name: "my-plugin", Path: "/some/plugin", Remote: "git@github.com:user/plugin.git"},
				},
			},
		},
	}
	writeRegistry(t, path, original)

	var loaded release.PluginRegistry
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loaded.Marketplaces) != 1 {
		t.Fatalf("expected 1 marketplace, got %d", len(loaded.Marketplaces))
	}
	if len(loaded.Marketplaces[0].Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(loaded.Marketplaces[0].Plugins))
	}
	p := loaded.Marketplaces[0].Plugins[0]
	if p.Name != "my-plugin" {
		t.Errorf("Name = %q, want %q", p.Name, "my-plugin")
	}

	// Round-trip: save and reload.
	path2 := filepath.Join(dir, "registry2.json")
	if err := loaded.Save(path2, false); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	var reloaded release.PluginRegistry
	if err := reloaded.Load(path2); err != nil {
		t.Fatalf("re-Load() error: %v", err)
	}
	if len(reloaded.Marketplaces) != 1 {
		t.Errorf("round-trip lost marketplaces")
	}
}

func TestPluginRegistry_LoadMissing(t *testing.T) {
	var r release.PluginRegistry
	if err := r.Load("/nonexistent/path/registry.json"); err != nil {
		t.Errorf("Load on missing file should return nil error, got: %v", err)
	}
	if len(r.Marketplaces) != 0 {
		t.Errorf("empty registry should have 0 marketplaces, got %d", len(r.Marketplaces))
	}
}

func TestPluginRegistry_SaveDryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	r := &release.PluginRegistry{Marketplaces: []release.MarketplaceEntry{}}
	if err := r.Save(path, true); err != nil {
		t.Fatalf("Save(dryRun=true) error: %v", err)
	}
	// File must NOT have been created.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("dry-run Save should not create file")
	}
}

// ─── FindPlugin ──────────────────────────────────────────────────────────────

func TestPluginRegistry_FindPlugin_ByName(t *testing.T) {
	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{
				Path: "/mp/marketplace.json",
				Plugins: []release.PluginEntry{
					{Name: "alpha", Path: "/alpha"},
					{Name: "beta", Path: "/beta"},
				},
			},
		},
	}
	pe, me := r.FindPlugin("alpha", "/cwd")
	if pe == nil {
		t.Fatal("FindPlugin(alpha) returned nil")
	}
	if pe.Name != "alpha" {
		t.Errorf("Name = %q, want %q", pe.Name, "alpha")
	}
	if me == nil {
		t.Fatal("FindPlugin returned nil marketplace")
	}
}

func TestPluginRegistry_FindPlugin_NotFound(t *testing.T) {
	r := release.PluginRegistry{}
	pe, me := r.FindPlugin("missing", "/cwd")
	if pe != nil || me != nil {
		t.Errorf("expected nil, nil for missing plugin")
	}
}

// ─── SyncVersions ────────────────────────────────────────────────────────────

func TestPluginRegistry_SyncVersions_NoDrift(t *testing.T) {
	base := t.TempDir()
	pluginDir := makePluginDir(t, base, "my-plugin", "1.0.0")

	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{Path: filepath.Join(base, "marketplace.json"), Plugins: []release.PluginEntry{
				{Name: "my-plugin", Path: pluginDir},
			}},
		},
	}
	drift, err := r.SyncVersions(true)
	if err != nil {
		t.Fatalf("SyncVersions error: %v", err)
	}
	if len(drift) != 0 {
		t.Errorf("expected no drift, got: %+v", drift)
	}
}

func TestPluginRegistry_SyncVersions_WithDrift(t *testing.T) {
	base := t.TempDir()
	pluginDir := filepath.Join(base, "my-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two version files with different versions.
	pyproj := "[project]\nversion = \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "pyproject.toml"), []byte(pyproj), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := `{"version":"2.0.0"}` + "\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{Path: filepath.Join(base, "marketplace.json"), Plugins: []release.PluginEntry{
				{Name: "my-plugin", Path: pluginDir},
			}},
		},
	}

	// Dry-run: detect drift without writing.
	drift, err := r.SyncVersions(true)
	if err != nil {
		t.Fatalf("SyncVersions(dryRun) error: %v", err)
	}
	if len(drift) != 1 {
		t.Fatalf("expected 1 drift entry, got %d: %+v", len(drift), drift)
	}
	if drift[0].Want != "1.0.0" || drift[0].Got != "2.0.0" {
		t.Errorf("unexpected drift: %+v", drift[0])
	}

	// Verify package.json still has 2.0.0 (dry-run should not modify).
	vf := release.NewJsonVersionFile("package.json", filepath.Join(pluginDir, "package.json"))
	v, _ := vf.Read()
	if v != "2.0.0" {
		t.Errorf("dry-run modified file; got %q", v)
	}

	// Live-run: should fix drift.
	drift, err = r.SyncVersions(false)
	if err != nil {
		t.Fatalf("SyncVersions(live) error: %v", err)
	}
	if len(drift) != 1 {
		t.Fatalf("expected 1 drift record, got %d", len(drift))
	}
	// Now read back: should be 1.0.0.
	v, _ = vf.Read()
	if v != "1.0.0" {
		t.Errorf("after sync, package.json = %q, want %q", v, "1.0.0")
	}
}

// ─── ReleaseOrder ─────────────────────────────────────────────────────────────

func TestPluginRegistry_ReleaseOrder(t *testing.T) {
	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{Plugins: []release.PluginEntry{
				{Name: "alpha"},
				{Name: "beta"},
			}},
			{Plugins: []release.PluginEntry{
				{Name: "gamma"},
				{Name: "alpha"}, // duplicate — should appear once
			}},
		},
	}
	order, err := r.ReleaseOrder()
	if err != nil {
		t.Fatalf("ReleaseOrder() error: %v", err)
	}
	// All unique names should appear.
	names := make(map[string]bool)
	for _, p := range order {
		names[p.Name] = true
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !names[want] {
			t.Errorf("missing %q in release order", want)
		}
	}
	// Length should match unique count.
	if len(order) != 3 {
		t.Errorf("expected 3 unique plugins, got %d", len(order))
	}
}

// ─── TopologicalSort ─────────────────────────────────────────────────────────

func TestTopologicalSort_Simple(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	// b depends on a, c depends on b.
	edges := map[string][]string{
		"b": {"a"},
		"c": {"b"},
	}
	order, err := release.TopologicalSort(nodes, edges)
	if err != nil {
		t.Fatalf("TopologicalSort error: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(order))
	}
	// a must come before b, b before c.
	pos := make(map[string]int)
	for i, n := range order {
		pos[n] = i
	}
	if pos["a"] > pos["b"] {
		t.Errorf("a should come before b")
	}
	if pos["b"] > pos["c"] {
		t.Errorf("b should come before c")
	}
}

func TestTopologicalSort_NoDeps(t *testing.T) {
	nodes := []string{"x", "y", "z"}
	order, err := release.TopologicalSort(nodes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(order))
	}
}

func TestTopologicalSort_CycleDetection(t *testing.T) {
	nodes := []string{"a", "b"}
	edges := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	_, err := release.TopologicalSort(nodes, edges)
	if err == nil {
		t.Error("expected cycle error, got nil")
	}
}

func TestTopologicalSort_DiamondDep(t *testing.T) {
	// a → b, a → c, b → d, c → d  (diamond)
	nodes := []string{"a", "b", "c", "d"}
	edges := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
	}
	order, err := release.TopologicalSort(nodes, edges)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pos := make(map[string]int)
	for i, n := range order {
		pos[n] = i
	}
	if pos["d"] > pos["b"] || pos["d"] > pos["c"] {
		t.Errorf("d should come before b and c; order=%v", order)
	}
	if pos["b"] > pos["a"] || pos["c"] > pos["a"] {
		t.Errorf("b and c should come before a; order=%v", order)
	}
}
