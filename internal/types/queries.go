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
	LastError        *string                       `json:"lastError,omitempty"`
	ActiveSessionCount int                         `json:"activeSessionCount"`
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
