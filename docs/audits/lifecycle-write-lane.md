# Lifecycle write lane — measurement record

Status: phase 1 landed (daemon-fronted, UDS-only, per-record commits).
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

## Lane budget derivation (DERIVED)

`writelane.BudgetFromProfile` derives every lane bound from the validated
timeout profile, so the lane joins the existing tier hierarchy instead of
carrying a second set of durations.

| Bound | Production value | Derivation |
|---|---|---|
| `HoldTarget` | 400 ms | 4/5 of the SQLite busy tier (500 ms), capped at 400 ms |
| `BatchRecords` | 20 | `HoldTarget / assumedPerRecordCommitCost` |
| `BatchBytes` | 8 MiB | 8 × the 1 MiB ingress payload cap |
| `QueueRecords` | 256 | staging depth; deeper than one turn |
| `QueueBytes` | 32 MiB | 4 × `BatchBytes` |
| `FlushInterval` | 25 ms | the design's stated start, capped at `HoldTarget/4` |

`assumedPerRecordCommitCost` (20 ms) is **DERIVED, NOT MEASURED**. The only
recorded figure for this path is the serial per-invocation p50 of 97 ms above,
which covers process start, the store open and two write transactions, so the
in-transaction cost is strictly smaller and 20 ms errors the batch count
shallow. The phase-2 sandbox run that owns the batching change replaces it with
a measured per-record commit cost.

`HoldTarget` is the one bound with an external consequence: it is what keeps
the lane from starving the engine's and the CLI's own writers past their busy
window. It is validated at construction (`WriteLaneBudget.Validate`) and
`BudgetFromProfile` refuses a profile whose busy tier cannot contain it.

## What is NOT measured here

- The store-size scaling of the open/verification path (PROPOSAL-19 bands it
  with the two live report numbers; not re-measured).
- Windows: phase 1 has no Unix-domain-socket transport, so Windows runs
  direct-only. That is a stated scope boundary, not a measurement.
- Phase 2 (one commit per batch, needs a provenance batch API and a pin bump).
