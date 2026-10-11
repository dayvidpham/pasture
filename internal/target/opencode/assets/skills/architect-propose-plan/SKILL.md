---
name: architect-propose-plan
description: Create PROPOSAL-N task with full technical plan
---

# Architect: Propose Plan

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:architect:propose-plan` — Create PROPOSAL-N task with full technical plan

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-3-proposal-n)** <- Phase 3

**[arch-propose-bdd-format]**
- Given: feature request
- When: proposing
- Then: use BDD Given/When/Then format with acceptance criteria
- Should not: write vague requirements

**[arch-propose-checklist-required]**
- Given: plan
- When: creating task
- Then: include validation_checklist and tradeoffs in design field
- Should not: leave checklist empty

**[arch-propose-revision-history]**
- Given: existing plan
- When: revising
- Then: create PROPOSAL-N+1 task and mark old as `pasture:superseded`
- Should not: lose history

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

Starting new feature design; creating formal plan for review.

## PROPOSAL-N Naming

Proposals are numbered incrementally: PROPOSAL-1, PROPOSAL-2, etc. Each revision increments N. Old proposals are marked `pasture:superseded` with a comment explaining why.

## Pasture Task Creation

```bash
PROPOSAL_ID_URI=$(pasture task create "PROPOSAL-1: <feature name>" --phase propose --namespace "$PASTURE_NAMESPACE" --format json --type=feature \
  --description="$(cat <<EOF
---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
---

## Problem Space

**Axes of the problem:**
- Parallelism: ...
- Distribution: ...

**Has-a / Is-a:**
- X HAS-A Y
- Z IS-A W

## Engineering Tradeoffs

| Option | Pros | Cons | Decision |
|--------|------|------|----------|
| A | ... | ... | Selected |
| B | ... | ... | Rejected |

## MVP Milestone

<scope with tradeoff rationale>

## Public Interfaces

\\`\\`\\`go
type Example interface { /* ... */ }
\\`\\`\\`

## Types & Enums

\\`\\`\\`go
type ExampleType int

const (
    ExampleTypeA ExampleType = iota
    ExampleTypeB
)
\\`\\`\\`

## Validation Checklist

### Phase 1
- [ ] Item 1
- [ ] Item 2

### Phase 2
- [ ] Item 3

## BDD Acceptance Criteria

**Given** precondition
**When** action
**Then** outcome
**Should Not** negative case

## Files Affected
- pkg/path/file1.go (create)
- pkg/path/file2.go (modify)
EOF
)" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$PROPOSAL_ID_URI" pasture:p3-plan:s3-propose
pasture task update "$PROPOSAL_ID_URI" --notes "Design: '{\"validation_checklist\":[\"Item 1\",\"Item 2\",\"Item 3\"],\"tradeoffs\":[{\"decision\":\"Use A\",\"rationale\":\"Because...\"}],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\",\"should_not\":\"W\"}]}'"

# Link to request
pasture task dep add "${REQUEST_ID_URI}" --blocked-by "${PROPOSAL_ID_URI}"
```

## Before Creating the Proposal

Read the URD and Phase 1 outputs to understand full context before drafting:
```bash
pasture task show "${URD_ID_URI}"
pasture task show "${REQUEST_ID_URI}"   # includes classification, research findings, explore findings as comments
```

The URD contains the structured requirements, priorities, design choices, and MVP goals from the URE survey. The REQUEST task comments contain Phase 1 outputs: classification (4 axes), domain research findings (prior art, standards), and codebase exploration findings (entry points, related types, dependencies). Your proposal must:
- Trace back to URD requirements
- Incorporate research findings (prior art, domain standards) into engineering tradeoffs
- Reference explore findings (entry points, existing patterns) in the files affected section

## Plan Structure

- **Requirements Traceability: URD:** `<urd-id>`
- Problem Space (axes, has-a/is-a)
- Engineering Tradeoffs (table with decisions)
- MVP Milestone (scope with tradeoff rationale)
- Public Interfaces (Go)
- Types & Enums
- Validation Checklist (per phase)
- BDD Acceptance Criteria
- Files Affected

## Next Steps

After creating PROPOSAL-N task:
1. Run `/pasture:architect-request-review` to spawn 3 reviewers
2. Wait for all 3 reviewers to vote ACCEPT
3. Run `/pasture:architect-ratify` to add ratify label to PROPOSAL-N

## Follow-up Proposals (FOLLOWUP_PROPOSAL-N)

When creating proposals for a follow-up epic (received via h6 from supervisor):
- **Title prefix:** `FOLLOWUP_PROPOSAL-N:` (e.g., `FOLLOWUP_PROPOSAL-1: Add request-id correlation`)
- **References:** Include both `original_urd: <id>` and `followup_urd: <id>` in frontmatter
- **Content:** Address specific IMPORTANT/MINOR findings scoped in FOLLOWUP_URE/URD
- Same review/ratify/UAT lifecycle applies (3 reviewers, ACCEPT/REVISE, UAT, ratify, handoff)
<!-- END GENERATED FROM pasture schema -->
