// Body content for the supervisor-commit skill SKILL.md.
// Ported from aura-plugins/skills/supervisor-commit/SKILL.md.
package codegen

var supervisorCommitBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-12-landing)** <- Phase 12`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "sup-commit-gates-first",
			Given:     "all files ready",
			When:      "committing",
			Then:      "run quality gates (type checking + tests) first — must pass before staging or committing",
			ShouldNot: "commit without quality gates passing",
		},
		{
			Id:        "sup-commit-message-format",
			Given:     "commit message",
			When:      "formatting",
			Then:      "reference Pasture task IDs in the trailer (Task: ${TASK_A_URI}, ${TASK_B_URI})",
			ShouldNot: "use vague messages without task references",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "sup-commit-when-to-use",
			Title:   "When to Use",
			Content: "All workers for a review wave have completed successfully — quality gates pass, Pasture tasks updated, IMPL_PLAN ready for progress note.",
		},
		{
			Id:      "sup-commit-steps",
			Title:   "Steps",
			Content: "1. Run quality gates (type checking + tests) — must pass\n2. Stage changed files\n3. Create commit with format below\n4. Close Pasture tasks\n5. Update IMPL_PLAN progress",
		},
		{
			Id:    "sup-commit-format",
			Title: "Commit Format",
			Content: `` + "```" + `
feat|fix|docs|refactor(scope): Description

Files: file1.go, file2.go
Task: ${TASK_A_URI}, ${TASK_B_URI}
Ratified-Plan: ${RATIFIED_PLAN_ID_URI}

Co-Authored-By: Claude <noreply@anthropic.com>
` + "```" + ``,
		},
		{
			Id:    "sup-commit-close-task",
			Title: "Close Pasture Tasks",
			Content: `` + "```" + `bash
pasture task close "${TASK_A_URI}" --reason="Committed in <commit-hash>"
pasture task close "${TASK_B_URI}" --reason="Committed in <commit-hash>"
` + "```" + ``,
		},
		{
			Id:    "sup-commit-update-impl-plan",
			Title: "Update IMPL_PLAN",
			Content: `` + "```" + `bash
pasture task update "${IMPL_PLAN_ID_URI}" --notes="SLICE-N complete: "${TASK_A_URI}", ${TASK_B_URI}"
` + "```" + ``,
		},
		{
			Id:    "sup-commit-followup",
			Title: "Follow-up Commits",
			Content: `For follow-up slices, add ` + "`" + `Followup-Epic:` + "`" + ` to the commit message trailer:

` + "```" + `
feat|fix(scope): Description (follow-up)

Files: file1.go, file2.go
Task: ${TASK_A_URI} (FOLLOWUP_SLICE-1)
Followup-Epic: ${TASK_B_URI}
Ratified-Plan: ${TASK_C_URI} (FOLLOWUP_PROPOSAL-1)

Co-Authored-By: Claude <noreply@anthropic.com>
` + "```" + ``,
		},
		{
			Id:    "sup-commit-commands",
			Title: "Commands",
			Content: `` + "```" + `bash
# Set these to the reviewed canonical source and direct test paths for this slice.
SLICE_SOURCE_FILE=path/to/source.go
SLICE_TEST_FILE=path/to/source_test.go
git add "$SLICE_SOURCE_FILE" "$SLICE_TEST_FILE"
git agent-commit -m "..."
` + "```" + ``,
		},
	},

	Recipes: []RecipeBlock{},
}
