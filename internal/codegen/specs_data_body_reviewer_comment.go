// Body content for the reviewer-comment skill SKILL.md.
// Ported from aura-plugins/skills/reviewer-comment/SKILL.md.
package codegen

var reviewerCommentBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phases 4 + 10`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "rev-comment-structured",
			Given:     "findings to document",
			When:      "documenting",
			Then:      "use structured format with severity levels",
			ShouldNot: "leave unstructured feedback",
		},
		{
			Id:        "rev-comment-task",
			Given:     "comment to create",
			When:      "creating",
			Then:      "add via `pasture task comment add`",
			ShouldNot: "create standalone files for review comments",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "rev-comment-when-to-use",
			Title:   "When to Use",
			Content: `Documenting review findings for the permanent record. Applies to both plan reviews (Phase 4) and code reviews (Phase 10).`,
		},
		{
			Id:    "rev-comment-steps",
			Title: "Steps",
			Content: `1. Identify the task to comment on (` + "`" + `pasture task show "${TASK_ID_URI}"` + "`" + `)
2. Categorize findings by severity
3. Add structured comment via Pasture`,
		},
		{
			Id:    "rev-comment-task-command",
			Title: "Comment via Pasture",
			Content: `` + "```" + `bash
# Plan review comment (no severity tree)
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "VOTE: ACCEPT - End-user alignment confirmed. MVP scope achievable."

# Code review comment (with severity references)
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REVIEW_ID_URI}" "VOTE: REVISE - 1 BLOCKER found (see severity tree). Suggestion: fix type error in auth middleware."
` + "```" + ``,
		},
		{
			Id:    "rev-comment-format",
			Title: "Format",
			Content: "```" + `markdown` + "\n" +
				`VOTE: {ACCEPT | REVISE}

## Findings

### BLOCKER Issues
{list or "None"}

### IMPORTANT Issues
{list or "None"}

### MINOR Issues
{list or "None"}

## Conclusion
{assessment and next steps}` + "\n" +
				"```",
		},
		{
			Id:    "rev-comment-severity",
			Title: "Severity Vocabulary",
			Content: `| Severity | When to Use | Blocks? |
|----------|-------------|---------|
| BLOCKER | Security, type errors, test failures, broken production code paths | Yes (code review only) |
| IMPORTANT | Performance, missing validation, architectural concerns | Must reach 0 before review wave closes |
| MINOR | Style, optional optimizations, naming improvements | Must reach 0 before review wave closes |`,
		},
		{
			Id:    "rev-comment-plan-vs-code",
			Title: "Plan Review vs Code Review",
			Content: `- **Plan review (Phase 4, ` + "`pasture:p4-plan:s4-review`" + `):** ACCEPT/REVISE only. No severity tree. Findings are described inline in the vote comment.
- **Code review (Phase 10, ` + "`pasture:p10-impl:s10-review`" + `):** ACCEPT/REVISE vote + full severity tree with EAGER creation (3 groups per round). Findings are tracked as child tasks of severity groups.`,
		},
	},
}
