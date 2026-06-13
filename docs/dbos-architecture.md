# Pasture Durable-Execution Architecture (DBOS substrate)

> **Status: implementation in progress.** This document describes pasture's
> durable-execution substrate after migrating off Temporal onto **DBOS Transact
> (Go + SQLite)**. It is the durable reference for the design decisions and
> integration surfaces; the blow-by-blow planning history lives in the parent repo's
> `docs/proposals/PROPOSAL-{1..5}-dbos-substrate-migration.md`.

## 1. What changes, in one paragraph

Pasture's orchestrator previously ran the 12-phase epoch workflow on **Temporal**,
which required a separate Temporal **server process** plus a Temporal-flavoured
`pastured` worker. This migration replaces that with **DBOS Transact** — an
embedded durable-execution library (Go SDK, SQLite backend) that runs in the
Pasture process with **no external workflow server**. The 12-phase state machine
itself is unchanged; only the wrapper that drives it durably is swapped. The
`pastured` binary remains as the long-running DBOS engine host for recovery,
queue dispatch, hooks, and background epoch work.

## 2. Why (the core decision)

| | Temporal (before) | DBOS Transact (after) |
|---|---|---|
| Durable execution | Yes (retry/replay, signals) | Yes (durable steps, automatic recovery) |
| Operational weight | **Temporal server process + Temporal worker** | **No external workflow server; `pastured` hosts DBOS for background work** |
| Storage | Temporal's store (its dev server on SQLite) | **The same `pasture.db`** (modernc SQLite) |
| Build | CGO-free already | CGO-free preserved (`modernc.org/sqlite`) |
| Static binaries | Undermined by the server requirement | **Preserved** (`pasture` CLI and `pastured` host) |

The decision space evaluated three substrates: keep Temporal, **DBOS Transact (chosen)**,
or a plain state-machine-over-SQLite with no framework. DBOS was chosen because the
orchestrator genuinely needs **automatic crash recovery** (resume an in-flight epoch
after a `kill -9`), which the plain-SQLite option does not provide, while still removing
the external workflow server that made Temporal operationally heavy. DBOS is
**pre-1.0 (v0.16.0)**; that risk
is de-risked by a kill-9 resume spike that becomes a permanent regression test, and the
version is pinned.

## 3. Layered structure

```
┌──────────────────────────────────────────────────────────────────────┐
│  pkg/protocol  (public, PURE Go — no substrate dependency)             │
│    • EpochStateMachine  (the 12-phase logic; unchanged by this work)   │
│    • signal/query name constants + payload types                       │
│    • EpochState (the projection serialized for queries / status)       │
└───────────────▲──────────────────────────────────────────────────────┘
                │ driven by
┌───────────────┴──────────────────────────────────────────────────────┐
│  internal/engine  (impure — the DBOS adapter)                          │
│    • owns the DBOSContext (RegisterWorkflow / Launch / Shutdown)       │
│    • EpochWorkflow: durable steps that call EpochStateMachine.Advance  │
│    • persists the EpochState projection on every transition            │
│    • idempotent activity emission (deterministic UUIDv5)               │
└───────────────▲───────────────────────────────▲──────────────────────┘
                │ database/sql (modernc)         │ DBOS owns its tables here
┌───────────────┴────────────────────────────────┴─────────────────────┐
│  pasture.db  (ONE SQLite file, opened by TWO drivers via WAL)          │
│    • modernc.org/sqlite  → DBOS tables, audit_events, pasture tables   │
│    • zombiezen.com/go/sqlite → Provenance tables (tasks, activities…)  │
└───────────────────────────────────────────────────────────────────────┘
```

The key structural fact, already true before this work: **`EpochStateMachine` is pure
Go** (no substrate import). Temporal was only ever a wrapper around it; DBOS replaces that
wrapper. The state machine moves from `internal/temporal/` to `pkg/protocol` (a package
rename of a substrate-free file), and `internal/temporal/` is deleted.

## 4. Design decisions

| # | Decision | Rationale |
|---|----------|-----------|
| **D1** | Substrate = **DBOS Transact (Go + SQLite)** | Embedded durable execution + automatic recovery in a single binary; only embedded option meeting the auto-recovery requirement |
| **D2** | A kill-9 resume **spike** runs first, fix-forward | DBOS is pre-1.0; prove crash-recovery on SQLite before building on it. The spike ships as a permanent `//go:build recovery` test |
| **D3** | **Big-bang** swap; **fold `pasture-msg` into `pasture`**, retire it | The wrapper is ~8 files around an unchanged engine; one coordinated swap. One binary instead of two |
| **D4** | Define the **Provenance↔engine boundary** in `pasture.db` | DBOS and Provenance share one file; table ownership must be explicit to avoid collision |
| **D5** (amended) | **Provenance library may be modified — additively only** | Enables native exactly-once for `activities` (see §7) without the broader bd→`pasture task` integration, which stays in the next epoch |
| **D6** | Add a live **`pasture status`** surface | Replaces Temporal's web UI for single-machine observability |
| **Recovery pinning** | Pin `ExecutorID` + `ApplicationVersion` | DBOS recovery is filtered by app-version, which defaults to a **binary hash** — a rebuilt binary would otherwise skip recovery of an in-flight epoch |
| **Concurrency** | **WAL multi-writer**, pasture sets PRAGMAs as **DSN params** | A custom SQLite handle bypasses DBOS's own PRAGMA setup; matches pasture's existing race-tested WAL model |

## 5. Integration surfaces

### 5.1 DBOS API (what `internal/engine` consumes)

- **Lifecycle:** `NewDBOSContext(ctx, Config) → RegisterWorkflow(...) → Launch(ctx) → … → Shutdown(ctx, timeout)`.
- **Config:** `SqliteSystemDB *sql.DB` — pasture passes its **own modernc handle** on `pasture.db`, so DBOS's durable tables live in the same file as audit + (via the other driver) Provenance.
- **Durable work:** `RunAsStep(ctx, fn)` — DBOS memoizes the step's return value; a completed step is not re-run on recovery. (A step body's *external* writes are not transactional with the checkpoint — see §7.)
- **Signals/events:** `Send`/`Recv` (durable messages by topic), `SetEvent`/`GetEvent`.
- **Lifecycle control:** `RunWorkflow`, `CancelWorkflow`, `ResumeWorkflow`.
- **Visibility:** `ListWorkflows`, `GetWorkflowSteps`.
- **Recovery:** automatic at `Launch()` for the local executor, filtered by `ExecutorID` + `ApplicationVersion` (both pinned).

### 5.2 `pasture.db` table ownership (the D4 boundary)

One file, three owners; the boundary is enforced by reserving names and a test that
introspects `sqlite_master` after `Launch()`.

| Owner | Driver | Tables |
|-------|--------|--------|
| **DBOS** (reserved) | modernc | `workflow_status, operation_outputs, notifications, workflow_events, workflow_events_history, streams, event_dispatch_kv, queues, workflow_schedules, application_versions, dbos_migrations` |
| **Provenance** | zombiezen | `tasks, context_edges, agents, agents_software, agent_kinds, activities, session_entries, comments, labels` |
| **Audit / pasture** | modernc | `audit_events, audit_schema_meta` |

> **The DBOS table set is empirical, not a magic number.** The list above is the
> v0.16.0 set as documented; a Phase-8 reading of the migrations counted **10** created
> on the SQLite path (`workflow_events_history` may be Postgres-only / created lazily).
> The reserved set is the substrate's contract — it can shift across DBOS versions and
> backends. The boundary test therefore enumerates whatever DBOS created by
> introspecting `sqlite_master` after `Launch()` and asserts that set is **disjoint**
> from the Provenance and Audit sets — it never asserts a hardcoded count.
>
> **Observed v0.16.0 set (modernc/SQLite path).** Derived empirically by the
> boundary test (`internal/engine/boundary_test.go`) as the tables that appear
> only after `Launch()`, minus the one pasture-owned table the engine adds
> (`epoch_state_projection`). On the SQLite path, DBOS v0.16.0 creates **11**
> tables — `workflow_events_history` *is* created here, contrary to the Phase-8
> migration-reading guess of 10:
>
> ```
> application_versions   dbos_migrations        event_dispatch_kv
> notifications          operation_outputs      queues
> streams                workflow_events        workflow_events_history
> workflow_schedules     workflow_status
> ```
>
> This is recorded as the observation as of v0.16.0, not as an assertion: the
> test pins the *boundary* (disjoint from + co-present with the pasture/Provenance
> sets), so a future DBOS version that shifts this set does not break it.

**Two-driver reality:** `pasture.db` is opened by **two different pure-Go SQLite
libraries** — pasture/DBOS use `modernc.org/sqlite` (standard `database/sql`),
Provenance uses `zombiezen.com/go/sqlite` (its own API). Both are CGO-free. They
coexist safely at the **file** level via **WAL** but cannot share a **transaction**
(different connection objects). This is why exactly-once is achieved per-connection
with deterministic keys, never with a cross-connection transaction (§7).

### 5.3 Signals, lifecycle, and queries (the control surface; folds in `pasture-msg`)

`pasture-msg`'s verbs move into the `pasture` CLI:

| Verb | DBOS mapping |
|------|--------------|
| `submit_vote`, `advance_phase`, `slice_progress`, `register_session`, `start_slice`, `complete_slice` | `Send` / `Recv` (one topic per signal name) |
| `epoch start` | DBOS client `Enqueue` of `pasture.epoch_control.v1` onto `pasture-control-queue` with workflow ID = epoch ID |
| `epoch cancel` / `terminate` | DBOS client `CancelWorkflow` (`terminate` records the audit cancellation event first) |
| queries: `current_state`, `available_transitions`, `full_state`, `slice_progress_state`, `active_sessions` | **SQL read** of the persisted `EpochState` projection + recompute transitions via the FSM |

**Slice/review dispatch:** worker-slice and review sub-workflows are dispatched via DBOS
**`Queue`/`Enqueue` with a configurable concurrency limit K** (not unbounded direct
spawning). This gives bounded concurrency + durable backpressure when fanning out to
many parallel agents — essential because the single `pasture.db` file is a single-writer
WAL bottleneck, so 30+ unbounded concurrent sub-workflows would thrash one connection.
K is surfaced as configuration and tuned to SQLite throughput.

Queries are *not* workflow round-trips: the engine persists a serialization of
`EpochState` on every transition, and queries (and `pasture status`) read that
projection. Cross-workflow filtering = `ListWorkflows` (fixed fields) + SQL over
`pasture.db`; the workflow ID **is** the epoch ID.

### 5.4 Provenance integration surface (additive — D5)

The only change to the Provenance library is **one additive, backward-compatible method**:

```go
// existing (unchanged): generates a random UUIDv7 id — correct for ordinary use.
StartActivity(agentID, phase, stage, notes string) (Activity, error)

// new: caller supplies a DETERMINISTIC id; idempotent insert.
StartActivityWithID(id ActivityID, agentID, phase, stage, notes string) (Activity, error)
//   internal: INSERT INTO activities (id, …) VALUES (…) ON CONFLICT(id) DO NOTHING; then SELECT.
```

`EndActivity(id)` needs **no** new variant: it is already `UPDATE … SET ended_at WHERE id=?`,
keyed by the id the caller supplies, so calling it with the same deterministic id is
idempotent (one row). A symmetric `EndActivityWithID` would be a byte-identical duplicate
and is deliberately not added.

No `activities` schema change (`id` is already `TEXT PRIMARY KEY`); it matches
Provenance's own `INSERT OR IGNORE` idiom (`edges`, `labels`). The engine supplies a
**deterministic UUIDv5** id = `DedupKey(epochID, phase, activity_kind, stepSeq)` where
`stepSeq = dbos.GetStepID(ctx)` (AMENDMENT-3, positional). The namespace constant and
encoder are pinned once in `pkg/protocol` so the engine and tests compute byte-identical
ids, and DBOS re-derives the same step ordinal on replay (hence the same id).

> **Why UUIDv5, not v7:** v7 is random — a crash-replay would mint a *different* id and
> duplicate the row. v5 is **derived** from the activity's logical identity, so a replay
> produces the *same* id → `ON CONFLICT DO NOTHING` collapses it → exactly one row.

`activities.agent_id` is a NOT-NULL FK to `agents(id)` (random UUIDv7); the engine must
resolve a **stable** agent id and ensure the agent row exists before the idempotent insert.

### 5.5 Observability (`pasture status`)

`pasture status [--epoch <id>]` reads the `EpochState` projection + recent `audit_events`
to report current phase, available transitions, slice progress, and active sessions —
the single-machine replacement for Temporal's web UI. (`AdminServer`, DBOS's optional
local HTTP surface, is available but not enabled by default.)

## 6. Durability & recovery model

- **Crash recovery:** `Launch()` resumes in-flight epochs from the last completed
  durable step, exactly-once for the step's recorded result. Requires the pinned
  `ExecutorID` + `ApplicationVersion` (so a rebuilt binary still recovers).
- **Manual recovery:** `ResumeWorkflow(id)` for cross-version cases.
- **Concurrency:** WAL multi-writer; pasture opens the shared handle with DSN-param
  `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`, `_txlock=immediate`,
  `foreign_keys=ON` (DBOS skips PRAGMA setup on a caller-supplied handle).

## 7. Exactly-once model (per tier)

A DBOS step body's *external* DB writes re-run if the process crashes after the write
but before DBOS records the step done (there is no public transactional-step API).
Exactly-once for an external row is therefore achieved by a **deterministic idempotency
key on the same connection as the write** — never a cross-connection transaction.

| Tier | Mechanism | Guarantee |
|------|-----------|-----------|
| Orchestration state (phases, votes) | DBOS's own tables (`workflow_status`, `operation_outputs`) | exactly-once, native |
| `audit_events` (pasture, modernc) | new `dedup_key TEXT` column + **partial unique index** `… WHERE dedup_key IS NOT NULL` holding the **same deterministic UUIDv5**; `INSERT … ON CONFLICT(dedup_key) DO NOTHING` | exactly-once |
| `activities` (Provenance, zombiezen) | `StartActivityWithID` + deterministic UUIDv5 id (reuses existing TEXT PK) + `ON CONFLICT(id) DO NOTHING` | exactly-once |

Each is a single idempotent statement on its own connection, so a crash-replay yields
exactly one row in every tier. **Both** forensic tables key off the **identical
mechanism** — one pinned **positional** encoder in `pkg/protocol` (AMENDMENT-3):
`DedupKey(epochID, phase, kind, stepSeq) → UUIDv5(ns, "<epoch>/<phase>/<kind>/<step_seq>")`,
where `stepSeq = dbos.GetStepID(ctx)` (DBOS re-derives the same step ordinal on replay).
Each tier passes its own **distinctly-namespaced** discriminator as `kind` (audit →
`event_type`; activities → a namespaced `activity_kind`, e.g. `"activity:…"`), so the two
tiers produce **distinct id values** for the same transition — they are different entities
(a system event record vs a PROV-O agent Activity, with different agent attribution) and
must not share a primary identity by construction. (Cross-tier correlation, if needed, is
via the shared `(epoch, phase, step)` coordinates, not id-equality.) They also differ in
**storage** (activities reuses its TEXT PK; audit
adds the sidecar `dedup_key` column because its PK is an autoincrement INTEGER).
- **Cross-epoch:** `epochID` is in the key → distinct epochs at the same `step_seq` get distinct keys.
- **Cyclic re-entries** (`p4→p3→p4`, `p10→p9→p10`) emit their two `PhaseTransition` rows at **different** `step_seq` → distinct keys → both survive, with no content analysis. (This is why the positional basis was restored over the semantic one — it handles cyclic repeats for free.)
- **One-emission-per-(kind, step) invariant:** the engine emits each forensic row in its **own** `RunAsStep`, so same-`kind` logically-simultaneous rows (e.g. 3 `VoteRecorded` for 3 reviewers) land at distinct `step_seq` → 3 rows. A test asserts a step never emits two same-`kind` rows.

> **Amendment (2026-06-09, Phase-8 IMPL_PLAN; hardened after a 3-reviewer re-review).** The `audit_events` mechanism was
> originally specified as `UNIQUE(epoch_id, phase, event_type, step_seq)`. That key is
> **not constructible on today's schema**: `audit_events.epoch_id` was dropped in the
> v3→v4 table-rebuild (`internal/audit/migrate_v3_v4.go::rebuildAuditEventsWithoutEpochId`; the epoch lives in
> `context_edges` as `EpochContext` edges, joined by `event_id`). `step_seq` alone
> (`dbos.GetStepID`) resets per-workflow, so a tuple without an epoch scope would
> false-dedup across epochs. Resolution: add a `dedup_key TEXT` column holding the
> **same UUIDv5** the `activities` tier already uses (the epoch is hashed *into* the
> id, so no `epoch_id` column is needed, and v4's table shape is untouched).
> **Realization:** land it as a versioned **v4→v5 migration** (`migrate_v4_v5.go`:
> `ALTER TABLE audit_events ADD COLUMN dedup_key TEXT` then
> `CREATE UNIQUE INDEX idx_audit_events_dedup ON audit_events(dedup_key) WHERE dedup_key IS NOT NULL`;
> bump `MaxKnownSchemaVersion=5`) — **not** a column `UNIQUE` constraint, which SQLite's
> `ALTER TABLE ADD COLUMN` forbids and which would force a full table rebuild. The
> partial index is self-documenting (uniqueness applies only to engine-emitted rows).
> `dedup_key` is `NULL`
> for legacy/non-engine rows; the partial index ignores NULLs, so the column
> is additive and back-compatible.

> **Amendment-3 (2026-06-09, Plan UAT round 2 — user-directed) — ACTIVE.** AMENDMENT-2 (below)
> is **WITHDRAWN**. After seeing the full semantic-key design, the user reverted to the
> **positional `step_seq`** basis (*"This is quite crazy. Let's just do the step_seq."*): it is
> simpler and handles cyclic re-entries for free (distinct step ordinals), with epoch-in-key for
> cross-epoch and the one-emission-per-step discipline for same-`kind` multiples. The active §7
> mechanism above reflects this. AMENDMENT-2 is retained below as superseded history only.
>
> **Amendment-2 (2026-06-09, Plan UAT — WITHDRAWN by Amendment-3, retained as history).** The key basis (briefly) changed from the
> **positional** `step_seq` (`dbos.GetStepID`) to the row's **semantic natural key** (its
> content). Two reasons: (1) it removes the fragile "one-emission-per-step" requirement —
> several same-`event_type` rows in one step (e.g. 3 `VoteRecorded` by `reviewerId`) now get
> distinct keys by identity, not by being in separate steps; (2) **fail-safe** — an
> under-specified key silently *drops* a legitimate forensic row (data loss), the malign
> direction. So `DedupKey` hashes `(epoch, phase, kind, naturalKey)` where `naturalKey` is a
> canonical serialization of the row's identifying payload, built by
> `AuditNaturalKey`/`ActivityNaturalKey` in `pkg/protocol`.
>
> **Correction (after correctness re-review F1):** content alone is NOT sufficient. The FSM
> has **cyclic transitions** (`p4→p3→p4`, `p10→p9→p10`); a re-entry emits a `PhaseTransition`
> with byte-identical payload `{from,to,conditionMet}`, so a pure-content key would silently
> drop the second legitimate row. Therefore the identity of any **recurrent-within-epoch**
> event type also folds in a **replay-deterministic domain occurrence/round** (the review
> round / transition index from `EpochState` — a domain fact, *not* the rejected DBOS
> `step_seq`). Each emitted type is classified singleton-vs-recurrent at S4 with a
> same-content-recurrence test; default recurrent when unsure. Serialization is pinned to
> `encoding/json` Marshal (sorted keys; computed pre-insert from the in-memory event, never
> recomputed from a DB-read row; `Timestamp` excluded).

> **Why UUIDv5, not v7** (restated here because it's the crux): v7 embeds random bits,
> so a crash-replay of the emitting step would mint a *different* id and insert a
> duplicate. v5 is a pure SHA-1 of (namespace, name) — identical inputs always yield
> the identical id, so the replay collapses onto the same row via `ON CONFLICT`. The
> normal, non-replayed creation paths (`StartActivity`, task/agent ids) keep v7; only
> the durable-engine emission path uses v5.

## 8. Explicitly NOT in scope (this epoch)

- **No changes to skill bodies / agent definitions / any codegen output.** The
  bd→`pasture task` migration that would rewrite `skills/*/SKILL.md` belongs to the
  **next epoch** (Provenance integration). This epoch's Go refactors (e.g. relocating
  constants to `pkg/protocol`) do **not** regenerate or alter any `SKILL.md`.
- **No broader Provenance integration** beyond the single additive method above.
- **No distributed / multi-machine execution; no Temporal web-UI parity.**
- `Queue`/`Enqueue` **is used** for concurrency-limited slice/review dispatch (§5.3).
  `ForkWorkflow` is available but unused.

## 9. Cross-repo dependency

The additive Provenance method ships as a **Provenance release** that pasture's
`go.mod` then pins; the release lands **before** the slice that wires activity
recording. This is the one ordering dependency the design introduces, traded for a
permanently simpler runtime (no sidecar, symmetric idempotency across both forensic
tables).
```
github.com/dayvidpham/provenance  (add StartActivityWithID + test → release)
        ▲ go.mod bump
github.com/dayvidpham/pasture     (engine consumes the new method)
```
