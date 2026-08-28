package main_test

// queue_test.go exercises the work-queue commands through the compiled binary,
// which is the only place the whole path is visible: argument parsing, the
// printed line, and the exit code an operator's script will branch on.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/engine"
)

// Exit codes an operator sees, from the mapping in internal/errors/errors.go
// (errors.ExitCode).
const (
	exitValidation = 1
	exitStorage    = 5
)

// queueFixture builds ONE database in the state a single daemon start leaves
// behind — the queues registered, with their configured limits — and every test
// here takes a private copy of it.
//
// It is built once per test binary on purpose. Registering the queues means
// building and stopping a whole durable engine, and doing that in each of
// several parallel tests loaded the machine enough to starve a slice test
// elsewhere in the tree that waits a fixed two seconds for a signal.
var queueFixture = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "pasture-cli-queue-fixture-*")
	if err != nil {
		return "", fmt.Errorf("create the queue fixture directory: %w", err)
	}
	queueFixtureDir = dir
	dbPath := filepath.Join(dir, "pasture.db")
	e, err := engine.New(context.Background(), engine.Config{
		DBPath:             dbPath,
		ApplicationVersion: "test-queue-cli",
		ExecutorID:         "test-queue-cli",
	})
	if err != nil {
		return "", fmt.Errorf("build the engine that registers the queues: %w", err)
	}
	// Shutdown closes the handle, so the file on disk is complete and can be
	// copied.
	e.Shutdown(10 * time.Second)
	return dbPath, nil
})

// queueFixtureDir is the directory queueFixture built, kept so it can be deleted
// when every test has finished. It is written inside queueFixture and read only
// after the run.
var queueFixtureDir string

// removeQueueFixture deletes the shared fixture. TestMain calls it after the
// tests finish; a t.Cleanup could not, because the fixture outlives the test
// that happened to build it.
func removeQueueFixture() {
	if queueFixtureDir != "" {
		_ = os.RemoveAll(queueFixtureDir)
	}
}

// dbWithRegisteredQueues returns a private copy of the fixture, which this test
// alone may change.
func dbWithRegisteredQueues(t *testing.T) string {
	t.Helper()
	src, err := queueFixture()
	if err != nil {
		t.Fatalf("queue fixture: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "pasture.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, readErr := os.ReadFile(src + suffix)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue // a cleanly closed database has no write-ahead files
			}
			t.Fatalf("read the queue fixture %q: %v", src+suffix, readErr)
		}
		if writeErr := os.WriteFile(dst+suffix, data, 0o600); writeErr != nil {
			t.Fatalf("write the queue fixture copy %q: %v", dst+suffix, writeErr)
		}
	}
	return dst
}

// TestCLI_QueueConcurrency_ShowAndChange walks the operator's whole path: read
// the limit, change it, and read it back in a separate process.
func TestCLI_QueueConcurrency_ShowAndChange(t *testing.T) {
	t.Parallel()
	db := dbWithRegisteredQueues(t)

	out := runCLI(t, "--db", db, "queue", "concurrency", "get", "slice")
	if out.exitCode != 0 {
		t.Fatalf("get exit %d; stdout=%s stderr=%s", out.exitCode, out.stdout, out.stderr)
	}
	if !strings.Contains(out.stdout, engine.SliceQueueName) {
		t.Errorf("get output does not name the queue: %s", out.stdout)
	}

	set := runCLI(t, "--db", db, "queue", "concurrency", "set", "slice", "3")
	if set.exitCode != 0 {
		t.Fatalf("set exit %d; stdout=%s stderr=%s", set.exitCode, set.stdout, set.stderr)
	}
	if !strings.Contains(set.stdout, "3 concurrent jobs per process") {
		t.Errorf("set does not report the limit now in force: %s", set.stdout)
	}

	// A separate process sees the change, which is the point of storing it in
	// the shared database.
	after := runCLI(t, "--db", db, "--format", "json", "queue", "concurrency", "get", "slice")
	if after.exitCode != 0 {
		t.Fatalf("get after set exit %d; stderr=%s", after.exitCode, after.stderr)
	}
	var decoded struct {
		Queue             string `json:"queue"`
		WorkerConcurrency *int   `json:"worker_concurrency"`
	}
	if err := json.Unmarshal([]byte(jsonObjectIn(t, after.stdout)), &decoded); err != nil {
		t.Fatalf("get --format json did not produce a JSON object (%v): %s", err, after.stdout)
	}
	if decoded.Queue != engine.SliceQueueName {
		t.Errorf("queue = %q, want %q", decoded.Queue, engine.SliceQueueName)
	}
	if decoded.WorkerConcurrency == nil || *decoded.WorkerConcurrency != 3 {
		t.Errorf("worker_concurrency = %v, want 3", decoded.WorkerConcurrency)
	}

	// The stored name is accepted wherever the short name is.
	stored := runCLI(t, "--db", db, "queue", "concurrency", "get", engine.SliceQueueName)
	if stored.exitCode != 0 {
		t.Errorf("get by stored name exit %d; stderr=%s", stored.exitCode, stored.stderr)
	}
}

// TestCLI_QueueConcurrency_RejectsBadArguments pins the exit code and the
// message for each way an operator can get the command wrong.
func TestCLI_QueueConcurrency_RejectsBadArguments(t *testing.T) {
	t.Parallel()
	db := dbWithRegisteredQueues(t)

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantText string
	}{
		{
			name:     "unknown queue",
			args:     []string{"queue", "concurrency", "get", "slices"},
			wantCode: exitValidation,
			wantText: engine.SliceQueueName,
		},
		{
			name:     "jobs is not a number",
			args:     []string{"queue", "concurrency", "set", "slice", "many"},
			wantCode: exitValidation,
			wantText: "not a number of jobs",
		},
		{
			name:     "set on the control queue",
			args:     []string{"queue", "concurrency", "set", "control", "4"},
			wantCode: exitValidation,
			wantText: "one epoch control workflow at a time",
		},
		{
			name:     "jobs is zero",
			args:     []string{"queue", "concurrency", "set", "slice", "0"},
			wantCode: exitValidation,
			wantText: "not a usable number",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := runCLI(t, append([]string{"--db", db}, tc.args...)...)
			if out.exitCode != tc.wantCode {
				t.Fatalf("exit %d, want %d; stdout=%s stderr=%s", out.exitCode, tc.wantCode, out.stdout, out.stderr)
			}
			combined := out.stdout + out.stderr
			if !strings.Contains(combined, tc.wantText) {
				t.Errorf("output does not contain %q:\n%s", tc.wantText, combined)
			}
		})
	}
}

// TestCLI_QueueConcurrency_ReportsADatabaseWithNoQueues pins what an operator
// meets when they run the command before any daemon has started: a storage exit
// code and a message that tells them to start the daemon.
func TestCLI_QueueConcurrency_ReportsADatabaseWithNoQueues(t *testing.T) {
	t.Parallel()
	db := absentDB(t)

	out := runCLI(t, "--db", db, "queue", "concurrency", "get", "slice")
	if out.exitCode != exitStorage {
		t.Fatalf("exit %d, want %d; stdout=%s stderr=%s", out.exitCode, exitStorage, out.stdout, out.stderr)
	}
	combined := out.stdout + out.stderr
	// Assert on wording unique to the never-registered case. The generic
	// read-failure message also mentions the daemon, so a looser check would
	// pass even when the two failures are confused with each other.
	for _, want := range []string{"is not in this pasture database", "Start the daemon"} {
		if !strings.Contains(combined, want) {
			t.Errorf("output does not contain %q, so it does not explain that no daemon has run:\n%s", want, combined)
		}
	}
}

// jsonObjectIn extracts the JSON object from a command's standard output.
//
// It is needed because the durable runtime writes its start-up log lines to
// STANDARD OUTPUT when it is given no logger of its own, so they land in the
// middle of a command's machine-readable output and a pipe into a JSON reader
// fails. That is a defect in how the shared client is built, not in this
// command, and it affects every command that opens one. It is reported at
// https://github.com/dayvidpham/pasture/issues/104. When the client is given a
// logger, this helper keeps working unchanged.
func jsonObjectIn(t *testing.T, stdout string) string {
	t.Helper()
	start := strings.Index(stdout, "{")
	end := strings.LastIndex(stdout, "}")
	if start < 0 || end < start {
		t.Fatalf("no JSON object in output:\n%s", stdout)
	}
	return stdout[start : end+1]
}

// TestCLI_QueueConcurrency_ControlQueueIsReadOnly pins the whole shape of the
// control queue on the command line: it can be read, it cannot be changed, and
// the help text says so, so an operator learns the rule before they meet the
// refusal.
func TestCLI_QueueConcurrency_ControlQueueIsReadOnly(t *testing.T) {
	t.Parallel()
	db := dbWithRegisteredQueues(t)

	get := runCLI(t, "--db", db, "queue", "concurrency", "get", "control")
	if get.exitCode != 0 {
		t.Fatalf("get control exit %d; stderr=%s", get.exitCode, get.stderr)
	}
	if !strings.Contains(get.stdout, "1 concurrent jobs per process") {
		t.Errorf("get control does not report the fixed limit: %s", get.stdout)
	}

	help := runCLI(t, "queue", "concurrency", "set", "--help")
	if help.exitCode != 0 {
		t.Fatalf("set help exit %d; stderr=%s", help.exitCode, help.stderr)
	}
	for _, want := range []string{"Only the slice queue can be changed", "read only, its concurrency is fixed"} {
		if !strings.Contains(help.stdout, want) {
			t.Errorf("the help text does not contain %q:\n%s", want, help.stdout)
		}
	}
}
