// Package main smoke-tests the pasture-release CLI binary via exec.Command
// subprocesses. Each test case compiles the binary once and then invokes it
// as a child process, matching against separately-captured stdout and stderr
// so a case can assert which stream produced which output.
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
// assertion is performed for empty fields). The *_contains fields assert a
// substring is present on that stream; the *_excludes fields assert one is
// absent, which locks down stream routing (e.g. an error must NOT leak to
// stdout).
type smokeCaseFixture struct {
	ID                 string   `yaml:"id"`
	Args               []string `yaml:"args"`
	WantExit           int      `yaml:"want_exit"`
	WantStdoutContains string   `yaml:"want_stdout_contains"`
	WantStderrContains string   `yaml:"want_stderr_contains"`
	WantStdoutExcludes string   `yaml:"want_stdout_excludes"`
	WantStderrExcludes string   `yaml:"want_stderr_excludes"`
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
// fixture case as an independent parallel subtest. stdout and stderr are
// captured separately so a case can assert Cobra's output routing (help →
// stdout, errors → stderr) rather than papering over it with a merged buffer.
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
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

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

			outStr := stdout.String()
			errStr := stderr.String()
			// streams is included in every failure message so a mismatch shows
			// exactly which stream carried what.
			streams := "stdout:\n" + outStr + "\nstderr:\n" + errStr

			assert.Equal(t, tc.WantExit, exitCode,
				"case %q: exit code mismatch\nargs: %v\n%s",
				tc.ID, tc.Args, streams)

			if tc.WantStdoutContains != "" {
				assert.Contains(t, outStr, tc.WantStdoutContains,
					"case %q: expected stdout to contain %q\n%s",
					tc.ID, tc.WantStdoutContains, streams,
				)
			}
			if tc.WantStderrContains != "" {
				assert.Contains(t, errStr, tc.WantStderrContains,
					"case %q: expected stderr to contain %q\n%s",
					tc.ID, tc.WantStderrContains, streams,
				)
			}
			if tc.WantStdoutExcludes != "" {
				assert.NotContains(t, outStr, tc.WantStdoutExcludes,
					"case %q: expected stdout to NOT contain %q\n%s",
					tc.ID, tc.WantStdoutExcludes, streams,
				)
			}
			if tc.WantStderrExcludes != "" {
				assert.NotContains(t, errStr, tc.WantStderrExcludes,
					"case %q: expected stderr to NOT contain %q\n%s",
					tc.ID, tc.WantStderrExcludes, streams,
				)
			}
		})
	}

}
