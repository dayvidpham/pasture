package protocol

import "time"

// TransitionRecord is an immutable audit entry for a single phase transition.
//
// Success is true for a completed phase advance, false for a failed attempt
// (e.g. constraint violation). All programmatic success/failure checks MUST
// use this boolean field, not any string prefix in ConditionMet.
type TransitionRecord struct {
	FromPhase    PhaseId   `json:"fromPhase"`
	ToPhase      PhaseId   `json:"toPhase"`
	Timestamp    time.Time `json:"timestamp"`
	TriggeredBy  string    `json:"triggeredBy"`
	ConditionMet string    `json:"conditionMet"`
	Success      bool      `json:"success"`
}

// EpochState holds the runtime state of a single epoch workflow.
//
// Tracks the current phase, completed phases, review votes, blocker count,
// current role, and full transition history. Mutable — updated by the durable
// engine on each transition; serialized into the projection that queries read.
type EpochState struct {
	EpochId           string                  `json:"epochId"`
	CurrentPhase      PhaseId                 `json:"currentPhase"`
	CurrentRole       RoleId                  `json:"currentRole"`
	CompletedPhases   []PhaseId               `json:"completedPhases"`
	ReviewVotes       map[ReviewAxis]VoteType `json:"reviewVotes"`
	BlockerCount      int                     `json:"blockerCount"`
	TransitionHistory []TransitionRecord      `json:"transitionHistory"`
	// ReviewCycles tracks per-slice review-fix cycle history.
	// Key: slice task ID. Value: ordered list of review rounds for that slice.
	ReviewCycles       map[string][]ReviewCycleRecord `json:"reviewCycles,omitempty"`
	LastError          *string                        `json:"lastError,omitempty"`
	ActiveSessionCount int                            `json:"activeSessionCount"`
}

// ReviewCycleRecord tracks the state of a single review-fix cycle for one slice.
//
// The supervisor creates one record per (slice, round) pair. It captures
// which reviewers participated, their votes, and the count of findings by
// severity. This enables the supervisor to enforce the max-3-cycles constraint
// and determine whether a clean exit was reached via IsCleanExit().
type ReviewCycleRecord struct {
	// SliceId is the task ID of the slice being reviewed.
	SliceId string `json:"sliceId"`
	// Round is the 1-based review cycle number for this slice (max 3).
	Round int `json:"round"`
	// Votes maps each reviewer axis to its vote for this round.
	Votes map[ReviewAxis]VoteType `json:"votes"`
	// FindingCounts maps severity level to the number of findings.
	FindingCounts map[SeverityLevel]int `json:"findingCounts"`
	// Timestamp records when the review round completed.
	Timestamp time.Time `json:"timestamp"`
}

// IsCleanExit returns true if the review cycle is clean: all 3 axes voted
// ACCEPT AND there are 0 BLOCKERs and 0 IMPORTANTs.
// This is the single authoritative check for the review-wave workflow.
func (r ReviewCycleRecord) IsCleanExit() bool {
	for _, axis := range AllReviewAxes {
		if r.Votes[axis] != VoteAccept {
			return false
		}
	}
	return r.FindingCounts[SeverityBlocker] == 0 && r.FindingCounts[SeverityImportant] == 0
}

// QueryStateResult is a serialization-safe snapshot of epoch state returned
// by the full-state query. Designed for CLI consumers.
//
// AvailableTransitions lists the target PhaseIds reachable from the current
// phase given the current vote/blocker state.
type QueryStateResult struct {
	CurrentPhase         PhaseId                 `json:"currentPhase"`
	CurrentRole          RoleId                  `json:"currentRole"`
	TransitionHistory    []TransitionRecord      `json:"transitionHistory"`
	Votes                map[ReviewAxis]VoteType `json:"votes"`
	LastError            *string                 `json:"lastError,omitempty"`
	AvailableTransitions []PhaseId               `json:"availableTransitions"`
	ActiveSessionCount   int                     `json:"activeSessionCount"`
}
