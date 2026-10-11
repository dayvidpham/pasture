---
name: reviewer-review-code
description: Review implementation slices with EAGER severity tree
---

# Review Code Implementation

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:reviewer:review-code` — Review implementation slices with EAGER severity tree

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-10-code-review)** <- Phase 10

**[rev-code-quality-gates]**
- Given: code assignment
- When: reviewing
- Then: apply end-user alignment criteria and verify production code paths
- Should not: approve without running quality gates

**[rev-code-verify-gates]**
- Given: implementation
- When: verifying
- Then: run the project's quality gates
- Should not: approve without passing checks

**[rev-code-eager-severity]**
- Given: issues found
- When: categorizing
- Then: use BLOCKER/IMPORTANT/MINOR severity with EAGER group creation
- Should not: skip creating empty severity groups

**[frag--sup-blocker-dual-parent]**
- Given: BLOCKER finding
- When: wiring dependencies
- Then: add dual-parent: blocks BOTH the severity group AND the slice
- Should not: wire BLOCKER to only one parent

**[frag--review-clean-exit]**
- Given: per-slice code review
- When: evaluating review results
- Then: iterate review -> fix -> re-review up to the chosen review-effort budget; clean = 0 BLOCKER + 0 IMPORTANT + 0 MINOR within budget; on budget exhaustion without clean, SURFACE the outstanding findings to the user at a gate for a decision
- Should not: hardcode the budget; proceed past the chosen budget without surfacing outstanding findings to the user; loop forever when a finite budget was chosen

**[frag--validation-cases]**
- Given: any REQUEST (every request, not only fix-intent ones)
- When: eliciting (URE), acceptance-testing (UAT), or implementing
- Then: elicit concrete validation cases — a definition of done plus correct and incorrect behaviours (inputs/behaviors that must pass or must fail), confirm the case set with the user in UAT, evaluate the implementation against them, and store failing real-data cases as test fixtures
- Should not: ship without validation cases; treat validation cases as applying to fix-intent requests only; introduce a request-type axis or enum to gate them

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

## When to Use

Assigned to review code implementation after worker slices complete (Phase 10).

## Severity Tree: EAGER Creation

First bind REVIEW_ID_URI using Step 4: Create Review Task below; then **ALWAYS create 3 severity group tasks per review round**, even if some groups have no findings:

### Step 1: Create All 3 Severity Groups Immediately

```bash
# Step 1: Create all 3 severity groups immediately (EAGER, not lazy)
BLOCKER_GROUP_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 BLOCKER" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_ID_URI}"
  review: "${REVIEW_ID_URI}"
---
BLOCKER findings for this review round" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$BLOCKER_GROUP_ID_URI" pasture:severity:blocker
pasture task label add "$BLOCKER_GROUP_ID_URI" pasture:p10-impl:s10-review

IMPORTANT_GROUP_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 IMPORTANT" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_ID_URI}"
  review: "${REVIEW_ID_URI}"
---
IMPORTANT findings for this review round" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$IMPORTANT_GROUP_ID_URI" pasture:severity:important
pasture task label add "$IMPORTANT_GROUP_ID_URI" pasture:p10-impl:s10-review

MINOR_GROUP_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1 MINOR" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_ID_URI}"
  review: "${REVIEW_ID_URI}"
---
MINOR findings for this review round" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$MINOR_GROUP_ID_URI" pasture:severity:minor
pasture task label add "$MINOR_GROUP_ID_URI" pasture:p10-impl:s10-review

# Step 2: Wire severity groups to review task
pasture task dep add "${REVIEW_ID_URI}" --blocked-by "${BLOCKER_GROUP_ID_URI}"
pasture task dep add "${REVIEW_ID_URI}" --blocked-by "${IMPORTANT_GROUP_ID_URI}"
pasture task dep add "${REVIEW_ID_URI}" --blocked-by "${MINOR_GROUP_ID_URI}"
```

### Adding Findings to Severity Groups

```bash
# BLOCKER finding — dual-parent relationship
BLOCKER_FINDING_ID_URI=$(pasture task create "BLOCKER: <finding title>" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "<finding details with file:line references>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task dep add "${BLOCKER_GROUP_ID_URI}" --blocked-by "${BLOCKER_FINDING_ID_URI}"
pasture task dep add "${SLICE_ID_URI}" --blocked-by "${BLOCKER_FINDING_ID_URI}"

# IMPORTANT finding — single parent (severity group only)
IMPORTANT_FINDING_ID_URI=$(pasture task create "IMPORTANT: <finding title>" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "<finding details>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task dep add "${IMPORTANT_GROUP_ID_URI}" --blocked-by "${IMPORTANT_FINDING_ID_URI}"

# MINOR finding — single parent (severity group only)
MINOR_FINDING_ID_URI=$(pasture task create "MINOR: <finding title>" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "<finding details>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task dep add "${MINOR_GROUP_ID_URI}" --blocked-by "${MINOR_FINDING_ID_URI}"
```

### Closing Empty Groups

Empty severity groups (no findings) are closed immediately:

```bash
# If no IMPORTANT findings were found:
pasture task close "${IMPORTANT_GROUP_ID_URI}"

# If no MINOR findings were found:
pasture task close "${MINOR_GROUP_ID_URI}"
```

### Dual-Parent BLOCKER Relationship

BLOCKER findings have **two parents**:
1. The severity group task (`pasture:severity:blocker`) — for categorization
2. The slice they block — for dependency tracking

This ensures BLOCKERs both categorize under the severity tree AND block the slice they apply to.

IMPORTANT and MINOR findings do **NOT** block the slice via dual-parent (only BLOCKER does), but they are **not** routed to a follow-up epic either: ALL severity groups (BLOCKER, IMPORTANT, MINOR) must reach 0 before the review wave closes (R7/A1). The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by any review severity.

## Steps



### Step 1: Read Code Changes and URD

```bash
pasture task show "${SLICE_ID_URI}"
pasture task show "${URD_ID_URI}"   # Read URD for requirements context
```

### Step 2: Run Quality Gates

```bash
# Run your project's type checking and test commands
```

### Step 3: Apply Review Criteria and Verify Production Code Paths

Apply end-user alignment criteria (see `pasture:reviewer`) and verify production code paths (see Verify Production Code Paths section below).

### Step 4: Create Review Task

```bash
REVIEW_ID_URI=$(pasture task create "SLICE-1-REVIEW-A-1: <feature>" --phase code_review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  slice: "${SLICE_ID_URI}"
  urd: "${URD_ID_URI}"
---
VOTE: <ACCEPT|REVISE> - <justification>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$REVIEW_ID_URI" pasture:p10-impl:s10-review
pasture task dep add "${SLICE_ID_URI}" --blocked-by "${REVIEW_ID_URI}"
```

### Steps 5–8: Severity Tree and Vote

5. Create severity tree (EAGER — all 3 groups immediately)
6. Add findings to appropriate severity groups
7. Close empty severity groups
8. Cast vote via `pasture task comment add`

## Verify Production Code Paths



### Check for Dual-Export Anti-Pattern

**Anti-pattern example:**
```go
// WRONG: Test-only export
func HandleCommand(argv []string, service Service) error { /* tested */ }

// WRONG: Production-only command (not tested)
var commandCmd = &cobra.Command{
    Use: "command",
    RunE: func(cmd *cobra.Command, args []string) error {
        // TODO: wire up service
        return nil
    },
}
```

**Correct example:**
```go
// CORRECT: Single command, both tested and used in production
var commandCmd = &cobra.Command{
    Use: "command",
    RunE: func(cmd *cobra.Command, args []string) error {
        service := NewService(RealDeps{})
        result, err := service.DoThing(args)
        if err != nil {
            return err
        }
        fmt.Println(result)
        return nil
    },
}

// Tests import commandCmd directly
// import "myproject/cmd/thing"
```

### Verify No TODO Placeholders

```bash
grep -r "TODO" src/  # Should not find any in delivered code
```

### Check Tests Import Production Code

- Test file should import the actual CLI command or API endpoint
- Not a separate test harness function
- No TODOs in CLI/API actions
- Real dependencies wired (not mocks in production code)

### Verify Validation Cases (R6)

For **every** REQUEST (not only fix-intent ones), per [frag--validation-cases] verify the implementation:
- Carries **test fixtures** for the concrete validation cases captured in URE/UAT (the definition of done plus the correct/incorrect behaviours that must pass or must fail).
- Evaluates the implementation against each confirmed validation case.

An implementation that ships without validation-case fixtures is an IMPORTANT finding. There is **no** request-type axis/enum gating this — recognize what a request needs from the REQUEST/URD.

## Clean-Review Exit (within the chosen review-effort budget)

Per [frag--review-clean-exit] and `C-review-effort-budget`, iterate **review → fix → re-review** up to the **review-effort budget chosen at Phase 8** (defaults: 3 rounds / 1 round / 0 rounds / unlimited / custom) until a fix-free clean round confirms **0 BLOCKER + 0 IMPORTANT + 0 MINOR** within budget. On **budget exhaustion without a clean round**, SURFACE the outstanding findings to the user at a gate for a decision — do not proceed dirty and do not loop forever. The budget is never hardcoded. A wave never closes on a fix-applying round, and never with any finding silently outstanding.

## Follow-up Epic

The FOLLOWUP epic is **not** created from review findings. ALL review severities (BLOCKER/IMPORTANT/MINOR) must reach 0 before the wave closes (R7/A1). The FOLLOWUP epic is fed ONLY by **user-DEFER'd UAT items** (Phase 11), and the Supervisor creates it from those (label `pasture:epic-followup`).

## Reviewing FOLLOWUP_SLICE-N (Follow-up Code Review)

When reviewing follow-up slices, use the same procedure:
- **Review task naming:** `FOLLOWUP_SLICE-N-REVIEW-{axis}-{round}`
- **Same EAGER severity tree** (BLOCKER/IMPORTANT/MINOR per review round)
- **All severities reach 0:** ALL findings (BLOCKER/IMPORTANT/MINOR) in a FOLLOWUP_SLICE review must also reach 0 before the follow-up wave closes — they are **never** re-routed to a follow-up epic (no followup-of-followup; the FOLLOWUP epic is fed only by user-DEFER'd UAT items)
- The worker's completion handoff (h4) reports which DEFER'd-item leaf tasks were resolved — verify these during review

## Report Results

```bash
# Add vote comment to the review task
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REVIEW_ID_URI}" "VOTE: ACCEPT - Implementation matches plan, tests comprehensive"

# Or
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REVIEW_ID_URI}" "VOTE: REVISE - BLOCKERs found, see severity tree for details"
```
<!-- END GENERATED FROM pasture schema -->
