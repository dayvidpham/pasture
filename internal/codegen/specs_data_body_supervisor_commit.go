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
			Then:      "reference Beads task IDs in the trailer (Task: aura-xxx, aura-yyy)",
			ShouldNot: "use vague messages without task references",
		},
	},

	Sections: []ProseSection{
		{
			Id:      "sup-commit-when-to-use",
			Title:   "When to Use",
			Content: `All workers for a layer have completed successfully — quality gates pass, Beads tasks updated, IMPL_PLAN ready for progress note.`,
		},
		{
			Id:    "sup-commit-steps",
			Title: "Steps",
			Content: `1. Run quality gates (type checking + tests) — must pass
2. Stage changed files
3. Create commit with format below
4. Close Beads tasks
5. Update IMPL_PLAN progress`,
		},
		{
			Id:    "sup-commit-format",
			Title: "Commit Format",
			Content: "```" + `
feat|fix|docs|refactor(scope): Description

Files: file1.go, file2.go
Task: aura-xxx, aura-yyy
Ratified-Plan: <ratified-plan-id>

Co-Authored-By: Claude <noreply@anthropic.com>
` + "```",
		},
		{
			Id:    "sup-commit-close-beads",
			Title: "Close Beads Tasks",
			Content: "```" + `bash
bd close aura-xxx aura-yyy --reason="Committed in <commit-hash>"
` + "```",
		},
		{
			Id:    "sup-commit-update-impl-plan",
			Title: "Update IMPL_PLAN",
			Content: "```" + `bash
bd update <impl-plan-id> --notes="SLICE-N complete: aura-xxx, aura-yyy"
` + "```",
		},
		{
			Id:    "sup-commit-followup",
			Title: "Follow-up Commits",
			Content: `For follow-up slices, add ` + "`Followup-Epic:`" + ` to the commit message trailer:

` + "```" + `
feat|fix(scope): Description (follow-up)

Files: file1.go, file2.go
Task: aura-xxx (FOLLOWUP_SLICE-1)
Followup-Epic: aura-yyy
Ratified-Plan: aura-zzz (FOLLOWUP_PROPOSAL-1)

Co-Authored-By: Claude <noreply@anthropic.com>
` + "```",
		},
		{
			Id:    "sup-commit-commands",
			Title: "Commands",
			Content: "```" + `bash
git add <files>
git agent-commit -m "..."
` + "```",
		},
	},

	Recipes: []RecipeBlock{},
}
