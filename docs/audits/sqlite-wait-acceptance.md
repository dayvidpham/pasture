# SQLite wait acceptance — issue 162

Run date: 2026-10-13. This is contributor integration evidence, not a release
claim or user UAT. No production code or test assertion was changed by this run.

## Tested sources

- Pasture combined fetched `origin/main`:
  `405aecbdfdb13f59e01736bff9610314f0d3117e`, tree
  `59c34231a8cbf7dd36f8cb2e6edca12d80cc39ec`.
- Actual published dependency: **Provenance v0.5.0**, source
  `6529af8728a382b82094adeefc77ef0b4ab895ce`, tree
  `1784d34c9f43b79447290de56b0988cf0cc6efe9`.
  Module sum `h1:5cb0Z8/DiOKsMeI1fKp46eWEK9kP2nDEhLXqsWgdM44=`, go.mod sum
  `h1:ao5LwQmM+gQljnlT9ZgG/NUcCxQwLVQKrZtS+a5Nkck=`.
- The expected definitions-track v0.6.0 bump was **not in that fetched main**.
  Supplemental upstream checks test published v0.6.0 at
  `35bd234e53adbbb3924c6e972316a3c9096617a3`, tree
  `45ad4e579a6588029f900a8430fc91d65c804f89`; they do not certify a Pasture
  v0.6.0 dependency combination that was not run.
- Opening fetch initially yielded Pasture
  `9ae21690b22800f4f89854f1cc8e1c9f6fb5ec0a`, tree
  `cadaf7ad6fca1b187622cc1f4a78950cf20c80e1`, pin v0.4.0/source
  `5f4d3d3e07480f20df0303db31b8aeaedaee8e9e`. Its focused/full race,
  tagged activation/recovery, format, vet, build and upstream gates passed,
  but C-owned engine/gate/registration changes were absent. These initial
  results are **not** the combined acceptance claim. After its tests finished,
  the isolated branch advanced with `git merge --ff-only origin/main`.

All repositories were tested in isolated Git worktrees. Root-checkout dirt was
untouched. `GOWORK` is empty, `go mod verify` passes, and no replace directive,
vendor patch, guessed version or local module override was used. Pasture uses
the Nix Go 1.26.3 shell; upstream uses its Nix Go 1.26.1 shell. Race execution
uses CGO=1; release-compatible builds use CGO=0.

## Seven-case evidence map

The named tests import production paths. Timing figures below are measured
operations only where explicitly logged; test-family duration includes setup
and is not misreported as cancellation latency. The named seven-case proof
families passed on the combined source/pinned dependency, including both tagged
activation cases and tagged recovery. This does not make the failed broader
upstream compatibility gate green (see Integration disposition).

| Case | Production evidence and assertions |
| --- | --- |
| 1: real hook deadline | `TestLifecycleHookReturnsInsideItsDeadlineWhileTheDatabaseIsLocked` runs the race-built native CLI under an independent verified write holder, retains the holder through child return, checks host continuation/fault behavior, and caps the production 5 s tier plus an explicit 4 s child/process allowance (also below 10 s). A separate recorded integration launch measured **6.026598006 s**, exit 0, empty Claude stdout, one fault, and **zero lifecycle_occurrences** after holder release. The race runtime's child-exit overhead is included, not mistaken for work beyond the 5 s cancellation tier. `TestNativeLifecycleOpenerWorkerFinishesUnderRealLock` independently observes synchronous native-worker completion with the WAL holder retained. `TestUnifiedContextOpenCancelledFirstUse` covers rollback-journal first open; `TestMigrateContextRealWriteLockCancelsWithoutReleasingHolder` covers migration. Structural hook/opener guards connect these proofs; child exit alone is not worker-completion evidence. |
| 2: caller cancellation | Audit `TestContextMigrationAndAuditPoolCancellation`, `TestOpenRetryCancellationDuringBackoff`, `TestOpenRetryCancellationPreventsAnotherAttempt`, and the complete `TestAuditWriter*` cancellation family cover expired/pre-cancelled input, pool wait, first/replacement connection, SQL, retry/backoff and cleanup. `TestAuditWriterShortColdRoleBudget` runs both DeadlineTestProfile and the production 500 ms profile with a **25 ms caller deadline + separately named 75 ms scheduling allowance**, not a 438 ms allowance. Engine `TestEngineContextFirstConnection`, `TestEngineContextFallbacks` (both migration branches), `TestEngineProjectionSetupReceivesCancelledContext`, tracker gate and cold-registration counterexamples are now in the combined tree. Engine's first-use test states its separate 150 ms dispatch allowance; this is not the forensic writer allowance. Cancellation causes are checked with errors.Is, and gate/retry tests assert no subsequent delegation/attempt. |
| 3: atomic migration/activation | `TestCancelledMigrationStepRollsBackWholeStep` cancels between the first schema/data write and final version stamp, joins rollback and reopens at the preceding committed version. Existing crash/reopen, concurrent migrator, repeated/future-schema and backfill tests run. Upstream activation cancellation phases cover integrity/replay, fresh schema/seed rollback, restoration/discard, and uncertain commit diagnostics. Existing journal integrity and projection replay suites also run; cancellation never substitutes for verification/replay. |
| 4: bounded forensic writes | `TestAuditWriterLongHolderRetries` and `TestAuditWriterLongHolderOtherModes` require verified independent locks, observed exhausted attempts, at least **750 ms** held time and success inside a **3 s** caller budget for cold/warm/well-known/dedup/session batch. Assertions check the complete agent pair/event/context, canonical dedup ID, row counts, concurrent independent trails, pool saturation, permanent SQL failures and uncertain/typed commit outcomes with no blind replay. The successful long-holder cases and still-held 25 ms failure cases are separate proofs. |
| 5: compatibility/default ceilings | Inspection pins `busyRetryCeiling` and `dbosRaceRetryCeiling` at **30 s**, with the DBOS default policy using its constant. `Migrate` delegates Background to the single context implementation; the migration context test checks the migration ceiling. CLI migrate current/explicit/dry-run byte-equality/newer-schema and migration-crash tests run. Tagged recovery covers the intentional probe stalls. Upstream `TestBorrowedActivationDefaultPolicy` separately retains the old **5 s attempt / 30 s ceiling**. Production tier/pool/fence/host-policy implementations were not changed by this slice. |
| 6: post-audit activation lock | Tagged `TestUnifiedPostAuditActivationCancellation` runs both WithCancel and deadline through the real post-audit/post-defensive-setup barrier and real Provenance activation; observes contended BEGIN, cancels while the holder stays locked, requires nil tracker/reachable cause and completion including cleanup inside **500 ms + 30 ms scheduling tolerance**. It verifies prior audit migration remains, no new journal/schema seeds or false receipt. Upstream tagged public activation, cancellation-phase, cleanup/ownership and PRAGMA tests prove retained borrowed-pool liveness, verified restoration or discard, no retained opening context, and unchanged prior data. |
| 7: first/replacement connections | `TestContextFirstConnectionLockedRollbackJournal`, `TestUnifiedContextOpenCancelledFirstUse`, and engine first-use start after real lock readiness but before the new connection. WAL first use is exercised by the native hook opener with an existing locked WAL store. `TestContextConnectRejectsCancelledAndNil`, `TestContextPoolSettingsAndReplacement`, `TestContextPoolExpansionPreservesImmediateTransactions`, and writer replacement/clamp/restoration tests cover fresh acquisition contexts, escaped URI filenames, settings on every connection and `_txlock=immediate`. Expansion runs while another real transaction owns the write lock. Raw Driver.Open/Close remain synchronous local-file operations without a context API: these tests include their completion on supported local stores, **not a guarantee against a stalled filesystem syscall**. |

Combined short cold-role measurements were **25.206464 ms** (DeadlineTestProfile)
and **26.103235 ms** (production), against the unchanged 25 ms caller deadline
and separately reported 75 ms scheduling allowance.

## Waiting-caller matrix on the combined source

Question: **does the site receive a context and select on it?** For SQL-owned
pool/driver waits, the proof is context delivery to database/sql, not a global
wall-clock ceiling. The earlier S4 matrix and authorized repair increments are
in the proposal's attributed comments; the table below states final disposition,
not the obsolete checkpoint's pending repairs.

| Site / entry | Caller, wait, timer and final disposition |
| --- | --- |
| dbconn raw Shared/Default helpers; OpenReadOnlyDB(path) | No context argument. Lazy sql.Open is not an active retry; raw modernc DSN initialization/Ping are not advertised as cancellable. Keep compatibility helpers. Traced read-only callers themselves have no context. |
| dbconn.OpenSharedDBContextWithProfile / sharedConnector.Connect(ctx) | Audit/unified/engine pass caller ctx through PingContext/acquisition and fixed driver Exec/QueryContext setup. Connector stores configuration, not open ctx; replacement Connect gets its own ctx. Initialization retries select ctx.Done within the unchanged 30 s setup ceiling. First-use, settings, replacement and expansion proofs above. |
| engine.New(ctx) shared handle / both audit fallbacks | Same shared context connector; both fallbacks use NewSqliteAuditTrailContextWithOptions(ctx), with private nil/pass-through observer pinned. Real first-use and post-schema-gate fallback counterexamples cover the delivered ctx. Existing schema-gate and DBOS timers already select ctx.Done. |
| engine projection initialization | Sole helper ensureProjectionTableContext(ctx,db) uses ExecContext. Saturated real one-connection pool counterexample and AST supplier guard. No contextless parallel implementation. |
| audit context open / MigrateContext / runStep | Connector, setup/version/schema SQL and BeginTx receive ctx. Open/migration timers select cancellation; runStep transaction/stamp/rollback boundaries unchanged. Background compatibility functions share one body. |
| audit event/session writes and session reads | One operation context is derived at method entry. It governs Conn, transaction attempts, SQL and backoff. Whole-transaction BUSY/LOCKED retry, local remaining-budget cap and restore/discard; canonical ID/cache only after confirmed commit. QuerySessionEntries uses QueryContext rather than a write retry. |
| tasks context audit/AttachContext/TransferTaskAssignment/registration | Same writer token shared with legacy calls. lockWriteContext selects ctx.Done and rechecks after token acquisition before unchanged-ctx delegation; no detached waiter. Six strict fixture cases and retained-owner counterexamples. Lazy schema setup also receives method ctx. |
| tasks cold well-known registration/binding | OpenTaskTrackerContext -> RegisterWellKnownAgents -> ensureWellKnownAgent carries ctx into RegisterSoftwareAgentContext, same cancellable gate, checked upstream capability, borrowed PingContext/bindScope/registration SQL; binding uses context SQL. Legacy registration delegates Background to the same body. Separate registration/binding transactions are **not** claimed atomic together: successful registration followed by cancelled binding can leave an unbound agent. Cold real-holder/reuse/debris proofs and upstream registration lease/post-insert rollback tests cover the repaired wait. |
| SetAgentCategories / AgentCategories | No context parameter; no claim that they obey one. Preserve interface absent a proved waiting caller. Category binding on the cold context path is separately context-aware. |
| tasks.ResolveDBPoolSize | Integer/environment resolution only, no SQL/acquisition/timer. Preserve default pool size one; actual saturation proofs belong at SQL/gate sites. |
| acceptance.SnapshotFile(ctx) | PingContext, BeginTx and QueryContext deliver ctx; no manual retry. Existing read-only/canonical/byte-budget/pre-cancelled tests run. Raw read-only DSN first-use initialization and CPU sorting are not certified as fully cancellable without a waiting-caller counterexample. |
| recovery-probe | Background constructor and deliberate stall callbacks/parent kill watchdog are crash stimuli, not received-context cancellation proofs. Retain tagged recovery evidence. |
| migrate-crash | Sole ctx supplier is Background; main transaction uses it, promotion setup remains contextless. Preserve intentional os.Exit-before-commit/version proof, not a speculative production API expansion. |
| Provenance borrowed activation | OpenBorrowedSQLiteContext -> path/Ping/Conn -> scoped activation schema/integrity/replay SQL all receive ctx. Attempt timer selects ctx.Done; active cap reserves shared bounded cleanup/verified restore/discard. Caller pool ownership and default 5 s/30 s compatibility remain; returned tracker does not retain opening ctx. |

## Commands and logs

Raw evidence is retained in `/tmp/opencode/162-D/`; final log hashes and outcomes
are recorded alongside this report. All shell invocations below use the
repository's own `nix develop --command sh -c '...'` unless noted otherwise.

Pasture combined commands:

```sh
CGO_ENABLED=1 go test -race -count=1 -timeout 30m -v \
  ./internal/audit ./internal/tasks ./internal/handlers \
  ./internal/engine ./cmd/pasture ./internal/dbconn
make fmt
make lint
make -B build
CGO_ENABLED=0 go build ./...
go mod verify
make test-race
CGO_ENABLED=1 go test -race -count=1 -tags provenance_testhooks -timeout 10m -v \
  ./internal/tasks -run 'TestUnifiedPostAuditActivationCancellation|TestOpenContextWrappersAndProductionActivationWiring'
CGO_ENABLED=1 go test -race -count=1 -tags recovery -timeout 15m -v \
  ./internal/engine -run Recovery
make check-operational-tracker
CGO_ENABLED=1 go build -race -o /tmp/opencode/162-D/pasture-race ./cmd/pasture
nix flake check --no-build
```

The separate measured binary invocation initializes an isolated file with the
built `bin/pasture --db FILE --format json task list`, obtains an independent
Python sqlite3 `BEGIN IMMEDIATE` plus DDL write, then synchronously runs the
race-built `--db FILE hook lifecycle --harness claude-code --event SessionStart
--host-version 2.1.261` with the committed cleared Claude SessionStart fixture.
It measures monotonic elapsed time, keeps the holder until child completion,
then rolls back/closes and queries lifecycle_occurrences. The exact output,
fault line and elapsed measurement are in `binary-real-lock.log`. The subprocess
has a 12 s kill/join safety ceiling; it did not fire. This launch supplements,
and does not replace, the independent in-process completion tests.

Provenance pinned-source and supplemental-source commands:

```sh
CGO_ENABLED=1 go test -race -shuffle=on -fullpath -timeout=20m -v ./...
CGO_ENABLED=0 go build ./...
go vet ./...
ast-grep scan --config sgconfig.yml --globs '!vendor/**' --globs '!worktree/**' .
test -z "$(gofmt -l .)"
CGO_ENABLED=1 go test -race -shuffle=on -fullpath -timeout=20m \
  -tags provenance_testhooks -v ./... \
  -run TestBorrowedActivationPublicCancelDuringContendedAttempt
nix flake check --no-build
```

No repeated upstream `-count` wave or non-race tests were used. Full root tests
include ownership, integrity, replay, activation contention and SQL architecture
guards. Nix checks above are evaluation-only, not claims of Nix-built artifacts.

## Integration disposition

**Acceptance is blocked by the actual pinned-source full race gate (exit 1).**
Original evidence is preserved in `provenance-v050-full-race.log`; no test was
softened, skipped, or rerun to hide it. Two failures:

```text
governed_allocation_integration_test.go:920:
TestFusedGovernedAllocationComposedRejectsStructurallyForgedSQLiteReceipts/deleted_activity_subtype/reopen
commit fixture: provenance.FusedGovernedAllocator.RunAllocateComposedBatch:
retrieve DBOS workflow result: DBOS Error WorkflowExecution: Workflow
forged-receipt-original-deleted activity subtype execution error: decoding step
result to expected type allocation.ComposedResult: failed to decode json data:
decode composed governed allocation result: decode governed operation closure:
unsupported or structurally incomplete closure; restore a valid version-1 operation output

internal/fusedtx/fusedtx_integration_test.go:86:
TestCallbackFailureRollsBackSentinelWithoutSuccessfulCheckpoint
workflow error = database is locked (5) (SQLITE_BUSY), want callback failure
```

Pinned upstream root failed in 271.619 s and fusedtx failed in 5.242 s; its SQLite
package passed in 98.932 s, including activation/integrity/replay tests. Pinned
static build/vet/AST/format/Nix and tagged public activation passed. Supplemental
v0.6.0 full race passed (root 253.932 s, SQLite 62.586 s), as did its static and
tagged gates. That later version's pass does not erase v0.5.0 failure or certify
an unrun Pasture pin combination. Upstream owner/supervisor must triage the
failures and resolve the required final integration gate; this slice does not
diagnose them as harmless or modify upstream production paths.

Combined Pasture focused race **PASS, exit 0**: audit 14.540 s, tasks 137.248 s,
handlers 73.317 s, engine 51.184 s, cmd/pasture 258.117 s, dbconn 1.158 s.
`make test-race` **PASS, exit 0**, with eligible unchanged-package cache reuse
reported by Go, not falsely described as a fully fresh execution. Format, vet,
forced four-binary build, all-package CGO0 build and module verification passed.
Tagged post-audit activation **PASS** (tasks 3.743 s) and tagged recovery **PASS**
(engine 23.689 s). Nix evaluation and operational sweep **PASS**; the sweep
reported 326 files, 78 historical references and **zero executable tracker calls**.

Local raw-log SHA-256 inventory (all paths beneath `/tmp/opencode/162-D/`):

| File | SHA-256 |
| --- | --- |
| pasture-combined-focused.log | `59087f15b360a067aad0f2bae3bb6c3d76b29c5a69ccc0dec335bbfc22cc67a8` |
| pasture-combined-full.log | `cdab7026f66f7997ab615400b1e026789c9f1669849a7fa0a792abcdfe74ce30` |
| pasture-combined-static-tagged.log | `a5ede4a11b55f8d7c352d77f85ca7d9d37d35bb82cd25d101cfc01ea16abfafb` |
| pasture-combined-nix.log | `ef81d4c2c4f9c409eb16ead0c9b96fd008929ba79443801b10eeb3159693d485` |
| pasture-combined-operational.log | `971fc48f076726efaefb1f45989e49307d0cc090389213c7399605982efb1278` |
| binary-real-lock.log | `a78984ec1d5ef8fa1181fdaa5163d36efdce2b79bc7774176d5b1ca3c774865c` |
| provenance-v050-full-race.log | `82cf53ca2f5563d86be1455ecf8b1b2291bce59f031d542fdb01957bb836d989` |
| provenance-v050-static-tagged.log | `a6092f869ebce5e387aa2c3e21c737f73fefaaf63ffed6dacf8028f9dd8e33dc` |
| provenance-v050-nix.log | `d60fd9a7970cb7439fbfce8f80fe13b8f4d4db74b1bb719903530f95079cdfcb` |
| provenance-v060-full-race.log | `fae3084457b878648eb299858188fb9cfb455ecf7b5fab39ba2248a32ec8c805` |
| provenance-v060-static-tagged.log | `7fc41d777368d075ec98179cb32b23409299dd26aa85a256c0dfab5670f26c8b` |
| provenance-v060-nix.log | `6a4c31855e6a325632ac2cdd64f39f174920fba464f6611c139c748d37677cad` |

Static inspection followed the real hook context -> native handler -> unified
context open -> audit setup/migration -> explicit-tier Provenance activation ->
context tracker/cold registration path, and event/batch -> single writer
budget/transaction -> SQL -> commit/cache/publication. No production mock,
placeholder or alternate test-only implementation was introduced. D owns only
this evidence artifact; production repair belongs to its original slice if a
gate fails. Independent review, final-tree landing checks and concrete user UAT
remain supervisor responsibilities. A later dependency/source merge requires
new exact-combination verification, not reuse of this report as a v0.6.0 pin
certification.
