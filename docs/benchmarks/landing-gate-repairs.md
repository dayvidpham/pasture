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
| `TestTheDaemonLaneBatchAdvancesTheVerifiedLedgerSoTheNextOpenIsFast` | default | Expected one occurrence; zero found. Investigate native fault/ACK and durable completion, not exit0 alone. |
| `TestSeparateAssetEntryPointsRejectWithoutStageWrites` | default/tagged | All three standalone generators stopped at unrelated workspace membership instead of the required operational-gate rejection. Fixed fixture-module environment. |
| `TestSliceSubWorkflow_HookSliceFailed` | default | Hook condition not met within10s; repaired durable queue-slot synchronization, not best-effort hook timing. |
| `TestAbandonedInvocationNeverLeavesAnOccurrenceWithoutItsBlob` | default | Fixture pre-activation expired while leasing a connection; setup now has its own bounded context, with original strict occurrence budget preserved. |
| `TestSliceStartHonestFailureUnderInjectedDelay` | default | Fixture pre-activation transaction hit caller deadline; definition setup separated from the measured occurrence failure. |
| `TestALostAnswerLeavesTheRecordCommittedAndTheHookReportingRecordUnknown` | default/tagged | Durable occurrence not observed after the hook stopped waiting; durability/fixture synchronization investigation. |
| `TestGoCompilerRejectsTargetNeutralHostContractImport` | default/tagged | External fixture compiler rejected `-mod=mod` in an unrelated workspace before checking internal-import legality. Fixed fixture-module environment. |
| `TestTheWriteLaneIsNeverAskedWhenDisabled` | tagged | Expected one direct-path occurrence; zero found. Investigate full Outcome, not native fail-open exit0. |
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

## Completion rule

Do not run another full wave while deterministic failures remain. Each remaining
failure needs an explicit causal disposition, preserved invariant and focused
sensitivity proof. Keep production20records/25ms and timeout/failure-policy
defaults unchanged; do not widen a ceiling, skip a test or accept an error to
turn a gate green. After integration and verified publication of the actual
upstream dependency, adopt the real pin/checksums/vendorHash and run exact full
default/tagged/recovery gates with `GOWORK=off`, then three-axis final-tree review.
No release-ready claim follows from this repair ledger alone.
