#!/usr/bin/env bash
#
# loadtest.sh — the one documented command for the disposable-store
# load-test harness (epic
# https://github.com/dayvidpham/pasture/01a13ee7-9b63-7cdc-951b-06cda2af55eb).
#
#   bash tools/loadtest.sh [--source PATH] [--tasks N] [knobs] [-- <go test args>]
#
# It resolves the source store (default: the operator's live store, which is
# only ever COPIED, never measured), exports the harness knobs, and runs the
# Go harness against a disposable copy. The harness itself
# (cmd/pastured/loadtest_test.go) takes the consistent copy into a temp dir,
# refuses any non-disposable path, reports the five metrics with date,
# revision and this exact command, and fails loudly past the stated bands.
#
# Knobs (env names the harness reads; flags here are sugar over them):
#
#   --source PATH          store to copy (default: live store when present,
#                          else a synthetic store is built)
#   --synthetic            build the synthetic shape even when a live store
#                          is present (what CI runs)
#   --out DIR              place the sandbox under DIR instead of a mktemp
#                          dir (debugging; implies --keep)
#   --tasks N              synthetic task count, default 600
#   --workers N            concurrent hook workers, default 18
#   --burst-secs N         C=18 burst window, default 3
#   --throughput-secs N    sustained window, default 10
#   --read-iters N         default 30
#   --open-iters N         default 20
#   --report-only          print without failing (exploration, never CI)
#   --keep                 keep the sandbox dir after the run (debugging)
#
# CI: `bash tools/loadtest.sh --tasks 600` needs no operator state — with no
# live store present the harness builds the synthetic shape. Bounded runtime:
# two binary builds plus ~15 s of measured windows plus the read/open loops.
# Run under the Nix shell when Go is not on PATH:
# `nix develop -c bash -lc 'bash tools/loadtest.sh'`.

set -euo pipefail

SOURCE=""
SYNTHETIC=""
OUT=""
TASKS="600"
WORKERS="18"
BURST_SECS="3"
THROUGHPUT_SECS="10"
READ_ITERS="30"
OPEN_ITERS="20"
REPORT_ONLY=""
KEEP=""

usage() {
  sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
}

main() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --source) SOURCE="$2"; shift 2 ;;
      --synthetic) SYNTHETIC="1"; shift ;;
      --out) OUT="$2"; shift 2 ;;
      --tasks) TASKS="$2"; shift 2 ;;
      --workers) WORKERS="$2"; shift 2 ;;
      --burst-secs) BURST_SECS="$2"; shift 2 ;;
      --throughput-secs) THROUGHPUT_SECS="$2"; shift 2 ;;
      --read-iters) READ_ITERS="$2"; shift 2 ;;
      --open-iters) OPEN_ITERS="$2"; shift 2 ;;
      --report-only) REPORT_ONLY="1"; shift ;;
      --keep) KEEP="1"; shift ;;
      -h|--help) usage; return 0 ;;
      --) shift; break ;;
      *) echo "loadtest.sh: unknown flag $1 (see --help)" >&2; return 2 ;;
    esac
  done

  if [[ -n "$SOURCE" && -n "$SYNTHETIC" ]]; then
    echo "loadtest.sh: --source and --synthetic conflict; pick one" >&2
    return 2
  fi

  if ! command -v go >/dev/null 2>&1; then
    echo "loadtest.sh: 'go' is not on PATH; run under the Nix shell:" >&2
    echo "  nix develop -c bash -lc 'bash tools/loadtest.sh'" >&2
    return 2
  fi

  local root
  root="$(cd "$(dirname "$0")/.." && pwd)"

  local sandbox
  if [[ -n "$OUT" ]]; then
    sandbox="$OUT"
    mkdir -p "$sandbox"
    KEEP="1"
  else
    sandbox="$(mktemp -d "${TMPDIR:-/tmp}/pasture-loadtest-XXXXXX")"
  fi
  if [[ -z "$KEEP" ]]; then
    trap 'rm -rf "$sandbox"' EXIT
  else
    echo "loadtest.sh: keeping sandbox $sandbox"
  fi

  local command_line="bash tools/loadtest.sh"
  [[ -n "$SOURCE" ]] && command_line+=" --source $SOURCE"
  [[ -n "$SYNTHETIC" ]] && command_line+=" --synthetic"
  [[ -n "$OUT" ]] && command_line+=" --out $OUT"
  [[ "$TASKS" != "600" ]] && command_line+=" --tasks $TASKS"
  [[ "$WORKERS" != "18" ]] && command_line+=" --workers $WORKERS"
  [[ "$BURST_SECS" != "3" ]] && command_line+=" --burst-secs $BURST_SECS"
  [[ "$THROUGHPUT_SECS" != "10" ]] && command_line+=" --throughput-secs $THROUGHPUT_SECS"
  [[ -n "$REPORT_ONLY" ]] && command_line+=" --report-only"
  if [[ $# -gt 0 ]]; then
    command_line+=" -- $*"
  fi

  (
    cd "$root"
    export PASTURE_LOADTEST_SOURCE="$SOURCE"
    export PASTURE_LOADTEST_FORCE_SYNTHETIC="$SYNTHETIC"
    export PASTURE_LOADTEST_TASKS="$TASKS"
    export PASTURE_LOADTEST_WORKERS="$WORKERS"
    export PASTURE_LOADTEST_BURST_SECS="$BURST_SECS"
    export PASTURE_LOADTEST_THROUGHPUT_SECS="$THROUGHPUT_SECS"
    export PASTURE_LOADTEST_READ_ITERS="$READ_ITERS"
    export PASTURE_LOADTEST_OPEN_ITERS="$OPEN_ITERS"
    export PASTURE_LOADTEST_SANDBOX="$sandbox"
    export PASTURE_LOADTEST_COMMAND="$command_line"
    if [[ -n "$REPORT_ONLY" ]]; then
      export PASTURE_LOADTEST_REPORT_ONLY="1"
    fi
    # The handed-down sandbox is where the harness builds its disposable
    # store (requireDisposableDB proves it stays there); --keep preserves it
    # for inspection, otherwise the trap removes it on exit.
    go test -count=1 -run TestDisposableStoreLoadHarnessIsTheRegressionTripwire -v ./cmd/pastured/ "$@"
  )
}

main "$@"
