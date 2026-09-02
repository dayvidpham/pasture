package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

	cmd := lifecycleTestCommand(t, string(coords.Harness), coords.Event, coords.HostVersion, flagDBPath)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	outcome := lifecycleFault(cmd, coords, failure, hostexit.FaultFailClosed,
		lifecycleContinuation(coords, failure), hostexit.FaultStageNotRecorded,
		errors.New("the task store refused the write"))

	assert.Equal(t, hostexit.ExitBlock, outcome.Exit,
		"an unwritable fault record must not change the host outcome")
	assert.Contains(t, outcome.Stderr, "pasture could not evaluate this lifecycle hook event",
		"the fault is still reported on stderr when the record cannot be written")

	// BEST-EFFORT IS NOT SILENT. This is the MkdirAll arm, and it returned
	// without a word while the open failure and the append failure below it
	// both reported. Measured on the built binary with a record directory that
	// is a FILE: exit 0, the host's continue bytes, the fault on standard
	// error, NO record file anywhere and no word that the record was lost --
	// on the very fault ("the database could not be opened") that the record's
	// placement beside the database is justified by.
	//
	// MUTATION: put a bare "return" back in the MkdirAll arm of
	// recordLifecycleFault. This test turns RED.
	text := stderr.String()
	assert.Contains(t, text, "could not create the directory for its fault record",
		"the operator must be TOLD the record was not written; this arm returned in silence "+
			"while the open failure and the append failure of the same writer both reported")
	assert.Contains(t, text, "the fault below is reported on this stream only",
		"every failing arm of this writer ends with the one clause, so the operator reads the "+
			"same sentence for the same loss whichever arm produced it")
	assert.Contains(t, text, blocked,
		"the message must NAME the directory that could not be created, or the operator cannot "+
			"tell which of the two path rules produced it")

	unchanged, readErr := os.ReadFile(blocked)
	require.NoError(t, readErr, "the file standing where the record directory would go must survive")
	assert.Equal(t, "this is a file, not a directory", string(unchanged),
		"nothing may be written where the record directory could not be created")
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

	assert.Contains(t, text, "one input of the fault was not usable: (1) ",
		"the lead-in must agree with the count, because \"these inputs\" in front of a one-item "+
			"list is wrong on the commonest case")
	assert.Contains(t, text, "stops the tool call on its GATE rows",
		"this arm leaves with exit 1, and the generated OpenCode plugin throws on any non-zero "+
			"exit, so a message that claimed only \"the host is not blocked\" was false there; "+
			"the stopping is true of the GATE callbacks only, so the claim must say which rows")
	assert.Contains(t, text, "observation rows catch the same failure and only log it",
		"the generated observation callback CATCHES the throw and logs, so a claim that every "+
			"row is stopped would be false of every observation row this build ships")
}

// TestBothExitOneArmsCarryTheSameNarrowedClaim holds the sweep shut, and holds
// the EXIT of both arms shut with it.
//
// This command has TWO arms that leave with exit 1: the fault the exit authority
// could not classify, and the outcome that named no exit status. One of them was
// corrected away from "the host is not blocked" and the other was not, so the
// retired sentence survived on its sibling with no test anywhere near it. The
// population is enumerated here so a third arm cannot be added silently: both
// arms are named below, and both are asserted.
//
// THE SECOND ARM'S EXIT CODE WAS A BARE LITERAL 1, written outside hostexit,
// which this command made the SOLE exit authority. A reviewer mutated it to 0
// and the whole cmd/pasture package stayed green, while the comment two lines
// above the arm promised it "must never become a silent exit 0" — the exact
// defect this command was written to remove. The message beside that literal had
// already been moved into a function so a test could read it; the integer was
// left where it was. Each arm now NAMES a hostexit.ExitStatus, and this test
// asserts the status and the process code it answers with.
//
// MUTATION: put "the host is not blocked" back into either composer, or drop the
// gate-row qualifier from either. This test turns RED.
// MUTATION: return hostexit.ExitContinue from noExitDecisionExit, which is the
// silent exit 0 the arm's own comment forbids. This test turns RED.
// MUTATION: replace hostexit.ExitNonBlockingError with hostexit.ExitBlock on the
// unclassifiable arm of lifecycleFault. This test turns RED.
func TestBothExitOneArmsCarryTheSameNarrowedClaim(t *testing.T) {
	coords := lifecycleCoordinates{Harness: ir.HarnessOpenCode, Event: "tool.execute.before", HostVersion: "1.18.19"}

	dir := t.TempDir()
	previous := flagDBPath
	flagDBPath = filepath.Join(dir, "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	// The unclassifiable arm is taken through PRODUCTION delivery, not through
	// its composer, so the exit asserted below is the one a caller receives.
	unclassified := lifecycleFault(hookLifecycleCmd, coords,
		pastureruntime.LifecycleFailurePolicy{}, hostexit.FaultPolicyUnset,
		hostexit.Continuation{}, hostexit.FaultStageUnset, nil)

	for _, arm := range []struct {
		name string
		text string
		exit hostexit.ExitStatus
	}{
		{name: "the unclassifiable fault of lifecycleFault", text: unclassified.Stderr, exit: unclassified.Exit},
		{name: "the no-exit-decision arm of emitLifecycleOutcome", text: noExitDecisionDiagnostic(), exit: noExitDecisionExit()},
	} {
		assert.NotContains(t, arm.text, "the host is not blocked",
			"%s leaves with exit 1, and exit 1 is not non-blocking on a host that reads any "+
				"non-zero exit as a broken installation; this sentence was retired on one arm "+
				"and must not survive on the other", arm.name)
		assert.Contains(t, arm.text, "the hook still leaves with exit 1",
			"%s must say which exit the operator is looking at, or the consequence below it "+
				"has nothing to attach to", arm.name)
		assert.Contains(t, arm.text, "stops the tool call on its GATE rows",
			"%s must name the rows the throw actually stops, because the generated observation "+
				"callback catches it and only logs", arm.name)

		assert.Equal(t, hostexit.ExitNonBlockingError, arm.exit,
			"%s must NAME its exit status, and it must be the one its message describes; the "+
				"second arm held a bare literal 1 outside the exit authority, so mutating it to "+
				"a silent exit 0 left every test in this package green", arm.name)
		code, known := arm.exit.Code()
		assert.True(t, known,
			"%s names a declared status, so the exit authority must answer for it; the arm "+
				"discards this result and would exit 0 if it ever became false", arm.name)
		assert.Equal(t, 1, code,
			"%s promises the operator exit 1 in the message asserted above, so any other code makes "+
				"that sentence false; a code of 0 in particular is the silent proceed a host reads "+
				"as an evaluated event that never happened", arm.name)
	}
}

// TestTheFaultRecordSaysSoWhenTheStorePathNamesNoDirectory holds the last silent
// loss of the durable record shut.
//
// When the resolved store path has NO DIRECTORY COMPONENT, the record has
// nowhere to sit beside the database, and the writer RETURNED WITHOUT A WORD —
// while the same writer's two sibling failures, the open and the append, both
// told the operator that the fault was reported on that stream only. This
// sentence used to QUOTE those two arms as saying "the fault above", fifty
// lines above the assertion below that calls that word retired: the word was
// retired from the production text in the same round, so a reader who searched
// for the quotation found it only here, in the description of a state that no
// longer exists. The clause every arm now ends with is faultRecordLossSuffix;
// where this file needs its text it asserts it, and it no longer paraphrases
// it in a comment.
//
// The silence made two SHIPPED sentences false. AGENTS.md tells a maintainer the
// line is appended beside the database, and the record-unknown diagnostic sends
// the reader to that file; on this path neither holds and nothing said so.
//
// THIS TEST COVERS THE FLAG ROUTE ONLY, and its comment used to claim both. The
// arm is reached by "--db pasture.db" and by the documented
// PASTURE_DB_PATH=pasture.db with no flag at all; on the second route flagDBPath
// is EMPTY, so this test cannot tell lifecycleStorePath from flagDBPath and a
// mutation that swapped them stayed green here. The environment route is driven
// through the built binary by
// TestTheFaultRecordRefusalQuotesThePathTheEnvironmentResolvedTo below.
//
// The default store path has a directory and the generated host hooks pass no
// --db, so no user is harmed today; this pins the diagnostic, not a repair of
// the placement, and the host outcome is unchanged.
//
// MUTATION: restore the bare "return" in place of the message in
// recordLifecycleFault. This test turns RED.
func TestTheFaultRecordSaysSoWhenTheStorePathNamesNoDirectory(t *testing.T) {
	coords := lifecycleCoordinates{Harness: ir.HarnessClaudeCode, Event: "PreToolUse", HostVersion: "2.1.222"}

	// A bare file name, which is what "--db pasture.db" resolves to. It names
	// no directory, so lifecycleFaultRecordPath has nowhere to put the record.
	cmd := lifecycleTestCommand(t, string(coords.Harness), coords.Event, coords.HostVersion, "pasture.db")
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	require.Empty(t, lifecycleFaultRecordPath(),
		"this test only covers the silent arm while the path resolves to nothing; a resolved "+
			"path means the placement was repaired and this pin belongs elsewhere")

	failure := lifecycleFailurePolicy(coords)
	outcome := lifecycleFault(cmd, coords, failure, hostexit.FaultFailOpen,
		lifecycleContinuation(coords, failure), hostexit.FaultStageRecordUnknown,
		errors.New("the store could not be opened"))

	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"the host outcome must not change: the record is evidence for a maintainer and never a "+
			"condition of the exit")

	text := stderr.String()
	assert.Contains(t, text, "could not place its fault record",
		"the operator must be TOLD the record was not written; this arm returned in silence "+
			"while its two sibling failures both reported")
	assert.Contains(t, text, "the fault below is reported on this stream only",
		"this is the sentence every failing arm of the writer uses, and it must say BELOW: "+
			"recordLifecycleFault runs inside lifecycleFault while emitLifecycleOutcome writes "+
			"the fault afterwards, so this message is line 1 of stderr and the fault is line 2")
	assert.NotContains(t, text, "the fault above",
		"the retired word sent the operator to a line that had not been written yet")
	assert.Contains(t, text, `"pasture.db"`,
		"the message must QUOTE the resolved store path, because the path comes from either the "+
			"--db flag or the default layout and the operator cannot otherwise tell which")
	assert.Contains(t, text, "PASTURE_DB_PATH",
		"the message must say how to fix it, and the environment variable is the documented way "+
			"in that reaches this arm with no flag at all")

	assert.Empty(t, readFaultRecords(t, "."),
		"nothing may be written to the working directory when the path names no directory")
}

// TestTheUnusableInputListHasAVisibleEnd pins the SHAPE of the multi-input
// list, which no test reached while only the one-item case was covered.
//
// The clauses that follow the list are separated by "; ". While the list was
// joined with the same "; ", the clause after it read as one more item: a
// six-input refusal ended "...; this happened in lifecycleFault", and
// "this happened in lifecycleFault" is not an unusable input. Each item now
// carries its ordinal and the list closes with a full stop.
//
// IT DRIVES THE DELIVERY, NOT THE COMPOSER. The earlier version handed a
// zero-value fault straight to unclassifiableFaultDiagnostic, so it pinned a
// shape lifecycleFault could not produce: the sixth unusable input is a NIL
// CAUSE, and the record writer dereferenced it and panicked before the message
// was ever emitted. A pin on the copy cannot see that. This goes through
// lifecycleFault, so the widest list is pinned on the artefacts a caller
// actually receives — the returned outcome AND the durable line.
//
// MUTATION: join the items with "; " and drop the ordinals, or drop the full
// stop that closes the list. This test turns RED.
// MUTATION: write cause.Error() instead of recordedCause(cause) in
// recordLifecycleFault. This test turns RED with a nil-pointer panic, which is
// the defect it exists to hold shut.
func TestTheUnusableInputListHasAVisibleEnd(t *testing.T) {
	coords := lifecycleCoordinates{Harness: ir.HarnessOpenCode, Event: "tool.execute.before", HostVersion: "1.18.19"}

	dir := t.TempDir()
	previous := flagDBPath
	flagDBPath = filepath.Join(dir, "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	// EVERY member is left at the value that makes it unusable, INCLUDING the
	// nil cause, which is the widest list this message can ever carry.
	require.Len(t, hostexit.Fault{}.UnusableInputs(), 6,
		"the zero-value fault must name all six inputs, or this test no longer covers the widest list")

	outcome := lifecycleFault(hookLifecycleCmd, coords,
		pastureruntime.LifecycleFailurePolicy{}, hostexit.FaultPolicyUnset,
		hostexit.Continuation{}, hostexit.FaultStageUnset, nil)

	require.Equal(t, hostexit.ExitNonBlockingError, outcome.Exit,
		"the delivery must still leave through the refusal arm with exit 1 when the cause is nil; "+
			"the record writer used to panic here, and the top-level recover then exited 0 with no list at all")

	text := outcome.Stderr
	assert.Contains(t, text, "6 inputs of the fault were not usable:",
		"the lead-in must state how many items follow, so the reader can count the list and see it ended")
	for ordinal := 1; ordinal <= 6; ordinal++ {
		assert.Contains(t, text, fmt.Sprintf("(%d) ", ordinal),
			"every item must carry its ordinal, because an unnumbered list joined inside a "+
				"semicolon-separated sentence has no visible end")
	}
	assert.NotContains(t, text, "(7) ",
		"the list must not gain an item; the clause after it is not an unusable input")

	end := "the cause is nil, so there is no fault to report. This happened in lifecycleFault"
	assert.Contains(t, text, end,
		"the last item must be closed by a full stop before the next clause, so that clause "+
			"cannot be read as a seventh item")

	records := readFaultRecords(t, filepath.Dir(flagDBPath))
	require.Len(t, records, 1,
		"the durable line must survive a nil cause; the writer used to panic before it was appended")
	assert.Equal(t, "unset-or-missing", records[0]["cause"],
		"a nil cause must be NAMED in the record, because an empty string there cannot be told "+
			"apart from a cause whose own message was empty")
	reasons, isArray := records[0]["unusableFaultInputs"].([]any)
	require.True(t, isArray, "the refusal reasons must reach the durable line, not only stderr")
	assert.Len(t, reasons, 6, "all six unusable inputs must be recorded, including the nil cause")
}

// TestTheMappableFaultRecordsAnEmptyArrayAndNotNull pins the shape of
// unusableFaultInputs on the arm that is REACHABLE.
//
// The refusal arm is unreachable by construction, so essentially every line
// lifecycle-faults.jsonl will ever hold comes from a fault the exit authority
// COULD map — and on that arm the member was a nil slice, which marshals to
// JSON `null`. `null` cannot be told apart from a member the writer forgot,
// which is the exact ambiguity recordedFailureMode was added one member above
// to remove. `[]` says "asked, and nothing was unusable".
//
// The only assertion on this member used to be inside the UNREACHABLE-arm test,
// so the shape of every real line was pinned by nothing.
//
// MUTATION: restore "var unusable []string" in hostexit.Fault.UnusableInputs.
// This test turns RED, because the member marshals to null and the type
// assertion to []any fails.
func TestTheMappableFaultRecordsAnEmptyArrayAndNotNull(t *testing.T) {
	coords := lifecycleCoordinates{Harness: ir.HarnessClaudeCode, Event: "PreToolUse", HostVersion: "2.1.222"}

	dir := t.TempDir()
	previous := flagDBPath
	flagDBPath = filepath.Join(dir, "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	// Every member is usable, which is what every reachable fault looks like.
	failure := lifecycleFailurePolicy(coords)
	outcome := lifecycleFault(hookLifecycleCmd, coords, failure, hostexit.FaultFailOpen,
		lifecycleContinuation(coords, failure), hostexit.FaultStageNotRecorded,
		errors.New("the store could not be opened"))
	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"this fault must be one the exit authority CAN map, or the test is back on the unreachable arm")

	records := readFaultRecords(t, filepath.Dir(flagDBPath))
	require.Len(t, records, 1)

	value, present := records[0]["unusableFaultInputs"]
	require.True(t, present, "the member must be written on every line, mappable or not")
	reasons, isArray := value.([]any)
	require.True(t, isArray,
		"the member must decode as a JSON ARRAY; null decodes as nil here, and a later reader "+
			"cannot tell null apart from a member the writer forgot")
	assert.Empty(t, reasons,
		"nothing was unusable on a mappable fault, so the array must be empty rather than carry a reason")
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

// TestTheUnmappableFaultRecordSaysWhatStderrSays drives the ONE arm the exit
// authority cannot classify through the real production path, and reads the
// durable record it leaves.
//
// The record outlives the process, and it used to be the weaker of the two
// artefacts on this arm: String() of an invalid failure mode is the empty
// string, so the record wrote "" for the mode and gave no reason at all, while
// stderr named every unusable input. A maintainer who found the file could not
// tell an unusable mode from a member the writer forgot.
//
// MUTATION: return mode.String() unconditionally from recordedFailureMode, or
// drop the unusableFaultInputs member from the record map. This test turns RED.
func TestTheUnmappableFaultRecordSaysWhatStderrSays(t *testing.T) {
	coords := lifecycleCoordinates{Harness: ir.HarnessClaudeCode, Event: "PreToolUse", HostVersion: "2.1.222"}

	dir := t.TempDir()
	previous := flagDBPath
	flagDBPath = filepath.Join(dir, "pasture.db")
	t.Cleanup(func() { flagDBPath = previous })

	// The declared mode is left unset, which is one of the six conditions that
	// make a fault unmappable. Everything else is what a real invocation holds.
	failure := pastureruntime.LifecycleFailurePolicy{Mode: pastureruntime.FailureExitTwoBlocks}
	cause := errors.New("the store could not be opened")

	outcome := lifecycleFault(hookLifecycleCmd, coords, failure, hostexit.FaultFailOpen,
		lifecycleContinuation(coords, failure), hostexit.FaultStageNotRecorded, cause)
	require.Equal(t, hostexit.ExitNonBlockingError, outcome.Exit,
		"an unclassifiable fault must leave through the refusal arm, which is the arm this record describes")

	records := readFaultRecords(t, filepath.Dir(flagDBPath))
	require.Len(t, records, 1, "the refusal arm must still leave exactly one durable line")
	record := records[0]

	assert.Equal(t, "unset-or-unknown", record["declaredFailureMode"],
		"an empty string here cannot be told apart from a member the writer forgot, so the "+
			"record must say WHICH of the two happened")
	assert.Equal(t, "exit-2-blocks", record["failureMode"],
		"the effective mode was usable and must still be recorded verbatim, so this member does "+
			"not become a placeholder for every mode")

	reasons, hasReasons := record["unusableFaultInputs"].([]any)
	require.True(t, hasReasons,
		"the record must carry the refusal reasons, because it is the artefact that outlives the "+
			"process and stderr is not kept anywhere")
	require.Len(t, reasons, 1, "exactly one input was unusable on this fault")
	assert.Equal(t, "the declared failure mode is unset or not a known mode", reasons[0],
		"the record must name the SAME input stderr named, because both come from the function "+
			"the refusal itself asks")
	assert.Contains(t, outcome.Stderr, reasons[0],
		"the two artefacts must not be able to describe different conditions")
}

// TestTheNoExitDecisionArmTakesItsCodeFromTheExitAuthority pins the ARM, and
// not the value the arm asks for.
//
// The previous round moved the arm's bare literal 1 into noExitDecisionExit and
// pinned THAT FUNCTION: mutating its return to ExitContinue is red. But the
// defect was never in the function — it was in the arm. Two reviewers replaced
// the arm's two lines with exitWithCode(0) and THE WHOLE cmd/pasture PACKAGE
// STAYED GREEN, so the silent exit 0 that the arm's own comment forbids was
// reinstated and nothing noticed. The round before that had moved the MESSAGE
// into a function and left the INTEGER unpinned; this round left the ARM.
//
// The assertion is STRUCTURAL for the same reason the two structural pins above
// it are: the arm calls os.Exit and is unreachable by construction today, so no
// value a table can read is produced by it, and a literal there can hold any
// code at all. It follows the precedent set by
// TestTheRecoverIsInstalledBeforeAnythingElseRuns and
// TestTheProductionPathWiresThePassThroughBarrierAndTheProductionTier.
//
// MUTATION, AT THE DEFECT SITE: replace the arm's two lines with
// exitWithCode(0). This test turns RED.
// SECOND MUTATION: replace them with exitWithCode(1). Still RED — a literal
// that happens to be right is the defect, because nothing holds it right.
func TestTheNoExitDecisionArmTakesItsCodeFromTheExitAuthority(t *testing.T) {
	t.Parallel()

	arm := unknownExitArm(t)

	assignments := map[string]string{}
	exits := []ast.Expr{}
	ast.Inspect(arm, func(node ast.Node) bool {
		if assign, isAssign := node.(*ast.AssignStmt); isAssign && len(assign.Lhs) > 0 && len(assign.Rhs) == 1 {
			if name, isIdentifier := assign.Lhs[0].(*ast.Ident); isIdentifier {
				assignments[name.Name] = sourceOf(assign.Rhs[0])
			}
		}
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		if function, isIdentifier := call.Fun.(*ast.Ident); isIdentifier && function.Name == "exitWithCode" {
			require.Len(t, call.Args, 1, "exitWithCode takes exactly one code")
			exits = append(exits, call.Args[0])
		}
		return true
	})

	require.Len(t, exits, 1,
		"the no-exit-decision arm leaves the process exactly once; a second exit here is a second "+
			"host-facing code, and the claim this test makes is stated over one")

	code, isIdentifier := exits[0].(*ast.Ident)
	require.True(t, isIdentifier,
		"the arm's exit code must be a VALUE THE EXIT AUTHORITY ANSWERED FOR, never a literal "+
			"written here: a literal in this arm is the exact defect this command exists to "+
			"remove, and exitWithCode(0) in its place left the whole package green; found %q",
		sourceOf(exits[0]))

	require.Contains(t, assignments, code.Name,
		"the code the arm exits with must be bound in the arm itself, so a reader sees where it "+
			"came from; %q is bound somewhere this test cannot see", code.Name)
	assert.Equal(t, "noExitDecisionExit().Code()", assignments[code.Name],
		"the code must come from noExitDecisionExit, which is inside hostexit, the SOLE exit "+
			"authority of this command; any other source puts an exit of this command back "+
			"outside the authority")
}

// unknownExitArm returns the body of the "the outcome named no exit status"
// branch of emitLifecycleOutcome, which is the arm the test above pins.
func unknownExitArm(t *testing.T) *ast.BlockStmt {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "hook_lifecycle.go", nil, 0)
	require.NoError(t, err, "the production source must be readable beside its test")

	var emit *ast.FuncDecl
	for _, node := range file.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Name.Name == "emitLifecycleOutcome" {
			emit = function
			break
		}
	}
	require.NotNil(t, emit,
		"emitLifecycleOutcome must exist: it is the only writer of this command's host-facing bytes")

	var arm *ast.BlockStmt
	ast.Inspect(emit, func(node ast.Node) bool {
		branch, isBranch := node.(*ast.IfStmt)
		if isBranch && sourceOf(branch.Cond) == "!known" {
			arm = branch.Body
			return false
		}
		return true
	})
	require.NotNil(t, arm,
		"emitLifecycleOutcome must still branch on the outcome naming no exit status; that branch "+
			"is what may never become a silent exit 0")
	return arm
}

// faultWriterArm is one GUARDED BRANCH of recordLifecycleFault, whatever its
// syntactic shape. The enumeration below reads arms of every shape that can
// leave the writer, not `if` statements alone, because the claim it makes is
// about branches and a claim must not be wider than what it reads.
type faultWriterArm struct {
	// Shape names the syntax, so a failure says which branch was found rather
	// than only how many there were.
	Shape string
	// Line is the line the branch opens on, in hook_lifecycle.go.
	Line int
	// Body is the statement list the branch runs.
	Body []ast.Stmt
}

// faultWriterArms returns every guarded branch of one function, in source
// order, covering `if` bodies, `else` blocks, switch cases, select cases and
// loop bodies.
//
// WHY IT READS MORE THAN `if`. The previous enumeration collected *ast.IfStmt
// only, and its message said a sixth arm could not be added in silence. A
// SWITCH-SHAPED silent return keeps the `if` population at five, so the count
// held, every assertion passed, and the sentence was wider than the population
// it read. Reading every branching shape makes the sentence true instead of
// shrinking it to match the guard.
func faultWriterArms(fileSet *token.FileSet, function *ast.FuncDecl) []faultWriterArm {
	arms := []faultWriterArm{}
	add := func(shape string, position token.Pos, body []ast.Stmt) {
		arms = append(arms, faultWriterArm{
			Shape: shape,
			Line:  fileSet.Position(position).Line,
			Body:  body,
		})
	}
	ast.Inspect(function, func(node ast.Node) bool {
		switch branch := node.(type) {
		case *ast.IfStmt:
			add("an if arm", branch.If, branch.Body.List)
			if block, isBlock := branch.Else.(*ast.BlockStmt); isBlock {
				add("an else block", block.Lbrace, block.List)
			}
		case *ast.CaseClause:
			add("a switch case", branch.Case, branch.Body)
		case *ast.CommClause:
			add("a select case", branch.Case, branch.Body)
		case *ast.ForStmt:
			add("a for body", branch.For, branch.Body.List)
		case *ast.RangeStmt:
			add("a range body", branch.For, branch.Body.List)
		}
		return true
	})
	return arms
}

// faultWriterWrite is ONE PLACE the fault writer hands bytes to a stream.
//
// THE POPULATION USED TO BE ONE CALL SHAPE, and that is what let the founding
// defect of this command back in. The previous reader collected a stream only
// for a call whose Fun was a selector on the identifier `fmt` named Fprint,
// Fprintf or Fprintln. Adding ONE LINE beside a correct stderr report,
//
//	cmd.OutOrStdout().Write([]byte("pasture: ...\n"))
//
// — the idiom emitLifecycleOutcome in the SAME FILE uses for stdout — left the
// full, unfiltered cmd/pasture package green together with every
// internal/lifecycle package, while the built binary put 76 bytes on standard
// output AHEAD of {"decision":"proceed"} and the shipped OpenCode plugin's
// JSON.parse threw on a GATE row, which stops the user's tool call.
//
// So the population is now every write of any shape this file can name, and
// the claim is stated over exactly that.
type faultWriterWrite struct {
	// Shape names the call shape, so a failure says how the bytes left.
	Shape string
	// Line is the line in hook_lifecycle.go the write stands on.
	Line int
	// Writer is the writer expression AS WRITTEN, so a message can quote what
	// the maintainer typed.
	Writer string
	// Resolved is Writer, or — when Writer is a bare name bound in this same
	// function — the expression that name was assigned. A maintainer who
	// hoists a writer into a local for readability has changed nothing about
	// the stream, and refusing that with a message about standard output tells
	// them they caused a defect they did not.
	Resolved string
}

// faultWriterWrites returns every write in the statements given.
//
// It recognises the fmt.Fprint family (writer is the first argument), the Write
// and WriteString methods of any writer expression (writer is the receiver),
// and io.WriteString (writer is the first argument). Anything else that could
// reach a stream is caught by the SEPARATE sweep for stream-naming expressions
// below, which needs no list of call shapes at all.
func faultWriterWrites(fileSet *token.FileSet, body []ast.Stmt, bound map[string]string) []faultWriterWrite {
	writes := []faultWriterWrite{}
	record := func(shape string, position token.Pos, writer ast.Expr) {
		text := sourceOf(writer)
		writes = append(writes, faultWriterWrite{
			Shape:    shape,
			Line:     fileSet.Position(position).Line,
			Writer:   text,
			Resolved: resolveWriterName(text, bound),
		})
	}
	for _, statement := range body {
		ast.Inspect(statement, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			selector, isSelector := call.Fun.(*ast.SelectorExpr)
			if !isSelector {
				return true
			}
			if package_, isIdentifier := selector.X.(*ast.Ident); isIdentifier {
				switch package_.Name {
				case "fmt":
					switch selector.Sel.Name {
					case "Fprint", "Fprintf", "Fprintln":
						if len(call.Args) > 0 {
							record("fmt."+selector.Sel.Name, call.Lparen, call.Args[0])
							return false
						}
					}
				case "io":
					if selector.Sel.Name == "WriteString" && len(call.Args) > 0 {
						record("io.WriteString", call.Lparen, call.Args[0])
						return false
					}
				}
			}
			switch selector.Sel.Name {
			case "Write", "WriteString":
				record("a bare ."+selector.Sel.Name+" call", call.Lparen, selector.X)
				return false
			}
			return true
		})
	}
	return writes
}

// resolveWriterName follows a bare name to the expression it was assigned in
// the same function, so that hoisting a writer into a local is not reported as
// a stream change. It stops at a fixed depth, and an unresolved name is
// returned unchanged so the caller refuses it rather than assuming.
func resolveWriterName(text string, bound map[string]string) string {
	for depth := 0; depth < 4; depth++ {
		next, isBound := bound[text]
		if !isBound || next == text {
			return text
		}
		text = next
	}
	return text
}

// faultWriterBindings maps each name a function binds to the source text of
// the single expression it was bound to. It is how a hoisted writer is
// resolved; a name assigned twice, or from a multi-value call, is left OUT, so
// resolveWriterName returns the bare name and the guard refuses it.
func faultWriterBindings(function *ast.FuncDecl) map[string]string {
	bound := map[string]string{}
	ambiguous := map[string]bool{}
	ast.Inspect(function, func(node ast.Node) bool {
		assign, isAssign := node.(*ast.AssignStmt)
		if !isAssign || len(assign.Rhs) != 1 {
			return true
		}
		// EVERY name on the left, not the single-value case alone: `file, err
		// := os.OpenFile(...)` is how the record's own sink is bound, and a
		// reader that skipped it could not tell the record write apart from a
		// report.
		for _, target := range assign.Lhs {
			name, isIdentifier := target.(*ast.Ident)
			if !isIdentifier || name.Name == "_" {
				continue
			}
			if _, seen := bound[name.Name]; seen {
				ambiguous[name.Name] = true
				continue
			}
			bound[name.Name] = sourceOf(assign.Rhs[0])
		}
		return true
	})
	for name := range ambiguous {
		delete(bound, name)
	}
	return bound
}

// faultWriterStream is the ONE writer expression every report of the fault
// writer is handed.
const faultWriterStream = "cmd.ErrOrStderr()"

// forbiddenStreamAccessors are the cobra accessor names that reach a stream
// this function may never touch. They are matched on the SELECTOR NAME ALONE,
// whatever the receiver, so `cmd.OutOrStdout()`, `command.OutOrStdout()` and
// `cmd.Root().OutOrStdout()` are all refused by one rule. The three names are
// distinctive enough that nothing else in this file is called them.
//
// OutOrStderr is here as well although it CAN be standard error: it is the
// command's OUT stream, which merely DEFAULTS to standard error, so a host that
// sets Out redirects it onto standard output without a line of this file
// changing.
var forbiddenStreamAccessors = map[string]string{
	"OutOrStdout": "the command's standard output",
	"OutOrStderr": "the command's OUT stream, which is standard error only until a host sets Out",
	"InOrStdin":   "the command's standard input",
}

// forbiddenProcessStreams are the process handles this function may not use.
// They are matched as WHOLE EXPRESSIONS and not on the selector name, because
// `Stdout` is also an ordinary member name — hostexit.Outcome has one, and the
// record line writes it as the bytes the host was given.
//
// os.Stderr is refused as well as os.Stdout. The reports must go through the
// command's own stream so that a test can capture them; a report written
// straight to the process handle leaves the in-process pins reading nothing.
var forbiddenProcessStreams = map[string]string{
	"os.Stdout": "the process's standard output",
	"os.Stderr": "the process's standard error, which bypasses the command stream the pins read",
	"os.Stdin":  "the process's standard input",
}

// faultWriterForbiddenStreams returns every expression in the function that
// names a stream this writer may not use, whatever it is then done with.
//
// IT NEEDS NO LIST OF CALL SHAPES. faultWriterWrites can only refuse writes it
// recognises; this refuses the STREAM ITSELF, so a shape nobody thought of —
// a bufio.Writer wrapped round it, an encoder constructed on it, a writer
// stored in a struct — is refused where the stream is named, before it can be
// handed anywhere at all.
func faultWriterForbiddenStreams(fileSet *token.FileSet, function *ast.FuncDecl) []string {
	found := []string{}
	ast.Inspect(function, func(node ast.Node) bool {
		selector, isSelector := node.(*ast.SelectorExpr)
		if !isSelector {
			return true
		}
		why, isForbidden := forbiddenStreamAccessors[selector.Sel.Name]
		if !isForbidden {
			why, isForbidden = forbiddenProcessStreams[sourceOf(selector)]
		}
		if !isForbidden {
			return true
		}
		found = append(found, fmt.Sprintf("%s at hook_lifecycle.go:%d, which is %s",
			sourceOf(selector), fileSet.Position(selector.Pos()).Line, why))
		return true
	})
	return found
}

// faultWriterSink names what the bytes of one write reach. The fault writer has
// exactly TWO legitimate sinks and no third: the operator, on the command's
// standard error, and the RECORD ITSELF, on the file this same function opened.
// Anything else is refused.
type faultWriterSink int

const (
	// sinkOperator is the command's standard error, the only stream this
	// function may name.
	sinkOperator faultWriterSink = iota
	// sinkRecord is the file os.OpenFile returned in this function: the one
	// write that is the whole point of the writer rather than a report about
	// it.
	sinkRecord
	// sinkUnknown is every other writer, including one this guard could not
	// resolve. It is refused rather than assumed.
	sinkUnknown
)

// faultRecordSinkPrefix is how the record's own file is recognised: the writer
// expression resolves to the os.OpenFile call THIS FUNCTION made. Naming the
// sink by what opened it, rather than by the local's spelling, means renaming
// the local cannot turn the record write into an unexplained one.
const faultRecordSinkPrefix = "os.OpenFile("

// faultWriterSinkOf classifies one write.
func faultWriterSinkOf(write faultWriterWrite) faultWriterSink {
	switch {
	case write.Resolved == faultWriterStream:
		return sinkOperator
	case strings.HasPrefix(write.Resolved, faultRecordSinkPrefix):
		return sinkRecord
	}
	return sinkUnknown
}

// assertFaultWriterStream requires one write to be handed standard error, and
// says the TRUE thing about the input when it is not.
//
// THE GUARD READS SOURCE TEXT, resolved through the names this same function
// binds. It cannot follow a writer that arrives any other way, and it refuses
// rather than assume — which is the safe direction, but the refusal has to
// state the real requirement. It used to answer a hoisted, behaviour-identical
// `operator := cmd.ErrOrStderr()` with "reports on operator ... operator is not
// that stream", both untrue for that input.
func assertFaultWriterStream(t *testing.T, where string, write faultWriterWrite) {
	t.Helper()

	if faultWriterSinkOf(write) != sinkUnknown {
		return
	}
	quoted := write.Writer
	if write.Resolved != write.Writer {
		quoted = fmt.Sprintf("%s, which this function binds to %s", write.Writer, write.Resolved)
	}
	if strings.Contains(write.Resolved, "OutOrStdout") || strings.Contains(write.Resolved, "os.Stdout") {
		assert.Fail(t, "a fault-writer report was put on standard output",
			"%s: the %s write at hook_lifecycle.go:%d is handed %s. STANDARD OUTPUT CARRIES THE "+
				"HOST'S CONTINUATION BYTES AND NOTHING ELSE; a diagnostic written there lands "+
				"AHEAD of {\"decision\":\"proceed\"}, the generated OpenCode plugin's JSON.parse "+
				"throws \"response is not JSON\", and on a gate callback nothing catches it, so "+
				"the user's tool call is stopped. Hand this write %s instead",
			where, write.Shape, write.Line, quoted, faultWriterStream)
		return
	}
	assert.Fail(t, "a fault-writer report was handed a stream this guard cannot read",
		"%s: the %s write at hook_lifecycle.go:%d is handed %s, and THIS GUARD CANNOT SEE THAT "+
			"IT IS STANDARD ERROR. It reads source text, and resolves a bare name only to the "+
			"expression that name is assigned once in this same function; it follows nothing "+
			"else, and it refuses rather than assume, because standard output here stops the "+
			"user's tool call on an OpenCode gate row. This is NOT a claim that you wrote to "+
			"standard output. If this write is a REPORT, name the stream inline as %s, or bind "+
			"it once in recordLifecycleFault to %s and hand the write that name. If it is the "+
			"RECORD LINE, write it to the file os.OpenFile returned here: wrapping that file in "+
			"a buffer or an encoder moves the write failure to a flush or a close, which is the "+
			"loss this writer exists to report",
		where, write.Shape, write.Line, quoted, faultWriterStream, faultWriterStream)
}

// TestEveryFailingArmOfTheFaultWriterTellsTheOperatorOnStandardError enumerates
// the failure population of recordLifecycleFault and requires each member to
// speak AND to speak ON STANDARD ERROR.
//
// THE STREAM IS THE HOST CONTRACT OF THIS COMMAND. Standard output carries the
// host's continuation bytes and nothing else: on OpenCode the proceed is the
// byte body `{"decision":"proceed"}`, and the generated plugin's acceptProceed
// hands whatever it read to JSON.parse. A diagnostic written on stdout AHEAD of
// those bytes makes the parse throw "response is not JSON", and on a GATE
// callback nothing catches that throw, so the user's tool call is STOPPED —
// which is the founding defect of this slice.
//
// THREE POPULATIONS, NAMED, BECAUSE A STRUCTURAL WITNESS IS ONLY AS WIDE AS THE
// DOMAIN IT IS READ OVER. Each round that narrowed one of these left the
// sentence above it unchanged and wider than the guard:
//
//   - BRANCHES. Every guarded branch must report. The reader collects if arms,
//     else blocks, switch cases, select cases, loop bodies and the bodies of
//     deferred closures. An earlier reader collected *ast.IfStmt alone, so a
//     switch-shaped silent return kept the count at five and the sentence held
//     while the population did not.
//   - WRITES. Every write, WHATEVER THE CALL SHAPE, must reach one of the TWO
//     sinks this writer has and no third: the operator, on cmd.ErrOrStderr(),
//     or the RECORD ITSELF, on the file this same function opened — recognised
//     by resolving the writer to the os.OpenFile call that produced it, so
//     renaming the local cannot disguise a write. The reports are counted
//     against the branch count; the record write is required to happen exactly
//     once. An earlier reader collected fmt.Fprint, Fprintf and Fprintln
//     alone. ONE ADDED LINE in the MkdirAll arm,
//     cmd.OutOrStdout().Write([]byte("pasture: ...")) — the idiom
//     emitLifecycleOutcome uses for stdout in this same file — left the FULL
//     unfiltered cmd/pasture package green while the built binary put 76 bytes
//     on standard output ahead of the proceed and the shipped plugin threw.
//   - STREAM EXPRESSIONS. No expression anywhere in the function may NAME
//     standard output or standard input, whatever is then done with it. This
//     one needs no list of call shapes, so a writer wrapped, encoded onto or
//     stored is refused where the stream is named.
//
// DISCARDED RESULTS ARE A FOURTH POPULATION AND ARE NOT HELD HERE. A loss route
// need not be a branch — the fault writer's close was once a bare
// `defer file.Close()` — and TestTheFaultWriterDiscardsNoResultThatCouldCarryALoss
// is stated over that one.
//
// THE ASSERTION IS STRUCTURAL BECAUSE ONE ARM IS NOT DRIVABLE. The json.Marshal
// arm is unreachable by construction: every member of the record map is a
// string or a slice of them, and encoding/json cannot refuse those. Reading the
// function is what covers it. EVERY OTHER ARM IS DRIVEN ON THE BYTES A HOST
// RECEIVES, through the built binary:
// TestTheFaultRecordRefusalQuotesThePathTheEnvironmentResolvedTo (the
// no-directory arm), TestEveryDrivableFaultRecordLossIsMeasuredOnTheHostBytes
// (the MkdirAll arm, the append arm and the close arm) and
// TestTheFaultRecordOpenFailureIsReportedOnStandardErrorOnly (the open arm).
//
// A RETRACTION, BECAUSE A READER MEETS THE CLAIM HERE. This comment, and the
// commit that wrote it, said the append arm "needs a write to fail on a file
// that opened, which no portable input produces", and exempted it from a byte
// measurement on that ground. THAT WAS WRONG. A symlink at the record path
// pointing at /dev/full opens with O_APPEND|O_CREATE|O_WRONLY and then refuses
// every write with ENOSPC — deterministic, binding on root as well, with no
// loopback filesystem, no quota, no new dependency and no production seam. The
// arm has a byte pin now, and the exemption is withdrawn rather than reworded.
//
// MUTATION, AT THE DEFECT SITE: change cmd.ErrOrStderr() to cmd.OutOrStdout()
// in ANY ONE of the arms. This test turns RED on that arm and again over the
// whole function.
// MUTATION: ADD cmd.OutOrStdout().Write([]byte("leaked\n")) to any arm, leaving
// its correct stderr report in place. This test turns RED: the added write is
// in the write population, and the stream it names is in the forbidden set.
// MUTATION: put a bare "return" back in any arm, of any branching shape. This
// test turns RED.
// MUTATION: rename this test and leave hook_lifecycle.go citing the old name.
// This test turns RED on the citation, which two shipped comments carry.
func TestEveryFailingArmOfTheFaultWriterTellsTheOperatorOnStandardError(t *testing.T) {
	t.Parallel()

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "hook_lifecycle.go", nil, parser.ParseComments)
	require.NoError(t, err, "the production source must be readable beside its test")

	var writer *ast.FuncDecl
	for _, node := range file.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Name.Name == "recordLifecycleFault" {
			writer = function
			break
		}
	}
	require.NotNil(t, writer, "recordLifecycleFault must exist to be the single writer of the record")

	bound := faultWriterBindings(writer)

	// The failure population is exactly the guarded branches of this function:
	// the store path names no directory, the line cannot be encoded, the
	// directory cannot be made, the file cannot be opened, the line cannot be
	// appended, and the file cannot be closed.
	arms := faultWriterArms(fileSet, writer)
	require.Len(t, arms, 6,
		"the failure population of this writer is six guarded branches, counted over if arms, "+
			"else blocks, switch cases, select cases, loop bodies and deferred closure bodies "+
			"alike; a seventh one has been added without this enumeration, which is how the same "+
			"N-1 sweep happened twice")

	// The stream is asserted here, arm by arm, so a failure names the branch.
	for _, arm := range arms {
		reports := 0
		for _, write := range faultWriterWrites(fileSet, arm.Body, bound) {
			assertFaultWriterStream(t,
				fmt.Sprintf("%s at hook_lifecycle.go:%d", arm.Shape, arm.Line), write)
			// A write whose stream this guard could not read still COUNTS AS
			// A WORD: the assertion above has already refused it, and telling
			// the same maintainer that the arm is silent as well would be the
			// second false sentence about one input.
			if faultWriterSinkOf(write) != sinkRecord {
				reports++
			}
		}
		require.NotZero(t, reports,
			"%s at hook_lifecycle.go:%d leaves WITHOUT A WORD: the fault it was recording then "+
				"has no durable record and nothing anywhere says so, which is the loss the "+
				"placement of this file beside the database exists to prevent", arm.Shape, arm.Line)
	}

	// THE SAME CLAIM OVER THE WHOLE FUNCTION, so a write standing OUTSIDE every
	// branching shape above cannot escape it. The arm loop can only see what the
	// enumeration collected; this sees every write the writer makes.
	reports, records := []faultWriterWrite{}, []faultWriterWrite{}
	for _, write := range faultWriterWrites(fileSet, writer.Body.List, bound) {
		assertFaultWriterStream(t, "over the whole of recordLifecycleFault", write)
		if faultWriterSinkOf(write) == sinkRecord {
			records = append(records, write)
			continue
		}
		reports = append(reports, write)
	}
	assert.Len(t, reports, len(arms),
		"this writer reports to the operator once per failing branch and never otherwise; a "+
			"report count that differs from the branch count means a report stands somewhere "+
			"the arm enumeration above cannot read, and the stream claim would not cover it")
	assert.Len(t, records, 1,
		"the record itself is written ONCE, to the file this function opened. A second write to "+
			"that file is a second line for one fault, and a write to any other sink is bytes "+
			"leaving this command by a route nothing here describes")

	// NO EXPRESSION MAY NAME A STREAM THIS WRITER MAY NOT USE. The two loops
	// above can only refuse writes whose call shape is recognised; this refuses
	// the stream itself, wherever it is named and whatever it is handed to.
	assert.Empty(t, faultWriterForbiddenStreams(fileSet, writer),
		"recordLifecycleFault may name NO stream but %s. Every one of these reaches a stream "+
			"whose bytes a host reads as the hook's answer, or reads the host's input, and a "+
			"diagnostic that lands on standard output ahead of {\"decision\":\"proceed\"} stops "+
			"the user's tool call on an OpenCode gate row", faultWriterStream)

	// THE PRODUCTION SOURCE CITES THIS TEST BY NAME, TWICE, and nothing held
	// either citation: renaming the guard in this file alone left the FULL
	// unfiltered package green in 199s with two shipped comments pointing at a
	// test that does not exist. The match is the WHOLE identifier, anchored on
	// word boundaries, so a PREFIX rename cannot satisfy it either — the same
	// pin internal/lifecycle/hostexit/hostexit_test.go carries.
	source, readErr := os.ReadFile("hook_lifecycle.go")
	require.NoError(t, readErr, "the production source must be readable beside its test")
	assert.Regexp(t, regexp.MustCompile(`\b`+regexp.QuoteMeta(t.Name())+`\b`), string(source),
		"hook_lifecycle.go names the guard that holds its fault writer shut, and this run is "+
			"that guard; a citation of a test that does not exist sends a maintainer looking "+
			"for something that is not there. The name must appear WHOLE: a citation of a "+
			"LONGER identifier that merely starts with this name is a citation of something else")
}

// TestTheFaultWriterDiscardsNoResultThatCouldCarryALoss is stated over a
// population that is NOT branches: the results this function throws away.
//
// WHY IT EXISTS. The enumeration above reads guarded branches, and a route that
// loses the record need not be one. hook_lifecycle.go carried `defer
// file.Close()` with its error discarded: a filesystem that defers the write
// reports the full disk at close(2), the append arm never fires, and the record
// is lost with NO WORD ON ANY STREAM. The arm count stayed at five, the write
// count stayed equal to it, every value pin passed, and the whole tree was
// green — twice measured, by two reviewers, on two separate probes. One of them
// added `_, _ = file.Write([]byte("partial"))` to the writer and the package
// was ok in 0.673s.
//
// WHAT IT REFUSES. A call statement that is not a report; a deferred or `go`
// call that is not a closure whose body the enumeration above can read; an
// assignment that binds the LAST result — which is where Go puts the error — to
// the blank identifier.
//
// THE ONE EXEMPTION, STATED. fmt.Fprint, Fprintf and Fprintln as statements.
// Their error is discarded because the report IS the last channel this writer
// has: there is nowhere left to report a failure to report.
//
// MUTATION: put `defer file.Close()` back, or add `_, _ = file.Write(...)`, or
// add any bare call statement to recordLifecycleFault. This test turns RED.
func TestTheFaultWriterDiscardsNoResultThatCouldCarryALoss(t *testing.T) {
	t.Parallel()

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "hook_lifecycle.go", nil, 0)
	require.NoError(t, err, "the production source must be readable beside its test")

	var writer *ast.FuncDecl
	for _, node := range file.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Name.Name == "recordLifecycleFault" {
			writer = function
			break
		}
	}
	require.NotNil(t, writer, "recordLifecycleFault must exist to be the single writer of the record")

	discards := []string{}
	note := func(position token.Pos, what string) {
		discards = append(discards, fmt.Sprintf("hook_lifecycle.go:%d %s",
			fileSet.Position(position).Line, what))
	}
	isReport := func(call *ast.CallExpr) bool {
		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		if !isSelector {
			return false
		}
		package_, isIdentifier := selector.X.(*ast.Ident)
		if !isIdentifier || package_.Name != "fmt" {
			return false
		}
		switch selector.Sel.Name {
		case "Fprint", "Fprintf", "Fprintln":
			return true
		}
		return false
	}
	ast.Inspect(writer, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.ExprStmt:
			call, isCall := statement.X.(*ast.CallExpr)
			if isCall && !isReport(call) {
				note(call.Pos(), "throws away the result of "+sourceOf(call.Fun)+
					", which is where a failure to write this record would be reported")
			}
		case *ast.DeferStmt:
			if _, isClosure := statement.Call.Fun.(*ast.FuncLit); !isClosure {
				note(statement.Defer, "defers "+sourceOf(statement.Call.Fun)+
					" and throws away its result; a deferred close or flush is exactly where a "+
					"filesystem that defers the write reports the full disk, so a discarded one "+
					"is a record lost in silence")
			}
		case *ast.GoStmt:
			if _, isClosure := statement.Call.Fun.(*ast.FuncLit); !isClosure {
				note(statement.Go, "starts "+sourceOf(statement.Call.Fun)+
					" and throws away its result")
			}
		case *ast.AssignStmt:
			if len(statement.Rhs) != 1 || len(statement.Lhs) == 0 {
				return true
			}
			if _, isCall := statement.Rhs[0].(*ast.CallExpr); !isCall {
				return true
			}
			last, isIdentifier := statement.Lhs[len(statement.Lhs)-1].(*ast.Ident)
			if isIdentifier && last.Name == "_" {
				note(statement.TokPos, "binds the LAST result of "+
					sourceOf(statement.Rhs[0])+" to the blank identifier, and the last result "+
					"is where Go puts the error")
			}
		}
		return true
	})

	assert.Empty(t, discards,
		"recordLifecycleFault may throw away NO result that could carry a lost record. Every "+
			"route that loses the record must tell the operator on standard error, and a "+
			"discarded error is a route with no branch, which the arm enumeration cannot see "+
			"and no value pin reads. Check the result and report it with faultRecordLossSuffix "+
			"like its siblings, or, if it genuinely cannot fail, bind it and say so where it is "+
			"bound. The one exemption is fmt.Fprint, Fprintf and Fprintln, whose error has no "+
			"channel left")
}

// TestEveryTestNameCitedByTheLifecycleSourceExists holds every citation
// hook_lifecycle.go makes to a test.
//
// A TEST NAME WRITTEN INTO SHIPPED SOURCE IS A PROMISE THAT THE GUARD EXISTS.
// Nothing held those promises: renaming a guard in the test file alone left the
// full, unfiltered cmd/pasture package green in 199s while two production
// comments sent a maintainer to a test that had gone. The worker of that round
// had to hand-edit both citations, and the commit message says so — which is
// itself the proof that no guard was standing over them.
//
// MUTATION: rename any test hook_lifecycle.go names, or misspell a citation.
// This test turns RED and names the missing one.
func TestEveryTestNameCitedByTheLifecycleSourceExists(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("hook_lifecycle.go")
	require.NoError(t, err, "the production source must be readable beside its test")

	declared := map[string]bool{}
	entries, err := os.ReadDir(".")
	require.NoError(t, err, "the package directory must be readable to find the tests it declares")
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		require.NoError(t, parseErr, "every test file of this package must parse")
		for _, node := range parsed.Decls {
			if function, isFunction := node.(*ast.FuncDecl); isFunction {
				declared[function.Name.Name] = true
			}
		}
	}
	require.NotEmpty(t, declared,
		"this test reads the package's own test files; finding none means it is looking in the "+
			"wrong directory and would pass vacuously")

	cited := regexp.MustCompile(`\bTest[A-Za-z0-9_]+`).FindAllString(string(source), -1)
	require.NotEmpty(t, cited,
		"hook_lifecycle.go cites the guards that hold its claims; finding no citation at all "+
			"means either the pointers were deleted or this pattern no longer matches them, and "+
			"an empty population makes the assertion below vacuous")

	missing := []string{}
	for _, name := range cited {
		if !declared[name] && !alreadyListed(missing, name) {
			missing = append(missing, name)
		}
	}
	assert.Empty(t, missing,
		"hook_lifecycle.go names these tests and this package declares none of them. A citation "+
			"of a test that does not exist sends the next maintainer looking for a guard that is "+
			"not there, and it is written in SHIPPED SOURCE where they will believe it. Rename "+
			"the citation with the test, or delete it")
}

// alreadyListed reports whether a string is already in a slice. It keeps the report
// above free of duplicates when one stale citation is written twice.
func alreadyListed(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestTheFaultRecordRefusalQuotesThePathTheEnvironmentResolvedTo drives the
// DOCUMENTED ENVIRONMENT ROUTE into the no-directory refusal, through the built
// binary, with no --db flag at all.
//
// WHY IT IS A BUILT-BINARY TEST. The refusal must quote lifecycleStorePath and
// not flagDBPath, and on the flag route the two are the same string, so the
// in-process pin cannot tell them apart: quoting flagDBPath there left the full,
// unfiltered package green. On this route the flag is EMPTY, and the mutant
// prints `the store path "" names no directory` — an operator told that the
// empty path names no directory learns nothing at all, which is the exact
// ambiguity lifecycleStorePath was separated out to remove.
//
// The store path is a bare name, so it names no directory AND it is a directory
// on disk, which makes the store unopenable. One input therefore produces both
// the fault and the refusal, with nothing simulated.
//
// MUTATION, AT THE DEFECT SITE: quote flagDBPath instead of lifecycleStorePath()
// in the no-directory arm of recordLifecycleFault. This test turns RED.
func TestTheFaultRecordRefusalQuotesThePathTheEnvironmentResolvedTo(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "lifecycle-cli")
	buildLifecycleBinary(t, binary)

	// The working directory of the hook, so a bare store path resolves inside
	// the test and never beside the package source.
	work := t.TempDir()
	const bareStorePath = "not-a-database"
	require.NoError(t, os.Mkdir(filepath.Join(work, bareStorePath), 0o755),
		"the store path must be a directory, so opening it as a database is a real storage fault")

	command := exec.Command(binary, "hook", "lifecycle",
		"--harness", "claude-code", "--event", "PreToolUse", "--host-version", "2.1.222")
	command.Dir = work
	command.Stdin = bytes.NewReader(claudeFixture(t, "pre_tool_use_2_1_222.json"))
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.Env = append(os.Environ(), tasks.DBPathEnv+"="+bareStorePath)

	exitCode := 0
	if runErr := command.Run(); runErr != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, runErr, &exit, "the hook must exit with a status, not fail to start")
		exitCode = exit.ExitCode()
	}

	require.Equal(t, 0, exitCode,
		"this route is fail-open, and the record is evidence for a maintainer and never a "+
			"condition of the host outcome")

	text := stderr.String()
	require.Contains(t, text, "could not place its fault record",
		"the environment route must reach the same refusal the flag route reaches; if it does "+
			"not, this test proves nothing about which path is quoted")
	assert.Contains(t, text, `the store path "`+bareStorePath+`" names no directory`,
		"the refusal must quote the path the ENVIRONMENT resolved to; flagDBPath is empty on this "+
			"route, and quoting it prints `the store path \"\" names no directory`, which names "+
			"nothing the operator can act on")
	assert.NotContains(t, text, `the store path "" names no directory`,
		"an empty quoted path is the mutant this test exists to catch")

	assert.Empty(t, stdout.String(),
		"standard output carries the host's continuation bytes and NOTHING else, and Claude "+
			"Code's continuation is the empty body. THIS TEST ALREADY CAPTURED STDOUT AND NEVER "+
			"READ IT, so the arm it drives had no byte pin at all: an added "+
			"cmd.OutOrStdout().Write in this arm left the full package green while the built "+
			"binary put the diagnostic ahead of {\"decision\":\"proceed\"}, where the generated "+
			"OpenCode plugin's JSON.parse throws and a gate callback stops the user's tool call")

	_, statErr := os.Stat(filepath.Join(work, lifecycleFaultRecordFile))
	assert.True(t, os.IsNotExist(statErr),
		"nothing may be written to the working directory when the store path names no directory")
}

// TestTheFaultRecordOpenFailureIsReportedOnStandardErrorOnly measures the OPEN
// arm of recordLifecycleFault on the bytes a host actually receives.
//
// WHY THIS ARM, AND WHY THROUGH THE BINARY. The open failure and the append
// failure were the two arms of the writer with NO VALUE PIN AT ALL. Nothing
// anywhere read what they wrote or which stream they wrote it on, so changing
// ONE identifier in either — cmd.ErrOrStderr() to cmd.OutOrStdout() — left the
// full, unfiltered cmd/pasture package green together with every
// internal/lifecycle package. The append arm needs a write to fail on a file
// that already opened, which no portable input produces, so the structural
// enumeration covers that one; this arm IS drivable, and a drivable arm should
// be measured on bytes rather than on syntax.
//
// STANDARD OUTPUT IS THIS COMMAND'S HOST CONTRACT AND CARRIES NOTHING ELSE.
// Claude Code's continue bytes are the EMPTY BODY, so on this row every byte on
// stdout is a violation and assert.Empty is the whole contract with nothing to
// exempt. The harm is READ elsewhere: on OpenCode the continue bytes are
// {"decision":"proceed"}, a diagnostic printed ahead of them makes the
// generated plugin's JSON.parse throw "response is not JSON", and a GATE
// callback has nothing that catches that throw, so the user's tool call is
// stopped. That is the founding defect of this slice, and one identifier
// reinstates it.
//
// THE INPUT IS REAL AND ROOT-PROOF. The store path is a DIRECTORY, so opening
// the pasture store is a genuine storage fault; the record's own path is also a
// DIRECTORY, so MkdirAll on its parent succeeds and os.OpenFile fails with
// EISDIR. A permission bit would have done it too, but permission bits do not
// bind a root user and this must fail the same way wherever it runs.
//
// MUTATION, AT THE DEFECT SITE: change cmd.ErrOrStderr() to cmd.OutOrStdout()
// in the open arm of recordLifecycleFault. This test turns RED on BOTH the
// stderr assertion, because the diagnostic left that stream, AND the stdout
// assertion, because it arrived on this one.
//
// THAT SENTENCE USED TO NAME THE STDOUT ASSERTION ALONE AND WAS FALSE. The
// stderr check above it was a require, so the mutation called FailNow there and
// the stdout assertion never ran: the only byte-level stdout pin of its round
// was not evaluated by the mutation cited to justify it. The check is an assert
// now, and the mutation was re-run to confirm it reaches both.
//
// MUTATION: put a bare "return" in that arm. This test turns RED on the stderr
// assertion.
// MUTATION: ADD one stdout write to that arm, leaving its stderr report in
// place. This test turns RED on the stdout assertion alone, which is the case
// no stream-change mutation can reach.
func TestTheFaultRecordOpenFailureIsReportedOnStandardErrorOnly(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "lifecycle-cli")
	buildLifecycleBinary(t, binary)

	store := t.TempDir()
	database := filepath.Join(store, "not-a-database")
	require.NoError(t, os.Mkdir(database, 0o755),
		"the store path must be a directory, so opening it as a database is a real storage fault")
	require.NoError(t, os.Mkdir(filepath.Join(store, lifecycleFaultRecordFile), 0o755),
		"the record's own path must be a directory, so its parent can be made and the file "+
			"itself cannot be opened, on any user including root")

	run := runLifecycleHook(t, binary, database, "PreToolUse",
		claudeFixture(t, "pre_tool_use_2_1_222.json"))

	require.Equal(t, 0, run.ExitCode,
		"this route is fail-open: the record is evidence for a maintainer and never a condition "+
			"of the host outcome")
	// ASSERT AND NOT REQUIRE, ON PURPOSE. This was a require, so the documented
	// mutation — cmd.ErrOrStderr() to cmd.OutOrStdout() in the open arm — failed
	// HERE and called FailNow, and the stdout assertion below, the one byte-level
	// stdout pin this test exists for, WAS NEVER EVALUATED. The cited mutation
	// must reach the assertion it is cited for; run continues so that both the
	// missing stderr and the leaked stdout are reported from one run.
	assert.Contains(t, run.Stderr, "could not open its fault record",
		"the open arm must reach its own diagnostic; if it does not, this test proves nothing "+
			"about which stream that diagnostic uses")
	assert.Contains(t, run.Stderr, faultRecordLossSuffix,
		"every failing arm of this writer ends with the one clause, so the operator reads the "+
			"same sentence for the same loss whichever arm produced it")

	assert.Empty(t, run.Stdout,
		"standard output carries the host's continuation bytes and NOTHING else, and Claude "+
			"Code's continuation is the empty body. A diagnostic written here would arrive on "+
			"OpenCode ahead of {\"decision\":\"proceed\"}, where the generated plugin's "+
			"JSON.parse throws and a gate callback stops the user's tool call")
}

// devFull is the path of the character device that accepts an open and then
// refuses every write with ENOSPC. It is what makes the append arm of the fault
// writer drivable with no loopback filesystem, no quota and no production seam.
const devFull = "/dev/full"

// TestEveryDrivableFaultRecordLossIsMeasuredOnTheHostBytes drives the two
// remaining reachable arms of recordLifecycleFault through the BUILT BINARY and
// reads the bytes a host receives.
//
// WHY IT EXISTS. Of the writer's reachable arms, only the open arm was measured
// on bytes. The MkdirAll arm was pinned in process, on outcome.Stderr alone, so
// an ADDED cmd.OutOrStdout().Write beside its correct stderr report left the
// full, unfiltered cmd/pasture package green while the built binary put 76
// bytes on standard output ahead of {"decision":"proceed"} and the shipped
// OpenCode plugin's JSON.parse threw on a gate row. The append arm was exempted
// from a byte measurement altogether, on the ground that no portable input
// produces a write failure on a file that opened. THE EXEMPTION RESTED ON A
// FALSE PREMISE and it is withdrawn here rather than reworded.
//
// THE INPUTS ARE REAL AND BIND ROOT AS WELL.
//   - The directory route: a --db path whose parent component is a FILE, so
//     os.MkdirAll fails with ENOTDIR. The same path makes the store unopenable,
//     so one input produces both the fault and the loss.
//   - The append route: a SYMLINK at the record path pointing at /dev/full.
//     os.OpenFile follows it and succeeds under O_APPEND|O_CREATE|O_WRONLY, its
//     parent directory already exists so MkdirAll succeeds, and then every
//     write returns ENOSPC. Nothing is simulated and nothing is mocked.
//
// The close arm is NOT driven here: /dev/full accepts the close, and a route
// that reports at close(2) needs a filesystem this suite does not have. It is
// held by the branch enumeration and by
// TestTheFaultWriterDiscardsNoResultThatCouldCarryALoss, and that limit is
// stated rather than dressed up as coverage.
//
// MUTATION, AT THE DEFECT SITE: change cmd.ErrOrStderr() to cmd.OutOrStdout()
// in either arm. That subtest turns RED on BOTH its stderr assertion and its
// stdout assertion. MUTATION: ADD a stdout write to either arm, leaving its
// stderr report in place. That subtest turns RED on the stdout assertion.
// MUTATION: put a bare "return" in either arm. That subtest turns RED on the
// stderr assertion.
func TestEveryDrivableFaultRecordLossIsMeasuredOnTheHostBytes(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "lifecycle-cli")
	buildLifecycleBinary(t, binary)

	t.Run("the directory for the record cannot be made", func(t *testing.T) {
		store := t.TempDir()
		blocker := filepath.Join(store, "afile")
		require.NoError(t, os.WriteFile(blocker, []byte("not a directory\n"), 0o644),
			"a parent component of the store path must be a FILE, so the record's directory "+
				"cannot be created and opening the store is a real fault as well")

		run := runLifecycleHook(t, binary, filepath.Join(blocker, "sub", "pasture.db"),
			"PreToolUse", claudeFixture(t, "pre_tool_use_2_1_222.json"))

		assert.Equal(t, 0, run.ExitCode,
			"this route is fail-open: the record is evidence for a maintainer and never a "+
				"condition of the host outcome")
		assert.Contains(t, run.Stderr, "could not create the directory for its fault record",
			"the MkdirAll arm must reach its own diagnostic; if it does not, this subtest "+
				"proves nothing about which stream that diagnostic uses")
		assert.Contains(t, run.Stderr, faultRecordLossSuffix,
			"every route that loses the record ends with the one clause, so the operator reads "+
				"the same sentence for the same loss whichever route produced it")
		assert.Empty(t, run.Stdout,
			"standard output carries the host's continuation bytes and NOTHING else, and "+
				"Claude Code's continuation is the empty body. This arm had no byte pin at all: "+
				"one added cmd.OutOrStdout().Write here left the whole tree green while the "+
				"built binary put the diagnostic ahead of {\"decision\":\"proceed\"}, where the "+
				"generated OpenCode plugin's JSON.parse throws and a gate callback stops the "+
				"user's tool call")
	})

	t.Run("the line cannot be appended to a file that opened", func(t *testing.T) {
		probe, probeErr := os.OpenFile(devFull, os.O_WRONLY, 0)
		if probeErr != nil {
			t.Skipf("this platform has no writable %s, which is what makes a real ENOSPC "+
				"available without a loopback filesystem or a quota: %v", devFull, probeErr)
		}
		require.NoError(t, probe.Close())

		store := t.TempDir()
		database := filepath.Join(store, "not-a-database")
		require.NoError(t, os.Mkdir(database, 0o755),
			"the store path must be a directory, so opening it as a database is a real "+
				"storage fault")
		require.NoError(t, os.Symlink(devFull, filepath.Join(store, lifecycleFaultRecordFile)),
			"the record path must resolve to a device that opens and then refuses every write, "+
				"which is the append failure driven with nothing simulated")

		run := runLifecycleHook(t, binary, database, "PreToolUse",
			claudeFixture(t, "pre_tool_use_2_1_222.json"))

		assert.Equal(t, 0, run.ExitCode,
			"this route is fail-open: the record is evidence for a maintainer and never a "+
				"condition of the host outcome")
		assert.Contains(t, run.Stderr, "could not append to its fault record",
			"the append arm must reach its own diagnostic; if it does not, this subtest proves "+
				"nothing about which stream that diagnostic uses")
		assert.Contains(t, run.Stderr, "no space left on device",
			"the failure must be the REAL ENOSPC the device returned, quoted for the operator; "+
				"a diagnostic that drops the cause tells them the record is gone and not why")
		assert.Contains(t, run.Stderr, faultRecordLossSuffix,
			"every route that loses the record ends with the one clause, so the operator reads "+
				"the same sentence for the same loss whichever route produced it")
		assert.Empty(t, run.Stdout,
			"standard output carries the host's continuation bytes and NOTHING else, and "+
				"Claude Code's continuation is the empty body. This arm was exempted from any "+
				"byte pin on the ground that no portable input drives it; the exemption was "+
				"wrong and this subtest is the input")
	})
}
