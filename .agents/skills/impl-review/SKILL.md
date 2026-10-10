---
name: impl-review
description: Code review coordination across all slices (Phase 10)
---

# Implementation Code Review (Phase 10)

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:impl:review` — Code review coordination across all slices (Phase 10)

Conduct code review across ALL implementation slices. Each of 3 reviewers reviews every slice.

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-10-code-review)** <- Phase 10

See `../protocol/CONSTRAINTS.md` for coding standards and severity definitions.

**[frag--sup-review-all-slices]**
- Given: all slices complete
- When: starting review
- Then: spawn 3 reviewers for ALL slices
- Should not: assign reviewers to single slices

**[frag--sup-review-check-each]**
- Given: reviewer assigned
- When: reviewing
- Then: check each slice against criteria
- Should not: skip any slice

**[frag--sup-review-severity-groups]**
- Given: review round
- When: creating severity groups
- Then: ALWAYS create 3 severity groups (BLOCKER, IMPORTANT, MINOR) per round even if empty
- Should not: lazily create groups only when findings exist

**[frag--sup-blocker-dual-parent]**
- Given: BLOCKER finding
- When: wiring dependencies
- Then: add dual-parent: blocks BOTH the severity group AND the slice
- Should not: wire BLOCKER to only one parent

**[frag--sup-deferred-followup]**
- Given: a review finding (BLOCKER, IMPORTANT, or MINOR)
- When: categorizing
- Then: track it in its severity group; ALL severity groups must reach 0 before wave close — the FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by any review severity
- Should not: route any review severity (BLOCKER/IMPORTANT/MINOR) to the FOLLOWUP epic; close a wave with any finding outstanding

**[frag--sup-followup-epic-timing]**
- Given: UAT (Phase 5 or 11) produces one or more user-DEFER'd items
- When: finishing UAT
- Then: supervisor creates the FOLLOWUP epic from the user-DEFER'd UAT items only
- Should not: create a FOLLOWUP epic from any review severity (BLOCKER/IMPORTANT/MINOR)

## Task Recovery

Recover from live Pasture state at session start and after compaction; never trust cached task rows.

Store selection: an explicit --db overrides PASTURE_DB_PATH; otherwise use XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db. Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

Set PASTURE_NAMESPACE to the repository's canonical namespace URI, for example https://github.com/dayvidpham/pasture. Explicit --namespace overrides the git-remote-derived namespace, then file:// of the working directory. List requires an explicit namespace to avoid mixing repositories. ready/blocked have only an exact --label filter, not namespace filtering; inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run pasture task ready, pasture task blocked, and pasture task list --namespace "$PASTURE_NAMESPACE" --status in_progress. For each referenced active task, run pasture task show "$TASK_URI", pasture task comments "$TASK_URI", and pasture task timeline "$TASK_URI". All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the actual JSON id returned by create; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create returns an object whose id is the task URI: capture it with --format json and python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'. Persist that URI before label/comment steps. Create-plus-label is non-atomic: on failure retain the URI and retry only the failed label/comment operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

Assignment transfer is not initial allocation: before using pasture task assignment transfer, obtain the existing owner-responsibility assignment and its exact successor assignment ID, registered committing actor, and registered worker occupant from the supervisor. If the task is unassigned, stop and let the supervisor arrange allocation; no assignment-start command is implied. Never repeat an identical transfer as a substitute for allocating different workers.

Parent stays open and is blocked by child: pasture task dep add "$PARENT_URI" --blocked-by "$CHILD_URI". Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

## Task Attribution

Select the current registered author with pasture task agents list and pasture task agents show ACTOR-ID. Never auto-register or guess an identity during recovery. Every comment supplies --author explicitly; machine examples use the pre-registered pasture-system--00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## Severity Tree (EAGER Creation)

Per [frag--sup-review-severity-groups], create all 3 severity groups immediately:

```bash
# Step 1: Create all 3 severity groups immediately (EAGER)
BLOCKER_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 BLOCKER" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  review_round: 1
---
BLOCKER findings from Reviewer A (Correctness) on SLICE-1." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$BLOCKER_ID_URI" pasture:severity:blocker
pasture task label add "$BLOCKER_ID_URI" pasture:p10-impl:s10-review

IMPORTANT_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 IMPORTANT" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  review_round: 1
---
IMPORTANT findings from Reviewer A (Correctness) on SLICE-1." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$IMPORTANT_ID_URI" pasture:severity:important
pasture task label add "$IMPORTANT_ID_URI" pasture:p10-impl:s10-review

MINOR_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 MINOR" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_1_ID_URI}"
  review_round: 1
---
MINOR findings from Reviewer A (Correctness) on SLICE-1." | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$MINOR_ID_URI" pasture:severity:minor
pasture task label add "$MINOR_ID_URI" pasture:p10-impl:s10-review

# Step 2: Wire severity groups to the review round task
pasture task dep add "${REVIEW_ROUND_ID_URI}" --blocked-by "$BLOCKER_ID_URI"
pasture task dep add "${REVIEW_ROUND_ID_URI}" --blocked-by "$IMPORTANT_ID_URI"
pasture task dep add "${REVIEW_ROUND_ID_URI}" --blocked-by "$MINOR_ID_URI"
# NEVER wire severity groups to IMPL_PLAN or slices directly.
# BLOCKER findings block slices via dual-parent (see below).
# IMPORTANT/MINOR must ALSO reach 0 before wave close — they are NOT routed to FOLLOWUP.
# The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items (see Follow-up Epic section).

# Step 3: Close empty groups immediately
# If a group has no findings, close it right away
pasture task close "$IMPORTANT_ID_URI"   # if no IMPORTANT findings
pasture task close "$MINOR_ID_URI"        # if no MINOR findings
```

### Naming Convention

```
SLICE-{N}-REVIEW-{axis}-{round}
```

Where axis = A (Correctness), B (Test quality), C (Elegance).

Examples:
- `SLICE-1-REVIEW-A-1` — Reviewer A (Correctness), Round 1, SLICE-1
- `SLICE-2-REVIEW-C-2` — Reviewer C (Elegance), Round 2, SLICE-2

Severity groups:
- `SLICE-1-REVIEW-A-1 BLOCKER`
- `SLICE-1-REVIEW-A-1 IMPORTANT`
- `SLICE-1-REVIEW-A-1 MINOR`

## Dual-Parent BLOCKER Relationship

BLOCKER findings have **two parents**:
1. The severity group task (`pasture:severity:blocker`) — for categorization
2. The slice they block — for dependency tracking

```bash
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
```

Per [frag--sup-deferred-followup], IMPORTANT/MINOR findings attach to their severity group only (they do **not** block the slice via dual-parent), but ALL severity groups (BLOCKER/IMPORTANT/MINOR) must reach 0 before the review wave closes — they are **never** routed to the FOLLOWUP epic. The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items.

```bash
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
```

## Review Structure

Each reviewer (A, B, C) reviews EVERY slice:

```
Reviewer A (Correctness): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-A-1, SLICE-2-REVIEW-A-1, SLICE-3-REVIEW-A-1
  Each review has 3 severity groups (BLOCKER/IMPORTANT/MINOR)

Reviewer B (Test quality): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-B-1, SLICE-2-REVIEW-B-1, SLICE-3-REVIEW-B-1

Reviewer C (Elegance): Reviews SLICE-1, SLICE-2, SLICE-3 →
  Creates: SLICE-1-REVIEW-C-1, SLICE-2-REVIEW-C-1, SLICE-3-REVIEW-C-1
```

## Spawning Reviewers

Supervisor spawns 3 parallel reviewers as **subagents** (via the Task tool) or via **TeamCreate**. Reviewers are short-lived — keep them in-session.

```
// Spawn 3 reviewers (one per axis)
Task({
  subagent_type: "general-purpose",
  run_in_background: true,
  prompt: `You are Reviewer A (Correctness).
URD: "${URD_ID_URI}" (read with pasture task show "${URD_ID_URI}" for user requirements context)
Focus: Does implementation faithfully serve the user? Are technical decisions consistent with rationale?
Review ALL slices: "${SLICE_1_ID_URI}", "${SLICE_2_ID_URI}", "${SLICE_3_ID_URI}"
For each slice, run: pasture task show "${SLICE_ID_URI}"
Create severity groups (BLOCKER/IMPORTANT/MINOR) for each slice. Title: SLICE-N-REVIEW-A-1
Call Skill(/pasture:reviewer-review-code) for the review procedure.`
})
```

**Handoff:** Before spawning each reviewer, author its handoff in a Pasture task body (the task body IS the handoff — no filesystem path).

### Supervisor → Reviewer Handoff Template

```markdown
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
1. For each slice: `pasture task show "${SLICE_ID_URI}"`
2. Create 3 severity groups per slice (EAGER)
3. Add findings as children of severity groups
4. BLOCKER findings: dual-parent (severity group + slice)
5. Close empty severity groups immediately
6. Vote ACCEPT or REVISE per slice
```

## Review Criteria

Each reviewer checks each slice for:

1. **Requirements Alignment (check URD)**
   - Does implementation match ratified plan?
   - Are all acceptance criteria met?
   - Read URD (`pasture task show "${URD_ID_URI}"`) for requirements traceability

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
   - All items from slice checklist verified?

## Voting: ACCEPT vs REVISE (Binary Only)

| Vote | Requirement |
|------|-------------|
| **ACCEPT** | All 5 criteria satisfied; no BLOCKER items |
| **REVISE** | BLOCKER issues found; must provide actionable feedback |

**Documentation (via Pasture comments):**
```bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${SLICE_ID_URI}" "VOTE: ACCEPT - [reason]"
# OR
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${SLICE_ID_URI}" "VOTE: REVISE - [specific issue]. Suggest: [fix]"
```

## Consensus Check

All reviews across all slices must be ACCEPT:

```bash
# Check for any REVISE votes
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p10-impl:s10-review"

# Check for unresolved BLOCKERs
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:blocker" --status=open

# If any REVISE or open BLOCKERs, return to implementation
# If all ACCEPT and BLOCKERs resolved, proceed to Phase 11 (UAT)
```

## Handling REVISE

If any reviewer votes REVISE on any slice:

1. **Document issues** in the review task description
2. **Return slice to worker** for fixes
3. **Re-review** after fixes complete (new review round)

```bash
# Mark slice as needing revision
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${SLICE_ID_URI}" "REVISION NEEDED: <specific issues>"

# After worker fixes, start new review round
# New severity groups are created fresh for the new round
```

## Follow-up Epic (EPIC_FOLLOWUP)

Per [frag--sup-followup-epic-timing], create only when UAT produces user-DEFER'd items. Review findings of every severity must be resolved before the review wave closes.

### Step 1: Create the follow-up epic

```bash
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
```

### Step 2: Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)

The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

```
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
```

```bash
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
```

### Step 3: DEFER'd-item leaf adoption (dual-parent)

When the supervisor creates FOLLOWUP_SLICE-N tasks, the user-DEFER'd UAT-item leaf tasks gain a second parent (dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice):

```bash
# Leaf task gets dual-parent: DEFER'd-items tracking group + follow-up slice
pasture task dep add "${FOLLOWUP_SLICE_ID_URI}" --blocked-by "${DEFERRED_ITEM_LEAF_ID_1_URI}"
pasture task dep add "${FOLLOWUP_SLICE_ID_URI}" --blocked-by "${DEFERRED_ITEM_LEAF_ID_2_URI}"
# Leaf task already has: pasture task dep add "${DEFERRED_ITEMS_TRACKING_GROUP_ID_URI}" --blocked-by "${LEAF_TASK_ID_URI}"
```

### Followup Handoff (h5)

The h5 handoff (Reviewer → Supervisor, summary-with-ids) closes out the review wave. The FOLLOWUP epic itself is created later, at UAT, from the user-DEFER'd UAT items — **not** from review findings (all review severities reach 0 before the wave closes). Author this handoff in its Pasture task body (no filesystem path):

```markdown
# Handoff: Reviewer → Supervisor (review wave complete)

## Context
- Request: ${REQUEST_ID_URI}
- URD: ${URD_ID_URI}
- Ratified Proposal: ${PROPOSAL_ID_URI}

## Review Outcome
- All slices reviewed; ALL severity groups (BLOCKER/IMPORTANT/MINOR) reached 0 on a fix-free clean round.

## Open Items
- None for this wave. Any user-DEFER'd UAT items feed the FOLLOWUP epic at Phase 11.
```

### Follow-up Handoff Chain

Inside the follow-up lifecycle, the same handoff types (h1-h4) apply but scoped to the follow-up epic:

| Order | Handoff | Transition |
|-------|---------|------------|
| 1 | h5 | Reviewer → Followup: **Starts** the follow-up lifecycle |
| 2 | *(none)* | Supervisor creates FOLLOWUP_URE (same actor) |
| 3 | *(none)* | Supervisor creates FOLLOWUP_URD (same actor) |
| 4 | h6 | Supervisor → Architect: Hands off FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL |
| 5 | h1 | Architect → Supervisor: After FOLLOWUP_PROPOSAL ratified |
| 6 | h2 | Supervisor → Worker: FOLLOWUP_SLICE-N with DEFER'd-item leaf tasks |
| 7 | h3 | Supervisor → Reviewer: Code review of follow-up slices |
| 8 | h4 | Worker → Reviewer: Follow-up slice completion |

Follow-up handoff storage: each handoff is authored in its Pasture task body (no filesystem path).

See `../protocol/HANDOFF_TEMPLATE.md` for full follow-up handoff examples and field requirements.

## Proceeding to UAT

Only when ALL reviews are ACCEPT and all BLOCKERs are resolved:

```text
# Verify consensus — no open BLOCKERs
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:blocker" --status=open
# Should return 0 results

# Proceed to Phase 11 (Implementation UAT)
Skill(/pasture:user-uat)
```
<!-- END GENERATED FROM pasture schema -->
