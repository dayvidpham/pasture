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
