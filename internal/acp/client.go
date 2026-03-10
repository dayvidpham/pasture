// Package acp provides an ACP (Agent Client Protocol) client for connecting
// to ACP-compatible agents and receiving live session update streams.
//
// The Client connects to a running agent process via stdio JSON-RPC, parses
// session update messages, and forwards them to a SessionHandler for indexing,
// storage, or forwarding.
//
// Design notes:
//   - ACP wire protocol: newline-delimited JSON-RPC 2.0 over stdio
//   - Session state is tracked per sessionID with goroutine-safe access
//   - No leaked goroutines: context cancellation stops the read loop cleanly
//   - All ACP types are defined here; acp-go-sdk is not a dependency (we define
//     our own aligned types to avoid the external SDK requirement)
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
)

// ─── ACP Wire Types ───────────────────────────────────────────────────────────

// StopReason classifies why an ACP session ended.
// Values align with the ACP protocol stop_reason field.
type StopReason string

const (
	// StopReasonEndTurn means the agent completed its turn normally.
	StopReasonEndTurn StopReason = "end_turn"
	// StopReasonMaxTokens means the session hit a token limit.
	StopReasonMaxTokens StopReason = "max_tokens"
	// StopReasonToolUse means the agent is paused at a tool call.
	StopReasonToolUse StopReason = "tool_use"
	// StopReasonError means the session ended due to an error.
	StopReasonError StopReason = "error"
	// StopReasonCancelled means the session was cancelled by the client.
	StopReasonCancelled StopReason = "cancelled"
)

// ToolKind classifies the category of a tool call for indexing and display.
type ToolKind string

const (
	// ToolKindFunction represents a standard function/capability call.
	ToolKindFunction ToolKind = "function"
	// ToolKindBash represents a shell command execution.
	ToolKindBash ToolKind = "bash"
	// ToolKindFile represents a file system read or write operation.
	ToolKindFile ToolKind = "file"
	// ToolKindSearch represents a search or grep-style operation.
	ToolKindSearch ToolKind = "search"
	// ToolKindWeb represents a web fetch or search operation.
	ToolKindWeb ToolKind = "web"
	// ToolKindUnknown is the fallback for unrecognized tool categories.
	ToolKindUnknown ToolKind = "unknown"
)

// ToolCall represents a tool invocation and its result within a SessionUpdate.
type ToolCall struct {
	// ToolCallID is the correlation ID linking tool_use to tool_result.
	ToolCallID string `json:"toolCallId"`
	// ToolKind classifies the tool category (function, bash, file, etc.).
	ToolKind ToolKind `json:"toolKind"`
	// ToolName is the name of the tool or function invoked.
	ToolName string `json:"toolName"`
	// ToolInput is the JSON-encoded input arguments supplied to the tool.
	ToolInput string `json:"toolInput,omitempty"`
	// ToolOutput is the JSON-encoded result returned by the tool (if completed).
	ToolOutput string `json:"toolOutput,omitempty"`
}

// ContentBlock represents a single piece of content in a session update.
// Aligned with the ACP content block model.
type ContentBlock struct {
	// Type identifies the block type (e.g. "text", "tool_use", "tool_result", "thinking").
	Type string `json:"type"`
	// Content holds the text payload for "text" and "thinking" blocks.
	// For "text" blocks parsed from Claude format, the "text" field is mapped here.
	Content string `json:"content,omitempty"`
	// Text holds the text content for "text" blocks in the ACP live-stream format.
	// Adapters should populate Content; the live client uses Text from wire format.
	Text string `json:"text,omitempty"`
	// ID is the tool call identifier for "tool_use" and "tool_result" blocks.
	ID string `json:"id,omitempty"`
	// Name is the tool name for "tool_use" blocks.
	Name string `json:"name,omitempty"`
	// Input holds tool input as raw JSON for "tool_use" blocks.
	Input json.RawMessage `json:"input,omitempty"`
	// RawContent holds tool result content for "tool_result" blocks in wire format.
	RawContent json.RawMessage `json:"rawContent,omitempty"`
}

// SessionUpdate represents a single streamed update from an ACP agent.
// Each update corresponds to one message turn in the session.
//
// For the live ACP client (Connect), updates come from JSON-RPC params.
// For adapter-parsed updates (Claude JSONL, OpenCode JSON), fields are
// populated by the respective Adapter.Parse implementation.
type SessionUpdate struct {
	// SessionID uniquely identifies the agent session.
	SessionID string `json:"sessionId"`
	// Role is the message author: "user", "assistant", or "tool".
	Role string `json:"role"`
	// Content holds the ordered content blocks for this update.
	Content []ContentBlock `json:"content,omitempty"`
	// ToolCalls holds extracted tool invocations from this update.
	// Populated by Adapter.Parse; the live ACP client derives these from Content.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// StopReason is present when the session ends; empty during normal updates.
	StopReason StopReason `json:"stopReason,omitempty"`
	// Usage holds token usage for this update (may be nil for intermediate updates).
	Usage *UsageStats `json:"usage,omitempty"`
	// TokensIn is the number of input tokens consumed (convenience accessor).
	// Adapters should populate this directly; live client reads from Usage.
	TokensIn int `json:"tokensIn,omitempty"`
	// TokensOut is the number of output tokens produced (convenience accessor).
	TokensOut int `json:"tokensOut,omitempty"`
	// Timestamp is the wall-clock time the update was emitted, in milliseconds since epoch.
	Timestamp int64 `json:"timestamp,omitempty"`
	// IsError signals that this update represents an error turn.
	IsError bool `json:"isError,omitempty"`
	// EntryID is the provider-native message ID.
	EntryID string `json:"entryId,omitempty"`
	// ParentEntryID links tool_result turns back to their tool_use origin.
	ParentEntryID string `json:"parentEntryId,omitempty"`
}

// UsageStats holds token consumption for a session update.
type UsageStats struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// jsonRPCMessage is the wire envelope for ACP's newline-delimited JSON-RPC 2.0.
// We only need to extract the "method" and "params" fields.
type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// ─── Session Tracking ─────────────────────────────────────────────────────────

// sessionState tracks per-session lifecycle state.
type sessionState struct {
	sessionID   string
	updateCount int
	startTime   time.Time
	lastUpdate  time.Time
	endReason   *StopReason // nil until session ends
	endTime     *time.Time  // nil until session ends
}

// SessionStats is the public view of a session's state.
type SessionStats struct {
	SessionID    string
	UpdateCount  int
	StartTime    time.Time
	LastUpdate   time.Time
	Ended        bool
	EndReason    StopReason // zero value if session is still active
	EndTime      time.Time  // zero value if session is still active
}

// ─── SessionHandler Interface ─────────────────────────────────────────────────

// SessionHandler processes live ACP session updates.
//
// Implementations handle indexing (SharedIndexer), storage, event forwarding,
// or hook dispatch. All methods must be safe for concurrent invocation.
type SessionHandler interface {
	// HandleUpdate processes a single session update.
	// Called for every message received from the agent, in order.
	// Returning an error causes the client to stop processing.
	HandleUpdate(ctx context.Context, update SessionUpdate) error

	// HandleSessionEnd is called when a session ends (stop_reason present).
	// sessionID is the session that ended. reason is the stop reason.
	HandleSessionEnd(ctx context.Context, sessionID string, reason StopReason) error
}

// ─── Client ──────────────────────────────────────────────────────────────────

// Client connects to ACP-compatible agents and receives live session updates.
//
// Call Connect to start an agent process and begin receiving updates.
// The client is goroutine-safe; SessionStats and SessionCount may be called
// concurrently with Connect.
//
// Lifecycle:
//
//	client := NewClient(handler)
//	err := client.Connect(ctx, "claude", "--mcp-server", "...")
//	// Connect blocks until ctx is cancelled or the agent exits
//	err = client.Disconnect()
type Client struct {
	handler  SessionHandler
	sessions map[string]*sessionState
	mu       sync.RWMutex

	// cmd is the running agent process; set by Connect, cleared by Disconnect.
	cmd    *exec.Cmd
	cmdMu  sync.Mutex

	// cancel stops the Connect read loop.
	cancel context.CancelFunc
}

// NewClient creates an ACP client that forwards session updates to handler.
// handler must not be nil.
func NewClient(handler SessionHandler) *Client {
	if handler == nil {
		panic("acp.NewClient: handler must not be nil")
	}
	return &Client{
		handler:  handler,
		sessions: make(map[string]*sessionState),
	}
}

// Connect starts the agent process identified by agentCmd and agentArgs,
// reads newline-delimited JSON-RPC messages from its stdout, and forwards
// parsed SessionUpdate events to the configured SessionHandler.
//
// Connect blocks until:
//   - ctx is cancelled (clean shutdown)
//   - the agent process exits
//   - a handler returns an error
//
// After Connect returns, call Disconnect to clean up the process.
// Calling Connect again on the same Client after it returns is not supported;
// create a new Client instead.
func (c *Client) Connect(ctx context.Context, agentCmd string, agentArgs ...string) error {
	c.cmdMu.Lock()
	if c.cmd != nil {
		c.cmdMu.Unlock()
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     "Connect called on an already-connected ACP client",
			Why:      "Client.cmd is non-nil, indicating a previous Connect call",
			Impact:   "Cannot start a second agent connection on the same Client",
			Fix:      "Create a new Client with NewClient for each agent connection",
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	cmd := exec.CommandContext(runCtx, agentCmd, agentArgs...) //nolint:gosec // agentCmd is caller-controlled
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.cmdMu.Unlock()
		cancel()
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("failed to attach stdout pipe to agent %q", agentCmd),
			Why:      err.Error(),
			Impact:   "ACP client cannot receive session updates from the agent",
			Fix:      "Ensure the agent binary exists and is executable",
		}
	}

	if err := cmd.Start(); err != nil {
		c.cmdMu.Unlock()
		cancel()
		return &pasterrors.StructuredError{
			Category: pasterrors.CategoryConnection,
			What:     fmt.Sprintf("failed to start agent process %q", agentCmd),
			Why:      err.Error(),
			Impact:   "ACP client cannot establish a session with the agent",
			Fix:      "Ensure the agent binary exists at the given path and is executable",
		}
	}
	c.cmd = cmd
	c.cmdMu.Unlock()

	// readErr captures errors from the read loop, returned to Connect's caller.
	var readErr error
	readErr = c.readLoop(runCtx, stdout)
	cancel() // stop context regardless of how we exited

	// Wait for the process to exit so we don't leak a zombie.
	// Ignore the exit error; we surface readErr instead.
	_ = cmd.Wait()

	return readErr
}

// readLoop reads newline-delimited JSON-RPC messages from r until ctx is done,
// r is exhausted, or a handler returns an error.
func (c *Client) readLoop(ctx context.Context, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	// Support large messages (up to 4 MiB per line).
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		// Check for context cancellation between lines.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Skip malformed lines; the agent may emit non-JSON diagnostic output.
			continue
		}

		// ACP session updates arrive as "session/update" method calls.
		if msg.Method != "session/update" {
			continue
		}

		var update SessionUpdate
		if err := json.Unmarshal(msg.Params, &update); err != nil {
			// Skip unparseable session updates.
			continue
		}

		if err := c.processUpdate(ctx, update); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		// Scanner error (not EOF) — could be a broken pipe on agent exit.
		// This is not fatal; return nil so Connect reports success.
		_ = err
	}
	return nil
}

// processUpdate records session state and dispatches to the handler.
func (c *Client) processUpdate(ctx context.Context, update SessionUpdate) error {
	now := time.Now()

	c.mu.Lock()
	sess, exists := c.sessions[update.SessionID]
	if !exists {
		sess = &sessionState{
			sessionID: update.SessionID,
			startTime: now,
		}
		c.sessions[update.SessionID] = sess
	}
	sess.updateCount++
	sess.lastUpdate = now
	c.mu.Unlock()

	// Forward to handler (outside lock to avoid deadlocks in handler implementations).
	if err := c.handler.HandleUpdate(ctx, update); err != nil {
		return fmt.Errorf("acp: SessionHandler.HandleUpdate for session %q: %w", update.SessionID, err)
	}

	// If this update signals session end, record final state and notify handler.
	if update.StopReason != "" {
		endTime := time.Now()

		c.mu.Lock()
		sess.endReason = &update.StopReason
		sess.endTime = &endTime
		c.mu.Unlock()

		if err := c.handler.HandleSessionEnd(ctx, update.SessionID, update.StopReason); err != nil {
			return fmt.Errorf("acp: SessionHandler.HandleSessionEnd for session %q: %w", update.SessionID, err)
		}
	}

	return nil
}

// Disconnect signals the agent process to stop and waits for cleanup.
// It is safe to call Disconnect after Connect returns; it is a no-op in that case.
func (c *Client) Disconnect() error {
	c.cmdMu.Lock()
	cancel := c.cancel
	c.cmdMu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// SessionCount returns the total number of sessions seen (including ended ones).
// Safe for concurrent use.
func (c *Client) SessionCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.sessions)
}

// SessionStats returns a snapshot of the stats for the given sessionID.
// Returns an error if the session has not been observed.
func (c *Client) SessionStats(sessionID string) (*SessionStats, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	sess, ok := c.sessions[sessionID]
	if !ok {
		return nil, &pasterrors.StructuredError{
			Category: pasterrors.CategoryValidation,
			What:     fmt.Sprintf("session %q not found", sessionID),
			Why:      "no updates have been received for this session ID",
			Impact:   "session stats cannot be returned",
			Fix:      "use a sessionID that has been observed by this Client",
		}
	}

	stats := &SessionStats{
		SessionID:   sess.sessionID,
		UpdateCount: sess.updateCount,
		StartTime:   sess.startTime,
		LastUpdate:  sess.lastUpdate,
	}
	if sess.endReason != nil {
		stats.Ended = true
		stats.EndReason = *sess.endReason
		stats.EndTime = *sess.endTime
	}
	return stats, nil
}
