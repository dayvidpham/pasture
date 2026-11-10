// Generated tracker adapter.
//
// TrackerOps is the one home of each neutral operation. The probe verb list,
// the rec asset's flag parser, and the pasture argv are rendered from that
// table, so a declared op and the script that serves it cannot name different
// commands. The ledger backend is the same case arm writing one JSONL line per
// mutation. Key order is ledgerFieldOrder. An independent pin rejects a reorder.
package codegen

import (
	"fmt"
	"regexp"
	"strings"
)

// ledgerFieldOrder is the stable key order of every ledger mutation line.
// The writer reads this slice. The order guard pins an independent literal
// and does not treat this slice as its expectation.
var ledgerFieldOrder = []string{
	"op", "id", "at", "namespace", "title", "kind", "phase", "status",
	"priority", "label", "parent", "child", "edge_kind", "author",
	"note_kind", "to", "reason", "ref", "body",
}

// ledgerLineKeyOrder reads the key sequence the rendered ledger_line writer
// emits. The order guard compares this to an independent literal, so a reorder
// of ledgerFieldOrder changes the writer and fails that pin.
func ledgerLineKeyOrder(script string) ([]string, error) {
	start := strings.Index(script, "ledger_line() {")
	if start < 0 {
		return nil, fmt.Errorf("ledger field order: rendered rec has no ledger_line function — why: the writer is generated from ledgerFieldOrder; where: internal/codegen/specs_data_tracker_script.go renderLedgerLineFunc; fix: keep ledger_line in the rec asset")
	}
	rest := script[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		return nil, fmt.Errorf("ledger field order: ledger_line function is not closed — where: rendered TrackerRecScript; fix: keep the function terminated before the next definition")
	}
	re := regexp.MustCompile(`printf '(?:,)?"([a-z_]+)":`)
	var keys []string
	for _, match := range re.FindAllStringSubmatch(rest[:end], -1) {
		keys = append(keys, match[1])
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("ledger field order: ledger_line emits no keys — why: the printf sequence is the writer; where: renderLedgerLineFunc; fix: emit one printf per ledgerFieldOrder key")
	}
	return keys, nil
}

// trackerFlag is one neutral flag. The rec parser is rendered from these
// fields; a flag that is not listed here is rejected instead of dropped.
type trackerFlag struct {
	Name      string
	Dest      string
	Bool      bool
	BoolValue string
	Repeat    bool
	RepeatNL  bool
	Default   string
}

func (f trackerFlag) dest() string {
	if f.Dest != "" {
		return f.Dest
	}
	return f.Name
}

// pastureOptional is a flag appended to a pasture invocation only when its
// variable is non-empty.
type pastureOptional struct {
	Var  string
	Flag string
}

// pastureCall is one pasture argv template. The rec asset emits these words;
// it does not carry a second, hand-written command list.
type pastureCall struct {
	// Name identifies the arm for the scope guard (create, list, blocked).
	Name string
	// When is a shell condition. Empty is the default arm.
	When string
	// Argv words are already shell-quoted where they must be ("$title").
	Argv      []string
	Optional  []pastureOptional
	Scoped    bool
	PassAllNS bool
	Format    string
	CaptureID bool
	Quiet     bool
	ThenPrint string
	RepeatVar string
	ExitAfter bool
}

// TrackerOps is built from the dispatch table. Pasture and Ledger prose are
// derived from that table so the documentation cannot describe a command the
// script does not run.
var TrackerOps = buildTrackerOps()

// TrackerRecScript is the epoch adapter, rendered from TrackerOps.
var TrackerRecScript = renderTrackerRecScript(TrackerOps)

// TrackerProbeScript is the side-effect-free probe. Its verb list is the
// task verbs the dispatch actually calls, so a new pasture command cannot
// ship without a capability check.
var TrackerProbeScript = renderTrackerProbeScript(TrackerOps)

func buildTrackerOps() map[TrackerOpId]TrackerOp {
	ops := map[TrackerOpId]TrackerOp{
		TrackerOpCreate: {
			Id:        TrackerOpCreate,
			Action:    "create an immutable record; the body is verbatim; print the new task URI",
			Signature: `"${EPOCH_REC}" create --kind K --title T --phase P [--priority N] [--label L]... [--ref K=V]... [--body TEXT|-] -> ID`,
			Flags: []trackerFlag{
				{Name: "kind", Default: "task"},
				{Name: "title"},
				{Name: "phase"},
				{Name: "priority"},
				{Name: "label", Dest: "labels", Repeat: true},
				{Name: "ref", Dest: "refs", Repeat: true, RepeatNL: true},
				{Name: "body"},
			},
			Prelude: `[ "$body" = "-" ] && body="$(cat)"
    fold_refs`,
			PastureCalls: []pastureCall{
				{
					Name: "create",
					Argv: []string{"task", "create", `"$title"`, "--type", `"$kind"`, "--phase", `"$phase"`},
					Optional: []pastureOptional{
						{Var: "priority", Flag: "--priority"},
						{Var: "body", Flag: "--description"},
					},
					Scoped:    true,
					Format:    "text",
					CaptureID: true,
				},
				{
					Name:      "labels",
					Argv:      []string{"task", "label", "add", `"$id"`, `"$label"`},
					RepeatVar: "labels",
					Quiet:     true,
				},
			},
		},
		TrackerOpLabel: {
			Id:         TrackerOpLabel,
			Action:     "add a label; never remove one",
			Signature:  `"${EPOCH_REC}" label ID L   (assignment: label ID owner:<role>)`,
			Positional: "id",
			Rest:       "labels",
			Prelude:    `[ -n "$labels" ] || fail "label needs at least one label" "no label operand was given" "pass the label after the task URI"`,
			PastureCalls: []pastureCall{{
				Name:      "label",
				Argv:      []string{"task", "label", "add", `"$id"`, `"$label"`},
				RepeatVar: "labels",
			}},
		},
		TrackerOpBlock: {
			Id:         TrackerOpBlock,
			Action:     "add an edge; a blocked_by edge keeps the parent open until the child closes; a typed edge uses --kind",
			Signature:  `"${EPOCH_REC}" block PARENT --by CHILD [--kind blocked_by|derived_from|supersedes|discovered_from]`,
			Positional: "parent",
			Flags: []trackerFlag{
				{Name: "by", Dest: "child"},
				{Name: "kind", Dest: "edge_kind", Default: "blocked_by"},
			},
			Prelude: `[ -n "$child" ] || fail "block needs --by CHILD" "the child operand was empty" "pass block PARENT --by CHILD"`,
			PastureCalls: []pastureCall{
				{
					Name: "blocked_by",
					When: `[ "$edge_kind" = blocked_by ]`,
					Argv: []string{"task", "dep", "add", `"$parent"`, "--blocked-by", `"$child"`},
				},
				{
					Name: "typed",
					Argv: []string{"task", "dep", "add", `"$parent"`, "--target", `"$child"`, "--kind", `"$edge_kind"`},
				},
			},
		},
		TrackerOpNote: {
			Id:         TrackerOpNote,
			Action:     "append an attributed immutable note; --to makes it a message",
			Signature:  `"${EPOCH_REC}" note ID --author A [--kind K] [--to ROLE] [--body TEXT|-]`,
			Positional: "id",
			Flags: []trackerFlag{
				{Name: "author", Dest: "AUTHOR"},
				{Name: "body", Dest: "note_body"},
				{Name: "kind", Dest: "note_kind"},
				{Name: "to", Dest: "note_to"},
				{Name: "format", Dest: "fmt", Default: "json"},
			},
			Prelude: `[ "$note_body" = "-" ] && note_body="$(cat)"
    [ -n "$AUTHOR" ] || fail "note needs --author" "the tracker never invents an author" "pass the registered actor id"
    [ -n "$note_kind" ] && note_body="[$note_kind] $note_body"
    [ -n "$note_to" ] && note_body="to:$note_to $note_body"`,
			PastureCalls: []pastureCall{{
				Name: "note",
				Argv: []string{"task", "comment", "add", `"$id"`, "--author", `"$AUTHOR"`, "--", `"$note_body"`},
			}},
		},
		TrackerOpShow: {
			Id:         TrackerOpShow,
			Action:     "read one record, including description, phase, priority and refs; --notes lists notes. Pasture show returns createdAt and updatedAt; ledger timestamps stay on --history",
			Signature:  `"${EPOCH_REC}" show ID [--notes] [--history] [--format text|json]`,
			Positional: "id",
			Flags: []trackerFlag{
				{Name: "notes", Dest: "mode", Bool: true, BoolValue: "notes"},
				{Name: "history", Dest: "mode", Bool: true, BoolValue: "history"},
				{Name: "format", Dest: "fmt", Default: "json"},
			},
			PastureCalls: []pastureCall{
				{Name: "notes", When: `[ "$mode" = notes ]`, Argv: []string{"task", "comments", `"$id"`, "--format", `"$fmt"`}},
				{Name: "history", When: `[ "$mode" = history ]`, Argv: []string{"task", "timeline", `"$id"`}},
				{Name: "show", Argv: []string{"task", "show", `"$id"`, "--format", `"$fmt"`}},
			},
		},
		TrackerOpQuery: {
			Id:        TrackerOpQuery,
			Action:    "enumerate records by status, label, blocked state, or dependency tree; --blocked rejects --status",
			Prelude:   `[ "$blocked" != 1 ] || [ -z "$status" ] || fail "query rejected --blocked with --status" "the blocked read has no --status flag, so forwarding it exits 1" "drop --status, or run query --status without --blocked"`,
			Signature: `"${EPOCH_REC}" query [--status S] [--label L] [--blocked] [--tree ID] [--all-namespaces] [--format text|json]`,
			Flags: []trackerFlag{
				{Name: "status"},
				{Name: "label"},
				{Name: "tree"},
				{Name: "format", Dest: "fmt", Default: "json"},
				{Name: "blocked", Dest: "blocked", Bool: true},
				{Name: "all-namespaces", Dest: "allns", Bool: true},
			},
			PastureCalls: []pastureCall{
				{Name: "tree", When: `[ -n "$tree" ]`, Argv: []string{"task", "dep", "tree", `"$tree"`}, ExitAfter: true},
				{
					Name:      "blocked",
					When:      `[ "$blocked" = 1 ]`,
					Argv:      []string{"task", "blocked"},
					Optional:  []pastureOptional{{Var: "label", Flag: "--label"}},
					Scoped:    true,
					PassAllNS: true,
					Format:    `"$fmt"`,
				},
				{
					Name:      "list",
					Argv:      []string{"task", "list"},
					Optional:  []pastureOptional{{Var: "status", Flag: "--status"}, {Var: "label", Flag: "--label"}},
					Scoped:    true,
					PassAllNS: true,
					Format:    `"$fmt"`,
				},
			},
		},
		TrackerOpReady: {
			Id:        TrackerOpReady,
			Action:    "list non-closed records with no non-closed blocker",
			Signature: `"${EPOCH_REC}" ready [--label L] [--all-namespaces] [--format text|json]`,
			Flags: []trackerFlag{
				{Name: "label"},
				{Name: "format", Dest: "fmt", Default: "json"},
				{Name: "all-namespaces", Dest: "allns", Bool: true},
			},
			PastureCalls: []pastureCall{{
				Name:      "ready",
				Argv:      []string{"task", "ready"},
				Optional:  []pastureOptional{{Var: "label", Flag: "--label"}},
				Scoped:    true,
				PassAllNS: true,
				Format:    `"$fmt"`,
			}},
		},
		TrackerOpStatus: {
			Id:         TrackerOpStatus,
			Action:     "change state (start work); the store keeps the only status field",
			Signature:  `"${EPOCH_REC}" status ID --set open|in_progress|closed`,
			Positional: "id",
			Flags:      []trackerFlag{{Name: "set", Dest: "status"}},
			Prelude:    `[ -n "$status" ] || fail "status needs --set" "no target status was given" "pass --set open, in_progress or closed"`,
			PastureCalls: []pastureCall{{
				Name: "status",
				Argv: []string{"task", "update", `"$id"`, "--status", `"$status"`},
			}},
		},
		TrackerOpClose: {
			Id:         TrackerOpClose,
			Action:     "close a record; policy reserves this for the supervisor",
			Signature:  `"${EPOCH_REC}" close ID --reason R`,
			Positional: "id",
			Flags:      []trackerFlag{{Name: "reason"}},
			PastureCalls: []pastureCall{{
				Name:     "close",
				Argv:     []string{"task", "close", `"$id"`},
				Optional: []pastureOptional{{Var: "reason", Flag: "--reason"}},
			}},
		},
		TrackerOpWhoami: {
			Id:             TrackerOpWhoami,
			Action:         "resolve the acting author; never invent one",
			Signature:      `"${EPOCH_REC}" whoami`,
			RequiresAuthor: true,
			PastureCalls: []pastureCall{{
				Name:      "whoami",
				Argv:      []string{"task", "agents", "show", `"$AUTHOR"`},
				Quiet:     true,
				ThenPrint: "AUTHOR",
			}},
		},
		TrackerOpSignal: {
			Id:        TrackerOpSignal,
			Action:    "record one user signal; pasture notes the feedback task, ledger appends one JSON object",
			Signature: `"${EPOCH_REC}" signal --kind K --phase P --skill S --entry E --quote Q`,
			Flags: []trackerFlag{
				{Name: "kind"},
				{Name: "phase"},
				{Name: "skill"},
				{Name: "entry"},
				{Name: "quote"},
			},
			Prelude: signalPrelude(),
			PastureCalls: []pastureCall{{
				Name: "note",
				Argv: []string{"task", "comment", "add", `"$FEEDBACK_ID"`, "--author", `"$AUTHOR"`, "--", `"$signal_line"`},
			}},
		},
	}
	for id, op := range ops {
		op.Pasture = renderPastureDoc(op)
		op.Ledger = renderLedgerDoc(op)
		ops[id] = op
	}
	return ops
}

func renderPastureDoc(op TrackerOp) string {
	if op.Id == TrackerOpSignal {
		return `read feedback: from EPOCH.md; refuse an empty id; pasture task comment add "$FEEDBACK_ID" --author "$AUTHOR" -- "$signal_json"`
	}
	if op.Id == TrackerOpWhoami {
		return `pasture task agents show "$AUTHOR"; print "$AUTHOR"`
	}
	parts := make([]string, 0, len(op.PastureCalls))
	for _, call := range op.PastureCalls {
		words := append([]string(nil), call.Argv...)
		if call.Scoped {
			words = append(words, "--namespace", `"$NS"`)
		}
		parts = append(parts, strings.Join(words, " "))
	}
	return "pasture " + strings.Join(parts, "; ")
}

func renderLedgerDoc(op TrackerOp) string {
	switch op.Id {
	case TrackerOpSignal:
		return "append one JSON object to the signals.jsonl beside EPOCH.md; fields kind, phase, skill, entry, quote"
	case TrackerOpShow, TrackerOpQuery, TrackerOpReady, TrackerOpWhoami:
		return "read .agents.local/pasture/ledger/<task>.jsonl; no mutation"
	default:
		return "append one line to .agents.local/pasture/ledger/<task>.jsonl; fields in ledgerFieldOrder"
	}
}

// validateTrackerDispatch fails the build when the table and the rules that
// keep it honest disagree: whoami has one pasture call, every advertised flag
// is parsed, and every scoped read passes --namespace.
func validateTrackerDispatch(ops map[TrackerOpId]TrackerOp) error {
	if len(ops[TrackerOpWhoami].PastureCalls) != 1 {
		return fmt.Errorf("tracker contract: whoami has %d pasture calls, want 1 — why: whoami has exactly one mapping; where: internal/codegen/specs_data_tracker_script.go TrackerOpWhoami; fix: keep a single task agents show call", len(ops[TrackerOpWhoami].PastureCalls))
	}
	who := ops[TrackerOpWhoami].PastureCalls[0]
	if strings.Join(who.Argv, " ") != `task agents show "$AUTHOR"` {
		return fmt.Errorf("tracker contract: whoami pasture argv is %q — why: the one mapping is agents show of the recorded author, not agents list; where: internal/codegen/specs_data_tracker_script.go; fix: set the whoami call to task agents show \"$AUTHOR\"", strings.Join(who.Argv, " "))
	}
	flagRe := regexp.MustCompile(`--([a-z0-9-]+)`)
	for _, id := range canonicalTrackerOpIds {
		op := ops[id]
		declared := map[string]bool{}
		for _, flag := range op.Flags {
			declared[flag.Name] = true
		}
		for _, match := range flagRe.FindAllStringSubmatch(op.Signature, -1) {
			name := match[1]
			if name == "by" || declared[name] {
				if name == "by" && !declared["by"] {
					return fmt.Errorf("tracker contract: %s advertises --by but does not parse it — where: TrackerOps[%s].Flags; fix: add the flag", id, id)
				}
				continue
			}
			if !declared[name] {
				return fmt.Errorf("tracker contract: %s advertises --%s but the parser does not accept it — why: an advertised flag that is not parsed is dropped; where: TrackerOps[%s]; fix: add the flag or remove it from the signature", id, name, id)
			}
		}
		if strings.Contains(op.Signature, "--ref") {
			found := false
			for _, flag := range op.Flags {
				if flag.Name == "ref" {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("tracker contract: create advertises --ref but does not parse it — fix: add the ref flag and fold it into the description")
			}
		}
	}
	for _, need := range []struct {
		id   TrackerOpId
		name string
	}{
		{TrackerOpCreate, "create"},
		{TrackerOpQuery, "list"},
		{TrackerOpQuery, "blocked"},
		{TrackerOpReady, "ready"},
	} {
		call, ok := callByName(ops[need.id], need.name)
		if !ok || !call.Scoped {
			return fmt.Errorf("tracker contract: %s call %q is not namespace-scoped — why: scoped reads must pass --namespace unless the caller asked for all namespaces; where: TrackerOps[%s]; fix: set Scoped on that call", need.id, need.name, need.id)
		}
	}
	verbs := probeVerbs(ops)
	seen := map[string]bool{}
	for _, verb := range verbs {
		seen[verb] = true
	}
	if !seen["task blocked"] {
		return fmt.Errorf("tracker contract: probe verb list omits task blocked — why: query --blocked calls it; where: probeVerbs; fix: derive the verb from the blocked call")
	}
	return nil
}

func callByName(op TrackerOp, name string) (pastureCall, bool) {
	for _, call := range op.PastureCalls {
		if call.Name == name {
			return call, true
		}
	}
	return pastureCall{}, false
}

func probeVerbs(ops map[TrackerOpId]TrackerOp) []string {
	seen := map[string]bool{}
	var verbs []string
	for _, id := range canonicalTrackerOpIds {
		for _, call := range ops[id].PastureCalls {
			if len(call.Argv) < 2 || call.Argv[0] != "task" {
				continue
			}
			verb := "task " + call.Argv[1]
			if seen[verb] {
				continue
			}
			seen[verb] = true
			verbs = append(verbs, verb)
		}
	}
	return verbs
}

func renderTrackerProbeScript(ops map[TrackerOpId]TrackerOp) string {
	var verbs strings.Builder
	for i, verb := range probeVerbs(ops) {
		if i == 0 {
			fmt.Fprintf(&verbs, "\"%s\"", verb)
			continue
		}
		if i%3 == 0 {
			fmt.Fprintf(&verbs, " \\\n            \"%s\"", verb)
			continue
		}
		fmt.Fprintf(&verbs, " \"%s\"", verb)
	}
	return insertAfterShebang(fmt.Sprintf(probeScriptTemplate, verbs.String()), namespaceDeriveShell)
}

// insertAfterShebang keeps the script executable and puts the shared function
// where both the header printer and the write path can call it.
func insertAfterShebang(script, extra string) string {
	const shebang = "#!/bin/sh\n"
	if strings.HasPrefix(script, shebang) {
		return shebang + extra + script[len(shebang):]
	}
	return extra + script
}

// namespaceDeriveShell is the one ledger normalisation. The probe prints it
// as NS. Every ledger write calls it unless EPOCH.md records namespace:.
// Pasture is not given a copy: it derives when the adapter omits --namespace.
const namespaceDeriveShell = `# derive_namespace is the one ledger normalisation. Forms: git@host:path,
# ssh://, git://, http://, https:// and user@host:path. An empty remote is
# file:// of the physical working directory. PASTURE_NAMESPACE matches the
# value Pasture uses when --namespace is omitted.
derive_namespace() {
  if [ -n "${PASTURE_NAMESPACE:-}" ]; then
    printf '%s\n' "$PASTURE_NAMESPACE"
    return 0
  fi
  remote=$(git remote get-url origin 2>/dev/null || true)
  remote=$(printf '%s' "$remote" | tr -d '\r\n')
  if [ -z "$remote" ]; then
    dir=$(pwd -P 2>/dev/null || pwd)
    printf 'file://%s\n' "$dir"
    return 0
  fi
  normalize_git_remote "$remote"
}

normalize_git_remote() {
  url=$1
  case "$url" in
    ssh://*)
      rest=${url#ssh://}
      case "$rest" in
        *@*) rest=${rest#*@} ;;
      esac
      url="https://$rest"
      ;;
    git://*)
      url="https://${url#git://}"
      ;;
    http://*)
      url="https://${url#http://}"
      ;;
    https://*)
      ;;
    *@*:*)
      rest=${url#*@}
      host=${rest%%:*}
      path=${rest#*:}
      url="https://$host/$path"
      ;;
  esac
  case "$url" in
    *.git) url=${url%.git} ;;
  esac
  printf '%s\n' "$url"
}

`

const probeScriptTemplate = `#!/bin/sh
# Epoch tracker probe. Side-effect free. Prints KEY=VALUE lines and exits 0.
# Order: capability (--help), store existence (test -f), usability (migrate --dry-run).
# Verb list is generated from TrackerOps.
set -u

PASTURE_BIN="${PASTURE_BIN:-pasture}"
LEDGER_DIR="${PWD}/.agents.local/pasture/ledger"
REC_PATH="${PWD}/.agents.local/pasture/rec"

emit() {
  printf 'BACKEND=%%s\n' "$1"
  printf 'REASON=%%s\n' "${2:-}"
  [ -n "${3:-}" ] && printf 'DB=%%s\n' "$3"
  [ -n "${4:-}" ] && printf 'NS=%%s\n' "$4"
  [ -n "${5:-}" ] && printf 'ACT=%%s\n' "$5"
  [ -n "${6:-}" ] && printf 'VER=%%s\n' "$6"
  printf 'LEDGER=%%s\n' "$LEDGER_DIR"
  printf 'REC=%%s\n' "$REC_PATH"
  printf 'PROBED_AT=%%s\n' "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"
  exit 0
}

# Ledger header value. Same function every ledger write calls.
emit_ledger() {
  _ns=$(derive_namespace)
  emit ledger "$1" "${2:-}" "$_ns" "" "${3:-}"
}

# 1. Capability. --help never opens a store.
if ! command -v "$PASTURE_BIN" >/dev/null 2>&1; then
  emit_ledger not-installed
fi

VER="$("$PASTURE_BIN" --version 2>/dev/null | awk '{print $NF}')"

"$PASTURE_BIN" --help >/dev/null 2>&1
if [ "$?" -ne 0 ]; then
  emit_ledger help-failed "" "$VER"
fi

for VERB in %s; do
  # shellcheck disable=SC2086
  "$PASTURE_BIN" $VERB --help >/dev/null 2>&1
  if [ "$?" -ne 0 ]; then
    emit_ledger "missing-verb:$VERB" "" "$VER"
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
  emit_ledger no-store "$DB" "$VER"
fi

# 3. Usability. --dry-run leaves the file's SHA-256 unchanged.
"$PASTURE_BIN" --db "$DB" migrate --dry-run >/dev/null 2>&1
RC=$?
if [ "$RC" -ne 0 ]; then
  emit error "store-exit-$RC" "$DB" "" "" "$VER"
fi

# 4. Author. A missing author is a warning, not an unusable store.
# The probe does not derive a namespace for pasture. Writing one into
# EPOCH.md would freeze it and override Pasture's own derivation.

ACT="${PASTURE_AUTHOR:-pasture-system/00000000-0000-0000-0000-000000000000}"
"$PASTURE_BIN" --db "$DB" task agents show "$ACT" --format json >/dev/null 2>&1
RC=$?
if [ "$RC" -ne 0 ]; then
  printf 'WARN=no-author\n'
  printf 'probe: warning: author %%s is not registered in %%s\n  why: task agents show exited %%s, and a missing author is not an unusable store\n  where: epoch tracker probe, author check\n  fix: register the actor, or export PASTURE_AUTHOR to a registered id; the epoch still records backend pasture\n' "$ACT" "$DB" "$RC" >&2
fi

emit pasture "" "$DB" "" "$ACT" "$VER"
`

// signalPrelude is the signal operation's checks. Kind and phase are closed
// sets rendered from the registries, so a hand-written list cannot drift.
func signalPrelude() string {
	kinds := strings.Join(signalKindNames(), "|")
	kindList := strings.Join(signalKindNames(), ", ")
	phases := strings.Join(signalPhaseNames(), "|")
	phaseList := strings.Join(signalPhaseNames(), ", ")
	return fmt.Sprintf(`[ -n "$kind" ] || fail "signal needs --kind" "the kind operand was empty" "pass --kind with one registered signal kind"
    [ -n "$phase" ] || fail "signal needs --phase" "the phase operand was empty" "pass --phase with one registered phase name"
    [ -n "$skill" ] || fail "signal needs --skill" "the skill operand was empty" "pass --skill with the skill directory name"
    [ -n "$entry" ] || fail "signal needs --entry" "the entry operand was empty" "pass --entry with the record that holds the interaction"
    [ -n "$quote" ] || fail "signal needs --quote" "the quote operand was empty" "pass --quote with a short verbatim fragment"
    case "$kind" in
      %s) ;;
      *) fail "signal rejected kind '$kind'" "the kind is not in the closed set, so a later count would treat an unregistered label as data" "pass --kind with one of: %s" "signal operation in the tracker adapter, before the backend call" ;;
    esac
    case "$phase" in
      %s) ;;
      *) fail "signal rejected phase '$phase'" "the phase is not in the registered vocabulary, so the record would not join the phase it names" "pass --phase with one of: %s" "signal operation in the tracker adapter, before the backend call" ;;
    esac
    signal_line=$(signal_json)
    if [ "$BACKEND" != ledger ]; then
      FEEDBACK_ID=$(field feedback)
      [ -n "$FEEDBACK_ID" ] || fail "signal has no feedback task" "EPOCH.md has no feedback: line, so a note would run with an empty id and record nothing" "the lookup rule is in Signal Capture; do not create a task from the signal operation"
      [ -n "$AUTHOR" ] || fail "signal needs a recorded author" "the header author: line and PASTURE_AUTHOR are both empty" "set author: in $EPOCH_FILE or export PASTURE_AUTHOR to a registered actor id"
    fi`, kinds, kindList, phases, phaseList)
}

// renderSignalJSONFunc writes one JSON object. Field order is SignalLineFields,
// the same registry the line template reads, so a reorder has one home.
func renderSignalJSONFunc() string {
	var format strings.Builder
	args := make([]string, 0, len(SignalLineFields))
	format.WriteString("{")
	for i, field := range SignalLineFields {
		if i > 0 {
			format.WriteString(",")
		}
		fmt.Fprintf(&format, `"%s":"%%s"`, field.Field)
		args = append(args, fmt.Sprintf(`"$(json_escape "$%s")"`, field.Field))
	}
	format.WriteString("}")
	var b strings.Builder
	b.WriteString("signal_json() {\n")
	fmt.Fprintf(&b, "  printf '%s' \\\n", format.String())
	for i, arg := range args {
		if i == len(args)-1 {
			fmt.Fprintf(&b, "    %s\n", arg)
			continue
		}
		fmt.Fprintf(&b, "    %s \\\n", arg)
	}
	b.WriteString("}\n\n")
	return b.String()
}

// signalJSONKeyOrder reads the key sequence the rendered signal_json writer
// emits. The order guard compares this to an independent literal.
func signalJSONKeyOrder(script string) ([]string, error) {
	start := strings.Index(script, "signal_json() {")
	if start < 0 {
		return nil, fmt.Errorf("signal field order: rendered rec has no signal_json function — why: the writer is generated from SignalLineFields; where: internal/codegen/specs_data_tracker_script.go renderSignalJSONFunc; fix: keep signal_json in the rec asset")
	}
	rest := script[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		return nil, fmt.Errorf("signal field order: signal_json function is not closed — where: rendered TrackerRecScript; fix: keep the function terminated before the next definition")
	}
	re := regexp.MustCompile(`"([a-z]+)":"%s"`)
	var keys []string
	for _, match := range re.FindAllStringSubmatch(rest[:end], -1) {
		keys = append(keys, match[1])
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("signal field order: signal_json emits no keys — where: internal/codegen/specs_data_tracker_script.go renderSignalJSONFunc; fix: render one %%s field per SignalLineFields entry")
	}
	return keys, nil
}

func renderTrackerRecScript(ops map[TrackerOpId]TrackerOp) string {
	var b strings.Builder
	b.WriteString(insertAfterShebang(recPreamble, namespaceDeriveShell))
	b.WriteString(renderLedgerLineFunc())
	b.WriteString(renderSignalJSONFunc())
	b.WriteString(ledgerRuntime)
	b.WriteString("case \"$OP\" in\n")
	for _, id := range canonicalTrackerOpIds {
		b.WriteString(renderOpArm(ops[id]))
	}
	fmt.Fprintf(&b, `  *)
    fail "unknown operation '$OP'" "the operation set is closed" \
      "use one of %s; run the probe if the header is missing"
    ;;
esac
`, trackerOpNames())
	return b.String()
}

func renderOpArm(op TrackerOp) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  # dispatch:%s\n", op.Id)
	fmt.Fprintf(&b, "  %s)\n", op.Id)
	b.WriteString(renderParser(op))
	if op.RequiresAuthor {
		b.WriteString("    [ -n \"$AUTHOR\" ] || fail \"whoami has no recorded author\" \\\n")
		b.WriteString("      \"neither the header author: line nor PASTURE_AUTHOR names an actor\" \\\n")
		b.WriteString("      \"set author: in $EPOCH_FILE or export PASTURE_AUTHOR to a registered actor id\"\n")
	}
	if op.Prelude != "" {
		b.WriteString("    " + op.Prelude + "\n")
	}
	fmt.Fprintf(&b, "    if [ \"$BACKEND\" = ledger ]; then\n      ledger_%s\n      exit $?\n    fi\n", op.Id)
	renderPastureCalls(&b, op.PastureCalls)
	b.WriteString("    ;;\n")
	return b.String()
}

func renderParser(op TrackerOp) string {
	var b strings.Builder
	for _, flag := range op.Flags {
		dest := flag.dest()
		if flag.Default != "" {
			fmt.Fprintf(&b, "    %s=%s\n", dest, flag.Default)
		} else if flag.Bool {
			if dest == "mode" {
				if !strings.Contains(b.String(), "\n    mode=show\n") {
					b.WriteString("    mode=show\n")
				}
			} else {
				fmt.Fprintf(&b, "    %s=0\n", dest)
			}
		} else if dest != "AUTHOR" && dest != "fmt" {
			fmt.Fprintf(&b, "    %s=\n", dest)
		}
	}
	if op.Positional != "" {
		fmt.Fprintf(&b, "    %s=\"${1:-}\"\n", op.Positional)
		b.WriteString("    [ \"$#\" -gt 0 ] && shift\n")
		fmt.Fprintf(&b, "    need_id \"$%s\"\n", op.Positional)
	}
	if op.Rest != "" {
		fmt.Fprintf(&b, "    %s=\n", op.Rest)
	}
	if len(op.Flags) == 0 && op.Rest == "" {
		return b.String()
	}
	b.WriteString("    while [ \"$#\" -gt 0 ]; do\n")
	b.WriteString("      case \"$1\" in\n")
	for _, flag := range op.Flags {
		dest := flag.dest()
		if flag.Bool {
			value := "1"
			if flag.BoolValue != "" {
				value = flag.BoolValue
			}
			fmt.Fprintf(&b, "        --%s) %s=%s; shift ;;\n", flag.Name, dest, value)
			continue
		}
		if flag.Repeat && flag.RepeatNL {
			fmt.Fprintf(&b, "        --%s)\n", flag.Name)
			fmt.Fprintf(&b, "          if [ -z \"$%s\" ]; then %s=\"$2\"; else %s=\"$%s\n$2\"; fi\n", dest, dest, dest, dest)
			b.WriteString("          shift 2 ;;\n")
			fmt.Fprintf(&b, "        --%s=*)\n", flag.Name)
			fmt.Fprintf(&b, "          _val=\"${1#*=}\"\n")
			fmt.Fprintf(&b, "          if [ -z \"$%s\" ]; then %s=\"$_val\"; else %s=\"$%s\n$_val\"; fi\n", dest, dest, dest, dest)
			b.WriteString("          shift ;;\n")
			continue
		}
		if flag.Repeat {
			if dest == "labels" {
				fmt.Fprintf(&b, "        --%s) label_push \"$2\"; shift 2 ;;\n", flag.Name)
				fmt.Fprintf(&b, "        --%s=*) label_push \"${1#*=}\"; shift ;;\n", flag.Name)
				continue
			}
			fmt.Fprintf(&b, "        --%s) %s=\"$%s $2\"; shift 2 ;;\n", flag.Name, dest, dest)
			fmt.Fprintf(&b, "        --%s=*) %s=\"$%s ${1#*=}\"; shift ;;\n", flag.Name, dest, dest)
			continue
		}
		fmt.Fprintf(&b, "        --%s) %s=\"$2\"; shift 2 ;;\n", flag.Name, dest)
		fmt.Fprintf(&b, "        --%s=*) %s=\"${1#*=}\"; shift ;;\n", flag.Name, dest)
	}
	if op.Rest != "" {
		fmt.Fprintf(&b, "        --*) fail \"%s rejected argument '$1'\" \"the argument is not in the %s contract\" \"pass the label as a positional after the task URI\" ;;\n", op.Id, op.Id)
		if op.Rest == "labels" {
			b.WriteString("        *) label_push \"$1\"; shift ;;\n")
		} else {
			fmt.Fprintf(&b, "        *) %s=\"$%s $1\"; shift ;;\n", op.Rest, op.Rest)
		}
	} else {
		names := make([]string, 0, len(op.Flags))
		for _, flag := range op.Flags {
			names = append(names, "--"+flag.Name)
		}
		hint := strings.Join(names, ", ")
		if hint == "" {
			hint = "no flags"
		}
		fmt.Fprintf(&b, "        *) fail \"%s rejected argument '$1'\" \"the argument is not in the %s contract\" \"use %s\" ;;\n", op.Id, op.Id, hint)
	}
	b.WriteString("      esac\n")
	b.WriteString("    done\n")
	return b.String()
}

func renderPastureCalls(b *strings.Builder, calls []pastureCall) {
	opened := false
	for i, call := range calls {
		indent := "      "
		if call.When != "" {
			if !opened {
				fmt.Fprintf(b, "      if %s; then\n", call.When)
				opened = true
			} else {
				fmt.Fprintf(b, "      elif %s; then\n", call.When)
			}
			indent = "        "
		} else if opened {
			b.WriteString("      else\n")
			indent = "        "
		}
		writePastureCall(b, call, indent)
		if i == len(calls)-1 && opened {
			b.WriteString("      fi\n")
		}
	}
}

func writePastureCall(b *strings.Builder, call pastureCall, indent string) {
	inner := indent
	if call.RepeatVar != "" {
		fmt.Fprintf(b, "%sset -f\n", indent)
		fmt.Fprintf(b, "%sfor _enc in $%s; do\n", indent, call.RepeatVar)
		fmt.Fprintf(b, "%s  [ -n \"$_enc\" ] || continue\n", indent)
		fmt.Fprintf(b, "%s  row_decode_set \"$_enc\"\n", indent)
		fmt.Fprintf(b, "%s  label=$decoded\n", indent)
		inner = indent + "  "
	}
	if call.CaptureID {
		b.WriteString(inner + "set --")
		for _, word := range call.Argv {
			b.WriteString(" " + word)
		}
		b.WriteString("\n")
		for _, opt := range call.Optional {
			fmt.Fprintf(b, "%s[ -n \"$%s\" ] && set -- \"$@\" %s \"$%s\"\n", inner, opt.Var, opt.Flag, opt.Var)
		}
		if call.Scoped {
			fmt.Fprintf(b, "%sif [ \"$allns\" != 1 ] && [ -n \"$NS\" ]; then set -- \"$@\" --namespace \"$NS\"; fi\n", inner)
		}
		fmt.Fprintf(b, "%sout=\"$(pb \"$@\" --format text)\" || exit $?\n", inner)
		fmt.Fprintf(b, "%sid=\"$(printf '%%s\\n' \"$out\" | sed -n 's/^ID: *//p' | head -1)\"\n", inner)
		fmt.Fprintf(b, "%s[ -n \"$id\" ] || fail \"create returned no task ID\" \"the backend text output had no ID line\" \"check the store named in $EPOCH_FILE and the backend help page\"\n", inner)
		fmt.Fprintf(b, "%sprintf '%%s\\n' \"$id\"\n", inner)
		return
	}
	b.WriteString(inner + "set --")
	for _, word := range call.Argv {
		b.WriteString(" " + word)
	}
	b.WriteString("\n")
	for _, opt := range call.Optional {
		fmt.Fprintf(b, "%s[ -n \"$%s\" ] && set -- \"$@\" %s \"$%s\"\n", inner, opt.Var, opt.Flag, opt.Var)
	}
	if call.Scoped {
		fmt.Fprintf(b, "%sif [ \"$allns\" != 1 ] && [ -n \"$NS\" ]; then set -- \"$@\" --namespace \"$NS\"; fi\n", inner)
	}
	if call.PassAllNS {
		fmt.Fprintf(b, "%s[ \"$allns\" = 1 ] && set -- \"$@\" --all-namespaces\n", inner)
	}
	if call.Format != "" {
		fmt.Fprintf(b, "%sset -- \"$@\" --format %s\n", inner, call.Format)
	}
	redir := ""
	if call.Quiet || call.ThenPrint != "" {
		redir = " >/dev/null"
	}
	fmt.Fprintf(b, "%spb \"$@\"%s || exit $?\n", inner, redir)
	if call.ThenPrint != "" {
		fmt.Fprintf(b, "%sprintf '%%s\\n' \"$%s\"\n", inner, call.ThenPrint)
	}
	if call.ExitAfter {
		fmt.Fprintf(b, "%sexit $?\n", inner)
	}
	if call.RepeatVar != "" {
		fmt.Fprintf(b, "%sdone\n", indent)
		fmt.Fprintf(b, "%sset +f\n", indent)
	}
}

func renderLedgerLineFunc() string {
	var b strings.Builder
	b.WriteString("ledger_line() {\n")
	for _, key := range ledgerFieldOrder {
		if key == "at" {
			continue
		}
		fmt.Fprintf(&b, "  _f_%s=\n", key)
	}
	b.WriteString("  _f_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)\n")
	b.WriteString("  while [ \"$#\" -gt 1 ]; do\n")
	b.WriteString("    case \"$1\" in\n")
	for _, key := range ledgerFieldOrder {
		if key == "at" {
			continue
		}
		fmt.Fprintf(&b, "      %s) _f_%s=\"$2\"; shift 2 ;;\n", key, key)
	}
	b.WriteString("      *) fail \"ledger_line rejected field '$1'\" \"the ledger field set is closed\" \"use a field from the stable ledger order\" ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("  done\n")
	b.WriteString("  printf '{'\n")
	for i, key := range ledgerFieldOrder {
		prefix := ""
		if i > 0 {
			prefix = ","
		}
		fmt.Fprintf(&b, "  printf '%s\"%s\":\"%%s\"' \"$(json_escape \"$_f_%s\")\"\n", prefix, key, key)
	}
	b.WriteString("  printf '}'\n")
	b.WriteString("}\n\n")
	return b.String()
}

const recPreamble = `#!/bin/sh
# Epoch tracker adapter (rec). Generated from TrackerOps. Do not hand-edit.
# Backend: the one recorded in EPOCH_FILE. Never re-probes, never switches.
set -u

EPOCH_FILE="${EPOCH_FILE:-.agents.local/pasture/EPOCH.md}"
PASTURE_BIN="${PASTURE_BIN:-pasture}"

fail() {
  printf 'rec: %s\n  why: %s\n' "$1" "$2" >&2
  if [ -n "${4:-}" ]; then
    printf '  where: %s\n' "$4" >&2
  fi
  printf '  fix: %s\n' "$3" >&2
  exit 3
}

[ -f "$EPOCH_FILE" ] || fail "no epoch header at $EPOCH_FILE" \
  "the backend is chosen once and recorded before the first write; EPOCH_FILE is relative to the working directory" \
  "run from the repository root, or set EPOCH_FILE to the absolute header path, then run the Starting an Epoch detection step"

field() {
  sed -n "s/^$1: //p" "$EPOCH_FILE" 2>/dev/null | tail -1
}

BACKEND="$(field backend)"
DB="${PASTURE_DB_PATH:-$(field db)}"
# --namespace is passed only when EPOCH.md records one. Otherwise pasture
# derives (env, else git remote, else file:// of cwd). Ledger calls the same
# derivation function the probe printed, unless the header pins a namespace.
HEADER_NS="$(field namespace)"
if [ "$BACKEND" = ledger ] && [ -z "$HEADER_NS" ]; then
  NS=$(derive_namespace)
else
  NS="$HEADER_NS"
fi
AUTHOR="${PASTURE_AUTHOR:-$(field author)}"
LEDGER="$(field ledger)"
if [ -z "$LEDGER" ]; then
  LEDGER=".agents.local/pasture/ledger"
fi

case "$BACKEND" in
  pasture) : ;;
  ledger) : ;;
  error)
    fail "the recorded backend is error" \
      "the store exists but cannot be used, and falling back could lose earlier epochs" \
      "repair the store named in $EPOCH_FILE, or start a new epoch with a working backend" ;;
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

allns=0
blocked=0
refs=
labels=
body=
kind=task
title=
phase=
priority=
label=
status=
tree=
fmt=json
mode=show
reason=
note_body=
note_kind=
note_to=
parent=
child=
edge_kind=blocked_by
id=

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

# json_escape and row_encode are total for every byte except NUL, which a
# shell argument cannot carry. Each is the inverse of its reader (take, and
# row_decode). A trailing newline is kept: the decoder is not read with a
# bare command substitution.
json_escape() {
  ROW_IN=$1 awk 'BEGIN {
    s = ENVIRON["ROW_IN"]
    out = ""
    for (i = 1; i <= length(s); i++) {
      c = substr(s, i, 1)
      if (c == "\\") out = out "\\\\"
      else if (c == "\"") out = out "\\\""
      else if (c == "\t") out = out "\\t"
      else if (c == "\r") out = out "\\r"
      else if (c == "\n") out = out "\\n"
      else out = out c
    }
    printf "%s", out
  }'
}

row_encode() {
  ROW_IN=$1 awk 'BEGIN {
    s = ENVIRON["ROW_IN"]
    out = ""
    for (i = 1; i <= length(s); i++) {
      c = substr(s, i, 1)
      if (c == "\\") out = out "\\\\"
      else if (c == "\n") out = out "\\n"
      else if (c == "\r") out = out "\\r"
      else if (c == "\t") out = out "\\t"
      else if (c == " ") out = out "\\s"
      else if (c == "\037") out = out "\\037"
      else out = out c
    }
    printf "%s", out
  }'
}

row_decode() {
  ROW_IN=$1 awk 'BEGIN {
    s = ENVIRON["ROW_IN"]
    esc = 0
    out = ""
    i = 1
    n = length(s)
    while (i <= n) {
      c = substr(s, i, 1)
      if (esc) {
        if (c == "n") out = out "\n"
        else if (c == "r") out = out "\r"
        else if (c == "t") out = out "\t"
        else if (c == "s") out = out " "
        else if (c == "0" && substr(s, i, 3) == "037") { out = out "\037"; i += 2 }
        else if (c == "\\") out = out "\\"
        else out = out c
        esc = 0
      } else if (c == "\\") esc = 1
      else out = out c
      i++
    }
    if (esc) out = out "\\"
    printf "%s", out
  }'
}

# Sets decoded. A bare $(row_decode) would eat a trailing newline.
row_decode_set() {
  _raw=$(row_decode "$1"; printf '\003')
  _mark=$(printf '\003')
  decoded=${_raw%"$_mark"}
}

label_push() {
  enc=$(row_encode "$1")
  if [ -z "$labels" ]; then
    labels=$enc
  else
    labels="$labels $enc"
  fi
}

fold_refs() {
  [ -n "$refs" ] || return 0
  block="references:"
  old=$IFS
  IFS='
'
  for pair in $refs; do
    key=${pair%%=*}
    val=${pair#*=}
    block="$block
  $key: $val"
  done
  IFS=$old
  if [ -z "$body" ]; then
    body="---
$block
---"
  else
    body="---
$block
---
$body"
  fi
}

mint_id() {
  if [ -r /proc/sys/kernel/random/uuid ]; then
    printf 'ledger/%s\n' "$(cat /proc/sys/kernel/random/uuid)"
    return 0
  fi
  printf 'ledger/%s-%s\n' "$(date -u +%Y%m%dT%H%M%SZ)" "$$"
}

ledger_append() {
  rec_id="$1"
  rec_line="$2"
  mkdir -p "$LEDGER" || fail "cannot create ledger directory $LEDGER" "the parent is not writable" "choose a writable ledger: path in $EPOCH_FILE"
  safe=$(printf '%s' "$rec_id" | sed -e 's/%/%25/g' -e 's#/#%2F#g' -e 's/ /%20/g' -e 's/:/%3A/g')
  file="$LEDGER/$safe.jsonl"
  lock="$LEDGER/.lock"
  n=0
  while ! mkdir "$lock" 2>/dev/null; do
    n=$((n + 1))
    if [ "$n" -gt 100 ]; then
      fail "ledger lock held at $lock" "another rec process did not release it" "remove $lock only if no rec process is running, then retry"
    fi
    sleep 0.1
  done
  if [ ! -f "$LEDGER/.gitignore-offered" ]; then
    printf 'rec: ledger records are under %s\n  offer: add that directory to .gitignore if the records should stay uncommitted\n  note: a gitignore entry is offered, never required\n' "$LEDGER" >&2
    : > "$LEDGER/.gitignore-offered" || true
  fi
  printf '%s\n' "$rec_line" >> "$file" || {
    rmdir "$lock" 2>/dev/null || true
    fail "ledger append failed for $file" "the ledger directory was not writable" "make $LEDGER writable and retry the same operation"
  }
  rmdir "$lock" 2>/dev/null || true
}

`

const ledgerRuntime = `
ledger_signal() {
  dir=$(dirname "$EPOCH_FILE")
  file="$dir/signals.jsonl"
  mkdir -p "$dir" || fail "cannot create signal directory $dir" "the parent of EPOCH.md is not writable" "choose a writable EPOCH_FILE directory and retry the same signal"
  printf '%s\n' "$signal_line" >> "$file" || fail "signal append failed for $file" "the signal file was not writable" "make $dir writable and retry the same signal"
}

ledger_create() {
  id=$(mint_id)
  # Empty priority matches the pasture create flag default so show agrees.
  [ -n "$priority" ] || priority=medium
  ledger_append "$id" "$(ledger_line op create id "$id" namespace "$NS" title "$title" kind "$kind" phase "$phase" status open priority "$priority" author "$AUTHOR" ref "$refs" body "$body")"
  printf '%s\n' "$id"
  set -f
  for _enc in $labels; do
    [ -n "$_enc" ] || continue
    row_decode_set "$_enc"
    label=$decoded
    ledger_append "$id" "$(ledger_line op label id "$id" namespace "$NS" label "$label" author "$AUTHOR")"
  done
  set +f
}

ledger_label() {
  set -f
  for _enc in $labels; do
    [ -n "$_enc" ] || continue
    row_decode_set "$_enc"
    label=$decoded
    ledger_append "$id" "$(ledger_line op label id "$id" namespace "$NS" label "$label" author "$AUTHOR")"
  done
  set +f
}

ledger_block() {
  ledger_append "$parent" "$(ledger_line op block id "$parent" namespace "$NS" parent "$parent" child "$child" edge_kind "$edge_kind" author "$AUTHOR")"
}

ledger_note() {
  ledger_append "$id" "$(ledger_line op note id "$id" namespace "$NS" author "$AUTHOR" note_kind "$note_kind" to "$note_to" body "$note_body")"
}

ledger_status() {
  ledger_append "$id" "$(ledger_line op status id "$id" namespace "$NS" status "$status" author "$AUTHOR")"
}

ledger_close() {
  ledger_append "$id" "$(ledger_line op close id "$id" namespace "$NS" status closed reason "$reason" author "$AUTHOR")"
}

ledger_whoami() {
  printf '%s\n' "$AUTHOR"
}

ledger_files() {
  [ -d "$LEDGER" ] || return 0
  find "$LEDGER" -name '*.jsonl' -type f | sort
}

ledger_rows() {
  files=$(ledger_files)
  [ -n "$files" ] || return 0
  printf '%s\n' "$files" | while IFS= read -r f; do
    cat "$f"
  done | awk '
function take(line, pos,   i, c, out, esc) {
  out = ""
  esc = 0
  for (i = pos + 1; i <= length(line); i++) {
    c = substr(line, i, 1)
    if (esc) {
      if (c == "n") out = out "\n"
      else if (c == "t") out = out "\t"
      else if (c == "r") out = out "\r"
      else out = out c
      esc = 0
    } else if (c == "\\") esc = 1
    else if (c == "\"") { got = out; got_i = i + 1; return }
    else out = out c
  }
  got = out
  got_i = length(line) + 1
}
function parse(line,   i, n, c) {
  delete f
  i = 1
  n = length(line)
  while (i <= n) {
    c = substr(line, i, 1)
    if (c == "\"") {
      take(line, i)
      key = got
      i = got_i
      while (i <= n && substr(line, i, 1) != "\"") i++
      if (i > n) return
      take(line, i)
      f[key] = got
      i = got_i
    } else i++
  }
}
function add_label(id, lab,   cur, i, n, p) {
  if (lab == "") return
  cur = labels[id]
  n = split(cur, p, ",")
  for (i = 1; i <= n; i++) if (p[i] == lab) return
  if (cur == "") labels[id] = lab
  else labels[id] = cur "," lab
}
function add_edge(id, kind, child,   cur) {
  if (child == "") return
  cur = edges[id]
  item = kind ">" child
  if (cur == "") edges[id] = item
  else edges[id] = cur ";" item
}
function row_encode(s,   i, c, out) {
  out = ""
  if (s == "") return ""
  for (i = 1; i <= length(s); i++) {
    c = substr(s, i, 1)
    if (c == "\\") out = out "\\\\"
    else if (c == "\n") out = out "\\n"
    else if (c == "\r") out = out "\\r"
    else if (c == "\t") out = out "\\t"
    else if (c == " ") out = out "\\s"
    else if (c == "\037") out = out "\\037"
    else out = out c
  }
  return out
}
{
  parse($0)
  id = f["id"]
  if (id == "") next
  seen[id] = 1
  if (f["op"] == "create") {
    title[id] = f["title"]
    ns[id] = f["namespace"]
    body[id] = f["body"]
    phase[id] = f["phase"]
    priority[id] = f["priority"]
    ref[id] = f["ref"]
    if (st[id] == "") st[id] = "open"
  }
  if (f["namespace"] != "" && ns[id] == "") ns[id] = f["namespace"]
  if (f["op"] == "status" || f["op"] == "close" || f["op"] == "create") {
    if (f["status"] != "") st[id] = f["status"]
  }
  add_label(id, f["label"])
  if (f["op"] == "block") add_edge(id, f["edge_kind"], f["child"])
  if (f["op"] == "note") notes[id]++
}
END {
  for (id in seen) {
    blocked = 0
    n = split(edges[id], e, ";")
    for (i = 1; i <= n; i++) {
      split(e[i], p, ">")
      if (p[1] != "blocked_by") continue
      child = p[2]
      cst = st[child]
      if (cst != "closed") blocked = 1
    }
    ready = 0
    if (st[id] != "closed" && blocked == 0) ready = 1
    printf "%s\037%s\037%s\037%s\037%s\037%s\037%d\037%d\037%d\037%s\037%s\037%s\037%s\n", id, row_encode(ns[id]), row_encode(title[id]), st[id], row_encode(labels[id]), row_encode(edges[id]), notes[id]+0, ready, blocked, row_encode(phase[id]), row_encode(priority[id]), row_encode(ref[id]), row_encode(body[id])
  }
}
'
}

ledger_split() {
  row="$1"
  old=$IFS
  set -f
  IFS=$(printf '\037')
  # shellcheck disable=SC2086
  set -- $row
  set +f
  IFS=$old
  rid=${1:-}
  row_decode_set "${2:-}"; rns=$decoded
  row_decode_set "${3:-}"; rtitle=$decoded
  rstatus=${4:-}
  row_decode_set "${5:-}"; rlabels=$decoded
  row_decode_set "${6:-}"; redges=$decoded
  rcomments=${7:-}; rready=${8:-}; rblocked=${9:-}
  row_decode_set "${10:-}"; rphase=$decoded
  row_decode_set "${11:-}"; rpriority=$decoded
  row_decode_set "${12:-}"; rref=$decoded
  row_decode_set "${13:-}"; rbody=$decoded
}

ledger_match() {
  if [ "$allns" != 1 ] && [ -n "$NS" ] && [ "$rns" != "$NS" ]; then
    return 1
  fi
  if [ -n "$status" ] && [ "$rstatus" != "$status" ]; then
    return 1
  fi
  if [ -n "$label" ]; then
    case ",$rlabels," in
      *",$label,"*) : ;;
      *) return 1 ;;
    esac
  fi
  if [ "${want_blocked:-0}" = 1 ] && [ "$rblocked" != 1 ]; then
    return 1
  fi
  if [ "${want_ready:-0}" = 1 ] && [ "$rready" != 1 ]; then
    return 1
  fi
  return 0
}

ledger_emit() {
  want_blocked=${1:-0}
  want_ready=${2:-0}
  rows=$(ledger_rows | sort)
  if [ "$fmt" = text ]; then
    printf '%s\n' "$rows" | while IFS= read -r row; do
      [ -n "$row" ] || continue
      ledger_split "$row"
      ledger_match || continue
      printf '%s\t%s\t%s\n' "$rid" "$rtitle" "$rstatus"
    done
    return 0
  fi
  printf '['
  first=1
  printf '%s\n' "$rows" | while IFS= read -r row; do
    [ -n "$row" ] || continue
    ledger_split "$row"
    ledger_match || continue
    if [ "$first" = 1 ]; then first=0; else printf ','; fi
    printf '{"id":"%s","title":"%s","status":"%s","namespace":"%s","labels":"%s","edges":"%s","comments":%s}' \
      "$(json_escape "$rid")" "$(json_escape "$rtitle")" "$(json_escape "$rstatus")" \
      "$(json_escape "$rns")" "$(json_escape "$rlabels")" "$(json_escape "$redges")" \
      "$rcomments"
  done
  printf ']\n'
}

ledger_show() {
  rows=$(ledger_rows)
  found=0
  # notes and history replace the record, matching pasture's separate commands.
  if [ "$mode" = notes ] || [ "$mode" = history ]; then
    hit=$(printf '%s\n' "$rows" | while IFS= read -r row; do
      [ -n "$row" ] || continue
      ledger_split "$row"
      [ "$rid" = "$id" ] && printf yes
    done)
    [ -n "$hit" ] || fail "no ledger record $id" "no line names that id under $LEDGER" "create the record first, or pass the id create printed"
    safe=$(printf '%s' "$id" | sed -e 's/%/%25/g' -e 's#/#%2F#g' -e 's/ /%20/g' -e 's/:/%3A/g')
    file="$LEDGER/$safe.jsonl"
    if [ "$mode" = history ]; then
      cat "$file"
      return 0
    fi
    awk '
function take(line, pos,   i, c, out, esc) {
  out = ""; esc = 0
  for (i = pos + 1; i <= length(line); i++) {
    c = substr(line, i, 1)
    if (esc) {
      if (c == "n") out = out "\n"
      else if (c == "t") out = out "\t"
      else if (c == "r") out = out "\r"
      else out = out c
      esc = 0
    } else if (c == "\\") esc = 1
    else if (c == "\"") { got = out; got_i = i + 1; return }
    else out = out c
  }
}
{
  i = 1
  while (i <= length($0)) {
    if (substr($0, i, 1) == "\"") {
      take($0, i); key = got; i = got_i
      while (i <= length($0) && substr($0, i, 1) != "\"") i++
      take($0, i); f[key] = got; i = got_i
    } else i++
  }
  if (f["op"] == "note") print f["body"]
  delete f
}
' "$file"
    return 0
  fi
  printf '%s\n' "$rows" | while IFS= read -r row; do
    [ -n "$row" ] || continue
    ledger_split "$row"
    [ "$rid" = "$id" ] || continue
    found=1
    if [ "$fmt" = text ]; then
      printf 'id: %s\ntitle: %s\nstatus: %s\nnamespace: %s\nlabels: %s\nedges: %s\ncomments: %s\nphase: %s\npriority: %s\nref: %s\ndescription: %s\n' \
        "$rid" "$rtitle" "$rstatus" "$rns" "$rlabels" "$redges" "$rcomments" \
        "$rphase" "$rpriority" "$rref" "$rbody"
    else
      printf '{"id":"%s","title":"%s","status":"%s","namespace":"%s","labels":"%s","edges":"%s","comments":%s,"phase":"%s","priority":"%s","ref":"%s","description":"%s"}\n' \
        "$(json_escape "$rid")" "$(json_escape "$rtitle")" "$(json_escape "$rstatus")" \
        "$(json_escape "$rns")" "$(json_escape "$rlabels")" "$(json_escape "$redges")" \
        "$rcomments" \
        "$(json_escape "$rphase")" "$(json_escape "$rpriority")" "$(json_escape "$rref")" "$(json_escape "$rbody")"
    fi
  done
  printf '%s\n' "$rows" | while IFS= read -r row; do
    [ -n "$row" ] || continue
    ledger_split "$row"
    [ "$rid" = "$id" ] && found=1
  done
  # The pipeline above cannot export found. Re-scan plainly.
  hit=$(printf '%s\n' "$rows" | while IFS= read -r row; do
    [ -n "$row" ] || continue
    ledger_split "$row"
    [ "$rid" = "$id" ] && printf yes
  done)
  [ -n "$hit" ] || fail "no ledger record $id" "no line names that id under $LEDGER" "create the record first, or pass the id create printed"
}

ledger_query() {
  if [ -n "$tree" ]; then
    ledger_tree "$tree"
    return 0
  fi
  ledger_emit "$blocked" 0
}

ledger_ready() {
  ledger_emit 0 1
}

ledger_tree() {
  root="$1"
  rows=$(ledger_rows)
  printf '%s\n' "$rows" | while IFS= read -r row; do
    [ -n "$row" ] || continue
    ledger_split "$row"
    [ "$rid" = "$root" ] || continue
    printf '%s\t%s\n' "$rid" "$redges"
  done
}
`
