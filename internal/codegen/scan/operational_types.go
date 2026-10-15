package scan

import "github.com/dayvidpham/pasture/internal/codegen/ir"

// OperationalDocument is exact input to the tracker gate, never rewritten.
// Owner names the canonical source; File names the input or staged projection.
type OperationalDocument struct {
	Owner   string
	File    string
	Content []byte
	// Markdown marks assembled prose held in a non-Markdown source file. Its
	// ranges address this exact owned content, not the Go literal's escaping.
	Markdown bool
}

// HistoricalTrackerEntry authorizes one non-operational quotation only. The
// document digest pins its framing as well as the command's exact coordinates.
// A native-call disposition can never supply this authority.
type HistoricalTrackerEntry struct {
	Owner   string         `json:"owner"`
	Section string         `json:"section"`
	Node    string         `json:"node"`
	Range   ir.SourceRange `json:"range"`
	Window  string         `json:"window"`
	Digest  string         `json:"digest"`
}

// TrackerCandidate keeps Goldmark's coordinate vocabulary and exact content.
type TrackerCandidate struct {
	Location ir.Location
	Node     string
	Window   string
	Text     string
	Reason   string
}
