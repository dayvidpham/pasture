package acp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// claudeAdapter parses Claude Code JSONL transcript lines into SessionUpdate.
//
// Claude Code emits one JSON object per line in its transcript files. Each
// object represents either a user message or an assistant message. Content
// blocks within the message may be "text", "thinking", "tool_use", or
// "tool_result" typed.
//
// Registration: claudeAdapter is registered in init() so callers can retrieve
// it via GetAdapter("claude-jsonl").
type claudeAdapter struct{}

// NewClaudeAdapter returns a Claude JSONL adapter. The adapter is also
// registered in the global registry automatically via init().
func NewClaudeAdapter() Adapter {
	return &claudeAdapter{}
}

// Format returns the canonical format name for this adapter.
func (a *claudeAdapter) Format() string { return "claude-jsonl" }

// claudeRecord is the wire format for a single Claude JSONL transcript line.
//
// Claude Code uses a flat JSON object with a "type" field indicating the
// message category. Content blocks are in the "content" array.
type claudeRecord struct {
	// Type is always "message" for transcript entries.
	Type string `json:"type"`
	// Role is "user" or "assistant".
	Role string `json:"role"`
	// SessionID may be set by Claude Code wrappers; fallback to empty string.
	SessionID string `json:"sessionId"`
	// Timestamp is milliseconds since epoch (set by Claude Code integrations).
	Timestamp int64 `json:"timestamp"`
	// Content holds the ordered content blocks.
	Content []claudeContentBlock `json:"content"`
	// StopReason is the per-turn stop reason ("end_turn", "tool_use", etc.).
	StopReason string `json:"stop_reason"`
	// Usage holds token counts.
	Usage *claudeUsage `json:"usage"`
}

// claudeContentBlock is a single block in a Claude transcript message.
type claudeContentBlock struct {
	// Type is "text", "thinking", "tool_use", or "tool_result".
	Type string `json:"type"`
	// Text holds text content (type="text").
	Text string `json:"text,omitempty"`
	// Thinking holds thinking content (type="thinking").
	Thinking string `json:"thinking,omitempty"`
	// ID is the tool call correlation ID (type="tool_use" or "tool_result").
	ID string `json:"id,omitempty"`
	// ToolUseID is the ID for tool_result blocks linking back to a tool_use.
	ToolUseID string `json:"tool_use_id,omitempty"`
	// Name is the tool name (type="tool_use").
	Name string `json:"name,omitempty"`
	// Input is the tool input object (type="tool_use").
	Input json.RawMessage `json:"input,omitempty"`
	// Content is the tool result payload (type="tool_result").
	// May be a string or an array of sub-blocks.
	Content json.RawMessage `json:"content,omitempty"`
}

// claudeUsage holds token consumption from a Claude message.
type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Parse converts a single Claude JSONL line into a SessionUpdate.
//
// Returns a descriptive error if the record is empty, not valid JSON, or
// missing required fields. Errors identify the adapter ("claude-jsonl"),
// the failure site, and how to correct the input.
func (a *claudeAdapter) Parse(record []byte) (SessionUpdate, error) {
	if len(record) == 0 {
		return SessionUpdate{}, fmt.Errorf(
			"claude-jsonl adapter: Parse: empty record — "+
				"expected a non-empty JSON object (one Claude transcript line); "+
				"check that the source file is not empty and the line was read correctly",
		)
	}

	var raw claudeRecord
	if err := json.Unmarshal(record, &raw); err != nil {
		return SessionUpdate{}, fmt.Errorf(
			"claude-jsonl adapter: Parse: invalid JSON at byte offset near %d: %w — "+
				"expected a valid JSON object matching the Claude JSONL transcript format",
			approxErrorOffset(record, err), err,
		)
	}

	// Validate the "type" field — it must be a string.
	// (json.Unmarshal already caught type mismatches above; this is belt-and-suspenders.)
	var typeCheck struct {
		Type interface{} `json:"type"`
	}
	_ = json.Unmarshal(record, &typeCheck)
	if typeCheck.Type != nil {
		if _, ok := typeCheck.Type.(string); !ok {
			return SessionUpdate{}, fmt.Errorf(
				"claude-jsonl adapter: Parse: field \"type\" must be a string, got %T — "+
					"Claude transcript lines always have {\"type\": \"message\", ...}",
				typeCheck.Type,
			)
		}
	}

	update := SessionUpdate{
		SessionID:  raw.SessionID,
		Role:       raw.Role,
		Timestamp:  raw.Timestamp,
		StopReason: parseStopReason(raw.StopReason),
	}

	if raw.Usage != nil {
		update.TokensIn = raw.Usage.InputTokens
		update.TokensOut = raw.Usage.OutputTokens
	}

	// Parse content blocks.
	for _, cb := range raw.Content {
		switch cb.Type {
		case "text":
			update.Content = append(update.Content, ContentBlock{
				Type:    "text",
				Content: cb.Text,
			})

		case "thinking":
			update.Content = append(update.Content, ContentBlock{
				Type:    "thinking",
				Content: cb.Thinking,
			})

		case "tool_use":
			inputStr := ""
			if len(cb.Input) > 0 {
				inputStr = string(cb.Input)
			}
			update.ToolCalls = append(update.ToolCalls, ToolCall{
				ToolCallID: cb.ID,
				ToolKind:   inferToolKind(cb.Name),
				ToolName:   cb.Name,
				ToolInput:  inputStr,
			})

		case "tool_result":
			// tool_result links back to a tool_use via ToolUseID.
			// The content may be a plain string or a JSON array of sub-blocks.
			outputStr := extractToolResultContent(cb.Content)
			update.ToolCalls = append(update.ToolCalls, ToolCall{
				ToolCallID: cb.ToolUseID,
				ToolKind:   ToolKindFunction,
				ToolName:   "", // tool_result does not repeat the tool name
				ToolOutput: outputStr,
			})
		}
	}

	return update, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parseStopReason maps a Claude stop_reason string to a StopReason constant.
func parseStopReason(s string) StopReason {
	switch s {
	case "end_turn":
		return StopReasonEndTurn
	case "max_tokens":
		return StopReasonMaxTokens
	case "tool_use":
		return StopReasonToolUse
	case "error":
		return StopReasonError
	case "":
		return ""
	default:
		return StopReason(s)
	}
}

// inferToolKind classifies a tool name into a ToolKind category.
// This is a heuristic for Claude tool names; it can be extended as needed.
func inferToolKind(toolName string) ToolKind {
	lower := strings.ToLower(toolName)
	switch {
	case lower == "bash" || strings.Contains(lower, "bash") || strings.Contains(lower, "shell"):
		return ToolKindBash
	case strings.Contains(lower, "read") || strings.Contains(lower, "write") ||
		strings.Contains(lower, "file") || strings.Contains(lower, "glob"):
		return ToolKindFile
	case strings.Contains(lower, "search") || strings.Contains(lower, "grep"):
		return ToolKindSearch
	case strings.Contains(lower, "web") || strings.Contains(lower, "fetch") ||
		strings.Contains(lower, "http"):
		return ToolKindWeb
	default:
		return ToolKindFunction
	}
}

// extractToolResultContent converts a tool_result content field to a string.
// The content may be a plain JSON string or an array of content sub-blocks.
func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try plain string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Fallback: return raw JSON as-is (covers array of sub-blocks).
	return string(raw)
}

// approxErrorOffset returns a rough byte offset for a json.SyntaxError,
// falling back to the length of the record if the error type is not recognized.
func approxErrorOffset(record []byte, err error) int {
	type syntaxError interface {
		Offset() int64
	}
	if se, ok := err.(interface{ Error() string }); ok {
		_ = se
	}
	// json.SyntaxError has an Offset field accessible via reflection or type assert.
	if jerr, ok := err.(*json.SyntaxError); ok {
		return int(jerr.Offset)
	}
	if jerr, ok := err.(*json.UnmarshalTypeError); ok {
		return int(jerr.Offset)
	}
	return len(record)
}

func init() {
	RegisterAdapter(NewClaudeAdapter())
}
