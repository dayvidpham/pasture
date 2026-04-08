package types

import (
	"time"

	"github.com/dayvidpham/pasture/pkg/protocol"
)

// TransitionRecord is an immutable audit entry for a single phase transition.
//
// Success is true for a completed phase advance, false for a failed attempt
// (e.g. constraint violation). All programmatic success/failure checks MUST
// use this boolean field, not any string prefix in ConditionMet.
type TransitionRecord struct {
	FromPhase    protocol.PhaseId `json:"fromPhase"`
	ToPhase      protocol.PhaseId `json:"toPhase"`
	Timestamp    time.Time        `json:"timestamp"`
	TriggeredBy  string           `json:"triggeredBy"`
	ConditionMet string           `json:"conditionMet"`
	Success      bool             `json:"success"`
}

// EpochState holds the runtime state of a single epoch workflow.
//
// Tracks the current phase, completed phases, review votes, blocker count,
// current role, and full transition history. Mutable — updated by signal
// handlers within EpochWorkflow.
type EpochState struct {
	EpochID          string                        `json:"epochId"`
	CurrentPhase     protocol.PhaseId              `json:"currentPhase"`
	CurrentRole      RoleId                        `json:"currentRole"`
	CompletedPhases  []protocol.PhaseId            `json:"completedPhases"`
	ReviewVotes      map[ReviewAxis]VoteType        `json:"reviewVotes"`
	BlockerCount     int                           `json:"blockerCount"`
	TransitionHistory []TransitionRecord            `json:"transitionHistory"`
	// ReviewCycles tracks per-slice review-fix cycle history.
	// Key: slice task ID. Value: ordered list of review rounds for that slice.
	ReviewCycles    map[string][]ReviewCycleRecord `json:"reviewCycles,omitempty"`
	LastError        *string                       `json:"lastError,omitempty"`
	ActiveSessionCount int                         `json:"activeSessionCount"`
}

// ReviewCycleRecord tracks the state of a single review-fix cycle for one slice.
//
// The supervisor creates one record per (slice, round) pair. It captures
// which reviewers participated, their votes, and the count of findings by
// severity. This enables the supervisor to enforce the max-3-cycles constraint
// and determine whether a clean exit (0 BLOCKERs + 0 IMPORTANTs) was reached.
type ReviewCycleRecord struct {
	// SliceID is the Beads task ID of the slice being reviewed.
	SliceID string `json:"sliceId"`
	// Round is the 1-based review cycle number for this slice (max 3).
	Round int `json:"round"`
	// Votes maps each reviewer axis to its vote for this round.
	Votes map[ReviewAxis]VoteType `json:"votes"`
	// FindingCounts maps severity level to the number of findings.
	// Keys: "blocker", "important", "minor".
	FindingCounts map[string]int `json:"findingCounts"`
	// Clean is true when all 3 axes voted ACCEPT and FindingCounts has
	// 0 blockers and 0 importants. Computed by the supervisor after all
	// votes are in — callers MUST use this field for programmatic checks.
	Clean bool `json:"clean"`
	// Timestamp records when the review round completed.
	Timestamp time.Time `json:"timestamp"`
}

// IsCleanExit returns true if the review cycle had 0 BLOCKERs and 0 IMPORTANTs.
// This is the "clean review" exit condition for the Ride the Wave workflow.
func (r ReviewCycleRecord) IsCleanExit() bool {
	return r.FindingCounts["blocker"] == 0 && r.FindingCounts["important"] == 0
}

// QueryStateResult is a serialization-safe snapshot of epoch state returned
// by the full_state Temporal query. Designed for CLI consumers (pasture-msg).
//
// AvailableTransitions lists the target PhaseIds reachable from the current
// phase given the current vote/blocker state.
type QueryStateResult struct {
	CurrentPhase         protocol.PhaseId   `json:"currentPhase"`
	CurrentRole          RoleId             `json:"currentRole"`
	TransitionHistory    []TransitionRecord `json:"transitionHistory"`
	Votes                map[ReviewAxis]VoteType `json:"votes"`
	LastError            *string            `json:"lastError,omitempty"`
	AvailableTransitions []protocol.PhaseId `json:"availableTransitions"`
	ActiveSessionCount   int                `json:"activeSessionCount"`
}
