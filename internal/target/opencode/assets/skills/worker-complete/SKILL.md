---
name: worker-complete
description: Signal slice completion after quality gates pass
---

# Worker: Signal Completion

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:worker:complete` — Signal slice completion after quality gates pass

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[wcomp-quality-gates]**
- Given: implementation done
- When: signaling
- Then: verify the project's quality gates pass
- Should not: report done with failing checks

**[wcomp-checklist]**
- Given: validation_checklist
- When: completing
- Then: confirm all items satisfied
- Should not: complete with unchecked items

**[wcomp-task-update]**
- Given: completion
- When: reporting
- Then: record completion evidence without closing the task
- Should not: omit Pasture update

**[wcomp-handoff-doc]**
- Given: completion
- When: handing off to reviewer
- Then: author the worker→reviewer handoff in the Pasture task body (the slice/handoff task body IS the handoff)
- Should not: skip handoff for actor transitions

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

Implementation complete and all checks pass.

## Steps

1. Run the project's quality gates (type checking + tests) - must pass
2. **Verify production code path via code inspection:**
   - [ ] Tests import production code (not test-only export)
   - [ ] No dual-export anti-pattern
   - [ ] No TODO placeholders in production code
   - [ ] Service wired with real dependencies (not mocks in production)
3. Verify all validation_checklist items satisfied:
   ```bash
   "${EPOCH_REC}" show "${TASK_ID_URI}" --notes  # Review checklist items
   ```
4. Update Pasture task:
   ```bash
   "${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Implementation complete; awaiting independent review and supervisor closure."
   "${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body="Implementation complete. Production code verified working."
   ```
5. Author the worker→reviewer handoff in the Pasture task body (see template below)

## Handoff Template (Worker → Reviewer)



### Storage

Authored in the Pasture task body — the slice (or a dedicated handoff) task body IS the handoff. No filesystem path.

### Template

```markdown
# Handoff: Worker <N> → Reviewer

## Context
- Request: ${REQUEST_ID_URI}
- URD: ${URD_ID_URI}
- Slice: SLICE-<N>
- Task ID: ${SLICE_TASK_ID_URI}

## What Was Implemented
- Production Code Path: <what end users run>
- Files Changed: <list of files>

## Key Decisions
- <decision 1>: <rationale>
- <decision 2>: <rationale>

## Quality Gates
- Type checking: PASS
- Tests: PASS
- Production code inspection: PASS (no TODOs, real deps wired)

## Areas of Concern
- <any areas the reviewer should pay special attention to>
```

## Report Completion

```bash
# Report completion; only the supervisor closes after independent review
"${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Implementation complete. Quality gates pass. Production code verified."
```

## Follow-up Slice Completion (FOLLOWUP_SLICE-N)

When completing a FOLLOWUP_SLICE-N, additionally report which original leaf tasks were resolved:

```bash
"${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Implementation complete. Resolved leaf tasks: "${LEAF_TASK_ID_1_URI}", ${LEAF_TASK_ID_2_URI}"
```

The handoff to the reviewer (h4) must include which original leaf tasks were resolved so reviewers can verify.
<!-- END GENERATED FROM pasture schema -->
