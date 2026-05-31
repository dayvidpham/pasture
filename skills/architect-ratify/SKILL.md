# Architect: Ratify Plan

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:architect:ratify` — Ratify proposal, mark old proposals aura:superseded

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-6-ratification)** <- Phase 6

**[arch-ratify-all-accept]**
- Given: all 3 reviewers voted ACCEPT
- When: ratifying
- Then: add `aura:p6-plan:s6-ratify` label to PROPOSAL-N
- Should not: ratify with any REVISE votes outstanding

**[arch-ratify-audit-trail]**
- Given: ratification
- When: documenting
- Then: add comment with reviewer sign-offs and UAT reference
- Should not: ratify without audit trail

**[arch-ratify-supersede-old]**
- Given: previous proposals exist
- When: ratifying new version
- Then: mark old proposals as `aura:superseded`
- Should not: leave old proposals without superseded marking

## When to Use

All 3 reviewers have voted ACCEPT on PROPOSAL-N and user has approved via UAT.

## Consensus Requirement

**All 3 reviewers must vote ACCEPT.** If any reviewer votes REVISE:
1. Architect creates PROPOSAL-N+1 addressing feedback
2. Marks PROPOSAL-N as `aura:superseded`
3. Reviewers re-review PROPOSAL-N+1
4. Repeat until all ACCEPT

## Steps



### Step 1: Check all reviews

```bash
bd show <proposal-id>
bd comments <proposal-id>
```

### Step 2: Verify all 3 votes are ACCEPT

Confirm each of the three review tasks (Reviewer A, B, C) has a VOTE: ACCEPT comment before proceeding.

### Step 3: Add ratify label to PROPOSAL-N

Do NOT create a new task — add label to the existing proposal:
```bash
bd label add <proposal-id> aura:p6-plan:s6-ratify
bd comments add <proposal-id> "RATIFIED: All 3 reviewers ACCEPT, UAT passed (<uat-task-id>)"
```

### Step 4: Mark all previous proposals as superseded

```bash
bd label add <old-proposal-id> aura:superseded
bd comments add <old-proposal-id> "Superseded by PROPOSAL-N (<ratified-proposal-id>)"
```

### Step 5: Update URD with ratification

```bash
bd comments add <urd-id> "Ratified: scope confirmed. Ratified proposal: <ratified-proposal-id>"
```

## Next Steps

After ratifying PROPOSAL-N:
1. **Prepare handoff** — Run `/aura:architect-handoff` to create handoff document and spawn supervisor

**IMPORTANT:** Do NOT start implementation yourself. The architect's role ends at handoff. Implementation is handled by the supervisor and workers spawned during handoff.

## Follow-up Proposals (FOLLOWUP_PROPOSAL-N)

When ratifying a FOLLOWUP_PROPOSAL-N, the next step is the same h1 handoff but scoped to the follow-up epic:
- **Storage:** `.git/.aura/handoff/{followup-epic-id}/architect-to-supervisor.md`
- The supervisor then creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks
- Original IMPORTANT/MINOR leaf tasks are adopted as dual-parent children of FOLLOWUP_SLICE-N
<!-- END GENERATED FROM aura schema -->
