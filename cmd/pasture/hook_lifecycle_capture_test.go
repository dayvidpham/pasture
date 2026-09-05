package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/lifecycle/registration"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// captureFileName is the file the capture sink writes for one Claude Code
// Notification payload at the recorded host version:
// <harness>_<snake_event>_<version with dots as underscores>.<n>.json.
var captureFileName = "claude-code_notification_" + strings.ReplaceAll(registration.ClaudeCode2_1_210().Version, ".", "_") + ".1.json"

// captureNoticePrefix is the load-bearing phrase of the one notice the hook
// prints when it records a session. It is pinned here on the binary as it is
// pinned on the sink, because this is where an operator reads it.
const captureNoticePrefix = "pasture: capture mode is recording this session to "

// TestCaptureDirectoryRefusalsLeaveTheHostOutcomeUnchanged drives the built
// binary on a WITHHELD Claude event four times: with no capture directory,
// with a relative one, with one inside the repository, and with an accepted
// one outside it. The two refusals must warn on standard error and change
// nothing else: same exit, same empty standard output, no file written. The
// accepted directory must receive the exact bytes the host wrote, print the
// notice exactly once, AND leave the withheld refusal unchanged. A withheld
// event is driven on purpose: the handler refuses it before reading a byte,
// which is exactly the case a capture campaign exists to record.
func TestCaptureDirectoryRefusalsLeaveTheHostOutcomeUnchanged(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	payload := []byte(`{"session_id":"s","hook_event_name":"Notification","message":"hello there"}`)
	run := func(captureDir string) lifecycleRun {
		dbPath := filepath.Join(t.TempDir(), "pasture.db")
		if captureDir == "" {
			return runLifecycleHookOn(t, binary, dbPath, "claude-code", "Notification", registration.ClaudeCode2_1_210().Version, payload)
		}
		return runLifecycleHookOn(t, binary, dbPath, "claude-code", "Notification", registration.ClaudeCode2_1_210().Version, payload, "PASTURE_CAPTURE_DIR="+captureDir)
	}
	base := run("")
	require.Equal(t, 0, base.ExitCode, base.Stderr)
	require.Empty(t, base.Stdout, "a Claude proceed is exit 0 with empty standard output")
	require.Contains(t, base.Stderr, "is withheld")
	require.NotContains(t, base.Stderr, "capture", "without the variable the hook says nothing about capture")

	relative := run("captures")
	assert.Equal(t, base.ExitCode, relative.ExitCode)
	assert.Equal(t, base.Stdout, relative.Stdout)
	assert.Contains(t, relative.Stderr, `PASTURE_CAPTURE_DIR is "captures", which is not an absolute path, so nothing is captured`)
	assert.Contains(t, relative.Stderr, "is withheld", "the event is still evaluated exactly as before")
	assert.NotContains(t, relative.Stderr, captureNoticePrefix)

	inside := filepath.Join(repoRoot, "internal")
	inRepo := run(inside)
	assert.Equal(t, base.ExitCode, inRepo.ExitCode)
	assert.Equal(t, base.Stdout, inRepo.Stdout)
	assert.Contains(t, inRepo.Stderr, "which is inside the repository at")
	assert.NotContains(t, inRepo.Stderr, captureNoticePrefix)
	_, err = os.Stat(filepath.Join(inside, captureFileName))
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing may be written inside the repository")

	outside := t.TempDir()
	accepted := run(outside)
	assert.Equal(t, base.ExitCode, accepted.ExitCode)
	assert.Equal(t, base.Stdout, accepted.Stdout)
	assert.Equal(t, 1, strings.Count(accepted.Stderr, captureNoticePrefix+outside), "the notice is printed exactly once per invocation")
	assert.Contains(t, accepted.Stderr, "is withheld", "a withheld event is captured AND still refused, unchanged")
	captured, err := os.ReadFile(filepath.Join(outside, captureFileName))
	require.NoError(t, err)
	assert.Equal(t, payload, captured, "the capture is the exact bytes the host wrote")
}

// TestACaptureFailureNeverChangesTheHostOutcomeOnAnEnabledEvent drives an
// ENABLED Claude event through the built binary three times: without
// capture, with an accepted directory, and with a directory the hook cannot
// write into. Exit code and standard output must be identical in all three;
// the unwritable directory produces the sink's warning and no notice, and the
// event is still recorded. This is the fail-open condition on the wiring: a
// full disk or a bad permission must not turn a proceed into a fault.
func TestACaptureFailureNeverChangesTheHostOutcomeOnAnEnabledEvent(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so an unwritable directory cannot be arranged")
	}
	binary := lifecycleBinary(t)
	payload := claudeFixture(t, "session_start_2_1_222.json")
	run := func(env ...string) lifecycleRun {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
		initializeLifecycleTestDatabase(t, dbPath)
		return runLifecycleHookOn(t, binary, dbPath, "claude-code", "SessionStart", "2.1.222", payload, env...)
	}
	base := run()
	require.Equal(t, 0, base.ExitCode, base.Stderr)
	require.Empty(t, base.Stdout)
	require.Empty(t, base.Stderr, "an enabled Claude observation says nothing on either stream")

	accepted := run("PASTURE_CAPTURE_DIR=" + t.TempDir())
	assert.Equal(t, base.ExitCode, accepted.ExitCode)
	assert.Equal(t, base.Stdout, accepted.Stdout)
	assert.Equal(t, 1, strings.Count(accepted.Stderr, captureNoticePrefix), "exactly one notice, and nothing else changes")

	unwritable := t.TempDir()
	require.NoError(t, os.Chmod(unwritable, 0o500))
	t.Cleanup(func() { _ = os.Chmod(unwritable, 0o700) })
	failed := run("PASTURE_CAPTURE_DIR=" + unwritable)
	assert.Equal(t, base.ExitCode, failed.ExitCode, "a capture that cannot be written must not change the exit code")
	assert.Equal(t, base.Stdout, failed.Stdout, "a capture that cannot be written must not change the host bytes")
	assert.Contains(t, failed.Stderr, "so this payload was not captured")
	assert.Contains(t, failed.Stderr, "the host is not affected")
	assert.NotContains(t, failed.Stderr, captureNoticePrefix, "a sink that recorded nothing must not claim to be recording")
	assert.NotContains(t, failed.Stderr, "could not evaluate", "the event was evaluated; only the capture failed")
	// On Claude a fail-open fault and a proceed share exit 0 and an empty
	// standard output, so the durable state is what tells them apart: the
	// event must be RECORDED, which a fault would not do.
	listed := runLifecycleList(t, binary, failed.FaultDir+"/"+tasks.DefaultDBFilename.String(), "json")
	assert.Contains(t, listed, `"registrationContract":"`+registration.ClaudeCode2_1_210().Contract.String()+`"`, "the event was recorded although the capture failed")
	assert.Contains(t, listed, `"event":1`, "the recorded occurrence is the SessionStart kind")
}

// TestTheCaptureReadIsGatedOnTheVariableAndSitsInsideTheWork is the source
// pin for the byte-identity proof. It reads cmd/pasture/hook_lifecycle.go and
// requires: exactly ONE call to captureHostPayload; that call inside an `if`
// whose condition reads env.CaptureDir; that `if` inside the function literal
// the work goroutine runs, the one that calls handlers.HookLifecycleNative;
// the handler's Input taken from the `input` variable that the goroutine
// initialises from cmd.InOrStdin() immediately before the `if`; and no other
// read of standard input anywhere in the command's production sources.
//
// With the variable unset the `if` is not entered, so the handler reads
// standard input itself exactly as before capture existed: the ordering and
// the bytes of the host path are those of a build without capture. The
// mutation that turns this RED is moving the read out of the `if`, or out of
// the goroutine, where a stalled stdin would no longer be bounded by the
// invocation deadline.
//
// WHAT IT VISITS: every non-test Go source of this command, found by glob
// and not by a list, so a source added later is read the day it is written.
// WHAT IT DOES NOT READ: the handler package, whose own read of standard
// input on the unset path is pinned by its own tests; and whether the bytes
// are identical to a build without capture, which is proven by running the
// built binary beside a build of the previous release on the same host input
// and comparing exit code, standard output and standard error byte for byte.
func TestTheCaptureReadIsGatedOnTheVariableAndSitsInsideTheWork(t *testing.T) {
	t.Parallel()
	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)

	captureCalls := 0
	stdinReads := 0
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		require.NoError(t, parseErr)
		// Every call to io.ReadAll in production sources of this command: the
		// only one allowed is the capture read inside captureHostPayload.
		ast.Inspect(file, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			if sourceOf(call.Fun) == "io.ReadAll" {
				stdinReads++
				assert.Equal(t, "hook_lifecycle.go", name, "the only standard-input read on the command side is the capture read")
			}
			return true
		})
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Body == nil {
				continue
			}
			if function.Name.Name == "captureHostPayload" {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall || sourceOf(call.Fun) != "captureHostPayload" {
					return true
				}
				captureCalls++
				assert.Equal(t, "lifecycleOutcome", function.Name.Name, "the capture call lives in the one host-facing path")
				enclosing := enclosingChain(function.Body, call)
				var guard *ast.IfStmt
				var literal *ast.FuncLit
				for _, ancestor := range enclosing {
					switch typed := ancestor.(type) {
					case *ast.IfStmt:
						if guard == nil {
							guard = typed
						}
					case *ast.FuncLit:
						if literal == nil {
							literal = typed
						}
					}
				}
				require.NotNil(t, guard, "the capture call must be guarded by an if")
				assert.Contains(t, sourceOf(guard.Cond), "env.CaptureDir", "the guard must read the parsed capture directory; with it unset no capture read may run")
				require.NotNil(t, literal, "the capture call must sit inside the work goroutine's function literal, so a stalled stdin is bounded by the invocation deadline")
				callsNative := false
				ast.Inspect(literal.Body, func(inner ast.Node) bool {
					innerCall, ok := inner.(*ast.CallExpr)
					if ok && sourceOf(innerCall.Fun) == "handlers.HookLifecycleNative" {
						callsNative = true
						for _, element := range innerCall.Args[1].(*ast.CompositeLit).Elts {
							pair, isPair := element.(*ast.KeyValueExpr)
							if isPair && sourceOf(pair.Key) == "Input" {
								assert.Equal(t, "input", sourceOf(pair.Value), "the handler must read the same variable the capture guard may replace")
							}
						}
					}
					return true
				})
				assert.True(t, callsNative, "the function literal holding the capture call must be the one that calls the handler")
				// The variable is initialised from stdin immediately before the guard.
				body := literal.Body.List
				for index, statement := range body {
					if statement == guard {
						require.Greater(t, index, 0)
						assert.Equal(t, "input := cmd.InOrStdin()", sourceOfNode(body[index-1]), "the guard must follow the plain stdin assignment, so the unset case hands the handler stdin itself")
					}
				}
				return true
			})
		}
	}
	assert.Equal(t, 1, captureCalls, "exactly one capture call site exists on the host-facing path")
	assert.Equal(t, 1, stdinReads, "exactly one io.ReadAll exists in the command's production sources, inside captureHostPayload")
}

// sourceOfNode renders any node back to source text.
func sourceOfNode(node ast.Node) string {
	var rendered strings.Builder
	if err := printer.Fprint(&rendered, token.NewFileSet(), node); err != nil {
		return ""
	}
	return rendered.String()
}

// enclosingChain returns the ancestors of target inside root, outermost last.
func enclosingChain(root ast.Node, target ast.Node) []ast.Node {
	var chain []ast.Node
	var stack []ast.Node
	found := false
	ast.Inspect(root, func(node ast.Node) bool {
		if found {
			return false
		}
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if node == target {
			chain = append([]ast.Node(nil), stack...)
			found = true
			return false
		}
		stack = append(stack, node)
		return true
	})
	// Innermost first.
	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}
	return chain
}

// stallCeiling bounds how long the proof waits for the hook process to exit.
// It is a failure ceiling and not a wait: the proof passes only when the
// process returns on its own, and the ceiling only turns a hang into a
// sentence. It is far above the production hook-invocation tier, so a slow
// runner cannot make the proof fail for the wrong reason.
const stallCeiling = 60 * time.Second

// TestAStalledStdinUnderCaptureIsBoundedByTheInvocationDeadline proves
// condition (c) of the wiring on the built binary: with a capture directory
// set and a standard input that NEVER closes, the hook returns on its own
// with the deadline fault, exit 0 and empty standard output, and nothing is
// captured. The condition waited on is the process exit; the only clock is
// the failure ceiling. If the capture read sat outside the work goroutine, no
// deadline could end the read and the process would not return.
//
// It runs the binary and not the in-process seam on purpose: an in-process
// invocation abandoned inside a read leaves its work goroutine alive after the
// test returns, and that goroutine then reads the package's store path while
// the next test writes it.
func TestAStalledStdinUnderCaptureIsBoundedByTheInvocationDeadline(t *testing.T) {
	t.Parallel()
	binary := lifecycleBinary(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	captureDir := t.TempDir()

	// A pipe whose write end is held open for the whole run: the hook can
	// read nothing and sees no end of file until the test closes it, which
	// happens only after the process has exited.
	stdinRead, stdinWrite, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdinWrite.Close(); _ = stdinRead.Close() })

	command := exec.Command(binary,
		databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle",
		"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.222")
	command.Stdin = stdinRead
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.Env = append(os.Environ(), "PASTURE_CAPTURE_DIR="+captureDir)
	require.NoError(t, command.Start())
	_ = stdinRead.Close()

	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	select {
	case waitErr := <-exited:
		code := 0
		if waitErr != nil {
			var exit *exec.ExitError
			require.ErrorAs(t, waitErr, &exit, "the hook must exit with a status, not fail to run")
			code = exit.ExitCode()
		}
		assert.Equal(t, 0, code, "the deadline fault fails open")
		assert.Empty(t, stdout.String(), "a Claude fault carries no bytes on standard output")
		assert.Contains(t, stderr.String(), "hook-invocation deadline", "the outcome is the deadline fault, not a hang and not a capture error")
	case <-time.After(stallCeiling):
		_ = command.Process.Kill()
		t.Fatalf("the hook did not return within %s with a standard input that never closes: the capture read is outside the bounded work", stallCeiling)
	}
	entries, err := os.ReadDir(captureDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a payload that never arrived is not captured")
}
