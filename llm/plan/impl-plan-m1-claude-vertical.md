---
title: IMPL_PLAN — M1 Claude vertical
status: DRAFT rev3 — revised after review round 2
proposal: llm/plan/proposal-11-harness-lifecycle-compiler.md
urd: llm/plan/urd-harness-lifecycle.md
authority: llm/research/hooks-ir-compilers-architecture-lessons.md
obsoletes: aura-plugins-sgxp6
baseline: bbc6cfd
review_effort_budget: revise until 0 BLOCKER / 0 IMPORTANT / 0 MINOR
worker_isolation: one worktree per slice; orchestrator merges
---

# IMPL_PLAN — M1: Claude vertical

**Ten enabled events. Nine slices.**

**M1 is mostly separation and renaming, not construction.** Three of the four
stages already exist under non-compiler names, and the IR exists in the deleted
source at `43dbbf1^`.

Repository: `worktree/proposal-57-integration/pasture`, branch
`feat/proposal-57-integration`, baseline `bbc6cfd`.

---

## 0. The class rule — read before anything else

Two review rounds found the same shape each time: a defect fixed *locally* and
reintroduced *elsewhere*. Round 1 — lowering had no consumer; rev2 added the
record stage; round 2 — `legalize`/`backend` had no consumer. Round 1 — a
direct-import check missed one indirection; rev2 wrote a transitive check whose
allowlist was a denylist. Round 1 — guards named no falsifying mutation; rev2
named four, two of which kill nothing.

URD R11 already states the remedy: **fix as a class, enforced by construction.**
Rev3 applies it as two standing rules, checked in §6 rather than per item.

**Rule 1 — every stage has a production caller at its own milestone.** A stage
built and guarded but never called is dead code, and dead lifecycle code is what
gets deleted as unreferenced two epochs later. If a stage has no M1 caller, that
is *declared* in §6, not discovered.

**Rule 2 — every gate declares four things.** What it detects; its scope
predicate; the mutation that must turn it red; and where that mutation is
*executed*. A gate missing any of the four is not a gate. §6 is the ledger.

---

## 1. Target package layout

```text
internal/lifecycle/
    dialect/             L1 + L2 IR AND their verified constructors.
    frontend/claude/     native payload -> L1        (extracted from ingress/claude)
    lowering/            L1 -> L2. Pure. Selects arm/axes; delegates construction.
    legalize/            L2 -> L3.
    backend/             L3 -> L4 effects.

    receipt/     (exists) THE RECORD STAGE — raw-body record + interpreted record
                          + consultation record
    projection/  (exists) EXTENDED with a binding filter
    activation/  (exists) enabled/withheld set
    guard/       (exists) EXTENDED with a driver + scope predicates
    model/       (exists) durable journal records; may import dialect, never the reverse
internal/acceptance/     (exists) EXTENDED with executor + classifier
internal/runtime/        (exists) RETAINED — EventSemantic + LifecycleEventMapping
```

**The record stage lives in the pre-existing `receipt` package.** "Record" is the
stage, `receipt` is the package; they are the same thing. Every other stage
shares one name with its package — this one does not, and saying so once is
cheaper than renaming a package the tree already uses.

**Not `internal/lifecycle/ir`** — `internal/codegen/ir` exists with 125 importing
files; the authority warns against conflation (§7:195-197).

---

## 2. Slices

Nine slices. Every path below is owned by **exactly one** slice, and every task
edits **only paths its slice owns** — both halves are checked in §5.

### SLICE-0 — Framework drivers (prerequisite)

**Owns:** `internal/lifecycle/guard/driver.go`,
`internal/acceptance/{executor,classifier}.go` (+ their tests)

Both frameworks are shells: `guard` has no tree-walker and **zero importers**;
`acceptance` has no `os/exec`, and `Case.Expect`/`Case.Delta` are declared,
cloned and shape-validated but **never compared against a real run anywhere**.

**Merges first, with SLICE-1.**

- `DR-1` the driver, with a **per-guard scope predicate** — two guards cannot
  share one scope. `CheckBoundedReaderSource` is scoped to lifecycle
  production-path *tests* (a tree-wide walk is red on correct production code at
  `receipt/journal.go:5`); `CheckTimeoutSource` is scoped to production files.

  ```go
  type ScopePredicate uint8   // closed enum
  const ( LifecycleTestFiles ScopePredicate = iota+1; LifecycleStageFiles; GeneratedArtifacts )
  type Check struct { ID CheckID; Scope ScopePredicate; Fn func(path string, src []byte) []string }
  func CheckTree(root string) ([]Finding, error)   // walks a DERIVED set per predicate
  ```
- `DR-2` register the two existing guards with their predicates stated
  explicitly. `CheckTimeoutSource` currently runs against four hardcoded paths
  (`guard/timeouts_test.go:19`).
- `DR-3` **executor split in two**, because `acceptance.Observation`
  (`report.go:20-25`) carries no exit/stdout/stderr field and cannot hold a raw
  run:
  - `RunCase(ctx, Case, binaryPath) (RawResult, error)` — exit code, stdout,
    stderr, and before/after `StoreSnapshot`
  - `Classify(Case, RawResult) (Observation, error)` — a **stated oracle table**.
    Under the exit-0 regime (§SLICE-7) exit code cannot discriminate
    `OracleSuccess` from `OracleValidation`, so classification derives from
    stderr class and `Delta`/`AssertExactRowChanges`, never from exit code.
- `DR-4` source-mutation runner: apply the mutation in a scratch worktree, run a
  **declared command**, set `Killed` from that command's non-zero exit and
  `Observed` from its captured output. `SourceMutationOperator.Guard` carries an
  exact test identifier, not prose. Closes `aura-plugins-0si2b` in substance.
- `DR-5` **must-pass verification** — compare `RawResult` against `Case.Expect`
  and `Case.Delta`. Without it a corpus can only say "a mutant died", never "the
  real command did the right thing".
- `DR-6` exhaustiveness: AST-walk package `guard` and fail if any exported
  `Check*` function is absent from the registry. Same fail-closed idiom as
  `guard/classification.go:22-32`. Prevents a slice adding an unregistered — and
  therefore zero-importer — guard.

**Note for the orchestrator:** `DR-4`'s scratch worktree must not collide with a
sibling slice's worktree. Confirm isolation before SLICE-0 starts.

### SLICE-1 — The dialect (L1, L2, and their constructors)

**Owns:** `internal/lifecycle/dialect/**`, `internal/lifecycle/guard/dialect_imports.go`,
**`internal/lifecycle/model/ids.go`**

**Port source:** `git show 43dbbf1^:internal/lifecycle/event.go` (657) and
`:internal/lifecycle/key.go` (86).

**Consumes** `runtime.EventSemantic` and `runtime.LifecycleEventMapping`
(authority §7:190-193 — retain).

**Exports:** IP-1 (L1), IP-2 (L2 + **the verified constructors** +
`SemanticFields()` + the class table).

- `DI-1` port L1. Event identity is a generated ordinal with pinned-contract
  lookup — no per-event Go constant, so `dialect` has no `PreToolUse` symbol.
- `DI-2` port L2. **Type-alias or re-export `runtime.EventSemantic`; declare no
  second arm enum.** Axes read from `LifecycleEventMapping`.
- `DI-3` **the verified constructors stay here.** `Semantics` and `Event` have
  entirely unexported fields plus a `constructed bool`
  (`43dbbf1^:event.go:259-264,433-437`), and `NewEvent` is a *method* on
  `EventBinding` (`:475`). No other package can construct them — that opacity is
  what makes "agrees with the pinned contract" a real check rather than a
  tautology (`:360-364`). Export `dialect.BindEvent` and
  `dialect.EventBinding.NewEvent` via IP-2.
- `DI-4` **drop the dedup surface here**, not downstream: `Digest`,
  `Origin.digest`, `Origin.PayloadDigest`, `Origin.ReplayKey` (`key.go:77`). The
  pre-parse digest stays on the recording side (`claude.Capture.Digest`).
- `DI-5` keep `Semantics.CanonicalKey` (`key.go:50`) and `EquivalentTo`
  (`event.go:301`). Add `SemanticFields() SemanticProjection`, the **only**
  surface differential equivalence compares.
- `DI-6` **totality guard over L2 fields**: every exported field of the L2 type
  is classified exactly once as semantic or explicitly non-semantic, fail-closed.
  `SemanticProjection` is a distinct type structurally incapable of holding a
  `provenance.JournalID`, a timestamp, or a harness ID. Without this an empty
  projection passes every M1 gate and is maximally back-fittable at M2 — which is
  the exact thing the M1 obligation exists to prevent.
- `DI-7` equivalence-class table over the **full thirty-event generated
  descriptor set**, not the M1-enabled subset — the enabled set is declared by
  `EV-5` in SLICE-6, which merges later, so a guard over it has no domain at
  SLICE-1.
- `DI-8` typed unresolved fact with a **closed reason enum**
  (`ReasonNoToolCallBinding`, …) on L1 and L2.
- `DI-9` boundary guard: `dialect` may not import `provenance`, `model`,
  `receipt`, `ingress`, or `database/sql`.
- `DI-10` delete the **twelve** unreferenced aliases at `model/ids.go:13-21,24-26`.
  **Do not touch `:22-23`** — `DefinitionJournalID` and `DefinitionStateID` are
  live at `model/definition.go:30,53,54,71`. Deleting the range as stated in rev2
  would have broken the build.

### SLICE-2 — Claude frontend

**Owns:** `internal/lifecycle/frontend/claude/**`,
`internal/lifecycle/ingress/claude/capture.go`,
**`internal/lifecycle/receipt/service.go`**

**Scope: ten events** — 1, 3, 8, 11, 12, 13, 25, 26, 29, 30.

`ingress/claude.Parse` (`capture.go:27-49`) is already a frontend. Extract and
rename; do not rewrite.

- `FE-1` payload → L1 for **all ten**, driven by the generated descriptor
- `FE-2` malformed or unclassifiable payloads produce a typed disposition **and a
  durable unresolved fact** — never a panic, never a guess
- `FE-3` **`receipt.Delivery` becomes a projection of L1 via exactly one
  function.** Not "state which" — this is the decision, made here, because the
  alternative silently required editing another slice's package. Observable
  recording behaviour is unchanged; the pre-parse digest stays.
- `FE-4` bindings: `BindingSession` on **all ten** (contract-required);
  `BindingToolCall` on 8/11/12; `BindingRequest` on 29/30. `PostToolBatch` binds
  session and emits a **tool-call-unresolved** fact — not a blanket unresolved.
- `FE-5` the pinned descriptor selects **interpretation only, never admission**.
  An out-of-range host version is recorded verbatim (R12/V6).
- `FE-6` golden cases per authority §11: captured payload → expected L1 →
  expected L2, per enabled event plus malformed and unresolved cases.

### SLICE-3 — Lowering pass (the middle-end)

**Owns:** `internal/lifecycle/lowering/**`,
`internal/lifecycle/guard/lowering_imports.go`

L1 → L2. **Port source is the arm/axis *selection*, not the constructors.**
`lowering.Lower` reads `LifecycleEventMapping` and delegates construction to
`dialect.EventBinding.NewEvent` (IP-2). The constructors cannot live here — their
fields are unexported and `NewEvent` is a method on a `dialect` type.

`43dbbf1^:lower.go` is **not** this slice's source: it consumes
`event.Semantics()` (`:237`), branches on blocking (`:241`) and writes via
`RecordObservation` (`:288`). It is legalization + backend — SLICE-4.

**Hard constraint:** `func Lower(dialect.L1) (dialect.L2, error)` — no
`context.Context`, no interface parameters, no receiver carrying dependencies.

- `LO-1` the pass; arms and axes from `LifecycleEventMapping`; no per-event table
- `LO-2` unit tests constructing L1 directly, opening no database
- `LO-3` guard: `lowering`'s transitive import closure **equals** this closed set —
  `dialect`, `internal/runtime`, `internal/codegen/ir`, `internal/effects`,
  `internal/errors`, and the I/O-incapable stdlib set `{errors, fmt, strings,
  strconv, sort, slices, maps, time}`. Rev2 wrote "stdlib minus `database/sql`",
  which is a denylist admitting `os`, `os/exec`, `net`, `syscall` — the same
  defect class it was written to fix. The listed non-stdlib packages are the
  real closure, since `DI-2` aliases `runtime.EventSemantic`.
- `LO-4` two distinct Claude events sharing an arm produce equal `EquivalentTo`
  shape and distinct `CanonicalKey`. Use `PreToolUse`/`PreCompact` — not the
  at-risk elicitation capture.

### SLICE-4 — Legalization and backend

**Owns:** `internal/lifecycle/legalize/**`, `internal/lifecycle/backend/**`

**Port source:** `43dbbf1^:lower.go` — blocking dispatch, semantic dispatch,
actor resolution, single-transaction write.

**This stage has a real M1 caller** (Rule 1). The gate-consultation arm produces
an L3 value which `backend` maps to one L4 effect: the **consultation record**.
That is 5 of 10 events, and it is what makes URD **V8** implementable —
*"evidence of consultation is recorded and the host is answered proceed"*. Rev2
declared the consultation record and assigned it to nobody, which left V8
unimplementable and `backend` uncalled.

- `LG-1` legalization L2 → L3. Observation and human-response arms produce **no**
  L3; the gate arm produces one.
- `LG-2` backend L3 → L4: the consultation effect, written through `receipt`
  (SLICE-5 owns the record type; SLICE-4 owns the rule that selects it).
- `LG-3` **static** no-**authority** proof: neither package performs an
  authority-bearing write. Restated from rev2's "no write call", which was
  incompatible with LG-2. Static, so it holds whether or not the elicitation pair
  is enabled.
- `LG-4` **INVERT the ported refusal.** `lower.go:242` returns
  `awaitedReplyError` (`:388`) for every blocking event; M1 enables five.
  Blocking events **record consultation and answer proceed; they never refuse.**
  `awaitedReplyError` must not survive the port.
- `LG-5` the consultation obligation keys on `Blocking != NonBlocking`, **not on
  the arm** — `PostToolBatch` is blocking, and an arm-keyed rule lets it escape.
- `LG-6` `MutationInput` modelled in the dialect, emitted by no backend rule
- `LG-7` human-response arm returns a typed `NoAuthority`; a guard asserts no
  backend rule maps it
- `LG-8` the L3 type has no field capable of holding authority

### SLICE-5 — Record stage and readers

**Owns:** `internal/lifecycle/receipt/{interpreted,consultation}.go`,
`internal/lifecycle/model/reader.go`, `internal/lifecycle/projection/reader.go`

- `RC-1` interpreted record: L2 arm, bindings, unresolved facts, plus the pinned
  contract ID and codebook version (R7, minimal form)
- `RC-2` **consultation record** (SLICE-4's L4 effect): L2 arm, the answered
  `HostResponse`, contract and codebook identity. Keyed on blocking mode per LG-5.
- `RC-3` **row identity**: the interpreted and consultation records are
  disambiguated **solely** by autoincrementing row ID. No payload-derived key
  participates. Blob writes use `ON CONFLICT(digest) DO NOTHING`
  (`journal.go:53`) — correct for content-addressed bodies and **wrong** for
  these records; do not mirror it. This is V1's mechanism.
- `RC-4` extend `OccurrenceQuery` with a typed binding filter; thread it through
  `projection.Reader` **including `queryFingerprint`** (`projection/reader.go:106`),
  or cursors silently mismatch
- `RC-5` negative test: a cursor from a binding-filtered query is rejected by an
  unfiltered one
- `RC-6` public bounded payload-by-digest reader — V2 has no public path today

### SLICE-6 — Activation and evidence binding

**Owns:** `internal/lifecycle/activation/**`

- `EV-1` evidence carries the whole `acceptance.CaptureProvenance`
- `EV-2` the enabling gate requires `Origin == OriginAuthenticCapture` **and** a
  passing `ValidateFixture` **and** an in-range `HarnessVersion`. A digest proves
  non-tampering, not provenance; `ValidateFixture` returns `nil` early for
  non-authentic origins (`capture.go:45-47`) and `Origin` is caller-asserted.
- `EV-3` bind `ProductionProof` to a referenced passing case ID and its recorded
  outcome — the identical bare-constant defect two lines below `FixtureEvidence`
  (`activation/types.go:33-38,55-57`)
- `EV-4` replace the hardcoded `if event.NativeName == "SessionStart"`
  (`claude_2_1_210.go:11`) with the declared ten-ordinal enabled set; assert
  twenty withheld with typed reasons
- `EV-5` the arms-exercised claim is **derived from the activation set**, not
  asserted, so a withheld event cannot be counted as exercised

### SLICE-7 — Exit-code safety

**Owns:** `cmd/pasture/hook_lifecycle.go`, `internal/lifecycle/guard/exit_codes.go`

Sole owner of the lifecycle command. Five of ten events are blocking; the host
reads exit 2 as deny. The command **exits non-zero today** (`:46`) and **panics**
(`:58`).

**Merges before any blocking event is enabled** — gates SLICE-6 and SLICE-8.

- `XC-1` syntactic ban — no non-zero `os.Exit`, no `log.Fatal*`, no `panic(`.
  **Scope is derived** over `internal/lifecycle/{dialect,frontend,lowering,legalize,backend}`
  plus the `RunE` body of `hookLifecycleCmd`. It does **not** cover all of
  `cmd/pasture` (`package main`, shared with every command, where `exitWithCode`
  is legitimate) and does **not** cover package-init `must` helpers
  (`timeouts/profile.go:73-77`, `acceptance/mutation.go:387-392`), which are
  reachable but correct.
- `XC-2` top-level `recover()` converting any panic to exit 0 + stderr; replace
  the `panic` at `:58` with a returned error
- `XC-3` fault classes over the **closed** `errors.Category` enum plus panic;
  totality guard fails when a category is added without a row
- `XC-4` integration cases executing the **built binary**: injected panic,
  injected nil dereference, **and an injected storage failure** (unwritable
  `--db`), each asserting exit 0 and non-empty stderr. The storage case is the
  guard for `XC-1`'s falsifying mutation — rev2 named that mutation with nothing
  able to kill it.
- `XC-5` typed `HostResponse` with a closed decision enum, exactly one legal
  value (`Proceed`) at M1, and a guard asserting one arm. Deny later becomes
  *adding an arm*, not *removing a guard* (R9's retained responder).

### SLICE-8 — Wiring, production proof, and generated-artifact conformance

**Owns:** `internal/handlers/hook_lifecycle.go`,
`internal/acceptance/testdata/corpora/lifecycle_m1.yaml`,
`internal/lifecycle/guard/generated_artifacts.go`, `docs/privacy.md`

**Consumes** IP-1..IP-5. **Merges last.**

- `EE-1` wire frontend → lowering → record → legalize → backend in the handler;
  no stage bypassed
- `EE-2` **per-event traversal proof.** For each of the ten events: `make build`,
  exec `bin/pasture` with argv, then through the **public readers** (`RC-4`
  binding filter, `RC-6` payload-by-digest) assert (a) the raw record, (b) the
  interpreted record carrying the expected L2 arm from `LifecycleEventMapping`,
  (c) the §2.1 terminal for that arm. Exit code and stderr remain assertions but
  **must not be the only ones** — under `XC-1`/`XC-2` exit 0 is a constant, so a
  handler returning `nil` would satisfy an exit-code-only test identically.
- `EE-3` deliver byte-identical input twice; assert two distinct occurrence IDs
  **and** two distinct interpreted-record IDs through the public reader. This is
  **V1**, which had no owner in rev1 or rev2.
- `EE-4` a malformed invocation creates no database file
- `EE-5` **V9 conformance guard**: walk the artifact set emitted by
  `make generate` and assert no artifact names a protocol operation or parses
  native JSON, against a declared per-harness table
  (`claude: enforced, opencode: known-failing, codex: known-failing`) with a
  totality guard over `acceptance.HarnessKind` (`schema.go:63-68`). Fails when
  actual disagrees with declared, so M2/M3 cannot forget to flip a row.
- `EE-6` privacy posture in `docs/privacy.md`. **Gates enabling `PreToolUse`**,
  which carries `FieldToolInput` — full file contents for `Write`/`Edit`.
- `EE-7` `TestEngineStartReviewUsesAttachedProvenanceAdapter` stays green (R14/V11)

---

## 3. Layer Integration Points

| ID | Contract | Owner | Consumers | Merge timing |
|---|---|---|---|---|
| IP-0 | guard driver + executor/classifier | SLICE-0 | all | **first** |
| IP-1 | L1 types | SLICE-1 | SLICE-2, 3 | before their implementation |
| IP-2 | L2 + **constructors** + `SemanticFields()` + class table | SLICE-1 | SLICE-2, 3, 4, 5 | same |
| IP-3 | `Lower` signature | SLICE-3 | SLICE-8 | before wiring |
| IP-4 | exit-code guard + `HostResponse` | SLICE-7 | SLICE-6, 8 | **before any blocking event is enabled** |
| IP-5 | interpreted + consultation records, binding-filtered reader | SLICE-5 | SLICE-4, 8 | before wiring |

```text
  SLICE-0 drivers ──┐
  SLICE-1 dialect ──┤  (merge together, first)
                    |
     +---------+----+----+---------+
     |         |         |         |
  SLICE-2  SLICE-3   SLICE-7   (parallel)
  frontend lowering  exit-code
     |         |         |
     +----+----+         |
          |              |
      SLICE-5 record ────┤
          |              |
      SLICE-4 legalize/backend
          |              |
          +------+-------+
                 |
            SLICE-6 activation  (gated by IP-4)
                 |
            SLICE-8 wiring + proof  (merges last)
```

---

## 4. Execution rules

One worktree per slice. **Merge conflicts are the orchestrator's job**;
ambiguous design choices go to the user. **Generated files are never
hand-merged** — merge the source, re-run `make generate`, verify a zero-diff regen.

**Guard scope is derived** — by walk under a declared `ScopePredicate`, or by
exhaustive enumeration over a closed typed set. A hand-maintained path or symbol
list is not acceptable scope for a new guard.

Gates before every commit: `make fmt`, `make lint`, `make build`,
`go test -race ./...`, zero-diff `make generate`. `go` is not on PATH by default.
Stage only owned paths, never `git add -A`, commit with `git agent-commit`.

---

## 5. Ownership check

Both halves, per §0 Rule 2 and the round-2 finding that rev2 checked only the first:

1. **No path is claimed by two slices.** Verified pairwise across all nine.
2. **Every task edits only paths its slice owns.** The two rev2 violations are
   fixed: `model/ids.go` → SLICE-1 (for `DI-10`); `receipt/service.go` → SLICE-2
   (for `FE-3`). SLICE-8's end-to-end corpus has an explicit path.

---

## 6. Mechanism ledger

Rule 2: every gate declares what it detects, its scope, its falsifying mutation,
and where that mutation executes. A gate missing a row here is not a gate.

| Gate | Detects | Scope | Falsifying mutation | Executed by |
|---|---|---|---|---|
| `DI-6` | unclassified L2 field | L2 type, closed set | add a field to L2 without classifying it | `DR-4` |
| `DI-9` | `dialect` importing storage/provenance | `LifecycleStageFiles` | add `import ".../receipt"` to a `dialect` file | `DR-4` |
| `LO-3` | `lowering` gaining any I/O capability | transitive closure, set equality | add `import "os"` + an `os.ReadFile` call | `DR-4` |
| `EV-2` | a synthesised fixture backing an enabled event | activation gate | relabel an authentic fixture's origin to `authored` | `DR-4` |
| `XC-1` | a non-zero exit path in a lifecycle stage | `LifecycleStageFiles` + `RunE` body | change a `return nil` on the storage-failure path to `return err` | **`XC-4`'s storage-failure case** |
| `DR-6` | an unregistered guard | package `guard`, AST walk | add an exported `Check*` without registering it | `DR-4` |
| `EE-2` | a bypassed stage | ten-event corpus | delete the record-stage call from `EE-1`'s wiring | `DR-4` |
| `EE-5` | a generated artifact regaining operation selection | `GeneratedArtifacts` | reintroduce `renderPythonLifecycleAdapter` into `claudeHooksEmitter.Emit` | `DR-4` |

**Rule 1 check — every stage has an M1 caller:**

| Stage | M1 caller |
|---|---|
| frontend | `EE-1` wiring |
| lowering | `EE-1` wiring |
| record | `EE-1` wiring; consultation effect from `backend` |
| legalize | `EE-1` wiring — gate arm, 5 of 10 events |
| backend | `LG-2` consultation effect — 5 of 10 events |

No stage is built without a caller at M1.

---

## 7. Definition of done

- [ ] All ten events traverse the pipeline through the built binary, proven
      per-event through public readers (`EE-2`) — not by exit code alone
- [ ] Two byte-identical deliveries yield two distinct occurrence IDs and two
      distinct interpreted-record IDs (`EE-3`, V1)
- [ ] The lowering pass is unit-tested with **no database**; its transitive
      closure equals the declared closed set
- [ ] Each enabled event: authentic origin + passing validation + in-range version
- [ ] `ProductionProof` bound to a referenced passing case
- [ ] Twenty events withheld with typed reasons
- [ ] Gate-consultation events produce a durable consultation record (V8)
- [ ] Human-response arm returns typed `NoAuthority`; arms-exercised derived from
      the activation set
- [ ] `toolUseID` and `requestID` correlated pairs retrievable through a public reader
- [ ] `PostToolBatch` records session correlation plus a tool-call-unresolved fact
- [ ] Every durable record carries contract ID and codebook version
- [ ] No non-zero exit path in a lifecycle stage; injected panic, nil deref and
      storage failure each exit 0
- [ ] No second arm enum; no `ReplayKey`/`RecordReplayed`/`Origin.PayloadDigest`
- [ ] `CanonicalKey`, `EquivalentTo` and `SemanticFields()` exist; the class table
      is total over all thirty descriptor events
- [ ] `docs/privacy.md` published before `PreToolUse` is enabled
- [ ] Every row of §6's ledger has its mutation executed and red
- [ ] All gates pass; `make generate` is a zero diff

**Capture risk.** `Elicitation`/`ElicitationResult` need an MCP server that
elicits. If uncapturable they stay **visibly withheld**, and **must not** be
closed with a synthesised fixture. Degraded form, stated so it is not silently
ticked: the human-response arm is then exercised at the `Lower` unit level over a
synthetic `dialect.L1` — an IR value, not a capture — and the arms-exercised DoD
item is recorded **partially met**, linked to the withheld reason. `LG-3`/`LG-7`
are static and hold regardless.

**Not in M1:** differential-equivalence execution (M2; its surface is built here),
the normative write gate, definition resolution, lineage, context disclosure, and
the raw-ingestion escape hatch (M4). **R10** — the bounded-wait deadline, user
decision *"Do A."* — is **deferred to M2** and named here so it is not lost a
third time.
