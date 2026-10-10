package main_test

import (
	"strings"
	"testing"
)

// These black-box tests exercise the compiled binary through runCLI and prove
// the exit-code translation and alias parity for the network-free paths. The
// built test binary is unstamped, so every spelling stops at the devel refusal
// without touching the network.

func TestCLI_UpgradeHelpShowsVerbAndRootFlag(t *testing.T) {
	t.Parallel()

	upgradeHelp := runCLI(t, "upgrade", "--help")
	if upgradeHelp.exitCode != 0 {
		t.Fatalf("upgrade --help exit %d; stderr=%s", upgradeHelp.exitCode, upgradeHelp.stderr)
	}
	for _, want := range []string{"Upgrade pasture", "--allow-downgrade", "--dry-run", "--yes", "--version"} {
		if !strings.Contains(upgradeHelp.stdout, want) {
			t.Fatalf("upgrade help missing %q:\n%s", want, upgradeHelp.stdout)
		}
	}
	if strings.Contains(upgradeHelp.stdout, "--prerelease") {
		t.Fatalf("upgrade help must not mention --prerelease:\n%s", upgradeHelp.stdout)
	}

	updateHelp := runCLI(t, "update", "--help")
	if updateHelp.exitCode != 0 {
		t.Fatalf("update --help exit %d; stderr=%s", updateHelp.exitCode, updateHelp.stderr)
	}
	if !strings.Contains(updateHelp.stdout, "Upgrade pasture") {
		t.Fatalf("update alias help differs:\n%s", updateHelp.stdout)
	}

	rootHelp := runCLI(t, "--help")
	if rootHelp.exitCode != 0 {
		t.Fatalf("root --help exit %d", rootHelp.exitCode)
	}
	if !strings.Contains(rootHelp.stdout, "upgrade") || !strings.Contains(rootHelp.stdout, "--upgrade") {
		t.Fatalf("root help missing upgrade surface:\n%s", rootHelp.stdout)
	}
}

func TestCLI_UpgradeRejectsBadFlags(t *testing.T) {
	t.Parallel()

	allowDowngrade := runCLI(t, "upgrade", "--allow-downgrade")
	if allowDowngrade.exitCode != 1 {
		t.Fatalf("--allow-downgrade exit = %d, want 1; stderr=%s", allowDowngrade.exitCode, allowDowngrade.stderr)
	}
	if !strings.Contains(allowDowngrade.stderr, "--allow-downgrade") {
		t.Fatalf("--allow-downgrade refusal missing retry shape:\n%s", allowDowngrade.stderr)
	}

	badVersion := runCLI(t, "upgrade", "--version", "bad!")
	if badVersion.exitCode != 1 {
		t.Fatalf("--version 'bad!' exit = %d, want 1; stderr=%s", badVersion.exitCode, badVersion.stderr)
	}
	if !strings.Contains(badVersion.stderr, "unsafe character") {
		t.Fatalf("--version 'bad!' refusal missing wording:\n%s", badVersion.stderr)
	}
}

func TestCLI_UpgradeDevelRefusedForEverySpelling(t *testing.T) {
	t.Parallel()

	spellings := [][]string{
		{"upgrade"},
		{"update"},
		{"--upgrade"},
	}
	for _, args := range spellings {
		out := runCLI(t, args...)
		if out.exitCode != 1 {
			t.Fatalf("%v exit = %d, want 1; stdout=%s stderr=%s", args, out.exitCode, out.stdout, out.stderr)
		}
		combined := out.stdout + out.stderr
		for _, want := range []string{`reports "devel"`, "cannot upgrade itself"} {
			if !strings.Contains(combined, want) {
				t.Fatalf("%v refusal missing %q:\n%s", args, want, combined)
			}
		}
	}
}

func TestCLI_UpgradeUnknownFlags(t *testing.T) {
	t.Parallel()

	prerelease := runCLI(t, "upgrade", "--prerelease")
	if prerelease.exitCode != 1 {
		t.Fatalf("--prerelease exit = %d, want 1", prerelease.exitCode)
	}
	if !strings.Contains(prerelease.stderr, "prerelease") {
		t.Fatalf("--prerelease should be an unknown flag:\n%s", prerelease.stderr)
	}

	shorthand := runCLI(t, "upgrade", "-v")
	if shorthand.exitCode != 1 {
		t.Fatalf("upgrade -v exit = %d, want 1", shorthand.exitCode)
	}
	if !strings.Contains(shorthand.stderr, "shorthand") && !strings.Contains(shorthand.stderr, "unknown") {
		t.Fatalf("upgrade -v should be an unknown shorthand:\n%s", shorthand.stderr)
	}
}

func TestCLI_RootUpgradeVersionCollisionPrintsVersion(t *testing.T) {
	t.Parallel()

	out := runCLI(t, "--upgrade", "--version")
	if out.exitCode != 0 {
		t.Fatalf("--upgrade --version exit = %d, want 0; stderr=%s", out.exitCode, out.stderr)
	}
	if out.stdout != "pasture version devel\n" {
		t.Fatalf("--upgrade --version stdout = %q, want the version line", out.stdout)
	}
	if strings.Contains(out.stderr, "cannot upgrade itself") {
		t.Fatalf("--upgrade --version must not run the upgrade flow:\n%s", out.stderr)
	}
}
