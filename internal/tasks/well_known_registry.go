// Package tasks — well_known_registry.go
//
// Canonical list of pasture's 15 well-known automaton agents (PROPOSAL-2
// §7.7.2). The registry is a static, immutable slice of (name, role) pairs
// processed by RegisterWellKnownAgents at daemon startup (§7.7.3) — one
// `pasture_well_known_agents` row + one `pasture_agent_categories` row per
// entry, all under a single `BEGIN IMMEDIATE` transaction on the audit DB.
//
// The 15 entries decompose as:
//
//	1                          ConstraintChecker  (check-constraints)
//	3                          TransitionGate     (consensus / vote-threshold / exit-condition)
//	9                          HookHandler        (one per Claude Code hook event)
//	1                          ConsensusReached   (UAT-1 first-class category)
//	1                          CreateFollowup     (UAT-1 first-class category)
//	─────                                          (15)
//
// Hook-count canonicity (worker note 2026-04-25):
//
//   - PROPOSAL-2 §7.7.2 enumerates exactly 9 *Claude Code hook events*:
//     SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Notification,
//     Stop, SubagentStop, PreCompact, SessionEnd.
//   - These are NOT pasture's internal hooks (`pasture/internal/hooks/hooks.go`
//     defines a separate 12-entry abstraction for pasture's own lifecycle /
//     error / session events; that table is consumed by the Manager.Dispatch
//     fan-out and is unrelated to Claude Code).
//   - Pasture URD `aura-plugins-jbnx3` D7 catalogues pasture's INTERNAL
//     hooks; it does not list the Claude Code hook names. §7.7.2 is therefore
//     the authoritative source for the well-known agent registry.
//
// Workers extending this list MUST keep entries in §7.7.2 order so the
// idempotent insertion order is preserved across audits and review diffs.

package tasks

import "github.com/dayvidpham/pasture/pkg/protocol"

// WellKnownAgentSpec pairs a stable logical name (the PK in
// `pasture_well_known_agents`) with its `protocol.AutomatonRole` (the value
// stored in `pasture_agent_categories.automaton_role`). The PastureRole for
// every well-known automaton is `None` (per §7.7.2 — these are software
// automata, not workflow agents).
type WellKnownAgentSpec struct {
	// Name is the stable logical name. It survives across restarts and is
	// minted exactly once per database (idempotency anchor on the UNIQUE
	// constraint over `pasture_well_known_agents.name`). Convention:
	// `pasture/automaton/<role-kebab>` (or `pasture/automaton/hook/<HookName>`).
	Name string

	// Role is the AutomatonRole this agent fulfils. MUST be one of
	// `protocol.AllAutomatonRoles` minus `AutomatonRoleNone` (every
	// concrete automaton has a non-None role). Validated at registration
	// time by ensureWellKnownAgent via Role.IsValid().
	Role protocol.AutomatonRole
}

// WellKnownAgentVersion is the `version` column written to `agents_software`
// when minting a well-known agent's Provenance row. It moves in lockstep with
// the daemon binary: bumping the daemon version does NOT re-register agents
// (idempotency is keyed on logical name, not version), so this string is
// effectively a stamp recording which binary version first observed the
// agent's absence.
const WellKnownAgentVersion = "v1"

// WellKnownAgentSource is the `source` column written to `agents_software`
// when minting a well-known agent's Provenance row. It is set to the pasture
// repo URL so downstream PROV-O queries can discover the agent's provenance.
const WellKnownAgentSource = "github.com/dayvidpham/pasture"

// WellKnownAgentNamespace is the namespace string passed to
// `provenance.Tracker.RegisterSoftwareAgent`. All well-known agents live
// under the `pasture` namespace; this distinguishes them from human and ML
// agents (which use other namespaces) and from external software agents
// registered by other tools.
const WellKnownAgentNamespace = "pasture"

// claudeCodeHookEvents is the canonical, ordered list of the 9 Claude Code
// hook event names enumerated in PROPOSAL-2 §7.7.2. Order matches the
// proposal table and is preserved when generating well-known names so two
// restarts of the daemon insert rows in identical order (Scenario 14).
//
// This is a package-private slice so consumers cannot mutate it; the
// canonical public surface is WellKnownAgents().
var claudeCodeHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"Notification",
	"Stop",
	"SubagentStop",
	"PreCompact",
	"SessionEnd",
}

// transitionGateKinds enumerates the 3 transition-gate sub-categories per
// PROPOSAL-2 §7.7.2 (rows 2-4 of the table). The role is shared
// (TransitionGate); only the name suffix distinguishes them.
var transitionGateKinds = []string{
	"consensus",
	"vote-threshold",
	"exit-condition",
}

// WellKnownAgents returns the canonical 15-entry slice of well-known agents
// in PROPOSAL-2 §7.7.2 row order. The returned slice is a fresh copy;
// callers may not assume it shares storage with package internals (this
// keeps the canonical list immutable from outside the package).
//
// Order:
//
//	0:    ConstraintChecker — pasture/automaton/check-constraints
//	1-3:  TransitionGate    — pasture/automaton/transition-gate/{consensus,vote-threshold,exit-condition}
//	4-12: HookHandler       — pasture/automaton/hook/<HookName> × 9
//	13:   ConsensusReached  — pasture/automaton/consensus-reached
//	14:   CreateFollowup    — pasture/automaton/create-followup
//
// Total: 15. Length-checked by the WellKnownAgentCount constant below.
func WellKnownAgents() []WellKnownAgentSpec {
	out := make([]WellKnownAgentSpec, 0, WellKnownAgentCount)

	// 1. ConstraintChecker (one entry).
	out = append(out, WellKnownAgentSpec{
		Name: "pasture/automaton/check-constraints",
		Role: protocol.AutomatonRoleConstraintChecker,
	})

	// 2-4. TransitionGate (3 entries — one per kind, all share the role).
	for _, kind := range transitionGateKinds {
		out = append(out, WellKnownAgentSpec{
			Name: "pasture/automaton/transition-gate/" + kind,
			Role: protocol.AutomatonRoleTransitionGate,
		})
	}

	// 5-13. HookHandler (9 entries — one per Claude Code hook event).
	for _, hook := range claudeCodeHookEvents {
		out = append(out, WellKnownAgentSpec{
			Name: "pasture/automaton/hook/" + hook,
			Role: protocol.AutomatonRoleHookHandler,
		})
	}

	// 14. ConsensusReached (UAT-1 first-class).
	out = append(out, WellKnownAgentSpec{
		Name: "pasture/automaton/consensus-reached",
		Role: protocol.AutomatonRoleConsensusReached,
	})

	// 15. CreateFollowup (UAT-1 first-class).
	out = append(out, WellKnownAgentSpec{
		Name: "pasture/automaton/create-followup",
		Role: protocol.AutomatonRoleCreateFollowup,
	})

	return out
}

// WellKnownAgentCount is the total number of canonical well-known agents
// registered at daemon startup (PROPOSAL-2 §7.7.2). It is exported so tests
// can assert the table size without re-counting; a divergence between this
// constant and len(WellKnownAgents()) is a programming error caught by
// init-time assertion in well_known_registry_init.go (or, equivalently, by
// the unit tests in well_known_test.go).
//
// The breakdown is documented in the package-level comment above:
//
//	1 (ConstraintChecker)
//	+ 3 (TransitionGate)
//	+ 9 (HookHandler)
//	+ 1 (ConsensusReached)
//	+ 1 (CreateFollowup)
//	= 15
const WellKnownAgentCount = 15
