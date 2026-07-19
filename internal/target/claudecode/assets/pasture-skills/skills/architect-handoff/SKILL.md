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
- Then: author the handoff in a HANDOFF Beads task body, linking to the ratified proposal
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

## When to Use

Plan ratified and user has approved proceeding with implementation.

## Handoff Template

Storage: the handoff is authored directly in the **HANDOFF Beads task body** (no filesystem path; the task body IS the handoff).

```markdown
# Handoff: Architect → Supervisor

## Supervisor Startup
1. Call `Skill(/pasture:supervisor)` to load your role instructions
2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed
3. Read the RATIFIED PROPOSAL and URD with `bd show` commands below
4. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1 types / L2 tests / L3 impl triple is only illustrative)

## References
- REQUEST: <request-task-id>
- URD: <urd-task-id> (read with `bd show <urd-id>`)
- RATIFIED PROPOSAL: <ratified-proposal-id> (read with `bd show <proposal-id>`)

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

1. Create the HANDOFF Beads task — its body IS the handoff document (use the template above):
   ```bash
   bd create --type=task --priority=2 \
     --title="HANDOFF: Architect → Supervisor for REQUEST" \
     --description="---
   references:
     request: <request-task-id>
     urd: <urd-task-id>
     proposal: <ratified-proposal-id>
   ---
   # Handoff: Architect → Supervisor
   <full handoff body per the template above>" \
     --add-label "pasture:p7-plan:s7-handoff"

   bd dep add <request-id> --blocked-by <handoff-id>
   ```

2. Launch the supervisor as an **Opus teammate** via TeamCreate (the IMPL_PLAN phase runs as an Agent Team, not aura-swarm):
   ```
   TeamCreate({ team_name: "<epoch>-impl", ... })          # supervisor + workers as Opus teammates
   # then assign the supervisor its task via SendMessage (see Example Prompt below)
   ```

3. Monitor supervisor progress:
   ```bash
   # Check beads status
   bd list --status=in_progress
   ```

   A supervisor that looks idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.

## Example Prompt

**CRITICAL:** The SendMessage assignment MUST instruct the supervisor to invoke `/pasture:supervisor` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.

```
Start by calling `Skill(/pasture:supervisor)` to load your role instructions.

Implement the ratified plan for <feature name>.

## Context
- REQUEST: <request-task-id>
- URD: <urd-task-id> (read with `bd show <urd-id>` for user requirements)
- RATIFIED PROPOSAL: <ratified-proposal-id>
- HANDOFF: <handoff-task-id> (the handoff body — read with `bd show <handoff-id>`)

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
4. Read the ratified plan with `bd show <ratified-proposal-id>` and the URD with `bd show <urd-id>`
```

Deliver this assignment to the supervisor teammate via SendMessage after TeamCreate:

```
SendMessage({
  to: "supervisor",
  message: `Start by calling Skill(/pasture:supervisor) to load your role instructions.

Implement the ratified plan for User Authentication.

## Context
- REQUEST: project-abc
- URD: project-xyz
- RATIFIED PROPOSAL: project-prop1
- HANDOFF: project-handoff (read with bd show project-handoff)

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
4. Read ratified plan: bd show project-prop1 and URD: bd show project-xyz`,
  summary: "IMPL_PLAN assignment with Beads context"
})
```

## Spawning via TeamCreate

- Spawn the supervisor (and the workers it will coordinate) as **Opus** teammates — the IMPL_PLAN phase benefits from the stronger model for decomposition and review.
- Teammates have **zero prior context**: every SendMessage assignment MUST be self-contained (call `Skill(/pasture:supervisor)`, the Beads task IDs, and `bd show` commands to fetch full requirements).
- Do not spawn the supervisor via `aura-swarm` for the IMPL_PLAN phase; aura-swarm remains available for worktree-isolated epics, but the default handoff uses TeamCreate.

## IMPORTANT

- **DO NOT** spawn the supervisor as a Task tool subagent or via `aura-swarm` for the IMPL_PLAN phase — use TeamCreate with an Opus supervisor
- **DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks
- **DO NOT** implement the plan yourself - your role is handoff and monitoring
- **DO NOT** shut down a supervisor that appears idle right after spawn — it is usually running Explore subagents
- The supervisor reads the ratified plan and determines vertical slice structure
- Architect monitors for blockers or escalations

## Follow-up Lifecycle (h1 Reuse)

This handoff (h1: Architect → Supervisor) also occurs after FOLLOWUP_PROPOSAL is ratified. In follow-up context:

- **Storage:** the follow-up handoff is authored in its own HANDOFF Beads task body (no filesystem path)
- **References:** Include both original URD and FOLLOWUP_URD task IDs
- **Context:** Summary of FOLLOWUP_PROPOSAL ratification and the user-DEFER'd UAT items the follow-up addresses
- **Next step:** Supervisor creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks for the follow-up scope
<!-- END GENERATED FROM pasture schema -->
