// Body content for the architect-request-review skill SKILL.md.
// Ported from aura-plugins/skills/architect-request-review/SKILL.md.
package codegen

var architectRequestReviewBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phase 4",

	Behaviors: []BehaviorSpec{
		{
			Id:        "arch-review-spawn-3-axes",
			Given:     "plan ready",
			When:      "requesting review",
			Then:      "spawn 3 axis-specific reviewers (A=Correctness, B=Test quality, C=Elegance)",
			ShouldNot: "spawn reviewers without axis assignment",
		},
		{
			Id:        "arch-review-provide-context",
			Given:     "reviewers",
			When:      "assigning",
			Then:      "provide Beads task ID and context",
			ShouldNot: "expect reviewers to search",
		},
	},

	Sections: []ProseSection{
		{
			Id:      "arch-review-when-to-use",
			Title:   "When to Use",
			Content: "Plan draft complete, ready for review.",
		},
		{
			Id:    "arch-review-naming",
			Title: "REVIEW Naming",
			Content: "Reviews are named `PROPOSAL-N-REVIEW-{axis}-{round}` where:\n" +
				"- N = proposal number (matches PROPOSAL-N)\n" +
				"- axis = reviewer criteria axis (A, B, or C)\n" +
				"- round = review round number (1, 2, ...)",
			Subsections: []ProseSection{
				{
					Id:    "arch-review-axes",
					Title: "Review Axes",
					Content: "| Axis | Focus | Key Questions |\n" +
						"|------|-------|---------------|\n" +
						"| **A** | Correctness (spirit and technicality) | Does it faithfully serve the user? Are technical decisions consistent with rationale? |\n" +
						"| **B** | Test quality | Integration over unit? SUT not mocked? Shared fixtures? Assert outcomes? |\n" +
						"| **C** | Elegance and complexity matching | Right API? Not over/under-engineered? Complexity proportional to problem? |",
				},
			},
		},
		{
			Id:    "arch-review-steps",
			Title: "Steps",
			Content: "1. Verify PROPOSAL-N task is complete with all sections\n" +
				"2. Spawn three reviewers with the task ID and URD reference:\n\n" +
				"```\n" +
				"Task(description: \"Reviewer A: correctness\", prompt: \"Review PROPOSAL-1 task <task-id>. URD: <urd-id> (read for requirements context). You are Reviewer A (Correctness). Focus: Does it faithfully serve the user? Are technical decisions consistent with rationale? Create review task titled PROPOSAL-1-REVIEW-A-1...\", subagent_type: \"general-purpose\")\n" +
				"Task(description: \"Reviewer B: test quality\", prompt: \"Review PROPOSAL-1 task <task-id>. URD: <urd-id> (read for requirements context). You are Reviewer B (Test quality). Focus: Integration over unit? SUT not mocked? Shared fixtures? Assert outcomes? Create review task titled PROPOSAL-1-REVIEW-B-1...\", subagent_type: \"general-purpose\")\n" +
				"Task(description: \"Reviewer C: elegance\", prompt: \"Review PROPOSAL-1 task <task-id>. URD: <urd-id> (read for requirements context). You are Reviewer C (Elegance). Focus: Right API? Not over/under-engineered? Complexity proportional to problem? Create review task titled PROPOSAL-1-REVIEW-C-1...\", subagent_type: \"general-purpose\")\n" +
				"```\n\n" +
				"3. Wait for all 3 reviewers to vote ACCEPT",
		},
		{
			Id:      "arch-review-consensus",
			Title:   "Consensus",
			Content: "**All 3 reviewers must vote ACCEPT.** Max revision rounds until consensus.",
		},
		{
			Id:    "arch-review-checking",
			Title: "Checking Reviews",
			Content: "```bash\n" +
				"bd show <proposal-id>\n" +
				"bd comments <proposal-id>\n" +
				"```",
		},
		{
			Id:    "arch-review-coordination",
			Title: "Coordination",
			Content: "```bash\n" +
				"# Add comment to notify that review is ready\n" +
				"bd comments add <proposal-id> \"Review requested — 3 reviewers spawned\"\n" +
				"\n" +
				"# Check for review votes\n" +
				"bd comments <proposal-id>\n" +
				"```",
		},
		{
			Id:    "arch-review-followup",
			Title: "Follow-up Proposal Reviews (FOLLOWUP_PROPOSAL-N)",
			Content: "For FOLLOWUP_PROPOSAL-N reviews, use the same procedure:\n" +
				"- **Review task naming:** `FOLLOWUP_PROPOSAL-N-REVIEW-{axis}-{round}`\n" +
				"- Same 3 axes (A/B/C), same binary ACCEPT/REVISE vote\n" +
				"- No severity tree for plan reviews (same as original plan reviews)\n" +
				"- Reviewers should also verify that FOLLOWUP_PROPOSAL addresses the specific IMPORTANT/MINOR findings scoped in FOLLOWUP_URE/URD",
		},
	},

	Recipes: []RecipeBlock{},
}
