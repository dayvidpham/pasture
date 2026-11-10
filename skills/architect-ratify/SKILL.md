---
name: architect-ratify
description: Ratify proposal, mark old proposals pasture:superseded
---

# Architect: Ratify Plan

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
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

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

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
"${EPOCH_REC}" show "${PROPOSAL_ID_URI}"
"${EPOCH_REC}" show "${PROPOSAL_ID_URI}" --notes
```

### Step 2: Verify all 3 votes are ACCEPT

Confirm each of the three review tasks (Reviewer A, B, C) has a VOTE: ACCEPT comment before proceeding.

### Step 3: Add ratify label to PROPOSAL-N

Do NOT create a new task — add label to the existing proposal:
```bash
"${EPOCH_REC}" label "${PROPOSAL_ID_URI}" pasture:p6-plan:s6-ratify
"${EPOCH_REC}" note "${PROPOSAL_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "RATIFIED: All 3 reviewers ACCEPT, UAT passed ("${UAT_ID_URI}")"
```

### Step 4: Mark all previous proposals as superseded

```bash
"${EPOCH_REC}" label "${OLD_PROPOSAL_ID_URI}" pasture:superseded
"${EPOCH_REC}" note "${OLD_PROPOSAL_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Superseded by PROPOSAL-N ("${RATIFIED_PROPOSAL_ID_URI}")"
```

### Step 5: Update URD with ratification

```bash
"${EPOCH_REC}" note "${URD_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Ratified: scope confirmed. Ratified proposal: ${RATIFIED_PROPOSAL_ID_URI}"
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
