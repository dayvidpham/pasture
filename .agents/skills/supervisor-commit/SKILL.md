---
name: supervisor-commit
description: Atomic commit per completed layer/slice
---

# Supervisor: Commit

<!-- BEGIN GENERATED FROM pasture schema -->
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
pasture task close "${TASK_A_URI}" --reason="Committed in <commit-hash>"
pasture task close "${TASK_B_URI}" --reason="Committed in <commit-hash>"
```

## Update IMPL_PLAN

```bash
pasture task update "${IMPL_PLAN_ID_URI}" --notes="SLICE-N complete: "${TASK_A_URI}", ${TASK_B_URI}"
```

## Follow-up Commits

For follow-up slices, add `Followup-Epic:` to the commit message trailer:

```
feat|fix(scope): Description (follow-up)

Files: file1.go, file2.go
Task: ${TASK_A_URI} (FOLLOWUP_SLICE-1)
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
