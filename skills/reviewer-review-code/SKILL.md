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
- Then: iterate review -> fix -> re-review with NO cycle cap until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR
- Should not: close a wave on a fix-applying round, proceed with any finding outstanding, or impose a maximum cycle cap

**[frag--fix-validation-cases]**
- Given: a REQUEST whose user intent is to FIX existing behavior
- When: eliciting (URE), acceptance-testing (UAT), or implementing the fix
- Then: elicit concrete validation cases (inputs/behaviors that currently fail or must pass), confirm the case set with the user in UAT, evaluate the fix against them, and store failing real-data cases as test fixtures
- Should not: ship a fix without validation cases; introduce a request-type axis or enum to detect fix-intent

## When to Use

Assigned to review code implementation after worker slices complete (Phase 10).

## Severity Tree: EAGER Creation

**ALWAYS create 3 severity group tasks per review round**, even if some groups have no findings:

### Step 1: Create All 3 Severity Groups Immediately

```bash
# Step 1: Create all 3 severity groups immediately (EAGER, not lazy)
bd create --labels "pasture:severity:blocker,pasture:p10-impl:s10-review" \
  --title "SLICE-1-REVIEW-A-1 BLOCKER" \
  --description "---
references:
  slice: <slice-id>
  review: <review-id>
---
BLOCKER findings for this review round"
# Result: <blocker-group-id>

bd create --labels "pasture:severity:important,pasture:p10-impl:s10-review" \
  --title "SLICE-1-REVIEW-A-1 IMPORTANT" \
  --description "---
references:
  slice: <slice-id>
  review: <review-id>
---
IMPORTANT findings for this review round"
# Result: <important-group-id>

bd create --labels "pasture:severity:minor,pasture:p10-impl:s10-review" \
  --title "SLICE-1-REVIEW-A-1 MINOR" \
  --description "---
references:
  slice: <slice-id>
  review: <review-id>
---
MINOR findings for this review round"
# Result: <minor-group-id>

# Step 2: Wire severity groups to review task
bd dep add <review-id> --blocked-by <blocker-group-id>
bd dep add <review-id> --blocked-by <important-group-id>
bd dep add <review-id> --blocked-by <minor-group-id>
```

### Adding Findings to Severity Groups

```bash
# BLOCKER finding — dual-parent relationship
bd create --title "BLOCKER: <finding title>" \
  --description "<finding details with file:line references>"
bd dep add <blocker-group-id> --blocked-by <blocker-finding-id>
bd dep add <slice-id> --blocked-by <blocker-finding-id>

# IMPORTANT finding — single parent (severity group only)
bd create --title "IMPORTANT: <finding title>" \
  --description "<finding details>"
bd dep add <important-group-id> --blocked-by <important-finding-id>

# MINOR finding — single parent (severity group only)
bd create --title "MINOR: <finding title>" \
  --description "<finding details>"
bd dep add <minor-group-id> --blocked-by <minor-finding-id>
```

### Closing Empty Groups

Empty severity groups (no findings) are closed immediately:

```bash
# If no IMPORTANT findings were found:
bd close <important-group-id>

# If no MINOR findings were found:
bd close <minor-group-id>
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
bd show <slice-id>
bd show <urd-id>   # Read URD for requirements context
```

### Step 2: Run Quality Gates

```bash
# Run your project's type checking and test commands
```

### Step 3: Apply Review Criteria and Verify Production Code Paths

Apply end-user alignment criteria (see `pasture:reviewer`) and verify production code paths (see Verify Production Code Paths section below).

### Step 4: Create Review Task

```bash
bd create --labels "pasture:p10-impl:s10-review" \
  --title "SLICE-1-REVIEW-A-1: <feature>" \
  --description "---
references:
  slice: <slice-id>
  urd: <urd-id>
---
VOTE: <ACCEPT|REVISE> - <justification>"
bd dep add <slice-id> --blocked-by <review-id>
```

### Steps 5–8: Severity Tree and Vote

5. Create severity tree (EAGER — all 3 groups immediately)
6. Add findings to appropriate severity groups
7. Close empty severity groups
8. Cast vote via `bd comments add`

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

### Verify Fix Validation Cases (R6)

When the REQUEST is to **fix existing behavior**, per [frag--fix-validation-cases] verify the implementation:
- Carries **test fixtures** for the concrete validation cases captured in URE/UAT (the inputs/behaviors that currently fail or must pass).
- Evaluates the fix against each confirmed validation case.

A fix that ships without validation-case fixtures is an IMPORTANT finding. There is **no** request-type axis/enum to detect fix-intent — recognize it from the REQUEST/URD.

## Clean-Review Exit (no cycle cap)

Per [frag--review-clean-exit], do not impose a maximum cycle cap. Iterate **review → fix → re-review with NO cap** until a fix-free clean round confirms **0 BLOCKER + 0 IMPORTANT + 0 MINOR**. A wave never closes on a fix-applying round, and never with any finding outstanding.

## Follow-up Epic

The FOLLOWUP epic is **not** created from review findings. ALL review severities (BLOCKER/IMPORTANT/MINOR) must reach 0 before the wave closes (R7/A1). The FOLLOWUP epic is fed ONLY by **user-DEFER'd UAT items** (Phase 11), and the Supervisor creates it from those (label `pasture:epic-followup`).

## Reviewing FOLLOWUP_SLICE-N (Follow-up Code Review)

When reviewing follow-up slices, use the same procedure:
- **Review task naming:** `FOLLOWUP_SLICE-N-REVIEW-{axis}-{round}`
- **Same EAGER severity tree** (BLOCKER/IMPORTANT/MINOR per review round)
- **No followup-of-followup:** New IMPORTANT/MINOR findings from FOLLOWUP_SLICE review are tracked on the existing follow-up epic, not a new nested follow-up
- The worker's completion handoff (h4) reports which original leaf tasks were resolved — verify these during review

## Report Results

```bash
# Add vote comment to the review task
bd comments add <review-id> "VOTE: ACCEPT - Implementation matches plan, tests comprehensive"

# Or
bd comments add <review-id> "VOTE: REVISE - BLOCKERs found, see severity tree for details"
```
<!-- END GENERATED FROM pasture schema -->
