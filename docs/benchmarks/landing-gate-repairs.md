# Release-gate repair ledger

The accepted candidate `602ea83` is preserved. Its original full default,
experiment-tagged and recovery race suites each exited1; passing narrow retries
do not erase those failures. Default/tagged vet, CGO0 builds, Python, two-pass
generation/drift and Nix evaluation exited0. All original logs remain private.

The complete machine-readable inventory records every failed test/subtest,
original line number and diagnostic in `failure-inventory.json` alongside the
retained release-gate logs. There were thirteen distinct top-level failures:

| Test | Gate(s) | Original diagnostic / disposition |
|---|---|---|
| `TestTheDaemonLaneBatchAdvancesTheVerifiedLedgerSoTheNextOpenIsFast` | default | Expected one occurrence; zero found. Definition setup is now separate; empty diagnostic plus current production-kind selectors prove actual receipt settlement. |
| `TestSeparateAssetEntryPointsRejectWithoutStageWrites` | default/tagged | All three standalone generators stopped at unrelated workspace membership instead of the required operational-gate rejection. Fixed fixture-module environment. |
| `TestSliceSubWorkflow_HookSliceFailed` | default | Hook condition not met within10s; repaired durable queue-slot synchronization, not best-effort hook timing. |
| `TestAbandonedInvocationNeverLeavesAnOccurrenceWithoutItsBlob` | default | Fixture pre-activation expired while leasing a connection; setup now has its own bounded context, with original strict occurrence budget preserved. |
| `TestSliceStartHonestFailureUnderInjectedDelay` | default | Fixture pre-activation transaction hit caller deadline; definition setup separated from the measured occurrence failure. |
| `TestALostAnswerLeavesTheRecordCommittedAndTheHookReportingRecordUnknown` | default/tagged | Durable occurrence not observed; disconnect now follows successful preparation and actual settlement is joined and checked using current production kinds. |
| `TestGoCompilerRejectsTargetNeutralHostContractImport` | default/tagged | External fixture compiler rejected `-mod=mod` in an unrelated workspace before checking internal-import legality. Fixed fixture-module environment. |
| `TestTheWriteLaneIsNeverAskedWhenDisabled` | tagged | Expected one direct-path occurrence; zero found. Definition setup separated; empty diagnostics exclude fail-open exit0, and durable current-kind evidence remains required. |
| `TestMigrateContextRealWriteLockCancelsWithoutReleasingHolder` | tagged |604.328ms exceeded600ms bound; package-serial native timing probe retains exact25ms caller/600ms ceiling and real locked holder. |
| `TestSliceSubWorkflow_CompleteSignalOverridesResult` | tagged/recovery | Success remained true and forced-failure override absent; repaired durable queue-slot ordering before both signals are sent. |
| `TestEngineShutdown_NamesTheWorkStillRunningWhenTheBudgetExpires` | tagged |5.287s exceeded5s ceiling; real wall-clock test now runs before this package's parallel engine load, with unchanged3s budget/5s ceiling. |
| `TestNativeLifecycleOpenerWorkerFinishesUnderRealLock` | tagged |612.212ms exceeded600ms bound; fixture input construction moved before the timer and package-serial native timing retains exact original ceiling. |
| `TestEngineAuditTrail_UsesTheConfiguredSQLiteLockBudget` | recovery | Tight caller subtest took206.985ms versus100ms allowance; unchanged base failed at189.147ms. Package-serial real25ms probe plus deterministic cancellation-after-BUSY proof retain original100ms limit. |

## Independent fixture modules

Copied generator trees and intentionally external compiler fixtures have their
own `go.mod`. Their child Go commands must resolve that module, not inherit the
parent integration workspace naming a different source directory. Child-only
`GOWORK=off` fixes that boundary; the outer integration remains in its explicit
workspace. Generator rejection text, rejected import and unchanged-stage-tree
assertions are unchanged. The inner test command retains race/shuffle/fullpath
and timeout flags.

The exact workspace errors were reproduced on unchanged base `5d8d654`, using
published dependencies and a workspace naming only that base. Focused tests pass
with the boundary repaired. Removing the child isolation from the repaired tree
makes both tests fail again for the original workspace errors; restoring it
passes. This is environment isolation, not bypassing patched-store integration.

A focused Nix probe then exposed the independent clone's missing offline vendor
closure: Go stopped at inconsistent vendoring before running the generator.
When the parent has a verified vendor tree, the fixture now copies it along with
the module sources. It does not disable vendor mode or download a replacement.
The complete before/after byte-map assertion still includes those dependencies.
The original focused Nix probe is red; the repaired probe passes, without being
represented as the final complete Nix check.

## Engine signal ordering

The old recording handler published its observed hook only after its expiring
gate returned. Waiting for that record therefore waited for the4s hook-dispatch
timeout, not for a held workflow. An optional completion override sent afterwards
could legitimately miss the production1s receive window. The repair holds the
sole real queue slot in a fixture workflow, durably sends both signals, confirms
the target remains ENQUEUED, then releases execution. Hook callbacks remain
best-effort observers, never a false workflow synchronization primitive.

Ignoring the actual received override in a production overlay makes both repaired
signal tests fail. An independent double-spend shutdown mutation still fails
against the unchanged5s ceiling. Focused published-pin tests pass; full default
and tagged engine checks passed in the slice, while its recovery check exposed
another existing expiring-gate recovery fixture,
`TestRecovery_RecoveredSlicesObeyTheLimitTheRestartedEngineSets`: a supposedly
held slice had already finished, so the attempted pending update affected zero
rows. That fixture now uses a cancellable durable step on the real slice queue,
holding execution before the production body. Shutdown joins every old owner;
read-only assertions confirm truly pending rows before restart. No completed
work is rewound and no workflow status is rewritten to manufacture recovery.
The same delayed-observation control breaks the old gate and passes the repair;
retaining the old queue limit in a production mutation still fails the stored
limit assertion. Original slice-level recovery failures remain preserved.

After integrating the engine and budget repairs, the complete combined
`GOWORK=off` published-pin recovery engine package check passes (engine103.074s,
budget16.635s). This is a new exact-source package result, not reclassification
of the original failed candidate suites; final whole-tree gates remain required.

## Cancellation and setup ownership

Definition activation is setup, not the occurrence append under test. It now
uses a bounded30s context on a value copy of the service, before taking the
occurrence lock; the original service still has exactly250ms for its measured
append. A forwarding-journal regression observes actual setup contexts and
asserts the original service is not mutated. Removing setup isolation reproduces
definition activation deadline failure. The blob-first abandonment fixture
still cancels after the real blob commit and joins the worker before tracker
close; dropping the real blob write makes its orphan/FK assertions fail.

Native migration and opener tests retain25ms caller deadlines and600ms ceilings;
the audit writer retains25ms and100ms. Native timing probes now precede competing
package-parallel fixtures; no global test concurrency or production timeout is
changed. The actual migration calls still take about502–503ms because modernc's
interrupt goroutine cannot preempt SQLite's standing500ms busy-handler sleep.
The audit writer independently clamps its reserved connection's busy window to
the caller budget and retains its cleanup allowance. No evidence justified
changing that production path.

A new real-store audit proof reaches typed SQLite BUSY while the independent
holder remains locked, pauses at the existing retry seam, explicitly cancels,
and requires join within the original100ms, exactly one attempt, typed context
cancellation and no event addition before lock release. The real25ms timer case
remains alongside it. Ignoring the caller context in a production mutation
fails both proofs; no broader error was accepted to make the gate pass.
Published-pin focused real25ms writes measured25.79–28.02ms. These focused
results do not retrospectively prove the cause of untraced historical scheduler
delays or clear the original full suites; final combined gates are required.

## Lane receipt and lost-answer fixtures

Native hook exit0 can mean a fail-open fault, not an acknowledged receipt.
The repaired command fixtures initialize immutable definitions before the
measured request, using a bounded setup context on a value copy of the service;
production request budgets and original service settings are unchanged. They
require empty diagnostic stderr as well as the host's exit and output contract.
Evidence queries now use the production occurrence/interpretation kinds rather
than obsolete v1/v2 literals: exactly one occurrence, interpretation and
consultation must belong to the same receipt operation.

The lost-answer fixture now disconnects transport only after preparation has
actually succeeded, before the real receipt apply. It captures settlement's
error, joins it, and checks the real committed operation rather than treating
the hook's record-unknown answer as proof of commit. The no-answer behavior and
durability assertions remain separate. Negative controls for stale selectors,
commit failure, routing bypass and fail-open exit0 each fail. The handoff's
deliberate undeclared-event mutation was restored before validation. Exact
strengthened default tests (twice), actual `pasture_experiment` focused tests,
CGO0 vet/build and formatting pass against the published pin. The worker's
misnamed tagged log used unrelated profile tags and is not tagged-gate evidence.
No production defect was established by these fixture failures; final full
combined gates remain the release criterion.

## Additional failures exposed by integrated published-pin gates

The first integrated published-pin default race suite passed on `0f3f4c0`;
the tagged suite failed. Those results remain distinct, with no full-pass claim.
Two additional tests were exposed:

* `TestInProcessLaneRuntimeStaysInsideTheTestRuntimeDir` observed a new lock
  file in the shared operator runtime. Its all-files snapshot could not
  attribute another process's write to the tested runtime. Both isolation
  tests now model the inherited ambient runtime with a private sentinel,
  then require the real runtime to use a separate private destination. Exact
  socket-path and complete no-growth assertions remain. A deterministic
  unrelated-writer control breaks the old test and passes the repair;
  removing the actual overrides still makes both tests fail. The source of
  the historical foreign file is not proven, and no operator file was removed.
* `TestAuditWriterBudgetIncludesAllAttempts` exceeded its original115ms
  allowance. Its2.32s report was whole-test time, not measured write time;
  the original message did not record elapsed or attempt count. Production
  derives one deadline before all attempts; no reset was established. The
  real40ms probe retains its40+75ms ceiling and typed deadline error, now
  package-serial with measured diagnostics. A new deterministic proof drives
  three real transactions, checks exactly equal deadlines on every attempt,
  proves rollback by reusing one primary key, cancels synchronously, and
  checks zero committed rows. A per-attempt deadline-reset production mutation
  fails this proof. Focused real probes measured40.07–41.39ms; default/tagged
  focused repeats passed. The original full-tagged failure remains preserved.

The next exact-source default gate (`9e97f78`) exposed
`TestAuditWriterReplacementConnectionUsesThisBudget`. Its original25ms caller,
100ms ceiling, typed deadline failure, held writer, subsequent successful write
and standing500ms read-back remain unchanged. The old already-WAL holder proves
writer-BEGIN contention, not necessarily connector setup contention; its2.23s
whole-test report did not record write elapsed. No historical scheduler cause
or production connector bypass is established by that report.

Source inspection confirms one caller-bounded operation context reaches
`db.Conn(ctx)`. The shared connector deliberately avoids modernc DSN PRAGMAs
(which use Background), clamps setup's busy timeout to the remaining caller
deadline, runs setup/read-back with that context, and restores standing policy
before publication. The writer separately reclamps its reserved connection.
Synchronous driver Open/Close have no context API; a stalled filesystem is not
bounded by this SQLite-contention policy. MaxIdleConns(0) closes the failed
connection, so the subsequent write checks a NEW retained connection, not reuse.

The repair isolates eight qualified native/wall-clock budget probes from the
package's parallel SQLite fixtures, retaining the actual concurrent writers
inside concurrency proofs; unrelated content/transaction tests remain parallel.
An explicit guard includes nested parallel subtests. A new real DELETE-journal
read holder prevents WAL conversion, isolating connector setup from audit BEGIN.
It retains the same25ms/100ms bound and typed deadline error, refuses publication
while locked, then requires WAL and standing500ms after release. Removing ONLY
the connector deadline clamp makes this proof fail at502.52ms; the old connector
test still passes that mutation. Ignoring ONLY the writer clamp breaks the
original replacement proof at501.63ms. Detaching acquisition from the caller
breaks a separate deterministic dependency-context/cancellation pin.

An unchanged `9e97f78` worktree with the same published dependency passes focused
audit tests; it does not clear the preserved full-suite failure. Restored focused
default/tagged repeats pass: native setup25.53–26.17ms and replacement25.62–26.79ms.
These results identify missing test isolation/attribution and strengthen the
actual acquisition guarantee without claiming a proven historical scheduling
trace. Production connector/writer sources are byte-identical.

Nix package and check derivations now explicitly set `GOWORK=off`: an outer
shell variable does not enter a sandbox. Race/installer CGO overrides merge the
common environment rather than replacing it. Derivation inspection verifies
off for all three, with CGO0 for the package and CGO1 for both checks; full
package/check builds remain part of the exact-tree validation, not inferred
from evaluation alone.

## Full Nix findings after the successful Go wave

On `1e010ad`, default/tagged/recovery race, vet/build, module verification,
38 Python tests, two-pass generation/drift, Nix evaluation/packages and the
actual packaged v0.0.21 version equivalence check passed. Full Nix race still
failed with five additional top-level findings; its complete log and all17
including route subtests remain preserved, not replaced by focused retries.

* The generated Codex conduit dependency used `/usr/bin/env`, absent in the
  hermetic sandbox, yielding126 instead of7 on12 routes. Resolve that stand-in's
  shell from PATH, while executing the same committed generated runner and
  retaining exact exit/stdout/stderr/argv/raw-stdin assertions. A focused Nix
  check passes; restoring ONLY the old interpreter fails with the actual
  bad-interpreter diagnostic.
* The two-daemon migration fixture inherited ambient runtime state or used an
  overlong socket sibling of its long temporary database path. Both children
  now share one short private0700 runtime, with an actual elected Unix socket
  required. A padded private TMPDIR reproduces both base bind failures
  deterministically and passes the repair; no lane is disabled or loser/winner
  assertion relaxed. This also prevents those children using operator runtime
  state; it does not prove which process created the earlier foreign lock.
* The real-BUSY cancellation proof failed during its unrelated warm-up write's
  tight cleanup reserve, BEFORE the cancellation experiment. Bounded fixture
  SQL now seeds the same committed agent/software/event/epoch-edge state and
  publishes its warm cache only after commit. All SQL/commit errors are required
  to be absent. The measured writer still uses the original profile, cleanup,
  typed contention, synchronous cancellation,100ms join and one-attempt rule;
  four retained-row checks strengthen the unchanged event-count assertion.
  Ignoring the caller or
  injecting measured cleanup failure still makes it fail.
* The malformed record-only envelope proof failed at lazy owner activation's
  definition error surface, not occurrence append. Its setup service value
  copy now activates exactly four occurrence owners, never an interpreter,
  before the original2s receipt. New pins check budget preservation, exact
  owner references and no new definition during Receive. Actual Nix receipt
  package checks pass; budget-widening and fabricated-semantics controls fail.
* The load tripwire committed776/776 with zero faults but measured75.8events/s
  below the unchanged80 floor. Unchanged `9e97f78` in a standalone Nix probe
  measured104.4 with1062/1062; this is NOT a comparable full-suite pass or a
  historical scheduling trace. Nix now prepares race build/cache state before
  its UNCHANGED full race command; preparation executes binaries/TestMain but
  is not suite evidence. Independent Nix builders are validated serially,
  without restricting Go test parallelism, enabling report-only/CI relaxation,
  skipping a test or changing the throughput floor. Complete final Nix success
  remains required; these setup changes cannot guarantee a contention-free host.

All production Go sources remain byte-identical to `9e97f78`. New fixture
coverage and build-environment changes must be included in final exact-source
validation and independent review, rather than attributed to the earlier wave.

The next complete wave on `6f15d41` again passed default/tagged/recovery race
and all non-full-Nix gates, including the packaged version check. Full Nix
still measured68.7events/s against80, with704/704 committed and zero faults.
Cache preparation and serial independent builders did NOT clear that failure;
no performance assertion, CI factor or test selection was relaxed.

That Nix run also exposed two post-commit command fixtures which had not
reached their intended boundary, plus an oversize-record statistics race:

* Codex's output-write proof had zero occurrences; OpenCode's panic proof had
  not called its post-commit seam. Controlled cold-definition delay produces
  actual fail-open definition-deadline faults on the base and passes both
  repaired fixtures. Setup initializes five immutable owners for each actual
  Codex/OpenCode contract on a bounded service copy; original1s/5s request
  settings are unchanged. No receipt is manufactured. Exact atomic receipt,
  empty-fault/actual-Outcome diagnostics and no-owner-growth pins strengthen
  the original assertions. Actual Nix focused default/tagged checks pass;
  bypassing the panic barrier or suppressing stdout writes still fails.
  These controlled results do not identify the original unlogged fault cause.
* Lane `Records` is documented to precede ACK, while whole-turn counters
  follow it. The oversize test incorrectly read both immediately after ACK.
  It now retains the immediate record-count assertion, joins the real lane,
  then requires one record, one batch and maximum batch size one. Delaying
  turn publication reproduces the original zero-batch failure and passes the
  repair; removing actual maximum publication still fails after the join.
  Production ACK/statistics ordering is unchanged.

These are focused corrections, not retroactive full-Nix clearance. The next
exact-source wave must establish whether the unchanged throughput requirement
holds. If it does not, isolating the performance phase requires an explicit
decision: never silently omit it, lower its floor or call standalone success
a passing full race check.

The operator's final version correction is **Pasture v0.0.21**; earlier
unpublished v0.1.0/v0.10.0 candidate suggestions are superseded. Provenance
remains the already published v0.10.1. Metadata correction does not turn any
interrupted or failed gate into a pass; final evidence identifies the corrected
exact integration tree. No dedicated version-command tests were added.

## Completion rule

### Approved separation of measurement and race correctness

On 2026-10-16 the operator explicitly approved separate required phases, not
weaker performance thresholds. See `validation-phases.md` for exact selection,
coverage and local/Nix/CI/release wiring. Full race still executes the complete
load workloads; controlled non-race measurement subsequently enforces the
unchanged timing bands without competing race packages. The focused controlled
run measured 94.4events/s (956/956 committed, zero faults), hook p95 73.91ms,
acceptance p95 65.10ms and relative0.47. This is not final full-gate clearance
or proof of the historical contention cause.

The completed `efd4bb2` Nix failure also exposed an additional hold-statistics
reader after ACK. It now joins the lane before checking whole-turn counters.
A delayed-publication control breaks the old reader and passes the joined
reader; suppressing HoldExceeded still fails. Production publication/ACK order
is unchanged. The session-claim silence proof now prepares immutable owners on
a bounded service copy, retains the original receipt deadline, and runs before
unrelated package-parallel fixtures. It additionally requires exactly two real
occurrences and read-back of the same stored actor. Suppressing the actual
claim write fails. The original log proves a receipt append deadline, not a
claim-rendering failure; it does not isolate which historical scheduling cost
caused that deadline.

The first split-phase full default run on `dc5fda8` failed the sibling
session-claim mismatch proof at its control receipt's commit deadline. The
same owner preparation and package-local sequencing now cover all three
end-to-end diagnostic scenarios, including the real trigger-driven lost race;
the pure renderer matrix remains parallel. The complete handlers race package
passes after this correction. Original full-run failure remains preserved;
it is not reclassified by that package result.

The same Nix log contains a whole-package default10-minute timeout while the
lineage over-cap test was still running. Nix now explicitly uses the existing
local/CI30-minute whole-suite ceiling; no operation deadline or assertion is
changed. Exact final full phases remain required and failures remain retained.

Do not run another full wave while deterministic failures remain. Each remaining
failure needs an explicit causal disposition, preserved invariant and focused
sensitivity proof. Keep production20records/25ms and timeout/failure-policy
defaults unchanged; do not widen a ceiling, skip a test or accept an error to
turn a gate green. After integration and verified publication of the actual
upstream dependency, adopt the real pin/checksums/vendorHash and run exact full
default/tagged/recovery gates with `GOWORK=off`, then three-axis final-tree review.
No release-ready claim follows from this repair ledger alone.
