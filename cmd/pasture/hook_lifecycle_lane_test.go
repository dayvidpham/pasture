package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/handlers"
	"github.com/dayvidpham/pasture/internal/lifecycle/hostexit"
	"github.com/dayvidpham/pasture/internal/lifecycle/laneserve"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/timeouts"
	"github.com/dayvidpham/pasture/internal/writelane"
)

// laneTestSocketPath returns a socket path inside a short private directory. A
// UNIX socket path is bounded by sun_path, and a test name is longer than that
// on its own, so the socket does not live under t.TempDir().
func laneTestSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "pasture-hook-lane-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "lane.sock")
}

// startHookTestWriteLane hosts a REAL daemon lane in-process over a private
// socket and returns its path, so the hook's client path is exercised against
// the production server, lane and store rather than a stub.
func startHookTestWriteLane(t *testing.T, dbPath string) string {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracker.Close()) })

	host, err := tasks.NewLifecycleLaneHost(tracker, tasks.LaneClock{}, tasks.LaneOperationIDs{}, timeouts.TestProfile())
	require.NoError(t, err)
	budget, err := writelane.BudgetFromProfile(timeouts.TestProfile())
	require.NoError(t, err)
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(host.Tracker(), host.Clock(), host.Operations(), host.Profile())
	require.NoError(t, err)
	lane, err := writelane.New(budget, service.Appender.AppendBatch)
	require.NoError(t, err)

	server, err := handlers.NewHookLifecycleServer(handlers.HookLifecycleServerConfig{
		Tracker: host.Tracker(), Lane: lane, Clock: host.Clock(), Operations: host.Operations(),
		Profile: host.Profile(), Readers: host.NewGateReaderFactory(),
	})
	require.NoError(t, err)
	connServer, err := laneserve.NewServer(server, timeouts.TestProfile().WorkflowResult())
	require.NoError(t, err)

	socketPath := laneTestSocketPath(t)
	listener, err := laneserve.Listen(socketPath, connServer)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, listener.Close(context.Background()))
		<-done
		require.NoError(t, lane.Close(context.Background()))
	})
	return socketPath
}

func initializeWarmLaneTestDatabase(t *testing.T, dbPath string) {
	t.Helper()
	initializeLifecycleTestDatabase(t, dbPath)
	tracker, err := tasks.OpenTaskTracker(dbPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	service, err := tasks.NewLifecycleReceiptServiceWithProfile(tracker, tasks.LaneClock{}, tasks.LaneOperationIDs{}, timeouts.TestProfile())
	require.NoError(t, err)
	testutil.ActivateLaneTestDefinitions(t, service)
	require.Empty(t, queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind),
		"definition setup must never manufacture an occurrence")
}

// readLastFaultRecordLine returns the newest fault record line as a map, so a
// test can assert on members rather than on the whole sentence.
func readLastFaultRecordLine(t *testing.T) map[string]any {
	t.Helper()
	recordPath := lifecycleFaultRecordPath()
	require.NotEmpty(t, recordPath)
	raw, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.NotEmpty(t, lines)
	var line map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &line))
	return line
}

// TestTheWriteLaneAbsentStaysSilentAndWritesDirectly is the fallback row for a
// machine that has never run pastured: the hook must behave EXACTLY as a build
// without a lane does — same host bytes, same standard error — while the fault
// record still says writer=direct.
func TestTheWriteLaneAbsentStaysSilentAndWritesDirectly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	t.Setenv(hookWriteLaneEnv, string(WriteLaneAuto))
	t.Setenv(hookWriteLaneSocketEnv, filepath.Join(t.TempDir(), "absent.sock"))

	// An event this build does not declare refuses before the store, so the
	// invocation produces a fault record without any store contention.
	cmd := lifecycleTestCommand(t, "claude-code", "anEventNoRegistrationDeclares", "2.1.261", dbPath)
	cmd.SetIn(strings.NewReader(`{}`))
	var diagnostics strings.Builder
	cmd.SetErr(&diagnostics)
	outcome := lifecycleTestOutcome(t, cmd, nil,
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)

	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"a lane that is not there must never change the host outcome")
	require.Empty(t, diagnostics.String(),
		"a machine with no lane socket must produce exactly the bytes a build without a lane produces")

	line := readLastFaultRecordLine(t)
	require.Equal(t, lifecycleWriterDirect, line["writer"],
		"a fallback invocation must record that the writer was the direct path")
	require.Equal(t, hostexit.FaultStageNotRecorded.String(), line["faultStage"])
}

// TestTheWriteLaneExpectedButUnusableSaysSoOnce is the diagnostic row: when
// something DOES stand at the socket path and cannot be used — a daemon that is
// starting, has crashed, or speaks another protocol — the operator is told once
// which path the hook took.
func TestTheWriteLaneExpectedButUnusableSaysSoOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	t.Setenv(hookWriteLaneEnv, string(WriteLaneAuto))
	// A regular file at the socket path: something is there, nothing serves it.
	socketPath := laneTestSocketPath(t)
	require.NoError(t, os.WriteFile(socketPath, []byte("not a socket"), 0o600))
	t.Setenv(hookWriteLaneSocketEnv, socketPath)

	cmd := lifecycleTestCommand(t, "claude-code", "anEventNoRegistrationDeclares", "2.1.261", dbPath)
	cmd.SetIn(strings.NewReader(`{}`))
	var diagnostics strings.Builder
	cmd.SetErr(&diagnostics)
	outcome := lifecycleTestOutcome(t, cmd, nil,
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)

	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"an unusable lane must never change the host outcome")
	require.Equal(t, 1, strings.Count(diagnostics.String(), "the lifecycle write lane is unavailable at"),
		"the operator must be told exactly once which path the hook took")
	require.Contains(t, diagnostics.String(), socketPath, "the diagnostic must name the socket it could not use")

	line := readLastFaultRecordLine(t)
	require.Equal(t, lifecycleWriterDirect, line["writer"],
		"an invocation that fell back must record that the writer was the direct path")
}

// TestTheWriteLaneAnswersAndRecordsTheDaemonWriter is the coexistence row: with
// a daemon serving, the hook's host bytes are exactly the direct path's, the
// record is committed by the daemon, and the fault record names writer=daemon.
func TestTheWriteLaneAnswersAndRecordsTheDaemonWriter(t *testing.T) {
	fixture := "session_start_2_1_261.json"
	raw := readProductionClaudeFixture(t, fixture, "SessionStart")

	// The direct path, on its own fresh store.
	directDB := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeWarmLaneTestDatabase(t, directDB)
	t.Setenv(hookWriteLaneEnv, string(WriteLaneDirect))
	directCmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", directDB)
	directCmd.SetIn(strings.NewReader(string(raw)))
	direct := lifecycleTestOutcome(t, directCmd, nil,
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)
	require.Empty(t, direct.Stderr, "exit 0 also describes fail-open; the direct receipt must actually succeed")

	// The served path, against a real in-process daemon lane on its own store.
	servedDB := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeWarmLaneTestDatabase(t, servedDB)
	socketPath := startHookTestWriteLane(t, servedDB)
	t.Setenv(hookWriteLaneEnv, string(WriteLaneAuto))
	t.Setenv(hookWriteLaneSocketEnv, socketPath)
	servedCmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", servedDB)
	servedCmd.SetIn(strings.NewReader(string(raw)))
	var servedDiagnostics strings.Builder
	servedCmd.SetErr(&servedDiagnostics)
	served := lifecycleTestOutcome(t, servedCmd, nil,
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)
	require.Empty(t, served.Stderr, "exit 0 also describes fail-open; the served receipt must actually succeed")

	require.Equal(t, direct.Exit, served.Exit,
		"the served path must produce the same host exit as the direct path for the same event")
	require.Equal(t, direct.Stdout, served.Stdout,
		"the served path must produce the same host continuation bytes as the direct path")
	assert.NotContains(t, servedDiagnostics.String(), "the lifecycle write lane is unavailable",
		"a healthy daemon must never trigger the fallback diagnostic")

	// The daemon committed the occurrence, on ITS store, and the hook's store
	// was not written directly.
	tracker, err := tasks.OpenTaskTracker(servedDB)
	require.NoError(t, err)
	defer func() { require.NoError(t, tracker.Close()) }()
	rows := queryLifecycleEvidence(t, tracker.Journal(), occurrenceEvidenceKind)
	require.Len(t, rows, 1, "the served path must commit exactly one occurrence through the daemon's lane")
}

// TestTheWriteLaneRefusalIsReportedWithTheDaemonStage proves the honest
// reporting half: a pre-admission refusal from the daemon is reported as
// not-recorded, with writer=daemon, and never as a fallback.
func TestTheWriteLaneRefusalIsReportedWithTheDaemonStage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	socketPath := startHookTestWriteLane(t, dbPath)
	t.Setenv(hookWriteLaneEnv, string(WriteLaneAuto))
	t.Setenv(hookWriteLaneSocketEnv, socketPath)

	cmd := lifecycleTestCommand(t, "claude-code", "anEventNoRegistrationDeclares", "2.1.261", dbPath)
	cmd.SetIn(strings.NewReader(`{}`))
	var diagnostics strings.Builder
	cmd.SetErr(&diagnostics)
	outcome := lifecycleTestOutcome(t, cmd, nil,
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)

	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"a pre-admission refusal is a fault, not a decision, so it must not block the session")
	assert.NotContains(t, diagnostics.String(), "the lifecycle write lane is unavailable",
		"the daemon answered, so this is not a fallback")

	line := readLastFaultRecordLine(t)
	require.Equal(t, lifecycleWriterDaemon, line["writer"],
		"an invocation the daemon answered must record that the writer was the daemon")
	require.Equal(t, hostexit.FaultStageNotRecorded.String(), line["faultStage"],
		"a refusal raised before admission is provably not-recorded")
}

// TestTheGuardRefusalOnTheDirectFallbackIsNotRecorded is the guard/lane
// interaction, hook side.
//
// The URI cutover added an open-path guard that can REFUSE a store before any
// schema work. When the daemon is not there (or never started, because the
// daemon's own open hit the same refusal), the hook writes directly and its own
// open meets the same guard. That refusal is raised BEFORE the first write
// attempt, so the fault it produces must say NOT-RECORDED — a refusal silently
// reported as record-unknown would tell the operator to go looking for an
// occurrence that the refusal's own Impact says cannot exist.
func TestTheGuardRefusalOnTheDirectFallbackIsNotRecorded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, dbPath)
	// A store recording a schema version above this build's ceiling is refused
	// by the SAME open-path guard the URI cutover added, with the same storage
	// category and the same "nothing was changed" impact.
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO audit_schema_meta (version, applied_at) VALUES (999, 0)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	t.Setenv(hookWriteLaneEnv, string(WriteLaneAuto))
	t.Setenv(hookWriteLaneSocketEnv, filepath.Join(t.TempDir(), "absent.sock"))

	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", dbPath)
	cmd.SetIn(strings.NewReader(`{}`))
	outcome := lifecycleTestOutcome(t, cmd, nil,
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)

	require.Equal(t, hostexit.ExitContinue, outcome.Exit,
		"a store refusal is a fault, not a decision, so the host continues under the fail-open default")
	require.Contains(t, outcome.Stderr, "written by a newer pasture (version 999)",
		"the guard's own refusal text must reach the operator unchanged")
	require.Contains(t, outcome.Stderr, "durable state not-recorded",
		"the fault diagnostic must state the not-recorded claim the refusal earns")

	line := readLastFaultRecordLine(t)
	require.Equal(t, hostexit.FaultStageNotRecorded.String(), line["faultStage"],
		"a refusal raised before the first write attempt is provably not-recorded; record-unknown would send the "+
			"operator looking for an occurrence the refusal itself says cannot exist")
	require.Equal(t, lifecycleWriterDirect, line["writer"],
		"the refusal was met on the direct fallback, so the record must say so")
}

// TestTheWriteLaneIsNeverAskedWhenDisabled pins the operator's direct-only
// choice: with the lane disabled, a healthy daemon holding the same socket is
// never contacted, so its store gains nothing and the hook's own store gains the
// occurrence.
func TestTheWriteLaneIsNeverAskedWhenDisabled(t *testing.T) {
	fixture := "session_start_2_1_261.json"
	raw := readProductionClaudeFixture(t, fixture, "SessionStart")

	daemonDB := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeLifecycleTestDatabase(t, daemonDB)
	socketPath := startHookTestWriteLane(t, daemonDB)

	hookDB := filepath.Join(t.TempDir(), tasks.DefaultDBFilename.String())
	initializeWarmLaneTestDatabase(t, hookDB)
	t.Setenv(hookWriteLaneEnv, string(WriteLaneDirect))
	t.Setenv(hookWriteLaneSocketEnv, socketPath)

	cmd := lifecycleTestCommand(t, "claude-code", "SessionStart", "2.1.261", hookDB)
	cmd.SetIn(strings.NewReader(string(raw)))
	outcome := lifecycleTestOutcome(t, cmd, nil,
		handlers.PassThroughCommitBarrier{}, timeouts.ProductionProfile(), context.WithTimeout)
	require.Equal(t, hostexit.ExitContinue, outcome.Exit)
	require.Empty(t, outcome.Stderr, "a fail-open timeout is not a successful direct write")

	daemonTracker, err := tasks.OpenTaskTracker(daemonDB)
	require.NoError(t, err)
	defer func() { require.NoError(t, daemonTracker.Close()) }()
	require.Empty(t, queryLifecycleEvidence(t, daemonTracker.Journal(), occurrenceEvidenceKind),
		"a disabled lane must never be contacted, so the daemon's store must gain nothing")

	hookTracker, err := tasks.OpenTaskTracker(hookDB)
	require.NoError(t, err)
	defer func() { require.NoError(t, hookTracker.Close()) }()
	require.Len(t, queryLifecycleEvidence(t, hookTracker.Journal(), occurrenceEvidenceKind), 1,
		"the direct path must have written the hook's own store")
}
