# Lifecycle write lane — measurement record

Status: phase 2 landed (daemon-fronted, UDS-only, ONE transaction per lane turn
via Provenance v0.9.0's batched apply).
Subject: PROPOSAL-19, `https://github.com/dayvidpham/pasture--01a13c08-4b57-7bd4-b25d-2f750b9ed6d9`.

## Rule 1 discipline

Every number below names WHEN it was taken, the REVISION it was taken at, and
the exact COMMAND. Numbers that are derived rather than measured are labelled
**DERIVED**. No estimate is presented as a measurement.

## Baseline (direct path), measured

Taken 2026-10-14 by the PROPOSAL-19 author. REVISION: `origin/main` merge
`1b5c7b0`, worktree `pasture-205--chore--pin-provenance-v053` at `bc41181`,
provenance v0.5.3, built with the Nix Go 1.26.3 shell. HARNESS:
`/tmp/opencode/lifecycle-wc/measure.sh`, a sandbox store bootstrapped with
`pasture task create` (never the live store).

- Serial, no external load: per-invocation p50 97 ms, p95 102 ms, max 109 ms,
  0 faults in 20 runs.
- C = 18 concurrent hook workers + a serial probe, 15 s: probe 15 runs,
  **12 faults (80%)**, p50 517 ms, **p95 2,003 ms**, max 3,593 ms; fault record
  +112 lines in 15 s; committed generation delta ≈ 113 events ≈ 7.5 events/s.
  Fault classes: 64 blob-transaction `BEGIN` failures (`SQLITE_BUSY`), 46
  journal-append writer-wait exhaustion, 2 hook-invocation deadline
  abandonments.

## Acceptance row (daemon lane), measured

Taken 2026-10-14 by the phase-1 implementer, on the MERGED tree (the URI
cutover is in). REVISION: branch `pasture-01a13c08--feat--daemon-write-lane`
merged with `origin/main` `d78d02b` (the URI cutover, PR #204), provenance
v0.5.3. COMMAND:

    CGO_ENABLED=1 go test -count=1 -v -run TestEighteenConcurrent ./cmd/pastured/

The test builds both binaries with `go build` (CGO_ENABLED=0), bootstraps a
fresh sandbox store, starts the real `pastured` on it, waits for its
`lifecycle write lane listening` line, then runs 18 concurrent
`pasture hook lifecycle --harness claude-code --event SessionStart
--host-version 2.1.261 --db <sandbox>` invocations and a serial probe for 3 s,
counting lines appended to `lifecycle-faults.jsonl` beside the sandbox store.

Observed (this machine, four runs on the merged tree):

| run | window | probe runs | probe p95 | faults/s | appended faults |
|---|---|---|---|---|---|
| 1 | 3.002 s | 87 | 34.9 ms | 0.000 | 0 |
| 2 | 3.030 s | 80 | 52.0 ms | 0.000 | 0 |
| 3 | 3.027 s | 83 | 43.9 ms | 0.000 | 0 |
| 4 | 3.004 s | 72 | 51.7 ms | 0.000 | 0 |

Re-measured after the final accounting fix (`writelane` publishes a record's
counter before its acknowledgement), three further runs on the final tree:

| run | window | probe runs | probe p95 | faults/s | appended faults |
|---|---|---|---|---|---|
| 5 | 3.012 s | 88 | 34.5 ms | 0.000 | 0 |
| 6 | 3.014 s | 88 | 34.3 ms | 0.000 | 0 |
| 7 | 3.031 s | 88 | 34.6 ms | 0.000 | 0 |

The PRE-MERGE run on the same harness (branch tip `3cd7f648`, before the URI
cutover merged) measured `probeRuns=89 probeP95=34.5 ms appendedFaults=0`. It is
recorded here only as the pre-merge comparison; the merged numbers above are the
ones that describe the tree.

Baseline for comparison (direct path, PROPOSAL-19, recorded 2026-10-14 at
`1b5c7b0`): at C = 18 the serial probe faulted 12 of 15 runs (80%), p95
2,003 ms, and the fault record gained 112 lines in 15 s — about **7.47
faults/s**, with committed throughput ≈ 7.5 events/s.

Against that baseline the acceptance target of **faults/s = 0 and probe
p95 ≤ 250 ms** is met on every run. This is ONE machine at ONE concurrency for
a few seconds; it is not a scaling study and it does not replace the sandbox
harness above.

## Phase 2 — one transaction per lane turn, measured

Taken 2026-10-14 by the phase-2 implementer. REVISION: branch
`pasture-01a13c08--feat--batched-lane-commit` off `origin/main` merge `d9c8b0c`,
provenance **v0.9.0** (the batched `BatchJournal`), built with the Nix Go 1.26.3
shell. The lane now prepares every record's canonical operation, commits the
whole turn's operations in ONE `ApplyBatchContext`, and maps each record's own
outcome back (`writelane.Report`).

### Acceptance row (same harness as phase 1), measured

COMMAND:

    CGO_ENABLED=1 go test -count=1 -v -run TestEighteenConcurrent ./cmd/pastured/

| run | window | probe runs | probe p95 | faults/s | appended faults |
|---|---|---|---|---|---|
| 1 | 3.004 s | 86 | 35.8 ms | 0.000 | 0 |
| 2 | 3.028 s | 87 | 34.8 ms | 0.000 | 0 |
| 3 | 3.018 s | 87 | 34.5 ms | 0.000 | 0 |

Baseline for comparison (direct path, recorded at `1b5c7b0`): 7.47 faults/s and
probe p95 2,003 ms. The acceptance target (faults/s = 0, probe p95 ≤ 250 ms) is
met on every run, as it was in phase 1.

### Throughput, measured

COMMAND:

    bash /tmp/opencode/lane-throughput/measure.sh <label> <tree-root> \
        <pastured> <pasture> 10 <workers>

The harness bootstraps a fresh sandbox store, starts the real `pastured` with
`PASTURE_WRITE_LANE=auto`, waits for the `lifecycle write lane listening` line,
runs `<workers>` concurrent `pasture hook lifecycle --harness claude-code
--event SessionStart --host-version 2.1.261 --db <sandbox>` workers plus a
serial probe for 10 s, and counts committed rows in `journal_evidence` with
`evidence_kind = 'pasture.lifecycle.occurrence.v1'`.

| tree | workers | window | committed events | events/s | faults/s | probe p95 |
|---|---|---|---|---|---|---|
| phase 1 (per-record) | 18 | 9.934 s | 1,392 | 140.1 | 0.000 | 180 ms |
| phase 1 (per-record) | 18 | 10.496 s | 1,471 | 140.2 | 0.000 | 189 ms |
| phase 1 (per-record) | 18 | 10.708 s | 1,497 | 139.8 | 0.000 | 191 ms |
| phase 1 (per-record) | 36 | 10.578 s | 1,453 | 137.4 | 0.000 | 365 ms |
| phase 1 (per-record) | 54 | 10.827 s | 1,452 | 134.1 | 0.000 | 513 ms |
| phase 2 (batched) | 18 | 10.656 s | 2,742 | 257.3 | 0.000 | 84 ms |
| phase 2 (batched) | 18 | 10.200 s | 2,484 | 243.5 | 0.000 | 88 ms |
| phase 2 (batched) | 18 | 10.725 s | 2,708 | 252.5 | 0.000 | 86 ms |
| phase 2 (batched) | 36 | 10.107 s | 2,444 | 241.8 | 0.000 | 181 ms |
| phase 2 (batched) | 54 | 10.688 s | 2,657 | 248.6 | 0.000 | 260 ms |

What this shows, stated carefully:

- The PER-RECORD lane SATURATES at about **135–140 events/s** on this harness:
  raising the offered concurrency from 18 to 36 to 54 does not move the committed
  rate, it only moves the probe latency (180 ms → 365 ms → 513 ms). That is the
  per-record commit ceiling.
- The BATCHED lane sustains about **244–257 events/s at C = 18**, roughly
  **1.8×** the per-record lane, and it does NOT saturate at C = 18: at C = 36 and
  C = 54 the committed rate stays ~242–249/s while the probe p95 rises. So on
  this harness the committed rate is bounded by host process spawn and CPU, not
  by the lane; ~250 events/s is a FLOOR on the batched lane's capacity, not its
  ceiling.
- The probe p95 at C = 18 fell from ~180–191 ms (per-record) to ~84–88 ms
  (batched).

An UNREPRODUCED figure is recorded rather than repeated: the phase-2 task
brief described the per-record lane as capped at "~10 events/s". On THIS
harness, with THIS 484-byte session-start fixture and a tiny sandbox store, the
per-record lane committed ~137–140 events/s, so ~10/s did not reproduce here. It
may describe the direct path (~7.5 events/s, recorded above) or a much larger
store; it is not claimed as a measurement of this tree.

## Lane budget derivation (DERIVED)

`writelane.BudgetFromProfile` derives every lane bound from the validated
timeout profile, so the lane joins the existing tier hierarchy instead of
carrying a second set of durations.

| Bound | Production value | Derivation |
|---|---|---|
| `HoldTarget` | 400 ms | 4/5 of the SQLite busy tier (500 ms), capped at 400 ms |
| `BatchRecords` | 20 | `HoldTarget / assumedPerRecordCommitCost` |
| `BatchBytes` | 64 MiB | 64 × the 1 MiB ingress payload cap, above a count-legal turn of maximum-size records (20 × 1 MiB). The previous 8 MiB start split that turn after eight records. |
| `QueueRecords` | 256 | staging depth; deeper than one turn |
| `QueueBytes` | 256 MiB | 4 × `BatchBytes` (was 32 MiB when `BatchBytes` was 8 MiB) |
| `FlushInterval` | 25 ms | the design's stated start, capped at `HoldTarget/4` |

`assumedPerRecordCommitCost` (20 ms) is **DERIVED, NOT MEASURED**, and phase 2
keeps it as the splitter's conservative per-record allowance rather than
replacing it. A lane turn is now ONE transaction, so the per-record cost is no
longer the turn's cost; what the count must still bound is the turn's hold on
the writer. The pinned Provenance v0.9.0 release records a batched per-record
fold cost of 11.64 ms (serial p50, its own sandbox), so 20 records per turn is
~233 ms, inside `HoldTarget` (400 ms). The phase-2 measurement above is the
check that the bound is not the bottleneck: the batched lane did not saturate at
C = 18, 36 or 54.

`HoldTarget` is the one bound with an external consequence: it is what keeps
the lane from starving the engine's and the CLI's own writers past their busy
window. It is validated at construction (`WriteLaneBudget.Validate`) and
`BudgetFromProfile` refuses a profile whose busy tier cannot contain it.
`Validate` also refuses a `BatchRecords` above Provenance's `MaxBatchRecords`
(256), because one turn is one `ApplyBatchContext` call and a larger turn would
be refused by the journal.

## What is NOT measured here

- The store-size scaling of the open/verification path (PROPOSAL-19 bands it
  with the two live report numbers; not re-measured).
- Windows: there is no Unix-domain-socket transport, so Windows runs
  direct-only. That is a stated scope boundary, not a measurement.
- The batched lane's CEILING: the harness above is process-spawn-bound, so the
  ~250 events/s it sustains is a floor. The measured per-record saturation
  (~137/s) and the 1.8× ratio are the comparison; the absolute batched ceiling
  is not measured.
