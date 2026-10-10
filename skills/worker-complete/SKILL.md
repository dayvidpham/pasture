---
name: worker-complete
description: Signal slice completion after quality gates pass
---

# Worker: Signal Completion

<!-- BEGIN GENERATED FROM pasture schema -->
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
   pasture task show "${TASK_ID_URI}"  # Review checklist items
   ```
4. Update Pasture task:
   ```bash
   pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "Implementation complete; awaiting independent review and supervisor closure."
   pasture task update "${TASK_ID_URI}" --notes="Implementation complete. Production code verified working."
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
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "Implementation complete. Quality gates pass. Production code verified."
```

## Follow-up Slice Completion (FOLLOWUP_SLICE-N)

When completing a FOLLOWUP_SLICE-N, additionally report which original leaf tasks were resolved:

```bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${TASK_ID_URI}" "Implementation complete. Resolved leaf tasks: "${LEAF_TASK_ID_1_URI}", ${LEAF_TASK_ID_2_URI}"
```

The handoff to the reviewer (h4) must include which original leaf tasks were resolved so reviewers can verify.
<!-- END GENERATED FROM pasture schema -->
