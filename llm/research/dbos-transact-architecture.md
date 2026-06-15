---
title: Understanding DBOS Transact (Go)
date: 2026-06-13
source: github.com/dbos-inc/dbos-transact-golang (~/codebases/dbos-transact-golang)
---

# Understanding DBOS Transact (Go)

## Executive Summary

DBOS Transact is a lightweight, Postgres-backed **library** (not a server) that adds durable, crash-recoverable workflow execution to ordinary Go programs. You import a package, annotate plain Go functions as workflows and steps, and DBOS checkpoints their execution state directly into a system database (Postgres, plus CockroachDB and SQLite). When a process crashes mid-operation, workflows automatically resume from the last completed step on restart.

The entire system rests on one architectural idea: **Postgres is the coordination layer, not just storage.** Every distributed decision — who owns a workflow, whether a step already ran, whether a queued task may start, whether a message was consumed, rate limiting, deduplication — is resolved inside a single SQL statement (`INSERT ... ON CONFLICT`, data-modifying CTEs, `FOR UPDATE SKIP LOCKED`/`NOWAIT`). There is no separate orchestrator, scheduler daemon, message broker, lock table, or lease/heartbeat mechanism. That is what lets DBOS slot into an existing program with minimal rearchitecting, and it is the source of both its strong semantics and its small, maintainable surface.

The core contract, precisely: **a step's function body runs at-least-once, but its observable result within a workflow is exactly-once** — once a step is checkpointed to the `operation_outputs` table, replay returns the stored value rather than re-executing the body. Recovery is *re-execution with memoization*, not state/continuation serialization: the workflow function is re-invoked from the top, and each step short-circuits to its recorded result until execution reaches the first unrecorded step. This places one obligation on the developer — the workflow body must be deterministic — which DBOS enforces actively at runtime.

---

## Main Use Cases

All facets of one theme — **reliable failure handling** — where a crash, restart, deploy, or partition mid-operation must not corrupt state or lose work:

1. **Reliable failure handling (umbrella).** Write imperative code "as if it never crashes"; the framework supplies durability by replaying and skipping completed steps.
2. **Payments / financial transactions.** Each side-effecting op (debit, credit, ledger write) is a step checkpointed after it completes; on restart the charge resumes from the last unrecorded step rather than re-charging or losing the credit.
3. **Long-running data pipelines.** Multi-stage ingest/transform resumes from checkpoints. Positioned against Airflow: workflows are regular Go code (not explicit DAGs), need only Postgres, and suit streaming/real-time.
4. **Exactly-once event processing (webhooks, Kafka).** Derive the workflow ID from the event (key/offset/delivery ID); a redelivered event re-attaches to the existing workflow instead of reprocessing.
5. **Scheduled / cron jobs.** Cron-syntax periodic jobs backed by Postgres; tick time is encoded in the workflow ID and guarded by the dedup index, so multiple replicas don't double-fire — and missed ticks can be backfilled.
6. **Async task queues with flow control.** Durable background jobs *plus* concurrency caps, rate limits, priority, partitions. Against Celery/BullMQ: fewer raw-throughput guarantees, but end-to-end durability.
7. **AI agents / flaky non-deterministic APIs.** Checkpoint recovery means a crash doesn't replay expensive non-idempotent LLM/3rd-party calls; non-determinism is confined to steps, recorded once.
8. **Human-in-the-loop / long waits.** Durable `Send`/`Recv`, `SetEvent`/`GetEvent`, and durable `Sleep` let a workflow block on an external signal or timer for days/weeks — the wait state lives in the DB, not a process that may be redeployed.

*(Temporal/Airflow/Celery comparisons are the project's own positioning, not benchmarks.)*

---

## Features

- **Durable Workflows** — `RegisterWorkflow`/`RunWorkflow`; steps via `RunAsStep` checkpointed to `operation_outputs` keyed by `(workflow_uuid, function_id)`. Idempotent start via `ON CONFLICT (workflow_uuid)`. Per-step retries w/ backoff (`MaxStepRetriesExceeded`), durable `Sleep`, deterministic child IDs (`<parentID>-<stepID>`), concurrent steps (`Go`/`Select`), dead-letter at `maxRetries+1` (`MAX_RECOVERY_ATTEMPTS_EXCEEDED`), version-scoped recovery, `ForkWorkflow`, and `Patch`/`DeprecatePatch`.
- **Durable Queues** — `NewWorkflowQueue`; a queued task is a `workflow_status` row with status `ENQUEUED` (no goroutine at enqueue time). A poller claims rows with `FOR UPDATE SKIP LOCKED`/`NOWAIT`. Crash-safe, fleet-wide: worker+global concurrency limits, rate limiting (aggregate `COUNT` inside the dequeue txn), deduplication (partial unique index), priority, partitioned queues, delayed execution, and orphan recovery (`clearQueueAssignment`).
- **Debouncing** — the debouncer is *itself a durable workflow*, using dedup to keep one per key and `Send`/`Recv` to push the deadline forward; mid-debounce state is durable and recoverable.
- **Durable Notifications (`Send`/`Recv`)** — ordered `notifications` inbox; `Recv` consumes one message *and* records its step result in one transaction. `LISTEN/NOTIFY` wakes receivers sub-ms, with polling fallback so correctness never depends on the notification.
- **Durable Events (`SetEvent`/`GetEvent`)** — latest-value KV in `workflow_events` + append-only `workflow_events_history` (lets fork replay reconstruct event state); `GetEvent` blocks with a durable timeout.
- **Streams** — ordered, durable, append-only log per `(workflow, key)` for progressive result delivery; externally readable via `Client`.
- **Cron Scheduling** — schedules in `workflow_schedules` reconciled into an in-memory cron; content-addressed tick IDs (`sched-<name>-<RFC3339 tick>`) + dedup index give *at most one workflow per tick across executors*; timezone support + automatic backfill.
- **Programmatic & Remote Management** — four surfaces all delegating to the same `systemDB` methods: in-process `WorkflowHandle[R]` (cancel/resume/fork/list/steps, guarded status-machine transitions); out-of-process `Client` (enqueue, send, schedule CRUD, cross-language dispatch via `PortableWorkflowArgs`); HTTP `adminServer` (port 3001 — **no built-in auth; `/dbos-garbage-collect` is a no-op stub**); and the `conductor` (outbound WebSocket to the cloud control plane, 27 typed verbs, NAT-friendly).
- **Supporting infra** — pluggable `Serializer[T]` (format name stored per record → mixed-language system DB), multi-backend dialect layer, typed `DBOSError`/`DBOSErrorCode` taxonomy, operator CLI (`init`/`migrate`/`start`/`reset`/`postgres`/`workflow ...`), and a `Launch`→recover / `Shutdown`→drain lifecycle.

---

## Downstream Semantics & Programming Model

The mental model in one sentence: **you write ordinary imperative Go, and DBOS replays your function from the top after a failure, skipping work it already finished.**

```go
// STEP: ordinary fn, may do anything non-deterministic/side-effecting.
// Takes context.Context — must NOT call DBOS primitives.
func chargeCard(ctx context.Context) (Receipt, error) {
    return paymentAPI.Charge(ctx, /* idempotency key */)
}

// WORKFLOW: deterministic orchestration. Takes dbos.DBOSContext.
// May be re-run many times; MUST issue the same sequence of DBOS calls each time.
func PaymentWorkflow(ctx dbos.DBOSContext, in PaymentInput) (PaymentResult, error) {
    // Step 0 → operation_outputs(workflow_uuid, 0). On replay, returns the
    // recorded Receipt WITHOUT re-running chargeCard.
    receipt, err := dbos.RunAsStep(ctx, chargeCard, dbos.WithStepMaxRetries(3))
    if err != nil { return PaymentResult{}, err }

    if receipt.Declined {                       // OK: branch on a step's OUTPUT
        return PaymentResult{Status: "declined"}, nil
    }
    _ = dbos.SetEvent(ctx, "receipt_id", receipt.ID)  // atomic with its checkpoint
    _, _ = dbos.Sleep(ctx, 24*time.Hour)              // durable wait, survives crash
    return PaymentResult{Status: "settled", ReceiptID: receipt.ID}, nil
}

func main() {
    ctx, _ := dbos.NewDBOSContext(context.Background(),
        dbos.Config{AppName: "payments", DatabaseURL: dbURL})
    dbos.RegisterWorkflow(ctx, PaymentWorkflow)   // before Launch
    _ = dbos.Launch(ctx)                          // migrate + recover pending
    h, _ := dbos.RunWorkflow(ctx, PaymentWorkflow, in, dbos.WithWorkflowID("order-42"))
    res, _ := h.GetResult()                       // same ID twice → attaches to existing run
    _ = res
}
```

**Exactly-once, precisely.** Step results are keyed by `(workflow_uuid, function_id)` where `function_id` is a monotonic integer (steps numbered 0,1,2… in call order). On re-execution, `checkOperationExecution` returns the recorded row without running the body. So the step *body* runs **at-least-once**; its *result within the workflow* is **exactly-once**. The honest caveat: DBOS guarantees exactly-once *checkpointing*, not exactly-once *external side effects* — if the process dies inside a side effect before the checkpoint commits, it can re-run. Make external effects idempotent. DBOS-internal writes (`Send`/`SetEvent`/`WriteStream`/`CloseStream`) share one transaction with their checkpoint via `runAsTxn`, so they are genuinely atomic.

**Idempotency via workflow IDs.** Start is `INSERT ... ON CONFLICT (workflow_uuid) DO UPDATE`; first committer owns execution, a second same-ID call gets a polling handle. Same ID + different fn/input → `ConflictingWorkflowError` (surfaced); benign same-ID race → `ConflictingIDError` (silently resolved by awaiting the existing run). Child IDs are derived deterministically so replay returns a handle rather than launching twice.

**The determinism contract (the one thing to internalize).** Step identity is *positional* — the Nth DBOS primitive call gets `function_id = N`. On every execution of the same workflow ID the body must issue the same sequence of `RunAsStep`/child `RunWorkflow`/`Send`/`Recv`/`SetEvent`/`GetEvent`/`Sleep`/`WriteStream`/`Go`/`Select` calls in the same order. Don't call `time.Now()`, randomness, or network directly in the body (wrap in steps); don't branch the *number/order* of DBOS calls on non-replayable state. You may branch freely on step outputs. Enforcement is **active**: a recorded-vs-current `function_name` mismatch raises `UnexpectedStep` ("Is your workflow deterministic?", `system_database.go:2283`); `isWithinStep` blocks DBOS primitives called from inside a step body.

**Failure modes** (all `DBOSError` with a typed code, matched via `errors.Is`): `WorkflowCancelled` (observed at step boundaries; in-flight steps not interrupted), `DeadLetterQueueError` (whole workflow exceeded `maxRetries+1` recovery attempts) vs `MaxStepRetriesExceeded` (single step), `ConflictingIDError`/`ConflictingWorkflowError`/`ConflictingRegistrationError`, `UnexpectedStep`, `QueueDeduplicated`, `TimeoutError`, etc. Treat `StepExecutionError` as potentially transient; treat dead-letter as terminal.

**What is / isn't guaranteed**

| Property | Guaranteed? |
|---|---|
| Step result recorded once, never re-executed on replay | Yes |
| Step *body* runs at-least-once (may re-run mid-side-effect after crash) | Yes — make external effects idempotent |
| DBOS-internal writes atomic with their checkpoint | Yes (single txn) |
| One execution per workflow ID at a time; idempotent re-start | Yes |
| Atomic at-least-once dequeue; fleet-wide concurrency & rate limits | Yes |
| Dedup: at most one enqueued/pending per (queue, key) | Yes |
| Durable timeouts/sleep/`Recv` survive restarts | Yes |
| Workflow-body determinism | Developer's responsibility, **runtime-enforced** |
| Exactly-once *external* side effects for non-idempotent ops | **No** — at-least-once at the boundary |
| New deploy replays old-version in-flight work | No — version-scoped; **pin `DBOS__APPVERSION`** |

---

## Architectural Design Principles That Enable the Semantics Maintainably

The through-line is **invariant minimization**: collapse the problem onto one substrate (Postgres) and derive every guarantee from a small set of DB constraints.

1. **Postgres as single source of truth.** The row *is* the workflow, queue, schedule, and inbox. Every durable decision is one SQL statement (`ON CONFLICT`, CTEs, `FOR UPDATE`) — no external orchestrator/broker/lock service.
2. **Checkpoint store + transactional exactly-once.** Two tables, one pattern: `workflow_status` (one row/workflow) + append-only `operation_outputs` (`(workflow_uuid, function_id)`). A check-then-record pair where the PRIMARY KEY makes a concurrent second writer a caught no-op. Correctness reduces to a primary key, a unique index, and transaction boundaries.
3. **The dialect-abstraction seam.** All backend-specific SQL is behind one `Dialect` interface; queries are written once (`$N` + `%s` schema slots) and `renderSQL` rewrites for SQLite. Only genuine divergences are isolated (row locking, `LISTEN/NOTIFY`, arrays, CTEs). CockroachDB is a Postgres sub-dialect overriding only `Name()`/`SupportsListenNotify()`.
4. **Executor / control-plane separation.** Only the executor runs workflow code; conductor, admin server, and `Client` all delegate to the same `systemDB` methods — no duplicated SQL, no "the dashboard does it differently" bugs.
5. **Versioned migrations as a self-contained artifact.** `//go:embed`ed numbered list (35+) applied at startup; online index changes use `CREATE/DROP INDEX CONCURRENTLY` with crash-recovery cleanup. Features land as additive, ordered, reviewable migrations.
6. **Application-version gating for safe recovery across deploys.** `recoverPendingWorkflows` filters by `executor_id` *and* `application_version` (default SHA-256 of the binary), answering "what happens to in-flight workflows when code changes?" with a versioning invariant.
7. **Serialization boundaries.** One pluggable `Serializer[T]`; the format name is stored per record, so one system DB can mix Go-native and portable-JSON workflows. A `__DBOS_NIL` sentinel distinguishes "ran, returned nil" from "not yet run."
8. **Composition on one substrate.** Queues, schedules, debouncers, notifications, events, streams, sleep, child workflows are all built on `workflow_status` + `operation_outputs` — **one durability mechanism, not eight.** Each new primitive is a thin composition that inherits crash safety, exactly-once, observability, and recovery, avoiding the combinatorial explosion of "add another coordination feature."

**Known tensions (honest caveats from the source reading, not confirmed defects):** `owner_xid` is a Go-generated UUID, not `pg_current_xact_id()`, so it doesn't track transaction visibility; `checkOperationExecution` runs at READ COMMITTED, leaving a theoretical TOCTOU window between the cancellation check and the output read; SQLite's effective write parallelism is 1 (`_txlock=immediate` serializes writers despite an 8-connection pool).

---

## How DBOS Differs From "Simply Using a Database"

DBOS does not store **data**; it stores **execution**. A raw DB persists the rows you hand it. DBOS persists the *position of your program counter* across crashes and replays your ordinary Go code so it resumes where it left off.

| You want | With a raw DB you write… | DBOS gives you… |
|---|---|---|
| Multi-step process state | A bespoke state table + state machine (`charge_done`, `email_sent` flags) | `RunAsStep` checkpoints automatically; the flags never need to exist |
| Crash recovery | A startup scan + reconstruct-in-memory + re-drive routine | Re-execute from the top; recovery path *is* the normal code path |
| Exactly-once side effects | Idempotency keys + check-then-act + two-process race reasoning | `(workflow_uuid, function_id)` PK + check/record; first writer wins |
| Background jobs | A `jobs` table + pollers + separate concurrency/rate/priority | `workflow_status` *is* the queue; atomic dequeue w/ globally-consistent limits |
| Cron surviving downtime | Cron process + replica dedup + missed-tick handling | Content-addressed tick IDs + dedup index + `backfillSchedule` |
| Messaging between processes | Messages table + poll-state + consume-then-process race | Durable `Send`/`Recv`; consume + record step in one transaction |
| Give-up / dead-letter | Retry counter + poison-job parking | `recovery_attempts` → `MAX_RECOVERY_ATTEMPTS_EXCEEDED` atomically |
| Single-executor ownership | Lock table or lease/heartbeat with TTL | `owner_xid` upsert + `RETURNING`; dead executors just stop writing |
| Operator tooling | Raw SQL or a custom admin app + DB creds | `Client` + admin HTTP + outbound conductor, same SQL underneath |

Beyond the table: `ForkWorkflow` is a multi-table transactional snapshot giving a deterministic replay prefix; `Go`/`Select` give durable replay-safe goroutine fan-out; durable timeouts reload from the row on recovery; `Patch`/`DeprecatePatch` let a long-running workflow branch on whether it has passed a code-change point — all impossible with plain DB ops. A raw database is a **passive store of data**; DBOS turns that same Postgres into an **active execution engine** where durability, exactly-once, queues, scheduling, messaging, dead-lettering, and remote management are properties of the data model itself.

---

## Key Takeaways

- **One substrate, one mechanism.** Every guarantee reduces to a DB constraint (a PK for exactly-once recording, a partial unique index for dedup, an `owner_xid` round-trip for single ownership, a version filter for safe recovery).
- **Recovery is re-execution with memoization,** not continuation serialization.
- **Exactly-once is precise:** step *bodies* run at-least-once, step *results* exactly-once — make external operations idempotent.
- **Determinism is the developer's one obligation,** actively enforced via `UnexpectedStep` and `isWithinStep`.
- **Everything composes on the same checkpoint machinery,** so new primitives inherit crash safety for free — the key to maintainability.
- **Operate it from anywhere** (in-process handle, `Client`, admin HTTP, outbound conductor) — but mind the caveats: admin server has no built-in auth, and `/dbos-garbage-collect` is a no-op stub.
- **Deploys are safe by default** via version-scoped recovery — but pin `DBOS__APPVERSION` in CI/CD, since the default binary-hash version fragments recovery groups across rebuilds.

---

The finalizer's source-verification (with file:line citations) covers the dedup index, the `maxRetries+1` dead-letter threshold, idempotent-start + `owner_xid`, the `UnexpectedStep`/`isWithinStep` guards, queue locking, and `ConflictingIDError` auto-resolution. The "known tensions" are flagged as open questions from the survey, not confirmed bugs.
