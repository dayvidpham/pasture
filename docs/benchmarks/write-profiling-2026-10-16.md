# Write-path profile evidence — 2026-10-16

## Provenance and procedure

Campaign started **2026-10-16T08:26:41Z**, base revision
`5d8d65473d0586d5edf69fa497693ca475997584`, with this change's opt-in
instrumentation uncommitted. The local manifest records dirty diff SHA-256
`eee1bf973625d269185a4e1a13a423c6084a7fec1116d06e452a7838f7ea7f32`,
full source snapshots (including untracked instrumentation), individual request
commands, seed hashes and matching binaries. Later changes only regrouped Go
imports and improved Python auditing/rendering; measured source is preserved.

Command, with the private copied-corpus path intentionally not published:

```sh
nix develop -c python3 tools/profile-writes.py --source "$COPIED_CORPUS" --tasks 60 --requests 36
```

Both binaries: optimized `CGO_ENABLED=0 go build -o <private-bin> ./cmd/<name>`,
Go 1.26.3, linux/amd64, Provenance v0.10.0. SHA-256:

- pasture: `936899d4e192abc3b1a4bb7ecd5952406d1f7f8325525141dbdc6fd3afcbb124`
- pastured: `8b17080848db4c63856f5f017ec046a1ebeda806940f4046ee6d7f78a870bd99`

The identical binaries served baseline, stage-only and full-profile modes.
Every case restored its corpus's same warmed seed. Synthetic shape: 60 tasks,
1 occurrence, 143 journal rows, 827,392 bytes. Copied shape: 1,063 tasks,
5,129 occurrences, 27,589 journal rows, 94,543,872 bytes. A previous exploratory
campaign supplied the consistent private copy; operator data was not recopied
between comparisons. Each case offered exactly 36 requests. Cold definition
activation and actual native-host executable discovery were excluded.

An earlier exploratory campaign is **not** used for the numbers below: it exposed
Python `communicate(timeout=...)` wait polling, which quantized short outer
timings. The corrected runner uses blocking wait with a separate watchdog. The
earlier run also overlapped initial gates. No speedup comparison uses that run.
See [the reproducible procedure](write-profiling.md) for isolation and limitations.

## Fixed-work results

Numbers are observed process-wall **p50 milliseconds**, including process start,
response and (in full mode) profile flushing. B/S/F means baseline/stages/full.
“Committed” is an independent SQL task/occurrence delta, not process exit count.
All rows offered 36. This is one host, fixed order, not a scaling study.

| Corpus / path | C | B | S | F | Committed B/S/F |
|---|---:|---:|---:|---:|---|
| Synthetic create | 1 | 56.01 | 25.60 | 42.09 | 36/36/36 |
| Synthetic create | 18 | 206.88 | 207.79 | 95.68 | 24/25/23 |
| Synthetic direct hook | 1 | 118.57 | 125.61 | 139.24 | 36/36/36 |
| Synthetic direct hook | 18 | 189.88 | 206.54 | 533.93 | 36/36/32 |
| Synthetic lane hook | 1 | 57.05 | 56.58 | 62.21 | 36/36/36 |
| Synthetic lane hook | 18 | 117.39 | 120.66 | 123.62 | 36/36/36 |
| Copied create | 1 | 1631.13 | 1582.68 | 1654.25 | 36/36/36 |
| Copied create | 18 | 515.63 | 516.07 | 535.29 | 1/1/1 |
| Copied direct hook | 1 | 399.83 | 403.20 | 438.64 | 36/36/36 |
| Copied direct hook | 18 | 928.51 | 919.66 | 925.16 | 22/22/19 |
| Copied lane hook | 1 | 369.81 | 331.40 | 338.60 | 36/36/36 |
| Copied lane hook | 18 | 668.91 | 680.08 | 664.94 | 36/36/36 |

Failed work is 36 minus committed. Direct-create failures were process errors;
direct-hook missing commits each produced a fault despite exit zero. Full
profiling introduced four synthetic C=18 hook faults and changed copied C=18
outcomes, so those rows cannot establish equal-successful-work overhead.
The campaign intentionally exited nonzero after saving/rendering all evidence.

For equal-outcome copied C=1 cases, observed F−B medians were **+23.12 ms create,
+38.81 ms direct hook, −31.21 ms lane hook**. These are contrasts, not isolated
causal overhead estimates: fixed order and host/cache variation are visible in
negative contrasts, including synthetic create. No negative contrast is claimed
as a speedup. Full sampling/trace/profile flushing perturb the subject; stage-only
measurements and profile-backed stacks are interpreted together, not interchanged.

All twelve lane cases passed an independent retrospective routing audit: orderly
daemon exit, no fallback diagnostic, lane records=SQL committed=offered=36.
Thus these were genuine served-lane writes, not silent auto-mode direct fallback.

## Wall regions: stage-only mode

Inclusive, overlapping regions; **do not sum these medians or concurrent waits**.
The open region includes migration checks, activation and built-in registration,
not just SQLite file open. All values below are milliseconds.

- Copied create C=1: open **5.91**, system identity **1499.52**, task
  preparation/apply/readback **63.20**, task total **1566.98**; Go write gate
  about **0.001**. The expensive identity path is not the open path.
- Copied direct hook C=1: total **396.46**, open **6.40**, journal append
  **62.37**, admission/parse **0.053**. Other preparation was not separately
  clocked; CPU profiles identify its dominant stacks below.
- Copied lane C=1: queue/accumulation **25.14**, per-record preparation
  **234.33**, batch apply **63.72**, turn preparation/apply/settlement **298.85**,
  submission-through-response **323.99**. Settlement alone was about **0.001**.
- Copied lane C=18: queue p50/p95 **578.77/663.66**; per-record preparation
  **2.07/260.05** (turn-shared preparation makes its distribution strongly
  asymmetric); four batch applies p50/p95 **77.47/80.47**; four turns
  **350.51/407.93**. Thirty-six concurrent queue waits totaled **20.31 s**:
  that is aggregate waiting, not campaign elapsed time.

Copied C=18 lane cases used four batches, maximum batch size 16. Baseline maximum
turn hold was **390.10 ms**; stages **407.92 ms** with one hold-exceeded event;
full **398.43 ms**. These turn holds include preparation, not solely SQLite lock
ownership. **Bounds tuning/enforcement is not complete**, and no knob was changed.

## Ranked profile-backed costs

Percentages below are inclusive sampled CPU within their named profile, not wall
percentages; ancestors/descendants overlap. Exact symbolized commands are indexed
locally, with matching binaries and the rendering-source snapshot.

1. **Task identity replay verification.** Copied C=1 create: 6,696 CPU samples,
   **66.96 s sampled CPU** across 36 processes. Stack:
   `trackerImpl.Create → systemSession → validatePersistedGenesisAuthority →
   borrowedJournal.Apply → committedOutcomeForExisting → verifyFactContextIntegrity
   → validateCanonicalFactContextSets → DecodeCanonicalMutation → canonicalJSON`.
   The integrity branch accounts for **55.37 s / 82.69%** sampled CPU. Source
   inspection confirms the existing-operation branch verifies canonical context
   sets; this is not inferred from an SQLite wait. Alloc-space reports about
   **39,723 MB aggregate**, with **98.9%** through system-identity replay — not
   one process's resident or peak memory.
2. **Hook definition/metamodel preparation and evidence reads.** Copied direct
   C=1 hook: 1,554 samples, **15.54 s CPU**. `Service.PrepareReceipt` accounts
   for **73.62%**; `finalizePreparedBatch → BridgeActiveMetamodel →
   journalDefinitionReader.QueryEvidence → readFactPageInTransaction →
   modernc sqlite3_step` for **55.28%**. SQL stepping is CPU here, not a measured
   writer-lock wait. Alloc-space reports **2,413 MB aggregate**; normalization
   (`canonicalJSON`) accounts for **69.49%**, pointing to allocation-heavy
   canonical definition construction as well as SQL work.
3. **Lane preparation remains dominant, with queueing under bursts.** Copied
   C=18 daemon: 166 samples, **1.66 s CPU**; served `Prepare` **65.66%**,
   evidence reads **53.61%**, `ApplyBatchContext` **19.88%**, canonical JSON
   **18.07%** (overlapping). C=1 daemon supplied 1,133 samples / **11.33 s CPU**.
   Block profile shows **24.22 s aggregate** in `Lane.Submit` result-channel
   waiting, while the mutex profile totals only **9.74 ms** and is mostly Go
   runtime locks. Neither figure is SQLite writer-lock elapsed time.

## Artifacts, verification and limits

The retained private campaign has **96 SVG + 96 HTML** function/stack flames,
CPU/in-use/alloc-space/alloc-object/block/mutex profiles, symbolized flat and
cumulative tops/full stacks, goroutine protobuf/text dumps, **436 original
execution traces**, and 16 representative trace-decoder validation records with
scheduler/sync/syscall extracts. Thin lane clients supplied only 1–6 CPU samples
per case: too sparse to rank client CPU costs; daemon sampling is the useful
write-path evidence. Flame width is its explicitly named sampled metric, never
elapsed wall time or literal call count. Unknown-stack samples retain their weight.

All child processes were reaped, no sandbox socket remained, and no profiling
HTTP listener was started. Operator state was used only as the original read-only
backup source; all experimental writes/faults/runtime/config lived privately.
Raw artifacts and actual private paths are not committed. Independent review
accepted the routing proof; seven Python tests, focused Go race suites (including
writeprofile, writelane and receipt), full `go vet ./...`, CGO-disabled
`go build ./...`, gofmt and diff checks passed. No codegen input changed.

Still unmeasured: native-host executable discovery, pre-main initialization,
separate pool checkout versus SQLite writer-lock wait, internal fold/projection
and integrity-gate wall partitions, COMMIT-call wall time versus physical fsync,
transport-response timing, active-load goroutine snapshots, peak/post-GC retained
heap, cold activation, different event mixes and longer-load/other-machine tails.
The combined region names intentionally expose these missing boundaries. Nothing
in this campaign optimizes production or completes the later bounds-tuning work.
