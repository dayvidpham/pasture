// Package main_test contains CLI smoke tests for pasture-msg.
//
// Tests compile the pasture-msg binary via go build and exercise it through
// exec.Command subprocess calls. This verifies the full CLI production code
// path (cobra wiring, flag parsing, exit codes) without requiring a live
// Temporal server connection.
//
// Fixtures are loaded from testdata/cli_smoke.yaml via testutil.LoadFixtures.
package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/testutil"
)

// smokeCase is the per-test-case schema that maps to each entry under
// testdata/cli_smoke.yaml's "cases" list.
type smokeCase struct {
	Name               string   `yaml:"name"`
	Args               []string `yaml:"args"`
	WantExit           int      `yaml:"want_exit"`
	WantStdoutContains string   `yaml:"want_stdout_contains"`
	WantStderrContains string   `yaml:"want_stderr_contains"`
}

// smokeSuite is the top-level YAML document: a list of cases.
type smokeSuite struct {
	Cases []smokeCase `yaml:"cases"`
}

// binaryPath holds the compiled pasture-msg binary, built once for the whole
// test run. Set in TestMain.
var binaryPath string

// TestMain compiles the pasture-msg binary before running any test, and removes
// it afterwards. Compilation failure causes all tests to be skipped with a
// clear diagnostic.
func TestMain(m *testing.M) {
	// Determine a temporary path for the compiled binary.
	tmpDir, err := os.MkdirTemp("", "pasture-msg-smoke-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pasture-msg smoke: could not create temp dir: %v\n", err)
		os.Exit(1)
	}
	binaryName := "pasture-msg"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath = filepath.Join(tmpDir, binaryName)

	// go build ./cmd/pasture-msg — run from the module root.
	buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/pasture-msg")
	buildCmd.Dir = moduleRoot()
	var buildOut bytes.Buffer
	buildCmd.Stderr = &buildOut
	buildCmd.Stdout = &buildOut
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr,
			"pasture-msg smoke: go build failed — cannot run smoke tests.\n"+
				"  binary: %s\n"+
				"  error:  %v\n"+
				"  output: %s\n",
			binaryPath, err, buildOut.String(),
		)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(tmpDir) // explicit cleanup before os.Exit (defer would be skipped)
	os.Exit(code)
}

// moduleRoot returns the absolute path to the pasture module root by walking
// upward from this file's directory until go.mod is found.
//
// exec.Command("go build", ...) must run from the module root so that the
// package path "." resolves correctly.
func moduleRoot() string {
	// __file__ is not available at runtime; use the known relative layout
	// instead: this test lives at cmd/pasture-msg/, and the module root is
	// two directories up.
	//
	// We use os.Getwd() because `go test` sets cwd to the package under test.
	wd, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("os.Getwd: %v", err))
	}
	// Walk upward until we find go.mod.
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding go.mod.
			panic(fmt.Sprintf("moduleRoot: could not find go.mod starting from %s", wd))
		}
		dir = parent
	}
}

// TestPastureMsgCLISmoke loads cli_smoke.yaml and runs each case as a
// subprocess against the compiled pasture-msg binary.
func TestPastureMsgCLISmoke(t *testing.T) {
	var suite smokeSuite
	testutil.LoadFixtures(t, testutil.CLISmoke, &suite)

	require.NotEmpty(t, suite.Cases,
		"cli_smoke.yaml must contain at least one test case")

	for _, tc := range suite.Cases {
		tc := tc // capture range variable
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			// #nosec G204 — binaryPath is set by TestMain from a controlled go build.
			cmd := exec.Command(binaryPath, tc.Args...)
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
					// Unexpected execution error (e.g., binary not found).
					require.NoError(t, runErr,
						"unexpected error executing pasture-msg: %v\nstdout: %s\nstderr: %s",
						runErr, stdout.String(), stderr.String(),
					)
				}
			}

			assert.Equal(t, tc.WantExit, exitCode,
				"exit code mismatch for %q\n  args:   %v\n  stdout: %s\n  stderr: %s",
				tc.Name, tc.Args, stdout.String(), stderr.String(),
			)

			if tc.WantStdoutContains != "" {
				assert.Contains(t, stdout.String(), tc.WantStdoutContains,
					"stdout missing expected substring %q for %q\n  stdout: %s",
					tc.WantStdoutContains, tc.Name, stdout.String(),
				)
			}

			if tc.WantStderrContains != "" {
				assert.Contains(t, stderr.String(), tc.WantStderrContains,
					"stderr missing expected substring %q for %q\n  stderr: %s",
					tc.WantStderrContains, tc.Name, stderr.String(),
				)
			}
		})
	}
}
