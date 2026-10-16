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
| `TestAbandonedInvocationNeverLeavesAnOccurrenceWithoutItsBlob` | default | Fixture pre-activation expired while leasing a connection; setup/cancellation investigation. |
| `TestSliceStartHonestFailureUnderInjectedDelay` | default | Fixture pre-activation transaction hit caller deadline; setup/cancellation investigation. |
| `TestALostAnswerLeavesTheRecordCommittedAndTheHookReportingRecordUnknown` | default/tagged | Durable occurrence not observed after the hook stopped waiting; durability/fixture synchronization investigation. |
| `TestGoCompilerRejectsTargetNeutralHostContractImport` | default/tagged | External fixture compiler rejected `-mod=mod` in an unrelated workspace before checking internal-import legality. Fixed fixture-module environment. |
| `TestTheWriteLaneIsNeverAskedWhenDisabled` | tagged | Expected one direct-path occurrence; zero found. Investigate full Outcome, not native fail-open exit0. |
| `TestMigrateContextRealWriteLockCancelsWithoutReleasingHolder` | tagged |604.328ms exceeded600ms bound; cancellation/measurement investigation. |
| `TestSliceSubWorkflow_CompleteSignalOverridesResult` | tagged/recovery | Success remained true and forced-failure override absent; repaired durable queue-slot ordering before both signals are sent. |
| `TestEngineShutdown_NamesTheWorkStillRunningWhenTheBudgetExpires` | tagged |5.287s exceeded5s ceiling; real wall-clock test now runs before this package's parallel engine load, with unchanged3s budget/5s ceiling. |
| `TestNativeLifecycleOpenerWorkerFinishesUnderRealLock` | tagged |612.212ms exceeded600ms bound; cancellation/measurement investigation. |
| `TestEngineAuditTrail_UsesTheConfiguredSQLiteLockBudget` | recovery | Tight caller subtest took206.985ms versus100ms allowance; unchanged-base full recovery package also failed at189.147ms. Cancellation/measurement investigation. |

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
another existing expiring-gate recovery fixture. That newly discovered recovery
failure remains under repair; audit-lock cancellation belongs to the separate
budget repair. Those full recovery failures are not cleared by focused passes.

## Completion rule

Do not run another full wave while deterministic failures remain. Each remaining
failure needs an explicit causal disposition, preserved invariant and focused
sensitivity proof. Keep production20records/25ms and timeout/failure-policy
defaults unchanged; do not widen a ceiling, skip a test or accept an error to
turn a gate green. After integration and verified publication of the actual
upstream dependency, adopt the real pin/checksums/vendorHash and run exact full
default/tagged/recovery gates with `GOWORK=off`, then three-axis final-tree review.
No release-ready claim follows from this repair ledger alone.
