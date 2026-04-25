// agent_categories.go — Pasture-side typed agent categorisation.
//
// PROPOSAL-2 §7.4 / §7.6 (UAT-1 direction): every Provenance Agent that needs
// pasture-level classification gets one row in the (private) pasture_agent_categories
// table whose two enum columns are AutomatonRole and PastureRole, both defined
// here in the public protocol package so external dayvidpham-org modules can
// program against the façade without needing the internal implementation.
//
// AutomatonRole describes rules-based automata (URD R8). The 5 concrete values
// (plus None) replace the earlier "Derivation" catch-all per UAT-1 direction.
//
// PastureRole mirrors PROV-O Role for non-automaton agents (humans, ML).

package protocol

// AutomatonRole is the strongly-typed pasture-side category for SoftwareAgent
// instances that represent rules-based automata (PROPOSAL-2 §7.6, URD R8).
//
// Wire values are stable strings stored in pasture_agent_categories.automaton_role.
// The enum has exactly 6 values (None + 5 concrete categories); UAT-1 dropped
// the earlier generic "Derivation" catch-all in favor of the two first-class
// values ConsensusReached and CreateFollowup.
type AutomatonRole string

const (
	// AutomatonRoleNone marks a SoftwareAgent that has no pasture-side
	// automaton role (e.g. the pastured daemon process itself).
	AutomatonRoleNone AutomatonRole = "None"

	// AutomatonRoleConstraintChecker is the canonical name for the
	// constraint-checker automaton (pasture/automaton/check-constraints).
	AutomatonRoleConstraintChecker AutomatonRole = "ConstraintChecker"

	// AutomatonRoleTransitionGate covers the 3 transition gate kinds
	// (consensus, vote-threshold, exit-condition).
	AutomatonRoleTransitionGate AutomatonRole = "TransitionGate"

	// AutomatonRoleHookHandler covers all Claude-Code-hook-event handlers
	// (per Pasture URD D7's hook list).
	AutomatonRoleHookHandler AutomatonRole = "HookHandler"

	// AutomatonRoleConsensusReached is the synthesized event emitted when
	// all reviewers have voted ACCEPT during a phase transition. UAT-1
	// promoted this from a Derivation child to a first-class category.
	AutomatonRoleConsensusReached AutomatonRole = "ConsensusReached"

	// AutomatonRoleCreateFollowup synthesizes follow-up epics from
	// PROPOSAL findings during ratification. UAT-1 promoted this to a
	// first-class category alongside ConsensusReached.
	AutomatonRoleCreateFollowup AutomatonRole = "CreateFollowup"
)

// AllAutomatonRoles is the ordered slice of all valid AutomatonRole values.
// Useful for iteration, completeness checks, and parameterised tests.
var AllAutomatonRoles = []AutomatonRole{
	AutomatonRoleNone,
	AutomatonRoleConstraintChecker,
	AutomatonRoleTransitionGate,
	AutomatonRoleHookHandler,
	AutomatonRoleConsensusReached,
	AutomatonRoleCreateFollowup,
}

// IsValid reports whether r is a known AutomatonRole value.
//
// Membership is tested via switch (not slice scan) so the compiler can flag
// missing cases when the enum grows.
func (r AutomatonRole) IsValid() bool {
	switch r {
	case AutomatonRoleNone,
		AutomatonRoleConstraintChecker,
		AutomatonRoleTransitionGate,
		AutomatonRoleHookHandler,
		AutomatonRoleConsensusReached,
		AutomatonRoleCreateFollowup:
		return true
	}
	return false
}

// String returns the wire-format string value of r.
func (r AutomatonRole) String() string { return string(r) }

// PastureRole mirrors the PROV-O Role concept for non-automaton agents
// (humans and MLAgents acting in epoch roles). PROPOSAL-2 §7.6 / URD R8.
//
// Wire values are stable strings stored in pasture_agent_categories.pasture_role.
type PastureRole string

const (
	// PastureRoleNone marks an agent without a pasture-side role
	// (e.g. SoftwareAgents whose categorisation is fully captured by
	// AutomatonRole).
	PastureRoleNone PastureRole = "None"

	// PastureRoleArchitect — owns proposal authoring (Phases 3-7).
	PastureRoleArchitect PastureRole = "Architect"

	// PastureRoleSupervisor — owns IMPL_PLAN, slice decomposition,
	// worker dispatch, and code-review coordination (Phases 8-10).
	PastureRoleSupervisor PastureRole = "Supervisor"

	// PastureRoleWorker — implements vertical slices in Phase 9.
	PastureRoleWorker PastureRole = "Worker"

	// PastureRoleReviewer — performs code/plan reviews (Phases 4, 5, 10).
	PastureRoleReviewer PastureRole = "Reviewer"
)

// AllPastureRoles is the ordered slice of all valid PastureRole values.
var AllPastureRoles = []PastureRole{
	PastureRoleNone,
	PastureRoleArchitect,
	PastureRoleSupervisor,
	PastureRoleWorker,
	PastureRoleReviewer,
}

// IsValid reports whether r is a known PastureRole value.
func (r PastureRole) IsValid() bool {
	switch r {
	case PastureRoleNone,
		PastureRoleArchitect,
		PastureRoleSupervisor,
		PastureRoleWorker,
		PastureRoleReviewer:
		return true
	}
	return false
}

// String returns the wire-format string value of r.
func (r PastureRole) String() string { return string(r) }
