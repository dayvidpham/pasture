package protocol

// Signal and query name constants for the epoch control surface. Defined once
// here so senders and receivers share a single source of truth and never drift.

// Signal names for epoch-level handlers.
const (
	SignalAdvancePhase    = "advance_phase"
	SignalSubmitVote      = "submit_vote"
	SignalSliceProgress   = "slice_progress"
	SignalRegisterSession = "register_session"
)

// Signal names for slice-level handlers — configuring and completing
// individual implementation slices.
const (
	// SignalStartSlice configures the slice execution mode before run.
	SignalStartSlice = "start_slice"
	// SignalCompleteSlice provides an external completion override for the slice.
	SignalCompleteSlice = "complete_slice"
)

// Query names for epoch-level state inspection.
const (
	QueryCurrentState         = "current_state"
	QueryAvailableTransitions = "available_transitions"
	QueryFullState            = "full_state"
	QuerySliceProgressState   = "slice_progress_state"
	QueryActiveSessions       = "active_sessions"
)
