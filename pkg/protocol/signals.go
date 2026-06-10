package protocol

// SliceExecutionMode is the execution strategy a slice sub-workflow uses.
type SliceExecutionMode string

const (
	// SliceMock runs the slice as a no-op stub (for testing and dry-runs).
	SliceMock SliceExecutionMode = "mock"
	// SliceTmux launches the slice command inside a tmux session.
	SliceTmux SliceExecutionMode = "tmux"
	// SliceSubprocess runs the slice command as a direct child process.
	SliceSubprocess SliceExecutionMode = "subprocess"
)

// IsValid reports whether m is one of the recognised execution modes.
func (m SliceExecutionMode) IsValid() bool {
	switch m {
	case SliceMock, SliceTmux, SliceSubprocess:
		return true
	}
	return false
}

// AllSliceExecutionModes is the ordered set of every valid SliceExecutionMode.
var AllSliceExecutionModes = []SliceExecutionMode{SliceMock, SliceTmux, SliceSubprocess}

// PhaseAdvanceSignal is the payload for the advance_phase signal.
//
// Sent by any authorized caller to transition the epoch to a new phase.
// TriggeredBy identifies who or what sent the signal (e.g. a role name or
// external trigger). ConditionMet describes the transition condition from the
// protocol table that was satisfied.
type PhaseAdvanceSignal struct {
	ToPhase      PhaseId `json:"toPhase"`
	TriggeredBy  string  `json:"triggeredBy"`
	ConditionMet string  `json:"conditionMet"`
}

// ReviewVoteSignal is the payload for the submit_vote signal.
//
// ReviewerId must be the unique identifier for the reviewer agent submitting
// the vote. Axis and Vote use their wire-format string values for JSON
// round-trip safety.
type ReviewVoteSignal struct {
	Axis       ReviewAxis `json:"axis"`
	Vote       VoteType   `json:"vote"`
	ReviewerId string     `json:"reviewerId"`
}

// SliceProgressSignal is the payload for the slice_progress signal.
//
// Sent by a slice sub-workflow to its parent epoch to report per-leaf-task
// progress. Completed is true when the leaf task finishes, false for
// in-progress heartbeat events.
type SliceProgressSignal struct {
	SliceId    string `json:"sliceId"`
	LeafTaskId string `json:"leafTaskId"`
	StageName  string `json:"stageName"`
	Completed  bool   `json:"completed"`
}

// RegisterSessionSignal is the payload for the register_session signal.
//
// Registers a Claude Code session with the active epoch for observability and
// permission tracking. Duplicate session_id registrations are silently ignored
// (idempotent). ModelHarness identifies the runtime harness (e.g. "claude-code").
type RegisterSessionSignal struct {
	EpochId      string `json:"epochId"`
	SessionId    string `json:"sessionId"`
	Role         string `json:"role"`
	ModelHarness string `json:"modelHarness"`
	Model        string `json:"model"`
}

// SliceStartSignal is the payload for the start_slice signal.
//
// Sent to a slice sub-workflow to configure how it executes before it runs. Mode
// selects the execution strategy (SliceMock, SliceTmux, or SliceSubprocess);
// Command is the shell command for the tmux/subprocess strategies; TimeoutSeconds
// overrides the default start-to-close timeout when non-zero.
type SliceStartSignal struct {
	Mode           SliceExecutionMode `json:"mode"`
	Command        string             `json:"command,omitempty"`
	TimeoutSeconds int                `json:"timeoutSeconds,omitempty"`
}

// SliceCompleteSignal is the payload for the complete_slice signal.
//
// Sent to a slice sub-workflow to override its outcome with an externally
// reported result. Success is true for a successful completion; Output carries a
// success message; Error carries the failure reason when Success is false.
type SliceCompleteSignal struct {
	Success bool    `json:"success"`
	Output  string  `json:"output,omitempty"`
	Error   *string `json:"error,omitempty"`
}
