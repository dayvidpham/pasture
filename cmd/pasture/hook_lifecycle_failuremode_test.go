package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is the exit contract of the lifecycle hook, proven against the REAL
// built binary.
//
// The defect it locks out: the command used to print its error and return nil,
// so a hook that could not evaluate a gate exited 0 with empty stdout, which
// every host reads as "proceed". A refusal that reads as a proceed is worse
// than no hook at all.

type lifecycleRun struct {
	ExitCode int
	Stdout   string
	Stderr   string
	FaultDir string
}

// runLifecycleHook drives the built binary exactly as a generated host hook
// does: coordinates on the command line, the host payload on stdin, the fault
// policy in the process environment.
func runLifecycleHook(t *testing.T, binary, dbPath, event string, payload []byte, env ...string) lifecycleRun {
	t.Helper()

	command := exec.Command(binary,
		databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle",
		"--harness", "claude-code", "--event", event, "--host-version", "2.1.222")
	command.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.Env = append(os.Environ(), env...)

	code := 0
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit, "the hook must exit with a status, not fail to start")
		code = exit.ExitCode()
	}
	return lifecycleRun{
		ExitCode: code,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		FaultDir: filepath.Dir(dbPath),
	}
}

func claudeFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", name))
	require.NoError(t, err)
	return raw
}

// unopenableDatabase returns a path that is a DIRECTORY, so every attempt to
// open the pasture store fails with a real storage error. It is a real fault of
// the kind a user meets (a wrong path, a broken permission), not a simulated
// one, and the fault record directory beside it stays writable.
func unopenableDatabase(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "not-a-database")
	require.NoError(t, os.Mkdir(path, 0o755))
	return path
}

func readFaultRecords(t *testing.T, dir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, lifecycleFaultRecordFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record), "each fault record is one JSON line")
		records = append(records, record)
	}
	return records
}

// TestLifecycleHookExitFollowsTheDeclaredFailureMode is the whole exit contract.
//
// Every case runs the same real storage fault through the built binary and
// changes only two things: the event's declared failure mode and the user's
// fault policy. The exit code follows those two and NOTHING else, which is what
// makes the internal error-category table irrelevant here.
func TestLifecycleHookExitFollowsTheDeclaredFailureMode(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "lifecycle-cli")
	buildLifecycleBinary(t, binary)

	preToolUse := claudeFixture(t, "pre_tool_use_2_1_222.json")
	sessionStart := claudeFixture(t, "session_start_2_1_222.json")

	// The two events used below carry the two ends of the contract, read from
	// the shipped profile rather than restated here.
	blocking, found := pastureruntime.LookupLifecycleFailure("claude-code", "PreToolUse")
	require.True(t, found)
	require.Equal(t, pastureruntime.FailureExitTwoBlocks, blocking.Mode,
		"PreToolUse is the evidenced blocking gate this contract is built on")
	require.True(t, blocking.Evidence.IsPresent())

	reporting, found := pastureruntime.LookupLifecycleFailure("claude-code", "SessionStart")
	require.True(t, found)
	require.Equal(t, pastureruntime.FailureReportAndContinue, reporting.Mode,
		"SessionStart is the report-and-continue event this contract is built on")

	t.Run("strict gate under fail-closed blocks the host", func(t *testing.T) {
		dir := t.TempDir()
		run := runLifecycleHook(t, binary, unopenableDatabase(t, dir), "PreToolUse", preToolUse,
			hookFailClosedEnv+"=1")

		assert.Equal(t, 2, run.ExitCode,
			"an evidenced blocking gate that could not be evaluated must refuse the tool call under fail-closed")
		assert.Empty(t, run.Stdout,
			"a fault must never write a native continuation, which the host would read as an answer")
		assert.Contains(t, run.Stderr, "pasture could not evaluate this lifecycle hook event")
		assert.Contains(t, run.Stderr, "the host refuses the operation")
	})

	t.Run("strict gate under the default fail-open lets the host continue", func(t *testing.T) {
		dir := t.TempDir()
		run := runLifecycleHook(t, binary, unopenableDatabase(t, dir), "PreToolUse", preToolUse)

		assert.Equal(t, 0, run.ExitCode,
			"the default must not stop a user working because pasture is broken")
		assert.Empty(t, run.Stdout,
			"failing open still means pasture says nothing about an event it did not evaluate")
		assert.Contains(t, run.Stderr, "pasture could not evaluate this lifecycle hook event")
		assert.Contains(t, run.Stderr, "the host continues")

		records := readFaultRecords(t, run.FaultDir)
		require.Len(t, records, 1, "a fault that lets the host continue must still be recorded durably")
		assert.Equal(t, "PreToolUse", records[0]["event"])
		assert.Equal(t, "claude-code", records[0]["harness"])
		assert.Equal(t, "exit-2-blocks", records[0]["failureMode"])
		assert.Equal(t, "fail-open", records[0]["faultPolicy"])
		assert.Equal(t, "continue", records[0]["hostExit"])
		assert.Contains(t, records[0]["cause"], "PreToolUse")
	})

	t.Run("report-and-continue exits zero even under fail-closed", func(t *testing.T) {
		dir := t.TempDir()
		run := runLifecycleHook(t, binary, unopenableDatabase(t, dir), "SessionStart", sessionStart,
			hookFailClosedEnv+"=1")

		assert.Equal(t, 0, run.ExitCode,
			"a report-and-continue event has no blocking exit code to claim, whatever the policy")
		assert.Empty(t, run.Stdout)
		assert.Contains(t, run.Stderr, "pasture could not evaluate this lifecycle hook event")

		records := readFaultRecords(t, run.FaultDir)
		require.Len(t, records, 1)
		assert.Equal(t, "report-and-continue", records[0]["failureMode"])
		assert.Equal(t, "continue", records[0]["hostExit"])
	})

	t.Run("the error category table is bypassed", func(t *testing.T) {
		// The SAME storage fault reaches both events. The internal category
		// table classes it as a connection error, which maps to operator exit
		// code 2. If that table decided this command, BOTH events would exit 2.
		// The declared failure mode decides instead, so one blocks and one does
		// not.
		blockingDir := t.TempDir()
		blockingRun := runLifecycleHook(t, binary, unopenableDatabase(t, blockingDir), "PreToolUse", preToolUse,
			hookFailClosedEnv+"=1")
		reportingDir := t.TempDir()
		reportingRun := runLifecycleHook(t, binary, unopenableDatabase(t, reportingDir), "SessionStart", sessionStart,
			hookFailClosedEnv+"=1")

		require.Contains(t, blockingRun.Stderr, "connection error",
			"both runs must meet the same underlying storage fault")
		require.Contains(t, reportingRun.Stderr, "connection error",
			"both runs must meet the same underlying storage fault")
		assert.Equal(t, 2, blockingRun.ExitCode)
		assert.Equal(t, 0, reportingRun.ExitCode,
			"one fault class, two exit codes: the event's declared mode decides, not the error category")
	})

	t.Run("an event this build does not declare cannot block", func(t *testing.T) {
		dir := t.TempDir()
		run := runLifecycleHook(t, binary, unopenableDatabase(t, dir), "NoSuchEvent", sessionStart,
			hookFailClosedEnv+"=1")

		assert.Equal(t, 0, run.ExitCode,
			"a build that cannot name the event cannot know the host blocks on it, so it must not guess")
		assert.NotEmpty(t, run.Stderr)

		records := readFaultRecords(t, run.FaultDir)
		require.Len(t, records, 1)
		assert.Equal(t, "observe-only", records[0]["failureMode"])
	})

	t.Run("a flag error is a fault and not a silent proceed", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
		initializeLifecycleTestDatabase(t, dbPath)

		command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
			"hook", "lifecycle", "--harness", "claude-code", "--event", "PreToolUse",
			"--host-version", "2.1.222", "--no-such-flag")
		command.Stdin = bytes.NewReader(preToolUse)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		command.Env = append(os.Environ(), hookFailClosedEnv+"=1")

		code := 0
		if err := command.Run(); err != nil {
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			code = exit.ExitCode()
		}

		assert.Equal(t, 2, code,
			"a hook that cannot even parse its flags did not evaluate the gate, so fail-closed must block")
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), "could not parse its flags")
		assert.Contains(t, stderr.String(), "flag error")
	})

	t.Run("positional arguments are a fault and not a silent proceed", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
		initializeLifecycleTestDatabase(t, dbPath)

		command := exec.Command(binary, databaseFlagName.Argument(), dbPath,
			"hook", "lifecycle", "--harness", "claude-code", "--event", "PreToolUse",
			"--host-version", "2.1.222", "unexpected-argument")
		command.Stdin = bytes.NewReader(preToolUse)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		command.Env = append(os.Environ(), hookFailClosedEnv+"=1")

		code := 0
		if err := command.Run(); err != nil {
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			code = exit.ExitCode()
		}

		assert.Equal(t, 2, code)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), "unexpected positional arguments")
	})

	t.Run("a healthy invocation still proceeds", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
		initializeLifecycleTestDatabase(t, dbPath)

		gateRun := runLifecycleHook(t, binary, dbPath, "PreToolUse", preToolUse, hookFailClosedEnv+"=1")
		assert.Equal(t, 0, gateRun.ExitCode, "a healthy gate evaluation must not block the host")
		assert.Equal(t, `{"decision":"proceed"}`, gateRun.Stdout,
			"the native continuation is unchanged by the exit rework")
		assert.Empty(t, gateRun.Stderr)

		observeRun := runLifecycleHook(t, binary, dbPath, "SessionStart", sessionStart)
		assert.Equal(t, 0, observeRun.ExitCode)
		assert.Empty(t, observeRun.Stdout, "an observation writes no continuation")
		assert.Empty(t, observeRun.Stderr)

		assert.Empty(t, readFaultRecords(t, dir), "a healthy invocation records no fault")
	})
}

// TestLifecycleHookPanicBecomesAFault covers the recovered-panic arm of the
// production fault path.
//
// It calls the SAME function the deferred recover in lifecycleOutcome calls, so
// there is one code path and no test-only branch. It is not driven through the
// built binary because no host input can make the binary panic on purpose, and
// adding an input that could would mean shipping a second code path whose only
// user is this test.
func TestLifecycleHookPanicBecomesAFault(t *testing.T) {
	coords := lifecycleCoordinates{Harness: "claude-code", Event: "PreToolUse", HostVersion: "2.1.222"}
	failure := lifecycleFailurePolicy(coords)
	require.Equal(t, pastureruntime.FailureExitTwoBlocks, failure.Mode)

	dir := t.TempDir()
	previous := flagDBPath
	flagDBPath = filepath.Join(dir, "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	panicCause := lifecyclePanicCause(coords, "the store handle was nil")

	blocked := lifecycleFault(hookLifecycleCmd, coords, failure, hostexit.FaultFailClosed, panicCause)
	assert.Equal(t, hostexit.ExitBlock, blocked.Exit,
		"a panic left the gate unevaluated, so fail-closed must refuse the operation")
	assert.Empty(t, blocked.Stdout, "a panic never produces a native continuation")
	assert.Contains(t, blocked.Stderr, "panicked")

	open := lifecycleFault(hookLifecycleCmd, coords, failure, hostexit.FaultFailOpen, panicCause)
	assert.Equal(t, hostexit.ExitContinue, open.Exit,
		"the default must not stop a user working because pasture panicked")
	assert.Contains(t, open.Stderr, "panicked")

	records := readFaultRecords(t, dir)
	require.Len(t, records, 2, "both panics must be recorded durably")
	for _, record := range records {
		assert.Contains(t, record["cause"], "panicked")
		assert.Equal(t, "PreToolUse", record["event"])
	}
}

// TestLifecycleFaultRecordIsBestEffort pins that the durable record can never
// change what the host is told. The record is evidence for a maintainer; a host
// must not be blocked, or released, by pasture's own bookkeeping.
func TestLifecycleFaultRecordIsBestEffort(t *testing.T) {
	coords := lifecycleCoordinates{Harness: "claude-code", Event: "PreToolUse", HostVersion: "2.1.222"}
	failure := lifecycleFailurePolicy(coords)

	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	require.NoError(t, os.WriteFile(blocked, []byte("this is a file, not a directory"), 0o644))

	previous := flagDBPath
	// The record directory is a FILE, so the record cannot be written.
	flagDBPath = filepath.Join(blocked, "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	outcome := lifecycleFault(hookLifecycleCmd, coords, failure, hostexit.FaultFailClosed,
		errors.New("the task store refused the write"))

	assert.Equal(t, hostexit.ExitBlock, outcome.Exit,
		"an unwritable fault record must not change the host outcome")
	assert.Contains(t, outcome.Stderr, "pasture could not evaluate this lifecycle hook event",
		"the fault is still reported on stderr when the record cannot be written")
}
