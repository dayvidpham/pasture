package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExactExternalCopy_IsPreservedAndNeverAdopted proves an exact copy of the
// shipped skills that Pasture did not install stays externally owned: install
// reports it as external instead of claiming it, and uninstall refuses to
// remove it. This is the user-visible guarantee that a hand-managed or
// third-party copy of the same files is never silently taken over or deleted.
func TestExactExternalCopy_IsPreservedAndNeverAdopted(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{})
	destination := filepath.Join(env.home, ".config", "opencode", "skills")
	copyTree(t, filepath.Join(repoRoot, ".opencode", "skill"), destination)
	before := digestTree(t, destination)

	install := decodeApply(t, env.mustRun("install", "opencode", "skills", "--json").stdout)
	row := install.row(t, "opencode.skills")
	if !install.OK || row.Management != "external" || row.Observation != "installed" {
		t.Fatalf("install over an exact external copy reported %+v, want an installed but externally managed cell", row)
	}
	if !strings.Contains(row.Diagnostic, "externally owned") {
		t.Fatalf("install diagnostic %q does not tell the user the copy stays externally owned", row.Diagnostic)
	}
	if record := env.status()["opencode.skills"]; record.Managed {
		t.Fatalf("confirmed inventory adopted the external copy: %+v", record)
	}

	uninstall := decodeApply(t, env.mustRun("uninstall", "opencode", "skills", "--json").stdout)
	if removal := uninstall.row(t, "opencode.skills"); removal.Status != "no_op" || removal.Operation != "inspect" {
		t.Fatalf("uninstall over an exact external copy reported %+v, want an explicit no-op", removal)
	}
	assertSameTree(t, "external copy before uninstall", before, "external copy after uninstall", digestTree(t, destination))
}

// TestExactExternalNativePlugins_ArePreservedAndNeverRemoved proves the same
// guarantee on the native Claude path: pre-existing exact split plugins that
// Pasture never installed are reported as external, and no native uninstall is
// ever issued for them.
func TestExactExternalNativePlugins_ArePreservedAndNeverRemoved(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{installedFixture: "exact-splits.json", marketplaceFixture: "marketplaces.json"})
	before := env.hostRows()

	install := decodeApply(t, env.mustRun("install", "claude", "skills", "--json").stdout)
	row := install.row(t, "claude-code.skills")
	if !install.OK || row.Management != "external" || row.Observation != "installed" {
		t.Fatalf("install over an exact external plugin reported %+v, want an installed but externally managed cell", row)
	}
	if !strings.Contains(row.Diagnostic, "externally owned") {
		t.Fatalf("install diagnostic %q does not tell the user the plugin stays externally owned", row.Diagnostic)
	}

	uninstall := decodeApply(t, env.mustRun("uninstall", "claude", "skills", "--json").stdout)
	if removal := uninstall.row(t, "claude-code.skills"); removal.Status != "no_op" {
		t.Fatalf("uninstall over an exact external plugin reported %+v, want an explicit no-op", removal)
	}
	if !equalStrings(env.hostRows(), before) {
		t.Fatalf("native host state changed from %v to %v; external plugins must never be mutated", before, env.hostRows())
	}
	if mutations := env.mutatingHostCommands(); len(mutations) != 0 {
		t.Fatalf("installer issued native mutations %v against externally owned plugins", mutations)
	}
}

// TestDeclarativeOwnership_RefusesToTouchDirectFileCells proves the Home
// Manager boundary: when the declarative controller owns a direct-file cell,
// the scriptable per-cell verb refuses before any filesystem access, and the
// exhaustive selection surface reports those cells as declaratively managed
// rather than inspecting or writing them.
func TestDeclarativeOwnership_RefusesToTouchDirectFileCells(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{})

	refused := env.run("install", "apply-cell", "--harness", "opencode", "--extension", "skills", "--enabled=true", "--source", "home-manager")
	if refused.exitCode == 0 {
		t.Fatalf("declaratively owned cell was applied instead of refused: %+v", refused)
	}
	if !strings.Contains(refused.combined(), "Home Manager owns DirectFile cell opencode.skills declaratively") ||
		!strings.Contains(refused.combined(), "rerun Home Manager activation") {
		t.Fatalf("refusal is not actionable: %s", refused.combined())
	}
	if files := env.files(); len(files) != 0 {
		t.Fatalf("refused declarative request still wrote %v below HOME; it must refuse before mutation", files)
	}

	desired := env.writeDesired(allCells(true))
	applied := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", desired, "--source", "home-manager", "--json").stdout)
	for _, harness := range []string{"opencode", "codex"} {
		for _, extension := range []string{"skills", "agents", "hooks"} {
			name := harness + "." + extension
			if row := applied.row(t, name); row.Status != "managed_declaratively" {
				t.Fatalf("declarative apply row for %s is %+v, want managed_declaratively", name, row)
			}
		}
	}
	if files := env.files(); len(files) != 0 {
		t.Fatalf("declarative apply-selection wrote %v below HOME; declarative cells belong to Home Manager", files)
	}
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(destination, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("seed an exact external copy of %q at %q: %v", source, destination, err)
	}
}
