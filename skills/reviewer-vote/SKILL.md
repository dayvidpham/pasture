# Cast Review Vote

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:reviewer:vote` — Cast ACCEPT or REVISE vote (binary only)

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phases 4 + 10

**[rev-vote-criteria]**
- Given: review complete
- When: voting
- Then: choose based on end-user alignment criteria
- Should not: vote without applying all criteria

**[rev-vote-rationale]**
- Given: vote to record
- When: recording
- Then: add comment to Beads task with justification
- Should not: vote without written rationale

**[rev-vote-severity-tree]**
- Given: code review
- When: voting
- Then: be aware that findings are tracked via severity tree (BLOCKER/IMPORTANT/MINOR)
- Should not: duplicate severity findings in vote comment

## When to Use

Review complete and ready to cast a binary ACCEPT or REVISE vote.

## Vote Options

| Vote | When |
|------|------|
| ACCEPT | All review criteria satisfied; no BLOCKER items |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No intermediate levels.

## Plan Review vs Code Review

- **Plan review (Phase 4, `aura:p4-plan:s4-review`):** ACCEPT/REVISE only. No severity tree.
- **Code review (Phase 10, `aura:p10-impl:s10-review`):** ACCEPT/REVISE vote. Findings tracked via severity tree (3 groups: BLOCKER, IMPORTANT, MINOR created per round).

## Consensus

**All 3 reviewers must vote ACCEPT** for plan to be ratified or code to be approved.

## Adding Vote to Beads

```bash
# If accepting:
bd comments add <task-id> "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
bd comments add <task-id> "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."
```

## Report Vote

Votes are recorded via beads comments (see "Adding Vote to Beads" above). No separate messaging step is needed.
<!-- END GENERATED FROM aura schema -->
