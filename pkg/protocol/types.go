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

// PhaseId identifies a phase in the 12-phase epoch lifecycle by its name.
// The position (pX number) is determined by the phase's index in a Pipeline,
// not by the value of the PhaseId itself.
type PhaseId string

const (
	PhaseRequest      PhaseId = "request"
	PhaseElicit       PhaseId = "elicit"
	PhasePropose      PhaseId = "propose"
	PhaseReview       PhaseId = "review"
	PhasePlanReview   PhaseId = "plan-review"
	PhaseRatify       PhaseId = "ratify"
	PhaseHandoff      PhaseId = "handoff"
	PhaseImplPlan     PhaseId = "impl-plan"
	PhaseWorkerSlices PhaseId = "worker-slices"
	PhaseCodeReview   PhaseId = "code-review"
	PhaseImplUAT      PhaseId = "impl-uat"
	PhaseLanding      PhaseId = "landing"
	PhaseComplete     PhaseId = "complete"
)

// AllPhaseIds is the ordered slice of all valid PhaseId values (pipeline + terminal).
// Useful for iteration, completeness checks, and building lookup tables.
var AllPhaseIds = []PhaseId{
	PhaseRequest,
	PhaseElicit,
	PhasePropose,
	PhaseReview,
	PhasePlanReview,
	PhaseRatify,
	PhaseHandoff,
	PhaseImplPlan,
	PhaseWorkerSlices,
	PhaseCodeReview,
	PhaseImplUAT,
	PhaseLanding,
	PhaseComplete,
}

// IsValid reports whether p is a known PhaseId value.
func (p PhaseId) IsValid() bool {
	switch p {
	case PhaseRequest, PhaseElicit, PhasePropose, PhaseReview, PhasePlanReview,
		PhaseRatify, PhaseHandoff, PhaseImplPlan, PhaseWorkerSlices,
		PhaseCodeReview, PhaseImplUAT, PhaseLanding, PhaseComplete:
		return true
	}
	return false
}

// String returns the wire-format string value of p.
func (p PhaseId) String() string { return string(p) }

// ─── Pipeline ─────────────────────────────────────────────────────────────────

// Pipeline is an ordered sequence of phases. The 0-based index of a PhaseId
// in the slice determines its 1-based phase number (pX). PhaseComplete is a
// terminal state and is NOT included in the pipeline itself.
type Pipeline []PhaseId

// DefaultPipeline is the standard 12-phase pasture protocol pipeline.
// The index in this slice determines the pX number (0-based index + 1).
var DefaultPipeline = Pipeline{
	PhaseRequest,      // p1
	PhaseElicit,       // p2
	PhasePropose,      // p3
	PhaseReview,       // p4
	PhasePlanReview,   // p5
	PhaseRatify,       // p6
	PhaseHandoff,      // p7
	PhaseImplPlan,     // p8
	PhaseWorkerSlices, // p9
	PhaseCodeReview,   // p10
	PhaseImplUAT,      // p11
	PhaseLanding,      // p12
}

// Index returns the 0-based index of id in the pipeline, or -1 if not found.
func (p Pipeline) Index(id PhaseId) int {
	for i, phase := range p {
		if phase == id {
			return i
		}
	}
	return -1
}

// Contains reports whether the pipeline contains id.
func (p Pipeline) Contains(id PhaseId) bool {
	return p.Index(id) >= 0
}

// PhaseNumber returns the 1-based phase number for id, or -1 if not found.
func (p Pipeline) PhaseNumber(id PhaseId) int {
	i := p.Index(id)
	if i < 0 {
		return -1
	}
	return i + 1
}

// PhaseAt returns the PhaseId at the given 1-based phase number.
// Returns ("", false) if number is out of range.
func (p Pipeline) PhaseAt(number int) (PhaseId, bool) {
	if number < 1 || number > len(p) {
		return "", false
	}
	return p[number-1], true
}

// Next returns the next phase after id in the pipeline.
// Returns PhaseComplete if id is the last phase or not found.
func (p Pipeline) Next(id PhaseId) PhaseId {
	i := p.Index(id)
	if i < 0 || i >= len(p)-1 {
		return PhaseComplete
	}
	return p[i+1]
}

// ─── ParsePhaseId ─────────────────────────────────────────────────────────────

// ParsePhaseId parses a flexible phase input string into a PhaseId.
//
// Supported formats (case-insensitive):
//   - Name only:   "request", "elicit", "propose", "review", "plan-review",
//     "ratify", "handoff", "impl-plan", "worker-slices",
//     "code-review", "impl-uat", "landing", "complete"
//   - pX format:   "p1", "p2", ..., "p12" (resolved via DefaultPipeline)
//   - pX-name:     "p1-request", "p2-elicit", ... (legacy; name portion used)
//   - Number only: "1", "2", ..., "12"
//
// Returns an error if the input does not match any known format.
func ParsePhaseId(s string) (PhaseId, error) {
	lower := strings.ToLower(strings.TrimSpace(s))

	// Direct name-only match.
	switch lower {
	case "request":
		return PhaseRequest, nil
	case "elicit":
		return PhaseElicit, nil
	case "propose":
		return PhasePropose, nil
	case "review":
		return PhaseReview, nil
	case "plan-review", "planreview", "plan_review":
		return PhasePlanReview, nil
	case "ratify":
		return PhaseRatify, nil
	case "handoff":
		return PhaseHandoff, nil
	case "impl-plan", "implplan", "impl_plan":
		return PhaseImplPlan, nil
	case "worker-slices", "workerslices", "worker_slices", "slice", "slices":
		return PhaseWorkerSlices, nil
	case "code-review", "codereview", "code_review":
		return PhaseCodeReview, nil
	case "impl-uat", "impluat", "impl_uat", "uat":
		return PhaseImplUAT, nil
	case "landing":
		return PhaseLanding, nil
	case "complete":
		return PhaseComplete, nil
	}

	// pX format: "p1", "p2", ..., "p12"
	if strings.HasPrefix(lower, "p") {
		rest := lower[1:]

		// Find end of digits.
		digEnd := 0
		for digEnd < len(rest) && rest[digEnd] >= '0' && rest[digEnd] <= '9' {
			digEnd++
		}

		if digEnd > 0 {
			numStr := rest[:digEnd]
			suffix := rest[digEnd:]

			// Strip optional separator (hyphen or underscore) before name.
			if len(suffix) > 0 && (suffix[0] == '-' || suffix[0] == '_') {
				suffix = suffix[1:]
			}

			// If there's a name suffix, parse it recursively (pX-name format).
			if suffix != "" {
				return ParsePhaseId(suffix)
			}

			// Pure pX format — resolve via DefaultPipeline.
			num := 0
			for _, ch := range numStr {
				num = num*10 + int(ch-'0')
			}
			phase, ok := DefaultPipeline.PhaseAt(num)
			if ok {
				return phase, nil
			}
		}
	}

	// Number-only format: "1", "2", ..., "12"
	num := 0
	allDigits := len(lower) > 0
	for _, ch := range lower {
		if ch < '0' || ch > '9' {
			allDigits = false
			break
		}
		num = num*10 + int(ch-'0')
	}
	if allDigits && len(lower) > 0 {
		phase, ok := DefaultPipeline.PhaseAt(num)
		if ok {
			return phase, nil
		}
	}

	return "", fmt.Errorf(
		"protocol.ParsePhaseId: unrecognized phase %q — "+
			"valid formats: \"p1\"..\"p12\", \"complete\", \"1\"..\"12\", "+
			"or a phase name like \"request\", \"elicit\", \"propose\", \"code-review\"",
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
//
// DedupKey is the OPTIONAL deterministic deduplication key (see DedupKey).
// When set, the SQLite trail writes it into the dedup_key column with an
// ON CONFLICT … DO NOTHING upsert, so a crash-replay of the emitting durable
// step records the row exactly once. Ordinary (non-engine) callers leave it
// empty; the column is then NULL and the partial unique index ignores the row,
// preserving the legacy insert-always behaviour.
type AuditEvent struct {
	EpochId   string         `json:"epochId"`
	Phase     PhaseId        `json:"phase"`
	Role      string         `json:"role"`
	EventType EventType      `json:"eventType"`
	Payload   map[string]any `json:"payload"`
	Timestamp time.Time      `json:"timestamp"`
	DedupKey  string         `json:"dedupKey,omitempty"`
}
