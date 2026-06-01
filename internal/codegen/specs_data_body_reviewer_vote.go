// Body content for the reviewer-vote skill SKILL.md.
// Ported from aura-plugins/skills/reviewer-vote/SKILL.md.
package codegen

var reviewerVoteBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phases 4 + 10`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "rev-vote-criteria",
			Given:     "review complete",
			When:      "voting",
			Then:      "choose based on end-user alignment criteria",
			ShouldNot: "vote without applying all criteria",
		},
		{
			Id:        "rev-vote-rationale",
			Given:     "vote to record",
			When:      "recording",
			Then:      "add comment to Beads task with justification",
			ShouldNot: "vote without written rationale",
		},
		{
			Id:        "rev-vote-severity-tree",
			Given:     "code review",
			When:      "voting",
			Then:      "be aware that findings are tracked via severity tree (BLOCKER/IMPORTANT/MINOR)",
			ShouldNot: "duplicate severity findings in vote comment",
		},
	},

	Sections: []ProseSection{
		{
			Id:      "rev-vote-when-to-use",
			Title:   "When to Use",
			Content: `Review complete and ready to cast a binary ACCEPT or REVISE vote.`,
		},
		fragRef(FragRevVoteOptions),
		{
			Id:    "rev-vote-plan-vs-code",
			Title: "Plan Review vs Code Review",
			Content: `- **Plan review (Phase 4, ` + "`aura:p4-plan:s4-review`" + `):** ACCEPT/REVISE only. No severity tree.
- **Code review (Phase 10, ` + "`aura:p10-impl:s10-review`" + `):** ACCEPT/REVISE vote. Findings tracked via severity tree (3 groups: BLOCKER, IMPORTANT, MINOR created per round).`,
		},
		{
			Id:      "rev-vote-consensus",
			Title:   "Consensus",
			Content: `**All 3 reviewers must vote ACCEPT** for plan to be ratified or code to be approved.`,
		},
		{
			Id:    "rev-vote-beads",
			Title: "Adding Vote to Beads",
			Content: "```" + `bash` + "\n" +
				`# If accepting:
bd comments add <task-id> "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
bd comments add <task-id> "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."` + "\n" +
				"```",
		},
		{
			Id:      "rev-vote-report",
			Title:   "Report Vote",
			Content: `Votes are recorded via beads comments (see "Adding Vote to Beads" above). No separate messaging step is needed.`,
		},
	},
}
