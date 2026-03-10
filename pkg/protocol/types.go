// Package protocol defines the public convergence types for the Pasture
// multi-agent orchestration protocol.
//
// These types are consumed by all other packages (formatters, handlers,
// workflows, audit, ACP adapter). They are designed to be serialization-safe
// for Temporal's JSONPlainPayloadConverter and standard encoding/json.
//
// JSON tags use camelCase to match Python aura-protocol output format.
package protocol

import (
	"fmt"
	"strings"
	"time"
)

// ─── PhaseId ─────────────────────────────────────────────────────────────────

// PhaseId identifies a phase in the 12-phase epoch lifecycle.
// Values are short wire-format strings (e.g. "p1", "p2", ..., "complete").
type PhaseId string

const (
	P1_Request     PhaseId = "p1"
	P2_Elicit      PhaseId = "p2"
	P3_Propose     PhaseId = "p3"
	P4_Review      PhaseId = "p4"
	P5_Uat         PhaseId = "p5"
	P6_Ratify      PhaseId = "p6"
	P7_Handoff     PhaseId = "p7"
	P8_ImplPlan    PhaseId = "p8"
	P9_Slice       PhaseId = "p9"
	P10_CodeReview PhaseId = "p10"
	P11_ImplUat    PhaseId = "p11"
	P12_Landing    PhaseId = "p12"
	Complete       PhaseId = "complete"
)

// AllPhaseIds is the ordered slice of all valid PhaseId values.
// Useful for iteration, completeness checks, and building lookup tables.
var AllPhaseIds = []PhaseId{
	P1_Request,
	P2_Elicit,
	P3_Propose,
	P4_Review,
	P5_Uat,
	P6_Ratify,
	P7_Handoff,
	P8_ImplPlan,
	P9_Slice,
	P10_CodeReview,
	P11_ImplUat,
	P12_Landing,
	Complete,
}

// IsValid reports whether p is a known PhaseId value.
func (p PhaseId) IsValid() bool {
	switch p {
	case P1_Request, P2_Elicit, P3_Propose, P4_Review, P5_Uat,
		P6_Ratify, P7_Handoff, P8_ImplPlan, P9_Slice,
		P10_CodeReview, P11_ImplUat, P12_Landing, Complete:
		return true
	}
	return false
}

// String returns the wire-format string value of p.
func (p PhaseId) String() string { return string(p) }

// ParsePhaseId parses a flexible phase input string into a PhaseId.
//
// Supported formats (case-insensitive):
//   - Short wire format:   "p1", "p2", ..., "p12", "complete"
//   - Number only:         "1", "2", ..., "12"
//   - Name only:           "request", "elicit", "propose", "review", "uat",
//     "ratify", "handoff", "implplan", "slice",
//     "codereview", "impluat", "landing", "complete"
//   - PascalCase:          "P1_Request", "P2_Elicit", ...
//   - Full underscore:     "p1_request", "p2_elicit", ...
//
// Returns an error if the input does not match any known format.
func ParsePhaseId(s string) (PhaseId, error) {
	lower := strings.ToLower(strings.TrimSpace(s))

	// Direct match on wire format (e.g. "p1", "complete").
	switch lower {
	case "p1":
		return P1_Request, nil
	case "p2":
		return P2_Elicit, nil
	case "p3":
		return P3_Propose, nil
	case "p4":
		return P4_Review, nil
	case "p5":
		return P5_Uat, nil
	case "p6":
		return P6_Ratify, nil
	case "p7":
		return P7_Handoff, nil
	case "p8":
		return P8_ImplPlan, nil
	case "p9":
		return P9_Slice, nil
	case "p10":
		return P10_CodeReview, nil
	case "p11":
		return P11_ImplUat, nil
	case "p12":
		return P12_Landing, nil
	case "complete":
		return Complete, nil
	}

	// Number-only format (e.g. "1", "12").
	switch lower {
	case "1":
		return P1_Request, nil
	case "2":
		return P2_Elicit, nil
	case "3":
		return P3_Propose, nil
	case "4":
		return P4_Review, nil
	case "5":
		return P5_Uat, nil
	case "6":
		return P6_Ratify, nil
	case "7":
		return P7_Handoff, nil
	case "8":
		return P8_ImplPlan, nil
	case "9":
		return P9_Slice, nil
	case "10":
		return P10_CodeReview, nil
	case "11":
		return P11_ImplUat, nil
	case "12":
		return P12_Landing, nil
	}

	// Normalized name matching: strip "p<n>_" prefix then match by name.
	// Handles: "request", "p1_request", "P1_Request", "p1request"
	//
	// First, strip a leading "p<digits>_" or "p<digits>" prefix if present.
	normalized := lower
	if strings.HasPrefix(normalized, "p") {
		// Skip the 'p' and any digits that follow.
		rest := normalized[1:]
		digEnd := 0
		for digEnd < len(rest) && rest[digEnd] >= '0' && rest[digEnd] <= '9' {
			digEnd++
		}
		if digEnd > 0 {
			// Strip optional trailing underscore separator.
			suffix := rest[digEnd:]
			if strings.HasPrefix(suffix, "_") {
				suffix = suffix[1:]
			}
			if suffix != "" {
				normalized = suffix
			}
		}
	}

	switch normalized {
	case "request":
		return P1_Request, nil
	case "elicit":
		return P2_Elicit, nil
	case "propose":
		return P3_Propose, nil
	case "review":
		return P4_Review, nil
	case "uat":
		return P5_Uat, nil
	case "ratify":
		return P6_Ratify, nil
	case "handoff":
		return P7_Handoff, nil
	case "implplan", "impl_plan":
		return P8_ImplPlan, nil
	case "slice":
		return P9_Slice, nil
	case "codereview", "code_review":
		return P10_CodeReview, nil
	case "impluat", "impl_uat":
		return P11_ImplUat, nil
	case "landing":
		return P12_Landing, nil
	}

	return "", fmt.Errorf(
		"protocol.ParsePhaseId: unrecognized phase %q — "+
			"valid formats: \"p1\"..\"p12\", \"complete\", \"1\"..\"12\", "+
			"or a phase name like \"request\", \"elicit\", \"propose\"",
		s,
	)
}

// ─── EventType ───────────────────────────────────────────────────────────────

// EventType classifies an audit event in the dual-write trail.
type EventType string

const (
	EventPhaseTransition    EventType = "PhaseTransition"
	EventPhaseAdvance       EventType = "PhaseAdvance"
	EventVoteRecorded       EventType = "VoteRecorded"
	EventConstraintChecked  EventType = "ConstraintChecked"
	EventSliceStarted       EventType = "SliceStarted"
	EventSliceCompleted     EventType = "SliceCompleted"
	EventSessionRegistered  EventType = "SessionRegistered"
	EventReviewCycleStarted EventType = "ReviewCycleStarted"
)

// AllEventTypes is the ordered slice of all valid EventType values.
var AllEventTypes = []EventType{
	EventPhaseTransition,
	EventPhaseAdvance,
	EventVoteRecorded,
	EventConstraintChecked,
	EventSliceStarted,
	EventSliceCompleted,
	EventSessionRegistered,
	EventReviewCycleStarted,
}

// IsValid reports whether e is a known EventType value.
func (e EventType) IsValid() bool {
	switch e {
	case EventPhaseTransition, EventPhaseAdvance, EventVoteRecorded,
		EventConstraintChecked, EventSliceStarted, EventSliceCompleted,
		EventSessionRegistered, EventReviewCycleStarted:
		return true
	}
	return false
}

// ─── AuditEvent ──────────────────────────────────────────────────────────────

// AuditEvent is a generic audit trail event emitted by epoch workflows and
// activities. JSON tags use camelCase to match Python aura-protocol output.
type AuditEvent struct {
	EpochID   string            `json:"epochId"`
	Phase     PhaseId           `json:"phase"`
	Role      string            `json:"role"`
	EventType EventType         `json:"eventType"`
	Payload   map[string]any    `json:"payload"`
	Timestamp time.Time         `json:"timestamp"`
}
