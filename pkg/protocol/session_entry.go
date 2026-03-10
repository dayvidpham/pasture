package protocol

// SessionEntry represents a single indexed entry within a session transcript.
//
// Aligned with agent-data-leverage pkg/schema.SessionEntry schema.
// All optional fields use pointer types to distinguish absent from zero-value.
// JSON tags use camelCase aligned with the ACP content model and Python output.
//
// Maps 1:1 to a row in the session_entries table in the audit SQLite backend.
type SessionEntry struct {
	SessionID      string  `json:"sessionId"`
	EntryIndex     int     `json:"entryIndex"`
	Provider       string  `json:"provider"`
	EntryType      string  `json:"entryType"`
	Role           string  `json:"role"`
	TimestampMs    *int64  `json:"timestampMs,omitempty"`
	ContentPreview *string `json:"contentPreview,omitempty"` // max 500 chars
	TokensIn       *int    `json:"tokensIn,omitempty"`
	TokensOut      *int    `json:"tokensOut,omitempty"`
	HasToolUse     bool    `json:"hasToolUse"`
	ToolKind       *string `json:"toolKind,omitempty"`     // ACP-aligned tool classification
	ToolNamesCsv   *string `json:"toolNamesCsv,omitempty"` // comma-separated tool names
	HasThinking    bool    `json:"hasThinking"`
	IsError        bool    `json:"isError"`
	StopReason     *string `json:"stopReason,omitempty"`    // ACP per-turn stop reason
	RawByteLength  *int    `json:"rawByteLength,omitempty"` // raw JSON byte count
	ToolCallID     *string `json:"toolCallId,omitempty"`    // MCP correlation
	EntryID        *string `json:"entryId,omitempty"`       // provider-native ID
	ParentEntryID  *string `json:"parentEntryId,omitempty"` // parent entry link
	Depth          int     `json:"depth"`                   // 0 = message, 1 = content part
	ParentIndex    *int    `json:"parentIndex,omitempty"`   // entryIndex of parent (nil for depth=0)
	ToolInput      *string `json:"toolInput,omitempty"`     // tool_use input JSON
	ToolOutput     *string `json:"toolOutput,omitempty"`    // tool_result output JSON
	Extra          *string `json:"extra,omitempty"`         // JSON overflow for provider-specific data
}
