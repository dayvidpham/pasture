package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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

	outcome := lifecycleOutcome(cmd, nil, handlers.PassThroughCommitBarrier{})

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

	outcome := lifecycleOutcome(cmd, nil, handlers.PassThroughCommitBarrier{})

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
	go func() { outcomes <- lifecycleOutcome(cmd, nil, barrier) }()

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
	assert.Contains(t, outcome.Stderr, "stopped waiting at its 5s hook-invocation deadline",
		"the diagnostic must name the deadline path, not any fault")
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
