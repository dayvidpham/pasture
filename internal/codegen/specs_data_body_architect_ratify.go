// Body content for the architect-ratify skill SKILL.md.
// Ported from aura-plugins/skills/architect-ratify/SKILL.md.
package codegen

var architectRatifyBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-6-ratification)** <- Phase 6",

	Behaviors: []BehaviorSpec{
		{
			Id:        "arch-ratify-all-accept",
			Given:     "all 3 reviewers voted ACCEPT",
			When:      "ratifying",
			Then:      "add `pasture:p6-plan:s6-ratify` label to PROPOSAL-N",
			ShouldNot: "ratify with any REVISE votes outstanding",
		},
		{
			Id:        "arch-ratify-audit-trail",
			Given:     "ratification",
			When:      "documenting",
			Then:      "add comment with reviewer sign-offs and UAT reference",
			ShouldNot: "ratify without audit trail",
		},
		{
			Id:        "arch-ratify-supersede-old",
			Given:     "previous proposals exist",
			When:      "ratifying new version",
			Then:      "mark old proposals as `pasture:superseded`",
			ShouldNot: "leave old proposals without superseded marking",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "arch-ratify-when-to-use",
			Title:   "When to Use",
			Content: "All 3 reviewers have voted ACCEPT on PROPOSAL-N and user has approved via UAT.",
		},
		{
			Id:    "arch-ratify-consensus-requirement",
			Title: "Consensus Requirement",
			Content: "**All 3 reviewers must vote ACCEPT.** If any reviewer votes REVISE:\n" +
				"1. Architect creates PROPOSAL-N+1 addressing feedback\n" +
				"2. Marks PROPOSAL-N as `pasture:superseded`\n" +
				"3. Reviewers re-review PROPOSAL-N+1\n" +
				"4. Repeat until all ACCEPT",
		},
		{
			Id:    "arch-ratify-steps",
			Title: "Steps",
			Subsections: []ProseSection{
				{
					Id:    "arch-ratify-step1-check",
					Title: "Step 1: Check all reviews",
					Content: `` + "```" + `bash
pasture task show "${PROPOSAL_ID_URI}"
pasture task comments "${PROPOSAL_ID_URI}"
` + "```" + ``,
				},
				{
					Id:      "arch-ratify-step2-verify",
					Title:   "Step 2: Verify all 3 votes are ACCEPT",
					Content: "Confirm each of the three review tasks (Reviewer A, B, C) has a VOTE: ACCEPT comment before proceeding.",
				},
				{
					Id:    "arch-ratify-step3-label",
					Title: "Step 3: Add ratify label to PROPOSAL-N",
					Content: `Do NOT create a new task — add label to the existing proposal:
` + "```" + `bash
pasture task label add "${PROPOSAL_ID_URI}" pasture:p6-plan:s6-ratify
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "RATIFIED: All 3 reviewers ACCEPT, UAT passed ("${UAT_ID_URI}")"
` + "```" + ``,
				},
				{
					Id:    "arch-ratify-step4-supersede",
					Title: "Step 4: Mark all previous proposals as superseded",
					Content: `` + "```" + `bash
pasture task label add "${OLD_PROPOSAL_ID_URI}" pasture:superseded
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${OLD_PROPOSAL_ID_URI}" "Superseded by PROPOSAL-N ("${RATIFIED_PROPOSAL_ID_URI}")"
` + "```" + ``,
				},
				{
					Id:    "arch-ratify-step5-urd",
					Title: "Step 5: Update URD with ratification",
					Content: `` + "```" + `bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${URD_ID_URI}" "Ratified: scope confirmed. Ratified proposal: ${RATIFIED_PROPOSAL_ID_URI}"
` + "```" + ``,
				},
			},
		},
		{
			Id:    "arch-ratify-next-steps",
			Title: "Next Steps",
			Content: "After ratifying PROPOSAL-N:\n" +
				"1. **Prepare handoff** — Run `/pasture:architect-handoff` to create handoff document and spawn supervisor\n\n" +
				"**IMPORTANT:** Do NOT start implementation yourself. The architect's role ends at handoff. " +
				"Implementation is handled by the supervisor and workers spawned during handoff.",
		},
		{
			Id:      "arch-ratify-followup",
			Title:   "Follow-up Proposals (FOLLOWUP_PROPOSAL-N)",
			Content: "When ratifying a FOLLOWUP_PROPOSAL-N, the next step is the same h1 handoff but scoped to the follow-up epic:\n- **Storage:** the follow-up handoff is authored in its HANDOFF Pasture task body (no filesystem path)\n- The supervisor then creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks\n- The follow-up scope comes from the user-DEFER'd UAT items the FOLLOWUP epic was created from",
		},
	},

	Recipes: []RecipeBlock{},
}
