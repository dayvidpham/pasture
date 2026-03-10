package acp

import (
	"fmt"
	"sort"
	"sync"
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

// ─── Registry ────────────────────────────────────────────────────────────────

var (
	registryMu sync.RWMutex
	registry   = map[string]Adapter{}
)

// RegisterAdapter adds a to the global adapter registry.
//
// The adapter is keyed by a.Format(). Registering a second adapter for the
// same format overwrites the previous entry. This is intentional to allow
// updated adapters to replace older versions during initialization.
//
// RegisterAdapter is safe for concurrent use.
func RegisterAdapter(a Adapter) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[a.Format()] = a
}

// GetAdapter retrieves the registered adapter for the given format name.
//
// Returns an error if no adapter has been registered for format. The error
// lists all currently registered format names to help the caller choose a
// valid option.
//
// GetAdapter is safe for concurrent use.
func GetAdapter(format string) (Adapter, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	a, ok := registry[format]
	if !ok {
		return nil, fmt.Errorf(
			"acp.GetAdapter: unknown format %q — "+
				"registered formats: [%s]; "+
				"register a new adapter with acp.RegisterAdapter",
			format,
			joinedFormats(),
		)
	}
	return a, nil
}

// RegisteredFormats returns a sorted slice of all currently registered format names.
//
// RegisteredFormats is safe for concurrent use.
func RegisteredFormats() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return joinedFormatSlice()
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// joinedFormats returns a comma-separated list of registered format names.
// Caller must hold registryMu (read or write).
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
// Caller must hold registryMu (read or write).
func joinedFormatSlice() []string {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
