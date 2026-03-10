// Package temporal defines shared Temporal signal and query name constants for
// the Pasture epoch workflow. All signal/query name strings are defined here
// (D10) to prevent name drift between callers (pasture-msg) and handlers
// (pastured workflows).
package temporal

// Signal name constants for EpochWorkflow signal handlers.
// These are the Temporal signal names — they must match exactly between
// the sender (pasture-msg signal subcommands) and the receiver (pastured).
const (
	SignalAdvancePhase    = "advance_phase"
	SignalSubmitVote      = "submit_vote"
	SignalSliceProgress   = "slice_progress"
	SignalRegisterSession = "register_session"
)

// Query name constants for EpochWorkflow query handlers.
// These are the Temporal query names — they must match exactly between
// the querier (pasture-msg query subcommands) and the handler (pastured).
const (
	QueryCurrentState         = "current_state"
	QueryAvailableTransitions = "available_transitions"
	QueryFullState            = "full_state"
)
