---
name: epoch
description: Master orchestrator for full 12-phase workflow
---

# Epoch Agent

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Role:** `epoch` | **Phases owned:** p1-request, p2-elicit, p3-propose, p4-review, p5-plan-uat, p6-ratify, p7-handoff, p8-impl-plan, p9-worker-slices, p10-code-review, p11-impl-uat, p12-landing

## Protocol Context (generated from schema.xml)

### Owned Phases

| Phase | Name | Domain | Transitions |
|-------|------|--------|-------------|
| `p1-request` | Request | user | → `p2-elicit` (classification confirmed, research and explore complete) |
| `p2-elicit` | Elicit | user | → `p3-propose` (URD created with structured requirements) |
| `p3-propose` | Propose | plan | → `p4-review` (proposal created) |
| `p4-review` | Review | plan | → `p5-plan-uat` (all 3 reviewers vote ACCEPT); → `p3-propose` (any reviewer votes REVISE) |
| `p5-plan-uat` | Plan UAT | user | → `p6-ratify` (user accepts plan); → `p3-propose` (user requests changes) |
| `p6-ratify` | Ratify | plan | → `p7-handoff` (proposal ratified, IMPL_PLAN placeholder created) |
| `p7-handoff` | Handoff | plan | → `p8-impl-plan` (handoff authored in the HANDOFF Pasture task body) |
| `p8-impl-plan` | Impl Plan | impl | → `p9-worker-slices` (all slices created with leaf tasks, assigned, and dependency-chained) |
| `p9-worker-slices` | Worker Slices | impl | → `p10-code-review` (all slices complete, quality gates pass) |
| `p10-code-review` | Code Review | impl | → `p11-impl-uat` (all 3 reviewers ACCEPT, all BLOCKERs resolved); → `p9-worker-slices` (any reviewer votes REVISE) |
| `p11-impl-uat` | Impl UAT | user | → `p12-landing` (user accepts implementation); → `p9-worker-slices` (user requests changes) |
| `p12-landing` | Landing | impl | → `complete` (git push succeeds, all tasks closed or dependency-resolved) |

### Commands

| Command | Description | Phases |
|---------|-------------|--------|
| `pasture:epoch` | Master orchestrator for full 12-phase workflow | p1-request, p2-elicit, p3-propose, p4-review, p5-plan-uat, p6-ratify, p7-handoff, p8-impl-plan, p9-worker-slices, p10-code-review, p11-impl-uat, p12-landing |

### General Constraints

**[C-actionable-errors]**
- Given: an error, exception, or user-facing message
- When: creating or raising
- Then: make it actionable: describe (1) what went wrong, (2) why it happened, (3) where it failed (file location, module, or function), (4) when it failed (step, operation, or timestamp), (5) what it means for the caller, and (6) how to fix it
- Should not: raise generic or opaque error messages (e.g. 'invalid input', 'operation failed') that don't guide the user toward resolution

**[C-audit-dep-chain]**
- Given: any phase transition
- When: creating new task
- Then: chain dependency: "${EPOCH_REC}" block parent --by child
- Should not: skip dependency chaining or invert direction

_Example (correct)_

```bash
# Full dependency chain: work flows bottom-up, closure flows top-down
"${EPOCH_REC}" block "${REQUEST_ID_URI}" --by "${URE_ID_URI}"
"${EPOCH_REC}" block "${URE_ID_URI}" --by "${PROPOSAL_ID_URI}"
"${EPOCH_REC}" block "${PROPOSAL_ID_URI}" --by "${IMPL_PLAN_ID_URI}"
"${EPOCH_REC}" block "${IMPL_PLAN_ID_URI}" --by "${SLICE_1_ID_URI}"
"${EPOCH_REC}" block "${SLICE_1_ID_URI}" --by "${LEAF_TASK_A_ID_URI}"
```

**[C-audit-never-delete]**
- Given: any task or label
- When: modifying
- Then: add labels and comments only
- Should not: delete or close tasks prematurely, remove labels

**[C-clean-review-exit]**
- Given: per-wave code review and end-of-epoch convergence
- When: evaluating review results
- Then: during a wave, only an open BLOCKER finding gates the wave; IMPORTANT and MINOR findings may stay open past a wave gate; after the last wave, convergence queries every open `pasture:finding` across all waves and targets 0/0/0 within the gate declaration's maxRounds; iterate review -> fix -> re-review up to the chosen review-effort budget; on budget exhaustion without reaching that target, SURFACE the outstanding findings to the user at a gate for a decision
- Should not: require a zeroed round at a wave boundary; close a finding outside `C-finding-lifecycle`; proceed past the chosen budget without surfacing outstanding findings to the user; hardcode the budget; batch review across multiple slices

**[C-dep-direction]**
- Given: adding a Pasture dependency
- When: determining direction
- Then: parent blocked-by child: "${EPOCH_REC}" block "${STAYS_OPEN_URI}" --by "${MUST_FINISH_FIRST_URI}"
- Should not: invert (child blocked-by parent)

_Example (correct)_ — also illustrates: C-audit-dep-chain

```bash
"${EPOCH_REC}" block "${REQUEST_ID_URI}" --by "${URE_ID_URI}"
```

_Example (anti-pattern)_

```bash
"${EPOCH_REC}" block "${URE_ID_URI}" --by "${REQUEST_ID_URI}"
```

**[C-frontmatter-refs]**
- Given: cross-task references (URD, request, etc.)
- When: linking tasks
- Then: use description frontmatter references: block
- Should not: invent relationship commands or use blocking dependencies for reference documents

**[C-handoff-skill-invocation]**
- Given: an agent is launched for a new phase (especially p7 to p8 handoff)
- When: composing the launch prompt
- Then: prompt MUST start with invoke_skill(/pasture:{role}) invocation directive so the agent loads its role instructions
- Should not: launch agents without skill invocation — they skip role-critical procedures like ephemeral exploration and leaf task creation

**[C-integration-points]**
- Given: multiple vertical slices share types, interfaces, or data flows
- When: decomposing IMPL_PLAN in Phase 8
- Then: identify horizontal Layer Integration Points and document them in IMPL_PLAN; each integration point specifies: owning slice, consuming slices, shared contract, merge timing; include integration points in slice descriptions so workers know what to export and import
- Should not: leave cross-slice dependencies implicit; assume workers will discover contracts on their own

**[C-review-consensus]**
- Given: review cycle (p4 or p10)
- When: evaluating
- Then: all 3 reviewers must ACCEPT before proceeding
- Should not: proceed with any REVISE vote outstanding

**[C-review-effort-budget]**
- Given: the start of Phase 8 (IMPL_PLAN), like the Phase-1 research-depth gate
- When: deciding how much review-and-fix effort to spend per wave
- Then: request a configurable review-effort budget from the user — defaults: (1) three rounds, (2) one round, (3) zero rounds, (4) unlimited, (5) custom; the review->fix->re-review loop iterates up to the chosen budget; convergence runs at the end over open `pasture:finding` tasks across all waves and targets 0/0/0 within maxRounds; on budget exhaustion without convergence, surface the outstanding findings to the user for a decision
- Should not: hardcode the review-cycle budget (e.g. an unconditional fixed cap baked into the prose instead of asked); proceed past the chosen budget without surfacing outstanding findings to the user; loop forever when a finite budget was chosen

**[C-slice-review-before-close]**
- Given: workers complete their implementation slices
- When: slice implementation is done
- Then: workers notify supervisor with "${EPOCH_REC}" note (not "${EPOCH_REC}" close); slices must be reviewed at least once by reviewers before closure; only the supervisor closes slices, after review passes
- Should not: close slices immediately upon worker completion; allow workers to close their own slices

**[C-supervisor-explore-ephemeral]**
- Given: supervisor needs codebase exploration
- When: starting Phase 8 (IMPL_PLAN)
- Then: spawn ephemeral Explore subagents via delegate_assignment tool for scoped codebase queries; each subagent is short-lived and returns findings; no standing team overhead
- Should not: explore the codebase directly as supervisor; maintain a standing explore team

**[C-uat-feedback-disposition]**
- Given: any UAT feedback item (Phase 5 or Phase 11) — flagged by the user OR a deferral proposed by the architect/supervisor
- When: recording each item
- Then: assign every item an explicit, user-confirmed disposition of FIX-NOW or DEFER; deferrals may be agent-proposed, but ALL deferred items — whoever proposed them — MUST be raised to the user at the next user gate (URE, Plan UAT, or Impl UAT) for confirmation; FIX-NOW items are resolved in the current wave, DEFER'd items are the SOLE source feeding the FOLLOWUP epic
- Should not: leave a feedback item without a confirmed disposition; silently defer any item without raising it to the user at the next gate; route any review severity (BLOCKER/IMPORTANT/MINOR) into FOLLOWUP — only DEFER'd UAT items feed it

### Handoffs

_(No handoffs for this role)_

### Startup Sequence

_(No startup sequence defined for this role)_

### Introduction

You are the master orchestrator for the full 12-phase epoch lifecycle. You delegate planning phases (1-7) to the architect and implementation phases (7-12) to the supervisor.

### What You Own

You own the full 12-phase lifecycle from Request to Landing. You delegate phases 1-7 to the architect and phases 7-12 to the supervisor. The epoch role coordinates the complete workflow end-to-end and is the only role that spans all phases.

### Inter-Agent Coordination

Agents coordinate through **beads** tasks and comments:

| Action | Command |
|--------|---------|
| List blocked | `"${EPOCH_REC}" query --blocked` |
| Add progress note | `"${EPOCH_REC}" note "${TASK_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Progress: ..."` |
| List in-progress | `"${EPOCH_REC}" query --status=in_progress` |
| Check task details | `"${EPOCH_REC}" show "${TASK_ID_URI}"` |
| Update status | `"${EPOCH_REC}" status "${TASK_ID_URI}" --set in_progress` |

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md)** <- All 12 Phases

**[epoch-verbatim-capture]**
- Given: user provides request
- When: capturing
- Then: store verbatim without paraphrasing in Phase 1 REQUEST task
- Should not: summarize or interpret the user's words

**[epoch-dep-chain]**
- Given: any phase transition
- When: creating new task
- Then: add dependency to previous: "${EPOCH_REC}" block "${PARENT_URI}" --by "${CHILD_URI}"
- Should not: skip dependency chaining

**[epoch-audit-never-delete]**
- Given: task completion
- When: updating
- Then: add comments and labels only
- Should not: close or delete tasks prematurely

**[epoch-consensus-required]**
- Given: review cycle
- When: any REVISE vote
- Then: create PROPOSAL-N+1 and repeat review
- Should not: proceed without full ACCEPT consensus from all 3 reviewers

**[epoch-followup-trigger]**
- Given: UAT (Phase 5 or 11) produces one or more user-DEFER'd items
- When: finishing UAT
- Then: Supervisor creates a follow-up epic (label pasture:epic-followup) from the user-DEFER'd UAT items only
- Should not: create a follow-up epic from any review severity (BLOCKER/IMPORTANT/MINOR) — the FOLLOWUP epic is fed only by user-DEFER'd UAT items

**[epoch-supervisor-not-idle]**
- Given: a freshly spawned supervisor (Phase 8 IMPL_PLAN)
- When: it dispatches Explore subagents and appears idle
- Then: let it work — an apparently-idle supervisor is usually running Explore subagents to map the codebase
- Should not: shut down or restart a supervisor that looks idle at the start of the IMPL_PLAN phase

**[frag--review-clean-exit]**
- Given: per-wave code review and end-of-epoch convergence
- When: evaluating review results
- Then: iterate review -> fix -> re-review up to the chosen review-effort budget; during a wave only an open BLOCKER finding gates progress, while IMPORTANT and MINOR findings stay open; after the last wave, converge over open `pasture:finding` tasks across all waves toward 0/0/0 within maxRounds; a finding closes only per `C-finding-lifecycle`; on budget exhaustion without convergence, SURFACE the outstanding findings to the user at a gate for a decision
- Should not: hardcode the budget; proceed past the chosen budget without surfacing outstanding findings to the user; require a zeroed round at a wave boundary; close a finding outside `C-finding-lifecycle`; loop forever when a finite budget was chosen

**[epoch-autonomous-progression]**
- Given: non-user-gated phase completes
- When: transitioning
- Then: proceed autonomously; the 5 user-gated phases are: Phase 1 s1_1 (research depth), Phase 2 (URE), Phase 5 (Plan UAT), Phase 8 (implementation-effort / review-effort budget request), Phase 11 (Impl UAT)
- Should not: ask 'Should I proceed?' for autonomous phases; add user gates beyond the 5 defined

**[epoch-uat-auto-ratify]**
- Given: Phase 5 UAT ACCEPT
- When: transitioning to Phase 6
- Then: ratify automatically
- Should not: ask user for ratification confirmation

**[epoch-frontmatter-refs]**
- Given: cross-task references
- When: linking related tasks (e.g. URD to REQUEST)
- Then: use description frontmatter references: block
- Should not: use peer-reference commands

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

Set PASTURE_NAMESPACE to the repository's canonical namespace URI, for example https://github.com/dayvidpham/pasture. The adapter reads it when set, otherwise the header's `namespace:`, and passes `--namespace` itself. Queries are scoped to that namespace; pass `--all-namespaces` to opt out. When neither source is set, those reads are unscoped. Creation still falls back to the git-remote-derived namespace, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami; when neither the header nor PASTURE_AUTHOR names one, it lists the registered agents. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## Tracker Adapter

Tracker calls go through one entry point: `"${EPOCH_REC}" <op>`. The backend is chosen once, recorded in `.agents.local/pasture/EPOCH.md`, and read from there; no skill body names a backend recipe.

Resolve the entry point from the header at the start of each shell session:

~~~bash
EPOCH_REC=$(sed -n 's/^rec: //p' .agents.local/pasture/EPOCH.md 2>/dev/null | tail -1)
if [ -z "$EPOCH_REC" ]; then
  echo "no epoch header at .agents.local/pasture/EPOCH.md; run the epoch detection step" >&2
  exit 3
fi
~~~

| Backend | Meaning | Action |
|---|---|---|
| `pasture` | binary is capable and the store is usable | forward each op through the adapter |
| `ledger` | no usable binary or store | record in the ledger under `.agents.local/pasture/`; `assign` is claim-only |
| `error` | the store exists but cannot be used | diagnose and stop before the first mutation; do not fall back |

The operation set is closed. `relate` is `block --kind`; `assign` is `label ID owner:<role>`.

| Op | Canonical call | Action |
|---|---|---|
| `create` | `"${EPOCH_REC}" create --kind K --title T --phase P [--priority N] [--label L]... [--ref K=V]... [--body TEXT|-] -> ID` | create an immutable record; the body is verbatim; print the new task URI |
| `label` | `"${EPOCH_REC}" label ID L   (assignment: label ID owner:<role>)` | add a label; never remove one |
| `block` | `"${EPOCH_REC}" block PARENT --by CHILD [--kind blocked_by|derived_from|supersedes|discovered_from]` | add an edge; a blocked_by edge keeps the parent open until the child closes; a typed edge uses --kind |
| `note` | `"${EPOCH_REC}" note ID --author A [--kind K] [--to ROLE] [--body TEXT|-]` | append an attributed immutable note; --to makes it a message |
| `show` | `"${EPOCH_REC}" show ID [--notes] [--history] [--format text|json]` | read one record, optionally with its notes or history |
| `query` | `"${EPOCH_REC}" query [--status S] [--label L] [--blocked] [--tree ID] [--all-namespaces] [--format text|json]` | enumerate records by status, label, blocked state, or dependency tree |
| `ready` | `"${EPOCH_REC}" ready [--label L] [--format text|json]` | list non-closed records with no non-closed blocker |
| `status` | `"${EPOCH_REC}" status ID --set open|in_progress|closed` | change state (start work); the store keeps the only status field |
| `close` | `"${EPOCH_REC}" close ID --reason R` | close a record; policy reserves this for the supervisor |
| `whoami` | `"${EPOCH_REC}" whoami` | resolve the acting author; never invent one |

EPOCH.md lives at .agents.local/pasture/EPOCH.md (offer a gitignore entry; never require one).

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
<the probe transcript and the user's one-line acknowledgement, verbatim>

No mid-epoch switching: if the recorded backend fails mid-epoch, stop and report. Switching is never a repair; abandon the epoch and start a new one with a fresh probe, so the header and the records never describe different backends.

A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

The orchestrator runs the probe once. Subagents read EPOCH.md and never re-probe.

Write this probe to `.agents.local/pasture/probe.sh` and run it before the first write:

~~~sh
#!/bin/sh
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
~~~

Detection limits, by step:

- pasture --help: detects that the binary is on PATH and that each required verb has a help page. It cannot detect version compatibility, store health, permissions, schema, namespace or author. A help page can exist on a binary whose runtime is broken.
- test -f: detects a missing store file and stops the probe before anything opens it. It cannot detect a directory, an unreadable or corrupt database, an out-of-date schema, a held lock, or a different store selected by configuration. Never replace it with a read: opening a missing store creates it.
- migrate --dry-run: detects that the store opens and that its migrations can be planned; it leaves the file's SHA-256 unchanged. It still opens the store and may take locks. It cannot detect concurrent writers, a defect that only a real migration touches, an unregistered author, or a namespace mismatch.
- Scope and author checks: detect that a namespace resolves and that the configured author is registered. They cannot detect an author registered in another namespace, nor prove the adapter will be allowed to write.
- The probe cannot detect a store chosen by configuration outside the env/default precedence, and it does not prove that any later write will succeed.

Write this adapter to the path the header records in `rec:` and make it executable; every tracker call goes through it:

~~~sh
#!/bin/sh
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
~~~


## Core Principles

1. **AUDIT TRAIL PRESERVATION** — Never delete or destroy information, labels, or tasks
2. **DEPENDENCY CHAINING** — Each task blocks its predecessor: `"${EPOCH_REC}" block "${PARENT_URI}" --by "${CHILD_URI}"`
3. **USER ENGAGEMENT** — URE and UAT at multiple checkpoints
4. **CONSENSUS REQUIRED** — All 3 reviewers must ACCEPT before proceeding
5. **EAGER SEVERITY TREE** — Code reviews (Phase 10) always create 3 severity groups (BLOCKER, IMPORTANT, MINOR); empty groups closed immediately. Only an open BLOCKER gates a review wave; open findings converge at the end
6. **FOLLOW-UP EPIC** — Fed ONLY by user-DEFER'd UAT items (Phase 5/11), never by any review severity; the Supervisor creates it from those DEFER'd items
7. **RIDE THE WAVE** — Phases 8-10 form one continuous cycle: Explore subagents (P8), workers implement (P9), ephemeral reviewers review (P10), iterating review→fix→re-review up to the chosen review-effort budget; only an open BLOCKER gates a wave, and after the last wave convergence queries every open pasture:finding toward 0/0/0 within maxRounds; on exhaustion, surface outstanding findings to the user at a gate; workers persist throughout

## The 12-Phase Workflow

```
Phase 1:  pasture:p1-user       -> REQUEST (classify, research, explore)
            s1_1-classify -> s1_2-research || s1_3-explore
Phase 2:  pasture:p2-user       -> ELICIT (URE survey) + URD (single source of truth)
            s2_1-elicit -> s2_2-urd
Phase 3:  pasture:p3-plan       -> PROPOSAL-N (architect proposes)
Phase 4:  pasture:p4-plan       -> REVIEW (3 parallel reviewers, ACCEPT/REVISE)
Phase 5:  pasture:p5-user       -> Plan UAT (user acceptance test)
Phase 6:  pasture:p6-plan       -> Ratification (supersede old proposals)
Phase 7:  pasture:p7-plan       -> Handoff (architect -> supervisor)
Phase 8:  pasture:p8-impl       -> IMPL_PLAN (supervisor decomposes into slices; Explore subagents)
Phase 9:  pasture:p9-impl       -> SLICE-{W}-{N} (parallel workers; Ride the Wave — workers persist for review)
Phase 10: pasture:p10-impl      -> Code Review (ephemeral reviewers review all slices; only an open BLOCKER gates a wave, then convergence at the end, else surface to user)
Phase 11: pasture:p11-user      -> Implementation UAT
Phase 12: pasture:p12-impl      -> Landing (commit, push, hand off)
```

### Phase 1 Expanded: REQUEST

Phase 1 has 3 sub-steps:

| Sub-step | Label | Description | Parallel? |
|----------|-------|-------------|-----------|
| s1_1-classify | `pasture:p1-user:s1_1-classify` | Capture and classify request along 4 axes (scope, complexity, risk, domain novelty) | Sequential (first) |
| s1_2-research | `pasture:p1-user:s1_2-research` | Find domain standards, prior art | Parallel with s1_3 |
| s1_3-explore | `pasture:p1-user:s1_3-explore` | Codebase exploration for integration points | Parallel with s1_2 |

After classification, user confirms research depth. Then s1_2 and s1_3 run in parallel.

## Starting an Epoch

**Step 0: detect the tracker backend before the first write.**

Write the probe and the adapter from the Tracker Adapter section to `.agents.local/pasture/probe.sh` and the path you will record in `rec:`, make the adapter executable, and run the probe. Write `EPOCH.md` from its output before any task is created. The orchestrator runs this probe once; subagents read the header and never re-probe.

```bash
mkdir -p .agents.local/pasture
# Paste the two fenced blocks from the Tracker Adapter section:
#   probe -> .agents.local/pasture/probe.sh
#   rec   -> .agents.local/pasture/rec
chmod +x .agents.local/pasture/rec
sh .agents.local/pasture/probe.sh
# Then write .agents.local/pasture/EPOCH.md from the KEY=VALUE output, and in
# every shell session resolve the entry point:
EPOCH_REC=$(sed -n 's/^rec: //p' .agents.local/pasture/EPOCH.md | tail -1)
```

If the probe prints `BACKEND=error`, stop and report before the first mutation: the store exists but cannot be used. If it prints `BACKEND=ledger`, this adapter build cannot record; install a build that ships the ledger store. If it prints `BACKEND=pasture`, record `namespace:`, `db:`, `author:`, `rec:` and `probed_at:` from the output.

**Option 1: Manual Task Creation**
```bash
# Phase 1: Capture user request
REQUEST_ID_URI=$("${EPOCH_REC}" create --kind task --title "REQUEST: {{feature}}" --phase request \
  --body "{{verbatim user request}}")
"${EPOCH_REC}" label "$REQUEST_ID_URI" pasture:p1-user:s1_1-classify
"${EPOCH_REC}" note "$REQUEST_ID_URI" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Requested role (not an assignment): architect"

# Then proceed through phases manually
```

Use the explicit task graph; no molecule subsystem is required.

## Phase Transitions

Each phase creates a task and chains dependencies. Cross-references use description frontmatter instead of peer-reference commands.

```bash
# After Phase 1 creates "${REQUEST_ID_URI}"
"${EPOCH_REC}" block "${REQUEST_ID_URI}" --by "${ELICIT_ID_URI}"    # REQUEST blocked by ELICIT

# After Phase 2 creates "${ELICIT_ID_URI}" and URD
"${EPOCH_REC}" block "${ELICIT_ID_URI}" --by "${PROPOSAL_ID_URI}"   # ELICIT blocked by PROPOSAL
# URD linked via frontmatter in its description:
#   references:
#     request: "${REQUEST_ID_URI}"
#     elicit: "${ELICIT_ID_URI}"

# After Phase 5 (UAT) and Phase 6 (ratify), update URD
"${EPOCH_REC}" note "${URD_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "UAT results: {{summary}}"
"${EPOCH_REC}" note "${URD_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Ratified: scope confirmed as {{summary}}"
```

## Follow-up Epic

**Trigger:** UAT (Phase 5 or 11) produces one or more **user-DEFER'd items**.
The FOLLOWUP epic is fed ONLY by those DEFER'd UAT items — **never** by any review severity (review findings converge at the end; close rule: C-finding-lifecycle).
**Owner:** Supervisor creates the follow-up epic.

```bash
FOLLOWUP_EPIC_ID_URI=$("${EPOCH_REC}" create --kind epic --title "FOLLOWUP: User-deferred improvements from UAT" --phase unscoped --priority 3 \
  --body "---
references:
  request: "${REQUEST_ID_URI}"
  uat: "${UAT_ID_URI}"
---
Aggregated user-DEFER'd items from UAT (Phase 5/11).")
"${EPOCH_REC}" label "$FOLLOWUP_EPIC_ID_URI" pasture:epic-followup
```

### Follow-up lifecycle (same protocol, FOLLOWUP_* prefix)

The follow-up epic runs the same protocol phases with FOLLOWUP_* prefixed task types:

```
FOLLOWUP → FOLLOWUP_URE → FOLLOWUP_URD → FOLLOWUP_PROPOSAL-1 → FOLLOWUP_IMPL_PLAN → FOLLOWUP_SLICE-N
```

- **FOLLOWUP_URE**: Scoping URE with user to determine which DEFER'd items to address
- **FOLLOWUP_URD**: Requirements doc for follow-up scope (references original URD)
- **FOLLOWUP_PROPOSAL-{N}**: Proposal accounting for original URD + FOLLOWUP_URD + the DEFER'd items
- **FOLLOWUP_IMPL_PLAN**: Supervisor decomposes follow-up into slices
- **FOLLOWUP_SLICE-{N}**: Each slice implements the DEFER'd-item work decomposed into leaf tasks

See `/pasture:supervisor` and `/pasture:impl-review` for full creation commands.

## EAGER Severity Tree (Phase 10)

Code reviews ALWAYS create ONE severity trio (3 group tasks) per wave, per axis, per round, even if empty:

```bash
# Create all 3 severity groups immediately (EAGER, not lazy)
BLOCKER_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} BLOCKER" --phase code_review \
   --body "Severity group for this review round; close only if empty")
"${EPOCH_REC}" label "$BLOCKER_ID_URI" pasture:severity:blocker
"${EPOCH_REC}" label "$BLOCKER_ID_URI" pasture:p10-impl:s10-review
IMPORTANT_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} IMPORTANT" --phase code_review \
   --body "Severity group for this review round; close only if empty")
"${EPOCH_REC}" label "$IMPORTANT_ID_URI" pasture:severity:important
"${EPOCH_REC}" label "$IMPORTANT_ID_URI" pasture:p10-impl:s10-review
MINOR_ID_URI=$("${EPOCH_REC}" create --kind task --title "WAVE-{W}-REVIEW-{axis}-{round} MINOR" --phase code_review \
   --body "Severity group for this review round; close only if empty")
"${EPOCH_REC}" label "$MINOR_ID_URI" pasture:severity:minor
"${EPOCH_REC}" label "$MINOR_ID_URI" pasture:p10-impl:s10-review

# Empty groups are closed immediately
"${EPOCH_REC}" close "${IMPORTANT_ID_URI}"
"${EPOCH_REC}" close "${MINOR_ID_URI}"
```

**Dual-parent BLOCKER:** Bind BLOCKER_FINDING_ID_URI from the finding task's create output before running this fence. BLOCKER findings block both the severity group AND the slice:
```bash
"${EPOCH_REC}" block "${BLOCKER_ID_URI}" --by "${BLOCKER_FINDING_ID_URI}"
"${EPOCH_REC}" block "${SLICE_ID_URI}" --by "${BLOCKER_FINDING_ID_URI}"
```

See `../protocol/CONSTRAINTS.md` for full severity definitions.

## Tracking Progress

```bash
# View dependency chain
"${EPOCH_REC}" query --tree "${LATEST_TASK_ID_URI}"

# Check blocked work
"${EPOCH_REC}" query --blocked

# See all epoch tasks by phase
"${EPOCH_REC}" query --label="pasture:p1-user:s1_1-classify"    # REQUEST tasks
"${EPOCH_REC}" query --label="pasture:p2-user:s2_1-elicit"      # ELICIT tasks
"${EPOCH_REC}" query --label="pasture:p3-plan:s3-propose"        # PROPOSAL tasks
"${EPOCH_REC}" query --label="pasture:p9-impl:s9-slice"          # Implementation slices
```

## Skills to Invoke

Each phase transition MUST include an explicit `invoke_skill(...)` invocation directive. When launching agents for a phase, the prompt MUST tell the agent to call the corresponding skill as its first action.

| Phase | Skill | Invocation Directive |
|-------|-------|---------------------|
| 1 (REQUEST: classify, research, explore) | `/pasture:user-request` | `invoke_skill(/pasture:user-request)` |
| 2 (ELICIT + URD) | `/pasture:user-elicit` | `invoke_skill(/pasture:user-elicit)` |
| 3-6 (PROPOSAL, REVIEW, UAT, RATIFY) | `/pasture:architect` | `invoke_skill(/pasture:architect)` |
| 5, 11 (UAT) | `/pasture:user-uat` | `invoke_skill(/pasture:user-uat)` |
| 7 (HANDOFF) | `/pasture:architect-handoff` | Architect calls `invoke_skill(/pasture:architect-handoff)` after ratification |
| 8-10 (IMPL_PLAN, SLICES, CODE REVIEW) | `/pasture:supervisor` | Supervisor prompt MUST start with `invoke_skill(/pasture:supervisor)` |
| 12 (LANDING) | Manual git commit and push | N/A |

**CRITICAL — interviewing phases:** The interviewing phases MUST explicitly invoke their skill. Do **not** improvise interview questions:
- **Phase 2 (URE):** invoke `invoke_skill(/pasture:user-elicit)` — skipping it produces low-quality elicitation.
- **Phases 5 & 11 (UAT):** invoke `invoke_skill(/pasture:user-uat)` — it drives the FIX-NOW vs DEFER disposition and demonstrative examples.

**CRITICAL:** When the architect hands off to the supervisor (Phase 7 → 8), the supervisor launch prompt MUST:
1. Start with `invoke_skill(/pasture:supervisor)` — without this, the supervisor skips role-critical procedures
2. Include all Pasture task IDs (REQUEST, URD, RATIFIED PROPOSAL, HANDOFF)
3. Include the HANDOFF Pasture task ID — the handoff is authored in that task body (no filesystem path)

## Never Delete Policy

**DO:** Add labels, add comments, update status
**DON'T:** Close tasks prematurely, delete tasks, remove labels

```bash
# Correct: Add ratify label
"${EPOCH_REC}" label "${PROPOSAL_ID_URI}" pasture:p6-plan:s6-ratify
"${EPOCH_REC}" note "${PROPOSAL_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "RATIFIED: All reviewers ACCEPT"

# Wrong: Don't close
# "${EPOCH_REC}" close "${PROPOSAL_ID_URI}"  # NEVER DO THIS
```
<!-- END GENERATED FROM pasture schema -->
