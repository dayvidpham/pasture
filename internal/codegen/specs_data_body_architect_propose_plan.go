// Body content for the architect-propose-plan skill SKILL.md.
// Ported from aura-plugins/skills/architect-propose-plan/SKILL.md.
package codegen

var architectProposePlanBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-3-proposal-n)** <- Phase 3",

	Behaviors: []BehaviorSpec{
		{
			Id:        "arch-propose-bdd-format",
			Given:     "feature request",
			When:      "proposing",
			Then:      "use BDD Given/When/Then format with acceptance criteria",
			ShouldNot: "write vague requirements",
		},
		{
			Id:        "arch-propose-checklist-required",
			Given:     "plan",
			When:      "creating task",
			Then:      "include validation_checklist and tradeoffs in design field",
			ShouldNot: "leave checklist empty",
		},
		{
			Id:        "arch-propose-revision-history",
			Given:     "existing plan",
			When:      "revising",
			Then:      "create PROPOSAL-N+1 task and mark old as `aura:superseded`",
			ShouldNot: "lose history",
		},
	},

	Sections: []ProseSection{
		{
			Id:      "arch-propose-when-to-use",
			Title:   "When to Use",
			Content: "Starting new feature design; creating formal plan for review.",
		},
		{
			Id:    "arch-propose-naming",
			Title: "PROPOSAL-N Naming",
			Content: "Proposals are numbered incrementally: PROPOSAL-1, PROPOSAL-2, etc. Each revision increments N. " +
				"Old proposals are marked `aura:superseded` with a comment explaining why.",
		},
		{
			Id:    "arch-propose-beads-task",
			Title: "Beads Task Creation",
			Content: "```bash\n" +
				"bd create --type=feature \\\n" +
				"  --labels=\"aura:p3-plan:s3-propose\" \\\n" +
				"  --title=\"PROPOSAL-1: <feature name>\" \\\n" +
				"  --description=\"$(cat <<'EOF'\n" +
				"---\n" +
				"references:\n" +
				"  request: <request-id>\n" +
				"  urd: <urd-id>\n" +
				"---\n" +
				"\n" +
				"## Problem Space\n" +
				"\n" +
				"**Axes of the problem:**\n" +
				"- Parallelism: ...\n" +
				"- Distribution: ...\n" +
				"\n" +
				"**Has-a / Is-a:**\n" +
				"- X HAS-A Y\n" +
				"- Z IS-A W\n" +
				"\n" +
				"## Engineering Tradeoffs\n" +
				"\n" +
				"| Option | Pros | Cons | Decision |\n" +
				"|--------|------|------|----------|\n" +
				"| A | ... | ... | Selected |\n" +
				"| B | ... | ... | Rejected |\n" +
				"\n" +
				"## MVP Milestone\n" +
				"\n" +
				"<scope with tradeoff rationale>\n" +
				"\n" +
				"## Public Interfaces\n" +
				"\n" +
				"\\`\\`\\`go\n" +
				"type Example interface { /* ... */ }\n" +
				"\\`\\`\\`\n" +
				"\n" +
				"## Types & Enums\n" +
				"\n" +
				"\\`\\`\\`go\n" +
				"type ExampleType int\n" +
				"\n" +
				"const (\n" +
				"    ExampleTypeA ExampleType = iota\n" +
				"    ExampleTypeB\n" +
				")\n" +
				"\\`\\`\\`\n" +
				"\n" +
				"## Validation Checklist\n" +
				"\n" +
				"### Phase 1\n" +
				"- [ ] Item 1\n" +
				"- [ ] Item 2\n" +
				"\n" +
				"### Phase 2\n" +
				"- [ ] Item 3\n" +
				"\n" +
				"## BDD Acceptance Criteria\n" +
				"\n" +
				"**Given** precondition\n" +
				"**When** action\n" +
				"**Then** outcome\n" +
				"**Should Not** negative case\n" +
				"\n" +
				"## Files Affected\n" +
				"- pkg/path/file1.go (create)\n" +
				"- pkg/path/file2.go (modify)\n" +
				"EOF\n" +
				")\" \\\n" +
				"  --design='{\"validation_checklist\":[\"Item 1\",\"Item 2\",\"Item 3\"],\"tradeoffs\":[{\"decision\":\"Use A\",\"rationale\":\"Because...\"}],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\",\"should_not\":\"W\"}]}'\n" +
				"\n" +
				"# Link to request\n" +
				"bd dep add <request-id> --blocked-by <proposal-id>\n" +
				"```",
		},
		{
			Id:    "arch-propose-before-creating",
			Title: "Before Creating the Proposal",
			Content: "Read the URD and Phase 1 outputs to understand full context before drafting:\n" +
				"```bash\n" +
				"bd show <urd-id>\n" +
				"bd show <request-id>   # includes classification, research findings, explore findings as comments\n" +
				"```\n\n" +
				"The URD contains the structured requirements, priorities, design choices, and MVP goals from the URE survey. " +
				"The REQUEST task comments contain Phase 1 outputs: classification (4 axes), domain research findings (prior art, standards), " +
				"and codebase exploration findings (entry points, related types, dependencies). Your proposal must:\n" +
				"- Trace back to URD requirements\n" +
				"- Incorporate research findings (prior art, domain standards) into engineering tradeoffs\n" +
				"- Reference explore findings (entry points, existing patterns) in the files affected section",
		},
		{
			Id:    "arch-propose-plan-structure",
			Title: "Plan Structure",
			Content: "- **Requirements Traceability: URD:** `<urd-id>`\n" +
				"- Problem Space (axes, has-a/is-a)\n" +
				"- Engineering Tradeoffs (table with decisions)\n" +
				"- MVP Milestone (scope with tradeoff rationale)\n" +
				"- Public Interfaces (Go)\n" +
				"- Types & Enums\n" +
				"- Validation Checklist (per phase)\n" +
				"- BDD Acceptance Criteria\n" +
				"- Files Affected",
		},
		{
			Id:    "arch-propose-next-steps",
			Title: "Next Steps",
			Content: "After creating PROPOSAL-N task:\n" +
				"1. Run `/aura:architect-request-review` to spawn 3 reviewers\n" +
				"2. Wait for all 3 reviewers to vote ACCEPT\n" +
				"3. Run `/aura:architect-ratify` to add ratify label to PROPOSAL-N",
		},
		{
			Id:    "arch-propose-followup",
			Title: "Follow-up Proposals (FOLLOWUP_PROPOSAL-N)",
			Content: "When creating proposals for a follow-up epic (received via h6 from supervisor):\n" +
				"- **Title prefix:** `FOLLOWUP_PROPOSAL-N:` (e.g., `FOLLOWUP_PROPOSAL-1: Add request-id correlation`)\n" +
				"- **References:** Include both `original_urd: <id>` and `followup_urd: <id>` in frontmatter\n" +
				"- **Content:** Address specific IMPORTANT/MINOR findings scoped in FOLLOWUP_URE/URD\n" +
				"- Same review/ratify/UAT lifecycle applies (3 reviewers, ACCEPT/REVISE, UAT, ratify, handoff)",
		},
	},

	Recipes: []RecipeBlock{},
}
