package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/spf13/cobra"
)

const hostVersionOutputLimit = 4096

// Claude's measured --version output is one release and the product suffix.
// Neither installation paths nor numeric substrings in arbitrary output are
// evidence of a version. Keep the accepted grammar closed until observed anew.
var claudeVersionOutput = regexp.MustCompile(`\A([0-9]+\.[0-9]+\.[0-9]+) \(Claude Code\)(?:\r?\n)?\z`)

func resolveLifecycleHostVersion(ctx context.Context, cmd *cobra.Command, coords lifecycleCoordinates) (string, error) {
	explicit := cmd.Flags().Changed("host-executable")
	if !explicit && (cmd.Flags().Changed("host-version") || coords.Harness != ir.HarnessClaudeCode) {
		return coords.HostVersion, nil
	}
	executable, _ := cmd.Flags().GetString("host-executable")
	var cause error
	switch {
	case explicit && cmd.Flags().Changed("host-version"):
		cause = errors.New("--host-executable and --host-version are mutually exclusive; supply only one version source")
	case coords.Harness != ir.HarnessClaudeCode:
		cause = errors.New("--host-executable is supported only for --harness claude-code; use --host-version for other harnesses")
	case explicit && executable == "":
		cause = errors.New("--host-executable was supplied empty; provide an absolute Claude executable path, omit the flag for default discovery, or supply an explicitly observed --host-version")
	case explicit && !filepath.IsAbs(executable):
		cause = errors.New("--host-executable must be a complete absolute path; omit the flag to discover the first claude executable on PATH")
	}
	if cause != nil {
		return "", lifecycleHostVersionError(cause)
	}
	if !explicit {
		// A usable hint is optional executable evidence, not process attestation.
		// LookPath tests absolute hints without searching PATH or splitting text.
		hint := os.Getenv("CLAUDE_CODE_EXECPATH")
		if filepath.IsAbs(hint) {
			if usable, err := exec.LookPath(hint); err == nil {
				executable = usable
			}
		}
		if executable == "" {
			var err error
			executable, err = exec.LookPath("claude")
			if err != nil {
				return "", lifecycleHostVersionError(fmt.Errorf("no usable Claude executable was selected: install Claude and expose it on PATH, fix the optional CLAUDE_CODE_EXECPATH hint, or supply --host-executable or an observed --host-version: %w", err))
			}
		}
	}
	// Selection is complete. A failed query must not start a version hunt.
	output, err := queryLifecycleHostVersion(ctx, executable)
	if err != nil {
		return "", lifecycleHostVersionError(err)
	}
	match := claudeVersionOutput.FindSubmatch(output)
	if match == nil {
		return "", lifecycleHostVersionError(errors.New("Claude --version stdout does not match the supported '<major>.<minor>.<patch> (Claude Code)' line; arbitrary process output was not retained as a version"))
	}
	version, err := pastureruntime.ParseHostVersion(string(match[1]))
	if err != nil {
		return "", lifecycleHostVersionError(errors.New("Claude --version supplied an invalid release number; no version was established"))
	}
	return version.String(), nil
}

func lifecycleHostVersionError(cause error) error {
	return fmt.Errorf("resolveLifecycleHostVersion (cmd/pasture/hook_lifecycle_host_version.go): host version resolution failed before capture, admission or storage; no occurrence was recorded; check the selected executable and its --version output, then retry the hook input: %w", cause)
}

// withLifecycleHostVersionSource adds attribution only when a version has an
// established source. It does not promote a query into process attestation;
// legacy and failed-query records omit the member rather than assert a source.
func withLifecycleHostVersionSource(coords lifecycleCoordinates, fields map[string]any) map[string]any {
	if coords.HostVersion != "" && coords.HostVersionSource != "" {
		fields["hostVersionSource"] = coords.HostVersionSource
	}
	return fields
}

type hostVersionRead struct {
	stdout bool
	bytes  []byte
	err    error
}

// queryLifecycleHostVersion waits for exit AND both output streams under the
// invocation context. Owned pipe descriptors let cancellation close reads even
// when an exited executable left an inherited writer open. No process output is
// attached to native stdout/stderr. Each reader retains at most limit+1 bytes.
func queryLifecycleHostVersion(ctx context.Context, executable string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, context.Cause(ctx))
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return nil, errors.New("the version query has no invocation deadline; restore the lifecycle timeout context")
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create version stdout pipe: %w", err)
	}
	defer stdout.Close()
	defer stdoutWriter.Close()
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create version stderr pipe: %w", err)
	}
	defer stderr.Close()
	defer stderrWriter.Close()
	command := exec.Command(executable, "--version")
	command.Stdout = stdoutWriter
	command.Stderr = stderrWriter
	prepareHostVersionProcess(command)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start selected Claude executable with --version (check that it exists and is executable): %w", err)
	}
	// Only the child now owns the writing ends; our reads can observe its EOF.
	stdoutWriter.Close()
	stderrWriter.Close()
	reads := make(chan hostVersionRead, 2)
	read := func(pipe *os.File, isStdout bool) {
		body, err := io.ReadAll(io.LimitReader(pipe, hostVersionOutputLimit+1))
		if len(body) > hostVersionOutputLimit {
			err = fmt.Errorf("Claude --version output exceeded the %d-byte per-stream limit", hostVersionOutputLimit)
		}
		reads <- hostVersionRead{stdout: isStdout, bytes: body, err: err}
	}
	go read(stdout, true)
	go read(stderr, false)
	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()
	var output []byte
	var failure error
	done := ctx.Done()
	remainingReads := 2
	for remainingReads > 0 || waited != nil {
		select {
		case result := <-reads:
			remainingReads--
			if result.stdout {
				output = result.bytes
			}
			if result.err != nil {
				failure = errors.Join(failure, result.err)
			}
		case err := <-waited:
			waited = nil
			if err != nil {
				failure = errors.Join(failure, fmt.Errorf("Claude --version process did not exit successfully: %w", err))
			}
		case <-done:
			failure = errors.Join(failure, ctx.Err(), context.Cause(ctx))
		}
		if failure != nil && done != nil {
			// Terminate once, close reads, and still collect every goroutine and
			// Wait result. A pipe-holding descendant cannot prolong our reads.
			failure = errors.Join(failure, stopHostVersionProcess(command))
			stdout.Close()
			stderr.Close()
			done = nil
		}
	}
	if failure != nil {
		return nil, failure
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, context.Cause(ctx))
	}
	return output, nil
}
