package types

import "github.com/dayvidpham/pasture/pkg/protocol"

// PhaseAdvanceSignal is the payload for the advance_phase Temporal signal.
//
// Sent by pasture-msg (or any authorized caller) to transition the epoch
// workflow to a new phase. TriggeredBy identifies who or what sent the signal
// (e.g. a role name or external trigger). ConditionMet describes the
// transition condition from the protocol table that was satisfied.
type PhaseAdvanceSignal struct {
	ToPhase      protocol.PhaseId `json:"toPhase"`
	TriggeredBy  string           `json:"triggeredBy"`
	ConditionMet string           `json:"conditionMet"`
}

// ReviewVoteSignal is the payload for the submit_vote Temporal signal.
//
// ReviewerID must be the unique identifier for the reviewer agent submitting
// the vote. Axis and Vote use their wire-format string values for
// Temporal JSON round-trip safety.
type ReviewVoteSignal struct {
	Axis       ReviewAxis `json:"axis"`
	Vote       VoteType   `json:"vote"`
	ReviewerID string     `json:"reviewerId"`
}

// SliceProgressSignal is the payload for the slice_progress Temporal signal.
//
// Sent by a SliceWorkflow to its parent EpochWorkflow to report per-leaf-task
// progress. Completed is true when the leaf task finishes, false for
// in-progress heartbeat events.
type SliceProgressSignal struct {
	SliceID    string `json:"sliceId"`
	LeafTaskID string `json:"leafTaskId"`
	StageName  string `json:"stageName"`
	Completed  bool   `json:"completed"`
}

// RegisterSessionSignal is the payload for the register_session Temporal signal.
//
// Registers a Claude Code session with the active epoch for observability and
// permission tracking. Duplicate session_id registrations are silently ignored
// (idempotent). ModelHarness identifies the runtime harness (e.g. "claude-code").
type RegisterSessionSignal struct {
	EpochID      string `json:"epochId"`
	SessionID    string `json:"sessionId"`
	Role         string `json:"role"`
	ModelHarness string `json:"modelHarness"`
	Model        string `json:"model"`
}
