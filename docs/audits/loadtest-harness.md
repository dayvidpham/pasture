# Disposable-store load-test harness — measurement record

Status: harness landed (`cmd/pastured/loadtest_test.go`,
`cmd/pastured/loadtest_guard_test.go`, `tools/loadtest.sh`). Subject: the
load-test epic
(`https://github.com/dayvidpham/pasture/01a13ee7-9b63-7cdc-951b-06cda2af55eb`).

## Rule 1 discipline

Every number below names WHEN it was taken, the REVISION it was taken at, and
the exact COMMAND. Numbers that are derived rather than measured are labelled
**DERIVED**. No estimate is presented as a measurement. Hypotheses about
causes are labelled HYPOTHESIS and banded as estimates, not measurements.

## The one command

```sh
bash tools/loadtest.sh                  # copy mode: disposable copy of the live store
bash tools/loadtest.sh --synthetic      # synthetic mode: 600-task built store (what CI runs)
```

The harness (`TestDisposableStoreLoadHarnessIsTheRegressionTripwire`) takes
the consistent copy into a temp sandbox, refuses any non-disposable path
(`requireDisposableDB`, unit-pinned in `loadtest_guard_test.go`), reports
read p50/p95, borrowed-open p50/p95, hook faults/second, hook p95 at C=18,
and sustained events/second, and fails loudly past the stated bands. CI
scaling: absolute bands x5 (upper) / /5 (floor) under GITHUB_ACTIONS, where
the lane row measured 4-5x runner variance; the burst-queue ratio and the
zero-fault band hold unscaled everywhere. Under `-race` the two in-process
bands (read, steady open) report without failing — the detector instruments
the measuring binary ~20x (measured 2026-10-16: open p95 6 ms -> 142 ms,
read p95 9 ms -> 138 ms) while the daemon and hooks run uninstrumented — so
`make test-race` still tripwires every child-process metric.

## Shape measured

Copy mode is a `sqlite3 .backup` of the operator's converted store
(`namespace-slash-uuid.v1` IDs: 596 tasks under
`https://github.com/dayvidpham/pasture`, 451 under humanoid, 12 legacy).
Synthetic mode builds 600 tasks through the production open path.

## Reference measurements

Taken 2026-10-16 by the harness author on the reference host.
REVISION: `origin/main` `59123e0` plus the uncommitted harness files
(the bands derive from these runs; the landing commit carries the harness).
COMMANDS: `bash tools/loadtest.sh` and `bash tools/loadtest.sh --synthetic`.

| mode | date (UTC) | shape | read p50/p95 | open cold / steady p50/p95 |
|---|---|---|---|---|
| copy | 2026-10-16T02:58:29Z | .backup, 94,359,552 B, 1059 tasks, schema 10 | 8.3 / 9.5 ms | 5.9 ms / 4.9 / 6.2 ms |
| synthetic | 2026-10-16T02:58:59Z | built 600 tasks, 1,642,496 B, schema 10 | 1.2 / 1.5 ms | 152 ms / 4.3 / 5.2 ms |

| mode | hook faults/s (burst+sustained) | serial served median | burst probe p95 (C=18) | queued ratio | sustained events/s |
|---|---|---|---|---|---|
| copy | 0.000, 0 sustained faults | ~308 ms | ~595 ms | 1.93 | 29.2 (308/308 offered) |
| synthetic | 0.000, 0 sustained faults | ~63 ms | ~72 ms | 1.14 | 111.4 (1134/1134 offered) |

Informational probes: copy-mode single direct hook FAULTED at the 5 s
hook-invocation deadline (open replay, see below); synthetic-mode single
direct hook served in ~1.7 s (replay fits in the deadline on a small store).

## Against the epic's recorded baselines, stated plainly

- read: epic says ~6,000 ms -> 27 ms. Measured 8-10 ms (copy) on MORE tasks
  (1059 vs 596). Faster than the recorded post-fix figure; different shape
  and a later tree, not a contradiction to resolve.
- borrowed open: epic says 5.56 s -> 2.2 ms. Measured steady ~5 ms,
  same order as the recorded post-fix figure. The ~5.5 s cold-replay first
  open seen on one run is the pre-fix class recurring as a cold cost, and
  the harness reports it separately rather than banding it.
- C=18 write: epic says 7.47 faults/s and p95 2,003 ms -> 0 faults/s and
  ~35 ms. Measured 0 faults/s on both shapes (the lane still never faults),
  but probe p95 ~595 ms (copy) / ~72 ms (synthetic), NOT ~35 ms. The
  ~35 ms figure was taken on a near-empty bootstrap store; on populated
  stores each served hook costs hundreds of ms (copy) or ~65 ms
  (synthetic), so the burst probe follows the per-hook cost, not the lane.
  Different shape, not a code regression: offered == committed in every
  sustained run, i.e. the lane is not saturated.
- throughput: epic says 140/s saturating -> ~250/s not saturating.
  Measured ~29/s (copy) / ~111/s (synthetic), both hook-cost-bound
  (offered == committed). Same shape story: per-hook served cost, not lane
  capacity, is the ceiling on populated stores.

## Findings the harness surfaced

1. The direct path cannot serve a large store. A stack dump of a stuck
   direct hook shows every open running Provenance's projection replay plus
   canonical re-encode of every journal mutation
   (`activationConvergenceVerified` -> `replayProjections` ->
   `verifyFactContextIntegrity` -> `DecodeCanonicalMutation` per row,
   27.5K journal rows here). On this shape the replay alone exceeds the 5 s
   hook deadline, so direct hooks fault and the daemon lane is REQUIRED.
   The harness keeps ONE informational direct probe as the witness and
   anchors its relative bound on the serial SERVED baseline instead.
2. Throughput on populated stores measures per-hook cost, not lane
   capacity. The sustained floor is therefore a collapse tripwire
   (~0/s), while lost batching is caught by the burst-queue bound
   (healthy 1-2x, collapse ~10x+, bound 5x — DERIVED from
   workers/batchRecords queueing arithmetic).
3. The live store was not measured. Evidence: the harness resolves its
   store under its own sandbox and `requireDisposableDB` refuses anything
   else (unit-pinned); the live `lifecycle-faults.jsonl` mtime predates
   the session (no harness hook ever recorded against live); the live
   `pasture.db` mtime moved only via the operator's own tracker opens
   (ordinary CLI reads checkpoint/housekeep) and daemon operation.

## Bands (in code, with derivation comments)

| metric | copy band | synthetic band | everywhere |
|---|---|---|---|
| read p95 | 250 ms | 100 ms | — |
| steady open p95 | 50 ms | 50 ms | — |
| hook faults/s | 0 | 0 | 0 |
| burst probe p95 | 1.5 s | 250 ms | — |
| burst queued ratio | — | — | <= 5.0 |
| sustained events/s floor | 10 | 80 | — |

## What is NOT measured here

- The batched lane's CEILING on a small store (phase 2 measured ~250/s
  spawn-bound; the synthetic shape here measures ~111/s hook-bound — both
  are floors under their harnesses, not ceilings of the lane).
- Per-hook served-cost breakdown (which part of the ~300-600 ms on the
  copy is gate read vs apply vs checkpoint is HYPOTHESIS, not measured).
- Windows, and any concurrency other than C=18.
