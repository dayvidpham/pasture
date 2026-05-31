// Body content for the architect-ratify skill SKILL.md.
// Ported from aura-plugins/skills/architect-ratify/SKILL.md.
package codegen

var architectRatifyBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-6-ratification)** <- Phase 6",

	Behaviors: []BehaviorSpec{
		{
			ID:        "arch-ratify-all-accept",
			Given:     "all 3 reviewers voted ACCEPT",
			When:      "ratifying",
			Then:      "add `aura:p6-plan:s6-ratify` label to PROPOSAL-N",
			ShouldNot: "ratify with any REVISE votes outstanding",
		},
		{
			ID:        "arch-ratify-audit-trail",
			Given:     "ratification",
			When:      "documenting",
			Then:      "add comment with reviewer sign-offs and UAT reference",
			ShouldNot: "ratify without audit trail",
		},
		{
			ID:        "arch-ratify-supersede-old",
			Given:     "previous proposals exist",
			When:      "ratifying new version",
			Then:      "mark old proposals as `aura:superseded`",
			ShouldNot: "leave old proposals without superseded marking",
		},
	},

	Sections: []ProseSection{
		{
			ID:      "arch-ratify-when-to-use",
			Title:   "When to Use",
			Content: "All 3 reviewers have voted ACCEPT on PROPOSAL-N and user has approved via UAT.",
		},
		{
			ID:    "arch-ratify-consensus-requirement",
			Title: "Consensus Requirement",
			Content: "**All 3 reviewers must vote ACCEPT.** If any reviewer votes REVISE:\n" +
				"1. Architect creates PROPOSAL-N+1 addressing feedback\n" +
				"2. Marks PROPOSAL-N as `aura:superseded`\n" +
				"3. Reviewers re-review PROPOSAL-N+1\n" +
				"4. Repeat until all ACCEPT",
		},
		{
			ID:    "arch-ratify-steps",
			Title: "Steps",
			Subsections: []ProseSection{
				{
					ID:    "arch-ratify-step1-check",
					Title: "Step 1: Check all reviews",
					Content: "```bash\n" +
						"bd show <proposal-id>\n" +
						"bd comments <proposal-id>\n" +
						"```",
				},
				{
					ID:      "arch-ratify-step2-verify",
					Title:   "Step 2: Verify all 3 votes are ACCEPT",
					Content: "Confirm each of the three review tasks (Reviewer A, B, C) has a VOTE: ACCEPT comment before proceeding.",
				},
				{
					ID:    "arch-ratify-step3-label",
					Title: "Step 3: Add ratify label to PROPOSAL-N",
					Content: "Do NOT create a new task — add label to the existing proposal:\n" +
						"```bash\n" +
						"bd label add <proposal-id> aura:p6-plan:s6-ratify\n" +
						"bd comments add <proposal-id> \"RATIFIED: All 3 reviewers ACCEPT, UAT passed (<uat-task-id>)\"\n" +
						"```",
				},
				{
					ID:    "arch-ratify-step4-supersede",
					Title: "Step 4: Mark all previous proposals as superseded",
					Content: "```bash\n" +
						"bd label add <old-proposal-id> aura:superseded\n" +
						"bd comments add <old-proposal-id> \"Superseded by PROPOSAL-N (<ratified-proposal-id>)\"\n" +
						"```",
				},
				{
					ID:    "arch-ratify-step5-urd",
					Title: "Step 5: Update URD with ratification",
					Content: "```bash\n" +
						"bd comments add <urd-id> \"Ratified: scope confirmed. Ratified proposal: <ratified-proposal-id>\"\n" +
						"```",
				},
			},
		},
		{
			ID:    "arch-ratify-next-steps",
			Title: "Next Steps",
			Content: "After ratifying PROPOSAL-N:\n" +
				"1. **Prepare handoff** — Run `/aura:architect-handoff` to create handoff document and spawn supervisor\n\n" +
				"**IMPORTANT:** Do NOT start implementation yourself. The architect's role ends at handoff. " +
				"Implementation is handled by the supervisor and workers spawned during handoff.",
		},
		{
			ID:    "arch-ratify-followup",
			Title: "Follow-up Proposals (FOLLOWUP_PROPOSAL-N)",
			Content: "When ratifying a FOLLOWUP_PROPOSAL-N, the next step is the same h1 handoff but scoped to the follow-up epic:\n" +
				"- **Storage:** " + "`.git/.aura/handoff/{followup-epic-id}/architect-to-supervisor.md`" + "\n" +
				"- The supervisor then creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks\n" +
				"- Original IMPORTANT/MINOR leaf tasks are adopted as dual-parent children of FOLLOWUP_SLICE-N",
		},
	},

	Recipes: []RecipeBlock{},
}
