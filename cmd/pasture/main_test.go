// Package main_test contains end-to-end CLI smoke tests for the `pasture`
// binary. Tests compile pasture once via `go build` in TestMain and exercise
// it through subprocess calls.
//
// Subprocess execution is the right model here because the production code
// path uses os.Exit (via exitWithCode) for non-zero exit codes; running the
// binary in-process would terminate the test runner.
package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/testutil"
)

// binaryPath holds the compiled pasture binary, built once for the test run.
var binaryPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "pasture-cli-smoke-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pasture cli smoke: could not create temp dir: %v\n", err)
		os.Exit(1)
	}
	binaryName := "pasture"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath = filepath.Join(tmpDir, binaryName)

	buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/pasture")
	buildCmd.Dir = moduleRoot()
	var buildOut bytes.Buffer
	buildCmd.Stderr = &buildOut
	buildCmd.Stdout = &buildOut
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr,
			"pasture cli smoke: go build failed — cannot run smoke tests.\n"+
				"  binary: %s\n"+
				"  error:  %v\n"+
				"  output: %s\n",
			binaryPath, err, buildOut.String(),
		)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}

// moduleRoot walks upward from cwd until it finds go.mod. The pasture cmd
// directory is two levels below the module root.
func moduleRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("os.Getwd: %v", err))
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic(fmt.Sprintf("moduleRoot: could not find go.mod from %s", wd))
		}
		dir = parent
	}
}

// runOutcome captures the result of a single CLI invocation.
type runOutcome struct {
	stdout   string
	stderr   string
	exitCode int
}

// runCLI executes the compiled binary with the given args and returns
// stdout, stderr, and the process exit code. Unexpected execution errors
// (e.g. binary not found) fail the test.
func runCLI(t *testing.T, args ...string) runOutcome {
	t.Helper()
	// #nosec G204 — binaryPath comes from TestMain's controlled go build.
	cmd := exec.Command(binaryPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("unexpected exec error: %v\nstdout: %s\nstderr: %s",
				err, stdout.String(), stderr.String())
		}
	}
	return runOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

func newDB(t *testing.T) string {
	t.Helper()
	return testutil.GoldenUnifiedDBPath(t)
}

func absentDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "pasture.db")
}

func TestCLI_TaskCreateAndShow_JSON(t *testing.T) {
	db := newDB(t)

	out := runCLI(t,
		"--db", db,
		"--namespace", "demo",
		"--format", "json",
		"task", "create", "Hello world",
		"--type=feature",
		"--priority=high",
		"--phase=request",
	)
	if out.exitCode != 0 {
		t.Fatalf("create exit %d; stdout=%s stderr=%s", out.exitCode, out.stdout, out.stderr)
	}
	var created struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Priority string `json:"priority"`
		Type     string `json:"type"`
		Phase    string `json:"phase"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &created); err != nil {
		t.Fatalf("decode create json: %v\nbody: %s", err, out.stdout)
	}
	if created.Priority != "high" || created.Type != "feature" || created.Phase != "request" {
		t.Errorf("flag wiring: %+v", created)
	}

	showOut := runCLI(t, "--db", db, "--format", "json", "task", "show", created.ID)
	if showOut.exitCode != 0 {
		t.Fatalf("show exit %d; stderr=%s", showOut.exitCode, showOut.stderr)
	}
	var shown struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(showOut.stdout), &shown); err != nil {
		t.Fatalf("decode show json: %v\nbody: %s", err, showOut.stdout)
	}
	if shown.ID != created.ID || shown.Title != "Hello world" {
		t.Errorf("show mismatch: %+v vs %+v", shown, created)
	}
}

func TestCLI_PriorityP3Form(t *testing.T) {
	db := newDB(t)

	out := runCLI(t,
		"--db", db,
		"--namespace", "demo",
		"--format", "json",
		"task", "create", "P3 form",
		"--priority=P3",
	)
	if out.exitCode != 0 {
		t.Fatalf("exit %d stderr=%s", out.exitCode, out.stderr)
	}
	var got struct {
		Priority string `json:"priority"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &got); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, out.stdout)
	}
	if got.Priority != "low" {
		t.Errorf("expected priority 'low' from P3, got %q", got.Priority)
	}
}

func TestCLI_PriorityNumericForm(t *testing.T) {
	db := newDB(t)

	out := runCLI(t,
		"--db", db,
		"--namespace", "demo",
		"--format", "json",
		"task", "create", "numeric form",
		"--priority=0",
	)
	if out.exitCode != 0 {
		t.Fatalf("exit %d stderr=%s", out.exitCode, out.stderr)
	}
	var got struct {
		Priority string `json:"priority"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &got); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, out.stdout)
	}
	if got.Priority != "critical" {
		t.Errorf("expected priority 'critical' from 0, got %q", got.Priority)
	}
}

func TestCLI_DepAddAndReady(t *testing.T) {
	db := newDB(t)

	mk := func(title string) string {
		t.Helper()
		out := runCLI(t,
			"--db", db,
			"--namespace", "demo",
			"--format", "json",
			"task", "create", title,
		)
		if out.exitCode != 0 {
			t.Fatalf("create %q exit %d stderr=%s", title, out.exitCode, out.stderr)
		}
		var got struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out.stdout), &got); err != nil {
			t.Fatalf("decode %q: %v", title, err)
		}
		return got.ID
	}
	parent := mk("parent")
	child := mk("child")

	depOut := runCLI(t,
		"--db", db,
		"--format", "json",
		"task", "dep", "add", parent, "--blocked-by", child,
	)
	if depOut.exitCode != 0 {
		t.Fatalf("dep add exit %d stderr=%s", depOut.exitCode, depOut.stderr)
	}
	var edge struct {
		SourceId string `json:"sourceId"`
		TargetId string `json:"targetId"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(depOut.stdout), &edge); err != nil {
		t.Fatalf("decode edge: %v\nbody: %s", err, depOut.stdout)
	}
	if edge.SourceId != parent || edge.TargetId != child || edge.Kind != "blocked_by" {
		t.Errorf("edge: %+v", edge)
	}

	readyOut := runCLI(t, "--db", db, "--format", "json", "task", "ready")
	if readyOut.exitCode != 0 {
		t.Fatalf("ready exit %d stderr=%s", readyOut.exitCode, readyOut.stderr)
	}
	var ready []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(readyOut.stdout), &ready); err != nil {
		t.Fatalf("decode ready: %v\nbody: %s", err, readyOut.stdout)
	}
	foundChild, foundParent := false, false
	for _, r := range ready {
		if r.ID == child {
			foundChild = true
		}
		if r.ID == parent {
			foundParent = true
		}
	}
	if !foundChild {
		t.Errorf("child %s should be ready, got %+v", child, ready)
	}
	if foundParent {
		t.Errorf("parent %s should be blocked, got %+v", parent, ready)
	}
}

func TestCLI_LabelAddRemove(t *testing.T) {
	db := newDB(t)

	out := runCLI(t,
		"--db", db, "--namespace", "demo", "--format", "json",
		"task", "create", "labelable",
	)
	if out.exitCode != 0 {
		t.Fatalf("create exit %d stderr=%s", out.exitCode, out.stderr)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	addOut := runCLI(t, "--db", db, "--format", "json", "task", "label", "add", created.ID, "important")
	if addOut.exitCode != 0 {
		t.Fatalf("label add exit %d stderr=%s", addOut.exitCode, addOut.stderr)
	}
	var added struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(addOut.stdout), &added); err != nil {
		t.Fatalf("decode add: %v\nbody: %s", err, addOut.stdout)
	}
	if !contains(added.Labels, "important") {
		t.Errorf("expected 'important' in labels, got %+v", added.Labels)
	}

	rmOut := runCLI(t, "--db", db, "--format", "json", "task", "label", "remove", created.ID, "important")
	if rmOut.exitCode != 0 {
		t.Fatalf("label remove exit %d stderr=%s", rmOut.exitCode, rmOut.stderr)
	}
	var removed struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(rmOut.stdout), &removed); err != nil {
		t.Fatalf("decode remove: %v\nbody: %s", err, rmOut.stdout)
	}
	if contains(removed.Labels, "important") {
		t.Errorf("'important' should be gone, got %+v", removed.Labels)
	}
}

func TestCLI_InvalidPriorityRejected(t *testing.T) {
	db := newDB(t)

	out := runCLI(t,
		"--db", db,
		"--namespace", "demo",
		"task", "create", "bad",
		"--priority=urgent",
	)
	if out.exitCode == 0 {
		t.Fatalf("expected non-zero exit for invalid priority, stdout=%s", out.stdout)
	}
	combined := out.stdout + out.stderr
	if !strings.Contains(combined, "invalid --priority") {
		t.Errorf("expected --priority validation message, got: %s", combined)
	}
}

func TestCLI_CommentAddRequiresAuthor(t *testing.T) {
	db := newDB(t)

	// First create a task so we have a valid ID — the failure should be on
	// missing --author, not a non-existent task.
	out := runCLI(t, "--db", db, "--namespace", "demo", "--format", "json", "task", "create", "x")
	if out.exitCode != 0 {
		t.Fatalf("setup create exit %d", out.exitCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out.stdout), &created); err != nil {
		t.Fatalf("decode setup: %v", err)
	}

	commentOut := runCLI(t, "--db", db, "task", "comment", "add", created.ID, "hi")
	if commentOut.exitCode == 0 {
		t.Fatalf("expected non-zero exit when --author is missing")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
