package integration_test

import (
	"strings"
	"testing"
)

// TestLegacyMonolith_MigratesExactlyAsPromised covers the one supported
// migration: the exact user-scoped v0.0.4 Pasture monolith is replaced by the
// three split plugins, and the monolith is removed only after each split is
// confirmed installed.
//
// The migration is only offered through the exhaustive selection surface,
// because replacing the monolith requires knowing the user's intent for all
// three Claude cells at once.
func TestLegacyMonolith_MigratesExactlyAsPromised(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{installedFixture: "exact-monolith.json", marketplaceFixture: "marketplaces.json"})

	selection := allCells(false)
	selection["claude-code.skills"] = true
	selection["claude-code.agents"] = true
	selection["claude-code.hooks"] = true

	migrated := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", env.writeDesired(selection), "--json").stdout)
	if !migrated.OK {
		t.Fatalf("monolith migration failed: %+v", migrated)
	}
	for _, name := range []string{"claude-code.skills", "claude-code.agents", "claude-code.hooks"} {
		if row := migrated.row(t, name); row.Operation != "ensure" || row.Status != "completed" || row.Observation != "installed" || row.Management != "pasture_managed" {
			t.Fatalf("migration row for %s is %+v, want an ensured, installed, Pasture-managed cell", name, row)
		}
		if record := env.status()[name]; record.Observation != "installed" || !record.Managed {
			t.Fatalf("confirmed inventory for %s after migration is %+v, want an installed Pasture-managed record", name, record)
		}
	}
	if rows := env.hostRows(); !equalStrings(rows, []string{"pasture-agents@aura-plugins", "pasture-hooks@aura-plugins", "pasture-skills@aura-plugins"}) {
		t.Fatalf("native host holds %v after migration, want exactly the three split plugins", rows)
	}

	// Ordering matters: the monolith must be removed only after the splits that
	// replace it are installed, so an interrupted migration never leaves the
	// user with neither.
	mutations := env.mutatingHostCommands()
	removal := indexOf(mutations, "plugin uninstall pasture@aura-plugins --scope user")
	if removal < 0 {
		t.Fatalf("the exact legacy monolith was never removed; native mutations were %v", mutations)
	}
	for _, selector := range []string{"pasture-skills@aura-plugins", "pasture-agents@aura-plugins", "pasture-hooks@aura-plugins"} {
		install := indexOf(mutations, "plugin install "+selector+" --scope user")
		if install < 0 || install > removal {
			t.Fatalf("split %s was installed at %d, after the monolith removal at %d; native mutations were %v", selector, install, removal, mutations)
		}
	}

	// Rerunning the same selection is a no-op: nothing is left to migrate.
	env.resetHostLog()
	again := decodeApply(t, env.mustRun("install", "apply-selection", "--desired", env.writeDesired(selection), "--json").stdout)
	if !again.OK {
		t.Fatalf("rerunning the migrated selection failed: %+v", again)
	}
	// A convergent rerun may reassert the managed cells through the manager's
	// idempotent update verb, but it must never reinstall or remove anything.
	for _, command := range env.mutatingHostCommands() {
		if !strings.HasPrefix(command, "plugin update ") {
			t.Fatalf("rerunning the migrated selection issued %q; the migration is not convergent", command)
		}
	}
	if rows := env.hostRows(); !equalStrings(rows, []string{"pasture-agents@aura-plugins", "pasture-hooks@aura-plugins", "pasture-skills@aura-plugins"}) {
		t.Fatalf("rerunning the migrated selection changed the native host to %v", rows)
	}
}

// TestLegacyMonolith_RefusesPerCellRequestsWithoutSiblingIntent proves the
// per-cell verbs refuse to act while the exact monolith is present, because a
// single cell cannot express what should happen to the other two. The refusal
// happens before any native mutation and names the surface that can complete
// the migration.
func TestLegacyMonolith_RefusesPerCellRequestsWithoutSiblingIntent(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{installedFixture: "exact-monolith.json", marketplaceFixture: "marketplaces.json"})
	before := env.hostRows()

	refused := env.run("install", "claude", "skills", "--json")
	if refused.exitCode == 0 {
		t.Fatalf("per-cell install proceeded while the exact monolith was present: %+v", refused)
	}
	result := decodeApply(t, refused.stdout)
	row := result.row(t, "claude-code.skills")
	if result.OK || row.Status != "failed" {
		t.Fatalf("per-cell install reported %+v, want a failure", row)
	}
	if !strings.Contains(row.Diagnostic, "no mutation was attempted") || !strings.Contains(row.Diagnostic, "apply-selection") {
		t.Fatalf("refusal does not tell the user nothing changed and which surface to use: %s", row.Diagnostic)
	}
	if !equalStrings(env.hostRows(), before) {
		t.Fatalf("native host changed from %v to %v during a refused request", before, env.hostRows())
	}
	if mutations := env.mutatingHostCommands(); len(mutations) != 0 {
		t.Fatalf("refused request still issued native mutations %v", mutations)
	}
}

// TestNearMatchLegacyPackage_IsNeverMigrated proves the migration exception is
// exact: a Pasture-named package installed at the wrong scope is not the
// supported monolith, so it is refused with zero mutation instead of being
// migrated, adopted, or removed.
func TestNearMatchLegacyPackage_IsNeverMigrated(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{installedFixture: "wrong-scope-monolith.json"})
	before := env.hostRows()

	refused := env.run("install", "claude", "skills", "--json")
	if refused.exitCode == 0 {
		t.Fatalf("a wrong-scope Pasture package was accepted: %+v", refused)
	}
	row := decodeApply(t, refused.stdout).row(t, "claude-code.skills")
	if row.Status != "failed" {
		t.Fatalf("wrong-scope package reported %+v, want a failure", row)
	}
	if !strings.Contains(row.Diagnostic, "no mutation was attempted") || !strings.Contains(row.Diagnostic, "repair the conflicting plugin row manually") {
		t.Fatalf("refusal is not actionable: %s", row.Diagnostic)
	}
	if !equalStrings(env.hostRows(), before) {
		t.Fatalf("native host changed from %v to %v; a near match must receive zero mutation", before, env.hostRows())
	}
	if mutations := env.mutatingHostCommands(); len(mutations) != 0 {
		t.Fatalf("a near match triggered native mutations %v", mutations)
	}
	if len(env.status()) != 0 {
		t.Fatalf("a near match was recorded in the confirmed inventory: %+v", env.status())
	}
}

// TestVersionlessNativeRow_IsProvedFromTheInstalledManifest covers the host
// response shape that omits a version: the installer must prove the release
// identity from the plugin's own installed manifest rather than trusting the
// listing or guessing.
func TestVersionlessNativeRow_IsProvedFromTheInstalledManifest(t *testing.T) {
	t.Parallel()
	env := newEnv(t, hostSeed{omitVersion: true})

	installed := decodeApply(t, env.mustRun("install", "claude", "agents", "--json").stdout)
	if row := installed.row(t, "claude-code.agents"); row.Status != "completed" || row.Observation != "installed" {
		t.Fatalf("versionless native row was not accepted from its manifest: %+v", row)
	}
	if record := env.status()["claude-code.agents"]; record.Observation != "installed" || !record.Managed {
		t.Fatalf("confirmed inventory for a versionless row is %+v, want an installed Pasture-managed record", record)
	}
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
