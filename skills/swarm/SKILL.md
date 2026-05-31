# Swarm — Unified Agent Orchestration

<!-- BEGIN GENERATED FROM aura schema -->
**Command:** `aura:swarm` — Launch worktree-based or intree agent workflows using aura-swarm

Orchestrate Claude agent sessions in two modes:
- **Worktree mode** (default): Isolated git worktrees per epic, with beads task discovery and rich prompt generation.
- **Intree mode**: In-place parallel agents (replaces `aura-parallel`). No worktree, prompt required.

**[swarm-epic-worktree]**
- Given: an epic needs implementation
- When: launching agents
- Then: use `aura-swarm start --epic <id>` to create an isolated worktree
- Should not: launch long-running workers as Task tool subagents

**[swarm-intree-longrunning]**
- Given: a long-running agent is needed in-place
- When: launching
- Then: use `aura-swarm start --swarm-mode intree --role <role> -n 1 --prompt "..."`
- Should not: spawn long-running agents as Task tool subagents

**[swarm-task-assignment]**
- Given: multiple workers are needed in-place
- When: distributing tasks
- Then: use `--task-id` to assign one task per worker
- Should not: launch workers without task assignments

**[swarm-reviewer-subagents]**
- Given: reviewers are needed
- When: spawning
- Then: use Task tool subagents or TeamCreate instead
- Should not: use `aura-swarm start` for reviewer rounds

**[swarm-status-check]**
- Given: agents are running
- When: checking progress
- Then: use `aura-swarm status` to see all active sessions
- Should not: try to inspect tmux sessions manually

**[swarm-cleanup]**
- Given: an epic is complete
- When: cleaning up
- Then: use `aura-swarm cleanup <id>` or `aura-swarm cleanup --done`
- Should not: manually delete worktrees or branches

## When to Use

- Starting a new epic implementation (`aura-swarm start --epic <id>`)
- Launching parallel in-place agents (`aura-swarm start --swarm-mode intree -n N --prompt "..."`)
- Checking status of running agent sessions (`aura-swarm status`)
- Attaching to a running session (`aura-swarm attach`)
- Merging completed work back to the epic branch (`aura-swarm merge`)
- Launching code review rounds (`aura-swarm review`)
- Cleaning up finished worktrees (`aura-swarm cleanup`)

## Branch Model (worktree mode)

```
main
 └── epic/<epic-id>                 (aura-swarm creates this branch + worktree)
       ├── agent/<task-id-1>         (Claude's Agent Teams creates these)
       ├── agent/<task-id-2>
       └── agent/<task-id-3>
```

## Commands



### Worktree Mode (default)

```bash
# Start an epic (creates worktree, gathers beads context, launches Claude)
aura-swarm start --epic <epic-id>
aura-swarm start --epic <epic-id> --model opus
aura-swarm start --epic <epic-id> --restart

# Window mode (agents accumulate in one tmux session)
aura-swarm start --epic <epic-id> --tmux-dest window -n 2

# With additional instructions appended to auto-generated prompt
aura-swarm start --epic <epic-id> --prompt-addon "Focus on tests first"
```

### Intree Mode (replaces aura-parallel)

```bash
# Launch a single supervisor
aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt "..."

# Launch 3 workers with task distribution (1:1 mapping)
aura-swarm start --swarm-mode intree --role worker -n 3 \
  --task-id impl-001 --task-id impl-002 --task-id impl-003 \
  --prompt "Implement the assigned task"

# Launch with skill invocation
aura-swarm start --swarm-mode intree --role reviewer -n 3 \
  --skill aura:reviewer-review-plan --prompt "Review plan aura-xyz"

# Dry run (preview commands without executing)
aura-swarm start --swarm-mode intree --role supervisor -n 1 --prompt "..." --dry-run
```

### Management

```bash
# Check status of all running agent sessions
aura-swarm status

# Attach to a running session's tmux
aura-swarm attach <epic-id-or-session-id>

# Stop a running session (keeps worktree)
aura-swarm stop <epic-id-or-session-id>

# Merge agent branches back to epic branch
aura-swarm merge <epic-id>

# Launch code review round for an epic
aura-swarm review --epic <epic-id>

# Clean up a specific epic's worktree
aura-swarm cleanup <epic-id>

# Clean up all completed epics
aura-swarm cleanup --done

# Clean up everything (including in-progress)
aura-swarm cleanup --all
```

## Options

| Flag | Description |
|------|-------------|
| `--epic` | Epic beads ID (required for worktree mode, optional for intree) |
| `--swarm-mode` | `worktree` (default) or `intree` |
| `--tmux-dest` | `session` (default) or `window` (agents accumulate in one tmux session) |
| `-n/--njobs` | Number of parallel agents (default: 1) |
| `--role` | Agent role: `architect`, `supervisor`, `reviewer`, `worker` (default: supervisor) |
| `--model` | Claude model: `sonnet`, `opus`, `haiku` (default: sonnet) |
| `--prompt` | Prompt text (required for intree mode) |
| `--prompt-file` | Read prompt from file (mutually exclusive with `--prompt`) |
| `--prompt-addon` | Additional instructions appended to auto-generated prompt (worktree mode) |
| `--skill` | Skill to invoke at session start |
| `--task-id` | Beads task IDs (repeatable). Intree: distributed 1:1 across agents |
| `--permission-mode` | `default`, `acceptEdits`, `bypassPermissions`, `plan` (default: acceptEdits) |
| `--restart` | Stop existing session and start fresh |
| `--dry-run` | Preview commands without executing |
| `--attach` | Attach to first session after launching |
| `--session-name` | Override tmux session name |
| `--working-dir` | Working directory (default: git root) |

## Prerequisites

- `aura-swarm` must be on PATH (installed via Nix or symlinked)
- `tmux` and `claude` must be available
- **Worktree mode**: `git` and `bd` (beads CLI) must be available; must be in a git repo with beads initialized
- **Intree mode**: `--prompt` or `--prompt-file` required; `bd` only needed if `--epic` is provided

## Migration from aura-parallel

`aura-parallel` is deprecated. All commands translate directly:

```bash
# Old:
aura-parallel --role worker -n 3 --prompt "..."

# New:
aura-swarm start --swarm-mode intree --role worker -n 3 --prompt "..."
```

The `aura-parallel` command still works as a thin wrapper but prints a deprecation warning.
<!-- END GENERATED FROM aura schema -->
