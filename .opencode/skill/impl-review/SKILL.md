---
name: impl-review
description: Code review coordination across all slices (Phase 10)
---

# Implementation Code Review (Phase 10)

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
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
- Given: a wave review round
- When: creating severity groups
- Then: ALWAYS create 3 severity groups (BLOCKER, IMPORTANT, MINOR) per wave, per axis, per round, even if empty
- Should not: lazily create groups only when findings exist

**[frag--sup-blocker-dual-parent]**
- Given: BLOCKER finding
- When: wiring dependencies
- Then: add dual-parent: blocks BOTH the severity group AND the slice
- Should not: wire BLOCKER to only one parent

**[frag--sup-deferred-followup]**
- Given: a review finding (BLOCKER, IMPORTANT, or MINOR)
- When: categorizing
- Then: track it in its severity group as an open finding; open findings do not block a wave unless they are BLOCKERs, and the FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by any review severity
- Should not: route any review severity (BLOCKER/IMPORTANT/MINOR) to the FOLLOWUP epic; close a finding outside `C-finding-lifecycle`

**[frag--sup-followup-epic-timing]**
- Given: UAT (Phase 5 or 11) produces one or more user-DEFER'd items
- When: finishing UAT
- Then: supervisor creates the FOLLOWUP epic from the user-DEFER'd UAT items only
- Should not: create a FOLLOWUP epic from any review severity (BLOCKER/IMPORTANT/MINOR)

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. A ledger with no origin uses file:// of the git work-tree root, so a subdirectory does not split the namespace. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## Severity Tree (EAGER Creation)

Per [frag--sup-review-severity-groups], create ONE severity trio per wave, per axis, per round — not one per slice:

```bash
# Wave 1 contains the first slice, the second slice, and the third slice.
# Step 1: Create all 3 severity groups immediately (EAGER)
BLOCKER_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} BLOCKER" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review_round: 1
---
BLOCKER findings from Reviewer A (Correctness) on wave 1.")
"${EPOCH_REC}" label "$BLOCKER_ID_URI" pasture:severity:blocker

IMPORTANT_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} IMPORTANT" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review_round: 1
---
IMPORTANT findings from Reviewer A (Correctness) on wave 1.")
"${EPOCH_REC}" label "$IMPORTANT_ID_URI" pasture:severity:important

MINOR_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} MINOR" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review_round: 1
---
MINOR findings from Reviewer A (Correctness) on wave 1.")
"${EPOCH_REC}" label "$MINOR_ID_URI" pasture:severity:minor

# Step 2: Wire the one severity trio to the wave's review round task
"${EPOCH_REC}" block "${REVIEW_ROUND_ID_URI}" --by "$BLOCKER_ID_URI"
"${EPOCH_REC}" block "${REVIEW_ROUND_ID_URI}" --by "$IMPORTANT_ID_URI"
"${EPOCH_REC}" block "${REVIEW_ROUND_ID_URI}" --by "$MINOR_ID_URI"
# NEVER wire severity groups to IMPL_PLAN or slices directly.
# BLOCKER findings block slices via dual-parent (see below).
# Open findings converge at the end; only an open BLOCKER gates the wave. IMPORTANT/MINOR are NOT routed to FOLLOWUP.
# The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items (see Follow-up Epic section).

# Step 3: Close empty groups immediately
# If a group has no findings, close it right away
"${EPOCH_REC}" close "$IMPORTANT_ID_URI"   # if no IMPORTANT findings
"${EPOCH_REC}" close "$MINOR_ID_URI"        # if no MINOR findings
```

### Naming Convention

Name the review for one wave, one axis, and one round. Axis A is correctness, B is test quality, and C is elegance. List every slice of that wave on the review. Create one severity trio under it: blocker, important, and minor. Do not create one review per slice.

The first wave's correctness review, first round, has one blocker group, one important group, and one minor group. A later wave gets its own review.

## Dual-Parent BLOCKER Relationship

BLOCKER findings have **two parents**:
1. The severity group task (`pasture:severity:blocker`) — for categorization
2. The slice they block — for dependency tracking

```bash
# Create a BLOCKER finding
FINDING_ID=$("${EPOCH_REC}" create --kind task --title "BLOCKER: Missing error handling in auth flow" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_1_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
Missing error handling causes silent failure in auth flow.")
"${EPOCH_REC}" label "$FINDING_ID" pasture:finding

# Wire dual-parent: finding blocks BOTH severity group AND slice
"${EPOCH_REC}" block "$BLOCKER_GROUP_ID_URI" --by "$FINDING_ID"
"${EPOCH_REC}" block "${SLICE_1_1_ID_URI}" --by "$FINDING_ID"
```

Per [frag--sup-deferred-followup], IMPORTANT/MINOR findings attach to their severity group only (they do **not** block the slice via dual-parent). They stay open past the wave gate and converge at the end, and they are **never** routed to the FOLLOWUP epic. The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items.

```bash
# IMPORTANT finding — attaches to the IMPORTANT severity group (NOT the slice)
IMPORTANT_FINDING_ID=$("${EPOCH_REC}" create --kind task --title "IMPORTANT: Add request timeout" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_2_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
API calls should have configurable timeouts.")
"${EPOCH_REC}" label "$IMPORTANT_FINDING_ID" pasture:finding

# Attaches to the IMPORTANT severity group (NOT the slice); its close rule is C-finding-lifecycle
"${EPOCH_REC}" block "$IMPORTANT_GROUP_ID_URI" --by "$IMPORTANT_FINDING_ID"
```

## Review Structure

One review round covers one whole wave, never a single slice. Wave 1 holds the first slice, the second slice, and the third slice; each reviewer reviews all three together and creates ONE review title for the wave:

```
Wave 1: the first slice, the second slice, the third slice
  Reviewer A (Correctness): reviews the whole wave →
    Creates: the review (references: wave: 1, slices: the first slice, the second slice, the third slice)
    Each review has 3 severity groups (BLOCKER/IMPORTANT/MINOR)

  Reviewer B (Test quality): reviews the whole wave →
    Creates: the review

  Reviewer C (Elegance): reviews the whole wave →
    Creates: the review
```

A later wave gets its own review titles (for example, wave 2 → the later review). A wave never produces one title per reviewed slice.

## Spawning Reviewers

Supervisor spawns 3 parallel reviewers as **subagents** via the task operation. Reviewers are short-lived — keep them in-session.

```
// Spawn 3 reviewers (one per axis)
delegation: {
  subagent_type: "general-purpose",
  run_in_background: true,
  prompt: `You are Reviewer A (Correctness).
URD: "${URD_ID_URI}" (read with "${EPOCH_REC}" show "${URD_ID_URI}" for user requirements context)
Focus: Does implementation faithfully serve the user? Are technical decisions consistent with rationale?
Review the whole wave: "${SLICE_1_1_ID_URI}", "${SLICE_1_2_ID_URI}", "${SLICE_1_3_ID_URI}"
For each slice, run: "${EPOCH_REC}" show "${SLICE_ID_URI}"
Create ONE severity trio (BLOCKER/IMPORTANT/MINOR) for the wave, listing every slice in its references. Title: the review
Call skill(/pasture:reviewer-review-code) for the review procedure.`
}
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
| the first slice | "${ID_URI}" | <description> | worker-1 |
| the second slice | "${ID_URI}" | <description> | worker-2 |

## Review Procedure
1. For each slice: `"${EPOCH_REC}" show "${SLICE_ID_URI}"`
2. Create ONE severity trio for the wave (EAGER)
3. Add findings as children of severity groups
4. BLOCKER findings: dual-parent (severity group + slice)
5. Close empty severity groups immediately
6. Vote ACCEPT or REVISE for the wave
```

## Review Criteria

Each reviewer checks every slice in the wave for:

1. **Requirements Alignment (check URD)**
   - Does implementation match ratified plan?
   - Are all acceptance criteria met?
   - Read URD (`"${EPOCH_REC}" show "${URD_ID_URI}"`) for requirements traceability

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

One vote per wave on the wave review task (not one per slice):

**Documentation (via Pasture comments):**
```bash
"${EPOCH_REC}" note "${REVIEW_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: ACCEPT - [reason]"
# OR
"${EPOCH_REC}" note "${REVIEW_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: REVISE - [specific issue]. Suggest: [fix]"
```

## Consensus Check

All reviews across all slices must be ACCEPT:

```bash
# Check for any REVISE votes
"${EPOCH_REC}" query --phase code_review

# Check for unresolved BLOCKERs
"${EPOCH_REC}" query --label="pasture:severity:blocker" --status=open

# If any REVISE or open BLOCKERs, return to implementation
# If all ACCEPT and BLOCKERs resolved, proceed to Phase 11 (UAT)
```

## Handling REVISE

If any reviewer votes REVISE on the wave:

1. **Document issues** in the review task description
2. **Return slice to worker** for fixes
3. **Re-review** after fixes complete (new review round)

```bash
# Mark slice as needing revision
"${EPOCH_REC}" note "${SLICE_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "REVISION NEEDED: <specific issues>"

# After worker fixes, start new review round
# New severity groups are created fresh for the new round
```

## Follow-up Epic (EPIC_FOLLOWUP)

Per [frag--sup-followup-epic-timing], create only when UAT produces user-DEFER'd items. Open review findings stay open past a wave gate and converge at the end; they never feed the FOLLOWUP epic.

### Step 1: Create the follow-up epic

```bash
FOLLOWUP_EPIC_ID_URI=$("${EPOCH_REC}" create --kind epic --title "FOLLOWUP: User-deferred improvements from UAT" --phase unscoped --priority 3 \
  --body="---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
  review_round: ${REVIEW_ROUND_ID_URI}
---
User-DEFER'd UAT items only; no review findings.")
"${EPOCH_REC}" label "$FOLLOWUP_EPIC_ID_URI" pasture:epic-followup

# Link only the UAT-deferred item leaves
"${EPOCH_REC}" block "$FOLLOWUP_EPIC_ID_URI" --by "$DEFERRED_ITEM_ID_1_URI"
"${EPOCH_REC}" block "$FOLLOWUP_EPIC_ID_URI" --by "$DEFERRED_ITEM_ID_2_URI"
```

### Step 2: Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)

The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

```
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
```

```bash
# Create follow-up lifecycle tasks. The URD lands first so the URE can record it.
FOLLOWUP_URD_ID=$("${EPOCH_REC}" create --kind task --title "FOLLOWUP_URD: Requirements for <feature> follow-up" --phase elicit \
  --body "---
references:
  followup_epic: "${FOLLOWUP_EPIC_ID_URI}"
  original_urd: "${ORIGINAL_URD_ID_URI}"
---
Follow-up requirements. References original URD.")
"${EPOCH_REC}" label "$FOLLOWUP_URD_ID" pasture:p2-user:s2_2-urd
"${EPOCH_REC}" label "$FOLLOWUP_URD_ID" pasture:urd

FOLLOWUP_URE_ID=$("${EPOCH_REC}" create --kind task --title "FOLLOWUP_URE: Scope follow-up for <feature>" --phase elicit \
  --body "---
references:
  followup_epic: \"${FOLLOWUP_EPIC_ID_URI}\"
  original_urd: \"${ORIGINAL_URD_ID_URI}\"
  followup_urd: \"${FOLLOWUP_URD_ID}\"
---
Scoping URE: determine which user-DEFER'd UAT items to address.")
"${EPOCH_REC}" label "$FOLLOWUP_URE_ID" pasture:p2-user:s2_1-elicit
"${EPOCH_REC}" block "${FOLLOWUP_EPIC_ID_URI}" --by "$FOLLOWUP_URE_ID"
```

### Step 3: DEFER'd-item leaf adoption (dual-parent)

When the supervisor creates the follow-up slice tasks, the user-DEFER'd UAT-item leaf tasks gain a second parent (dual-parent: leaf blocks BOTH the DEFER'd-items tracking group AND the follow-up slice):

```bash
# Leaf task gets dual-parent: DEFER'd-items tracking group + follow-up slice
"${EPOCH_REC}" block "${FOLLOWUP_SLICE_ID_URI}" --by "${DEFERRED_ITEM_LEAF_ID_1_URI}"
"${EPOCH_REC}" block "${FOLLOWUP_SLICE_ID_URI}" --by "${DEFERRED_ITEM_LEAF_ID_2_URI}"
# Leaf task already has: "${EPOCH_REC}" block "${DEFERRED_ITEMS_TRACKING_GROUP_ID_URI}" --by "${LEAF_TASK_ID_URI}"
```

### Reviewer → Supervisor Handoff (h5)

The h5 handoff (Reviewer → Supervisor, summary-with-ids) closes out the review wave. The FOLLOWUP epic itself is created later, at UAT, from the user-DEFER'd UAT items — **not** from review findings (open findings converge at the end; close rule: `C-finding-lifecycle`). Author this handoff in its Pasture task body (no filesystem path):

```markdown
# Handoff: Reviewer → Supervisor (review wave complete)

## Context
- Request: ${REQUEST_ID_URI}
- URD: ${URD_ID_URI}
- Ratified Proposal: ${PROPOSAL_ID_URI}

## Review Outcome
- All slices reviewed; convergence found no open pasture:finding across all waves.

## Open Items
- None for this wave. Any user-DEFER'd UAT items feed the FOLLOWUP epic at Phase 11.
```

### Follow-up Handoff Chain

Inside the follow-up lifecycle, the same handoff types (h1-h4) apply but scoped to the follow-up epic:

The follow-up lifecycle starts ONLY from user-DEFER'd UAT items (`C-followup-timing`); code review never starts it. The Supervisor creates the FOLLOWUP epic from those items (same actor), then:

| Order | Handoff | Transition |
|-------|---------|------------|
| 1 | *(none)* | Supervisor creates the FOLLOWUP epic from user-DEFER'd UAT items (same actor) |
| 2 | *(none)* | Supervisor creates FOLLOWUP_URE (same actor) |
| 3 | *(none)* | Supervisor creates FOLLOWUP_URD (same actor) |
| 4 | h6 | Supervisor → Architect: Hands off FOLLOWUP_URE + FOLLOWUP_URD for FOLLOWUP_PROPOSAL |
| 5 | h1 | Architect → Supervisor: After FOLLOWUP_PROPOSAL ratified |
| 6 | h2 | Supervisor → Worker: the follow-up slice with DEFER'd-item leaf tasks |
| 7 | h3 | Supervisor → Reviewer: One code review per follow-up wave |
| 8 | h4 | Worker → Reviewer: Follow-up slice completion |

Follow-up handoff storage: each handoff is authored in its Pasture task body (no filesystem path).

See `../protocol/HANDOFF_TEMPLATE.md` for full follow-up handoff examples and field requirements.

## Proceeding to UAT

Only when ALL reviews are ACCEPT and all BLOCKERs are resolved:

```text
# Verify consensus — no open BLOCKERs
"${EPOCH_REC}" query --label="pasture:severity:blocker" --status=open
# Should return 0 results

# Proceed to Phase 11 (Implementation UAT)
skill(/pasture:user-uat)
```
<!-- END GENERATED FROM pasture schema -->
