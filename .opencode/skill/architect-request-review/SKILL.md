---
name: architect-request-review
description: Spawn 3 axis-specific reviewers (A/B/C)
---

# Architect: Request Review

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:architect:request-review` — Spawn 3 axis-specific reviewers (A/B/C)

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-4-plan-review)** <- Phase 4

**[arch-review-spawn-3-axes]**
- Given: plan ready
- When: requesting review
- Then: spawn 3 axis-specific reviewers (A=Correctness, B=Test quality, C=Elegance)
- Should not: spawn reviewers without axis assignment

**[arch-review-provide-context]**
- Given: reviewers
- When: assigning
- Then: provide Pasture task ID and context
- Should not: expect reviewers to search

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

Plan draft complete, ready for review.

## REVIEW Naming

Reviews are named `PROPOSAL-N-REVIEW-{axis}-{round}` where:
- N = proposal number (matches PROPOSAL-N)
- axis = reviewer criteria axis (A, B, or C)
- round = review round number (1, 2, ...)

### Review Axes

| Axis | Focus | Key Questions |
|------|-------|---------------|
| **A** | Correctness (spirit and technicality) | Does it faithfully serve the user? Are technical decisions consistent with rationale? |
| **B** | Test quality | Integration over unit? SUT not mocked? Shared fixtures? Assert outcomes? |
| **C** | Elegance and complexity matching | Right API? Not over/under-engineered? Complexity proportional to problem? |

## Steps

1. Verify PROPOSAL-N task is complete with all sections
2. Spawn three reviewers with the task ID and URD reference:

```
Task(description: "Reviewer A: correctness", prompt: "Review PROPOSAL-1 task ${TASK_ID_URI}. URD: ${URD_ID_URI} (read for requirements context). You are Reviewer A (Correctness). Focus: Does it faithfully serve the user? Are technical decisions consistent with rationale? Create review task titled PROPOSAL-1-REVIEW-A-1...", subagent_type: "general-purpose")
Task(description: "Reviewer B: test quality", prompt: "Review PROPOSAL-1 task ${TASK_ID_URI}. URD: ${URD_ID_URI} (read for requirements context). You are Reviewer B (Test quality). Focus: Integration over unit? SUT not mocked? Shared fixtures? Assert outcomes? Create review task titled PROPOSAL-1-REVIEW-B-1...", subagent_type: "general-purpose")
Task(description: "Reviewer C: elegance", prompt: "Review PROPOSAL-1 task ${TASK_ID_URI}. URD: ${URD_ID_URI} (read for requirements context). You are Reviewer C (Elegance). Focus: Right API? Not over/under-engineered? Complexity proportional to problem? Create review task titled PROPOSAL-1-REVIEW-C-1...", subagent_type: "general-purpose")
```

3. Wait for all 3 reviewers to vote ACCEPT

## Consensus

**All 3 reviewers must vote ACCEPT.** Max revision rounds until consensus.

## Checking Reviews

```bash
pasture task show "${PROPOSAL_ID_URI}"
pasture task comments "${PROPOSAL_ID_URI}"
```

## Coordination

```bash
# Add comment to notify that review is ready
pasture task comment add --author pasture-system--00000000-0000-0000-0000-000000000000 "${PROPOSAL_ID_URI}" "Review requested — 3 reviewers spawned"

# Check for review votes
pasture task comments "${PROPOSAL_ID_URI}"
```

## Follow-up Proposal Reviews (FOLLOWUP_PROPOSAL-N)

For FOLLOWUP_PROPOSAL-N reviews, use the same procedure:
- **Review task naming:** `FOLLOWUP_PROPOSAL-N-REVIEW-{axis}-{round}`
- Same 3 axes (A/B/C), same binary ACCEPT/REVISE vote
- No severity tree for plan reviews (same as original plan reviews)
- Reviewers should also verify that FOLLOWUP_PROPOSAL addresses the specific IMPORTANT/MINOR findings scoped in FOLLOWUP_URE/URD
<!-- END GENERATED FROM pasture schema -->
