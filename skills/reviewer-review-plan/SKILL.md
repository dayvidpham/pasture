---
name: reviewer-review-plan
description: Evaluate proposal against one axis (binary ACCEPT/REVISE)
---

# Review Plan

<!-- BEGIN GENERATED FROM pasture schema -->
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
pasture task show "${PROPOSAL_ID_URI}"
pasture task show "${URD_ID_URI}"   # Read URD for user requirements context
```

### Step 2: Apply Criteria

Apply end-user alignment criteria (check against URD requirements). Verify `validation_checklist` items are verifiable and BDD acceptance criteria are complete.

### Step 3: Create Review Task

```bash
REVIEW_ID_URI=$(pasture task create "PROPOSAL-1-REVIEW-A-1: <feature>" --phase review --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  proposal: "${PROPOSAL_ID_URI}"
  urd: "${URD_ID_URI}"
---
VOTE: <ACCEPT|REVISE> - <justification>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$REVIEW_ID_URI" pasture:p4-plan:s4-review
pasture task dep add "${PROPOSAL_ID_URI}" --blocked-by "${REVIEW_ID_URI}"
```

### Step 4: Add Vote Comment

```bash
# If accepting:
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "VOTE: ACCEPT - End-user impact clear. MVP scope appropriate. Checklist items verifiable."

# If requesting revision:
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "VOTE: REVISE - Missing: what happens if X fails? Suggestion: add error handling to checklist."
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
