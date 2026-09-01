package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/codegen/ir"
	"github.com/dayvidpham/pasture/internal/dbconn"
	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	pastureruntime "github.com/dayvidpham/pasture/internal/runtime"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/timeouts"
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

	return runLifecycleHookOn(t, binary, dbPath, "claude-code", event, "2.1.222", payload, env...)
}

// runLifecycleHookOn is runLifecycleHook with the harness and the host version
// open. It exists because the fail-closed REASON has to be read on two
// harnesses: the demotion that made the reason wrong applies to the Claude rows
// and the Codex rows alike, so a proof on one harness alone would not show that
// the fix reaches both.
func runLifecycleHookOn(
	t *testing.T,
	binary, dbPath, harness, event, hostVersion string,
	payload []byte,
	env ...string,
) lifecycleRun {
	t.Helper()

	command := exec.Command(binary,
		databaseFlagName.Argument(), dbPath,
		"hook", "lifecycle",
		"--harness", harness, "--event", event, "--host-version", hostVersion)
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

// TestLifecycleHookExitFollowsTheEffectiveFailureMode is the whole exit contract.
//
// Every case runs the same real storage fault through the built binary and
// changes only two things: the event's EFFECTIVE failure mode and the user's
// fault policy. The exit code follows those two and NOTHING else, which is what
// makes the internal error-category table irrelevant here.
//
// THE EFFECTIVE MODE IS THE ONE THAT REACHES AN EXIT STATUS. The declared mode
// is for EXPLANATION only: the failure-evidence rule demotes an uncited
// blocking row before any exit decision is taken, so a row can declare the
// blocking exit code and still exit 0. Keying this table on the declared mode
// would read as a rule this build does not have.
func TestLifecycleHookExitFollowsTheEffectiveFailureMode(t *testing.T) {
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
		// The EFFECTIVE failure mode of the event decides instead, so one blocks
		// and one does not.
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
			"one fault class, two exit codes: the event's EFFECTIVE mode decides, not the error category")
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

	blocked := lifecycleFault(hookLifecycleCmd, coords, failure, hostexit.FaultFailClosed,
		lifecycleContinuation(coords, failure), hostexit.FaultStageNotRecorded, panicCause)
	assert.Equal(t, hostexit.ExitBlock, blocked.Exit,
		"a panic left the gate unevaluated, so fail-closed must refuse the operation")
	assert.Empty(t, blocked.Stdout, "a panic never produces a native continuation")
	assert.Contains(t, blocked.Stderr, "panicked")

	open := lifecycleFault(hookLifecycleCmd, coords, failure, hostexit.FaultFailOpen,
		lifecycleContinuation(coords, failure), hostexit.FaultStageNotRecorded, panicCause)
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
		lifecycleContinuation(coords, failure), hostexit.FaultStageNotRecorded,
		errors.New("the task store refused the write"))

	assert.Equal(t, hostexit.ExitBlock, outcome.Exit,
		"an unwritable fault record must not change the host outcome")
	assert.Contains(t, outcome.Stderr, "pasture could not evaluate this lifecycle hook event",
		"the fault is still reported on stderr when the record cannot be written")
}

// TestLifecycleHookReturnsInsideItsDeadlineWhileTheDatabaseIsLocked is the
// host-budget proof. A host freezes while it waits for a hook, so pasture must
// stop first, whatever the store is doing.
//
// A second connection holds the SQLite WRITE lock for the whole run. The test
// waits on a CONDITION (the writer signals once the lock is really held) with a
// bounded timeout, and it releases the lock only after the hook process has
// returned. There is no sleep and no fixed-deadline poll anywhere.
//
// THIS TEST RUNS ON THE PRODUCTION TIER AND COSTS REAL SECONDS. That is the
// price of the only end-to-end measurement in this command, and two cheaper
// shapes will suggest themselves to a later reader. Both are refused here by
// MECHANISM, not by preference:
//
//   - "Run it in-process and inject the short tier, like the other deadline
//     proof." The tier is a PARAMETER OF A FUNCTION, and a parameter cannot
//     cross a process boundary, so it can never reach the binary this test
//     runs. Converting the test to reach the parameter would DELETE the
//     process: no build, no start, no separate opener contending for the real
//     lock. The elapsed bounds below would then bound one function call, which
//     is not the thing a host waits for, so the host-budget claim would be
//     gone while the test stayed green. The in-process proof of the same PATH
//     already exists (see the abandoned-after-commit test); this one exists
//     for the CLOCK, and nothing else measures it.
//   - "Add an environment variable or a flag so the binary can be told to use
//     a short tier." An environment variable or a flag is the only way a value
//     can reach a separate process, and creating one makes the tier reachable
//     BY A USER, which is exactly what the parameter's declaration refuses and
//     for the reasons recorded there. There is no version of that change that
//     shortens the tier for this test without also handing the dial to every
//     person running the hook.
//
// So the tier here stays production and the elapsed assertions stay as they
// are. If this test becomes too slow, the answer is to run it less often, not
// to measure something else.
func TestLifecycleHookReturnsInsideItsDeadlineWhileTheDatabaseIsLocked(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lifecycle-cli")
	buildLifecycleBinary(t, binary)

	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	sessionStart := claudeFixture(t, "session_start_2_1_222.json")

	locked := make(chan struct{})
	release := make(chan struct{})
	lockFailed := make(chan error, 1)

	go func() {
		db, err := sql.Open("sqlite", dbconn.SharedDSN(dbPath))
		if err != nil {
			lockFailed <- err
			return
		}
		defer db.Close()

		// BEGIN IMMEDIATE takes the write lock at once, so when this returns
		// the lock is really held and the condition below is true, not merely
		// likely.
		transaction, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			lockFailed <- err
			return
		}
		if _, err := transaction.Exec(
			`CREATE TABLE IF NOT EXISTS lifecycle_hook_deadline_probe (id INTEGER PRIMARY KEY)`); err != nil {
			lockFailed <- err
			return
		}

		close(locked)
		<-release
		_ = transaction.Rollback()
	}()

	select {
	case <-locked:
	case err := <-lockFailed:
		t.Fatalf("could not hold the SQLite write lock: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the SQLite write lock was never taken, so the deadline could not be exercised")
	}

	budget := timeouts.ProductionProfile().HookInvocation()
	started := time.Now()
	run := runLifecycleHook(t, binary, dbPath, "SessionStart", sessionStart)
	elapsed := time.Since(started)
	close(release)

	// The tier plus one process start. The hook's own work is bounded by the
	// tier; building and starting a race-instrumented binary is not.
	const processStartAllowance = 4 * time.Second
	assert.Less(t, elapsed, budget+processStartAllowance,
		"the hook must return inside its %s deadline while the store is locked, so the host is never frozen", budget)

	// The smallest host budget is 10s. Exceeding it is what freezes a session.
	assert.Less(t, elapsed, 10*time.Second,
		"the hook must always return inside the smallest host budget")

	assert.Equal(t, 0, run.ExitCode,
		"a report-and-continue event must let the host continue whatever the store is doing")
	assert.Empty(t, run.Stdout,
		"Claude Code reads an empty body as a proceed, so a fail-open fault writes nothing here")

	// THE PATH, NOT THE CLOCK. The elapsed bounds above are the host-budget
	// claim and nothing more: they hold identically if the deadline select is
	// deleted and the store instead fails FAST. The assertions below are what
	// make this a proof of the MECHANISM.
	//
	// They matter more, not less, now that the retry ceilings below stay as
	// they are. The store opener takes no context, so the deadline never
	// reaches the loop that waits; the goroutine select is the ONLY thing that
	// bounds this invocation today. A test that could not tell the deadline
	// path from a fast store failure would go green on a build where that bound
	// was removed.
	assert.Contains(t, run.Stderr, "stopped waiting at its 5s hook-invocation deadline",
		"the diagnostic must name the DEADLINE path, not merely report a fault")
	assert.Contains(t, run.Stderr, "abandoned the work",
		"the operator must be told the work was abandoned, because that is why the record state is unknown")
	assert.NotContains(t, run.Stderr, "the hook could not evaluate event",
		"that is the wording of the store-error path; if it appears, the store failed fast and this test proved nothing about the deadline")

	records := readFaultRecords(t, run.FaultDir)
	require.Len(t, records, 1, "the fault must be recorded durably")
	assert.Equal(t, "continue", records[0]["hostExit"])
	assert.Equal(t, "fault", records[0]["outcomeClass"])
	assert.Equal(t, "record-unknown", records[0]["faultStage"],
		"an abandoned invocation cannot know whether the occurrence committed, and the record must say so")
	assert.Contains(t, records[0]["cause"], "hook-invocation deadline",
		"the durable record must agree with the host-facing diagnostic about which path ran")
}

// panickingReader is a TEST INPUT, not a production branch. cobra already owns
// the seam: the command reads its host payload through cmd.InOrStdin(), which
// cmd.SetIn replaces. Nothing test-only is compiled into the binary.
type panickingReader struct{ message string }

func (r panickingReader) Read([]byte) (int, error) { panic(r.message) }

// lifecycleTestCommand prepares the PRODUCTION command for one in-process
// invocation and restores every global it touches, so the command a later test
// receives is the one it expects.
func lifecycleTestCommand(t *testing.T, harness, event, version, dbPath string) *cobra.Command {
	t.Helper()

	previousDB := flagDBPath
	flagDBPath = dbPath
	t.Cleanup(func() { flagDBPath = previousDB })

	cmd := hookLifecycleCmd
	for name, value := range map[string]string{"harness": harness, "event": event, "host-version": version} {
		require.NoError(t, cmd.Flags().Set(name, value))
	}
	previousIn, previousOut, previousErr := cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	cmd.SetContext(context.Background())
	t.Cleanup(func() {
		cmd.SetIn(previousIn)
		cmd.SetOut(previousOut)
		cmd.SetErr(previousErr)
		cmd.SetContext(context.Background())
		for _, name := range []string{"harness", "event", "host-version"} {
			_ = cmd.Flags().Set(name, "")
		}
	})
	return cmd
}

// TestAPanicInTheWorkGoroutineIsAFaultAndNeverABlock exercises the recover
// INSIDE the work goroutine, which is the one the outer deferred recover cannot
// reach because it is a different stack.
//
// What it guards against, in the user's terms: if that recover is removed, the
// Go runtime terminates the process with status 2 and prints a stack trace on
// standard error. Claude Code reads exit 2 as a BLOCK and shows standard error
// as the reason, so a pasture crash would arrive at the user as a policy refusal
// whose stated reason is a Go stack trace. That is the fault-versus-decision
// confusion this whole command exists to remove.
//
// The panic is raised by a reader injected through cobra's existing SetIn seam,
// so no production branch exists whose only user is a test.
func TestAPanicInTheWorkGoroutineIsAFaultAndNeverABlock(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	cmd := lifecycleTestCommand(t, "opencode", "tool.execute.before", "1.18.10", dbPath)
	cmd.SetIn(panickingReader{message: "the host payload reader failed"})

	outcome := lifecycleOutcome(cmd, nil, handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile())

	assert.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"a pasture panic must not stop the user working")
	code, known := outcome.Exit.Code()
	require.True(t, known)
	assert.Equal(t, 0, code)
	assert.NotEqual(t, 2, code,
		"exit 2 is a BLOCK on Claude Code, so a panic must never produce it under the fail-open default")
	assert.Equal(t, `{"decision":"proceed"}`, string(outcome.Stdout),
		"a fail-open panic emits this host's continue bytes, or the generated plugin aborts the tool call")
	assert.Contains(t, outcome.Stderr, "panicked")
	assert.Contains(t, outcome.Stderr, "the host payload reader failed",
		"the recovered value names what failed and must survive into the diagnostic")

	records := readFaultRecords(t, dir)
	require.Len(t, records, 1, "a recovered panic is recorded durably")
	assert.Equal(t, "fault", records[0]["outcomeClass"],
		"a panic is a fault, never a decision, whatever bytes the host was given")
	assert.Equal(t, "continue", records[0]["hostExit"])
}

// TestAPanicBeforeTheWorkStartsIsAFaultAndNeverACrash exercises the OTHER
// recover: the deferred one on the main stack.
//
// It is installed FIRST, before the coordinates and the environment are read, so
// a panic in either is a fault. This test reaches it through cobra's SetContext
// seam, which is the same class of test input as SetIn: the command consumes
// cmd.Context() to bound its work, and a nil context makes the standard library
// refuse to derive from it. No production branch exists for this either.
//
// Under the safe defaults that apply before the coordinates are read the fault
// is observe-only and fail-open, which is the weakest claim the command can
// make, so the host continues.
func TestAPanicBeforeTheWorkStartsIsAFaultAndNeverACrash(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.222", dbPath)
	cmd.SetIn(bytes.NewReader(claudeFixture(t, "session_start_2_1_222.json")))
	//nolint:staticcheck // A nil context is the injected fault; cobra owns this seam.
	cmd.SetContext(nil)

	outcome := lifecycleOutcome(cmd, nil, handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile())

	assert.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"the main-path recover must turn a panic into a fault that lets the host continue")
	assert.Contains(t, outcome.Stderr, "panicked")
	assert.Empty(t, outcome.Stdout, "Claude Code reads an empty body as a proceed")

	records := readFaultRecords(t, dir)
	require.Len(t, records, 1)
	assert.Equal(t, "fault", records[0]["outcomeClass"])
	assert.Contains(t, records[0]["cause"], "panicked")
}

// blockingBarrier holds one invocation exactly at the commit-to-emit boundary:
// after the durable receipt is committed, and before the host is told anything.
// It signals when it is reached and waits until the test releases it, so the
// interleaving is DETERMINISTIC and the assertions read on STATE, not on a
// clock.
type blockingBarrier struct {
	reached chan struct{}
	release chan struct{}
}

func (b *blockingBarrier) AfterCommit(context.Context, handlers.CommitBoundary) error {
	close(b.reached)
	<-b.release
	return nil
}

// TestAnInvocationAbandonedAfterItsCommitTellsTheHostTheTruth is the honesty
// proof for the abandonment path.
//
// The hook bounds its own work and abandons it at the deadline. The receipt
// commits BEFORE the native bytes are produced, so an expiry can land AFTER the
// commit. The hook then cannot claim the event was not evaluated, and it used to
// claim exactly that.
//
// The barrier makes that interleaving deterministic: the invocation is held at
// the named commit-to-emit boundary until the deadline fires. The state is then
// read back through the PRODUCTION read path.
//
// This is the FOURTH of the four states an abandoned invocation can land in — a
// committed occurrence with no continuation to the host. The other three, and
// the invariant that no occurrence ever names an absent blob, are proven at the
// commit sequence itself in internal/engine/budget/abandonment_test.go.
func TestAnInvocationAbandonedAfterItsCommitTellsTheHostTheTruth(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.222", dbPath)
	cmd.SetIn(bytes.NewReader(claudeFixture(t, "session_start_2_1_222.json")))

	barrier := &blockingBarrier{reached: make(chan struct{}), release: make(chan struct{})}
	outcomes := make(chan hostexit.Outcome, 1)
	// The INJECTED SHORT TIER. This proof is about the PATH the invocation
	// takes and the STATE it leaves, and neither comes from the length of the
	// tier: the barrier holds the invocation at the commit boundary until the
	// deadline fires, whatever the deadline is. Running it on the production
	// tier would cost five seconds of the suite and prove nothing more. The
	// PRODUCTION value is pinned where it belongs, against the smallest host
	// budget, and it is unchanged.
	budget := timeouts.DeadlineTestProfile()
	go func() { outcomes <- lifecycleOutcome(cmd, nil, barrier, budget) }()

	// The condition, not a sleep: the receipt is committed and the host has not
	// been told anything.
	select {
	case <-barrier.reached:
	case outcome := <-outcomes:
		t.Fatalf("the invocation finished without reaching the commit boundary: %+v", outcome)
	}

	outcome := <-outcomes
	close(barrier.release)

	assert.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"an abandoned invocation fails open, so the host is never stopped by it")
	assert.Empty(t, outcome.Stdout,
		"the host received NO continuation, because the work never reached the encode")
	assert.Contains(t, outcome.Stderr, "hook-invocation deadline",
		"the diagnostic must name the deadline path, not any fault")
	assert.Contains(t, outcome.Stderr, "abandoned the work",
		"and it must say the work was abandoned, which is why the record state is unknown")
	assert.NotContains(t, outcome.Stderr, "the hook could not evaluate event",
		"that is the wording of the store-error path; if it appears, this test proved nothing about the deadline")
	assert.Contains(t, outcome.Stderr, "MAY OR MAY NOT exist",
		"the receipt IS committed here, so claiming the event was not recorded would be false")
	assert.NotContains(t, outcome.Stderr, "no occurrence was recorded for it")

	records := readFaultRecords(t, dir)
	require.Len(t, records, 1)
	assert.Equal(t, "record-unknown", records[0]["faultStage"],
		"the durable record must agree with the host-facing text about what is known")
	assert.Equal(t, "fault", records[0]["outcomeClass"])

	// THE STATE, read back through the production read path rather than by
	// inspecting files: the occurrence IS there, so this run produced the
	// committed-receipt-with-no-stdout outcome and not the no-receipt one.
	var listed bytes.Buffer
	code, err := handlers.HookLifecycleList(context.Background(), &listed,
		handlers.HookLifecycleListInput{DBPath: dbPath, PageSize: 50}, "json")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(listed.Bytes(), &page))
	require.Len(t, page.Items, 1,
		"the receipt committed before the deadline fired, so exactly one occurrence must be readable")
	t.Log("this run produced the COMMITTED-RECEIPT-WITH-NO-CONTINUATION outcome, " +
		"which is the one the barrier makes deterministic")
}

// TestTheRecoverIsInstalledBeforeAnythingElseRuns pins the POSITION of the main
// recover, which no injected input can exercise.
//
// The recover must be the FIRST thing lifecycleOutcome does, so that a panic in
// the coordinate read or in the environment parse is a fault and not a process
// crash. Nothing between the top of the function and the deadline setup has a
// seam a test can make panic, so moving the recover back down would not turn any
// value-driven test red. Reading the function is what catches that, and this is
// the one assertion in this file that is structural for that reason.
func TestTheRecoverIsInstalledBeforeAnythingElseRuns(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "hook_lifecycle.go", nil, 0)
	require.NoError(t, err)

	var body []ast.Stmt
	for _, node := range file.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Name.Name == "lifecycleOutcome" {
			body = function.Body.List
			break
		}
	}
	require.NotEmpty(t, body, "lifecycleOutcome must exist to be the single exit authority")

	deferAt := -1
	for index, statement := range body {
		if _, isDefer := statement.(*ast.DeferStmt); isDefer {
			deferAt = index
			break
		}
	}
	require.NotEqual(t, -1, deferAt, "lifecycleOutcome must install a recover")

	// Only the declaration of the values the recover reads may precede it.
	for index := 0; index < deferAt; index++ {
		declaration, isDeclaration := body[index].(*ast.DeclStmt)
		require.True(t, isDeclaration,
			"statement %d runs BEFORE the recover is installed, so a panic in it would crash the process "+
				"and reach Claude Code as exit 2, which that host reads as a block", index)
		generic, isGeneric := declaration.Decl.(*ast.GenDecl)
		require.True(t, isGeneric && generic.Tok == token.VAR,
			"only the declaration of the values the recover reads may precede it")
	}
}

// TestTheProductionPathWiresThePassThroughBarrierAndTheProductionTier pins the
// two seams this command injects, because both are production parameters whose
// only other supplier today is a test.
//
// That shape is worth one assertion each. A parameter that only a test supplies
// is one refactor away from being a parameter that production supplies
// DIFFERENTLY, and neither drift would fail any existing test:
//
//   - a barrier that is not the pass-through one would run code between the
//     durable commit and the host's continuation, which is the one place this
//     command promises nothing happens;
//   - a tier that is not the production one would silently move the deadline
//     the whole host-budget claim rests on, and the hook would keep passing its
//     own proofs while freezing a session.
//
// The assertion is structural because there is nothing to observe: both seams
// are correct by being wired, and a wrong wiring produces no value a table can
// read.
func TestTheProductionPathWiresThePassThroughBarrierAndTheProductionTier(t *testing.T) {
	t.Parallel()

	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)

	calls := 0
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		require.NoError(t, parseErr, "every production source of this command must be readable")
		ast.Inspect(file, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			function, isIdentifier := call.Fun.(*ast.Ident)
			if !isIdentifier || function.Name != "lifecycleOutcome" {
				return true
			}
			calls++
			require.Len(t, call.Args, 4, "%s calls lifecycleOutcome with the wrong shape", name)
			assert.Equal(t, "handlers.PassThroughCommitBarrier{}", sourceOf(call.Args[2]),
				"%s must pass the pass-through commit barrier: production may never supply a barrier "+
					"that runs between the durable commit and the host's continuation", name)
			assert.Equal(t, "timeouts.ProductionProfile()", sourceOf(call.Args[3]),
				"%s must pass the production timeout profile: the hook-invocation tier is chosen "+
					"against the smallest host budget, and production may not run on another one", name)
			return true
		})
	}
	assert.Equal(t, 1, calls,
		"the command has exactly ONE production entry into the exit authority; a second one is a "+
			"second host-facing path, and every guarantee here is stated over one")
}

// sourceOf renders an expression back to source text, so an assertion can name
// the exact argument a call site must pass.
func sourceOf(node ast.Expr) string {
	var rendered strings.Builder
	if err := printer.Fprint(&rendered, token.NewFileSet(), node); err != nil {
		return ""
	}
	return rendered.String()
}

// codexFixture reads one committed Codex host payload.
func codexFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "internal", "lifecycle", "ingress", "codex", "testdata", "fixtures", name))
	require.NoError(t, err)
	return raw
}

// TestTheFailClosedReasonFollowsTheDeclaredModeThroughTheBuiltBinary is the
// delivery proof of the fail-closed reason.
//
// WHY IT IS A BUILT-BINARY TEST AND NOT A UNIT TABLE. The first version of the
// per-row reason keyed on the EFFECTIVE failure mode, which is the mode left
// after the failure-evidence rule has demoted an uncited blocking row to
// report-and-continue. A unit table proved that arm correct, because a unit
// table can build a fault the production path CANNOT build: an uncited row
// still carrying its blocking mode. On the real binary the evidence arm was
// DEAD, and every declared-blocking gate with no citation read the sentence
// written for a row that can never block. So the assertion has to start where
// the operator does: at the process, with the real profile behind it.
//
// The rows below are the whole shape of the decision, and each is here for a
// reason a reader can check:
//
//   - claude-code PreCompact and codex PreToolUse are DECLARED BLOCKING gates
//     with NO citation. They are the rows the demotion hides. They must read
//     the EVIDENCE reason, which is the one that carries an action: supply the
//     citation and the row becomes able to block.
//   - claude-code PostToolUse is declared NON-BLOCKING. It must read the mode
//     reason, which is true of it and has no action, because no citation can
//     ever make an observation refuse a tool call.
//   - claude-code PreToolUse is declared blocking AND cited. It must still
//     BLOCK, with exit code 2. It is here so that a fix aimed at the wording
//     cannot quietly move the exit table.
//
// MUTATION, and it is the mutation that proves the arm is alive on the
// production path: put panic("MUTATION") inside the evidence arm of
// failClosedAdvice in internal/lifecycle/hostexit. This test turns RED on
// claude-code PreCompact and on codex PreToolUse. Before the declared mode was
// carried, the same panic left the whole built-binary suite GREEN.
//
// SECOND MUTATION: change fault.DeclaredMode.BlocksByExitCode() back to
// fault.Mode.BlocksByExitCode(). The two declared-blocking rows then read the
// mode reason and this test turns RED on both.
//
// THIRD MUTATION: make declaredFailureArm consult the evidence, the way
// evidenceBoundFailure does. The declared mode collapses onto the effective one
// and this test turns RED on the same two rows.
func TestTheFailClosedReasonFollowsTheDeclaredModeThroughTheBuiltBinary(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "lifecycle-cli")
	buildLifecycleBinary(t, binary)

	const (
		evidenceReason = "the event declares the blocking exit code but carries no host evidence for it"
		evidenceAction = "add the host documentation or a committed capture showing the host refuses on that exit code"
		modeReason     = "does not refuse through a process exit code"
	)

	tests := []struct {
		name        string
		harness     string
		event       string
		hostVersion string
		payload     []byte
		// wantDeclared is read from the shipped profile inside the subtest, so
		// the case cannot drift away from the row it claims to cover.
		wantDeclared pastureruntime.FailureMode
		wantEvidence bool
		wantExitCode int
		wantReason   string
		wantAbsent   string
	}{
		{
			name:         "a declared blocking Claude gate with no citation reads the evidence reason",
			harness:      "claude-code",
			event:        "PreCompact",
			hostVersion:  "2.1.222",
			payload:      claudeFixture(t, "pre_compact_2_1_222.json"),
			wantDeclared: pastureruntime.FailureExitTwoBlocks,
			wantExitCode: 0,
			wantReason:   evidenceReason,
			wantAbsent:   modeReason,
		},
		{
			name:         "a declared blocking Codex gate with no citation reads the evidence reason",
			harness:      "codex",
			event:        "PreToolUse",
			hostVersion:  "0.146.0",
			payload:      codexFixture(t, "pre_tool_use_0_146_0.json"),
			wantDeclared: pastureruntime.FailureStrictExitTwoBlocks,
			wantExitCode: 0,
			wantReason:   evidenceReason,
			wantAbsent:   modeReason,
		},
		{
			name:         "a declared non-blocking Claude observation reads the mode reason",
			harness:      "claude-code",
			event:        "PostToolUse",
			hostVersion:  "2.1.222",
			payload:      claudeFixture(t, "post_tool_use_2_1_222.json"),
			wantDeclared: pastureruntime.FailureReportAndContinue,
			wantExitCode: 0,
			wantReason:   modeReason,
			wantAbsent:   evidenceReason,
		},
		{
			name:         "a declared blocking Claude gate WITH a citation still refuses the operation",
			harness:      "claude-code",
			event:        "PreToolUse",
			hostVersion:  "2.1.222",
			payload:      claudeFixture(t, "pre_tool_use_2_1_222.json"),
			wantDeclared: pastureruntime.FailureExitTwoBlocks,
			wantEvidence: true,
			wantExitCode: 2,
			wantReason:   "the host refuses the operation",
			wantAbsent:   "had no channel on this event",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			row, found := pastureruntime.LookupLifecycleFailure(ir.HarnessID(test.harness), test.event)
			require.True(t, found, "the row under test must be declared by this build")
			require.Equal(t, test.wantDeclared, row.DeclaredMode,
				"this case exists for a row with that DECLARED mode; if the profile changed, the case must be re-chosen, not re-labelled")
			require.Equal(t, test.wantEvidence, row.Evidence.IsPresent(),
				"the citation state is half of what chooses the reason, so the case pins it")

			dir := t.TempDir()
			run := runLifecycleHookOn(t, binary, unopenableDatabase(t, dir),
				test.harness, test.event, test.hostVersion, test.payload, hookFailClosedEnv+"=1")

			assert.Equal(t, test.wantExitCode, run.ExitCode,
				"the wording of the reason must never move the exit table")
			assert.Contains(t, run.Stderr, test.wantReason,
				"the operator must read the reason that is TRUE of this row's DECLARATION")
			assert.NotContains(t, run.Stderr, test.wantAbsent,
				"the reason of the other kind of row is false here, and a false reason sends the operator to the wrong place")
			if test.wantReason == evidenceReason {
				assert.Contains(t, run.Stderr, evidenceAction,
					"the evidence reason exists to hand the operator an ACTION; without it the row is only told bad news")
			}
		})
	}
}

// TestTheDiagnosticSeparatesTheDeclaredModeFromTheEffectiveOne pins the phase
// line of the fault diagnostic.
//
// The line used to print ONE mode and call it "declared failure mode", while
// the value was the mode left after the evidence rule. For a demoted row that
// single word was false. Both modes are now named, so a reader can see the
// demotion itself rather than only its result.
//
// MUTATION: remove the "effective failure mode" clause from faultDiagnostic, or
// print fault.Mode again after the word "declared". This test turns RED.
func TestTheDiagnosticSeparatesTheDeclaredModeFromTheEffectiveOne(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "lifecycle-cli")
	buildLifecycleBinary(t, binary)

	dir := t.TempDir()
	run := runLifecycleHookOn(t, binary, unopenableDatabase(t, dir),
		"claude-code", "PreCompact", "2.1.222",
		claudeFixture(t, "pre_compact_2_1_222.json"), hookFailClosedEnv+"=1")

	assert.Contains(t, run.Stderr, "declared failure mode exit-2-blocks",
		"the DECLARED mode of an uncited blocking gate is the blocking one, and the diagnostic must say so")
	assert.Contains(t, run.Stderr, "effective failure mode report-and-continue",
		"the mode the row actually runs as must be named too, or the demotion is invisible")
}

// TestTheFaultRecordTellsADemotedGateFromADeclaredObservation pins the two
// modes in the DURABLE record.
//
// The record outlives the process, and the stderr text does not. A demoted gate
// (claude-code PreCompact declares the blocking exit code and carries no host
// citation) and a declared observation (claude-code PostToolUse) run as the
// SAME effective mode, so with the effective mode alone the two are
// byte-identical in every failure field of the record. They need opposite
// maintainer action, so the record must separate them.
//
// MUTATION: remove the "declaredFailureMode" member from recordLifecycleFault,
// or write failure.Mode into it. This test turns RED.
func TestTheFaultRecordTellsADemotedGateFromADeclaredObservation(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "lifecycle-cli")
	buildLifecycleBinary(t, binary)

	demotedDir := t.TempDir()
	demoted := runLifecycleHookOn(t, binary, unopenableDatabase(t, demotedDir),
		"claude-code", "PreCompact", "2.1.222",
		claudeFixture(t, "pre_compact_2_1_222.json"))
	observationDir := t.TempDir()
	observation := runLifecycleHookOn(t, binary, unopenableDatabase(t, observationDir),
		"claude-code", "PostToolUse", "2.1.222",
		claudeFixture(t, "post_tool_use_2_1_222.json"))

	demotedRecords := readFaultRecords(t, demoted.FaultDir)
	require.Len(t, demotedRecords, 1)
	observationRecords := readFaultRecords(t, observation.FaultDir)
	require.Len(t, observationRecords, 1)

	require.Equal(t, demotedRecords[0]["failureMode"], observationRecords[0]["failureMode"],
		"the two rows run as the same EFFECTIVE mode; that is exactly why the record cannot tell them apart with that field alone")
	assert.Equal(t, "exit-2-blocks", demotedRecords[0]["declaredFailureMode"],
		"a demoted gate declares the blocking exit code, and the record must keep that so a maintainer can see the citation is the missing thing")
	assert.Equal(t, "report-and-continue", observationRecords[0]["declaredFailureMode"],
		"a declared observation never blocks, and the record must say so rather than look like a demoted gate")
}

// TestAFaultThatCannotBeClassifiedNamesEveryInputThatWasNotUsable pins the
// refusal message of the unmappable fault.
//
// Six inputs can produce that refusal. The message used to blame "the declared
// failure mode or the fault policy", which names one of the six and sends the
// reader to the wrong field, and "declared" is now a specific field name.
//
// MUTATION: return a fixed sentence from unclassifiableFaultDiagnostic instead
// of the list, or drop the DeclaredMode arm from hostexit.Fault.UnusableInputs.
// This test turns RED.
func TestAFaultThatCannotBeClassifiedNamesEveryInputThatWasNotUsable(t *testing.T) {
	t.Parallel()

	// Everything is usable EXCEPT the declared mode, which is left unset.
	fault := hostexit.Fault{
		Mode:         pastureruntime.FailureExitTwoBlocks,
		Evidence:     pastureruntime.FailureEvidence{},
		Policy:       hostexit.FaultFailOpen,
		Stage:        hostexit.FaultStageNotRecorded,
		Continuation: hostexit.EmptyContinuation(),
		Cause:        errors.New("the store could not be opened"),
	}
	_, mapped := hostexit.ForFault(fault)
	require.False(t, mapped,
		"a fault with no declared mode has nothing to map, which is the case this message explains")

	text := unclassifiableFaultDiagnostic(
		lifecycleCoordinates{Harness: ir.HarnessClaudeCode, Event: "PreToolUse", HostVersion: "2.1.222"},
		fault.UnusableInputs(), fault.Cause)

	assert.Contains(t, text, "the declared failure mode is unset or not a known mode",
		"the message must name the input that was actually not usable")
	assert.NotContains(t, text, "the fault policy is unset",
		"the message must not name an input that was usable, which is what one field name standing for six conditions did")
	assert.NotContains(t, text, "the declared failure mode or the fault policy was not usable",
		"the retired sentence named one field of six and read as an accusation against the declared mode")
	assert.Contains(t, text, "the store could not be opened",
		"the cause stays verbatim, because it is the only thing that says what really broke")
}

// lifecycleProfileRow names one declared row of one shipped lifecycle profile:
// the harness, and the exact native event name a host puts on the command line.
type lifecycleProfileRow struct {
	harness ir.HarnessID
	event   string
}

// everyShippedLifecycleRow DERIVES the full population of declared rows from
// the exported native event catalogs of the three pinned profiles. It is
// derived and never hand-listed, so a row added by a later profile is walked
// WITHOUT a test edit.
//
// A hand list was the defect this replaces. The walk below named 11 rows of the
// 87 the build ships, so a wrong declared mode on any of the other 76 stayed
// green across the whole repository while the built binary printed a false
// sentence to the operator.
func everyShippedLifecycleRow() []lifecycleProfileRow {
	claude := pastureruntime.ClaudeLifecycleEvents()
	codex := pastureruntime.CodexLifecycleEvents()
	openCode := pastureruntime.OpenCodeLifecycleEvents()

	rows := make([]lifecycleProfileRow, 0, len(claude)+len(codex)+len(openCode))
	for _, event := range claude {
		rows = append(rows, lifecycleProfileRow{harness: ir.HarnessClaudeCode, event: event.NativeName()})
	}
	for _, event := range codex {
		rows = append(rows, lifecycleProfileRow{harness: ir.HarnessCodex, event: event.NativeName()})
	}
	for _, event := range openCode {
		rows = append(rows, lifecycleProfileRow{harness: ir.HarnessOpenCode, event: event.NativeName()})
	}
	return rows
}

// TestEveryDeclaredRowDiffersFromItsEffectiveModeOnlyByTheEvidenceRule walks
// EVERY row of EVERY shipped profile through the PUBLIC lookup. The population
// comes from everyShippedLifecycleRow, which reads the exported event catalogs,
// so the coverage this comment claims is the coverage the walk has. There is NO
// exemption: every derived row must be declared, and every declared row is
// checked.
//
// It is the guard on the new declared mode. A declared mode is only useful
// while it is the mode the row would run as if it were cited; a row that set it
// to anything else would put an arbitrary sentence in front of an operator, and
// no other test would notice, because the effective mode still decides every
// behaviour.
//
// WHAT TURNS EACH ASSERTION RED, one by one. Every line below was measured on
// this tree, because an assertion whose mutation does not reach it proves
// something other than what it claims.
//
//   - the per-row assertions inside the loop: give any row a declared mode that
//     is not its harness arm — for example set declaredFailure to
//     FailureObserveOnly on the claude-code Elicitation row, which no earlier
//     version of this walk reached. RED, naming the row.
//   - require.True(found): make one shipped row unreachable through the public
//     lookup. RED, naming the row. MEASURED on claude-code WorktreeCreate.
//   - the size floor: remove ONE row CONSISTENTLY — the enum member, its native
//     name and its mapping entry — so that the catalog stays self-consistent and
//     the contract still builds. RED with "86 is not greater than or equal to
//     87". This is the ONLY measured mutation that reaches the floor. Cutting
//     ClaudeLifecycleEvents to a single row does NOT: newLifecycleContract
//     already refuses a profile whose event count and mapping count differ, so
//     mustLifecycleContract PANICS and the walk never runs. That run proves the
//     pre-existing refusal, and it would stay RED with the floor line deleted.
//   - the reached-equality: NO PRODUCTION CHANGE CAN TURN IT RED. checked++ is
//     unconditional, and the only abort above it is a require that ends the test
//     first, so checked always equals len(rows) by the time the equality is
//     read. It guards A FUTURE EDIT TO THIS WALK — an exemption, a filter or a
//     "continue" placed before the counter — and its message says so. It is kept
//     rather than deleted because it is the only line that would notice such an
//     edit, and it is documented rather than left to read as a population check
//     the floor already makes.
func TestEveryDeclaredRowDiffersFromItsEffectiveModeOnlyByTheEvidenceRule(t *testing.T) {
	t.Parallel()

	rows := everyShippedLifecycleRow()
	checked := 0
	for _, profileRow := range rows {
		harness, event := profileRow.harness, profileRow.event
		row, found := pastureruntime.LookupLifecycleFailure(harness, event)
		require.True(t, found,
			"%s %s is in the shipped native catalog, so the public lookup must declare it; this walk grants NO exemption", harness, event)
		checked++

		require.True(t, row.DeclaredMode.IsValid(),
			"%s %s carries no declared mode, so its fault diagnostic could not name one", harness, event)
		if row.DeclaredMode == row.Mode {
			continue
		}
		assert.True(t, row.DeclaredMode.BlocksByExitCode(),
			"%s %s runs as something other than it declares, and the ONLY rule allowed to do that is the failure-evidence rule, which acts on an exit-code arm", harness, event)
		assert.False(t, row.Evidence.IsPresent(),
			"%s %s was demoted although it cites evidence, which no rule permits", harness, event)
		assert.Equal(t, pastureruntime.FailureReportAndContinue, row.Mode,
			"%s %s was demoted to something other than report-and-continue", harness, event)
	}
	require.Equal(t, len(rows), checked,
		"the walk itself was edited: it no longer counts one row per derived row. No production "+
			"change can reach this line — the counter is unconditional and the lookup check above "+
			"ends the test first — so it guards a later exemption, filter or early continue added "+
			"inside this loop, which would silently shrink the population every assertion above sees")
	require.GreaterOrEqual(t, checked, 87,
		"the three pinned profiles declare 87 rows between them (30 Claude, 10 Codex, 47 OpenCode); a catalog that returned fewer would pass every assertion above while walking a fraction of the population, which is the defect this size assertion replaces")
}
