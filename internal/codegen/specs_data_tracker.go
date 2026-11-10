// Tracker backend contract: the closed operation set, the side-effect-free
// probe, and the EPOCH.md record that pins one backend for an epoch.
//
// This file is the one home of the native Pasture recipes. Authored skill prose
// never names a backend: it calls the [[rec]] marker, which resolves to the
// runtime adapter entry point trackerEntryPoint ("${EPOCH_REC}"). The adapter
// reads EPOCH.md and dispatches to the recorded backend.
//
// Requirements: R21 (Pasture optional; probe once; no mid-epoch switching).
// Amendment A-3: the IR contract gains zero kinds; tracker calls go through one
// entry point.
package codegen

import (
	"fmt"
	"sort"
	"strings"
)

// TrackerBackend is the closed set of tracker backends an epoch may record.
type TrackerBackend string

const (
	// TrackerBackendPasture means the binary is capable and the store is usable.
	TrackerBackendPasture TrackerBackend = "pasture"
	// TrackerBackendLedger means no usable Pasture binary or store exists; the
	// epoch keeps its records in the markdown/JSONL ledger.
	TrackerBackendLedger TrackerBackend = "ledger"
	// TrackerBackendError means the store exists but cannot be used. The epoch
	// diagnoses the failure and stops before the first mutation; it does not
	// fall back to the ledger, because a store that exists may hold earlier
	// epochs.
	TrackerBackendError TrackerBackend = "error"
)

var canonicalTrackerBackends = [...]TrackerBackend{
	TrackerBackendPasture,
	TrackerBackendLedger,
	TrackerBackendError,
}

// AllTrackerBackends returns a fresh defensive copy of the closed backend set.
func AllTrackerBackends() []TrackerBackend {
	return append([]TrackerBackend(nil), canonicalTrackerBackends[:]...)
}

// IsValid reports whether the backend is a registered member of the closed set.
func (b TrackerBackend) IsValid() bool {
	for _, candidate := range canonicalTrackerBackends {
		if b == candidate {
			return true
		}
	}
	return false
}

// TrackerOpId names one operation in the closed tracker vocabulary.
type TrackerOpId string

const (
	TrackerOpCreate TrackerOpId = "create"
	TrackerOpLabel  TrackerOpId = "label"
	TrackerOpBlock  TrackerOpId = "block"
	TrackerOpNote   TrackerOpId = "note"
	TrackerOpShow   TrackerOpId = "show"
	TrackerOpQuery  TrackerOpId = "query"
	TrackerOpReady  TrackerOpId = "ready"
	TrackerOpStatus TrackerOpId = "status"
	TrackerOpClose  TrackerOpId = "close"
	TrackerOpWhoami TrackerOpId = "whoami"
)

var canonicalTrackerOpIds = [...]TrackerOpId{
	TrackerOpCreate,
	TrackerOpLabel,
	TrackerOpBlock,
	TrackerOpNote,
	TrackerOpShow,
	TrackerOpQuery,
	TrackerOpReady,
	TrackerOpStatus,
	TrackerOpClose,
	TrackerOpWhoami,
}

// AllTrackerOpIds returns a fresh defensive copy of the closed operation set,
// in canonical order.
func AllTrackerOpIds() []TrackerOpId {
	return append([]TrackerOpId(nil), canonicalTrackerOpIds[:]...)
}

// TrackerOp is one operation: its canonical signature and its recipe on each
// backend. The canonical signature is what a skill body writes after the op
// marker. The Pasture and Ledger fields are adapter recipes and live only in
// this table; no authored skill prose copies them.
type TrackerOp struct {
	Id        TrackerOpId
	Action    string
	Signature string
	Pasture   string
	Ledger    string
}

// TrackerOps is the closed operation vocabulary. relate is not an operation:
// a typed edge is block --kind. assign is not an operation: an owner is
// recorded with label ID owner:<role>. The Pasture recipe in each row documents
// what the shipped rec asset (TrackerRecScript) executes; TestRecAssetServesTheTenOps
// binds the two so the table and the asset cannot drift apart.
var TrackerOps = map[TrackerOpId]TrackerOp{
	TrackerOpCreate: {
		Id:        TrackerOpCreate,
		Action:    "create an immutable record; the body is verbatim; print the new task URI",
		Signature: `"${EPOCH_REC}" create --kind K --title T --phase P [--priority N] [--label L]... [--ref K=V]... [--body TEXT|-] -> ID`,
		Pasture:   `pasture task create "$TITLE" --type "$KIND" --description "$BODY" --phase "$PHASE" --priority "$PRIORITY" --namespace "$NS" --format text; print the ID; then pasture task label add "$ID" "$LABEL" for each label`,
		Ledger:    `mkdir -p "$LEDGER/records" "$LEDGER/events/$ID"; write records/$ID.md with frontmatter id,title,kind,phase,labels,refs,created_by,created_at,sha256 and the body; append events/$ID/<ts>-created.md; print $ID`,
	},
	TrackerOpLabel: {
		Id:        TrackerOpLabel,
		Action:    "add a label; never remove one",
		Signature: `"${EPOCH_REC}" label ID L   (assignment: label ID owner:<role>)`,
		Pasture:   `pasture task label add "$ID" "$LABEL"`,
		Ledger:    `append events/$ID/<ts>-<actor>.md with op: label and label: $LABEL`,
	},
	TrackerOpBlock: {
		Id:        TrackerOpBlock,
		Action:    "add an edge; a blocked_by edge keeps the parent open until the child closes; a typed edge uses --kind",
		Signature: `"${EPOCH_REC}" block PARENT --by CHILD [--kind blocked_by|derived_from|supersedes|discovered_from]`,
		Pasture:   `pasture task dep add "$PARENT" --blocked-by "$CHILD"   (typed edge: pasture task dep add "$SRC" --target "$DST" --kind "$KIND")`,
		Ledger:    `append events/$PARENT/<ts>-<actor>.md with op: link, child: $CHILD, kind: $KIND`,
	},
	TrackerOpNote: {
		Id:        TrackerOpNote,
		Action:    "append an attributed immutable note; --to makes it a message",
		Signature: `"${EPOCH_REC}" note ID --author A [--kind K] [--to ROLE] [--body TEXT|-]`,
		Pasture:   `pasture task comment add "$ID" "$BODY" --author "$AUTHOR"`,
		Ledger:    `append events/$ID/<ts>-<actor>.md with op: note, author, kind, optional to, and the body`,
	},
	TrackerOpShow: {
		Id:        TrackerOpShow,
		Action:    "read one record, optionally with its notes or history",
		Signature: `"${EPOCH_REC}" show ID [--notes] [--history] [--format text|json]`,
		Pasture:   `pasture task show "$ID"   (notes: pasture task comments "$ID"; history: pasture task timeline "$ID")`,
		Ledger:    `concatenate records/$ID.md and its sorted events/$ID/*.md`,
	},
	TrackerOpQuery: {
		Id:        TrackerOpQuery,
		Action:    "enumerate records by status, label, blocked state, or dependency tree",
		Signature: `"${EPOCH_REC}" query [--status S] [--label L] [--blocked] [--tree ID] [--all-namespaces] [--format text|json]`,
		Pasture:   `pasture task list --status "$STATUS" --label "$LABEL" --namespace "$NS"   (blocked: pasture task blocked; tree: pasture task dep tree "$ID")`,
		Ledger:    `scan record frontmatter and events; --blocked keeps records with a non-closed blocked_by target`,
	},
	TrackerOpReady: {
		Id:        TrackerOpReady,
		Action:    "list non-closed records with no non-closed blocker",
		Signature: `"${EPOCH_REC}" ready [--label L] [--format text|json]`,
		Pasture:   `pasture task ready`,
		Ledger:    `derived scan: non-closed records whose blocked_by targets are all closed`,
	},
	TrackerOpStatus: {
		Id:        TrackerOpStatus,
		Action:    "change state (start work); the store keeps the only status field",
		Signature: `"${EPOCH_REC}" status ID --set open|in_progress|closed`,
		Pasture:   `pasture task update "$ID" --status "$STATUS"`,
		Ledger:    `append events/$ID/<ts>-<actor>.md with op: status and status: $STATUS`,
	},
	TrackerOpClose: {
		Id:        TrackerOpClose,
		Action:    "close a record; policy reserves this for the supervisor",
		Signature: `"${EPOCH_REC}" close ID --reason R`,
		Pasture:   `pasture task close "$ID" --reason "$REASON"`,
		Ledger:    `append events/$ID/<ts>-<actor>.md with op: status, status: closed, and reason: $REASON`,
	},
	TrackerOpWhoami: {
		Id:        TrackerOpWhoami,
		Action:    "resolve the acting author; never invent one",
		Signature: `"${EPOCH_REC}" whoami`,
		Pasture:   `pasture task agents list and pasture task agents show "$ACTOR"`,
		Ledger:    `read the author: claim from EPOCH.md; ledger authors are quoted on import, never impersonated`,
	},
}

// TrackerAssignmentForm is how an owner is recorded. There is no assign
// operation: a ledger cannot enforce an assignment, so the epoch records the
// claim as an additive label.
const TrackerAssignmentForm = `label ID owner:<role>`

// ValidateTrackerOps fails when the closed operation set is incomplete or a
// recipe is missing. It is called by the generator, so an incomplete contract
// fails the build instead of shipping a partial adapter.
func ValidateTrackerOps() error {
	ids := AllTrackerOpIds()
	ops := make([]TrackerOp, 0, len(ids))
	for _, id := range ids {
		op, ok := TrackerOps[id]
		if !ok {
			return fmt.Errorf("tracker contract: operation %q is missing from TrackerOps — why: the vocabulary is closed and every registered id needs a recipe; where: internal/codegen/specs_data_tracker.go TrackerOps; fix: add the operation or remove its id from canonicalTrackerOpIds", id)
		}
		ops = append(ops, op)
	}
	if err := validateTrackerOps(ops); err != nil {
		return err
	}
	if len(TrackerOps) != len(ids) {
		return fmt.Errorf("tracker contract: TrackerOps has %d entries but the closed set has %d — why: an unregistered operation would be unvalidated; where: internal/codegen/specs_data_tracker.go; fix: register the id in canonicalTrackerOpIds and TrackerOps, or remove the extra entry", len(TrackerOps), len(ids))
	}
	return nil
}

// validateTrackerOps is the pure core of the contract guard. It is exported to
// tests only through ValidateTrackerOps; the two-sample guard calls it directly
// with a defective fixture.
func validateTrackerOps(ops []TrackerOp) error {
	if len(ops) != len(canonicalTrackerOpIds) {
		return fmt.Errorf("tracker contract: got %d operations, want %d — why: the operation set is closed to the ten canonical ops; where: internal/codegen/specs_data_tracker.go; fix: supply exactly one entry per canonical id", len(ops), len(canonicalTrackerOpIds))
	}
	seen := make(map[TrackerOpId]bool, len(ops))
	for _, op := range ops {
		if !op.Id.isRegistered() {
			return fmt.Errorf("tracker contract: operation %q is not registered — why: an unregistered op would bypass the closed-set rule; where: internal/codegen/specs_data_tracker.go; fix: use one of the canonical ids", op.Id)
		}
		if seen[op.Id] {
			return fmt.Errorf("tracker contract: operation %q appears twice — why: one op has one recipe; where: internal/codegen/specs_data_tracker.go; fix: remove the duplicate", op.Id)
		}
		seen[op.Id] = true
		if strings.TrimSpace(op.Action) == "" {
			return fmt.Errorf("tracker contract: operation %q has no action — why: a caller cannot tell what the op does; where: internal/codegen/specs_data_tracker.go TrackerOps[%q]; fix: add an actionable description", op.Id, op.Id)
		}
		if !strings.HasPrefix(op.Signature, trackerEntryPoint+" "+string(op.Id)) {
			return fmt.Errorf("tracker contract: operation %q signature does not start with %s %s — why: the one call form is the adapter entry point followed by the op; where: internal/codegen/specs_data_tracker.go TrackerOps[%q].Signature; fix: write the signature in the canonical form", op.Id, trackerEntryPoint, op.Id, op.Id)
		}
		if strings.TrimSpace(op.Pasture) == "" {
			return fmt.Errorf("tracker contract: operation %q has no Pasture recipe — why: a Pasture epoch would have no call for this op; where: internal/codegen/specs_data_tracker.go TrackerOps[%q].Pasture; fix: add the native recipe", op.Id, op.Id)
		}
		if strings.TrimSpace(op.Ledger) == "" {
			return fmt.Errorf("tracker contract: operation %q has no ledger recipe — why: a ledger epoch would have no call for this op; where: internal/codegen/specs_data_tracker.go TrackerOps[%q].Ledger; fix: add the ledger mutation", op.Id, op.Id)
		}
	}
	for _, id := range canonicalTrackerOpIds {
		if !seen[id] {
			return fmt.Errorf("tracker contract: operation %q is missing — why: the closed set requires all ten operations; where: internal/codegen/specs_data_tracker.go; fix: add the missing entry", id)
		}
	}
	return nil
}

// isRegistered reports whether id is a member of the closed operation set.
func (id TrackerOpId) isRegistered() bool {
	for _, candidate := range canonicalTrackerOpIds {
		if id == candidate {
			return true
		}
	}
	return false
}

// TrackerProbeScript is the side-effect-free epoch probe.
//
// Order is normative: capability (--help), store existence (test -f, never an
// open), usability (migrate --dry-run). It prints KEY=VALUE lines and exits 0;
// malformed environment is reported as a backend decision, not a leak.
//
// The probe is the only production code that names the Pasture binary. Authored
// skill prose must call the adapter instead.
const TrackerProbeScript = `#!/bin/sh
# Epoch tracker probe. Side-effect free. Prints KEY=VALUE lines and exits 0.
# Order: capability (--help), store existence (test -f), usability (migrate --dry-run).
set -u

PASTURE_BIN="${PASTURE_BIN:-pasture}"

emit() {
  printf 'BACKEND=%s\n' "$1"
  printf 'REASON=%s\n' "${2:-}"
  [ -n "${3:-}" ] && printf 'DB=%s\n' "$3"
  [ -n "${4:-}" ] && printf 'NS=%s\n' "$4"
  [ -n "${5:-}" ] && printf 'ACT=%s\n' "$5"
  [ -n "${6:-}" ] && printf 'VER=%s\n' "$6"
  exit 0
}

# 1. Capability. --help never opens a store.
if ! command -v "$PASTURE_BIN" >/dev/null 2>&1; then
  emit ledger not-installed
fi

VER="$("$PASTURE_BIN" --version 2>/dev/null | awk '{print $NF}')"

"$PASTURE_BIN" --help >/dev/null 2>&1
if [ "$?" -ne 0 ]; then
  emit ledger help-failed "" "" "" "$VER"
fi

for VERB in "task create" "task label" "task dep" "task comment" "task show" \
            "task comments" "task timeline" "task list" "task ready" \
            "task close" "task update" "task agents"; do
  # shellcheck disable=SC2086
  "$PASTURE_BIN" $VERB --help >/dev/null 2>&1
  if [ "$?" -ne 0 ]; then
    emit ledger "missing-verb:$VERB" "" "" "" "$VER"
  fi
done

# 2. Store existence. Never open a missing store: opening creates it.
if [ -n "${PASTURE_DB_PATH:-}" ]; then
  DB="$PASTURE_DB_PATH"
elif [ -n "${XDG_DATA_HOME:-}" ]; then
  DB="$XDG_DATA_HOME/pasture/pasture.db"
elif [ -n "${HOME:-}" ]; then
  DB="$HOME/.local/share/pasture/pasture.db"
else
  DB=".pasture/pasture.db"
fi

if [ ! -f "$DB" ]; then
  emit ledger no-store "$DB" "" "" "$VER"
fi

# 3. Usability. --dry-run leaves the file's SHA-256 unchanged.
"$PASTURE_BIN" --db "$DB" migrate --dry-run >/dev/null 2>&1
RC=$?
if [ "$RC" -ne 0 ]; then
  emit error "store-exit-$RC" "$DB" "" "" "$VER"
fi

# 4. Scope and identity are read-only checks on the existing store.
if [ -n "${PASTURE_NAMESPACE:-}" ]; then
  NS="$PASTURE_NAMESPACE"
else
  NS="$(git remote get-url origin 2>/dev/null | sed -E 's#^git@([^:]+):#https://\1/#; s#\.git$##')"
fi
if [ -z "$NS" ]; then
  emit error no-namespace "$DB" "" "" "$VER"
fi

ACT="${PASTURE_AUTHOR:-pasture-system/00000000-0000-0000-0000-000000000000}"
"$PASTURE_BIN" --db "$DB" task agents show "$ACT" --format json >/dev/null 2>&1
RC=$?
if [ "$RC" -ne 0 ]; then
  emit error no-author "$DB" "$NS" "$ACT" "$VER"
fi

emit pasture "" "$DB" "$NS" "$ACT" "$VER"
`

// TrackerRecScript is the epoch tracker adapter ("rec"). It resolves the one
// recorded backend and serves the ten neutral operations.
//
// This is the minimal, mergeable asset: the pasture backend is complete. The
// ledger store is a later slice; until it lands, a ledger epoch is told plainly
// that this adapter build cannot record for it instead of silently dropping the
// record. The error backend always stops before the first mutation.
//
// The script reads EPOCH_FILE (default .agents.local/pasture/EPOCH.md) and never
// probes. It is authored once here, rendered into the Tracker Adapter section,
// and executed by the generated-recipe harness through this constant.
const TrackerRecScript = `#!/bin/sh
# Epoch tracker adapter (rec). Serves the ten neutral tracker operations.
# Backend: the one recorded in EPOCH_FILE. Never re-probes, never switches.
set -u

EPOCH_FILE="${EPOCH_FILE:-.agents.local/pasture/EPOCH.md}"
PASTURE_BIN="${PASTURE_BIN:-pasture}"

fail() {
  printf 'rec: %s\n  why: %s\n  fix: %s\n' "$1" "$2" "$3" >&2
  exit 3
}

[ -f "$EPOCH_FILE" ] || fail "no epoch header at $EPOCH_FILE" \
  "the backend is chosen once and recorded before the first write" \
  "run the Starting an Epoch detection step, or set EPOCH_FILE to the header"

field() {
  sed -n "s/^$1: //p" "$EPOCH_FILE" 2>/dev/null | tail -1
}

BACKEND="$(field backend)"
DB="${PASTURE_DB_PATH:-$(field db)}"
NS="${PASTURE_NAMESPACE:-$(field namespace)}"
AUTHOR="${PASTURE_AUTHOR:-$(field author)}"

case "$BACKEND" in
  pasture) : ;;
  error)
    fail "the recorded backend is error" \
      "the store exists but cannot be used, and falling back could lose earlier epochs" \
      "repair the store named in $EPOCH_FILE, or start a new epoch with a working backend" ;;
  ledger)
    fail "the recorded backend is ledger" \
      "this adapter build serves only the pasture backend; the ledger store lands in a later slice" \
      "use a build that ships the ledger store, or re-probe with a working pasture" ;;
  "")
    fail "no backend recorded in $EPOCH_FILE" \
      "the probe has not written the header yet" \
      "run the Starting an Epoch detection step" ;;
  *)
    fail "unknown backend '$BACKEND' in $EPOCH_FILE" \
      "the backend set is closed to pasture, ledger and error" \
      "rewrite the backend line from the probe output" ;;
esac

OP="${1:-}"
[ "$#" -gt 0 ] && shift

pb() {
  if [ -n "$DB" ]; then
    "$PASTURE_BIN" --db "$DB" "$@"
  else
    "$PASTURE_BIN" "$@"
  fi
}

need_id() {
  [ -n "${1:-}" ] || fail "$OP needs a task ID" "the first operand was empty" "pass the full task URI as the first operand"
}

case "$OP" in
  create)
    kind=task title= phase= body= priority= labels=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --kind) kind="$2"; shift 2 ;;
        --kind=*) kind="${1#*=}"; shift ;;
        --title) title="$2"; shift 2 ;;
        --title=*) title="${1#*=}"; shift ;;
        --phase) phase="$2"; shift 2 ;;
        --phase=*) phase="${1#*=}"; shift ;;
        --priority) priority="$2"; shift 2 ;;
        --priority=*) priority="${1#*=}"; shift ;;
        --body) body="$2"; shift 2 ;;
        --body=*) body="${1#*=}"; shift ;;
        --label) labels="$labels $2"; shift 2 ;;
        --label=*) labels="$labels ${1#*=}"; shift ;;
        --ref) shift 2 ;;
        --ref=*) shift ;;
        *) shift ;;
      esac
    done
    [ "$body" = "-" ] && body="$(cat)"
    set -- task create "$title" --type "$kind" --phase "$phase"
    [ -n "$NS" ] && set -- "$@" --namespace "$NS"
    [ -n "$priority" ] && set -- "$@" --priority "$priority"
    [ -n "$body" ] && set -- "$@" --description "$body"
    out="$(pb "$@" --format text)" || exit $?
    id="$(printf '%s\n' "$out" | sed -n 's/^ID: *//p' | head -1)"
    [ -n "$id" ] || fail "create returned no task ID" "the backend text output had no ID line" "check the store named in $EPOCH_FILE and the backend help page"
    printf '%s\n' "$id"
    for label in $labels; do
      pb task label add "$id" "$label" >/dev/null || exit $?
    done
    ;;
  label)
    id="${1:-}"; shift; need_id "$id"
    [ "$#" -gt 0 ] || fail "label needs at least one label" "no label operand was given" "pass the label after the task URI"
    for label in "$@"; do
      pb task label add "$id" "$label" || exit $?
    done
    ;;
  block)
    parent="${1:-}"; shift; need_id "$parent"
    child= kind=blocked_by
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --by) child="$2"; shift 2 ;;
        --by=*) child="${1#*=}"; shift ;;
        --kind) kind="$2"; shift 2 ;;
        --kind=*) kind="${1#*=}"; shift ;;
        *) shift ;;
      esac
    done
    [ -n "$child" ] || fail "block needs --by CHILD" "the child operand was empty" "pass block PARENT --by CHILD"
    if [ "$kind" = blocked_by ]; then
      pb task dep add "$parent" --blocked-by "$child" || exit $?
    else
      pb task dep add "$parent" --target "$child" --kind "$kind" || exit $?
    fi
    ;;
  note)
    id="${1:-}"; shift; need_id "$id"
    note_body= note_kind= note_to= fmt=json
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --author) AUTHOR="$2"; shift 2 ;;
        --author=*) AUTHOR="${1#*=}"; shift ;;
        --body) note_body="$2"; shift 2 ;;
        --body=*) note_body="${1#*=}"; shift ;;
        --kind) note_kind="$2"; shift 2 ;;
        --kind=*) note_kind="${1#*=}"; shift ;;
        --to) note_to="$2"; shift 2 ;;
        --to=*) note_to="${1#*=}"; shift ;;
        --format) fmt="$2"; shift 2 ;;
        --format=*) fmt="${1#*=}"; shift ;;
        *) shift ;;
      esac
    done
    [ "$note_body" = "-" ] && note_body="$(cat)"
    [ -n "$AUTHOR" ] || fail "note needs --author" "the tracker never invents an author" "pass the registered actor id"
    [ -n "$note_kind" ] && note_body="[$note_kind] $note_body"
    [ -n "$note_to" ] && note_body="to:$note_to $note_body"
    # The body is the last positional argument and may begin with dashes; -- stops
    # the CLI's flag parser so a body like "---" is text, not a malformed flag.
    pb task comment add "$id" --author "$AUTHOR" -- "$note_body" || exit $?
    ;;
  show)
    id="${1:-}"; shift; need_id "$id"
    mode=show fmt=json
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --notes) mode=notes; shift ;;
        --history) mode=history; shift ;;
        --format) fmt="$2"; shift 2 ;;
        --format=*) fmt="${1#*=}"; shift ;;
        *) shift ;;
      esac
    done
    case "$mode" in
      notes) pb task comments "$id" --format "$fmt" ;;
      history) pb task timeline "$id" ;;
      *) pb task show "$id" --format "$fmt" ;;
    esac
    ;;
  query)
    status= label= tree= fmt=json blocked=0 allns=0
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --status) status="$2"; shift 2 ;;
        --status=*) status="${1#*=}"; shift ;;
        --label) label="$2"; shift 2 ;;
        --label=*) label="${1#*=}"; shift ;;
        --tree) tree="$2"; shift 2 ;;
        --tree=*) tree="${1#*=}"; shift ;;
        --format) fmt="$2"; shift 2 ;;
        --format=*) fmt="${1#*=}"; shift ;;
        --blocked) blocked=1; shift ;;
        --all-namespaces) allns=1; shift ;;
        *) shift ;;
      esac
    done
    if [ -n "$tree" ]; then
      pb task dep tree "$tree"
      exit $?
    fi
    if [ "$blocked" = 1 ]; then
      set -- task blocked
    else
      set -- task list
      [ -n "$NS" ] && set -- "$@" --namespace "$NS"
    fi
    [ -n "$status" ] && set -- "$@" --status "$status"
    [ -n "$label" ] && set -- "$@" --label "$label"
    [ "$allns" = 1 ] && set -- "$@" --all-namespaces
    pb "$@" --format "$fmt"
    ;;
  ready)
    label= fmt=json
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --label) label="$2"; shift 2 ;;
        --label=*) label="${1#*=}"; shift ;;
        --format) fmt="$2"; shift 2 ;;
        --format=*) fmt="${1#*=}"; shift ;;
        *) shift ;;
      esac
    done
    set -- task ready
    [ -n "$label" ] && set -- "$@" --label "$label"
    pb "$@" --format "$fmt"
    ;;
  status)
    id="${1:-}"; shift; need_id "$id"
    want=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --set) want="$2"; shift 2 ;;
        --set=*) want="${1#*=}"; shift ;;
        *) shift ;;
      esac
    done
    [ -n "$want" ] || fail "status needs --set" "no target status was given" "pass --set open, in_progress or closed"
    pb task update "$id" --status "$want"
    ;;
  close)
    id="${1:-}"; shift; need_id "$id"
    reason=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --reason) reason="$2"; shift 2 ;;
        --reason=*) reason="${1#*=}"; shift ;;
        *) shift ;;
      esac
    done
    if [ -n "$reason" ]; then
      pb task close "$id" --reason "$reason"
    else
      pb task close "$id"
    fi
    ;;
  whoami)
    if [ -n "$AUTHOR" ]; then
      printf '%s\n' "$AUTHOR"
    else
      pb task agents list
    fi
    ;;
  *)
    fail "unknown operation '$OP'" "the operation set is closed to the ten neutral ops" \
      "use create, label, block, note, show, query, ready, status, close or whoami; run the probe if the header is missing"
    ;;
esac
`

// TrackerProbeLimits states what each probe step can and cannot detect. The
// limits are normative: a probe result is evidence about the three checked
// facts, not proof that a later write will succeed.
const TrackerProbeLimits = `Detection limits, by step:

- pasture --help: detects that the binary is on PATH and that each required verb has a help page. It cannot detect version compatibility, store health, permissions, schema, namespace or author. A help page can exist on a binary whose runtime is broken.
- test -f: detects a missing store file and stops the probe before anything opens it. It cannot detect a directory, an unreadable or corrupt database, an out-of-date schema, a held lock, or a different store selected by configuration. Never replace it with a read: opening a missing store creates it.
- migrate --dry-run: detects that the store opens and that its migrations can be planned; it leaves the file's SHA-256 unchanged. It still opens the store and may take locks. It cannot detect concurrent writers, a defect that only a real migration touches, an unregistered author, or a namespace mismatch.
- Scope and author checks: detect that a namespace resolves and that the configured author is registered. They cannot detect an author registered in another namespace, nor prove the adapter will be allowed to write.
- The probe cannot detect a store chosen by configuration outside the env/default precedence, and it does not prove that any later write will succeed.`

// TrackerEpochHeaderTemplate is the EPOCH.md shape. The file lives outside the
// backend so it remains writable when the backend is broken.
const TrackerEpochHeaderTemplate = `EPOCH.md lives at .agents.local/pasture/EPOCH.md (offer a gitignore entry; never require one).

---
epoch: <epoch id>
backend: pasture|ledger|error
probed_at: <UTC timestamp>
namespace: <namespace URI>        # pasture only
db: <absolute store path>         # pasture only
author: <registered actor id>     # pasture only; what whoami prints
ledger: <absolute ledger path>    # ledger only
rec: <absolute adapter path>      # pasture and ledger
reviewer_tier: T2|T3              # optional; the phase-8 review-effort choice
degrades: assign=claim-only       # ledger only
---
<the probe transcript and the user's one-line acknowledgement, verbatim>`

// TrackerWriteRetryLimit is how many times an adapter retries one failed write
// before surfacing it at the next user gate.
const TrackerWriteRetryLimit = 3

// TrackerWriteFailureRule is the mid-epoch write-failure rule. It never allows
// a backend switch and never allows a record to be dropped. The retry count is
// rendered from TrackerWriteRetryLimit, its one home.
var TrackerWriteFailureRule = fmt.Sprintf(`A mid-epoch write failure retries %d times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.`, TrackerWriteRetryLimit)

// trackerAdapterFragment builds the shared appendix that carries the adapter
// contract into generated skill prose. It is rendered once and referenced by
// the epoch body, so the backend table, the ten ops, the probe and the failure
// rule have one authored home.
func trackerAdapterFragment() SharedFragment {
	prose := ProseSection{
		Id:      string(FragTrackerAdapter),
		Title:   "Tracker Adapter",
		Content: buildTrackerAdapterContent(),
	}
	return SharedFragment{
		Id:    FragTrackerAdapter,
		Kind:  FragmentKindProse,
		Prose: &prose,
	}
}

// buildTrackerAdapterContent renders the adapter appendix from the contract
// table, so the generated text cannot drift from the closed set.
func buildTrackerAdapterContent() string {
	var b strings.Builder
	b.WriteString("Tracker calls go through one entry point: `\"${EPOCH_REC}\" <op>`. The backend is chosen once, recorded in `.agents.local/pasture/EPOCH.md`, and read from there; no skill body names a backend recipe.\n\n")
	b.WriteString("Resolve the entry point from the header at the start of each shell session:\n\n")
	b.WriteString("~~~bash\n")
	b.WriteString("EPOCH_REC=$(sed -n 's/^rec: //p' .agents.local/pasture/EPOCH.md 2>/dev/null | tail -1)\n")
	b.WriteString("if [ -z \"$EPOCH_REC\" ]; then\n")
	b.WriteString("  echo \"no epoch header at .agents.local/pasture/EPOCH.md; run the epoch detection step\" >&2\n")
	b.WriteString("  exit 3\n")
	b.WriteString("fi\n")
	b.WriteString("~~~\n\n")
	b.WriteString("| Backend | Meaning | Action |\n")
	b.WriteString("|---|---|---|\n")
	b.WriteString("| `pasture` | binary is capable and the store is usable | forward each op through the adapter |\n")
	b.WriteString("| `ledger` | no usable binary or store | record in the ledger under `.agents.local/pasture/`; `assign` is claim-only |\n")
	b.WriteString("| `error` | the store exists but cannot be used | diagnose and stop before the first mutation; do not fall back |\n\n")
	b.WriteString("The operation set is closed. `relate` is `block --kind`; `assign` is `label ID owner:<role>`.\n\n")
	b.WriteString("| Op | Canonical call | Action |\n")
	b.WriteString("|---|---|---|\n")
	for _, id := range AllTrackerOpIds() {
		op := TrackerOps[id]
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", op.Id, op.Signature, op.Action)
	}
	b.WriteString("\n")
	b.WriteString(TrackerEpochHeaderTemplate)
	b.WriteString("\n\nNo mid-epoch switching: if the recorded backend fails mid-epoch, stop and report. Switching is never a repair; abandon the epoch and start a new one with a fresh probe, so the header and the records never describe different backends.\n\n")
	b.WriteString(TrackerWriteFailureRule)
	b.WriteString("\n\nThe orchestrator runs the probe once. Subagents read EPOCH.md and never re-probe.\n\n")
	b.WriteString("Write this probe to `.agents.local/pasture/probe.sh` and run it before the first write:\n\n")
	b.WriteString("~~~sh\n")
	b.WriteString(TrackerProbeScript)
	b.WriteString("~~~\n\n")
	b.WriteString(TrackerProbeLimits)
	b.WriteString("\n\nWrite this adapter to the path the header records in `rec:` and make it executable; every tracker call goes through it:\n\n")
	b.WriteString("~~~sh\n")
	b.WriteString(TrackerRecScript)
	b.WriteString("~~~\n")
	return b.String()
}

// SortedTrackerOps returns the operations in canonical order. It exists so a
// caller never ranges a map and gets a nondeterministic projection.
func SortedTrackerOps() []TrackerOp {
	out := make([]TrackerOp, 0, len(canonicalTrackerOpIds))
	for _, id := range canonicalTrackerOpIds {
		out = append(out, TrackerOps[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}
