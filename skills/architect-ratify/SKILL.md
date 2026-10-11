---
name: architect-ratify
description: Ratify proposal, mark old proposals pasture:superseded
---

# Architect: Ratify Plan

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:architect:ratify` — Ratify proposal, mark old proposals pasture:superseded

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-6-ratification)** <- Phase 6

**[arch-ratify-all-accept]**
- Given: all 3 reviewers voted ACCEPT
- When: ratifying
- Then: add `pasture:p6-plan:s6-ratify` label to PROPOSAL-N
- Should not: ratify with any REVISE votes outstanding

**[arch-ratify-audit-trail]**
- Given: ratification
- When: documenting
- Then: add comment with reviewer sign-offs and UAT reference
- Should not: ratify without audit trail

**[arch-ratify-supersede-old]**
- Given: previous proposals exist
- When: ratifying new version
- Then: mark old proposals as `pasture:superseded`
- Should not: leave old proposals without superseded marking

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

All 3 reviewers have voted ACCEPT on PROPOSAL-N and user has approved via UAT.

## Consensus Requirement

**All 3 reviewers must vote ACCEPT.** If any reviewer votes REVISE:
1. Architect creates PROPOSAL-N+1 addressing feedback
2. Marks PROPOSAL-N as `pasture:superseded`
3. Reviewers re-review PROPOSAL-N+1
4. Repeat until all ACCEPT

## Steps



### Step 1: Check all reviews

```bash
pasture task show "${PROPOSAL_ID_URI}"
pasture task comments "${PROPOSAL_ID_URI}"
```

### Step 2: Verify all 3 votes are ACCEPT

Confirm each of the three review tasks (Reviewer A, B, C) has a VOTE: ACCEPT comment before proceeding.

### Step 3: Add ratify label to PROPOSAL-N

Do NOT create a new task — add label to the existing proposal:
```bash
pasture task label add "${PROPOSAL_ID_URI}" pasture:p6-plan:s6-ratify
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "RATIFIED: All 3 reviewers ACCEPT, UAT passed ("${UAT_ID_URI}")"
```

### Step 4: Mark all previous proposals as superseded

```bash
pasture task label add "${OLD_PROPOSAL_ID_URI}" pasture:superseded
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${OLD_PROPOSAL_ID_URI}" "Superseded by PROPOSAL-N ("${RATIFIED_PROPOSAL_ID_URI}")"
```

### Step 5: Update URD with ratification

```bash
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${URD_ID_URI}" "Ratified: scope confirmed. Ratified proposal: ${RATIFIED_PROPOSAL_ID_URI}"
```

## Next Steps

After ratifying PROPOSAL-N:
1. **Prepare handoff** — Run `/pasture:architect-handoff` to create handoff document and spawn supervisor

**IMPORTANT:** Do NOT start implementation yourself. The architect's role ends at handoff. Implementation is handled by the supervisor and workers spawned during handoff.

## Follow-up Proposals (FOLLOWUP_PROPOSAL-N)

When ratifying a FOLLOWUP_PROPOSAL-N, the next step is the same h1 handoff but scoped to the follow-up epic:
- **Storage:** the follow-up handoff is authored in its HANDOFF Pasture task body (no filesystem path)
- The supervisor then creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks
- The follow-up scope comes from the user-DEFER'd UAT items the FOLLOWUP epic was created from
<!-- END GENERATED FROM pasture schema -->
