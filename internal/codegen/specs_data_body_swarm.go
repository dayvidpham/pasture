// Body content for the swarm command SKILL.md.
// Ported from aura-plugins/skills/swarm/SKILL.md.
package codegen

var swarmBody = SkillBody{
	Preamble: "Orchestrate Claude agent sessions in two modes:\n" +
		"- **Worktree mode** (default): Isolated git worktrees per epic, with beads task discovery and rich prompt generation.\n" +
		"- **Intree mode**: In-place parallel agents (replaces `aura-parallel`). No worktree, prompt required.",

	Behaviors: []BehaviorSpec{
		{
			Id:        "swarm-epic-worktree",
			Given:     "an epic needs implementation",
			When:      "launching agents",
			Then:      "use `aura-swarm start --epic <id>` to create an isolated worktree",
			ShouldNot: "launch long-running workers as Task tool subagents",
		},
		{
			Id:        "swarm-intree-longrunning",
			Given:     "a long-running agent is needed in-place",
			When:      "launching",
			Then:      "use `aura-swarm start --swarm-mode intree --role <role> -n 1 --prompt \"...\"`",
			ShouldNot: "spawn long-running agents as Task tool subagents",
		},
		{
			Id:        "swarm-task-assignment",
			Given:     "multiple workers are needed in-place",
			When:      "distributing tasks",
			Then:      "use `--task-id` to assign one task per worker",
			ShouldNot: "launch workers without task assignments",
		},
		{
			Id:        "swarm-reviewer-subagents",
			Given:     "reviewers are needed",
			When:      "spawning",
			Then:      "use Task tool subagents or TeamCreate instead",
			ShouldNot: "use `aura-swarm start` for reviewer rounds",
		},
		{
			Id:        "swarm-status-check",
			Given:     "agents are running",
			When:      "checking progress",
			Then:      "use `aura-swarm status` to see all active sessions",
			ShouldNot: "try to inspect tmux sessions manually",
		},
		{
			Id:        "swarm-cleanup",
			Given:     "an epic is complete",
			When:      "cleaning up",
			Then:      "use `aura-swarm cleanup <id>` or `aura-swarm cleanup --done`",
			ShouldNot: "manually delete worktrees or branches",
		},
	},

	Sections: []ProseSection{
		{
			Id:    "swarm-when-to-use",
			Title: "When to Use",
			Content: `- Starting a new epic implementation (` + "`aura-swarm start --epic <id>`" + `)
- Launching parallel in-place agents (` + "`aura-swarm start --swarm-mode intree -n N --prompt \"...\"`" + `)
- Checking status of running agent sessions (` + "`aura-swarm status`" + `)
- Attaching to a running session (` + "`aura-swarm attach`" + `)
- Merging completed work back to the epic branch (` + "`aura-swarm merge`" + `)
- Launching code review rounds (` + "`aura-swarm review`" + `)
- Cleaning up finished worktrees (` + "`aura-swarm cleanup`" + `)`,
		},
		{
			Id:    "swarm-branch-model",
			Title: "Branch Model (worktree mode)",
			Content: "```\n" +
				"main\n" +
				" └── epic/<epic-id>                 (aura-swarm creates this branch + worktree)\n" +
				"       ├── agent/<task-id-1>         (Claude's Agent Teams creates these)\n" +
				"       ├── agent/<task-id-2>\n" +
				"       └── agent/<task-id-3>\n" +
				"```",
		},
		{
			Id:      "swarm-commands",
			Title:   "Commands",
			Content: "",
			Subsections: []ProseSection{
				{
					Id:    "swarm-cmd-worktree",
					Title: "Worktree Mode (default)",
					Content: "```bash\n" +
						"# Start an epic (creates worktree, gathers beads context, launches Claude)\n" +
						"aura-swarm start --epic <epic-id>\n" +
						"aura-swarm start --epic <epic-id> --model opus\n" +
						"aura-swarm start --epic <epic-id> --restart\n\n" +
						"# Window mode (agents accumulate in one tmux session)\n" +
						"aura-swarm start --epic <epic-id> --tmux-dest window -n 2\n\n" +
						"# With additional instructions appended to auto-generated prompt\n" +
						"aura-swarm start --epic <epic-id> --prompt-addon \"Focus on tests first\"\n" +
						"```",
				},
				{
					Id:    "swarm-cmd-intree",
					Title: "Intree Mode (replaces aura-parallel)",
					Content: "```bash\n" +
						"# Launch a single supervisor\n" +
						"aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt \"...\"\n\n" +
						"# Launch 3 workers with task distribution (1:1 mapping)\n" +
						"aura-swarm start --swarm-mode intree --role worker -n 3 \\\n" +
						"  --task-id impl-001 --task-id impl-002 --task-id impl-003 \\\n" +
						"  --prompt \"Implement the assigned task\"\n\n" +
						"# Launch with skill invocation\n" +
						"aura-swarm start --swarm-mode intree --role reviewer -n 3 \\\n" +
						"  --skill aura:reviewer-review-plan --prompt \"Review plan aura-xyz\"\n\n" +
						"# Dry run (preview commands without executing)\n" +
						"aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt \"...\" --dry-run\n" +
						"```",
				},
				{
					Id:    "swarm-cmd-management",
					Title: "Management",
					Content: "```bash\n" +
						"# Check status of all running agent sessions\n" +
						"aura-swarm status\n\n" +
						"# Attach to a running session's tmux\n" +
						"aura-swarm attach <epic-id-or-session-id>\n\n" +
						"# Stop a running session (keeps worktree)\n" +
						"aura-swarm stop <epic-id-or-session-id>\n\n" +
						"# Merge agent branches back to epic branch\n" +
						"aura-swarm merge <epic-id>\n\n" +
						"# Launch code review round for an epic\n" +
						"aura-swarm review --epic <epic-id>\n\n" +
						"# Clean up a specific epic's worktree\n" +
						"aura-swarm cleanup <epic-id>\n\n" +
						"# Clean up all completed epics\n" +
						"aura-swarm cleanup --done\n\n" +
						"# Clean up everything (including in-progress)\n" +
						"aura-swarm cleanup --all\n" +
						"```",
				},
			},
		},
		{
			Id:    "swarm-options",
			Title: "Options",
			Content: `| Flag | Description |
|------|-------------|
| ` + "`--epic`" + ` | Epic beads ID (required for worktree mode, optional for intree) |
| ` + "`--swarm-mode`" + ` | ` + "`worktree`" + ` (default) or ` + "`intree`" + ` |
| ` + "`--tmux-dest`" + ` | ` + "`session`" + ` (default) or ` + "`window`" + ` (agents accumulate in one tmux session) |
| ` + "`-n/--njobs`" + ` | Number of parallel agents (default: 1) |
| ` + "`--role`" + ` | Agent role: ` + "`architect`" + `, ` + "`supervisor`" + `, ` + "`reviewer`" + `, ` + "`worker`" + ` (default: supervisor) |
| ` + "`--model`" + ` | Claude model: ` + "`sonnet`" + `, ` + "`opus`" + `, ` + "`haiku`" + ` (default: sonnet) |
| ` + "`--prompt`" + ` | Prompt text (required for intree mode) |
| ` + "`--prompt-file`" + ` | Read prompt from file (mutually exclusive with ` + "`--prompt`" + `) |
| ` + "`--prompt-addon`" + ` | Additional instructions appended to auto-generated prompt (worktree mode) |
| ` + "`--skill`" + ` | Skill to invoke at session start |
| ` + "`--task-id`" + ` | Beads task IDs (repeatable). Intree: distributed 1:1 across agents |
| ` + "`--permission-mode`" + ` | ` + "`default`" + `, ` + "`acceptEdits`" + `, ` + "`bypassPermissions`" + `, ` + "`plan`" + ` (default: acceptEdits) |
| ` + "`--restart`" + ` | Stop existing session and start fresh |
| ` + "`--dry-run`" + ` | Preview commands without executing |
| ` + "`--attach`" + ` | Attach to first session after launching |
| ` + "`--session-name`" + ` | Override tmux session name |
| ` + "`--working-dir`" + ` | Working directory (default: git root) |`,
		},
		{
			Id:    "swarm-prerequisites",
			Title: "Prerequisites",
			Content: "- " + "`aura-swarm`" + " must be on PATH (installed via Nix or symlinked)\n" +
				"- " + "`tmux`" + " and " + "`claude`" + " must be available\n" +
				"- **Worktree mode**: " + "`git`" + " and " + "`bd`" + " (beads CLI) must be available; must be in a git repo with beads initialized\n" +
				"- **Intree mode**: " + "`--prompt`" + " or " + "`--prompt-file`" + " required; " + "`bd`" + " only needed if " + "`--epic`" + " is provided",
		},
		{
			Id:    "swarm-migration",
			Title: "Migration from aura-parallel",
			Content: "`aura-parallel` is deprecated. All commands translate directly:\n\n" +
				"```bash\n" +
				"# Old:\n" +
				"aura-parallel --role worker -n 3 --prompt \"...\"\n\n" +
				"# New:\n" +
				"aura-swarm start --swarm-mode intree --role worker -n 3 --prompt \"...\"\n" +
				"```\n\n" +
				"The `aura-parallel` command still works as a thin wrapper but prints a deprecation warning.",
		},
	},

	Recipes: []RecipeBlock{},
}
