# Isolated lane tuning experiment

## Preregistered design (before measurement)

This experiment does not change production defaults or install settings. It
starts from the corrected write-cost checkpoint and unpublished, measured
exact-replay optimization. Separate source worktrees preserve reviewed commits.

Proposed acceptance limits are **design choices pending operator approval**, not
measured optima: zero incorrect commit acknowledgements or unexplained missing
writes; all failures explicit; pending records/bytes never exceed configured
caps; at least 99% hook completion; no competing-writer failures; competing CLI
p95 at most the smaller of 500 ms and its unloaded p95 plus 100 ms; observed
successful transaction-scope p95 at most 400 ms. The transaction limit is a
screening target, not an enforced lock-duration guarantee. A sample with fewer
than 100 successes cannot support a p99 claim. Failing limits remain failures;
we do not relax them after seeing results.

Passing these limits is only an observed pilot screening result, not a
statistical reliability guarantee. Zero failures in 36 requests cannot prove a
99% long-run completion probability. HoldTarget remains diagnostic, not enforced;
neither this experiment nor a favorable p95 makes a hard scheduler/lock bound.

Bounded pilot: batch records 1/5/10/20/40 at 25 ms, then current 20 records at
5/50/100 ms. Synthetic and copied starting corpora; C1, C18 and controlled
20-ms arrivals. Fresh identical seed backups and exact warmed payload for every
case. Randomized order with seed 20261016, at most 80 cases and 1800 total
seconds, split into a 900-second pilot and a 900-second finalist campaign
(including builds/setup). The pilot is 56 cases: 48 grid, six current-setting
instrumentation-off pairs and two peer-only baselines. Pair instrumentation-off/on current-setting
controls; peer-only controls establish unloaded CLI delay. If the pilot leaves
time, repeat two batch-count finalists at 25 ms twice on C18 and controlled
arrival, 108 offers per case (18 further cases including peer baselines;
74 total). Finalist selection is exploratory and disclosed; per-finalist matched
work is compared separately from the 36-offer pilot. Peer writes are
actual `pasture task create` transactions, not synthetic lock-holding sleeps
and not a claim to run an engine workflow. Repeat finalists only if the pilot
leaves enough budget. An untested full cross-product or larger corpus remains
explicitly untested, not inferred safe.

Validated orchestration is at most one pilot command and one finalist command,
each with a hard ceiling of 900 seconds (the CLI refuses a larger per-phase
budget); defaults are exactly 36 and 108 offers respectively. Thus both phases
together have at most 1800 seconds of active budget. Controlled arrivals launch
independent scheduled threads, bounded by the phase's offer count, rather than
blocking subsequent offers on an 18-client completion pool. A hook arrival more
than one 20-ms interval late, or a peer offer more than its 100-ms interval late,
invalidates that case's arrival contract. These scheduling tolerances are
preregistered workload design choices, not latency acceptance bands.

Metrics distinguish queue residence ending at batch start, same-batch
preparation wait, process/request end-to-end, full turn elapsed and actual SQL
owner boundaries. SQL connection acquisition, BEGIN driver call/wait, transaction
body and COMMIT call are separately measured where the SQLite owner exposes
them. Driver scheduling and busy handling cannot be split into physical lock
hold or fsync time without additional driver/OS evidence. The successful BEGIN
return to COMMIT return is an observed transaction-scope proxy, not a strict
physical writer-lock bound: acquisition before BEGIN returns is omitted, and
COMMIT may release the lock before returning. It is not the
old approximately 408-ms turn statistic called a writer-lock duration.

Immediate drained-queue flushes may make FlushInterval inert for staged ingress.
Count actual full/drained/stopped/timer decisions and batch sizes before
attributing effects to that knob. Queue byte metrics count staged payload bytes,
not total Go heap. Retain native logs, binary hashes, per-offer outcomes and raw
timings privately; commit only privacy-safe aggregates.

The warmed 292-byte repeated payload exercises count limits, not near-cap byte
pressure. Batch/queue byte and count inputs are exposed and validated only in
the experiment build. Unless additional payload shapes are actually run, byte
cap sizing, saturation behavior and total memory safety remain untested beyond
existing correctness bounds and the observed pending-byte high water.

## Exploratory finalist selection (after pilot, before repeats)

The completed pilot committed all 1,944 offered hook writes and all 336 peer
creates, but copied batch=1 produced 19 fault/deadline responses despite eventual
durability. It is rejected, not credited with success because a process exited
zero. Smaller 5/10-record batches produced more fixed-cost transactions and worse
copied burst/arrival response tails. Repeat **20 versus 40 records, 25 ms** to
test whether a larger cap helps long arrivals without exceeding the observable
transaction/peer budgets or growing the pending queue. This is exploratory
selection, not a fastest-median optimum claim. The repeats use 108 offers per
case and two randomized repetitions per corpus/load/configuration; all limits
above stay unchanged. Peer samples remain too sparse for a reliability guarantee.

The campaign uses committed recorder `dbbde538` in a separate clean source
worktree, not the other worker's unfinished general artifact-hardening edits.
Independent review accepted this restricted measurement use: every output is a
fixed fresh distinct file inside an exclusive mode-0700 sandbox, absent before
launch, with no concurrent path mutation. The recorder's general existing-file
hazard is not cleared for arbitrary users. Missing/truncated/error-marked output
invalidates observations. SQL scope remains an interval/proxy, never a physical
lock bound or fsync measurement.

## Actual completed experiment

Both phases completed: **56 pilot cases plus 18 finalist cases**, deterministic
randomized order, seed **20261016**. Two initial zero-offer attempts exposed and
repaired missing subprocess `PIPE` forwarding and a missing stage-artifact parent
directory; their logs/stores remain retained, excluded from measurements. The
successful pilot had an 850-second hard ceiling; finalists 900 seconds. Neither
watchdog expired. Summed request windows were **187.287 + 39.186 = 226.473 s**;
build/setup time is outside those sums but inside the watchdog ceilings.

### Inputs, provenance and commands

Unchanged source snapshots: synthetic **60 tasks / 1 occurrence / 143 journal
rows**; copied **1,063 tasks / 5,129 occurrences / 27,589 journal rows**. Both
phases hash-checked the original warmed seed files and reused exactly the same
292-byte warmed payload. Every case restored a fresh consistent backup. No
operator database was opened by the workload, daemon or writer peers.

All **complete configured budgets** are recorded per case and in aggregation
identity: `batch_records` as below; `flush_ms` as below; **BatchBytes 8,388,608,
QueueRecords 256, QueueBytes 33,554,432, HoldTarget 400,000,000 ns**. HoldTarget is
fixed by the unchanged timeout profile, not a hidden experiment override; each
ON observer verifies that actual value. Distinct complete budgets cannot pool.

Pasture build sources were `b0b2f92` (pilot), `0c57a75` (finalists); the Go source
and actual binaries matched across phases. Provenance was **`dbbde538`**, which
includes the independently accepted guarded optimization **`63298e6`**. Private
Go workspaces only; the published dependency pin stayed unchanged. Builds used
**Go 1.26.3, CGO_ENABLED=0, -tags=pasture_experiment**, optimized, no race.
The same binaries ran OFF/ON, every setting and peer writer:

- `pasture` SHA-256 `0cd5143975db67489ddadd2c5c387d4d775bf21e61edb4582c2596aaa1b71964`
- `pastured` SHA-256 `dce8213a4a4fb08544663472aa674cfe5dd3189b10077c8b6d753794085e9710`

Exact commands, with `TIMING_SOURCE` naming the clean local `dbbde538` worktree
and `SEED_CAMPAIGN` naming the retained campaign with the verified seed hashes:

```sh
nix develop -c python3 tools/lane-sweep.py --seed-campaign "$SEED_CAMPAIGN" --stages --budget-seconds 850 --dependency-worktree "$TIMING_SOURCE"
nix develop -c python3 tools/lane-sweep.py --seed-campaign "$SEED_CAMPAIGN" --stages --finalist-batches 20,40 --budget-seconds 900 --dependency-worktree "$TIMING_SOURCE"
PYTHONDONTWRITEBYTECODE=1 python3 tools/analyze-lane-sweep.py "$PILOT" "$FINALISTS" > "$FINALISTS/aggregate.json"
```

All **70 hook-load cases** passed daemon routing/counter/SQL reconciliation;
there was no direct fallback. Four peer-only controls had no hook offers. All
74 case process groups were killed/reaped/verified, no sockets remained, and
there was no heavy correctness build or renderer inside the request windows.

### Pilot measurements

Every row below offered **36 hooks plus six independently scheduled real CLI
task creates**. `b/f` means batch-record cap / flush milliseconds. Response
figures are **successful-only p50/p95 milliseconds**. `ACK/SQL` separates a
non-fault native response from independent committed occurrence deltas; the
denominator is 36. Peer p95 has only six samples, therefore is descriptive and
has no p99. `TX` is successful BEGIN-return → COMMIT-return p95, with transaction
sample count, **not whole-turn or physical-lock duration**. `F/D/T` counts the
actual full/drained/timer flush branch; no stopped flushes occurred.

| Corpus/load | b/f | Successful p50/p95 ms | ACK/SQL | TX p95 ms (n) | Peer p95 ms | F/D/T |
|---|---|---:|---:|---:|---:|---|
| Copied C1 | 1/25 | 323.15/335.91 | 36/36 | 57.85 (36) | 137.70 | 0/0/36 |
| Copied C1 | 5/25 | 323.92/338.30 | 36/36 | 60.55 (36) | 115.80 | 0/0/36 |
| Copied C1 | 10/25 | 310.44/336.27 | 36/36 | 58.26 (36) | 117.48 | 0/0/36 |
| Copied C1 | 20/5 | 306.10/319.79 | 36/36 | 59.82 (36) | 137.89 | 0/0/36 |
| Copied C1 | 20/25 | 323.39/340.24 | 36/36 | 63.20 (36) | 138.67 | 0/0/36 |
| Copied C1 | 20/50 | 349.93/378.95 | 36/36 | 62.83 (36) | 142.88 | 0/0/36 |
| Copied C1 | 20/100 | 397.66/406.31 | 36/36 | 61.62 (36) | 119.04 | 0/0/36 |
| Copied C1 | 40/25 | 324.81/336.40 | 36/36 | 60.88 (36) | 141.29 | 0/0/36 |
| Copied C18 | 1/25 | 4757.28/4838.76 | 35/36 | 59.60 (36) | 142.53 | 35/0/1 |
| Copied C18 | 5/25 | 1055.00/1350.57 | 36/36 | 59.52 (8) | 114.34 | 6/2/0 |
| Copied C18 | 10/25 | 651.81/1091.09 | 36/36 | 60.06 (5) | 133.55 | 1/4/0 |
| Copied C18 | 20/5 | 641.92/812.15 | 36/36 | 65.59 (4) | 139.57 | 0/4/0 |
| Copied C18 | 20/25 | 641.17/821.99 | 36/36 | 67.49 (4) | 141.11 | 0/4/0 |
| Copied C18 | 20/50 | 620.96/792.47 | 36/36 | 64.11 (4) | 137.81 | 0/4/0 |
| Copied C18 | 20/100 | 586.74/796.79 | 36/36 | 66.76 (4) | 122.66 | 0/4/0 |
| Copied C18 | 40/25 | 624.09/812.40 | 36/36 | 66.36 (4) | 140.92 | 0/4/0 |
| Copied arrival | 1/25 | 2567.25/4777.78 | 18/36 | 63.25 (36) | 122.03 | 35/0/1 |
| Copied arrival | 5/25 | 1249.26/1917.57 | 36/36 | 66.58 (8) | 118.39 | 6/2/0 |
| Copied arrival | 10/25 | 795.58/1056.48 | 36/36 | 63.19 (5) | 141.56 | 3/2/0 |
| Copied arrival | 20/5 | 616.38/799.77 | 36/36 | 68.40 (3) | 141.98 | 1/1/1 |
| Copied arrival | 20/25 | 558.13/757.89 | 36/36 | 65.07 (3) | 139.93 | 1/2/0 |
| Copied arrival | 20/50 | 599.31/789.26 | 36/36 | 70.20 (3) | 141.71 | 1/2/0 |
| Copied arrival | 20/100 | 564.18/764.26 | 36/36 | 67.18 (3) | 118.29 | 0/3/0 |
| Copied arrival | 40/25 | 568.87/769.07 | 36/36 | 71.02 (3) | 142.93 | 0/3/0 |
| Synthetic C1 | 1/25 | 68.38/76.80 | 36/36 | 1.94 (36) | 76.13 | 0/0/36 |
| Synthetic C1 | 5/25 | 67.29/72.03 | 36/36 | 1.78 (36) | 69.28 | 0/0/36 |
| Synthetic C1 | 10/25 | 68.88/81.31 | 36/36 | 2.13 (36) | 74.55 | 0/0/36 |
| Synthetic C1 | 20/5 | 48.50/57.29 | 36/36 | 2.02 (36) | 75.01 | 0/0/36 |
| Synthetic C1 | 20/25 | 63.03/68.54 | 36/36 | 1.83 (36) | 71.83 | 0/0/36 |
| Synthetic C1 | 20/50 | 88.01/91.81 | 36/36 | 2.13 (36) | 74.72 | 0/0/36 |
| Synthetic C1 | 20/100 | 141.49/153.01 | 36/36 | 1.90 (36) | 75.85 | 0/0/36 |
| Synthetic C1 | 40/25 | 68.57/72.63 | 36/36 | 1.96 (36) | 76.59 | 0/0/36 |
| Synthetic C18 | 1/25 | 469.01/559.95 | 36/36 | 1.88 (36) | 79.17 | 35/0/1 |
| Synthetic C18 | 5/25 | 155.06/240.64 | 36/36 | 3.64 (8) | 78.17 | 6/2/0 |
| Synthetic C18 | 10/25 | 115.63/237.60 | 36/36 | 5.62 (5) | 75.00 | 1/4/0 |
| Synthetic C18 | 20/5 | 116.91/216.99 | 36/36 | 8.62 (4) | 73.55 | 0/4/0 |
| Synthetic C18 | 20/25 | 120.66/216.51 | 36/36 | 7.88 (4) | 78.41 | 0/4/0 |
| Synthetic C18 | 20/50 | 113.67/218.29 | 36/36 | 7.71 (4) | 76.90 | 0/4/0 |
| Synthetic C18 | 20/100 | 118.23/209.48 | 36/36 | 7.71 (4) | 73.72 | 0/4/0 |
| Synthetic C18 | 40/25 | 128.28/224.00 | 36/36 | 7.95 (4) | 74.02 | 0/4/0 |
| Synthetic arrival | 1/25 | 238.39/296.16 | 36/36 | 1.86 (36) | 70.62 | 35/0/1 |
| Synthetic arrival | 5/25 | 62.23/144.28 | 36/36 | 3.56 (17) | 73.33 | 0/16/1 |
| Synthetic arrival | 10/25 | 63.81/143.39 | 36/36 | 2.93 (17) | 76.42 | 0/16/1 |
| Synthetic arrival | 20/5 | 60.33/122.45 | 36/36 | 2.18 (21) | 69.23 | 0/12/9 |
| Synthetic arrival | 20/25 | 64.14/145.65 | 36/36 | 3.40 (17) | 72.33 | 0/16/1 |
| Synthetic arrival | 20/50 | 63.39/140.77 | 36/36 | 2.95 (17) | 70.31 | 0/16/1 |
| Synthetic arrival | 20/100 | 63.65/151.18 | 36/36 | 4.01 (16) | 73.15 | 0/16/0 |
| Synthetic arrival | 40/25 | 65.36/138.92 | 36/36 | 3.27 (17) | 74.55 | 0/16/1 |

Pilot total: **1,944 offered, 1,944 SQL commits, 1,925 non-fault ACKs**. Exactly
**19 other-deadline faults** occurred: copied C18 batch=1 had one, copied
controlled-arrival batch=1 had 18. Their diagnostics name the **5-second
hook-invocation deadline**, not an acquired SQLite BEGIN deadline. All 19 exited
zero under native fail-open policy; none is a successful-response sample.
All had eventual committed deltas by shutdown. This is an aggregate durable-
versus-acknowledged difference, not per-request identity proof or a false
successful ACK. The cases are rejected, not used for equal-successful-work
speedup. SQL counts show no missing writes; deadline response guarantees failed.
There were **zero busy/authority/conflict/unclassified/watchdog-censored failures**
and all **336 peer creates** succeeded. Pilot exit 1 honestly reports the two
rejected cases after all 56 completed.

### Repeated finalists

Each row pools **two independent 108-offer cases: n=216 successful hooks and
n=12 peers**. All **1,728 hooks and 108 peers** committed and received non-fault
responses; every failure/deadline/censored count was zero. Hook p99 is a
descriptive order statistic with n=216, not a well-estimated long-run tail;
peer p99 remains null. `TX n` counts transactions, not hooks. No ratio below
compares a 36-offer pilot to a 108-offer finalist.

| Corpus/load | Batch/flush | Hook p50/p95/p99 ms (n216) | TX p95 ms (n) | Peer p95 ms (n12) | Committed/window Hz |
|---|---|---:|---:|---:|---:|
| Copied C18 | 20/25 | 641.45/820.99/822.53 | 65.78 (24) | 138.71 | 26.85 |
| Copied C18 | 40/25 | 642.35/822.33/823.73 | 66.37 (24) | 142.94 | 26.79 |
| Copied arrival | 20/25 | 527.62/715.04/763.87 | 68.72 (14) | 140.42 | 43.29 |
| Copied arrival | 40/25 | 538.29/724.46/784.86 | 70.58 (14) | 142.13 | 42.90 |
| Synthetic C18 | 20/25 | 110.46/218.89/220.78 | 7.87 (24) | 76.25 | 141.26 |
| Synthetic C18 | 40/25 | 116.19/219.05/220.04 | 8.73 (24) | 79.97 | 133.12 |
| Synthetic arrival | 20/25 | 64.60/109.23/151.38 | 2.55 (104) | 74.43 | 48.86 |
| Synthetic arrival | 40/25 | 61.19/102.09/133.52 | 2.65 (105) | 77.08 | 49.43 |

Arrival request rate was **50 Hz**, achieved offer-start rate **50.02 Hz** in
all finalist cohorts; maximum hook schedule lateness was **0.910 ms**, inside
the unchanged 20-ms tolerance (pilot maximum 1.841 ms). Finite-window completion
rates above include final drain/peer activity; 43 Hz does not establish a steady-
state capacity ceiling. C1/C18 are closed-loop concurrency settings, not a
promised exogenous rate. Peers offered six real writes at 100-ms intervals per
case, concentrated near the beginning; continuous engine workflow contention
and other host payloads remain untested.

### What costs and flush controls actually mean

- **Serial:** all 16 ON C1 pilot cases flushed every record by timer: **576 timer
  flushes**. Reducing 25 to 5 ms changed synthetic successful median
  **63.03 → 48.50 ms**, copied **323.39 → 306.10 ms**; 100 ms raised them to
  141.49/397.66 ms. This is real idle-fallback relevance, not a throughput gain
  from a batch cap that no lone request reaches. Single randomized observations
  do not establish causal differences beyond the recorded timer mechanism.
- **C18:** at current/larger caps, all **96 finalist flushes were drained**;
  zero full or timer flushes, actual maximum batch **16**, so **20 vs 40 is
  effectively nonbinding here**. At batch20, changing 5/25/50/100 ms produced
  zero timers in all eight C18 pilot cases. The faster-looking copied median
  at 100 ms is not evidence the interval helped; that timer never fired.
- **Arrivals:** copied20 repeats had **2 full + 12 drained**, copied40
  **14 drained**, max batch20/21; both had zero timers. Synthetic repeats had
  **103 drained +1 timer** at20 and **105 drained** at40, max batch6/5. Therefore
  25 ms was mostly bypassed in these arrival patterns. Actual branch reasons
  have precedence; the separate `full_condition` flag preserves cases where
  drained and a full condition coincide. An initial lone batch=1 can still take
  the timer path; instrumentation did not change that existing behavior.
- **Queue bounds:** overall pending high water **34 records / 9,928 staged
  bytes**. Finalist copied arrival high water **21 / 6,132 bytes**. Maximum
  observed batch21 / 6,132 bytes. These are below fixed caps, not heap/RSS
  estimates, and exclude the in-flight batch from pending memory accounting.
- **Actual SQL vs turn:** copied20 C18 transaction p95 **65.78 ms** (n24) versus
  whole-turn p95 **444.35 ms** (n24). Queue residence p50/p95
  **274.51/441.94 ms**, within-batch preparation wait **243.08/265.99 ms**
  (n216 each); they are distinct, not added or double-counted. The turn is
  dominated outside the observed batch transaction, not a 444-ms measured
  SQLite lock hold.
- **Owner partitions, copied20 C18:** connection acquisition p95 **0.00114 ms**,
  BEGIN wait **0.01477 ms**, body **65.495 ms**, COMMIT **0.441 ms**, n24 each.
  The overlapping subtype-integrity gate inside body was **57.127 ms p95**.
  Necessary fixed gates explain why batch1's shorter transactions nevertheless
  cause excessive hook latency. COMMIT here includes driver/scheduler overhead,
  not an isolated fsync attribution. Synthetic arrival had occasional ~39-ms
  COMMIT outliers; cause is unmeasured.
- **Peer effect:** unloaded finalist controls had p95 **75.49 ms synthetic /
  86.29 ms copied**, n6. Loaded finalist peers had p95 **74.43–79.97 /
  138.71–142.94 ms**, n12 per cohort; all inside observed proposed bands
  175.49/186.29 ms, but not a statistical guarantee. Copied peer `Apply` BEGIN
  waits had p95 about **53.4 ms**, n24 per cohort (two Apply calls per CLI create);
  daemon batch BEGIN p95 stayed under **0.187 ms**. These intervals include
  driver busy handling/backoff/scheduling, not isolated lock residence.

All ON observations reconciled complete SQL operation records with batch counts;
missing output was never interpreted as zero latency. Payload-preparation SQL
and startup do not share the Provenance transaction recorder: their individual
pool/BEGIN/fold/commit partitions remain **unmeasured**. Inclusive preparation
stage tails remain substantial and are the next bottleneck alongside required
per-batch gates; no experiment removed checks or changed failure policy.

### Overhead, acceptance and recommendation

Six current-setting OFF/ON pairs used identical binary/input/offered work and
all acknowledged/committed it. ON/OFF summed-client-time ratios were **0.937,
1.010, 1.025** for synthetic C1/C18/arrival and **1.036, 1.000, 0.981** for
copied C1/C18/arrival. This includes stages plus lane/SQL artifact recording.
They are single randomized pairs, with some ratios below one: no zero-overhead,
significance or exact causal instrumentation-tax claim follows. Correctness
builds and graph rendering ran outside the measured windows.

**Keep production 20 records / 25 ms unchanged.** Reject batch1 for copied burst
and arrival work; reducing to5/10 increases fixed-gate turns and copied tails.
20–40 showed no observed transaction/queue/peer violation for these finite
repeats, but 40 bought no repeatable copied improvement and actual batches never
exceeded21: this is **not certification of a full 40-record turn**, a larger
journal, or arbitrary byte sizes. A **5–25 ms idle-fallback range** is worth
operator consideration for lone arrivals; only the 25-ms finalist point was
repeated, so the evidence does not authorize a default change to5.

No setting earns formal **PASS** under the preregistered confidence rule: peer
samples are sparse, so non-rejected settings remain **INDETERMINATE**. All
measured finalist transaction p95 values stayed below400 ms and loaded peer
sample p95 values stayed inside their bands, but bands were not loosened to
call the fastest median optimal. Results are per shape and host, not one optimum.

Untested: remaining12 combinations of the20-point count×flush cross-product;
additional journal sizes beyond143/27,589 rows; queue/byte saturation and
near-cap payloads; full40-record transactions; longer sustained arrivals/peers,
engine workflow writes, other harnesses/host versions, machines and storage.
Larger authentic corpora need additional sanctioned setup writes and were
deferred within the fixed budget, not simulated by copying journal rows.

### Evidence and verification

Private runs **`lane-sweep-25lgfrqi`** and **`lane-sweep-s_cd9voz`** retain exact
commands/environments, stable input hashes, matching binaries/build info,
source snapshots, all74 disposable stores, native stdout/stderr/fault logs,
JSONL owner intervals, stage profiles, incremental manifests and aggregate JSON.
Local SVGs show the pilot successful p95 curve, finalist p50/p95/p99, transaction
scope versus turn, and finite-window rate versus requested arrival rate.
Original write-cost profiles, flames and traces remain untouched; no sampling
profiler was enabled for this timing-only experiment.

Completed focused gates on the actual private dependency integration: **26 lane
Python regressions +12 corrected-profiling tests**, shuffled race tests for
system identity and experiment controls, full tagged vet/CGO0 build, fmt/diff.
Prior lane full focused race, normal/tagged build/vet, Nix evaluation and upstream
timing race/SQL guard gates remain recorded. No generation input changed. Red
controls proved queue observation, schedule-miss refusal and complete-budget
pooling tests are sensitive; restored tests pass. The restricted recorder use,
lane isolation and corrected driver/aggregation received independent acceptance.
Uncommitted general recorder-hardening work is preserved separately and is not
claimed integrated. No push, release, installed-pin/default/live-setting change
or task closure was performed.
