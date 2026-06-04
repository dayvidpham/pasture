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
			Then:      "author the handoff in a HANDOFF Beads task body, linking to the ratified proposal",
			ShouldNot: "hand off without linking to ratified proposal",
		},
		{
			Id:        "arch-handoff-spawn-supervisor",
			Given:     "handoff for the IMPL_PLAN phase",
			When:      "spawning the supervisor",
			Then:      "use TeamCreate to spawn the supervisor as an Opus teammate (workers also Opus), then assign work via SendMessage",
			ShouldNot: "spawn the supervisor as a Task tool subagent or via aura-swarm for the IMPL_PLAN phase",
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
		{
			Id:      "arch-handoff-when-to-use",
			Title:   "When to Use",
			Content: "Plan ratified and user has approved proceeding with implementation.",
		},
		{
			Id:    "arch-handoff-template",
			Title: "Handoff Template",
			Content: "Storage: the handoff is authored directly in the **HANDOFF Beads task body** (no filesystem path; the task body IS the handoff).\n\n" +
				"```" + `markdown` + "\n" +
				"# Handoff: Architect → Supervisor\n" +
				"\n" +
				"## Supervisor Startup\n" +
				"1. Call `Skill(/pasture:supervisor)` to load your role instructions\n" +
				"2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed\n" +
				"3. Read the RATIFIED PROPOSAL and URD with `bd show` commands below\n" +
				"4. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1 types / L2 tests / L3 impl triple is only illustrative)\n" +
				"\n" +
				"## References\n" +
				"- REQUEST: <request-task-id>\n" +
				"- URD: <urd-task-id> (read with `bd show <urd-id>`)\n" +
				"- RATIFIED PROPOSAL: <ratified-proposal-id> (read with `bd show <proposal-id>`)\n" +
				"\n" +
				"## Summary\n" +
				"<1-2 sentence summary of what needs to be implemented>\n" +
				"\n" +
				"## Key Files\n" +
				"<list main files to be created/modified from the ratified plan>\n" +
				"\n" +
				"## Validation Checklist\n" +
				"<validation checklist from the ratified proposal>\n" +
				"\n" +
				"## BDD Acceptance Criteria\n" +
				"<Given/When/Then criteria from the ratified plan>\n" +
				"\n" +
				"## Implementation Notes\n" +
				"<any special considerations, known risks, or constraints>\n" +
				"```",
		},
		{
			Id:    "arch-handoff-steps",
			Title: "Steps",
			Content: "1. Create the HANDOFF Beads task — its body IS the handoff document (use the template above):\n" +
				"   " + "```bash" + "\n" +
				"   bd create --type=task --priority=2 \\\n" +
				"     --title=\"HANDOFF: Architect → Supervisor for REQUEST\" \\\n" +
				"     --description=\"---\n" +
				"   references:\n" +
				"     request: <request-task-id>\n" +
				"     urd: <urd-task-id>\n" +
				"     proposal: <ratified-proposal-id>\n" +
				"   ---\n" +
				"   # Handoff: Architect → Supervisor\n" +
				"   <full handoff body per the template above>\" \\\n" +
				"     --add-label \"pasture:p7-plan:s7-handoff\"\n" +
				"\n" +
				"   bd dep add <request-id> --blocked-by <handoff-id>\n" +
				"   ```" + "\n\n" +
				"2. Launch the supervisor as an **Opus teammate** via TeamCreate (the IMPL_PLAN phase runs as an Agent Team, not aura-swarm):\n" +
				"   " + "```\n" +
				"   TeamCreate({ team_name: \"<epoch>-impl\", ... })          # supervisor + workers as Opus teammates\n" +
				"   # then assign the supervisor its task via SendMessage (see Example Prompt below)\n" +
				"   ```" + "\n\n" +
				"3. Monitor supervisor progress:\n" +
				"   " + "```bash" + "\n" +
				"   # Check beads status\n" +
				"   bd list --status=in_progress\n" +
				"   ```" + "\n\n" +
				"   A supervisor that looks idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.",
		},
		{
			Id:    "arch-handoff-example-prompt",
			Title: "Example Prompt",
			Content: "**CRITICAL:** The SendMessage assignment MUST instruct the supervisor to invoke `/pasture:supervisor` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.\n\n" +
				"```\n" +
				"Start by calling `Skill(/pasture:supervisor)` to load your role instructions.\n" +
				"\n" +
				"Implement the ratified plan for <feature name>.\n" +
				"\n" +
				"## Context\n" +
				"- REQUEST: <request-task-id>\n" +
				"- URD: <urd-task-id> (read with `bd show <urd-id>` for user requirements)\n" +
				"- RATIFIED PROPOSAL: <ratified-proposal-id>\n" +
				"- HANDOFF: <handoff-task-id> (the handoff body — read with `bd show <handoff-id>`)\n" +
				"\n" +
				"## Summary\n" +
				"<1-2 sentence summary of what needs to be implemented>\n" +
				"\n" +
				"## Key Files\n" +
				"<list main files to be created/modified from the ratified plan>\n" +
				"\n" +
				"## Acceptance Criteria\n" +
				"<Given/When/Then criteria from the ratified plan>\n" +
				"\n" +
				"## Reminders\n" +
				"1. Call `Skill(/pasture:supervisor)` FIRST — do not proceed without loading your role\n" +
				"2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed\n" +
				"3. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1/L2/L3 triple is only illustrative); a slice without leaf tasks is undecomposed\n" +
				"4. Read the ratified plan with `bd show <ratified-proposal-id>` and the URD with `bd show <urd-id>`\n" +
				"```\n\n" +
				"Deliver this assignment to the supervisor teammate via SendMessage after TeamCreate:\n\n" +
				"```\n" +
				"SendMessage({\n" +
				"  to: \"supervisor\",\n" +
				"  message: `Start by calling Skill(/pasture:supervisor) to load your role instructions.\n" +
				"\n" +
				"Implement the ratified plan for User Authentication.\n" +
				"\n" +
				"## Context\n" +
				"- REQUEST: project-abc\n" +
				"- URD: project-xyz\n" +
				"- RATIFIED PROPOSAL: project-prop1\n" +
				"- HANDOFF: project-handoff (read with bd show project-handoff)\n" +
				"\n" +
				"## Summary\n" +
				"Add JWT-based authentication with login/logout endpoints and middleware.\n" +
				"\n" +
				"## Key Files\n" +
				"- pkg/auth/jwt.go\n" +
				"- pkg/auth/middleware.go\n" +
				"- cmd/api/auth.go\n" +
				"\n" +
				"## Acceptance Criteria\n" +
				"Given a valid JWT token when accessing protected routes then allow access\n" +
				"Given an expired token when accessing protected routes then return 401\n" +
				"\n" +
				"## Reminders\n" +
				"1. Call Skill(/pasture:supervisor) FIRST\n" +
				"2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed\n" +
				"3. Every slice MUST have leaf tasks (any number; L1/L2/L3 is only illustrative)\n" +
				"4. Read ratified plan: bd show project-prop1 and URD: bd show project-xyz`,\n" +
				"  summary: \"IMPL_PLAN assignment with Beads context\"\n" +
				"})\n" +
				"```",
		},
		{
			Id:    "arch-handoff-teamcreate-notes",
			Title: "Spawning via TeamCreate",
			Content: "- Spawn the supervisor (and the workers it will coordinate) as **Opus** teammates — the IMPL_PLAN phase benefits from the stronger model for decomposition and review.\n" +
				"- Teammates have **zero prior context**: every SendMessage assignment MUST be self-contained (call `Skill(/pasture:supervisor)`, the Beads task IDs, and `bd show` commands to fetch full requirements).\n" +
				"- Do not spawn the supervisor via `aura-swarm` for the IMPL_PLAN phase; aura-swarm remains available for worktree-isolated epics, but the default handoff uses TeamCreate.",
		},
		{
			Id:    "arch-handoff-important",
			Title: "IMPORTANT",
			Content: "- **DO NOT** spawn the supervisor as a Task tool subagent or via `aura-swarm` for the IMPL_PLAN phase — use TeamCreate with an Opus supervisor\n" +
				"- **DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks\n" +
				"- **DO NOT** implement the plan yourself - your role is handoff and monitoring\n" +
				"- **DO NOT** shut down a supervisor that appears idle right after spawn — it is usually running Explore subagents\n" +
				"- The supervisor reads the ratified plan and determines vertical slice structure\n" +
				"- Architect monitors for blockers or escalations",
		},
		{
			Id:    "arch-handoff-followup-lifecycle",
			Title: "Follow-up Lifecycle (h1 Reuse)",
			Content: "This handoff (h1: Architect → Supervisor) also occurs after FOLLOWUP_PROPOSAL is ratified. In follow-up context:\n\n" +
				"- **Storage:** the follow-up handoff is authored in its own HANDOFF Beads task body (no filesystem path)\n" +
				"- **References:** Include both original URD and FOLLOWUP_URD task IDs\n" +
				"- **Context:** Summary of FOLLOWUP_PROPOSAL ratification and the user-DEFER'd UAT items the follow-up addresses\n" +
				"- **Next step:** Supervisor creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks for the follow-up scope",
		},
	},

	Recipes: []RecipeBlock{},
}
