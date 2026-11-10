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
// marker. Flags and PastureCalls are the executable contract; the rec asset is
// rendered from them. Pasture and Ledger prose are derived from that contract.
type TrackerOp struct {
	Id             TrackerOpId
	Action         string
	Signature      string
	Pasture        string
	Ledger         string
	Flags          []trackerFlag
	Positional     string
	Rest           string
	Prelude        string
	PastureCalls   []pastureCall
	RequiresAuthor bool
}

// TrackerOps is built in specs_data_tracker_script.go from the dispatch
// table. relate is not an operation: a typed edge is block --kind. assign is
// not an operation: an owner is recorded with label ID owner:<role>.

// TrackerAssignmentForm is how an owner is recorded. There is no assign
// operation: a ledger cannot enforce an assignment, so the epoch records the
// claim as an additive label.
const TrackerAssignmentForm = `label ID owner:<role>`

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
	if err := validateTrackerImplementations(ops, ledgerRuntime); err != nil {
		return err
	}
	if err := validateTrackerDispatch(TrackerOps); err != nil {
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
		if len(op.PastureCalls) == 0 {
			return fmt.Errorf("tracker contract: operation %q has no Pasture calls — why: an empty call list still renders a non-empty Pasture string, so the pasture arm is a silent no-op; where: internal/codegen/specs_data_tracker.go validateTrackerOps; when: checking TrackerOps before generation; impact: the op ships and exits 0 without calling the backend; fix: add at least one PastureCall for %q", op.Id, op.Id)
		}
	}
	for _, id := range canonicalTrackerOpIds {
		if !seen[id] {
			return fmt.Errorf("tracker contract: operation %q is missing — why: the closed set requires all ten operations; where: internal/codegen/specs_data_tracker.go; fix: add the missing entry", id)
		}
	}
	return nil
}

// validateTrackerImplementations fails the build when an op has no ledger
// handler definition. A comment that merely names ledger_<id>() does not
// count: renderOpArm calls the function, and a missing definition is exit
// 127 at runtime, after generation has already shipped.
func validateTrackerImplementations(ops []TrackerOp, runtime string) error {
	for _, op := range ops {
		needle := "\nledger_" + string(op.Id) + "() {"
		if strings.Contains(runtime, needle) {
			continue
		}
		return fmt.Errorf("tracker contract: operation %q has no ledger_%s() { handler — why: a mention of the name is not a definition, and the rec asset calls ledger_%s; a missing function fails at runtime with exit 127 (command not found); where: internal/codegen/specs_data_tracker_script.go ledgerRuntime; when: checking TrackerOps before generation; impact: a ledger epoch dies on the first %s call; fix: define ledger_%s() { in ledgerRuntime", op.Id, op.Id, op.Id, op.Id, op.Id)
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

// TrackerProbeLimits states what each probe step can and cannot detect. The
// limits are normative: a probe result is evidence about the three checked
// facts, not proof that a later write will succeed.
const TrackerProbeLimits = `Detection limits, by step:

- pasture --help: detects that the binary is on PATH and that each required verb has a help page. It cannot detect version compatibility, store health, permissions, schema, namespace or author. A help page can exist on a binary whose runtime is broken.
- test -f: detects a missing store file and stops the probe before anything opens it. It cannot detect a directory, an unreadable or corrupt database, an out-of-date schema, a held lock, or a different store selected by configuration. Never replace it with a read: opening a missing store creates it.
- migrate --dry-run: detects that the store opens and that its migrations can be planned; it leaves the file's SHA-256 unchanged. It still opens the store and may take locks. It cannot detect concurrent writers, a defect that only a real migration touches, an unregistered author, or a namespace mismatch. A non-zero result is the only error backend: the store exists but cannot be used.
- Namespace: the probe does not derive one, and it does not write a derived value into the header. A missing remote is not an unusable store. Pasture derives when EPOCH.md has no namespace: line (PASTURE_NAMESPACE, else the git origin, else file:// of the working directory). The ledger prints NS from derive_namespace, the one function every unpinned ledger write calls. It cannot detect a remote other than origin.
- Author: a missing or unregistered author is a warning (WARN=no-author), not an error backend. It cannot detect an author registered in another namespace, nor prove a later write will be allowed.
- The probe cannot detect a store chosen by configuration outside the env/default precedence, and it does not prove that any later write will succeed.`

// TrackerEpochHeaderTemplate is the EPOCH.md shape. The file lives outside the
// backend so it remains writable when the backend is broken.
const TrackerEpochHeaderTemplate = `EPOCH.md lives at .agents.local/pasture/EPOCH.md (offer a gitignore entry; never require one).

---
epoch: <epoch id>
backend: pasture|ledger|error
probed_at: <UTC timestamp>
namespace: <namespace URI>        # optional pin; absent means the backend derives
db: <absolute store path>         # pasture only
author: <registered actor id>     # pasture only; what whoami prints
ledger: <absolute ledger path>    # ledger only
rec: <absolute adapter path>      # pasture and ledger
feedback: <task URI>              # pasture only; the epoch feedback task for signals
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
	b.WriteString("<!-- pasture:asset probe -->\n")
	b.WriteString("~~~sh\n")
	b.WriteString(ensureTrailingNewline(TrackerProbeScript))
	b.WriteString("~~~\n")
	b.WriteString("<!-- /pasture:asset probe -->\n\n")
	b.WriteString(TrackerProbeLimits)
	b.WriteString("\n\nWrite this adapter to the path the header records in `rec:` and make it executable; every tracker call goes through it:\n\n")
	b.WriteString("<!-- pasture:asset rec -->\n")
	b.WriteString("~~~sh\n")
	b.WriteString(ensureTrailingNewline(TrackerRecScript))
	b.WriteString("~~~\n")
	b.WriteString("<!-- /pasture:asset rec -->\n")
	return b.String()
}

func ensureTrailingNewline(script string) string {
	if strings.HasSuffix(script, "\n") {
		return script
	}
	return script + "\n"
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
