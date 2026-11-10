---
name: supervisor-commit
description: Atomic commit per completed layer/slice
---

# Supervisor: Commit

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:supervisor:commit` — Atomic commit per completed layer/slice

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-12-landing)** <- Phase 12

**[sup-commit-gates-first]**
- Given: all files ready
- When: committing
- Then: run quality gates (type checking + tests) first — must pass before staging or committing
- Should not: commit without quality gates passing

**[sup-commit-message-format]**
- Given: commit message
- When: formatting
- Then: reference Pasture task IDs in the trailer (Task: ${TASK_A_URI}, ${TASK_B_URI})
- Should not: use vague messages without task references

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. A ledger with no origin uses file:// of the git work-tree root, so a subdirectory does not split the namespace. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

All workers for a review wave have completed successfully — quality gates pass, Pasture tasks updated, IMPL_PLAN ready for progress note.

## Steps

1. Run quality gates (type checking + tests) — must pass
2. Stage changed files
3. Create commit with format below
4. Close Pasture tasks
5. Update IMPL_PLAN progress

## Commit Format

```
feat|fix|docs|refactor(scope): Description

Files: file1.go, file2.go
Task: ${TASK_A_URI}, ${TASK_B_URI}
Ratified-Plan: ${RATIFIED_PLAN_ID_URI}

Co-Authored-By: Claude <noreply@anthropic.com>
```

## Close Pasture Tasks

```bash
"${EPOCH_REC}" close "${TASK_A_URI}" --reason="Committed in <commit-hash>"
"${EPOCH_REC}" close "${TASK_B_URI}" --reason="Committed in <commit-hash>"
```

## Update IMPL_PLAN

```bash
"${EPOCH_REC}" note "${IMPL_PLAN_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body="Slice complete: "${TASK_A_URI}", ${TASK_B_URI}"
```

## Follow-up Commits

For follow-up slices, add `Followup-Epic:` to the commit message trailer:

```
feat|fix(scope): Description (follow-up)

Files: file1.go, file2.go
Task: ${TASK_A_URI} (the first follow-up slice)
Followup-Epic: ${TASK_B_URI}
Ratified-Plan: ${TASK_C_URI} (FOLLOWUP_PROPOSAL-1)

Co-Authored-By: Claude <noreply@anthropic.com>
```

## Commands

```bash
# Set these to the reviewed canonical source and direct test paths for this slice.
SLICE_SOURCE_FILE=path/to/source.go
SLICE_TEST_FILE=path/to/source_test.go
git add "$SLICE_SOURCE_FILE" "$SLICE_TEST_FILE"
git agent-commit -m "..."
```
<!-- END GENERATED FROM pasture schema -->
