package acp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// openCodeAdapter parses OpenCode session JSON records into SessionUpdate.
//
// OpenCode emits flat JSON objects per message turn. Tool calls are in a
// "toolCalls" array; text responses are in a "text" field. Token usage is
// nested under a "tokens" object.
//
// openCodeAdapter is registered in the static compile-time registry in adapter.go
// under the key "opencode-json".
type openCodeAdapter struct{}

// NewOpenCodeAdapter returns an OpenCode JSON adapter instance.
func NewOpenCodeAdapter() Adapter {
	return &openCodeAdapter{}
}

// Format returns the canonical format name for this adapter.
func (a *openCodeAdapter) Format() string { return "opencode-json" }

// openCodeRecord is the wire format for a single OpenCode session JSON record.
type openCodeRecord struct {
	// ID is the provider-native message identifier.
	ID string `json:"id"`
	// SessionID uniquely identifies the agent session.
	SessionID string `json:"sessionId"`
	// Timestamp is milliseconds since epoch.
	Timestamp int64 `json:"timestamp"`
	// Role is "user", "assistant", or "tool".
	Role string `json:"role"`
	// Text holds the plain text response for assistant messages.
	Text string `json:"text,omitempty"`
	// Model identifies the LLM model used.
	Model string `json:"model,omitempty"`
	// Tokens holds input/output token counts.
	Tokens *openCodeTokens `json:"tokens,omitempty"`
	// ToolCalls holds the tool invocations for this turn.
	ToolCalls []openCodeToolCall `json:"toolCalls,omitempty"`
	// Error indicates whether this record represents an error turn.
	Error bool `json:"error,omitempty"`
}

// openCodeTokens holds token usage for an OpenCode record.
type openCodeTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// openCodeToolCall is a single tool call record in an OpenCode session.
type openCodeToolCall struct {
	// ID is the correlation ID for this tool call.
	ID string `json:"id"`
	// Type is the tool category string (e.g. "bash", "function", "file_read").
	Type string `json:"type"`
	// Name is the tool name.
	Name string `json:"name"`
	// Input is the tool input as a raw JSON object.
	Input json.RawMessage `json:"input,omitempty"`
	// Output is the tool result as a string.
	Output string `json:"output,omitempty"`
}

// Parse converts a single OpenCode JSON record into a SessionUpdate.
//
// Returns a descriptive error if the record is empty, not valid JSON, or
// missing required fields (sessionId). Errors identify the adapter
// ("opencode-json"), the failure site, and how to correct the input.
func (a *openCodeAdapter) Parse(record []byte) (SessionUpdate, error) {
	if len(record) == 0 {
		return SessionUpdate{}, fmt.Errorf(
			"opencode-json adapter: Parse: empty record — " +
				"expected a non-empty JSON object (one OpenCode session record); " +
				"check that the source was not empty before calling Parse",
		)
	}

	var raw openCodeRecord
	if err := json.Unmarshal(record, &raw); err != nil {
		return SessionUpdate{}, fmt.Errorf(
			"opencode-json adapter: Parse: invalid JSON: %w — "+
				"expected a valid JSON object matching the OpenCode session record format "+
				"(fields: id, sessionId, timestamp, role, text?, toolCalls?)",
			err,
		)
	}

	if raw.SessionID == "" {
		return SessionUpdate{}, fmt.Errorf(
			"opencode-json adapter: Parse: missing required field \"sessionId\" — " +
				"every OpenCode session record must include a non-empty sessionId; " +
				"verify the record was captured from an active OpenCode session",
		)
	}

	update := SessionUpdate{
		SessionID: raw.SessionID,
		Role:      raw.Role,
		Timestamp: raw.Timestamp,
		IsError:   raw.Error,
		EntryID:   raw.ID,
	}

	if raw.Tokens != nil {
		update.TokensIn = raw.Tokens.Input
		update.TokensOut = raw.Tokens.Output
	}

	// Text response → ContentBlock.
	if raw.Text != "" {
		update.Content = append(update.Content, ContentBlock{
			Type:    "text",
			Content: raw.Text,
		})
	}

	// Tool calls.
	for _, tc := range raw.ToolCalls {
		inputStr := ""
		if len(tc.Input) > 0 {
			inputStr = string(tc.Input)
		}
		update.ToolCalls = append(update.ToolCalls, ToolCall{
			ToolCallID: tc.ID,
			ToolKind:   openCodeToolKind(tc.Type),
			ToolName:   tc.Name,
			ToolInput:  inputStr,
			ToolOutput: tc.Output,
		})
	}

	return update, nil
}

// openCodeToolKind maps an OpenCode tool type string to a ToolKind.
func openCodeToolKind(toolType string) ToolKind {
	lower := strings.ToLower(toolType)
	switch {
	case lower == "bash":
		return ToolKindBash
	case lower == "function":
		return ToolKindFunction
	case strings.HasPrefix(lower, "file"):
		return ToolKindFile
	case strings.HasPrefix(lower, "web"):
		return ToolKindWeb
	case strings.Contains(lower, "search"):
		return ToolKindSearch
	default:
		return ToolKindUnknown
	}
}
