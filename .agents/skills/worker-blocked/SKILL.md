---
name: worker-blocked
description: Report a blocker to supervisor via Pasture
---

# Worker: Handle Blockers

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:worker:blocked` — Report a blocker to supervisor via Pasture

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[wblk-update-status]**
- Given: a blocker
- When: reporting
- Then: update Pasture task status and document details
- Should not: guess or work around the blocker

**[wblk-wait-for-response]**
- Given: blocker sent
- When: waiting
- Then: wait for supervisor response
- Should not: continue with incomplete info

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

Cannot proceed due to missing dependency, unclear requirement, or need changes in another file.

## Steps

1. Identify what's blocking (missing type, unclear requirement, file dependency)

2. Update Pasture task:
   ```bash
   pasture task update "${TASK_ID_URI}" --notes="Blocked: <reason>. Missing: <dependency or clarification needed>"
   ```

3. Document the blocker in the task:
   ```bash
   pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "BLOCKED: <reason>. Need: <dependency or clarification>"
   ```

4. Wait for supervisor or dependency resolution — check with `pasture task show "${TASK_ID_URI}"`

## Common Blockers

- Missing type definition from another file
- Unclear requirement in acceptance_criteria
- Need interface defined in dependent file
- Conflicting constraints in validation_checklist
<!-- END GENERATED FROM pasture schema -->
