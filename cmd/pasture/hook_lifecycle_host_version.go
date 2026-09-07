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
	if !cmd.Flags().Changed("host-executable") {
		return coords.HostVersion, nil
	}
	executable, _ := cmd.Flags().GetString("host-executable")
	var cause error
	switch {
	case cmd.Flags().Changed("host-version"):
		cause = errors.New("--host-executable and --host-version are mutually exclusive; supply only one version source")
	case coords.Harness != ir.HarnessClaudeCode:
		cause = errors.New("--host-executable is supported only for --harness claude-code; use --host-version for other harnesses")
	case executable == "":
		cause = errors.New("--host-executable was supplied empty; the generated hook reads the observed, undocumented CLAUDE_CODE_EXECPATH variable, which this host did not supply; provide the absolute Claude executable path or an explicitly observed --host-version")
	case !filepath.IsAbs(executable):
		cause = errors.New("--host-executable must be a complete absolute path; PATH lookup and installation-path version inference are not supported")
	}
	if cause != nil {
		return "", lifecycleHostVersionError(cause)
	}
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
	return fmt.Errorf("resolveLifecycleHostVersion (cmd/pasture/hook_lifecycle_host_version.go): host version resolution failed before capture, admission or storage; no occurrence was recorded; check the explicit executable and its --version output, then retry the hook input: %w", cause)
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
		return nil, fmt.Errorf("start explicit Claude executable with --version (check that it exists and is executable): %w", err)
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
