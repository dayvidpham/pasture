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

Agents coordinate through tracker tasks and comments:

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

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. With no origin, both backends use file:// of the working directory, so invocations from different subdirectories use different namespaces. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

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
| `create` | `"${EPOCH_REC}" create --kind K --title T --phase P [--priority N] [--label L]... [--ref K=V]... [--body TEXT|@-] -> ID` | create an immutable record; the body is verbatim; print the new task URI |
| `label` | `"${EPOCH_REC}" label ID L   (assignment: label ID owner:<role>)` | add a label; never remove one |
| `block` | `"${EPOCH_REC}" block PARENT --by CHILD [--kind blocked_by|derived_from|supersedes|discovered_from]` | add an edge; a blocked_by edge keeps the parent open until the child closes; a typed edge uses --kind |
| `note` | `"${EPOCH_REC}" note ID --author A [--kind K] [--to ROLE] [--body TEXT|@-]` | append an attributed immutable note; --to makes it a message |
| `show` | `"${EPOCH_REC}" show ID [--notes] [--history] [--format text|json]` | read one record, including description, phase, priority and refs; --notes lists notes. Pasture show returns createdAt and updatedAt; ledger timestamps stay on --history |
| `query` | `"${EPOCH_REC}" query [--status S] [--label L] [--blocked] [--tree ID] [--all-namespaces] [--format text|json]` | enumerate records by status, label, blocked state, or dependency tree; --blocked rejects --status |
| `ready` | `"${EPOCH_REC}" ready [--label L] [--all-namespaces] [--format text|json]` | list non-closed records with no non-closed blocker |
| `status` | `"${EPOCH_REC}" status ID --set open|in_progress|closed` | change state (start work); the store keeps the only status field |
| `close` | `"${EPOCH_REC}" close ID --reason R` | close a record; policy reserves this for the supervisor |
| `whoami` | `"${EPOCH_REC}" whoami` | resolve the acting author; never invent one |
| `signal` | `"${EPOCH_REC}" signal --kind K --phase P --skill S --entry E --quote Q` | record one user signal; pasture notes the feedback task, ledger appends one JSON object |

EPOCH.md lives at .agents.local/pasture/EPOCH.md (offer a gitignore entry; never require one).

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
<the probe transcript and the user's one-line acknowledgement, verbatim>

No mid-epoch switching: if the recorded backend fails mid-epoch, stop and report. Switching is never a repair; abandon the epoch and start a new one with a fresh probe, so the header and the records never describe different backends.

A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

The orchestrator runs the probe once. Subagents read EPOCH.md and never re-probe.

Write this probe to `.agents.local/pasture/probe.sh` and run it before the first write:

<!-- pasture:asset probe -->
~~~sh
#!/bin/sh
# derive_namespace is the one ledger normalisation. Forms: git@host:path,
# ssh://, git://, http://, https:// and user@host:path. An empty remote inside
# a git work tree is file:// of the physical working directory, matching
# Pasture. PASTURE_NAMESPACE matches the value Pasture uses when
# --namespace is omitted.
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
  tab=$(printf '\t')
  while :; do
    case $url in
      ' '*) url=${url# } ;;
      "$tab"*) url=${url#"$tab"} ;;
      *) break ;;
    esac
  done
  while :; do
    case $url in
      *' ') url=${url% } ;;
      *"$tab") url=${url%"$tab"} ;;
      *) break ;;
    esac
  done
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

# Epoch tracker probe. Side-effect free. Prints KEY=VALUE lines and exits 0.
# Order: capability (--help), store existence (test -f), usability (migrate --dry-run).
# Verb list is generated from TrackerOps.
set -u

PASTURE_BIN="${PASTURE_BIN:-pasture}"
LEDGER_DIR="${PWD}/.agents.local/pasture/ledger"
REC_PATH="${PWD}/.agents.local/pasture/rec"

emit() {
  printf 'BACKEND=%s\n' "$1"
  printf 'REASON=%s\n' "${2:-}"
  [ -n "${3:-}" ] && printf 'DB=%s\n' "$3"
  [ -n "${4:-}" ] && printf 'NS=%s\n' "$4"
  [ -n "${5:-}" ] && printf 'ACT=%s\n' "$5"
  [ -n "${6:-}" ] && printf 'VER=%s\n' "$6"
  printf 'LEDGER=%s\n' "$LEDGER_DIR"
  printf 'REC=%s\n' "$REC_PATH"
  printf 'PROBED_AT=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
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

for VERB in "task create" "task label" "task dep" \
            "task comment" "task comments" "task timeline" \
            "task show" "task blocked" "task list" \
            "task ready" "task update" "task close" \
            "task agents"; do
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
# Pasture: the probe does not derive a namespace. Writing one into
# EPOCH.md would freeze it and override Pasture's own derivation.
# Ledger: emit_ledger derives and prints NS because there is no Pasture store
# to ask.

ACT="${PASTURE_AUTHOR:-pasture-system/00000000-0000-0000-0000-000000000000}"
"$PASTURE_BIN" --db "$DB" task agents show "$ACT" --format json >/dev/null 2>&1
RC=$?
if [ "$RC" -ne 0 ]; then
  printf 'WARN=no-author\n'
  printf 'probe: warning: author %s is not registered in %s\n  why: task agents show exited %s, and a missing author is not an unusable store\n  where: epoch tracker probe, author check\n  fix: register the actor, or export PASTURE_AUTHOR to a registered id; the epoch still records backend pasture\n' "$ACT" "$DB" "$RC" >&2
fi

emit pasture "" "$DB" "" "$ACT" "$VER"
~~~
<!-- /pasture:asset probe -->

Detection limits, by step:

- pasture --help: detects that the binary is on PATH and that each required verb has a help page. It cannot detect version compatibility, store health, permissions, schema, namespace or author. A help page can exist on a binary whose runtime is broken.
- test -f: detects a missing store file and stops the probe before anything opens it. It cannot detect a directory, an unreadable or corrupt database, an out-of-date schema, a held lock, or a different store selected by configuration. Never replace it with a read: opening a missing store creates it.
- migrate --dry-run: detects that the store opens and that its migrations can be planned; it leaves the file's SHA-256 unchanged. It still opens the store and may take locks. It cannot detect concurrent writers, a defect that only a real migration touches, an unregistered author, or a namespace mismatch. A non-zero result is the only error backend: the store exists but cannot be used.
- Namespace: for pasture, the probe does not derive a namespace and does not write one into the header. Writing one would freeze Pasture's own derivation (PASTURE_NAMESPACE, else the git origin, else file:// of the working directory). For ledger, there is no Pasture store to ask, so the probe derives and prints NS from derive_namespace, and every unpinned ledger write uses that value. A missing remote is not an unusable store. It cannot detect a remote other than origin.
- Author: a missing or unregistered author is a warning (WARN=no-author), not an error backend. It cannot detect an author registered in another namespace, nor prove a later write will be allowed.
- The probe cannot detect a store chosen by configuration outside the env/default precedence, and it does not prove that any later write will succeed.

Write this adapter to the path the header records in `rec:` and make it executable; every tracker call goes through it:

<!-- pasture:asset rec -->
~~~sh
#!/bin/sh
# derive_namespace is the one ledger normalisation. Forms: git@host:path,
# ssh://, git://, http://, https:// and user@host:path. An empty remote inside
# a git work tree is file:// of the physical working directory, matching
# Pasture. PASTURE_NAMESPACE matches the value Pasture uses when
# --namespace is omitted.
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
  tab=$(printf '\t')
  while :; do
    case $url in
      ' '*) url=${url# } ;;
      "$tab"*) url=${url#"$tab"} ;;
      *) break ;;
    esac
  done
  while :; do
    case $url in
      *' ') url=${url% } ;;
      *"$tab") url=${url%"$tab"} ;;
      *) break ;;
    esac
  done
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
# bare command substitution. C0 controls other than tab, CR and LF are
# \u00XX, because a raw control byte makes show --format json invalid JSON.
json_escape() {
  ROW_IN=$1 awk 'BEGIN {
    s = ENVIRON["ROW_IN"]
    out = ""
    for (i = 0; i < 32; i++) ctrl[sprintf("%c", i)] = i
    for (i = 1; i <= length(s); i++) {
      c = substr(s, i, 1)
      if (c == "\\") out = out "\\\\"
      else if (c == "\"") out = out "\\\""
      else if (c == "\t") out = out "\\t"
      else if (c == "\r") out = out "\\r"
      else if (c == "\n") out = out "\\n"
      else if (c in ctrl) out = out sprintf("\\u00%02x", ctrl[c])
      else out = out c
    }
    printf "%s", out
  }'
}

# text_field keeps one show --format text record to one line per key. A
# description that contains a newline would otherwise span lines, and the
# next line would look like a new key.
text_field() {
  ROW_IN=$1 awk 'BEGIN {
    s = ENVIRON["ROW_IN"]
    out = ""
    for (i = 1; i <= length(s); i++) {
      c = substr(s, i, 1)
      if (c == "\\") out = out "\\\\"
      else if (c == "\n") out = out "\\n"
      else if (c == "\r") out = out "\\r"
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

ledger_line() {
  _f_op=
  _f_id=
  _f_namespace=
  _f_title=
  _f_kind=
  _f_phase=
  _f_status=
  _f_priority=
  _f_label=
  _f_parent=
  _f_child=
  _f_edge_kind=
  _f_author=
  _f_note_kind=
  _f_to=
  _f_reason=
  _f_ref=
  _f_body=
  _f_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  while [ "$#" -gt 1 ]; do
    case "$1" in
      op) _f_op="$2"; shift 2 ;;
      id) _f_id="$2"; shift 2 ;;
      namespace) _f_namespace="$2"; shift 2 ;;
      title) _f_title="$2"; shift 2 ;;
      kind) _f_kind="$2"; shift 2 ;;
      phase) _f_phase="$2"; shift 2 ;;
      status) _f_status="$2"; shift 2 ;;
      priority) _f_priority="$2"; shift 2 ;;
      label) _f_label="$2"; shift 2 ;;
      parent) _f_parent="$2"; shift 2 ;;
      child) _f_child="$2"; shift 2 ;;
      edge_kind) _f_edge_kind="$2"; shift 2 ;;
      author) _f_author="$2"; shift 2 ;;
      note_kind) _f_note_kind="$2"; shift 2 ;;
      to) _f_to="$2"; shift 2 ;;
      reason) _f_reason="$2"; shift 2 ;;
      ref) _f_ref="$2"; shift 2 ;;
      body) _f_body="$2"; shift 2 ;;
      *) fail "ledger_line rejected field '$1'" "the ledger field set is closed" "use a field from the stable ledger order" ;;
    esac
  done
  printf '{'
  printf '"op":"%s"' "$(json_escape "$_f_op")"
  printf ',"id":"%s"' "$(json_escape "$_f_id")"
  printf ',"at":"%s"' "$(json_escape "$_f_at")"
  printf ',"namespace":"%s"' "$(json_escape "$_f_namespace")"
  printf ',"title":"%s"' "$(json_escape "$_f_title")"
  printf ',"kind":"%s"' "$(json_escape "$_f_kind")"
  printf ',"phase":"%s"' "$(json_escape "$_f_phase")"
  printf ',"status":"%s"' "$(json_escape "$_f_status")"
  printf ',"priority":"%s"' "$(json_escape "$_f_priority")"
  printf ',"label":"%s"' "$(json_escape "$_f_label")"
  printf ',"parent":"%s"' "$(json_escape "$_f_parent")"
  printf ',"child":"%s"' "$(json_escape "$_f_child")"
  printf ',"edge_kind":"%s"' "$(json_escape "$_f_edge_kind")"
  printf ',"author":"%s"' "$(json_escape "$_f_author")"
  printf ',"note_kind":"%s"' "$(json_escape "$_f_note_kind")"
  printf ',"to":"%s"' "$(json_escape "$_f_to")"
  printf ',"reason":"%s"' "$(json_escape "$_f_reason")"
  printf ',"ref":"%s"' "$(json_escape "$_f_ref")"
  printf ',"body":"%s"' "$(json_escape "$_f_body")"
  printf '}'
}

signal_json() {
  printf '{"kind":"%s","phase":"%s","skill":"%s","entry":"%s","quote":"%s"}' \
    "$(json_escape "$kind")" \
    "$(json_escape "$phase")" \
    "$(json_escape "$skill")" \
    "$(json_escape "$entry")" \
    "$(json_escape "$quote")"
}


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
function take(line, pos,   i, c, out, esc, hex, v, ok, h, d) {
  out = ""
  esc = 0
  for (i = pos + 1; i <= length(line); i++) {
    c = substr(line, i, 1)
    if (esc) {
      if (c == "n") out = out "\n"
      else if (c == "t") out = out "\t"
      else if (c == "r") out = out "\r"
      else if (c == "u" && i + 4 <= length(line)) {
        hex = substr(line, i + 1, 4)
        v = 0
        ok = 1
        for (h = 1; h <= 4; h++) {
          d = index("0123456789abcdef", tolower(substr(hex, h, 1)))
          if (d == 0) ok = 0
          else v = v * 16 + d - 1
        }
        if (ok) { out = out sprintf("%c", v); i += 4 }
        else out = out c
      }
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
function take(line, pos,   i, c, out, esc, hex, v, ok, h, d) {
  out = ""; esc = 0
  for (i = pos + 1; i <= length(line); i++) {
    c = substr(line, i, 1)
    if (esc) {
      if (c == "n") out = out "\n"
      else if (c == "t") out = out "\t"
      else if (c == "r") out = out "\r"
      else if (c == "u" && i + 4 <= length(line)) {
        hex = substr(line, i + 1, 4)
        v = 0
        ok = 1
        for (h = 1; h <= 4; h++) {
          d = index("0123456789abcdef", tolower(substr(hex, h, 1)))
          if (d == 0) ok = 0
          else v = v * 16 + d - 1
        }
        if (ok) { out = out sprintf("%c", v); i += 4 }
        else out = out c
      }
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
        "$(text_field "$rid")" "$(text_field "$rtitle")" "$rstatus" "$(text_field "$rns")" "$(text_field "$rlabels")" "$(text_field "$redges")" "$rcomments" \
        "$(text_field "$rphase")" "$(text_field "$rpriority")" "$(text_field "$rref")" "$(text_field "$rbody")"
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
case "$OP" in
  # dispatch:create
  create)
    kind=task
    title=
    phase=
    priority=
    labels=
    refs=
    body=
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
        --label) label_push "$2"; shift 2 ;;
        --label=*) label_push "${1#*=}"; shift ;;
        --ref)
          if [ -z "$refs" ]; then refs="$2"; else refs="$refs
$2"; fi
          shift 2 ;;
        --ref=*)
          _val="${1#*=}"
          if [ -z "$refs" ]; then refs="$_val"; else refs="$refs
$_val"; fi
          shift ;;
        --body) body="$2"; shift 2 ;;
        --body=*) body="${1#*=}"; shift ;;
        *) fail "create rejected argument '$1'" "the argument is not in the create contract" "use --kind, --title, --phase, --priority, --label, --ref, --body" ;;
      esac
    done
    [ "$body" = "@-" ] && body="$(cat)"
    fold_refs
    if [ "$BACKEND" = ledger ]; then
      ledger_create
      exit $?
    fi
      set -- task create "$title" --type "$kind" --phase "$phase"
      [ -n "$priority" ] && set -- "$@" --priority "$priority"
      [ -n "$body" ] && set -- "$@" --description "$body"
      if [ "$allns" != 1 ] && [ -n "$NS" ]; then set -- "$@" --namespace "$NS"; fi
      out="$(pb "$@" --format text)" || exit $?
      id="$(printf '%s\n' "$out" | sed -n 's/^ID: *//p' | head -1)"
      [ -n "$id" ] || fail "create returned no task ID" "the backend text output had no ID line" "check the store named in $EPOCH_FILE and the backend help page"
      printf '%s\n' "$id"
      set -f
      for _enc in $labels; do
        [ -n "$_enc" ] || continue
        row_decode_set "$_enc"
        label=$decoded
        set -- task label add "$id" "$label"
        pb "$@" >/dev/null || exit $?
      done
      set +f
    ;;
  # dispatch:label
  label)
    id="${1:-}"
    [ "$#" -gt 0 ] && shift
    need_id "$id"
    labels=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --*) fail "label rejected argument '$1'" "the argument is not in the label contract" "pass the label as a positional after the task URI" ;;
        *) label_push "$1"; shift ;;
      esac
    done
    [ -n "$labels" ] || fail "label needs at least one label" "no label operand was given" "pass the label after the task URI"
    if [ "$BACKEND" = ledger ]; then
      ledger_label
      exit $?
    fi
      set -f
      for _enc in $labels; do
        [ -n "$_enc" ] || continue
        row_decode_set "$_enc"
        label=$decoded
        set -- task label add "$id" "$label"
        pb "$@" || exit $?
      done
      set +f
    ;;
  # dispatch:block
  block)
    child=
    edge_kind=blocked_by
    parent="${1:-}"
    [ "$#" -gt 0 ] && shift
    need_id "$parent"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --by) child="$2"; shift 2 ;;
        --by=*) child="${1#*=}"; shift ;;
        --kind) edge_kind="$2"; shift 2 ;;
        --kind=*) edge_kind="${1#*=}"; shift ;;
        *) fail "block rejected argument '$1'" "the argument is not in the block contract" "use --by, --kind" ;;
      esac
    done
    [ -n "$child" ] || fail "block needs --by CHILD" "the child operand was empty" "pass block PARENT --by CHILD"
    if [ "$BACKEND" = ledger ]; then
      ledger_block
      exit $?
    fi
      if [ "$edge_kind" = blocked_by ]; then
        set -- task dep add "$parent" --blocked-by "$child"
        pb "$@" || exit $?
      else
        set -- task dep add "$parent" --target "$child" --kind "$edge_kind"
        pb "$@" || exit $?
      fi
    ;;
  # dispatch:note
  note)
    note_body=
    note_kind=
    note_to=
    fmt=json
    id="${1:-}"
    [ "$#" -gt 0 ] && shift
    need_id "$id"
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
        *) fail "note rejected argument '$1'" "the argument is not in the note contract" "use --author, --body, --kind, --to, --format" ;;
      esac
    done
    [ "$note_body" = "@-" ] && note_body="$(cat)"
    [ -n "$AUTHOR" ] || fail "note needs --author" "the tracker never invents an author" "pass the registered actor id"
    [ -n "$note_kind" ] && note_body="[$note_kind] $note_body"
    [ -n "$note_to" ] && note_body="to:$note_to $note_body"
    if [ "$BACKEND" = ledger ]; then
      ledger_note
      exit $?
    fi
      set -- task comment add "$id" --author "$AUTHOR" -- "$note_body"
      pb "$@" || exit $?
    ;;
  # dispatch:show
  show)
    mode=show
    mode=show
    fmt=json
    id="${1:-}"
    [ "$#" -gt 0 ] && shift
    need_id "$id"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --notes) mode=notes; shift ;;
        --history) mode=history; shift ;;
        --format) fmt="$2"; shift 2 ;;
        --format=*) fmt="${1#*=}"; shift ;;
        *) fail "show rejected argument '$1'" "the argument is not in the show contract" "use --notes, --history, --format" ;;
      esac
    done
    if [ "$BACKEND" = ledger ]; then
      ledger_show
      exit $?
    fi
      if [ "$mode" = notes ]; then
        set -- task comments "$id" --format "$fmt"
        pb "$@" || exit $?
      elif [ "$mode" = history ]; then
        set -- task timeline "$id"
        pb "$@" || exit $?
      else
        set -- task show "$id" --format "$fmt"
        pb "$@" || exit $?
      fi
    ;;
  # dispatch:query
  query)
    status=
    label=
    tree=
    fmt=json
    blocked=0
    allns=0
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
        *) fail "query rejected argument '$1'" "the argument is not in the query contract" "use --status, --label, --tree, --format, --blocked, --all-namespaces" ;;
      esac
    done
    [ "$blocked" != 1 ] || [ -z "$status" ] || fail "query rejected --blocked with --status" "the blocked read has no --status flag, so forwarding it exits 1" "drop --status, or run query --status without --blocked"
    if [ "$BACKEND" = ledger ]; then
      ledger_query
      exit $?
    fi
      if [ -n "$tree" ]; then
        set -- task dep tree "$tree"
        pb "$@" || exit $?
        exit $?
      elif [ "$blocked" = 1 ]; then
        set -- task blocked
        [ -n "$label" ] && set -- "$@" --label "$label"
        if [ "$allns" != 1 ] && [ -n "$NS" ]; then set -- "$@" --namespace "$NS"; fi
        [ "$allns" = 1 ] && set -- "$@" --all-namespaces
        set -- "$@" --format "$fmt"
        pb "$@" || exit $?
      else
        set -- task list
        [ -n "$status" ] && set -- "$@" --status "$status"
        [ -n "$label" ] && set -- "$@" --label "$label"
        if [ "$allns" != 1 ] && [ -n "$NS" ]; then set -- "$@" --namespace "$NS"; fi
        [ "$allns" = 1 ] && set -- "$@" --all-namespaces
        set -- "$@" --format "$fmt"
        pb "$@" || exit $?
      fi
    ;;
  # dispatch:ready
  ready)
    label=
    fmt=json
    allns=0
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --label) label="$2"; shift 2 ;;
        --label=*) label="${1#*=}"; shift ;;
        --format) fmt="$2"; shift 2 ;;
        --format=*) fmt="${1#*=}"; shift ;;
        --all-namespaces) allns=1; shift ;;
        *) fail "ready rejected argument '$1'" "the argument is not in the ready contract" "use --label, --format, --all-namespaces" ;;
      esac
    done
    if [ "$BACKEND" = ledger ]; then
      ledger_ready
      exit $?
    fi
      set -- task ready
      [ -n "$label" ] && set -- "$@" --label "$label"
      if [ "$allns" != 1 ] && [ -n "$NS" ]; then set -- "$@" --namespace "$NS"; fi
      [ "$allns" = 1 ] && set -- "$@" --all-namespaces
      set -- "$@" --format "$fmt"
      pb "$@" || exit $?
    ;;
  # dispatch:status
  status)
    status=
    id="${1:-}"
    [ "$#" -gt 0 ] && shift
    need_id "$id"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --set) status="$2"; shift 2 ;;
        --set=*) status="${1#*=}"; shift ;;
        *) fail "status rejected argument '$1'" "the argument is not in the status contract" "use --set" ;;
      esac
    done
    [ -n "$status" ] || fail "status needs --set" "no target status was given" "pass --set open, in_progress or closed"
    if [ "$BACKEND" = ledger ]; then
      ledger_status
      exit $?
    fi
      set -- task update "$id" --status "$status"
      pb "$@" || exit $?
    ;;
  # dispatch:close
  close)
    reason=
    id="${1:-}"
    [ "$#" -gt 0 ] && shift
    need_id "$id"
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --reason) reason="$2"; shift 2 ;;
        --reason=*) reason="${1#*=}"; shift ;;
        *) fail "close rejected argument '$1'" "the argument is not in the close contract" "use --reason" ;;
      esac
    done
    if [ "$BACKEND" = ledger ]; then
      ledger_close
      exit $?
    fi
      set -- task close "$id"
      [ -n "$reason" ] && set -- "$@" --reason "$reason"
      pb "$@" || exit $?
    ;;
  # dispatch:whoami
  whoami)
    [ -n "$AUTHOR" ] || fail "whoami has no recorded author" \
      "neither the header author: line nor PASTURE_AUTHOR names an actor" \
      "set author: in $EPOCH_FILE or export PASTURE_AUTHOR to a registered actor id"
    if [ "$BACKEND" = ledger ]; then
      ledger_whoami
      exit $?
    fi
      set -- task agents show "$AUTHOR"
      pb "$@" >/dev/null || exit $?
      printf '%s\n' "$AUTHOR"
    ;;
  # dispatch:signal
  signal)
    kind=
    phase=
    skill=
    entry=
    quote=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --kind) kind="$2"; shift 2 ;;
        --kind=*) kind="${1#*=}"; shift ;;
        --phase) phase="$2"; shift 2 ;;
        --phase=*) phase="${1#*=}"; shift ;;
        --skill) skill="$2"; shift 2 ;;
        --skill=*) skill="${1#*=}"; shift ;;
        --entry) entry="$2"; shift 2 ;;
        --entry=*) entry="${1#*=}"; shift ;;
        --quote) quote="$2"; shift 2 ;;
        --quote=*) quote="${1#*=}"; shift ;;
        *) fail "signal rejected argument '$1'" "the argument is not in the signal contract" "use --kind, --phase, --skill, --entry, --quote" ;;
      esac
    done
    [ -n "$kind" ] || fail "signal needs --kind" "the kind operand was empty" "pass --kind with one registered signal kind"
    [ -n "$phase" ] || fail "signal needs --phase" "the phase operand was empty" "pass --phase with one registered phase name"
    [ -n "$skill" ] || fail "signal needs --skill" "the skill operand was empty" "pass --skill with the skill directory name"
    [ -n "$entry" ] || fail "signal needs --entry" "the entry operand was empty" "pass --entry with the record that holds the interaction"
    [ -n "$quote" ] || fail "signal needs --quote" "the quote operand was empty" "pass --quote with a short verbatim fragment"
    case "$kind" in
      frustration|positive-signal|satisfaction|confusion|correction) ;;
      *) fail "signal rejected kind '$kind'" "the kind is not in the closed set, so a later count would treat an unregistered label as data" "pass --kind with one of: frustration, positive-signal, satisfaction, confusion, correction" "signal operation in the tracker adapter, before the backend call" ;;
    esac
    case "$phase" in
      request|elicit|propose|review|plan-review|ratify|handoff|impl-plan|worker-slices|code-review|impl-uat|landing) ;;
      *) fail "signal rejected phase '$phase'" "the phase is not in the registered vocabulary, so the record would not join the phase it names" "pass --phase with one of: request, elicit, propose, review, plan-review, ratify, handoff, impl-plan, worker-slices, code-review, impl-uat, landing" "signal operation in the tracker adapter, before the backend call" ;;
    esac
    signal_line=$(signal_json)
    if [ "$BACKEND" != ledger ]; then
      FEEDBACK_ID=$(field feedback)
      [ -n "$FEEDBACK_ID" ] || fail "signal has no feedback task" "EPOCH.md has no feedback: line, so a note would run with an empty id and record nothing" "the lookup rule is in Signal Capture; do not create a task from the signal operation"
      [ -n "$AUTHOR" ] || fail "signal needs a recorded author" "the header author: line and PASTURE_AUTHOR are both empty" "set author: in $EPOCH_FILE or export PASTURE_AUTHOR to a registered actor id"
    fi
    if [ "$BACKEND" = ledger ]; then
      ledger_signal
      exit $?
    fi
      set -- task comment add "$FEEDBACK_ID" --author "$AUTHOR" -- "$signal_line"
      pb "$@" || exit $?
    ;;
  *)
    fail "unknown operation '$OP'" "the operation set is closed" \
      "use one of create, label, block, note, show, query, ready, status, close, whoami, signal; run the probe if the header is missing"
    ;;
esac
~~~
<!-- /pasture:asset rec -->


## Signal Capture

Record the user's friction or approval when it appears, so a later project-level skill can distill it. Capture is a side effect of work already happening: it never prompts the user and never blocks a phase.

### Closed kind list

The kind is a closed list. Use exactly one of:

- `frustration`: the user shows annoyance, repeats a complaint, or rejects the interaction itself.
- `positive-signal`: the user endorses a direction or a result before a gate asks.
- `satisfaction`: the user accepts a gate and says the result is what they wanted.
- `confusion`: the user asks what a term means, misreads a question, or answers a different question.
- `correction`: the user corrects a fact, a requirement, or an earlier answer.

### One feedback task per epoch

On the pasture backend, the epoch creates one feedback task at epoch start and writes `feedback: <task URI>` into `.agents.local/pasture/EPOCH.md`. A second run reads the `feedback:` line and does not create another task. On the ledger backend, create nothing and record nothing: there is no feedback task and no `feedback:` line. No other skill creates a feedback task. If the pasture line is absent, the signal operation fails and records nothing; do not create a task to fill the gap.

### Line format and recording

Write one JSON object per signal. The fields, in this order, are kind, phase, skill, entry and quote.

~~~
{"kind":"<kind>","phase":"<phase>","skill":"<skill>","entry":"<entry-ref>","quote":"<short verbatim quote>"}
~~~

- `kind`: one of the registered kinds above, exactly.
- `phase`: one of request, elicit, propose, review, plan-review, ratify, handoff, impl-plan, worker-slices, code-review, impl-uat, landing.
- `skill`: the skill that recorded it, named exactly as the skill directory.
- `entry`: the record that holds the interaction, such as a task URI plus the question or section.
- `quote`: a short verbatim fragment, max 20 words. Never paraphrase or tidy it.

Record the signal with one call. Do not name a backend command in this section.

~~~bash
"${EPOCH_REC}" signal --kind confusion --phase elicit --skill user-elicit --entry '<task URI>#end-vision' --quote 'what does arm mean here'
~~~

The adapter records that JSON object. An empty feedback id fails in the adapter and records nothing. Write the call in the same turn as the work. Do not ask permission to record. Do not wait for the write before you continue the phase.

If the write fails, follow the tracker write-failure rule: retry, keep the recorded backend, and surface the pending line at the next gate. A failed record never blocks the phase and never prompts the user.

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
Phase 8:  implementation plan -> IMPL_PLAN (supervisor decomposes into slices; Explore subagents)
Phase 9:  worker slices       -> one slice per worker (parallel workers; Ride the Wave — workers persist for review)
Phase 10: code review         -> reviewers review every slice in the wave; only an open BLOCKER gates a wave, then convergence at the end, else surface to the user
Phase 11: pasture:p11-user    -> Implementation UAT
Phase 12: landing             -> commit, push, hand off
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

Write the probe and the adapter by running the installer below against this skill file. It writes `.agents.local/pasture/probe.sh` and `.agents.local/pasture/rec` from the shipped fences in the Tracker Adapter section. Set `SKILL_FILE` to this file, run the installer, then the probe. Write `EPOCH.md` from the probe output before any task is created. The orchestrator runs this probe once; subagents read the header and never re-probe. A missing git origin is not an error backend. For pasture, the probe does not derive a namespace: record `namespace:` only to pin one, otherwise the adapter omits `--namespace` and Pasture derives it. For ledger, the probe does derive a namespace: it prints NS from derive_namespace, and every unpinned ledger write calls that same function. Copy NS to `namespace:` only to override it. A missing or unregistered author is a warning (`WARN=no-author`), not an error backend.

```bash
# pasture:install-tracker
install_tracker_assets() {
  skill=$1
  dest=${2:-.agents.local/pasture}
  if [ -z "$skill" ] || [ ! -f "$skill" ]; then
    printf 'install: no skill file\n  why: install_tracker_assets needs the rendered skill path\n  where: Starting an Epoch install step\n  fix: set SKILL_FILE to this skill and pass it as the first argument\n' >&2
    return 1
  fi
  mkdir -p "$dest" || return 1
  awk -v dest="$dest" '
    $0 == "<!-- pasture:asset probe -->" { kind = "probe"; next }
    $0 == "<!-- pasture:asset rec -->" { kind = "rec"; next }
    $0 ~ /^<!-- \/pasture:asset / { kind = ""; grab = 0; next }
    kind != "" && $0 == "~~~sh" { grab = 1; next }
    kind != "" && grab == 1 && $0 == "~~~" { grab = 0; next }
    grab == 1 && kind == "probe" { print > (dest "/probe.sh"); next }
    grab == 1 && kind == "rec" { print > (dest "/rec"); next }
  ' "$skill"
  if [ ! -s "$dest/probe.sh" ] || [ ! -s "$dest/rec" ]; then
    printf 'install: the skill did not emit probe.sh and rec\n  why: the Tracker Adapter fences were missing or empty\n  where: %s\n  fix: regenerate the skill so it contains pasture:asset probe and pasture:asset rec\n' "$skill" >&2
    return 1
  fi
  chmod +x "$dest/probe.sh" "$dest/rec"
}
# /pasture:install-tracker
install_tracker_assets "$SKILL_FILE" .agents.local/pasture
sh .agents.local/pasture/probe.sh
# Then write .agents.local/pasture/EPOCH.md from the keys named below. Do not
# copy a derived NS into namespace: unless you mean to pin it. In every shell
# session resolve the entry point:
EPOCH_REC=$(sed -n 's/^rec: //p' .agents.local/pasture/EPOCH.md | tail -1)
```

If the probe prints `BACKEND=error`, stop and report before the first mutation: the store exists but cannot be used. If it prints `BACKEND=ledger`, record `backend:`, `ledger:`, `rec:` and `probed_at:` from the output and continue; the adapter writes one JSONL line per mutation under that ledger and derives the namespace with the same function that printed NS. Offer a gitignore entry; never require one. If it prints `BACKEND=pasture`, record `backend:`, `db:`, `author:`, `rec:` and `probed_at:` from the output. Record `namespace:` only to pin one; the adapter then passes `--namespace`, and that pin wins over Pasture's derivation.

**Step 0b: create the epoch feedback task once, and only on the pasture backend.** This is the only create. On the pasture backend, create the task once and record `feedback:`. On the ledger backend, create nothing and record nothing. The lookup rule, including who may create the task, is in Signal Capture.

```bash
BACKEND=$(sed -n 's/^backend: //p' .agents.local/pasture/EPOCH.md | tail -1)
if [ "$BACKEND" = pasture ]; then
  FEEDBACK_ID_URI=$(sed -n 's/^feedback: //p' .agents.local/pasture/EPOCH.md | tail -1)
  if [ -z "$FEEDBACK_ID_URI" ]; then
    FEEDBACK_ID_URI=$("${EPOCH_REC}" create --kind task --title "FEEDBACK: epoch signals" --phase unscoped)
    printf 'feedback: %s\n' "$FEEDBACK_ID_URI" >> .agents.local/pasture/EPOCH.md
  fi
fi
```

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
FOLLOWUP → FOLLOWUP_URE → FOLLOWUP_URD → FOLLOWUP_PROPOSAL-1 → FOLLOWUP_IMPL_PLAN → the follow-up slice
```

- **FOLLOWUP_URE**: Scoping URE with user to determine which DEFER'd items to address
- **FOLLOWUP_URD**: Requirements doc for follow-up scope (references original URD)
- **FOLLOWUP_PROPOSAL-{N}**: Proposal accounting for original URD + FOLLOWUP_URD + the DEFER'd items
- **FOLLOWUP_IMPL_PLAN**: Supervisor decomposes follow-up into slices
- **the follow-up slice**: Each slice implements the DEFER'd-item work decomposed into leaf tasks

See `/pasture:supervisor` and `/pasture:impl-review` for full creation commands.

## EAGER Severity Tree (Phase 10)

Code reviews ALWAYS create ONE severity trio (3 group tasks) per wave, per axis, per round, even if empty:

```bash
# Create all 3 severity groups immediately (EAGER, not lazy)
BLOCKER_ID_URI=$("${EPOCH_REC}" create --kind task --title "BLOCKER" --phase code_review \
   --body "Severity group for this review round; close only if empty")
"${EPOCH_REC}" label "$BLOCKER_ID_URI" pasture:severity:blocker
IMPORTANT_ID_URI=$("${EPOCH_REC}" create --kind task --title "IMPORTANT" --phase code_review \
   --body "Severity group for this review round; close only if empty")
"${EPOCH_REC}" label "$IMPORTANT_ID_URI" pasture:severity:important
MINOR_ID_URI=$("${EPOCH_REC}" create --kind task --title "MINOR" --phase code_review \
   --body "Severity group for this review round; close only if empty")
"${EPOCH_REC}" label "$MINOR_ID_URI" pasture:severity:minor

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
"${EPOCH_REC}" query --phase worker_slices          # Implementation slices
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
