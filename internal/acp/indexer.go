package acp

import (
	"fmt"
	"strings"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// SharedIndexer converts a slice of ACP SessionUpdate events into a flat list
// of protocol.SessionEntry rows suitable for persistence in the audit trail.
//
// The indexer is stateless and safe for concurrent use. Each call to Index
// processes only the provided updates; no state is retained between calls.
//
// Conversion rules:
//   - Each SessionUpdate with no ToolCalls produces one depth=0 SessionEntry.
//   - Each ToolCall within a SessionUpdate produces one depth=1 SessionEntry
//     whose ParentIndex refers to the depth=0 entry generated for the same update.
//   - ContentPreview is truncated to 500 characters.
//   - ToolNamesCsv collects all unique tool names for the update (depth=0 entry).
type SharedIndexer struct{}

// NewSharedIndexer constructs a ready-to-use SharedIndexer.
func NewSharedIndexer() *SharedIndexer {
	return &SharedIndexer{}
}

// Index converts the provided SessionUpdate slice into protocol.SessionEntry rows.
//
// Returns a non-nil error only if an individual update cannot be converted
// (malformed data). Partial results up to the first error are not returned;
// the caller receives either the full result set or an error.
//
// Index guarantees: len(entries) >= len(updates) (each update contributes at
// least one depth=0 entry plus one depth=1 entry per ToolCall).
func (idx *SharedIndexer) Index(updates []SessionUpdate) ([]protocol.SessionEntry, error) {
	// Pre-allocate: 1 depth-0 entry per update + 1 depth-1 per tool call.
	estimated := len(updates)
	for i := range updates {
		estimated += len(updates[i].ToolCalls)
	}
	entries := make([]protocol.SessionEntry, 0, estimated)

	for i, u := range updates {
		parentIdx := len(entries)

		// ── depth=0: message-level entry ──────────────────────────────────
		msgEntry, err := messageEntry(u, parentIdx)
		if err != nil {
			return nil, fmt.Errorf(
				"acp.SharedIndexer.Index: update[%d] (sessionId=%q): %w",
				i, u.SessionId, err,
			)
		}
		entries = append(entries, msgEntry)

		// ── depth=1: one entry per tool call ──────────────────────────────
		for j, tc := range u.ToolCalls {
			tcEntry := toolCallEntry(u, tc, parentIdx, parentIdx+j+1)
			entries = append(entries, tcEntry)
		}
	}

	return entries, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// messageEntry builds the depth=0 SessionEntry for a SessionUpdate.
func messageEntry(u SessionUpdate, entryIndex int) (protocol.SessionEntry, error) {
	e := protocol.SessionEntry{
		SessionId:  u.SessionId,
		EntryIndex: entryIndex,
		Provider:   "acp",
		EntryType:  "message",
		Role:       u.Role,
		Depth:      0,
		HasToolUse: len(u.ToolCalls) > 0,
		IsError:    u.IsError,
	}

	// Timestamp
	if u.Timestamp != 0 {
		ts := u.Timestamp
		e.TimestampMs = &ts
	}

	// Content preview (first text block, max 500 chars).
	// ContentBlock.Text is the single canonical field — the spec wire key
	// "text" decodes into it directly, so no per-call fallback is needed here.
	for _, cb := range u.Content {
		if cb.Type == "text" || cb.Type == "thinking" {
			if cb.Text != "" {
				preview := truncate(cb.Text, 500)
				e.ContentPreview = &preview
			}
			break
		}
	}

	// Token counts
	if u.TokensIn > 0 {
		e.TokensIn = &u.TokensIn
	}
	if u.TokensOut > 0 {
		e.TokensOut = &u.TokensOut
	}

	// Stop reason
	if u.StopReason != "" {
		sr := string(u.StopReason)
		e.StopReason = &sr
	}

	// Tool summary
	if len(u.ToolCalls) > 0 {
		names := make([]string, 0, len(u.ToolCalls))
		kindSeen := make(map[ToolKind]struct{})
		for _, tc := range u.ToolCalls {
			names = append(names, tc.ToolName)
			kindSeen[tc.ToolKind] = struct{}{}
		}
		csv := strings.Join(names, ",")
		e.ToolNamesCsv = &csv

		// Primary tool kind: pick the most common or first seen.
		// Using first ToolCall's kind as the representative value.
		tk := string(u.ToolCalls[0].ToolKind)
		e.ToolKind = &tk
		_ = kindSeen // available for future multi-kind aggregation
	}

	// EntryId / ParentEntryId
	if u.EntryId != "" {
		id := u.EntryId
		e.EntryId = &id
	}
	if u.ParentEntryId != "" {
		pid := u.ParentEntryId
		e.ParentEntryId = &pid
	}

	// Raw byte length: not available at this level without original bytes.
	// Callers that need raw byte tracking should set RawByteLength before
	// persisting (the field is preserved here as nil).

	return e, nil
}

// toolCallEntry builds the depth=1 SessionEntry for a single ToolCall.
func toolCallEntry(u SessionUpdate, tc ToolCall, parentIndex, entryIndex int) protocol.SessionEntry {
	e := protocol.SessionEntry{
		SessionId:   u.SessionId,
		EntryIndex:  entryIndex,
		Provider:    "acp",
		EntryType:   "tool_call",
		Role:        u.Role,
		Depth:       1,
		HasToolUse:  true,
		ParentIndex: &parentIndex,
	}

	if u.Timestamp != 0 {
		ts := u.Timestamp
		e.TimestampMs = &ts
	}

	tk := string(tc.ToolKind)
	e.ToolKind = &tk
	e.ToolNamesCsv = &tc.ToolName

	if tc.ToolCallId != "" {
		id := tc.ToolCallId
		e.ToolCallId = &id
	}
	if tc.ToolInput != "" {
		inp := tc.ToolInput
		e.ToolInput = &inp
	}
	if tc.ToolOutput != "" {
		out := tc.ToolOutput
		e.ToolOutput = &out
	}

	// Content preview: show tool name as a human-readable hint.
	preview := fmt.Sprintf("[tool_call] %s", tc.ToolName)
	e.ContentPreview = &preview

	return e
}

// truncate returns s trimmed to at most maxLen characters (rune-aware).
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
