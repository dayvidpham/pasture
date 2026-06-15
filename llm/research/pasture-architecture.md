---
title: Understanding Pasture
date: 2026-06-13
source: github.com/dayvidpham/pasture (this repo)
---

# Understanding Pasture

## Executive summary

Pasture (`github.com/dayvidpham/pasture`) is a Go durable orchestrator for multi-agent LLM software-engineering workflows. It runs a software feature — an **epoch** — through a fixed, auditable **12-phase protocol** (`request → elicit → propose → review → plan-review → ratify → handoff → impl-plan → worker-slices → code-review → impl-uat → landing → complete`) while coordinating ephemeral Claude Code agents (architect, supervisor, worker, reviewer, and a master epoch orchestrator).

Its defining choice is a clean seam between **a pure, substrate-free protocol state machine** (`pkg/protocol`) and **an impure durable-execution adapter** (`internal/engine`) built on **DBOS Transact** over SQLite — which replaced an earlier Temporal substrate. Because the FSM has zero DBOS/SQLite imports, the entire Temporal→DBOS migration rewrote only the adapter behind one substrate-neutral interface (`EpochController`); the 12-phase topology, gate rules, and vote accounting stay exhaustively unit-testable without starting any engine.

Pasture ships as a **Claude Code plugin**: phases, roles, constraints, and commands are declared once as typed Go specs and code-generated into `schema.xml`, per-role skills, and agent definitions, so agent prompts can't drift from the contract the engine enforces. The intelligence lives in the agents; **durability, gating, audit, recovery, and provenance** live in Pasture.

The client/host split is deliberate. **Submission is daemonless**: the `pasture` CLI opens one SQLite file, writes a durable enqueue/signal row, and exits — no daemon, IPC, or network. **Execution is hosted**: a single `pastured` daemon is the only process that runs the DBOS recovery sweep and the long-lived `Recv` loops that advance the FSM. Submitted work is durable but **inert until a host consumes it**.

## What Pasture is and main use cases

Pasture makes multi-agent software workflows **correct-by-construction rather than correct-by-convention** — instead of agents free-associating over a shared scratchpad, it imposes a state machine where each phase is owned by a role with gates that must pass before advancing.

Three deployment faces:
- **`pastured`** — the long-running DBOS engine host; the *only* caller of `engine.Launch()` (recovery sweep on startup, then dequeues epoch-control/slice/review workflows and dispatches hooks until SIGINT/SIGTERM).
- **`pasture`** — thin ephemeral CLI: open the file, do one operation, print, exit. No IPC.
- **`pasture-release`** — SemVer/changelog/GitHub-release tool for plugin distribution. (Plus test binaries: `pasture-recovery-probe`, `pasture-migrate-crash`, `pasture-test-agent`.)

All durable state lives in **one `pasture.db`** (default `~/.local/share/pasture/pasture.db`) holding DBOS workflow/queue/step tables, the audit trail (`audit_events` + `context_edges`), the CQRS read model (`epoch_state_projection`), and a PROV-O provenance graph. Build is pure Go (`CGO_ENABLED=0`, `modernc.org/sqlite`) — static binaries, no native deps.

**Use cases:** local multi-agent coordination with no external infra; durable agent workflows surviving crashes; audit/forensics/provenance with deterministic dedup keys and timeline reconstruction; constraint-gated execution; Claude Code integration where generated skills/agents stay in sync with the enforced protocol.

## Features

1. **Durable 12-phase epoch lifecycle.** `EpochStateMachine.Advance()` is the sole mutation path; it `ValidateAdvance()`s first and rejects invalid jumps as errors that never hit the DB. `PhaseSpecs` is a compile-time constant; phase numbering is positional, so phases reorder without renaming.
2. **Signal-driven advancement.** The CLI never calls engine methods directly — it sends typed signals via `dbos.Client.Send` addressed to the workflow by id (= epoch id). The control loop drains three side-channels (`submit_vote`, `register_session`, `slice_progress`) before each advance so just-arrived votes are visible to the gate.
3. **Consensus/voting gates + revision loop.** Binary `ACCEPT`/`REVISE` across three axes (correctness, test_quality, elegance). Consensus gate (P4→P5, P10→P11) needs all three ACCEPT (silence ≠ consent); any single REVISE strips the forward edge and forces backward; blocker gate holds P10→P11 while `BlockerCount > 0`. Fixed priority: **REVISE > consensus > blocker**. Votes clear on every transition.
4. **Concurrency-bounded slice/review sub-workflows.** Enqueued (not launched) onto DBOS queues; slice queue concurrency **K** (`--slice-concurrency` > `$PASTURE_SLICE_CONCURRENCY` > 8; ≈4 on HDD, 16 on NVMe). Control queue concurrency-1. Excess waits durably and survives restarts.
5. **Exactly-once forensics.** Each transition writes one `audit_events` row and one PROV-O activity row, both keyed by `DedupKey(epochID, phase, kind, stepSeq)` — a UUIDv5 over a frozen namespace folding the replay-stable `dbos.GetStepID`. Audit uses a partial unique index + `ON CONFLICT DO NOTHING`; provenance uses `StartActivityWithID`. `context_edges` (BCNF triple) attaches one event to many contexts.
6. **Crash recovery across rebuilds.** `Launch()` resumes incomplete workflows from the last committed step, filtered by **pinned** `ExecutorID`/`ApplicationVersion` (constants, not binary hashes — otherwise every rebuild orphans in-flight epochs). `engine.New` rejects an empty version.
7. **Sessions + observability hooks.** Idempotent session registration (dup `SessionId` dropped); a typed non-blocking `hooks.Manager` over 13 events, handlers in bounded-timeout goroutines, dispatched inside a durable step so hooks fire exactly once on replay; hook errors logged, never propagated.
8. **Daemonless audit recording.** `pasture hook record` builds its own in-process `Manager` + `GitRecorder` and writes audit rows with no daemon.
9. **The Claude Code plugin.** One spec source generates `schema.xml`, `/pasture:*` skills, and `agents/*.md`; hand-authored prose preserved across regen. Five roles; a configurable review-effort budget; DEFER'd UAT items (only those) seed a FOLLOWUP epic.
10. **Cross-cutting infra.** Layered config (flag > env > YAML > default via `bindChangedFlag`); one DSN contract (`SharedDSN`: WAL, `busy_timeout=5000`, `synchronous=NORMAL`, `foreign_keys=ON`, `_txlock=immediate`); one `StructuredError` whose `Category` drives header + stable exit code; ACP adapter ingesting agent transcripts into audit sessions.

## The Epoch Protocol & Programming Model

```
   SUBMISSION HALF (daemonless)              EXECUTION HALF (hosted)
   ────────────────────────────             ───────────────────────
   pasture CLI / LLM harness                pastured  (ONLY caller of engine.Launch)
        │  opens pasture.db                      │  opens pasture.db, runs recovery sweep
        │  dbos.NewClient ONLY                   │  EpochControlWorkflow parks on dbos.Recv
        │  (no engine, no Launch, no IPC)        │  drains side-channels, drives EpochStateMachine
        ▼                                        ▼
   ┌────────────────────  pasture.db (single SQLite file, WAL)  ────────────────────┐
   │  DBOS workflow/queue/step · audit_events + context_edges · epoch_state_projection │
   │  PROV-O activities/agents/tasks/edges                                            │
   └─────────────────────────────────────────────────────────────────────────────────┘
   A CLI-written signal is durable the instant it commits, but INERT until pastured's Recv loop consumes it.
```

Everything an external actor does goes through `EpochController` (`StartEpoch`, `CancelEpoch`, `TerminateEpoch`, `AdvancePhase`, `SubmitVote`, `ReportSliceProgress`, `RegisterSession`, `StartSlice`, `CompleteSlice`, `Close`) — no DBOS/Temporal terms; this *is* both the swap seam and the surface an LLM harness programs against via CLI verbs.

**Walkthrough:** `pasture task create` → the epoch id **is** a Provenance `TaskId`, the correlation key across all three subsystems. `pasture epoch start` → `dbos.Enqueue` onto `pasture-control-queue` targeting `pasture.epoch_control.v1`, epoch id as both workflow id and input → idempotent (re-enqueue of a running epoch is a no-op). When `pastured` dequeues it, `EpochControlWorkflow` loops: block on `Recv(advance_phase, 30s)`; on timeout drain side-channels + project; on advance, drain first, capture `GetStepID` **in the workflow body**, call pure `sm.Advance`, then `commitTransition`. A gate violation is **non-fatal** — recorded as a failed transition, loop keeps waiting. `commitTransition` does projection + audit + provenance in one `RunAsStep`. Slices/reviews enqueue as sub-workflows with deterministic ids (review id folds the round counter so a REVISE retry runs fresh, not a memoized round-1 result). At `PhaseComplete` the loop returns the terminal state.

**Determinism contract:** pure mutations in the workflow body (replay-identical); all I/O in durable steps (memoized on replay); recovery cohort pinned at build time; dedup namespace immutable.

## Downstream semantics — guaranteed vs. needs a host

**Guaranteed with a host running:** exactly-once transitions + forensic emission (kill-9 test asserts count==1/phase); durable signals; crash recovery filtered by pinned cohort; idempotent session/dedup; K-bounded backpressure.

**Guaranteed even daemonless:** durable enqueue + signal writes; idempotent epoch start; terminate records the `EpochCancelled` audit row *before* `CancelWorkflow` (survives a wedged target); read paths (`status`/`query` are plain SQL on the projection, transitions recomputed live from the FSM, never stored); `pasture hook record`.

**Requires a host (inert until then):** any *consumption* of a signal — a queued epoch doesn't start, a vote gates nothing, a slice doesn't run, a phase doesn't advance until `pastured` dequeues and its `Recv` loops fire. A vote sent while down is **not lost** — it waits in the signals table and is drained on the next poll.

**Hooks vs. signals (load-bearing):**

| | Signals | Hooks |
|---|---|---|
| Purpose | **State advancement** | **Observability** |
| Mechanism | `client.Send` → `dbos.Recv` | `Manager.Dispatch` fan-out |
| Consumed by | the host's Recv loop | handler goroutines (best-effort) |
| Effect on FSM | drives Advance/RecordVote/slice | **none** |
| Needs daemon? | yes, to be consumed | no |
| Failure | durable, retried | logged + swallowed |

Note: Pasture's `HookEvent` enum is *internal* lifecycle observability and deliberately excludes Claude Code's own hook events; bridging a Claude Code Stop hook to `HookGitCommit` is flagged as **future work**.

## Architectural design principles

1. **Pure FSM core vs. impure adapter — the Temporal→DBOS swap seam.** `pkg/protocol` imports only stdlib + uuid + the Provenance `TaskTracker` interface — no DBOS/Temporal/SQLite anywhere. The *same* `EpochStateMachine` was wrapped by the legacy Temporal workflow and is now wrapped by DBOS; the migration rewrote only the adapter. Two seams made it surgical: `EpochController`, and "pure FSM in the workflow body, all I/O in durable steps." New gates land as pure Go with `go test` (30+ FSM tests), no engine needed.
2. **Durability delegated to DBOS over one unified SQLite file** — exactly-once, recovery, signal delivery, queues are a *dependency*, not a subsystem Pasture maintains. One WAL concurrency contract, one file to back up, no cross-DB JOIN, no drift between workflow truth and audit truth.
3. **CQRS projection read model** — writes to the DBOS log + durable steps; reads from `epoch_state_projection` as plain SQL; available transitions recomputed from canonical rules per read (a new gate auto-changes what readers show).
4. **Client/host split honors SQLite single-writer by construction** — only `pastured` writes; backpressure bounded at the queue (K), not ad-hoc locking.
5. **Signal-driven FSM with drain-before-gate** — new side-channel types just join the drain; non-fatal gates mean stricter gates need no new abort paths.
6. **Hooks as non-blocking fan-out decoupled from state** — a handler failure can't roll back a transition; new observability is strictly additive; `HooksMgr == nil` is a safe no-op.
7. **Exactly-once via deterministic ids + `OnTransition` seam** — one `DedupKey` fn, composition over inheritance (a function field; engine prepends `recordActivity`); frozen namespace pinned by a golden test.
8. **Pinned cohort for recovery across rebuilds** — turns a dangerous implicit DBOS default into a reviewed decision.
9. **`StructuredError` discipline** — one categorized type drives header + stable exit code from an enum.

## How Pasture differs from scripts, task queues, and raw DBOS

- **vs. a script:** a script loses all state on SIGKILL/OOM/closed laptop; Pasture parks between advances and resumes from the last committed step, enforces gates in Go *before any write*, and tracks ephemeral-agent churn via idempotent sessions + `context_edges`.
- **vs. a generic task queue:** durability is necessary but not sufficient — a queue dispatches *opaque* units and would happily "advance to phase 5" with a REVISE outstanding. Pasture encodes the whole topology + gate priority + phase-scoped vote clearing as protocol invariants. The historical proof: it embedded Temporal and deliberately moved off it (preserved as an opt-in `legacy/temporal/` module) — the queue was swapped wholesale behind `EpochController`; the protocol is the asset.
- **vs. raw DBOS:** DBOS supplies the engine block; Pasture adds the pure FSM, the role/phase domain model, the projection/audit/PROV-O layers, the hook bus + daemonless `hook record`, and the Claude Code plugin. With DBOS alone you'd write gate logic entangled with the substrate, design your own dedup scheme, have no roles/votes/slices, and hand-build the prompt layer.
- **The daemon question:** submission is fully daemonless; orchestration needs a host that can be **on-demand, not always-on**. Queue an epoch's signals while `pastured` is down (they persist), start it to make progress (resumes from last step), stop it when idle (ordered `Shutdown(10s)` drain), rebuild without orphaning (pinned cohort). Serverless, local-first submission; a single optional on-demand execution host.

## Honest caveats

- **The CLI/daemon boundary:** submission writes are durable but inert; whether `pastured` is *meant* to be routinely stopped/started on-demand vs. left running is a capability inferred from the design, not a stated operator recommendation.
- **A host is required to consume signals** — they wait durably until the `Recv` loops fire.
- **SQLite single-writer:** the audit handle pins `MaxOpenConns=1`; the engine handle stays uncapped because DBOS's notification poller needs a second connection — an asymmetry that could surface under heavy CLI use concurrent with a running daemon.
- **`ApplicationVersion` literal is reported inconsistently** in the findings (`"1"` from the source-grounded engine/handlers indexes vs. `"v0.1.0"`/`"recovery-probe-v1"` from the cmd index). The load-bearing invariant — a *stable pinned constant, not a per-build hash* — holds regardless; the exact literal wasn't reconciled. Worth a direct check.
- **Mid-transition details:** a v3→v4 `epoch_id`-drop migration, an `ensurePastureTables` bridge, vestigial `auditDB` params, unimplemented tmux/subprocess slice modes, and the not-yet-built Stop-hook bridge are flagged as possibly in-flight.
- **Potential hook double-fire window during recovery** (a handler slower than the 5s timeout before DBOS saves the step) is noted as unguarded, unlike audit rows which have dedup keys.
- Cited paths were confirmed to exist, but exact line numbers/symbol names at HEAD were not independently re-verified.

## Key takeaways

- Pasture is a **domain-specific durable state machine, not a workflow engine** — the protocol is the product; DBOS is interchangeable plumbing it already swapped in.
- The **pure-FSM / impure-adapter seam** is the central maintainability decision and what made the Temporal→DBOS swap a one-adapter change.
- **Submission is daemonless and durable; execution is hosted and on-demand.**
- **Exactly-once is one deterministic derivation** (`DedupKey` + pinned cohort) surviving both kill-9 and rebuilds.
- **Gates are enforced in code before any write**, in fixed priority, with phase-scoped vote clearing.
- **Signals advance state; hooks only observe** — no feedback path from a hook into the FSM.
- **One SQLite file, single-writer-aware by construction** via the single-host rule + K-bounded queue.
- **The plugin can't drift from the engine** — skills/agents are code-generated from one typed Go spec.

---

# Appendix: Can the submitter also drain? (Why `pastured` is genuinely needed)

**Question explored:** what if every `pasture` CLI invocation (the submitter) ALSO ran an execution/drain step after writing its durable enqueue/signal row — folding the on-demand host *into* the submitter so the system could make progress "generally without a daemon"?

**DECISION: KEEP the `pastured` daemon.** The submitter-drain idea is sound in spirit but the concerns below are genuine, and a host is genuinely required for time-driven transitions and in-workflow slice execution. `pastured` stays as the single long-running execution host; submission remains daemonless (CLI `dbos.Client` enqueue + `Send`). This appendix records *why* so the trade-off is not re-litigated from scratch.

**Conclusion (supporting the decision):** the instinct is right — it's the principled version of "submission is daemonless, execution is on-demand," and at most it would turn `pastured` from a *required* always-on daemon into an *optional* one for the event-driven common path. But making it correct requires a focused refactor across four load-bearing points, each tracing back to a constraint already in the codebase, and even then a periodic host is still needed. The concerns are genuine, so **`pastured` is kept**.

## The naive version reproduces the known bug

Inlining execution by simply starting the long-lived `EpochControlWorkflow` in the short-lived CLI context is exactly the `ERROR: context canceled` failure the PR reviewer flagged (`aura-plugins-xfyrj`): the control workflow runs *until* `PhaseComplete`, blocking on `Recv(advance_phase, 30s)` between advances; when the CLI exits, the context cancels and DBOS records the workflow as errored, leaving later signals persisted but unconsumed. The fix is not "start it and hope" — it is "drive to quiescence and exit cleanly." The workflow must be designed to **return**, not to **park**.

## Three hard requirements

**1. Mutual exclusion — only one drainer at a time (SQLite single-writer).**
The load-bearing constraint. With `_txlock=immediate`, writers serialize. If a submitter launches an engine while `pastured` *or another submitter* is also hosting one, you have two executors with the **same pinned `ExecutorID` (`pasture`)** both running the recovery sweep over the same pending cohort, contending on the one WAL writer. The "single-host rule" exists precisely to forbid this. A submitter-drain must therefore **replace** the daemon, not supplement it, and concurrent submitters must not both drain. The clean primitive is a **host/drain lock with try-or-skip semantics**: the submitter attempts to grab it; if another drainer holds it, the submitter exits immediately — its write is already durable, someone else will drain it. Opportunistic, self-healing, no daemon.

**2. A bounded "drive-to-quiescence" operation, not the current `Recv` loop.**
`EpochControlWorkflow` today runs for the whole epoch lifecycle (hours/days) and is unbreakable for a CLI. The replacement operation:
> rebuild the FSM from the projection (`NewEpochStateMachineFromState` already exists) → consume all *currently pending* signals → apply every gate that is now satisfiable → commit each transition → **return when no further progress is possible** (no pending advance, or blocked awaiting more votes / blockers).

This is a real shift from "a workflow that owns the loop" to "crank the handle once." Crash safety is inherited for free: SIGKILL mid-drain is resumed by the next drainer via DBOS exactly-once + the dedup keys.

**3. Externally-driven slices only.**
Advancing into implementation enqueues slice sub-workflows. If a slice's body *blocks* (execs a tmux/subprocess agent and waits for it), the host must stay alive for that slice's entire duration → you are back to needing a daemon. The daemonless model **requires** the pattern that already has an escape hatch: the agent does its work out-of-band, then calls `pasture slice complete`, and a *later* drain advances on the completion signal. (The tmux/subprocess modes are currently not-implemented; `complete_slice` is exactly this override.) In-workflow blocking execution is fundamentally incompatible with a transient host.

## The tail-drain guarantee (the subtle part)

Opportunistic drain risks the *last* signal never being drained (the final voter writes the deciding ACCEPT and exits; no one runs after them). The fix: **every signal-writer runs drive-to-quiescence after its own write**, under the lock, try-or-skip. The last writer always observes the complete signal set; overlapping drains are safe because dedup keys collapse a double-advance; a crashed drainer is recovered by the next one. That closes the loop for **event-driven** transitions.

It does **not** cover **time-driven** transitions — a durable `Sleep` or a timeout/deadline gate will not fire while nobody is draining. Those still need *something* periodic. That "something" can be a `systemd`/cron timer firing `pasture drain` (stateless, restart-free) — lighter than a persistent daemon, but still a host that must exist. **This is one of the two reasons `pastured` cannot be fully eliminated.**

## The sharpest gotcha: the dedup key becomes invocation-dependent

Grounded in the current code. The forensic dedup key is `DedupKey(epochID, phase, kind, stepSeq)`, where `stepSeq` comes from `dbos.GetStepID` captured **inside the running control workflow** — replay-stable *within one workflow execution*. If execution is split across many separate drain invocations, `stepSeq` is **no longer stable across processes**: two different drainers advancing the same transition would derive *different* `stepSeq` values and write *two* audit/provenance rows instead of collapsing to one — breaking the exactly-once forensics the whole design guarantees.

Moving to a drain model therefore requires re-deriving the dedup key from something **invocation-independent** — the transition's own identity (e.g. `from-phase → to-phase` plus the REVISE round/attempt) rather than the DBOS step ordinal. Contained, but mandatory, and easy to miss. (Scope: trace `commitTransition`'s `stepSeqInt, _ := dbos.GetStepID(ctx)` capture in `internal/engine/control.go` and the `DedupKey` derivation in `pkg/protocol/dedup.go`.)

## Design sketch — opportunistic drain (if this were pursued)

```
pasture <verb> (any signal/lifecycle write):
  1. open pasture.db (dbos.Client) — as today
  2. write the durable row (Enqueue / Send)            ← submission, daemonless, always succeeds
  3. try-acquire HOST_LOCK (single-writer drain lock)
       ├─ NOT acquired → exit 0   (row is durable; another drainer or a later
       │                            invocation will consume it)
       └─ acquired → driveToQuiescence(); release; exit 0

driveToQuiescence(epochId):           # bounded, idempotent, crash-safe
  sm := NewEpochStateMachineFromState(readProjection(epochId))
  loop:
    drained := drainPendingSignals(sm)         # votes, sessions, slice_progress, one advance
    if no advance was applicable: break        # quiescent: no pending advance, or gate-blocked
    rec := sm.Advance(...)                      # pure; gate violation → record failed, break
    commitTransition(epochId, rec,             # one RunAsStep: projection + audit + provenance
                     dedupKey = f(fromPhase, toPhase, round))   # ← invocation-independent key
  # returns; process exits. Dedup keys make a concurrent/overlapping drain a no-op.
```

Invariants this must preserve (all already true of the daemon path; the drain must not regress them):
- **At most one drainer** holds `HOST_LOCK` ⇒ honors SQLite single-writer.
- **Every transition is exactly-once** ⇒ invocation-independent dedup key + DBOS step memoization.
- **Gate violations are non-fatal** ⇒ record failed transition, stop, leave epoch parked (as today).
- **Crash mid-drain is safe** ⇒ next drainer resumes; nothing partially-committed survives un-dedup'd.

## Why `pastured` stays

Even with all of the above, two needs keep a host in the picture:

1. **Time-driven progress.** Durable `Sleep`, timeout gates, and any future deadline-based transition require *something* running to fire them. Opportunistic submitter-drain only advances when a submitter happens to run. A periodic kick (`pastured`, or a timer running `pasture drain`) is unavoidable for timely time-based behavior.
2. **In-workflow slice execution.** Any slice that runs and *waits* inside the sub-workflow needs a host alive for its duration. Only the fully externally-driven slice pattern avoids this, and that is a stronger constraint on how agents are launched than the protocol currently assumes.

So the honest position: the submitter-drain model is a **legitimate optimization that makes `pastured` optional for the event-driven common path**, but it is **not** a way to remove the host entirely. The four bolded requirements (drain lock; `driveToQuiescence()`; invocation-independent dedup key; externally-driven slices) are the price, and the residual need for a periodic host for time-driven transitions and blocking slices is why **`pastured` is genuinely needed** rather than merely convenient.

## Summary table

| | Always-on `pastured` (today) | Submitter-drains (the "what if") |
|---|---|---|
| Daemon | required | optional for event-driven path; still needed for time-driven & blocking slices |
| Concurrency control | one host by construction | **needs an explicit drain lock (try-or-skip)** |
| Control flow | long-lived `Recv` loop | **bounded `driveToQuiescence()`** |
| Slices | host can block in-workflow | **must be externally-driven** |
| Dedup key | `stepSeq` from `GetStepID` | **must be invocation-independent** |
| Time-based gates | handled by the running loop | **need a periodic kick** |
| Latency to advance | immediate | eventual (next drain) |
| Crash safety | DBOS exactly-once | DBOS exactly-once (preserved) |
