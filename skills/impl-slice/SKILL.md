---
name: impl-slice
description: Vertical slice assignment and tracking
---

# Implementation Slice (Phase 9)

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:impl:slice` — Vertical slice assignment and tracking

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

**[impl-slice-full-specs]**
- Given: IMPL_PLAN complete
- When: assigning slices
- Then: create the slice tasks with full specs
- Should not: leave specs vague

**[impl-slice-dep-chain]**
- Given: slice assigned
- When: creating task
- Then: chain dependency to IMPL_PLAN: "${EPOCH_REC}" block "${IMPL_PLAN_ID_URI}" --by "${SLICE_ID_URI}"
- Should not: create orphan slices

**[impl-slice-track-status]**
- Given: worker starts
- When: tracking
- Then: update task to in_progress
- Should not: leave status as open

**[impl-slice-complete-label]**
- Given: slice complete
- When: verifying
- Then: record a completion note
- Should not: close the task prematurely

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

## Slice Structure

Each vertical slice contains:
- **slice_id**: A name for the work, not an internal code
- **slice_name**: Human-readable name
- **slice_spec**: Detailed implementation specification
- **slice_files**: Files owned by this slice

## Creating Slices

After supervisor decomposes the ratified plan:

```bash
# Create the first slice
SLICE_1_ID_URI=$("${EPOCH_REC}" create --kind task --title "<slice name>" --phase worker_slices \
  --body "---
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
- [ ] Production code path verified")
"${EPOCH_REC}" note "$SLICE_1_ID_URI" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Design: '{\"validation_checklist\":[\"Types defined\",\"Tests written (import production code)\",\"Implementation complete\",\"Wiring complete\",\"Production code path verified\"],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\"}],\"ratified_plan\":\"${RATIFIED_PLAN_ID_URI}\"}'; Requested role (not an assignment): worker-1"

"${EPOCH_REC}" block "${IMPL_PLAN_ID_URI}" --by "${SLICE_1_ID_URI}"
```

## Assigning Workers

```bash
"${EPOCH_REC}" label "${SLICE_1_ID_URI}" owner:"${REGISTERED_WORKER_ACTOR_1}"
"${EPOCH_REC}" label "${SLICE_2_ID_URI}" owner:"${REGISTERED_WORKER_ACTOR_2}"
"${EPOCH_REC}" label "${SLICE_3_ID_URI}" owner:"${REGISTERED_WORKER_ACTOR_3}"
```

## Tracking Progress

```bash
# Worker starts
"${EPOCH_REC}" status "${SLICE_ID_URI}" --set in_progress

# Check all slice status
"${EPOCH_REC}" query --phase worker_slices --status=open
"${EPOCH_REC}" query --phase worker_slices --status=in_progress

# Worker completes (add comment and label)
"${EPOCH_REC}" note "${SLICE_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "COMPLETE: All checklist items verified. Production code path working."
```

## Slice Dependencies

Slices can have dependencies on each other (sync points):

```bash
# the second slice depends on the first slice completing first
"${EPOCH_REC}" block "${SLICE_2_ID_URI}" --by "${SLICE_1_ID_URI}"
```

Minimize inter-slice dependencies when possible.

## Aggregation

The aggregation step waits for every slice to record completion before code review:

```bash
# Slices in this phase, including those still open
"${EPOCH_REC}" query --phase worker_slices
```

## Follow-up slices

Follow-up slices use the same structure and tracking, with additional fields:
- **Title:** name the work, for example `Add correlation`
- **Adopted leaf tasks:** User-DEFER'd UAT-item leaf tasks become dual-parent children (original severity group + follow-up slice)
- **Tracking:** `"${EPOCH_REC}" query --phase worker_slices` includes both ordinary slices and follow-up slices
<!-- END GENERATED FROM pasture schema -->
