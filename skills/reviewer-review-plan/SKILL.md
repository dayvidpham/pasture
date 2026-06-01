# Review Plan

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:reviewer:review-plan` — Evaluate proposal against one axis (binary ACCEPT/REVISE)

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phase 4

**[rev-plan-alignment]**
- Given: plan assignment
- When: reviewing
- Then: apply end-user alignment criteria
- Should not: focus only on technical details

**[rev-plan-revise-actionable]**
- Given: issues found
- When: voting
- Then: vote REVISE with specific feedback
- Should not: vote REVISE without actionable suggestions

**[rev-plan-document]**
- Given: review complete
- When: documenting
- Then: add comment to Beads task
- Should not: vote without written justification

**[rev-plan-binary-vote]**
- Given: plan review
- When: assessing
- Then: use ACCEPT/REVISE binary vote only
- Should not: create severity tree for plan reviews

## When to Use

Assigned to review a plan specification (Phase 4, `aura:p4-plan:s4-review`).

## End-User Alignment Criteria

Ask these questions for every plan:

1. **Who are the end-users?**
2. **What would end-users want?**
3. **How would this affect them?**
4. **Are there implementation gaps?**
5. **Does MVP scope make sense?**
6. **Is validation checklist complete and correct?**

## Production Code Path Questions

When reviewing plans, explicitly ask:

1. **What are the production code paths?**
   - CLI commands: Entry points users will run
   - API endpoints: HTTP handlers, services
   - Background jobs: Daemon processes

2. **How will production code be tested?**
   - Do Layer 2 tests import the actual CLI/API?
   - Or do they test a separate test-only export? (anti-pattern)

3. **What needs to be wired together?**
   - Service instantiation with real dependencies?
   - CLI command registration?
   - Entry point hookup?

4. **Are implementation tasks explicit about production code?**
   - Does the plan include tasks to wire production code?
   - Or are they only testing isolated units?

**Red flag:** Plan shows "Layer 2: service_test.go" but no task for "wire service into CLI command"

**Green flag:** Plan shows "Layer 3: Wire cobra command with NewService(realDeps)"

## Steps



### Step 1: Read PROPOSAL-N and URD

```bash
bd show <proposal-id>
bd show <urd-id>   # Read URD for user requirements context
```

### Step 2: Apply Criteria

Apply end-user alignment criteria (check against URD requirements). Verify `validation_checklist` items are verifiable and BDD acceptance criteria are complete.

### Step 3: Create Review Task

```bash
bd create --labels "aura:p4-plan:s4-review" \
  --title "PROPOSAL-1-REVIEW-A-1: <feature>" \
  --description "---
references:
  proposal: <proposal-id>
  urd: <urd-id>
---
VOTE: <ACCEPT|REVISE> - <justification>"
bd dep add <proposal-id> --blocked-by <review-id>
```

### Step 4: Add Vote Comment

```bash
# If accepting:
bd comments add <proposal-id> "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
bd comments add <proposal-id> "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."
```

## Vote Options

| Vote | When |
|------|------|
| ACCEPT | All review criteria satisfied; no BLOCKER items |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No severity tree for plan reviews.

## Consensus

All 3 reviewers must vote ACCEPT for plan to be ratified.

## Follow-up Proposal Reviews (FOLLOWUP_PROPOSAL-N)

The same procedure applies when reviewing FOLLOWUP_PROPOSAL-N:
- **Task naming:** `FOLLOWUP_PROPOSAL-N-REVIEW-{axis}-{round}`
- Same binary ACCEPT/REVISE vote (no severity tree)
- Additionally verify that FOLLOWUP_PROPOSAL addresses the specific IMPORTANT/MINOR findings scoped in FOLLOWUP_URE/URD
<!-- END GENERATED FROM aura schema -->
