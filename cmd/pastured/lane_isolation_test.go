package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/config"
	"github.com/dayvidpham/pasture/internal/lifecycle/lanewire"
	"github.com/dayvidpham/pasture/internal/types"
)

// laneTestRuntimeDir returns the private runtime directory a test's child
// processes use as their XDG_RUNTIME_DIR, so lane sockets land in a temp dir
// instead of the operator's live runtime state. One helper owns the directory
// so startPastured, the lane acceptance row, and the isolation pin below all
// agree on it.
//
// The directory intentionally does NOT live under t.TempDir(): a Unix socket
// path is bounded by sun_path (about 108 bytes) and a test name alone can
// exceed that, so the runtime root is a short os.MkdirTemp directory — the
// same reason laneserve's own tests use shortSocketPath and the hook tests
// use laneTestSocketPath. It is still a temp dir, and t.Cleanup removes it.
func laneTestRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pasture-test-runtime-")
	if err != nil {
		t.Fatalf("create the test's private runtime directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// isolateLaneChildEnv redirects a child process's lane socket directory at the
// test's temp dir.
//
// WHY THIS EXISTS. lanewire.SocketPath derives the lane socket from the
// store path AND the ambient XDG_RUNTIME_DIR: when the variable is set — as
// it is in every operator session — the socket and its election lock live in
// $XDG_RUNTIME_DIR/pasture/lane-<digest>.sock[.lock]. A test that starts a
// REAL pastured with an inherited environment therefore hosts its lane in
// the operator's live runtime directory, and every run leaves a lock file
// behind (the lock is never removed; the socket survives any unclean stop).
// The digest is a hash of the test's temp store path, so each run adds a NEW
// file. Appending the override last wins over the inherited value
// (os/exec keeps the last duplicate key), and the daemon and every hook
// must share the SAME override so they still meet on one socket.
func isolateLaneChildEnv(t *testing.T, cmd *exec.Cmd, runtimeDir string) {
	t.Helper()
	cmd.Env = append(cmd.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
}

// awaitLogLineReturning reads log lines until one carries want and returns
// that line, failing the test if the daemon's output ends or the ceiling
// passes first. It mirrors awaitLogLine for callers that need the matched
// line itself (here: the socket path the daemon reports).
func awaitLogLineReturning(t *testing.T, lines <-chan string, want string, ceiling time.Duration) string {
	t.Helper()
	deadline := time.After(ceiling)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("pastured stopped producing output before it reported %q", want)
			}
			if strings.Contains(line, want) {
				return line
			}
		case <-deadline:
			t.Fatalf("pastured did not report %q within %v", want, ceiling)
		}
	}
}

// laneSocketFromListenLine extracts the socket= attribute from the daemon's
// "lifecycle write lane listening" log line (slog text form: space-separated
// key=value pairs).
func laneSocketFromListenLine(t *testing.T, line string) string {
	t.Helper()
	const key = "socket="
	start := strings.Index(line, key)
	if start < 0 {
		t.Fatalf("the daemon's listen line carries no socket path: %q", line)
	}
	rest := line[start+len(key):]
	if end := strings.Index(rest, " "); end >= 0 {
		rest = rest[:end]
	}
	if rest == "" {
		t.Fatalf("the daemon's listen line carries an empty socket path: %q", line)
	}
	return rest
}

// listLaneRuntimeEntries returns the names of the entries in dir, or an empty
// set when dir does not exist yet.
func listLaneRuntimeEntries(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}
		}
		t.Fatalf("list the runtime directory %s: %v", dir, err)
	}
	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = struct{}{}
	}
	return names
}

// snapshotRealLaneRoot records the operator's real lane directory and its
// current entries, when this test runs inside a session that has one. When
// XDG_RUNTIME_DIR is unset there is no session runtime dir to protect and the
// production fallback already sits beside the temp store; callers then get an
// empty root and skip the directory-growth half of the pin.
func snapshotRealLaneRoot(t *testing.T) (realRoot string, before map[string]struct{}) {
	t.Helper()
	if ambient := os.Getenv("XDG_RUNTIME_DIR"); ambient != "" {
		realRoot = filepath.Join(ambient, lanewire.SocketDirectoryName)
		before = listLaneRuntimeEntries(t, realRoot)
	}
	return realRoot, before
}

// assertNoNewRealLaneFiles fails the test for every entry the real lane
// directory gained since the snapshot.
func assertNoNewRealLaneFiles(t *testing.T, realRoot string, before map[string]struct{}) {
	t.Helper()
	if realRoot == "" {
		t.Log("XDG_RUNTIME_DIR is unset: no operator runtime dir exists, so only the temp-dir half applies")
		return
	}
	after := listLaneRuntimeEntries(t, realRoot)
	for name := range after {
		if _, ok := before[name]; !ok {
			t.Errorf("a test lane leaked %s into the operator's runtime directory %s; lane sockets belong under the test's temp dir",
				name, realRoot)
		}
	}
}

// TestLaneSocketsStayInsideTheTestRuntimeDir is the isolation pin for the
// runtime-directory leak: a test-started daemon must host its lane under the
// test's temp dir and must not add any file to the operator's real runtime
// directory.
//
// HOW IT GOES RED. Without the XDG_RUNTIME_DIR override in startPastured,
// the daemon's lanewire.SocketPath resolves into the ambient runtime dir, so
// (1) no lane-*.sock appears under the test's private runtime directory and
// (2) the real directory gains a lane-<digest>.sock and its .lock — both
// assertions below fail. With the override, the socket lands under the temp
// dir and the real directory is untouched.
func TestLaneSocketsStayInsideTheTestRuntimeDir(t *testing.T) {
	if testing.Short() {
		t.Skip("the isolation pin starts a real daemon; -short skips it")
	}
	tmp := t.TempDir()
	realRoot, before := snapshotRealLaneRoot(t)

	command, lines, runtimeDir := startPastured(t, tmp)

	// Tee the daemon's log so the pin can quote the exact socket path the
	// daemon reports, while awaitLogLine keeps its existing contract.
	teed := make(chan string, 64)
	var mu sync.Mutex
	var seen []string
	go func() {
		defer close(teed)
		for line := range lines {
			mu.Lock()
			seen = append(seen, line)
			mu.Unlock()
			teed <- line
		}
	}()
	listenLine := awaitLogLineReturning(t, teed, "lifecycle write lane listening", 90*time.Second)

	// The daemon logs the socket it bound (lane.go: "lifecycle write lane
	// listening" with a socket= attribute in slog text form). That exact
	// path must sit under the test's private runtime directory: a glob
	// alone could miss a mis-resolved path, and asserting on the reported
	// path keeps the failure message diagnostic.
	socket := laneSocketFromListenLine(t, listenLine)
	wantRoot := runtimeDir + string(os.PathSeparator)
	if !strings.HasPrefix(socket, wantRoot) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("the test daemon hosted its lane at %s, outside the test's private runtime directory %s; daemon log:\n%s",
			socket, runtimeDir, strings.Join(seen, "\n"))
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("the daemon reported its lane at %s but the socket is not there: %v", socket, err)
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal the test daemon: %v", err)
	}
	for range teed {
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("the test daemon exited with %v, want a clean exit 0 after SIGTERM", err)
	}

	assertNoNewRealLaneFiles(t, realRoot, before)
}

// TestInProcessLaneRuntimeStaysInsideTheTestRuntimeDir is the isolation pin
// for the in-process half of the leak: buildDaemonRuntime hosts a REAL lane
// in the test process itself, so a child-env override cannot reach it and the
// test must redirect XDG_RUNTIME_DIR instead.
//
// HOW IT GOES RED. Without the t.Setenv override, buildDaemonRuntime's
// lanewire.SocketPath resolves into the ambient runtime dir, so the hosted
// socket sits outside the test's private runtime directory and the real
// directory gains a lane-<digest>.sock and its .lock — both assertions below
// fail. (The lock is never removed and the socket survives until Close, so
// even a perfectly closed runtime leaves a trace without the override.)
func TestInProcessLaneRuntimeStaysInsideTheTestRuntimeDir(t *testing.T) {
	realRoot, before := snapshotRealLaneRoot(t)

	runtimeDir := laneTestRuntimeDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt, err := buildDaemonRuntime(context.Background(), config.PasturedConfig{
		AuditTrail:  types.BackendSqlite,
		AuditDBPath: filepath.Join(t.TempDir(), "pasture.db"),
	}, 2, logger)
	if err != nil {
		t.Fatalf("buildDaemonRuntime: %v", err)
	}
	defer func() {
		if err := rt.Close(logger); err != nil {
			t.Errorf("close the test runtime: %v", err)
		}
	}()

	if rt.Lane == nil {
		t.Fatal("the test runtime hosts no lane; the isolation assertion would pass vacuously")
	}
	wantRoot := runtimeDir + string(os.PathSeparator)
	if !strings.HasPrefix(rt.Lane.socket, wantRoot) {
		t.Fatalf("the in-process runtime hosted its lane at %s, outside the test's private runtime directory %s",
			rt.Lane.socket, runtimeDir)
	}
	if _, err := os.Stat(rt.Lane.socket); err != nil {
		t.Fatalf("the runtime reports its lane at %s but the socket is not there: %v", rt.Lane.socket, err)
	}

	assertNoNewRealLaneFiles(t, realRoot, before)
}
