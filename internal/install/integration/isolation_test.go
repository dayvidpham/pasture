package integration_test

import (
	"strings"
	"testing"
)

// cellFixture describes one of the nine harness/extension cells: how the user
// names it on the command line, and the user-visible artifact that proves it
// was delivered.
type cellFixture struct {
	// name is the canonical cell identifier used by the apply-result and
	// status documents.
	name string
	// harness and extension are the positional words a user types.
	harness   string
	extension string
	// marker is a path below HOME that exists only when this cell is
	// installed. Native Claude cells deliver through the host's plugin
	// manager and therefore have no HOME marker; selector is used instead.
	marker string
	// selector is the native plugin selector for Claude cells.
	selector string
	// pendingTrust marks the one cell whose artifacts install successfully but
	// whose execution stays unclaimed until the user approves it natively.
	pendingTrust bool
}

// installedStatus is the settled apply status this cell reports after a
// successful ensure.
func (c cellFixture) installedStatus() string {
	if c.pendingTrust {
		return "installed_pending_trust"
	}
	return "completed"
}

func (c cellFixture) native() bool { return c.selector != "" }

// nineCells is the complete closed set of installable cells. Each is exercised
// in isolation: the suite installs exactly one, proves no sibling artifact
// appeared, then removes it and proves the destination is clean again.
func nineCells() []cellFixture {
	return []cellFixture{
		{name: "claude-code.skills", harness: "claude", extension: "skills", selector: "pasture-skills@aura-plugins"},
		{name: "claude-code.agents", harness: "claude", extension: "agents", selector: "pasture-agents@aura-plugins"},
		{name: "claude-code.hooks", harness: "claude", extension: "hooks", selector: "pasture-hooks@aura-plugins"},
		{name: "opencode.skills", harness: "opencode", extension: "skills", marker: ".config/opencode/skills/"},
		{name: "opencode.agents", harness: "opencode", extension: "agents", marker: ".config/opencode/agent/"},
		{name: "opencode.hooks", harness: "opencode", extension: "hooks", marker: ".config/opencode/plugins/pasture-hooks.ts"},
		{name: "codex.skills", harness: "codex", extension: "skills", marker: ".agents/skills/"},
		{name: "codex.agents", harness: "codex", extension: "agents", marker: ".codex/agents/"},
		{name: "codex.hooks", harness: "codex", extension: "hooks", marker: ".codex/hooks/", pendingTrust: true},
	}
}

// TestNineCells_InstallAndUninstallAreIsolated proves each of the nine cells is
// independently deliverable and removable through the human-facing verbs, with
// its eight siblings absent throughout: no sibling artifact is written below
// HOME, no sibling native plugin is installed or removed, and the confirmed
// inventory records exactly one cell.
func TestNineCells_InstallAndUninstallAreIsolated(t *testing.T) {
	t.Parallel()
	for _, subject := range nineCells() {
		t.Run(subject.name, func(t *testing.T) {
			t.Parallel()
			env := newEnv(t, hostSeed{})

			install := decodeApply(t, env.mustRun("install", subject.harness, subject.extension, "--json").stdout)
			if !install.OK || !equalStrings(install.cellNames(), []string{subject.name}) {
				t.Fatalf("install reported %+v, want one ok row for %s", install, subject.name)
			}
			if row := install.row(t, subject.name); row.Operation != "ensure" || row.Status != subject.installedStatus() || row.Observation != "installed" || row.Management != "pasture_managed" {
				t.Fatalf("install row for %s is %+v, want an ensured %s, installed, Pasture-managed cell", subject.name, row, subject.installedStatus())
			}

			assertOnlyCellPresent(t, env, subject, true)

			status := env.status()
			if len(status) != 1 {
				t.Fatalf("confirmed inventory holds %d cells (%+v), want only %s", len(status), status, subject.name)
			}
			record, ok := status[subject.name]
			if !ok || record.Observation != "installed" || !record.Managed || record.Source != "installer" || record.LastAction != "ensure" || record.LastOutcome != "completed" {
				t.Fatalf("confirmed inventory row for %s is %+v, want an installer-owned installed record", subject.name, record)
			}

			uninstall := decodeApply(t, env.mustRun("uninstall", subject.harness, subject.extension, "--json").stdout)
			if !uninstall.OK || !equalStrings(uninstall.cellNames(), []string{subject.name}) {
				t.Fatalf("uninstall reported %+v, want one ok row for %s", uninstall, subject.name)
			}
			if row := uninstall.row(t, subject.name); row.Operation != "remove" || row.Observation != "absent" {
				t.Fatalf("uninstall row for %s is %+v, want a removed, absent cell", subject.name, row)
			}

			assertOnlyCellPresent(t, env, subject, false)

			if final := env.status()[subject.name]; final.Observation != "absent" || final.LastAction != "remove" || final.LastOutcome != "completed" {
				t.Fatalf("confirmed inventory row for %s after removal is %+v, want an absent record whose last action is a completed remove", subject.name, final)
			}
		})
	}
}

// assertOnlyCellPresent proves the isolated home and the isolated native host
// contain the subject cell's artifacts (when installed is true) and nothing
// belonging to any of its eight siblings, in either direction.
func assertOnlyCellPresent(t *testing.T, env *installerEnv, subject cellFixture, installed bool) {
	t.Helper()
	files := env.files()
	rows := env.hostRows()
	mutations := env.mutatingHostCommands()

	for _, other := range nineCells() {
		if other.name == subject.name {
			continue
		}
		if other.marker != "" {
			for _, file := range files {
				if strings.HasPrefix(file, other.marker) {
					t.Fatalf("installing only %s produced sibling artifact %q belonging to %s; siblings must never be written",
						subject.name, file, other.name)
				}
			}
		}
		if other.native() {
			for _, row := range rows {
				if row == other.selector {
					t.Fatalf("installing only %s left sibling native plugin %q installed; siblings must never be mutated",
						subject.name, other.selector)
				}
			}
			for _, command := range mutations {
				if strings.Contains(command, other.selector) {
					t.Fatalf("installing only %s issued native mutation %q against sibling %s; siblings must never be mutated",
						subject.name, command, other.name)
				}
			}
		}
	}

	if subject.native() {
		want := []string{}
		if installed {
			want = append(want, subject.selector)
		}
		if !equalStrings(rows, want) {
			t.Fatalf("isolated native host reports %v installed, want %v", rows, want)
		}
		if len(files) != 0 {
			t.Fatalf("native cell %s wrote %v below HOME; native cells activate through the host plugin manager only", subject.name, files)
		}
		return
	}

	owned := 0
	for _, file := range files {
		if strings.HasPrefix(file, subject.marker) {
			owned++
		}
	}
	switch {
	case installed && owned == 0:
		t.Fatalf("cell %s reported installed but delivered no file under %q; files: %v", subject.name, subject.marker, files)
	case !installed && owned != 0:
		t.Fatalf("cell %s reported removed but left %d file(s) under %q; files: %v", subject.name, owned, subject.marker, files)
	}
	if len(rows) != 0 {
		t.Fatalf("direct-file cell %s changed the isolated native host state to %v; it must never invoke the plugin manager", subject.name, rows)
	}
}

// TestInstallAndUninstall_AreIdempotent proves a repeated install and a
// repeated uninstall converge: the second run reports the same settled cell,
// leaves the same files, and leaves the confirmed inventory unchanged.
func TestInstallAndUninstall_AreIdempotent(t *testing.T) {
	t.Parallel()
	for _, subject := range []cellFixture{
		{name: "claude-code.skills", harness: "claude", extension: "skills", selector: "pasture-skills@aura-plugins"},
		{name: "opencode.skills", harness: "opencode", extension: "skills", marker: ".config/opencode/skills/"},
		{name: "codex.hooks", harness: "codex", extension: "hooks", marker: ".codex/hooks/", pendingTrust: true},
	} {
		t.Run(subject.name, func(t *testing.T) {
			t.Parallel()
			env := newEnv(t, hostSeed{})

			env.mustRun("install", subject.harness, subject.extension)
			firstFiles := env.files()
			firstStatus := env.status()[subject.name]

			second := decodeApply(t, env.mustRun("install", subject.harness, subject.extension, "--json").stdout)
			if !second.OK || second.row(t, subject.name).Observation != "installed" || second.row(t, subject.name).Status != subject.installedStatus() {
				t.Fatalf("repeated install did not converge: %+v", second)
			}
			if !equalStrings(env.files(), firstFiles) {
				t.Fatalf("repeated install changed the delivered files:\nfirst:  %v\nsecond: %v", firstFiles, env.files())
			}
			if got := env.status()[subject.name]; got != firstStatus {
				t.Fatalf("repeated install changed the confirmed record:\nfirst:  %+v\nsecond: %+v", firstStatus, got)
			}

			env.mustRun("uninstall", subject.harness, subject.extension)
			removedFiles := env.files()
			removedStatus := env.status()[subject.name]

			repeat := decodeApply(t, env.mustRun("uninstall", subject.harness, subject.extension, "--json").stdout)
			if !repeat.OK {
				t.Fatalf("repeated uninstall did not converge: %+v", repeat)
			}
			// The second removal has no managed installed fact left to act on,
			// so the installer reports an explicit no-op instead of inventing
			// another removal.
			if row := repeat.row(t, subject.name); row.Operation != "inspect" || row.Status != "no_op" {
				t.Fatalf("repeated uninstall row is %+v, want an explicit inspect/no_op", row)
			}
			if !equalStrings(env.files(), removedFiles) {
				t.Fatalf("repeated uninstall changed the remaining files:\nfirst:  %v\nsecond: %v", removedFiles, env.files())
			}
			if got := env.status()[subject.name]; got != removedStatus {
				t.Fatalf("repeated uninstall changed the confirmed record:\nfirst:  %+v\nsecond: %+v", removedStatus, got)
			}
		})
	}
}
