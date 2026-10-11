---
name: reviewer-vote
description: Cast ACCEPT or REVISE vote (binary only)
---

# Cast Review Vote

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:reviewer:vote` — Cast ACCEPT or REVISE vote (binary only)

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phases 4 + 10

**[rev-vote-criteria]**
- Given: review complete
- When: voting
- Then: choose based on end-user alignment criteria
- Should not: vote without applying all criteria

**[rev-vote-rationale]**
- Given: vote to record
- When: recording
- Then: add comment to Pasture task with justification
- Should not: vote without written rationale

**[rev-vote-severity-tree]**
- Given: code review
- When: voting
- Then: be aware that findings are tracked via severity tree (BLOCKER/IMPORTANT/MINOR)
- Should not: duplicate severity findings in vote comment

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

Review complete and ready to cast a binary ACCEPT or REVISE vote.

## Vote Options

| Vote | When |
|------|------|
| ACCEPT | All review criteria satisfied; no BLOCKER items |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No intermediate levels.

## Plan Review vs Code Review

- **Plan review (Phase 4, `pasture:p4-plan:s4-review`):** ACCEPT/REVISE only. No severity tree.
- **Code review (Phase 10, `pasture:p10-impl:s10-review`):** ACCEPT/REVISE vote. Findings tracked via severity tree (3 groups: BLOCKER, IMPORTANT, MINOR created per round).

## Consensus

**All 3 reviewers must vote ACCEPT** for plan to be ratified or code to be approved.

## Adding Vote to Pasture

```bash
# If accepting:
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."
```

## Report Vote

Votes are recorded via pasture comments (see "Adding Vote to Pasture" above). No separate messaging step is needed.
<!-- END GENERATED FROM pasture schema -->
