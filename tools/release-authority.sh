#!/usr/bin/env bash
# Decide whether a GitHub Release may be cut for one exact main commit.
#
# CI is the only place the required correctness and performance suites run.
# This script proves that the CI result being consumed belongs to the exact
# commit and is complete, instead of trusting a workflow conclusion string.
# Any missing, failed, cancelled, skipped, stale, foreign or partial evidence
# exits non-zero and publishes nothing.
#
# Environment:
#   REPO               owner/name of the current repository (required)
#   EVENT_NAME         github.event_name (workflow_run or workflow_dispatch)
#   RUN_ID             github.event.workflow_run.id (workflow_run only)
#   RUN_SHA            github.event.workflow_run.head_sha (workflow_run only)
#   DISPATCH_SHA       github.sha (workflow_dispatch only)
#   DISPATCH_REF       github.ref (workflow_dispatch only; must be refs/heads/main)
#   REQUIRED_WORKFLOW  CI workflow file name, e.g. ci.yml (required)
#   REQUIRED_JOBS      JSON array of job names that must exist and be green
#   GITHUB_OUTPUT      when set, outputs are appended there; stdout otherwise
#
# Outputs on success: sha, version, released=true|false.
set -euo pipefail

: "${REPO:?REPO is required}"
: "${REQUIRED_WORKFLOW:?REQUIRED_WORKFLOW is required}"
: "${REQUIRED_JOBS:?REQUIRED_JOBS is required}"

out="${GITHUB_OUTPUT:-/dev/stdout}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "::error::$*" >&2; exit 1; }

if [ "${EVENT_NAME:-}" = "workflow_run" ]; then
  sha="${RUN_SHA:-}"
  run_id="${RUN_ID:-}"
else
  sha="${DISPATCH_SHA:-}"
  run_id=""
  [ "${DISPATCH_REF:-}" = "refs/heads/main" ] \
    || fail "manual dispatch must target main, got '${DISPATCH_REF:-}'"
fi
printf '%s' "$sha" | grep -Eq '^[0-9a-f]{40}$' \
  || fail "commit '$sha' is not an exact 40-character lowercase SHA"

if [ -z "$run_id" ]; then
  # Manual recovery: the newest completed push run of the CI workflow for this
  # exact main commit. Selecting the newest run for the commit means an older
  # success cannot hide a newer failure. A commit with no completed run refuses.
  run_id="$(gh api "repos/$REPO/actions/workflows/$REQUIRED_WORKFLOW/runs?branch=main&event=push&status=completed&head_sha=$sha&per_page=100" \
    --jq "[.workflow_runs[] | select(.head_sha == \"$sha\")] | sort_by(.created_at) | last | .id // empty")"
  [ -n "$run_id" ] || fail "no completed CI run on main for commit $sha"
fi

gh api "repos/$REPO/actions/runs/$run_id" > "$work/run.json"
jq -e --arg repo "$REPO" --arg sha "$sha" --arg path ".github/workflows/$REQUIRED_WORKFLOW" '
  .id != null
  and .head_sha == $sha
  and .event == "push"
  and .head_branch == "main"
  and .status == "completed"
  and .conclusion == "success"
  and .path == $path
  and (.head_repository.full_name // "") == $repo
  and (.repository.full_name // "") == $repo
' "$work/run.json" >/dev/null \
  || fail "CI run $run_id is not a successful push-to-main run of $REQUIRED_WORKFLOW for $sha"

gh api "repos/$REPO/actions/runs/$run_id/jobs?filter=latest&per_page=100" > "$work/jobs.json"
jq -e '(.jobs | length) > 0' "$work/jobs.json" >/dev/null \
  || fail "CI run $run_id reported no jobs"
missing="$(jq -r --argjson required "$REQUIRED_JOBS" '($required - [.jobs[].name]) | join(", ")' "$work/jobs.json")"
[ -z "$missing" ] || fail "CI run $run_id is missing required job(s): $missing"
jq -e 'all(.jobs[]; .status == "completed" and .conclusion == "success")' "$work/jobs.json" >/dev/null \
  || fail "CI run $run_id has a job that is not green"

version="$(gh api -H 'Accept: application/vnd.github.raw' \
  "repos/$REPO/contents/.claude-plugin/plugin.json?ref=$sha" | jq -r .version)"
printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
  || fail "plugin version '$version' at $sha is not x.y.z"

# matching-refs answers 200 with an empty list when the tag is absent, so a
# transient API failure cannot be mistaken for "no tag yet".
refs="$(gh api "repos/$REPO/git/matching-refs/tags/v$version")"
present="$(jq -r --arg ref "refs/tags/v$version" '[.[] | select(.ref == $ref)] | length' <<<"$refs")"
if [ "$present" = "0" ]; then
  echo "released=true" >> "$out"
else
  tagged="$(gh api "repos/$REPO/commits/v$version" --jq .sha)"
  if [ "$tagged" = "$sha" ]; then
    echo "v$version already released at $sha"
    echo "released=false" >> "$out"
  else
    # A later, non-bump commit still carries the released version. Comparing
    # the tag commit against this one keeps that ordinary case a no-op instead
    # of a red release, while a tag that moved off this commit's history still
    # refuses.
    relation="$(gh api "repos/$REPO/compare/$tagged...$sha" --jq .status)"
    [ "$relation" = "ahead" ] \
      || fail "tag v$version at $tagged is not an ancestor of $sha ($relation); refusing to move it"
    tagged_version="$(gh api -H 'Accept: application/vnd.github.raw' \
      "repos/$REPO/contents/.claude-plugin/plugin.json?ref=$tagged" | jq -r .version)"
    [ "$tagged_version" = "$version" ] \
      || fail "tag v$version at $tagged carries plugin version '$tagged_version', not '$version'; refusing"
    echo "v$version already released at ancestor $tagged; this commit did not bump it"
    echo "released=false" >> "$out"
  fi
fi
echo "sha=$sha" >> "$out"
echo "version=$version" >> "$out"
echo "CI run $run_id is authoritative for $sha (v$version)"
