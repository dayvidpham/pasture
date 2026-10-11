---
name: status
description: Project status and monitoring via Pasture queries
---

# Pasture Status

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:status` — Project status and monitoring via Pasture queries

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md)**

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

## Steps



### 1. Check for active plans

```bash
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p3-plan:s3-propose" --status=open
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p6-plan:s6-ratify" --status=open
```

### 2. Check implementation progress

```bash
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p8-impl:s8-plan" --status=open
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=in_progress
pasture task blocked --label="pasture:p9-impl:s9-slice"
pasture task list --namespace "$PASTURE_NAMESPACE" --label="pasture:p9-impl:s9-slice" --status=closed
```

### 3. Get project stats

```bash
pasture task list --namespace "$PASTURE_NAMESPACE" --format json | python3 -c 'import collections,json,sys; print(dict(collections.Counter(x["status"] for x in json.load(sys.stdin))))'
```

### 4. Report status

Summarize findings across plans, implementation, and blocked tasks in the output format below.

## Output Format

```
## Pasture Protocol Status

**Phase:** {Phase 1: Request | Phase 3: Propose | Phase 4: Review | Phase 6: Ratified | Phase 9: Implementation}
**Active Plan:** {task-id or "None"}

### Plans
- ["${PROPOSAL_ID_URI}"] Status: {open|closed}
- [ratified-id] Status: {open|closed}

### Implementation Progress
- IMPL_PLAN: {task-id}
- Layer 1: {N}/{M} complete
- Layer 2: {N}/{M} complete (blocked: {count})

### Blocked Tasks
- {task-id}: {blocker reason}

### Recent Activity
pasture task list --namespace "$PASTURE_NAMESPACE"
```

## Quick Status

```bash
pasture task list --namespace "$PASTURE_NAMESPACE" --format json | python3 -c 'import collections,json,sys; print(dict(collections.Counter(x["status"] for x in json.load(sys.stdin))))'
pasture task ready
pasture task blocked
```
<!-- END GENERATED FROM pasture schema -->
