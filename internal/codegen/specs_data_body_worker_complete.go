// Body content for the worker-complete skill SKILL.md.
// Ported from aura-plugins/skills/worker-complete/SKILL.md.
package codegen

var workerCompleteBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9`,

	Behaviors: []BehaviorSpec{
		{
			ID:        "wcomp-quality-gates",
			Given:     "implementation done",
			When:      "signaling",
			Then:      "verify the project's quality gates pass",
			ShouldNot: "report done with failing checks",
		},
		{
			ID:        "wcomp-checklist",
			Given:     "validation_checklist",
			When:      "completing",
			Then:      "confirm all items satisfied",
			ShouldNot: "complete with unchecked items",
		},
		{
			ID:        "wcomp-beads-update",
			Given:     "completion",
			When:      "reporting",
			Then:      "update Beads task status",
			ShouldNot: "omit Beads update",
		},
		{
			ID:        "wcomp-handoff-doc",
			Given:     "completion",
			When:      "handing off to reviewer",
			Then:      "create handoff document at `.git/.aura/handoff/<request-task-id>/worker-<N>-to-reviewer.md`",
			ShouldNot: "skip handoff for actor transitions",
		},
	},

	Sections: []ProseSection{
		{
			ID:      "wcomp-when-to-use",
			Title:   "When to Use",
			Content: `Implementation complete and all checks pass.`,
		},
		{
			ID:    "wcomp-steps",
			Title: "Steps",
			Content: `1. Run the project's quality gates (type checking + tests) - must pass
2. **Verify production code path via code inspection:**
   - [ ] Tests import production code (not test-only export)
   - [ ] No dual-export anti-pattern
   - [ ] No TODO placeholders in production code
   - [ ] Service wired with real dependencies (not mocks in production)
3. Verify all validation_checklist items satisfied:
   ` + "```bash" + `
   bd show <task-id>  # Review checklist items
   ` + "```" + `
4. Update Beads task:
   ` + "```bash" + `
   bd update <task-id> --status=done
   bd update <task-id> --notes="Implementation complete. Production code verified working."
   ` + "```" + `
5. Create handoff document for reviewer transition`,
		},
		{
			ID:      "wcomp-handoff-template",
			Title:   "Handoff Template (Worker → Reviewer)",
			Content: "",
			Subsections: []ProseSection{
				{
					ID:      "wcomp-handoff-storage",
					Title:   "Storage",
					Content: `Path: ` + "`.git/.aura/handoff/<request-task-id>/worker-<N>-to-reviewer.md`",
				},
				{
					ID:    "wcomp-handoff-content",
					Title: "Template",
					Content: "```markdown" + `
# Handoff: Worker <N> → Reviewer

## Context
- Request: <request-task-id>
- URD: <urd-task-id>
- Slice: SLICE-<N>
- Task ID: <slice-task-id>

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
` + "```",
				},
			},
		},
		{
			ID:    "wcomp-report-completion",
			Title: "Report Completion",
			Content: "```bash" + `
# Close the task and add completion notes
bd close <task-id>
bd comments add <task-id> "Implementation complete. Quality gates pass. Production code verified."
` + "```",
		},
		{
			ID:    "wcomp-followup-slice",
			Title: "Follow-up Slice Completion (FOLLOWUP_SLICE-N)",
			Content: `When completing a FOLLOWUP_SLICE-N, additionally report which original leaf tasks were resolved:

` + "```bash" + `
bd comments add <task-id> "Implementation complete. Resolved leaf tasks: <leaf-task-id-1>, <leaf-task-id-2>"
` + "```" + `

The handoff to the reviewer (h4) must include which original leaf tasks were resolved so reviewers can verify.`,
		},
	},

	Recipes: []RecipeBlock{},
}
