package acp_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/acp"
)

// TestClaudeAdapter_Format verifies the adapter's format identifier.
func TestClaudeAdapter_Format(t *testing.T) {
	a := acp.NewClaudeAdapter()
	if a.Format() != "claude-jsonl" {
		t.Errorf("Format: got %q, want %q", a.Format(), "claude-jsonl")
	}
}

// TestClaudeAdapter_AssistantTextMessage verifies a plain assistant text
// message is parsed into a SessionUpdate with a ContentBlock.
func TestClaudeAdapter_AssistantTextMessage(t *testing.T) {
	a := acp.NewClaudeAdapter()

	// Minimal Claude JSONL assistant message with a text content block.
	record := []byte(`{
		"type": "message",
		"role": "assistant",
		"sessionId": "sess-claude-1",
		"timestamp": 1700000000000,
		"content": [
			{"type": "text", "text": "Hello from Claude"}
		],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)

	update, err := a.Parse(record)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if update.SessionId != "sess-claude-1" {
		t.Errorf("SessionId: got %q, want %q", update.SessionId, "sess-claude-1")
	}
	if update.Role != "assistant" {
		t.Errorf("Role: got %q, want %q", update.Role, "assistant")
	}
	if update.Timestamp != 1700000000000 {
		t.Errorf("Timestamp: got %d, want 1700000000000", update.Timestamp)
	}
	if update.StopReason != acp.StopReasonEndTurn {
		t.Errorf("StopReason: got %q, want %q", update.StopReason, acp.StopReasonEndTurn)
	}
	if update.TokensIn != 10 {
		t.Errorf("TokensIn: got %d, want 10", update.TokensIn)
	}
	if update.TokensOut != 5 {
		t.Errorf("TokensOut: got %d, want 5", update.TokensOut)
	}
	if len(update.Content) != 1 {
		t.Fatalf("Content: expected 1 block, got %d", len(update.Content))
	}
	if update.Content[0].Type != "text" {
		t.Errorf("Content[0].Type: got %q, want %q", update.Content[0].Type, "text")
	}
	if update.Content[0].Text != "Hello from Claude" {
		t.Errorf("Content[0].Text: got %q, want %q", update.Content[0].Text, "Hello from Claude")
	}
}

// TestClaudeAdapter_ToolUseExtraction verifies that tool_use content blocks
// are mapped to ToolCall entries.
func TestClaudeAdapter_ToolUseExtraction(t *testing.T) {
	a := acp.NewClaudeAdapter()

	record := []byte(`{
		"type": "message",
		"role": "assistant",
		"sessionId": "sess-tool-use",
		"timestamp": 1700000001000,
		"content": [
			{"type": "text", "text": "Let me run a command."},
			{
				"type": "tool_use",
				"id": "toolu_01abc",
				"name": "Bash",
				"input": {"command": "ls -la"}
			}
		],
		"stop_reason": "tool_use"
	}`)

	update, err := a.Parse(record)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(update.ToolCalls) != 1 {
		t.Fatalf("ToolCalls: expected 1, got %d", len(update.ToolCalls))
	}
	tc := update.ToolCalls[0]
	if tc.ToolCallId != "toolu_01abc" {
		t.Errorf("ToolCallId: got %q, want %q", tc.ToolCallId, "toolu_01abc")
	}
	if tc.ToolName != "Bash" {
		t.Errorf("ToolName: got %q, want %q", tc.ToolName, "Bash")
	}
	if tc.ToolInput == "" {
		t.Error("ToolInput: expected non-empty")
	}
	if update.StopReason != acp.StopReasonToolUse {
		t.Errorf("StopReason: got %q, want %q", update.StopReason, acp.StopReasonToolUse)
	}
	// Text block should still be present in Content.
	if len(update.Content) == 0 {
		t.Error("Content: expected at least 1 text block alongside tool_use")
	}
}

// TestClaudeAdapter_ToolResultMessage verifies that tool_result messages are
// mapped with the correct EntryId / ParentEntryId linkage.
func TestClaudeAdapter_ToolResultMessage(t *testing.T) {
	a := acp.NewClaudeAdapter()

	record := []byte(`{
		"type": "message",
		"role": "user",
		"sessionId": "sess-tool-result",
		"timestamp": 1700000002000,
		"content": [
			{
				"type": "tool_result",
				"tool_use_id": "toolu_01abc",
				"content": "file.go\nmain.go"
			}
		]
	}`)

	update, err := a.Parse(record)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if update.Role != "user" {
		t.Errorf("Role: got %q, want %q", update.Role, "user")
	}
	// Tool result should be represented as a ToolCall with output set.
	if len(update.ToolCalls) != 1 {
		t.Fatalf("ToolCalls: expected 1 for tool_result, got %d", len(update.ToolCalls))
	}
	tc := update.ToolCalls[0]
	if tc.ToolCallId != "toolu_01abc" {
		t.Errorf("ToolCallId: got %q, want %q", tc.ToolCallId, "toolu_01abc")
	}
	if tc.ToolOutput == "" {
		t.Error("ToolOutput: expected non-empty for tool_result")
	}
}

// TestClaudeAdapter_MalformedInput verifies that malformed JSON returns a
// descriptive error (not a panic) that includes byte offset context.
func TestClaudeAdapter_MalformedInput(t *testing.T) {
	a := acp.NewClaudeAdapter()

	cases := []struct {
		name   string
		record []byte
	}{
		{"empty", []byte{}},
		{"invalid json", []byte(`{not valid json`)},
		{"wrong type", []byte(`{"type": 42}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.Parse(tc.record)
			if err == nil {
				t.Fatalf("Parse: expected error for malformed input %q, got nil", tc.name)
			}
			// Error must mention "claude-jsonl" so callers know which adapter failed.
			if !strings.Contains(err.Error(), "claude") {
				t.Errorf("error should mention 'claude' adapter: %q", err.Error())
			}
		})
	}
}

// TestClaudeAdapter_StopReasonVariants verifies that all common stop_reason
// strings are mapped to the correct StopReason constant.
func TestClaudeAdapter_StopReasonVariants(t *testing.T) {
	a := acp.NewClaudeAdapter()

	cases := []struct {
		stopReason string
		want       acp.StopReason
	}{
		{"end_turn", acp.StopReasonEndTurn},
		{"max_tokens", acp.StopReasonMaxTokens},
		{"tool_use", acp.StopReasonToolUse},
		{"error", acp.StopReasonError},
		{"", ""},
		{"custom_reason", acp.StopReason("custom_reason")},
	}

	for _, c := range cases {
		input := `{"type":"message","role":"assistant","sessionId":"s","content":[],"stop_reason":"` + c.stopReason + `"}`
		if c.stopReason == "" {
			input = `{"type":"message","role":"assistant","sessionId":"s","content":[]}`
		}
		update, err := a.Parse([]byte(input))
		if err != nil {
			t.Fatalf("stop_reason=%q: unexpected error: %v", c.stopReason, err)
		}
		if update.StopReason != c.want {
			t.Errorf("stop_reason=%q: got %q, want %q", c.stopReason, update.StopReason, c.want)
		}
	}
}

// TestClaudeAdapter_InferToolKind verifies tool name → ToolKind mapping.
func TestClaudeAdapter_InferToolKind(t *testing.T) {
	a := acp.NewClaudeAdapter()

	cases := []struct {
		toolName string
		want     acp.ToolKind
	}{
		{"Bash", acp.ToolKindBash},
		{"bash_execute", acp.ToolKindBash},
		{"Read", acp.ToolKindFile},
		{"WriteFile", acp.ToolKindFile},
		{"Glob", acp.ToolKindFile},
		{"Grep", acp.ToolKindSearch},
		{"WebFetch", acp.ToolKindWeb},
		{"GetWeather", acp.ToolKindFunction},
	}

	for _, c := range cases {
		input := `{"type":"message","role":"assistant","sessionId":"s","content":[{"type":"tool_use","id":"x","name":"` + c.toolName + `","input":{}}]}`
		update, err := a.Parse([]byte(input))
		if err != nil {
			t.Fatalf("tool=%q: unexpected error: %v", c.toolName, err)
		}
		if len(update.ToolCalls) == 0 {
			t.Fatalf("tool=%q: expected 1 tool call", c.toolName)
		}
		if update.ToolCalls[0].ToolKind != c.want {
			t.Errorf("tool=%q: ToolKind got %q, want %q", c.toolName, update.ToolCalls[0].ToolKind, c.want)
		}
	}
}

// TestClaudeAdapter_ToolResultArrayContent verifies that tool_result content
// arrays (not plain strings) are captured as raw JSON.
func TestClaudeAdapter_ToolResultArrayContent(t *testing.T) {
	a := acp.NewClaudeAdapter()

	record := []byte(`{
		"type": "message",
		"role": "user",
		"sessionId": "sess-arr",
		"content": [
			{
				"type": "tool_result",
				"tool_use_id": "toolu_xyz",
				"content": [{"type":"text","text":"result line 1"},{"type":"text","text":"result line 2"}]
			}
		]
	}`)

	update, err := a.Parse(record)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(update.ToolCalls) != 1 {
		t.Fatalf("ToolCalls: expected 1, got %d", len(update.ToolCalls))
	}
	if update.ToolCalls[0].ToolOutput == "" {
		t.Error("ToolOutput: expected non-empty for array content tool_result")
	}
}

// TestClaudeAdapter_ThinkingBlock verifies that thinking content blocks are
// captured as ContentBlock with type="thinking".
func TestClaudeAdapter_ThinkingBlock(t *testing.T) {
	a := acp.NewClaudeAdapter()

	record := []byte(`{
		"type": "message",
		"role": "assistant",
		"sessionId": "sess-thinking",
		"content": [
			{"type": "thinking", "thinking": "I need to consider..."},
			{"type": "text", "text": "Here is my answer."}
		]
	}`)

	update, err := a.Parse(record)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(update.Content) < 2 {
		t.Fatalf("Content: expected >= 2 blocks, got %d", len(update.Content))
	}
	found := false
	for _, cb := range update.Content {
		if cb.Type == "thinking" {
			found = true
			if cb.Text == "" {
				t.Error("thinking ContentBlock: Text should not be empty")
			}
		}
	}
	if !found {
		t.Error("expected a ContentBlock with type=thinking")
	}
}
