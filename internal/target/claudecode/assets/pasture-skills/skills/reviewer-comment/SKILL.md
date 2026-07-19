---
name: reviewer-comment
description: Leave structured review comment via Beads
---

# Leave Structured Review Comment

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:reviewer:comment` — Leave structured review comment via Beads

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phases 4 + 10

**[rev-comment-structured]**
- Given: findings to document
- When: documenting
- Then: use structured format with severity levels
- Should not: leave unstructured feedback

**[rev-comment-beads]**
- Given: comment to create
- When: creating
- Then: add via `bd comments add`
- Should not: create standalone files for review comments

## When to Use

Documenting review findings for the permanent record. Applies to both plan reviews (Phase 4) and code reviews (Phase 10).

## Steps

1. Identify the task to comment on (`bd show <task-id>`)
2. Categorize findings by severity
3. Add structured comment via Beads

## Comment via Beads

```bash
# Plan review comment (no severity tree)
bd comments add <proposal-id> "VOTE: ACCEPT - End-user alignment confirmed. MVP scope achievable."

# Code review comment (with severity references)
bd comments add <review-id> "VOTE: REVISE - 1 BLOCKER found (see severity tree). Suggestion: fix type error in auth middleware."
```

## Format

```markdown
VOTE: {ACCEPT | REVISE}

## Findings

### BLOCKER Issues
{list or "None"}

### IMPORTANT Issues
{list or "None"}

### MINOR Issues
{list or "None"}

## Conclusion
{assessment and next steps}
```

## Severity Vocabulary

| Severity | When to Use | Blocks? |
|----------|-------------|---------|
| BLOCKER | Security, type errors, test failures, broken production code paths | Yes (code review only) |
| IMPORTANT | Performance, missing validation, architectural concerns | No (follow-up epic) |
| MINOR | Style, optional optimizations, naming improvements | No (follow-up epic) |

## Plan Review vs Code Review

- **Plan review (Phase 4, `pasture:p4-plan:s4-review`):** ACCEPT/REVISE only. No severity tree. Findings are described inline in the vote comment.
- **Code review (Phase 10, `pasture:p10-impl:s10-review`):** ACCEPT/REVISE vote + full severity tree with EAGER creation (3 groups per round). Findings are tracked as child tasks of severity groups.
<!-- END GENERATED FROM pasture schema -->
