// Package codegen — validator interface types and stubs.
//
// This file defines the public types for the 3-layer schema.xml validator
// (structural, referential integrity, semantic). The ValidateSchema and
// ValidateTree functions are stubs here; SLICE-C provides the implementation.
//
// Public API:
//
//	ValidateSchema(r io.Reader) ([]ValidationError, error)
//	ValidateTree(root *XMLNode) []ValidationError
package codegen

import (
	"encoding/xml"
	"io"
)

// ErrorLayer categorizes validation errors by their detection layer.
// Values match the Python ErrorLayer enum wire values.
type ErrorLayer string

const (
	// LayerStructural covers missing required attributes, duplicate IDs,
	// and malformed XML.
	LayerStructural ErrorLayer = "Structural"

	// LayerReferential covers references (phase-ref, role-ref, etc.) that
	// point to IDs not defined elsewhere in the document.
	LayerReferential ErrorLayer = "Referential Integrity"

	// LayerSemantic covers logical inconsistencies such as out-of-order
	// phase numbers, duplicate axis letters, or invalid enum values.
	LayerSemantic ErrorLayer = "Semantic"
)

// ValidationError represents a single schema validation finding.
// All three fields are always populated; an empty ElementPath indicates the
// error is at the document root level.
type ValidationError struct {
	// Layer is the detection layer that produced this error.
	Layer ErrorLayer

	// ElementPath is the XPath-style description of the offending element,
	// e.g. "phase[@id='p1']/substep[@id='s1_1']".
	ElementPath string

	// Message describes what is wrong, why, and how to fix it.
	Message string
}

// SchemaIndex holds all IDs and metadata extracted during structural
// validation. It is populated by buildIndex (SLICE-C) and consumed by
// checkRefs and checkSemantics. Mirrors the Python SchemaIndex dataclass.
type SchemaIndex struct {
	// ── ID sets ───────────────────────────────────────────────────────────

	PhaseIDs      map[string]bool
	SubstepIDs    map[string]bool
	LabelIDs      map[string]bool
	RoleIDs       map[string]bool
	CommandIDs    map[string]bool
	AxisIDs       map[string]bool
	HandoffIDs    map[string]bool
	ConstraintIDs map[string]bool
	DocumentIDs   map[string]bool
	TeamIDs       map[string]bool
	SeverityIDs   map[string]bool

	// EnumValueIDs maps enum name → set of value IDs defined within it.
	EnumValueIDs map[string]map[string]bool

	// ── Metadata for semantic checks ──────────────────────────────────────

	// PhaseNumbers maps phase_id → numeric order (e.g. "p1" → 1).
	PhaseNumbers map[string]int

	// PhaseDomains maps phase_id → domain string (e.g. "p1" → "user").
	PhaseDomains map[string]string

	// PhaseSubstepOrders maps phase_id → ordered list of substep entries.
	PhaseSubstepOrders map[string][]SubstepOrderEntry

	// LabelValues maps label_id → value string (e.g. "L-p1s1_1" → "aura:p1-user:s1_1-request").
	LabelValues map[string]string

	// AxisLetters maps axis_id → letter string (e.g. "axis-correctness" → "A").
	AxisLetters map[string]string

	// RolePhaseRefs maps role_id → set of phase_ids that role owns.
	RolePhaseRefs map[string]map[string]bool

	// StartupStepOrders maps substep_id → slice of step order values found
	// inside that substep's <startup-sequence>.
	StartupStepOrders map[string][]int
}

// SubstepOrderEntry holds ordering metadata for a single substep within a
// phase. Mirrors the Python tuple (id, order, execution) from phase_substep_orders.
type SubstepOrderEntry struct {
	ID        string
	Order     int
	Execution string // "sequential" or "parallel"
}

// XMLNode is a generic XML tree node for validator tree operations.
// It can represent any element in the schema document and is used by
// ValidateTree to walk the document without requiring concrete struct types.
type XMLNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []*XMLNode `xml:",any"`
	Text     string     `xml:",chardata"`
}

// ValidateSchema reads XML from r and validates it against the 3-layer
// Aura Protocol schema rules.
//
// Error contract:
//   - io.Reader read failure → (nil, error)
//   - XML parse failure → ([]ValidationError{{Layer: LayerStructural, ...}}, nil)
//   - Violations found → ([]ValidationError{...}, nil) with len > 0
//   - Valid schema → (nil, nil)
//
// This function is a stub. SLICE-C provides the implementation.
func ValidateSchema(r io.Reader) ([]ValidationError, error) {
	return nil, nil
}

// ValidateTree validates a parsed XMLNode tree against the 3-layer Aura
// Protocol schema rules and returns all violations found.
//
// Returns nil when the tree is valid.
//
// This function is a stub. SLICE-C provides the implementation.
func ValidateTree(root *XMLNode) []ValidationError {
	return nil
}
