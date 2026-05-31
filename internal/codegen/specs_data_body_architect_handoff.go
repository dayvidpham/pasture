// Body content for the architect-handoff skill SKILL.md.
// Ported from aura-plugins/skills/architect-handoff/SKILL.md.
package codegen

var architectHandoffBody = SkillBody{
	Preamble: "**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-7-handoff)** <- Phase 7",

	Behaviors: []BehaviorSpec{
		{
			ID:        "arch-handoff-link-proposal",
			Given:     "ratified PROPOSAL-N task",
			When:      "handing off",
			Then:      "create handoff document and HANDOFF task, linking to ratified proposal",
			ShouldNot: "hand off without linking to ratified proposal",
		},
		{
			ID:        "arch-handoff-spawn-supervisor",
			Given:     "handoff",
			When:      "spawning supervisor",
			Then:      "use `aura-swarm start --swarm-mode intree --role supervisor` or `aura-swarm start --epic <id>`",
			ShouldNot: "spawn supervisor as Task tool subagent",
		},
		{
			ID:        "arch-handoff-no-impl-tasks",
			Given:     "implementation planning",
			When:      "handing off",
			Then:      "let supervisor create vertical slice tasks",
			ShouldNot: "create implementation tasks as architect",
		},
	},

	Sections: []ProseSection{
		{
			ID:      "arch-handoff-when-to-use",
			Title:   "When to Use",
			Content: "Plan ratified and user has approved proceeding with implementation.",
		},
		{
			ID:    "arch-handoff-template",
			Title: "Handoff Template",
			Content: "Storage: " + "`.git/.aura/handoff/{request-task-id}/architect-to-supervisor.md`" + "\n\n" +
				"```" + `markdown` + "\n" +
				"# Handoff: Architect → Supervisor\n" +
				"\n" +
				"## Supervisor Startup\n" +
				"1. Call `Skill(/aura:supervisor)` to load your role instructions\n" +
				"2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed\n" +
				"3. Read the RATIFIED PROPOSAL and URD with `bd show` commands below\n" +
				"4. Every vertical slice MUST have leaf tasks (L1: types, L2: tests, L3: impl)\n" +
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
			ID:    "arch-handoff-steps",
			Title: "Steps",
			Content: "1. Create the handoff document:\n" +
				"   " + "```bash" + "\n" +
				"   mkdir -p .git/.aura/handoff/<request-task-id>/\n" +
				"   # Write architect-to-supervisor.md with the template above\n" +
				"   ```" + "\n\n" +
				"2. Create HANDOFF Beads task:\n" +
				"   " + "```bash" + "\n" +
				"   bd create --type=task --priority=2 \\\n" +
				"     --title=\"HANDOFF: Architect → Supervisor for REQUEST\" \\\n" +
				"     --description=\"---\n" +
				"   references:\n" +
				"     request: <request-task-id>\n" +
				"     urd: <urd-task-id>\n" +
				"     proposal: <ratified-proposal-id>\n" +
				"   ---\n" +
				"   Handoff from architect to supervisor. See handoff document at\n" +
				"   .git/.aura/handoff/<request-task-id>/architect-to-supervisor.md\" \\\n" +
				"     --add-label \"aura:p7-plan:s7-handoff\"\n" +
				"\n" +
				"   bd dep add <request-id> --blocked-by <handoff-id>\n" +
				"   ```" + "\n\n" +
				"3. Launch supervisor:\n" +
				"   " + "```bash" + "\n" +
				"   # In-place mode (long-running supervisor in tmux session)\n" +
				"   aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt \"...\"\n" +
				"\n" +
				"   # Or worktree mode (epic-based workflow)\n" +
				"   aura-swarm start --epic <id>\n" +
				"   ```" + "\n\n" +
				"4. Monitor supervisor progress:\n" +
				"   " + "```bash" + "\n" +
				"   # Check beads status\n" +
				"   bd list --status=in_progress\n" +
				"\n" +
				"   # Attach to supervisor session\n" +
				"   aura-swarm attach <epic-id-or-session-id>\n" +
				"   ```",
		},
		{
			ID:    "arch-handoff-example-prompt",
			Title: "Example Prompt",
			Content: "**CRITICAL:** The prompt MUST instruct the supervisor to invoke `/aura:supervisor` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.\n\n" +
				"```\n" +
				"Start by calling `Skill(/aura:supervisor)` to load your role instructions.\n" +
				"\n" +
				"Implement the ratified plan for <feature name>.\n" +
				"\n" +
				"## Context\n" +
				"- REQUEST: <request-task-id>\n" +
				"- URD: <urd-task-id> (read with `bd show <urd-id>` for user requirements)\n" +
				"- RATIFIED PROPOSAL: <ratified-proposal-id>\n" +
				"- HANDOFF: <handoff-task-id>\n" +
				"- Handoff document: .git/.aura/handoff/<request-task-id>/architect-to-supervisor.md\n" +
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
				"1. Call `Skill(/aura:supervisor)` FIRST — do not proceed without loading your role\n" +
				"2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed\n" +
				"3. Every vertical slice MUST have leaf tasks (L1: types, L2: tests, L3: impl) — a slice without leaf tasks is undecomposed\n" +
				"4. Read the ratified plan with `bd show <ratified-proposal-id>` and the URD with `bd show <urd-id>`\n" +
				"```\n\n" +
				"Pass the prompt to the script:\n\n" +
				"```bash\n" +
				"aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt \"$(cat <<'EOF'\n" +
				"Start by calling Skill(/aura:supervisor) to load your role instructions.\n" +
				"\n" +
				"Implement the ratified plan for User Authentication.\n" +
				"\n" +
				"## Context\n" +
				"- REQUEST: project-abc\n" +
				"- URD: project-xyz\n" +
				"- RATIFIED PROPOSAL: project-prop1\n" +
				"- Handoff document: .git/.aura/handoff/project-abc/architect-to-supervisor.md\n" +
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
				"1. Call Skill(/aura:supervisor) FIRST\n" +
				"2. Spawn ephemeral Explore subagents via Task tool when codebase exploration is needed\n" +
				"3. Every slice MUST have leaf tasks (L1/L2/L3)\n" +
				"4. Read ratified plan: bd show project-prop1 and URD: bd show project-xyz\n" +
				"EOF\n" +
				")\"\n" +
				"```",
		},
		{
			ID:    "arch-handoff-script-options",
			Title: "Script Options",
			Content: "```bash\n" +
				"aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt \"...\"             # Launch supervisor\n" +
				"aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt \"...\" --dry-run   # Preview without launching\n" +
				"aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt-file prompt.md    # Read prompt from file\n" +
				"```",
		},
		{
			ID:    "arch-handoff-important",
			Title: "IMPORTANT",
			Content: "- **DO NOT** spawn supervisor as a Task tool subagent - use `aura-swarm start`\n" +
				"- **DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks\n" +
				"- **DO NOT** implement the plan yourself - your role is handoff and monitoring\n" +
				"- The supervisor reads the ratified plan and determines vertical slice structure\n" +
				"- Architect monitors for blockers or escalations",
		},
		{
			ID:    "arch-handoff-followup-lifecycle",
			Title: "Follow-up Lifecycle (h1 Reuse)",
			Content: "This handoff (h1: Architect → Supervisor) also occurs after FOLLOWUP_PROPOSAL is ratified. In follow-up context:\n\n" +
				"- **Storage:** " + "`.git/.aura/handoff/{followup-epic-id}/architect-to-supervisor.md`" + "\n" +
				"- **References:** Include both original URD and FOLLOWUP_URD task IDs\n" +
				"- **Context:** Summary of FOLLOWUP_PROPOSAL ratification and outstanding leaf tasks from original review\n" +
				"- **Next step:** Supervisor creates FOLLOWUP_IMPL_PLAN and FOLLOWUP_SLICE-N tasks, adopting original IMPORTANT/MINOR leaf tasks as dual-parent children",
		},
	},

	Recipes: []RecipeBlock{},
}
