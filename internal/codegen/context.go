// Package codegen — context injection for role- and phase-specific constraint prompting.
//
// This file ports context_injection.py to Go. It provides static constraint
// mappings (role → constraint IDs, phase → constraint IDs) and builder
// functions that construct RoleContext and PhaseContext values for use by
// downstream codegen generators (schema.xml, SKILL.md, agent definitions).
//
// Key types:
//   - RoleContext  — populated by GetRoleContext(role)
//   - PhaseContext — populated by GetPhaseContext(phase)
//
// Static maps:
//   - generalConstraints  — applies to ALL roles and ALL phases
//   - roleConstraints     — hand-authored: RoleId → set of constraint IDs
//   - phaseConstraints    — hand-authored: PhaseId → set of constraint IDs
package codegen

import (
	"fmt"
	"sort"

	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// ─── Context Structs ──────────────────────────────────────────────────────────

// ConstraintContext is a resolved constraint with its Given/When/Then/ShouldNot
// fields populated from ConstraintSpecs. Used inside RoleContext and PhaseContext.
type ConstraintContext struct {
	ID        string
	Given     string
	When      string
	Then      string
	ShouldNot string
}

// RoleContext is the context injection fragment for a specific agent role.
// Populated by GetRoleContext and used by prompt construction to embed
// role-appropriate constraints, phases, commands, and handoffs.
type RoleContext struct {
	Role                 types.RoleId
	Phases               []protocol.PhaseId
	Constraints          []ConstraintContext
	Commands             []string
	Handoffs             []string
	Introduction         string
	OwnershipNarrative   string
	Behaviors            []BehaviorSpec
	Checklists           []Checklist
	CoordinationCommands []CoordinationCommand
	Workflows            []Workflow
	ReviewAxes           []ReviewAxisSpec
	Figures              []FigureSpec
}

// PhaseContext is the context injection fragment for a specific protocol phase.
// Populated by GetPhaseContext and used by prompt construction to embed
// phase-appropriate constraints, labels, and valid transitions.
type PhaseContext struct {
	Phase       protocol.PhaseId
	Constraints []ConstraintContext
	Labels      []string
	Transitions []Transition
}

// ─── General Constraints (apply to ALL roles and ALL phases) ──────────────────
// These constraints govern universal protocol rules regardless of role/phase.

// generalConstraints holds constraint IDs that apply to every role and every phase.
// Mirrors Python _GENERAL_CONSTRAINTS frozenset.
var generalConstraints = map[string]bool{
	// C-audit-never-delete: "any task or label" when modifying → universal
	"C-audit-never-delete": true,
	// C-audit-dep-chain: "any phase transition" when creating new task → universal
	"C-audit-dep-chain": true,
	// C-dep-direction: "adding a Beads dependency" → universal
	"C-dep-direction": true,
	// C-frontmatter-refs: "cross-task references" → universal
	"C-frontmatter-refs": true,
	// C-actionable-errors: "an error, exception, or user-facing message" → universal
	"C-actionable-errors": true,
}

// ─── Static Role → Constraint ID Mapping ─────────────────────────────────────
// Hand-authored from ConstraintSpecs given/when text.
// Mirrors Python _ROLE_CONSTRAINTS dict.
//
// Authoring rationale per constraint:
//   C-audit-never-delete      → ALL (see generalConstraints)
//   C-audit-dep-chain         → ALL (see generalConstraints)
//   C-review-consensus        → REVIEWER (does the reviewing), SUPERVISOR (gates the transition)
//   C-review-binary           → REVIEWER (given: "a reviewer" when: "voting")
//   C-severity-eager          → REVIEWER (given: "code review round (p10 only)" — reviewer must create eagerly)
//   C-severity-not-plan       → REVIEWER (given: "plan review (p4)" — reviewer must not use severity in p4)
//   C-blocker-dual-parent     → REVIEWER (given: "a BLOCKER finding in code review" — reviewer creates the finding)
//   C-followup-timing         → SUPERVISOR (given: "code review completion" — supervisor orchestrates followup)
//   C-vertical-slices         → SUPERVISOR (given: "implementation decomposition" when: "assigning work")
//   C-supervisor-no-impl      → SUPERVISOR (given: "supervisor role")
//   C-supervisor-explore-ephemeral → SUPERVISOR (given: "supervisor needs codebase exploration")
//   C-integration-points      → SUPERVISOR (given: "multiple vertical slices share types" when: "decomposing IMPL_PLAN")
//   C-slice-review-before-close → SUPERVISOR (given: "workers complete their implementation slices")
//   C-max-review-cycles       → SUPERVISOR (given: "per-slice review-fix cycles are ongoing")
//   C-slice-leaf-tasks        → SUPERVISOR (given: "vertical slice created" — supervisor creates slices)
//   C-handoff-skill-invocation→ ARCHITECT + SUPERVISOR (both are sources of handoffs h1 and h2/h3)
//   C-dep-direction           → ALL (see generalConstraints)
//   C-frontmatter-refs        → ALL (see generalConstraints)
//   C-agent-commit            → WORKER + SUPERVISOR (roles that commit code)
//   C-proposal-naming         → ARCHITECT (given: "a new or revised proposal" — architect creates proposals)
//   C-review-naming           → REVIEWER (given: "a review task" when: "creating")
//   C-ure-verbatim            → ARCHITECT (given: "user interview (URE or UAT)" — architect runs interviews)
//   C-followup-lifecycle      → SUPERVISOR (given: "follow-up epic created" when: "starting follow-up work")
//   C-followup-leaf-adoption  → SUPERVISOR (given: "supervisor creates FOLLOWUP_SLICE-N")
//   C-worker-gates            → WORKER (given: "worker finishes implementation")
//   C-actionable-errors       → ALL (see generalConstraints)

// roleConstraints is the hand-authored mapping of RoleId → set of constraint IDs.
var roleConstraints = map[types.RoleId]map[string]bool{
	types.RoleEpoch: mergeConstraints(generalConstraints, map[string]bool{
		// Epoch orchestrates all phases — review consensus gating applies to advance
		"C-review-consensus": true,
		// Epoch creates handoffs as master orchestrator
		"C-handoff-skill-invocation": true,
		// Epoch delegates exploration to ephemeral Explore subagents (Ride the Wave)
		"C-supervisor-explore-ephemeral": true,
		// Epoch ensures supervisor documents integration points between slices
		"C-integration-points": true,
		// Epoch enforces: slices reviewed before closure; supervisor closes, not workers
		"C-slice-review-before-close": true,
		// Epoch enforces: max 3 worker-reviewer cycles; remaining IMPORTANT → FOLLOWUP
		"C-max-review-cycles": true,
	}),
	types.RoleArchitect: mergeConstraints(generalConstraints, map[string]bool{
		// Architect creates proposals → must follow naming convention
		"C-proposal-naming": true,
		// Architect runs user interviews (URE/UAT) → must capture verbatim
		"C-ure-verbatim": true,
		// Architect is source of h1 handoff (architect → supervisor at p7)
		"C-handoff-skill-invocation": true,
		// Architect commits code outputs occasionally (ratified docs)
		"C-agent-commit": true,
	}),
	types.RoleReviewer: mergeConstraints(generalConstraints, map[string]bool{
		// Reviewer checks consensus in review phases
		"C-review-consensus": true,
		// Reviewer must use binary ACCEPT/REVISE
		"C-review-binary": true,
		// Reviewer must create severity tree eagerly in p10
		"C-severity-eager": true,
		// Reviewer must NOT use severity tree in p4
		"C-severity-not-plan": true,
		// Reviewer records BLOCKER findings with dual parents
		"C-blocker-dual-parent": true,
		// Reviewer creates review task names
		"C-review-naming": true,
	}),
	types.RoleSupervisor: mergeConstraints(generalConstraints, map[string]bool{
		// Supervisor gates transition on consensus
		"C-review-consensus": true,
		// Supervisor must not implement code directly
		"C-supervisor-no-impl": true,
		// Supervisor must use ephemeral Explore subagents for codebase exploration
		"C-supervisor-explore-ephemeral": true,
		// Supervisor must document integration points between slices
		"C-integration-points": true,
		// Slices must be reviewed before closure
		"C-slice-review-before-close": true,
		// Worker-reviewer cycles capped at 3
		"C-max-review-cycles": true,
		// Supervisor assigns vertical slices to workers
		"C-vertical-slices": true,
		// Supervisor creates slices and must add leaf tasks
		"C-slice-leaf-tasks": true,
		// Supervisor is source of h2/h3 handoffs
		"C-handoff-skill-invocation": true,
		// Supervisor commits merged code (landing phase)
		"C-agent-commit": true,
		// Supervisor creates follow-up timing after review
		"C-followup-timing": true,
		// Supervisor manages follow-up lifecycle
		"C-followup-lifecycle": true,
		// Supervisor adopts leaf tasks into follow-up slices
		"C-followup-leaf-adoption": true,
	}),
	types.RoleWorker: mergeConstraints(generalConstraints, map[string]bool{
		// Worker must pass quality gates before closing slice
		"C-worker-gates": true,
		// Worker commits code with agent-commit
		"C-agent-commit": true,
	}),
}

// ─── Static Phase → Constraint ID Mapping ─────────────────────────────────────
// Hand-authored from ConstraintSpecs given/when text.
// Mirrors Python _PHASE_CONSTRAINTS dict.
//
// Authoring rationale per constraint:
//   C-audit-never-delete      → ALL phases (see generalConstraints)
//   C-audit-dep-chain         → ALL phases (new tasks created in any phase)
//   C-review-consensus        → PhaseReview, PhaseCodeReview (given: "review cycle (p4 or p10)")
//   C-review-binary           → PhaseReview, PhaseCodeReview (given: reviewer voting)
//   C-severity-eager          → PhaseCodeReview ONLY (given: "code review round (p10 only)")
//   C-severity-not-plan       → PhaseReview ONLY (given: "plan review (p4)")
//   C-blocker-dual-parent     → PhaseCodeReview (given: "a BLOCKER finding in code review")
//   C-followup-timing         → PhaseCodeReview (given: "code review completion")
//   C-vertical-slices         → PhaseImplPlan, PhaseWorkerSlices (given: "implementation decomposition")
//   C-supervisor-no-impl      → PhaseImplPlan, PhaseWorkerSlices (given: "implementation phase")
//   C-supervisor-explore-ephemeral → PhaseImplPlan, PhaseWorkerSlices, PhaseCodeReview (ephemeral explore + review)
//   C-integration-points      → PhaseImplPlan (given: "decomposing IMPL_PLAN in Phase 8")
//   C-slice-review-before-close → PhaseWorkerSlices, PhaseCodeReview (given: "slice implementation is done")
//   C-max-review-cycles       → PhaseCodeReview (given: "counting review-fix iterations")
//   C-slice-leaf-tasks        → PhaseImplPlan, PhaseWorkerSlices (vertical slices created in p8, tracked in p9)
//   C-handoff-skill-invocation→ PhaseHandoff (given: "new phase (especially p7 to p8 handoff)")
//   C-dep-direction           → ALL phases
//   C-frontmatter-refs        → ALL phases
//   C-agent-commit            → PhaseWorkerSlices, PhaseLanding (code committed in worker and landing)
//   C-proposal-naming         → PhasePropose (given: "a new or revised proposal")
//   C-review-naming           → PhaseReview, PhaseCodeReview (given: "a review task" when: "creating")
//   C-ure-verbatim            → PhaseElicit, PhasePlanReview (given: "user interview (URE or UAT)")
//   C-followup-lifecycle      → PhaseCodeReview (given: follow-up epic from code review)
//   C-followup-leaf-adoption  → PhaseCodeReview (given: "supervisor creates FOLLOWUP_SLICE-N" in review context)
//   C-worker-gates            → PhaseWorkerSlices (given: "worker finishes implementation")
//   C-actionable-errors       → ALL phases

// phaseConstraints is the hand-authored mapping of PhaseId → set of constraint IDs.
var phaseConstraints = map[protocol.PhaseId]map[string]bool{
	protocol.PhaseRequest: copyConstraints(generalConstraints),
	protocol.PhaseElicit: mergeConstraints(generalConstraints, map[string]bool{
		// User interviews happen in elicitation
		"C-ure-verbatim": true,
	}),
	protocol.PhasePropose: mergeConstraints(generalConstraints, map[string]bool{
		// Proposals created in p3
		"C-proposal-naming": true,
	}),
	protocol.PhaseReview: mergeConstraints(generalConstraints, map[string]bool{
		// Plan review → consensus required
		"C-review-consensus": true,
		// Plan review → binary voting only
		"C-review-binary": true,
		// Plan review → must NOT create severity tree
		"C-severity-not-plan": true,
		// Review tasks created in p4
		"C-review-naming": true,
	}),
	protocol.PhasePlanReview: mergeConstraints(generalConstraints, map[string]bool{
		// User acceptance test → verbatim capture
		"C-ure-verbatim": true,
	}),
	protocol.PhaseRatify: copyConstraints(generalConstraints),
	protocol.PhaseHandoff: mergeConstraints(generalConstraints, map[string]bool{
		// Handoff document required at p7 transition
		"C-handoff-skill-invocation": true,
	}),
	protocol.PhaseImplPlan: mergeConstraints(generalConstraints, map[string]bool{
		// Implementation decomposition into vertical slices
		"C-vertical-slices": true,
		// Supervisor must not implement directly
		"C-supervisor-no-impl": true,
		// Supervisor must use ephemeral Explore subagents for p8 exploration
		"C-supervisor-explore-ephemeral": true,
		// Supervisor must document integration points in p8
		"C-integration-points": true,
		// Each slice must have leaf tasks
		"C-slice-leaf-tasks": true,
	}),
	protocol.PhaseWorkerSlices: mergeConstraints(generalConstraints, map[string]bool{
		// Worker quality gates before slice completion
		"C-worker-gates": true,
		// Commits happen in slice phase
		"C-agent-commit": true,
		// Supervisor still manages vertical slice ownership
		"C-vertical-slices": true,
		// Supervisor must not implement directly even in p9
		"C-supervisor-no-impl": true,
		// Slice tasks still need leaf tasks tracked
		"C-slice-leaf-tasks": true,
		// Ephemeral explore/review pattern applies across p8-p10
		"C-supervisor-explore-ephemeral": true,
		// Slices must be reviewed before closure; workers notify, supervisor closes
		"C-slice-review-before-close": true,
	}),
	protocol.PhaseCodeReview: mergeConstraints(generalConstraints, map[string]bool{
		// Code review → consensus required (all 3 reviewers ACCEPT)
		"C-review-consensus": true,
		// Code review → binary voting
		"C-review-binary": true,
		// Code review → severity tree must be created eagerly
		"C-severity-eager": true,
		// Code review → BLOCKER findings need dual parents
		"C-blocker-dual-parent": true,
		// Code review tasks → naming convention
		"C-review-naming": true,
		// Follow-up epic timing after code review
		"C-followup-timing": true,
		// Follow-up lifecycle management
		"C-followup-lifecycle": true,
		// Follow-up leaf adoption
		"C-followup-leaf-adoption": true,
		// Ephemeral reviewers spawned for per-slice review in p10
		"C-supervisor-explore-ephemeral": true,
		// Slices reviewed before closure — supervisor closes after review passes
		"C-slice-review-before-close": true,
		// Review-fix cycles capped at 3; remaining IMPORTANTs move to FOLLOWUP
		"C-max-review-cycles": true,
	}),
	protocol.PhaseImplUAT: mergeConstraints(generalConstraints, map[string]bool{
		// Implementation UAT → verbatim capture
		"C-ure-verbatim": true,
	}),
	protocol.PhaseLanding: mergeConstraints(generalConstraints, map[string]bool{
		// Landing phase commits code
		"C-agent-commit": true,
	}),
	// Terminal state — intentionally only general constraints: no additional constraints apply after landing.
	protocol.PhaseComplete: copyConstraints(generalConstraints),
}

// ─── Map Helpers ──────────────────────────────────────────────────────────────

// mergeConstraints returns a new map that is the union of base and extra.
// Neither base nor extra is modified.
func mergeConstraints(base, extra map[string]bool) map[string]bool {
	result := make(map[string]bool, len(base)+len(extra))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

// copyConstraints returns a shallow copy of src.
func copyConstraints(src map[string]bool) map[string]bool {
	result := make(map[string]bool, len(src))
	for k, v := range src {
		result[k] = v
	}
	return result
}

// ─── Constraint Context Builder ───────────────────────────────────────────────

// buildConstraintContexts resolves a set of constraint IDs to ConstraintContext
// values by looking up each ID in ConstraintSpecs.
//
// Returns an error if any constraint ID is not found in ConstraintSpecs.
// This guards against stale hand-authored mappings referencing IDs that no
// longer exist in the canonical data.
func buildConstraintContexts(constraintIDs map[string]bool) ([]ConstraintContext, error) {
	contexts := make([]ConstraintContext, 0, len(constraintIDs))
	for cid := range constraintIDs {
		spec, ok := ConstraintSpecs[cid]
		if !ok {
			return nil, fmt.Errorf(
				"codegen.buildConstraintContexts: constraint ID %q not found in ConstraintSpecs — "+
					"this indicates a stale entry in roleConstraints or phaseConstraints; "+
					"fix: update the hand-authored mapping to use a valid constraint ID from ConstraintSpecs",
				cid,
			)
		}
		contexts = append(contexts, ConstraintContext{
			ID:        spec.ID,
			Given:     spec.Given,
			When:      spec.When,
			Then:      spec.Then,
			ShouldNot: spec.ShouldNot,
		})
	}
	// Sort by ID for deterministic output.
	sort.Slice(contexts, func(i, j int) bool {
		return contexts[i].ID < contexts[j].ID
	})
	return contexts, nil
}

// ─── Builder Functions ────────────────────────────────────────────────────────

// GetRoleContext returns the context injection fragment for the given agent role.
//
// It populates RoleContext with:
//   - Phases: phases where this role is an owner role (inverted from PhaseSpecs)
//   - Constraints: resolved ConstraintContext objects from roleConstraints[role]
//   - Commands: command names from CommandSpecs where RoleRef == role
//   - Handoffs: handoff IDs from HandoffSpecs where role is source or target
//   - Introduction: from RoleSpecs[role].Introduction
//   - OwnershipNarrative: from RoleSpecs[role].OwnershipNarrative
//   - Behaviors: from RoleSpecs[role].Behaviors
//   - Checklists: from ChecklistSpecs where RoleRef == role
//   - CoordinationCommands: role-specific + shared from CoordinationCommands
//   - Workflows: from WorkflowSpecs where RoleRef == role
//   - ReviewAxes: from ReviewAxisSpecs (reviewer only; empty for all others)
//   - Figures: from FigureSpecs where role in RoleRefs
//
// Panics if any constraint ID in roleConstraints[role] is not found in
// ConstraintSpecs. This is a programming error (stale mapping) that must be
// fixed in the source, not handled at runtime.
func GetRoleContext(role types.RoleId) RoleContext {
	// Invert PhaseSpecs[phase].OwnerRoles to find phases owned by this role.
	var ownedPhases []protocol.PhaseId
	for phaseID, spec := range PhaseSpecs {
		for _, ownerRole := range spec.OwnerRoles {
			if ownerRole == role {
				ownedPhases = append(ownedPhases, phaseID)
				break
			}
		}
	}
	sort.Slice(ownedPhases, func(i, j int) bool {
		return string(ownedPhases[i]) < string(ownedPhases[j])
	})

	// Build ConstraintContext objects from the hand-authored role constraint mapping.
	constraintIDs := roleConstraints[role]
	constraints, err := buildConstraintContexts(constraintIDs)
	if err != nil {
		panic(fmt.Sprintf("GetRoleContext(%q): %v", role, err))
	}

	// Collect commands where RoleRef matches this role.
	var commands []string
	for _, spec := range CommandSpecs {
		if spec.RoleRef == role {
			commands = append(commands, spec.Name)
		}
	}
	sort.Strings(commands)

	// Collect handoff IDs where this role is source or target.
	var handoffs []string
	for _, spec := range HandoffSpecs {
		if spec.SourceRole == role || spec.TargetRole == role {
			handoffs = append(handoffs, spec.ID)
		}
	}
	sort.Strings(handoffs)

	// Populate role spec fields.
	roleSpec := RoleSpecs[role]

	// Checklists filtered by role_ref matching this role.
	var checklists []Checklist
	for _, spec := range ChecklistSpecs {
		if spec.RoleRef == role {
			checklists = append(checklists, spec)
		}
	}

	// Coordination commands: role-specific (RoleRef == role) OR shared.
	var coordCommands []CoordinationCommand
	for _, cmd := range CoordinationCommands {
		if cmd.RoleRef == role || cmd.Shared {
			coordCommands = append(coordCommands, cmd)
		}
	}

	// Workflows filtered by RoleRef matching this role.
	var workflows []Workflow
	for _, wf := range WorkflowSpecs {
		if wf.RoleRef == role {
			workflows = append(workflows, wf)
		}
	}

	// Review axes only for reviewer role; empty for all others.
	var reviewAxes []ReviewAxisSpec
	if role == types.RoleReviewer {
		for _, axis := range ReviewAxisSpecs {
			reviewAxes = append(reviewAxes, axis)
		}
		sort.Slice(reviewAxes, func(i, j int) bool {
			return reviewAxes[i].ID < reviewAxes[j].ID
		})
	}

	// Figures filtered by role (M:N via RoleRefs slice).
	// Note: FigureSpec.Content is intentionally left empty here.
	// Content loading from YAML is a generation-time concern handled by S5/S6.
	var figures []FigureSpec
	for _, fig := range FigureSpecs {
		for _, ref := range fig.RoleRefs {
			if ref == role {
				figures = append(figures, fig)
				break
			}
		}
	}
	sort.Slice(figures, func(i, j int) bool {
		return figures[i].ID < figures[j].ID
	})

	return RoleContext{
		Role:                 role,
		Phases:               ownedPhases,
		Constraints:          constraints,
		Commands:             commands,
		Handoffs:             handoffs,
		Introduction:         roleSpec.Introduction,
		OwnershipNarrative:   roleSpec.OwnershipNarrative,
		Behaviors:            roleSpec.Behaviors,
		Checklists:           checklists,
		CoordinationCommands: coordCommands,
		Workflows:            workflows,
		ReviewAxes:           reviewAxes,
		Figures:              figures,
	}
}

// GetPhaseContext returns the context injection fragment for the given protocol phase.
//
// It populates PhaseContext with:
//   - Constraints: resolved ConstraintContext objects from phaseConstraints[phase]
//   - Labels: label values from LabelSpecs where PhaseRef matches phase's p-number
//   - Transitions: valid transitions from PhaseSpecs[phase]
//
// Panics if any constraint ID in phaseConstraints[phase] is not found in
// ConstraintSpecs. This is a programming error (stale mapping) that must be
// fixed in the source, not handled at runtime.
func GetPhaseContext(phase protocol.PhaseId) PhaseContext {
	// Build ConstraintContext objects from the hand-authored phase constraint mapping.
	constraintIDs := phaseConstraints[phase]
	constraints, err := buildConstraintContexts(constraintIDs)
	if err != nil {
		panic(fmt.Sprintf("GetPhaseContext(%q): %v", phase, err))
	}

	// Collect labels where PhaseRef matches this phase's p-number.
	// PhaseSpecs stores the phase number; LabelSpecs uses "p{N}" strings.
	phaseSpec, hasSpec := PhaseSpecs[phase]
	var phaseRef string
	if hasSpec {
		phaseRef = fmt.Sprintf("p%d", phaseSpec.Number)
	}

	var labels []string
	if phaseRef != "" {
		for _, spec := range LabelSpecs {
			if spec.PhaseRef == phaseRef {
				labels = append(labels, spec.Value)
			}
		}
		sort.Strings(labels)
	}

	// Get valid transitions from PhaseSpecs.
	var transitions []Transition
	if hasSpec {
		transitions = phaseSpec.Transitions
	}

	return PhaseContext{
		Phase:       phase,
		Constraints: constraints,
		Labels:      labels,
		Transitions: transitions,
	}
}

// ─── Constraint Inversion ─────────────────────────────────────────────────────

// ConstraintToRoleRefs returns a map from constraint ID to the sorted list of
// RoleIds that reference it in roleConstraints.
//
// This inversion is used by S5 schema generation to emit role-ref attributes
// into schema.xml for each constraint element.
func ConstraintToRoleRefs() map[string][]types.RoleId {
	result := make(map[string][]types.RoleId)
	for role, ids := range roleConstraints {
		for cid := range ids {
			result[cid] = append(result[cid], role)
		}
	}
	// Sort each slice for deterministic output.
	for cid := range result {
		roles := result[cid]
		sort.Slice(roles, func(i, j int) bool {
			return string(roles[i]) < string(roles[j])
		})
		result[cid] = roles
	}
	return result
}

// ConstraintToPhaseRefs returns a map from constraint ID to the sorted list of
// PhaseIds that reference it in phaseConstraints.
//
// This inversion is used by S5 schema generation to emit phase-ref attributes
// into schema.xml for each constraint element.
func ConstraintToPhaseRefs() map[string][]protocol.PhaseId {
	result := make(map[string][]protocol.PhaseId)
	for phase, ids := range phaseConstraints {
		for cid := range ids {
			result[cid] = append(result[cid], phase)
		}
	}
	// Sort each slice for deterministic output.
	for cid := range result {
		phases := result[cid]
		sort.Slice(phases, func(i, j int) bool {
			return string(phases[i]) < string(phases[j])
		})
		result[cid] = phases
	}
	return result
}
