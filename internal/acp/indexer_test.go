package acp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dayvidpham/pasture/internal/acp"
)

// TestSharedIndexer_1000Updates verifies that 1000 SessionUpdate values are
// indexed without drops: the result slice must contain at least 1000 entries.
func TestSharedIndexer_1000Updates(t *testing.T) {
	idx := acp.NewSharedIndexer()

	updates := make([]acp.SessionUpdate, 1000)
	for i := range updates {
		updates[i] = acp.SessionUpdate{
			SessionId:  fmt.Sprintf("session-%d", i),
			Timestamp:  int64(1_000_000 + i),
			Role:       "assistant",
			Content:    []acp.ContentBlock{{Type: "text", Content: fmt.Sprintf("message %d", i)}},
			StopReason: acp.StopReasonEndTurn,
			TokensIn:   10,
			TokensOut:  20,
		}
	}

	entries, err := idx.Index(updates)
	if err != nil {
		t.Fatalf("Index returned unexpected error: %v", err)
	}
	if len(entries) < 1000 {
		t.Errorf("expected >= 1000 entries, got %d", len(entries))
	}
}

// TestSharedIndexer_DepthZeroEntry verifies a basic message entry is created
// with correct field values.
func TestSharedIndexer_DepthZeroEntry(t *testing.T) {
	idx := acp.NewSharedIndexer()

	ts := int64(1_700_000_000_000)
	updates := []acp.SessionUpdate{
		{
			SessionId:  "sess-abc",
			Timestamp:  ts,
			Role:       "assistant",
			Content:    []acp.ContentBlock{{Type: "text", Content: "hello world"}},
			StopReason: acp.StopReasonEndTurn,
			TokensIn:   5,
			TokensOut:  10,
		},
	}

	entries, err := idx.Index(updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) < 1 {
		t.Fatal("expected at least 1 entry")
	}

	e := entries[0]
	if e.SessionId != "sess-abc" {
		t.Errorf("SessionId: got %q, want %q", e.SessionId, "sess-abc")
	}
	if e.Role != "assistant" {
		t.Errorf("Role: got %q, want %q", e.Role, "assistant")
	}
	if e.Depth != 0 {
		t.Errorf("Depth: got %d, want 0", e.Depth)
	}
	if e.TimestampMs == nil || *e.TimestampMs != ts {
		t.Errorf("TimestampMs: got %v, want %d", e.TimestampMs, ts)
	}
	if e.ContentPreview == nil || *e.ContentPreview != "hello world" {
		t.Errorf("ContentPreview: got %v, want %q", e.ContentPreview, "hello world")
	}
	if e.TokensIn == nil || *e.TokensIn != 5 {
		t.Errorf("TokensIn: got %v, want 5", e.TokensIn)
	}
	if e.TokensOut == nil || *e.TokensOut != 10 {
		t.Errorf("TokensOut: got %v, want 10", e.TokensOut)
	}
	if e.StopReason == nil || *e.StopReason != "end_turn" {
		t.Errorf("StopReason: got %v, want %q", e.StopReason, "end_turn")
	}
	if e.HasToolUse {
		t.Errorf("HasToolUse: expected false for update with no tool calls")
	}
}

// TestSharedIndexer_ToolCallEntries verifies that ToolCall entries are emitted
// as depth=1 children referencing their parent depth=0 entry.
func TestSharedIndexer_ToolCallEntries(t *testing.T) {
	idx := acp.NewSharedIndexer()

	updates := []acp.SessionUpdate{
		{
			SessionId: "sess-tool",
			Role:      "assistant",
			ToolCalls: []acp.ToolCall{
				{
					ToolCallId: "tc-1",
					ToolKind:   acp.ToolKindBash,
					ToolName:   "Bash",
					ToolInput:  `{"command":"ls"}`,
					ToolOutput: `{"output":"file.go"}`,
				},
				{
					ToolCallId: "tc-2",
					ToolKind:   acp.ToolKindFile,
					ToolName:   "Read",
					ToolInput:  `{"path":"main.go"}`,
				},
			},
		},
	}

	entries, err := idx.Index(updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect 1 depth=0 + 2 depth=1
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	parent := entries[0]
	if !parent.HasToolUse {
		t.Errorf("depth=0 entry: HasToolUse should be true")
	}
	if parent.ToolNamesCsv == nil {
		t.Errorf("depth=0 entry: ToolNamesCsv should not be nil")
	} else if !strings.Contains(*parent.ToolNamesCsv, "Bash") {
		t.Errorf("depth=0 entry: ToolNamesCsv %q should contain Bash", *parent.ToolNamesCsv)
	}

	tc1 := entries[1]
	if tc1.Depth != 1 {
		t.Errorf("tool call entry: Depth should be 1, got %d", tc1.Depth)
	}
	if tc1.ParentIndex == nil || *tc1.ParentIndex != 0 {
		t.Errorf("tool call entry: ParentIndex should be 0, got %v", tc1.ParentIndex)
	}
	if tc1.ToolCallId == nil || *tc1.ToolCallId != "tc-1" {
		t.Errorf("tool call entry: ToolCallId should be tc-1, got %v", tc1.ToolCallId)
	}
	if tc1.ToolInput == nil || *tc1.ToolInput != `{"command":"ls"}` {
		t.Errorf("tool call entry: ToolInput mismatch, got %v", tc1.ToolInput)
	}
	if tc1.ToolOutput == nil || *tc1.ToolOutput != `{"output":"file.go"}` {
		t.Errorf("tool call entry: ToolOutput mismatch, got %v", tc1.ToolOutput)
	}

	tc2 := entries[2]
	if tc2.Depth != 1 {
		t.Errorf("second tool call entry: Depth should be 1, got %d", tc2.Depth)
	}
	if tc2.ToolOutput != nil {
		t.Errorf("second tool call entry: ToolOutput should be nil for unanswered call, got %v", tc2.ToolOutput)
	}
}

// TestSharedIndexer_ContentPreviewTruncation verifies long text is capped at
// 500 characters.
func TestSharedIndexer_ContentPreviewTruncation(t *testing.T) {
	idx := acp.NewSharedIndexer()

	longText := strings.Repeat("a", 600)
	updates := []acp.SessionUpdate{
		{
			SessionId: "sess-trunc",
			Role:      "assistant",
			Content:   []acp.ContentBlock{{Type: "text", Content: longText}},
		},
	}

	entries, err := idx.Index(updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries[0].ContentPreview == nil {
		t.Fatal("ContentPreview should not be nil")
	}
	if len([]rune(*entries[0].ContentPreview)) > 500 {
		t.Errorf("ContentPreview exceeds 500 chars: got %d", len(*entries[0].ContentPreview))
	}
}

// TestSharedIndexer_EmptyUpdates verifies that indexing an empty slice returns
// an empty slice without error.
func TestSharedIndexer_EmptyUpdates(t *testing.T) {
	idx := acp.NewSharedIndexer()
	entries, err := idx.Index(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// TestSharedIndexer_EntryIDPropagation verifies EntryId and ParentEntryId are
// forwarded to the protocol.SessionEntry.
func TestSharedIndexer_EntryIDPropagation(t *testing.T) {
	idx := acp.NewSharedIndexer()
	updates := []acp.SessionUpdate{
		{
			SessionId:     "sess-ids",
			Role:          "assistant",
			EntryId:       "msg-uuid-123",
			ParentEntryId: "msg-uuid-000",
			Content:       []acp.ContentBlock{{Type: "text", Content: "hi"}},
		},
	}
	entries, err := idx.Index(updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := entries[0]
	if e.EntryId == nil || *e.EntryId != "msg-uuid-123" {
		t.Errorf("EntryId: got %v, want %q", e.EntryId, "msg-uuid-123")
	}
	if e.ParentEntryId == nil || *e.ParentEntryId != "msg-uuid-000" {
		t.Errorf("ParentEntryId: got %v, want %q", e.ParentEntryId, "msg-uuid-000")
	}
}
