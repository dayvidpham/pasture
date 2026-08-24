package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFirstFailureStopsAndRetryConverges_DirectFile proves the reviewed
// stop-at-first-failure contract on the filesystem path, and that an ordinary
// rerun converges once the user repairs the reported obstruction.
//
// A conflicting file is planted in the OpenCode agents destination: it matches
// neither the shipped bundle nor any recorded Pasture ownership, so the
// installer must refuse to overwrite it. The cell applied before it keeps its
// verified effect, the obstructed cell reports a failure naming the exact
// destination, and every later cell is reported as unattempted rather than
// guessed at.
func TestFirstFailureStopsAndRetryConverges_DirectFile(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{})
	obstruction := filepath.Join(env.home, ".config", "opencode", "agent", "worker.md")
	if err := os.MkdirAll(filepath.Dir(obstruction), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(obstruction, []byte("user-owned content the installer must never overwrite\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	selection := allCells(false)
	selection["opencode.skills"] = true
	selection["opencode.agents"] = true
	selection["codex.skills"] = true
	desired := env.writeDesired(selection)

	stopped := decodeApply(t, env.run("install", "apply-selection", "--desired", desired, "--json").stdout)
	if stopped.OK {
		t.Fatalf("obstructed selection reported success: %+v", stopped)
	}
	if row := stopped.row(t, "opencode.skills"); row.Status != "completed" || row.Observation != "installed" {
		t.Fatalf("cell applied before the failure lost its verified effect: %+v", row)
	}
	failed := stopped.row(t, "opencode.agents")
	if failed.Status != "failed" {
		t.Fatalf("obstructed cell reported %+v, want a failure", failed)
	}
	if !strings.Contains(failed.Diagnostic, filepath.Dir(obstruction)) {
		t.Fatalf("failure diagnostic does not name the exact obstructed destination %q: %s", filepath.Dir(obstruction), failed.Diagnostic)
	}
	if later := stopped.row(t, "codex.skills"); later.Status != "unattempted" {
		t.Fatalf("cell after the failure reported %+v, want unattempted", later)
	}

	if got, err := os.ReadFile(obstruction); err != nil || string(got) != "user-owned content the installer must never overwrite\n" {
		t.Fatalf("the installer overwrote user-owned content at %q (%q, %v)", obstruction, string(got), err)
	}
	if _, ok := env.status()["codex.skills"]; ok {
		t.Fatalf("an unattempted cell was recorded in the confirmed inventory: %+v", env.status())
	}

	// The user repairs the obstruction exactly as the diagnostic instructs, and
	// an ordinary rerun converges with no extra flags or cleanup.
	if err := os.Remove(obstruction); err != nil {
		t.Fatal(err)
	}
	converged := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", desired, "--json").stdout)
	if !converged.OK {
		t.Fatalf("rerun after repair did not converge: %+v", converged)
	}
	for _, name := range []string{"opencode.skills", "opencode.agents", "codex.skills"} {
		if row := converged.row(t, name); row.Status != "completed" || row.Observation != "installed" {
			t.Fatalf("rerun row for %s is %+v, want a completed, installed cell", name, row)
		}
		if record := env.status()[name]; record.Observation != "installed" || !record.Managed {
			t.Fatalf("confirmed inventory for %s after convergence is %+v, want an installed Pasture-managed record", name, record)
		}
	}
}

// TestFirstFailureStopsAndRetryConverges_NativeManager proves the same contract
// on the Claude native path, where the plugin manager itself fails partway
// through an exhaustive selection.
func TestFirstFailureStopsAndRetryConverges_NativeManager(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{failCommands: map[string]string{
		"plugin install pasture-agents@aura-plugins": "the isolated host refused this install",
	}})

	selection := allCells(false)
	selection["claude-code.skills"] = true
	selection["claude-code.agents"] = true
	selection["claude-code.hooks"] = true
	desired := env.writeDesired(selection)

	stopped := decodeApply(t, env.run("install", "apply-selection", "--desired", desired, "--json").stdout)
	if stopped.OK {
		t.Fatalf("selection with a failing native action reported success: %+v", stopped)
	}
	if row := stopped.row(t, "claude-code.skills"); row.Status != "completed" || row.Observation != "installed" {
		t.Fatalf("cell installed before the failure lost its verified effect: %+v", row)
	}
	failed := stopped.row(t, "claude-code.agents")
	if failed.Status != "failed" || failed.Observation != "absent" {
		t.Fatalf("failing native cell reported %+v, want a failure whose strongest confirmed state is absent", failed)
	}
	if !strings.Contains(failed.Diagnostic, "the isolated host refused this install") ||
		!strings.Contains(failed.Diagnostic, "retry the full selection") {
		t.Fatalf("native failure diagnostic is not actionable: %s", failed.Diagnostic)
	}
	if rows := env.hostRows(); !equalStrings(rows, []string{"pasture-skills@aura-plugins"}) {
		t.Fatalf("native host holds %v after the stop, want only the cell verified before the failure", rows)
	}
	for _, command := range env.mutatingHostCommands() {
		if strings.Contains(command, "pasture-hooks") {
			t.Fatalf("a native action ran for a cell after the failure: %q", command)
		}
	}

	// Repair the host and rerun: the same command converges without needing to
	// undo the work that already succeeded.
	env.clearHostFailures()
	converged := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", desired, "--json").stdout)
	if !converged.OK {
		t.Fatalf("rerun after repairing the host did not converge: %+v", converged)
	}
	if rows := env.hostRows(); !equalStrings(rows, []string{"pasture-agents@aura-plugins", "pasture-hooks@aura-plugins", "pasture-skills@aura-plugins"}) {
		t.Fatalf("converged native host holds %v, want all three split plugins", rows)
	}
	for _, name := range []string{"claude-code.skills", "claude-code.agents", "claude-code.hooks"} {
		if record := env.status()[name]; record.Observation != "installed" || !record.Managed {
			t.Fatalf("confirmed inventory for %s after convergence is %+v, want an installed Pasture-managed record", name, record)
		}
	}
}

// TestScriptableSurfaces_ReportFailedCellsButStillExitZero locks in a defect
// found while validating the stop-at-first-failure contract.
//
// The human verbs (`pasture install ...`, `pasture uninstall ...`) exit 1 when
// any named cell fails. The scriptable surfaces `install apply-selection` and
// `install apply-cell` — the ones automation and declarative activation call —
// print a document whose "ok" field is false and then exit 0, so a caller that
// checks the process status believes a failed apply succeeded.
//
// This test asserts the current, defective behavior deliberately so the gap is
// visible and cannot regress further; when the exit status is corrected it must
// be updated to require a non-zero status. Tracked at
// https://github.com/dayvidpham/pasture/issues/39.
func TestScriptableSurfaces_ReportFailedCellsButStillExitZero(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{failCommands: map[string]string{
		"plugin install pasture-skills@aura-plugins": "the isolated host refused this install",
	}})

	cellRun := env.run("install", "apply-cell", "--harness", "claude-code", "--extension", "skills", "--enabled=true", "--json")
	cellResult := decodeApply(t, cellRun.stdout)
	if cellResult.OK || cellResult.row(t, "claude-code.skills").Status != "failed" {
		t.Fatalf("apply-cell did not report the failure in its document: %+v", cellResult)
	}
	if cellRun.exitCode != 0 {
		t.Fatalf("apply-cell now exits %d on a failed cell; the defect this test locks in has been fixed — "+
			"update this test to require a non-zero exit status", cellRun.exitCode)
	}

	selection := allCells(false)
	selection["claude-code.skills"] = true
	selectionRun := env.run("install", "apply-selection", "--desired", env.writeDesired(selection), "--json")
	selectionResult := decodeApply(t, selectionRun.stdout)
	if selectionResult.OK {
		t.Fatalf("apply-selection did not report the failure in its document: %+v", selectionResult)
	}
	if selectionRun.exitCode != 0 {
		t.Fatalf("apply-selection now exits %d on a failed cell; the defect this test locks in has been fixed — "+
			"update this test to require a non-zero exit status", selectionRun.exitCode)
	}

	// The human-facing verb, by contrast, already reports failure through the
	// process status. This contrast is what makes the scriptable behavior a
	// defect rather than a deliberate contract.
	verbRun := env.run("install", "claude", "skills")
	if verbRun.exitCode == 0 {
		t.Fatalf("the human install verb stopped reporting cell failures through its exit status: %+v", verbRun)
	}
}
