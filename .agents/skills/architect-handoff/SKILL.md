---
name: architect-handoff
description: Create handoff document and transfer to supervisor
---

# Architect: Handoff to Supervisor

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
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
- Then: delegate the supervisor as the implementation-plan owner (workers also on the frontier tier), then give it a self-contained assignment
- Should not: spawn the supervisor without a self-contained assignment, or via aura-swarm, for the IMPL_PLAN phase

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

Select the current registered author with "${EPOCH_REC}" whoami; when neither the header nor PASTURE_AUTHOR names one, it lists the registered agents. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## When to Use

Plan ratified and user has approved proceeding with implementation.

## Handoff Template

Storage: the handoff is authored directly in the **HANDOFF Pasture task body** (no filesystem path; the task body IS the handoff).

```markdown
# Handoff: Architect → Supervisor

## Supervisor Startup
1. Call `invoke_skill(/pasture:supervisor)` to load your role instructions
2. Spawn ephemeral Explore subagents with the delegate_assignment tool when codebase exploration is needed
3. Read the RATIFIED PROPOSAL and URD with `"${EPOCH_REC}" show` commands below
4. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1 types / L2 tests / L3 impl triple is only illustrative)

## References
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with `"${EPOCH_REC}" show "${URD_ID_URI}"`)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}" (read with `"${EPOCH_REC}" show "${RATIFIED_PROPOSAL_ID_URI}"`)

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
   HANDOFF_ID_URI=$("${EPOCH_REC}" create --kind task --title "HANDOFF: Architect → Supervisor for REQUEST" --phase handoff --priority 2 \
     --body="---
   references:
     request: "${REQUEST_ID_URI}"
     urd: "${URD_ID_URI}"
     proposal: "${RATIFIED_PROPOSAL_ID_URI}"
   ---
   # Handoff: Architect → Supervisor
   <full handoff body per the template above>")
   "${EPOCH_REC}" label "$HANDOFF_ID_URI" pasture:p7-plan:s7-handoff

   ```

2. Delegate the supervisor with the delegate_assignment operation (the IMPL_PLAN phase runs under the supervisor, not aura-swarm):
   ```
   delegate the implementation-plan owner, then give it the self-contained assignment below
   ```

3. Monitor supervisor progress:
   ```bash
   # Check pasture status
   "${EPOCH_REC}" query --status=in_progress
   ```

   A supervisor that looks idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.

## Example Prompt

**CRITICAL:** The send_assignment_message assignment MUST instruct the supervisor to invoke `/pasture:supervisor` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.

```
Start by calling `invoke_skill(/pasture:supervisor)` to load your role instructions.

Implement the ratified plan for <feature name>.

## Context
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with `"${EPOCH_REC}" show "${URD_ID_URI}"` for user requirements)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (the handoff body — read with `"${EPOCH_REC}" show "${HANDOFF_ID_URI}"`)

## Summary
<1-2 sentence summary of what needs to be implemented>

## Key Files
<list main files to be created/modified from the ratified plan>

## Acceptance Criteria
<Given/When/Then criteria from the ratified plan>

## Reminders
1. Call `invoke_skill(/pasture:supervisor)` FIRST — do not proceed without loading your role
2. Spawn ephemeral Explore subagents with the delegate_assignment tool when codebase exploration is needed
3. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1/L2/L3 triple is only illustrative); a slice without leaf tasks is undecomposed
4. Read the ratified plan with `"${EPOCH_REC}" show "${RATIFIED_PROPOSAL_ID_URI}"` and the URD with `"${EPOCH_REC}" show "${URD_ID_URI}"`
```

Deliver this assignment to the supervisor when you delegate it:

```
send_assignment_message({
  to: "supervisor",
  message: `Start by calling invoke_skill(/pasture:supervisor) to load your role instructions.

Implement the ratified plan for User Authentication.

## Context
- REQUEST: ${REQUEST_ID_URI}
- URD: "${URD_ID_URI}"
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (read with "${EPOCH_REC}" show "${HANDOFF_ID_URI}")

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
1. Call invoke_skill(/pasture:supervisor) FIRST
2. Spawn ephemeral Explore subagents with the delegate_assignment tool when codebase exploration is needed
3. Every slice MUST have leaf tasks (any number; L1/L2/L3 is only illustrative)
4. Read ratified plan: "${EPOCH_REC}" show "${RATIFIED_PROPOSAL_ID_URI}" and URD: "${EPOCH_REC}" show "${URD_ID_URI}"`,
  summary: "IMPL_PLAN assignment with Pasture context"
})
```

## Delegation Notes

- Delegate the supervisor (and the workers it will coordinate) as **T1 frontier** assignments — the IMPL_PLAN phase benefits from the frontier tier for decomposition and review.
- A delegated assignment has **zero prior context**: every assignment MUST be self-contained (call `invoke_skill(/pasture:supervisor)`, the Pasture task IDs, and `"${EPOCH_REC}" show` commands to fetch full requirements).
- Do not spawn the supervisor via `aura-swarm` for the IMPL_PLAN phase; the deprecated orchestrator must not be invoked.

## IMPORTANT

- **DO NOT** spawn the supervisor via `aura-swarm` for the IMPL_PLAN phase — delegate it with the delegate_assignment operation
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
