package acp_test

import (
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/acp"
)

// TestOpenCodeAdapter_Format verifies the adapter's format identifier.
func TestOpenCodeAdapter_Format(t *testing.T) {
	a := acp.NewOpenCodeAdapter()
	if a.Format() != "opencode-json" {
		t.Errorf("Format: got %q, want %q", a.Format(), "opencode-json")
	}
}

// TestOpenCodeAdapter_TextResponse verifies a plain assistant text response is
// parsed into a SessionUpdate with a ContentBlock.
func TestOpenCodeAdapter_TextResponse(t *testing.T) {
	a := acp.NewOpenCodeAdapter()

	// OpenCode session record format: flat JSON object.
	record := []byte(`{
		"id": "msg-oc-001",
		"sessionId": "sess-oc-1",
		"timestamp": 1700000000000,
		"role": "assistant",
		"text": "Hello from OpenCode",
		"model": "gpt-4o",
		"tokens": {"input": 15, "output": 8}
	}`)

	update, err := a.Parse(record)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if update.SessionId != "sess-oc-1" {
		t.Errorf("SessionId: got %q, want %q", update.SessionId, "sess-oc-1")
	}
	if update.Role != "assistant" {
		t.Errorf("Role: got %q, want %q", update.Role, "assistant")
	}
	if update.Timestamp != 1700000000000 {
		t.Errorf("Timestamp: got %d, want 1700000000000", update.Timestamp)
	}
	if update.TokensIn != 15 {
		t.Errorf("TokensIn: got %d, want 15", update.TokensIn)
	}
	if update.TokensOut != 8 {
		t.Errorf("TokensOut: got %d, want 8", update.TokensOut)
	}
	if update.EntryId != "msg-oc-001" {
		t.Errorf("EntryId: got %q, want %q", update.EntryId, "msg-oc-001")
	}
	if len(update.Content) != 1 {
		t.Fatalf("Content: expected 1 block, got %d", len(update.Content))
	}
	if update.Content[0].Type != "text" {
		t.Errorf("Content[0].Type: got %q, want %q", update.Content[0].Type, "text")
	}
	if update.Content[0].Content != "Hello from OpenCode" {
		t.Errorf("Content[0].Content: got %q, want %q", update.Content[0].Content, "Hello from OpenCode")
	}
}

// TestOpenCodeAdapter_ToolCall verifies that OpenCode tool_call records are
// mapped to ToolCall entries with correct fields.
func TestOpenCodeAdapter_ToolCall(t *testing.T) {
	a := acp.NewOpenCodeAdapter()

	record := []byte(`{
		"id": "msg-oc-002",
		"sessionId": "sess-oc-2",
		"timestamp": 1700000001000,
		"role": "assistant",
		"toolCalls": [
			{
				"id": "call-001",
				"type": "bash",
				"name": "bash",
				"input": {"command": "go build ./..."},
				"output": "ok"
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
	tc := update.ToolCalls[0]
	if tc.ToolCallId != "call-001" {
		t.Errorf("ToolCallId: got %q, want %q", tc.ToolCallId, "call-001")
	}
	if tc.ToolKind != acp.ToolKindBash {
		t.Errorf("ToolKind: got %q, want %q", tc.ToolKind, acp.ToolKindBash)
	}
	if tc.ToolName != "bash" {
		t.Errorf("ToolName: got %q, want %q", tc.ToolName, "bash")
	}
	if tc.ToolInput == "" {
		t.Error("ToolInput: expected non-empty")
	}
	if tc.ToolOutput != "ok" {
		t.Errorf("ToolOutput: got %q, want %q", tc.ToolOutput, "ok")
	}
}

// TestOpenCodeAdapter_MultipleToolCalls verifies multiple tool calls within a
// single record are all captured.
func TestOpenCodeAdapter_MultipleToolCalls(t *testing.T) {
	a := acp.NewOpenCodeAdapter()

	record := []byte(`{
		"id": "msg-oc-003",
		"sessionId": "sess-oc-3",
		"role": "assistant",
		"toolCalls": [
			{"id": "c1", "type": "function", "name": "read_file", "input": {"path":"a.go"}, "output": ""},
			{"id": "c2", "type": "function", "name": "write_file", "input": {"path":"b.go"}, "output": "ok"}
		]
	}`)

	update, err := a.Parse(record)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if len(update.ToolCalls) != 2 {
		t.Fatalf("ToolCalls: expected 2, got %d", len(update.ToolCalls))
	}
}

// TestOpenCodeAdapter_MalformedInput verifies that malformed JSON returns a
// descriptive error that mentions "opencode" and does not panic.
func TestOpenCodeAdapter_MalformedInput(t *testing.T) {
	a := acp.NewOpenCodeAdapter()

	cases := []struct {
		name   string
		record []byte
	}{
		{"empty", []byte{}},
		{"invalid json", []byte(`{bad json`)},
		{"missing sessionId", []byte(`{"role":"assistant"}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.Parse(tc.record)
			if err == nil {
				t.Fatalf("Parse: expected error for malformed input %q, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "opencode") {
				t.Errorf("error should mention 'opencode' adapter: %q", err.Error())
			}
		})
	}
}

// TestOpenCodeAdapter_ToolKindMapping verifies that tool type strings are
// correctly classified into ToolKind values.
func TestOpenCodeAdapter_ToolKindMapping(t *testing.T) {
	a := acp.NewOpenCodeAdapter()

	cases := []struct {
		toolType string
		want     acp.ToolKind
	}{
		{"bash", acp.ToolKindBash},
		{"function", acp.ToolKindFunction},
		{"file_read", acp.ToolKindFile},
		{"file_write", acp.ToolKindFile},
		{"web_search", acp.ToolKindWeb},
		{"unknown_type", acp.ToolKindUnknown},
	}

	for _, c := range cases {
		record := []byte(`{"sessionId":"s","role":"assistant","toolCalls":[{"id":"x","type":"` + c.toolType + `","name":"t","input":{}}]}`)
		update, err := a.Parse(record)
		if err != nil {
			t.Fatalf("toolType=%q: unexpected error: %v", c.toolType, err)
		}
		if len(update.ToolCalls) == 0 {
			t.Fatalf("toolType=%q: expected 1 tool call", c.toolType)
		}
		if update.ToolCalls[0].ToolKind != c.want {
			t.Errorf("toolType=%q: ToolKind got %q, want %q", c.toolType, update.ToolCalls[0].ToolKind, c.want)
		}
	}
}
