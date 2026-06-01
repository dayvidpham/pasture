// context_kind.go — Strongly-typed context-edge attachment kind.
//
// PROPOSAL-2 §7.5 + §7.8: every audit_event row may attach to one or more
// "contexts" (epoch task, slice task, review cycle, follow-up, git commit,
// skill run, claude session). The (event_id, context_kind, context_id) triple
// is the BCNF composite primary key in the context_edges table; ContextKind
// is the typed enum that drives the second column.
//
// Per UAT-1 / URE clarification C1, ResearcherNoteContext is INTENTIONALLY
// excluded from the enum — researcher's notes are out of scope and any attempt
// to add such a value should be rejected during code review with reference to
// C1 (Scenario 10).

package protocol

// ContextKind enumerates the kinds of context an audit event may attach to.
//
// Wire values are stable strings stored in context_edges.context_kind. The
// enum has exactly 8 values (None + 7 concrete kinds) — note that PROPOSAL-2
// Scenario 10 asserts ResearcherNoteContext is NOT a member.
type ContextKind string

const (
	// ContextNone is the zero value indicating "no context" — used by
	// callers building empty Context structs and by IsValid() to allow the
	// zero value through.
	ContextNone ContextKind = "None"

	// ContextEpoch attaches an event to an epoch (Provenance TaskID for the
	// originating REQUEST). Wire format of context_id: "namespace--uuid".
	ContextEpoch ContextKind = "EpochContext"

	// ContextSlice attaches an event to an implementation slice (Beads
	// SLICE-N task ID).
	ContextSlice ContextKind = "SliceContext"

	// ContextReview attaches an event to a review cycle.
	ContextReview ContextKind = "ReviewContext"

	// ContextFollowup attaches an event to a FOLLOWUP_SLICE-N task.
	ContextFollowup ContextKind = "FollowupContext"

	// ContextGit attaches a free-floating git event (commit, push, rebase)
	// using the commit SHA (or remote ref) as context_id.
	ContextGit ContextKind = "GitContext"

	// ContextSkill attaches a /pasture:* skill invocation; context_id is the
	// skill run ID.
	ContextSkill ContextKind = "SkillContext"

	// ContextSession attaches a Claude Code session event; context_id is
	// the session ID.
	ContextSession ContextKind = "SessionContext"
)

// AllContextKinds is the ordered slice of all valid ContextKind values.
//
// Useful for parameterised tests and Scenario 10's enum-membership assertion
// (the test confirms that ResearcherNoteContext is NOT present).
var AllContextKinds = []ContextKind{
	ContextNone,
	ContextEpoch,
	ContextSlice,
	ContextReview,
	ContextFollowup,
	ContextGit,
	ContextSkill,
	ContextSession,
}

// IsValid reports whether k is a known ContextKind value.
//
// Used by Scenario 10 to assert IsValid("ResearcherNoteContext") == false.
func (k ContextKind) IsValid() bool {
	switch k {
	case ContextNone,
		ContextEpoch,
		ContextSlice,
		ContextReview,
		ContextFollowup,
		ContextGit,
		ContextSkill,
		ContextSession:
		return true
	}
	return false
}

// String returns the wire-format string value of k.
func (k ContextKind) String() string { return string(k) }

// Context carries a typed (Kind, ContextId) pair as returned by
// TaskTracker.EventContexts. PROPOSAL-2 §7.5.
//
// ContextId's shape varies per Kind:
//   - ContextEpoch / ContextSlice / ContextReview / ContextFollowup: a
//     Provenance TaskID string ("namespace--uuid").
//   - ContextGit: a git commit SHA (or remote ref).
//   - ContextSkill: a skill run ID.
//   - ContextSession: a Claude Code session ID.
//   - ContextNone: unused.
type Context struct {
	Kind      ContextKind `json:"kind"`
	ContextId string      `json:"contextId"`
}
