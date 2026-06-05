package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── fixtures ─────────────────────────────────────────────────────────────────

// setupReconcileFixture creates a plugin dir (.claude-plugin/plugin.json at
// pluginVer), a marketplace.json (entry at entryVer), and a registry.json
// wiring them together. It returns the registry path and the marketplace path.
func setupReconcileFixture(t *testing.T, pluginVer, entryVer string) (registryPath, mpPath string) {
	t.Helper()
	base := t.TempDir()

	pluginDir := filepath.Join(base, "pasture")
	cpDir := filepath.Join(pluginDir, ".claude-plugin")
	if err := os.MkdirAll(cpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pj, _ := json.MarshalIndent(map[string]interface{}{"name": "pasture", "version": pluginVer}, "", "  ")
	if err := os.WriteFile(filepath.Join(cpDir, "plugin.json"), append(pj, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	mpDir := filepath.Join(base, "marketplace")
	if err := os.MkdirAll(mpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mp, _ := json.MarshalIndent(map[string]interface{}{
		"name":     "test-marketplace",
		"metadata": map[string]interface{}{"version": "9.9.9"},
		"plugins": []interface{}{
			map[string]interface{}{"name": "pasture", "version": entryVer, "source": "./pasture"},
		},
	}, "", "  ")
	mpPath = filepath.Join(mpDir, "marketplace.json")
	if err := os.WriteFile(mpPath, append(mp, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, _ := json.MarshalIndent(map[string]interface{}{
		"marketplaces": []interface{}{
			map[string]interface{}{
				"path": mpPath,
				"plugins": []interface{}{
					map[string]interface{}{"name": "pasture", "path": pluginDir},
				},
			},
		},
	}, "", "  ")
	registryPath = filepath.Join(base, "registry.json")
	if err := os.WriteFile(registryPath, append(reg, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return registryPath, mpPath
}

func entryVersion(t *testing.T, mpPath string) string {
	t.Helper()
	data, err := os.ReadFile(mpPath)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	for _, raw := range obj["plugins"].([]interface{}) {
		e := raw.(map[string]interface{})
		if e["name"] == "pasture" {
			return e["version"].(string)
		}
	}
	t.Fatalf("no pasture entry in %s", mpPath)
	return ""
}

// runSyncVersions executes `registry sync-versions` in-process with the given
// extra args, stdin, and forced-TTY state. Returns combined output and error.
func runSyncVersions(t *testing.T, registryPath string, stdin string, tty bool, extraArgs ...string) (string, error) {
	t.Helper()
	prev := stdinIsTTY
	stdinIsTTY = func(io.Reader) bool { return tty }
	t.Cleanup(func() { stdinIsTTY = prev })

	root := newRootCmd()
	args := append([]string{"registry", "sync-versions", "--registry", registryPath}, extraArgs...)
	root.SetArgs(args)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))

	err := root.Execute()
	return out.String(), err
}

// setupIntraPluginDriftFixture creates a plugin dir with two disagreeing
// version files (pyproject.toml canonical vs package.json drifted) and NO
// .claude-plugin/plugin.json, so the marketplace reconciliation is skipped and
// the only pending change is an intra-plugin DriftWriteFile. Returns the
// registry path and the plugin name.
func setupIntraPluginDriftFixture(t *testing.T, canonicalVer, driftedVer string) (registryPath, pluginName string) {
	t.Helper()
	base := t.TempDir()
	pluginName = "intra-plugin"
	pluginDir := filepath.Join(base, pluginName)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pyproj := "[project]\nversion = \"" + canonicalVer + "\"\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "pyproject.toml"), []byte(pyproj), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, _ := json.MarshalIndent(map[string]interface{}{"name": pluginName, "version": driftedVer}, "", "  ")
	if err := os.WriteFile(filepath.Join(pluginDir, "package.json"), append(pkg, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, _ := json.MarshalIndent(map[string]interface{}{
		"marketplaces": []interface{}{
			map[string]interface{}{
				// marketplace path need not exist: no plugin.json → reconciliation skipped.
				"path": filepath.Join(base, "marketplace", "marketplace.json"),
				"plugins": []interface{}{
					map[string]interface{}{"name": pluginName, "path": pluginDir},
				},
			},
		},
	}, "", "  ")
	registryPath = filepath.Join(base, "registry.json")
	if err := os.WriteFile(registryPath, append(reg, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return registryPath, pluginName
}

// ─── DriftWriteFile renders inside the unified preview (B-MIN aura-plugins-pwmhr) ─

func TestCLISyncVersions_OutputFormat_WriteFile(t *testing.T) {
	reg, name := setupIntraPluginDriftFixture(t, "1.0.0", "2.0.0")
	out, err := runSyncVersions(t, reg, "", false, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"Reconciling registered plugins (plugin.json  ⟷  marketplace entry):",
		// canonical intra-plugin file-fix line: <plugin>  <file>  <got>  →  <want>
		name + "  package.json  2.0.0  →  1.0.0   (sync intra-plugin version file)",
		"1 change(s) pending  ·  dry-run: nothing written, no repos pulled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q\nfull output:\n%s", want, out)
		}
	}
}

// ─── output-format ─────────────────────────────────────────────────────────────

func TestCLISyncVersions_OutputFormat_WriteMarketplace(t *testing.T) {
	reg, _ := setupReconcileFixture(t, "0.0.2", "0.0.1")
	out, err := runSyncVersions(t, reg, "", false, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"Reconciling registered plugins (plugin.json  ⟷  marketplace entry):",
		"plugin.json 0.0.2  >  marketplace 0.0.1",
		"→ UPDATE marketplace entry → 0.0.2",
		"1 change(s) pending  ·  dry-run: nothing written, no repos pulled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestCLISyncVersions_OutputFormat_PullPlugin(t *testing.T) {
	reg, _ := setupReconcileFixture(t, "0.0.2", "0.0.3")
	out, err := runSyncVersions(t, reg, "", false, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"plugin.json 0.0.2  <  marketplace 0.0.3",
		"← GIT PULL plugin repo (local behind released 0.0.3)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// ─── V-g: --dry-run skips interaction and writes nothing ───────────────────────

func TestCLISyncVersions_DryRun_SkipsInteraction(t *testing.T) {
	reg, mp := setupReconcileFixture(t, "0.0.2", "0.0.1")
	// stdin "n" would abort if it were read — dry-run must not read it.
	out, err := runSyncVersions(t, reg, "n\n", true, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run error: %v", err)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("dry-run must not prompt; output:\n%s", out)
	}
	if got := entryVersion(t, mp); got != "0.0.1" {
		t.Errorf("dry-run wrote marketplace entry (now %q); must stay 0.0.1", got)
	}
}

// ─── V-e: interactive y applies, n aborts ─────────────────────────────────────

func TestCLISyncVersions_Interactive_YesApplies(t *testing.T) {
	reg, mp := setupReconcileFixture(t, "0.0.2", "0.0.1")
	out, err := runSyncVersions(t, reg, "y\n", true)
	if err != nil {
		t.Fatalf("apply error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "[y/N]") {
		t.Errorf("expected an interactive prompt; output:\n%s", out)
	}
	if got := entryVersion(t, mp); got != "0.0.2" {
		t.Errorf("after 'y', marketplace entry = %q, want 0.0.2", got)
	}
}

func TestCLISyncVersions_Interactive_NoAborts(t *testing.T) {
	reg, mp := setupReconcileFixture(t, "0.0.2", "0.0.1")
	out, err := runSyncVersions(t, reg, "n\n", true)
	if err != nil {
		t.Fatalf("abort path should not error: %v", err)
	}
	if !strings.Contains(out, "Aborted") {
		t.Errorf("expected abort message; output:\n%s", out)
	}
	if got := entryVersion(t, mp); got != "0.0.1" {
		t.Errorf("after 'n', marketplace entry = %q; must stay 0.0.1", got)
	}
}

// ─── V-f: --non-interactive applies without prompting ─────────────────────────

func TestCLISyncVersions_NonInteractive_Applies(t *testing.T) {
	reg, mp := setupReconcileFixture(t, "0.0.2", "0.0.1")
	// tty=false and no stdin: would error in interactive mode, but
	// --non-interactive must apply regardless.
	out, err := runSyncVersions(t, reg, "", false, "--non-interactive")
	if err != nil {
		t.Fatalf("non-interactive error: %v\noutput:\n%s", err, out)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("--non-interactive must not prompt; output:\n%s", out)
	}
	if got := entryVersion(t, mp); got != "0.0.2" {
		t.Errorf("after --non-interactive, entry = %q, want 0.0.2", got)
	}
}

// ─── non-TTY without flags is an actionable error ─────────────────────────────

func TestCLISyncVersions_NonTTY_RequiresFlag(t *testing.T) {
	reg, mp := setupReconcileFixture(t, "0.0.2", "0.0.1")
	out, err := runSyncVersions(t, reg, "", false) // changes pending, no flags, non-TTY
	if err == nil {
		t.Fatalf("expected an error on a non-interactive terminal; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--non-interactive") || !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("error should suggest --non-interactive / --dry-run; got: %v", err)
	}
	if got := entryVersion(t, mp); got != "0.0.1" {
		t.Errorf("non-TTY error path must not write; entry = %q", got)
	}
}

// ─── all-consistent prints the short message ──────────────────────────────────

func TestCLISyncVersions_AllConsistent(t *testing.T) {
	reg, _ := setupReconcileFixture(t, "0.0.2", "0.0.2")
	out, err := runSyncVersions(t, reg, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "All plugins are version-consistent.") {
		t.Errorf("expected consistent message; output:\n%s", out)
	}
}
