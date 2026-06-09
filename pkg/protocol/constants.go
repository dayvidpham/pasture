package protocol

// Signal and query names for the epoch control surface. Defined once here, as
// distinct named types, so senders and receivers share a single source of truth
// and a topic/query name is never written as a bare string literal at a call
// site. The durable substrate's Send/Recv take plain strings, so call sites pass
// the constant's String() (or an explicit conversion) — the type still anchors
// the value to this canonical set.

// SignalTopic is the delivery topic of a durable epoch signal. There is exactly
// one topic per signal name; the workflow's receive loop and every sender
// reference these constants instead of literals.
type SignalTopic string

// Signal topics for epoch-level handlers.
const (
	SignalAdvancePhase    SignalTopic = "advance_phase"
	SignalSubmitVote      SignalTopic = "submit_vote"
	SignalSliceProgress   SignalTopic = "slice_progress"
	SignalRegisterSession SignalTopic = "register_session"
)

// Signal topics for slice-level handlers — configuring and completing
// individual implementation slices.
const (
	// SignalStartSlice configures the slice execution mode before run.
	SignalStartSlice SignalTopic = "start_slice"
	// SignalCompleteSlice provides an external completion override for the slice.
	SignalCompleteSlice SignalTopic = "complete_slice"
)

// AllSignalTopics is the ordered set of every valid SignalTopic.
var AllSignalTopics = []SignalTopic{
	SignalAdvancePhase,
	SignalSubmitVote,
	SignalSliceProgress,
	SignalRegisterSession,
	SignalStartSlice,
	SignalCompleteSlice,
}

// String returns the wire-format topic name.
func (t SignalTopic) String() string { return string(t) }

// IsValid reports whether t is one of the known signal topics.
func (t SignalTopic) IsValid() bool {
	switch t {
	case SignalAdvancePhase, SignalSubmitVote, SignalSliceProgress,
		SignalRegisterSession, SignalStartSlice, SignalCompleteSlice:
		return true
	}
	return false
}

// QueryName identifies a read-only epoch state query. Queries are answered from
// the persisted EpochState projection (a SQL read), never a workflow round-trip.
type QueryName string

// Query names for epoch-level state inspection.
const (
	QueryCurrentState         QueryName = "current_state"
	QueryAvailableTransitions QueryName = "available_transitions"
	QueryFullState            QueryName = "full_state"
	QuerySliceProgressState   QueryName = "slice_progress_state"
	QueryActiveSessions       QueryName = "active_sessions"
)

// AllQueryNames is the ordered set of every valid QueryName.
var AllQueryNames = []QueryName{
	QueryCurrentState,
	QueryAvailableTransitions,
	QueryFullState,
	QuerySliceProgressState,
	QueryActiveSessions,
}

// String returns the wire-format query name.
func (q QueryName) String() string { return string(q) }

// IsValid reports whether q is one of the known query names.
func (q QueryName) IsValid() bool {
	switch q {
	case QueryCurrentState, QueryAvailableTransitions, QueryFullState,
		QuerySliceProgressState, QueryActiveSessions:
		return true
	}
	return false
}

// ParseQueryName converts a raw string to a QueryName, reporting whether it is
// a recognized query. Used at the CLI boundary where the query is user-supplied.
func ParseQueryName(s string) (QueryName, bool) {
	q := QueryName(s)
	return q, q.IsValid()
}
