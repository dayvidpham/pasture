#!/usr/bin/env bash
# PreToolUse hook: block destructive git operations for worker agents on
# shared worktrees.
#
# Why this exists:
#   The /pasture:worker role forbids destructive git ops on shared
#   worktrees (git reset --hard, git checkout HEAD -- <path>, git stash
#   pop/apply, git clean -f, git branch -D). The rule has been broken in
#   real parallel-worker sessions with real data loss. This hook is the
#   runtime backstop to that role guidance.
#
# Threat model — READ THIS:
#   This is a BACKSTOP against *accidental* destructive git commands by a
#   cooperative worker. It is NOT a security sandbox and does NOT stop a
#   worker that is deliberately trying to evade it. Known NOT detected,
#   by design (defeating them needs a real shell parser, and a worker who
#   reaches for them is no longer "accidental"):
#     - shell evasion:   eval "git reset --hard", $(git reset --hard),
#                        `git reset --hard`, bash -c "...", printf ... | sh
#     - obfuscation:     g\it reset --hard, git${IFS}reset, 'git' 'reset' '--hard'
#     - indirection:     aliases, shell functions, or a different `git` in PATH
#   Detected: the plain forms plus the common accidental variants — a global
#   option prefix (git -C <path>, git -c k=v, --git-dir=..., --no-pager) and
#   unbundled/any-order flags (git clean -f -d, git branch -d -D).
#
# Activation (currently LATENT):
#   Enforces only when PASTURE_ROLE == "worker" is in the session environment.
#   No launcher exports PASTURE_ROLE yet, so until it is plumbed this hook is a
#   silent no-op. See https://github.com/dayvidpham/pasture/issues/31.
#
# Behaviour:
#   - Reads the PreToolUse JSON event from stdin.
#   - Activates only when PASTURE_ROLE == "worker" AND tool_name == "Bash".
#     For other roles or tools, exits 0 silently (no-op).
#   - When the Bash command matches a forbidden pattern, prints a
#     plain-language error to stderr and exits 2 (blocking error).
#   - Escape hatch: set BYPASS_GIT_DISCIPLINE=1 in the agent session env
#     to skip enforcement for a single command (intended for "I'm alone on
#     this branch" cases — must be justified in a bd comment).
#
# Hooks must NOT block on transient failure: if jq is missing or the input
# cannot be parsed, the hook exits 0 (allow) rather than 2 (block). Failing
# open is correct for a backstop — the role guidance is the primary authority.

set -uo pipefail

# ── Escape hatch ──────────────────────────────────────────────────────────────
if [[ "${BYPASS_GIT_DISCIPLINE:-0}" == "1" ]]; then
    exit 0
fi

# ── Role gating (cheap; checked first so non-worker sessions are a true
#    zero-subprocess no-op — no stdin read, no jq) ───────────────────────────────
if [[ "${PASTURE_ROLE:-}" != "worker" ]]; then
    exit 0
fi

# jq is required to parse the event. If unavailable, fail open (allow).
if ! command -v jq >/dev/null 2>&1; then
    exit 0
fi

# ── Read PreToolUse event from stdin ──────────────────────────────────────────
input=$(cat)

tool_name=$(printf '%s' "$input" | jq -r '.tool_name // empty' 2>/dev/null || true)
if [[ "$tool_name" != "Bash" ]]; then
    exit 0
fi

command_str=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null || true)
if [[ -z "$command_str" ]]; then
    exit 0
fi

# ── Pattern detection ─────────────────────────────────────────────────────────
# Each pattern is a regex matched against the full command string. We anchor on
# (^|[ \t;&|]) so a quoted substring like "echo 'git reset --hard'" does not
# match while real invocations and pipelines ("true && git reset --hard") do.
#
# GIT is a reusable prefix: "git" optionally followed by global options
# (-C <path>, -c k=v, --git-dir=..., --work-tree=..., --no-pager, -p) before
# the subcommand — this catches the common accidental "git -C <path> reset
# --hard" form that plain adjacency misses.
GIT='(^|[[:space:]]|;|&|\|)git([[:space:]]+-{1,2}[A-Za-z][A-Za-z-]*(=[^[:space:]]*|[[:space:]]+[^-;&|[:space:]][^[:space:]]*)?)*[[:space:]]+'

matched_label=""

# _match sets matched_label on the first hit (and returns immediately after).
_match() {
    local label="$1"
    local pattern="$2"
    if [[ -z "$matched_label" ]] && [[ "$command_str" =~ $pattern ]]; then
        matched_label="$label"
    fi
}

# git reset --hard (any args)
_match "git reset --hard"             "${GIT}reset[[:space:]]+--hard($|[[:space:]])"

# git checkout HEAD -- <path>  (also covers `git checkout HEAD~N -- ...`)
_match "git checkout HEAD -- <path>"  "${GIT}checkout[[:space:]]+HEAD[~^0-9]*[[:space:]]+--[[:space:]]"

# git restore --source=HEAD <path>  (allow intervening flags, e.g. --worktree)
_match "git restore --source=HEAD"    "${GIT}restore[[:space:]]+[^;&|]*(--source=HEAD|--source[[:space:]]+HEAD)"

# git stash pop / apply
_match "git stash pop"                "${GIT}stash[[:space:]]+pop($|[[:space:]])"
_match "git stash apply"              "${GIT}stash[[:space:]]+apply($|[[:space:]])"

# git clean with -f/--force (deletes untracked files — incl. peer WIP);
# matches bundled or separate flags in any order (-fd, -f -d, -d -f, --force).
_match "git clean -f"                 "${GIT}clean[[:space:]]+[^;&|]*(-[a-zA-Z]*f[a-zA-Z]*|--force)($|[[:space:]]|[^;&|]*$)"

# git branch -D (capital D = force-delete); bundled or separate, any order.
_match "git branch -D"                "${GIT}branch[[:space:]]+[^;&|]*(-[a-zA-Z]*D[a-zA-Z]*|--delete[[:space:]]+[^;&|]*--force)"

# git rebase --abort (soft-forbidden: dangerous when peers depend on the branch;
# use the BYPASS escape hatch when alone on the branch).
_match "git rebase --abort"           "${GIT}rebase[[:space:]]+--abort($|[[:space:]])"

if [[ -z "$matched_label" ]]; then
    # Benign git command (status, log, diff, add by name, agent-commit, etc.) — allow.
    exit 0
fi

# ── Block: emit a plain-language error and exit 2 ─────────────────────────────
# Format follows the project plain-language error convention: short top line,
# vertical-aligned labels (Problem / Reason / Where / Impact / How to fix),
# numbered fix steps with concrete commands.

cat >&2 <<EOF
Error: This destructive git operation is blocked for worker agents.

  Problem:   You tried to run a '${matched_label}'-style command, which would
             discard uncommitted work in this worktree — including changes by
             other workers running in parallel against the same tree.
  Reason:    Workers share the worktree. Destructive git operations here have
             already caused real data-loss incidents in parallel-worker sessions.
  Where:     Worker pre-tool-use git-discipline hook
             (hooks/scripts/git-discipline.sh).
  Impact:    Your command was blocked. Nothing in the worktree changed.
             You still need to decide how to proceed (see "How to fix").
  How to fix:
    1. If you wanted to discard your own staged changes only, use the
       safe per-file form (does not affect peer worker work):
         git restore --staged <specific-files>
       or, to drop your unstaged edits to a specific file:
         git restore <specific-files>
    2. If unexpected files in the worktree are blocking your fix, they are
       almost certainly peer-worker work-in-progress. Do NOT clean them up.
       Raise the concern to your team-lead and wait for direction — post a
       coordination comment on your slice task so the escalation is recorded:
         bd comments add <your-task-id> "Blocked: peer-worker changes present in <files>; raising to team-lead for coordination."
    3. If you genuinely need to run this command (for example, resolving a
       merge conflict on a branch where you are the only worker), bypass this
       hook for that single command by setting BYPASS_GIT_DISCIPLINE=1 in the
       environment, and record in a 'bd comments add' why the bypass was needed:
         BYPASS_GIT_DISCIPLINE=1 <your-git-command>
    4. The full rationale and the complete list of forbidden operations are
       documented in this hook's header and the /pasture:worker role guidance.

EOF

exit 2
