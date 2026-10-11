---
name: reviewer-comment
description: Leave structured review comment via Pasture
---

# Leave Structured Review Comment

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:reviewer:comment` — Leave structured review comment via Pasture

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phases 4 + 10

**[rev-comment-structured]**
- Given: findings to document
- When: documenting
- Then: use structured format with severity levels
- Should not: leave unstructured feedback

**[rev-comment-task]**
- Given: comment to create
- When: creating
- Then: add via `pasture task comment add`
- Should not: create standalone files for review comments

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

Documenting review findings for the permanent record. Applies to both plan reviews (Phase 4) and code reviews (Phase 10).

## Steps

1. Identify the task to comment on (`pasture task show "${TASK_ID_URI}"`)
2. Categorize findings by severity
3. Add structured comment via Pasture

## Comment via Pasture

```bash
# Plan review comment (no severity tree)
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "VOTE: ACCEPT - End-user alignment confirmed. MVP scope achievable."

# Code review comment (with severity references)
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${REVIEW_ID_URI}" "VOTE: REVISE - 1 BLOCKER found (see severity tree). Suggestion: fix type error in auth middleware."
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
| IMPORTANT | Performance, missing validation, architectural concerns | Must reach 0 before review wave closes |
| MINOR | Style, optional optimizations, naming improvements | Must reach 0 before review wave closes |

## Plan Review vs Code Review

- **Plan review (Phase 4, `pasture:p4-plan:s4-review`):** ACCEPT/REVISE only. No severity tree. Findings are described inline in the vote comment.
- **Code review (Phase 10, `pasture:p10-impl:s10-review`):** ACCEPT/REVISE vote + full severity tree with EAGER creation (3 groups per round). Findings are tracked as child tasks of severity groups.
<!-- END GENERATED FROM pasture schema -->
