# Lifecycle hook privacy

Pasture persists payloads delivered through registered lifecycle hooks by
default. Depending on the hook, those payloads may contain prompts, tool inputs,
and file contents. The retained bytes support auditability and lifecycle
interpretation, but they should be treated as sensitive data and protected with
the same access controls and retention practices as the source workspace.

Review this persistence posture before enabling additional hooks. In particular,
do not enable `PreToolUse` until operators understand that its tool input can be
stored in the Pasture database.
