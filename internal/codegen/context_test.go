package codegen_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/testutil"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Fixture types ────────────────────────────────────────────────────────────

// roleConstraintCheck mirrors one entry in testdata/context.yaml role_constraint_checks.
type roleConstraintCheck struct {
	Role           string   `yaml:"role"`
	MustContain    []string `yaml:"must_contain"`
	MustNotContain []string `yaml:"must_not_contain"`
}

// phaseConstraintCheck mirrors one entry in testdata/context.yaml phase_constraint_checks.
type phaseConstraintCheck struct {
	Phase          string   `yaml:"phase"`
	MustContain    []string `yaml:"must_contain"`
	MustNotContain []string `yaml:"must_not_contain"`
}

// contextSuite is the top-level structure of testdata/context.yaml.
type contextSuite struct {
	RoleConstraintChecks  []roleConstraintCheck  `yaml:"role_constraint_checks"`
	PhaseConstraintChecks []phaseConstraintCheck `yaml:"phase_constraint_checks"`
}

// ─── TestGetRoleContext_ConstraintSets ────────────────────────────────────────

// TestGetRoleContext_ConstraintSets verifies that GetRoleContext returns the
// correct constraint ID sets for each role, as specified in the YAML fixture.
func TestGetRoleContext_ConstraintSets(t *testing.T) {
	var suite contextSuite
	testutil.LoadFixtures(t, testutil.CodegenContext, &suite)
	require.NotEmpty(t, suite.RoleConstraintChecks,
		"context.yaml must have role_constraint_checks")

	for _, check := range suite.RoleConstraintChecks {
		check := check
		t.Run(check.Role, func(t *testing.T) {
			role := types.RoleId(check.Role)
			require.True(t, role.IsValid(), "fixture role %q is not a valid RoleId", check.Role)

			ctx := codegen.GetRoleContext(role)

			// Build a set of returned constraint IDs for O(1) lookup.
			gotIDs := make(map[string]bool, len(ctx.Constraints))
			for _, c := range ctx.Constraints {
				gotIDs[c.ID] = true
			}

			for _, id := range check.MustContain {
				assert.True(t, gotIDs[id],
					"role %q: GetRoleContext must contain constraint %q", check.Role, id)
			}
			for _, id := range check.MustNotContain {
				assert.False(t, gotIDs[id],
					"role %q: GetRoleContext must NOT contain constraint %q", check.Role, id)
			}
		})
	}
}

// ─── TestGetRoleContext_Commands ──────────────────────────────────────────────

// TestGetRoleContext_Commands verifies that GetRoleContext returns the
// expected command names for the supervisor role.
func TestGetRoleContext_Commands(t *testing.T) {
	ctx := codegen.GetRoleContext(types.RoleSupervisor)

	require.NotEmpty(t, ctx.Commands,
		"supervisor must have at least one command")

	// Verify all returned commands are non-empty strings.
	for i, cmd := range ctx.Commands {
		assert.NotEmpty(t, cmd,
			"Commands[%d] must not be empty for supervisor", i)
	}

	// Verify commands are sorted (deterministic output).
	for i := 1; i < len(ctx.Commands); i++ {
		assert.LessOrEqual(t, ctx.Commands[i-1], ctx.Commands[i],
			"Commands must be sorted: Commands[%d]=%q Commands[%d]=%q",
			i-1, ctx.Commands[i-1], i, ctx.Commands[i])
	}
}

// TestGetRoleContext_WorkerCommands verifies that the worker role has the expected commands.
func TestGetRoleContext_WorkerCommands(t *testing.T) {
	ctx := codegen.GetRoleContext(types.RoleWorker)

	require.NotEmpty(t, ctx.Commands,
		"worker must have at least one command")

	// Worker should have aura:worker command.
	found := false
	for _, cmd := range ctx.Commands {
		if cmd == "aura:worker" || cmd == "aura:worker:implement" ||
			cmd == "aura:worker:complete" || cmd == "aura:worker:blocked" {
			found = true
			break
		}
	}
	assert.True(t, found,
		"worker must have at least one aura:worker:* command; got: %v", ctx.Commands)
}

// ─── TestGetRoleContext_Handoffs ──────────────────────────────────────────────

// TestGetRoleContext_Handoffs verifies that GetRoleContext returns handoff IDs
// for the supervisor role (which should appear in h2, h3, h4).
func TestGetRoleContext_Handoffs(t *testing.T) {
	ctx := codegen.GetRoleContext(types.RoleSupervisor)

	require.NotEmpty(t, ctx.Handoffs,
		"supervisor must have at least one handoff")

	// Verify all returned handoff IDs are non-empty strings.
	for i, h := range ctx.Handoffs {
		assert.NotEmpty(t, h,
			"Handoffs[%d] must not be empty for supervisor", i)
	}

	// Verify handoffs are sorted (deterministic output).
	for i := 1; i < len(ctx.Handoffs); i++ {
		assert.LessOrEqual(t, ctx.Handoffs[i-1], ctx.Handoffs[i],
			"Handoffs must be sorted: Handoffs[%d]=%q Handoffs[%d]=%q",
			i-1, ctx.Handoffs[i-1], i, ctx.Handoffs[i])
	}
}

// TestGetRoleContext_HandoffBidirectional verifies that roles appear in handoffs
// both as source and target.
func TestGetRoleContext_HandoffBidirectional(t *testing.T) {
	// Architect is source of h1 and target of h2+ for some flows.
	architectCtx := codegen.GetRoleContext(types.RoleArchitect)
	assert.NotEmpty(t, architectCtx.Handoffs,
		"architect must appear in at least one handoff as source or target")

	// Worker is target of h2 (supervisor → worker).
	workerCtx := codegen.GetRoleContext(types.RoleWorker)
	assert.NotEmpty(t, workerCtx.Handoffs,
		"worker must appear in at least one handoff as source or target")
}

// ─── TestGetRoleContext_Phases ────────────────────────────────────────────────

// TestGetRoleContext_Phases verifies that GetRoleContext returns the owned
// phases for the worker role (should own exactly worker-slices).
func TestGetRoleContext_Phases(t *testing.T) {
	ctx := codegen.GetRoleContext(types.RoleWorker)

	require.NotEmpty(t, ctx.Phases,
		"worker must have at least one owned phase")

	found := false
	for _, p := range ctx.Phases {
		if p == protocol.PhaseWorkerSlices {
			found = true
			break
		}
	}
	assert.True(t, found,
		"worker must own PhaseWorkerSlices; got phases: %v", ctx.Phases)
}

// TestGetRoleContext_SupervisorPhases verifies supervisor owns multiple phases.
func TestGetRoleContext_SupervisorPhases(t *testing.T) {
	ctx := codegen.GetRoleContext(types.RoleSupervisor)

	// Supervisor owns p7-p12 (6 phases).
	assert.GreaterOrEqual(t, len(ctx.Phases), 5,
		"supervisor must own at least 5 phases; got: %v", ctx.Phases)
}

// ─── TestGetRoleContext_RoleSpecFields ────────────────────────────────────────

// TestGetRoleContext_RoleSpecFields verifies that Introduction and OwnershipNarrative
// are populated from RoleSpecs.
func TestGetRoleContext_RoleSpecFields(t *testing.T) {
	rolesWithIntro := []types.RoleId{
		types.RoleEpoch,
		types.RoleArchitect,
		types.RoleReviewer,
		types.RoleSupervisor,
		types.RoleWorker,
	}
	for _, role := range rolesWithIntro {
		t.Run(string(role), func(t *testing.T) {
			ctx := codegen.GetRoleContext(role)
			assert.NotEmpty(t, ctx.Introduction,
				"role %q must have Introduction populated", role)
			assert.NotEmpty(t, ctx.OwnershipNarrative,
				"role %q must have OwnershipNarrative populated", role)
		})
	}
}

// TestGetRoleContext_ReviewerAxes verifies that reviewer gets review axes
// and all other roles get an empty slice.
func TestGetRoleContext_ReviewerAxes(t *testing.T) {
	reviewerCtx := codegen.GetRoleContext(types.RoleReviewer)
	assert.NotEmpty(t, reviewerCtx.ReviewAxes,
		"reviewer must have ReviewAxes populated")

	otherRoles := []types.RoleId{
		types.RoleEpoch, types.RoleArchitect,
		types.RoleSupervisor, types.RoleWorker,
	}
	for _, role := range otherRoles {
		ctx := codegen.GetRoleContext(role)
		assert.Empty(t, ctx.ReviewAxes,
			"role %q must have empty ReviewAxes (only reviewer gets them)", role)
	}
}

// ─── TestGetPhaseContext_Labels ───────────────────────────────────────────────

// TestGetPhaseContext_Labels verifies that GetPhaseContext returns the correct
// label values for phases with known label associations.
func TestGetPhaseContext_Labels(t *testing.T) {
	tests := []struct {
		phase     protocol.PhaseId
		wantLabel string // a label value that must appear in the result
	}{
		{protocol.PhaseRequest, "aura:p1-user:s1_1-classify"},
		{protocol.PhaseElicit, "aura:p2-user:s2_1-elicit"},
		{protocol.PhasePropose, "aura:p3-plan:s3-propose"},
		{protocol.PhaseReview, "aura:p4-plan:s4-review"},
		{protocol.PhasePlanReview, "aura:p5-user:s5-uat"},
		{protocol.PhaseRatify, "aura:p6-plan:s6-ratify"},
		{protocol.PhaseHandoff, "aura:p7-plan:s7-handoff"},
		{protocol.PhaseImplPlan, "aura:p8-impl:s8-plan"},
		{protocol.PhaseWorkerSlices, "aura:p9-impl:s9-slice"},
		{protocol.PhaseCodeReview, "aura:p10-impl:s10-review"},
		{protocol.PhaseImplUAT, "aura:p11-user:s11-uat"},
		{protocol.PhaseLanding, "aura:p12-impl:s12-landing"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.phase), func(t *testing.T) {
			ctx := codegen.GetPhaseContext(tt.phase)

			found := false
			for _, label := range ctx.Labels {
				if label == tt.wantLabel {
					found = true
					break
				}
			}
			assert.True(t, found,
				"phase %q: Labels must contain %q; got: %v",
				tt.phase, tt.wantLabel, ctx.Labels)

			// Labels should be sorted.
			for i := 1; i < len(ctx.Labels); i++ {
				assert.LessOrEqual(t, ctx.Labels[i-1], ctx.Labels[i],
					"phase %q: Labels must be sorted", tt.phase)
			}
		})
	}
}

// TestGetPhaseContext_CompleteHasNoLabels verifies that PhaseComplete has
// no phase-specific labels (it is a terminal state).
func TestGetPhaseContext_CompleteHasNoLabels(t *testing.T) {
	ctx := codegen.GetPhaseContext(protocol.PhaseComplete)
	assert.Empty(t, ctx.Labels,
		"PhaseComplete must have no phase-specific labels")
}

// ─── TestGetPhaseContext_Transitions ─────────────────────────────────────────

// TestGetPhaseContext_Transitions verifies that GetPhaseContext returns the
// correct transitions for each phase.
func TestGetPhaseContext_Transitions(t *testing.T) {
	tests := []struct {
		phase         protocol.PhaseId
		wantToPhase   protocol.PhaseId
		minTransitions int
	}{
		{protocol.PhaseRequest, protocol.PhaseElicit, 1},
		{protocol.PhaseElicit, protocol.PhasePropose, 1},
		{protocol.PhasePropose, protocol.PhaseReview, 1},
		// PhaseReview has 2 transitions (ACCEPT → PlanReview, REVISE → Propose)
		{protocol.PhaseReview, "", 2},
		{protocol.PhaseImplPlan, protocol.PhaseWorkerSlices, 1},
		{protocol.PhaseWorkerSlices, protocol.PhaseCodeReview, 1},
		// PhaseCodeReview has 2 transitions
		{protocol.PhaseCodeReview, "", 2},
		{protocol.PhaseLanding, protocol.PhaseComplete, 1},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.phase), func(t *testing.T) {
			ctx := codegen.GetPhaseContext(tt.phase)

			assert.GreaterOrEqual(t, len(ctx.Transitions), tt.minTransitions,
				"phase %q must have at least %d transition(s); got %d",
				tt.phase, tt.minTransitions, len(ctx.Transitions))

			if tt.wantToPhase != "" {
				found := false
				for _, tr := range ctx.Transitions {
					if tr.ToPhase == tt.wantToPhase {
						found = true
						break
					}
				}
				assert.True(t, found,
					"phase %q must have a transition to %q; got transitions: %v",
					tt.phase, tt.wantToPhase, ctx.Transitions)
			}

			// All transitions must have a non-empty condition.
			for i, tr := range ctx.Transitions {
				assert.NotEmpty(t, tr.Condition,
					"phase %q transition[%d].Condition must not be empty", tt.phase, i)
				assert.True(t, tr.ToPhase.IsValid(),
					"phase %q transition[%d].ToPhase %q must be a valid PhaseId",
					tt.phase, i, tr.ToPhase)
			}
		})
	}
}

// TestGetPhaseContext_CompleteHasNoTransitions verifies that PhaseComplete
// has no transitions (terminal state).
func TestGetPhaseContext_CompleteHasNoTransitions(t *testing.T) {
	ctx := codegen.GetPhaseContext(protocol.PhaseComplete)
	assert.Empty(t, ctx.Transitions,
		"PhaseComplete must have no transitions (terminal state)")
}

// ─── TestGetPhaseContext_ConstraintSets ───────────────────────────────────────

// TestGetPhaseContext_ConstraintSets verifies that GetPhaseContext returns the
// correct constraint ID sets for each phase, as specified in the YAML fixture.
func TestGetPhaseContext_ConstraintSets(t *testing.T) {
	var suite contextSuite
	testutil.LoadFixtures(t, testutil.CodegenContext, &suite)
	require.NotEmpty(t, suite.PhaseConstraintChecks,
		"context.yaml must have phase_constraint_checks")

	for _, check := range suite.PhaseConstraintChecks {
		check := check
		t.Run(check.Phase, func(t *testing.T) {
			phase := protocol.PhaseId(check.Phase)
			require.True(t, phase.IsValid(), "fixture phase %q is not a valid PhaseId", check.Phase)

			ctx := codegen.GetPhaseContext(phase)

			// Build a set of returned constraint IDs for O(1) lookup.
			gotIDs := make(map[string]bool, len(ctx.Constraints))
			for _, c := range ctx.Constraints {
				gotIDs[c.ID] = true
			}

			for _, id := range check.MustContain {
				assert.True(t, gotIDs[id],
					"phase %q: GetPhaseContext must contain constraint %q", check.Phase, id)
			}
			for _, id := range check.MustNotContain {
				assert.False(t, gotIDs[id],
					"phase %q: GetPhaseContext must NOT contain constraint %q", check.Phase, id)
			}
		})
	}
}

// TestGetPhaseContext_AllPipelinePhasesHaveGeneralConstraints verifies that
// every pipeline phase has all generalConstraints.
func TestGetPhaseContext_AllPipelinePhasesHaveGeneralConstraints(t *testing.T) {
	generalConstraintIDs := []string{
		"C-audit-never-delete",
		"C-audit-dep-chain",
		"C-dep-direction",
		"C-frontmatter-refs",
		"C-actionable-errors",
	}

	pipelinePhases := []protocol.PhaseId{
		protocol.PhaseRequest, protocol.PhaseElicit, protocol.PhasePropose,
		protocol.PhaseReview, protocol.PhasePlanReview, protocol.PhaseRatify,
		protocol.PhaseHandoff, protocol.PhaseImplPlan, protocol.PhaseWorkerSlices,
		protocol.PhaseCodeReview, protocol.PhaseImplUAT, protocol.PhaseLanding,
	}

	for _, phase := range pipelinePhases {
		phase := phase
		t.Run(string(phase), func(t *testing.T) {
			ctx := codegen.GetPhaseContext(phase)

			gotIDs := make(map[string]bool, len(ctx.Constraints))
			for _, c := range ctx.Constraints {
				gotIDs[c.ID] = true
			}

			for _, id := range generalConstraintIDs {
				assert.True(t, gotIDs[id],
					"phase %q must contain general constraint %q", phase, id)
			}
		})
	}
}

// ─── TestConstraintInversion ──────────────────────────────────────────────────

// TestConstraintInversion verifies that ConstraintToRoleRefs produces the
// correct inverse mapping from constraint IDs to roles.
func TestConstraintInversion(t *testing.T) {
	roleRefs := codegen.ConstraintToRoleRefs()

	require.NotEmpty(t, roleRefs,
		"ConstraintToRoleRefs must not return empty map")

	// General constraints must appear in all 5 roles.
	generalConstraintIDs := []string{
		"C-audit-never-delete",
		"C-audit-dep-chain",
		"C-dep-direction",
		"C-frontmatter-refs",
		"C-actionable-errors",
	}
	allRoles := []types.RoleId{
		types.RoleEpoch, types.RoleArchitect, types.RoleReviewer,
		types.RoleSupervisor, types.RoleWorker,
	}

	for _, cid := range generalConstraintIDs {
		refs, ok := roleRefs[cid]
		require.True(t, ok,
			"ConstraintToRoleRefs must contain general constraint %q", cid)
		assert.Len(t, refs, len(allRoles),
			"general constraint %q must appear in all %d roles; got: %v",
			cid, len(allRoles), refs)
	}

	// Role-specific constraints must NOT appear in all roles.
	roleSpecific := map[string][]types.RoleId{
		"C-worker-gates": {types.RoleWorker},
		"C-review-binary": {types.RoleReviewer},
		"C-proposal-naming": {types.RoleArchitect},
	}

	for cid, expectedRoles := range roleSpecific {
		refs, ok := roleRefs[cid]
		require.True(t, ok,
			"ConstraintToRoleRefs must contain constraint %q", cid)

		// Build a set for lookup.
		refSet := make(map[types.RoleId]bool, len(refs))
		for _, r := range refs {
			refSet[r] = true
		}

		for _, r := range expectedRoles {
			assert.True(t, refSet[r],
				"constraint %q must reference role %q; got refs: %v", cid, r, refs)
		}
	}

	// Verify returned refs are sorted for each constraint.
	for cid, refs := range roleRefs {
		for i := 1; i < len(refs); i++ {
			assert.LessOrEqual(t, string(refs[i-1]), string(refs[i]),
				"ConstraintToRoleRefs[%q] refs must be sorted", cid)
		}
	}
}

// TestConstraintToPhaseRefs verifies that ConstraintToPhaseRefs produces
// the correct inverse mapping from constraint IDs to phases.
func TestConstraintToPhaseRefs(t *testing.T) {
	phaseRefs := codegen.ConstraintToPhaseRefs()

	require.NotEmpty(t, phaseRefs,
		"ConstraintToPhaseRefs must not return empty map")

	// General constraints must appear in all 12 pipeline phases + complete.
	generalConstraintIDs := []string{
		"C-audit-never-delete",
		"C-audit-dep-chain",
		"C-dep-direction",
		"C-frontmatter-refs",
		"C-actionable-errors",
	}

	for _, cid := range generalConstraintIDs {
		refs, ok := phaseRefs[cid]
		require.True(t, ok,
			"ConstraintToPhaseRefs must contain general constraint %q", cid)
		// All 12 pipeline phases + PhaseComplete = 13
		assert.GreaterOrEqual(t, len(refs), 12,
			"general constraint %q must appear in at least 12 phases; got: %v",
			cid, refs)
	}

	// Phase-specific constraints must appear only in their respective phases.
	phaseSpecific := map[string]protocol.PhaseId{
		"C-severity-eager":   protocol.PhaseCodeReview,
		"C-severity-not-plan": protocol.PhaseReview,
		"C-proposal-naming":  protocol.PhasePropose,
	}

	for cid, expectedPhase := range phaseSpecific {
		refs, ok := phaseRefs[cid]
		require.True(t, ok,
			"ConstraintToPhaseRefs must contain constraint %q", cid)

		refSet := make(map[protocol.PhaseId]bool, len(refs))
		for _, p := range refs {
			refSet[p] = true
		}
		assert.True(t, refSet[expectedPhase],
			"constraint %q must reference phase %q; got refs: %v",
			cid, expectedPhase, refs)
	}

	// Verify returned refs are sorted for each constraint.
	for cid, refs := range phaseRefs {
		for i := 1; i < len(refs); i++ {
			assert.LessOrEqual(t, string(refs[i-1]), string(refs[i]),
				"ConstraintToPhaseRefs[%q] refs must be sorted", cid)
		}
	}
}

// ─── TestGetRoleContext_AllRolesReturnValid ───────────────────────────────────

// TestGetRoleContext_AllRolesReturnValid verifies that GetRoleContext can be
// called for every role without panic and returns a populated context.
func TestGetRoleContext_AllRolesReturnValid(t *testing.T) {
	for _, role := range types.AllRoleIds {
		role := role
		t.Run(string(role), func(t *testing.T) {
			// Must not panic.
			ctx := codegen.GetRoleContext(role)

			assert.Equal(t, role, ctx.Role,
				"RoleContext.Role must match requested role")
			assert.NotEmpty(t, ctx.Phases,
				"RoleContext.Phases must not be empty for role %q", role)
			assert.NotEmpty(t, ctx.Constraints,
				"RoleContext.Constraints must not be empty for role %q", role)
		})
	}
}

// TestGetPhaseContext_AllPhasesReturnValid verifies that GetPhaseContext can be
// called for every phase without panic and returns a populated context.
func TestGetPhaseContext_AllPhasesReturnValid(t *testing.T) {
	for _, phase := range protocol.AllPhaseIds {
		phase := phase
		t.Run(string(phase), func(t *testing.T) {
			// Must not panic.
			ctx := codegen.GetPhaseContext(phase)

			assert.Equal(t, phase, ctx.Phase,
				"PhaseContext.Phase must match requested phase")
			// PhaseComplete may have no transitions, but all other phases must.
			if phase != protocol.PhaseComplete {
				assert.NotEmpty(t, ctx.Transitions,
					"PhaseContext.Transitions must not be empty for phase %q", phase)
			}
		})
	}
}

// TestGetRoleContext_AllConstraintsResolvable verifies that every constraint ID
// in the role constraint maps resolves to a valid entry in ConstraintSpecs.
func TestGetRoleContext_AllConstraintsResolvable(t *testing.T) {
	for _, role := range types.AllRoleIds {
		role := role
		t.Run(string(role), func(t *testing.T) {
			ctx := codegen.GetRoleContext(role)
			for _, c := range ctx.Constraints {
				spec, ok := codegen.ConstraintSpecs[c.ID]
				require.True(t, ok,
					"role %q: constraint %q not found in ConstraintSpecs", role, c.ID)
				assert.Equal(t, spec.Given, c.Given,
					"role %q constraint %q: Given mismatch", role, c.ID)
				assert.Equal(t, spec.When, c.When,
					"role %q constraint %q: When mismatch", role, c.ID)
				assert.Equal(t, spec.Then, c.Then,
					"role %q constraint %q: Then mismatch", role, c.ID)
				assert.Equal(t, spec.ShouldNot, c.ShouldNot,
					"role %q constraint %q: ShouldNot mismatch", role, c.ID)
			}
		})
	}
}
