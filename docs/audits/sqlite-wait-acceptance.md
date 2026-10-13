# SQLite wait acceptance — issue 162

Run date: 2026-10-13. This is contributor integration evidence, not a release
claim or user UAT. No production code or test assertion was changed by this run.

## Tested sources

- Pasture combined fetched `origin/main` (final acceptance run):
  `3aa7b2073ac96b105ff5c7648751ec43b9e560be`, tree
  `d569804288aa29af0e0cb6ffe91fab71e983410b`. The slice's evidence-only branch
  is rebased directly onto that revision and adds only this document.
- Actual published dependency: **Provenance v0.5.0**, source
  `6529af8728a382b82094adeefc77ef0b4ab895ce`, tree
  `1784d34c9f43b79447290de56b0988cf0cc6efe9`.
  Module sum `h1:5cb0Z8/DiOKsMeI1fKp46eWEK9kP2nDEhLXqsWgdM44=`, go.mod sum
  `h1:ao5LwQmM+gQljnlT9ZgG/NUcCxQwLVQKrZtS+a5Nkck=`.
- The v0.5.0 pin is the intended published acceptance dependency for this
  slice; the later definitions-track v0.6.0 bump is a separate change.
- Supplemental upstream checks also ran against published v0.6.0 at
  `35bd234e53adbbb3924c6e972316a3c9096617a3`, tree
  `45ad4e579a6588029f900a8430fc91d65c804f89`; they do not certify a Pasture
  v0.6.0 dependency combination that was not run.
- Earlier intermediate fetches during this slice yielded Pasture
  `405aecbdfdb13f59e01736bff9610314f0d3117e`/tree
  `59c34231a8cbf7dd36f8cb2e6edca12d80cc39ec` and, before that,
  `9ae21690b22800f4f89854f1cc8e1c9f6fb5ec0a`/tree
  `cadaf7ad6fca1b187622cc1f4a78950cf20c80e1` (pin v0.4.0/source
  `5f4d3d3e07480f20df0303db31b8aeaedaee8e9e`). Their gate results were
  superseded by the final `3aa7b20` run and are **not** the acceptance claim.
- `3aa7b20` adds the merged cold-registration load-tolerance follow-up
  (`626fe96`), which raises only that test's separate scheduling allowance to
  250 ms while keeping it below the 500 ms production busy tier and leaving all
  production budgets unchanged. The slice did not edit that test.

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
| 1: real hook deadline | `TestLifecycleHookReturnsInsideItsDeadlineWhileTheDatabaseIsLocked` runs the race-built native CLI under an independent verified write holder, retains the holder through child return, checks host continuation/fault behavior, and caps the production 5 s tier plus an explicit 4 s child/process allowance (also below 10 s). A separate recorded integration launch (final `3aa7b20` run) measured **6.021377 s**, exit 0, empty Claude stdout, one fault, and **zero lifecycle_occurrences** after holder release. The race runtime's child-exit overhead is included, not mistaken for work beyond the 5 s cancellation tier. `TestNativeLifecycleOpenerWorkerFinishesUnderRealLock` independently observes synchronous native-worker completion with the WAL holder retained. `TestUnifiedContextOpenCancelledFirstUse` covers rollback-journal first open; `TestMigrateContextRealWriteLockCancelsWithoutReleasingHolder` covers migration. Structural hook/opener guards connect these proofs; child exit alone is not worker-completion evidence. |
| 2: caller cancellation | Audit `TestContextMigrationAndAuditPoolCancellation`, `TestOpenRetryCancellationDuringBackoff`, `TestOpenRetryCancellationPreventsAnotherAttempt`, and the complete `TestAuditWriter*` cancellation family cover expired/pre-cancelled input, pool wait, first/replacement connection, SQL, retry/backoff and cleanup. `TestAuditWriterShortColdRoleBudget` runs both DeadlineTestProfile and the production 500 ms profile with a **25 ms caller deadline + separately named 75 ms scheduling allowance**, not a 438 ms allowance. Engine `TestEngineContextFirstConnection`, `TestEngineContextFallbacks` (both migration branches), `TestEngineProjectionSetupReceivesCancelledContext`, tracker gate and cold-registration counterexamples are now in the combined tree. Engine's first-use test states its separate 150 ms dispatch allowance; this is not the forensic writer allowance. Cancellation causes are checked with errors.Is, and gate/retry tests assert no subsequent delegation/attempt. |
| 3: atomic migration/activation | `TestCancelledMigrationStepRollsBackWholeStep` cancels between the first schema/data write and final version stamp, joins rollback and reopens at the preceding committed version. Existing crash/reopen, concurrent migrator, repeated/future-schema and backfill tests run. Upstream activation cancellation phases cover integrity/replay, fresh schema/seed rollback, restoration/discard, and uncertain commit diagnostics. Existing journal integrity and projection replay suites also run; cancellation never substitutes for verification/replay. |
| 4: bounded forensic writes | `TestAuditWriterLongHolderRetries` and `TestAuditWriterLongHolderOtherModes` require verified independent locks, observed exhausted attempts, at least **750 ms** held time and success inside a **3 s** caller budget for cold/warm/well-known/dedup/session batch. Assertions check the complete agent pair/event/context, canonical dedup ID, row counts, concurrent independent trails, pool saturation, permanent SQL failures and uncertain/typed commit outcomes with no blind replay. The successful long-holder cases and still-held 25 ms failure cases are separate proofs. |
| 5: compatibility/default ceilings | Inspection pins `busyRetryCeiling` and `dbosRaceRetryCeiling` at **30 s**, with the DBOS default policy using its constant. `Migrate` delegates Background to the single context implementation; the migration context test checks the migration ceiling. CLI migrate current/explicit/dry-run byte-equality/newer-schema and migration-crash tests run. Tagged recovery covers the intentional probe stalls. Upstream `TestBorrowedActivationDefaultPolicy` separately retains the old **5 s attempt / 30 s ceiling**. Production tier/pool/fence/host-policy implementations were not changed by this slice. |
| 6: post-audit activation lock | Tagged `TestUnifiedPostAuditActivationCancellation` runs both WithCancel and deadline through the real post-audit/post-defensive-setup barrier and real Provenance activation; observes contended BEGIN, cancels while the holder stays locked, requires nil tracker/reachable cause and completion including cleanup inside **500 ms + 30 ms scheduling tolerance**. It verifies prior audit migration remains, no new journal/schema seeds or false receipt. Upstream tagged public activation, cancellation-phase, cleanup/ownership and PRAGMA tests prove retained borrowed-pool liveness, verified restoration or discard, no retained opening context, and unchanged prior data. |
| 7: first/replacement connections | `TestContextFirstConnectionLockedRollbackJournal`, `TestUnifiedContextOpenCancelledFirstUse`, and engine first-use start after real lock readiness but before the new connection. WAL first use is exercised by the native hook opener with an existing locked WAL store. `TestContextConnectRejectsCancelledAndNil`, `TestContextPoolSettingsAndReplacement`, `TestContextPoolExpansionPreservesImmediateTransactions`, and writer replacement/clamp/restoration tests cover fresh acquisition contexts, escaped URI filenames, settings on every connection and `_txlock=immediate`. Expansion runs while another real transaction owns the write lock. Raw Driver.Open/Close remain synchronous local-file operations without a context API: these tests include their completion on supported local stores, **not a guarantee against a stalled filesystem syscall**. |

Combined short cold-role measurements (final `3aa7b20` run) were
**26.699957 ms** (production) and **27.312822 ms** (DeadlineTestProfile), against
the unchanged 25 ms caller deadline and separately reported 75 ms scheduling
allowance. The earlier `405aecbd` run measured 25.206464 ms / 26.103235 ms; both
runs stay inside the allowance and neither widened it.

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

**Final acceptance run PASSES on the exact combined tree** `3aa7b20` with the
published v0.5.0 pin. Earlier runs in this slice had recorded a pinned-v0.5.0
full-race failure; that evidence is preserved below and is not rewritten as a
pass. The load-sensitive failure families now have recorded non-blocking
dispositions: the fusedtx/`internal/fusedtx` contention family is tracked by
[provenance #44](https://github.com/dayvidpham/provenance/issues/44) and the
cold-registration allowance is the separate merged follow-up `626fe96` (its
tolerance only, not production budgets). Both are noted as flakes, not silently
dropped.

Historical failure (preserved, `provenance-v050-full-race.log` at
`/tmp/opencode/162-D/`):

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

Final fresh run on `3aa7b20` (logs under `/tmp/opencode/162-D/final/`):

- Pasture focused race **PASS, exit 0**: audit 9.188 s, tasks 116.139 s,
  handlers 68.698 s, engine 44.464 s, cmd/pasture 190.893 s, dbconn 1.169 s.
- `make test-race` is **load-sensitive on one engine test**. Fresh run 1 exited 2
  with exactly one failure:

  ```text
  --- FAIL: TestEngineAuditTrail_UsesTheConfiguredSQLiteLockBudget (0.00s)
      --- FAIL: .../caller_deadline_below_the_lock_hold:_the_write_cancels (12.30s)
          concurrency_spike_test.go:379: the write waited 155.950471ms before
          cancellation, above caller budget plus scheduler allowance 100ms;
          caller deadline was 25ms
  ```

  This is the already-recorded load-sensitive family, not a new production
  defect: the same test **PASS 3/3 in focused reruns** (`flake-focused.log`) and
  the **immediately repeated full `make test-race` run PASSED, exit 0, zero
  failures** (`full-race-rerun.log`). The earlier `3df5de0` full race also
  passed. The failing original log is retained, not deleted, and the assertion
  was not widened.
- Format, vet, forced four-binary build, all-package CGO0 build, module
  verification and the operational sweep **PASS**.
- Tagged post-audit activation **PASS** (tasks 3.775 s) and tagged recovery
  **PASS** (engine 22.868 s).
- Nix evaluation **PASS** (evaluation-only).
- Pinned Provenance v0.5.0 full race **PASS, exit 0** on source `6529af8`
  (root 224.379 s, `internal/sqlite` 71.607 s, fusedtx 6.152 s), with its static
  build/vet/AST/format, tagged public activation and Nix evaluation all PASS.
- Supplemental v0.6.0 full race/static/tagged/Nix also PASS; recorded only as
  context for the later pin bump, not as certification of an unrun combination.
- Race-built CLI real-lock launch: independent verified `BEGIN IMMEDIATE` plus
  DDL held across child completion; hook returned in **6.021 s** (5 s work tier
  plus explicit child/process allowance), exit 0, empty Claude stdout, exactly
  one fault record, **zero lifecycle_occurrences** after holder release.

The one honest caveat is that the held-lock hook test's family duration
(~10–20 s in these runs) includes building and starting a race-instrumented
child; the measured child latency is the separate `binary-real-lock.log` number,
not the family duration.

Local raw-log SHA-256 inventory (`/tmp/opencode/162-D/final/`; the earlier
blocked-run files remain under `/tmp/opencode/162-D/`):

| File | SHA-256 |
| --- | --- |
| focused-race.log | `d557396077dfbae9ecddccc026ca4d150f432ed9e10a1c2d659b19cf82bfa4cb` |
| full-race.log (flake FAIL) | `1cd5227cffc1fecb109d73f33564a13f6171a0ffcf61968b7ff83b382e8ed7f2` |
| full-race-rerun.log (PASS) | `0fcd989ce58cd202337a38948ee794402404b303338116d1c6ec563fcbd1a9c1` |
| flake-focused.log (3/3 PASS) | `078a3bc2f7371f2218d2cf27de60a8f49eeca80cadce93e433765ef97fa2caeb` |
| static-build.log | `80b8375e87788e59823ce61c0d474f54f57f98d35baeb975bc43836acb18b5db` |
| nix.log | `3c3faec41d075b8ef10e140ea7e2d7ddfe78f8289306bc67515ec97320c6cae9` |
| tagged.log | `ccdf45b83653c9631cbc853fde1d893eff1f2296a7358eae1cafa31018941049` |
| provenance-full-race.log | `20bf1e88231a391299addd63e45681083cd814eba4c13fb37a835204ab36d8f8` |
| provenance-static-tagged.log | `8c18e1a0613da6dfa8f0692a4c26fb1b3a346c9e9dfa3c6478276b14f4d61f21` |
| provenance-nix.log | `1d230339bb62a1154ee92706953ff05dba55b059ee7815aa56f6963171d066bf` |
| binary-real-lock.log | `4ae0a2299dbc3864b6e6f185e3c2b1b190d037677240a7a542caaeeede9f29a7` |

Diff inspection: the slice branch differs from `origin/main` only by this
document. `go.mod`/`go.sum` are unchanged from `origin/main` (still the immutable
v0.5.0 pin), there is no `replace` directive, no `vendor/` tree and no moving
pin. No production tier, pool cap, global ceiling, host fault policy, commit
fence or schema was changed by this evidence slice.

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
