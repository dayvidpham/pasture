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
			Then:      "add dependency to previous: pasture task dep add \"${PARENT_URI}\" --blocked-by \"${CHILD_URI}\"",
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
			Given:     "UAT (Phase 5 or 11) produces one or more user-DEFER'd items",
			When:      "finishing UAT",
			Then:      "Supervisor creates a follow-up epic (label pasture:epic-followup) from the user-DEFER'd UAT items only",
			ShouldNot: "create a follow-up epic from any review severity (BLOCKER/IMPORTANT/MINOR) — all review severities must reach 0 before wave close",
		},
		{
			Id:        "epoch-supervisor-not-idle",
			Given:     "a freshly spawned supervisor (Phase 8 IMPL_PLAN)",
			When:      "it dispatches Explore subagents and appears idle",
			Then:      "let it work — an apparently-idle supervisor is usually running Explore subagents to map the codebase",
			ShouldNot: "shut down or restart a supervisor that looks idle at the start of the IMPL_PLAN phase",
		},
		// R7/A1: Phase-10 code review iterates up to the chosen review-effort budget
		// until a fix-free clean round confirms 0/0/0; on budget exhaustion without
		// clean, surface outstanding findings to the user at a gate. Resolves to
		// SharedFragmentSpecs[FragReviewCleanExit] (SLICE-1).
		behaviorRef(FragReviewCleanExit),
		{
			Id:        "epoch-autonomous-progression",
			Given:     "non-user-gated phase completes",
			When:      "transitioning",
			Then:      "proceed autonomously; the 5 user-gated phases are: Phase 1 s1_1 (research depth), Phase 2 (URE), Phase 5 (Plan UAT), Phase 8 (implementation-effort / review-effort budget request), Phase 11 (Impl UAT)",
			ShouldNot: "ask 'Should I proceed?' for autonomous phases; add user gates beyond the 5 defined",
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
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:    "epoch-core-principles",
			Title: "Core Principles",
			Content: `1. **AUDIT TRAIL PRESERVATION** — Never delete or destroy information, labels, or tasks
2. **DEPENDENCY CHAINING** — Each task blocks its predecessor: ` + "`" + `pasture task dep add "${PARENT_URI}" --blocked-by "${CHILD_URI}"` + "`" + `
3. **USER ENGAGEMENT** — URE and UAT at multiple checkpoints
4. **CONSENSUS REQUIRED** — All 3 reviewers must ACCEPT before proceeding
5. **EAGER SEVERITY TREE** — Code reviews (Phase 10) always create 3 severity groups (BLOCKER, IMPORTANT, MINOR); empty groups closed immediately. ALL three groups must reach 0 before a review wave closes
6. **FOLLOW-UP EPIC** — Fed ONLY by user-DEFER'd UAT items (Phase 5/11), never by any review severity; the Supervisor creates it from those DEFER'd items
7. **RIDE THE WAVE** — Phases 8-10 form one continuous cycle: Explore subagents (P8), workers implement (P9), ephemeral reviewers review (P10), iterating review→fix→re-review up to the chosen review-effort budget until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR; on budget exhaustion without clean, surface outstanding findings to the user at a gate; workers persist throughout`,
		},
		{
			Id:    "epoch-12-phase-workflow",
			Title: "The 12-Phase Workflow",
			Content: "```" + `
Phase 1:  pasture:p1-user       -> REQUEST (classify, research, explore)
            s1_1-classify -> s1_2-research || s1_3-explore
Phase 2:  pasture:p2-user       -> ELICIT (URE survey) + URD (single source of truth)
            s2_1-elicit -> s2_2-urd
Phase 3:  pasture:p3-plan       -> PROPOSAL-N (architect proposes)
Phase 4:  pasture:p4-plan       -> REVIEW (3 parallel reviewers, ACCEPT/REVISE)
Phase 5:  pasture:p5-user       -> Plan UAT (user acceptance test)
Phase 6:  pasture:p6-plan       -> Ratification (supersede old proposals)
Phase 7:  pasture:p7-plan       -> Handoff (architect -> supervisor)
Phase 8:  pasture:p8-impl       -> IMPL_PLAN (supervisor decomposes into slices; Explore subagents)
Phase 9:  pasture:p9-impl       -> SLICE-N (parallel workers; Ride the Wave — workers persist for review)
Phase 10: pasture:p10-impl      -> Code Review (ephemeral reviewers review all slices; review->fix->re-review up to the chosen review-effort budget until 0/0/0 clean, else surface to user)
Phase 11: pasture:p11-user      -> Implementation UAT
Phase 12: pasture:p12-impl      -> Landing (commit, push, hand off)
` + "```" + `

### Phase 1 Expanded: REQUEST

Phase 1 has 3 sub-steps:

| Sub-step | Label | Description | Parallel? |
|----------|-------|-------------|-----------|
| s1_1-classify | ` + "`pasture:p1-user:s1_1-classify`" + ` | Capture and classify request along 4 axes (scope, complexity, risk, domain novelty) | Sequential (first) |
| s1_2-research | ` + "`pasture:p1-user:s1_2-research`" + ` | Find domain standards, prior art | Parallel with s1_3 |
| s1_3-explore | ` + "`pasture:p1-user:s1_3-explore`" + ` | Codebase exploration for integration points | Parallel with s1_2 |

After classification, user confirms research depth. Then s1_2 and s1_3 run in parallel.`,
		},
		{
			Id:    "epoch-starting",
			Title: "Starting an Epoch",
			Content: `**Option 1: Manual Task Creation**
` + "```" + `bash
# Phase 1: Capture user request
REQUEST_ID_URI=$(pasture task create "REQUEST: {{feature}}" --phase request --namespace "$PASTURE_NAMESPACE" --format json \
  --description "{{verbatim user request}}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
pasture task update "$REQUEST_ID_URI" --notes "Requested role (not an assignment): architect"

# Then proceed through phases manually
` + "```" + `

Use the explicit task graph; no molecule subsystem is required.`,
		},
		{
			Id:    "epoch-phase-transitions",
			Title: "Phase Transitions",
			Content: `Each phase creates a task and chains dependencies. Cross-references use description frontmatter instead of peer-reference commands.

` + "```" + `bash
# After Phase 1 creates "${REQUEST_ID_URI}"
pasture task dep add "${REQUEST_ID_URI}" --blocked-by "${ELICIT_ID_URI}"    # REQUEST blocked by ELICIT

# After Phase 2 creates "${ELICIT_ID_URI}" and URD
pasture task dep add "${ELICIT_ID_URI}" --blocked-by "${PROPOSAL_ID_URI}"   # ELICIT blocked by PROPOSAL
# URD linked via frontmatter in its description:
#   references:
#     request: "${REQUEST_ID_URI}"
#     elicit: "${ELICIT_ID_URI}"

# After Phase 5 (UAT) and Phase 6 (ratify), update URD
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${URD_ID_URI}" "UAT results: {{summary}}"
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${URD_ID_URI}" "Ratified: scope confirmed as {{summary}}"
` + "```" + ``,
		},
		{
			Id:    "epoch-followup-epic",
			Title: "Follow-up Epic",
			Content: `**Trigger:** UAT (Phase 5 or 11) produces one or more **user-DEFER'd items**.
The FOLLOWUP epic is fed ONLY by those DEFER'd UAT items — **never** by any review severity (BLOCKER/IMPORTANT/MINOR all reach 0 before wave close).
**Owner:** Supervisor creates the follow-up epic.

` + "```" + `bash
FOLLOWUP_EPIC_ID_URI=$(pasture task create "FOLLOWUP: User-deferred improvements from UAT" --phase unscoped --namespace "$PASTURE_NAMESPACE" --format json --type=epic --priority=3 \
  --description "---
references:
  request: "${REQUEST_ID_URI}"
  uat: "${UAT_ID_URI}"
---
Aggregated user-DEFER'd items from UAT (Phase 5/11)." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_EPIC_ID_URI" pasture:epic-followup
` + "```" + `

### Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)

The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

` + "```" + `
FOLLOWUP → FOLLOWUP_URE → FOLLOWUP_URD → FOLLOWUP_PROPOSAL-1 → FOLLOWUP_IMPL_PLAN → FOLLOWUP_SLICE-N
` + "```" + `

- **FOLLOWUP_URE**: Scoping URE with user to determine which DEFER'd items to address
- **FOLLOWUP_URD**: Requirements doc for follow-up scope (references original URD)
- **FOLLOWUP_PROPOSAL-{N}**: Proposal accounting for original URD + FOLLOWUP_URD + the DEFER'd items
- **FOLLOWUP_IMPL_PLAN**: Supervisor decomposes follow-up into slices
- **FOLLOWUP_SLICE-{N}**: Each slice implements the DEFER'd-item work decomposed into leaf tasks

See ` + "`" + `/pasture:supervisor` + "`" + ` and ` + "`" + `/pasture:impl-review` + "`" + ` for full creation commands.`,
		},
		{
			Id:    "epoch-eager-severity",
			Title: "EAGER Severity Tree (Phase 10)",
			Content: `Code reviews ALWAYS create 3 severity group tasks per review round, even if empty:

` + "```" + `bash
# Create all 3 severity groups immediately (EAGER, not lazy)
BLOCKER_ID_URI=$(pasture task create "SLICE-N-REVIEW-{axis}-{round} BLOCKER" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
   --description "Severity group for this review round; close only if empty" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$BLOCKER_ID_URI" pasture:severity:blocker
pasture task label add "$BLOCKER_ID_URI" pasture:p10-impl:s10-review
IMPORTANT_ID_URI=$(pasture task create "SLICE-N-REVIEW-{axis}-{round} IMPORTANT" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
   --description "Severity group for this review round; close only if empty" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$IMPORTANT_ID_URI" pasture:severity:important
pasture task label add "$IMPORTANT_ID_URI" pasture:p10-impl:s10-review
MINOR_ID_URI=$(pasture task create "SLICE-N-REVIEW-{axis}-{round} MINOR" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
   --description "Severity group for this review round; close only if empty" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$MINOR_ID_URI" pasture:severity:minor
pasture task label add "$MINOR_ID_URI" pasture:p10-impl:s10-review

# Empty groups are closed immediately
pasture task close "${IMPORTANT_ID_URI}"
pasture task close "${MINOR_ID_URI}"
` + "```" + `

**Dual-parent BLOCKER:** Bind BLOCKER_FINDING_ID_URI from the finding task's create output before running this fence. BLOCKER findings block both the severity group AND the slice:
` + "```" + `bash
pasture task dep add "${BLOCKER_ID_URI}" --blocked-by "${BLOCKER_FINDING_ID_URI}"
pasture task dep add "${SLICE_ID_URI}" --blocked-by "${BLOCKER_FINDING_ID_URI}"
` + "```" + `

See ` + "`" + `../protocol/CONSTRAINTS.md` + "`" + ` for full severity definitions.`,
		},
		{
			Id:    "epoch-tracking",
			Title: "Tracking Progress",
			Content: `` + "```" + `bash
# View dependency chain
pasture task dep tree "${LATEST_TASK_ID_URI}"

# Check blocked work
pasture task blocked

# See all epoch tasks by phase
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p1-user:s1_1-classify"    # REQUEST tasks
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p2-user:s2_1-elicit"      # ELICIT tasks
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p3-plan:s3-propose"        # PROPOSAL tasks
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice"          # Implementation slices
` + "```" + ``,
		},
		{
			Id:      "epoch-skills-table",
			Title:   "Skills to Invoke",
			Content: "Each phase transition MUST include an explicit `Skill(...)` invocation directive. When launching agents for a phase, the prompt MUST tell the agent to call the corresponding skill as its first action.\n\n| Phase | Skill | Invocation Directive |\n|-------|-------|---------------------|\n| 1 (REQUEST: classify, research, explore) | `/pasture:user-request` | `Skill(/pasture:user-request)` |\n| 2 (ELICIT + URD) | `/pasture:user-elicit` | `Skill(/pasture:user-elicit)` |\n| 3-6 (PROPOSAL, REVIEW, UAT, RATIFY) | `/pasture:architect` | `Skill(/pasture:architect)` |\n| 5, 11 (UAT) | `/pasture:user-uat` | `Skill(/pasture:user-uat)` |\n| 7 (HANDOFF) | `/pasture:architect-handoff` | Architect calls `Skill(/pasture:architect-handoff)` after ratification |\n| 8-10 (IMPL_PLAN, SLICES, CODE REVIEW) | `/pasture:supervisor` | Supervisor prompt MUST start with `Skill(/pasture:supervisor)` |\n| 12 (LANDING) | Manual git commit and push | N/A |\n\n**CRITICAL — interviewing phases:** The interviewing phases MUST explicitly invoke their skill. Do **not** improvise interview questions:\n- **Phase 2 (URE):** invoke `Skill(/pasture:user-elicit)` — skipping it produces low-quality elicitation.\n- **Phases 5 & 11 (UAT):** invoke `Skill(/pasture:user-uat)` — it drives the FIX-NOW vs DEFER disposition and demonstrative examples.\n\n**CRITICAL:** When the architect hands off to the supervisor (Phase 7 → 8), the supervisor launch prompt MUST:\n1. Start with `Skill(/pasture:supervisor)` — without this, the supervisor skips role-critical procedures\n2. Include all Pasture task IDs (REQUEST, URD, RATIFIED PROPOSAL, HANDOFF)\n3. Include the HANDOFF Pasture task ID — the handoff is authored in that task body (no filesystem path)",
		},
		{
			Id:    "epoch-never-delete",
			Title: "Never Delete Policy",
			Content: `**DO:** Add labels, add comments, update status
**DON'T:** Close tasks prematurely, delete tasks, remove labels

` + "```" + `bash
# Correct: Add ratify label
pasture task label add "${PROPOSAL_ID_URI}" pasture:p6-plan:s6-ratify
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "RATIFIED: All reviewers ACCEPT"

# Wrong: Don't close
# pasture task close "${PROPOSAL_ID_URI}"  # NEVER DO THIS
` + "```" + ``,
		},
	},

	Recipes: []RecipeBlock{},
}
