// Body content for the impl-review skill SKILL.md.
package codegen

var implReviewBody = SkillBody{
	Preamble: `Conduct code review across ALL implementation slices. Each of 3 reviewers reviews every slice.

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-10-code-review)** <- Phase 10

See ` + "`../protocol/CONSTRAINTS.md`" + ` for coding standards and severity definitions.`,
	// Behaviors shared with supervisor body via SharedFragmentSpecs:
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
FINDING_ID=$([[rec:create]] --kind task --title "BLOCKER: Missing error handling in auth flow" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_1_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
Missing error handling causes silent failure in auth flow.")
[[rec:label]] "$FINDING_ID" pasture:finding

# Wire dual-parent: finding blocks BOTH severity group AND slice
[[rec:block]] "$BLOCKER_GROUP_ID_URI" --by "$FINDING_ID"
[[rec:block]] "${SLICE_1_1_ID_URI}" --by "$FINDING_ID"
` + "```" + `

Per [frag--sup-deferred-followup], IMPORTANT/MINOR findings attach to their severity group only (they do **not** block the slice via dual-parent). They stay open past the wave gate and converge at the end, and they are **never** routed to the FOLLOWUP epic. The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items.

` + "```" + `bash
# IMPORTANT finding — attaches to the IMPORTANT severity group (NOT the slice)
IMPORTANT_FINDING_ID=$([[rec:create]] --kind task --title "IMPORTANT: Add request timeout" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_2_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
API calls should have configurable timeouts.")
[[rec:label]] "$IMPORTANT_FINDING_ID" pasture:finding

# Attaches to the IMPORTANT severity group (NOT the slice); its close rule is C-finding-lifecycle
[[rec:block]] "$IMPORTANT_GROUP_ID_URI" --by "$IMPORTANT_FINDING_ID"
` + "```" + ``,
		},
		{
			Id:    "impl-rev-review-structure",
			Title: "Review Structure",
			Content: `One review round covers one whole wave, never a single slice. Wave 1 holds the first slice, the second slice, and the third slice; each reviewer reviews all three together and creates ONE review title for the wave:

` + "```\n" +
				`Wave 1: the first slice, the second slice, the third slice
  Reviewer A (Correctness): reviews the whole wave →
    Creates: the review (references: wave: 1, slices: the first slice, the second slice, the third slice)
    Each review has 3 severity groups (BLOCKER/IMPORTANT/MINOR)

  Reviewer B (Test quality): reviews the whole wave →
    Creates: the review

  Reviewer C (Elegance): reviews the whole wave →
    Creates: the review
` + "```" + `

A later wave gets its own review titles (for example, wave 2 → the later review). A wave never produces one title per reviewed slice.`,
		},
		{
			Id:    "impl-rev-spawning",
			Title: "Spawning Reviewers",
			Content: `Supervisor spawns 3 parallel reviewers as **subagents** via the [[op:delegate_assignment]] operation. Reviewers are short-lived — keep them in-session.

` + "```" + `
// Spawn 3 reviewers (one per axis)
delegation: {
  subagent_type: "general-purpose",
  run_in_background: true,
  prompt: ` + "`" + `You are Reviewer A (Correctness).
URD: "${URD_ID_URI}" (read with [[rec:show]] "${URD_ID_URI}" for user requirements context)
Focus: Does implementation faithfully serve the user? Are technical decisions consistent with rationale?
Review the whole wave: "${SLICE_1_1_ID_URI}", "${SLICE_1_2_ID_URI}", "${SLICE_1_3_ID_URI}"
For each slice, run: [[rec:show]] "${SLICE_ID_URI}"
Create ONE severity trio (BLOCKER/IMPORTANT/MINOR) for the wave, listing every slice in its references. Title: the review
Call [[op:invoke_skill]](/pasture:reviewer-review-code) for the review procedure.` + "`" + `
}
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
| the first slice | "${ID_URI}" | <description> | worker-1 |
| the second slice | "${ID_URI}" | <description> | worker-2 |

## Review Procedure
1. For each slice: ` + "`" + `[[rec:show]] "${SLICE_ID_URI}"` + "`" + `
2. Create ONE severity trio for the wave (EAGER)
3. Add findings as children of severity groups
4. BLOCKER findings: dual-parent (severity group + slice)
5. Close empty severity groups immediately
6. Vote ACCEPT or REVISE for the wave
` + "```" + ``,
				},
			},
		},
		{
			Id:    "impl-rev-criteria",
			Title: "Review Criteria",
			Content: `Each reviewer checks every slice in the wave for:

1. **Requirements Alignment (check URD)**
   - Does implementation match ratified plan?
   - Are all acceptance criteria met?
   - Read URD (` + "`" + `[[rec:show]] "${URD_ID_URI}"` + "`" + `) for requirements traceability

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

One vote per wave on the wave review task (not one per slice):

**Documentation (via Pasture comments):**
` + "```" + `bash
[[rec:note]] "${REVIEW_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: ACCEPT - [reason]"
# OR
[[rec:note]] "${REVIEW_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: REVISE - [specific issue]. Suggest: [fix]"
` + "```" + ``,
		},
		{
			Id:    "impl-rev-consensus",
			Title: "Consensus Check",
			Content: `All reviews across all slices must be ACCEPT:

` + "```" + `bash
# Check for any REVISE votes
[[rec:query]] --phase code_review

# Check for unresolved BLOCKERs
[[rec:query]] --label="pasture:severity:blocker" --status=open

# If any REVISE or open BLOCKERs, return to implementation
# If all ACCEPT and BLOCKERs resolved, proceed to Phase 11 (UAT)
` + "```" + ``,
		},
		{
			Id:    "impl-rev-handling-revise",
			Title: "Handling REVISE",
			Content: `If any reviewer votes REVISE on the wave:

1. **Document issues** in the review task description
2. **Return slice to worker** for fixes
3. **Re-review** after fixes complete (new review round)

` + "```" + `bash
# Mark slice as needing revision
[[rec:note]] "${SLICE_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "REVISION NEEDED: <specific issues>"

# After worker fixes, start new review round
# New severity groups are created fresh for the new round
` + "```" + ``,
		},
		{
			Id:      "impl-rev-followup-epic",
			Title:   "Follow-up Epic (EPIC_FOLLOWUP)",
			Content: `Per [frag--sup-followup-epic-timing], create only when UAT produces user-DEFER'd items. Open review findings stay open past a wave gate and converge at the end; they never feed the FOLLOWUP epic.`,
			Subsections: []ProseSection{
				{
					Id:    "impl-rev-followup-step1",
					Title: "Step 1: Create the follow-up epic",
					Content: `` + "```" + `bash
FOLLOWUP_EPIC_ID_URI=$([[rec:create]] --kind epic --title "FOLLOWUP: User-deferred improvements from UAT" --phase unscoped --priority 3 \
  --body="---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
  review_round: ${REVIEW_ROUND_ID_URI}
---
User-DEFER'd UAT items only; no review findings.")
[[rec:label]] "$FOLLOWUP_EPIC_ID_URI" pasture:epic-followup

# Link only the UAT-deferred item leaves
[[rec:block]] "$FOLLOWUP_EPIC_ID_URI" --by "$DEFERRED_ITEM_ID_1_URI"
[[rec:block]] "$FOLLOWUP_EPIC_ID_URI" --by "$DEFERRED_ITEM_ID_2_URI"
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
        ├── frontmatter reference: FOLLOWUP_URD (living requirements document)
        └── blocked-by: FOLLOWUP_PROPOSAL-1  (Phase 3: proposal for follow-up)
              └── blocked-by: FOLLOWUP_IMPL_PLAN  (Phase 8: decompose into slices)
                    ├── blocked-by: the first follow-up slice  (Phase 9)
                    │     ├── blocked-by: deferred-item-leaf-task-...
                    │     └── blocked-by: deferred-item-leaf-task-...
                    └── blocked-by: the second follow-up slice
` + "```" + `

` + "```" + `bash
# Create follow-up lifecycle tasks. The URD lands first so the URE can record it.
FOLLOWUP_URD_ID=$([[rec:create]] --kind task --title "FOLLOWUP_URD: Requirements for <feature> follow-up" --phase elicit \
  --body "---
references:
  followup_epic: "${FOLLOWUP_EPIC_ID_URI}"
  original_urd: "${ORIGINAL_URD_ID_URI}"
---
Follow-up requirements. References original URD.")
[[rec:label]] "$FOLLOWUP_URD_ID" pasture:p2-user:s2_2-urd
[[rec:label]] "$FOLLOWUP_URD_ID" pasture:urd

FOLLOWUP_URE_ID=$([[rec:create]] --kind task --title "FOLLOWUP_URE: Scope follow-up for <feature>" --phase elicit \
  --body "---
references:
  followup_epic: \"${FOLLOWUP_EPIC_ID_URI}\"
  original_urd: \"${ORIGINAL_URD_ID_URI}\"
  followup_urd: \"${FOLLOWUP_URD_ID}\"
---
Scoping URE: determine which user-DEFER'd UAT items to address.")
[[rec:label]] "$FOLLOWUP_URE_ID" pasture:p2-user:s2_1-elicit
[[rec:block]] "${FOLLOWUP_EPIC_ID_URI}" --by "$FOLLOWUP_URE_ID"
` + "```" + ``,
				},
				{
					Id:    "impl-rev-followup-step3",
					Title: "Step 3: DEFER'd-item leaf adoption (dual-parent)",
					Content: `When the supervisor creates the follow-up slice tasks, the user-DEFER'd UAT-item leaf tasks gain a second parent (dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice):

` + "```" + `bash
# Leaf task gets dual-parent: DEFER'd-items tracking group + follow-up slice
[[rec:block]] "${FOLLOWUP_SLICE_ID_URI}" --by "${DEFERRED_ITEM_LEAF_ID_1_URI}"
[[rec:block]] "${FOLLOWUP_SLICE_ID_URI}" --by "${DEFERRED_ITEM_LEAF_ID_2_URI}"
# Leaf task already has: [[rec:block]] "${DEFERRED_ITEMS_TRACKING_GROUP_ID_URI}" --by "${LEAF_TASK_ID_URI}"
` + "```" + ``,
				},
				{
					Id:    "impl-rev-followup-handoff",
					Title: "Reviewer → Supervisor Handoff (h5)",
					Content: `The h5 handoff (Reviewer → Supervisor, summary-with-ids) closes out the review wave. The FOLLOWUP epic itself is created later, at UAT, from the user-DEFER'd UAT items — **not** from review findings (open findings converge at the end; close rule: ` + "`C-finding-lifecycle`" + `). Author this handoff in its Pasture task body (no filesystem path):

` + "```" + `markdown
# Handoff: Reviewer → Supervisor (review wave complete)

## Context
- Request: ${REQUEST_ID_URI}
- URD: ${URD_ID_URI}
- Ratified Proposal: ${PROPOSAL_ID_URI}

## Review Outcome
- All slices reviewed; convergence found no open pasture:finding across all waves.

## Open Items
- None for this wave. Any user-DEFER'd UAT items feed the FOLLOWUP epic at Phase 11.
` + "```" + ``,
				},
				{
					Id:      "impl-rev-followup-chain",
					Title:   "Follow-up Handoff Chain",
					Content: "Inside the follow-up lifecycle, the same handoff types (h1-h4) apply but scoped to the follow-up epic:\n\nThe follow-up lifecycle starts ONLY from user-DEFER'd UAT items (`C-followup-timing`); code review never starts it. The Supervisor creates the FOLLOWUP epic from those items (same actor), then:\n\n| Order | Handoff | Transition |\n|-------|---------|------------|\n| 1 | *(none)* | Supervisor creates the FOLLOWUP epic from user-DEFER'd UAT items (same actor) |\n| 2 | *(none)* | Supervisor creates FOLLOWUP_URE (same actor) |\n| 3 | *(none)* | Supervisor creates FOLLOWUP_URD (same actor) |\n| 4 | h6 | Supervisor → Architect: Hands off FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL |\n| 5 | h1 | Architect → Supervisor: After FOLLOWUP_PROPOSAL ratified |\n| 6 | h2 | Supervisor → Worker: the follow-up slice with DEFER'd-item leaf tasks |\n| 7 | h3 | Supervisor → Reviewer: One code review per follow-up wave |\n| 8 | h4 | Worker → Reviewer: Follow-up slice completion |\n\nFollow-up handoff storage: each handoff is authored in its Pasture task body (no filesystem path).\n\nSee `../architect-handoff/SKILL.md` for handoff field requirements.",
				},
			},
		},
		{
			Id:    "impl-rev-proceed-uat",
			Title: "Proceeding to UAT",
			Content: `Only when ALL reviews are ACCEPT and all BLOCKERs are resolved:

` + "```" + `text
# Verify consensus — no open BLOCKERs
[[rec:query]] --label="pasture:severity:blocker" --status=open
# Should return 0 results

# Proceed to Phase 11 (Implementation UAT)
[[op:invoke_skill]](/pasture:user-uat)
` + "```" + ``,
		},
	},
}
