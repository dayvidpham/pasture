package release_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/release"
)

// ─── fixtures ─────────────────────────────────────────────────────────────────

// writePluginJSON writes <dir>/.claude-plugin/plugin.json with the given version.
func writePluginJSON(t *testing.T, dir, name, version string) {
	t.Helper()
	cpDir := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(cpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	obj := map[string]interface{}{"name": name, "version": version}
	data, _ := json.MarshalIndent(obj, "", "  ")
	if err := os.WriteFile(filepath.Join(cpDir, "plugin.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeMarketplace writes a marketplace.json carrying metadata.version plus a
// single plugins[] entry at entryVersion. Returns the marketplace.json path.
func writeMarketplace(t *testing.T, dir, metaVersion, pluginName, entryVersion string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	obj := map[string]interface{}{
		"name":     "test-marketplace",
		"metadata": map[string]interface{}{"version": metaVersion},
		"plugins": []interface{}{
			map[string]interface{}{
				"name":    pluginName,
				"version": entryVersion,
				"source":  "./" + pluginName,
			},
		},
	}
	data, _ := json.MarshalIndent(obj, "", "  ")
	path := filepath.Join(dir, "marketplace.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readEntryVersion reads plugins[name].version from a marketplace.json.
func readEntryVersion(t *testing.T, path, name string) string {
	t.Helper()
	v, err := release.ReadPluginVersion(path, name)
	if err != nil {
		t.Fatalf("ReadPluginVersion(%s, %s): %v", path, name, err)
	}
	return v
}

// readMetaVersion reads metadata.version from a marketplace.json.
func readMetaVersion(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
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

func sha256File(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

// newReconcileRegistry builds a registry with one marketplace + one plugin and
// a stubbed gitPull recording the dirs it was called with.
func newReconcileRegistry(marketplacePath, pluginName, pluginDir string, pulled *[]string) release.PluginRegistry {
	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{
				Path: marketplacePath,
				Plugins: []release.PluginEntry{
					{Name: pluginName, Path: pluginDir},
				},
			},
		},
	}
	r.WithGitPull(func(dir string) error {
		*pulled = append(*pulled, dir)
		return nil
	})
	return r
}

// ─── V-a: plugin newer → write marketplace entry ──────────────────────────────

func TestSyncVersions_WriteMarketplace_PluginNewer(t *testing.T) {
	base := t.TempDir()
	pluginDir := filepath.Join(base, "pasture")
	writePluginJSON(t, pluginDir, "pasture", "0.0.2")
	mpDir := filepath.Join(base, "marketplace")
	mpPath := writeMarketplace(t, mpDir, "9.9.9", "pasture", "0.0.1")

	var pulled []string
	r := newReconcileRegistry(mpPath, "pasture", pluginDir, &pulled)

	// Plan.
	plan, err := r.SyncVersions(true)
	if err != nil {
		t.Fatalf("SyncVersions(dry-run): %v", err)
	}
	if len(plan) != 1 {
		t.Fatalf("expected 1 pending change, got %d: %+v", len(plan), plan)
	}
	d := plan[0]
	if d.Action != release.DriftWriteMarketplace {
		t.Errorf("Action = %v, want DriftWriteMarketplace", d.Action)
	}
	if d.PluginVersion != "0.0.2" || d.MarketplaceVersion != "0.0.1" {
		t.Errorf("pv/mv = %q/%q, want 0.0.2/0.0.1", d.PluginVersion, d.MarketplaceVersion)
	}

	// Apply.
	if _, err := r.SyncVersions(false); err != nil {
		t.Fatalf("SyncVersions(apply): %v", err)
	}
	if got := readEntryVersion(t, mpPath, "pasture"); got != "0.0.2" {
		t.Errorf("after apply, marketplace entry = %q, want 0.0.2", got)
	}
	if got := readMetaVersion(t, mpPath); got != "9.9.9" {
		t.Errorf("metadata.version changed to %q, must stay 9.9.9", got)
	}
	if len(pulled) != 0 {
		t.Errorf("gitPull must not run for a write-marketplace action; pulled=%v", pulled)
	}
}

// ─── V-a2: marketplace newer → pull plugin repo ───────────────────────────────

func TestSyncVersions_PullPlugin_MarketplaceNewer(t *testing.T) {
	base := t.TempDir()
	pluginDir := filepath.Join(base, "pasture")
	writePluginJSON(t, pluginDir, "pasture", "0.0.2")
	mpDir := filepath.Join(base, "marketplace")
	mpPath := writeMarketplace(t, mpDir, "9.9.9", "pasture", "0.0.3")

	// Dry-run: detects pull but must NOT invoke gitPull.
	var pulledDry []string
	rDry := newReconcileRegistry(mpPath, "pasture", pluginDir, &pulledDry)
	plan, err := rDry.SyncVersions(true)
	if err != nil {
		t.Fatalf("SyncVersions(dry-run): %v", err)
	}
	if len(plan) != 1 || plan[0].Action != release.DriftPullPlugin {
		t.Fatalf("expected 1 DriftPullPlugin, got %+v", plan)
	}
	if len(pulledDry) != 0 {
		t.Errorf("gitPull must NOT run on dry-run; pulled=%v", pulledDry)
	}

	// Apply: gitPull called once with the plugin dir; warns still-behind because
	// the stub does not actually advance plugin.json.
	var pulled []string
	var warn bytes.Buffer
	r := newReconcileRegistry(mpPath, "pasture", pluginDir, &pulled)
	r.WithOutput(&warn)
	if _, err := r.SyncVersions(false); err != nil {
		t.Fatalf("SyncVersions(apply): %v", err)
	}
	if len(pulled) != 1 || pulled[0] != pluginDir {
		t.Fatalf("gitPull should be called once with %q; got %v", pluginDir, pulled)
	}
	if !strings.Contains(warn.String(), "still behind") {
		t.Errorf("expected still-behind warning, got: %q", warn.String())
	}
	// Marketplace entry untouched on a pull (we update the local checkout, not it).
	if got := readEntryVersion(t, mpPath, "pasture"); got != "0.0.3" {
		t.Errorf("marketplace entry changed to %q on a pull; want 0.0.3", got)
	}
}

// ─── V-b: dry-run is byte-identical and pulls nothing ─────────────────────────

func TestSyncVersions_DryRun_ByteIdentical(t *testing.T) {
	base := t.TempDir()
	pluginDir := filepath.Join(base, "pasture")
	writePluginJSON(t, pluginDir, "pasture", "0.0.2")
	mpDir := filepath.Join(base, "marketplace")
	mpPath := writeMarketplace(t, mpDir, "9.9.9", "pasture", "0.0.1")

	before := sha256File(t, mpPath)

	var pulled []string
	r := newReconcileRegistry(mpPath, "pasture", pluginDir, &pulled)
	if _, err := r.SyncVersions(true); err != nil {
		t.Fatalf("SyncVersions(dry-run): %v", err)
	}

	after := sha256File(t, mpPath)
	if before != after {
		t.Errorf("dry-run modified marketplace.json (SHA changed)")
	}
	if len(pulled) != 0 {
		t.Errorf("dry-run must not pull; pulled=%v", pulled)
	}
}

// ─── V-c: idempotent + metadata.version constant ──────────────────────────────

func TestSyncVersions_Idempotent_MetadataConstant(t *testing.T) {
	base := t.TempDir()
	pluginDir := filepath.Join(base, "pasture")
	writePluginJSON(t, pluginDir, "pasture", "0.0.2")
	mpDir := filepath.Join(base, "marketplace")
	mpPath := writeMarketplace(t, mpDir, "9.9.9", "pasture", "0.0.1")

	var pulled []string
	r := newReconcileRegistry(mpPath, "pasture", pluginDir, &pulled)

	// First apply fixes the entry.
	if _, err := r.SyncVersions(false); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// Second run is a no-op.
	plan, err := r.SyncVersions(false)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(plan) != 0 {
		t.Errorf("second run should be a no-op, got %d change(s): %+v", len(plan), plan)
	}
	if got := readMetaVersion(t, mpPath); got != "9.9.9" {
		t.Errorf("metadata.version = %q, must stay 9.9.9", got)
	}
}

// ─── V-d: no false positive when equal ────────────────────────────────────────

func TestSyncVersions_NoFalsePositive_WhenEqual(t *testing.T) {
	base := t.TempDir()
	pluginDir := filepath.Join(base, "pasture")
	writePluginJSON(t, pluginDir, "pasture", "0.0.2")
	mpDir := filepath.Join(base, "marketplace")
	mpPath := writeMarketplace(t, mpDir, "9.9.9", "pasture", "0.0.2")

	var pulled []string
	r := newReconcileRegistry(mpPath, "pasture", pluginDir, &pulled)
	plan, err := r.SyncVersions(true)
	if err != nil {
		t.Fatalf("SyncVersions: %v", err)
	}
	if len(plan) != 0 {
		t.Errorf("equal versions should yield no drift; got %+v", plan)
	}
	if len(pulled) != 0 {
		t.Errorf("equal versions should not pull; pulled=%v", pulled)
	}
}

// ─── ff-only failure: error names plugin + abs repo path ──────────────────────

func TestSyncVersions_FfOnlyFailure_IncludesRepoPath(t *testing.T) {
	base := t.TempDir()
	pluginDir := filepath.Join(base, "pasture")
	writePluginJSON(t, pluginDir, "pasture", "0.0.2")
	mpDir := filepath.Join(base, "marketplace")
	mpPath := writeMarketplace(t, mpDir, "9.9.9", "pasture", "0.0.3") // marketplace ahead → pull

	r := release.PluginRegistry{
		Marketplaces: []release.MarketplaceEntry{
			{Path: mpPath, Plugins: []release.PluginEntry{{Name: "pasture", Path: pluginDir}}},
		},
	}
	r.WithGitPull(func(dir string) error {
		return os.ErrPermission // simulate a non-fast-forwardable / dirty tree
	})

	_, err := r.SyncVersions(false)
	if err == nil {
		t.Fatal("expected an error when ff-only pull fails")
	}
	absRepo, _ := filepath.Abs(pluginDir)
	if !strings.Contains(err.Error(), "pasture") {
		t.Errorf("error should name the plugin; got: %v", err)
	}
	if !strings.Contains(err.Error(), absRepo) {
		t.Errorf("error should include the abs repo path %q; got: %v", absRepo, err)
	}
	if !strings.Contains(err.Error(), "git -C "+absRepo+" status") {
		t.Errorf("error should suggest 'git -C <path> status'; got: %v", err)
	}
}

// ─── ReadPluginVersion: absent name is an actionable error ─────────────────────

func TestReadPluginVersion_AbsentName(t *testing.T) {
	base := t.TempDir()
	mpPath := writeMarketplace(t, base, "9.9.9", "pasture", "0.0.1")

	_, err := release.ReadPluginVersion(mpPath, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for absent plugin name")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does-not-exist") {
		t.Errorf("error should name the missing plugin; got: %v", err)
	}
	if !strings.Contains(msg, "pasture") {
		t.Errorf("error should list available entries (pasture); got: %v", err)
	}
}

func TestReadPluginVersion_Found(t *testing.T) {
	base := t.TempDir()
	mpPath := writeMarketplace(t, base, "9.9.9", "pasture", "1.2.3")
	got, err := release.ReadPluginVersion(mpPath, "pasture")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("got %q, want 1.2.3", got)
	}
}

// ─── CompareVersions: ordering + non-semver error ─────────────────────────────

func TestCompareVersions_Ordering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.0.1", "0.0.2", -1},
		{"0.0.2", "0.0.1", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.2.0", "1.10.0", -1},
		{"2.0.0", "1.9.9", 1},
	}
	for _, c := range cases {
		got, err := release.CompareVersions(c.a, c.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q,%q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareVersions_NonSemverError(t *testing.T) {
	if _, err := release.CompareVersions("not-a-version", "1.0.0"); err == nil {
		t.Error("expected error for invalid left-hand version")
	}
	if _, err := release.CompareVersions("1.0.0", "v2"); err == nil {
		t.Error("expected error for invalid right-hand version")
	}
}
