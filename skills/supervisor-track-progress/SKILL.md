---
name: supervisor-track-progress
description: Monitor worker status via Pasture
---

# Supervisor: Track Progress

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:supervisor:track-progress` — Monitor worker status via Pasture

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[sup-track-poll-rate]**
- Given: workers running
- When: monitoring
- Then: check Pasture status at natural intervals (when a worker signals completion or blocker)
- Should not: poll aggressively or busy-wait in a tight loop

**[sup-track-partial-commit]**
- Given: worker complete
- When: all slices for a phase are done
- Then: proceed to code review or commit
- Should not: commit partial work — wait for all slices in the layer to complete

**[sup-track-resolve-blockers]**
- Given: worker blocked
- When: handling
- Then: resolve or reassign immediately
- Should not: leave workers waiting on a blocker without action

**[sup-track-urd-source-of-truth]**
- Given: requirements question arises
- When: resolving
- Then: consult the URD (`pasture task show "${URD_ID_URI}"`) as the single source of truth
- Should not: guess at user intent without checking the URD first

**[sup-track-severity-awareness]**
- Given: all slices complete
- When: transitioning to review
- Then: check for BLOCKER resolution tracking in the review severity groups
- Should not: skip severity awareness when moving to Phase 10

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

Workers spawned and running — monitoring for completions and blockers until all slices reach `closed` or a phase transition is warranted.

## Pasture Status Queries

```bash
# Check all implementation slices
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=in_progress

# Check for blocked slices
pasture task blocked --label="pasture:p9-impl:s9-slice"

# Check specific task
pasture task show "${TASK_ID_URI}"

# Check completed slices
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=closed

# Check BLOCKER severity groups (during/after review)
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:severity:blocker" --status=open

# Check follow-up epic
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:epic-followup"
```

## Tracking via Pasture

All coordination happens through Pasture task state and authored comments:

```bash
# Check for task updates
pasture task show "${TASK_ID_URI}"

# Review comments for status updates
pasture task comments "${TASK_ID_URI}"

# Add coordination notes
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "All slices complete — proceeding to Phase 10 (code review)"
```

## Status Patterns

| Status | Action |
|--------|--------|
| `closed` | Mark slice progress, check if all slices complete |
| Open blocked_by child | Review `pasture task show "${ID_URI}"` for blocker details, resolve or reassign |
| `in_progress` | Worker is actively working |

## Severity Awareness (Phase 10)

When tracking review progress, monitor severity groups:

| Severity | Blocks Slice? | Action |
|----------|---------------|--------|
| BLOCKER | Yes | Must reach 0 before wave close (dual-parent: also blocks the slice) |
| IMPORTANT | No (not via dual-parent) | Must reach 0 before wave close (never routed to FOLLOWUP) |
| MINOR | No (not via dual-parent) | Must reach 0 before wave close (never routed to FOLLOWUP) |

## Follow-up Lifecycle Tracking

```bash
# Track follow-up lifecycle progress
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:epic-followup"
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p2-user:s2_1-elicit" --status=open   # FOLLOWUP_URE
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p3-plan:s3-propose" --status=open     # FOLLOWUP_PROPOSAL
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=in_progress  # FOLLOWUP_SLICE in progress
```
<!-- END GENERATED FROM pasture schema -->
