// Body content for the worker-complete skill SKILL.md.
// Ported from aura-plugins/skills/worker-complete/SKILL.md.
package codegen

var workerCompleteBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "wcomp-quality-gates",
			Given:     "implementation done",
			When:      "signaling",
			Then:      "verify the project's quality gates pass",
			ShouldNot: "report done with failing checks",
		},
		{
			Id:        "wcomp-checklist",
			Given:     "validation_checklist",
			When:      "completing",
			Then:      "confirm all items satisfied",
			ShouldNot: "complete with unchecked items",
		},
		{
			Id:        "wcomp-task-update",
			Given:     "completion",
			When:      "reporting",
			Then:      "record completion evidence without closing the task",
			ShouldNot: "omit Pasture update",
		},
		{
			Id:        "wcomp-handoff-doc",
			Given:     "completion",
			When:      "handing off to reviewer",
			Then:      "author the worker→reviewer handoff in the Pasture task body (the slice/handoff task body IS the handoff)",
			ShouldNot: "skip handoff for actor transitions",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "wcomp-when-to-use",
			Title:   "When to Use",
			Content: `Implementation complete and all checks pass.`,
		},
		{
			Id:    "wcomp-steps",
			Title: "Steps",
			Content: `1. Run the project's quality gates (type checking + tests) - must pass
2. **Verify production code path via code inspection:**
   - [ ] Tests import production code (not test-only export)
   - [ ] No dual-export anti-pattern
   - [ ] No TODO placeholders in production code
   - [ ] Service wired with real dependencies (not mocks in production)
3. Verify all validation_checklist items satisfied:
   ` + "```" + `bash
   pasture task show "${TASK_ID_URI}"  # Review checklist items
   ` + "```" + `
4. Update Pasture task:
   ` + "```" + `bash
   pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "Implementation complete; awaiting independent review and supervisor closure."
   pasture task update "${TASK_ID_URI}" --notes="Implementation complete. Production code verified working."
   ` + "```" + `
5. Author the worker→reviewer handoff in the Pasture task body (see template below)`,
		},
		{
			Id:      "wcomp-handoff-template",
			Title:   "Handoff Template (Worker → Reviewer)",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:      "wcomp-handoff-storage",
					Title:   "Storage",
					Content: "Authored in the Pasture task body — the slice (or a dedicated handoff) task body IS the handoff. No filesystem path.",
				},
				{
					Id:    "wcomp-handoff-content",
					Title: "Template",
					Content: `` + "```" + `markdown
# Handoff: Worker <N> → Reviewer

## Context
- Request: ${REQUEST_ID_URI}
- URD: ${URD_ID_URI}
- Slice: SLICE-<N>
- Task ID: ${SLICE_TASK_ID_URI}

## What Was Implemented
- Production Code Path: <what end users run>
- Files Changed: <list of files>

## Key Decisions
- <decision 1>: <rationale>
- <decision 2>: <rationale>

## Quality Gates
- Type checking: PASS
- Tests: PASS
- Production code inspection: PASS (no TODOs, real deps wired)

## Areas of Concern
- <any areas the reviewer should pay special attention to>
` + "```" + ``,
				},
			},
		},
		{
			Id:    "wcomp-report-completion",
			Title: "Report Completion",
			Content: `` + "```" + `bash
# Report completion; only the supervisor closes after independent review
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "Implementation complete. Quality gates pass. Production code verified."
` + "```" + ``,
		},
		{
			Id:    "wcomp-followup-slice",
			Title: "Follow-up Slice Completion (FOLLOWUP_SLICE-N)",
			Content: `When completing a FOLLOWUP_SLICE-N, additionally report which original leaf tasks were resolved:

` + "```" + `bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "Implementation complete. Resolved leaf tasks: "${LEAF_TASK_ID_1_URI}", ${LEAF_TASK_ID_2_URI}"
` + "```" + `

The handoff to the reviewer (h4) must include which original leaf tasks were resolved so reviewers can verify.`,
		},
	},

	Recipes: []RecipeBlock{},
}
