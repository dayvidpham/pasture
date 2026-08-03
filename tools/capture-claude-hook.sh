#!/usr/bin/env bash
#
# Capture an authentic Claude Code hook payload as a test fixture.
#
# Claude Code pipes the event JSON to a hook command on stdin. This script is
# that command: it writes the bytes to a fixture file named after the event, and
# writes a provenance record beside it.
#
# WHY AUTHENTIC CAPTURE MATTERS
#
#   A fixture generated from the host descriptor cannot falsify that descriptor —
#   asserting a payload conforms to the shape it was generated from is a
#   tautology. Only bytes that came from a real host can reveal a field the
#   descriptor got wrong, an unexpected key, or a type surprise.
#
# WHY REDACTION IS STILL SAFE
#
#   Redaction here is VALUE-ONLY: it rewrites path values and nothing else. The
#   field names, nesting, types and any unexpected keys survive untouched, so the
#   fixture retains its full power to falsify the descriptor.
#
#   Structural redaction would destroy that and is forbidden: never remove a
#   field, rename a key, change a type, or drop a null. If a payload cannot be
#   made shareable by value substitution alone, do not commit it.
#
# USAGE
#
#   1. Point every event you want at this script in ~/.claude/settings.json:
#
#        { "hooks": { "SessionStart": [ { "hooks": [ { "type": "command",
#            "command": "PASTURE_CAPTURE_DIR=/tmp/pasture-capture bash /abs/path/tools/capture-claude-hook.sh" } ] } ] } }
#
#      One registration per event. The script names its own output from the
#      payload's hook_event_name, so the same command works for all of them.
#
#   2. Use Claude Code normally to trigger the events:
#
#        SessionStart / SessionEnd    start and exit a session
#        PreToolUse / PostToolUse     any tool call
#        PostToolUseFailure           read a nonexistent file
#        PostToolBatch                several tool calls in one message
#        PreCompact / PostCompact     /compact
#        Elicitation/-Result          requires an MCP server that elicits
#
#   3. Move the pairs into
#        internal/lifecycle/ingress/claude/testdata/fixtures/
#
# This script always exits 0. A capture hook must never fail a user's session,
# and for blocking events a non-zero exit is read by the host as "deny".

set -uo pipefail

out="${PASTURE_CAPTURE_DIR:-}"
if [ -z "$out" ]; then
    echo "capture-claude-hook: PASTURE_CAPTURE_DIR is not set; nothing captured" >&2
    exit 0
fi
mkdir -p "$out" 2>/dev/null || {
    echo "capture-claude-hook: cannot create $out; nothing captured" >&2
    exit 0
}

# Exact bytes. Never round-trip the payload through a shell variable —
# $(cat) strips trailing newlines, and the digest is over exact bytes.
raw="$(mktemp)"
cat >"$raw"

if ! command -v jq >/dev/null 2>&1; then
    echo "capture-claude-hook: jq not found; raw payload left at $raw" >&2
    exit 0
fi

event="$(jq -r '.hook_event_name // empty' <"$raw" 2>/dev/null)"
if [ -z "$event" ]; then
    echo "capture-claude-hook: payload has no hook_event_name; left at $raw" >&2
    exit 0
fi

# Resolve the host version before choosing either output path. The version is
# part of the sibling stem so captures from different host versions cannot
# overwrite one another.
version="${CLAUDE_CODE_VERSION:-}"
if [ -z "$version" ] && command -v claude >/dev/null 2>&1; then
    version="$(claude --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
fi
if [ -z "$version" ]; then
    version="UNKNOWN-SET-CLAUDE_CODE_VERSION"
fi

case "$event" in
    *[![:alnum:]_-]*)
        echo "capture-claude-hook: hook_event_name contains path-unsafe characters; left at $raw" >&2
        exit 0
        ;;
esac
case "$version" in
    *[![:alnum:]._-]*)
        echo "capture-claude-hook: Claude Code version contains path-unsafe characters; left at $raw" >&2
        exit 0
        ;;
esac

snake_event="$(printf '%s' "$event" | sed -E 's/([a-z0-9])([A-Z])/\1_\2/g' | tr '[:upper:]' '[:lower:]')"
version_stem="${version//./_}"
stem="${snake_event}_${version_stem}"
fixture="$out/${stem}.json"
provenance="$out/${stem}.provenance.json"

# ---------------------------------------------------------------------------
# Redaction: value-only, declared, mechanical.
#
# transcript_path dash-encodes the project path, so $HOME appears in two
# encodings and both must be rewritten. Session UUIDs are left alone — they are
# real, harmless, and part of the shape under test.
# ---------------------------------------------------------------------------
REDACTION_RULE="home-path-v1"
home_slash="${HOME:-/home/unset}"
home_dash="$(printf '%s' "$home_slash" | tr '/' '-')"

sed -e "s|${home_slash}|/home/user|g" \
    -e "s|${home_dash}|-home-user|g" \
    <"$raw" >"$fixture"
rm -f "$raw"

# Digest is computed over the committed bytes, which is what
# acceptance.CaptureProvenance.ValidateFixture re-computes and compares.
sum="$(sha256sum <"$fixture" | cut -d' ' -f1)"

jq -n \
    --arg origin "authentic-capture" \
    --arg harness "claude-code" \
    --arg version "${version:-UNKNOWN-SET-CLAUDE_CODE_VERSION}" \
    --arg source "tools/capture-claude-hook.sh" \
    --arg digest "sha256:${sum}" \
    --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg redaction "$REDACTION_RULE" \
    --arg event "$event" \
    '{origin: $origin, harness: $harness, harnessVersion: $version,
      captureSource: $source, rawFileDigest: $digest, capturedAt: $at,
      redaction: $redaction, event: $event}' \
    >"$provenance"

echo "capture-claude-hook: captured $event -> $fixture" >&2
exit 0
