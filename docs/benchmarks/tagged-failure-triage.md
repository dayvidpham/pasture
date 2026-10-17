# Tagged race failure triage after the CI fixture repair

The `081d15a` default race wave passed; its following tagged wave failed.
The original log and a machine-readable inventory retain every diagnostic,
subtest, original line number and phase (35 failure entries, 22 top-level
failures, plus the hung interleaving test). Elapsed whole-test time is not
measured operation latency. Another independent Pasture race wave was running;
that establishes competing workload, not the cause of each expired context.

## Concrete interleaving-owner defect

`TestGateReaderClaimInsertRacingTheSnapshotCannotTearIt/insert-at-read-boundary`
waited unconditionally for `afterClaimRead`. Snapshot can return an error before
that seam, leaving its result unread and the test waiting until the package's
30-minute timeout. The retained stack shows the test at that wait, with no
Snapshot worker left running.

The fixture now observes either the actual boundary or the owner's completed
result. An early error reports its original wrapped cause. A bounded wait
diagnoses a missing signal; it never represents a successful boundary. Cleanup
is installed before launch, releases the held seam idempotently, cancels and
joins the owner, then closes any snapshot before the store can be removed.
The actual second-connection insert and immutable-answer/read-count assertions
are unchanged. No production reader or timeout changed.

A real Snapshot with a synchronously cancelled context proves the early-error
path. Restoring the unconditional wait makes that control time out in five
seconds, not thirty minutes; the repair reports the context error immediately.
A separate real held-owner proof returns without explicit release, requiring
subtest cleanup to release and join. Changing the production unbound seal into
a bound answer still fails the original semantic assertion. These controls
exercise failure handling without replacing the actual read/insert scenario.

## Complete failure population

The table names the failing phase, not a guessed scheduler cause. The primary
failure is stated below; the retained inventory includes each nested SQLite
diagnostic verbatim. A setup failure does not exercise the later assertion.
Conversely, a genuine operation-under-test deadline must not be relabelled as
setup or widened to obtain a pass.

| Test | Phase and primary diagnostic |
|---|---|
| `TestReviewSubWorkflow_PartialVoteGateHolds` | Actual completion after third vote: DBOS GetResult timed out in review workflow. |
| `TestRecovery_OffQueueEpochWorkflowLandsOnTheReservedQueue` | Actual recovery pickup after replacement launch: condition not met within unchanged5s. |
| `TestRecovery_EpochControlWorkflowReturnsToItsOwnQueue` | Actual recovery pickup after replacement launch: condition not met within unchanged5s. |
| `TestAbandonedInvocationNeverLeavesAnOccurrenceWithoutItsBlob` | Before abandonment experiment, fixture opener: borrowed activation could not restore/read back foreign_keys because context expired. |
| `TestMetamodelBridgeResolvesActiveProducer` | Before bridge read, legacy producer activation: gated commit context deadline, subtype exclusivity verification. |
| `TestMetamodelBridgeResolvesDistinctCanonicalProducer` | Before bridge read, producer commit: gated commit context deadline, subtype exclusivity verification. |
| `TestDefinitionCommitConflictingActorIsNotAReplayException` | Before conflicting-actor write, first legitimate activation: context deadline starting reducer savepoint. |
| `TestRuntimeDefinitionTransactionReaderUsesCallerChanges` | Before caller transaction mutation, initial activation: context deadline inserting journal_operations. |
| `TestMetamodelBridgeRejectsDamagedRows` | Before each corruption, valid producer commit: deadlines during commit, authority verification or event reconstruction. All three corruptions retained. |
| `TestRuntimeActivationReopenAndNoDuplicateEvidence` | First actual activation under test: deadlines inserting operation/journal rows, before reopen. Claude and OpenCode failures retained. |
| `TestDefinitionCorruptionFailsClosedWithoutRepair` | Before deliberate corruption, initial activation: deadlines during subtype/authority verification. Both failing corruptions retained. |
| `TestV2OriginOmissionAndRawCarriage` | Before origin assertions, openEnvelopeStore metamodel setup: deadline during totality verification. |
| `TestRawOriginOccurrenceReadsBackThroughProductionStore` | Actual record-only Receive before readback: commit deadline during subtype exclusivity verification. |
| `TestReceiveCommitsGatePairWithFinalConsultation` | Actual Receive/finalization before assertions: SQLite commit deadline. |
| `TestEnsureActiveMetamodelRaceSafe` | Actual concurrent ensure: caller deadline while leasing connection; retain all writers and budget. |
| `TestRuntimeDefinitionsProductionRoundTrip` | Actual definition ensure before resolve: deadline persisting evidence; rollback also reports no active transaction. |
| `TestActiveForContractProductionRoundTrip` | Actual per-document ensure: deadline verifying actor placement. |
| `TestReceiptSettlementCoversRealCommitReturnGapCancellationAndRollback` | Before held Apply, runtime-owner preactivation: deadline persisting evidence; rollback also reports no active transaction. |
| `TestGateReaderUnboundSessionMakesNoProvenanceCall` | Actual Snapshot claim read: context deadline. |
| `TestGateReaderBoundPathMakesOneClaimReadAndOnePublicCall` | Actual Snapshot claim read: context deadline. |
| `TestGateReaderReadsUnderAHeldWriter` | Actual Snapshot claim read with retained independent writer: context deadline. |
| `TestSessionStartStoresClaimAndKeepsSystemReceiptAuthor` | OpenCode/Codex first actual receipt: deadline reconstructing slots. Claude second hook opener: shared30s caller context expired during audit open. |

No unrelated test is deleted, skipped, serialised, given a longer profile or
given a fabricated receipt by this triage. Setup and actual operation failures
stay distinct. Sequential, otherwise unchanged tagged package checks in a
quiet window must first determine whether any failure persists. A passing
focused check cannot erase the original whole-suite failure; final tagged,
recovery and both race/performance Nix phases remain required on the final
tree. No production performance or deadline guarantee follows from triage.
