package acp

import (
	"fmt"
	"sort"
)

// Adapter converts agent-specific transcript record bytes into a SessionUpdate.
//
// Each Adapter handles one agent format (e.g. "claude-jsonl", "opencode-json").
// Format() returns the canonical format name, which is used as the registration
// key and is also the string callers pass to GetAdapter.
//
// Parse receives a single raw record (one line for JSONL formats, one JSON
// object for structured formats). It must return a descriptive error that
// includes the byte offset or field name of any parsing failure so that callers
// can locate the problem in the source file.
//
// Implementations must be safe for concurrent use; no shared mutable state.
type Adapter interface {
	// Parse converts a raw record into a SessionUpdate.
	//
	// Returns a non-nil error if the record is malformed, missing required
	// fields, or cannot be decoded. The error must identify:
	//   (1) what field or byte caused the failure,
	//   (2) why decoding failed (unexpected type, missing key, etc.),
	//   (3) how to fix it (expected format or value).
	Parse(record []byte) (SessionUpdate, error)

	// Format returns the canonical name of the format this adapter handles.
	// Example values: "claude-jsonl", "opencode-json".
	Format() string
}

// ─── Static Registry ─────────────────────────────────────────────────────────

// adapters is the compile-time static adapter registry.
//
// New adapters must be added here at compile time. The map is never mutated
// after package initialisation, so no mutex is required for reads.
var adapters = map[string]Adapter{
	"claude-jsonl":  &claudeAdapter{},
	"opencode-json": &openCodeAdapter{},
}

// GetAdapter retrieves the registered adapter for the given format name.
//
// Returns an error if no adapter has been registered for format. The error
// lists all currently registered format names to help the caller choose a
// valid option.
//
// GetAdapter is safe for concurrent use; the underlying map is read-only.
func GetAdapter(format string) (Adapter, error) {
	a, ok := adapters[format]
	if !ok {
		return nil, fmt.Errorf(
			"acp.GetAdapter: unknown format %q — "+
				"registered formats: [%s]; "+
				"add a new adapter to the static registry in internal/acp/adapter.go",
			format,
			joinedFormats(),
		)
	}
	return a, nil
}

// RegisteredFormats returns a sorted slice of all compile-time registered
// format names.
//
// RegisteredFormats is safe for concurrent use.
func RegisteredFormats() []string {
	return joinedFormatSlice()
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// joinedFormats returns a comma-separated list of registered format names.
func joinedFormats() string {
	names := joinedFormatSlice()
	result := ""
	for i, n := range names {
		if i > 0 {
			result += ", "
		}
		result += n
	}
	return result
}

// joinedFormatSlice returns a sorted slice of registered format names.
func joinedFormatSlice() []string {
	names := make([]string, 0, len(adapters))
	for k := range adapters {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
