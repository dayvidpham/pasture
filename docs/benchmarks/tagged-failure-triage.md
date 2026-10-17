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

## Follow-on pool-of-one fixture boundary

The sequential tagged tasks package next exposed
`TestGateReaderCompletesOnAPoolOfOne`: `GateReadStageClaim` reported a context
deadline. That stage precedes the public ownership call, so this was not the
nested ownership call waiting on a retained claim lease. The21.73s whole-test
time does not locate the expired read within that test.

All actor/task/assignment/claim preparation already preceded Snapshot. But
`countGateStore` then replaced the initialized production handle with a new
**lazy** `sql.OpenDB(gateCountingConnector)` pool. Its first connection was
not opened until the ingress-bounded claim query. The test connector calls
modernc's `driver.Open` with DSN PRAGMAs; that is fixture connection
initialization, not a retained lease or a definition activation. Unlike this
counting handle, the real production opener has already initialized its pool.
The original test's comment correctly states that Provenance still borrows the
original pool; the separate shared-pool hazard proof remains unchanged.

The repair initializes only the counting connection before Snapshot, using a
separate setup context. It asserts max connections1, exactly one Connect,
idle1/in-use0 before the actual reader, exactly one claim SELECT, no new
Connect or queued lease during the reader, and in-use0 afterwards. Snapshot
still derives the exact original one-second production ingress deadline; the
existing30-second outer completion ceiling is unchanged. No fixture SQL,
receipt or definition is manufactured and no production source is changed.

A controlled1100ms connection-initialization cost records these actual phases:
the old fixture enters Connect with999.99ms remaining, returns after1.132s
with its context expired, then attempts SQL with negative132ms remaining and
fails at the same claim-read boundary. The repaired fixture enters Connect
under the separate30s setup context and SQL with999.99ms of the original
claim budget still available; it passes. This demonstrates a real fixture
boundary defect, not the cause of the historical untraced scheduler delay.

An independent held-lease mutation takes the initialized counting pool's only
connection immediately before Snapshot. The repaired proof still fails after
1.0008s, reporting exactly one Connect, in-use1 and WaitCount1 instead of
misidentifying the failure as initialization. Thus preparation cannot hide
actual pool starvation. The real shared-pool borrowing/release tests remain
required alongside this regression.
