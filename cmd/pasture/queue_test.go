package main_test

// queue_test.go exercises the work-queue commands through the compiled binary,
// which is the only place the whole path is visible: argument parsing, the
// printed line, and the exit code an operator's script will branch on.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/engine"
	"github.com/dayvidpham/pasture/internal/engine/enginetest"
)

// Exit codes an operator sees, from the mapping in internal/errors/errors.go
// (errors.ExitCode).
const (
	exitValidation = 1
	exitStorage    = 5
)

// TestCLI_QueueConcurrency_ShowAndChange walks the operator's whole path: read
// the limit, change it, and read it back in a separate process.
func TestCLI_QueueConcurrency_ShowAndChange(t *testing.T) {
	t.Parallel()
	db := enginetest.RegisteredQueuesDBPath(t)

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
	// The WHOLE of standard output is decoded, not a JSON object picked out of
	// it. That is the contract an operator's `| jq` depends on: with
	// --format json the command's stdout is one document and nothing else. A
	// lenient parse here would hide exactly the defect this asserts against —
	// the durable runtime printing its start-up notes into the answer.
	if err := json.Unmarshal([]byte(after.stdout), &decoded); err != nil {
		t.Fatalf("stdout of get --format json is not one JSON document (%v). "+
			"Anything printed beside the answer breaks a pipe into a JSON reader; "+
			"the durable client must log to standard error. stdout was:\n%s",
			err, after.stdout)
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
	db := enginetest.RegisteredQueuesDBPath(t)

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
			wantText: "one epoch at a time in each process",
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

// TestCLI_QueueConcurrency_ControlQueueIsReadOnly pins the whole shape of the
// control queue on the command line: it can be read, it cannot be changed, and
// the help text says so, so an operator learns the rule before they meet the
// refusal.
func TestCLI_QueueConcurrency_ControlQueueIsReadOnly(t *testing.T) {
	t.Parallel()
	db := enginetest.RegisteredQueuesDBPath(t)

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
