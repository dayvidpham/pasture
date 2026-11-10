---
name: worker-blocked
description: Report a blocker to supervisor via Pasture
---

# Worker: Handle Blockers

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
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

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls one derivation function for the header and for every write that has no pin; it handles git@, ssh://, git://, http://, https:// and user@host:path. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

Cannot proceed due to missing dependency, unclear requirement, or need changes in another file.

## Steps

1. Identify what's blocking (missing type, unclear requirement, file dependency)

2. Update Pasture task:
   ```bash
   "${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body="Blocked: <reason>. Missing: <dependency or clarification needed>"
   ```

3. Document the blocker in the task:
   ```bash
   "${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "BLOCKED: <reason>. Need: <dependency or clarification>"
   ```

4. Wait for supervisor or dependency resolution — check with `"${EPOCH_REC}" show "${TASK_ID_URI}"`

## Common Blockers

- Missing type definition from another file
- Unclear requirement in acceptance_criteria
- Need interface defined in dependent file
- Conflicting constraints in validation_checklist
<!-- END GENERATED FROM pasture schema -->
