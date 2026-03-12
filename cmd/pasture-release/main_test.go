// Package main smoke-tests the pasture-release CLI binary via exec.Command
// subprocesses. Each test case compiles the binary once and then invokes it
// as a child process, matching against combined stdout+stderr output.
//
// UAT requirement: tests must use exec.Command (subprocess), not the
// in-process newRootCmd() exported by this package, so that they exercise
// the real production binary path end-to-end.
package main_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Fixture types ────────────────────────────────────────────────────────────

// smokeFixtures is the top-level structure of testdata/cli_smoke.yaml.
type smokeFixtures struct {
	Tests []smokeCaseFixture `yaml:"tests"`
}

// smokeCaseFixture represents one CLI smoke-test case.
// Fields that are absent in the YAML are left as empty strings (no match
// assertion is performed for empty fields).
type smokeCaseFixture struct {
	ID                   string   `yaml:"id"`
	Args                 []string `yaml:"args"`
	WantExit             int      `yaml:"want_exit"`
	WantStdoutContains   string   `yaml:"want_stdout_contains"`
	WantStderrContains   string   `yaml:"want_stderr_contains"`
}

// ─── Binary build helper ──────────────────────────────────────────────────────

// buildBinary compiles the pasture-release main package into a temporary
// directory and returns the path to the compiled binary. The binary is
// removed automatically when the test ends.
//
// Failure mode: if the build fails the test is stopped immediately via
// t.Fatalf, with the compiler output included for diagnosis.
func buildBinary(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "pasture-release")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	// Run from the package directory so "." refers to cmd/pasture-release.
	cmd.Dir = "."

	if err := cmd.Run(); err != nil {
		t.Fatalf(
			"buildBinary: failed to compile pasture-release binary — "+
				"build output:\n%s\nerror: %v",
			out.String(), err,
		)
	}
	return binPath
}

// ─── Smoke tests ──────────────────────────────────────────────────────────────

// TestCLISmoke compiles the pasture-release binary once and then runs each
// fixture case as an independent parallel subtest. Combined stdout+stderr is
// checked for required substrings so that Cobra's output routing (help →
// stdout, errors → stderr) does not cause false negatives.
func TestCLISmoke(t *testing.T) {
	var fixtures smokeFixtures
	testutil.LoadFixtures(t, testutil.CLISmoke, &fixtures)

	require.NotEmpty(t, fixtures.Tests,
		"cli_smoke.yaml must contain at least one test case")

	binPath := buildBinary(t)

	for _, tc := range fixtures.Tests {
		tc := tc // capture for parallel closure
		t.Run(tc.ID, func(t *testing.T) {
			t.Parallel()

			cmd := exec.Command(binPath, tc.Args...) //nolint:gosec
			var combined bytes.Buffer
			cmd.Stdout = &combined
			cmd.Stderr = &combined

			runErr := cmd.Run()

			// Determine actual exit code.
			exitCode := 0
			if runErr != nil {
				if exitErr, ok := runErr.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					// Unexpected execution failure (e.g. binary not found).
					t.Fatalf(
						"case %q: unexpected exec error (not an exit error) — "+
							"ensure the binary compiled successfully: %v",
						tc.ID, runErr,
					)
				}
			}

			output := combined.String()

			assert.Equal(t, tc.WantExit, exitCode,
				"case %q: exit code mismatch\nargs: %v\noutput:\n%s",
				tc.ID, tc.Args, output)

			if tc.WantStdoutContains != "" {
				assert.Contains(t, output, tc.WantStdoutContains,
					"case %q: expected stdout to contain %q\nfull output:\n%s",
					tc.ID, tc.WantStdoutContains, output,
				)
			}

			if tc.WantStderrContains != "" {
				assert.Contains(t, output, tc.WantStderrContains,
					"case %q: expected stderr to contain %q\nfull output:\n%s",
					tc.ID, tc.WantStderrContains, output,
				)
			}
		})
	}

}
