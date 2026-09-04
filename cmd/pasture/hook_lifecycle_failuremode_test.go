package main

import (
	"bytes"
	"context"
	"database/sql"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	binary := lifecycleBinary(t)

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
//
// THE CHILD IS THE RACE-INSTRUMENTED ONE, and this is the only proof that runs
// it. Every other built-binary proof runs the plain shared child, because the
// paths it drives are already read by the race detector in-process (see
// lifecycleBinary). Here the thing under proof is a live process contending
// with a second opener for the real write lock while its deadline runs, and
// that contention exists only across the process boundary, so the detector has
// to ride in the child to read it. TestTheHeldLockProofRunsTheOnlyRaceInstrumentedChild
// holds this arrangement.
func TestLifecycleHookReturnsInsideItsDeadlineWhileTheDatabaseIsLocked(t *testing.T) {
	dir := t.TempDir()
	binary := raceLifecycleBinary(t)

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

	// The smallest host budget this tree has evidence for is the 10s that
	// hooks/hooks.json sets on each Claude Code lifecycle row (held in
	// internal/timeouts). Exceeding it is what freezes a session.
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

// TestTheHeldLockProofRunsTheOnlyRaceInstrumentedChild pins the child-binary
// arrangement that keeps this package's wall time down without losing its one
// live-process race proof.
//
// The arrangement has three parts, and a drift in any one of them would leave
// the package green while the proofs quietly changed what they measure:
//
//   - The held-lock deadline proof runs raceLifecycleBinary, and not the plain
//     child. Pointed at the plain child it would still pass, still bound the
//     elapsed time, and its doc would claim a detector that was not there.
//   - raceLifecycleBinary is built with -race and lifecycleBinary is not. This
//     is read from the BUILD SETTINGS recorded in each binary, not from the
//     helper's source: a build whose flags drifted would carry different
//     settings whatever its source said.
//   - No other test runs the race child. The plain child exists because a
//     race-instrumented child costs 20x per invocation; a second caller of the
//     race child is the cost coming back one test at a time, and it should
//     arrive as a decision written here, not as a slow run.
//
// WHAT IT VISITS: every test function declared in this package's test files,
// for the two identifiers it asks about; and the build settings of the two
// shared children.
// WHAT IT DOES NOT READ: whether either child is up to date with the source,
// or whether the held-lock proof's assertions still hold; the proof itself
// does that.
func TestTheHeldLockProofRunsTheOnlyRaceInstrumentedChild(t *testing.T) {
	t.Parallel()

	const heldLockProof = "TestLifecycleHookReturnsInsideItsDeadlineWhileTheDatabaseIsLocked"
	const thisPin = "TestTheHeldLockProofRunsTheOnlyRaceInstrumentedChild"

	entries, err := os.ReadDir(".")
	require.NoError(t, err, "the package directory must be readable to find the tests it declares")
	callers := map[string][]string{}
	heldLockFound := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		require.NoError(t, parseErr, "every test file of this package must parse")
		for _, node := range parsed.Decls {
			function, isFunction := node.(*ast.FuncDecl)
			if !isFunction || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(inner ast.Node) bool {
				call, isCall := inner.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if callee, isName := call.Fun.(*ast.Ident); isName {
					switch callee.Name {
					case "raceLifecycleBinary", "lifecycleBinary":
						callers[callee.Name] = append(callers[callee.Name], function.Name.Name)
					}
				}
				return true
			})
			if function.Name.Name == heldLockProof {
				heldLockFound = true
			}
		}
	}
	require.True(t, heldLockFound,
		"the held-lock proof %s is not declared in this package; if it was renamed, rename it here too, "+
			"or this pin holds nothing", heldLockProof)
	require.NotEmpty(t, callers["lifecycleBinary"],
		"no test calls lifecycleBinary; the built-binary proofs must run the plain shared child, and an "+
			"empty population here means the walk found nothing and every assertion below is vacuous")

	assert.Contains(t, callers["raceLifecycleBinary"], heldLockProof,
		"the held-lock deadline proof must run the race-instrumented child; its doc promises a race "+
			"detector riding in the live process, and on the plain child that sentence is false")
	assert.NotContains(t, callers["lifecycleBinary"], heldLockProof,
		"the held-lock deadline proof must not also run the plain child")

	sort.Strings(callers["raceLifecycleBinary"])
	assert.Equal(t, []string{heldLockProof, thisPin}, callers["raceLifecycleBinary"],
		"only the held-lock proof (and this pin, which reads the child's build settings) may run the "+
			"race-instrumented child; a new caller pays 20x per hook invocation and must be a decision "+
			"recorded here, not a slow run")

	raceSettings := buildSettingsOf(t, raceLifecycleBinary(t))
	plainSettings := buildSettingsOf(t, lifecycleBinary(t))
	assert.Equal(t, "true", raceSettings["-race"],
		"the race child must record -race=true in its build settings; without it the held-lock proof "+
			"runs no detector and its doc is false")
	assert.NotEqual(t, "true", plainSettings["-race"],
		"the plain child must not be race-instrumented; every other built-binary proof runs it, and "+
			"instrumenting it is the 20x cost this arrangement exists to avoid")
}

// buildSettingsOf reads the build settings the Go toolchain recorded in a
// binary, keyed by setting name, so a test can ask what flags built it.
func buildSettingsOf(t *testing.T, binary string) map[string]string {
	t.Helper()
	info, err := buildinfo.ReadFile(binary)
	require.NoError(t, err, "the built binary must carry readable Go build information")
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return settings
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

	outcome := lifecycleOutcome(cmd, nil, handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)

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

	outcome := lifecycleOutcome(cmd, nil, handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)

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

// trippedDeadline is a hook-invocation deadline with NO CLOCK in it. The signal
// fires when the test trips it and never before, so the TEST orders the expiry
// against the commit, and a slow store before the boundary can only make the
// proof slower, never make it fail.
//
// The context it derives still descends from the invocation's own, so every
// cancellation the production path honours is honoured here unchanged; the
// only thing absent is the timer. The tier it is handed is printed by the
// diagnostic and never started.
type trippedDeadline struct {
	signal context.Context
	trip   context.CancelFunc
}

func newTrippedDeadline(t *testing.T) *trippedDeadline {
	t.Helper()
	signal, trip := context.WithCancel(context.Background())
	t.Cleanup(trip)
	return &trippedDeadline{signal: signal, trip: trip}
}

// derive has the shape of context.WithTimeout, which is what production passes
// in its place.
func (d *trippedDeadline) derive(parent context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(d.signal, cancel)
	return ctx, func() { stop(); cancel() }
}

// preCommitStallCeiling bounds how long a proof waits for the invocation to
// reach the commit boundary. It is a failure ceiling and not a wait: the proof
// never passes because of it, it only fails with a sentence instead of hanging
// until the test binary is killed. Nothing in the proof is ordered by it.
const preCommitStallCeiling = 30 * time.Second

// abandonAfterTheCommit runs ONE invocation of the prepared command, holds it
// at the commit-to-emit boundary, trips the hook-invocation deadline there, and
// returns the outcome the host receives. Every step is a condition:
//
//  1. the barrier signals that the durable receipt is committed and the host
//     has not been told anything;
//  2. the test trips the deadline, which is the signal the production select
//     waits on, and only then;
//  3. the outcome arrives, and the barrier is released afterwards, so the
//     encode could not have run before the outcome was decided.
//
// No clock orders any step; the only clock in the function is the failure
// ceiling below, which can only fail the proof, never pass it. A proof that
// let the tier's timer trip the deadline
// raced that timer against the store work before the boundary, and on a loaded
// runner the timer won: the invocation abandoned work that had not committed,
// and the proof failed with "finished without reaching the commit boundary".
// That failure is what this shape makes impossible. The tier is the production
// one, and it is printed and never started.
func abandonAfterTheCommit(t *testing.T, cmd *cobra.Command) hostexit.Outcome {
	t.Helper()

	barrier := &blockingBarrier{reached: make(chan struct{}), release: make(chan struct{})}
	deadline := newTrippedDeadline(t)
	outcomes := make(chan hostexit.Outcome, 1)
	go func() {
		outcomes <- lifecycleOutcome(cmd, nil, barrier, timeouts.ProductionProfile(), deadline.derive)
	}()

	select {
	case <-barrier.reached:
	case outcome := <-outcomes:
		close(barrier.release)
		t.Fatalf("the invocation finished without reaching the commit boundary, so the work returned or "+
			"faulted before the receipt committed and nothing here proves an abandonment after the "+
			"commit; the outcome it returned instead is %+v", outcome)
	case <-time.After(preCommitStallCeiling):
		close(barrier.release)
		t.Fatalf("the invocation did not reach the commit boundary within %s: the store work before the "+
			"commit (open, migrate, blob, journal row) stalled, and no deadline in this proof can end it "+
			"because the proof owns the deadline; look for another writer holding %s",
			preCommitStallCeiling, flagDBPath)
	}

	deadline.trip()
	outcome := <-outcomes
	close(barrier.release)
	return outcome
}

// TestAnInvocationAbandonedAfterItsCommitTellsTheHostTheTruth is the honesty
// proof for the abandonment path.
//
// The hook bounds its own work and abandons it at the deadline. The receipt
// commits BEFORE the native bytes are produced, so an expiry can land AFTER the
// commit. The hook then cannot claim the event was not evaluated, and it used to
// claim exactly that.
//
// The interleaving is deterministic and owned by the test: the invocation is
// held at the named commit-to-emit boundary, and the deadline is tripped THERE
// by the test, so no clock orders the expiry against the commit. The shape is
// abandonAfterTheCommit. The state is then read back through the PRODUCTION
// read path.
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

	outcome := abandonAfterTheCommit(t, cmd)

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
// three seams this command injects, because each is a production parameter
// whose only other supplier today is a test.
//
// That shape is worth one assertion each. A parameter that only a test supplies
// is one refactor away from being a parameter that production supplies
// DIFFERENTLY. Only the barrier drift would fail no existing test; the other
// two would fail one test that costs real seconds and names no seam:
//
//   - a barrier that is not the pass-through one would run code between the
//     durable commit and the host's continuation, which is the one place this
//     command promises nothing happens, and no existing test would catch it;
//   - a tier that is not the production one would silently move the deadline
//     the whole host-budget claim rests on; the built-binary test that holds
//     the store under a real lock reads the tier's number in its diagnostic
//     and would turn red, but only after paying for that lock;
//   - a deadline that is not context.WithTimeout would start the clock
//     somewhere other than the work, or start no clock at all; that same
//     built-binary test bounds the elapsed time and would turn red, again
//     only after paying for the lock.
//
// The pin makes all three fail by NAME in milliseconds instead, and it is the
// only guard the barrier drift has at all.
//
// The assertion is structural because the barrier drift has nothing to
// observe, and the other two are observable only after real seconds and
// without a name: each seam is correct by being wired, so the pin reads the
// wiring itself.
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
			require.Len(t, call.Args, 5, "%s calls lifecycleOutcome with the wrong shape", name)
			assert.Equal(t, "handlers.PassThroughCommitBarrier{}", sourceOf(call.Args[2]),
				"%s must pass the pass-through commit barrier: production may never supply a barrier "+
					"that runs between the durable commit and the host's continuation", name)
			assert.Equal(t, "timeouts.ProductionProfile()", sourceOf(call.Args[3]),
				"%s must pass the production timeout profile: the hook-invocation tier is chosen "+
					"against the smallest host budget, and production may not run on another one", name)
			assert.Equal(t, "context.WithTimeout", sourceOf(call.Args[4]),
				"%s must pass context.WithTimeout as the deadline: the production deadline is a clock "+
					"that starts with the work and expires at the tier, and production may not derive "+
					"it any other way", name)
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
// WHAT IT VISITS: the two rows below — one declared-blocking gate with no
// citation on each of two harnesses — because the demotion this pins applies
// to both and a proof on one would not show it reaches the other.
// WHAT IT DOES NOT READ: the other rows of either manifest. That the
// evidence rule covers every row is held by the fault table over every
// declared mode and policy, not by this pair.
func TestTheFailClosedReasonFollowsTheDeclaredModeThroughTheBuiltBinary(t *testing.T) {
	binary := lifecycleBinary(t)

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
	binary := lifecycleBinary(t)

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
	binary := lifecycleBinary(t)

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
//
// WHAT IT VISITS: the TWO exit-one arms, written out here because they are two
// named sites in one function and not a class with a source to derive from.
// WHAT IT DOES NOT READ: any third exit-one arm somebody adds. The arm count of
// this command is held by the exit-authority guards, not here.
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
		case *ast.FuncLit:
			// A DEFERRED CLOSURE BODY IS AN ARM. Three sentences already said
			// this reader collected one, and it did not: there was no FuncLit
			// case at all, and the count held at six only because the single
			// closure in the writer happens to contain an `if`. A second
			// closure holding `syncErr := file.Sync(); _ = syncErr` kept the
			// count, required no report, and lost the record in silence on a
			// full disk — the identical defect the checked close was added to
			// remove, in the one shape the guard could not read.
			add("a deferred closure body", branch.Type.Func, branch.Body.List)
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
	// Pos identifies the write uniquely, so the same write collected twice —
	// once inside an arm and once over the whole function — is recognised as
	// one write. A LINE is not enough: two writes can share one.
	Pos token.Pos
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
	record := func(shape string, position token.Pos, writer ast.Expr, synthetic string) {
		text := synthetic
		if writer != nil {
			text = sourceOf(writer)
		}
		writes = append(writes, faultWriterWrite{
			Shape:    shape,
			Pos:      position,
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
			// A WRITE NEED NOT NAME ITS WRITER. fmt.Println reaches os.Stdout
			// through a writer that appears in NO EXPRESSION, so a reader that
			// starts from a writer argument or a receiver cannot see it, the
			// forbidden-stream sweep has nothing to refuse, and binding both
			// results to real names hides it from the discard reader too. It
			// was planted in the CLOSE arm — the one arm this suite states it
			// cannot drive, where structure is the sole witness — and the whole
			// unfiltered package stayed green for 208s.
			if shape, writer, isWriterless := writerlessPrint(call); isWriterless {
				record(shape, call.Lparen, nil, writer)
				return false
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
							record("fmt."+selector.Sel.Name, call.Lparen, call.Args[0], "")
							return false
						}
					}
				case "io":
					if selector.Sel.Name == "WriteString" && len(call.Args) > 0 {
						record("io.WriteString", call.Lparen, call.Args[0], "")
						return false
					}
				}
			}
			switch selector.Sel.Name {
			case "Write", "WriteString":
				record("a bare ."+selector.Sel.Name+" call", call.Lparen, selector.X, "")
				return false
			}
			return true
		})
	}
	return writes
}

// implicitStdoutWriter is the synthetic writer text recorded for a call that
// reaches standard output WITHOUT NAMING IT. It is not valid Go, on purpose: it
// can never be produced by sourceOf, so it can never be confused with a writer
// a maintainer actually wrote, and it contains "os.Stdout" so the existing
// standard-output refusal fires with the message that names the real harm.
const implicitStdoutWriter = "os.Stdout (implicit, named by no expression)"

// implicitBuiltinWriter is the synthetic writer text for the print and println
// BUILTINS, whose Fun is a plain identifier rather than a selector and which
// write to the process's standard error, outside the command stream every pin
// in this package reads.
const implicitBuiltinWriter = "the process's standard error (implicit, named by no expression)"

// writerlessPrint reports whether a call writes through a writer that appears
// in no expression, and returns the shape and the synthetic writer to record.
//
// THE POPULATION IT ADDS. fmt.Print, fmt.Printf and fmt.Println, which write to
// os.Stdout; the fmt.Fatal-free log family (log.Print, log.Printf, log.Println,
// log.Fatal, log.Fatalf, log.Fatalln, log.Panic, log.Panicf, log.Panicln),
// which writes to whatever output log holds; and the print and println
// builtins. None of these names a stream, so no reader that starts from a
// writer argument or a receiver can see them.
func writerlessPrint(call *ast.CallExpr) (string, string, bool) {
	if name, isIdentifier := call.Fun.(*ast.Ident); isIdentifier {
		switch name.Name {
		case "print", "println":
			return "the " + name.Name + " builtin", implicitBuiltinWriter, true
		}
		return "", "", false
	}
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector {
		return "", "", false
	}
	package_, isIdentifier := selector.X.(*ast.Ident)
	if !isIdentifier {
		return "", "", false
	}
	switch package_.Name {
	case "fmt":
		switch selector.Sel.Name {
		case "Print", "Printf", "Println":
			return "fmt." + selector.Sel.Name, implicitStdoutWriter, true
		}
	case "log":
		switch selector.Sel.Name {
		case "Print", "Printf", "Println",
			"Fatal", "Fatalf", "Fatalln",
			"Panic", "Panicf", "Panicln":
			return "log." + selector.Sel.Name,
				"the log package's own output (implicit, named by no expression)", true
		}
	}
	return "", "", false
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

// faultWriterArmCount is the number of guarded branches recordLifecycleFault
// has. It is a named constant and not a literal because TWO documents state it:
// the enumeration guard below, and the route list in AGENTS.md, which a
// maintainer reads to learn how a fault record can be lost. The document's
// count was raised BY HAND when the close route was added, and nothing held it;
// TestTheFaultRecordRouteListStatesTheCountTheCodeHas now does, against this.
//
// SIX ARMS, SEVEN BRANCHES. The six are the store path naming no directory, the
// line that cannot be encoded, the directory that cannot be made, the file that
// cannot be opened, the line that cannot be appended and the file that cannot
// be closed. The seventh branch is the DEFERRED CLOSURE that holds the close
// arm: it is a branch of the enumeration, not a way for the record to be lost,
// so the document counts six and this counts seven.
const faultWriterArmCount = 7

// faultWriterWherePhrase is the WHERE every loss arm of the fault writer
// carries, and faultWriterRemedyPhrase and faultWriterReportPhrase are the two
// ACTIONS an arm may end with: the remedy, on every route an operator can
// change, and the request to report, on the one arm a correct build cannot
// reach. They are held on every arm by
// TestEveryFailingArmOfTheFaultWriterTellsTheOperatorOnStandardError.
const (
	faultWriterWherePhrase  = "this happened in recordLifecycleFault (cmd/pasture/hook_lifecycle.go)"
	faultWriterRemedyPhrase = "and the record returns"
	faultWriterReportPhrase = "report this"
)

// faultWriterArmText concatenates every string literal in the statements
// given, in source order, so a phrase split across a `+` chain is read whole.
func faultWriterArmText(body []ast.Stmt) string {
	var text strings.Builder
	for _, statement := range body {
		ast.Inspect(statement, func(node ast.Node) bool {
			literal, isLiteral := node.(*ast.BasicLit)
			if isLiteral && literal.Kind == token.STRING {
				if unquoted, err := strconv.Unquote(literal.Value); err == nil {
					text.WriteString(unquoted)
				}
			}
			return true
		})
	}
	return text.String()
}

// faultWriterLossRouteCount is how many of those branches are ways the record
// can be LOST, which is what AGENTS.md enumerates. It excludes the deferred
// closure, which holds a loss route rather than being one.
const faultWriterLossRouteCount = faultWriterArmCount - 1

// faultWriterUndrivableRoutes are the loss routes no input in this suite can
// drive, so AGENTS.md's "routes a user can reach" count is derived and not
// stated twice. The encode arm is unreachable by construction.
const faultWriterUnreachableRoutes = 1

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

// streamNamingHelpers returns the names of functions DECLARED IN THIS FILE
// whose own body names a stream recordLifecycleFault may not use.
//
// THE FOURTH WRITE SHAPE IS A WRITE PERFORMED BY SOMEBODY ELSE. The write
// population reads the calls recordLifecycleFault makes; the forbidden-stream
// sweep reads the expressions it contains. A one-line helper beside it that
// names cmd.OutOrStdout(), called with its result bound to a live name, is
// invisible to both: the call site names no stream and performs no write that
// this file's shapes recognise, and the bytes still land on standard output.
// Measured green over the whole package.
//
// The reader is bounded at this FILE on purpose, and the bound is stated rather
// than left implied: a helper in another package is not read, so the refusal
// below is not a claim that no callee anywhere can write. It is the claim that
// no neighbour of this writer can, which is where such a helper would be
// written.
func streamNamingHelpers(fileSet *token.FileSet, file *ast.File) map[string]string {
	named := map[string]string{}
	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction || function.Name.Name == "recordLifecycleFault" {
			continue
		}
		if found := faultWriterForbiddenStreams(fileSet, function); len(found) > 0 {
			named[function.Name.Name] = found[0]
		}
	}
	return named
}

// faultWriterHelperCalls returns every call recordLifecycleFault makes to one of
// those helpers.
func faultWriterHelperCalls(fileSet *token.FileSet, function *ast.FuncDecl, named map[string]string) []string {
	found := []string{}
	ast.Inspect(function, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		name, isIdentifier := call.Fun.(*ast.Ident)
		if !isIdentifier {
			return true
		}
		if where, isNamed := named[name.Name]; isNamed {
			found = append(found, fmt.Sprintf("%s at hook_lifecycle.go:%d, whose own body names %s",
				name.Name, fileSet.Position(call.Lparen).Line, where))
		}
		return true
	})
	return found
}

// faultWriterSuccessGuards returns every condition in the function that is
// entered because something SUCCEEDED, judged as an equality comparison against
// nil.
func faultWriterSuccessGuards(fileSet *token.FileSet, function *ast.FuncDecl) []string {
	found := []string{}
	ast.Inspect(function, func(node ast.Node) bool {
		binary, isBinary := node.(*ast.BinaryExpr)
		if !isBinary || binary.Op != token.EQL {
			return true
		}
		left, right := sourceOf(binary.X), sourceOf(binary.Y)
		if left != "nil" && right != "nil" {
			return true
		}
		found = append(found, fmt.Sprintf("hook_lifecycle.go:%d tests %s",
			fileSet.Position(binary.OpPos).Line, sourceOf(binary)))
		return true
	})
	return found
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
//   - BRANCHES. Every guarded branch must report, and every report must stand
//     inside one. The reader collects if arms, else blocks, switch cases,
//     select cases, loop bodies and the bodies of deferred closures. It
//     collected *ast.IfStmt alone once, so a switch-shaped silent return kept
//     the count at five; and it CLAIMED deferred closure bodies for a round
//     before it read them, so a second closure spending a named Sync error on
//     `_ = syncErr` held the count and lost the record in silence. A sentence
//     wider than its reader is the defect these guards exist to catch, one
//     level up.
//   - WRITES. Every write must reach one of the TWO sinks this writer has and
//     no third: the operator, on cmd.ErrOrStderr(), or the RECORD ITSELF, on
//     the file this same function opened — recognised by resolving the writer
//     to the os.OpenFile call that produced it, so renaming the local cannot
//     disguise a write. The record write must happen exactly once.
//     THE THREE SHAPES THE READER COLLECTS, stated because this population has
//     been too narrow twice: a writer passed as an ARGUMENT (fmt.Fprint,
//     Fprintf, Fprintln, io.WriteString); a writer used as a RECEIVER (.Write,
//     .WriteString on any expression); and A WRITE WITH NO WRITER EXPRESSION
//     (fmt.Print, Printf, Println; the log package's package-level printers;
//     the print and println builtins), recorded against a synthetic writer so
//     the refusal below can name the stream nobody wrote down.
//     The first reader collected fmt.Fprint, Fprintf and Fprintln
//     alone. ONE ADDED LINE in the MkdirAll arm,
//     cmd.OutOrStdout().Write([]byte("pasture: ...")) — the idiom
//     emitLifecycleOutcome uses for stdout in this same file — left the FULL
//     unfiltered cmd/pasture package green while the built binary put 76 bytes
//     on standard output ahead of the proceed and the shipped plugin threw. The
//     SECOND reader still started from a writer expression, so `fmt.Println` in
//     the close arm — the one arm this suite cannot drive, where structure is
//     the sole witness — left the full unfiltered package green for 208s.
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
// function is what covers it.
//
// THE ARMS DRIVEN ON THE BYTES A HOST RECEIVES, through the built binary, are
// FOUR: TestTheFaultRecordRefusalQuotesThePathTheEnvironmentResolvedTo (the
// no-directory arm), TestEveryDrivableFaultRecordLossIsMeasuredOnTheHostBytes
// (the MkdirAll arm and the append arm) and
// TestTheFaultRecordOpenFailureIsReportedOnStandardErrorOnly (the open arm).
//
// THE CLOSE ARM IS NOT AMONG THEM, and this sentence said it was for a round.
// No input in this suite drives a close failure: /dev/full, which gives the
// append arm a real ENOSPC, accepts the close, and a route that reports at
// close(2) needs a filesystem this suite does not have. The close arm is held
// STRUCTURALLY — by the branch enumeration here and by
// TestTheFaultWriterDiscardsNoResultThatCouldCarryALoss — and the limit is
// written out where that test refuses to claim it, which is the honest
// statement this one contradicted. Do not restore the wider sentence; either
// drive the arm or leave the limit stated.
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
// EVERY ARM ALSO SAYS WHERE IT FAILED AND WHAT TO DO. The open and append arms
// reported the loss with neither while their three siblings carried both, and
// on those routes standard error is the only channel left; the remedy stood in
// AGENTS.md, which the operator reading the stream does not have open. Each
// arm's string literals are read whole, in source order, so the WHERE clause
// and one of the two ACTION clauses must appear in every arm, whatever `+`
// chain they are split across.
// MUTATION: delete "and the record returns" from the open arm, or its "this
// happened in recordLifecycleFault" clause. This test turns RED naming the arm.
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
	require.Len(t, arms, faultWriterArmCount,
		"the failure population of this writer is %d guarded branches, counted over if arms, "+
			"else blocks, switch cases, select cases, loop bodies and deferred closure bodies "+
			"alike; one has been added or removed without this enumeration, which is how the "+
			"same N-1 sweep happened twice. The route list in AGENTS.md states this same count "+
			"in words and is held against it", faultWriterArmCount)

	// The stream is asserted here, arm by arm, so a failure names the branch.
	// The reader descends, so an arm that CONTAINS another arm is credited with
	// the writes of its child: the deferred closure body and the `if` inside it
	// are two arms carrying one report between them, and each of them has told
	// the operator.
	reported := map[token.Pos]bool{}
	for _, arm := range arms {
		spoke := 0
		for _, write := range faultWriterWrites(fileSet, arm.Body, bound) {
			assertFaultWriterStream(t,
				fmt.Sprintf("%s at hook_lifecycle.go:%d", arm.Shape, arm.Line), write)
			// A write whose stream this guard could not read still COUNTS AS
			// A WORD: the assertion above has already refused it, and telling
			// the same maintainer that the arm is silent as well would be the
			// second false sentence about one input.
			if faultWriterSinkOf(write) != sinkRecord {
				spoke++
			}
			reported[write.Pos] = true
		}
		require.NotZero(t, spoke,
			"%s at hook_lifecycle.go:%d leaves WITHOUT A WORD: the fault it was recording then "+
				"has no durable record and nothing anywhere says so, which is the loss the "+
				"placement of this file beside the database exists to prevent", arm.Shape, arm.Line)

		// WHERE AND HOW TO FIX, on every arm. A loss reported with neither
		// leaves the operator with bad news and no action, on the one channel
		// this route has.
		text := faultWriterArmText(arm.Body)
		require.Contains(t, text, faultWriterWherePhrase,
			"%s at hook_lifecycle.go:%d reports a lost record without saying WHERE it was lost; "+
				"every arm of this writer names the function and the file, so the operator can "+
				"find the code that gave up the record", arm.Shape, arm.Line)
		require.Truef(t, strings.Contains(text, faultWriterRemedyPhrase) || strings.Contains(text, faultWriterReportPhrase),
			"%s at hook_lifecycle.go:%d reports a lost record without an ACTION: it must end "+
				"with %q after telling the operator what to change, or with %q on the one arm no "+
				"input can drive", arm.Shape, arm.Line, faultWriterRemedyPhrase, faultWriterReportPhrase)
	}

	// THE SAME CLAIM OVER THE WHOLE FUNCTION, so a write standing OUTSIDE every
	// branching shape above cannot escape it. The arm loop can only see what the
	// enumeration collected; this sees every write the writer makes.
	//
	// THE RELATION IS CONTAINMENT AND NOT A COUNT, and this is a re-derivation
	// forced by the arm reader gaining deferred closure bodies. A count was
	// sound only while the arms could not NEST: one report per arm made
	// reports == arms an exact statement of "no report stands outside a
	// branch". With nesting the closure body and its `if` are two arms sharing
	// one report, so the count is false while the property it stood for still
	// holds. Comparing POSITIONS says the property directly and survives any
	// nesting a later arm introduces.
	outside, records := []string{}, []faultWriterWrite{}
	for _, write := range faultWriterWrites(fileSet, writer.Body.List, bound) {
		assertFaultWriterStream(t, "over the whole of recordLifecycleFault", write)
		if faultWriterSinkOf(write) == sinkRecord {
			records = append(records, write)
			continue
		}
		if !reported[write.Pos] {
			outside = append(outside, fmt.Sprintf("the %s write at hook_lifecycle.go:%d, handed %s",
				write.Shape, write.Line, write.Writer))
		}
	}
	assert.Empty(t, outside,
		"every report of this writer must stand INSIDE a guarded branch the enumeration above "+
			"collected. A report outside them all is a word the arm loop never read, so nothing "+
			"checked which stream it used and nothing requires the branch it belongs to to "+
			"speak; it is also the shape that made this writer an N-1 sweep twice")
	assert.Len(t, records, 1,
		"the record itself is written ONCE, to the file this function opened. A second write to "+
			"that file is a second line for one fault, and a write to any other sink is bytes "+
			"leaving this command by a route nothing here describes")

	// NO BRANCH MAY BE ENTERED BECAUSE SOMETHING SUCCEEDED. The enumeration
	// above reads the SHAPE of a branch and never its CONDITION, so inverting
	// one — `if closeErr == nil` in place of `if closeErr != nil` — left the arm
	// count, the containment property, the record count and the stream sweep all
	// satisfied, because the arm still CONTAINS a report. The close route went
	// silent again, and it is the one route this suite states structure alone
	// holds.
	//
	// The rule is narrow and says what it reads: no condition in this writer may
	// compare anything to nil for EQUALITY. Every arm here is entered on a
	// failure, and a failure in Go is an error that is NOT nil. The
	// no-directory arm tests `path == ""`, which is an equality but not against
	// nil, so it is untouched — and that is the honest limit of this reader: it
	// refuses a success-guard written against nil, not every success-guard
	// imaginable.
	assert.Empty(t, faultWriterSuccessGuards(fileSet, writer),
		"a branch of this writer is entered when a call SUCCEEDED. Every arm here reports that "+
			"the fault record was lost, so entering one on success means the report fires when "+
			"nothing went wrong AND — the reason this matters — the failure it was written for "+
			"now reports nothing at all")

	// NO EXPRESSION MAY NAME A STREAM THIS WRITER MAY NOT USE. The two loops
	// above can only refuse writes whose call shape is recognised; this refuses
	// the stream itself, wherever it is named and whatever it is handed to.
	assert.Empty(t, faultWriterHelperCalls(fileSet, writer, streamNamingHelpers(fileSet, file)),
		"recordLifecycleFault may not CALL a function in this file that names a stream it may not "+
			"use itself. A write performed by a callee is invisible to the write population, which "+
			"reads the calls this function makes, and to the stream sweep, which reads the "+
			"expressions it contains: the call site names no stream and performs no recognised "+
			"write, and the bytes reach standard output all the same")

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
// call that is not a closure whose body the enumeration above can read; and an
// assignment whose LAST left-hand name — which is where Go puts the error — is
// the blank identifier, WHATEVER stands on the right.
//
// THE RIGHT-HAND SIDE IS DELIBERATELY UNRESTRICTED. It used to have to be a
// call, and that let a second shape through: bind the result to a REAL name and
// spend it on `_ = name`. The name makes the compiler happy, the discard is an
// assignment from a bare identifier, and a call-shaped rule reads neither.
//
// THIS COMMENT ONCE ENDED "there is no fourth way". THAT WAS FALSE, and it was
// falsified by being stated plainly enough to test: a result can also be spent
// on a LIVE VARIABLE —
//
//	syncErr := file.Sync()
//	unusable = append(unusable, recordedCause(syncErr))
//
// which is not a branch, not a report and not a blank discard. The whole
// package stayed green.
//
// SO THE READER WAS WIDENED RATHER THAN THE SENTENCE RESTATED, and the sentence
// now describes what the reader actually does: every name this function binds
// from a call must be SPENT ON A DECISION OR ON A WORD — tested in a condition,
// or handed to a write. A name spent any other way is refused, whatever it is
// spent on, so the shape above and the shapes nobody has thought of are covered
// by the same rule. The one thing this does not read is a value that is never
// bound to a name at all, which cannot carry an error out of a call in Go
// without being discarded first, and a discard is the rule above.
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
			if len(statement.Lhs) == 0 {
				return true
			}
			last, isIdentifier := statement.Lhs[len(statement.Lhs)-1].(*ast.Ident)
			if !isIdentifier || last.Name != "_" {
				return true
			}
			// THE RIGHT-HAND SIDE IS NOT REQUIRED TO BE A CALL, and requiring
			// it was the hole. `syncErr := file.Sync()` binds a REAL name, so
			// Go compels a use, and the use that discards is `_ = syncErr` —
			// an assignment from a bare identifier, which a call-shaped rule
			// walks straight past. Together the two statements lost the record
			// in silence with every guard green.
			//
			// A NAME MUST THEREFORE BE CHECKED OR BE UNUSED, and an unused one
			// does not compile. That is what closes the shape: the only ways
			// to spend a bound error here are a branch, which the enumeration
			// collects and requires to speak, a report, which the write
			// population reads, or this discard, which is refused.
			what := sourceOf(statement.Rhs[0])
			if len(statement.Rhs) != 1 {
				what = "this statement's last value"
			}
			note(statement.TokPos, "binds the LAST result of "+what+
				" to the blank identifier, and the last result is where Go puts the error")
		}
		return true
	})

	// EVERY NAME BOUND FROM A CALL MUST BE SPENT ON A DECISION OR ON A WORD.
	// A name tested in a condition reaches an arm the enumeration requires to
	// speak; a name handed to a write reaches the operator. A name spent any
	// other way — appended to a slice, formatted into a string, passed to a
	// helper — carries its failure nowhere, and that is how `syncErr` was spent
	// with the whole package green.
	spent := map[string]bool{}
	ast.Inspect(writer, func(node ast.Node) bool {
		switch used := node.(type) {
		case *ast.IfStmt:
			ast.Inspect(used.Cond, func(inner ast.Node) bool {
				if name, isIdentifier := inner.(*ast.Ident); isIdentifier {
					spent[name.Name] = true
				}
				return true
			})
		case *ast.SwitchStmt:
			if used.Tag != nil {
				ast.Inspect(used.Tag, func(inner ast.Node) bool {
					if name, isIdentifier := inner.(*ast.Ident); isIdentifier {
						spent[name.Name] = true
					}
					return true
				})
			}
		}
		return true
	})
	ast.Inspect(writer, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		if _, _, isWriterless := writerlessPrint(call); !isWriterless {
			selector, isSelector := call.Fun.(*ast.SelectorExpr)
			if !isSelector {
				return true
			}
			package_, isPackage := selector.X.(*ast.Ident)
			isFmtWrite := isPackage && package_.Name == "fmt" &&
				(selector.Sel.Name == "Fprint" || selector.Sel.Name == "Fprintf" || selector.Sel.Name == "Fprintln")
			isMethodWrite := selector.Sel.Name == "Write" || selector.Sel.Name == "WriteString"
			if !isFmtWrite && !isMethodWrite {
				return true
			}
			if isMethodWrite {
				if name, isIdentifier := selector.X.(*ast.Ident); isIdentifier {
					spent[name.Name] = true
				}
			}
		}
		for _, argument := range call.Args {
			ast.Inspect(argument, func(inner ast.Node) bool {
				if name, isIdentifier := inner.(*ast.Ident); isIdentifier {
					spent[name.Name] = true
				}
				return true
			})
		}
		return true
	})

	unspent := []string{}
	for name, source := range faultWriterBindings(writer) {
		if !strings.Contains(source, "(") || spent[name] {
			continue
		}
		unspent = append(unspent, fmt.Sprintf("%s, bound from %s", name, source))
	}
	sort.Strings(unspent)
	assert.Empty(t, unspent,
		"recordLifecycleFault binds these names from a call and spends them on neither a DECISION "+
			"nor a WORD. A name that is never tested in a condition reaches no arm this suite "+
			"requires to speak, and a name never handed to a write reaches no operator, so a "+
			"failure it carries is lost as completely as a discarded one. `syncErr := file.Sync()` "+
			"appended to a slice is exactly this shape, and it passed while the comment above this "+
			"guard claimed there was no way to do it")

	assert.Empty(t, discards,
		"recordLifecycleFault may throw away NO result that could carry a lost record. Every "+
			"route that loses the record must tell the operator on standard error, and a "+
			"discarded error is a route with no branch, which the arm enumeration cannot see "+
			"and no value pin reads. Check the result and report it with faultRecordLossSuffix "+
			"like its siblings, or, if it genuinely cannot fail, bind it and say so where it is "+
			"bound. The one exemption is fmt.Fprint, Fprintf and Fprintln, whose error has no "+
			"channel left")
}

// openCodeBeltArtefacts are the two SHIPPED plugin copies, relative to the
// repository root. The behavioural pin drives each one, because an operator
// runs whichever their installation carries and a claim about the plugin's
// behaviour must be measured on the bytes that ship.
var openCodeBeltArtefacts = []string{
	".opencode/plugins/pasture-lifecycle.ts",
	"internal/target/opencode/assets/hooks/pasture-hooks.ts",
}

// TestTheOpenCodeBeltSurfacesTheDiagnosticItSendsTheOperatorTo drives the
// empty-body belt of the SHIPPED plugin under Bun and requires the child's
// standard error to reach a stream the operator has.
//
// WHY A BEHAVIOURAL PIN AND NOT A PHRASE. The belt line tells the operator to
// read the pasture diagnostic on standard error. For one round that instruction
// was FALSE FOR THE READER IT ADDRESSED: invokeLifecycle spawns with
// stderr: "pipe", so fd 2 is captured rather than inherited, and the captured
// value was used in exactly one place — the non-zero-exit throw. The belt route
// is exit 0 with an empty body, so on every occasion the line fired, the
// diagnostic it named had already been swallowed by the callback that printed
// it, and reached neither the host's standard error nor anything else.
// The content pin beside this one reads the sentence and cannot see that: a
// phrase pin cannot tell whether the stream a phrase names is reachable from
// the code that prints the phrase. Only running it can.
//
// THE INPUT IS THE READER THE BELT EXISTS FOR: a stand-in for an already
// installed OLDER pasture that exits 0, writes NOTHING on stdout, and puts a
// diagnostic on standard error. That is exactly the shape the belt was added to
// survive, and it is driven here rather than described.
//
// MUTATION, AT THE DEFECT SITE: delete the forwarding line from
// invokeLifecycle in internal/codegen/opencode_hooks.go and regenerate, or
// delete it from a shipped artefact alone. This test turns RED on the token
// assertion for that artefact: the belt line is still printed and the host
// still continues, and the diagnostic is nowhere.
func TestTheOpenCodeBeltSurfacesTheDiagnosticItSendsTheOperatorTo(t *testing.T) {
	t.Parallel()

	bun, err := exec.LookPath("bun")
	require.NoError(t, err,
		"Bun runs the generated OpenCode plugin, and this claim is about what that plugin does "+
			"rather than about what it says; enter the flake dev shell")

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "resolve the repository root from cmd/pasture")

	// A stand-in for an older installed pasture: exit 0, empty stdout, a
	// diagnostic on standard error. No pasture build is needed, because the
	// claim under test belongs to the PLUGIN and not to the binary.
	const token = "pasture-diagnostic-token-the-operator-must-see"
	dir := t.TempDir()
	stub := filepath.Join(dir, "pasture-stub")
	// THE STUB WRITES ITS DIAGNOSTIC WITH NO TRAILING NEWLINE, on purpose. A
	// diagnostic that does not end in one is what makes the forward's newline
	// normalisation load-bearing: without it the forwarded bytes and the belt
	// line that follows run together mid-sentence on one line, and the operator
	// reads a single mangled sentence made of two speakers. Nothing held that,
	// so dropping the normalisation was green.
	require.NoError(t, os.WriteFile(stub,
		[]byte("#!/bin/sh\nprintf '%s' '"+token+"' >&2\nexit 0\n"), 0o700),
		"write the stand-in binary the plugin will spawn")

	for _, artefact := range openCodeBeltArtefacts {
		t.Run(artefact, func(t *testing.T) {
			module := (&url.URL{
				Scheme: "file",
				Path:   filepath.Join(root, filepath.FromSlash(artefact)),
			}).String()
			runner := filepath.Join(t.TempDir(), "belt.ts")
			script := fmt.Sprintf(`
import { toolExecuteBefore } from %q;
const output = { args: { path: "a" } };
await toolExecuteBefore({ tool: "read", sessionID: "s", callID: "c" }, output);
console.log("HOST-CONTINUED");
`, module)
			require.NoError(t, os.WriteFile(runner, []byte(script), 0o600),
				"write the harness that drives the belt route")

			command := exec.Command(bun, runner)
			command.Env = append(os.Environ(), "PASTURE_BIN="+stub)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			runErr := command.Run()
			require.NoError(t, runErr,
				"the belt must let the host continue, never throw: a throw on tool.execute.before "+
					"is a GATE callback failing, which stops the user's tool call.\nstdout: %s\nstderr: %s",
				stdout.String(), stderr.String())

			require.Contains(t, stdout.String(), "HOST-CONTINUED",
				"the user's action must proceed on this route; if it did not, this subtest proves "+
					"nothing about what the operator was told")
			require.Contains(t, stderr.String(), "and returned no decision",
				"the belt line must be printed on this input; if it is not, the route under test "+
					"was never taken and the token assertion below would pass vacuously")

			assert.Contains(t, stderr.String(), token+"\n",
				"the forwarded diagnostic must END ON ITS OWN LINE. pasture's diagnostic need not "+
					"carry a trailing newline, and this stub's does not; without the forward's "+
					"normalisation the belt line is appended to it and the operator reads one "+
					"mangled sentence spoken by two different voices")
			assert.NotContains(t, stderr.String(), token+"Pasture did not evaluate",
				"the two speakers must not run together. This is the exact shape the normalisation "+
					"prevents, and it is asserted directly so that removing the normalisation "+
					"cannot stay green")

			assert.Contains(t, stderr.String(), token,
				"the belt tells the operator to read the pasture diagnostic on standard error, so "+
					"the diagnostic must BE on standard error. This plugin pipes the child's fd 2 "+
					"rather than inheriting it, so the bytes reach no stream unless the plugin "+
					"forwards them; without the forward, an operator who follows the instruction "+
					"finds nothing and cannot tell a record-written fault from a record-lost one, "+
					"which is the distinction the sentence exists to give them")
		})
	}
}

// spelledCounts maps the words AGENTS.md writes its route counts in to the
// numbers they mean. The document states them in WORDS, which is right for
// prose and useless to a guard unless the guard can read them.
var spelledCounts = map[int]string{
	1: "ONE", 2: "TWO", 3: "THREE", 4: "FOUR", 5: "FIVE", 6: "SIX", 7: "SEVEN", 8: "EIGHT", 9: "NINE",
}

// spelledOrdinals maps the same numbers to the ordinal the document uses for
// the one failure it declares unreachable.
var spelledOrdinals = map[int]string{
	4: "FOURTH", 5: "FIFTH", 6: "SIXTH", 7: "SEVENTH", 8: "EIGHTH", 9: "NINTH",
}

// TestTheFaultRecordRouteListStatesTheCountTheCodeHas holds the route counts in
// AGENTS.md to the count the enumeration guard reads out of the code.
//
// WHY. The document says "The writer has SIX guarded failures. FIVE of them are
// routes a user can reach", it enumerates 1 to 5, and it calls the encode arm
// "The SIXTH failure". Those numbers were raised BY HAND when the close route
// was added, and NOTHING held them. The code-side count is pinned, so the next
// maintainer who adds an arm is stopped by a loud message, bumps the constant,
// and the document goes on saying SIX and FIVE with every gate green — a
// maintainer note that quietly stops describing the product.
//
// This branch argued in its own commit message that "correcting a generator
// without a pin leaves the wording resting on nothing". A hand-kept count in a
// document a maintainer reads to learn how evidence is lost is the same claim
// resting on the same nothing. internal/timeouts/docs_guard_test.go is the
// precedent in this tree for holding AGENTS.md to live values.
//
// WHAT IS DERIVED AND WHAT IS STATED. The guarded-branch count comes from
// faultWriterArmCount; the LOSS-ROUTE count is that minus the deferred closure,
// which holds a route rather than being one; the reachable count is the loss
// routes minus the arms no input can reach. Nothing here restates a number the
// enumeration guard already owns.
//
// MUTATION: change any count in the AGENTS.md paragraph, or add an arm to
// recordLifecycleFault and bump faultWriterArmCount without touching the
// document. This test turns RED and names the number the document should carry.
func TestTheFaultRecordRouteListStatesTheCountTheCodeHas(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "resolve the repository root from cmd/pasture")
	raw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err, "read AGENTS.md, which is where the fault-record routes are listed")

	const heading = "### The lifecycle fault record"
	document := string(raw)
	require.Contains(t, document, heading,
		"the route list must still live under its own heading; a guard that cannot find the "+
			"section would pass on every document that does not contain it")
	section := document[strings.Index(document, heading):]
	if next := strings.Index(section[len(heading):], "\n### "); next != -1 {
		section = section[:len(heading)+next]
	}

	losses := faultWriterLossRouteCount
	reachable := losses - faultWriterUnreachableRoutes

	require.Contains(t, spelledCounts, losses,
		"the route count %d has no spelling in this table, so the document cannot be checked "+
			"against it; add the word", losses)
	require.Contains(t, spelledCounts, reachable,
		"the reachable-route count %d has no spelling in this table; add the word", reachable)
	require.Contains(t, spelledOrdinals, losses,
		"the unreachable route's ordinal for %d has no spelling in this table; add the word", losses)

	assert.Contains(t, section,
		"The writer has "+spelledCounts[losses]+" guarded failures.",
		"the route list must state the number of ways the record can be lost that the code "+
			"actually has, which is %d: %d guarded branches in recordLifecycleFault less the %d "+
			"deferred closure that holds one rather than being one. The document's numbers were "+
			"raised by hand once and nothing held them",
		losses, faultWriterArmCount, faultWriterArmCount-faultWriterLossRouteCount)
	assert.Contains(t, section,
		spelledCounts[reachable]+" of them are routes a user can reach",
		"the route list must state how many of those %d routes an input can drive, which is %d: "+
			"all of them but the %d that is unreachable by construction",
		losses, reachable, faultWriterUnreachableRoutes)
	assert.Contains(t, section,
		"The "+spelledOrdinals[losses]+" failure, a record line that cannot be encoded, is unreachable by",
		"the document names the unreachable failure by its ORDINAL, so that number moves with "+
			"the count as well; it is the %s of %d", spelledOrdinals[losses], losses)

	for route := 1; route <= reachable; route++ {
		assert.Regexp(t, regexp.MustCompile(`(?m)^`+fmt.Sprint(route)+`\. `), section,
			"the list must ENUMERATE every reachable route it claims, and route %d is missing. A "+
				"count with no matching entry is the drift this guard exists to catch", route)
	}
	assert.NotRegexp(t, regexp.MustCompile(`(?m)^`+fmt.Sprint(reachable+1)+`\. `), section,
		"the list enumerates %d reachable routes and must stop there; a further numbered entry "+
			"is a route the counts above do not include", reachable)

	// THE PROSE CROSS-REFERENCES ARE HELD TOO. The counts, the ordinal and the
	// filename were derived, and FIVE ordinals written into the surrounding
	// sentences were left as hand-kept prose: "on that route, route 4 never
	// fires", "ROUTES 1 AND 2 ARE ABOUT PLACING THE FILE, ROUTES 3, 4 AND 5 ARE
	// ABOUT WRITING IT", "not exempt from routes 3, 4 and 5", "that is route 3".
	// Every one of them points at a numbered entry, so every one of them can go
	// stale the moment an entry is inserted or removed — which is precisely the
	// drift the counts above were pinned against.
	//
	// The rule is the one thing that can be stated without restating the
	// document: EVERY ordinal these sentences cite must be a route the list
	// actually enumerates.
	citedRoutes := regexp.MustCompile(`[Rr]outes? ((?:\d+(?:, | and | AND )?)+)`)
	for _, match := range citedRoutes.FindAllStringSubmatch(section, -1) {
		for _, ordinal := range regexp.MustCompile(`\d+`).FindAllString(match[1], -1) {
			number, convErr := strconv.Atoi(ordinal)
			require.NoError(t, convErr, "read the route ordinal %q cited in the section", ordinal)
			assert.LessOrEqual(t, number, reachable,
				"the section's prose cites route %d in %q, and the list enumerates only %d "+
					"reachable routes. A cross-reference to a route that is not there sends a "+
					"maintainer to a paragraph that does not exist, and these sentences are how "+
					"the routes are explained to each other",
				number, strings.TrimSpace(match[0]), reachable)
			assert.Positive(t, number,
				"a route ordinal is counted from 1; %q cites %d", strings.TrimSpace(match[0]), number)
		}
	}

	// The record file the whole section is about is named by the product, so a
	// rename cannot leave the document describing a file that no longer exists.
	assert.Contains(t, section, lifecycleFaultRecordFile,
		"the section must name the record file THIS BUILD writes (%s); renaming the product "+
			"constant left this document, and the operator text beside it, naming a file that is "+
			"not there", lifecycleFaultRecordFile)
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
	binary := lifecycleBinary(t)

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
	binary := lifecycleBinary(t)

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
	// WHERE AND HOW TO FIX, on the bytes the operator reads. This arm said
	// neither while its siblings said both, and on this route standard error
	// is the only channel the record has left.
	assert.Contains(t, run.Stderr, faultWriterWherePhrase,
		"the open arm must say WHERE the record was lost")
	assert.Contains(t, run.Stderr, "writable by the user running the hook",
		"the open arm must name the condition to change: the directory is not writable by this user")
	assert.Contains(t, run.Stderr, faultWriterRemedyPhrase,
		"the open arm must tell the operator that the record returns once the condition is changed")
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
	binary := lifecycleBinary(t)

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
		// WHERE AND HOW TO FIX, on the bytes the operator reads. This arm said
		// neither while its siblings said both.
		assert.Contains(t, run.Stderr, faultWriterWherePhrase,
			"the append arm must say WHERE the record was lost")
		assert.Contains(t, run.Stderr, "free space or quota on the filesystem holding",
			"the append arm must name the condition to change: the filesystem or the quota is full")
		assert.Contains(t, run.Stderr, faultWriterRemedyPhrase,
			"the append arm must tell the operator that the record returns once the condition is changed")
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

// openCodeBeltSources are the three files that must agree on the console line
// the OpenCode plugin prints when pasture returns no decision: the generator
// that composes it, and BOTH generated artefacts. They are listed relative to
// the repository root.
//
// BOTH ARTEFACTS ARE READ AND NOT ONE. They are generated from the same source
// and other guards hold them against it, but the claim here is what an OPERATOR
// READS, and an operator reads whichever copy their installation shipped. A pin
// on the generator alone would be a pin on the recipe and not on the meal.
var openCodeBeltSources = []string{
	"internal/codegen/opencode_hooks.go",
	".opencode/plugins/pasture-lifecycle.ts",
	"internal/target/opencode/assets/hooks/pasture-hooks.ts",
}

// openCodeBeltLinePrefix identifies the console line inside each of those files.
const openCodeBeltLinePrefix = `console.error("Pasture did not evaluate " + event + " and returned no decision;`

// retiredFaultRecordPromise is the clause this line USED TO END WITH, and it
// was false on exactly the occasions the line is printed.
//
// A fault-record LOSS route is BY DEFINITION one where no record was written:
// the store path names no directory, the directory cannot be created, the file
// cannot be opened, the line cannot be appended, or the file cannot be closed.
// On every one of those the operator was sent to read a file that is not there,
// having just lost a gate evaluation. Standard error is the half that holds —
// pasture reports every such fault there, and reports there too when it could
// not place or write a record, quoting the path it tried.
const retiredFaultRecordPromise = "Read the pasture diagnostic on standard error and the lifecycle " +
	"fault record beside the pasture database."

// TestTheOpenCodeBeltPromisesOnlyWhatTheFaultRecordDelivers holds the console
// line of the OpenCode plugin's empty-body belt to what the product actually
// leaves behind.
//
// WHY A TEST AND NOT A REVIEW NOTE. The line is GENERATED. Correcting the
// generator without a pin leaves the wording resting on nothing: a later
// regeneration from a reverted source, or a hand edit of one shipped artefact,
// restores the false promise with every other guard green, because the drift
// guards compare the artefacts WITH the generator and would happily agree on
// the wrong sentence.
//
// WHAT IT REQUIRES. The retired clause appears in none of the three files; the
// line still sends the operator to standard error, which is the half that is
// true; the record is OFFERED and not PROMISED, with the reason a reader can
// act on; and all three files carry the SAME line, byte for byte.
//
// MUTATION: restore the retired clause in any one of the three files. This test
// turns RED on that file. MUTATION: reword the line in one artefact only, as a
// stale regeneration would. This test turns RED on the agreement assertion.
// MUTATION: drop the standard-error clause. This test turns RED.
func TestTheOpenCodeBeltPromisesOnlyWhatTheFaultRecordDelivers(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "resolve the repository root from cmd/pasture")

	lines := map[string]string{}
	for _, name := range openCodeBeltSources {
		raw, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		require.NoError(t, readErr,
			"%s carries the console line an OpenCode operator reads when pasture returns no "+
				"decision; if it has moved, this pin must move with it rather than pass "+
				"vacuously", name)
		text := string(raw)

		assert.NotContains(t, text, retiredFaultRecordPromise,
			"%s promises the operator a fault record UNCONDITIONALLY, and this line is printed "+
				"on the occasions where there is none: every route that loses the record leaves "+
				"no file to read. Offer the record and name what makes it missing, or say "+
				"nothing about it", name)

		var found string
		for _, candidate := range strings.Split(text, "\n") {
			if strings.Contains(candidate, openCodeBeltLinePrefix) {
				require.Empty(t, found,
					"%s prints this console line twice; two copies of one operator promise drift, "+
						"and only one of them is the one that ran", name)
				found = strings.TrimSpace(candidate)
			}
		}
		require.NotEmpty(t, found,
			"%s must still print the empty-body belt's console line. It is the ONLY report an "+
				"OpenCode operator gets on this route from the plugin itself, and this pin is "+
				"stated over its wording", name)
		lines[name] = found

		// THIS PIN READS WORDS, AND WHAT MAKES THE WORDS TRUE IS ELSEWHERE.
		// Its message used to justify itself by asserting the very belief it
		// exists to protect — that standard error "is where pasture reports
		// every such fault". A guard that states its premise as its reason
		// cannot fail when the premise is false, and that premise WAS false:
		// an unbindable payload left as a decision rather than a fault, so
		// pasture wrote nothing for the plugin to forward, and an operator who
		// followed this sentence found the belt line alone.
		//
		// The premise is held by tests that RUN things, and they are named here
		// so a maintainer who weakens one meets the other:
		// TestAnUnbindableHostPayloadIsTreatedAsAnEventThatWasNotEvaluated
		// requires pasture to produce the diagnostic at all, and
		// TestTheOpenCodeBeltSurfacesTheDiagnosticItSendsTheOperatorTo requires
		// the plugin to put it on the stream this sentence names. This
		// assertion only checks that the sentence still points there.
		assert.Contains(t, found, "Read the pasture diagnostic on standard error first",
			"%s must still send the operator to standard error, which is the half of the retired "+
				"sentence that was true. This assertion does NOT establish that a diagnostic is "+
				"there: that pasture writes one is held by "+
				"TestAnUnbindableHostPayloadIsTreatedAsAnEventThatWasNotEvaluated, and that the "+
				"plugin forwards it onto that stream is held by "+
				"TestTheOpenCodeBeltSurfacesTheDiagnosticItSendsTheOperatorTo. If either is "+
				"weakened, this sentence becomes an instruction to read an empty stream", name)
		// THE FILENAME COMES FROM THE PRODUCT CONSTANT AND NEVER FROM A
		// LITERAL HERE. Renaming lifecycleFaultRecordFile turns internal/codegen
		// RED but left cmd/pasture — which owns BOTH the constant and the
		// sentence that names the file — green for 203s, so the shipped
		// operator text could go false in silence while the product renamed the
		// file underneath it.
		assert.Contains(t, found, "A line may also have been appended to "+lifecycleFaultRecordFile,
			"%s must OFFER the durable record rather than promise it, and must name the file "+
				"THIS BUILD writes (%s): the file exists for most faults and for none of the "+
				"loss routes, and an operator needs to know which case they are in before they "+
				"go looking. If the product renamed the record file, the sentence renames with "+
				"it", name, lifecycleFaultRecordFile)
		assert.Contains(t, found, "could not be placed or written leaves none",
			"%s must say WHY the record may be absent. An offer with no reason leaves the "+
				"operator unable to tell a missing file from a wrong path", name)
	}

	for _, name := range openCodeBeltSources[1:] {
		assert.Equal(t, lines[openCodeBeltSources[0]], lines[name],
			"the generator and every generated artefact must carry the SAME operator line, byte "+
				"for byte. An operator reads whichever copy their installation shipped, so a "+
				"copy that says something else is a second promise nobody reviewed")
	}
}

// unbindableHostPayloads are, per harness, a host payload whose IDENTITY FIELDS
// have been RENAMED — the shape a host produces when it renames or drops a
// correlation field between versions, which the design invites by retaining the
// host version without using it as an admission check.
//
// Each is accompanied by the exact bytes that harness reads as "you may
// continue", because the claim under test is about the bytes a host receives.
var unbindableHostPayloads = []struct {
	Harness     string
	Event       string
	HostVersion string
	Payload     string
	Continue    string
	Identities  string
}{
	{
		Harness: "opencode", Event: "tool.execute.before", HostVersion: "1.18.10",
		Payload:    `{"input":{"session_id":"s","call_id":"c"},"output":{"args":{}}}`,
		Continue:   `{"decision":"proceed"}`,
		Identities: "session, tool-call",
	},
	{
		Harness: "codex", Event: "PreToolUse", HostVersion: "0.146.0",
		Payload:    `{"renamed_session":"s","hook_event_name":"PreToolUse"}`,
		Continue:   `{"continue":true}`,
		Identities: "session, turn, tool-call",
	},
	{
		// Claude's continuation IS the empty body, so this row's continue bytes
		// are empty on purpose. The claim it carries is the diagnostic and the
		// record, which are what a Claude operator has.
		Harness: "claude-code", Event: "PreToolUse", HostVersion: "2.1.222",
		Payload:    `{"renamed_session":"s","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{}}`,
		Continue:   "",
		Identities: "session",
	},
}

// TestAnUnbindableHostPayloadIsTreatedAsAnEventThatWasNotEvaluated drives the
// built binary against a HEALTHY store with a payload whose identity fields
// cannot be bound, and requires the full unevaluated-event answer.
//
// THE ROUTE THIS PINS PRODUCED NOTHING AT ALL. When the ingress parser could
// not classify a payload, the handler wrote its disposition receipt and
// returned a NIL ERROR. A nil error is how that handler says "evaluated", so
// the command took its SUCCESS path and asked the exit authority for a decision
// with EMPTY continuation bytes: exit 0, nothing on standard output, nothing on
// standard error, no fault record, and no fail-closed consideration. AN EVENT
// THAT WAS NEVER EVALUATED LEFT AS A DECISION.
//
// EVERY GUARD THIS FILE HOLDS WATCHES THE FAULT PATH, AND THIS ROUTE NEVER
// ENTERED IT. That is why it survived: it is a success path that succeeds at
// producing nothing. No broken database, no held lock, no old binary — one
// renamed identity field on a healthy machine was enough, on all three
// harnesses.
//
// THE HARM IS READ BY THE PLUGIN. On OpenCode an empty body reaches
// JSON.parse inside a GATE callback of an already-installed older plugin, which
// throws, and nothing catches it, so the user's tool call STOPS. That is the
// founding defect of this slice, and lifecycleContinuation's own comment says
// the continue bytes exist to protect exactly that reader. They were not
// delivered here.
//
// MUTATION, AT THE DEFECT SITE: return `backend.HostResponse{}, err` from the
// non-valid-capture arm of hookLifecycle in internal/handlers/hook_lifecycle.go,
// as it did. Every subtest turns RED — on the continue bytes for the two
// harnesses that have them, and on the diagnostic and the fault record for all
// three.
func TestAnUnbindableHostPayloadIsTreatedAsAnEventThatWasNotEvaluated(t *testing.T) {
	binary := lifecycleBinary(t)

	for _, row := range unbindableHostPayloads {
		t.Run(row.Harness, func(t *testing.T) {
			store := t.TempDir()
			database := filepath.Join(store, "pasture.db")
			initializeLifecycleTestDatabase(t, database)

			run := runLifecycleHookOn(t, binary, database,
				row.Harness, row.Event, row.HostVersion, []byte(row.Payload))

			require.Equal(t, 0, run.ExitCode,
				"an unbindable payload is a FAIL-OPEN fault by default: the host must be allowed to "+
					"carry on with its own answer, never blocked because pasture could not read the "+
					"payload.\nstderr: %s", run.Stderr)

			assert.Equal(t, row.Continue, strings.TrimSpace(run.Stdout),
				"the host must receive THIS harness's continue bytes. They were EMPTY on this route, "+
					"and on OpenCode an empty body reaches JSON.parse inside a gate callback of an "+
					"already-installed older plugin, which throws with nothing to catch it and stops "+
					"the user's tool call")

			require.Contains(t, run.Stderr, "could not be bound, so the event WAS NOT EVALUATED",
				"the operator must be told the event was NOT EVALUATED. This route said nothing at "+
					"all: zero bytes on standard error, on a healthy machine")
			assert.Contains(t, run.Stderr, row.Identities,
				"the diagnostic must NAME the identities that could not be bound, taken from this "+
					"build's generated registration; an operator told only that binding failed cannot "+
					"tell which correlation field their host renamed")
			assert.Contains(t, run.Stderr, "is the version the host actually runs",
				"the diagnostic must point at the host version too: this build retains the version "+
					"without using it as an admission check, so a host that changes a field name "+
					"between versions arrives here by construction")

			// THE DURABLE STATE IS READ ON THE RENDERED BYTES, not on the
			// value the code constructed. This route was told "no occurrence
			// was recorded for it" while its delivery row sat in the journal —
			// a sentence that was correct nowhere and was only ever visible on
			// the console. It is checked here, on what the operator receives,
			// for the same reason the identity names are.
			assert.Contains(t, run.Stderr, "durable state recorded",
				"the machine-readable stage must say the delivery WAS written, because it was")
			assert.Contains(t, run.Stderr, "the delivery for it IS committed in the lifecycle occurrence journal",
				"the operator must be sent to the row that exists. This said no occurrence was "+
					"recorded, which sends somebody looking for evidence they already have")
			assert.NotContains(t, run.Stderr, "no occurrence was recorded for it",
				"the retired sentence must not survive on any arm of the impact switch")

			records := readFaultRecords(t, run.FaultDir)
			assert.Len(t, records, 1,
				"an unevaluated event must leave a durable fault record like every other one. This "+
					"route wrote none, so nothing outlived the process to say the event was skipped")
		})
	}
}

// TestTheFailClosedOptInReachesAnUnbindableHostPayload requires the strict-gate
// opt-in to be LIVE on the unbindable route.
//
// WHY IT IS A SEPARATE CLAIM. The opt-in exists so that an operator who would
// rather stop than proceed unevaluated can say so. It worked on the fault path
// and was INERT here, because this route never became a fault: the event that
// was never evaluated is precisely the case a strict operator most wants
// covered, and it was the one case the switch could not reach.
//
// Claude's PreToolUse is the row driven because it is an evidenced blocking
// gate, so the evidence rule cannot demote it. A row with no host evidence
// stays continuing under the opt-in BY DESIGN, and its diagnostic says so.
//
// MUTATION: restore the nil-error return in the non-valid-capture arm. This
// test turns RED on the fail-closed exit code, which returns to 0.
func TestTheFailClosedOptInReachesAnUnbindableHostPayload(t *testing.T) {
	binary := lifecycleBinary(t)

	const payload = `{"renamed_session":"s","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{}}`

	open := t.TempDir()
	openDB := filepath.Join(open, "pasture.db")
	initializeLifecycleTestDatabase(t, openDB)
	openRun := runLifecycleHookOn(t, binary, openDB,
		"claude-code", "PreToolUse", "2.1.222", []byte(payload))
	require.Equal(t, 0, openRun.ExitCode,
		"the default is fail-open, so this row must let the host continue.\nstderr: %s", openRun.Stderr)

	closed := t.TempDir()
	closedDB := filepath.Join(closed, "pasture.db")
	initializeLifecycleTestDatabase(t, closedDB)
	closedRun := runLifecycleHookOn(t, binary, closedDB,
		"claude-code", "PreToolUse", "2.1.222", []byte(payload),
		"PASTURE_HOOK_FAIL_CLOSED=1")

	assert.Equal(t, 2, closedRun.ExitCode,
		"PASTURE_HOOK_FAIL_CLOSED must REACH this route. An event that could not be evaluated is "+
			"the case a strict operator most wants stopped, and the opt-in did nothing here while "+
			"working on every other fault: exit stayed 0 under both policies.\nstderr: %s",
		closedRun.Stderr)
	assert.Contains(t, closedRun.Stderr, "could not be bound, so the event WAS NOT EVALUATED",
		"the blocking exit must carry the same reason as the continuing one, so an operator who "+
			"turns the opt-in on learns WHY their host was stopped")
	assert.NotContains(t, openRun.Stderr, "no occurrence was recorded for it",
		"the fail-OPEN arm must not claim the delivery was lost either. The durable-state sentence "+
			"was written out in three places and two of them ignored the stage, so correcting one "+
			"arm left the others saying it — this reads the arm a default operator actually meets")
}

// TestEveryFaultRouteDeclaresAStageThatMatchesItsDurableState is the ROUTE half
// of the durable-state sweep. The stage half lives beside the renderer, in
// TestEveryFaultStageRendersItsOwnDurableStateOnEveryArm; neither is enough
// alone, because a route can declare a stage that renders perfectly and is
// still the wrong answer for that route.
//
// WHY IT ENUMERATES FROM THE SOURCE. Two routes declared a stage that was false
// about them, and both were found by asking "which routes exist?" rather than
// by checking the ones anybody remembered:
//
//   - THE DELIVERY THAT COULD NOT BE BOUND declared not-recorded, and its row
//     is in the journal.
//   - THE RECEIPT COMMITTED WITHOUT A CONTINUATION declared record-unknown, and
//     the error that marks it is NAMED for the commit pasture observed. Its own
//     declaration says the occurrence EXISTS. Nobody had looked at that pair
//     since the stage was introduced.
//
// So this test READS THE COMMAND'S SOURCE for every lifecycleFault call and
// requires each stage argument to be one this file has judged, by name. Adding
// a route without judging it fails here rather than shipping a sentence nobody
// checked.
//
// WHAT IT DOES NOT READ, STATED SO NOBODY RELIES ON IT. This checks WHICH stage
// each route declares, not whether that stage is TRUE of that route: swapping
// two judged stages between two routes passes here, because both expressions
// are in the table. Whether a route's stage is true of it is a judgement, made
// in the prose beside each call and measured by the behavioural tests above,
// which read the rendered bytes on a real store. What this catches is the case
// that produced both defects — a route whose stage nobody has looked at.
//
// MUTATION: add a lifecycleFault call whose stage expression is not in the
// table. This test turns RED naming the line.
func TestEveryFaultRouteDeclaresAStageThatMatchesItsDurableState(t *testing.T) {
	t.Parallel()

	// EVERY ROUTE, AND THE DURABLE STATE THAT IS TRUE OF IT. The value is the
	// reason, so that a maintainer changing one must state why rather than edit
	// a number.
	judged := map[string]string{
		// THE ROUTE LIST HERE WAS WRONG AT BOTH ENDS AND WAS PUBLISHED TWICE.
		// It named an ORPHANS route that does not exist — neither orphans file
		// holds a lifecycleFault call — and it OMITTED the flag-parse route
		// inside SetFlagErrorFunc, the only one outside lifecycleOutcome. Both
		// ends were found by enumerating from the source, which is what the
		// reason had claimed to do. The six real routes are below.
		"hostexit.FaultStageNotRecorded": "the route faults before any durable write, or the durable write itself " +
			"returned an error and committed nothing. Three routes pass it directly — the environment refusal and " +
			"the argument refusal, neither of which opens a store, and the flag-parse refusal inside " +
			"SetFlagErrorFunc, which runs before the command body — and it is also the default of the computed " +
			"stage below",
		"hostexit.FaultStageRecordUnknown": "the hook was abandoned at its deadline: the work runs in a goroutine and " +
			"the receipt commits before the native bytes are produced, so the expiry can land on either side of it " +
			"and pasture genuinely does not know",
		"hostexit.FaultStageRecorded": "pasture observed the commit: a receipt committed without a continuation, and " +
			"a delivery whose capture could not be bound, whose row carries the refusing disposition",
		"panicStage": "the panic recovery's local, which is not-recorded until the work goroutine is started and " +
			"record-unknown from that instant on; it is never recorded, because this recovery does not observe the " +
			"commit, only that one became possible",
		"stage": "the handler-error route's local, computed by the sentinel table faultStageByError, whose rows are " +
			"judged where they are written",
	}

	// EVERY NON-TEST SOURCE FILE OF THE PACKAGE, not one named file.
	//
	// THIS GUARD SAID "the command's source" AND PARSED ONE FILE, which is the
	// defect it exists to catch, committed by the fix for an instance of it. A
	// seventh route added to hook_lifecycle_orphans.go stayed INVISIBLE and the
	// count stayed at six; the same route in hook_lifecycle.go was RED. AND THE
	// FILE IT COULD NOT SEE IS THE ONE AN EARLIER CORRECTION HAD TO REMOVE FROM
	// THE ROUTE LIST — the guard against a mis-enumeration could not read the very file
	// the mis-enumeration named.
	fileSet := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err, "the package directory must be readable to find the sources it declares")
	sources := []*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fileSet, name, nil, 0)
		require.NoError(t, parseErr, "every production source of this package must parse: %s", name)
		sources = append(sources, parsed)
	}
	require.NotEmpty(t, sources,
		"this sweep reads the package's own production sources; finding none means it is looking in "+
			"the wrong directory and every assertion below would pass vacuously")

	found := map[string]int{}
	unjudged := []string{}
	inspect := func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		name, isIdentifier := call.Fun.(*ast.Ident)
		if !isIdentifier || name.Name != "lifecycleFault" {
			return true
		}
		// The stage is the sixth argument of lifecycleFault.
		require.Len(t, call.Args, 7,
			"lifecycleFault takes seven arguments; this test reads the stage by position and must "+
				"be updated with the signature")
		stage := sourceOf(call.Args[5])
		found[stage]++
		if _, isJudged := judged[stage]; !isJudged {
			position := fileSet.Position(call.Lparen)
			unjudged = append(unjudged, fmt.Sprintf("%s:%d passes %s",
				filepath.Base(position.Filename), position.Line, stage))
		}
		return true
	}
	for _, source := range sources {
		ast.Inspect(source, inspect)
	}

	require.NotEmpty(t, found,
		"this test reads every lifecycleFault call in the command; finding none means the call was "+
			"renamed and the sweep is passing vacuously")
	assert.Empty(t, unjudged,
		"a fault route declares a durable state this file has not judged. The stage is a CLAIM ABOUT "+
			"THE JOURNAL made to the operator, and two routes have already claimed one that was false "+
			"about them. Add the stage to the table above with the reason it is true for this route, "+
			"or use one that already is")

	// The recorded stage must actually be REACHED. A stage nothing passes is a
	// sentence nothing renders, and this one was added because two routes
	// needed it.
	assert.NotZero(t, found["stage"],
		"the handler-error route computes its stage in a local, and that local is where the two "+
			"recorded cases are selected; if it is gone, the recorded stage has no producer here")

	// THE ROUTE COUNT IS PINNED, because the published enumeration was wrong at
	// both ends: it invented one route and missed another, and no count stood
	// between the prose and the source. Six is what the source holds.
	total := 0
	for _, count := range found {
		total += count
	}
	assert.Equal(t, 6, total,
		"this command has SIX fault routes: the panic recovery, the environment refusal, the argument "+
			"refusal, the deadline abandonment, the handler error, and the flag-parse refusal inside "+
			"SetFlagErrorFunc. A route added or removed without updating the judged reasons above leaves "+
			"the prose describing a command that does not exist, which has happened once already")
}

// TestTheRoutesThatNeverOpenAStoreSayTheDeliveryWasNotRecorded drives, on the
// built binary, the two fault routes that had no behavioural pin at all.
//
// WHY THESE TWO. Four of the six routes are measured on host bytes somewhere in
// this file. The FLAG-PARSE refusal inside SetFlagErrorFunc and the ARGUMENT
// refusal were not, and both declare not-recorded. Mutating either to declare
// "recorded" left the whole cmd/pasture package GREEN — over 208 seconds each —
// while an operator was told THE DELIVERY IS COMMITTED IN THE JOURNAL about an
// invocation THAT NEVER OPENED A STORE. Nothing was written, nothing could be,
// and the message sent them to look for a row that cannot exist.
//
// NEITHER NEEDS A STORE, A LOCK OR A FIXTURE: an unparseable flag and a
// positional argument are both refused before the command body runs.
//
// MUTATION, AT THE DEFECT SITE: change either route's stage argument to
// hostexit.FaultStageRecorded. That subtest turns RED on the durable-state
// assertion.
//
// WHAT IT VISITS: the TWO routes that refuse before the command body runs,
// written out because each is one invocation shape and no source lists them.
// WHAT IT DOES NOT READ: the other four fault routes, which have their own
// behavioural pins; and it does not check that the route SET is complete, which
// the route sweep reads from the package source.
func TestTheRoutesThatNeverOpenAStoreSayTheDeliveryWasNotRecorded(t *testing.T) {
	binary := lifecycleBinary(t)

	for _, row := range []struct {
		Name string
		Args []string
		Says string
	}{
		{
			Name: "the flag-parse refusal",
			Args: []string{"hook", "lifecycle", "--harness", "claude-code", "--event", "PreToolUse",
				"--host-version", "2.1.222", "--not-a-flag"},
			Says: "could not parse its flags",
		},
		{
			Name: "the argument refusal",
			Args: []string{"hook", "lifecycle", "--harness", "claude-code", "--event", "PreToolUse",
				"--host-version", "2.1.222", "an-unexpected-argument"},
			Says: "",
		},
	} {
		t.Run(row.Name, func(t *testing.T) {
			store := t.TempDir()
			command := exec.Command(binary, append([]string{
				databaseFlagName.Argument(), filepath.Join(store, "pasture.db")}, row.Args...)...)
			command.Stdin = bytes.NewReader(nil)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			_ = command.Run()

			require.NotEmpty(t, stderr.String(),
				"this route must reach a diagnostic; if it does not, the assertions below prove nothing")
			if row.Says != "" {
				require.Contains(t, stderr.String(), row.Says,
					"the subtest must drive the route it names, not some other refusal")
			}

			assert.Contains(t, stderr.String(), "durable state not-recorded",
				"this route refuses BEFORE the command body opens anything, so nothing was written and "+
					"nothing could have been")
			assert.Contains(t, stderr.String(), "no occurrence was recorded for it",
				"and the sentence beside the machine-readable stage must say the same thing")
			assert.NotContains(t, stderr.String(), "IS committed in the lifecycle occurrence journal",
				"an operator must never be sent to look for a row on an invocation that never opened a "+
					"store. Declaring the recorded stage here left the whole package green while saying "+
					"exactly that")

			_, statErr := os.Stat(filepath.Join(store, "pasture.db"))
			assert.True(t, os.IsNotExist(statErr),
				"the claim under test is that NOTHING was written; if a store exists, not-recorded is "+
					"the wrong stage and this test is asserting the wrong thing")
		})
	}
}

// TestEveryWorkErrorMapsToTheDurableStateItCanSupport pins the ERROR-TO-STAGE
// mapping as a population, over the sentinel errors themselves.
//
// WHY IT EXISTS. An earlier change corrected two of these rows for stating something
// false about the journal, and NOTHING HELD EITHER CORRECTION: reverting one
// left the whole cmd/pasture package green for 213 seconds, because the
// sentinel errors appeared in NO test file anywhere in the tree. The route
// sweep could not see it — that is its own stated limit, which reads WHICH
// stage a route declares and not whether the stage is true — and it was biting
// on a route the same round had just corrected.
//
// It reads the production table directly, so a row added without a judgement
// fails here, and each row's declared stage is checked against what that
// sentinel actually means.
//
// MUTATION: change any row's stage, or delete a row so its error falls through
// to the default. This test turns RED naming the sentinel.
func TestEveryWorkErrorMapsToTheDurableStateItCanSupport(t *testing.T) {
	t.Parallel()

	// What each sentinel MEANS about the journal, judged here and independently
	// of the production table, so the two must agree rather than one restating
	// the other.
	judged := map[string]struct {
		Stage hostexit.FaultStage
		Why   string
	}{
		"the lifecycle receipt was committed but the host received no continuation": {
			Stage: hostexit.FaultStageRecorded,
			Why:   "the error is named for a commit pasture observed, so the row is certainly there",
		},
		"the lifecycle delivery was recorded but its capture could not be bound": {
			Stage: hostexit.FaultStageRecorded,
			Why:   "the delivery row is written with the disposition that refused it",
		},
		"the lifecycle work panicked after it began": {
			Stage: hostexit.FaultStageRecordUnknown,
			Why:   "the panic landed after the work started, so the commit may or may not have completed",
		},
		"the lifecycle fault happened before any durable write was attempted": {
			Stage: hostexit.FaultStageNotRecorded,
			Why: "the handler refused before it ATTEMPTED A WRITE, so no row can exist; this row is " +
				"the EVIDENCE that lets the ordinary refusals keep the precise claim now that the " +
				"default is the weakest one. It is defined by the WRITE and not by the open: this " +
				"judgement claims to be reached independently of the production table, and while " +
				"it said 'before it opened the store' it was inheriting a definition the region " +
				"itself had already moved away from — true conclusion, false reason",
		},
	}

	require.Len(t, faultStageByError, len(judged),
		"every row of the production mapping must be judged here, and every judgement must "+
			"correspond to a row; a row added without one is a durable-state claim nobody checked")

	for _, row := range faultStageByError {
		expected, isJudged := judged[row.Err.Error()]
		require.True(t, isJudged,
			"the sentinel %q is mapped to a durable state that this test has not judged", row.Err)
		assert.Equal(t, expected.Stage, row.Stage,
			"%q must map to %q because %s; it maps to %q",
			row.Err, expected.Stage.String(), expected.Why, row.Stage.String())
		assert.NotEmpty(t, row.Why,
			"every row states WHY its stage is true of it, so the next reader need not re-derive it")
		require.True(t, row.Stage.IsValid(),
			"a row may not map an error to an undeclared stage")

		// The mapping is reached through errors.Is, so a WRAPPED sentinel must
		// resolve the same way: every producer wraps its cause.
		wrapped := fmt.Errorf("the hook could not evaluate this event: %w: %w", row.Err, errors.New("a cause"))
		assert.Equal(t, row.Stage, faultStageForWorkError(wrapped),
			"%q must map the same when wrapped, because every producer wraps it", row.Err)
	}

	// THE DEFAULT IS THE WEAKEST CLAIM, AND IT USED TO BE THE STRONGEST ONE
	// POINTING THE OTHER WAY. not-recorded is not weak: it ASSERTS that nothing
	// was written. The journal appender can fail after its commit succeeded —
	// its own diagnostic says "the operation reported success" — and carries no
	// sentinel, so that assertion reached an operator about a committed row.
	assert.Equal(t, hostexit.FaultStageRecordUnknown,
		faultStageForWorkError(errors.New("an error no row names")),
		"an unmapped error must take the WEAKEST claim. not-recorded ASSERTS that nothing was "+
			"written, which is a promise about every error nobody enumerated, and at least one "+
			"unsentinelled error can be raised after a successful commit")
	assert.Equal(t, hostexit.FaultStageRecordUnknown, faultStageForWorkError(nil),
		"and a nil error must not reach a stronger claim either")

	// The precise answer for the ordinary refusals is EVIDENCE, not assumption:
	// the handler says NO WRITE WAS ATTEMPTED, and only then is not-recorded
	// claimed. It is the write and not the open: an open creates a file and no
	// occurrence, and refusals between the two are inside the sentinel's reach
	// while being outside any region the open would have defined.
	assert.Equal(t, hostexit.FaultStageNotRecorded,
		faultStageForWorkError(fmt.Errorf("%w: %w",
			handlers.ErrLifecycleBeforeDurableWrite, errors.New("the event is not registered"))),
		"a refusal that reports it happened before any durable write keeps the precise claim, "+
			"because the site that knows said so")
}

// panickingCommitBarrier panics AFTER the durable receipt is committed. It is
// injected through the barrier parameter the command already has, so no
// production branch exists whose only user is a test.
type panickingCommitBarrier struct{ message string }

func (b panickingCommitBarrier) AfterCommit(context.Context, handlers.CommitBoundary) error {
	panic(b.message)
}

// TestAPanicAfterTheCommitDoesNotClaimTheDeliveryWasNotRecorded drives the
// panic recovery on the far side of the durable write.
//
// THE MEASURED DEFECT. A panic injected at the commit boundary reported
// "durable state not-recorded" and "no occurrence was recorded for it" WHILE
// THE JOURNAL HELD THE ROW WITH A FULL INTERPRETED SET. The recovery covered
// the whole invocation and declared one stage for all of it, which is true only
// until the work begins.
//
// WHICH RECOVERY THIS DRIVES, STATED BECAUSE I FIRST GOT IT WRONG. The barrier
// runs INSIDE the work goroutine, so this panic is caught by the goroutine's
// own recover and travels back as a wrapped sentinel that the error-to-stage
// table answers for. It does NOT exercise the outer recovery's local. I
// discovered that by mutation: deleting the line that widens the outer local
// leaves this test GREEN, so citing it for that line would have been a dead
// pin of the kind this slice keeps producing. The outer local is held
// separately and structurally, by
// TestTheOuterPanicRecoveryNeverClaimsMoreThanItsRegionCanSupport, which says
// why structure is the only witness available for it.
//
// THE RULE BOTH RECOVERIES IMPLEMENT. A recovery may claim only the WEAKEST
// TRUE claim for the region it stands in: not-recorded before the work begins,
// record-unknown from that instant on, and NEVER recorded — because neither
// recovery OBSERVES the commit, only that one became possible.
//
// MUTATION, AT THE DEFECT SITE: delete the errLifecycleWorkPanicked row from
// the stage table, or change its stage. This test turns RED on the
// durable-state assertions, and the row is in the journal either way.
func TestAPanicAfterTheCommitDoesNotClaimTheDeliveryWasNotRecorded(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)

	cmd := lifecycleTestCommand(t, "opencode", "tool.execute.before", "1.18.10", dbPath)
	cmd.SetIn(bytes.NewReader(openCodeToolExecuteBeforeWire(t)))

	outcome := lifecycleOutcome(cmd, nil,
		panickingCommitBarrier{message: "the commit boundary failed after the receipt was written"},
		timeouts.ProductionProfile(), context.WithTimeout)

	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"a pasture panic must still let the host carry on")
	require.Contains(t, outcome.Stderr, "the hook panicked",
		"this test must drive the panic path; if it does not, the assertions below prove nothing")

	assert.Contains(t, outcome.Stderr, "durable state record-unknown",
		"the commit had already happened when this panic landed, so the recovery may not say the "+
			"delivery was never written. It said not-recorded while the row sat in the journal")
	assert.Contains(t, outcome.Stderr, "MAY OR MAY NOT exist",
		"and the sentence must match the machine-readable stage: pasture knows the write became "+
			"possible, not that it completed")
	assert.NotContains(t, outcome.Stderr, "no occurrence was recorded for it",
		"this is the sentence that was false: an operator was told nothing was recorded and the "+
			"journal held the row with a full interpreted set")
	assert.NotContains(t, outcome.Stderr, "IS committed in the lifecycle occurrence journal",
		"and the recovery must not overreach the other way either: it does not OBSERVE the commit, "+
			"so it may not claim one")
}

// TestTheOuterPanicRecoveryNeverClaimsMoreThanItsRegionCanSupport reads the
// outer panic recovery's stage local.
//
// WHY STRUCTURE IS THE ONLY WITNESS HERE, STATED RATHER THAN DRESSED UP. The
// outer recovery covers the whole command body, and the region it covers AFTER
// the work goroutine starts is: a select on the completion channel and the
// context; a read of the work struct; the deferred cancel, whose statement
// stands above the go statement and runs on return; no composite literal; and
// these EIGHT CALLS, in source order:
//
//	ctx.Done()                in the select
//	lifecycleFault(...)       the deadline arm
//	fmt.Errorf(...)           that arm's message
//	ctx.Err()                 wrapped into it
//	faultStageForWorkError()  the handler-error arm
//	lifecycleFault(...)       that arm
//	fmt.Errorf(...)           its message
//	hostexit.ForDecision()    the success path
//
// THE ENUMERATION HAS BEEN WIDENED TWICE AND BOTH TIMES BY THE CONSTRUCTS
// SOMEBODY FOUND INTERESTING. It first named four cheap statements and no
// calls; then four calls and not the four beside them. The omitted four were
// ctx.Done, ctx.Err and BOTH fmt.Errorf calls — and a %w-composing fmt.Errorf
// is not a neutral construct in this command's history, where a nil cause once
// panicked the record writer. The one call family with a panic record here was
// the one the enumeration kept leaving out. So the list above is no longer
// re-read: this test COUNTS the calls and the literals after the go statement
// and fails when either differs from what is written here, which is what turns
// the paragraph from a claim somebody re-examined into one the tree holds.
//
// THE CONCLUSION IS UNCHANGED AND EACH IS CHECKED: ctx.Done and ctx.Err read a
// context that is non-nil for the whole region; both fmt.Errorf calls compose
// values already in hand; faultStageForWorkError walks a table; ForDecision
// composes an outcome. One of the eight is the recovery's OWN handler, so a
// panic inside lifecycleFault would be caught by the defer that called it.
//
// So no input drives it, and no behavioural test can. The local is defensive:
// it exists so that the next statement added to that region cannot inherit a
// claim nobody re-examined, which is exactly how the recovery came to say
// not-recorded about an invocation whose row was already committed.
//
// WHAT IT REQUIRES. The local starts at the weakest claim; it is assigned
// exactly once more, to record-unknown, and that assignment stands ABOVE the
// statement that starts the work; and it is NEVER assigned the recorded stage,
// because this recovery does not observe the commit.
//
// IT IS ALSO A GUARD AGAINST THE FIX ITSELF GROWING. One local set once is the
// design; a running "stage so far" updated at each step would reintroduce the
// shared mutable claim this package removed by making the stage a required
// argument with a refused zero value. A third assignment fails here.
//
// MUTATION: delete the widening assignment, move it below the `go` statement,
// give the local a third assignment, or assign it the recorded stage. This test
// turns RED on each. Add a call or a composite literal after the `go`
// statement: it turns RED naming the region's new contents.
func TestTheOuterPanicRecoveryNeverClaimsMoreThanItsRegionCanSupport(t *testing.T) {
	t.Parallel()

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "hook_lifecycle.go", nil, 0)
	require.NoError(t, err, "the production source must be readable beside its test")

	var outcome *ast.FuncDecl
	for _, node := range file.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Name.Name == "lifecycleOutcome" {
			outcome = function
			break
		}
	}
	require.NotNil(t, outcome, "lifecycleOutcome must exist: it is where the outer recovery stands")

	const local = "panicStage"
	assignments := []struct {
		Value string
		Line  int
		Pos   token.Pos
	}{}
	ast.Inspect(outcome, func(node ast.Node) bool {
		assign, isAssign := node.(*ast.AssignStmt)
		if !isAssign || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		if name, isIdentifier := assign.Lhs[0].(*ast.Ident); !isIdentifier || name.Name != local {
			return true
		}
		assignments = append(assignments, struct {
			Value string
			Line  int
			Pos   token.Pos
		}{Value: sourceOf(assign.Rhs[0]), Line: fileSet.Position(assign.TokPos).Line, Pos: assign.TokPos})
		return true
	})

	// The local is DECLARED in the var block the recovery reads — nothing but a
	// declaration may precede the recover — so its initial value is checked
	// there and its widening is the only assignment.
	var declared string
	ast.Inspect(outcome, func(node ast.Node) bool {
		spec, isSpec := node.(*ast.ValueSpec)
		if !isSpec || len(spec.Values) != 1 {
			return true
		}
		for _, name := range spec.Names {
			if name.Name == local {
				declared = sourceOf(spec.Values[0])
			}
		}
		return true
	})
	assert.Equal(t, "hostexit.FaultStageNotRecorded", declared,
		"before the work starts, nothing can have been written, so the recovery begins at the "+
			"weakest claim; it must also be DECLARED rather than assigned, because a statement "+
			"before the recover could panic while no recovery exists")

	require.Len(t, assignments, 1,
		"the outer recovery's stage is ONE LOCAL WIDENED ONCE, where the work begins. A second "+
			"assignment is a running \"stage so far\", which is the shared mutable claim this "+
			"package removed by making the stage a required argument with a refused zero value; "+
			"found %d assignments", len(assignments))
	assert.Equal(t, "hostexit.FaultStageRecordUnknown", assignments[0].Value,
		"once the work has begun the commit MAY have happened, and record-unknown is exactly that "+
			"much knowledge. It must never be the recorded stage: this recovery does not observe "+
			"the commit, only that one became possible")

	// The widening must stand ABOVE the statement that starts the work, or the
	// region between them inherits a claim that is already too strong.
	var goStatement token.Pos
	ast.Inspect(outcome, func(node ast.Node) bool {
		if start, isGo := node.(*ast.GoStmt); isGo && !goStatement.IsValid() {
			goStatement = start.Go
		}
		return true
	})
	require.True(t, goStatement.IsValid(),
		"lifecycleOutcome must still start its work in a goroutine; that statement is what the "+
			"widening below is positioned against")
	assert.Less(t, int(assignments[0].Pos), int(goStatement),
		"the widening must stand ABOVE the `go` statement at hook_lifecycle.go:%d. Below it, a panic "+
			"raised between the two would be reported as not-recorded although the work had begun",
		fileSet.Position(goStatement).Line)

	// THE REGION IS COUNTED, NOT RE-READ. Everything positioned after the go
	// statement ends is the outer recovery's region; the calls in it, in
	// source order, and the composite literals in it are what the doc above
	// enumerates, and a statement added there changes this list.
	var goEnd token.Pos
	ast.Inspect(outcome, func(node ast.Node) bool {
		if start, isGo := node.(*ast.GoStmt); isGo && !goEnd.IsValid() {
			goEnd = start.End()
		}
		return true
	})
	calls := []string{}
	literals := 0
	ast.Inspect(outcome, func(node ast.Node) bool {
		if node == nil || node.Pos() <= goEnd {
			return true
		}
		switch typed := node.(type) {
		case *ast.CallExpr:
			calls = append(calls, sourceOf(typed.Fun))
		case *ast.CompositeLit:
			literals++
		}
		return true
	})
	assert.Equal(t, []string{
		"ctx.Done", "lifecycleFault", "fmt.Errorf", "ctx.Err",
		"faultStageForWorkError", "lifecycleFault", "fmt.Errorf", "hostexit.ForDecision",
	}, calls,
		"the calls after the go statement are not the eight the doc of this test enumerates. The "+
			"conclusion that none of them can panic was checked against THAT list, so a call added "+
			"or removed here must be re-examined and the enumeration rewritten with it")
	assert.Zero(t, literals,
		"the region after the go statement now holds %d composite literal(s), and the doc of this "+
			"test says it holds none; re-examine whether the new one can panic and rewrite the "+
			"enumeration", literals)
}

// claudePayloadWithAddedMember is the authentic Claude fixture plus ONE member
// this build does not declare: every identity present, correctly named and
// usable, and refused all the same.
func claudePayloadWithAddedMember(t *testing.T) []byte {
	t.Helper()
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(claudeFixture(t, "pre_tool_use_2_1_222.json"), &members),
		"the committed fixture must decode")
	members["a_field_this_build_does_not_declare"] = json.RawMessage(`"x"`)
	extended, err := json.Marshal(members)
	require.NoError(t, err)
	return extended
}

// TestEachRefusalDispositionCarriesTheFixThatFollowsIt drives one payload per
// producible disposition through the built binary and reads what the operator
// is told to DO about it.
//
// THE DEFECT. Only the event-mismatch disposition had a fix of its own. A
// malformed, invalid-UTF-8 or duplicate-field payload was told to CHECK THE
// IDENTITY FIELD NAMES AND THE HOST VERSION — and on those routes ingress
// stopped at the decode, so NEITHER WAS EVER INSPECTED. The sentence was true
// of the product and pointed at the wrong thing, which costs its reader the
// same hour a false one does.
//
// AND THE IDENTITY CLAUSE FOLLOWS THE DIAGNOSIS TOO. A payload that never
// decoded never reached the identity lookup, so naming the identities that went
// unbound described a step that did not run.
//
// MUTATION: give any disposition a fix belonging to another, or restore the
// identity clause on a route that never reached the identities. That subtest
// turns RED.
// WHAT IT VISITS: one payload per disposition this build states advice for,
// on the harness whose parser can produce it, driven through the built
// binary; the disposition set itself is derived from the advice table.
// WHAT IT DOES NOT READ: which of a disposition's several CAUSES fired —
// the parser does not report that — and every harness for every row. It
// drove ONE harness until a harness-specific contradiction survived it.
func TestEachRefusalDispositionCarriesTheFixThatFollowsIt(t *testing.T) {
	binary := lifecycleBinary(t)

	const identityAdvice = "Compare the payload with this build's"
	const versionAdvice = "is the version the host actually runs"

	refusals := []struct {
		Name            string
		Payload         []byte
		Says            string
		Tells           string
		NamesIdentities bool
		// IdentityClause is the exact clause the message must carry, its full
		// stop included, or empty where the message must name no identity.
		IdentityClause string
		// Event is the coordinate to drive; empty means PreToolUse, which
		// requires two identities. A SINGLE-identity event is driven too,
		// because no row drove one and the singular arm therefore rendered
		// text nothing had ever read.
		Event string
		// Harness is the parser to drive; empty means claude-code.
		//
		// EVERY ROW HERE WAS claude-code, AND THAT IS WHY A HARNESS-SPECIFIC
		// CONTRADICTION SURVIVED THIS GUARD. The reason offered "or the payload
		// carries a member the registration does not allow" to parsers that
		// ignore such members, and this sweep could not see it because on the
		// one harness it drove, the clause is TRUE. Neither of its two stated
		// limits said it reads one harness.
		Harness string
		// MentionsVersion says whether the HOST VERSION is a plausible cause of
		// THIS refusal, and it is judged per disposition rather than derived
		// from NamesIdentities. The two are not the same question: an event
		// mismatch never reaches the identity lookup, yet the version is a real
		// cause of it, because the event a host reports can change between
		// versions. A decode failure has neither.
		MentionsVersion bool
	}{
		{
			Name:    "a payload that is not well-formed JSON",
			Payload: []byte(`{"session_id":`),
			Says:    "the payload is not a JSON object, so no field could be read from it",
			Tells:   "Send a JSON OBJECT",
		},
		{
			Name:    "a payload that is not valid UTF-8",
			Payload: []byte{'{', '"', 's', '"', ':', '"', 0xff, 0xfe, '"', '}'},
			Says:    "the payload is not valid UTF-8, so it was never decoded",
			Tells:   "Send UTF-8",
		},
		{
			Name:    "a payload that repeats a field",
			Payload: []byte(`{"session_id":"a","session_id":"b","hook_event_name":"PreToolUse","tool_name":"R","tool_input":{}}`),
			Says:    "the payload repeats a field",
			Tells:   "Send each field once",
		},
		{
			Name:            "a payload whose identity fields are renamed",
			Payload:         []byte(`{"renamed":"s","hook_event_name":"PreToolUse","tool_name":"R","tool_input":{}}`),
			Says:            "either an identity field is missing, renamed or unusable",
			Tells:           identityAdvice,
			NamesIdentities: true,
			IdentityClause:  "; the identities this event requires are session, tool-call.",
			MentionsVersion: true,
		},
		{
			// THE ROW THE WRITTEN LIST OMITTED, which is why nothing caught
			// the inspection result invented for it. It shares a disposition
			// with the renamed-identity row and differs in the cause that
			// fired.
			Name:            "a payload carrying a member the registration does not declare",
			Payload:         claudePayloadWithAddedMember(t),
			Says:            "carries a member the registration does not allow",
			Tells:           identityAdvice,
			NamesIdentities: true,
			IdentityClause:  "; the identities this event requires are session, tool-call.",
			MentionsVersion: true,
		},
		{
			// A SINGLE-IDENTITY EVENT, which no row drove. The singular arm of
			// the identity clause therefore rendered text nothing had ever
			// read, and a reviewer restored the retired singular literal with
			// the whole tree green.
			Name:            "a renamed identity on an event that requires only one",
			Event:           "SessionStart",
			Payload:         []byte(`{"renamed":"s","hook_event_name":"SessionStart"}`),
			Says:            "either an identity field is missing, renamed or unusable",
			Tells:           identityAdvice,
			NamesIdentities: true,
			IdentityClause:  "; the identity this event requires is session.",
			MentionsVersion: true,
		},
		{
			// THE SAME DISPOSITION ON A LENIENT PARSER. The row above drives it
			// on the validating parser, where every clause of the reason is
			// true; this drives it where one clause is not, which is the case
			// the single-harness sweep could not see.
			Name:            "a renamed identity on a parser that decodes into a struct",
			Harness:         "codex",
			Payload:         []byte(`{"renamed":"s","hook_event_name":"PreToolUse"}`),
			Says:            "an identity field is missing or unusable",
			Tells:           identityAdvice,
			NamesIdentities: true,
			IdentityClause:  "; the identities this event requires are session, turn, tool-call.",
			MentionsVersion: true,
		},
		{
			Name:            "a payload that declares a different event",
			Payload:         []byte(`{"session_id":"s","hook_event_name":"SessionEnd","tool_name":"R","tool_input":{}}`),
			Says:            "the payload does not report this event — the field is absent, unreadable, or names a different event",
			Tells:           "Invoke the hook with the event the payload actually describes",
			MentionsVersion: true,
		},
	}
	// EVERY DISPOSITION THIS BUILD STATES ADVICE FOR MUST BE DRIVEN HERE,
	// derived from the production advice table rather than trusted to a list.
	//
	// THE LIMIT, STATED, BECAUSE IT IS THE ONE THAT BIT. This derives
	// DISPOSITIONS, and the population that matters is CAUSES. One disposition
	// carries three of them, and the row that exposed an inspection result
	// invented for a step that never ran was missing from the written list
	// while its disposition was already covered — so this derivation would NOT
	// have caught that omission either. Enumerating causes needs the classifier
	// split, which lives in the ingress and the occurrence model and is not
	// this slice's to make. What this closes is a disposition added with no
	// payload driving it at all.
	// THE DRIVEN SET, NOT ITS CARDINALITY. The sentence said EVERY DISPOSITION
	// must be driven and the check compared COUNTS: replacing the
	// event-mismatch row with a second renamed-identity row kept the total and
	// left that disposition driven by nothing at all. Comparing the set is what
	// the sentence already claimed, and it retires the bare "+N" with it.
	for disposition := range handlers.CaptureDispositionAdvice() {
		// AGAINST EVERY REASON THIS DISPOSITION CAN RENDER, not one string. One
		// reason is composed from the parser that refused, so asking against a
		// single text made a composed disposition look undriven.
		driven := false
		for _, reason := range handlers.CaptureDispositionReasons(disposition) {
			for _, row := range refusals {
				if strings.Contains(reason, row.Says) {
					driven = true
					break
				}
			}
		}
		assert.True(t, driven,
			"disposition %d states advice that NO payload here drives. A count cannot see this: "+
				"swapping one row for a duplicate of another keeps the total while a disposition "+
				"goes unread on a real invocation, which is the difference between a set and its "+
				"size", uint8(disposition))
	}

	for _, row := range refusals {
		t.Run(row.Name, func(t *testing.T) {
			store := t.TempDir()
			database := filepath.Join(store, "pasture.db")
			initializeLifecycleTestDatabase(t, database)

			event := row.Event
			if event == "" {
				event = "PreToolUse"
			}
			harness, version := row.Harness, "2.1.222"
			if harness == "" {
				harness = "claude-code"
			}
			if harness == "codex" {
				version = "0.146.0"
			}
			run := runLifecycleHookOn(t, binary, database,
				harness, event, version, row.Payload)

			// THE REASON AND THE REMEDY MUST NOT DISAGREE. One offered an
			// added member as a possible CAUSE while the other, three clauses
			// later in the same rendered line, said added members are IGNORED.
			// A reader cannot act on a sentence that argues with itself.
			//
			// IT STANDS ABOVE THE DISPOSITION CHECK ON PURPOSE. The mutation
			// that restores the shared reason also changes the words the
			// lenient row is driven by, so the require below would stop this
			// subtest with "not driven" and hide the assertion that names the
			// defect.
			if strings.Contains(run.Stderr, "Members this build does not declare are IGNORED") {
				assert.NotContains(t, run.Stderr, "carries a member the registration does not allow",
					"this message offers an added member as a possible cause AND says added members "+
						"are ignored. The remedy half was repaired and the diagnosis half was left, "+
						"so one message now contradicts itself on every harness whose parser "+
						"decodes into a struct")
			}

			require.Contains(t, run.Stderr, row.Says,
				"this subtest must drive the disposition it names; if it does not, everything below "+
					"is about some other refusal")
			assert.Contains(t, run.Stderr, row.Tells,
				"the instruction must follow THIS diagnosis. Every disposition but one was told to "+
					"check identity field names and the host version, on routes where neither was "+
					"ever inspected")

			if row.MentionsVersion {
				assert.Contains(t, run.Stderr, versionAdvice,
					"the host version is a plausible cause of THIS refusal, so the reader is told to "+
						"check it")
			} else {
				assert.NotContains(t, run.Stderr, versionAdvice,
					"the host version was never inspected on this route and could not have caused "+
						"the refusal, so sending the reader to check it costs them an hour")
			}

			// THE CLAUSE IS PINNED WHOLE, INCLUDING ITS FULL STOP.
			//
			// It was NotContains("was not bound"), AND THAT STRING IS IN NO
			// PRODUCTION TEXT — it lives only in two comments — so the
			// assertion could never fire. Restoring the result claim in any
			// other wording, "... are session, tool-call, and none of them was
			// bound", left the whole tree green while the binary told an
			// operator that identities it never inspected had failed to bind.
			// A negative on one phrasing recognises that phrasing and nothing
			// else; the clause ENDS at its full stop, so pinning it whole
			// refuses every continuation of it.
			if row.IdentityClause != "" {
				assert.Contains(t, run.Stderr, row.IdentityClause,
					"the identity clause must be EXACTLY %q. The message may NAME the identities "+
						"this event requires — a reader needs them to check anything — and may "+
						"assert NOTHING about them, because the three causes of this disposition "+
						"disagree over whether the identity loop ran at all", row.IdentityClause)
				return
			}
			assert.NotContains(t, run.Stderr, "identities this event requires",
				"this route never looked at the identities — ingress stopped at the decode — so "+
					"naming them at all points the reader at the wrong thing")
			assert.NotContains(t, run.Stderr, identityAdvice,
				"and it must not send the reader to compare fields against a registration that was "+
					"never consulted")
		})
	}
}

// TestAdviceFollowsTheCauseAndNotTheClassifier drives the two causes that share
// the record-unknown stage and requires each to be told only what is true of it.
//
// THE DEFECT. The remedy "a long-running writer holding the pasture store is
// the usual reason, so find that writer or retry once it releases the store"
// was keyed on the STAGE. That stage had one producer when the sentence was
// written and now has three — the abandoned deadline, a panic raised after the
// work began, and any error the stage table does not recognise — so a panicking
// invocation was sent hunting for a writer that is not there, on the same line
// whose cause says the work panicked.
//
// THE GENERAL FORM, which is the reason this test is a pair and not a case:
// when a classifier gains a producer, every sentence keyed on that classifier
// has to be re-asked. A sentence that was true of the only producer is not
// thereby true of the classifier.
//
// MUTATION: move the writer remedy back onto the record-unknown arm of the
// impact switch. The panic subtest turns RED; the deadline subtest stays green,
// which is what shows the sentence is right for one cause and wrong for the
// other.
func TestAdviceFollowsTheCauseAndNotTheClassifier(t *testing.T) {
	const writerRemedy = "holding the pasture store"

	t.Run("a panic after the work began", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
		initializeLifecycleTestDatabase(t, dbPath)

		cmd := lifecycleTestCommand(t, "opencode", "tool.execute.before", "1.18.10", dbPath)
		cmd.SetIn(bytes.NewReader(openCodeToolExecuteBeforeWire(t)))
		outcome := lifecycleOutcome(cmd, nil,
			panickingCommitBarrier{message: "the commit boundary failed after the receipt was written"},
			timeouts.ProductionProfile(), context.WithTimeout)

		require.Contains(t, outcome.Stderr, "durable state record-unknown",
			"this subtest must reach the record-unknown stage, or it proves nothing about advice "+
				"keyed on it")
		require.Contains(t, outcome.Stderr, "the hook panicked",
			"and it must reach it by PANICKING, which is the producer the remedy is wrong for")
		assert.NotContains(t, outcome.Stderr, writerRemedy,
			"a panicking invocation must not be told to go and find a long-running writer. Nothing "+
				"is holding the store; the work raised a panic, and the same line says so")
	})

	t.Run("the abandoned deadline", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, tasks.DefaultDBFilename.String())
		initializeLifecycleTestDatabase(t, dbPath)

		cmd := lifecycleTestCommand(t, "opencode", "tool.execute.before", "1.18.10", dbPath)
		cmd.SetIn(bytes.NewReader(openCodeToolExecuteBeforeWire(t)))

		// The same shape as the abandonment proof beside this one: the
		// invocation is HELD at the commit boundary and the deadline is tripped
		// there by the test, so the abandonment is driven by conditions and
		// never by a clock or a sleep.
		outcome := abandonAfterTheCommit(t, cmd)

		require.Contains(t, outcome.Stderr, "durable state record-unknown",
			"this subtest must reach the same stage as the one above; the two differ only in CAUSE")
		require.Contains(t, outcome.Stderr, "deadline",
			"and it must reach it by ABANDONMENT, which is the producer the remedy is right for")
		assert.Contains(t, outcome.Stderr, writerRemedy,
			"the remedy is not lost by being moved off the classifier: it lives with the cause it "+
				"is true of, and this is that cause")
	})
}

// TestAHostThatAddsAFieldIsRefusedWithATrueSentence drives the refusal a host
// meets when it ADDS a member, which nothing drove before.
//
// WHY THIS ROUTE MATTERS MOST. A host adding a field is the most ordinary thing
// that happens to a payload over time, so this is the refusal most likely to be
// met in practice. It was told "the payload does not carry the identity fields
// this event's registration declares, or carries them under different names" —
// FALSE IN BOTH HALVES for a payload that carries every one of them under the
// exact declared name. A reader would spend the day checking field names that
// were already correct.
//
// THE CONTROL IS PART OF THE TEST, because the claim is that ONE ADDED MEMBER
// is the whole difference: the same payload without it binds and says nothing.
//
// AN HONEST LIMIT, STATED. This refusal shares ONE disposition with the missing
// and unusable identity cases, so the sentence names all three causes rather
// than the one that fired. It is true of every payload that reaches it and it
// does not DISCRIMINATE. Telling them apart needs a new disposition in the
// model enum and a parser that returns it, which are outside this slice's files.
//
// MUTATION: narrow the reason back to the identity half. This test turns RED on
// the added-member clause.
func TestAHostThatAddsAFieldIsRefusedWithATrueSentence(t *testing.T) {
	binary := lifecycleBinary(t)

	authentic := claudeFixture(t, "pre_tool_use_2_1_222.json")
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(authentic, &members),
		"the committed fixture must decode, or the control below proves nothing")
	members["a_field_this_build_does_not_declare"] = json.RawMessage(`"x"`)
	extended, err := json.Marshal(members)
	require.NoError(t, err)

	t.Run("the control: the same payload without the added member", func(t *testing.T) {
		store := t.TempDir()
		database := filepath.Join(store, "pasture.db")
		initializeLifecycleTestDatabase(t, database)
		run := runLifecycleHookOn(t, binary, database, "claude-code", "PreToolUse", "2.1.222", authentic)
		require.Equal(t, 0, run.ExitCode)
		require.Empty(t, run.Stderr,
			"the authentic payload must bind in silence; if it does not, the added member is not "+
				"the difference this test is about")
	})

	t.Run("one added member", func(t *testing.T) {
		store := t.TempDir()
		database := filepath.Join(store, "pasture.db")
		initializeLifecycleTestDatabase(t, database)
		run := runLifecycleHookOn(t, binary, database, "claude-code", "PreToolUse", "2.1.222", extended)

		require.Equal(t, 0, run.ExitCode,
			"an unreadable payload is fail-open: the host carries on with its own answer")
		require.Contains(t, run.Stderr, "WAS NOT EVALUATED",
			"this subtest must reach the refusal; if it does not, nothing below is about it")

		assert.Contains(t, run.Stderr, "carries a member the registration does not allow",
			"the reason must name the cause that actually fired. This payload carries EVERY identity "+
				"field under the EXACT declared name, so a sentence about missing or renamed fields "+
				"is false of it in both halves")
		assert.Contains(t, run.Stderr, "must carry no member the registration does not declare",
			"and the instruction must tell the reader to look in that direction too, or they check "+
				"field names that are already correct")
		assert.NotContains(t, run.Stderr, "does not carry the identity fields this event's registration declares",
			"the retired sentence named one cause of a disposition that carries three")
	})
}

// TestEveryRefusalBeforeAWriteSaysNoRowExists drives, on the built binary, the
// routes that refuse BEFORE this command ever attempts a write, and requires
// each to say so.
//
// WHY IT EXISTS: THE PRODUCER OF THE EVIDENCE WAS UNPINNED. The stage table's
// row was pinned and the weakest-claim default was pinned, but the thing that
// PRODUCES the sentinel was not: deleting the wrapper whole left `go test ./...`
// green on every package while the binary's answer degraded from "no occurrence
// was recorded for it" to "MAY OR MAY NOT exist" — from a true claim to no
// claim at all. A default is a claim about everything nobody enumerated, so its
// PRODUCERS are a population, and this enumerates them.
//
// THE THREE ROUTES WERE FOUND BY THREE REVIEWERS TAKING THREE DIFFERENT
// APPROACHES, and none found the same hole:
//   - the store that cannot be OPENED, which the marker used to sit above, so
//     an operator was sent to the occurrence journal of a database whose own
//     cause says "unable to open database file";
//   - the UNSUPPORTED HARNESS, refused in a function the wrapper did not cover
//     at all;
//   - the WITHHELD EVENT, which was already right, and is pinned here so it
//     cannot become wrong while the other two are fixed.
//
// MUTATION: delete the ErrLifecycleBeforeDurableWrite wrapper from either
// producer, or move the durablePossible marker back above the open. The
// affected subtests turn RED on the durable-state assertions.
//
// WHAT IT VISITS: the THREE routes below, each written out because a route is a
// semantic condition — a store that cannot open, an unsupported harness, a
// withheld event — and there is no source that enumerates those.
// WHAT IT DOES NOT READ: any other pre-write refusal. That the STAGES are right
// per route is held here; that no route is MISSING is held by the route sweep,
// which reads every lifecycleFault call in the package.
func TestEveryRefusalBeforeAWriteSaysNoRowExists(t *testing.T) {
	binary := lifecycleBinary(t)
	payload := claudeFixture(t, "pre_tool_use_2_1_222.json")

	for _, row := range []struct {
		Name    string
		Harness string
		Event   string
		Store   func(t *testing.T) string
	}{
		{
			Name: "the store cannot be opened", Harness: "claude-code", Event: "PreToolUse",
			Store: func(t *testing.T) string {
				// A directory the user may not write to, so the store cannot be
				// created. Nothing is simulated.
				readOnly := filepath.Join(t.TempDir(), "read-only")
				require.NoError(t, os.Mkdir(readOnly, 0o500))
				t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
				return filepath.Join(readOnly, "pasture.db")
			},
		},
		{
			Name: "the harness is not one this build supports", Harness: "not-a-harness", Event: "PreToolUse",
			Store: func(t *testing.T) string { return filepath.Join(t.TempDir(), "pasture.db") },
		},
		{
			Name: "the event is withheld", Harness: "claude-code", Event: "Elicitation",
			Store: func(t *testing.T) string { return filepath.Join(t.TempDir(), "pasture.db") },
		},
	} {
		t.Run(row.Name, func(t *testing.T) {
			dbPath := row.Store(t)
			run := runLifecycleHookOn(t, binary, dbPath, row.Harness, row.Event, "2.1.222", payload)

			require.Equal(t, 0, run.ExitCode,
				"these refusals are fail-open: the host carries on.\nstderr: %s", run.Stderr)
			require.NotEmpty(t, run.Stderr,
				"this route must reach a diagnostic, or the assertions below prove nothing")

			assert.Contains(t, run.Stderr, "durable state not-recorded",
				"this route refused BEFORE any write was attempted, so no row can exist for it")
			assert.Contains(t, run.Stderr, "no occurrence was recorded for it",
				"and the sentence beside the machine-readable stage must say the same")
			assert.NotContains(t, run.Stderr, "MAY OR MAY NOT exist",
				"hedging here is not caution, it is a claim pasture need not make: it KNOWS no "+
					"write was attempted, and the hedge sends the reader looking for a row")
			assert.NotContains(t, run.Stderr, "IS committed in the lifecycle occurrence journal",
				"and it must not claim a row either")

			_, statErr := os.Stat(dbPath)
			assert.True(t, os.IsNotExist(statErr),
				"the claim under test is that NOTHING was written; a store on disk here would mean "+
					"not-recorded is the wrong answer and this test asserts the wrong thing")
		})
	}
}

// TestTheDurableRegionBeginsAtItsWrites reads the handler and requires the
// evidence marker to stand at every write attempt and nowhere else.
//
// WHAT THIS READER VISITS, STATED BECAUSE THE LAST TWO VERSIONS CLAIMED MORE
// THAN THEY READ:
//   - THE SUBJECT is hookLifecycle. deliveryCommit writes too, and its writes
//     are covered by the marker at its call site; its ONE step that precedes a
//     write, the warrant, wraps its own refusal at the point it is raised, so
//     nothing there depends on this reader.
//   - THE STATEMENTS are every nested list a statement can hold — else
//     branches, loops, switch and select cases, labelled statements and
//     deferred closures — not IfStmt.Body alone, which is all the first version
//     walked while its message spoke of every write in the function.
//   - THE WRITERS are the shared commit tail and EVERY CALL HANDED A NAME THAT
//     HOLDS THE RECEIPT SERVICE, as an argument or as a receiver, whether the
//     name stands alone or roots a selector or index chain (`svc`, `deps.Svc`,
//     `byName["a"]`, `holder[0]`). A name holds the service when it is bound
//     to the constructor's result, to another such name, to a method value
//     taken off one, or to a struct, map or slice literal that carries one
//     among its elements. Naming service.Receive and deliveryCommit was a list
//     of the two anybody remembered, and it missed
//     receipt.EnsureActiveMetamodel(ctx, service) — a real write one function
//     away in this same file; accepting a bare identifier as the receiver then
//     missed a service carried in a container, which is the ordinary Go way to
//     hand collaborators around.
//   - WHAT IT DOES NOT READ: a writer reached without the service in hand; any
//     write inside a function this reader does not open; and a service that
//     enters a container by a CALL rather than a literal or an assignment —
//     `holder = append(holder, service)`, `deps.Set(service)` — because this
//     reader follows literals and assignments and does not resolve types.
//
// THE MARKER IS A CLAIM ABOUT A REGION, so its placement is the whole of its
// meaning. It was set above open(in.DBPath) and described as "the line the
// durable region begins", and OPENING A STORE WRITES NO OCCURRENCE — so every
// refusal between the open and the first write inherited a claim that was too
// strong. Reading the source is what makes the region a population rather than
// a remembered line.
//
// MUTATION: move the marker above the open, delete it from either write, or add
// a write with no marker before it — through the service by name, through a
// rebound name, through a method value, or through a struct field, map value or
// slice element that carries it. This test turns RED naming the line.
func TestTheDurableRegionBeginsAtItsWrites(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "resolve the repository root from cmd/pasture")
	path := filepath.Join(root, "internal", "handlers", "hook_lifecycle.go")

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	require.NoError(t, err, "the handler source must be readable")

	var handler *ast.FuncDecl
	for _, node := range file.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Name.Name == "hookLifecycle" {
			handler = function
			break
		}
	}
	require.NotNil(t, handler, "hookLifecycle must exist: it is the region this marker describes")

	// A statement ATTEMPTS A WRITE when it calls the receipt service or the
	// shared commit tail. Nothing else in this function can leave a row.
	// EVERY NAME THAT HOLDS THE RECEIPT SERVICE, derived by following the
	// assignments rather than by trusting one spelling. It starts at whatever
	// the constructor returns and grows by transitive rebinding, so a service
	// passed on under another name is still the service.
	//
	// THE ROOT OF AN EXPRESSION is the name a selector or index chain starts
	// from: `deps.Svc`, `byName["a"]`, `holder[0]` and `(*p).Svc` all root at
	// their first identifier. The reader once accepted a bare identifier only,
	// so a write through a struct field, a map value or a slice element was a
	// genuine unmarked write that passed. A chain rooted at a name that holds
	// the service is the service in hand.
	rootIdentifier := func(expression ast.Expr) *ast.Ident {
		for {
			switch typed := expression.(type) {
			case *ast.Ident:
				return typed
			case *ast.SelectorExpr:
				expression = typed.X
			case *ast.IndexExpr:
				expression = typed.X
			case *ast.ParenExpr:
				expression = typed.X
			case *ast.StarExpr:
				expression = typed.X
			case *ast.UnaryExpr:
				expression = typed.X
			default:
				return nil
			}
		}
	}
	serviceNames := map[string]bool{}
	holdsService := func(expression ast.Expr) bool {
		root := rootIdentifier(expression)
		return root != nil && serviceNames[root.Name]
	}
	for changed := true; changed; {
		changed = false
		// BOTH BINDING FORMS. The reader followed `svc := service` and not
		// `var svc = service`, which is the same binding written the other way.
		bind := func(targets []ast.Expr, value ast.Expr) {
			holds := strings.Contains(sourceOf(value), "NewLifecycleReceiptService") || holdsService(value)
			// A CONTAINER BUILT FROM THE SERVICE holds it: a struct, map or
			// slice literal with the service, or a name that holds it, among
			// its elements.
			if literal, isLiteral := value.(*ast.CompositeLit); isLiteral {
				for _, element := range literal.Elts {
					if pair, isPair := element.(*ast.KeyValueExpr); isPair {
						element = pair.Value
					}
					if holdsService(element) {
						holds = true
					}
				}
			}
			if !holds || len(targets) == 0 {
				return
			}
			// The target may itself be a field or an element (`deps.Svc =
			// service`); the name that then holds the service is the root.
			name := rootIdentifier(targets[0])
			if name != nil && name.Name != "_" && !serviceNames[name.Name] {
				serviceNames[name.Name] = true
				changed = true
			}
		}
		ast.Inspect(handler, func(node ast.Node) bool {
			if spec, isSpec := node.(*ast.ValueSpec); isSpec && len(spec.Values) == 1 {
				targets := make([]ast.Expr, 0, len(spec.Names))
				for _, name := range spec.Names {
					targets = append(targets, name)
				}
				bind(targets, spec.Values[0])
				return true
			}
			assign, isAssign := node.(*ast.AssignStmt)
			if !isAssign || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
				return true
			}
			// THE FIRST RESULT ONLY. The constructor returns (Service, error),
			// and taking every name on the left made "err" a service — after
			// which every fmt.Errorf carrying it counted as a write. The
			// service is the value; the error beside it is not.
			bind(assign.Lhs, assign.Rhs[0])
			return true
		})
	}
	require.NotEmpty(t, serviceNames,
		"this handler must still construct a receipt service; finding no name that holds one means "+
			"the writer population below is empty and every assertion passes vacuously")

	attemptsWrite := func(statement ast.Stmt) bool {
		found := false
		ast.Inspect(statement, func(node ast.Node) bool {
			// PRUNE AT A NESTED BLOCK. The question is whether THIS statement
			// attempts a write, not whether one occurs anywhere beneath it: a
			// reader that descends reports the enclosing `if` as a write, and
			// then demands a marker above a branch that only CONTAINS one. The
			// walk below visits nested bodies in their own right.
			if _, isBlock := node.(*ast.BlockStmt); isBlock {
				return false
			}
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			// THE WRITER POPULATION IS EVERY CALL THAT IS HANDED THE
			// RECEIPT SERVICE, plus the shared commit tail.
			//
			// IT NAMED service.Receive AND deliveryCommit, and that is a list
			// of the two writers anybody remembered. receipt has a
			// package-level EnsureActiveMetamodel(ctx, service) that JOURNALS
			// THE METAMODEL — a real write, called one function away in this
			// same file — and it was invisible: not a method on the service, so
			// the selector arm missed it, and not a bare identifier named
			// deliveryCommit, so the ident arm missed it too.
			//
			// Recognising "the service is handed to it" needs no list to be
			// kept: a call that can write is a call that was given the thing
			// that writes.
			if name, isIdentifier := call.Fun.(*ast.Ident); isIdentifier {
				// The shared commit tail, or a NAME THAT HOLDS THE SERVICE'S
				// WRITE. `write := service.Receive` then `write(...)` reaches
				// the same code with no selector left for the receiver check to
				// see, so the callee is asked about too.
				if name.Name == "deliveryCommit" || serviceNames[name.Name] {
					found = true
				}
			}
			// THE SERVICE BY IDENTITY, NOT BY ITS FOUR-LETTER SPELLING, AND
			// NOT ONLY AS A BARE NAME. The reader matched the name "service",
			// so `svc := service` followed by an unmarked write through svc
			// was green; then it matched an identifier, so `deps.Svc.Receive`
			// was green. A rebound service IS the service in hand, and so is
			// one reached through a chain rooted at a name that holds it —
			// as an argument, as a method value passed on, or as a receiver.
			for _, argument := range call.Args {
				if holdsService(argument) {
					found = true
				}
			}
			if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector && holdsService(selector.X) {
				found = true
			}
			return true
		})
		return found
	}
	marksRegion := func(statement ast.Stmt) bool {
		assign, isAssign := statement.(*ast.AssignStmt)
		if !isAssign || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return false
		}
		name, isIdentifier := assign.Lhs[0].(*ast.Ident)
		return isIdentifier && name.Name == "durablePossible" && sourceOf(assign.Rhs[0]) == "true"
	}

	writes, markers := 0, 0
	var unmarked []string
	var walk func(body []ast.Stmt)
	walk = func(body []ast.Stmt) {
		for index, statement := range body {
			if marksRegion(statement) {
				markers++
			}
			if attemptsWrite(statement) {
				writes++
				if index == 0 || !marksRegion(body[index-1]) {
					unmarked = append(unmarked, fmt.Sprintf("hook_lifecycle.go:%d",
						fileSet.Position(statement.Pos()).Line))
				}
			}
			// EVERY NESTED STATEMENT LIST, not IfStmt.Body alone.
			//
			// THE WALK VISITED ONE POSITION. An unmarked write in an ELSE
			// branch, a loop, a switch case, a select case or a deferred
			// closure was never reached, so the guard's message — which speaks
			// of every write in this function — was wider than the statements
			// it read. The reader below visits every list a statement can hold.
			switch nested := statement.(type) {
			case *ast.IfStmt:
				walk(nested.Body.List)
				switch otherwise := nested.Else.(type) {
				case *ast.BlockStmt:
					walk(otherwise.List)
				case *ast.IfStmt:
					walk([]ast.Stmt{otherwise})
				}
			case *ast.BlockStmt:
				walk(nested.List)
			case *ast.ForStmt:
				walk(nested.Body.List)
			case *ast.RangeStmt:
				walk(nested.Body.List)
			case *ast.SwitchStmt:
				walk(nested.Body.List)
			case *ast.TypeSwitchStmt:
				walk(nested.Body.List)
			case *ast.SelectStmt:
				walk(nested.Body.List)
			case *ast.CaseClause:
				walk(nested.Body)
			case *ast.CommClause:
				walk(nested.Body)
			case *ast.LabeledStmt:
				walk([]ast.Stmt{nested.Stmt})
			}
			// ANY FUNCTION LITERAL, wherever it stands. The walk opened defer
			// and go closures BY NAME while its sentence claimed every list a
			// statement can hold, so an immediately-invoked literal holding an
			// unmarked write was green. A literal is a statement list whoever
			// wrote it, and this finds them at this level without descending
			// into the nested blocks the switch above already visits.
			ast.Inspect(statement, func(node ast.Node) bool {
				if literal, isLiteral := node.(*ast.FuncLit); isLiteral {
					walk(literal.Body.List)
					return false
				}
				return true
			})
		}
	}
	walk(handler.Body.List)

	require.NotZero(t, writes,
		"this handler must still attempt a write; finding none means the reader no longer "+
			"recognises them and every assertion here would pass vacuously")
	assert.Empty(t, unmarked,
		"a write is attempted at %v with no durablePossible marker immediately above it. The marker "+
			"is what lets a refusal claim NO ROW EXISTS, so a write outside it makes that claim "+
			"about a statement that may have written one", unmarked)
	assert.Equal(t, writes, markers,
		"the marker must stand at every write and NOWHERE ELSE. A marker above a statement that "+
			"writes nothing — the open, the service construction, the gate — hands every refusal "+
			"after it a claim too strong to support, which is exactly what sent an operator to the "+
			"occurrence journal of a database that could not be opened")
}

// TestAnEmptyStandardInputNamesTheRealCondition drives a zero-length payload on
// every harness this build supports.
//
// WHAT THE OPERATOR USED TO READ: "storage error: The lifecycle payload blob
// could not be stored. Cause: constraint failed: NOT NULL constraint failed:
// lifecycle_payload_blobs.body (1299)". A storage fault naming a column and an
// SQLite error number, for a condition that is neither storage nor a fault of
// the store — the host simply sent nothing. The read succeeded, so the
// read-failure arm did not fire; the length was under the bound, so the
// over-limit arm did not either; and the empty slice travelled to the blob
// writer, where a nil body binds as NULL against a NOT NULL column.
//
// IT IS THE NIL-VERSUS-EMPTY CLASS AGAIN. An absent value and a present empty
// one are different facts, and the layer that could not tell them apart was the
// one that reported.
//
// THE HARNESSES ARE DERIVED from the registry the command dispatches on, so a
// harness added to the product is driven here without anyone remembering to add
// it — and a harness the registry cannot coordinate fails BY NAME rather than
// leaving the set one short. The derivation once dropped such a harness through
// a silent continue, and this test ran two subtests instead of three and
// passed: a derived population that shrinks with no reader noticing.
//
// MUTATION: delete the zero-length arm from hookLifecycle. Every subtest turns
// RED on the condition assertion, and the operator gets the column name back.
// Or make one harness's activation proofs fail: the derivation turns RED naming
// that harness before any subtest runs.
func TestAnEmptyStandardInputNamesTheRealCondition(t *testing.T) {
	binary := lifecycleBinary(t)

	harnesses, derivationErr := handlers.LifecycleHarnessCoordinates()
	require.NoError(t, derivationErr,
		"the harness set is derived from the command's own registry, and the registry could not give "+
			"every harness a coordinate. A set one harness short runs one subtest fewer and stays "+
			"green, which is a derived population that shrank with nobody reading it")
	require.NotEmpty(t, harnesses,
		"the harness set is derived from the command's own registry; an empty one would make every "+
			"subtest below vacuous")

	for _, row := range harnesses {
		t.Run(row.Harness, func(t *testing.T) {
			store := t.TempDir()
			database := filepath.Join(store, "pasture.db")
			initializeLifecycleTestDatabase(t, database)

			run := runLifecycleHookOn(t, binary, database,
				row.Harness, row.Event, row.HostVersion, nil)

			require.Equal(t, 0, run.ExitCode,
				"an empty payload is a fail-open refusal: the host carries on.\nstderr: %s", run.Stderr)

			// C-ASSERT-CONTENT-NOT-PRESENCE: what it SAYS, not that it exists.
			assert.Contains(t, run.Stderr, "The host sent no payload: standard input carried zero bytes.",
				"the diagnostic must name the condition that actually occurred")
			assert.Contains(t, run.Stderr, "writing the event payload to the hook's standard input",
				"and it must tell the reader where to look: a hook wired without its input "+
					"redirected is the ordinary cause, and no column name points at that")
			assert.NotContains(t, run.Stderr, "NOT NULL constraint failed",
				"an empty stream is not a storage fault, and a column name is not an instruction")
			assert.NotContains(t, run.Stderr, "lifecycle_payload_blobs",
				"nor may a schema table reach the person running a hook")
			assert.Contains(t, run.Stderr, "durable state not-recorded",
				"nothing was parsed and no store was opened, so no row can exist")
		})
	}
}

// TestTheWarrantRefusalCarriesItsOwnEvidence pins the wrap the previous round
// added and nothing held.
//
// REVERTING IT LEFT THREE PACKAGES ENTIRELY GREEN, and a guard in this file
// CITED that unheld property as its reason for not reading the shared commit
// tail. A reason resting on an unpinned property is the shape this slice has
// met before: the conclusion was right and the warrant for it was missing.
//
// The refusal is raised inside the commit tail, past the caller's marker and
// before any write, so without its own wrap the same gate refusal answers
// "not-recorded" on the invalid-capture arm and "MAY OR MAY NOT exist" here.
// This reads the source because no host payload drives a warrant refusal: the
// contract is derived from the delivery the parser produced, so a payload that
// reaches this point has already produced a legal one. THAT IS THE LIMIT, and
// it is why this is structural rather than behavioural.
//
// MUTATION: drop the ErrLifecycleBeforeDurableWrite wrap from the warrant
// refusal. This test turns RED.
func TestTheWarrantRefusalCarriesItsOwnEvidence(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "resolve the repository root from cmd/pasture")
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet,
		filepath.Join(root, "internal", "handlers", "hook_lifecycle.go"), nil, 0)
	require.NoError(t, err, "the handler source must be readable")

	var tail *ast.FuncDecl
	for _, node := range file.Decls {
		function, isFunction := node.(*ast.FuncDecl)
		if isFunction && function.Name.Name == "deliveryCommit" {
			tail = function
			break
		}
	}
	require.NotNil(t, tail, "deliveryCommit must exist: it is where the warrant refusal is raised")

	var warrantArm *ast.IfStmt
	for index, statement := range tail.Body.List {
		if assign, isAssign := statement.(*ast.AssignStmt); isAssign &&
			strings.Contains(sourceOf(assign.Rhs[0]), "deliveryWarrant(") {
			require.Greater(t, len(tail.Body.List), index+1,
				"the warrant call must still be followed by its refusal arm")
			arm, isIf := tail.Body.List[index+1].(*ast.IfStmt)
			require.True(t, isIf, "the statement after the warrant call must be its refusal arm")
			warrantArm = arm
			break
		}
	}
	require.NotNil(t, warrantArm,
		"deliveryCommit must still derive a warrant and refuse on it; finding neither means this "+
			"guard reads nothing and passes vacuously")

	returned := ""
	ast.Inspect(warrantArm, func(node ast.Node) bool {
		if statement, isReturn := node.(*ast.ReturnStmt); isReturn && len(statement.Results) == 2 {
			returned = sourceOf(statement.Results[1])
		}
		return true
	})
	assert.Contains(t, returned, "ErrLifecycleBeforeDurableWrite",
		"the warrant refusal is raised BEFORE any write and PAST the caller's marker, so it must "+
			"carry its own evidence. Without it the same gate refusal tells one operator no row "+
			"exists and another that theirs may or may not — decided by which arm reached it, "+
			"which is not a fact about their invocation. Found: %q", returned)
}

// guardSweepOwned are the test files in this package that this slice changed,
// and so may change again. The sweep below reads these and no others, because a
// guard it flags is a guard somebody must be able to change, and reaching into
// another slice's file is how ownership statements stop meaning anything.
// guardSweepForeign are the package's other test files, each with the reason.
// Together they must be EXACTLY the directory.
//
// BOTH LISTS ARE WRITTEN DOWN, AND THE PIN IS WHAT MAKES THAT SAFE. A test
// cannot read the set of files this work changed — that lives in version
// control, which an archive copy does not carry — so the set is prose here.
// TWO LISTS AND A COVERAGE PIN, because each single list failed differently. A
// positive list alone was a DEAD PIN: it was called derived, was hand-written,
// and dropping two files from it left the suite green. Inverting it to an
// exclusion list made the sweep reach into four files this slice does not own,
// which is the opposite error. Requiring the two to PARTITION the directory
// gives what neither had: a file cannot leave the sweep without being written
// down as foreign, and a new file cannot arrive without being classified at all.
var guardSweepOwned = []string{
	"hook_environment_test.go",
	"hook_lifecycle_codex_test.go",
	"hook_lifecycle_docs_test.go",
	"hook_lifecycle_failuremode_test.go",
	"hook_lifecycle_orphans_test.go",
	"hook_lifecycle_production_test.go",
}

// guardSweepForeign are the package's other test files, which this slice did
// not change. The guards they carry are handed to their owners, not reached for.
var guardSweepForeign = map[string]string{
	"bundle_export_test.go":                      "not changed by this slice",
	"epoch_test.go":                              "not changed by this slice",
	"hook_lifecycle_context_production_test.go":  "not changed by this slice",
	"hook_lifecycle_gate_test.go":                "not changed by this slice",
	"hook_lifecycle_lineage_production_test.go":  "not changed by this slice",
	"hook_lifecycle_raw_test.go":                 "not changed by this slice",
	"hook_lifecycle_readback_production_test.go": "not changed by this slice",
	"hook_test.go":                               "not changed by this slice",
	"install_frontend_test.go":                   "not changed by this slice",
	"install_test.go":                            "not changed by this slice",
	"install_verbs_test.go":                      "not changed by this slice",
	"integration_test.go":                        "not changed by this slice",
	"main_test.go":                               "not changed by this slice",
	"queue_test.go":                              "not changed by this slice",
	"version_test.go":                            "not changed by this slice",
}

// guardSweepSubjects returns the owned files, and REQUIRES the two lists to
// partition the directory so neither can drift from it.
func guardSweepSubjects(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err, "the package directory must be readable to classify its test files")

	classified := map[string]bool{}
	for _, name := range guardSweepOwned {
		classified[name] = true
	}
	for name := range guardSweepForeign {
		classified[name] = true
	}
	unclassified := []string{}
	present := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		present[name] = true
		if !classified[name] {
			unclassified = append(unclassified, name)
		}
	}
	sort.Strings(unclassified)
	require.Empty(t, unclassified,
		"these test files are neither owned nor declared foreign: %v. Every file in this package "+
			"must be one or the other, so a new one cannot arrive unswept and an owned one cannot "+
			"leave the sweep without somebody writing down why", unclassified)

	missing := []string{}
	for name := range classified {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing,
		"these files are classified and do not exist: %v. A stale entry hides a real file behind a "+
			"name nobody checks", missing)

	subjects := append([]string{}, guardSweepOwned...)
	sort.Strings(subjects)
	require.NotEmpty(t, subjects, "the sweep must have subjects, or every assertion is vacuous")
	return subjects
}

// guardReachClaims are the phrases a guard's own doc uses to claim it reads a
// WHOLE CLASS. They are the vocabulary this slice has repeatedly used while
// reading a list.
var guardReachClaims = []string{
	"every ", "each ", "any ", "all ", "no other", "whatever ", "wherever ",
}

// guardReachDisclaimers are the LABELS that open a reach statement, and a
// discharge is one of them HEADING ITS OWN LINE, IN CAPITALS, in the guard's
// doc. That form is what makes it about the guard rather than about anything
// the doc happens to mention.
//
// THE BARE WORDS ARE GONE, AND THE ARCHETYPE IS WHY. "derived" or "population"
// occurring ANYWHERE in a doc, about ANY subject, used to clear a guard — and
// the archetype's own doc said "STAGES are derived from IsValid" while its ARMS
// were a hand-written list. A discharge the original defect would have PASSED
// is not a discharge; it is a word that happens to appear near one.
//
// THE SUBJECT AXIS, which two rewrites missed. The first discharge was single
// words; the second was the phrases a reviewer had used; both were substring
// tests over the whole doc, so a sentence about THE PRODUCT — "the product
// READS ONLY the bytes on standard input", "WHAT IT DOES NOT do is open a
// database" — silenced the sweep on the first attempt. A third rewrite required
// the label to open a line and still admitted "it reads only", which ordinary
// prose puts at the head of a wrapped line. So the labels are now the ones an
// honest reach statement in this tree actually uses, each names a reading, and
// each must stand in capitals at the head of its line: a sentence about the
// product does not arrive in that form by accident. A doc that mimics the form
// deliberately is not caught, and that is the stated limit of a word test.
var guardReachDisclaimers = []string{
	"WHAT IT VISITS", "WHAT IT DOES NOT READ", "WHAT IT DOES NOT COVER",
	"WHAT IT DOES NOT VISIT", "WHAT IT DOES NOT:", "WHAT IT DOES NOT —",
	"THE LIMIT, STATED", "AN HONEST LIMIT",
}

// dischargesAClassClaim reports whether a guard's doc carries a reach statement
// in the form guardReachDisclaimers describes. Comment markers and list
// punctuation are stripped from the head of each line first, because a doc
// line reads "// WHAT IT VISITS: ..." or "//   - WHAT IT DOES NOT READ: ...".
func dischargesAClassClaim(doc string) bool {
	for _, line := range strings.Split(doc, "\n") {
		opening := strings.TrimLeft(line, " \t-*•")
		for _, label := range guardReachDisclaimers {
			if strings.HasPrefix(opening, label) {
				return true
			}
		}
	}
	return false
}

// rangesOverAConstantPopulation reports whether a function RANGES OVER a
// literal collection of constants — the shape of a written-down population.
//
// THIS IS THE DEFECT'S SIGNATURE, not its vocabulary. A guard that derives its
// population reads it from a predicate, a registry or a document and has no
// such literal; a guard that widened a list has exactly one. Detecting the
// SHAPE is what lets this sweep avoid being the thirteenth instance of the
// class it looks for: it does not ask what a doc MEANS, it asks what the code
// RANGES OVER.
// compositeBehind resolves a ranged-over expression to the composite literal
// that supplies it: the literal itself, or the one a name in this function is
// bound to.
//
// WHAT IT VISITS: a literal at the range site, and a name assigned or declared
// a literal inside the same function.
// WHAT IT DOES NOT READ: a package-level variable, a value returned by a call,
// a table built by appends, and a literal reached through a selector or an
// index on a name (`table.Rows`). Those remain invisible, and this sentence is
// the only thing standing between that and a claim of completeness.
func compositeBehind(function *ast.FuncDecl, ranged ast.Expr) *ast.CompositeLit {
	if literal, isLiteral := ranged.(*ast.CompositeLit); isLiteral {
		return literal
	}
	name, isIdentifier := ranged.(*ast.Ident)
	if !isIdentifier {
		return nil
	}
	var found *ast.CompositeLit
	ast.Inspect(function, func(node ast.Node) bool {
		switch bound := node.(type) {
		case *ast.AssignStmt:
			for index, target := range bound.Lhs {
				if candidate, isName := target.(*ast.Ident); isName && candidate.Name == name.Name &&
					index < len(bound.Rhs) {
					if literal, isLiteral := bound.Rhs[index].(*ast.CompositeLit); isLiteral {
						found = literal
					}
				}
			}
		case *ast.ValueSpec:
			for index, candidate := range bound.Names {
				if candidate.Name == name.Name && index < len(bound.Values) {
					if literal, isLiteral := bound.Values[index].(*ast.CompositeLit); isLiteral {
						found = literal
					}
				}
			}
		}
		return true
	})
	return found
}

func rangesOverAConstantPopulation(function *ast.FuncDecl) bool {
	found := false
	ast.Inspect(function, func(node ast.Node) bool {
		loop, isRange := node.(*ast.RangeStmt)
		if !isRange {
			return true
		}
		// THE BINDING AXIS, which nobody had probed and which I did not widen
		// when I widened the ELEMENT axis a reviewer did probe. A table lifted
		// into a local or a package-level variable is the same written-down
		// population one line further away, and it hid FIVE of this slice's own
		// class-claiming guards — so "zero flagged" was an artefact of the
		// reader, not an outcome of the code.
		literal := compositeBehind(function, loop.X)
		if literal == nil || len(literal.Elts) < 2 {
			return true
		}
		// ANY WRITTEN-DOWN COLLECTION, NOT A LIST OF STRING LITERALS.
		//
		// THIS DETECTOR WAS THE THIRTEENTH INSTANCE OF THE CLASS IT ENDS. It
		// required EVERY element to be a string literal, so an inline
		// []struct{...}, a list of typed enum constants and a map literal were
		// all invisible — and a []struct of hand-listed rows is EXACTLY the
		// shape of the archetype it was written beside, the four arms fixed in
		// the same commit. A signature that cannot see the defect it was
		// modelled on is not a signature.
		//
		// A literal ranged over IS a written-down population whatever its
		// elements are; what matters is that the collection is spelled at the
		// range site rather than derived. So the element kind is no longer
		// asked about at all.
		found = true
		return true
	})
	return found
}

// TestEveryGuardThatClaimsAClassSaysWhatItReads is the closing sweep the class
// itself asked for: a structural check for the defect this slice has met
// thirteen times — a rule described one level more general than it is written.
//
// WHAT IT VISITS: every test function in THE FILES THIS SLICE OWNS that BOTH
// claims a class in its doc AND ranges over a written-down collection — a
// composite literal of any element kind, at the range site or held in a name
// bound inside the same function. That pair is the defect's signature: a
// sentence about a class, standing over a written-down list. It requires such a
// guard's doc to say what it reads or where it stops.
//
// WHAT IT DOES NOT — AND THIS IS THE HONEST HALF, WITHOUT WHICH THIS SWEEP
// WOULD BE THE THIRTEENTH INSTANCE:
//
//   - It CANNOT decide whether a stated population is TRUE of the code. That is
//     the reviewers' work, and comparing a sentence to a reader's reach needs
//     the meaning of both.
//
//   - THE SUBJECT IS THIS SLICE'S OWN TEST FILES IN THIS PACKAGE, and that
//     bound is a fact about who may change what, not about where the defect
//     lives. THE SET WAS TWO FILES AND IS SIX: it was written from the
//     supervisor's running list of "files changed recently" rather than derived
//     from the slice's ownership statements, so two guards genuinely inside the
//     set — in hook_lifecycle_codex_test.go and hook_lifecycle_orphans_test.go,
//     the latter named verbatim in the slice's own ownership fold — were
//     reported as somebody else's. The set here is WRITTEN DOWN from those
//     statements, in guardSweepOwned, and it is pinned rather than derived: the
//     files outside it are named in guardSweepForeign with a reason, and the
//     two lists must PARTITION the directory, so a file cannot leave the sweep
//     unnoticed and a new one cannot arrive unclassified.
//
//     THE HANDOVER, MEASURED RATHER THAN REMEMBERED. Running the same predicate
//     over the foreign files finds the guards below carrying the shape, where
//     the previous handover had named two of them. They are, for their owners
//     to judge:
//     TestCLI_HookRecord_FlagWiring_RoundTrips,
//     TestCLI_QueueConcurrency_RejectsBadArguments,
//     TestCLI_QueueConcurrency_ReportsADatabaseWithNoQueues,
//     TestCLI_S10_TaskCreate_LeavesUnifiedAuditTables,
//     TestInvalidInvocationCreatesNoDatabaseFile,
//     TestLineageMaterializeThenSecondRunIsNoOp and
//     TestRawContinuationParityWithNativePerEvent.
//     TestTheOutOfSetHandoverNamesEveryGuardItFound holds that list EQUAL to
//     what the predicate finds — nothing found unnamed, nothing named that is
//     not found — so it cannot go stale the way the one it replaces did.
//
//   - Guards in internal/lifecycle/hostexit and internal/handlers are not
//     visited; their packages must ask this of themselves.
//
//   - It finds a list written INLINE at the range site, or held in a name bound
//     a literal INSIDE THE SAME FUNCTION. A population held in a package-level
//     variable, returned by a call, built by appends, or reached through a
//     selector or an index on a local (`table.Rows`, `tables[0]`) is invisible
//     to it, and so is one spelled as a switch. It no longer asks what the elements ARE —
//     strings, structs, typed constants and map entries all count — because
//     requiring string literals made it blind to the very shape of the
//     archetype it was modelled on, and it no longer asks that the literal sit
//     at the range site, because that hid five of this package's own guards.
//
//   - Its claim vocabulary is itself a list, which is the very move that failed
//     three times. It is admissible only because it is not the whole rule: it
//     narrows WHICH guards are asked, and the structural half above is what
//     makes the asking worth anything.
//
// So this finds the SHAPE cheaply and never claims to find the class.
func TestEveryGuardThatClaimsAClassSaysWhatItReads(t *testing.T) {
	t.Parallel()

	silent := []string{}
	claiming := 0
	for _, name := range guardSweepSubjects(t) {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, parser.ParseComments)
		require.NoError(t, parseErr, "every subject file must parse: %s", name)
		entry := struct{ Name func() string }{Name: func() string { return name }}
		for _, node := range parsed.Decls {
			function, isFunction := node.(*ast.FuncDecl)
			if !isFunction || !strings.HasPrefix(function.Name.Name, "Test") || function.Doc == nil {
				continue
			}
			doc := strings.ToLower(function.Doc.Text())
			claims := false
			for _, phrase := range guardReachClaims {
				if strings.Contains(doc, phrase) {
					claims = true
					break
				}
			}
			if !claims {
				continue
			}
			claiming++
			if !rangesOverAConstantPopulation(function) {
				continue
			}
			// The discharge reads the doc AS WRITTEN, capitals included; the
			// lowered copy above serves the claim vocabulary only.
			if !dischargesAClassClaim(function.Doc.Text()) {
				silent = append(silent, entry.Name()+": "+function.Name.Name)
			}
		}
	}

	// TWO VACUITY CHECKS, BECAUSE ZERO FLAGGED IS THE RIGHT ANSWER HERE AND MUST
	// NOT BE INDISTINGUISHABLE FROM A DETECTOR THAT LOOKS AT NOTHING.
	//
	// The subject files currently contain NO guard with the defect's signature,
	// which is the outcome this round worked for. So the sweep cannot prove
	// itself by finding one. It proves itself in two other ways: the claim
	// vocabulary must still match how guards here are written, and the
	// structural half must still recognise the shape on an input built to have
	// it.
	require.NotZero(t, claiming,
		"no guard in the subject files claims a class at all. Either they stopped making such "+
			"claims, or the vocabulary above no longer matches how they are written — and in the "+
			"second case this sweep is asking nothing of anybody")

	// ONE CONTROL PER AXIS THE DETECTOR HAS. Once every owned guard states its
	// reach, "zero flagged" is green whether the detector is wide or narrow, so
	// narrowing it CANNOT be caught by the subject files. Each axis therefore
	// gets a synthetic input the detector must still recognise. Both axes here
	// were widened only after a reviewer probed them; the controls are what
	// stop the next narrowing from being silent.
	for _, control := range []struct {
		Axis   string
		Source string
	}{
		{
			Axis: "a literal AT THE RANGE SITE",
			Source: `package main
func TestControl() {
	for _, item := range []string{"one", "two"} {
		_ = item
	}
}`,
		},
		{
			Axis: "a literal HELD IN A LOCAL, one line away from the range",
			Source: `package main
func TestControl() {
	rows := []struct{ Name string }{{Name: "one"}, {Name: "two"}}
	for _, item := range rows {
		_ = item
	}
}`,
		},
		{
			Axis: "a literal held in a VAR DECLARATION",
			Source: `package main
func TestControl() {
	var rows = map[string]string{"a": "one", "b": "two"}
	for key := range rows {
		_ = key
	}
}`,
		},
	} {
		parsed, controlErr := parser.ParseFile(token.NewFileSet(), "control.go", control.Source, parser.ParseComments)
		require.NoError(t, controlErr, "the %s control must parse", control.Axis)
		recognised := false
		for _, node := range parsed.Decls {
			if function, isFunction := node.(*ast.FuncDecl); isFunction {
				recognised = rangesOverAConstantPopulation(function)
			}
		}
		require.True(t, recognised,
			"the detector no longer recognises %s. With every owned guard discharged, narrowing it "+
				"produces ZERO FLAGGED and a green suite, so this control is the only thing "+
				"standing between a working detector and one that reports nothing because it sees "+
				"nothing", control.Axis)
	}
	// THE DISCHARGE AXIS HAS ITS OWN CONTROLS, for the same reason: every
	// owned guard is discharged, so a discharge that widened to clear product
	// prose would flag nothing and stay green. Each row is a doc that must, or
	// must not, count as a reach statement.
	for _, control := range []struct {
		Doc        string
		Discharges bool
		Why        string
	}{
		{Doc: "TestControl checks every arm.\nWHAT IT VISITS: the arms below.\n", Discharges: true,
			Why: "a reach label heading its own line, in capitals, is the honest form every discharge in this tree uses"},
		{Doc: "TestControl checks every arm.\n  - WHAT IT DOES NOT READ: a fourth arm.\n", Discharges: true,
			Why: "a reach label may open a list item"},
		{Doc: "TestControl checks every arm. The product\nREADS ONLY the bytes on standard input.\n", Discharges: false,
			Why: "prose about the product is not a statement of the guard's reach"},
		{Doc: "TestControl checks every arm.\nWHAT IT DOES NOT do is open a database.\n", Discharges: false,
			Why: "a label must name a reading; 'what it does not do' names the product's behaviour"},
		{Doc: "TestControl checks every arm, and whether\nit reads only the bytes on standard input.\n", Discharges: false,
			Why: "ordinary prose puts 'it reads only' at the head of a wrapped line, so it cannot be a label"},
		{Doc: "TestControl checks every arm.\nwhat it visits: the arms below.\n", Discharges: false,
			Why: "the label must stand in capitals, which is the form an honest reach statement takes and an accident does not"},
	} {
		assert.Equal(t, control.Discharges, dischargesAClassClaim(control.Doc),
			"the discharge test gives the wrong answer for %q: %s", control.Doc, control.Why)
	}

	sort.Strings(silent)
	assert.Empty(t, silent,
		"these guards claim a CLASS in their doc and say nothing about the population they "+
			"actually read. Three times running, the fix for that defect has contained it, because "+
			"each fix was written the way the defect was. Either DERIVE the population from the "+
			"same source the product derives it from, or STATE what the guard visits and what it "+
			"does not — and never widen a list and call it a population")
}

// ─────────────────────────────────────────────────────────────────────────────
// INTERNAL-REFERENCE RECOGNITION FOR THIS PACKAGE
// ─────────────────────────────────────────────────────────────────────────────
//
// THE POPULATION IS SHARED; THE READER IS NOT, AND THAT IS A STATED COST.
// internal/lifecycle/hostexit's test carries the same derivation, because a
// test in one package cannot call a helper in another package's _test file.
// Both read THE SAME BLOCK OF AGENTS.md and scan THE SAME TREE for the task
// identifier corpus, so the populations cannot drift even though the code is
// in two places. What could drift is the hand-written half — the table of
// patterns and the durable samples — and TestTheTwoInternalReferenceTablesAgree
// reads the sibling's source and holds the two tables equal, because the two
// example lists this replaced had already diverged under a comment saying they
// were the same. A single home would be a new shared package, which is not this
// slice's to create.

// packageInternalReferenceForms maps each exemplar the rule spells to the
// pattern recognising its family. It is the same table its sibling carries, and
// the same reasoning applies to every entry.
var packageInternalReferenceForms = map[string]string{
	// Built from the DERIVED prefixes at run time; see packageTaskIdentifierCorpus.
	"<project>-xxxxx": ``,
	"beads://…":       `beads://`,
	"p3-propose":      `\bp\d+-[a-z][a-z-]*\b`,
	"s10-review":      `\bs\d+-[a-z][a-z-]*\b`,
	"PROPOSAL-N":      `\bPROPOSAL-\w+`,
	"URD":             `\bURD\b`,
	"URE":             `\bURE\b`,
	"SLICE-N":         `\bSLICE-?\w*`,
	"RATIFIED":        `\bRATIFIED\b`,
	"§7.1":            `§\s*\d`,
	"BLOCKER B3":      `\bBLOCKER\b`,
	"Scenario 14":     `\bScenario \d+\b`,
	"D5":              `\bD\d{1,2}\b`,
	"R13":             `\bR\d{1,2}\b`,
}

// packageInternalReferencePatterns derives the forbidden forms from the AGENTS.md
// rule that defines them, and refuses an exemplar it cannot expand, an entry the
// rule no longer spells, and a pattern that does not recognise its own exemplar.
func packageInternalReferencePatterns(t *testing.T) []*regexp.Regexp {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "resolve the repository root from cmd/pasture")
	raw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err, "read AGENTS.md, where the rule this guard enforces is written")

	document := string(raw)
	const heading = "## References & Internal Identifiers"
	start := strings.Index(document, heading)
	require.NotEqual(t, -1, start,
		"AGENTS.md must still carry the %q section; without it this guard recognises NOTHING", heading)
	block := document[start:]
	const opens = "**Rule — do NOT place"
	require.Contains(t, block, opens, "the section must still state its rule")
	block = block[strings.Index(block, opens):]
	if ends := strings.Index(block, "\nThe rule targets"); ends != -1 {
		block = block[:ends]
	}

	patterns := []*regexp.Regexp{}
	unknown := []string{}
	spelled := map[string]bool{}
	for _, match := range regexp.MustCompile("`([^`\n]+)`").FindAllStringSubmatch(block, -1) {
		exemplar := match[1]
		if spelled[exemplar] {
			continue
		}
		spelled[exemplar] = true
		expression, known := packageInternalReferenceForms[exemplar]
		if !known {
			unknown = append(unknown, exemplar)
			continue
		}
		if expression == "" {
			continue
		}
		pattern := regexp.MustCompile(expression)
		// EACH PATTERN MUST RECOGNISE THE EXEMPLAR IT WAS WRITTEN FOR. That is
		// the least a translation can be asked, and without it a pattern
		// narrowed to nothing keeps its family's name and matches nothing.
		require.Regexp(t, pattern, exemplar,
			"the pattern %s was written for the exemplar %q the rule spells and does not recognise "+
				"it. A pattern that misses its own exemplar has been narrowed past the family it "+
				"stands for", pattern, exemplar)
		patterns = append(patterns, pattern)
	}
	sort.Strings(unknown)
	require.Empty(t, unknown,
		"AGENTS.md spells these forbidden forms and this guard cannot recognise them: %v. The "+
			"POPULATION is read from the rule so a new form cannot be missed in silence; give each "+
			"a pattern, or map it to the empty string with the reason it is not recognised", unknown)
	// THE OTHER DIRECTION. A form that LEAVES the rule leaves the guard with
	// it, and that used to happen in silence: deleting one exemplar from the
	// rule retired its whole family from both copies and everything stayed
	// green. An entry the rule no longer spells fails by name instead.
	stale := []string{}
	for exemplar := range packageInternalReferenceForms {
		if !spelled[exemplar] {
			stale = append(stale, exemplar)
		}
	}
	sort.Strings(stale)
	require.Empty(t, stale,
		"this guard carries a pattern for %v, which the AGENTS.md rule no longer spells. A form "+
			"leaving the rule narrows this guard in silence unless somebody is made to say so: "+
			"either restore the exemplar to the rule, or delete the entry here and record why that "+
			"family is no longer forbidden", stale)
	// THE SAME REPOSITORY SCAN ITS SIBLING RUNS. The two packages cannot share a
	// helper, but they share a SOURCE: both derive the prefixes and the corpus
	// from this tree, and both pin the same stated project prefix and the same
	// floor (held equal by TestTheTwoInternalReferenceTablesAgree), so their
	// populations are equal by construction rather than by a comment claiming
	// they are.
	//
	// THE PATTERNS ARE THE BARE PREFIXES, one per prefix over the floor, and a
	// bare prefix is EXACTLY the literal it replaced and so exactly as wide.
	// Every hand-written suffix shape has narrowed — on length first, then on
	// alphabet — and nothing in this tree says what shape a generated suffix
	// takes, so no shape is asked for.
	prefixes, corpus := packageTaskIdentifierCorpus(t, root)
	for _, prefix := range prefixes {
		patterns = append(patterns, regexp.MustCompile(regexp.QuoteMeta(prefix+"-")))
	}
	require.NotEmpty(t, patterns, "the guard must recognise at least one form")

	// AT LEAST AS WIDE AS THE REAL POPULATION. This holds by construction
	// today, and it stays here as the control against the next hand-written
	// shape: the examples are the corpus, so a pattern cannot narrow on one
	// axis while the check measures another.
	for _, identifier := range corpus {
		recognised := false
		for _, pattern := range patterns {
			if pattern.MatchString(identifier) {
				recognised = true
				break
			}
		}
		require.True(t, recognised,
			"the derived patterns do not recognise %q, a REAL identifier from this repository's "+
				"own records. A derivation narrower than the population it stands for is a "+
				"regression wearing the word 'derived'", identifier)
	}
	for _, durable := range packageDurableReferenceSamples {
		for _, pattern := range patterns {
			require.NotRegexp(t, pattern, packageStripDurablePaths(durable),
				"%q is a reference the rule RECOMMENDS and %s refuses it", durable, pattern)
		}
	}
	return patterns
}

// assertNoInternalReferenceInPackage requires user-visible text to carry none.
//
// WHAT IT VISITS: the string given, against every pattern derived above.
// WHAT IT DOES NOT: text this test never renders, and the intent behind a word
// as opposed to the FORMS the rule spells.
func assertNoInternalReferenceInPackage(t *testing.T, where, text string) {
	t.Helper()
	for _, pattern := range packageInternalReferencePatterns(t) {
		assert.NotRegexp(t, pattern, packageStripDurablePaths(text),
			"%s carries an internal process reference matching %s. These identifiers mean nothing "+
				"to the person reading them and they rot as tasks close and proposals are "+
				"superseded; cite a durable file path, or nothing at all", where, pattern)
	}
}

// TestTheSchemaAdviceFollowsTheParserThatRefused drives the schema refusal on
// EVERY harness this build dispatches on and requires each reader to be told
// its own parser's rules — as MEASURED on that parser, not as a flag says.
//
// THE ADVICE NAMED THE HARNESS AND DESCRIBED ANOTHER ONE'S BEHAVIOUR. It told
// every reader, by harness name, that a member the registration does not
// declare is refused and that identity field names must match exactly. Claude
// validates the member set and looks names up in a map, so both hold there.
// Codex and OpenCode decode into a struct: an added member is IGNORED and the
// event is recorded, and a field name matches case-insensitively. A Codex
// operator was sent to remove a field that was never the problem and to
// re-spell names that already bind.
//
// THE EXPECTATION IS TAKEN FROM THE PARSER, NOT FROM THE DISPATCH ROW. The
// first version asserted the lenient wording on the harness whose row said
// lenient, so a parser made strict while its row stayed false was GREEN: the
// text was pinned to the flag and the flag was pinned to nothing. Each harness
// is therefore driven with two CONTROL probes first — a valid payload plus one
// undeclared member, and the same valid payload with one identity member
// re-cased — and what the parser DOES with them decides which sentences the
// refusal may carry. A row that lies about its parser in either direction
// turns RED here.
//
// WHAT IT VISITS: every harness in the derived coordinate set, each driven
// through the built binary on a valid payload, on its added-member and re-cased
// variants, and on a payload that reaches the schema refusal. The payload rows
// are written here and PINNED to the derived set, so a harness added to the
// product fails by name until it has a row.
// WHAT IT DOES NOT READ: which of a disposition's several causes fired, which
// the parser does not report.
//
// MUTATION: set refusesUndeclaredMembers or matchesFieldNamesExactly true on a
// lenient harness's dispatch row, or make a lenient parser strict while its row
// stays false (decode with DisallowUnknownFields in the Codex ingress). The
// subtest for that harness turns RED.
func TestTheSchemaAdviceFollowsTheParserThatRefused(t *testing.T) {
	binary := lifecycleBinary(t)

	rows := map[string]struct {
		Event       string
		HostVersion string
		Valid       []byte
		// Identity is the identity member the re-cased control re-spells. A
		// parser that looks names up exactly then finds it absent; a parser
		// that decodes into a struct binds it anyway.
		Identity string
		// Renamed reaches the schema refusal whose text is under test.
		Renamed []byte
	}{
		"claude-code": {
			Event: "PreToolUse", HostVersion: "2.1.222",
			Valid:    claudeFixture(t, "pre_tool_use_2_1_222.json"),
			Identity: "session_id",
			Renamed:  []byte(`{"renamed":"s","hook_event_name":"PreToolUse","tool_name":"R","tool_input":{}}`),
		},
		"codex": {
			Event: "PreToolUse", HostVersion: "0.146.0",
			Valid:    codexFixture(t, "pre_tool_use_0_146_0.json"),
			Identity: "session_id",
			Renamed:  []byte(`{"renamed":"s","hook_event_name":"PreToolUse"}`),
		},
		"opencode": {
			Event: "tool.execute.before", HostVersion: "1.18.10",
			Valid:    openCodeToolExecuteBeforeWire(t),
			Identity: "sessionID",
			Renamed:  []byte(`{"input":{"tool":"read","renamed":"ses1","callID":"call1"},"output":{"args":{}}}`),
		},
	}
	coordinates, derivationErr := handlers.LifecycleHarnessCoordinates()
	require.NoError(t, derivationErr, "the harness set is derived from the command's own registry")
	derived := []string{}
	for _, coordinate := range coordinates {
		derived = append(derived, coordinate.Harness)
	}
	written := []string{}
	for harness := range rows {
		written = append(written, harness)
	}
	sort.Strings(derived)
	sort.Strings(written)
	require.Equal(t, derived, written,
		"the payload rows here must cover EXACTLY the harnesses the command dispatches on. A harness "+
			"with no row is described by nothing, and a row for no harness describes nothing")

	for _, harness := range written {
		row := rows[harness]
		t.Run(harness, func(t *testing.T) {
			drive := func(payload []byte) lifecycleRun {
				store := t.TempDir()
				database := filepath.Join(store, "pasture.db")
				initializeLifecycleTestDatabase(t, database)
				return runLifecycleHookOn(t, binary, database, harness, row.Event, row.HostVersion, payload)
			}
			valid := drive(row.Valid)
			require.Equal(t, 0, valid.ExitCode, "the valid payload must be accepted\nstderr: %s", valid.Stderr)
			require.Empty(t, valid.Stderr,
				"the valid payload must be accepted in silence, or the controls below measure something "+
					"other than the one member each of them changes")

			// CONTROL ONE: does THIS parser refuse a member the registration
			// does not declare? Measured, not read off the dispatch row.
			added := drive(withTopLevelMember(t, row.Valid, "a_member_this_build_does_not_declare", `"x"`))
			refusesUndeclared := added.Stderr != ""
			if refusesUndeclared {
				require.Contains(t, added.Stderr, "Compare the payload with this build's",
					"the added member was refused by something other than the schema check, so nothing "+
						"below is about this parser's member rule\nstderr: %s", added.Stderr)
			}
			// CONTROL TWO: does THIS parser bind an identity member spelled in
			// another case?
			recased := drive(withMemberRecased(t, row.Valid, row.Identity))
			matchesExactly := recased.Stderr != ""
			if matchesExactly {
				require.Contains(t, recased.Stderr, "Compare the payload with this build's",
					"the re-cased identity was refused by something other than the schema check\nstderr: %s",
					recased.Stderr)
			}

			run := drive(row.Renamed)
			require.Contains(t, run.Stderr, "Compare the payload with this build's",
				"this subtest must reach the schema refusal, or nothing below is about it")

			if refusesUndeclared {
				assert.Contains(t, run.Stderr, "must carry no member the registration does not declare",
					"this parser DOES refuse an undeclared member — measured, the added-member control "+
						"was refused — so its reader is told so")
				assert.Contains(t, run.Stderr, "carries a member the registration does not allow",
					"and the diagnosis offers that cause, because on this parser it can fire")
			} else {
				assert.Contains(t, run.Stderr, "Members this build does not declare are IGNORED",
					"this parser accepted the added-member control in silence, so an added member is not "+
						"what refused this payload — and telling its reader otherwise sends them to "+
						"remove a field that was never the problem")
				assert.NotContains(t, run.Stderr, "must carry no member the registration does not declare",
					"the claim that added members are refused is FALSE of this parser: measured, an "+
						"added member gives exit 0, zero bytes of stderr and a recorded occurrence")
				assert.NotContains(t, run.Stderr, "carries a member the registration does not allow",
					"nor may the diagnosis offer that cause, which cannot fire on this parser; offered "+
						"beside the sentence that denies it, it made one message contradict itself")
			}
			if matchesExactly {
				assert.Contains(t, run.Stderr, "spelled exactly",
					"this parser refused the re-cased identity, so spelling is load-bearing here and "+
						"the reader is told so")
			} else {
				assert.NotContains(t, run.Stderr, "spelled exactly",
					"this parser bound the re-cased identity, so it must not be told names must match "+
						"exactly")
				assert.NotContains(t, run.Stderr, "renamed or unusable",
					"and a name that binds in any spelling cannot be 'renamed' into refusal, so the "+
						"diagnosis must not offer that either")
			}
		})
	}
}

// withTopLevelMember returns the payload with one member added at its top
// level. Every other member is carried byte-for-byte.
func withTopLevelMember(t *testing.T, payload []byte, name, rawValue string) []byte {
	t.Helper()
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &members), "the base payload must decode as an object")
	members[name] = json.RawMessage(rawValue)
	extended, err := json.Marshal(members)
	require.NoError(t, err)
	return extended
}

// withMemberRecased returns the payload with every member of the given name, at
// any object depth, re-spelled in upper case. Values are carried byte-for-byte;
// only object keys are rewritten, because the identity members of every
// harness here live in an object.
func withMemberRecased(t *testing.T, payload []byte, name string) []byte {
	t.Helper()
	found := false
	var recase func(raw json.RawMessage) json.RawMessage
	recase = func(raw json.RawMessage) json.RawMessage {
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			return raw
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return raw
		}
		rewritten := map[string]json.RawMessage{}
		for key, member := range object {
			if key == name {
				key = strings.ToUpper(name)
				found = true
			}
			rewritten[key] = recase(member)
		}
		encoded, err := json.Marshal(rewritten)
		require.NoError(t, err)
		return encoded
	}
	recased := recase(payload)
	require.True(t, found, "the control must change the payload; no member named %q was found in it", name)
	return recased
}

// packageStripDurablePaths removes the tokens the rule names as LEGITIMATE — file
// paths and URLs — before the forbidden forms are looked for.
//
// WHY: the rule's own recommended replacement for an internal reference is
// `docs/proposals/PROPOSAL-2-pasture-workflow-record.md`, and the PROPOSAL-N
// pattern matches inside it. Without this, citing the document the rule tells
// you to cite would fail the guard that enforces the rule.
func packageStripDurablePaths(text string) string {
	return regexp.MustCompile(`\S*/\S*`).ReplaceAllString(text, " ")
}

// ─────────────────────────────────────────────────────────────────────────────
// THE TASK-IDENTIFIER CORPUS, DERIVED FROM THIS REPOSITORY'S OWN RECORDS
// ─────────────────────────────────────────────────────────────────────────────
//
// THE AXES THIS DERIVATION HAS TO COVER, NAMED BEFORE IT IS WRITTEN, because
// the last three attempts were each exactly as wide as the probe that found them:
//
//	LENGTH    — the first pattern pinned a suffix to the five characters the
//	            illustration beside the rule spells, and every identifier in
//	            this tree's own records is five or six.
//	ALPHABET  — a suffix is base-36, so it need not carry a digit, and many
//	            real ones do not. The second pattern required one, so one
//	            real identifier in nine passed, including the founding blocker.
//	POPULATION— the examples that check the width were hand-written, so the
//	            check measured LENGTH while the pattern had narrowed on ALPHABET
//	            and nothing noticed.
//	OVER-MATCH— the digit heuristic ALSO matched a workflow id, a content digest
//	            and a versioned capture path, which are the DURABLE references
//	            the rule recommends using instead of an internal id.
//	DRIFT     — two packages carry this guard and their example lists had
//	            already diverged while a comment said they were the same.
//	IDENTITY  — the third attempt ELECTED one prefix by distinct-suffix count
//	            and pinned only the MARGIN between the winner and the runner-up.
//	            One Markdown file carrying more than twice as many distinct
//	            tails under another hyphenated prefix won that election, passed
//	            the margin pin, and the guard stopped recognising every real
//	            identifier — in both packages, with the whole tree green.
//
// ONE DERIVATION CLOSES ALL SIX. The pattern is the bare prefix, so length and
// alphabet are not asked about at all; the width check is the corpus itself,
// so it cannot measure a different axis from the one a pattern narrows on; a
// digest or a workflow id carries another prefix, so it cannot match; both
// packages scan the same tree and hold their hand-written tables and their
// two constants equal by test (TestTheTwoInternalReferenceTablesAgree, below,
// reads the sibling's source); and NOTHING IS ELECTED. Every prefix over the
// population floor is recognised, as a UNION, so a newcomer ADDS a pattern and
// can never REPLACE one; and the project's own prefix is a stated FACT the
// union must contain, not the result of a count.

// packageProjectTaskIdentifierPrefix is the prefix this project's task tracker puts
// on every task identifier. It is a FACT about the project, stated here, and
// NOT a derivation: the tracker's configuration lives outside this tree, so
// nothing in the tree can read it, and the derivation below cannot be trusted
// to find it, because a derivation that can find a prefix can also lose it.
// The derivation must FIND this prefix over the floor or fail by name. It may
// find other prefixes beside it, and then it recognises those as well.
const packageProjectTaskIdentifierPrefix = "aura-plugins"

// packageTaskIdentifierPopulationFloor is the number of DISTINCT suffixes a multi-word
// prefix must carry, over the whole tree, to be recognised as a task-identifier
// prefix. A generated identifier family carries many distinct random tails
// under one prefix; ordinary hyphenated English carries a handful. Measured on
// this tree: the project prefix carries fifty-five, the widest phrase eight.
const packageTaskIdentifierPopulationFloor = 20

// packageTaskIdentifierCorpus returns every task-identifier prefix this repository's
// own records carry, sorted, and every distinct identifier found under them.
//
// WHAT IT VISITS: every .go and .md file under the repository root, for tokens
// shaped `<multi-word-prefix>-<five or six base-36 characters>`; every
// multi-word prefix carrying at least packageTaskIdentifierPopulationFloor distinct
// suffixes is a prefix of the result.
// WHAT IT DOES NOT READ: any other file kind, and an identifier of another
// length. THE LENGTH RANGE IS WRITTEN DOWN, AND IT BOUNDS THE CORPUS ONLY: the
// pattern the result feeds is the bare prefix, so an identifier of another
// length under a recognised prefix is still recognised. Widening the range to
// three characters lets ordinary hyphenated English (`unified-schema-...`)
// clear the floor. Such a phrase JOINS the union, and the guard then refuses
// that phrase in host-visible text BY NAME, quoting the pattern — an over-match
// that is loud where it lands. What no widening, narrowing or newcomer can do
// is REMOVE the project prefix: that is pinned below against the stated fact,
// and a floor raised above the project's own records, a file kind that carries
// them dropped from the walk, or a shape that no longer admits them, each
// fails here by name instead of leaving the guard blind.
func packageTaskIdentifierCorpus(t *testing.T, root string) ([]string, []string) {
	t.Helper()

	shape := regexp.MustCompile(`\b([a-z][a-z0-9]*(?:-[a-z0-9]+)*)-([a-z0-9]{5,6})\b`)
	found := map[string]map[string]bool{}
	// kinds records which file kinds each prefix was read from, so the walk
	// can be held to BOTH kinds its doc names.
	kinds := map[string]map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".md" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, match := range shape.FindAllStringSubmatch(string(raw), -1) {
			prefix, suffix := match[1], match[2]
			if found[prefix] == nil {
				found[prefix] = map[string]bool{}
				kinds[prefix] = map[string]bool{}
			}
			found[prefix][prefix+"-"+suffix] = true
			kinds[prefix][ext] = true
		}
		return nil
	})
	require.NoError(t, err, "walk the repository for real task identifiers")

	// THE SELECTOR IS DISTINCT SUFFIXES UNDER A MULTI-WORD PREFIX, and it is
	// not occurrence count. Ranking by occurrences admits "review", because
	// ordinary hyphenated English dominates a Go repository and any five- or
	// six-letter word looks like a suffix. A task identifier is generated, so
	// ONE prefix carries MANY DISTINCT random suffixes while an English phrase
	// carries a handful. The prefix must itself be multi-word, which every
	// project identifier is and most stray matches are not.
	//
	// THERE IS NO WINNER. Every prefix over the floor is kept, so the one thing
	// a newcomer can do is join, and the one thing it cannot do is push another
	// prefix out. The previous version elected the single prefix with the most
	// distinct suffixes and pinned the margin over the runner-up; a runner-up
	// that overtook by MORE than that margin won in silence.
	prefixes := []string{}
	for candidate, identifiers := range found {
		if !strings.Contains(candidate, "-") || len(identifiers) < packageTaskIdentifierPopulationFloor {
			continue
		}
		prefixes = append(prefixes, candidate)
	}
	sort.Strings(prefixes)
	require.NotEmpty(t, prefixes,
		"no multi-word prefix carries %d distinct five-or-six-character suffixes anywhere in this "+
			"repository, so this guard has nothing to derive from and every assertion resting on "+
			"it would pass vacuously", packageTaskIdentifierPopulationFloor)

	// THE PROJECT PREFIX IS A FACT THE DERIVATION MUST FIND, not a result it
	// may report. This is the pin the election never had: the recognised set
	// may grow, and it may never lose the prefix the project's own records
	// carry.
	require.Contains(t, prefixes, packageProjectTaskIdentifierPrefix,
		"the prefixes over the floor are %v, and %q — the prefix this project's task tracker "+
			"assigns, stated as a fact above — is not among them: it carries %d distinct suffixes "+
			"in the tree against a floor of %d. Either the records that carry it left the tree, "+
			"the floor was raised above them, a file kind that carries them was dropped from the "+
			"walk, or the shape no longer admits them. Left unpinned, the guard would stop "+
			"recognising every real task identifier in silence, which is the flip this pin "+
			"refuses", prefixes, packageProjectTaskIdentifierPrefix,
		len(found[packageProjectTaskIdentifierPrefix]), packageTaskIdentifierPopulationFloor)

	// EACH FILE KIND MUST CONTRIBUTE, or a kind can leave the walk in silence.
	// Measured on this tree, the .md files alone carry the project prefix over
	// the floor and the .go files alone do not, so dropping .go from the walk
	// would leave the fact pin above satisfied while the corpus stopped
	// reading source. The doc of this function names both kinds; this holds it
	// to both.
	for _, kind := range []string{".go", ".md"} {
		require.True(t, kinds[packageProjectTaskIdentifierPrefix][kind],
			"no %s file carries a %q identifier, so the walk no longer reads that kind, or the "+
				"kind was dropped from it. The doc of this function states that both kinds are "+
				"read, and the corpus must be read from both", kind, packageProjectTaskIdentifierPrefix)
	}

	identifiers := []string{}
	for _, prefix := range prefixes {
		for identifier := range found[prefix] {
			identifiers = append(identifiers, identifier)
		}
	}
	sort.Strings(identifiers)

	// THE CORPUS MUST COVER THE ALPHABET AND THE LENGTH AXES over the project's
	// own identifiers, or the width check measures one axis while a pattern or
	// the shape narrows on another.
	digitFree, fiveLong, sixLong := 0, 0, 0
	for identifier := range found[packageProjectTaskIdentifierPrefix] {
		suffix := identifier[len(packageProjectTaskIdentifierPrefix)+1:]
		if !regexp.MustCompile(`[0-9]`).MatchString(suffix) {
			digitFree++
		}
		switch len(suffix) {
		case 5:
			fiveLong++
		case 6:
			sixLong++
		}
	}
	require.NotZero(t, digitFree,
		"the corpus carries no DIGIT-FREE identifier, so it cannot catch a pattern that narrows on "+
			"the alphabet — which is exactly how the previous width check passed while one real "+
			"identifier in nine went unrecognised")
	require.NotZero(t, fiveLong,
		"the corpus carries no FIVE-character project identifier, so the shape above no longer "+
			"admits the length most real identifiers have")
	require.NotZero(t, sixLong,
		"the corpus carries no SIX-character project identifier, so the shape above no longer "+
			"admits the length the first width check missed; a corpus bounded to one length "+
			"cannot catch a pattern that narrows on the other")
	return prefixes, identifiers
}

// packageDurableReferenceSamples are references the rule RECOMMENDS. None may
// be recognised as an internal identifier: a guard that refuses the replacement
// it exists to encourage is worse than no guard.
//
// THEY ARE SAMPLES AND NOT THE CLASS, one per axis the over-match had: a
// workflow identifier, a content digest, a versioned path, and ordinary
// hyphenated English from this command's own diagnostics.
var packageDurableReferenceSamples = []string{
	"e005-416d-a53a-49b495cd5d4a",
	"sha256-9f2a1b",
	"pre-tool-use-0146",
	"report-and-continue",
	"throw-fail-fast",
	"fail-closed",
	"docs/proposals/PROPOSAL-2-pasture-workflow-record.md",
}

// TestTheTwoInternalReferenceTablesAgree reads the sibling guard's source in
// internal/lifecycle/hostexit and requires its hand-written tables — the
// pattern per exemplar and the durable samples — to equal the ones here.
//
// WHY. The derived halves of the two guards cannot drift: both read the same
// rule block and scan the same tree. The hand-written halves can, and did — the
// two example lists this derivation replaced had eight entries against six
// under a comment saying they carried the same ones. A comment cannot hold two
// files equal; a test that reads one file from the other can.
//
// WHAT IT VISITS: the two package-level composite literals and the two
// package-level constants named below in the sibling's test source, read
// through the parser.
// WHAT IT DOES NOT READ: the sibling's derivation code, which is held equal to
// this one's only by the shared sources it reads — and by the two constants
// it is pinned against, which ARE read here, because a floor raised in one
// copy or a project prefix changed in one copy is drift in the derived half
// that no shared source can prevent: those two values are stated, not read.
func TestTheTwoInternalReferenceTablesAgree(t *testing.T) {
	t.Parallel()

	sibling := filepath.Join("..", "..", "internal", "lifecycle", "hostexit", "hostexit_test.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), sibling, nil, 0)
	require.NoError(t, err, "the sibling guard's source must parse")

	literals := map[string]*ast.CompositeLit{}
	for _, node := range parsed.Decls {
		declaration, isDeclaration := node.(*ast.GenDecl)
		if !isDeclaration {
			continue
		}
		for _, spec := range declaration.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			if literal, isLiteral := value.Values[0].(*ast.CompositeLit); isLiteral {
				literals[value.Names[0].Name] = literal
			}
		}
	}
	unquote := func(expression ast.Expr) string {
		basic, isBasic := expression.(*ast.BasicLit)
		require.True(t, isBasic, "every table entry in the sibling must be a string literal")
		text, unquoteErr := strconv.Unquote(basic.Value)
		require.NoError(t, unquoteErr)
		return text
	}

	forms, present := literals["internalReferenceForms"]
	require.True(t, present, "the sibling must still declare internalReferenceForms as a map literal")
	siblingForms := map[string]string{}
	for _, element := range forms.Elts {
		pair, isPair := element.(*ast.KeyValueExpr)
		require.True(t, isPair, "every entry of the sibling's table must be key: value")
		siblingForms[unquote(pair.Key)] = unquote(pair.Value)
	}
	assert.Equal(t, siblingForms, packageInternalReferenceForms,
		"the pattern table here and the one in internal/lifecycle/hostexit differ. A pattern "+
			"corrected in one copy and not the other is the drift the two example lists already "+
			"suffered; change both, or neither")

	samples, present := literals["durableReferenceSamples"]
	require.True(t, present, "the sibling must still declare durableReferenceSamples as a slice literal")
	siblingSamples := []string{}
	for _, element := range samples.Elts {
		siblingSamples = append(siblingSamples, unquote(element))
	}
	assert.Equal(t, siblingSamples, packageDurableReferenceSamples,
		"the durable samples here and the ones in internal/lifecycle/hostexit differ; a sample "+
			"added to one copy measures an over-match the other copy cannot see")

	// THE TWO CONSTANTS THE DERIVATION IS PINNED AGAINST: the stated project
	// prefix and the population floor. Neither is read from a source, so
	// nothing but this holds the two copies to the same values.
	constants := map[string]string{}
	for _, node := range parsed.Decls {
		declaration, isDeclaration := node.(*ast.GenDecl)
		if !isDeclaration || declaration.Tok != token.CONST {
			continue
		}
		for _, spec := range declaration.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			if basic, isBasic := value.Values[0].(*ast.BasicLit); isBasic {
				constants[value.Names[0].Name] = basic.Value
			}
		}
	}
	prefix, present := constants["projectTaskIdentifierPrefix"]
	require.True(t, present, "the sibling must still state projectTaskIdentifierPrefix as a constant")
	assert.Equal(t, strconv.Quote(packageProjectTaskIdentifierPrefix), prefix,
		"the stated project prefix here and the one in internal/lifecycle/hostexit differ. The "+
			"prefix is a fact about the project and there is one project; a copy that states "+
			"another one pins its derivation to a prefix the records do not carry")
	floor, present := constants["taskIdentifierPopulationFloor"]
	require.True(t, present, "the sibling must still state taskIdentifierPopulationFloor as a constant")
	assert.Equal(t, strconv.Itoa(packageTaskIdentifierPopulationFloor), floor,
		"the population floor here and the one in internal/lifecycle/hostexit differ. A floor "+
			"raised in one copy narrows what that copy recognises while the other still "+
			"recognises it, which is the drift the two example lists already suffered")
}

// TestTheOutOfSetHandoverNamesEveryGuardItFound runs the same predicate over the
// FOREIGN files and requires the handover in the closing sweep's doc to name
// EXACTLY the guards it finds there.
//
// WHAT IT VISITS: every file in guardSweepForeign, with the same claim-and-shape
// predicate the sweep uses on the owned ones; and the doc comment of the sweep,
// read through the parser rather than as a whole-file substring, so a name
// mentioned anywhere else in this file cannot stand in for the handover.
// WHAT IT DOES NOT READ: whether those guards are actually defective — that is
// their owners' judgement, and this hands them a list rather than a verdict.
//
// WHY IT EXISTS. The handover was written from the guards a reviewer happened to
// name, and said TWO where the predicate finds more. A handover assembled from
// memory is the same defect as a population assembled from memory, one document
// further out, and it is the one place where being wrong costs somebody ELSE the
// time.
func TestTheOutOfSetHandoverNamesEveryGuardItFound(t *testing.T) {
	t.Parallel()

	found := []string{}
	for name := range guardSweepForeign {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, parser.ParseComments)
		require.NoError(t, parseErr, "every foreign test file must parse: %s", name)
		for _, node := range parsed.Decls {
			function, isFunction := node.(*ast.FuncDecl)
			if !isFunction || !strings.HasPrefix(function.Name.Name, "Test") || function.Doc == nil {
				continue
			}
			doc := strings.ToLower(function.Doc.Text())
			claims := false
			for _, phrase := range guardReachClaims {
				if strings.Contains(doc, phrase) {
					claims = true
					break
				}
			}
			if claims && rangesOverAConstantPopulation(function) {
				found = append(found, function.Name.Name)
			}
		}
	}
	sort.Strings(found)
	require.NotEmpty(t, found,
		"the predicate must find guards in the foreign files; finding none means it no longer "+
			"matches how they are written and this handover is empty for the wrong reason")

	// THE HANDOVER IS THE SWEEP'S OWN DOC, and the names in it that this
	// package does not declare are the ones it hands over.
	parsed, parseErr := parser.ParseFile(token.NewFileSet(), "hook_lifecycle_failuremode_test.go", nil, parser.ParseComments)
	require.NoError(t, parseErr, "the file that carries the handover must parse")
	var handover string
	for _, node := range parsed.Decls {
		if function, isFunction := node.(*ast.FuncDecl); isFunction &&
			function.Name.Name == "TestEveryGuardThatClaimsAClassSaysWhatItReads" && function.Doc != nil {
			handover = function.Doc.Text()
		}
	}
	require.NotEmpty(t, handover, "the closing sweep must still carry the handover in its doc")
	declaredHere := map[string]bool{}
	for _, name := range guardSweepSubjects(t) {
		owned, ownedErr := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		require.NoError(t, ownedErr, "every owned test file must parse: %s", name)
		for _, node := range owned.Decls {
			if function, isFunction := node.(*ast.FuncDecl); isFunction {
				declaredHere[function.Name.Name] = true
			}
		}
	}
	named := map[string]bool{}
	for _, name := range regexp.MustCompile(`\bTest[A-Za-z0-9_]+`).FindAllString(handover, -1) {
		if !declaredHere[name] {
			named[name] = true
		}
	}

	unnamed, stale := []string{}, []string{}
	foundSet := map[string]bool{}
	for _, name := range found {
		foundSet[name] = true
		if !named[name] {
			unnamed = append(unnamed, name)
		}
	}
	for name := range named {
		if !foundSet[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	assert.Empty(t, unnamed,
		"the predicate finds these guards in files this slice does not own, and the handover in "+
			"the closing sweep's doc does not name them: %v. A handover written from the guards "+
			"somebody remembered is the same defect as a population written that way, one document "+
			"further out — and this is the one where being wrong costs another owner the time", unnamed)
	assert.Empty(t, stale,
		"the handover names these guards and the predicate no longer finds them in the foreign "+
			"files: %v. A name that outlives its finding sends an owner to look at a guard that "+
			"has already been derived or discharged", stale)
}

// TestTheReadOnlyStoreRouteWritesOnlyTheHostsContinueBytesOnEveryHarness
// measures route 3 of the fault record — the record cannot be opened although
// its directory exists — on the bytes every host receives, and holds the
// AGENTS.md sentence that describes the route to what each host received.
//
// THE SENTENCE WAS MEASURED ON ONE HARNESS AND WRITTEN FOR ALL. It said
// "nothing at all on standard output", which is true of Claude Code, whose
// continue bytes are empty, and false of OpenCode and Codex, which write their
// continue bytes on this route because that is what fail-open means there. An
// operator on those hosts, debugging a lost record, was told to expect an empty
// standard output and met a JSON body with nothing saying it was the
// continuation and not a decision.
//
// WHAT IT VISITS: every harness in the derived coordinate set, driven through
// the built binary twice on one valid payload — once with the record path
// standing as a DIRECTORY, which is route 3 on any user including root, and
// once with it free, the CONTROL. The route must write the same standard output
// as the control, exit 0 like the control, report the open failure on standard
// error where the control reports none, and leave no record where the control
// leaves one line. Then the "The lifecycle fault record" section of AGENTS.md
// must state, per harness by name, the bytes that were measured.
// WHAT IT DOES NOT READ: routes 1, 2, 4 and 5, which have their own byte pins;
// and whether the host reads those bytes as a proceed, which the production
// proofs measure.
//
// MUTATION: make the open arm write to cmd.OutOrStdout(), or return without a
// word; every harness's subtest turns RED. MUTATION: restore "nothing at all on
// standard output" in AGENTS.md in place of the per-harness clauses; the Codex
// and OpenCode subtests turn RED on the document pin.
func TestTheReadOnlyStoreRouteWritesOnlyTheHostsContinueBytesOnEveryHarness(t *testing.T) {
	binary := lifecycleBinary(t)

	rows := map[string]struct {
		Event       string
		HostVersion string
		Payload     []byte
		// Name is how AGENTS.md names the host, which is what the document
		// pin below looks for beside the measured bytes.
		Name string
	}{
		"claude-code": {Event: "PreToolUse", HostVersion: "2.1.222",
			Payload: claudeFixture(t, "pre_tool_use_2_1_222.json"), Name: "Claude Code"},
		"codex": {Event: "PreToolUse", HostVersion: "0.146.0",
			Payload: codexFixture(t, "pre_tool_use_0_146_0.json"), Name: "Codex"},
		"opencode": {Event: "tool.execute.before", HostVersion: "1.18.10",
			Payload: openCodeToolExecuteBeforeWire(t), Name: "OpenCode"},
	}
	coordinates, derivationErr := handlers.LifecycleHarnessCoordinates()
	require.NoError(t, derivationErr, "the harness set is derived from the command's own registry")
	derived := []string{}
	for _, coordinate := range coordinates {
		derived = append(derived, coordinate.Harness)
	}
	written := []string{}
	for harness := range rows {
		written = append(written, harness)
	}
	sort.Strings(derived)
	sort.Strings(written)
	require.Equal(t, derived, written,
		"the payload rows here must cover EXACTLY the harnesses the command dispatches on. A harness "+
			"with no row is measured by nothing, and the document sentence is then true of nothing")

	repository, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, "resolve the repository root from cmd/pasture")
	raw, err := os.ReadFile(filepath.Join(repository, "AGENTS.md"))
	require.NoError(t, err, "read AGENTS.md, which is where the route is described")
	const heading = "### The lifecycle fault record"
	document := string(raw)
	require.Contains(t, document, heading, "the route description must still live under its own heading")
	section := document[strings.Index(document, heading):]
	if next := strings.Index(section[len(heading):], "\n### "); next != -1 {
		section = section[:len(heading)+next]
	}
	// THE COUNT THE SENTENCE OPENS WITH IS THE SIZE OF THE DERIVED SET, so a
	// fourth harness cannot leave "all three" standing.
	require.Contains(t, spelledCounts, len(derived),
		"the harness count %d has no spelling in this table; add the word", len(derived))
	assert.Contains(t, section, "on all "+strings.ToLower(spelledCounts[len(derived)])+" harnesses",
		"AGENTS.md must open the measurement with the number of harnesses it was taken on, "+
			"which is the size of the derived set, %d", len(derived))

	for _, harness := range written {
		row := rows[harness]
		t.Run(harness, func(t *testing.T) {
			drive := func(recordPathIsADirectory bool) lifecycleRun {
				store := t.TempDir()
				database := filepath.Join(store, "not-a-database")
				require.NoError(t, os.Mkdir(database, 0o755),
					"the store path must be a directory, so opening it as a database is a real "+
						"storage fault on every harness alike")
				if recordPathIsADirectory {
					require.NoError(t, os.Mkdir(filepath.Join(store, lifecycleFaultRecordFile), 0o755),
						"the record's own path must be a directory, so its parent can be made and "+
							"the file itself cannot be opened, on any user including root")
				}
				return runLifecycleHookOn(t, binary, database, harness, row.Event, row.HostVersion, row.Payload)
			}
			control := drive(false)
			require.Equal(t, 0, control.ExitCode,
				"the control is the same fault with a record that can be placed, and it is fail-open")
			require.NotContains(t, control.Stderr, "could not open its fault record",
				"the control must keep its record, or the route below is measured against a broken control")
			require.Len(t, readFaultRecords(t, control.FaultDir), 1,
				"the control must leave one record line, or nothing below is about the record")

			route := drive(true)
			assert.Equal(t, 0, route.ExitCode,
				"route 3 is fail-open: the record is evidence for a maintainer and never a condition "+
					"of the host outcome")
			assert.Equal(t, control.Stdout, route.Stdout,
				"standard output on route 3 must be EXACTLY what the same fault writes when its record "+
					"can be placed: this host's continue bytes and nothing else. A byte more is a "+
					"diagnostic ahead of the continuation, which stops an OpenCode gate; a byte fewer "+
					"is a continuation the host never received")
			assert.Contains(t, route.Stderr, "could not open its fault record",
				"the open arm must reach its own diagnostic; if it does not, this subtest proves "+
					"nothing about the route")
			// The record path stands as a directory on this route by
			// construction, so "no record file anywhere" is read as: the
			// directory is still there and nothing was written into it.
			entries, readErr := os.ReadDir(filepath.Join(route.FaultDir, lifecycleFaultRecordFile))
			require.NoError(t, readErr,
				"the record path must still be the directory this subtest made; anything else "+
					"means the input did not drive route 3")
			assert.Empty(t, entries, "no record can exist on route 3, and nothing may be written beside where it would stand")

			// THE DOCUMENT STATES WHAT WAS MEASURED, PER HOST BY NAME.
			if route.Stdout == "" {
				assert.Contains(t, section, "nothing at all on "+row.Name,
					"AGENTS.md must say, naming this host, that it receives nothing on standard output "+
						"on this route; the sentence was true of this host and written for all")
			} else {
				assert.Contains(t, section, "`"+route.Stdout+"` on "+row.Name,
					"AGENTS.md must state the bytes this host receives on standard output on this "+
						"route, naming the host, or an operator on it expects an empty standard "+
						"output and meets a JSON body")
			}
		})
	}
}

// TestAnUndeclaredCoordinateIsNotReportedAsADeclaration drives the two routes a
// host can reach with a stale generated hook or a mismatched binary — a harness
// this build does not support, and an event name its registration does not
// carry — and requires the diagnostic and the durable record to say that
// NOTHING declares the row.
//
// ONE MESSAGE, TWO CLAUSES THAT DISAGREED. The command treats such a
// coordinate as observe-only, which is the weakest claim it can make, and the
// phase line rendered that as "declared failure mode observe-only" while the
// cause two clauses later said the harness is not supported or the event is
// not in the registration. The record wrote "declaredFailureMode":
// "observe-only" as well, so a maintainer grouping the record by declaration
// — the grouping AGENTS.md anticipates — counted a stale hook among the
// thirty-two OpenCode rows that really declare observe-only.
//
// WHAT IT VISITS: the two undeclared routes through the built binary, the
// phase line and the fail-closed advice on standard error, and the two mode
// members of the record; plus a CONTROL on a declared row, which must keep
// reading as a declaration.
// WHAT IT DOES NOT READ: the cause text of either refusal, which its own tests
// pin.
//
// MUTATION: give lifecycleFailurePolicy's fallback a valid Semantic, drop
// `Undeclared:` from the Fault the command builds, or write
// recordedFailureMode(failure.DeclaredMode) into the record again. The
// undeclared subtests turn RED on "declared failure mode none" or on
// "undeclared".
func TestAnUndeclaredCoordinateIsNotReportedAsADeclaration(t *testing.T) {
	binary := lifecycleBinary(t)

	const undeclaredClause = "declared failure mode none (no row of this build's registration declares this event, so it is treated as observe-only)"

	for _, route := range []struct {
		name, harness, event, hostVersion, cause string
	}{
		{name: "a harness this build does not support", harness: "gemini", event: "NotAnEvent",
			hostVersion: "1.0", cause: "is not supported"},
		{name: "an event the registration does not carry", harness: "claude-code", event: "NotAnEvent",
			hostVersion: "2.1.222", cause: "is not in the generated"},
	} {
		route := route
		t.Run(route.name, func(t *testing.T) {
			dir := t.TempDir()
			run := runLifecycleHookOn(t, binary, filepath.Join(dir, "pasture.db"),
				route.harness, route.event, route.hostVersion, []byte("{}"))
			require.Equal(t, 0, run.ExitCode, "an undeclared coordinate is treated as observe-only and continues")
			require.Contains(t, run.Stderr, route.cause,
				"this route must reach the cause that says nothing declares the row, or the assertions "+
					"below are about another fault")
			assert.Contains(t, run.Stderr, undeclaredClause,
				"the phase line must say that no row declares the event and what the event is treated as")
			assert.NotContains(t, run.Stderr, "declared failure mode observe-only",
				"calling the treated-as mode a declaration contradicts the cause two clauses later")
			assert.Contains(t, run.Stderr, "effective failure mode observe-only",
				"the mode the row runs as is still named, because that one is true")

			records := readFaultRecords(t, run.FaultDir)
			require.Len(t, records, 1, "the fault must leave exactly one record line")
			assert.Equal(t, "undeclared", records[0]["declaredFailureMode"],
				"a parser grouping the record by declaration must not count this hook among the rows "+
					"that declare observe-only; the member carries one stable word instead")
			assert.Equal(t, "observe-only", records[0]["failureMode"],
				"the effective mode is what the command treated the row as, and that is true")

			optedIn := runLifecycleHookOn(t, binary, filepath.Join(t.TempDir(), "pasture.db"),
				route.harness, route.event, route.hostVersion, []byte("{}"), hookFailClosedEnv+"=1")
			require.Equal(t, 0, optedIn.ExitCode, "an undeclared coordinate has no exit code the opt-in could use")
			assert.Contains(t, optedIn.Stderr,
				"no row of this build's registration declares the event, so it is treated as observe-only",
				"the fail-closed advice must give the reason that is true of this row: nothing declares it")
			assert.NotContains(t, optedIn.Stderr, "its declared failure mode observe-only",
				"the treated-as mode is not the row's declaration")
		})
	}

	t.Run("a declared row is still reported as a declaration", func(t *testing.T) {
		dir := t.TempDir()
		run := runLifecycleHookOn(t, binary, unopenableDatabase(t, dir),
			"claude-code", "PostToolUse", "2.1.222", claudeFixture(t, "post_tool_use_2_1_222.json"))
		assert.Contains(t, run.Stderr, "declared failure mode report-and-continue",
			"a declared row names its declaration, and the control must keep saying so")
		assert.NotContains(t, run.Stderr, "declared failure mode none",
			"the undeclared clause must not leak onto a declared row")
		records := readFaultRecords(t, run.FaultDir)
		require.Len(t, records, 1)
		assert.Equal(t, "report-and-continue", records[0]["declaredFailureMode"],
			"a declared row's record keeps the declared mode by name")
	})
}
