---
name: supervisor-spawn-worker
description: Launch a worker agent for an assigned slice
---

# Supervisor Spawn Worker

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:supervisor:spawn-worker` — Launch a worker agent for an assigned slice

Launch the wave of workers for parallel vertical slice implementation, reviewed by ephemeral reviewers.

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-9-worker-slices)** <- Phase 9

### Ride the Wave — Coordinated Phase 8-10 Execution

```text
Phase 8: PLAN
  ├─ Read RATIFIED_PLAN + URD
  ├─ Spawn ephemeral Explore subagents (delegation operation, scoped queries)
  ├─ Use Explore findings to map codebase
  ├─ Decompose into vertical slices + integration points
  └─ Create leaf tasks for every slice

Phase 9: BUILD
  ├─ Spawn N Workers for parallel slice implementation
  ├─ Workers implement their slices in parallel
  └─ Workers do NOT shut down when finished

Phase 10: REVIEW + FIX CYCLES (up to the chosen review-effort budget)
  ├─ Cycle 1:
  │   ├─ Spawn ephemeral reviewers (delegation operation, whole-wave review)
  │   ├─ Reviewers review ALL slices (severity tree: BLOCKER/IMPORTANT/MINOR)
  │   ├─ Reviewers label every finding pasture:finding under its severity group
  │   ├─ Workers fix ALL findings (BLOCKER + IMPORTANT + MINOR) with atomic commits
  │   └─ Spawn new ephemeral reviewers for re-review
  ├─ Cycle 2 (if needed): same pattern
  ├─ Cycle N (as many as needed): same pattern
  └─ Each wave advances when no BLOCKER is open; IMPORTANT and MINOR findings stay open

DONE → Phase 11 (UAT)
  ├─ Shut down Workers
  └─ FOLLOWUP epic (if any) is created at UAT from user-DEFER'd items only

Cycle Exit Conditions:
  A wave gate: no open BLOCKER finding                        → the wave advances; open findings stay open
  After the last wave: convergence over open pasture:finding  → targets 0/0/0 within maxRounds; on exhaustion, surface to the user
  Finding close rule: see C-finding-lifecycle
  Genuinely stuck (cannot converge)                           → Escalate to architect for re-planning

```

**[sup-spawn-task-tool]**
- Given: implementation tasks
- When: spawning
- Then: use Agent tool with `run_in_background: true`
- Should not: block on worker completion

**[sup-spawn-parallel-wave]**
- Given: multiple workers
- When: launching
- Then: spawn all slices in parallel as a single wave
- Should not: spawn sequentially

**[sup-spawn-worker-context]**
- Given: worker assignment
- When: providing context
- Then: include Pasture task ID, full context, and handoff document
- Should not: omit checklist or criteria

**[sup-spawn-handoff-doc]**
- Given: worker handoff
- When: creating
- Then: author the supervisor→worker handoff in the slice (or a dedicated handoff) Pasture task body
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
- Then: iterate review->fix->re-review up to the chosen review-effort budget; only an open BLOCKER gates a wave, IMPORTANT and MINOR findings stay open, and convergence at the end targets 0/0/0 within maxRounds; on budget exhaustion without convergence, surface outstanding findings to the user at a gate
- Should not: hardcode the budget, proceed past the chosen budget without surfacing to the user, require a zeroed round to close a wave, or close a finding outside `C-finding-lifecycle`

**[sup-spawn-important-after-cycles]**
- Given: IMPORTANT or MINOR findings remain
- When: deciding next step
- Then: keep them open past the wave gate and converge over every open `pasture:finding` at the end — only an open BLOCKER blocks the wave
- Should not: proceed to UAT with an open BLOCKER or route any review severity (IMPORTANT/MINOR) to the FOLLOWUP epic

**[frag--review-clean-exit]**
- Given: per-wave code review and end-of-epoch convergence
- When: evaluating review results
- Then: iterate review -> fix -> re-review up to the chosen review-effort budget; during a wave only an open BLOCKER finding gates progress, while IMPORTANT and MINOR findings stay open; after the last wave, converge over open `pasture:finding` tasks across all waves toward 0/0/0 within maxRounds; a finding closes only per `C-finding-lifecycle`; on budget exhaustion without convergence, SURFACE the outstanding findings to the user at a gate for a decision
- Should not: hardcode the budget; proceed past the chosen budget without surfacing outstanding findings to the user; require a zeroed round at a wave boundary; close a finding outside `C-finding-lifecycle`; loop forever when a finite budget was chosen

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls one derivation function for the header and for every write that has no pin; it handles git@, ssh://, git://, http://, https:// and user@host:path. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

Implementation tasks ready. Ephemeral reviewers will be spawned for the wave during review phase.

## Ride the Wave — Overview

The supervisor executes Phases 8-10 as a single coordinated cycle called **Ride the Wave**:

```
1. PLAN  → supervisor-plan-tasks: decompose into slices + integration points
2. EXPLORE → Ephemeral Explore subagents (Agent tool): map codebase, short-lived
3. BUILD → N Workers: implement slices in parallel
4. REVIEW → Ephemeral reviewers (Agent tool): review the whole wave
5. FIX   → Workers fix BLOCKERs + IMPORTANTs with atomic commits
6. RE-REVIEW → Spawn new ephemeral reviewers for re-review
7. REPEAT → Steps 5-6 up to the chosen review-effort budget; only an open BLOCKER gates the wave, and convergence at the end targets 0/0/0 within maxRounds; on budget exhaustion without convergence, surface to the user
8. TRACK → Open findings converge at the end per `C-finding-lifecycle`; none route to FOLLOWUP
9. NEXT  → When convergence finds no open `pasture:finding` → Phase 11 (UAT); escalate to architect only if genuinely stuck
```

**Key rules:**
- Reviewers are ephemeral (spawned per review cycle with the Agent tool)
- Slices are **never closed** until reviewed at least once
- **Configurable review-effort budget** (chosen at Phase 8: 3 rounds / 1 round / 0 rounds / unlimited / custom) — iterate review→fix→re-review up to the budget; only an open BLOCKER gates a wave, and convergence at the end queries open `pasture:finding` toward 0/0/0 within maxRounds; on budget exhaustion without convergence, surface outstanding findings to the user at a gate; escalate to architect only if genuinely stuck
- The FOLLOWUP epic is fed ONLY by user-DEFER'd UAT items, never by review severities

## Handoff Template (Supervisor → Worker)

Before spawning each worker, author its handoff in the slice (or a dedicated handoff) Pasture task body:

**Storage:** the Pasture task body IS the handoff — no filesystem path.

```markdown
# Handoff: Supervisor → Worker <N>

## Context
- Request: ${REQUEST_ID_URI}
- URD: ${URD_ID_URI}
- IMPL_PLAN: ${IMPL_PLAN_ID_URI}
- Ratified Proposal: ${PROPOSAL_ID_URI}

## Your Slice
- Slice: the assigned work
- Task ID: ${SLICE_TASK_ID_URI}
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
delegation: {
  description: "Worker: implement the slice",
  prompt: `Call Skill(/pasture:worker) and implement the assigned slice.

Pasture Task ID: "${TASK_ID_URI}"
Read full requirements + handoff: "${EPOCH_REC}" show "${TASK_ID_URI}"

Do NOT shut down after implementation. You will receive review feedback and may need to fix issues.`,
  subagent_type: "general-purpose",
  run_in_background: true
}
```

Per [sup-spawn-workers], use `subagent_type: "general-purpose"`, not a custom agent type. The worker skill is invoked inside the agent via `Skill(/pasture:worker)`.

## Delegated Assignment Message

When a worker is delegated an assignment, the assignment message is its only context. The message MUST be self-contained — the worker has **no prior context**:

```
SendMessage({
  type: "message",
  recipient: "worker-1",
  content: `You are assigned the slice. Start by calling Skill(/pasture:worker).

Your Pasture task ID: "${SLICE_TASK_ID_URI}"
Run this to get full requirements + handoff: "${EPOCH_REC}" show "${SLICE_TASK_ID_URI}"

Key references (run "${EPOCH_REC}" show on each for full context):
- Request: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}"
- IMPL_PLAN: "${IMPL_PLAN_ID_URI}"
- Ratified Proposal: "${PROPOSAL_ID_URI}"

Read the handoff doc and your Pasture task before starting implementation.

IMPORTANT: Do NOT shut down after completing implementation. You will receive
review feedback from ephemeral reviewers and may need to fix BLOCKERs and IMPORTANT
findings. Stay alive for the full Ride the Wave cycle.`,
  summary: "slice assignment"
})
```

Per [sup-teamcreate-msg], include Pasture task IDs and `"${EPOCH_REC}" show` commands in every assignment. Teammates cannot see your conversation or task tree.

## Worker Persistence (Ride the Wave)

Per [sup-worker-persistence], workers stay alive for the full review-fix cycle:

1. Worker completes slice → notifies supervisor
2. Supervisor does **NOT** close the slice or shut down the worker
3. Ephemeral reviewers review the slice
4. If ANY findings (BLOCKER/IMPORTANT/MINOR): supervisor sends fix assignment to worker
5. Worker fixes issues → notifies supervisor
6. New ephemeral reviewers re-review
7. Repeat steps 4-6 up to the chosen review-effort budget; only an open BLOCKER gates the wave, and convergence at the end targets 0/0/0 within maxRounds; on budget exhaustion without convergence, surface outstanding findings to the user at a gate
8. After convergence finds no open `pasture:finding`: supervisor shuts down worker

### Fix Assignment Message Template

```
SendMessage({
  type: "message",
  recipient: "worker-1",
  content: `Review cycle <N> found issues in your slice (the slice).

BLOCKERs (must fix — blocks slice closure):
- "${FINDING_ID_URI}": <description> ("${EPOCH_REC}" show "${FINDING_ID_URI}")

IMPORTANT (must fix — closes only per C-finding-lifecycle):
- "${FINDING_ID_URI}": <description> ("${EPOCH_REC}" show "${FINDING_ID_URI}")

MINOR (must fix — closes only per C-finding-lifecycle):
- "${FINDING_ID_URI}": <description> ("${EPOCH_REC}" show "${FINDING_ID_URI}")

After fixing all items:
  "${EPOCH_REC}" note "${SLICE_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Fixes applied for review cycle <N>"

Do NOT shut down. Ephemeral reviewers will re-review.`,
  summary: "Review cycle <N> fixes for the slice"
})
```

## Worker Should Update Pasture Status

- On start: `"${EPOCH_REC}" status "${TASK_ID_URI}" --set in_progress`
- On implementation complete (NOT slice close): `"${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Implementation complete, awaiting review"`
- On blocked: `"${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body="Blocked: <reason>"`
- Slice closure: **only the supervisor** closes slices after review passes

## Assign via Pasture

```bash
"${EPOCH_REC}" label "${TASK_ID_URI}" owner:"${REGISTERED_WORKER_ACTOR}"
"${EPOCH_REC}" status "${TASK_ID_URI}" --set in_progress
```

## Follow-up slice handoff

For follow-up slices, the handoff (authored in the Pasture task body — no filesystem path) extends with additional fields:

```markdown
# Handoff: Supervisor → Worker <N> (Follow-up)

## Context
- Original Request: "${REQUEST_ID_URI}"
- Follow-up Epic: "${FOLLOWUP_EPIC_ID_URI}"
- FOLLOWUP_URD: "${FOLLOWUP_URD_ID_URI}"
- FOLLOWUP_IMPL_PLAN: "${FOLLOWUP_IMPL_PLAN_ID_URI}"

## Your Slice
- Slice: the follow-up slice
- Task ID: "${SLICE_TASK_ID_URI}"

## DEFER'd Items (from UAT)
| Item Task ID | Source UAT | Description |
|---|---|---|
| "${ITEM_ID_1_URI}" | "${UAT_ID_URI}" | <user-DEFER'd item description> |
| "${ITEM_ID_2_URI}" | "${UAT_ID_URI}" | <user-DEFER'd item description> |

## Acceptance Criteria
- All DEFER'd items in this slice resolved (tests pass, production code path verified)
- See "${EPOCH_REC}" show "${SLICE_TASK_ID_URI}" for full validation_checklist
```
<!-- END GENERATED FROM pasture schema -->
