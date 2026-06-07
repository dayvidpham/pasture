package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayvidpham/pasture/internal/acp"
	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

// recordingHandler implements acp.SessionHandler and records all calls.
type recordingHandler struct {
	mu      sync.Mutex
	updates []acp.SessionUpdate
	ends    []sessionEndCall
	errOnN  int // if > 0, return error on the Nth HandleUpdate call (1-indexed)
	callN   int
}

type sessionEndCall struct {
	sessionId string
	reason    acp.StopReason
}

func (h *recordingHandler) HandleUpdate(_ context.Context, update acp.SessionUpdate) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callN++
	if h.errOnN > 0 && h.callN >= h.errOnN {
		return fmt.Errorf("injected error on call %d", h.callN)
	}
	h.updates = append(h.updates, update)
	return nil
}

func (h *recordingHandler) HandleSessionEnd(_ context.Context, sessionId string, reason acp.StopReason) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ends = append(h.ends, sessionEndCall{sessionId: sessionId, reason: reason})
	return nil
}

func (h *recordingHandler) UpdateCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.updates)
}

func (h *recordingHandler) EndCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.ends)
}

// buildFakeAgent compiles a fake agent binary that writes the provided JSON-RPC
// lines to stdout and exits. Returns the path to the compiled binary.
//
// The fake agent source is built via go build at test time. It writes each line
// supplied as a command-line argument to stdout, then exits.
func buildFakeAgent(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "main.go")
	binPath := filepath.Join(tmpDir, "fake-agent")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	const src = `package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	for _, line := range os.Args[1:] {
		// Unescape \n back to actual newlines (args can't contain real newlines).
		actual := strings.ReplaceAll(line, "\\n", "\n")
		fmt.Print(actual)
		// Small delay so the reader sees lines arrive over time.
		time.Sleep(1 * time.Millisecond)
	}
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0600); err != nil {
		t.Fatalf("buildFakeAgent: write src: %v", err)
	}

	//nolint:gosec // test helper only
	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("buildFakeAgent: go build: %v\n%s", err, out)
	}

	t.Cleanup(func() { _ = os.Remove(binPath) })
	return binPath
}

// makeSessionUpdateLine creates a newline-terminated JSON-RPC session/update line.
func makeSessionUpdateLine(t *testing.T, update acp.SessionUpdate) string {
	t.Helper()
	params, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("makeSessionUpdateLine: marshal update: %v", err)
	}
	msg := struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  params,
	}
	line, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("makeSessionUpdateLine: marshal msg: %v", err)
	}
	return string(line) + "\n"
}

// ─── NewClient tests ──────────────────────────────────────────────────────────

func TestNewClient_PanicsOnNilHandler(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic for nil handler, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "handler must not be nil") {
			t.Errorf("unexpected panic message: %q", msg)
		}
	}()
	_ = acp.NewClient(nil)
}

func TestNewClient_ReturnsNonNil(t *testing.T) {
	h := &recordingHandler{}
	c := acp.NewClient(h)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestNewClient_InitialSessionCountIsZero(t *testing.T) {
	h := &recordingHandler{}
	c := acp.NewClient(h)
	if got := c.SessionCount(); got != 0 {
		t.Errorf("initial SessionCount = %d, want 0", got)
	}
}

// ─── SessionStats before any connection ──────────────────────────────────────

func TestSessionStats_UnknownSessionReturnsError(t *testing.T) {
	h := &recordingHandler{}
	c := acp.NewClient(h)

	_, err := c.SessionStats("nonexistent-session")
	if err == nil {
		t.Fatal("expected error for unknown session, got nil")
	}

	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("expected *pasterrors.StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("error category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}
}

// ─── Connect tests (using fake agent binary) ──────────────────────────────────

func TestConnect_DoubleConnectReturnsError(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	// Start a long-running fake agent in background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The agent writes nothing and just hangs until stdout is closed.
	// We simulate this by running a process that sleeps, but we cancel immediately.
	done := make(chan error, 1)
	go func() {
		done <- c.Connect(ctx, binPath)
	}()

	// Give Connect time to start the process.
	time.Sleep(50 * time.Millisecond)

	// Attempt a second Connect on the same client.
	err := c.Connect(ctx, binPath)
	if err == nil {
		t.Error("expected error on double Connect, got nil")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("expected *pasterrors.StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryValidation {
		t.Errorf("error category = %q, want %q", se.Category, pasterrors.CategoryValidation)
	}

	cancel()
	<-done
}

func TestConnect_NoSuchBinaryReturnsConnectionError(t *testing.T) {
	h := &recordingHandler{}
	c := acp.NewClient(h)

	err := c.Connect(context.Background(), "/no/such/binary/fake-agent-xyz")
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	var se *pasterrors.StructuredError
	if !errors.As(err, &se) {
		t.Fatalf("expected *pasterrors.StructuredError, got %T: %v", err, err)
	}
	if se.Category != pasterrors.CategoryConnection {
		t.Errorf("error category = %q, want %q", se.Category, pasterrors.CategoryConnection)
	}
}

func TestConnect_ContextCancelledStopsCleanly(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		// Agent writes nothing; Connect blocks until ctx is cancelled.
		done <- c.Connect(ctx, binPath)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Connect returned error after context cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Connect did not return after context cancellation")
	}
}

// ─── Session lifecycle tests ──────────────────────────────────────────────────

func TestConnect_SingleSessionUpdate(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	update := acp.SessionUpdate{
		SessionId: "sess-1",
		Role:      "assistant",
		Content:   []acp.ContentBlock{{Type: "text", Text: "Hello!"}},
	}
	line := makeSessionUpdateLine(t, update)

	if err := c.Connect(context.Background(), binPath, line); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}

	if got := h.UpdateCount(); got != 1 {
		t.Errorf("UpdateCount = %d, want 1", got)
	}
	if got := c.SessionCount(); got != 1 {
		t.Errorf("SessionCount = %d, want 1", got)
	}

	stats, err := c.SessionStats("sess-1")
	if err != nil {
		t.Fatalf("SessionStats: %v", err)
	}
	if stats.UpdateCount != 1 {
		t.Errorf("stats.UpdateCount = %d, want 1", stats.UpdateCount)
	}
	if stats.Ended {
		t.Error("session should not be marked as ended (no stop_reason)")
	}
}

func TestConnect_SessionEndRecorded(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	// Regular update followed by a terminal update.
	update1 := acp.SessionUpdate{
		SessionId: "sess-end-1",
		Role:      "assistant",
		Content:   []acp.ContentBlock{{Type: "text", Text: "Processing..."}},
	}
	update2 := acp.SessionUpdate{
		SessionId:  "sess-end-1",
		Role:       "assistant",
		StopReason: acp.StopReasonEndTurn,
	}

	line1 := makeSessionUpdateLine(t, update1)
	line2 := makeSessionUpdateLine(t, update2)

	if err := c.Connect(context.Background(), binPath, line1, line2); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}

	// HandleSessionEnd should have been called once.
	if got := h.EndCount(); got != 1 {
		t.Errorf("EndCount = %d, want 1", got)
	}

	stats, err := c.SessionStats("sess-end-1")
	if err != nil {
		t.Fatalf("SessionStats: %v", err)
	}
	if !stats.Ended {
		t.Error("stats.Ended = false, want true")
	}
	if stats.EndReason != acp.StopReasonEndTurn {
		t.Errorf("stats.EndReason = %q, want %q", stats.EndReason, acp.StopReasonEndTurn)
	}
	if stats.EndTime.IsZero() {
		t.Error("stats.EndTime is zero, want non-zero")
	}
	// Total updates: 2 (the terminal update is also dispatched to HandleUpdate).
	if stats.UpdateCount != 2 {
		t.Errorf("stats.UpdateCount = %d, want 2", stats.UpdateCount)
	}
}

func TestConnect_MultipleSessionsTrackedIndependently(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	updates := []acp.SessionUpdate{
		{SessionId: "sess-A", Role: "user", Content: []acp.ContentBlock{{Type: "text", Text: "Hello"}}},
		{SessionId: "sess-B", Role: "user", Content: []acp.ContentBlock{{Type: "text", Text: "World"}}},
		{SessionId: "sess-A", Role: "assistant", StopReason: acp.StopReasonEndTurn},
		{SessionId: "sess-B", Role: "assistant", Content: []acp.ContentBlock{{Type: "text", Text: "More"}}},
		{SessionId: "sess-B", Role: "assistant", StopReason: acp.StopReasonMaxTokens},
	}

	args := make([]string, len(updates))
	for i, u := range updates {
		args[i] = makeSessionUpdateLine(t, u)
	}

	if err := c.Connect(context.Background(), binPath, args...); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}

	if got := c.SessionCount(); got != 2 {
		t.Errorf("SessionCount = %d, want 2", got)
	}

	statsA, err := c.SessionStats("sess-A")
	if err != nil {
		t.Fatalf("SessionStats(sess-A): %v", err)
	}
	if statsA.UpdateCount != 2 {
		t.Errorf("sess-A UpdateCount = %d, want 2", statsA.UpdateCount)
	}
	if !statsA.Ended || statsA.EndReason != acp.StopReasonEndTurn {
		t.Errorf("sess-A: Ended=%v EndReason=%q", statsA.Ended, statsA.EndReason)
	}

	statsB, err := c.SessionStats("sess-B")
	if err != nil {
		t.Fatalf("SessionStats(sess-B): %v", err)
	}
	if statsB.UpdateCount != 3 {
		t.Errorf("sess-B UpdateCount = %d, want 3", statsB.UpdateCount)
	}
	if !statsB.Ended || statsB.EndReason != acp.StopReasonMaxTokens {
		t.Errorf("sess-B: Ended=%v EndReason=%q", statsB.Ended, statsB.EndReason)
	}
}

func TestConnect_HandlerErrorStopsProcessing(t *testing.T) {
	binPath := buildFakeAgent(t)
	// errOnN=1: return error on the very first HandleUpdate call.
	h := &recordingHandler{errOnN: 1}
	c := acp.NewClient(h)

	update := acp.SessionUpdate{
		SessionId: "sess-err",
		Role:      "assistant",
	}
	line := makeSessionUpdateLine(t, update)

	err := c.Connect(context.Background(), binPath, line)
	if err == nil {
		t.Fatal("expected error from handler, got nil")
	}
	if !strings.Contains(err.Error(), "injected error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConnect_MalformedLinesSkipped(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	// Mix malformed lines with a valid session update.
	validUpdate := acp.SessionUpdate{SessionId: "sess-ok", Role: "user"}
	validLine := makeSessionUpdateLine(t, validUpdate)

	malformed1 := "not-json-at-all\n"
	malformed2 := `{"jsonrpc":"2.0","method":"session/update","params":"broken-params"}` + "\n"
	nonUpdate := `{"jsonrpc":"2.0","method":"initialized","params":{}}` + "\n"

	args := []string{malformed1, malformed2, nonUpdate, validLine}
	if err := c.Connect(context.Background(), binPath, args...); err != nil {
		t.Fatalf("Connect returned error on malformed lines: %v", err)
	}

	if got := h.UpdateCount(); got != 1 {
		t.Errorf("UpdateCount = %d, want 1 (malformed lines should be skipped)", got)
	}
}

// ─── SessionStats tests ───────────────────────────────────────────────────────

func TestSessionStats_TrackStartAndLastUpdate(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	before := time.Now()

	update := acp.SessionUpdate{SessionId: "sess-time", Role: "assistant"}
	line := makeSessionUpdateLine(t, update)
	if err := c.Connect(context.Background(), binPath, line); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	after := time.Now()

	stats, err := c.SessionStats("sess-time")
	if err != nil {
		t.Fatalf("SessionStats: %v", err)
	}
	if stats.StartTime.Before(before) || stats.StartTime.After(after) {
		t.Errorf("StartTime %v not in [%v, %v]", stats.StartTime, before, after)
	}
	if stats.LastUpdate.Before(before) || stats.LastUpdate.After(after) {
		t.Errorf("LastUpdate %v not in [%v, %v]", stats.LastUpdate, before, after)
	}
}

func TestSessionStats_UpdateCountIncrements(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	updates := make([]acp.SessionUpdate, 5)
	args := make([]string, 5)
	for i := range updates {
		updates[i] = acp.SessionUpdate{SessionId: "sess-count", Role: "assistant"}
		args[i] = makeSessionUpdateLine(t, updates[i])
	}

	if err := c.Connect(context.Background(), binPath, args...); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	stats, err := c.SessionStats("sess-count")
	if err != nil {
		t.Fatalf("SessionStats: %v", err)
	}
	if stats.UpdateCount != 5 {
		t.Errorf("UpdateCount = %d, want 5", stats.UpdateCount)
	}
}

// ─── Concurrent access safety ─────────────────────────────────────────────────

func TestConcurrentSessionCountAndStats(t *testing.T) {
	// Test that SessionCount and SessionStats are safe to call concurrently
	// while session state is being updated.
	h := &recordingHandler{}
	c := acp.NewClient(h)

	// Prime the client with some sessions by manipulating via the public API.
	// Since we can't inject session state directly, we run Connect briefly with
	// a single update, then test concurrent reads.
	binPath := buildFakeAgent(t)

	update := acp.SessionUpdate{SessionId: "sess-concurrent", Role: "assistant"}
	line := makeSessionUpdateLine(t, update)
	if err := c.Connect(context.Background(), binPath, line); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Now run many goroutines reading concurrently.
	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.SessionCount()
			if _, err := c.SessionStats("sess-concurrent"); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent read error: %v", err)
	}
}

// ─── Goroutine leak detection ─────────────────────────────────────────────────

func TestConnect_NoGoroutineLeakOnContextCancel(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	// Count goroutines before and after.
	// We can't use goleak here without importing it, so we verify that
	// Connect returns promptly and doesn't hold open the process.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	// Agent writes nothing; context times out after 200ms.
	err := c.Connect(ctx, binPath)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Connect returned error: %v", err)
	}
	// Should have returned within a reasonable window after the timeout.
	if elapsed > 1*time.Second {
		t.Errorf("Connect took %v after context deadline, expected < 1s (goroutine leak?)", elapsed)
	}
}

func TestConnect_NoGoroutineLeakOnAgentExit(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	// Agent exits immediately with no output. Connect should return quickly.
	start := time.Now()
	err := c.Connect(context.Background(), binPath)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Connect returned error: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Connect took %v after agent exit, expected < 5s", elapsed)
	}
}

// ─── Disconnect tests ─────────────────────────────────────────────────────────

func TestDisconnect_BeforeConnectIsNoOp(t *testing.T) {
	h := &recordingHandler{}
	c := acp.NewClient(h)

	if err := c.Disconnect(); err != nil {
		t.Errorf("Disconnect before Connect returned error: %v", err)
	}
}

func TestDisconnect_AfterConnectReturnsStopsAgent(t *testing.T) {
	binPath := buildFakeAgent(t)
	h := &recordingHandler{}
	c := acp.NewClient(h)

	ctx := context.Background()
	connectDone := make(chan error, 1)

	go func() {
		connectDone <- c.Connect(ctx, binPath)
	}()

	// Wait for Connect to start.
	time.Sleep(30 * time.Millisecond)

	// Disconnect should signal the agent to stop.
	if err := c.Disconnect(); err != nil {
		t.Errorf("Disconnect returned error: %v", err)
	}

	select {
	case err := <-connectDone:
		if err != nil {
			t.Errorf("Connect returned error after Disconnect: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Connect did not return after Disconnect")
	}
}

// ─── StopReason constant coverage ────────────────────────────────────────────

func TestStopReasonValues(t *testing.T) {
	cases := []struct {
		reason acp.StopReason
		want   string
	}{
		{acp.StopReasonEndTurn, "end_turn"},
		{acp.StopReasonMaxTokens, "max_tokens"},
		{acp.StopReasonToolUse, "tool_use"},
		{acp.StopReasonError, "error"},
	}
	for _, tc := range cases {
		t.Run(string(tc.reason), func(t *testing.T) {
			if string(tc.reason) != tc.want {
				t.Errorf("StopReason value = %q, want %q", tc.reason, tc.want)
			}
		})
	}
}

// ─── HandleSessionEnd records final state ────────────────────────────────────

func TestHandleSessionEnd_FinalStateRecordedBeforeCallback(t *testing.T) {
	// Verify that by the time HandleSessionEnd is called, SessionStats
	// already reflects the ended state (endReason and endTime are set).
	binPath := buildFakeAgent(t)

	var capturedStats *acp.SessionStats
	var capturedErr error
	var statsMu sync.Mutex

	h := &inspectingHandler{
		onEnd: func(c *acp.Client, sessionId string) {
			stats, err := c.SessionStats(sessionId)
			statsMu.Lock()
			capturedStats = stats
			capturedErr = err
			statsMu.Unlock()
		},
	}
	c := acp.NewClient(h)
	h.client = c

	update := acp.SessionUpdate{
		SessionId:  "sess-final",
		Role:       "assistant",
		StopReason: acp.StopReasonEndTurn,
	}
	line := makeSessionUpdateLine(t, update)
	if err := c.Connect(context.Background(), binPath, line); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	statsMu.Lock()
	defer statsMu.Unlock()

	if capturedErr != nil {
		t.Fatalf("SessionStats inside HandleSessionEnd: %v", capturedErr)
	}
	if capturedStats == nil {
		t.Fatal("no stats captured — HandleSessionEnd was not called")
	}
	if !capturedStats.Ended {
		t.Error("stats.Ended = false inside HandleSessionEnd, want true")
	}
	if capturedStats.EndReason != acp.StopReasonEndTurn {
		t.Errorf("stats.EndReason = %q, want %q", capturedStats.EndReason, acp.StopReasonEndTurn)
	}
}

// inspectingHandler calls onEnd with the Client when HandleSessionEnd fires.
type inspectingHandler struct {
	acp.SessionHandler // embed nil — only override the methods we need
	client             *acp.Client
	onEnd              func(c *acp.Client, sessionId string)
	updateCount        atomic.Int64
}

func (h *inspectingHandler) HandleUpdate(_ context.Context, _ acp.SessionUpdate) error {
	h.updateCount.Add(1)
	return nil
}

func (h *inspectingHandler) HandleSessionEnd(_ context.Context, sessionId string, _ acp.StopReason) error {
	if h.onEnd != nil {
		h.onEnd(h.client, sessionId)
	}
	return nil
}
