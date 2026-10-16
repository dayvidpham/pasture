# Write-cost follow-up

## Historical failure attribution

The retained 2026-10-16 campaign at the profiling checkpoint is still valid for
its observed process times, completion counts and CPU stacks. Read its corrected
[field definitions](write-profiling-2026-10-16.md). Classification below comes
from retained process-error diagnostics and JSON fault causes, not latency.

| C=18 case | Mode | Offered | Committed | Genesis replay SQLITE_BUSY | Payload BEGIN SQLITE_BUSY | Append SQLITE_BUSY, ingress live | Append deadline before acquisition |
|---|---|---:|---:|---:|---:|---:|---:|
| Synthetic create | baseline | 36 | 24 | 12 | 0 | 0 | 0 |
| Synthetic create | stages | 36 | 25 | 11 | 0 | 0 | 0 |
| Synthetic create | full | 36 | 23 | 13 | 0 | 0 | 0 |
| Synthetic direct hook | full | 36 | 32 | 0 | 4 | 0 | 0 |
| Copied create | baseline | 36 | 1 | 35 | 0 | 0 | 0 |
| Copied create | stages | 36 | 1 | 35 | 0 | 0 | 0 |
| Copied create | full | 36 | 1 | 35 | 0 | 0 | 0 |
| Copied direct hook | baseline | 36 | 22 | 0 | 10 | 3 | 1 |
| Copied direct hook | stages | 36 | 22 | 0 | 12 | 2 | 0 |
| Copied direct hook | full | 36 | 19 | 0 | 17 | 0 | 0 |

All other historical cases committed every offer. Across all 1,296 offers:
**141 genesis replay busy errors, 43 payload-BEGIN busy faults, 5 append busy
faults with ingress still live, and 1 append acquisition deadline**. There were
zero authority refusals, operation conflicts, other deadlines or unclassified
failures. These are observed categories, not a promise about other workloads.
Native-hook faults exited zero; SQL occurrence deltas independently confirmed
the missing-commit totals. Individual faults retain the conservative
record-unknown policy. The append deadline is not evidence that the outer five-second hook
invocation budget expired. Pool versus SQLite lock residence and physical
COMMIT/fsync attribution remain unmeasured.

## What the hotspot establishes

Copied serial create's baseline process median was 1,631.13 ms. In the separate
full-profile mode, `validatePersistedGenesisAuthority → Apply →
committedOutcomeForExisting → verifyFactContextIntegrity →
validateCanonicalFactContextSets` consumed 55.37 of 66.96 sampled CPU seconds
(82.69%). That percentage is **not** a partition of baseline elapsed time.

Source inspection established that this full context gate runs **once**, not
twice, in an exact Apply replay. Normal new-operation final gates are different
checks. Simply removing the replay gate would discard its only transaction-local
whole-context validation and is not an acceptable optimization. A sound reuse
must prove that this exact transaction snapshot is still covered by completed
verification, including DML generation, journal tail and structural/trigger
currency; a missing, stale or unreadable proof must retain the original gate.
Canonical operation-identity comparison, result reconstruction, authority-slot
validation, reducer parity and atomic rollback must remain independent checks.

No timeout, retry policy, failure policy, task routing or production lane budget
is changed in this step. Direct-hook busy/deadline faults are not silently
converted into successes. They remain a separate cost/ingress-budget question
unless the measured implementation change demonstrably addresses their cause.

## Measurement controls

The revised runner reuses the exact retained warmed corpus and payload bytes,
with source hashes and SQL blob membership checks; it does not warm them again.
Two repetitions of each mode use a recorded deterministic randomized order.
Every case still offers 36 requests at C=1 or C=18. Cases are independently
restored. Successful, failed, deadline-limited and watchdog-censored observations
are explicit; failed-response cohorts include deadline-limited failures, and
watchdog-censored observations are a subset of deadline-limited observations.
Never sum overlapping cohorts as if they partition elapsed work.

The new queue timer ends for the entire batch before any Prepare begins. The
separate within-batch preparation delay and inclusive admission-to-own-Prepare
delay retain the old useful observation without calling all of it queueing.
Turn elapsed is not SQLite lock-held time. Removing the batch-start queue end
was an executed red control: both boundary tests failed, then passed after
restoration. Exact warmed-payload substitution and task/hook deadline-cohort
regressions also have focused tests. CPU sample counts use the profile's actual
period, not an assumed 10 ms. Independent review accepted these harness changes.

## Completed matched experiment

Before started **2026-10-16T11:11:59Z**; after started
**2026-10-16T11:23:46Z**. Both used Pasture commit
`7bbe20b54db55edf76e0502a405fcf194883d324`, optimized CGO-disabled binaries and
Go **1.26.3**. Before used published Provenance v0.10.0; after used the local
unpublished source commit **`63298e654177b6c532c0bc65e5041db58f940dbd`** through
a private Go workspace. No dependency pin, installed binary or live configuration
was changed. Baseline and instrumented modes within each campaign used identical
binaries. Build commands, source snapshots, hashes and exact per-request commands
are retained in each private manifest.

Reproduce with variables naming retained private evidence and the source worktree:

```sh
nix develop -c python3 tools/profile-writes.py --seed-campaign "$SEED_CAMPAIGN" --tasks 60 --requests 36 --repetitions 2 --order-seed 20261016
nix develop -c python3 tools/profile-writes.py --seed-campaign "$SEED_CAMPAIGN" --tasks 60 --requests 36 --repetitions 2 --order-seed 20261016 --dependency-worktree "$PROVENANCE_WORKTREE"
python3 tools/compare-write-profiles.py "$BEFORE" "$AFTER"
```

The source is the retained warmed corpus from the earlier campaign, not a new
operator-store copy. Both starting corpus hashes matched between before/after.
Synthetic: **60 tasks, 1 occurrence, 143 journal rows, 827,392 bytes**. Copied:
**1,063 tasks, 5,129 occurrences, 27,589 journal rows, 94,560,256 bytes** after
consistent backup. The same **292-byte warmed payload** was verified in both
seeds and reused byte-exact. No second warm invocation changed the input work.

Each campaign completed **72 cases / 2,592 offers**: two repetitions per
corpus/path/concurrency/mode, 36 offers per repetition. Mode order was seeded and
randomized identically before/after, with a fresh seed restore for each case.
The complete before campaign preceded the after campaign: treatment order was
not counterbalanced, and host/cache variation remains a limitation. No heavy
correctness builds ran in the measured windows; symbolization ran afterwards.

### Exact outcomes and successful-response latency

Each row below is **72 offered attempts**, not one 36-request case. Latency is
the pooled **successful-response median in milliseconds**, before → after.
Commits are independent SQL deltas. `B/S/F` identify baseline/stages/full, not
different binaries within a campaign.

Failure classes: **R** genesis-replay SQLITE_BUSY; **O** operation-apply
SQLITE_BUSY (combined transaction boundary, not separately attributed);
**P** payload-BEGIN SQLITE_BUSY; **A** append SQLITE_BUSY while ingress remained
live. A dash means zero failures. All current deadline, watchdog-censored,
authority-refusal, operation-conflict, other-busy and unclassified counts were
**zero**. These current observations are separate from the historical 190-failure
aggregate above.

| Corpus/path | C | Mode | Successful ms before → after | Committed before → after | Failures before → after |
|---|---:|---|---:|---:|---|
| Synthetic create | 1 | B | 54.01 → 40.38 | 72 → 72 | — → — |
| Synthetic create | 1 | S | 53.93 → 40.84 | 72 → 72 | — → — |
| Synthetic create | 1 | F | 70.26 → 57.35 | 72 → 72 | — → — |
| Synthetic create | 18 | B | 50.57 → 45.80 | 48 → 69 | R24 → R2,O1 |
| Synthetic create | 18 | S | 58.90 → 37.49 | 50 → 66 | R22 → R6 |
| Synthetic create | 18 | F | 75.73 → 52.84 | 52 → 69 | R19,O1 → R3 |
| Synthetic direct hook | 1 | B | 159.06 → 154.41 | 72 → 72 | — → — |
| Synthetic direct hook | 1 | S | 159.28 → 155.20 | 72 → 72 | — → — |
| Synthetic direct hook | 1 | F | 172.67 → 171.93 | 72 → 72 | — → — |
| Synthetic direct hook | 18 | B | 168.17 → 171.38 | 72 → 72 | — → — |
| Synthetic direct hook | 18 | S | 170.21 → 170.60 | 72 → 72 | — → — |
| Synthetic direct hook | 18 | F | 179.74 → 180.15 | 72 → 72 | — → — |
| Synthetic lane hook | 1 | B | 55.94 → 58.67 | 72 → 72 | — → — |
| Synthetic lane hook | 1 | S | 57.47 → 55.81 | 72 → 72 | — → — |
| Synthetic lane hook | 1 | F | 62.37 → 62.79 | 72 → 72 | — → — |
| Synthetic lane hook | 18 | B | 122.99 → 124.51 | 72 → 72 | — → — |
| Synthetic lane hook | 18 | S | 112.71 → 122.04 | 72 → 72 | — → — |
| Synthetic lane hook | 18 | F | 113.86 → 120.10 | 72 → 72 | — → — |
| Copied create | 1 | B | 1686.21 → 100.65 | 72 → 72 | — → — |
| Copied create | 1 | S | 1687.83 → 98.26 | 72 → 72 | — → — |
| Copied create | 1 | F | 1725.47 → 122.33 | 72 → 72 | — → — |
| Copied create | 18 | B | 1703.62 → 156.09 | 3 → 28 | R69 → R39,O5 |
| Copied create | 18 | S | 1711.83 → 176.61 | 4 → 29 | R68 → R41,O2 |
| Copied create | 18 | F | 1787.11 → 276.83 | 3 → 30 | R69 → R39,O3 |
| Copied direct hook | 1 | B | 421.65 → 424.78 | 72 → 72 | — → — |
| Copied direct hook | 1 | S | 420.69 → 427.92 | 72 → 72 | — → — |
| Copied direct hook | 1 | F | 451.26 → 450.17 | 72 → 72 | — → — |
| Copied direct hook | 18 | B | 610.36 → 673.24 | 47 → 45 | P18,A7 → P25,A2 |
| Copied direct hook | 18 | S | 606.60 → 670.23 | 42 → 45 | P29,A1 → P21,A6 |
| Copied direct hook | 18 | F | 719.30 → 700.97 | 44 → 41 | P24,A4 → P28,A3 |
| Copied lane hook | 1 | B | 298.99 → 304.76 | 72 → 72 | — → — |
| Copied lane hook | 1 | S | 309.87 → 303.31 | 72 → 72 | — → — |
| Copied lane hook | 1 | F | 305.94 → 324.65 | 72 → 72 | — → — |
| Copied lane hook | 18 | B | 613.31 → 631.14 | 72 → 72 | — → — |
| Copied lane hook | 18 | S | 613.67 → 629.01 | 72 → 72 | — → — |
| Copied lane hook | 18 | F | 622.59 → 629.47 | 72 → 72 | — → — |

All-offered copied C=18 create medians were **513.48 → 512.43 ms**, mostly
reflecting failed attempts at the busy boundary. They are not successful-request
latencies. Synthetic C=18 create all-offered medians were **117.65 → 48.99 ms**.
Changed success sets prohibit an equal-successful-work speedup claim for either
burst. The comparison tool deliberately emits no wall speedup ratio there.

Across all modes, current before failures were **R271, O1, P71, A12 = 355**;
after **R130, O11, P74, A11 = 226**. Commits were **2,237 → 2,366** of 2,592.
Both commands exited nonzero honestly after completing/rendering every case.
All 24 lane cases in each campaign passed routing/SQL/shutdown reconciliation.
For every case, successful-response counts equalled committed SQL deltas and
classified-failure counts equalled missing commits. Maximum observed process
times were 2,346.53 ms before and 1,890.73 ms after, below the 90-second watchdog;
no censoring affected these comparisons.

### Cost reduction and remaining work

Copied C=1 baseline did identical successful work, 72 commits on each side:
measured windows totaled **118.450 → 7.340 seconds**, an observed **16.14×**
wall-time ratio on this host. Synthetic C=1 baseline windows were
**3.959 → 3.214 seconds** (1.23×). Neither result is a deployment or cross-machine
performance guarantee.

In copied C=1 **full-profile** mode across the same 72 creates:

- Sampled CPU: **135.04 → 6.17 seconds** (13,504 → 617 CPU samples; 95.43%
  lower total sampled CPU). The old stack
  `systemSession → validatePersistedGenesisAuthority → Apply →
  committedOutcomeForExisting → verifyFactContextIntegrity →
  validateCanonicalFactContextSets` contributed **113.05 seconds** to the
  whole-context scan before; it contributed **zero sampled scan CPU** after.
- System-identity validation sampled CPU: **113.16 → 0.09 seconds**. The guarded
  proof itself contributed **0.06 seconds**; scope/invalidation tests establish
  the absence of the scan, rather than interpreting a missing sample as proof.
- The **unchanged necessary new-write** `verifySubtypeIntegrity` work was
  **4.40 → 4.36 seconds**. It now dominates at about **70.7%** of after CPU.
  Removing a required per-write check is not the source of the improvement.
- Sampled allocated bytes: **82,993,606,494 → 484,096,073** across all 72
  processes. This is alloc-space, not retained heap, peak RSS or one process's
  memory footprint; instrumentation allocations are included on both sides.

Stage-only copied C=1 medians: identity **1574.441 → 1.625 ms**;
preparation/apply/readback **76.842 → 67.653 ms**. These are inclusive regions,
not a sum partition of baseline time. The per-write region and subtype-integrity
stacks, plus remaining burst busy failures, are the next measured bottleneck.
Direct-hook preparation/evidence reads and its busy failures were not fixed by
this task-identity optimization. No retries, deadlines or routing were changed
to manufacture a better completion count.

Corrected copied C=18 stage medians distinguish admission-to-batch-start queue
residence **292.408 → 307.118 ms**, within-batch preparation wait
**225.573 → 235.519 ms**, and inclusive admission-to-own-Prepare
**516.928 → 529.656 ms**. Do not add medians or double-count the inclusive field.
Whole-turn elapsed was **316.083 → 334.300 ms**; SQLite lock-held duration remains
unknown. Fixed-order historical +23/+39/−31 ms contrasts are not overhead
estimates; current randomized repeat/mode distributions are retained in full,
with before-first treatment ordering and host noise still explicit limitations.

### Soundness and gates

The actual optimization is upstream only: exact replay rechecks the durable
proof in its own transaction. Every DML generation/tail/shape mismatch, failed
read, missing proof or structural/trigger-definition uncertainty uses the
original full context gate. There is no cross-call cache or ledger advance.
New folds invalidate proof reuse within a batch. Expected operation identity,
authority-slot checks, reducer paths and rollback/result taxonomy remain intact.

File-backed tests count actual scans, preserve complete durable snapshots and
reject post-open unrelated/hidden context corruption. Stale/missing/unreadable
ledger, actual in-memory operation, same-name no-op generation trigger and
fresh-fold-then-replay selection are covered. Executed red controls fail both
ways: always skip admits corruption; always scan violates the zero-scan positive.
Restoration passes the focused race suite. Independent review accepted the final
production diff and coverage without remaining important findings.

Completed gates: focused shuffled race tests for replay/context/ledger/batch;
public batch/borrowed-store, SQL-source architecture and vocabulary guards;
real Pasture `TestSystemIdentity` race suite through the private workspace;
full upstream and consumer vet, CGO-disabled builds, gofmt/diff checks, upstream
ast-grep and Nix evaluation. Twelve profiling Python checks pass, including
watchdog/task-deadline cohorts, identical-payload reuse and failed-work speedup
refusal. No code-generation input changed. Full integration suites and release
adoption remain integration-owner work, not claimed here.

### Retained private evidence

Each campaign retains matching binaries, source/build manifests, all request and
failure accounting, CPU/heap/alloc-space/alloc-objects/block/mutex/goroutine
profiles, **872 traces**, and **192 SVG plus 192 HTML** metric-labeled stack
flames. `artifact-index.json`, `manifest.json`, `summary.json`, `lane-audit.json`,
the after `comparison.json` and reproducible local `analysis.json` index the data.
Actual private paths and payloads are not committed. No profiling HTTP service
was started; all children were reaped and no sandbox socket remained.

Still unknown: separate SQLite pool/lock/fold/commit wall partitions and physical
fsync attribution, native-host discovery, pre-main startup, peak/post-GC retained
heap, active-load goroutine snapshots and long-tail/multi-machine behavior.
Production batch parameters and the later lane sweep are deliberately untouched.
The source optimization is complete and measured, but the released dependency
pin and operator installation remain unchanged under the no-release instruction.
