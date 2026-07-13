// Body content for the reviewer role SKILL.md.
package codegen

var reviewerBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)**",
	Sections: []ProseSection{
		{
			Id:    "rev-plan-vs-code",
			Title: "Plan Review vs Code Review",
			Content: `| Aspect | Plan Review (Phase 4) | Code Review (Phase 10) |
|--------|-----------------------|------------------------|
| Label | ` + "`pasture:p4-plan:s4-review`" + ` | ` + "`pasture:p10-impl:s10-review`" + ` |
| Vote | ACCEPT / REVISE (binary) | ACCEPT / REVISE (binary) |
| Severity tree | **NO** — no severity groups | **YES** — EAGER creation (always 3 groups) |
| Naming | PROPOSAL-N-REVIEW-{axis}-{round} | SLICE-N-REVIEW-{axis}-{round} |
| Focus | End-user alignment, MVP scope | Production code paths, severity findings |`,
		},
		{
			Id:    "rev-end-user-alignment",
			Title: "End-User Alignment Criteria",
			Content: `All reviewers also apply these general questions:

1. **Who are the end-users?**
2. **What would end-users want?**
3. **How would this affect them?**
4. **Are there implementation gaps?**
5. **Does MVP scope make sense?**
6. **Is validation checklist complete and correct?**`,
		},
		fragRef(FragRevVoteOptions),
		{
			Id:    "rev-severity-vocab",
			Title: "Severity Vocabulary (Code Review Only)",
			Content: `| Severity | When to Use | Blocks Slice? |
|----------|-------------|---------------|
| BLOCKER | Security, type errors, test failures, broken production code paths | Yes |
| IMPORTANT | Performance, missing validation, architectural concerns | No (follow-up epic) |
| MINOR | Style, optional optimizations, naming improvements | No (follow-up epic) |`,
		},
		{
			Id:    "rev-followup-lifecycle",
			Title: "Follow-up Lifecycle Reviews",
			Content: `Reviewers also participate in the follow-up lifecycle:

- **FOLLOWUP_PROPOSAL review (Phase 4):** Same procedure as standard plan review. Task naming: ` + "`FOLLOWUP_PROPOSAL-N-REVIEW-{axis}-{round}`" + `. Binary ACCEPT/REVISE, no severity tree.
- **FOLLOWUP_SLICE code review (Phase 10):** Same procedure as standard code review. Task naming: ` + "`FOLLOWUP_SLICE-N-REVIEW-{axis}-{round}`" + `. Full EAGER severity tree (BLOCKER/IMPORTANT/MINOR).
- **All severities reach 0 (no followup-of-followup):** ALL findings (BLOCKER/IMPORTANT/MINOR) from a FOLLOWUP_SLICE code review must reach 0 before the follow-up wave closes — they are never re-routed to a follow-up epic. The FOLLOWUP epic is fed only by user-DEFER'd UAT items.`,
		},
		{
			Id:    "rev-beads-process",
			Title: "Beads Review Process",
			Content: `Read the plan and URD:
` + "```bash\n" +
				`bd show <task-id>
bd show <urd-id>   # Read URD for user requirements context
` + "```" + `

Add review comment with vote:
` + "```bash\n" +
				`# If accepting:
bd comments add <task-id> "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
bd comments add <task-id> "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."
` + "```",
		},
		{
			Id:    "rev-consensus",
			Title: "Consensus",
			Content: `All 3 reviewers must vote ACCEPT for plan to be ratified. If any reviewer votes REVISE:
1. Architect creates PROPOSAL-N+1 addressing feedback
2. Old proposal marked ` + "`pasture:superseded`" + `
3. Reviewers re-review new proposal
4. Repeat until all ACCEPT`,
		},
	},
	Behaviors: []BehaviorSpec{
		{
			Id:        "rev-review-task-creation",
			Given:     "review complete",
			When:      "documenting findings",
			Then:      "create review task with dependency chain linking findings to the reviewed artifact",
			ShouldNot: "vote without creating a review task",
		},
	},
}
