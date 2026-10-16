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
case. Randomized order with seed 20261016, at most 80 cases and 1800 measured
seconds for the first campaign. Pair instrumentation-off/on current-setting
controls; peer-only controls establish unloaded CLI delay. Peer writes are
actual `pasture task create` transactions, not synthetic lock-holding sleeps
and not a claim to run an engine workflow. Repeat finalists only if the pilot
leaves enough budget. An untested full cross-product or larger corpus remains
explicitly untested, not inferred safe.

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
