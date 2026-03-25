// Body content for reviewer and impl-review skills.
//
// reviewerBody encodes the hand-authored body of skills/reviewer/SKILL.md
// (lines after the <!-- END GENERATED FROM aura schema --> marker at line 213).
//
// implReviewBody encodes the full instructional content of
// skills/impl-review/SKILL.md. That file has no codegen markers — the entire
// file is hand-authored. The generated header (frontmatter + role table) will
// be prepended by SLICE-7 once impl-review is added to the codegen pipeline.
// The body here captures everything from the first instructional paragraph
// onward (skipping the frontmatter and H1 title that become the header).
package codegen

func init() {
	SkillBodySpecs["reviewer"] = reviewerBody
	SkillBodySpecs["impl-review"] = implReviewBody
}

// ─── reviewerBody ─────────────────────────────────────────────────────────────

var reviewerBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)**",
	Sections: []ProseSection{
		{
			ID:    "rev-plan-vs-code",
			Title: "Plan Review vs Code Review",
			Content: `| Aspect | Plan Review (Phase 4) | Code Review (Phase 10) |
|--------|-----------------------|------------------------|
| Label | ` + "`aura:p4-plan:s4-review`" + ` | ` + "`aura:p10-impl:s10-review`" + ` |
| Vote | ACCEPT / REVISE (binary) | ACCEPT / REVISE (binary) |
| Severity tree | **NO** — no severity groups | **YES** — EAGER creation (always 3 groups) |
| Naming | PROPOSAL-N-REVIEW-{axis}-{round} | SLICE-N-REVIEW-{axis}-{round} |
| Focus | End-user alignment, MVP scope | Production code paths, severity findings |

**Given** review complete **when** documenting **then** create review task with dependency chain **should never** vote without creating task`,
		},
		{
			ID:    "rev-end-user-alignment",
			Title: "End-User Alignment Criteria",
			Content: `All reviewers also apply these general questions:

1. **Who are the end-users?**
2. **What would end-users want?**
3. **How would this affect them?**
4. **Are there implementation gaps?**
5. **Does MVP scope make sense?**
6. **Is validation checklist complete and correct?**`,
		},
		{
			ID:    "rev-vote-options",
			Title: "Vote Options",
			Content: `| Vote | When |
|------|------|
| ACCEPT | All 6 criteria satisfied; no BLOCKER items |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No intermediate levels.`,
		},
		{
			ID:    "rev-severity-vocab",
			Title: "Severity Vocabulary (Code Review Only)",
			Content: `| Severity | When to Use | Blocks Slice? |
|----------|-------------|---------------|
| BLOCKER | Security, type errors, test failures, broken production code paths | Yes |
| IMPORTANT | Performance, missing validation, architectural concerns | No (follow-up epic) |
| MINOR | Style, optional optimizations, naming improvements | No (follow-up epic) |`,
		},
		{
			ID:    "rev-followup-lifecycle",
			Title: "Follow-up Lifecycle Reviews",
			Content: `Reviewers also participate in the follow-up lifecycle:

- **FOLLOWUP_PROPOSAL review (Phase 4):** Same procedure as standard plan review. Task naming: ` + "`FOLLOWUP_PROPOSAL-N-REVIEW-{axis}-{round}`" + `. Binary ACCEPT/REVISE, no severity tree.
- **FOLLOWUP_SLICE code review (Phase 10):** Same procedure as standard code review. Task naming: ` + "`FOLLOWUP_SLICE-N-REVIEW-{axis}-{round}`" + `. Full EAGER severity tree (BLOCKER/IMPORTANT/MINOR).
- **No followup-of-followup:** IMPORTANT/MINOR findings from FOLLOWUP_SLICE code review are tracked on the existing follow-up epic. A nested follow-up epic is never created.`,
		},
		{
			ID:    "rev-beads-process",
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
			ID:    "rev-consensus",
			Title: "Consensus",
			Content: `All 3 reviewers must vote ACCEPT for plan to be ratified. If any reviewer votes REVISE:
1. Architect creates PROPOSAL-N+1 addressing feedback
2. Old proposal marked ` + "`aura:superseded`" + `
3. Reviewers re-review new proposal
4. Repeat until all ACCEPT`,
		},
	},
}

// ─── implReviewBody ───────────────────────────────────────────────────────────

var implReviewBody = SkillBody{
	Preamble: `Conduct code review across ALL implementation slices. Each of 3 reviewers reviews every slice.

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-10-code-review)** <- Phase 10

See ` + "`../protocol/CONSTRAINTS.md`" + ` for coding standards and severity definitions.`,
	Behaviors: []BehaviorSpec{
		{
			ID:    "impl-rev-b1",
			Given: "all slices complete",
			When:  "starting review",
			Then:  "spawn 3 reviewers for ALL slices",
			ShouldNot: "assign reviewers to single slices",
		},
		{
			ID:    "impl-rev-b2",
			Given: "reviewer assigned",
			When:  "reviewing",
			Then:  "check each slice against criteria",
			ShouldNot: "skip any slice",
		},
		{
			ID:    "impl-rev-b3",
			Given: "review round",
			When:  "creating severity groups",
			Then:  "ALWAYS create 3 severity groups (BLOCKER, IMPORTANT, MINOR) per round even if empty",
			ShouldNot: "lazily create groups only when findings exist",
		},
		{
			ID:    "impl-rev-b4",
			Given: "BLOCKER finding",
			When:  "wiring dependencies",
			Then:  "add dual-parent: blocks BOTH severity group AND slice",
			ShouldNot: "wire BLOCKER to only one parent",
		},
		{
			ID:    "impl-rev-b5",
			Given: "IMPORTANT or MINOR finding",
			When:  "categorizing",
			Then:  "add to severity group only (NOT to slice) — these go to follow-up epic",
			ShouldNot: "block slices on non-BLOCKER findings",
		},
		{
			ID:    "impl-rev-b6",
			Given: "review complete with IMPORTANT/MINOR",
			When:  "finishing",
			Then:  "supervisor creates EPIC_FOLLOWUP immediately (NOT gated on BLOCKER resolution)",
			ShouldNot: "wait for BLOCKERs to resolve before creating follow-up",
		},
	},
	Sections: []ProseSection{
		{
			ID:    "impl-rev-severity-tree",
			Title: "Severity Tree (EAGER Creation)",
			Content: `**ALWAYS create 3 severity group tasks per review round**, even if some groups have no findings:

` + "```bash\n" +
				`# Step 1: Create all 3 severity groups immediately (EAGER)
BLOCKER_ID=$(bd create --title "SLICE-1-REVIEW-A-1 BLOCKER" \
  --labels "aura:severity:blocker,aura:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  review_round: 1
---
BLOCKER findings from Reviewer A (Correctness) on SLICE-1.")

IMPORTANT_ID=$(bd create --title "SLICE-1-REVIEW-A-1 IMPORTANT" \
  --labels "aura:severity:important,aura:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  review_round: 1
---
IMPORTANT findings from Reviewer A (Correctness) on SLICE-1.")

MINOR_ID=$(bd create --title "SLICE-1-REVIEW-A-1 MINOR" \
  --labels "aura:severity:minor,aura:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  review_round: 1
---
MINOR findings from Reviewer A (Correctness) on SLICE-1.")

# Step 2: Wire severity groups to the review round task
bd dep add <review-round-id> --blocked-by $BLOCKER_ID
bd dep add <review-round-id> --blocked-by $IMPORTANT_ID
bd dep add <review-round-id> --blocked-by $MINOR_ID
# NEVER wire severity groups to IMPL_PLAN or slices directly.
# BLOCKER findings block slices via dual-parent (see below).
# IMPORTANT/MINOR route to FOLLOWUP epic only (see Follow-up Epic section).

# Step 3: Close empty groups immediately
# If a group has no findings, close it right away
bd close $IMPORTANT_ID   # if no IMPORTANT findings
bd close $MINOR_ID        # if no MINOR findings
` + "```",
			Subsections: []ProseSection{
				{
					ID:    "impl-rev-naming-convention",
					Title: "Naming Convention",
					Content: "```\n" +
						`SLICE-{N}-REVIEW-{axis}-{round}
` + "```\n" +
						`
Where axis = A (Correctness), B (Test quality), C (Elegance).

Examples:
- ` + "`SLICE-1-REVIEW-A-1`" + ` — Reviewer A (Correctness), Round 1, SLICE-1
- ` + "`SLICE-2-REVIEW-C-2`" + ` — Reviewer C (Elegance), Round 2, SLICE-2

Severity groups:
- ` + "`SLICE-1-REVIEW-A-1 BLOCKER`" + `
- ` + "`SLICE-1-REVIEW-A-1 IMPORTANT`" + `
- ` + "`SLICE-1-REVIEW-A-1 MINOR`",
				},
			},
		},
		{
			ID:    "impl-rev-dual-parent",
			Title: "Dual-Parent BLOCKER Relationship",
			Content: `BLOCKER findings have **two parents**:
1. The severity group task (` + "`aura:severity:blocker`" + `) — for categorization
2. The slice they block — for dependency tracking

` + "```bash\n" +
				`# Create a BLOCKER finding
FINDING_ID=$(bd create --title "BLOCKER: Missing error handling in auth flow" \
  --labels "aura:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  reviewer: reviewer-A
  round: 1
---
Missing error handling causes silent failure in auth flow.")

# Wire dual-parent: finding blocks BOTH severity group AND slice
bd dep add $BLOCKER_ID --blocked-by $FINDING_ID
bd dep add <slice-1-id> --blocked-by $FINDING_ID
` + "```\n" +
				`
**IMPORTANT and MINOR findings only block their severity group (NOT the slice):**

` + "```bash\n" +
				`# IMPORTANT finding — blocks severity group only
IMPORTANT_FINDING_ID=$(bd create --title "IMPORTANT: Add request timeout" \
  --labels "aura:p10-impl:s10-review" \
  --description "---
references:
  slice: <slice-1-id>
  reviewer: reviewer-A
  round: 1
---
API calls should have configurable timeouts.")

# Only blocks the IMPORTANT severity group (NOT the slice)
bd dep add $IMPORTANT_ID --blocked-by $IMPORTANT_FINDING_ID
` + "```",
		},
		{
			ID:    "impl-rev-review-structure",
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
			ID:    "impl-rev-spawning",
			Title: "Spawning Reviewers",
			Content: `Supervisor spawns 3 parallel reviewers as **subagents** (via the Task tool) or via **TeamCreate**. Reviewers are short-lived — keep them in-session.

` + "```\n" +
				`// Spawn 3 reviewers (one per axis)
Task({
  subagent_type: "general-purpose",
  run_in_background: true,
  prompt: ` + "`" + `You are Reviewer A (Correctness).
URD: <urd-id> (read with bd show <urd-id> for user requirements context)
Focus: Does implementation faithfully serve the user? Are technical decisions consistent with rationale?
Review ALL slices: <slice-1-id>, <slice-2-id>, <slice-3-id>
For each slice, run: bd show <slice-id>
Create severity groups (BLOCKER/IMPORTANT/MINOR) for each slice. Title: SLICE-N-REVIEW-A-1
Call Skill(/aura:reviewer-review-code) for the review procedure.` + "`" + `
})
` + "```\n" +
				`
**Handoff:** Before spawning each reviewer, create a handoff document:
` + "```\n" +
				`.git/.aura/handoff/<request-task-id>/supervisor-to-reviewer-<N>.md
` + "```",
			Subsections: []ProseSection{
				{
					ID:    "impl-rev-handoff-template",
					Title: "Supervisor → Reviewer Handoff Template",
					Content: "```markdown\n" +
						`# Handoff: Supervisor → Reviewer <N>

## Context
- Request: <request-task-id>
- URD: <urd-task-id>
- IMPL_PLAN: <impl-plan-task-id>
- Ratified Proposal: <proposal-task-id>

## Slices to Review
| Slice | Task ID | Description | Worker |
|-------|---------|-------------|--------|
| SLICE-1 | <id> | <description> | worker-1 |
| SLICE-2 | <id> | <description> | worker-2 |

## Review Procedure
1. For each slice: ` + "`bd show <slice-id>`" + `
2. Create 3 severity groups per slice (EAGER)
3. Add findings as children of severity groups
4. BLOCKER findings: dual-parent (severity group + slice)
5. Close empty severity groups immediately
6. Vote ACCEPT or REVISE per slice
` + "```",
				},
			},
		},
		{
			ID:    "impl-rev-criteria",
			Title: "Review Criteria",
			Content: `Each reviewer checks each slice for:

1. **Requirements Alignment (check URD)**
   - Does implementation match ratified plan?
   - Are all acceptance criteria met?
   - Read URD (` + "`bd show <urd-id>`" + `) for requirements traceability

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
			ID:    "impl-rev-voting",
			Title: "Voting: ACCEPT vs REVISE (Binary Only)",
			Content: `| Vote | Requirement |
|------|-------------|
| **ACCEPT** | All 5 criteria satisfied; no BLOCKER items |
| **REVISE** | BLOCKER issues found; must provide actionable feedback |

**Documentation (via Beads comments):**
` + "```bash\n" +
				`bd comments add <slice-id> "VOTE: ACCEPT - [reason]"
# OR
bd comments add <slice-id> "VOTE: REVISE - [specific issue]. Suggest: [fix]"
` + "```",
		},
		{
			ID:    "impl-rev-consensus",
			Title: "Consensus Check",
			Content: `All reviews across all slices must be ACCEPT:

` + "```bash\n" +
				`# Check for any REVISE votes
bd list --labels="aura:p10-impl:s10-review" --desc-contains "VOTE: REVISE"

# Check for unresolved BLOCKERs
bd list --labels="aura:severity:blocker" --status=open

# If any REVISE or open BLOCKERs, return to implementation
# If all ACCEPT and BLOCKERs resolved, proceed to Phase 11 (UAT)
` + "```",
		},
		{
			ID:    "impl-rev-handling-revise",
			Title: "Handling REVISE",
			Content: `If any reviewer votes REVISE on any slice:

1. **Document issues** in the review task description
2. **Return slice to worker** for fixes
3. **Re-review** after fixes complete (new review round)

` + "```bash\n" +
				`# Mark slice as needing revision
bd comments add <slice-id> "REVISION NEEDED: <specific issues>"

# After worker fixes, start new review round
# New severity groups are created fresh for the new round
` + "```",
		},
		{
			ID:    "impl-rev-followup-epic",
			Title: "Follow-up Epic (EPIC_FOLLOWUP)",
			Content: `**Trigger:** Review round completion + ANY IMPORTANT or MINOR findings exist.
**NOT gated on BLOCKER resolution.** Supervisor creates it immediately.`,
			Subsections: []ProseSection{
				{
					ID:    "impl-rev-followup-step1",
					Title: "Step 1: Create the follow-up epic",
					Content: "```bash\n" +
						`bd create --type=epic --priority=3 \
  --title="FOLLOWUP: Non-blocking improvements from code review" \
  --description="---
references:
  request: <request-task-id>
  urd: <urd-task-id>
  review_round: <review-round-ids>
---
Aggregated IMPORTANT and MINOR findings from code review." \
  --add-label "aura:epic-followup"

# Link IMPORTANT/MINOR severity groups
bd dep add <followup-epic-id> --blocked-by <important-group-id>
bd dep add <followup-epic-id> --blocked-by <minor-group-id>
` + "```",
				},
				{
					ID:    "impl-rev-followup-step2",
					Title: "Step 2: Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)",
					Content: `The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

` + "```\n" +
						`FOLLOWUP epic (aura:epic-followup)
  ├── relates_to: original URD
  ├── relates_to: original REVIEW-A/B/C tasks
  └── blocked-by: FOLLOWUP_URE         (Phase 2: scope which findings to address)
        └── blocked-by: FOLLOWUP_URD   (Phase 2: requirements for follow-up)
              └── blocked-by: FOLLOWUP_PROPOSAL-1  (Phase 3: proposal for follow-up)
                    └── blocked-by: FOLLOWUP_IMPL_PLAN  (Phase 8: decompose into slices)
                          ├── blocked-by: FOLLOWUP_SLICE-1  (Phase 9)
                          │     ├── blocked-by: important-leaf-task-...
                          │     └── blocked-by: minor-leaf-task-...
                          └── blocked-by: FOLLOWUP_SLICE-2
` + "```\n" +
						`
` + "```bash\n" +
						`# Create follow-up lifecycle tasks
FOLLOWUP_URE_ID=$(bd create \
  --title "FOLLOWUP_URE: Scope follow-up for <feature>" \
  --labels "aura:p2-user:s2_1-elicit" \
  --description "---
references:
  followup_epic: <followup-epic-id>
  original_urd: <original-urd-id>
---
Scoping URE: determine which IMPORTANT/MINOR findings to address.")
bd dep add <followup-epic-id> --blocked-by $FOLLOWUP_URE_ID

FOLLOWUP_URD_ID=$(bd create \
  --title "FOLLOWUP_URD: Requirements for <feature> follow-up" \
  --labels "aura:p2-user:s2_2-urd,aura:urd" \
  --description "---
references:
  followup_epic: <followup-epic-id>
  original_urd: <original-urd-id>
---
Follow-up requirements. References original URD.")
bd dep add $FOLLOWUP_URE_ID --blocked-by $FOLLOWUP_URD_ID
` + "```",
				},
				{
					ID:    "impl-rev-followup-step3",
					Title: "Step 3: Leaf task adoption (dual-parent)",
					Content: `When the supervisor creates FOLLOWUP_SLICE-N tasks, the IMPORTANT/MINOR leaf tasks from the original review gain a second parent:

` + "```bash\n" +
						`# Leaf task gets dual-parent: original severity group + follow-up slice
bd dep add <followup-slice-id> --blocked-by <important-leaf-task-id>
bd dep add <followup-slice-id> --blocked-by <minor-leaf-task-id>
# Leaf task already has: bd dep add <severity-group-id> --blocked-by <leaf-task-id>
` + "```",
				},
				{
					ID:    "impl-rev-followup-handoff",
					Title: "Reviewer → Followup Handoff (h5)",
					Content: `The h5 handoff **starts** the follow-up lifecycle. Create this handoff document:
` + "```\n" +
						`.git/.aura/handoff/<request-task-id>/reviewer-to-followup.md
` + "```\n" +
						`
` + "```markdown\n" +
						`# Handoff: Reviewer → Follow-up Epic

## Context
- Request: <request-task-id>
- Follow-up Epic: <followup-epic-id>

## IMPORTANT Findings
| Finding | Slice | Severity Group | Description |
|---------|-------|---------------|-------------|
| <id> | SLICE-1 | <important-group-id> | <summary> |

## MINOR Findings
| Finding | Slice | Severity Group | Description |
|---------|-------|---------------|-------------|
| <id> | SLICE-2 | <minor-group-id> | <summary> |

## Recommended Priority Order
1. <highest-priority IMPORTANT finding>
2. <next>
` + "```",
				},
				{
					ID:    "impl-rev-followup-chain",
					Title: "Follow-up Handoff Chain",
					Content: `Inside the follow-up lifecycle, the same handoff types (h1-h4) apply but scoped to the follow-up epic:

| Order | Handoff | Transition |
|-------|---------|------------|
| 1 | h5 | Reviewer → Followup: **Starts** the follow-up lifecycle |
| 2 | *(none)* | Supervisor creates FOLLOWUP_URE (same actor) |
| 3 | *(none)* | Supervisor creates FOLLOWUP_URD (same actor) |
| 4 | h6 | Supervisor → Architect: Hands off FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL |
| 5 | h1 | Architect → Supervisor: After FOLLOWUP_PROPOSAL ratified |
| 6 | h2 | Supervisor → Worker: FOLLOWUP_SLICE-N with adopted leaf task IDs |
| 7 | h3 | Supervisor → Reviewer: Code review of follow-up slices |
| 8 | h4 | Worker → Reviewer: Follow-up slice completion |

Follow-up handoff storage: ` + "`.git/.aura/handoff/{followup-epic-id}/{source}-to-{target}.md`" + `

See ` + "`../protocol/HANDOFF_TEMPLATE.md`" + ` for full follow-up handoff examples and field requirements.`,
				},
			},
		},
		{
			ID:    "impl-rev-proceed-uat",
			Title: "Proceeding to UAT",
			Content: `Only when ALL reviews are ACCEPT and all BLOCKERs are resolved:

` + "```bash\n" +
				`# Verify consensus — no open BLOCKERs
bd list --labels="aura:severity:blocker" --status=open
# Should return 0 results

# Proceed to Phase 11 (Implementation UAT)
Skill(/aura:user-uat)
` + "```",
		},
	},
}
