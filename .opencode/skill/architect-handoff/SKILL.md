---
name: architect-handoff
description: Create handoff document and transfer to supervisor
---

# Architect: Handoff to Supervisor

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:architect:handoff` — Create handoff document and transfer to supervisor

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-7-handoff)** <- Phase 7

**[arch-handoff-link-proposal]**
- Given: ratified PROPOSAL-N task
- When: handing off
- Then: author the handoff in a HANDOFF Pasture task body, linking to the ratified proposal
- Should not: hand off without linking to ratified proposal

**[arch-handoff-spawn-supervisor]**
- Given: handoff for the IMPL_PLAN phase
- When: spawning the supervisor
- Then: use TeamCreate to spawn the supervisor as an Opus teammate (workers also Opus), then assign work via SendMessage
- Should not: spawn the supervisor as a Task tool subagent or via aura-swarm for the IMPL_PLAN phase

**[arch-handoff-supervisor-not-idle]**
- Given: a freshly spawned supervisor
- When: it dispatches Explore subagents and appears idle
- Then: let it work — an apparently-idle supervisor is usually running Explore subagents to map the codebase before decomposing slices
- Should not: shut down or restart a supervisor that looks idle at the start of the IMPL_PLAN phase

**[arch-handoff-no-impl-tasks]**
- Given: implementation planning
- When: handing off
- Then: let supervisor create vertical slice tasks
- Should not: create implementation tasks as architect

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

Plan ratified and user has approved proceeding with implementation.

## Handoff Template

Storage: the handoff is authored directly in the **HANDOFF Pasture task body** (no filesystem path; the task body IS the handoff).

```markdown
# Handoff: Architect → Supervisor

## Supervisor Startup
1. Call `Skill(/pasture:supervisor)` to load your role instructions
2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed
3. Read the RATIFIED PROPOSAL and URD with `pasture task show` commands below
4. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1 types / L2 tests / L3 impl triple is only illustrative)

## References
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with `pasture task show "${URD_ID_URI}"`)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}" (read with `pasture task show "${RATIFIED_PROPOSAL_ID_URI}"`)

## Summary
<1-2 sentence summary of what needs to be implemented>

## Key Files
<list main files to be created/modified from the ratified plan>

## Validation Checklist
<validation checklist from the ratified proposal>

## BDD Acceptance Criteria
<Given/When/Then criteria from the ratified plan>

## Implementation Notes
<any special considerations, known risks, or constraints>
```

## Steps

1. Create the HANDOFF Pasture task — its body IS the handoff document (use the template above):
   ```bash
   HANDOFF_ID_URI=$(pasture task create "HANDOFF: Architect → Supervisor for REQUEST" --phase handoff --namespace "$PASTURE_NAMESPACE" --format json --type=task --priority=2 \
     --description="---
   references:
     request: "${REQUEST_ID_URI}"
     urd: "${URD_ID_URI}"
     proposal: "${RATIFIED_PROPOSAL_ID_URI}"
   ---
   # Handoff: Architect → Supervisor
   <full handoff body per the template above>" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
   pasture task label add "$HANDOFF_ID_URI" pasture:p7-plan:s7-handoff

   ```

2. Launch the supervisor as an **Opus teammate** via TeamCreate (the IMPL_PLAN phase runs as an Agent Team, not aura-swarm):
   ```
   TeamCreate({ team_name: "<epoch>-impl", ... })          # supervisor + workers as Opus teammates
   # then assign the supervisor its task via SendMessage (see Example Prompt below)
   ```

3. Monitor supervisor progress:
   ```bash
   # Check pasture status
   pasture task list --namespace "$PASTURE_NAMESPACE" --status=in_progress
   ```

   A supervisor that looks idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.

## Example Prompt

**CRITICAL:** The SendMessage assignment MUST instruct the supervisor to invoke `/pasture:supervisor` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.

```
Start by calling `Skill(/pasture:supervisor)` to load your role instructions.

Implement the ratified plan for <feature name>.

## Context
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with `pasture task show "${URD_ID_URI}"` for user requirements)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (the handoff body — read with `pasture task show "${HANDOFF_ID_URI}"`)

## Summary
<1-2 sentence summary of what needs to be implemented>

## Key Files
<list main files to be created/modified from the ratified plan>

## Acceptance Criteria
<Given/When/Then criteria from the ratified plan>

## Reminders
1. Call `Skill(/pasture:supervisor)` FIRST — do not proceed without loading your role
2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed
3. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1/L2/L3 triple is only illustrative); a slice without leaf tasks is undecomposed
4. Read the ratified plan with `pasture task show "${RATIFIED_PROPOSAL_ID_URI}"` and the URD with `pasture task show "${URD_ID_URI}"`
```

Deliver this assignment to the supervisor teammate via SendMessage after TeamCreate:

```
SendMessage({
  to: "supervisor",
  message: `Start by calling Skill(/pasture:supervisor) to load your role instructions.

Implement the ratified plan for User Authentication.

## Context
- REQUEST: ${REQUEST_ID_URI}
- URD: "${URD_ID_URI}"
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (read with pasture task show "${HANDOFF_ID_URI}")

## Summary
Add JWT-based authentication with login/logout endpoints and middleware.

## Key Files
- pkg/auth/jwt.go
- pkg/auth/middleware.go
- cmd/api/auth.go

## Acceptance Criteria
Given a valid JWT token when accessing protected routes then allow access
Given an expired token when accessing protected routes then return 401

## Reminders
1. Call Skill(/pasture:supervisor) FIRST
2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed
3. Every slice MUST have leaf tasks (any number; L1/L2/L3 is only illustrative)
4. Read ratified plan: pasture task show "${RATIFIED_PROPOSAL_ID_URI}" and URD: pasture task show "${URD_ID_URI}"`,
  summary: "IMPL_PLAN assignment with Pasture context"
})
```

## Spawning via TeamCreate

- Spawn the supervisor (and the workers it will coordinate) as **Opus** teammates — the IMPL_PLAN phase benefits from the stronger model for decomposition and review.
- Teammates have **zero prior context**: every SendMessage assignment MUST be self-contained (call `Skill(/pasture:supervisor)`, the Pasture task IDs, and `pasture task show` commands to fetch full requirements).
- Do not spawn the supervisor via `aura-swarm` for the IMPL_PLAN phase; the deprecated orchestrator must not be invoked; the supported handoff uses TeamCreate.

## IMPORTANT

- **DO NOT** spawn the supervisor as a Task tool subagent or via `aura-swarm` for the IMPL_PLAN phase — use TeamCreate with an Opus supervisor
- **DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks
- **DO NOT** implement the plan yourself - your role is handoff and monitoring
- **DO NOT** shut down a supervisor that appears idle right after spawn — it is usually running Explore subagents
- The supervisor reads the ratified plan and determines vertical slice structure
- Architect monitors for blockers or escalations

## Follow-up Lifecycle (h1 Reuse)

This handoff (h1: Architect → Supervisor) also occurs after FOLLOWUP_PROPOSAL is ratified. In follow-up context:

- **Storage:** the follow-up handoff is authored in its own HANDOFF Pasture task body (no filesystem path)
- **References:** Include both original URD and FOLLOWUP_URD task IDs
- **Context:** Summary of FOLLOWUP_PROPOSAL ratification and the user-DEFER'd UAT items the follow-up addresses
- **Next step:** Supervisor creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks for the follow-up scope
<!-- END GENERATED FROM pasture schema -->
