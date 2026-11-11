---
name: architect-propose-plan
description: Create PROPOSAL-N task with full technical plan
---

# Architect: Propose Plan

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.33 artifact v1 -->
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

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. With no origin, both backends use file:// of the working directory, so invocations from different subdirectories use different namespaces. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

Starting new feature design; creating formal plan for review.

## PROPOSAL-N Naming

Proposals are numbered incrementally: PROPOSAL-1, PROPOSAL-2, etc. Each revision increments N. Old proposals are marked `pasture:superseded` with a comment explaining why.

## Pasture Task Creation

```bash
PROPOSAL_ID_URI=$("${EPOCH_REC}" create --kind feature --title "PROPOSAL-1: <feature name>" --phase propose \
  --body="$(cat <<EOF
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
)")
"${EPOCH_REC}" label "$PROPOSAL_ID_URI" pasture:p3-plan:s3-propose
"${EPOCH_REC}" note "$PROPOSAL_ID_URI" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Design: '{\"validation_checklist\":[\"Item 1\",\"Item 2\",\"Item 3\"],\"tradeoffs\":[{\"decision\":\"Use A\",\"rationale\":\"Because...\"}],\"acceptance_criteria\":[{\"given\":\"X\",\"when\":\"Y\",\"then\":\"Z\",\"should_not\":\"W\"}]}'"

# Link to request
"${EPOCH_REC}" block "${REQUEST_ID_URI}" --by "${PROPOSAL_ID_URI}"
```

## Before Creating the Proposal

Read the URD and Phase 1 outputs to understand full context before drafting:
```bash
"${EPOCH_REC}" show "${URD_ID_URI}"
"${EPOCH_REC}" show "${REQUEST_ID_URI}"   # includes classification, research findings, explore findings as comments
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
