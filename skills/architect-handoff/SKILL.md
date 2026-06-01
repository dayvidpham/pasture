# Architect: Handoff to Supervisor

<!-- BEGIN GENERATED FROM pasture schema -->
**Command:** `pasture:architect:handoff` — Create handoff document and transfer to supervisor

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-7-handoff)** <- Phase 7

**[arch-handoff-link-proposal]**
- Given: ratified PROPOSAL-N task
- When: handing off
- Then: create handoff document and HANDOFF task, linking to ratified proposal
- Should not: hand off without linking to ratified proposal

**[arch-handoff-spawn-supervisor]**
- Given: handoff
- When: spawning supervisor
- Then: use `aura-swarm start --swarm-mode intree --role supervisor` or `aura-swarm start --epic <id>`
- Should not: spawn supervisor as Task tool subagent

**[arch-handoff-no-impl-tasks]**
- Given: implementation planning
- When: handing off
- Then: let supervisor create vertical slice tasks
- Should not: create implementation tasks as architect

## When to Use

Plan ratified and user has approved proceeding with implementation.

## Handoff Template

Storage: `.git/.aura/handoff/{request-task-id}/architect-to-supervisor.md`

```markdown
# Handoff: Architect → Supervisor

## Supervisor Startup
1. Call `Skill(/pasture:supervisor)` to load your role instructions
2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed
3. Read the RATIFIED PROPOSAL and URD with `bd show` commands below
4. Every vertical slice MUST have leaf tasks (L1: types, L2: tests, L3: impl)

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

1. Create the handoff document:
   ```bash
   mkdir -p .git/.aura/handoff/<request-task-id>/
   # Write architect-to-supervisor.md with the template above
   ```

2. Create HANDOFF Beads task:
   ```bash
   bd create --type=task --priority=2 \
     --title="HANDOFF: Architect → Supervisor for REQUEST" \
     --description="---
   references:
     request: <request-task-id>
     urd: <urd-task-id>
     proposal: <ratified-proposal-id>
   ---
   Handoff from architect to supervisor. See handoff document at
   .git/.aura/handoff/<request-task-id>/architect-to-supervisor.md" \
     --add-label "pasture:p7-plan:s7-handoff"

   bd dep add <request-id> --blocked-by <handoff-id>
   ```

3. Launch supervisor:
   ```bash
   # In-place mode (long-running supervisor in tmux session)
   aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt "..."

   # Or worktree mode (epic-based workflow)
   aura-swarm start --epic <id>
   ```

4. Monitor supervisor progress:
   ```bash
   # Check beads status
   bd list --status=in_progress

   # Attach to supervisor session
   aura-swarm attach <epic-id-or-session-id>
   ```

## Example Prompt

**CRITICAL:** The prompt MUST instruct the supervisor to invoke `/pasture:supervisor` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.

```
Start by calling `Skill(/pasture:supervisor)` to load your role instructions.

Implement the ratified plan for <feature name>.

## Context
- REQUEST: <request-task-id>
- URD: <urd-task-id> (read with `bd show <urd-id>` for user requirements)
- RATIFIED PROPOSAL: <ratified-proposal-id>
- HANDOFF: <handoff-task-id>
- Handoff document: .git/.aura/handoff/<request-task-id>/architect-to-supervisor.md

## Summary
<1-2 sentence summary of what needs to be implemented>

## Key Files
<list main files to be created/modified from the ratified plan>

## Acceptance Criteria
<Given/When/Then criteria from the ratified plan>

## Reminders
1. Call `Skill(/pasture:supervisor)` FIRST — do not proceed without loading your role
2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed
3. Every vertical slice MUST have leaf tasks (L1: types, L2: tests, L3: impl) — a slice without leaf tasks is undecomposed
4. Read the ratified plan with `bd show <ratified-proposal-id>` and the URD with `bd show <urd-id>`
```

Pass the prompt to the script:

```bash
aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt "$(cat <<'EOF'
Start by calling Skill(/pasture:supervisor) to load your role instructions.

Implement the ratified plan for User Authentication.

## Context
- REQUEST: project-abc
- URD: project-xyz
- RATIFIED PROPOSAL: project-prop1
- Handoff document: .git/.aura/handoff/project-abc/architect-to-supervisor.md

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
3. Every slice MUST have leaf tasks (L1/L2/L3)
4. Read ratified plan: bd show project-prop1 and URD: bd show project-xyz
EOF
)"
```

## Script Options

```bash
aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt "..."             # Launch supervisor
aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt "..." --dry-run   # Preview without launching
aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt-file prompt.md    # Read prompt from file
```

## IMPORTANT

- **DO NOT** spawn supervisor as a Task tool subagent - use `aura-swarm start`
- **DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks
- **DO NOT** implement the plan yourself - your role is handoff and monitoring
- The supervisor reads the ratified plan and determines vertical slice structure
- Architect monitors for blockers or escalations

## Follow-up Lifecycle (h1 Reuse)

This handoff (h1: Architect → Supervisor) also occurs after FOLLOWUP_PROPOSAL is ratified. In follow-up context:

- **Storage:** `.git/.aura/handoff/{followup-epic-id}/architect-to-supervisor.md`
- **References:** Include both original URD and FOLLOWUP_URD task IDs
- **Context:** Summary of FOLLOWUP_PROPOSAL ratification and outstanding leaf tasks from original review
- **Next step:** Supervisor creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks, adopting original IMPORTANT/MINOR leaf tasks as dual-parent children
<!-- END GENERATED FROM pasture schema -->
