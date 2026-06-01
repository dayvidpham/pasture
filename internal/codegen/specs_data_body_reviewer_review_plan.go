// Body content for the reviewer-review-plan skill SKILL.md.
// Ported from aura-plugins/skills/reviewer-review-plan/SKILL.md.
package codegen

var reviewerReviewPlanBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phase 4`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "rev-plan-alignment",
			Given:     "plan assignment",
			When:      "reviewing",
			Then:      "apply end-user alignment criteria",
			ShouldNot: "focus only on technical details",
		},
		{
			Id:        "rev-plan-revise-actionable",
			Given:     "issues found",
			When:      "voting",
			Then:      "vote REVISE with specific feedback",
			ShouldNot: "vote REVISE without actionable suggestions",
		},
		{
			Id:        "rev-plan-document",
			Given:     "review complete",
			When:      "documenting",
			Then:      "add comment to Beads task",
			ShouldNot: "vote without written justification",
		},
		{
			Id:        "rev-plan-binary-vote",
			Given:     "plan review",
			When:      "assessing",
			Then:      "use ACCEPT/REVISE binary vote only",
			ShouldNot: "create severity tree for plan reviews",
		},
	},

	Sections: []ProseSection{
		{
			Id:      "rev-plan-when-to-use",
			Title:   "When to Use",
			Content: `Assigned to review a plan specification (Phase 4, ` + "`pasture:p4-plan:s4-review`" + `).`,
		},
		{
			Id:    "rev-plan-criteria",
			Title: "End-User Alignment Criteria",
			Content: `Ask these questions for every plan:

1. **Who are the end-users?**
2. **What would end-users want?**
3. **How would this affect them?**
4. **Are there implementation gaps?**
5. **Does MVP scope make sense?**
6. **Is validation checklist complete and correct?**`,
		},
		{
			Id:    "rev-plan-production-code",
			Title: "Production Code Path Questions",
			Content: `When reviewing plans, explicitly ask:

1. **What are the production code paths?**
   - CLI commands: Entry points users will run
   - API endpoints: HTTP handlers, services
   - Background jobs: Daemon processes

2. **How will production code be tested?**
   - Do Layer 2 tests import the actual CLI/API?
   - Or do they test a separate test-only export? (anti-pattern)

3. **What needs to be wired together?**
   - Service instantiation with real dependencies?
   - CLI command registration?
   - Entry point hookup?

4. **Are implementation tasks explicit about production code?**
   - Does the plan include tasks to wire production code?
   - Or are they only testing isolated units?

**Red flag:** Plan shows "Layer 2: service_test.go" but no task for "wire service into CLI command"

**Green flag:** Plan shows "Layer 3: Wire cobra command with NewService(realDeps)"`,
		},
		{
			Id:      "rev-plan-steps",
			Title:   "Steps",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "rev-plan-step1-read",
					Title: "Step 1: Read PROPOSAL-N and URD",
					Content: "```" + `bash` + "\n" +
						`bd show <proposal-id>
bd show <urd-id>   # Read URD for user requirements context` + "\n" +
						"```",
				},
				{
					Id:      "rev-plan-step2-criteria",
					Title:   "Step 2: Apply Criteria",
					Content: `Apply end-user alignment criteria (check against URD requirements). Verify ` + "`validation_checklist`" + ` items are verifiable and BDD acceptance criteria are complete.`,
				},
				{
					Id:    "rev-plan-step3-create",
					Title: "Step 3: Create Review Task",
					Content: "```" + `bash` + "\n" +
						`bd create --labels "pasture:p4-plan:s4-review" \
  --title "PROPOSAL-1-REVIEW-A-1: <feature>" \
  --description "---
references:
  proposal: <proposal-id>
  urd: <urd-id>
---
VOTE: <ACCEPT|REVISE> - <justification>"
bd dep add <proposal-id> --blocked-by <review-id>` + "\n" +
						"```",
				},
				{
					Id:    "rev-plan-step4-vote",
					Title: "Step 4: Add Vote Comment",
					Content: "```" + `bash` + "\n" +
						`# If accepting:
bd comments add <proposal-id> "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
bd comments add <proposal-id> "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."` + "\n" +
						"```",
				},
			},
		},
		// SLICE-4: promoted to fragment for registry completeness and D2
		// distinctness enforcement. Content verbatim → golden byte-identical.
		fragRef(FragRevPlanVoteOptions),
		{
			Id:      "rev-plan-consensus",
			Title:   "Consensus",
			Content: `All 3 reviewers must vote ACCEPT for plan to be ratified.`,
		},
		{
			Id:    "rev-plan-followup",
			Title: "Follow-up Proposal Reviews (FOLLOWUP_PROPOSAL-N)",
			Content: `The same procedure applies when reviewing FOLLOWUP_PROPOSAL-N:
- **Task naming:** ` + "`FOLLOWUP_PROPOSAL-N-REVIEW-{axis}-{round}`" + `
- Same binary ACCEPT/REVISE vote (no severity tree)
- Additionally verify that FOLLOWUP_PROPOSAL addresses the specific IMPORTANT/MINOR findings scoped in FOLLOWUP_URE/URD`,
		},
	},
}
