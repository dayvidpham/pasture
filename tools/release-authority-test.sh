#!/usr/bin/env bash
# Contract test for tools/release-authority.sh. It runs the real script with a
# stub `gh` and canned API records; it never contacts GitHub. Covers the trust
# denials (failed run, pull-request run, foreign repository, missing or skipped
# required job, moved tag, unavailable API) and the accepted paths (successful
# main run, already-tagged commit, manual dispatch picking the newest run).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
authority="$here/release-authority.sh"
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/bin" "$root/fixtures"
fx="$root/fixtures"
sha="0123456789abcdef0123456789abcdef01234567"
other="89abcdef0123456789abcdef0123456789abcdef"
required='["Build & Test (1.26)","Lint","Codegen Drift"]'

cat > "$root/bin/gh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[ "${1:-}" = "api" ] || { echo "unexpected gh invocation: $*" >&2; exit 2; }
shift
jqexpr=""; raw=0; url=""
while [ $# -gt 0 ]; do
  case "$1" in
    --jq) jqexpr="${2:-}"; shift 2 ;;
    -H) case "${2:-}" in *raw*) raw=1 ;; esac; shift 2 ;;
    -*) echo "unexpected gh flag: $1" >&2; exit 2 ;;
    *) url="$1"; shift ;;
  esac
done
fx="${FIXTURES:?}"
case "$url" in
  */actions/workflows/*/runs\?*) src="$fx/runs.json" ;;
  */actions/runs/*/jobs\?*) src="$fx/jobs.json" ;;
  */actions/runs/*) src="$fx/run.json" ;;
  */compare/*) src="$fx/compare.json" ;;
  */contents/.claude-plugin/plugin.json*)
    case "$url" in
      *"ref=$FIXTURE_HEAD_SHA"*) src="$fx/plugin.json" ;;
      *"ref=$FIXTURE_TAG_SHA"*) src="$fx/plugin-tagged.json" ;;
      *) src="$fx/plugin.json" ;;
    esac ;;
  */git/matching-refs/tags/*)
    [ ! -f "$fx/api-fail" ] || { echo "HTTP 502 Bad Gateway" >&2; exit 1; }
    src="$fx/tag-refs.json" ;;
  */commits/v*)
    [ -f "$fx/tag-commit.json" ] || { echo "HTTP 404 Not Found" >&2; exit 1; }
    src="$fx/tag-commit.json" ;;
  *) echo "unexpected gh url: $url" >&2; exit 2 ;;
esac
if [ "$raw" = 1 ]; then cat "$src"; exit 0; fi
if [ -n "$jqexpr" ]; then jq -r "$jqexpr" "$src"; exit $?; fi
cat "$src"
STUB
chmod +x "$root/bin/gh"

reset_fixtures() {
  rm -f "$fx"/*
  printf '{"version":"0.0.21"}\n' > "$fx/plugin.json"
  printf '{"version":"0.0.21"}\n' > "$fx/plugin-tagged.json"
  printf '{"status":"ahead"}\n' > "$fx/compare.json"
  printf '[]\n' > "$fx/tag-refs.json"
  cat > "$fx/run.json" <<JSON
{"id":42,"head_sha":"$sha","event":"push","head_branch":"main","status":"completed",
 "conclusion":"success","path":".github/workflows/ci.yml",
 "head_repository":{"full_name":"dayvidpham/pasture"},
 "repository":{"full_name":"dayvidpham/pasture"}}
JSON
  cat > "$fx/jobs.json" <<'JSON'
{"jobs":[
 {"name":"Build & Test (1.26)","status":"completed","conclusion":"success"},
 {"name":"Lint","status":"completed","conclusion":"success"},
 {"name":"Codegen Drift","status":"completed","conclusion":"success"}]}
JSON
  cat > "$fx/runs.json" <<JSON
{"workflow_runs":[
 {"id":1,"head_sha":"$sha","created_at":"2026-10-18T00:00:00Z"},
 {"id":2,"head_sha":"$sha","created_at":"2026-10-18T01:00:00Z"}]}
JSON
}

failures=0
run_case() {
  local name="$1" want_exit="$2" want_released="$3" want_text="$4"
  local out="$root/out-$name" log="$root/log-$name"
  : > "$out"
  local code=0
  PATH="$root/bin:$PATH" FIXTURES="$fx" GITHUB_OUTPUT="$out" \
    REPO=dayvidpham/pasture EVENT_NAME=workflow_run RUN_ID=42 RUN_SHA="$sha" \
    DISPATCH_SHA="$sha" DISPATCH_REF=refs/heads/main REQUIRED_WORKFLOW=ci.yml REQUIRED_JOBS="$required" \
    FIXTURE_HEAD_SHA="$sha" FIXTURE_TAG_SHA="$other" \
    bash "$authority" > "$log" 2>&1 || code=$?
  local ok=1
  [ "$code" = "$want_exit" ] || ok=0
  if [ -n "$want_released" ]; then
    grep -qx "released=$want_released" "$out" || ok=0
  fi
  if [ -n "$want_text" ]; then
    grep -qF "$want_text" "$log" || ok=0
  fi
  if [ "$ok" = 1 ]; then
    echo "PASS $name (exit $code)"
  else
    echo "FAIL $name (exit $code, want $want_exit)"
    sed 's/^/  /' "$log" "$out" | tail -12
    failures=$((failures + 1))
  fi
}

reset_fixtures
run_case successful-main 0 true "is authoritative"

reset_fixtures
jq '.conclusion = "failure"' "$fx/run.json" > "$fx/t" && mv "$fx/t" "$fx/run.json"
run_case failed-run 1 "" "not a successful push-to-main run"

reset_fixtures
jq '.event = "pull_request" | .head_branch = "feature"' "$fx/run.json" > "$fx/t" && mv "$fx/t" "$fx/run.json"
run_case pull-request-run 1 "" "not a successful push-to-main run"

reset_fixtures
jq '.head_repository.full_name = "someone/else"' "$fx/run.json" > "$fx/t" && mv "$fx/t" "$fx/run.json"
run_case foreign-repository 1 "" "not a successful push-to-main run"

reset_fixtures
jq '.jobs |= map(select(.name != "Codegen Drift"))' "$fx/jobs.json" > "$fx/t" && mv "$fx/t" "$fx/jobs.json"
run_case missing-required-job 1 "" "missing required job(s): Codegen Drift"

reset_fixtures
jq '.jobs |= map(if .name == "Codegen Drift" then .conclusion = "skipped" else . end)' "$fx/jobs.json" > "$fx/t" && mv "$fx/t" "$fx/jobs.json"
run_case skipped-required-job 1 "" "has a job that is not green"

reset_fixtures
printf '[{"ref":"refs/tags/v0.0.21"}]\n' > "$fx/tag-refs.json"
printf '{"sha":"%s"}\n' "$sha" > "$fx/tag-commit.json"
run_case already-tagged-here 0 false "already released"

reset_fixtures
printf '[{"ref":"refs/tags/v0.0.21"}]\n' > "$fx/tag-refs.json"
printf '{"sha":"%s"}\n' "$other" > "$fx/tag-commit.json"
printf '{"status":"diverged"}\n' > "$fx/compare.json"
run_case diverged-tag 1 "" "is not an ancestor"

reset_fixtures
printf '[{"ref":"refs/tags/v0.0.21"}]\n' > "$fx/tag-refs.json"
printf '{"sha":"%s"}\n' "$other" > "$fx/tag-commit.json"
printf '{"status":"behind"}\n' > "$fx/compare.json"
run_case behind-tag 1 "" "is not an ancestor"

reset_fixtures
printf '[{"ref":"refs/tags/v0.0.21"}]\n' > "$fx/tag-refs.json"
printf '{"sha":"%s"}\n' "$other" > "$fx/tag-commit.json"
jq '.version = "0.0.20"' "$fx/plugin-tagged.json" > "$fx/t" && mv "$fx/t" "$fx/plugin-tagged.json"
run_case tagged-version-mismatch 1 "" "carries plugin version"

reset_fixtures
printf '[{"ref":"refs/tags/v0.0.21"}]\n' > "$fx/tag-refs.json"
printf '{"sha":"%s"}\n' "$other" > "$fx/tag-commit.json"
run_case released-on-ancestor 0 false "did not bump it"

reset_fixtures
touch "$fx/api-fail"
run_case tag-lookup-unavailable 1 "" ""

reset_fixtures
printf '{"workflow_runs":[{"id":9,"head_sha":"%s","created_at":"2026-10-18T01:00:00Z"}]}\n' "$other" > "$fx/runs.json"
PATH="$root/bin:$PATH" FIXTURES="$fx" GITHUB_OUTPUT="$root/out-dispatch-none" \
  REPO=dayvidpham/pasture EVENT_NAME=workflow_dispatch RUN_ID="" RUN_SHA="" \
  DISPATCH_SHA="$sha" DISPATCH_REF=refs/heads/main REQUIRED_WORKFLOW=ci.yml REQUIRED_JOBS="$required" \
  FIXTURE_HEAD_SHA="$sha" FIXTURE_TAG_SHA="$other" \
  bash "$authority" > "$root/log-dispatch-none" 2>&1 \
  && code=0 || code=$?
if [ "$code" = 1 ] && grep -qF "no completed CI run on main" "$root/log-dispatch-none"; then
  echo "PASS dispatch-no-run (exit $code)"
else
  echo "FAIL dispatch-no-run (exit $code)"; failures=$((failures + 1))
fi

reset_fixtures
PATH="$root/bin:$PATH" FIXTURES="$fx" GITHUB_OUTPUT="$root/out-dispatch-newest" \
  REPO=dayvidpham/pasture EVENT_NAME=workflow_dispatch RUN_ID="" RUN_SHA="" \
  DISPATCH_SHA="$sha" DISPATCH_REF=refs/heads/main REQUIRED_WORKFLOW=ci.yml REQUIRED_JOBS="$required" \
  FIXTURE_HEAD_SHA="$sha" FIXTURE_TAG_SHA="$other" \
  bash "$authority" > "$root/log-dispatch-newest" 2>&1 \
  && code=0 || code=$?
if [ "$code" = 0 ] && grep -qF "CI run 2 is authoritative" "$root/log-dispatch-newest" \
   && grep -qx "released=true" "$root/out-dispatch-newest"; then
  echo "PASS dispatch-newest (exit $code)"
else
  echo "FAIL dispatch-newest (exit $code)"; failures=$((failures + 1))
fi

# The CI workflow name and job names are a contract with release.yml. Renaming
# a CI job, changing the workflow name, or bumping the Go matrix must fail here,
# at PR time, instead of at release time.
ci="$here/../.github/workflows/ci.yml"
release="$here/../.github/workflows/release.yml"
contract=0
grep -qx 'name: CI' "$ci" || { echo "FAIL ci.yml no longer names the workflow CI"; contract=1; }
grep -qF 'workflows: ["CI"]' "$release" || { echo "FAIL release.yml no longer triggers on CI"; contract=1; }
grep -qF "$required" "$release" || { echo "FAIL release.yml REQUIRED_CI_JOBS differs from this test"; contract=1; }
while IFS= read -r job; do
  if grep -qF "name: $job" "$ci"; then continue; fi
  base="${job%% (*}"
  version="${job##* (}"; version="${version%)}"
  # The matrix value must live in the same job block as the name. A bare file
  # grep would be satisfied by unrelated go-version settings in other jobs.
  block="$(awk -v want="    name: $base" '
    $0 == want { grab = 1 }
    grab && /^  [^ ]/ { exit }
    grab { print }
  ' "$ci")"
  if [ -n "$block" ] && printf '%s\n' "$block" | grep -qF "\"$version\""; then
    if [ "$base" = "Build & Test" ] && ! printf '%s\n' "$block" | grep -qF "make test-race"; then
      echo "FAIL '$job' no longer runs make test-race"
      contract=1
    fi
    continue
  fi
  echo "FAIL required job '$job' is not rendered by ci.yml"
  contract=1
done < <(jq -r '.[]' <<<"$required")
if [ "$contract" -eq 0 ]; then
  echo "PASS ci contract"
else
  failures=$((failures + 1))
fi

reset_fixtures
PATH="$root/bin:$PATH" FIXTURES="$fx" GITHUB_OUTPUT="$root/out-dispatch-branch" \
  REPO=dayvidpham/pasture EVENT_NAME=workflow_dispatch RUN_ID="" RUN_SHA="" \
  DISPATCH_SHA="$sha" DISPATCH_REF=refs/heads/other REQUIRED_WORKFLOW=ci.yml \
  REQUIRED_JOBS="$required" FIXTURE_HEAD_SHA="$sha" FIXTURE_TAG_SHA="$other" \
  bash "$authority" > "$root/log-dispatch-branch" 2>&1 \
  && code=0 || code=$?
if [ "$code" = 1 ] && grep -qF "must target main" "$root/log-dispatch-branch"; then
  echo "PASS dispatch-non-main (exit $code)"
else
  echo "FAIL dispatch-non-main (exit $code)"; failures=$((failures + 1))
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures release-authority case(s) failed" >&2
  exit 1
fi
echo "release-authority: all cases passed"
