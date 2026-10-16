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
