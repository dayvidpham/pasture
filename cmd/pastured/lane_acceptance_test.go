package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/provenance"
	_ "modernc.org/sqlite"

	"github.com/dayvidpham/pasture/internal/lifecycle/lanewire"
	"github.com/dayvidpham/pasture/internal/tasks"
)

// buildPastureHookBinary builds the production pasture command, which is the
// hook the hosts invoke. It is built CGO-free like the daemon so the acceptance
// row measures the shipped shape.
func buildPastureHookBinary(t *testing.T, binary string) {
	t.Helper()
	build := exec.Command("go", "build", "-o", binary, "./cmd/pasture")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build pasture: %v\n%s", err, output)
	}
}

// TestEighteenConcurrentHooksThroughTheDaemonLaneIsTheAcceptanceRow is the
// built-binary acceptance row for PROPOSAL-19.
//
// WHAT IT MEASURES. A real pastured hosts the lane on a sandbox store; eighteen
// concurrent hook processes fire the same session-start event, and a SERIAL
// probe fires the same event repeatedly. The design's recorded direct-path
// baseline at concurrency 18 is 12 faults in 15 probe runs (80%), p95 2,003 ms,
// about 7.5 committed events/s. The acceptance target is faults/s = 0 and probe
// p95 <= 250 ms.
//
// WHAT IT DOES NOT CLAIM. It measures ONE machine at ONE concurrency for a few
// seconds; it is not a scaling study, and it is not a substitute for the
// recorded sandbox harness. The numbers it prints are what it observed.
func TestEighteenConcurrentHooksThroughTheDaemonLaneIsTheAcceptanceRow(t *testing.T) {
	if testing.Short() {
		t.Skip("the built-binary acceptance row builds two binaries and starts a daemon; -short skips it")
	}
	tmp := t.TempDir()
	daemonBinary := filepath.Join(tmp, "pastured")
	buildPasturedBinary(t, daemonBinary, "")
	hookBinary := filepath.Join(tmp, "pasture")
	buildPastureHookBinary(t, hookBinary)

	dbPath := filepath.Join(tmp, "pasture.db")
	// A store must be initialised before any hook can be recorded against it:
	// the persisted system identity is written by a normal Pasture command, and
	// the served receipt resolves it read-only. This is the same prerequisite the
	// direct path has always had; the test satisfies it the way an operator does.
	bootstrapStore(t, dbPath)

	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte("audit_trail: sqlite\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	fixturePath := filepath.Join("..", "..", "internal", "lifecycle", "ingress", "claude", "testdata", "fixtures", "session_start_2_1_261.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read the session-start fixture: %v", err)
	}

	daemon := exec.Command(daemonBinary, "--config", configPath, "--db", dbPath)
	daemon.Env = append(daemon.Environ(),
		"PASTURE_WRITE_LANE=auto",
	)
	stderr, err := daemon.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatalf("start pastured: %v", err)
	}
	defer func() {
		_ = daemon.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = daemon.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = daemon.Process.Kill()
			<-done
		}
	}()

	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	awaitLogLine(t, lines, "lifecycle write lane listening", 90*time.Second)
	go func() {
		for range lines {
		}
	}()

	faultRecord := filepath.Join(filepath.Dir(dbPath), "lifecycle-faults.jsonl")
	baselineFaults := countFaultLines(t, faultRecord)

	run := func() (time.Duration, error) {
		started := time.Now()
		cmd := exec.Command(hookBinary, "hook", "lifecycle",
			"--harness", "claude-code", "--event", "SessionStart", "--host-version", "2.1.261",
			"--db", dbPath)
		cmd.Env = append(cmd.Environ(), "PASTURE_WRITE_LANE=auto")
		cmd.Stdin = bytes.NewReader(fixture)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		if runErr != nil {
			return time.Since(started), fmt.Errorf("%w: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return time.Since(started), nil
	}

	const workers = 18
	windowStarted := time.Now()
	var wg sync.WaitGroup
	workerErr := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := run(); err != nil {
				workerErr <- err
			}
		}()
	}

	// The serial probe: one invocation at a time, measuring the latency the
	// host pays while the firehose runs.
	probeLatencies := []time.Duration{}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		elapsed, err := run()
		if err != nil {
			workerErr <- err
			break
		}
		probeLatencies = append(probeLatencies, elapsed)
	}
	wg.Wait()
	close(workerErr)
	for err := range workerErr {
		t.Errorf("a hook invocation failed against a healthy daemon lane: %v", err)
	}

	window := time.Since(windowStarted)
	appended := countFaultLines(t, faultRecord) - baselineFaults
	if appended != 0 {
		t.Errorf("the daemon lane appended %d fault lines while it was healthy; the acceptance target is zero faults", appended)
	}
	if len(probeLatencies) == 0 {
		t.Fatal("the serial probe ran no invocations")
	}
	sort.Slice(probeLatencies, func(i, j int) bool { return probeLatencies[i] < probeLatencies[j] })
	p95 := probeLatencies[(len(probeLatencies)*95)/100]
	faultsPerSecond := float64(appended) / window.Seconds()
	t.Logf("acceptance row: workers=%d window=%s probeRuns=%d probeP95=%s faultsPerSecond=%.3f appendedFaults=%d",
		workers, window.Round(time.Millisecond), len(probeLatencies), p95, faultsPerSecond, appended)
	if p95 > 250*time.Millisecond {
		t.Errorf("serial probe p95 was %s, above the 250 ms acceptance target", p95)
	}
	if faultsPerSecond != 0 {
		t.Errorf("faults/second was %.3f, above the zero-fault acceptance target", faultsPerSecond)
	}
}

// bootstrapStore initialises a fresh store exactly as the first ordinary
// Pasture command does, so the persisted system identity exists.
func bootstrapStore(t *testing.T, dbPath string) {
	t.Helper()
	tracker, err := tasks.OpenTaskTracker(dbPath)
	if err != nil {
		t.Fatalf("bootstrap the acceptance store: %v", err)
	}
	defer func() {
		if err := tracker.Close(); err != nil {
			t.Fatalf("close the acceptance store: %v", err)
		}
	}()
	if _, err := tracker.Create("file://lane-acceptance", "bootstrap", "initialise the persisted ingress identity",
		provenance.TaskTypeTask, provenance.PriorityMedium, provenance.PhaseUnscoped); err != nil {
		t.Fatalf("bootstrap the acceptance store: %v", err)
	}
}

// TestTheDaemonRefusesARefusedStoreAndHostsNoLane is the guard/lane
// interaction, daemon side.
//
// The URI cutover's open-path guard can refuse a store before any schema work.
// The daemon opens its ONE long-lived handle at startup, so a refused store
// makes the daemon exit with the guard's own storage category and text, and the
// lane is never hosted — there is no socket for a hook to reach, which is why
// the hook's fallback meets the SAME refusal on its own open and reports it
// not-recorded (proved in cmd/pasture by
// TestTheGuardRefusalOnTheDirectFallbackIsNotRecorded). A refusal must never be
// reachable as a LANE error, because a lane error reads as record-unknown.
func TestTheDaemonRefusesARefusedStoreAndHostsNoLane(t *testing.T) {
	if testing.Short() {
		t.Skip("this row builds the daemon and starts it; -short skips it")
	}
	tmp := t.TempDir()
	daemonBinary := filepath.Join(tmp, "pastured")
	buildPasturedBinary(t, daemonBinary, "")

	dbPath := filepath.Join(tmp, "pasture.db")
	bootstrapStore(t, dbPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open the acceptance store: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO audit_schema_meta (version, applied_at) VALUES (999, 0)`); err != nil {
		t.Fatalf("raise the recorded schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close the acceptance store: %v", err)
	}

	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte("audit_trail: sqlite\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	command := exec.Command(daemonBinary, "--config", configPath, "--db", dbPath)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	runErr := command.Run()

	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	if exitCode != 5 {
		t.Fatalf("the daemon exited %d on a store the guard refuses; the refusal is a storage-category error and "+
			"must carry exit 5, with this output:\n%s", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "written by a newer pasture (version 999)") {
		t.Fatalf("the guard's own refusal text did not reach the operator:\n%s", stderr.String())
	}
	if _, statErr := os.Lstat(lanewire.SocketPath(dbPath)); statErr == nil {
		t.Fatalf("a lane socket exists at %s although the daemon refused the store; a hook would then reach a lane "+
			"that cannot commit anything", lanewire.SocketPath(dbPath))
	}
}

// countFaultLines returns the number of lines in the lifecycle fault record, or
// zero when the record does not exist yet.
func countFaultLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read the fault record %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}
