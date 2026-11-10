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
// backend. The canonical signature is what a skill body writes after the
// [[rec]] marker. The Pasture and Ledger fields are adapter recipes and live
// only in this table; no authored skill prose copies them.
type TrackerOp struct {
	Id        TrackerOpId
	Action    string
	Signature string
	Pasture   string
	Ledger    string
}

// TrackerOps is the closed operation vocabulary. relate is not an operation:
// a typed edge is block --kind. assign is not an operation: an owner is
// recorded with label ID owner:<role>.
var TrackerOps = map[TrackerOpId]TrackerOp{
	TrackerOpCreate: {
		Id:        TrackerOpCreate,
		Action:    "create an immutable record; the body is verbatim",
		Signature: `"${EPOCH_REC}" create --kind K --title T --phase P [--label L]... [--ref K=V]... [--body FILE|-] -> ID`,
		Pasture:   `pasture task create "$TITLE" --description "$BODY" --phase "$PHASE" --namespace "$NS" --format json`,
		Ledger:    `mkdir -p "$LEDGER/records" "$LEDGER/events/$ID"; write records/$ID.md with frontmatter id,title,kind,phase,labels,refs,created_by,created_at,sha256 and the body; append events/$ID/<ts>-created.md`,
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
		Signature: `"${EPOCH_REC}" note ID --author A [--kind K] [--to ROLE] [--body FILE|-]`,
		Pasture:   `pasture task comment add "$ID" "$BODY" --author "$AUTHOR"`,
		Ledger:    `append events/$ID/<ts>-<actor>.md with op: note, author, kind, optional to, and the body`,
	},
	TrackerOpShow: {
		Id:        TrackerOpShow,
		Action:    "read one record, optionally with its notes or history",
		Signature: `"${EPOCH_REC}" show ID [--notes] [--history]`,
		Pasture:   `pasture task show "$ID"   (notes: pasture task comments "$ID"; history: pasture task timeline "$ID")`,
		Ledger:    `concatenate records/$ID.md and its sorted events/$ID/*.md`,
	},
	TrackerOpQuery: {
		Id:        TrackerOpQuery,
		Action:    "enumerate records by status, label, or blocked state",
		Signature: `"${EPOCH_REC}" query [--status S] [--label L] [--blocked]`,
		Pasture:   `pasture task list --status "$STATUS" --label "$LABEL" --namespace "$NS"   (blocked: pasture task blocked)`,
		Ledger:    `scan record frontmatter and events; --blocked keeps records with a non-closed blocked_by target`,
	},
	TrackerOpReady: {
		Id:        TrackerOpReady,
		Action:    "list non-closed records with no non-closed blocker",
		Signature: `"${EPOCH_REC}" ready [--label L]`,
		Pasture:   `pasture task ready --namespace "$NS"`,
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
ledger: <absolute ledger path>    # ledger only
rec: <absolute adapter path>      # pasture and ledger
degrades: assign=claim-only       # ledger only
---
<the probe transcript and the user's one-line acknowledgement, verbatim>`

// TrackerWriteRetryLimit is how many times an adapter retries one failed write
// before surfacing it at the next user gate.
const TrackerWriteRetryLimit = 3

// TrackerWriteFailureRule is the mid-epoch write-failure rule. It never allows
// a backend switch and never allows a record to be dropped.
const TrackerWriteFailureRule = `A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.`

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
	b.WriteString("Tracker calls go through one entry point: `\"${EPOCH_REC}\" <op>`. The backend is chosen once, recorded in `.agents.local/pasture/EPOCH.md`, and read from there; no skill body names a backend.\n\n")
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
	b.WriteString("\n\nNo mid-epoch switching: if the recorded backend fails mid-epoch, stop and report; a switch requires the user's explicit decision, recorded in EPOCH.md, plus a note on the last successful record.\n\n")
	b.WriteString(TrackerWriteFailureRule)
	b.WriteString("\n\nThe orchestrator runs the probe once. Subagents read EPOCH.md and never re-probe.\n\n")
	b.WriteString("~~~sh\n")
	b.WriteString(TrackerProbeScript)
	b.WriteString("~~~\n\n")
	b.WriteString(TrackerProbeLimits)
	b.WriteString("\n")
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
