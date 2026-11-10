---
name: status
description: Project status and monitoring via Pasture queries
---

# Pasture Status

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:status` — Project status and monitoring via Pasture queries

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md)**

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

Set PASTURE_NAMESPACE to the repository's canonical namespace URI, for example https://github.com/dayvidpham/pasture. The adapter reads it when set, otherwise the header's `namespace:`, and passes `--namespace` itself. Queries are scoped to that namespace; pass `--all-namespaces` to opt out. When neither source is set, those reads are unscoped. Creation still falls back to the git-remote-derived namespace, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami; when neither the header nor PASTURE_AUTHOR names one, it lists the registered agents. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## Steps



### 1. Check for active plans

```bash
"${EPOCH_REC}" query --label="pasture:p3-plan:s3-propose" --status=open
"${EPOCH_REC}" query --label="pasture:p6-plan:s6-ratify" --status=open
```

### 2. Check implementation progress

```bash
"${EPOCH_REC}" query --label="pasture:p8-impl:s8-plan" --status=open
"${EPOCH_REC}" query --label="pasture:p9-impl:s9-slice" --status=in_progress
"${EPOCH_REC}" query --blocked --label="pasture:p9-impl:s9-slice"
"${EPOCH_REC}" query --label="pasture:p9-impl:s9-slice" --status=closed
```

### 3. Get project stats

```bash
"${EPOCH_REC}" query | python3 -c 'import collections,json,sys; print(dict(collections.Counter(x["status"] for x in json.load(sys.stdin))))'
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
"${EPOCH_REC}" query
```

## Quick Status

```bash
"${EPOCH_REC}" query | python3 -c 'import collections,json,sys; print(dict(collections.Counter(x["status"] for x in json.load(sys.stdin))))'
"${EPOCH_REC}" ready
"${EPOCH_REC}" query --blocked
```
<!-- END GENERATED FROM pasture schema -->
