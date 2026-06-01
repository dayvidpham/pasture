// Body content for the epoch role SKILL.md.
// Ported from aura-plugins/skills/epoch/SKILL.md.
package codegen

var epochBody = SkillBody{
	Preamble: `**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md)** <- All 12 Phases`,

	Behaviors: []BehaviorSpec{
		{
			Id:        "epoch-verbatim-capture",
			Given:     "user provides request",
			When:      "capturing",
			Then:      "store verbatim without paraphrasing in Phase 1 REQUEST task",
			ShouldNot: "summarize or interpret the user's words",
		},
		{
			Id:        "epoch-dep-chain",
			Given:     "any phase transition",
			When:      "creating new task",
			Then:      "add dependency to previous: bd dep add <parent> --blocked-by <child>",
			ShouldNot: "skip dependency chaining",
		},
		{
			Id:        "epoch-audit-never-delete",
			Given:     "task completion",
			When:      "updating",
			Then:      "add comments and labels only",
			ShouldNot: "close or delete tasks prematurely",
		},
		{
			Id:        "epoch-consensus-required",
			Given:     "review cycle",
			When:      "any REVISE vote",
			Then:      "create PROPOSAL-N+1 and repeat review",
			ShouldNot: "proceed without full ACCEPT consensus from all 3 reviewers",
		},
		{
			Id:        "epoch-followup-trigger",
			Given:     "code review completion",
			When:      "ANY IMPORTANT or MINOR findings exist",
			Then:      "Supervisor creates a follow-up epic (label aura:epic-followup)",
			ShouldNot: "gate follow-up epic creation on BLOCKER resolution",
		},
		{
			Id:        "epoch-autonomous-progression",
			Given:     "non-user-gated phase completes",
			When:      "transitioning",
			Then:      "proceed autonomously; user-gated phases are: Phase 1 s1_1 (research depth), Phase 2 (URE), Phase 5 (Plan UAT), Phase 11 (Impl UAT)",
			ShouldNot: "ask 'Should I proceed?' for autonomous phases",
		},
		{
			Id:        "epoch-uat-auto-ratify",
			Given:     "Phase 5 UAT ACCEPT",
			When:      "transitioning to Phase 6",
			Then:      "ratify automatically",
			ShouldNot: "ask user for ratification confirmation",
		},
		{
			Id:        "epoch-frontmatter-refs",
			Given:     "cross-task references",
			When:      "linking related tasks (e.g. URD to REQUEST)",
			Then:      "use description frontmatter references: block",
			ShouldNot: "use peer-reference commands",
		},
	},

	Sections: []ProseSection{
		{
			Id:    "epoch-core-principles",
			Title: "Core Principles",
			Content: `1. **AUDIT TRAIL PRESERVATION** — Never delete or destroy information, labels, or tasks
2. **DEPENDENCY CHAINING** — Each task blocks its predecessor: ` + "`bd dep add <parent> --blocked-by <child>`" + `
3. **USER ENGAGEMENT** — URE and UAT at multiple checkpoints
4. **CONSENSUS REQUIRED** — All 3 reviewers must ACCEPT before proceeding
5. **EAGER SEVERITY TREE** — Code reviews (Phase 10) always create 3 severity groups (BLOCKER, IMPORTANT, MINOR); empty groups closed immediately
6. **FOLLOW-UP EPIC** — Triggered by review completion + ANY IMPORTANT/MINOR findings; NOT gated on BLOCKER resolution
7. **RIDE THE WAVE** — Phases 8-10 form one continuous cycle: Explore subagents (P8), workers implement (P9), ephemeral reviewers review (P10), max 3 fix cycles per slice; workers persist throughout`,
		},
		{
			Id:    "epoch-12-phase-workflow",
			Title: "The 12-Phase Workflow",
			Content: "```" + `
Phase 1:  aura:p1-user       -> REQUEST (classify, research, explore)
            s1_1-classify -> s1_2-research || s1_3-explore
Phase 2:  aura:p2-user       -> ELICIT (URE survey) + URD (single source of truth)
            s2_1-elicit -> s2_2-urd
Phase 3:  aura:p3-plan       -> PROPOSAL-N (architect proposes)
Phase 4:  aura:p4-plan       -> REVIEW (3 parallel reviewers, ACCEPT/REVISE)
Phase 5:  aura:p5-user       -> Plan UAT (user acceptance test)
Phase 6:  aura:p6-plan       -> Ratification (supersede old proposals)
Phase 7:  aura:p7-plan       -> Handoff (architect -> supervisor)
Phase 8:  aura:p8-impl       -> IMPL_PLAN (supervisor decomposes into slices; Explore subagents)
Phase 9:  aura:p9-impl       -> SLICE-N (parallel workers; Ride the Wave — workers persist for review)
Phase 10: aura:p10-impl      -> Code Review (ephemeral reviewers review all slices; max 3 fix cycles per slice)
Phase 11: aura:p11-user      -> Implementation UAT
Phase 12: aura:p12-impl      -> Landing (commit, push, hand off)
` + "```" + `

### Phase 1 Expanded: REQUEST

Phase 1 has 3 sub-steps:

| Sub-step | Label | Description | Parallel? |
|----------|-------|-------------|-----------|
| s1_1-classify | ` + "`aura:p1-user:s1_1-classify`" + ` | Capture and classify request along 4 axes (scope, complexity, risk, domain novelty) | Sequential (first) |
| s1_2-research | ` + "`aura:p1-user:s1_2-research`" + ` | Find domain standards, prior art | Parallel with s1_3 |
| s1_3-explore | ` + "`aura:p1-user:s1_3-explore`" + ` | Codebase exploration for integration points | Parallel with s1_2 |

After classification, user confirms research depth. Then s1_2 and s1_3 run in parallel.`,
		},
		{
			Id:    "epoch-starting",
			Title: "Starting an Epoch",
			Content: "**Option 1: Manual Task Creation**\n" +
				"```bash\n" +
				"# Phase 1: Capture user request\n" +
				"bd create --labels \"aura:p1-user:s1_1-classify\" \\\n" +
				"  --title \"REQUEST: {{feature}}\" \\\n" +
				"  --description \"{{verbatim user request}}\" \\\n" +
				"  --assignee architect\n\n" +
				"# Then proceed through phases manually\n" +
				"```\n\n" +
				"**Option 2: Formula-Based (if bd mol available)**\n" +
				"```bash\n" +
				"bd mol pour aura-epoch \\\n" +
				"  --var feature=\"{{feature name}}\" \\\n" +
				"  --var user_request=\"{{verbatim request}}\"\n" +
				"```",
		},
		{
			Id:    "epoch-phase-transitions",
			Title: "Phase Transitions",
			Content: `Each phase creates a task and chains dependencies. Cross-references use description frontmatter instead of peer-reference commands.

` + "```bash" + `
# After Phase 1 creates task-req
bd dep add task-req --blocked-by task-eli    # REQUEST blocked by ELICIT

# After Phase 2 creates task-eli and URD
bd dep add task-eli --blocked-by task-prop   # ELICIT blocked by PROPOSAL
# URD linked via frontmatter in its description:
#   references:
#     request: task-req
#     elicit: task-eli

# After Phase 5 (UAT) and Phase 6 (ratify), update URD
bd comments add task-urd "UAT results: {{summary}}"
bd comments add task-urd "Ratified: scope confirmed as {{summary}}"
` + "```",
		},
		{
			Id:    "epoch-followup-epic",
			Title: "Follow-up Epic",
			Content: `**Trigger:** Code review (Phase 10) completion + ANY IMPORTANT or MINOR findings exist.
**NOT** gated on BLOCKER resolution.
**Owner:** Supervisor creates the follow-up epic.

` + "```bash" + `
bd create --type=epic --priority=3 \
  --title "FOLLOWUP: Non-blocking improvements from code review" \
  --description "---
references:
  request: <request-task-id>
  review_round: <review-task-ids>
---
Aggregated IMPORTANT and MINOR findings from code review." \
  --labels "aura:epic-followup"
` + "```" + `

### Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)

The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

` + "```" + `
FOLLOWUP → FOLLOWUP_URE → FOLLOWUP_URD → FOLLOWUP_PROPOSAL-1 → FOLLOWUP_IMPL_PLAN → FOLLOWUP_SLICE-N
` + "```" + `

- **FOLLOWUP_URE**: Scoping URE with user to determine which findings to address
- **FOLLOWUP_URD**: Requirements doc for follow-up scope (references original URD)
- **FOLLOWUP_PROPOSAL-{N}**: Proposal accounting for original URD + FOLLOWUP_URD + outstanding findings
- **FOLLOWUP_IMPL_PLAN**: Supervisor decomposes follow-up into slices
- **FOLLOWUP_SLICE-{N}**: Each slice adopts original IMPORTANT/MINOR leaf tasks as children (dual-parent)

See ` + "`/aura:supervisor`" + ` and ` + "`/aura:impl-review`" + ` for full creation commands and leaf task adoption.`,
		},
		{
			Id:    "epoch-eager-severity",
			Title: "EAGER Severity Tree (Phase 10)",
			Content: `Code reviews ALWAYS create 3 severity group tasks per review round, even if empty:

` + "```bash" + `
# Create all 3 severity groups immediately (EAGER, not lazy)
bd create --title "SLICE-N-REVIEW-{axis}-{round} BLOCKER" \
  --labels "aura:severity:blocker,aura:p10-impl:s10-review" ...
bd create --title "SLICE-N-REVIEW-{axis}-{round} IMPORTANT" \
  --labels "aura:severity:important,aura:p10-impl:s10-review" ...
bd create --title "SLICE-N-REVIEW-{axis}-{round} MINOR" \
  --labels "aura:severity:minor,aura:p10-impl:s10-review" ...

# Empty groups are closed immediately
bd close <empty-important-id>
bd close <empty-minor-id>
` + "```" + `

**Dual-parent BLOCKER:** BLOCKER findings block both the severity group AND the slice:
` + "```bash" + `
bd dep add <blocker-group-id> --blocked-by <blocker-finding-id>
bd dep add <slice-id> --blocked-by <blocker-finding-id>
` + "```" + `

See ` + "`../protocol/CONSTRAINTS.md`" + ` for full severity definitions.`,
		},
		{
			Id:    "epoch-tracking",
			Title: "Tracking Progress",
			Content: "```bash\n" +
				"# View dependency chain\n" +
				"bd dep tree {{latest-task-id}}\n\n" +
				"# Check blocked work\n" +
				"bd blocked\n\n" +
				"# See all epoch tasks by phase\n" +
				"bd list --labels=\"aura:p1-user:s1_1-classify\"    # REQUEST tasks\n" +
				"bd list --labels=\"aura:p2-user:s2_1-elicit\"      # ELICIT tasks\n" +
				"bd list --labels=\"aura:p3-plan:s3-propose\"        # PROPOSAL tasks\n" +
				"bd list --labels=\"aura:p9-impl:s9-slice\"          # Implementation slices\n" +
				"```",
		},
		{
			Id:    "epoch-skills-table",
			Title: "Skills to Invoke",
			Content: `Each phase transition MUST include an explicit ` + "`Skill(...)`" + ` invocation directive. When launching agents for a phase, the prompt MUST tell the agent to call the corresponding skill as its first action.

| Phase | Skill | Invocation Directive |
|-------|-------|---------------------|
| 1 (REQUEST: classify, research, explore) | ` + "`/aura:user-request`" + ` | ` + "`Skill(/aura:user-request)`" + ` |
| 2 (ELICIT + URD) | ` + "`/aura:user-elicit`" + ` | ` + "`Skill(/aura:user-elicit)`" + ` |
| 3-6 (PROPOSAL, REVIEW, UAT, RATIFY) | ` + "`/aura:architect`" + ` | ` + "`Skill(/aura:architect)`" + ` |
| 5, 11 (UAT) | ` + "`/aura:user-uat`" + ` | ` + "`Skill(/aura:user-uat)`" + ` |
| 7 (HANDOFF) | ` + "`/aura:architect-handoff`" + ` | Architect calls ` + "`Skill(/aura:architect-handoff)`" + ` after ratification |
| 8-10 (IMPL_PLAN, SLICES, CODE REVIEW) | ` + "`/aura:supervisor`" + ` | Supervisor prompt MUST start with ` + "`Skill(/aura:supervisor)`" + ` |
| 12 (LANDING) | Manual git commit and push | N/A |

**CRITICAL:** When the architect hands off to the supervisor (Phase 7 → 8), the supervisor launch prompt MUST:
1. Start with ` + "`Skill(/aura:supervisor)`" + ` — without this, the supervisor skips role-critical procedures
2. Include all Beads task IDs (REQUEST, URD, RATIFIED PROPOSAL, HANDOFF)
3. Include the handoff document path`,
		},
		{
			Id:    "epoch-never-delete",
			Title: "Never Delete Policy",
			Content: "**DO:** Add labels, add comments, update status\n" +
				"**DON'T:** Close tasks prematurely, delete tasks, remove labels\n\n" +
				"```bash\n" +
				"# Correct: Add ratify label\n" +
				"bd label add task-prop aura:p6-plan:s6-ratify\n" +
				"bd comments add task-prop \"RATIFIED: All reviewers ACCEPT\"\n\n" +
				"# Wrong: Don't close\n" +
				"# bd close task-prop  # NEVER DO THIS\n" +
				"```",
		},
	},

	Recipes: []RecipeBlock{},
}
