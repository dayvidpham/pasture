---
name: supervisor-spawn-worker
description: Launch a worker agent for an assigned slice
---

# Supervisor Spawn Worker

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:supervisor:spawn-worker` — Launch a worker agent for an assigned slice

Launch the wave of workers for parallel vertical slice implementation, reviewed by ephemeral reviewers.

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

### Ride the Wave — Coordinated Phase 8-10 Execution

```text
Phase 8: PLAN
  ├─ Read RATIFIED_PLAN + URD
  ├─ Spawn ephemeral Explore subagents (Task tool, scoped queries)
  ├─ Use Explore findings to map codebase
  ├─ Decompose into vertical slices + integration points
  └─ Create leaf tasks for every slice

Phase 9: BUILD
  ├─ Spawn N Workers for parallel slice implementation
  ├─ Workers implement their slices in parallel
  └─ Workers do NOT shut down when finished

Phase 10: REVIEW + FIX CYCLES (no cycle cap — iterate until 0/0/0 clean)
  ├─ Cycle 1:
  │   ├─ Spawn ephemeral reviewers (Task tool, per-slice review)
  │   ├─ Reviewers review ALL slices (severity tree: BLOCKER/IMPORTANT/MINOR)
  │   ├─ Workers fix ALL findings (BLOCKER + IMPORTANT + MINOR) with atomic commits
  │   └─ Spawn new ephemeral reviewers for re-review
  ├─ Cycle 2 (if needed): same pattern
  ├─ Cycle N (as many as needed): same pattern
  └─ Continue until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR

DONE → Phase 11 (UAT)
  ├─ Shut down Workers
  └─ FOLLOWUP epic (if any) is created at UAT from user-DEFER'd items only

Cycle Exit Conditions:
  Fix-free clean round: 0 BLOCKER + 0 IMPORTANT + 0 MINOR   → Proceed to Phase 11 (UAT)
  ANY finding remains (BLOCKER/IMPORTANT/MINOR)             → Workers fix, spawn new ephemeral reviewers (no cap)
  Genuinely stuck (cannot reach a clean round)             → Escalate to architect for re-planning

```

**[sup-spawn-task-tool]**
- Given: implementation tasks
- When: spawning
- Then: use Task tool with `run_in_background: true`
- Should not: block on worker completion

**[sup-spawn-parallel-wave]**
- Given: multiple workers
- When: launching
- Then: spawn all slices in parallel as a single wave
- Should not: spawn sequentially

**[sup-spawn-worker-context]**
- Given: worker assignment
- When: providing context
- Then: include Beads task ID, full context, and handoff document
- Should not: omit checklist or criteria

**[sup-spawn-handoff-doc]**
- Given: worker handoff
- When: creating
- Then: author the supervisor→worker handoff in the slice (or a dedicated handoff) Beads task body
- Should not: skip the handoff or store it as a filesystem path

**[sup-spawn-no-close-before-review]**
- Given: workers complete their slices
- When: first wave finishes
- Then: do NOT close slices — ephemeral reviewers must review ALL slices first
- Should not: close a slice that has not been reviewed at least once

**[sup-spawn-fix-and-rereview]**
- Given: reviewers finish reviewing
- When: BLOCKERs or IMPORTANT findings exist
- Then: send findings to workers for fixing, then spawn new ephemeral reviewers for re-review
- Should not: skip re-review after fixes

**[sup-spawn-max-cycles]**
- Given: worker-reviewer cycle
- When: counting iterations
- Then: iterate review->fix->re-review with NO cycle cap until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR
- Should not: impose a maximum cycle cap, close a wave on a fix-applying round, or proceed with any finding outstanding

**[sup-spawn-important-after-cycles]**
- Given: IMPORTANT or MINOR findings remain
- When: deciding next step
- Then: keep iterating review->fix->re-review until ALL severity groups reach 0 — every severity must be resolved before the wave closes
- Should not: proceed to UAT with non-zero findings or route any review severity (IMPORTANT/MINOR) to the FOLLOWUP epic

**[frag--review-clean-exit]**
- Given: per-slice code review
- When: evaluating review results
- Then: iterate review -> fix -> re-review with NO cycle cap until a fix-free clean round confirms 0 BLOCKER + 0 IMPORTANT + 0 MINOR
- Should not: close a wave on a fix-applying round, proceed with any finding outstanding, or impose a maximum cycle cap

## When to Use

Implementation tasks ready. Ephemeral reviewers will be spawned per-slice during review phase.

## Ride the Wave — Overview

The supervisor executes Phases 8-10 as a single coordinated cycle called **Ride the Wave**:

```
1. PLAN  → supervisor-plan-tasks: decompose into slices + integration points
2. EXPLORE → Ephemeral Explore subagents (Task tool): map codebase, short-lived
3. BUILD → N Workers: implement slices in parallel
4. REVIEW → Ephemeral reviewers (Task tool): review per-slice
5. FIX   → Workers fix BLOCKERs + IMPORTANTs with atomic commits
6. RE-REVIEW → Spawn new ephemeral reviewers for re-review
7. REPEAT → Steps 5-6 with NO cycle cap until a fix-free clean round confirms 0/0/0
8. TRACK → ALL severities (BLOCKER/IMPORTANT/MINOR) must reach 0 — none route to FOLLOWUP
9. NEXT  → When fix-free clean (0 BLOCKER + 0 IMPORTANT + 0 MINOR) → Phase 11 (UAT); escalate to architect only if genuinely stuck
```

**Key rules:**
- Reviewers are ephemeral (spawned per review cycle via Task tool)
- Slices are **never closed** until reviewed at least once
- **No cycle cap** — iterate review→fix→re-review until 0 BLOCKER + 0 IMPORTANT + 0 MINOR on a fix-free round; escalate to architect only if genuinely stuck
- The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by review severities

## Handoff Template (Supervisor → Worker)

Before spawning each worker, author its handoff in the slice (or a dedicated handoff) Beads task body:

**Storage:** the Beads task body IS the handoff — no filesystem path.

```markdown
# Handoff: Supervisor → Worker <N>

## Context
- Request: <request-task-id>
- URD: <urd-task-id>
- IMPL_PLAN: <impl-plan-task-id>
- Ratified Proposal: <proposal-task-id>

## Your Slice
- Slice: SLICE-<N>
- Task ID: <slice-task-id>
- Production Code Path: <what end users run>

## Key Files
| File | What You Own |
|------|-------------|
| pkg/feature/types.go | ListOptions, ListEntry types |
| cmd/feature/list_test.go | List command tests |
| pkg/feature/service.go | ListItems() method |
| cmd/feature/list.go | list subcommand wiring |

## Implementation Order
1. Layer 1: Types (your slice only)
2. Layer 2: Tests (import production code — will FAIL, expected)
3. Layer 3: Implementation + Wiring (make tests PASS)

## Validation Checklist
- [ ] Production code verified via code inspection
- [ ] Tests import actual CLI (not test-only export)
- [ ] No dual-export anti-pattern
- [ ] No TODO placeholders
- [ ] Service wired with real dependencies

## Persistence
Do NOT shut down after implementation. You will receive review feedback
and may need to fix BLOCKERs and IMPORTANT findings. Stay alive for the
full Ride the Wave cycle.
```

## Task Call

```
Task({
  description: "Worker: implement SLICE-N",
  prompt: `Call Skill(/pasture:worker) and implement the assigned slice.

Beads Task ID: <task-id>
Read full requirements + handoff: bd show <task-id>

Do NOT shut down after implementation. You will receive review feedback and may need to fix issues.`,
  subagent_type: "general-purpose",
  run_in_background: true
})
```

Per [sup-spawn-workers], use `subagent_type: "general-purpose"`, not a custom agent type. The worker skill is invoked inside the agent via `Skill(/pasture:worker)`.

## TeamCreate: SendMessage Assignment

When workers are spawned via TeamCreate, they receive context through SendMessage instead of a Task prompt. The message MUST be self-contained — teammates have **no prior context**:

```
SendMessage({
  type: "message",
  recipient: "worker-1",
  content: `You are assigned SLICE-1. Start by calling Skill(/pasture:worker).

Your Beads task ID: <slice-task-id>
Run this to get full requirements + handoff: bd show <slice-task-id>

Key references (run bd show on each for full context):
- Request: <request-task-id>
- URD: <urd-task-id>
- IMPL_PLAN: <impl-plan-task-id>
- Ratified Proposal: <proposal-task-id>

Read the handoff doc and your Beads task before starting implementation.

IMPORTANT: Do NOT shut down after completing implementation. You will receive
review feedback from ephemeral reviewers and may need to fix BLOCKERs and IMPORTANT
findings. Stay alive for the full Ride the Wave cycle.`,
  summary: "SLICE-1 assignment with Beads context"
})
```

Per [sup-teamcreate-msg], include Beads task IDs and `bd show` commands in every assignment. Teammates cannot see your conversation or task tree.

## Worker Persistence (Ride the Wave)

Per [sup-worker-persistence], workers stay alive for the full review-fix cycle:

1. Worker completes slice → notifies supervisor
2. Supervisor does **NOT** close the slice or shut down the worker
3. Ephemeral reviewers review the slice
4. If ANY findings (BLOCKER/IMPORTANT/MINOR): supervisor sends fix assignment to worker
5. Worker fixes issues → notifies supervisor
6. New ephemeral reviewers re-review
7. Repeat steps 4-6 with NO cycle cap until a fix-free clean round confirms 0/0/0
8. After a fix-free clean round (0 BLOCKER + 0 IMPORTANT + 0 MINOR): supervisor shuts down worker

### Fix Assignment Message Template

```
SendMessage({
  type: "message",
  recipient: "worker-1",
  content: `Review cycle <N> found issues in your slice (SLICE-1).

BLOCKERs (must fix — blocks slice closure):
- <finding-id>: <description> (bd show <finding-id>)

IMPORTANT (must fix — must reach 0 before wave close):
- <finding-id>: <description> (bd show <finding-id>)

MINOR (must fix — must reach 0 before wave close):
- <finding-id>: <description> (bd show <finding-id>)

After fixing all items:
  bd comments add <slice-id> "Fixes applied for review cycle <N>"

Do NOT shut down. Ephemeral reviewers will re-review.`,
  summary: "Review cycle <N> fixes for SLICE-1"
})
```

## Worker Should Update Beads Status

- On start: `bd update <task-id> --status=in_progress`
- On implementation complete (NOT slice close): `bd comments add <task-id> "Implementation complete, awaiting review"`
- On blocked: `bd update <task-id> --notes="Blocked: <reason>"`
- Slice closure: **only the supervisor** closes slices after review passes

## Assign via Beads

```bash
bd update <task-id> --assignee="<worker-agent-name>"
bd update <task-id> --status=in_progress
```

## Follow-up Slice Handoff (FOLLOWUP_SLICE-N)

For follow-up slices, the handoff (authored in the Beads task body — no filesystem path) extends with additional fields:

```markdown
# Handoff: Supervisor → Worker <N> (Follow-up)

## Context
- Original Request: <request-task-id>
- Follow-up Epic: <followup-epic-id>
- FOLLOWUP_URD: <followup-urd-id>
- FOLLOWUP_IMPL_PLAN: <followup-impl-plan-id>

## Your Slice
- Slice: FOLLOWUP_SLICE-<N>
- Task ID: <slice-task-id>

## DEFER'd Items (from UAT)
| Item Task ID | Source UAT | Description |
|---|---|---|
| <item-id-1> | <uat-id> | <user-DEFER'd item description> |
| <item-id-2> | <uat-id> | <user-DEFER'd item description> |

## Acceptance Criteria
- All DEFER'd items in this slice resolved (tests pass, production code path verified)
- See bd task <slice-task-id> for full validation_checklist
```
<!-- END GENERATED FROM pasture schema -->
