---
name: reviewer-review-code
description: Review implementation slices with EAGER severity tree
---

# Review Code Implementation

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.33 artifact v1 -->
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
- Given: per-wave code review and end-of-epoch convergence
- When: evaluating review results
- Then: iterate review -> fix -> re-review up to the chosen review-effort budget; during a wave only an open BLOCKER finding gates progress, while IMPORTANT and MINOR findings stay open; after the last wave, converge over open `pasture:finding` tasks across all waves toward 0/0/0 within maxRounds; a finding closes only per `C-finding-lifecycle`; on budget exhaustion without convergence, SURFACE the outstanding findings to the user at a gate for a decision
- Should not: hardcode the budget; proceed past the chosen budget without surfacing outstanding findings to the user; require a zeroed round at a wave boundary; close a finding outside `C-finding-lifecycle`; loop forever when a finite budget was chosen

**[frag--validation-cases]**
- Given: any REQUEST (every request, not only fix-intent ones)
- When: eliciting (URE), acceptance-testing (UAT), or implementing
- Then: elicit concrete validation cases — a definition of done plus correct and incorrect behaviours (inputs/behaviors that must pass or must fail), confirm the case set with the user in UAT, evaluate the implementation against them, and store failing real-data cases as test fixtures
- Should not: ship without validation cases; treat validation cases as applying to fix-intent requests only; introduce a request-type axis or enum to gate them

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. With no origin, both backends use file:// of the working directory, so invocations from different subdirectories use different namespaces. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

Assigned to review code implementation after worker slices complete (Phase 10).

## Severity Tree: EAGER Creation

First bind REVIEW_ID_URI using Step 4: Create Review Task below; then **ALWAYS create 3 severity group tasks per wave, per axis, per round**, even if some groups have no findings:

### Step 1: Create All 3 Severity Groups Immediately

```bash
# One severity trio per wave, per axis, per round — not one per slice.
# Wave 1 contains the first slice, the second slice, and the third slice.
# Step 1: Create all 3 severity groups immediately (EAGER, not lazy)
BLOCKER_GROUP_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} BLOCKER" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review: \"${REVIEW_ID_URI}\"
---
BLOCKER findings for this wave review round")
"${EPOCH_REC}" label "$BLOCKER_GROUP_ID_URI" pasture:severity:blocker

IMPORTANT_GROUP_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} IMPORTANT" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review: \"${REVIEW_ID_URI}\"
---
IMPORTANT findings for this wave review round")
"${EPOCH_REC}" label "$IMPORTANT_GROUP_ID_URI" pasture:severity:important

MINOR_GROUP_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} MINOR" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  review: \"${REVIEW_ID_URI}\"
---
MINOR findings for this wave review round")
"${EPOCH_REC}" label "$MINOR_GROUP_ID_URI" pasture:severity:minor

# Step 2: Wire the one severity trio to the wave's review task
"${EPOCH_REC}" block "${REVIEW_ID_URI}" --by "${BLOCKER_GROUP_ID_URI}"
"${EPOCH_REC}" block "${REVIEW_ID_URI}" --by "${IMPORTANT_GROUP_ID_URI}"
"${EPOCH_REC}" block "${REVIEW_ID_URI}" --by "${MINOR_GROUP_ID_URI}"
```

### Adding Findings to Severity Groups

```bash
# Each finding carries slice: and wave: in references.
# BLOCKER finding — dual-parent relationship
BLOCKER_FINDING_ID_URI=$("${EPOCH_REC}" create --kind task --title "BLOCKER: <finding title>" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_1_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
<finding details with file:line references>")
"${EPOCH_REC}" label "${BLOCKER_FINDING_ID_URI}" pasture:finding
"${EPOCH_REC}" block "${BLOCKER_GROUP_ID_URI}" --by "${BLOCKER_FINDING_ID_URI}"
"${EPOCH_REC}" block "${SLICE_1_1_ID_URI}" --by "${BLOCKER_FINDING_ID_URI}"

# IMPORTANT finding — single parent (severity group only)
IMPORTANT_FINDING_ID_URI=$("${EPOCH_REC}" create --kind task --title "IMPORTANT: <finding title>" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_2_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
<finding details>")
"${EPOCH_REC}" label "${IMPORTANT_FINDING_ID_URI}" pasture:finding
"${EPOCH_REC}" block "${IMPORTANT_GROUP_ID_URI}" --by "${IMPORTANT_FINDING_ID_URI}"

# MINOR finding — single parent (severity group only)
MINOR_FINDING_ID_URI=$("${EPOCH_REC}" create --kind task --title "MINOR: <finding title>" --phase code_review \
  --body "---
references:
  wave: 1
  slice: \"${SLICE_1_3_ID_URI}\"
  reviewer: reviewer-A
  round: 1
---
<finding details>")
"${EPOCH_REC}" label "${MINOR_FINDING_ID_URI}" pasture:finding
"${EPOCH_REC}" block "${MINOR_GROUP_ID_URI}" --by "${MINOR_FINDING_ID_URI}"
```

### Closing Empty Groups

Empty severity groups (no findings) are closed immediately:

```bash
# If no IMPORTANT findings were found:
"${EPOCH_REC}" close "${IMPORTANT_GROUP_ID_URI}"

# If no MINOR findings were found:
"${EPOCH_REC}" close "${MINOR_GROUP_ID_URI}"
```

### Dual-Parent BLOCKER Relationship

BLOCKER findings have **two parents**:
1. The severity group task (`pasture:severity:blocker`) — for categorization
2. The slice they block — for dependency tracking

This ensures BLOCKERs both categorize under the severity tree AND block the slice they apply to.

IMPORTANT and MINOR findings do **NOT** block the slice via dual-parent (only BLOCKER does), and they are **not** routed to a follow-up epic: they stay open past the wave gate and converge at the end, where the convergence rule reads open pasture:finding tasks across all waves (R7/A1). The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by any review severity.

## Steps



### Step 1: Read Code Changes and URD

```bash
"${EPOCH_REC}" show "${SLICE_ID_URI}"
"${EPOCH_REC}" show "${URD_ID_URI}"   # Read URD for requirements context
```

### Step 2: Run Quality Gates

```bash
# Run your project's type checking and test commands
```

### Step 3: Apply Review Criteria and Verify Production Code Paths

Apply end-user alignment criteria (see `pasture:reviewer`) and verify production code paths (see Verify Production Code Paths section below).

### Step 4: Create Review Task

```bash
REVIEW_ID_URI=$("${EPOCH_REC}" create --kind task --title "Review: <feature>" --phase code_review \
  --body "---
references:
  wave: 1
  slices:
    - \"${SLICE_1_1_ID_URI}\"
    - \"${SLICE_1_2_ID_URI}\"
    - \"${SLICE_1_3_ID_URI}\"
  urd: \"${URD_ID_URI}\"
---
VOTE: <ACCEPT|REVISE> - <justification>")
# One review covers the whole wave, so wire every slice in the wave to it.
"${EPOCH_REC}" block "${SLICE_1_1_ID_URI}" --by "${REVIEW_ID_URI}"
"${EPOCH_REC}" block "${SLICE_1_2_ID_URI}" --by "${REVIEW_ID_URI}"
"${EPOCH_REC}" block "${SLICE_1_3_ID_URI}" --by "${REVIEW_ID_URI}"
```

### Steps 5–8: Severity Tree and Vote

5. Create severity tree (EAGER — all 3 groups immediately)
6. Add findings to appropriate severity groups
7. Close empty severity groups
8. Cast vote via `"${EPOCH_REC}" note`

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

## Two-Phase Review (within the chosen review-effort budget)

Per [frag--review-clean-exit], `C-finding-lifecycle`, and `C-review-effort-budget`, run the two-phase review. **During a wave**, only an open BLOCKER finding gates progress; IMPORTANT and MINOR findings stay open. **After the last wave**, convergence queries every open `pasture:finding` across all waves and targets **0/0/0** within the gate declaration's `maxRounds`. Iterate **review → fix → re-review** up to the **review-effort budget chosen at Phase 8** (defaults: 3 rounds / 1 round / 0 rounds / unlimited / custom). The close rule is owned by `C-finding-lifecycle`. On **budget exhaustion without convergence**, SURFACE the outstanding findings to the user at a gate for a decision — do not proceed dirty and do not loop forever. The budget is never hardcoded.

## Follow-up Epic

The FOLLOWUP epic is **not** created from review findings. Open review findings converge at the end; their close rule is `C-finding-lifecycle` (R7/A1). The FOLLOWUP epic is fed ONLY by **user-DEFER'd UAT items** (Phase 11), and the Supervisor creates it from those (label `pasture:epic-followup`).

## Reviewing a follow-up slice

When reviewing follow-up slices, use the same procedure:
- **Review task naming:** name the review for the follow-up slice, the axis, and the round
- **Same EAGER severity tree** (BLOCKER/IMPORTANT/MINOR per wave, per axis, per round)
- **Open findings converge at the end:** findings (BLOCKER/IMPORTANT/MINOR) in a FOLLOWUP_SLICE review also stay open past the wave gate and converge at the end — they are **never** re-routed to a follow-up epic (no followup-of-followup; the FOLLOWUP epic is fed only by user-DEFER'd UAT items)
- The worker's completion handoff (h4) reports which DEFER'd-item leaf tasks were resolved — verify these during review

## Report Results

```bash
# Add vote comment to the review task
"${EPOCH_REC}" note "${REVIEW_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: ACCEPT - Implementation matches plan, tests comprehensive"

# Or
"${EPOCH_REC}" note "${REVIEW_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: REVISE - BLOCKERs found, see severity tree for details"
```
<!-- END GENERATED FROM pasture schema -->
