package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildVersionBinary builds the production `pasture` command (this package)
// into binary, optionally passing extra linker flags. A subprocess build is
// used deliberately: the stamping contract IS a link-time property, so it can
// only be proven by linking a binary and asking it for its version. Inspecting
// the `version` variable from inside the test binary would prove nothing about
// -ldflags, and would additionally be unable to observe the unstamped default
// whenever the test binary itself were built with a stamp.
//
// The builds are CGO-free and unoptimised for speed (no -race): this test cares
// about the linker symbol, not about runtime behaviour under concurrency.
func buildVersionBinary(t *testing.T, binary string, ldflags string) {
	t.Helper()
	args := []string{"build"}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "-o", binary, ".")
	build := exec.Command("go", args...)
	build.Dir = "."
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
}

// runVersion runs `<binary> --version` and returns its stdout verbatim.
func runVersion(t *testing.T, binary string) string {
	t.Helper()
	command := exec.Command(binary, "--version")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Run(), stderr.String())
	require.Empty(t, stderr.String(), "--version must report on stdout only")
	return stdout.String()
}

// TestUnstampedBuildReportsDevelMarker proves an ordinary `go build` (CI, dev
// checkouts, `go install` from a branch) reports an honest development marker
// rather than a fabricated release tag. "devel" carries no "v" and no dotted
// triple, so a consumer scraping the line for a release tag finds nothing and
// cannot freeze a fiction into a compatibility floor.
func TestUnstampedBuildReportsDevelMarker(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pasture")
	buildVersionBinary(t, binary, "")

	require.Equal(t, "pasture version devel\n", runVersion(t, binary))
}

// TestStampedBuildReportsTheStampedVersion proves the release path: the value
// the release workflow and the Nix build pass as -X main.version is exactly what
// the binary reports, in the `pasture version vX.Y.Z` shape downstream tooling
// parses.
func TestStampedBuildReportsTheStampedVersion(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pasture")
	buildVersionBinary(t, binary, "-X main.version=v1.2.3")

	require.Equal(t, "pasture version v1.2.3\n", runVersion(t, binary))
}

// TestRootCommandUsesTheStampableVariable pins the in-process wiring: cobra
// reports whatever the package-level variable currently holds, so stamping the
// variable is sufficient to change the reported version.
func TestRootCommandUsesTheStampableVariable(t *testing.T) {
	require.Equal(t, version, rootCmd.Version,
		"rootCmd.Version must read the stampable package variable")
}
