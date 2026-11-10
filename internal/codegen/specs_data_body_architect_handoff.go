// Body content for the architect-handoff skill SKILL.md.
// Ported from aura-plugins/skills/architect-handoff/SKILL.md.
package codegen

var architectHandoffBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-7-handoff)** <- Phase 7",

	Behaviors: []BehaviorSpec{
		{
			Id:        "arch-handoff-link-proposal",
			Given:     "ratified PROPOSAL-N task",
			When:      "handing off",
			Then:      "author the handoff in a HANDOFF Pasture task body, linking to the ratified proposal",
			ShouldNot: "hand off without linking to ratified proposal",
		},
		{
			Id:        "arch-handoff-spawn-supervisor",
			Given:     "handoff for the IMPL_PLAN phase",
			When:      "spawning the supervisor",
			Then:      "delegate the supervisor as the implementation-plan owner (workers also on the frontier tier), then give it a self-contained assignment",
			ShouldNot: "spawn the supervisor without a self-contained assignment, or via aura-swarm, for the IMPL_PLAN phase",
		},
		{
			Id:        "arch-handoff-supervisor-not-idle",
			Given:     "a freshly spawned supervisor",
			When:      "it dispatches Explore subagents and appears idle",
			Then:      "let it work — an apparently-idle supervisor is usually running Explore subagents to map the codebase before decomposing slices",
			ShouldNot: "shut down or restart a supervisor that looks idle at the start of the IMPL_PLAN phase",
		},
		{
			Id:        "arch-handoff-no-impl-tasks",
			Given:     "implementation planning",
			When:      "handing off",
			Then:      "let supervisor create vertical slice tasks",
			ShouldNot: "create implementation tasks as architect",
		},
	},

	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "arch-handoff-when-to-use",
			Title:   "When to Use",
			Content: "Plan ratified and user has approved proceeding with implementation.",
		},
		{
			Id:    "arch-handoff-template",
			Title: "Handoff Template",
			Content: `Storage: the handoff is authored directly in the **HANDOFF Pasture task body** (no filesystem path; the task body IS the handoff).

` + "```" + `markdown
# Handoff: Architect → Supervisor

## Supervisor Startup
1. Call ` + "`" + `[[op:invoke_skill]](/pasture:supervisor)` + "`" + ` to load your role instructions
2. Spawn ephemeral Explore subagents with the [[op:delegate_assignment]] tool when codebase exploration is needed
3. Read the RATIFIED PROPOSAL and URD with ` + "`" + `[[rec:show]]` + "`" + ` commands below
4. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1 types / L2 tests / L3 impl triple is only illustrative)

## References
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with ` + "`" + `[[rec:show]] "${URD_ID_URI}"` + "`" + `)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}" (read with ` + "`" + `[[rec:show]] "${RATIFIED_PROPOSAL_ID_URI}"` + "`" + `)

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
` + "```" + ``,
		},
		{
			Id:    "arch-handoff-steps",
			Title: "Steps",
			Content: `1. Create the HANDOFF Pasture task — its body IS the handoff document (use the template above):
   ` + "```" + `bash
   HANDOFF_ID_URI=$([[rec:create]] --kind task --title "HANDOFF: Architect → Supervisor for REQUEST" --phase handoff --priority 2 \
     --body="---
   references:
     request: "${REQUEST_ID_URI}"
     urd: "${URD_ID_URI}"
     proposal: "${RATIFIED_PROPOSAL_ID_URI}"
   ---
   # Handoff: Architect → Supervisor
   <full handoff body per the template above>")
   [[rec:label]] "$HANDOFF_ID_URI" pasture:p7-plan:s7-handoff

   ` + "```" + `

2. Delegate the supervisor with the [[op:delegate_assignment]] operation (the IMPL_PLAN phase runs under the supervisor, not aura-swarm):
   ` + "```" + `
   delegate the implementation-plan owner, then give it the self-contained assignment below
   ` + "```" + `

3. Monitor supervisor progress:
   ` + "```" + `bash
   # Check pasture status
   [[rec:query]] --status=in_progress
   ` + "```" + `

   A supervisor that looks idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.`,
		},
		{
			Id:    "arch-handoff-example-prompt",
			Title: "Example Prompt",
			Content: `**CRITICAL:** The [[op:send_assignment_message]] assignment MUST instruct the supervisor to invoke ` + "`" + `/pasture:supervisor` + "`" + ` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.

` + "```" + `
Start by calling ` + "`" + `[[op:invoke_skill]](/pasture:supervisor)` + "`" + ` to load your role instructions.

Implement the ratified plan for <feature name>.

## Context
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with ` + "`" + `[[rec:show]] "${URD_ID_URI}"` + "`" + ` for user requirements)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (the handoff body — read with ` + "`" + `[[rec:show]] "${HANDOFF_ID_URI}"` + "`" + `)

## Summary
<1-2 sentence summary of what needs to be implemented>

## Key Files
<list main files to be created/modified from the ratified plan>

## Acceptance Criteria
<Given/When/Then criteria from the ratified plan>

## Reminders
1. Call ` + "`" + `[[op:invoke_skill]](/pasture:supervisor)` + "`" + ` FIRST — do not proceed without loading your role
2. Spawn ephemeral Explore subagents with the [[op:delegate_assignment]] tool when codebase exploration is needed
3. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1/L2/L3 triple is only illustrative); a slice without leaf tasks is undecomposed
4. Read the ratified plan with ` + "`" + `[[rec:show]] "${RATIFIED_PROPOSAL_ID_URI}"` + "`" + ` and the URD with ` + "`" + `[[rec:show]] "${URD_ID_URI}"` + "`" + `
` + "```" + `

Deliver this assignment to the supervisor when you delegate it:

` + "```" + `
[[op:send_assignment_message]]({
  to: "supervisor",
  message: ` + "`" + `Start by calling [[op:invoke_skill]](/pasture:supervisor) to load your role instructions.

Implement the ratified plan for User Authentication.

## Context
- REQUEST: ${REQUEST_ID_URI}
- URD: "${URD_ID_URI}"
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (read with [[rec:show]] "${HANDOFF_ID_URI}")

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
1. Call [[op:invoke_skill]](/pasture:supervisor) FIRST
2. Spawn ephemeral Explore subagents with the [[op:delegate_assignment]] tool when codebase exploration is needed
3. Every slice MUST have leaf tasks (any number; L1/L2/L3 is only illustrative)
4. Read ratified plan: [[rec:show]] "${RATIFIED_PROPOSAL_ID_URI}" and URD: [[rec:show]] "${URD_ID_URI}"` + "`" + `,
  summary: "IMPL_PLAN assignment with Pasture context"
})
` + "```" + ``,
		},
		{
			Id:    "arch-handoff-delegation-notes",
			Title: "Delegation Notes",
			Content: `- Delegate the supervisor (and the workers it will coordinate) as **T1 frontier** assignments — the IMPL_PLAN phase benefits from the frontier tier for decomposition and review.
- A delegated assignment has **zero prior context**: every assignment MUST be self-contained (call ` + "`" + `[[op:invoke_skill]](/pasture:supervisor)` + "`" + `, the Pasture task IDs, and ` + "`" + `[[rec:show]]` + "`" + ` commands to fetch full requirements).
- Do not spawn the supervisor via ` + "`" + `aura-swarm` + "`" + ` for the IMPL_PLAN phase; the deprecated orchestrator must not be invoked.`,
		},
		{
			Id:    "arch-handoff-important",
			Title: "IMPORTANT",
			Content: "- **DO NOT** spawn the supervisor via `aura-swarm` for the IMPL_PLAN phase — delegate it with the [[op:delegate_assignment]] operation\n" +
				"- **DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks\n" +
				"- **DO NOT** implement the plan yourself - your role is handoff and monitoring\n" +
				"- **DO NOT** shut down a supervisor that appears idle right after spawn — it is usually running Explore subagents\n" +
				"- The supervisor reads the ratified plan and determines vertical slice structure\n" +
				"- Architect monitors for blockers or escalations",
		},
		{
			Id:      "arch-handoff-followup-lifecycle",
			Title:   "Follow-up Lifecycle (h1 Reuse)",
			Content: "This handoff (h1: Architect → Supervisor) also occurs after FOLLOWUP_PROPOSAL is ratified. In follow-up context:\n\n- **Storage:** the follow-up handoff is authored in its own HANDOFF Pasture task body (no filesystem path)\n- **References:** Include both original URD and FOLLOWUP_URD task IDs\n- **Context:** Summary of FOLLOWUP_PROPOSAL ratification and the user-DEFER'd UAT items the follow-up addresses\n- **Next step:** Supervisor creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks for the follow-up scope",
		},
	},

	Recipes: []RecipeBlock{},
}
