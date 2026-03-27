package codegen_test

import (
	"testing"

	"github.com/dayvidpham/pasture/internal/codegen"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPhaseSpecsCompleteness verifies that every known PhaseId (except Complete)
// has a corresponding entry in PhaseSpecs.
func TestPhaseSpecsCompleteness(t *testing.T) {
	// These are the 12 pipeline phases (PhaseComplete is terminal and excluded).
	pipelinePhases := []protocol.PhaseId{
		protocol.PhaseRequest,
		protocol.PhaseElicit,
		protocol.PhasePropose,
		protocol.PhaseReview,
		protocol.PhasePlanReview,
		protocol.PhaseRatify,
		protocol.PhaseHandoff,
		protocol.PhaseImplPlan,
		protocol.PhaseWorkerSlices,
		protocol.PhaseCodeReview,
		protocol.PhaseImplUAT,
		protocol.PhaseLanding,
	}

	for _, phaseID := range pipelinePhases {
		t.Run(string(phaseID), func(t *testing.T) {
			spec, ok := codegen.PhaseSpecs[phaseID]
			require.True(t, ok, "PhaseSpecs missing entry for phase %q", phaseID)
			assert.Equal(t, phaseID, spec.ID, "PhaseSpec.ID mismatch for %q", phaseID)
			assert.NotEmpty(t, spec.Name, "PhaseSpec.Name must not be empty for %q", phaseID)
			assert.Greater(t, spec.Number, 0, "PhaseSpec.Number must be > 0 for %q", phaseID)
			assert.NotEmpty(t, spec.Domain, "PhaseSpec.Domain must not be empty for %q", phaseID)
			assert.NotEmpty(t, spec.OwnerRoles, "PhaseSpec.OwnerRoles must not be empty for %q", phaseID)
			assert.NotEmpty(t, spec.Transitions, "PhaseSpec.Transitions must not be empty for %q", phaseID)
		})
	}

	// Verify count: exactly 12 pipeline phases.
	assert.Len(t, codegen.PhaseSpecs, len(pipelinePhases),
		"PhaseSpecs should have exactly %d entries (one per pipeline phase)", len(pipelinePhases))
}

// TestPhaseSpecsNumbering verifies phase numbers are 1-12 with no duplicates.
func TestPhaseSpecsNumbering(t *testing.T) {
	seen := make(map[int]protocol.PhaseId)
	for id, spec := range codegen.PhaseSpecs {
		existing, dup := seen[spec.Number]
		assert.False(t, dup,
			"Duplicate phase number %d: phases %q and %q", spec.Number, existing, id)
		seen[spec.Number] = id

		assert.GreaterOrEqual(t, spec.Number, 1, "Phase number must be >= 1")
		assert.LessOrEqual(t, spec.Number, 12, "Phase number must be <= 12")
	}
}

// TestRoleSpecsCompleteness verifies that every known RoleId has an entry in RoleSpecs.
func TestRoleSpecsCompleteness(t *testing.T) {
	for _, roleID := range types.AllRoleIds {
		t.Run(string(roleID), func(t *testing.T) {
			spec, ok := codegen.RoleSpecs[roleID]
			require.True(t, ok, "RoleSpecs missing entry for role %q", roleID)
			assert.Equal(t, roleID, spec.ID, "RoleSpec.ID mismatch for %q", roleID)
			assert.NotEmpty(t, spec.Name, "RoleSpec.Name must not be empty for %q", roleID)
			assert.NotEmpty(t, spec.Description, "RoleSpec.Description must not be empty for %q", roleID)
			assert.NotEmpty(t, spec.OwnedPhases, "RoleSpec.OwnedPhases must not be empty for %q", roleID)
		})
	}

	assert.Len(t, codegen.RoleSpecs, len(types.AllRoleIds),
		"RoleSpecs should have exactly %d entries (one per RoleId)", len(types.AllRoleIds))
}

// TestRoleSpecsBehaviors verifies that roles with expected behaviors have them populated.
func TestRoleSpecsBehaviors(t *testing.T) {
	rolesWithBehaviors := []types.RoleId{
		types.RoleArchitect,
		types.RoleReviewer,
		types.RoleSupervisor,
		types.RoleWorker,
	}
	for _, roleID := range rolesWithBehaviors {
		spec := codegen.RoleSpecs[roleID]
		assert.NotEmpty(t, spec.Behaviors, "Role %q should have behaviors defined", roleID)
		for i, b := range spec.Behaviors {
			assert.NotEmpty(t, b.ID, "Behavior[%d].ID must not be empty for role %q", i, roleID)
			assert.NotEmpty(t, b.Given, "Behavior[%d].Given must not be empty for role %q", i, roleID)
			assert.NotEmpty(t, b.When, "Behavior[%d].When must not be empty for role %q", i, roleID)
			assert.NotEmpty(t, b.Then, "Behavior[%d].Then must not be empty for role %q", i, roleID)
			assert.NotEmpty(t, b.ShouldNot, "Behavior[%d].ShouldNot must not be empty for role %q", i, roleID)
		}
	}
}

// TestConstraintSpecsNotEmpty verifies ConstraintSpecs has entries and all have non-empty GWT fields.
func TestConstraintSpecsNotEmpty(t *testing.T) {
	require.NotEmpty(t, codegen.ConstraintSpecs, "ConstraintSpecs must not be empty")

	for id, spec := range codegen.ConstraintSpecs {
		t.Run(id, func(t *testing.T) {
			assert.Equal(t, id, spec.ID, "ConstraintSpec key %q must match spec.ID", id)
			assert.NotEmpty(t, spec.Given, "ConstraintSpec %q: Given must not be empty", id)
			assert.NotEmpty(t, spec.When, "ConstraintSpec %q: When must not be empty", id)
			assert.NotEmpty(t, spec.Then, "ConstraintSpec %q: Then must not be empty", id)
			assert.NotEmpty(t, spec.ShouldNot, "ConstraintSpec %q: ShouldNot must not be empty", id)
		})
	}
}

// TestConstraintSpecsKnownEntries verifies key constraints from the Python source are present.
func TestConstraintSpecsKnownEntries(t *testing.T) {
	knownConstraints := []string{
		"C-audit-never-delete",
		"C-audit-dep-chain",
		"C-review-consensus",
		"C-review-binary",
		"C-severity-eager",
		"C-dep-direction",
		"C-agent-commit",
		"C-worker-gates",
		"C-actionable-errors",
		"C-vertical-slices",
		"C-supervisor-no-impl",
		"C-slice-leaf-tasks",
		"C-followup-lifecycle",
	}
	for _, id := range knownConstraints {
		_, ok := codegen.ConstraintSpecs[id]
		assert.True(t, ok, "ConstraintSpecs missing expected entry %q", id)
	}
}

// TestConstraintSpecsExamples verifies that constraints with examples have valid example structure.
func TestConstraintSpecsExamples(t *testing.T) {
	for id, spec := range codegen.ConstraintSpecs {
		for i, ex := range spec.Examples {
			assert.NotEmpty(t, ex.ID, "Constraint %q example[%d]: ID must not be empty", id, i)
			assert.NotEmpty(t, ex.Lang, "Constraint %q example[%d]: Lang must not be empty", id, i)
			assert.NotEmpty(t, ex.Label, "Constraint %q example[%d]: Label must not be empty", id, i)
			assert.NotEmpty(t, ex.Code, "Constraint %q example[%d]: Code must not be empty", id, i)
		}
	}
}

// TestHandoffSpecsCompleteness verifies HandoffSpecs has all 6 handoffs with valid roles and phases.
func TestHandoffSpecsCompleteness(t *testing.T) {
	expectedHandoffs := []string{"h1", "h2", "h3", "h4", "h5", "h6"}
	for _, id := range expectedHandoffs {
		t.Run(id, func(t *testing.T) {
			spec, ok := codegen.HandoffSpecs[id]
			require.True(t, ok, "HandoffSpecs missing entry %q", id)
			assert.Equal(t, id, spec.ID, "HandoffSpec key %q must match spec.ID", id)
			assert.True(t, spec.SourceRole.IsValid(), "HandoffSpec %q: SourceRole %q must be valid", id, spec.SourceRole)
			assert.True(t, spec.TargetRole.IsValid(), "HandoffSpec %q: TargetRole %q must be valid", id, spec.TargetRole)
			assert.True(t, spec.AtPhase.IsValid(), "HandoffSpec %q: AtPhase %q must be valid", id, spec.AtPhase)
			assert.NotEmpty(t, spec.ContentLevel, "HandoffSpec %q: ContentLevel must not be empty", id)
			assert.NotEmpty(t, spec.RequiredFields, "HandoffSpec %q: RequiredFields must not be empty", id)
		})
	}
}

// TestCommandSpecsNotEmpty verifies CommandSpecs has entries and all have required fields.
func TestCommandSpecsNotEmpty(t *testing.T) {
	require.NotEmpty(t, codegen.CommandSpecs, "CommandSpecs must not be empty")

	for id, spec := range codegen.CommandSpecs {
		t.Run(id, func(t *testing.T) {
			assert.Equal(t, id, spec.ID, "CommandSpec key %q must match spec.ID", id)
			assert.NotEmpty(t, spec.Name, "CommandSpec %q: Name must not be empty", id)
			assert.NotEmpty(t, spec.Description, "CommandSpec %q: Description must not be empty", id)
			assert.NotEmpty(t, spec.File, "CommandSpec %q: File must not be empty", id)
		})
	}
}

// TestReviewAxisSpecsCompleteness verifies all 3 review axes are present.
func TestReviewAxisSpecsCompleteness(t *testing.T) {
	expectedAxes := []string{"axis-correctness", "axis-test_quality", "axis-elegance"}
	for _, id := range expectedAxes {
		t.Run(id, func(t *testing.T) {
			spec, ok := codegen.ReviewAxisSpecs[id]
			require.True(t, ok, "ReviewAxisSpecs missing entry %q", id)
			assert.Equal(t, id, spec.ID, "ReviewAxisSpec key %q must match spec.ID", id)
			assert.NotEmpty(t, spec.Letter, "ReviewAxisSpec %q: Letter must not be empty", id)
			assert.NotEmpty(t, spec.Name, "ReviewAxisSpec %q: Name must not be empty", id)
			assert.NotEmpty(t, spec.Short, "ReviewAxisSpec %q: Short must not be empty", id)
			assert.NotEmpty(t, spec.KeyQuestions, "ReviewAxisSpec %q: KeyQuestions must not be empty", id)
		})
	}
}

// TestChecklistSpecsCompleteness verifies all expected checklists are present.
func TestChecklistSpecsCompleteness(t *testing.T) {
	expectedChecklists := []string{
		"worker-completion",
		"worker-slice-closure",
		"supervisor-review-ready",
		"supervisor-landing",
	}
	for _, id := range expectedChecklists {
		t.Run(id, func(t *testing.T) {
			spec, ok := codegen.ChecklistSpecs[id]
			require.True(t, ok, "ChecklistSpecs missing entry %q", id)
			assert.True(t, spec.RoleRef.IsValid(), "ChecklistSpec %q: RoleRef %q must be valid", id, spec.RoleRef)
			assert.NotEmpty(t, spec.Gate, "ChecklistSpec %q: Gate must not be empty", id)
			assert.NotEmpty(t, spec.Items, "ChecklistSpec %q: Items must not be empty", id)
		})
	}
}

// TestWorkflowSpecsCompleteness verifies all 3 workflows are present with stages.
func TestWorkflowSpecsCompleteness(t *testing.T) {
	expectedWorkflows := []string{"ride-the-wave", "layer-cake", "architect-state-flow"}
	for _, id := range expectedWorkflows {
		t.Run(id, func(t *testing.T) {
			spec, ok := codegen.WorkflowSpecs[id]
			require.True(t, ok, "WorkflowSpecs missing entry %q", id)
			assert.Equal(t, id, spec.ID, "Workflow key %q must match spec.ID", id)
			assert.NotEmpty(t, spec.Name, "Workflow %q: Name must not be empty", id)
			assert.NotEmpty(t, spec.Description, "Workflow %q: Description must not be empty", id)
			assert.True(t, spec.RoleRef.IsValid(), "Workflow %q: RoleRef %q must be valid", id, spec.RoleRef)
			assert.NotEmpty(t, spec.Stages, "Workflow %q: Stages must not be empty", id)

			for i, stage := range spec.Stages {
				assert.NotEmpty(t, stage.ID, "Workflow %q stage[%d]: ID must not be empty", id, i)
				assert.NotEmpty(t, stage.Name, "Workflow %q stage[%d]: Name must not be empty", id, i)
				assert.Greater(t, stage.Order, 0, "Workflow %q stage[%d]: Order must be > 0", id, i)
				assert.NotEmpty(t, stage.Execution, "Workflow %q stage[%d]: Execution must not be empty", id, i)
			}
		})
	}
}

// TestProcedureStepsCompleteness verifies all roles have entries (even if empty).
func TestProcedureStepsCompleteness(t *testing.T) {
	for _, roleID := range types.AllRoleIds {
		_, ok := codegen.ProcedureSteps[roleID]
		assert.True(t, ok, "ProcedureSteps missing entry for role %q", roleID)
	}
}

// TestProcedureStepsOrdering verifies that steps with multiple entries are monotonically ordered.
func TestProcedureStepsOrdering(t *testing.T) {
	for roleID, steps := range codegen.ProcedureSteps {
		for i := 1; i < len(steps); i++ {
			assert.Greater(t, steps[i].Order, steps[i-1].Order,
				"ProcedureSteps[%q]: step[%d].Order (%d) must be > step[%d].Order (%d)",
				roleID, i, steps[i].Order, i-1, steps[i-1].Order)
		}
	}
}

// TestLabelSpecsNotEmpty verifies LabelSpecs has entries and phase-specific labels have refs.
func TestLabelSpecsNotEmpty(t *testing.T) {
	require.NotEmpty(t, codegen.LabelSpecs, "LabelSpecs must not be empty")

	for id, spec := range codegen.LabelSpecs {
		assert.Equal(t, id, spec.ID, "LabelSpec key %q must match spec.ID", id)
		assert.NotEmpty(t, spec.Value, "LabelSpec %q: Value must not be empty", id)
	}
}

// TestSubstepDataMapCompleteness verifies all 12 phases have substep data.
func TestSubstepDataMapCompleteness(t *testing.T) {
	expectedPhases := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10", "p11", "p12"}
	for _, phaseKey := range expectedPhases {
		t.Run(phaseKey, func(t *testing.T) {
			substeps, ok := codegen.SubstepDataMap[phaseKey]
			require.True(t, ok, "SubstepDataMap missing entry for phase %q", phaseKey)
			assert.NotEmpty(t, substeps, "SubstepDataMap[%q] must not be empty", phaseKey)
			for i, s := range substeps {
				assert.NotEmpty(t, s.ID, "SubstepData[%q][%d]: ID must not be empty", phaseKey, i)
				assert.NotEmpty(t, s.Type, "SubstepData[%q][%d]: Type must not be empty", phaseKey, i)
				assert.NotEmpty(t, s.Execution, "SubstepData[%q][%d]: Execution must not be empty", phaseKey, i)
				assert.Greater(t, s.Order, 0, "SubstepData[%q][%d]: Order must be > 0", phaseKey, i)
				assert.NotEmpty(t, s.LabelRef, "SubstepData[%q][%d]: LabelRef must not be empty", phaseKey, i)
			}
		})
	}
}

// TestFigureSpecsCompleteness verifies all 3 figures are present.
func TestFigureSpecsCompleteness(t *testing.T) {
	expectedFigures := []string{"layer-cake", "ride-the-wave", "architect-state-flow"}
	for _, id := range expectedFigures {
		spec, ok := codegen.FigureSpecs[id]
		require.True(t, ok, "FigureSpecs missing entry %q", id)
		assert.Equal(t, id, spec.ID, "FigureSpec key %q must match spec.ID", id)
		assert.NotEmpty(t, spec.Title, "FigureSpec %q: Title must not be empty", id)
		assert.NotEmpty(t, spec.Type, "FigureSpec %q: Type must not be empty", id)
		assert.NotEmpty(t, spec.RoleRefs, "FigureSpec %q: RoleRefs must not be empty", id)
		assert.NotEmpty(t, spec.WorkflowRefs, "FigureSpec %q: WorkflowRefs must not be empty", id)
	}
}

// TestCoordinationCommandsNotEmpty verifies CoordinationCommands has entries.
func TestCoordinationCommandsNotEmpty(t *testing.T) {
	require.NotEmpty(t, codegen.CoordinationCommands, "CoordinationCommands must not be empty")

	sharedCount := 0
	for id, cmd := range codegen.CoordinationCommands {
		assert.Equal(t, id, cmd.ID, "CoordinationCommand key %q must match cmd.ID", id)
		assert.NotEmpty(t, cmd.Action, "CoordinationCommand %q: Action must not be empty", id)
		assert.NotEmpty(t, cmd.Template, "CoordinationCommand %q: Template must not be empty", id)
		if cmd.Shared {
			sharedCount++
		}
	}
	// At least some commands should be shared (available to all roles).
	assert.Greater(t, sharedCount, 0, "At least one CoordinationCommand should be shared")
}

// TestTitleConventionsNotEmpty verifies TitleConventions is populated.
func TestTitleConventionsNotEmpty(t *testing.T) {
	assert.NotEmpty(t, codegen.TitleConventions, "TitleConventions must not be empty")
	for i, tc := range codegen.TitleConventions {
		assert.NotEmpty(t, tc.Pattern, "TitleConvention[%d]: Pattern must not be empty", i)
		assert.NotEmpty(t, tc.LabelRef, "TitleConvention[%d]: LabelRef must not be empty", i)
		assert.NotEmpty(t, tc.CreatedBy, "TitleConvention[%d]: CreatedBy must not be empty", i)
	}
}

// TestSkillBodySpecsCompleteness verifies all 7 SkillBodySpecs entries exist
// and each has non-empty Sections or Recipes (at least one must be populated).
func TestSkillBodySpecsCompleteness(t *testing.T) {
	expectedKeys := []string{
		"supervisor", "supervisor-plan-tasks", "supervisor-spawn-worker",
		"worker", "architect", "reviewer", "impl-review",
	}

	// Skills known to have non-empty preambles.
	skillsWithPreamble := map[string]bool{
		"supervisor":              true,
		"supervisor-plan-tasks":   true,
		"supervisor-spawn-worker": true,
		"worker":                  true,
		"architect":               true,
		"reviewer":                true,
		"impl-review":             true,
	}

	require.NotNil(t, codegen.SkillBodySpecs, "SkillBodySpecs must not be nil")
	require.Len(t, codegen.SkillBodySpecs, len(expectedKeys),
		"SkillBodySpecs should have exactly %d entries", len(expectedKeys))

	for _, key := range expectedKeys {
		t.Run(key, func(t *testing.T) {
			body, ok := codegen.SkillBodySpecs[key]
			require.True(t, ok,
				"SkillBodySpecs missing entry for %q", key)

			hasContent := len(body.Sections) > 0 || len(body.Recipes) > 0
			assert.True(t, hasContent,
				"SkillBodySpecs[%q] must have non-empty Sections or Recipes — "+
					"found %d sections and %d recipes",
				key, len(body.Sections), len(body.Recipes))

			// Verify preamble for skills known to have one.
			if skillsWithPreamble[key] {
				assert.NotEmpty(t, body.Preamble,
					"SkillBodySpecs[%q].Preamble must not be empty (skill is expected to have a preamble)", key)
			}

			// Verify that sections have non-empty titles and content.
			for i, section := range body.Sections {
				assert.NotEmpty(t, section.Title,
					"SkillBodySpecs[%q].Sections[%d].Title must not be empty", key, i)
				// Content may be empty for pure-subsection parents, but
				// if there are no subsections, content must be non-empty.
				if len(section.Subsections) == 0 {
					assert.NotEmpty(t, section.Content,
						"SkillBodySpecs[%q].Sections[%d].Content must not be empty (no subsections)",
						key, i)
				}
			}

			// Verify that recipes have non-empty titles and code.
			for i, recipe := range body.Recipes {
				assert.NotEmpty(t, recipe.Title,
					"SkillBodySpecs[%q].Recipes[%d].Title must not be empty", key, i)
				assert.NotEmpty(t, recipe.Code,
					"SkillBodySpecs[%q].Recipes[%d].Code must not be empty", key, i)
			}

			// Verify that body behaviors (if any) have non-empty GWT fields.
			for i, b := range body.Behaviors {
				assert.NotEmpty(t, b.ID,
					"SkillBodySpecs[%q].Behaviors[%d].ID must not be empty", key, i)
				assert.NotEmpty(t, b.Given,
					"SkillBodySpecs[%q].Behaviors[%d].Given must not be empty", key, i)
				assert.NotEmpty(t, b.When,
					"SkillBodySpecs[%q].Behaviors[%d].When must not be empty", key, i)
				assert.NotEmpty(t, b.Then,
					"SkillBodySpecs[%q].Behaviors[%d].Then must not be empty", key, i)
				assert.NotEmpty(t, b.ShouldNot,
					"SkillBodySpecs[%q].Behaviors[%d].ShouldNot must not be empty", key, i)
			}

			// Fix 8: Verify no subsection has its own subsections (max depth = 2 levels).
			// The skill.go.tmpl / skill_sub.go.tmpl templates only render 2 levels of sections;
			// deeper nesting would be silently dropped.
			for i, section := range body.Sections {
				for j, sub := range section.Subsections {
					assert.Empty(t, sub.Subsections,
						"SkillBodySpecs[%q].Sections[%d].Subsections[%d] has nested Subsections — "+
							"max depth is 2 levels (template only renders H2 and H3); "+
							"deeper nesting would be silently lost",
						key, i, j)
				}
			}
		})
	}
}
