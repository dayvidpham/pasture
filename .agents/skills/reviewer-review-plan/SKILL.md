---
name: reviewer-review-plan
description: Evaluate proposal against one axis (binary ACCEPT/REVISE)
---

# Review Plan

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:reviewer:review-plan` — Evaluate proposal against one axis (binary ACCEPT/REVISE)

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phase 4

**[rev-plan-alignment]**
- Given: plan assignment
- When: reviewing
- Then: apply end-user alignment criteria
- Should not: focus only on technical details

**[rev-plan-revise-actionable]**
- Given: issues found
- When: voting
- Then: vote REVISE with specific feedback
- Should not: vote REVISE without actionable suggestions

**[rev-plan-document]**
- Given: review complete
- When: documenting
- Then: add comment to Pasture task
- Should not: vote without written justification

**[rev-plan-binary-vote]**
- Given: plan review
- When: assessing
- Then: use ACCEPT/REVISE binary vote only
- Should not: create severity tree for plan reviews

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

Assigned to review a plan specification (Phase 4, `pasture:p4-plan:s4-review`).

## End-User Alignment Criteria

Ask these questions for every plan:

1. **Who are the end-users?**
2. **What would end-users want?**
3. **How would this affect them?**
4. **Are there implementation gaps?**
5. **Does MVP scope make sense?**
6. **Is validation checklist complete and correct?**

## Production Code Path Questions

When reviewing plans, explicitly ask:

1. **What are the production code paths?**
   - CLI commands: Entry points users will run
   - API endpoints: HTTP handlers, services
   - Background jobs: Daemon processes

2. **How will production code be tested?**
   - Do Layer 2 tests import the actual CLI/API?
   - Or do they test a separate test-only export? (anti-pattern)

3. **What needs to be wired together?**
   - Service instantiation with real dependencies?
   - CLI command registration?
   - Entry point hookup?

4. **Are implementation tasks explicit about production code?**
   - Does the plan include tasks to wire production code?
   - Or are they only testing isolated units?

**Red flag:** Plan shows "Layer 2: service_test.go" but no task for "wire service into CLI command"

**Green flag:** Plan shows "Layer 3: Wire cobra command with NewService(realDeps)"

## Steps



### Step 1: Read PROPOSAL-N and URD

```bash
"${EPOCH_REC}" show "${PROPOSAL_ID_URI}"
"${EPOCH_REC}" show "${URD_ID_URI}"   # Read URD for user requirements context
```

### Step 2: Apply Criteria

Apply end-user alignment criteria (check against URD requirements). Verify `validation_checklist` items are verifiable and BDD acceptance criteria are complete.

### Step 3: Create Review Task

```bash
REVIEW_ID_URI=$("${EPOCH_REC}" create --kind task --title "PROPOSAL-1-REVIEW-A-1: <feature>" --phase review \
  --body "---
references:
  proposal: "${PROPOSAL_ID_URI}"
  urd: "${URD_ID_URI}"
---
VOTE: <ACCEPT|REVISE> - <justification>")
"${EPOCH_REC}" label "$REVIEW_ID_URI" pasture:p4-plan:s4-review
"${EPOCH_REC}" block "${PROPOSAL_ID_URI}" --by "${REVIEW_ID_URI}"
```

### Step 4: Add Vote Comment

```bash
# If accepting:
"${EPOCH_REC}" note "${PROPOSAL_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
"${EPOCH_REC}" note "${PROPOSAL_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."
```

## Vote Options

| Vote | When |
|------|------|
| ACCEPT | All review criteria satisfied; no BLOCKER items |
| REVISE | BLOCKER issues found; must provide actionable feedback |

Binary only. No severity tree for plan reviews.

## Consensus

All 3 reviewers must vote ACCEPT for plan to be ratified.

## Follow-up Proposal Reviews (FOLLOWUP_PROPOSAL-N)

The same procedure applies when reviewing FOLLOWUP_PROPOSAL-N:
- **Task naming:** `FOLLOWUP_PROPOSAL-N-REVIEW-{axis}-{round}`
- Same binary ACCEPT/REVISE vote (no severity tree)
- Additionally verify that FOLLOWUP_PROPOSAL addresses the specific IMPORTANT/MINOR findings scoped in FOLLOWUP_URE/URD
<!-- END GENERATED FROM pasture schema -->
