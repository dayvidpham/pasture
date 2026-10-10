// Body content for the impl-review skill SKILL.md.
package codegen

var implReviewBody = SkillBody{
	Preamble: `Conduct code review across ALL implementation slices. Each of 3 reviewers reviews every slice.

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-10-code-review)** <- Phase 10

See ` + "`../protocol/CONSTRAINTS.md`" + ` for coding standards and severity definitions.`,
	// Behaviors shared with supervisor body via SharedFragmentSpecs (SLICE-3):
	// all 6 review-wave behaviors are now fragments so the canonical text is
	// single-sourced and both skill bodies render byte-identical content.
	Behaviors: []BehaviorSpec{
		behaviorRef(FragSupReviewAllSlices),
		behaviorRef(FragSupReviewCheckEach),
		behaviorRef(FragSupReviewSeverityGroups),
		behaviorRef(FragSupBlockerDualParent),
		behaviorRef(FragSupDeferredFollowup),
		behaviorRef(FragSupFollowupEpicTiming),
	},
	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		// fragRef resolves to severity-tree prose; naming-convention is its embedded Subsection
		// (renders as H3 via the template's Subsections iteration — see Part 6 of worker-b impl-plan).
		fragRef(FragSupSeverityTree),
		{
			Id:    "impl-rev-dual-parent",
			Title: "Dual-Parent BLOCKER Relationship",
			Content: `BLOCKER findings have **two parents**:
1. The severity group task (` + "`" + `pasture:severity:blocker` + "`" + `) — for categorization
2. The slice they block — for dependency tracking

` + "```" + `bash
# Create a BLOCKER finding
FINDING_ID=$(pasture task create "BLOCKER: Missing error handling in auth flow" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  reviewer: reviewer-A
  round: 1
---
Missing error handling causes silent failure in auth flow." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FINDING_ID" pasture:p10-impl:s10-review

# Wire dual-parent: finding blocks BOTH severity group AND slice
pasture task dep add "$BLOCKER_GROUP_ID_URI" --blocked-by "$FINDING_ID"
pasture task dep add "${SLICE_1_ID_URI}" --blocked-by "$FINDING_ID"
` + "```" + `

Per [frag--sup-deferred-followup], IMPORTANT/MINOR findings attach to their severity group only (they do **not** block the slice via dual-parent), but ALL severity groups (BLOCKER/IMPORTANT/MINOR) must reach 0 before the review wave closes — they are **never** routed to the FOLLOWUP epic. The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items.

` + "```" + `bash
# IMPORTANT finding — attaches to the IMPORTANT severity group (NOT the slice)
IMPORTANT_FINDING_ID=$(pasture task create "IMPORTANT: Add request timeout" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  reviewer: reviewer-A
  round: 1
---
API calls should have configurable timeouts." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$IMPORTANT_FINDING_ID" pasture:p10-impl:s10-review

# Attaches to the IMPORTANT severity group (NOT the slice); the group must still reach 0
pasture task dep add "$IMPORTANT_GROUP_ID_URI" --blocked-by "$IMPORTANT_FINDING_ID"
` + "```" + ``,
		},
		{
			Id:    "impl-rev-review-structure",
			Title: "Review Structure",
			Content: `Each reviewer (A, B, C) reviews EVERY slice:

` + "```\n" +
				`Reviewer A (Correctness): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-A-1, SLICE-2-REVIEW-A-1, SLICE-3-REVIEW-A-1
  Each review has 3 severity groups (BLOCKER/IMPORTANT/MINOR)

Reviewer B (Test quality): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-B-1, SLICE-2-REVIEW-B-1, SLICE-3-REVIEW-B-1

Reviewer C (Elegance): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-C-1, SLICE-2-REVIEW-C-1, SLICE-3-REVIEW-C-1
` + "```",
		},
		{
			Id:    "impl-rev-spawning",
			Title: "Spawning Reviewers",
			Content: `Supervisor spawns 3 parallel reviewers as **subagents** (via the Task tool) or via **TeamCreate**. Reviewers are short-lived — keep them in-session.

` + "```" + `
// Spawn 3 reviewers (one per axis)
Task({
  subagent_type: "general-purpose",
  run_in_background: true,
  prompt: ` + "`" + `You are Reviewer A (Correctness).
URD: "${URD_ID_URI}" (read with pasture task show "${URD_ID_URI}" for user requirements context)
Focus: Does implementation faithfully serve the user? Are technical decisions consistent with rationale?
Review ALL slices: "${SLICE_1_ID_URI}", "${SLICE_2_ID_URI}", "${SLICE_3_ID_URI}"
For each slice, run: pasture task show "${SLICE_ID_URI}"
Create severity groups (BLOCKER/IMPORTANT/MINOR) for each slice. Title: SLICE-N-REVIEW-A-1
Call Skill(/pasture:reviewer-review-code) for the review procedure.` + "`" + `
})
` + "```" + `

**Handoff:** Before spawning each reviewer, author its handoff in a Pasture task body (the task body IS the handoff — no filesystem path).`,
			Subsections: []ProseSection{
				{
					Id:    "impl-rev-handoff-template",
					Title: "Supervisor → Reviewer Handoff Template",
					Content: `` + "```" + `markdown
# Handoff: Supervisor → Reviewer <N>

## Context
- Request: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}"
- IMPL_PLAN: "${IMPL_PLAN_ID_URI}"
- Ratified Proposal: "${PROPOSAL_ID_URI}"

## Slices to Review
| Slice | Task ID | Description | Worker |
|-------|---------|-------------|--------|
| SLICE-1 | "${ID_URI}" | <description> | worker-1 |
| SLICE-2 | "${ID_URI}" | <description> | worker-2 |

## Review Procedure
1. For each slice: ` + "`" + `pasture task show "${SLICE_ID_URI}"` + "`" + `
2. Create 3 severity groups per slice (EAGER)
3. Add findings as children of severity groups
4. BLOCKER findings: dual-parent (severity group + slice)
5. Close empty severity groups immediately
6. Vote ACCEPT or REVISE per slice
` + "```" + ``,
				},
			},
		},
		{
			Id:    "impl-rev-criteria",
			Title: "Review Criteria",
			Content: `Each reviewer checks each slice for:

1. **Requirements Alignment (check URD)**
   - Does implementation match ratified plan?
   - Are all acceptance criteria met?
   - Read URD (` + "`" + `pasture task show "${URD_ID_URI}"` + "`" + `) for requirements traceability

2. **User Vision (check URD)**
   - Does it fulfill the user's original request (as documented in URD)?
   - Does it match UAT expectations?

3. **MVP Scope**
   - Is scope appropriate (not over/under engineered)?

4. **Codebase Quality**
   - Follows project style/constraints?
   - No TODO placeholders?
   - Tests import production code?

5. **Validation Checklist**
   - All items from slice checklist verified?`,
		},
		{
			Id:    "impl-rev-voting",
			Title: "Voting: ACCEPT vs REVISE (Binary Only)",
			Content: `| Vote | Requirement |
|------|-------------|
| **ACCEPT** | All 5 criteria satisfied; no BLOCKER items |
| **REVISE** | BLOCKER issues found; must provide actionable feedback |

**Documentation (via Pasture comments):**
` + "```" + `bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${SLICE_ID_URI}" "VOTE: ACCEPT - [reason]"
# OR
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${SLICE_ID_URI}" "VOTE: REVISE - [specific issue]. Suggest: [fix]"
` + "```" + ``,
		},
		{
			Id:    "impl-rev-consensus",
			Title: "Consensus Check",
			Content: `All reviews across all slices must be ACCEPT:

` + "```" + `bash
# Check for any REVISE votes
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p10-impl:s10-review"

# Check for unresolved BLOCKERs
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:blocker" --status=open

# If any REVISE or open BLOCKERs, return to implementation
# If all ACCEPT and BLOCKERs resolved, proceed to Phase 11 (UAT)
` + "```" + ``,
		},
		{
			Id:    "impl-rev-handling-revise",
			Title: "Handling REVISE",
			Content: `If any reviewer votes REVISE on any slice:

1. **Document issues** in the review task description
2. **Return slice to worker** for fixes
3. **Re-review** after fixes complete (new review round)

` + "```" + `bash
# Mark slice as needing revision
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${SLICE_ID_URI}" "REVISION NEEDED: <specific issues>"

# After worker fixes, start new review round
# New severity groups are created fresh for the new round
` + "```" + ``,
		},
		{
			Id:      "impl-rev-followup-epic",
			Title:   "Follow-up Epic (EPIC_FOLLOWUP)",
			Content: `Per [frag--sup-followup-epic-timing], create only when UAT produces user-DEFER'd items. Review findings of every severity must be resolved before the review wave closes.`,
			Subsections: []ProseSection{
				{
					Id:    "impl-rev-followup-step1",
					Title: "Step 1: Create the follow-up epic",
					Content: `` + "```" + `bash
FOLLOWUP_EPIC_ID_URI=$(pasture task create "FOLLOWUP: User-deferred improvements from UAT" --phase unscoped --namespace "$PASTURE_NAMESPACE" --format json --type=epic --priority=3 \
  --description="---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
  review_round: ${REVIEW_ROUND_ID_URI}
---
User-DEFER'd UAT items only; no review findings." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_EPIC_ID_URI" pasture:epic-followup

# Link only the UAT-deferred item leaves
pasture task dep add "$FOLLOWUP_EPIC_ID_URI" --blocked-by "$DEFERRED_ITEM_ID_1_URI"
pasture task dep add "$FOLLOWUP_EPIC_ID_URI" --blocked-by "$DEFERRED_ITEM_ID_2_URI"
` + "```" + ``,
				},
				{
					Id:    "impl-rev-followup-step2",
					Title: "Step 2: Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)",
					Content: `The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

` + "```" + `
FOLLOWUP epic (pasture:epic-followup)
  ├── frontmatter reference: original URD
  ├── frontmatter reference: original REVIEW-A/B/C tasks
  └── blocked-by: FOLLOWUP_URE         (Phase 2: scope which DEFER'd items to address)
        └── blocked-by: FOLLOWUP_URD   (Phase 2: requirements for follow-up)
              └── blocked-by: FOLLOWUP_PROPOSAL-1  (Phase 3: proposal for follow-up)
                    └── blocked-by: FOLLOWUP_IMPL_PLAN  (Phase 8: decompose into slices)
                          ├── blocked-by: FOLLOWUP_SLICE-1  (Phase 9)
                          │     ├── blocked-by: deferred-item-leaf-task-...
                          │     └── blocked-by: deferred-item-leaf-task-...
                          └── blocked-by: FOLLOWUP_SLICE-2
` + "```" + `

` + "```" + `bash
# Create follow-up lifecycle tasks
FOLLOWUP_URE_ID=$(pasture task create "FOLLOWUP_URE: Scope follow-up for <feature>" --phase elicit --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  followup_epic: "${FOLLOWUP_EPIC_ID_URI}"
  original_urd: "${ORIGINAL_URD_ID_URI}"
---
Scoping URE: determine which user-DEFER'd UAT items to address." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_URE_ID" pasture:p2-user:s2_1-elicit
pasture task dep add "${FOLLOWUP_EPIC_ID_URI}" --blocked-by "$FOLLOWUP_URE_ID"

FOLLOWUP_URD_ID=$(pasture task create "FOLLOWUP_URD: Requirements for <feature> follow-up" --phase elicit --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  followup_epic: "${FOLLOWUP_EPIC_ID_URI}"
  original_urd: "${ORIGINAL_URD_ID_URI}"
---
Follow-up requirements. References original URD." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$FOLLOWUP_URD_ID" pasture:p2-user:s2_2-urd
pasture task label add "$FOLLOWUP_URD_ID" pasture:urd
pasture task dep add "$FOLLOWUP_URE_ID" --blocked-by "$FOLLOWUP_URD_ID"
` + "```" + ``,
				},
				{
					Id:    "impl-rev-followup-step3",
					Title: "Step 3: DEFER'd-item leaf adoption (dual-parent)",
					Content: `When the supervisor creates FOLLOWUP_SLICE-N tasks, the user-DEFER'd UAT-item leaf tasks gain a second parent (dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice):

` + "```" + `bash
# Leaf task gets dual-parent: DEFER'd-items tracking group + follow-up slice
pasture task dep add "${FOLLOWUP_SLICE_ID_URI}" --blocked-by "${DEFERRED_ITEM_LEAF_ID_1_URI}"
pasture task dep add "${FOLLOWUP_SLICE_ID_URI}" --blocked-by "${DEFERRED_ITEM_LEAF_ID_2_URI}"
# Leaf task already has: pasture task dep add "${DEFERRED_ITEMS_TRACKING_GROUP_ID_URI}" --blocked-by "${LEAF_TASK_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "impl-rev-followup-handoff",
					Title: "Followup Handoff (h5)",
					Content: `The h5 handoff (Reviewer → Supervisor, summary-with-ids) closes out the review wave. The FOLLOWUP epic itself is created later, at UAT, from the user-DEFER'd UAT items — **not** from review findings (all review severities reach 0 before the wave closes). Author this handoff in its Pasture task body (no filesystem path):

` + "```" + `markdown
# Handoff: Reviewer → Supervisor (review wave complete)

## Context
- Request: ${REQUEST_ID_URI}
- URD: ${URD_ID_URI}
- Ratified Proposal: ${PROPOSAL_ID_URI}

## Review Outcome
- All slices reviewed; ALL severity groups (BLOCKER/IMPORTANT/MINOR) reached 0 on a fix-free clean round.

## Open Items
- None for this wave. Any user-DEFER'd UAT items feed the FOLLOWUP epic at Phase 11.
` + "```" + ``,
				},
				{
					Id:      "impl-rev-followup-chain",
					Title:   "Follow-up Handoff Chain",
					Content: "Inside the follow-up lifecycle, the same handoff types (h1-h4) apply but scoped to the follow-up epic:\n\n| Order | Handoff | Transition |\n|-------|---------|------------|\n| 1 | h5 | Reviewer → Followup: **Starts** the follow-up lifecycle |\n| 2 | *(none)* | Supervisor creates FOLLOWUP_URE (same actor) |\n| 3 | *(none)* | Supervisor creates FOLLOWUP_URD (same actor) |\n| 4 | h6 | Supervisor → Architect: Hands off FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL |\n| 5 | h1 | Architect → Supervisor: After FOLLOWUP_PROPOSAL ratified |\n| 6 | h2 | Supervisor → Worker: FOLLOWUP_SLICE-N with DEFER'd-item leaf tasks |\n| 7 | h3 | Supervisor → Reviewer: Code review of follow-up slices |\n| 8 | h4 | Worker → Reviewer: Follow-up slice completion |\n\nFollow-up handoff storage: each handoff is authored in its Pasture task body (no filesystem path).\n\nSee `../protocol/HANDOFF_TEMPLATE.md` for full follow-up handoff examples and field requirements.",
				},
			},
		},
		{
			Id:    "impl-rev-proceed-uat",
			Title: "Proceeding to UAT",
			Content: `Only when ALL reviews are ACCEPT and all BLOCKERs are resolved:

` + "```" + `text
# Verify consensus — no open BLOCKERs
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:blocker" --status=open
# Should return 0 results

# Proceed to Phase 11 (Implementation UAT)
Skill(/pasture:user-uat)
` + "```" + ``,
		},
	},
}
