---
name: impl-slice
description: Vertical slice assignment and tracking
---

# Implementation Slice (Phase 9)

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:impl:slice` — Vertical slice assignment and tracking

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[impl-slice-full-specs]**
- Given: IMPL_PLAN complete
- When: assigning slices
- Then: create SLICE-N tasks with full specs
- Should not: leave specs vague

**[impl-slice-dep-chain]**
- Given: slice assigned
- When: creating task
- Then: chain dependency to IMPL_PLAN: pasture task dep add "${IMPL_PLAN_ID_URI}" --blocked-by "${SLICE_ID_URI}"
- Should not: create orphan slices

**[impl-slice-track-status]**
- Given: worker starts
- When: tracking
- Then: update task to in_progress
- Should not: leave status as open

**[impl-slice-complete-label]**
- Given: slice complete
- When: verifying
- Then: add completion label and comments
- Should not: close the task prematurely

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

## Slice Structure

Each vertical slice contains:
- **slice_id**: Identifier (SLICE-1, SLICE-2, SLICE-3, ...)
- **slice_name**: Human-readable name
- **slice_spec**: Detailed implementation specification
- **slice_files**: Files owned by this slice

## Creating Slices

After supervisor decomposes the ratified plan:

```bash
# Create SLICE-1
SLICE_1_ID_URI=$(pasture task create "SLICE-1: <slice name>" --phase worker_slices --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  impl_plan: "${IMPL_PLAN_ID_URI}"
  urd: "${URD_ID_URI}"
---
## Specification
<detailed implementation spec>

## Files Owned
<list of files this slice owns>

## Acceptance Criteria
<criteria from ratified plan>

## Validation Checklist
- [ ] Types defined
- [ ] Tests written (import production code)
- [ ] Implementation complete
- [ ] Wiring complete
- [ ] Production code path verified" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$SLICE_1_ID_URI" pasture:p9-impl:s9-slice
pasture task update "$SLICE_1_ID_URI" --notes "Design: '{\"validation_checklist\":[\"Types defined\",\"Tests written (import production code)\",\"Implementation complete\",\"Wiring complete\",\"Production code path verified\"],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\"}],\"ratified_plan\":\"${RATIFIED_PLAN_ID_URI}\"}'; Requested role (not an assignment): worker-1"

pasture task dep add "${IMPL_PLAN_ID_URI}" --blocked-by "${SLICE_1_ID_URI}"
```

## Assigning Workers

```bash
pasture task assignment transfer "${SLICE_1_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_1_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_1}"
pasture task assignment transfer "${SLICE_2_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_2_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_2}"
pasture task assignment transfer "${SLICE_3_ID_URI}" --slot owner-responsibility --assignment "${SUCCESSOR_ASSIGNMENT_3_ID}" --actor pasture-system--00000000-0000-0000-0000-000000000000 --occupant "${REGISTERED_WORKER_ACTOR_3}"
```

## Tracking Progress

```bash
# Worker starts
pasture task update "${SLICE_ID_URI}" --status in_progress

# Check all slice status
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=open
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=in_progress

# Worker completes (add comment and label)
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${SLICE_ID_URI}" "COMPLETE: All checklist items verified. Production code path working."
pasture task label add "${SLICE_ID_URI}" pasture:p9-impl:slice-complete
```

## Slice Dependencies

Slices can have dependencies on each other (sync points):

```bash
# SLICE-2 depends on SLICE-1 completing first
pasture task dep add "${SLICE_2_ID_URI}" --blocked-by "${SLICE_1_ID_URI}"
```

Minimize inter-slice dependencies when possible.

## Aggregation

The aggregation step waits for all slices to complete before code review:

```bash
# Check if all slices have complete label
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:slice-complete"

# Compare to total slices
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice"
```

## Follow-up Slices (FOLLOWUP_SLICE-N)

Follow-up slices use the same structure and tracking, with additional fields:
- **Title prefix:** `FOLLOWUP_SLICE-N:` (e.g., `FOLLOWUP_SLICE-1: Add "${REQUEST_ID_URI}" correlation`)
- **Adopted leaf tasks:** User-DEFER'd UAT-item leaf tasks become dual-parent children (original severity group + follow-up slice)
- **Tracking:** Same `pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice"` queries include both regular and follow-up slices
<!-- END GENERATED FROM pasture schema -->
