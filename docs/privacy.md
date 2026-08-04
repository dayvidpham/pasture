# Lifecycle hook privacy

Pasture persists payloads delivered through registered lifecycle hooks by
default. Depending on the hook, those payloads may contain prompts, tool inputs,
and file contents. The retained bytes support auditability and lifecycle
interpretation, but they should be treated as sensitive data and protected with
the same access controls and retention practices as the source workspace.

Review this persistence posture before enabling additional hooks. In particular,
do not enable `PreToolUse` until operators understand that its tool input can be
stored in the Pasture database.

## Exact provider fixture boundary

Exact-payload fixture permission is provider- and record-specific. The OpenCode
1.18.10 work may import only two user-reviewed records from the authentic
callback-object capture: record 1 (`session.created`) and record 6 (the first
`read` `tool.execute.before`). OpenCode invokes an in-process plugin with objects,
so these are exact JSONL bytes serialized by the capture plugin, not a distinct
native wire-byte stream.

The two reviewed Codex 0.146.0 candidates (`SessionStart` and `PreToolUse`) are
reserved for the later Codex milestone and are not part of the current OpenCode
import. No clearance extends to any other lifecycle payload, transcript, tool
response, final message, database, cache, model catalog, plugin cache, or
authentication file. A negative scan for common credential and private-key
signatures is useful evidence, but it is not proof that arbitrary secrets are
absent.
