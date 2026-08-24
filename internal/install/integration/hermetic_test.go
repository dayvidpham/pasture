package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFullSelection_InstallsAllNineCellsFromAnEmptyRoot proves the shipped
// binary delivers every cell from an unrelated empty working directory with no
// source checkout, no package manager, and no network: the only program
// reachable through PATH is the isolated Claude host, and every other cell is
// served from bytes embedded in the binary itself.
func TestFullSelection_InstallsAllNineCellsFromAnEmptyRoot(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{})
	env.workDir = t.TempDir() // an unrelated empty directory, not the checkout

	assertOnlyIsolatedHostOnPath(t, env)

	result := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", env.writeDesired(allCells(true)), "--json").stdout)
	if !result.OK {
		t.Fatalf("full nine-cell selection failed from an empty root: %+v", result)
	}
	for _, subject := range nineCells() {
		row := result.row(t, subject.name)
		if row.Status != subject.installedStatus() || row.Observation != "installed" {
			t.Fatalf("cell %s is %+v, want an installed %s row", subject.name, row, subject.installedStatus())
		}
		if record := env.status()[subject.name]; record.Observation != "installed" || !record.Managed {
			t.Fatalf("confirmed inventory for %s is %+v, want an installed Pasture-managed record", subject.name, record)
		}
	}

	// Everything the run produced lives under the isolated home and the
	// isolated state root; nothing was written anywhere else.
	files := env.files()
	if len(files) == 0 {
		t.Fatal("a successful nine-cell install delivered no files below the isolated home")
	}
	if entries, err := os.ReadDir(env.workDir); err != nil || len(entries) != 0 {
		t.Fatalf("the installer wrote into its working directory %q (%v, %v); it must operate from an unrelated empty directory", env.workDir, entries, err)
	}
}

// TestUninstall_RemovesEveryCellAndLeavesNoArtifacts proves the reverse
// direction converges too: removing the full selection leaves no delivered file
// behind and records every cell as absent.
func TestUninstall_RemovesEveryCellAndLeavesNoArtifacts(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{})
	env.mustRun("install", "apply-selection", "--desired", env.writeDesired(allCells(true)), "--json")

	removed := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", env.writeDesired(allCells(false)), "--json").stdout)
	if !removed.OK {
		t.Fatalf("removing the full selection failed: %+v", removed)
	}
	if files := env.files(); len(files) != 0 {
		t.Fatalf("removing every cell left %v below the isolated home", files)
	}
	if rows := env.hostRows(); len(rows) != 0 {
		t.Fatalf("removing every cell left native plugins %v installed", rows)
	}
	for _, subject := range nineCells() {
		if record := env.status()[subject.name]; record.Observation != "absent" {
			t.Fatalf("confirmed inventory for %s after removal is %+v, want an absent record", subject.name, record)
		}
	}
}

// TestDirectFileCells_NeedNoHostProgram proves the OpenCode and Codex cells
// install with no host program on PATH at all. Together with the empty
// environment every invocation runs under, this is the structural argument
// that ordinary installs touch neither a real harness installation nor the
// network.
func TestDirectFileCells_NeedNoHostProgram(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{})
	if err := os.Remove(filepath.Join(env.binDir, "claude")); err != nil {
		t.Fatalf("empty the isolated PATH directory: %v", err)
	}

	for _, subject := range []cellFixture{
		{name: "opencode.skills", harness: "opencode", extension: "skills"},
		{name: "codex.agents", harness: "codex", extension: "agents"},
	} {
		result := decodeApply(t, env.mustRun("install", subject.harness, subject.extension, "--json").stdout)
		if !result.OK || result.row(t, subject.name).Observation != "installed" {
			t.Fatalf("cell %s needed a host program on PATH: %+v", subject.name, result)
		}
	}

	// With no Claude binary reachable, an all-false selection must still report
	// truthfully rather than claiming or mutating unknown native state.
	optional := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", env.writeDesired(allCells(false)), "--json").stdout)
	for _, name := range []string{"claude-code.skills", "claude-code.agents", "claude-code.hooks"} {
		row := optional.row(t, name)
		if row.Observation != "unknown" || !strings.Contains(row.Diagnostic, "no state was claimed or mutated") {
			t.Fatalf("with no Claude host installed, %s reported %+v; want an explicitly unknown, unclaimed cell", name, row)
		}
	}
}

// TestInstallPlan_EmitsTheExhaustiveDocumentTheApplyVerbAccepts proves the
// read-only `install plan` verb normalizes the user's saved preferences into
// the same exhaustive nine-cell document this suite hand-builds, and that its
// output is accepted verbatim by apply-selection. It also proves plan mutates
// nothing.
func TestInstallPlan_EmitsTheExhaustiveDocumentTheApplyVerbAccepts(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{})

	// With no saved preferences every harness is disabled, so the normalized
	// document is the all-false selection.
	plan := env.mustRun("install", "plan")
	if want := readFile(t, env.writeDesired(allCells(false))); plan.stdout != want {
		t.Fatalf("`install plan` rendered:\n%s\nwant:\n%s", plan.stdout, want)
	}
	if files := env.files(); len(files) != 0 {
		t.Fatalf("`install plan` wrote %v below the isolated home; it must be read-only", files)
	}

	// Enabling every harness and every extension normalizes to the all-true
	// selection, which is the document Home Manager and automation apply.
	env.writeConfig(`install:
  harnesses:
    claude-code: true
    opencode: true
    codex: true
  extensions:
    skills: true
    agents: true
    hooks: true
`)
	enabled := env.mustRun("install", "plan")
	if want := readFile(t, env.writeDesired(allCells(true))); enabled.stdout != want {
		t.Fatalf("`install plan` rendered:\n%s\nwant:\n%s", enabled.stdout, want)
	}

	rendered := filepath.Join(t.TempDir(), "plan.yaml")
	if err := os.WriteFile(rendered, []byte(enabled.stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	applied := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", rendered, "--json").stdout)
	if !applied.OK || len(applied.Cells) != 9 {
		t.Fatalf("the document `install plan` produced was not applied as an exhaustive nine-cell selection: %+v", applied)
	}
	for _, subject := range nineCells() {
		if row := applied.row(t, subject.name); row.Observation != "installed" {
			t.Fatalf("cell %s from the planned selection is %+v, want an installed row", subject.name, row)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

// assertOnlyIsolatedHostOnPath proves the single PATH entry the CLI subprocess
// receives contains nothing but the isolated Claude host stand-in, so no
// network-capable helper program is reachable from the installer.
func assertOnlyIsolatedHostOnPath(t *testing.T, env *installerEnv) {
	t.Helper()
	entries, err := os.ReadDir(env.binDir)
	if err != nil {
		t.Fatalf("read the isolated PATH directory %q: %v", env.binDir, err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 1 || !strings.HasPrefix(names[0], "claude") {
		t.Fatalf("the isolated PATH directory holds %v, want only the isolated Claude host stand-in", names)
	}
}
