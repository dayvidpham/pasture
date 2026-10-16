# Private write-path profiling

Run from a fresh worktree, with no competing builds or experiments:

```sh
nix develop -c python3 tools/profile-writes.py --tasks 60 --requests 36
# Optional additional corpus: consistent SQLite backup, source read-only:
nix develop -c python3 tools/profile-writes.py --source /absolute/source/pasture.db --tasks 60 --requests 36
# Optional: re-render an existing campaign without executing any writes:
nix develop -c python3 tools/render-write-profiles.py /tmp/opencode/wp-EXAMPLE
```

The first command prints its exclusively created, mode-0700 artifact directory.
It renders the views after measurements and teardown, even when counted write
failures will make its final exit nonzero.
The optional source is NEVER passed to Pasture. Python's SQLite backup API opens
it with `mode=ro`, includes committed WAL frames, and checks the destination's
integrity. There is no byte-copy fallback and no implicit operator source.
The source must be a compatible current-format store; schema migration is not
part of this experiment. Raw artifacts, source paths, copied databases, payloads,
logs, traces, binaries and source diffs stay local. Never attach them to an issue.

## Work and lifetime

This is an opt-in developer experiment, not a duration gate or tuning tool.
It reuses the existing load harness's authentic SessionStart fixture, production
CLI arguments, readiness log, occurrence-evidence accounting and real daemon
lane, without its time-window offer counts, main-file fallback or live defaults.
The fixture's filesystem paths are redirected into the sandbox. No Git hooks
are installed and no native harness is launched. Host version is explicit;
executable discovery is deliberately **not measured**.

Each corpus is warmed once through the direct hook to establish definitions.
Every path × concurrency × mode gets an independent SQLite backup of that exact
seed. Corpus counts, bytes and SHA-256 are recorded. Runs offer a fixed number
of requests, not “as many as fit a time window.” C=1 consists of repeated single
requests; C=18 uses eighteen bounded process workers. Task create, direct hook
and daemon-served hook use the real shipped entrypoints. No test double writes
records. Cold definition activation is outside the measurement.

Every child has an explicit `--db` and a matching `PASTURE_DB_PATH`. Its environment
is allowlisted, with private HOME/config/data/state/tmp/runtime roots and an
explicit direct/auto lane setting. The runtime root is short enough for Unix
sockets. The daemon has an explicit private config. Source data is never used as
a runtime root. All process groups are waited and stopped on ordinary failure;
SIGKILL or machine loss cannot run language-level cleanup. Stores are retained
privately for audit, not removed automatically. Inspect `manifest.json` cleanup
state and confirm no daemon survives before manually disposing of artifacts.

Offered, committed (SQL task/occurrence delta), failed, process-error and fault
counts are separate. Hooks can fail open with process exit zero: evidence deltas
and fault files, not exit codes alone, establish committed work. A failed case
remains in the manifest and the overall command exits nonzero after all cases.
Do not compare unlike committed counts as successful throughput or a speedup.
Lane cases additionally require orderly daemon exit, no fallback diagnostics,
and shutdown lane-record counters equal to both offered work and committed SQL
occurrences. Readiness alone cannot prove routing: auto-mode can silently fall
back when a socket disappears. `lane-audit.json` records that independent proof,
including batches, maximum batch size, observed turn hold and hold-exceeded count.
The logged `committedRecords` counter counts settled failures too, so it is never
accepted without the SQL delta. Audit retained evidence without rerunning work:
`python3 tools/profile-writes.py --audit-existing /tmp/opencode/wp-EXAMPLE`.

## Instrumentation and boundaries

The **same optimized CGO-disabled binaries** run all modes, with matching binaries
saved for symbolization. No production timeout, batch, queue or SQLite setting is
changed. Baseline leaves profiling disabled; `stages` collects constant-name wall
regions only; `full` adds 100-Hz CPU sampling, normal Go memory sampling, mutex
fraction 1, block rate 1, goroutine dumps and execution trace. Short operations
are repeated and their profiles merged; inspect actual sample counts rather than
assuming one operation supplies adequate CPU evidence.

`internal/writeprofile` is inert without `PASTURE_WRITE_PROFILE_DIR`. Supplying it
also requires `PASTURE_WRITE_PROFILE_MODE=stages|full` and a new absolute output
directory. It does not make an arbitrary command safe; use the checked-in runner.
Instrumentation begins at `main`, after Go initialization. No public pprof
listener is started. Profile shutdown flushes CPU/trace buffers and itself costs
time; allocation profiles include profiler/runtime overhead. Memory profiles are
shutdown sampled in-use estimates, not post-forced-GC retained memory or peaks.

Measured regions cover unified store open/migration/activation/registration;
task system identity; task preparation/apply/readback; hook admission/parse and
host-version selection; direct journal append; lane queue/accumulation wait;
per-record blob/identity/definition/encoding preparation; batch apply; settlement.
Trace regions stay on their originating goroutine; queue timers cross goroutines
and appear only in stage JSON. `go.write_gate.wait` measures the in-process Go
semaphore, **not** SQLite lock acquisition.

The pinned Provenance library owns pool checkout, `BEGIN IMMEDIATE`, fold,
projection, integrity gates and COMMIT. The combined `*.pool.lock.fold.integrity.commit`
region does **not** split those boundaries. CPU stacks can identify CPU spent
inside them; mutex/block profiles measure Go contention, not total SQLite writer
lock waiting. Trace syscall/sync views do not magically attribute fsync latency.
Commit-call wall time and physical fsync service time are not separately measured.
Startup before `main`, response I/O and executable discovery remain unsplit.
Do not subtract sampled CPU from wall time and call the remainder “disk.”

All stage totals are inclusive: nested regions and concurrent request waits
overlap. Their sums can exceed wall time. Baseline/stages/full comparisons need
the same corpus, requests and outcomes; report overhead, never “instrumented
speedup.” Run order is fixed, not randomized: cache drift and host load remain
confounders. Repeat complete campaigns before making performance decisions.

## Artifact index and reopening

`manifest.json` records date, base revision plus dirty diff fingerprint, build
commands/info, binary hashes, seed shape, request commands, counts, durations and
stages. `summary.json` omits individual request records. The renderer writes
`artifact-index.json`, symbolized flat/cumulative tops, full stacks and raw pprof
text, plus self-contained SVG/HTML flames for CPU, in-use bytes, allocated bytes,
allocated objects, mutex delay and block delay. Merged in-use snapshots are a
**sum across processes**, not one process's peak. Flame width is its named sampled
metric, never elapsed wall time or literal function-call counts. Samples are
statistical; an empty mutex profile means no sampled contention, not proof that
no lock existed. The offline SVG root is at the top; hover frames for full names.

```sh
# Offline: open any views-client/cpu-cpu.html or .svg in a local browser.
# Interactive pprof (loopback only; Ctrl-C when finished):
nix develop -c go tool pprof -http=127.0.0.1:0 -no_browser \
  /tmp/opencode/wp-EXAMPLE/bin/pasture \
  /tmp/opencode/wp-EXAMPLE/synthetic-create-c1-full/profiles/0/cpu.pprof
# In pprof select View → Flame Graph; it uses the same sampled CPU metric.
nix develop -c go tool trace -http=127.0.0.1:0 \
  /tmp/opencode/wp-EXAMPLE/synthetic-create-c1-full/profiles/0/trace.out
```

Representative traces are runtime-decoded into scheduler, sync and syscall pprof
extracts; `trace-validation.json` records successful decoder commands. The runner
avoids `go tool trace -d=parsed`, whose repeated stack expansion can produce many
gigabytes of text from a small trace. Original traces for every request remain local.
The campaign starts no profiling HTTP server and leaves none running.

## Focused checks

```sh
python3 -m unittest discover -s tools -p test_profile_writes.py
nix develop -c env CGO_ENABLED=1 go test -race ./internal/writeprofile ./internal/writelane ./internal/lifecycle/receipt
nix develop -c env CGO_ENABLED=0 go vet ./...
nix develop -c env CGO_ENABLED=0 go build ./...
```

Race detection needs CGO solely for its detector; measured/product builds remain
CGO-disabled. No generated protocol surface is changed. Tests protect opt-in,
exclusive/private outputs, profile containers, timers, source-preserving WAL
backup, environment isolation, short sockets, process reaping, count inspection,
sample-weight preservation and SVG escaping. No timing threshold is an assertion.
